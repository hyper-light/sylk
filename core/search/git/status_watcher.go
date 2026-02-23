package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// =============================================================================
// Constants
// =============================================================================

// statusDebounce is the delay after a trigger before running git status.
// Coalesces rapid filesystem events (e.g. git checkout touching many refs).
const statusDebounce = 150 * time.Millisecond

// statusMaxDebounce is the maximum time from the first trigger to a forced
// refresh. Prevents starvation when continuous events keep resetting the
// debounce timer (e.g. rebase touching hundreds of refs).
const statusMaxDebounce = 1 * time.Second

// statusFallbackBase is the starting fallback interval for catching external
// working-tree changes not detected by .git/ watching or Nudge.
const statusFallbackBase = 5 * time.Second

// statusFallbackMax is the ceiling for adaptive backoff. When repeated
// fallback refreshes find no changes, the interval doubles each time up
// to this cap. Derived from: 60s is long enough that idle CPU is negligible,
// short enough that external edits are still detected within a minute.
const statusFallbackMax = 60 * time.Second

// statusChanSize is the buffer size for the output channel.
// Buffered so the watcher never blocks on a slow consumer.
const statusChanSize = 1

// =============================================================================
// StatusWatcher
// =============================================================================

// StatusWatcher monitors .git/ internals and working-tree directories via
// fsnotify and exposes a channel of resolved StatusUpdate snapshots. Uses
// a stat-dirty StatusEngine for efficient refresh, falling back to a
// periodic scan for external working-tree edits.
type StatusWatcher struct {
	client     *GitClient
	gitDir     string // resolved .git directory (handles worktrees)
	refsPrefix string // gitDir + "/refs" for new-subdir detection

	out     chan StatusUpdate
	nudgeCh chan struct{}

	engine    *StatusEngine     // stat-dirty engine, replaces direct client calls
	fsw       *fsnotify.Watcher // .git/ internal watcher
	wtWatcher *fsnotify.Watcher // working-tree directory watcher
	stopOnce  sync.Once
	lastUpdate atomic.Pointer[StatusUpdate]
}

// NewStatusWatcher creates a watcher for the given git client.
// Returns an error if the repository's .git directory cannot be resolved
// or the fsnotify watcher cannot be created.
func NewStatusWatcher(client *GitClient) (*StatusWatcher, error) {
	gitDir, err := resolveGitDir(client)
	if err != nil {
		return nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	wtw, err := fsnotify.NewWatcher()
	if err != nil {
		fsw.Close()
		return nil, err
	}

	engine := NewStatusEngine(client, gitDir)

	return &StatusWatcher{
		client:     client,
		gitDir:     gitDir,
		refsPrefix: filepath.Join(gitDir, "refs"),
		out:        make(chan StatusUpdate, statusChanSize),
		nudgeCh:    make(chan struct{}, 1),
		engine:     engine,
		fsw:        fsw,
		wtWatcher:  wtw,
	}, nil
}

// resolveGitDir finds the actual .git directory using pure filesystem logic,
// handling worktrees where .git is a file containing "gitdir: <path>".
func resolveGitDir(client *GitClient) (string, error) {
	dotGit := filepath.Join(client.repoPath, ".git")

	fi, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("resolve .git: %w", err)
	}

	if fi.IsDir() {
		return dotGit, nil
	}

	// Worktree: .git is a file containing "gitdir: <path>".
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("read .git file: %w", err)
	}

	line := strings.TrimSpace(string(data))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("malformed .git file: missing %q prefix", prefix)
	}

	dir := strings.TrimPrefix(line, prefix)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(client.repoPath, dir)
	}

	return filepath.Clean(dir), nil
}

// =============================================================================
// Start
// =============================================================================

// Start begins watching. The output channel (accessible via Events) is closed
// when the context is cancelled or Stop is called.
func (w *StatusWatcher) Start(ctx context.Context) {
	w.addWatchPaths()
	go w.loop(ctx)
}

// Events returns the read-only channel that emits StatusUpdate snapshots on
// each refresh. The caller re-reads from this channel after each receive
// (Bubble Tea subscription pattern).
func (w *StatusWatcher) Events() <-chan StatusUpdate {
	return w.out
}

// LastUpdate returns the most recent StatusUpdate, or nil if no refresh
// has completed yet. The returned pointer is safe to read concurrently.
func (w *StatusWatcher) LastUpdate() *StatusUpdate {
	return w.lastUpdate.Load()
}

// addWatchPaths registers fsnotify watches on key .git/ paths and
// working-tree directories.
func (w *StatusWatcher) addWatchPaths() {
	// Watch .git/ itself for HEAD, index, MERGE_HEAD, REBASE_HEAD, etc.
	_ = w.fsw.Add(w.gitDir)

	// Watch refs subdirectories for branch/tag pointer changes.
	addDirRecursive(w.fsw, w.refsPrefix)

	// Watch working-tree directories for instant change detection.
	w.addWorktreeWatches()
}

// addWorktreeWatches recursively adds fsnotify watches for all non-ignored
// directories under the repository root. Skips .git/ and gitignored dirs.
func (w *StatusWatcher) addWorktreeWatches() {
	repoPath := w.client.repoPath
	ignores := newIgnoreStack(repoPath, w.gitDir)

	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}

		osRel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}

		// Normalize to forward slashes for gitignore matching.
		rel := toSlash(osRel)

		if rel == ".git" {
			return filepath.SkipDir
		}

		if rel != "." && ignores.isIgnored(rel, true) {
			return filepath.SkipDir
		}

		if rel != "." {
			ignores.loadDirPatterns(repoPath, rel)
		}

		_ = w.wtWatcher.Add(path)
		return nil
	})
}

// addDirRecursive adds a directory and all subdirectories to the watcher.
func addDirRecursive(fsw *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		_ = fsw.Add(path)
		return nil
	})
}

// =============================================================================
// Main Loop
// =============================================================================

// loop is the main event loop. It debounces triggers from fsnotify events,
// nudge signals, and an adaptive fallback timer. Refresh runs asynchronously
// so the loop never blocks on slow git operations.
//
// The fallback uses exponential backoff: when a refresh finds no changes the
// interval doubles (up to statusFallbackMax). Any real trigger (fsnotify or
// nudge) resets it to statusFallbackBase.
func (w *StatusWatcher) loop(ctx context.Context) {
	defer close(w.out)
	defer w.fsw.Close()
	defer w.wtWatcher.Close()

	// Adaptive fallback timer (one-shot, re-armed after each refresh).
	fallbackInterval := statusFallbackBase
	fallback := time.NewTimer(0) // fire immediately for initial load
	fallbackFromEvent := true    // first refresh is event-driven (initial load)

	debounce := newStoppedTimer()
	debounceActive := false

	// maxWindow enforces the statusMaxDebounce ceiling. It fires once after
	// the first trigger, forcing a refresh even if events keep resetting the
	// debounce timer.
	maxWindow := newStoppedTimer()
	maxWindowActive := false

	// refreshDone receives completed status updates from the async goroutine.
	refreshDone := make(chan StatusUpdate, 1)
	inFlight := false

	// Change detection for adaptive backoff. Tracks fingerprints of
	// the previous refresh result.
	prevStatusLen   := -1
	prevStatusHash  := uint64(0)
	prevTrackedLen  := -1

	for {
		select {
		case <-ctx.Done():
			drainTimer(debounce)
			drainTimer(maxWindow)
			drainTimer(fallback)
			return

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.watchNewRefDir(ev)
			fallbackFromEvent = true
			fallbackInterval = statusFallbackBase // reset backoff
			arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

		case ev, ok := <-w.wtWatcher.Events:
			if !ok {
				return
			}
			w.handleWorktreeEvent(ev)
			fallbackFromEvent = true
			fallbackInterval = statusFallbackBase // reset backoff
			arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

		case <-w.nudgeCh:
			fallbackFromEvent = true
			fallbackInterval = statusFallbackBase // reset backoff
			arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

		case <-fallback.C:
			if !debounceActive && !inFlight {
				fallbackFromEvent = false
				arm(debounce, &debounceActive, maxWindow, &maxWindowActive)
			} else {
				// Re-arm fallback since we skipped this cycle.
				fallback.Reset(fallbackInterval)
			}

		case <-debounce.C:
			debounceActive = false
			drainTimer(maxWindow)
			maxWindowActive = false
			if !inFlight {
				inFlight = true
				go doRefresh(ctx, w.engine, refreshDone)
			}

		case <-maxWindow.C:
			maxWindowActive = false
			drainTimer(debounce)
			debounceActive = false
			if !inFlight {
				inFlight = true
				go doRefresh(ctx, w.engine, refreshDone)
			}

		case update := <-refreshDone:
			inFlight = false
			w.lastUpdate.Store(&update)

			// Adaptive backoff: if this was a fallback-initiated refresh
			// and neither StatusMap nor TrackedSet changed, double the
			// fallback interval to reduce idle CPU.
			curStatusLen := len(update.StatusMap)
			curStatusHash := statusHash(update.StatusMap)
			curTrackedLen := len(update.TrackedSet)
			unchanged := curStatusLen == prevStatusLen &&
				curStatusHash == prevStatusHash &&
				curTrackedLen == prevTrackedLen
			if !fallbackFromEvent && unchanged {
				fallbackInterval = min(fallbackInterval*2, statusFallbackMax)
			} else {
				fallbackInterval = statusFallbackBase
			}
			prevStatusLen = curStatusLen
			prevStatusHash = curStatusHash
			prevTrackedLen = curTrackedLen

			// Re-arm fallback for the next cycle.
			drainTimer(fallback)
			fallback.Reset(fallbackInterval)

			// Drop-oldest: if the channel is full, discard the stale value.
			select {
			case <-w.out:
			default:
			}
			w.out <- update
		}
	}
}

// arm resets (or starts) the debounce timer and starts the max-window timer
// if not already running.
func arm(debounce *time.Timer, debounceActive *bool, maxWindow *time.Timer, maxWindowActive *bool) {
	resetDebounce(debounce, debounceActive)
	if !*maxWindowActive {
		maxWindow.Reset(statusMaxDebounce)
		*maxWindowActive = true
	}
}

// resetDebounce resets (or starts) the debounce timer.
func resetDebounce(t *time.Timer, active *bool) {
	if !*active {
		t.Reset(statusDebounce)
		*active = true
		return
	}
	// Already active — stop and reset to extend the window.
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(statusDebounce)
}

// drainTimer stops a timer and drains its channel if necessary.
func drainTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

// newStoppedTimer creates a timer that is immediately stopped.
func newStoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

// doRefresh computes working-tree status via the stat-dirty StatusEngine,
// then sends the result on the done channel. Runs in a separate goroutine
// so the event loop is not blocked.
func doRefresh(ctx context.Context, engine *StatusEngine, done chan<- StatusUpdate) {
	if ctx.Err() != nil {
		return
	}

	update, err := engine.Refresh(ctx)
	if err != nil || ctx.Err() != nil {
		return
	}

	done <- update
}

// handleWorktreeEvent processes a working-tree fsnotify event.
// For newly created directories, adds a watch to catch future events.
func (w *StatusWatcher) handleWorktreeEvent(ev fsnotify.Event) {
	if ev.Op&fsnotify.Create == 0 {
		return
	}
	info, err := os.Stat(ev.Name)
	if err != nil || !info.IsDir() {
		return
	}

	// Skip .git directories. Uses OS separator since ev.Name is an OS path.
	rel, relErr := filepath.Rel(w.client.repoPath, ev.Name)
	if relErr != nil || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		return
	}

	_ = w.wtWatcher.Add(ev.Name)
}

// watchNewRefDir dynamically adds fsnotify watches for newly created
// directories under .git/refs/. This catches operations that create new
// ref namespaces (e.g. first stash creating refs/stash/).
func (w *StatusWatcher) watchNewRefDir(ev fsnotify.Event) {
	if ev.Op&fsnotify.Create == 0 {
		return
	}
	if !strings.HasPrefix(ev.Name, w.refsPrefix) {
		return
	}
	info, err := os.Stat(ev.Name)
	if err != nil || !info.IsDir() {
		return
	}
	_ = w.fsw.Add(ev.Name)
}

// statusHash computes an order-independent fingerprint of a status map.
// Each entry is hashed independently (FNV-1a of path+state), then all
// per-entry hashes are XOR'd together. XOR is commutative, so Go's
// non-deterministic map iteration order does not affect the result.
// Collisions are benign (they only delay backoff by one cycle).
func statusHash(m map[string]GitFileState) uint64 {
	const fnvBasis = 14695981039346656037
	const fnvPrime = 1099511628211

	var combined uint64
	for path, state := range m {
		h := uint64(fnvBasis)
		for i := range len(path) {
			h ^= uint64(path[i])
			h *= fnvPrime
		}
		h ^= uint64(state)
		h *= fnvPrime
		combined ^= h
	}
	return combined
}

// =============================================================================
// Nudge
// =============================================================================

// Nudge signals the watcher to refresh soon. Non-blocking; rapid calls are
// coalesced by the buffered channel and debounce timer.
func (w *StatusWatcher) Nudge() {
	select {
	case w.nudgeCh <- struct{}{}:
	default:
	}
}

// =============================================================================
// Stop
// =============================================================================

// Stop closes both fsnotify watchers, which causes the loop to exit and close
// the output channel. Safe to call multiple times.
func (w *StatusWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.fsw.Close()
		w.wtWatcher.Close()
	})
}
