package activitystore

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// HarvestDispatcher wraps a synchronous HarvestFunc with an async
// pipeline that satisfies the substrate's "async by default"
// invariant: bounded queue, worker pool, activity-ID dedupe,
// drop/error counters, and errors-as-artifacts surfacing via an
// optional error sink. Submitting a candidate is non-blocking — full
// queue increments the drop counter, dedupe hits increment the dedup
// counter, and runtime errors increment the error counter and
// (when wired) emit a kind=error_harvest_failed artifact via the
// error sink.
//
// Workers are persistent goroutines spawned in NewHarvestDispatcher.
// Stop() closes the queue and waits for workers to drain. Designed
// for the forest-fabric harvest path where a synchronous SQLite
// ledger write would otherwise serialize every claim-related
// activity emitter on a single mutex (the prior subscriber.Receive
// behavior).
type HarvestDispatcher struct {
	inner       HarvestFunc
	queue       chan ForestCandidate
	workTimeout time.Duration

	// Activity-ID dedupe — bounded ring buffer. Re-deliveries
	// (subscriber retry, replay) collapse to a single harvest call.
	// dedupRing is allocated once at construction and never grows;
	// dedupHead is the next-write slot (also the oldest entry when
	// the ring is full); dedupSize tracks the count of live entries.
	// Eviction overwrites in place rather than the slice-shift
	// pattern that would leave evicted IDs reachable through the
	// backing array between GC cycles.
	dedupMu   sync.Mutex
	dedupSeen map[activity.ActivityID]struct{}
	dedupRing []activity.ActivityID
	dedupHead int
	dedupSize int
	dedupCap  int

	// Counters — observable via the *Count accessors.
	submitted atomic.Uint64
	enqueued  atomic.Uint64
	dropped   atomic.Uint64
	deduped   atomic.Uint64
	completed atomic.Uint64
	errored   atomic.Uint64

	// errorSink, when non-nil, receives every harvest error so the
	// caller can surface it as a kind=error_harvest_failed artifact
	// on the in-flight testament (per CLAIMS.md §5.11 errors-as-
	// artifacts invariant). Nil sink degrades to log-only.
	errorSink HarvestErrorSink

	// Shutdown coordination.
	stopOnce sync.Once
	stopCh   chan struct{}
	workers  sync.WaitGroup
}

// HarvestErrorSink receives each harvest error along with the
// originating activity. Implementations that touch the claims board
// must be non-blocking themselves — the sink runs on the dispatcher's
// worker goroutine.
type HarvestErrorSink func(activity.AgentActivity, error)

// HarvestDispatcherConfig tunes the dispatcher's resource bounds.
// All fields are derived rather than literal where possible:
//
//   - Workers defaults to clamp(NumCPU, 1, 8). One worker per core
//     up to a small cap, since SQLite ledger writes are serialized
//     by the underlying handle anyway and over-spawning workers
//     past that ceiling produces lock contention without throughput
//     gain.
//   - QueueCapacity defaults to 8 * Workers. Each worker has up to
//     8 items in its pipeline depth budget; beyond that, fast-fail
//     to the drop counter rather than unbounded buffer growth.
//   - DedupeCapacity defaults to 4 * QueueCapacity. Bounded LRU
//     covering 4× the queue depth so re-deliveries within a worst-
//     case backlog are still suppressed.
//   - WorkTimeout defaults to 5s, the same fence used by the forest
//     ledger write path's outer context budgets. Hard cap so a
//     stuck inner harvester can't pin a worker indefinitely.
type HarvestDispatcherConfig struct {
	Workers        int
	QueueCapacity  int
	DedupeCapacity int
	WorkTimeout    time.Duration
	ErrorSink      HarvestErrorSink
}

// DefaultHarvestDispatcherConfig returns the derivation defaults.
// Use this and override individual fields rather than constructing
// a config from scratch.
func DefaultHarvestDispatcherConfig() HarvestDispatcherConfig {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	queueCap := 8 * workers
	dedupeCap := 4 * queueCap
	return HarvestDispatcherConfig{
		Workers:        workers,
		QueueCapacity:  queueCap,
		DedupeCapacity: dedupeCap,
		WorkTimeout:    5 * time.Second,
	}
}

// ErrHarvestDispatcherStopped is returned by Submit when the
// dispatcher has already been stopped.
var ErrHarvestDispatcherStopped = errors.New("harvest dispatcher stopped")

// NewHarvestDispatcher constructs a dispatcher and spawns the
// configured worker pool. Pass cfg = DefaultHarvestDispatcherConfig()
// for the derived defaults; override only when a specific anchor
// changes (e.g. very high event rate).
//
// inner is the synchronous HarvestFunc the dispatcher wraps, typically
// (*forest.FabricContextObserver).Observe.
func NewHarvestDispatcher(inner HarvestFunc, cfg HarvestDispatcherConfig) *HarvestDispatcher {
	if cfg.Workers <= 0 || cfg.QueueCapacity <= 0 || cfg.DedupeCapacity <= 0 || cfg.WorkTimeout <= 0 {
		defaults := DefaultHarvestDispatcherConfig()
		if cfg.Workers <= 0 {
			cfg.Workers = defaults.Workers
		}
		if cfg.QueueCapacity <= 0 {
			cfg.QueueCapacity = defaults.QueueCapacity
		}
		if cfg.DedupeCapacity <= 0 {
			cfg.DedupeCapacity = defaults.DedupeCapacity
		}
		if cfg.WorkTimeout <= 0 {
			cfg.WorkTimeout = defaults.WorkTimeout
		}
	}
	d := &HarvestDispatcher{
		inner:       inner,
		queue:       make(chan ForestCandidate, cfg.QueueCapacity),
		workTimeout: cfg.WorkTimeout,
		dedupSeen:   make(map[activity.ActivityID]struct{}, cfg.DedupeCapacity),
		dedupRing:   make([]activity.ActivityID, cfg.DedupeCapacity),
		dedupCap:    cfg.DedupeCapacity,
		errorSink:   cfg.ErrorSink,
		stopCh:      make(chan struct{}),
	}
	for i := 0; i < cfg.Workers; i++ {
		d.workers.Add(1)
		go d.worker()
	}
	return d
}

// Harvest matches the HarvestFunc signature so the dispatcher can be
// passed directly to the forest fabric observer registrar. Non-blocking: enqueues the
// candidate or increments the drop counter on overflow / dedupe / stop.
//
// The error return is reserved for a stopped dispatcher; queue overflow
// and dedupe hits are observable via DroppedCount / DedupedCount and
// do NOT return an error (they're best-effort drops by design).
func (d *HarvestDispatcher) Harvest(_ context.Context, candidate ForestCandidate) error {
	d.submitted.Add(1)
	if d == nil {
		return nil
	}
	if !d.checkAndMarkSeen(candidate.Activity.ID) {
		d.deduped.Add(1)
		return nil
	}
	select {
	case d.queue <- candidate:
		d.enqueued.Add(1)
		return nil
	case <-d.stopCh:
		return ErrHarvestDispatcherStopped
	default:
		d.dropped.Add(1)
		slog.Warn("forest_harvest_dispatcher_dropped",
			"activity_id", string(candidate.Activity.ID),
			"action_kind", string(candidate.Activity.Action),
			"reason", candidate.Reason,
			"queue_capacity", cap(d.queue),
		)
		return nil
	}
}

// Stop closes the queue and waits for workers to drain. Idempotent.
// After Stop, Harvest returns ErrHarvestDispatcherStopped on every
// call; in-flight items already in the queue still complete.
func (d *HarvestDispatcher) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() {
		close(d.stopCh)
		close(d.queue)
	})
	d.workers.Wait()
}

// SubmittedCount returns the running total of Harvest calls.
func (d *HarvestDispatcher) SubmittedCount() uint64 { return d.submitted.Load() }

// EnqueuedCount returns the count of candidates that made it onto
// the queue (post-dedupe, pre-worker).
func (d *HarvestDispatcher) EnqueuedCount() uint64 { return d.enqueued.Load() }

// DroppedCount returns the count of candidates dropped due to a full
// queue. A non-zero value indicates harvest pressure exceeds capacity;
// operators should investigate downstream slowness or raise the cap.
func (d *HarvestDispatcher) DroppedCount() uint64 { return d.dropped.Load() }

// DedupedCount returns the count of candidates suppressed because
// their activity ID had already been seen within the dedupe window.
func (d *HarvestDispatcher) DedupedCount() uint64 { return d.deduped.Load() }

// CompletedCount returns the count of candidates the inner harvester
// processed without returning an error.
func (d *HarvestDispatcher) CompletedCount() uint64 { return d.completed.Load() }

// ErroredCount returns the count of candidates the inner harvester
// returned an error for. Each error was logged and (when an error
// sink is wired) surfaced as an error artifact.
func (d *HarvestDispatcher) ErroredCount() uint64 { return d.errored.Load() }

// worker drains the queue. One per cfg.Workers. Exits when the queue
// is closed and drained.
func (d *HarvestDispatcher) worker() {
	defer d.workers.Done()
	for candidate := range d.queue {
		d.runOne(candidate)
	}
}

// runOne executes the inner harvester under WorkTimeout. Errors are
// counted, logged, and (when wired) surfaced via the error sink so
// the caller can emit a kind=error_harvest_failed artifact onto the
// in-flight testament (per CLAIMS.md §5.11 errors-as-artifacts).
func (d *HarvestDispatcher) runOne(candidate ForestCandidate) {
	if d.inner == nil {
		d.completed.Add(1)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.workTimeout)
	defer cancel()
	if err := d.inner(ctx, candidate); err != nil {
		d.errored.Add(1)
		slog.Error("forest_harvest_dispatcher_error",
			"activity_id", string(candidate.Activity.ID),
			"action_kind", string(candidate.Activity.Action),
			"reason", candidate.Reason,
			"err", err.Error(),
		)
		if d.errorSink != nil {
			d.errorSink(candidate.Activity, err)
		}
		return
	}
	d.completed.Add(1)
}

// checkAndMarkSeen returns true when the activity ID is new (caller
// should proceed with enqueue) and false when it was already seen
// inside the bounded dedupe window. Maintains a bounded ring buffer
// keyed by insertion order: the ring is allocated once at
// construction and never grows; eviction overwrites the oldest slot
// in place. This avoids the slice-shift pattern that would leave
// evicted IDs reachable through the backing array between GC cycles.
func (d *HarvestDispatcher) checkAndMarkSeen(id activity.ActivityID) bool {
	if id == "" {
		return true
	}
	d.dedupMu.Lock()
	defer d.dedupMu.Unlock()
	if _, seen := d.dedupSeen[id]; seen {
		return false
	}
	d.dedupSeen[id] = struct{}{}
	if d.dedupSize == d.dedupCap {
		// Ring full — overwrite the oldest slot at dedupHead and drop
		// the evicted id from the seen-map so it can be observed
		// again later.
		evict := d.dedupRing[d.dedupHead]
		delete(d.dedupSeen, evict)
	} else {
		d.dedupSize++
	}
	d.dedupRing[d.dedupHead] = id
	d.dedupHead = (d.dedupHead + 1) % d.dedupCap
	return true
}
