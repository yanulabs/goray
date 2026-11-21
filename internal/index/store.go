package index

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// State is the explicit lifecycle state of the index snapshot.
type State string

const (
	StateInitializing State = "initializing"
	StateReady        State = "ready"
	StateDirty        State = "dirty"
	StateRebuilding   State = "rebuilding"
	StateError        State = "error"

	debounceDelay = 400 * time.Millisecond
	freshnessWait = 2 * time.Second
)

// Status describes the indexer's current state.
type Status struct {
	RootDir              string   `json:"root_dir"`
	Indexing             bool     `json:"indexing"`
	Ready                bool     `json:"ready"`
	IndexState           State    `json:"index_state"`
	LastError            string   `json:"last_err,omitempty"`
	ModulePath           string   `json:"module_path,omitempty"`
	Packages             int      `json:"packages,omitempty"`
	Modules              int      `json:"modules,omitempty"`
	Symbols              int      `json:"symbols,omitempty"`
	XRefs                int      `json:"xrefs,omitempty"`
	LoadErrors           int      `json:"load_errors,omitempty"`
	LoadErrorDetails     []string `json:"load_error_details,omitempty"`
	TestLoadErrors       int      `json:"test_load_errors,omitempty"`
	TestLoadErrorDetails []string `json:"test_load_error_details,omitempty"`
	BuildMS              int64    `json:"build_ms,omitempty"`
}

// Store owns the current immutable index snapshot.
type Store struct {
	root string
	log  *slog.Logger

	mu         sync.RWMutex
	idx        *Index
	err        error
	loadErrors []string
	indexing   bool
	state      State
	changeSeq  uint64
	stateCh    chan struct{}

	ready     chan struct{}
	readyOnce sync.Once
	buildMu   sync.Mutex
	buildWG   sync.WaitGroup
	autoWG    sync.WaitGroup
	dirtyCh   chan struct{}
	hook      func(*Index) error
}

func NewStore(root string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Store{
		root: root, log: logger, ready: make(chan struct{}),
		state: StateInitializing, stateCh: make(chan struct{}),
		dirtyCh: make(chan struct{}, 1),
	}
}

// SetBuildHook runs before a new snapshot becomes visible.
func (s *Store) SetBuildHook(hook func(*Index) error) {
	s.mu.Lock()
	s.hook = hook
	s.mu.Unlock()
}

// Start builds the index in the background.
func (s *Store) Start() {
	s.buildWG.Add(1)
	go func() {
		defer s.buildWG.Done()
		_ = s.Rebuild()
	}()
}

// Root is the project directory being indexed.
func (s *Store) Root() string { return s.root }

// State returns the current lifecycle state.
func (s *Store) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// MarkDirty signals that source files changed.
func (s *Store) MarkDirty() {
	s.mu.Lock()
	s.changeSeq++
	s.setStateLocked(StateDirty)
	s.mu.Unlock()
	select {
	case s.dirtyCh <- struct{}{}:
	default:
	}
}

// StartAutoRebuild runs the debounced rebuild loop in a tracked goroutine.
func (s *Store) StartAutoRebuild(ctx context.Context) {
	s.autoWG.Add(1)
	go func() {
		defer s.autoWG.Done()
		s.AutoRebuild(ctx)
	}()
}

// WaitForAuto blocks until the auto-rebuild loop exits.
func (s *Store) WaitForAuto() {
	s.autoWG.Wait()
}

// AutoRebuild coalesces dirty events into a single trailing rebuild.
func (s *Store) AutoRebuild(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.dirtyCh:
			if timer == nil {
				timer = time.NewTimer(debounceDelay)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounceDelay)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			if err := s.Rebuild(); err != nil {
				s.log.Error("auto reindex failed", "err", err)
			}
		}
	}
}

// WaitForBuild blocks until any active rebuild completes.
func (s *Store) WaitForBuild() {
	s.buildWG.Wait()
}

// Rebuild replaces the snapshot. Builds are serialized.
func (s *Store) Rebuild() error {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	s.buildWG.Add(1)
	defer s.buildWG.Done()

	s.mu.Lock()
	s.indexing = true
	startSeq := s.changeSeq
	s.setStateLocked(StateRebuilding)
	hook := s.hook
	s.mu.Unlock()

	start := time.Now()
	s.log.Info("indexing started", "root", s.root)
	idx, err := Build(s.root)
	if err == nil && hook != nil {
		err = hook(idx)
	}
	duration := time.Since(start)

	s.mu.Lock()
	s.indexing = false
	if err != nil {
		s.err = err
		s.loadErrors = loadErrorMessages(err)
		s.setStateLocked(StateError)
	} else {
		s.idx = idx
		s.err = nil
		s.loadErrors = nil
		if s.changeSeq != startSeq {
			s.setStateLocked(StateDirty)
		} else {
			s.setStateLocked(StateReady)
		}
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

// Wait returns the latest snapshot, waiting briefly for an in-flight rebuild.
func (s *Store) Wait(ctx context.Context) (*Index, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := s.waitSettled(ctx); err != nil {
		return nil, err
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

func (s *Store) waitSettled(ctx context.Context) error {
	deadline := time.NewTimer(freshnessWait)
	defer deadline.Stop()
	for {
		s.mu.RLock()
		state, ch := s.state, s.stateCh
		s.mu.RUnlock()
		if state != StateDirty && state != StateRebuilding {
			return nil
		}
		select {
		case <-ch:
		case <-deadline.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func loadErrorMessages(err error) []string {
	var loadErr *LoadError
	if errors.As(err, &loadErr) {
		return append([]string{}, loadErr.Errors...)
	}
	return []string{err.Error()}
}

func (s *Store) setStateLocked(state State) {
	if s.state == state {
		return
	}
	s.state = state
	close(s.stateCh)
	s.stateCh = make(chan struct{})
}

func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := Status{
		RootDir:    s.root,
		Indexing:   s.indexing,
		Ready:      s.idx != nil,
		IndexState: s.state,
	}
	if s.err != nil {
		status.LastError = s.err.Error()
	}
	if len(s.loadErrors) > 0 {
		status.LoadErrors = len(s.loadErrors)
		status.LoadErrorDetails = append([]string{}, s.loadErrors...)
	} else if s.idx != nil {
		status.LoadErrors = len(s.idx.LoadErrors)
	}
	if s.idx != nil {
		status.TestLoadErrors = len(s.idx.TestLoadErrors)
		status.TestLoadErrorDetails = append([]string{}, s.idx.TestLoadErrors...)
		status.ModulePath = s.idx.ModulePath
		status.Packages = len(s.idx.Packages)
		status.Modules = len(s.idx.Modules)
		status.Symbols = len(s.idx.Symbols)
		status.XRefs = s.idx.TotalXRefs()
		status.BuildMS = s.idx.BuildDuration.Milliseconds()
	}
	return status
}
