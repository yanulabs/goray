package index

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// State is the explicit lifecycle state of the index snapshot.
type State string

// Store lifecycle states.
const (
	StateInitializing State = "initializing"
	StateReady        State = "ready"
	StateDirty        State = "dirty"
	StateRebuilding   State = "rebuilding"
	StateError        State = "error"

	debounceDelay = 400 * time.Millisecond
	freshnessWait = 2 * time.Second
)

// indexWaitGrace bounds how long a tool call waits for the initial index. It
// is a variable so tests can exercise the cold-start path without waiting 8s.
var indexWaitGrace = 8 * time.Second

// IndexingError signals that the initial build did not finish within the
// bounded grace period. Tools translate it into a structured indexing payload.
type IndexingError struct {
	Elapsed time.Duration
	State   State
}

func (e *IndexingError) Error() string {
	return fmt.Sprintf("index is still building after %s (state: %s)", e.Elapsed.Round(time.Millisecond), e.State)
}

// ElapsedSeconds is the JSON-friendly elapsed time reported to MCP clients.
func (e *IndexingError) ElapsedSeconds() float64 { return e.Elapsed.Seconds() }

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
	BuildTags            []string `json:"build_tags,omitempty"`
	Workspace            bool     `json:"workspace,omitempty"`
	ModulePaths          []string `json:"module_paths,omitempty"`
	LastBuildMode        string   `json:"last_build_mode,omitempty"`
	AffectedPackages     int      `json:"affected_packages,omitempty"`
}

// Store owns the current immutable index snapshot.
type Store struct {
	root string
	log  *slog.Logger
	opts BuildOptions

	mu          sync.RWMutex
	idx         *Index
	err         error
	loadErrors  []string
	diagnostics []Diagnostic
	indexing    bool
	state       State
	changeSeq   uint64
	stateCh     chan struct{}

	ready     chan struct{}
	readyOnce sync.Once
	buildMu   sync.Mutex
	buildWG   sync.WaitGroup
	autoWG    sync.WaitGroup
	dirtyCh   chan struct{}
	hook      func(*Index) error

	lastMode       string
	lastAffected   int
	pendingChanges []string
	forceFull      bool
}

// NewStore creates a store using the default build options.
func NewStore(root string, logger *slog.Logger) *Store {
	return NewStoreWithOptions(root, logger, BuildOptions{})
}

// NewStoreWithOptions creates a store that loads with the given build tags and
// workspace mode.
func NewStoreWithOptions(root string, logger *slog.Logger, opts BuildOptions) *Store {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	opts.Tags = NormalizeTags(opts.Tags)
	return &Store{
		root: root, log: logger, opts: opts, ready: make(chan struct{}),
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
	s.StartContext(context.Background())
}

// StartContext builds the index in the background with cancellation.
func (s *Store) StartContext(ctx context.Context) {
	s.buildWG.Add(1)
	go func() {
		defer s.buildWG.Done()
		_ = s.RebuildContext(ctx)
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

// MarkDirty signals that source files changed. Passing paths enables
// package-level incremental rebuilds; calling with no paths forces a full
// rebuild, even when path-scoped events were queued earlier in the debounce
// window.
func (s *Store) MarkDirty(paths ...string) {
	s.mu.Lock()
	s.changeSeq++
	if len(paths) == 0 {
		s.forceFull = true
	} else {
		s.pendingChanges = appendUniquePaths(s.pendingChanges, paths...)
	}
	s.setStateLocked(StateDirty)
	s.mu.Unlock()
	select {
	case s.dirtyCh <- struct{}{}:
	default:
	}
}

func appendUniquePaths(existing []string, paths ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(paths))
	out := make([]string, 0, len(existing)+len(paths))
	for _, path := range existing {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
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
			if err := s.rebuild(ctx, true); err != nil {
				s.log.Error("auto reindex failed", "err", err)
			}
		}
	}
}

// WaitForBuild blocks until any active rebuild completes.
func (s *Store) WaitForBuild() {
	s.buildWG.Wait()
}

// Rebuild performs a clean full rebuild. Builds are serialized.
func (s *Store) Rebuild() error {
	return s.RebuildContext(context.Background())
}

// RebuildContext is Rebuild with cancellation.
func (s *Store) RebuildContext(ctx context.Context) error {
	return s.rebuild(ctx, false)
}

func (s *Store) rebuild(ctx context.Context, incremental bool) error {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	s.buildWG.Add(1)
	defer s.buildWG.Done()

	s.mu.Lock()
	s.indexing = true
	startSeq := s.changeSeq
	s.setStateLocked(StateRebuilding)
	hook := s.hook
	prev := s.idx
	changed := append([]string{}, s.pendingChanges...)
	forceFull := s.forceFull
	s.pendingChanges = nil
	s.forceFull = false
	s.mu.Unlock()

	start := time.Now()
	mode := "full"
	modeLabel := "full"
	if incremental {
		modeLabel = "incremental"
	}
	s.log.Info("indexing started", "root", s.root, "tags", s.opts.Tags, "workspace", s.opts.Workspace, "mode", modeLabel)
	var idx *Index
	var err error
	affected := 0
	if incremental && !forceFull && prev != nil && len(changed) > 0 {
		var usedIncremental bool
		idx, usedIncremental, affected, err = BuildIncrementalContext(ctx, s.root, s.opts, prev, changed)
		if usedIncremental {
			mode = "incremental"
		}
	} else {
		idx, err = BuildWithOptionsContext(ctx, s.root, s.opts)
		if err == nil {
			affected = len(idx.Packages)
		}
	}
	if err == nil && hook != nil {
		err = hook(idx)
	}
	if s.opts.ReleaseArtifacts {
		// Dropping the package references only makes them collectable. Return
		// the unused build heap to the OS now so an idle server does not keep the
		// large AST/type working set resident until unrelated allocations happen.
		debug.FreeOSMemory()
	}
	duration := time.Since(start)

	s.mu.Lock()
	s.indexing = false
	if err != nil {
		s.err = err
		s.loadErrors = loadErrorMessages(err)
		s.diagnostics = diagnosticsFromError(err)
		// A failed build must not lose files that were already dirty. Otherwise
		// a transient error in one package permanently drops valid edits from
		// other packages once the broken package is fixed.
		s.pendingChanges = appendUniquePaths(changed, s.pendingChanges...)
		if forceFull {
			s.forceFull = true
		}
		s.setStateLocked(StateError)
	} else {
		s.idx = idx
		s.err = nil
		s.loadErrors = nil
		s.diagnostics = nil
		s.lastMode = mode
		s.lastAffected = affected
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
		s.log.Info("indexing ready", "mode", mode, "affected_packages", affected, "packages", len(idx.Packages), "symbols", len(idx.Symbols), "duration", duration)
	}
	s.readyOnce.Do(func() { close(s.ready) })
	return err
}

// Wait returns the latest snapshot, waiting up to indexWaitGrace for the
// initial build and briefly for in-flight rebuilds. Only a cold start that
// exceeds the grace period returns *IndexingError; a long rebuild serves the
// last healthy snapshot instead of discarding it.
func (s *Store) Wait(ctx context.Context) (*Index, error) {
	start := time.Now()
	timer := time.NewTimer(indexWaitGrace)
	defer timer.Stop()
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		// The build may have completed while the timer fired; prefer readiness.
		select {
		case <-s.ready:
		default:
			return nil, &IndexingError{Elapsed: time.Since(start), State: s.State()}
		}
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

// Diagnostics returns structured load errors without discarding the last
// healthy snapshot. After a failed rebuild the production diagnostics describe
// the current tree; after a successful build only test-only diagnostics remain
// (they are captured on the Index).
func (s *Store) Diagnostics() []Diagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.diagnostics) > 0 {
		return append([]Diagnostic{}, s.diagnostics...)
	}
	if s.idx != nil {
		return append([]Diagnostic{}, s.idx.Diagnostics...)
	}
	return nil
}

func (s *Store) setStateLocked(state State) {
	if s.state == state {
		return
	}
	s.state = state
	close(s.stateCh)
	s.stateCh = make(chan struct{})
}

// Status returns a consistent snapshot of the store lifecycle and metadata.
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
		status.ModulePaths = append([]string{}, s.idx.ModulePaths...)
		status.BuildTags = append([]string{}, s.idx.BuildTags...)
		status.Workspace = s.idx.Workspace
		status.Packages = len(s.idx.Packages)
		status.Modules = len(s.idx.Modules)
		status.Symbols = len(s.idx.Symbols)
		status.XRefs = s.idx.TotalXRefs()
		status.BuildMS = s.idx.BuildDuration.Milliseconds()
	}
	status.LastBuildMode = s.lastMode
	status.AffectedPackages = s.lastAffected
	return status
}
