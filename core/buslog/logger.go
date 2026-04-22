package buslog

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

// BusLogger owns the session-global bus observability stream. One
// logger per session. Methods are safe for concurrent callers.
//
// The logger holds a single agentlog.StreamWriter that rotates daily
// and at 64 MiB per file. Writes flow through a bounded async
// channel to a single drain goroutine so bus publish/delivery hot
// paths are never blocked on disk I/O.
type BusLogger struct {
	writer *agentlog.StreamWriter

	sessionID string

	seq   atomic.Int64
	mono0 time.Time

	ch   chan BusLogRecord
	done chan struct{}

	drops     atomic.Int64
	closed    atomic.Bool
	emitted   atomic.Int64
	written   atomic.Int64
	writeErrs atomic.Int64

	subsMu      sync.Mutex
	subscribers []Subscriber

	dropReportEvery int64
}

// Config controls writer rotation, redaction, and buffer depth.
type Config struct {
	// BaseDir is the per-session directory the logger anchors
	// under. The StreamWriter creates {BaseDir}/{YYYY-MM-DD}/bus.{nnn}.jsonl.
	BaseDir string

	// SessionID identifies the session. Embedded in every record.
	SessionID string

	// StreamWriterConfig tunes rotation. Zero falls back to defaults
	// (64 MiB, local TZ, 2s sync).
	StreamWriterConfig agentlog.StreamWriterConfig

	// Redactor applied to every record. Nil is a no-op.
	Redactor agentlog.Redactor

	// BufferSize bounds the async write channel. Zero falls back to
	// defaultBufferSize.
	BufferSize int

	// DropReportEvery controls how often KindDrop records are
	// emitted under sustained pressure. Zero falls back to default.
	DropReportEvery int64
}

const (
	defaultBufferSize      = 8192
	defaultDropReportEvery = 1024
)

// Subscriber is invoked once per record as it flows through the
// logger. Runs on the drain goroutine — must not block or call back
// into BusLogger methods that enqueue.
type Subscriber interface {
	OnRecord(record BusLogRecord)
}

// SubscriberFunc adapts a function to Subscriber.
type SubscriberFunc func(record BusLogRecord)

// OnRecord satisfies Subscriber.
func (f SubscriberFunc) OnRecord(record BusLogRecord) {
	if f != nil {
		f(record)
	}
}

// New constructs a logger at cfg.BaseDir/bus. Writer opens lazily on
// first write; directory creation failures surface on emit, not
// here, matching agentlog.StreamWriter's contract.
func New(cfg Config) (*BusLogger, error) {
	if cfg.BaseDir == "" {
		return nil, fmt.Errorf("buslog: BaseDir required")
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = defaultBufferSize
	}
	if cfg.DropReportEvery <= 0 {
		cfg.DropReportEvery = defaultDropReportEvery
	}

	w, err := agentlog.NewStreamWriter(cfg.BaseDir, "bus", cfg.StreamWriterConfig, cfg.Redactor)
	if err != nil {
		return nil, fmt.Errorf("buslog: new stream writer: %w", err)
	}

	l := &BusLogger{
		writer:          w,
		sessionID:       cfg.SessionID,
		mono0:           time.Now(),
		ch:              make(chan BusLogRecord, cfg.BufferSize),
		done:            make(chan struct{}),
		dropReportEvery: cfg.DropReportEvery,
	}
	go l.drain()
	return l, nil
}

// Close drains the buffer, flushes the writer, and releases
// resources. Idempotent.
func (l *BusLogger) Close() error {
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

// Stats returns a snapshot of counters for diagnostics.
type Stats struct {
	Emitted     int64 `json:"emitted"`
	Written     int64 `json:"written"`
	Drops       int64 `json:"drops"`
	WriteErrors int64 `json:"write_errors"`
}

// Stats returns the current counter snapshot.
func (l *BusLogger) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	return Stats{
		Emitted:     l.emitted.Load(),
		Written:     l.written.Load(),
		Drops:       l.drops.Load(),
		WriteErrors: l.writeErrs.Load(),
	}
}

// Subscribe registers a listener that receives every record after
// it lands on disk. Listeners run in registration order on the
// drain goroutine.
func (l *BusLogger) Subscribe(s Subscriber) {
	if l == nil || s == nil {
		return
	}
	l.subsMu.Lock()
	l.subscribers = append(l.subscribers, s)
	l.subsMu.Unlock()
}

// ─── Emit API (called by ChannelBus hooks) ──────────────────────────

// Publish records a KindPublish event. err is the result of the
// underlying bus publish — nil on success, the error on failure.
func (l *BusLogger) Publish(topic string, msg MessageRef, subscriberCount int, payloadBytes int, err error) {
	if l == nil {
		return
	}
	body := &PublishBody{
		Topic:           topic,
		Message:         msg,
		SubscriberCount: subscriberCount,
		PayloadBytes:    payloadBytes,
	}
	if err != nil {
		body.Err = err.Error()
	}
	l.enqueue(BusLogRecord{
		Kind:      KindPublish,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Publish:   body,
	})
}

// Delivery records a KindDelivery event per subscriber delivery.
// QueueLatencyMicros is the elapsed time between enqueue and
// handler invocation (or 0 for synchronous deliveries).
func (l *BusLogger) Delivery(
	publishTopic, subscriberTopic string,
	subscriberID int64,
	subscriberAgent string,
	async bool,
	queueLatencyMicros int64,
	msg MessageRef,
	handlerErr error,
) {
	if l == nil {
		return
	}
	body := &DeliveryBody{
		PublishTopic:       publishTopic,
		SubscriberTopic:    subscriberTopic,
		SubscriberID:       subscriberID,
		SubscriberAgent:    subscriberAgent,
		Async:              async,
		QueueLatencyMicros: queueLatencyMicros,
		Message:            msg,
	}
	if handlerErr != nil {
		body.HandlerErr = handlerErr.Error()
	}
	l.enqueue(BusLogRecord{
		Kind:      KindDelivery,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Delivery:  body,
	})
}

// Subscribe records a KindSubscribe event on subscription registration.
func (l *BusLogger) Subscribed(topic string, subscriberID int64, async, wildcard bool, matchPattern, subscriberAgent string) {
	if l == nil {
		return
	}
	l.enqueue(BusLogRecord{
		Kind:      KindSubscribe,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Subscribe: &SubscribeBody{
			Topic:           topic,
			SubscriberID:    subscriberID,
			Async:           async,
			Wildcard:        wildcard,
			MatchPattern:    matchPattern,
			SubscriberAgent: subscriberAgent,
		},
	})
}

// Unsubscribed records a KindUnsubscribe event on subscription removal.
func (l *BusLogger) Unsubscribed(topic string, subscriberID int64, duration time.Duration, subscriberAgent string) {
	if l == nil {
		return
	}
	l.enqueue(BusLogRecord{
		Kind:      KindUnsubscribe,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Unsubscribe: &UnsubscribeBody{
			Topic:           topic,
			SubscriberID:    subscriberID,
			Duration:        duration,
			SubscriberAgent: subscriberAgent,
		},
	})
}

// Overflow records a subscription queue overflow.
func (l *BusLogger) Overflow(topic string, dropped int, totalDropped uint64, correlations []string, queueRemaining int) {
	if l == nil {
		return
	}
	l.enqueue(BusLogRecord{
		Kind:      KindOverflow,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Overflow: &OverflowBody{
			Topic:          topic,
			Dropped:        dropped,
			TotalDropped:   totalDropped,
			Correlations:   correlations,
			QueueRemaining: queueRemaining,
		},
	})
}

// Expired records a Publish rejected for TTL/Deadline expiry.
func (l *BusLogger) Expired(topic string, msg MessageRef, expiredAt time.Time) {
	if l == nil {
		return
	}
	l.enqueue(BusLogRecord{
		Kind:      KindExpired,
		SessionID: l.sessionID,
		Seq:       l.nextSeq(),
		Timestamp: time.Now(),
		MonoNano:  monoDelta(l.mono0),
		Expired: &ExpiredBody{
			Topic:     topic,
			Message:   msg,
			ExpiredAt: expiredAt,
		},
	})
}

// ─── Internals ──────────────────────────────────────────────────────

func (l *BusLogger) nextSeq() int64 {
	return l.seq.Add(1)
}

// enqueue is non-blocking. Overflow increments drops; a periodic
// KindDrop record is emitted so lost observability is visible.
func (l *BusLogger) enqueue(r BusLogRecord) {
	if l.closed.Load() {
		l.drops.Add(1)
		return
	}
	select {
	case l.ch <- r:
		l.emitted.Add(1)
	default:
		dropped := l.drops.Add(1)
		if l.dropReportEvery > 0 && dropped%l.dropReportEvery == 0 {
			drop := BusLogRecord{
				Kind:      KindDrop,
				SessionID: l.sessionID,
				Seq:       l.nextSeq(),
				Timestamp: time.Now(),
				MonoNano:  monoDelta(l.mono0),
				Drop: &DropBody{
					Reason:       "buffer_full",
					DroppedCount: dropped,
				},
			}
			select {
			case l.ch <- drop:
				l.emitted.Add(1)
			default:
				// drop-the-drop
			}
		}
	}
}

// drain is the single consumer of the write channel. Fans out to
// subscribers after each successful write.
func (l *BusLogger) drain() {
	defer close(l.done)
	for r := range l.ch {
		if err := l.writer.Write(r); err != nil {
			l.writeErrs.Add(1)
			continue
		}
		l.written.Add(1)
		l.fanout(r)
	}
	_ = l.writer.Flush()
}

func (l *BusLogger) fanout(r BusLogRecord) {
	l.subsMu.Lock()
	subs := make([]Subscriber, len(l.subscribers))
	copy(subs, l.subscribers)
	l.subsMu.Unlock()
	for _, s := range subs {
		func() {
			defer func() { _ = recover() }()
			s.OnRecord(r)
		}()
	}
}

func monoDelta(start time.Time) int64 {
	return int64(time.Since(start))
}

// ─── Default singleton ──────────────────────────────────────────────

var defaultLogger atomic.Pointer[BusLogger]

// SetDefault installs the process-wide logger and returns the
// previous installation.
func SetDefault(l *BusLogger) *BusLogger {
	return defaultLogger.Swap(l)
}

// Default returns the currently-installed logger or nil.
func Default() *BusLogger {
	return defaultLogger.Load()
}
