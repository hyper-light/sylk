package fabriclog

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
)

// FabricLogger writes the session-global fabric observability stream.
// One logger per session. Methods are safe for concurrent callers.
//
// The logger owns one agentlog.StreamWriter at
// {baseDir}/fabric/fabric.{YYYY-MM-DD}.{nnn}.jsonl. Writes go through
// a bounded channel to a single drain goroutine so the producing
// fabric publish/read hot path never blocks on disk I/O. Overflow
// increments a drop counter and periodically emits a KindDrop record
// so dead-letter is observable.
//
// A bounded recent-publish LRU keyed on ActivityID retains (Seq,
// Timestamp, ActionKind, Actor) for each published activity. When a
// consume or resolve record is produced, the logger joins against
// this cache to compute publish→consume / publish→resolve latency
// and publisher attribution.
type FabricLogger struct {
	writer *agentlog.StreamWriter

	sessionID string

	seq   atomic.Int64
	mono0 time.Time // process-start monotonic reference

	ch   chan FabricLogRecord
	done chan struct{}

	drops     atomic.Int64
	dropMarks atomic.Int64
	closed    atomic.Bool
	emitted   atomic.Int64 // records successfully enqueued
	written   atomic.Int64 // records successfully written to disk
	writeErrs atomic.Int64

	// Recent-publish cache: bounded FIFO keyed on activity ID so
	// consume/resolve records can resolve the originating publish.
	// Bounded to FabricLoggerConfig.RecentPublishLRU.
	recentMu    sync.Mutex
	recentByID  map[string]publishMeta
	recentOrder []string // FIFO eviction order
	recentCap   int

	// Subscribers fanout. The drain goroutine is the single reader;
	// registration uses an atomic swap pattern to avoid locking on
	// the hot path. A nil slice means no subscribers — the common
	// case before bridges install.
	subsMu      sync.Mutex
	subscribers []Subscriber

	// Periodicity for KindDrop emissions — one drop record per 1024
	// drops by default so the log doesn't amplify pressure.
	dropReportEvery int64
}

// FabricLoggerConfig controls writer rotation, redaction, buffer
// depth, and recent-publish cache size. Zero values fall back to
// sensible defaults.
type FabricLoggerConfig struct {
	// BaseDir is the per-session directory the logger anchors under.
	// The writer creates {BaseDir}/{YYYY-MM-DD}/fabric.{nnn}.jsonl
	// inside it.
	BaseDir string

	// SessionID identifies the session. Embedded in every record for
	// cross-session aggregation.
	SessionID string

	// StreamWriterConfig tunes rotation. Zero values resolve to
	// agentlog defaults (64 MiB per file, local TZ, 2s sync).
	StreamWriterConfig agentlog.StreamWriterConfig

	// Redactor is applied to every record before write. Nil is a
	// no-op (records pass through unchanged).
	Redactor agentlog.Redactor

	// BufferSize bounds the async write channel. When the channel
	// fills, new records are dropped with a KindDrop emission. Zero
	// falls back to defaultBufferSize.
	BufferSize int

	// RecentPublishLRU bounds the recent-publish join cache. Zero
	// falls back to defaultRecentCap. A larger cache resolves more
	// publish→consume joins at the cost of memory (~200 bytes per
	// entry).
	RecentPublishLRU int

	// DropReportEvery controls how often KindDrop records are emitted
	// under sustained pressure. One record per N drops. Zero falls
	// back to defaultDropReportEvery. Setting to 1 records every drop
	// (useful in tests), but in production can itself fall into a
	// dropped-emission loop.
	DropReportEvery int64
}

// Defaults chosen for a typical multi-agent session. Tuned so
// instrumentation cost stays well under 1% of total throughput.
//
// defaultRecentCap derivation: cache exists to join publish→consume
// records by ActivityID. Consume happens within milliseconds of
// publish in the steady state (subscribers run on the dispatch
// goroutine), so the meaningful in-flight window is small. 1024
// entries covers worst-case spikes amply while bounding the
// per-entry FullActivity copy (~1 KiB × 1024 = ~1 MiB resident vs.
// the prior 16 MiB at 16384). Larger sessions that need longer
// publish→consume joins can override via FabricLoggerConfig.
const (
	defaultBufferSize       = 4096
	defaultRecentCap        = 1024
	defaultDropReportEvery  = 1024
	maxReturnedIDsPerRead   = 128
	maxActivityPayloadBytes = 64 * 1024
)

type publishMeta struct {
	Seq        int64
	Timestamp  time.Time
	ActionKind string
	AgentID    string
	AgentType  string
	// FullActivity retains the complete activity payload so
	// subscribers (e.g., the forest bridge) can reason over
	// consumed/resolved activities without an extra SQLite lookup.
	// Bounded by the same LRU cap as the rest of the cache — roughly
	// 500B × RecentPublishLRU memory cost.
	FullActivity activity.AgentActivity
}

// Subscriber is invoked once per record as it flows through the
// logger. Subscribers run on the drain goroutine — they must not
// block indefinitely, and must not call back into FabricLogger
// methods that enqueue (risking recursion). Forest bridging, UI
// telemetry, and custom analysis tools implement this interface.
type Subscriber interface {
	// OnRecord fires exactly once per record, after the record has
	// been written to disk. Subscribers run in-order (registration
	// order) and synchronously in the drain goroutine, so all
	// subscribers should keep per-record work bounded. Errors are
	// logged; they do not stop delivery to other subscribers.
	OnRecord(record FabricLogRecord)
}

// SubscriberFunc adapts a function to the Subscriber interface.
type SubscriberFunc func(record FabricLogRecord)

// OnRecord satisfies Subscriber.
func (f SubscriberFunc) OnRecord(record FabricLogRecord) {
	if f != nil {
		f(record)
	}
}

// NewFabricLogger constructs a logger anchored at cfg.BaseDir/fabric.
// The writer is opened lazily on first write, so failing to create
// the directory up front is not fatal — errors surface on emit.
func NewFabricLogger(cfg FabricLoggerConfig) (*FabricLogger, error) {
	if cfg.BaseDir == "" {
		return nil, fmt.Errorf("fabriclog: BaseDir required")
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = defaultBufferSize
	}
	if cfg.RecentPublishLRU <= 0 {
		cfg.RecentPublishLRU = defaultRecentCap
	}
	if cfg.DropReportEvery <= 0 {
		cfg.DropReportEvery = defaultDropReportEvery
	}

	w, err := agentlog.NewStreamWriter(cfg.BaseDir, "fabric", cfg.StreamWriterConfig, cfg.Redactor)
	if err != nil {
		return nil, fmt.Errorf("fabriclog: new stream writer: %w", err)
	}

	l := &FabricLogger{
		writer:          w,
		sessionID:       cfg.SessionID,
		mono0:           time.Now(),
		ch:              make(chan FabricLogRecord, cfg.BufferSize),
		done:            make(chan struct{}),
		recentByID:      make(map[string]publishMeta, cfg.RecentPublishLRU),
		recentOrder:     make([]string, 0, cfg.RecentPublishLRU),
		recentCap:       cfg.RecentPublishLRU,
		dropReportEvery: cfg.DropReportEvery,
	}
	go l.drain()
	return l, nil
}

// Close drains the buffer, flushes the writer, and closes the stream.
// Idempotent. Safe to call from a defer in main().
func (l *FabricLogger) Close() error {
	if l == nil {
		return nil
	}
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(l.ch)
	<-l.done
	return l.writer.Close()
}

// Stats returns a snapshot of logger counters for observability.
// EmittedRecords / WrittenRecords / Drops let you answer "is the
// logger keeping up?" at a glance.
type Stats struct {
	EmittedRecords int64 `json:"emitted"`
	WrittenRecords int64 `json:"written"`
	Drops          int64 `json:"drops"`
	WriteErrors    int64 `json:"write_errors"`
}

// Stats returns the current counter snapshot. Safe to call at any
// time.
func (l *FabricLogger) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	return Stats{
		EmittedRecords: l.emitted.Load(),
		WrittenRecords: l.written.Load(),
		Drops:          l.drops.Load(),
		WriteErrors:    l.writeErrs.Load(),
	}
}

// ─── Emit paths (called by recording decorators) ───────────────────

// RecordPublish emits a KindPublish record and updates the recent-
// publish cache. The activity is marshaled once; the raw JSON is
// embedded in PublishBody.Activity so downstream consumers can
// reconstruct the full payload without another join.
func (l *FabricLogger) RecordPublish(a activity.AgentActivity) {
	if l == nil {
		return
	}
	raw, err := json.Marshal(a)
	if err != nil {
		// Unrecoverable — marshal errors mean the activity can't be
		// persisted. Count as a write error and skip.
		l.writeErrs.Add(1)
		return
	}
	if len(raw) > maxActivityPayloadBytes {
		// Trim extremely large payloads rather than drop the record;
		// the log still carries the activity ID and Action so the
		// pattern is recoverable even if the body is truncated.
		raw = raw[:maxActivityPayloadBytes]
	}

	publishSeq := l.nextSeq()
	body := &PublishBody{
		ActivityID:   string(a.ID),
		ActionKind:   string(a.Action),
		State:        string(a.State),
		Confidence:   string(a.Confidence),
		Scope:        a.Subject.PathPrefix,
		TargetAgent:  a.Subject.TargetAgent,
		Domain:       a.Subject.Domain,
		PayloadBytes: len(raw),
		Activity:     raw,
	}
	if a.Caused != nil {
		body.Caused = string(*a.Caused)
	}
	if a.Resolves != nil {
		body.Resolves = string(*a.Resolves)
	}

	rec := FabricLogRecord{
		Kind:      KindPublish,
		SessionID: string(a.SessionID),
		Seq:       publishSeq,
		Timestamp: a.Timestamp,
		MonoNano:  monoDelta(l.mono0),
		Agent: AgentRef{
			AgentID:    a.Actor.AgentID,
			AgentType:  a.Actor.AgentType,
			PipelineID: a.Actor.PipelineID,
		},
		Publish: body,
	}
	l.cacheRecent(string(a.ID), publishMeta{
		Seq:          publishSeq,
		Timestamp:    a.Timestamp,
		ActionKind:   string(a.Action),
		AgentID:      a.Actor.AgentID,
		AgentType:    a.Actor.AgentType,
		FullActivity: a,
	})
	l.enqueue(rec)

	// Resolution edge: if this activity closes out another, emit a
	// matching KindResolve so lifecycle traces don't require a
	// second pass over the publish stream.
	if a.Resolves != nil && *a.Resolves != "" {
		l.recordResolveLocked(a, *a.Resolves)
	}
}

// RecordRead emits a KindRead record and returns its Seq so the
// caller can attach KindConsume records that join back via
// ReaderRecordSeq. Returns zero when the logger is nil so callers
// can always call RecordConsume unconditionally — a zero
// ReaderRecordSeq is a sentinel meaning "no matching read record".
func (l *FabricLogger) RecordRead(
	ctx context.Context,
	lens string,
	alias string,
	filter *FilterBody,
	returnedIDs []string,
	returnedCount int,
	elapsed time.Duration,
	err error,
) int64 {
	if l == nil {
		return 0
	}
	// Bound the captured returned IDs to keep the record reasonable;
	// we still record ReturnedCount so downstream analysis knows the
	// lens had more to say.
	capped := returnedIDs
	if len(capped) > maxReturnedIDsPerRead {
		capped = capped[:maxReturnedIDsPerRead]
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	seq := l.nextSeq()
	rec := FabricLogRecord{
		Kind:      KindRead,
		SessionID: l.sessionID,
		Seq:       seq,
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Agent:     ReaderFromContext(ctx),
		Read: &ReadBody{
			Lens:          lens,
			LensAlias:     alias,
			Filter:        filter,
			ReturnedIDs:   capped,
			ReturnedCount: returnedCount,
			ElapsedMicros: elapsed.Microseconds(),
			Err:           errStr,
		},
	}
	l.enqueue(rec)
	return seq
}

// RecordConsume emits a KindConsume record joining a reader
// (identified by readerRecordSeq) to a consumed activity. When the
// activity is present in the recent-publish cache, the originating
// publish seq and publish→consume latency are populated.
func (l *FabricLogger) RecordConsume(
	ctx context.Context,
	readerRecordSeq int64,
	a activity.AgentActivity,
) {
	if l == nil {
		return
	}
	body := &ConsumeBody{
		ReaderRecordSeq: readerRecordSeq,
		ActivityID:      string(a.ID),
		ActionKind:      string(a.Action),
		ProducedByAgent: a.Actor.AgentID,
		ProducedByType:  a.Actor.AgentType,
	}
	if meta, ok := l.lookupRecent(string(a.ID)); ok {
		body.PublishSeq = meta.Seq
		if !meta.Timestamp.IsZero() && !a.Timestamp.IsZero() {
			latency := time.Since(meta.Timestamp)
			body.LatencyMicros = latency.Microseconds()
		}
	}
	rec := FabricLogRecord{
		Kind:      KindConsume,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Agent:     ReaderFromContext(ctx),
		Consume:   body,
	}
	l.enqueue(rec)
}

// RecordAmbient emits a KindAmbient record attesting that a bounded
// set of activities was surfaced to the LLM on a tool result's
// ambient tail. When rateLimited is true, the helper throttled the
// envelope and nothing was actually attached — we still record the
// would-have-fired intent so analysis can see the throttle shape.
func (l *FabricLogger) RecordAmbient(
	ctx context.Context,
	body *AmbientBody,
) {
	if l == nil || body == nil {
		return
	}
	rec := FabricLogRecord{
		Kind:      KindAmbient,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Agent:     ReaderFromContext(ctx),
		Ambient:   body,
	}
	l.enqueue(rec)
}

// recordResolveLocked is the internal helper invoked from
// RecordPublish when an activity carries a non-nil Resolves pointer.
func (l *FabricLogger) recordResolveLocked(resolver activity.AgentActivity, resolvedID activity.ActivityID) {
	body := &ResolveBody{
		ResolverActivityID: string(resolver.ID),
		ResolverKind:       string(resolver.Action),
		ResolvedActivityID: string(resolvedID),
	}
	if meta, ok := l.lookupRecent(string(resolvedID)); ok {
		body.ResolvedKind = meta.ActionKind
		if !meta.Timestamp.IsZero() && !resolver.Timestamp.IsZero() {
			body.LatencyMicros = resolver.Timestamp.Sub(meta.Timestamp).Microseconds()
		}
	}
	rec := FabricLogRecord{
		Kind:      KindResolve,
		SessionID: string(resolver.SessionID),
		Seq:       l.nextSeq(),
		Timestamp: resolver.Timestamp,
		MonoNano:  monoDelta(l.mono0),
		Agent: AgentRef{
			AgentID:    resolver.Actor.AgentID,
			AgentType:  resolver.Actor.AgentType,
			PipelineID: resolver.Actor.PipelineID,
		},
		Resolve: body,
	}
	l.enqueue(rec)
}

// ─── Internals ─────────────────────────────────────────────────────

func (l *FabricLogger) nextSeq() int64 {
	return l.seq.Add(1)
}

// enqueue attempts a non-blocking send; on overflow, increments
// drops and periodically emits a KindDrop. The periodic emission is
// itself non-blocking so a fully-backed-up channel cannot recurse
// into another drop.
func (l *FabricLogger) enqueue(r FabricLogRecord) {
	if l.closed.Load() {
		l.drops.Add(1)
		return
	}
	select {
	case l.ch <- r:
		l.emitted.Add(1)
	default:
		l.drops.Add(1)
	}
}

// drain is the single consumer of the write channel. It terminates
// when the channel is closed by Close. After each successful write,
// it fans out to any registered subscribers in registration order
// so bridges (forest, telemetry, etc.) can react to records as they
// land on disk.
func (l *FabricLogger) drain() {
	defer close(l.done)
	for r := range l.ch {
		if l.writeRecord(r) {
			l.flushPendingDrop("buffer_full", false)
		}
	}
	// Final flush on shutdown.
	l.flushPendingDrop("buffer_full", true)
	_ = l.writer.Flush()
}

func (l *FabricLogger) writeRecord(r FabricLogRecord) bool {
	if err := l.writer.Write(r); err != nil {
		l.writeErrs.Add(1)
		return false
	}
	l.written.Add(1)
	l.fanoutLocked(r)
	return true
}

func (l *FabricLogger) flushPendingDrop(reason string, force bool) {
	for {
		dropped := l.drops.Load()
		reported := l.dropMarks.Load()
		if dropped <= reported {
			return
		}
		if !force && dropped-reported < l.dropReportEvery {
			return
		}
		if !l.dropMarks.CompareAndSwap(reported, dropped) {
			continue
		}
		l.writeRecord(FabricLogRecord{
			Kind:      KindDrop,
			SessionID: l.sessionID,
			Seq:       l.nextSeq(),
			Timestamp: time.Now(),
			MonoNano:  monoDelta(l.mono0),
			Drop: &DropBody{
				Reason:       reason,
				DroppedCount: dropped,
			},
		})
		return
	}
}

// fanoutLocked dispatches r to every registered subscriber. Runs on
// the drain goroutine; subscribers must keep per-record work bounded.
// A panicking subscriber is recovered so it cannot take down the
// drain loop.
func (l *FabricLogger) fanoutLocked(r FabricLogRecord) {
	l.subsMu.Lock()
	subs := make([]Subscriber, len(l.subscribers))
	copy(subs, l.subscribers)
	l.subsMu.Unlock()
	for _, s := range subs {
		func() {
			defer func() {
				_ = recover() // subscriber panics do not break the drain
			}()
			s.OnRecord(r)
		}()
	}
}

// Subscribe registers a subscriber that receives every record after
// it is written. Call once at orchestrator startup; the registration
// is permanent for the logger's lifetime. Safe to call concurrently
// with writes — new subscribers start receiving on the next record.
func (l *FabricLogger) Subscribe(s Subscriber) {
	if l == nil || s == nil {
		return
	}
	l.subsMu.Lock()
	l.subscribers = append(l.subscribers, s)
	l.subsMu.Unlock()
}

// LookupRecent returns the full AgentActivity for the given ID if
// it is still present in the recent-publish cache, and a boolean
// indicating presence. Subscribers use this to resolve consume and
// resolve records back to the original publisher's activity without
// round-tripping through the SQLite store.
func (l *FabricLogger) LookupRecent(activityID string) (activity.AgentActivity, bool) {
	if l == nil {
		return activity.AgentActivity{}, false
	}
	meta, ok := l.lookupRecent(activityID)
	if !ok {
		return activity.AgentActivity{}, false
	}
	return meta.FullActivity, true
}

// cacheRecent inserts (or refreshes) an entry in the recent-publish
// cache. Evicts FIFO when the cap is reached. The cache is
// bounded and best-effort — a cache miss for a consume/resolve is
// acceptable (the record still records the edge, just without
// publisher metadata).
func (l *FabricLogger) cacheRecent(id string, meta publishMeta) {
	if id == "" || l.recentCap <= 0 {
		return
	}
	l.recentMu.Lock()
	defer l.recentMu.Unlock()
	if _, exists := l.recentByID[id]; exists {
		l.recentByID[id] = meta
		return
	}
	l.recentByID[id] = meta
	l.recentOrder = append(l.recentOrder, id)
	for len(l.recentOrder) > l.recentCap {
		evict := l.recentOrder[0]
		l.recentOrder = l.recentOrder[1:]
		delete(l.recentByID, evict)
	}
}

func (l *FabricLogger) lookupRecent(id string) (publishMeta, bool) {
	if id == "" {
		return publishMeta{}, false
	}
	l.recentMu.Lock()
	defer l.recentMu.Unlock()
	m, ok := l.recentByID[id]
	return m, ok
}

// monoDelta returns the monotonic nanoseconds since start. Used as
// an ordering field immune to wall-clock adjustments.
func monoDelta(start time.Time) int64 {
	// time.Since uses monotonic clock when available.
	return int64(time.Since(start))
}

// HashFilter computes a stable 8-hex-digit fingerprint over a
// FilterBody's semantic content. Exposed because the recording
// source constructs FilterBody values and embeds the hash so
// analysis tools can group "same filter, different session".
func HashFilter(f FilterBody) string {
	// Normalize slice ordering for a stable hash regardless of input
	// ordering.
	kinds := append([]string(nil), f.ActionKinds...)
	sort.Strings(kinds)
	states := append([]string(nil), f.States...)
	sort.Strings(states)
	h := sha1.New()
	fmt.Fprintln(h, kinds)
	fmt.Fprintln(h, states)
	fmt.Fprintln(h, f.ActorAgentID, f.ActorAgentType, f.ActorPipelineID)
	fmt.Fprintln(h, f.SubjectPathPrefix, f.SubjectTargetAgent, f.SubjectDomain)
	fmt.Fprintln(h, f.Since.UnixNano(), f.Until.UnixNano())
	fmt.Fprintln(h, f.Caused, f.Resolves, f.Limit)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:4])
}

// ─── Default singleton ─────────────────────────────────────────────

var defaultLogger atomic.Pointer[FabricLogger]

// SetDefault installs the process-wide FabricLogger and returns the
// previous installation so callers can defer-restore. Production
// wiring calls this once at session bind; tests may swap temporarily.
// Safe under concurrent readers — Default uses an atomic load.
func SetDefault(l *FabricLogger) *FabricLogger {
	return defaultLogger.Swap(l)
}

// Default returns the currently-installed FabricLogger. Returns nil
// when no logger has been installed; every method on a nil
// FabricLogger is a no-op so callers can invoke them
// unconditionally.
func Default() *FabricLogger {
	return defaultLogger.Load()
}

// CallerPackage returns a short name for the package that called
// into the logger. Used when constructing ReadBody.Lens for calls
// that don't supply an explicit name. The runtime lookup is about
// 500ns, acceptable once per lens call (which is rare relative to
// publishes).
func CallerPackage(skipFrames int) string {
	pcs := make([]uintptr, 1)
	n := runtime.Callers(skipFrames+2, pcs)
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs)
	f, _ := frames.Next()
	name := f.Function
	// Trim to just the last dotted segment for brevity.
	if i := lastDot(name); i >= 0 {
		return name[i+1:]
	}
	return name
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
