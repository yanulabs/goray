package index

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Status describes the indexer's current state.
type Status struct {
	RootDir    string `json:"root_dir"`
	Indexing   bool   `json:"indexing"`
	Ready      bool   `json:"ready"`
	LastError  string `json:"last_err,omitempty"`
	ModulePath string `json:"module_path,omitempty"`
	Packages   int    `json:"packages,omitempty"`
	Modules    int    `json:"modules,omitempty"`
	Symbols    int    `json:"symbols,omitempty"`
	XRefs      int    `json:"xrefs,omitempty"`
	LoadErrors int    `json:"load_errors,omitempty"`
	BuildMS    int64  `json:"build_ms,omitempty"`
}

// Store owns the current immutable index snapshot.
type Store struct {
	root string
	log  *slog.Logger

	mu       sync.RWMutex
	idx      *Index
	err      error
	indexing bool

	ready     chan struct{}
	readyOnce sync.Once
	buildMu   sync.Mutex
}

func NewStore(root string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Store{root: root, log: logger, ready: make(chan struct{})}
}

// Start builds the index in the background.
func (s *Store) Start() {
	go func() { _ = s.Rebuild() }()
}

// Root is the project directory being indexed.
func (s *Store) Root() string { return s.root }

// Rebuild replaces the snapshot. Builds are serialized.
func (s *Store) Rebuild() error {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()

	s.mu.Lock()
	s.indexing = true
	s.mu.Unlock()

	start := time.Now()
	s.log.Info("indexing started", "root", s.root)
	idx, err := Build(s.root)
	duration := time.Since(start)

	s.mu.Lock()
	s.indexing = false
	if err != nil {
		s.err = err
	} else {
		s.idx = idx
		s.err = nil
	}
	s.mu.Unlock()

	if err != nil {
		s.log.Error("indexing failed", "err", err, "duration", duration)
	} else {
		s.log.Info("indexing ready", "packages", len(idx.Packages), "symbols", len(idx.Symbols), "duration", duration)
	}
	s.readyOnce.Do(func() { close(s.ready) })
	return err
}

// Wait blocks until the first build finishes or ctx is done.
func (s *Store) Wait(ctx context.Context) (*Index, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.idx != nil {
		return s.idx, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, errors.New("index is not ready")
}

func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := Status{
		RootDir:  s.root,
		Indexing: s.indexing,
		Ready:    s.idx != nil,
	}
	if s.err != nil {
		status.LastError = s.err.Error()
	}
	if s.idx != nil {
		status.ModulePath = s.idx.ModulePath
		status.Packages = len(s.idx.Packages)
		status.Modules = len(s.idx.Modules)
		status.Symbols = len(s.idx.Symbols)
		status.XRefs = s.idx.TotalXRefs()
		status.LoadErrors = len(s.idx.LoadErrors)
		status.BuildMS = s.idx.BuildDuration.Milliseconds()
	}
	return status
}
