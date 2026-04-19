package accounting

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// Errors surfaced by Accountant.
var (
	ErrNilEvent      = errors.New("accounting: nil event")
	ErrNilIdentity   = errors.New("accounting: event has nil identity")
	ErrNilTask       = errors.New("accounting: event has nil task")
	ErrNamespaceSkew = errors.New("accounting: event namespace does not match accountant")
)

// defaultSubscriberBuffer is the cap-size of each subscriber's
// delta channel. Large enough to absorb a burst of ~1K deltas
// before the subscriber wakes; counted drops are emitted as logs.
const defaultSubscriberBuffer = 1024

// Config configures an Accountant.
type Config struct {
	// Namespace scopes the accountant to a single session. All
	// events must carry matching identity.Namespace.
	Namespace identity.Namespace
	// WAL (optional) persists deltas. A nil WAL is acceptable for
	// tests and short-lived runs; in production cmd/tui.go wires a
	// file-backed WAL at .sylk/sessions/{namespace}/accounting/.
	WAL WAL
	// Clock is optional; defaults to time.Now.
	Clock func() time.Time
	// Logger is optional; defaults to slog.Default.
	Logger *slog.Logger
	// SubscriberBufferSize overrides defaultSubscriberBuffer.
	SubscriberBufferSize int
}

// WAL is the persistence contract the accountant uses. The default
// file-backed implementation lives in wal.go. A nil WAL is
// equivalent to Noop.
type WAL interface {
	Append(UsageDelta) error
	Replay(func(UsageDelta) error) error
	Close() error
}

// Accountant is the single aggregator for LLM token usage across
// the session. Thread-safe; all exported methods are safe for
// concurrent use.
type Accountant struct {
	namespace identity.Namespace
	clock     func() time.Time
	logger    *slog.Logger
	wal       WAL
	bufSize   int

	mu      sync.RWMutex
	buckets map[AccountingKey]*AggregatedUsage
	// containerMeta remembers each container's identity and most
	// recent task so snapshots can annotate the canonical
	// identity details without re-walking the map.
	containerMeta map[identity.UID]containerSnapshot

	subMu        sync.RWMutex
	subscribers  []*subscriber
	droppedTotal atomic.Uint64

	closed atomic.Bool
}

type containerSnapshot struct {
	kind     identity.AgentType
	pod      identity.PodRef
	category identity.Category
	labels   identity.Labels
}

type subscriber struct {
	ch      chan UsageDelta
	dropped atomic.Uint64
}

// New constructs an Accountant.
func New(cfg Config) (*Accountant, error) {
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("accounting: namespace required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	buf := cfg.SubscriberBufferSize
	if buf <= 0 {
		buf = defaultSubscriberBuffer
	}
	a := &Accountant{
		namespace:     cfg.Namespace,
		clock:         clock,
		logger:        logger,
		wal:           cfg.WAL,
		bufSize:       buf,
		buckets:       make(map[AccountingKey]*AggregatedUsage),
		containerMeta: make(map[identity.UID]containerSnapshot),
	}
	// Best-effort replay: a corrupt record is logged and skipped
	// rather than aborting startup.
	if a.wal != nil {
		if err := a.wal.Replay(a.replayDelta); err != nil {
			a.logger.Warn("accounting: WAL replay partial", "error", err)
		}
	}
	return a, nil
}

// Namespace returns the session the accountant is bound to.
func (a *Accountant) Namespace() identity.Namespace { return a.namespace }

// Record ingests one event. Thread-safe; returns an error only on
// structural invariants (nil identity, nil task, wrong namespace).
// Persistence errors are logged, not surfaced, so callers never
// block on disk.
func (a *Accountant) Record(evt Event) error {
	if a.closed.Load() {
		return nil
	}
	if evt.Identity == nil {
		return ErrNilIdentity
	}
	if evt.Task == nil {
		return ErrNilTask
	}
	if evt.Identity.Namespace() != a.namespace {
		return fmt.Errorf("%w: event ns %q, accountant ns %q",
			ErrNamespaceSkew, evt.Identity.Namespace(), a.namespace)
	}
	if evt.RecordedAt.IsZero() {
		evt.RecordedAt = a.clock()
	}

	key := AccountingKey{
		Container: evt.Identity.Key(),
		Task:      evt.Task.UID(),
	}
	billable := evt.Identity.Category().IsBillable()

	delta := UsageDelta{
		Key:       key,
		Identity:  evt.Identity,
		Task:      evt.Task,
		Usage:     evt.Usage,
		Elapsed:   evt.Elapsed,
		Phase:     evt.Phase,
		Billable:  billable,
		Timestamp: evt.RecordedAt,
	}

	a.mu.Lock()
	bucket := a.buckets[key]
	if bucket == nil {
		bucket = &AggregatedUsage{Billable: billable}
		a.buckets[key] = bucket
	}
	bucket.Add(evt.Usage, evt.Elapsed, evt.Phase, evt.RecordedAt)
	a.containerMeta[evt.Identity.UID()] = containerSnapshot{
		kind:     evt.Identity.Kind(),
		pod:      evt.Identity.Pod(),
		category: evt.Identity.Category(),
		labels:   evt.Identity.Labels(),
	}
	a.mu.Unlock()

	if a.wal != nil {
		if err := a.wal.Append(delta); err != nil {
			a.logger.Warn("accounting: WAL append failed",
				"error", err,
				"key", key.String(),
			)
		}
	}

	a.fanout(delta)
	return nil
}

// fanout sends delta to every live subscriber. Non-blocking: a
// full channel increments that subscriber's dropped counter and
// continues.
func (a *Accountant) fanout(d UsageDelta) {
	a.subMu.RLock()
	subs := make([]*subscriber, len(a.subscribers))
	copy(subs, a.subscribers)
	a.subMu.RUnlock()

	for _, s := range subs {
		select {
		case s.ch <- d:
		default:
			s.dropped.Add(1)
			a.droppedTotal.Add(1)
		}
	}
}

// Subscribe returns a read-only channel that receives every
// subsequent UsageDelta. The returned cancel function drains and
// closes the channel. Callers must eventually invoke cancel to
// avoid leaking.
func (a *Accountant) Subscribe() (<-chan UsageDelta, func()) {
	s := &subscriber{ch: make(chan UsageDelta, a.bufSize)}
	a.subMu.Lock()
	a.subscribers = append(a.subscribers, s)
	a.subMu.Unlock()

	cancel := func() {
		a.subMu.Lock()
		for i, sub := range a.subscribers {
			if sub == s {
				a.subscribers = append(a.subscribers[:i], a.subscribers[i+1:]...)
				break
			}
		}
		a.subMu.Unlock()
		// Drain + close safely. We close after removing from the
		// list so future Record calls can't race a send into a
		// closed channel.
		close(s.ch)
	}
	return s.ch, cancel
}

// replayDelta is invoked during WAL replay. It re-applies a delta
// to the in-memory state without re-persisting or fanning out.
func (a *Accountant) replayDelta(d UsageDelta) error {
	if d.Identity == nil || d.Task == nil {
		return nil
	}
	if d.Identity.Namespace() != a.namespace {
		return nil
	}
	bucket := a.buckets[d.Key]
	if bucket == nil {
		bucket = &AggregatedUsage{Billable: d.Billable}
		a.buckets[d.Key] = bucket
	}
	bucket.Add(d.Usage, d.Elapsed, d.Phase, d.Timestamp)
	a.containerMeta[d.Identity.UID()] = containerSnapshot{
		kind:     d.Identity.Kind(),
		pod:      d.Identity.Pod(),
		category: d.Identity.Category(),
		labels:   d.Identity.Labels(),
	}
	return nil
}

// Close finalizes the WAL and marks the accountant read-only. No
// further Record calls will succeed after Close. Safe to call
// multiple times.
func (a *Accountant) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Close subscribers — forcibly so no goroutine blocks.
	a.subMu.Lock()
	for _, s := range a.subscribers {
		close(s.ch)
	}
	a.subscribers = nil
	a.subMu.Unlock()

	if a.wal != nil {
		return a.wal.Close()
	}
	return nil
}

// DroppedCount returns the cumulative number of deltas dropped
// across every subscriber due to backpressure. For telemetry.
func (a *Accountant) DroppedCount() uint64 {
	return a.droppedTotal.Load()
}

// =============================================================================
// Views
// =============================================================================

// ByKey returns the usage bucket for exactly this key. Returns a
// zero-value AggregatedUsage when no data has been recorded.
func (a *Accountant) ByKey(k AccountingKey) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if u, ok := a.buckets[k]; ok {
		return *u
	}
	return AggregatedUsage{}
}

// ByContainer returns, for each generation under this container
// UID, a map of (task → aggregated usage). Empty map when no
// data.
func (a *Accountant) ByContainer(uid identity.UID) map[identity.Generation]map[identity.TaskUID]AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[identity.Generation]map[identity.TaskUID]AggregatedUsage)
	for k, u := range a.buckets {
		if k.Container.UID != uid {
			continue
		}
		gen := k.Container.Generation
		if out[gen] == nil {
			out[gen] = make(map[identity.TaskUID]AggregatedUsage)
		}
		out[gen][k.Task] = *u
	}
	return out
}

// ByGeneration returns the aggregate across all tasks under one
// (UID, Generation, Model).
func (a *Accountant) ByGeneration(uid identity.UID, gen identity.Generation) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for k, u := range a.buckets {
		if k.Container.UID == uid && k.Container.Generation == gen {
			total.Merge(*u)
		}
	}
	return total
}

// ByKind aggregates across every container of the given agent kind.
func (a *Accountant) ByKind(k identity.AgentType) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for key, u := range a.buckets {
		meta, ok := a.containerMeta[key.Container.UID]
		if !ok || meta.kind != k {
			continue
		}
		total.Merge(*u)
	}
	return total
}

// ByModel aggregates across every bucket with this model identifier.
func (a *Accountant) ByModel(m identity.ModelID) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for k, u := range a.buckets {
		if k.Container.Model == m {
			total.Merge(*u)
		}
	}
	return total
}

// ByPod aggregates across every container hosted by the given pod.
func (a *Accountant) ByPod(p identity.PodID) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for key, u := range a.buckets {
		meta, ok := a.containerMeta[key.Container.UID]
		if !ok || meta.pod.ID != p {
			continue
		}
		total.Merge(*u)
	}
	return total
}

// ByTask aggregates across every container that serviced this task.
func (a *Accountant) ByTask(t identity.TaskUID) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for k, u := range a.buckets {
		if k.Task == t {
			total.Merge(*u)
		}
	}
	return total
}

// ByPipeline aggregates across every task with a matching pipeline
// label. Requires the pipeline id to be present in the container's
// labels under LabelPipeline (set by dispatch sites when the task
// is a pipeline step).
func (a *Accountant) ByPipeline(p identity.PipelineID) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for k, u := range a.buckets {
		meta, ok := a.containerMeta[k.Container.UID]
		if !ok {
			continue
		}
		if meta.labels.Get(identity.LabelPipeline) == string(p) {
			total.Merge(*u)
		}
	}
	return total
}

// ByNamespace returns the session-wide total. For a session-bound
// accountant this equals the All() reduction.
func (a *Accountant) ByNamespace(n identity.Namespace) AggregatedUsage {
	if n != a.namespace {
		return AggregatedUsage{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for _, u := range a.buckets {
		total.Merge(*u)
	}
	return total
}

// BySelector aggregates across every container whose labels match
// the selector.
func (a *Accountant) BySelector(sel *identity.Selector) AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for k, u := range a.buckets {
		meta, ok := a.containerMeta[k.Container.UID]
		if !ok {
			continue
		}
		if !sel.Matches(meta.labels) {
			continue
		}
		total.Merge(*u)
	}
	return total
}

// Billable returns the namespace total restricted to billable
// (non-CategorySystem) buckets. CategorySystem requests are
// recorded but excluded.
func (a *Accountant) Billable() AggregatedUsage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total AggregatedUsage
	for _, u := range a.buckets {
		if !u.Billable {
			continue
		}
		total.Merge(*u)
	}
	return total
}

// All returns a point-in-time snapshot of every bucket. The slice
// is newly allocated; mutation by the caller is safe.
func (a *Accountant) All() []AccountingSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AccountingSnapshot, 0, len(a.buckets))
	for k, u := range a.buckets {
		out = append(out, AccountingSnapshot{Key: k, Usage: *u})
	}
	return out
}
