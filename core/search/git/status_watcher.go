package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// statusFallback is the periodic safety-net interval for catching external
// working-tree changes not detected by .git/ watching or Nudge.
const statusFallback = 5 * time.Second

// statusChanSize is the buffer size for the output channel.
// Buffered so the watcher never blocks on a slow consumer.
const statusChanSize = 1

// =============================================================================
// StatusWatcher
// =============================================================================

// StatusWatcher monitors .git/ internals via fsnotify and exposes a channel
// of resolved StatusUpdate snapshots. It replaces tick-based polling with
// event-driven refresh, falling back to a periodic scan for external
// working-tree edits.
type StatusWatcher struct {
	client     *GitClient
	gitDir     string // resolved .git directory (handles worktrees)
	refsPrefix string // gitDir + "/refs" for new-subdir detection

	out     chan StatusUpdate
	nudgeCh chan struct{}

	fsw      *fsnotify.Watcher
	stopOnce sync.Once
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

	return &StatusWatcher{
		client:     client,
		gitDir:     gitDir,
		refsPrefix: filepath.Join(gitDir, "refs"),
		out:        make(chan StatusUpdate, statusChanSize),
		nudgeCh:    make(chan struct{}, 1),
		fsw:        fsw,
	}, nil
}

// resolveGitDir uses git rev-parse to find the actual .git directory,
// handling worktrees where .git is a file pointing elsewhere.
func resolveGitDir(client *GitClient) (string, error) {
	output, err := client.runGitCommand("rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}

	dir := strings.TrimSpace(output)
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

// addWatchPaths registers fsnotify watches on key .git/ paths.
func (w *StatusWatcher) addWatchPaths() {
	// Watch .git/ itself for HEAD, index, MERGE_HEAD, REBASE_HEAD, etc.
	_ = w.fsw.Add(w.gitDir)

	// Watch refs subdirectories for branch/tag pointer changes.
	addDirRecursive(w.fsw, w.refsPrefix)
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
// nudge signals, and a periodic fallback timer. Refresh runs asynchronously
// so the loop never blocks on slow git operations.
func (w *StatusWatcher) loop(ctx context.Context) {
	defer close(w.out)
	defer w.fsw.Close()

	fallback := time.NewTicker(statusFallback)
	defer fallback.Stop()

	debounce := time.NewTimer(0) // fire immediately for initial load
	debounceActive := true

	// maxWindow enforces the statusMaxDebounce ceiling. It fires once after
	// the first trigger, forcing a refresh even if events keep resetting the
	// debounce timer.
	maxWindow := newStoppedTimer()
	maxWindowActive := false

	// refreshDone receives completed status updates from the async goroutine.
	refreshDone := make(chan StatusUpdate, 1)
	inFlight := false

	for {
		select {
		case <-ctx.Done():
			drainTimer(debounce)
			drainTimer(maxWindow)
			return

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.watchNewRefDir(ev)
			arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

		case <-w.nudgeCh:
			arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

		case <-fallback.C:
			if !debounceActive && !inFlight {
				arm(debounce, &debounceActive, maxWindow, &maxWindowActive)
			}

		case <-debounce.C:
			debounceActive = false
			drainTimer(maxWindow)
			maxWindowActive = false
			if !inFlight {
				inFlight = true
				go doRefresh(ctx, w.client, refreshDone)
			}

		case <-maxWindow.C:
			maxWindowActive = false
			drainTimer(debounce)
			debounceActive = false
			if !inFlight {
				inFlight = true
				go doRefresh(ctx, w.client, refreshDone)
			}

		case update := <-refreshDone:
			inFlight = false
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

// doRefresh queries git status and tracked set, then sends the result on the
// done channel. Runs in a separate goroutine so the event loop is not blocked.
func doRefresh(ctx context.Context, client *GitClient, done chan<- StatusUpdate) {
	// Early exit if context is already cancelled.
	if ctx.Err() != nil {
		return
	}

	wts, err := client.WorktreeStatus()
	if err != nil {
		return
	}

	statusMap := BuildStatusMap(wts)
	tracked := client.TrackedSet()
	trackedDirs := BuildTrackedDirs(tracked)

	// Check context again after potentially slow operations.
	if ctx.Err() != nil {
		return
	}

	done <- StatusUpdate{
		StatusMap:   statusMap,
		TrackedSet:  tracked,
		TrackedDirs: trackedDirs,
	}
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

// Stop closes the fsnotify watcher, which causes the loop to exit and close
// the output channel. Safe to call multiple times.
func (w *StatusWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.fsw.Close()
	})
}
