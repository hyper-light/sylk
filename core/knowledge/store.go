// Package knowledge provides the KnowledgeStore which manages progressive
// knowledge backend lifecycle. Searcher backends (Bleve, vector, graph) are
// atomically set on a single HybridQueryCoordinator as they come online.
package knowledge

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/diagnostics"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

// ReadinessLevel indicates how much of the knowledge layer is available.
type ReadinessLevel int32

const (
	// ReadinessNone means no searchers are set on the coordinator.
	ReadinessNone ReadinessLevel = 0

	// ReadinessPartial means Bleve is available (and optionally graph+vector).
	ReadinessPartial ReadinessLevel = 1

	// ReadinessFull means BackgroundIndexer has completed all docs.
	ReadinessFull ReadinessLevel = 2
)

// ReadinessEvent is published when a knowledge backend layer is promoted.
type ReadinessEvent struct {
	Level     ReadinessLevel
	Searchers []string
}

// ReadinessPublisher receives knowledge readiness notifications.
// Implemented by the bus adapter in cmd/tui.go.
type ReadinessPublisher interface {
	PublishKnowledgeReady(event ReadinessEvent)
}

// BackgroundIndexWaiter exposes a Ready channel, progress counters, and an
// observer hook for background indexing. Satisfied by *sylkdir.BackgroundIndexer.
type BackgroundIndexWaiter interface {
	Ready() <-chan struct{}
	Progress() (indexed, total int64)
	OnProgress(fn func(indexed, total int64))
}

// ProgressObserver receives progress events from the knowledge pipeline and
// background indexer. Set via SetProgressObserver; called from producer goroutines.
type ProgressObserver func(phase string, current, total int64)

// LifecycleEventKind identifies a knowledge lifecycle transition emitted to observers.
type LifecycleEventKind int

const (
	// LifecycleEventPartial is emitted whenever PromotePartial updates the active
	// searcher/waiter pair, even if the store was already partially or fully ready.
	LifecycleEventPartial LifecycleEventKind = iota + 1
	// LifecycleEventFull is emitted whenever the store reaches full readiness.
	LifecycleEventFull
)

// LifecycleEvent reports a partial/full promotion together with the active
// background waiter for that promotion cycle.
type LifecycleEvent struct {
	Kind   LifecycleEventKind
	Level  ReadinessLevel
	Waiter BackgroundIndexWaiter
}

// LifecycleObserver receives repeatable promotion-cycle updates.
type LifecycleObserver func(event LifecycleEvent)

// KnowledgeStore owns the single HybridQueryCoordinator and the underlying
// storage resources. Agents receive the coordinator at construction time;
// backends are atomically set as they come online.
type KnowledgeStore struct {
	coordinator *query.HybridQueryCoordinator // single instance, lives forever
	level       atomic.Int32                  // ReadinessLevel

	partialReady chan struct{} // closed at ReadinessPartial
	fullReady    chan struct{} // closed at ReadinessFull
	partialOnce  sync.Once
	fullOnce     sync.Once

	mu          sync.Mutex // guards resource ownership for cleanup
	bgWaiter    BackgroundIndexWaiter
	closeable   io.Closer        // bleve store closer, set by caller
	progressFn  ProgressObserver // UI callback, guarded by mu
	lifecycleFn LifecycleObserver

	publisher  ReadinessPublisher
	logger     *slog.Logger
	bootLogger *agentlog.BootEventLogger // nil-safe structured logger
	closeOnce  sync.Once
}

// NewKnowledgeStore creates a store with an empty coordinator (nil searchers).
// Agents can use the coordinator immediately — queries degrade gracefully.
func NewKnowledgeStore(publisher ReadinessPublisher, logger *slog.Logger) *KnowledgeStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &KnowledgeStore{
		coordinator:  query.NewHybridQueryCoordinator(nil, nil, nil),
		partialReady: make(chan struct{}),
		fullReady:    make(chan struct{}),
		publisher:    publisher,
		logger:       logger,
	}
}

// Coordinator returns the single coordinator instance. Never nil after construction.
func (ks *KnowledgeStore) Coordinator() *query.HybridQueryCoordinator {
	return ks.coordinator
}

// SetBootLogger sets the structured event logger for knowledge lifecycle events.
func (ks *KnowledgeStore) SetBootLogger(l *agentlog.BootEventLogger) {
	ks.mu.Lock()
	ks.bootLogger = l
	ks.mu.Unlock()
}

// logKnowledge emits a structured knowledge event. No-op if bootLogger is nil.
func (ks *KnowledgeStore) logKnowledge(eventType agentlog.EventType, level string, data any) {
	ks.mu.Lock()
	l := ks.bootLogger
	ks.mu.Unlock()
	if l == nil {
		return
	}
	l.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     "boot",
		Event:     eventType.String(),
		EventCode: eventType,
		Data:      data,
	})
}

// Level returns the current readiness level.
func (ks *KnowledgeStore) Level() ReadinessLevel {
	return ReadinessLevel(ks.level.Load())
}

// BackgroundWaiter returns the waiter for background index completion, or nil.
func (ks *KnowledgeStore) BackgroundWaiter() BackgroundIndexWaiter {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.bgWaiter
}

// SetProgressObserver registers a callback for future pipeline and indexer
// progress. Safe to call concurrently; replaces any previously registered
// observer.
func (ks *KnowledgeStore) SetProgressObserver(fn ProgressObserver) {
	ks.mu.Lock()
	ks.progressFn = fn
	ks.mu.Unlock()
	diagnostics.LogStartup("knowledge_store_progress_observer_set", "observer", fn != nil)
}

// SetLifecycleObserver registers a callback for repeatable partial/full
// promotion events. The observer receives an immediate snapshot of the current
// active waiter or full readiness when available.
func (ks *KnowledgeStore) SetLifecycleObserver(fn LifecycleObserver) {
	ks.mu.Lock()
	ks.lifecycleFn = fn
	level := ReadinessLevel(ks.level.Load())
	bgWaiter := ks.bgWaiter
	ks.mu.Unlock()
	diagnostics.LogStartup("knowledge_store_lifecycle_observer_set", "observer", fn != nil, "level", int(level), "waiter", bgWaiter != nil)

	if fn == nil {
		return
	}
	switch {
	case bgWaiter != nil || level == ReadinessPartial:
		fn(LifecycleEvent{Kind: LifecycleEventPartial, Level: level, Waiter: bgWaiter})
	case level >= ReadinessFull:
		fn(LifecycleEvent{Kind: LifecycleEventFull, Level: level})
	}
}

// NotifyProgress stores and forwards a progress event to the registered
// observer. Safe as a PipelineConfig.OnProgress callback.
func (ks *KnowledgeStore) NotifyProgress(phase string, current, total int64) {
	ks.mu.Lock()
	fn := ks.progressFn
	ks.mu.Unlock()
	diagnostics.LogStartup("knowledge_store_notify_progress", "phase", phase, "current", current, "total", total, "observer", fn != nil)
	if fn != nil {
		fn(phase, current, total)
	}
}

// PromotePartial atomically sets the bleve searcher on the coordinator and
// transitions to ReadinessPartial. The caller builds the searcher and adapter
// (in cmd/tui.go) to avoid import cycles.
func (ks *KnowledgeStore) PromotePartial(searcher *query.BleveSearcher, bgWaiter BackgroundIndexWaiter, closer io.Closer) {
	ks.mu.Lock()
	ks.bgWaiter = bgWaiter
	if closer != nil {
		ks.closeable = closer
	}
	lifecycleFn := ks.lifecycleFn
	ks.mu.Unlock()
	diagnostics.LogStartup("knowledge_store_promote_partial_start", "waiter", bgWaiter != nil, "lifecycle_observer", lifecycleFn != nil)

	ks.coordinator.SetBleveSearcher(searcher)

	if lifecycleFn != nil {
		level := ks.Level()
		if level < ReadinessPartial {
			level = ReadinessPartial
		}
		lifecycleFn(LifecycleEvent{
			Kind:   LifecycleEventPartial,
			Level:  level,
			Waiter: bgWaiter,
		})
	}

	if ks.Level() >= ReadinessPartial {
		diagnostics.LogStartup("knowledge_store_promote_partial_already_ready", "level", int(ks.Level()))
		return
	}

	ks.level.Store(int32(ReadinessPartial))
	ks.partialOnce.Do(func() { close(ks.partialReady) })
	ks.publishEvent(ReadinessPartial)
	ks.logKnowledge(agentlog.EventKnowledgePromotePartial, "info", &agentlog.BootPhasePayload{
		Phase: "promote_partial",
	})
	ks.logger.Info("knowledge promoted to partial",
		"searchers", ks.coordinator.ReadySearchers())
	diagnostics.LogStartup("knowledge_store_promote_partial_done", "searchers", strings.Join(ks.coordinator.ReadySearchers(), ","))
}

// PromoteFull transitions to ReadinessFull (all docs indexed).
func (ks *KnowledgeStore) PromoteFull() {
	ks.mu.Lock()
	ks.bgWaiter = nil
	lifecycleFn := ks.lifecycleFn
	ks.mu.Unlock()
	diagnostics.LogStartup("knowledge_store_promote_full_start", "lifecycle_observer", lifecycleFn != nil)

	if lifecycleFn != nil {
		lifecycleFn(LifecycleEvent{
			Kind:  LifecycleEventFull,
			Level: ReadinessFull,
		})
	}

	if ks.Level() >= ReadinessFull {
		diagnostics.LogStartup("knowledge_store_promote_full_already_ready", "level", int(ks.Level()))
		return
	}

	ks.level.Store(int32(ReadinessFull))

	ks.fullOnce.Do(func() { close(ks.fullReady) })

	ks.publishEvent(ReadinessFull)
	ks.logKnowledge(agentlog.EventKnowledgePromoteFull, "info", &agentlog.BootPhasePayload{
		Phase: "promote_full",
	})
	ks.logger.Info("knowledge promoted to full",
		"searchers", ks.coordinator.ReadySearchers())
	diagnostics.LogStartup("knowledge_store_promote_full_done", "searchers", strings.Join(ks.coordinator.ReadySearchers(), ","))
}

// WaitForPartial blocks until ReadinessPartial is reached or ctx is cancelled.
func (ks *KnowledgeStore) WaitForPartial(ctx context.Context) error {
	select {
	case <-ks.partialReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForFull blocks until ReadinessFull is reached or ctx is cancelled.
func (ks *KnowledgeStore) WaitForFull(ctx context.Context) error {
	select {
	case <-ks.fullReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close releases the store's own resources (the query coordinator's
// metrics tracker). Idempotent.
//
// Note: the closeable registered by PromotePartial — typically the
// CommittedKnowledgeBackend — is intentionally NOT closed here. The
// caller is responsible for closing the backend directly so a single
// resource has a single close path; otherwise both Store.Close and
// Backend.Close would race through the backend's sync.Once and each
// report their own timeout for the same underlying work.
func (ks *KnowledgeStore) Close() error {
	ks.closeOnce.Do(func() {
		ks.coordinator.Close()

		if ks.bootLogger != nil {
			ks.bootLogger.LogEvent(agentlog.JSONLEntry{
				Timestamp: time.Now(),
				Level:     "info",
				Agent:     "boot",
				Event:     agentlog.EventKnowledgeClosed.String(),
				EventCode: agentlog.EventKnowledgeClosed,
			})
		}
	})
	return nil
}

func (ks *KnowledgeStore) publishEvent(level ReadinessLevel) {
	if ks.publisher == nil {
		return
	}
	ks.publisher.PublishKnowledgeReady(ReadinessEvent{
		Level:     level,
		Searchers: ks.coordinator.ReadySearchers(),
	})
}
