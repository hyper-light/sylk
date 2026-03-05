// Package knowledge provides the KnowledgeStore which manages progressive
// knowledge backend lifecycle. Searcher backends (Bleve, vector, graph) are
// atomically set on a single HybridQueryCoordinator as they come online.
package knowledge

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
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

// BackgroundIndexWaiter exposes a Ready channel for waiting on background
// indexing completion. Satisfied by *sylkdir.BackgroundIndexer.
type BackgroundIndexWaiter interface {
	Ready() <-chan struct{}
}

// KnowledgeStore owns the single HybridQueryCoordinator and the underlying
// storage resources. Agents receive the coordinator at construction time;
// backends are atomically set as they come online.
type KnowledgeStore struct {
	coordinator *query.HybridQueryCoordinator // single instance, lives forever
	level       atomic.Int32                  // ReadinessLevel

	partialReady chan struct{} // closed at ReadinessPartial
	fullReady    chan struct{} // closed at ReadinessFull

	mu        sync.Mutex // guards resource ownership for cleanup
	bgWaiter  BackgroundIndexWaiter
	closeable io.Closer // bleve store closer, set by caller

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

// PromotePartial atomically sets the bleve searcher on the coordinator and
// transitions to ReadinessPartial. The caller builds the searcher and adapter
// (in cmd/tui.go) to avoid import cycles.
func (ks *KnowledgeStore) PromotePartial(searcher *query.BleveSearcher, bgWaiter BackgroundIndexWaiter, closer io.Closer) {
	ks.mu.Lock()
	ks.bgWaiter = bgWaiter
	ks.closeable = closer
	ks.mu.Unlock()

	ks.coordinator.SetBleveSearcher(searcher)

	ks.level.Store(int32(ReadinessPartial))
	close(ks.partialReady)

	ks.publishEvent(ReadinessPartial)
	ks.logKnowledge(agentlog.EventKnowledgePromotePartial, "info", &agentlog.BootPhasePayload{
		Phase: "promote_partial",
	})
	ks.logger.Info("knowledge promoted to partial",
		"searchers", ks.coordinator.ReadySearchers())
}

// PromoteFull transitions to ReadinessFull (all docs indexed).
func (ks *KnowledgeStore) PromoteFull() {
	ks.level.Store(int32(ReadinessFull))

	select {
	case <-ks.fullReady:
	default:
		close(ks.fullReady)
	}

	ks.publishEvent(ReadinessFull)
	ks.logKnowledge(agentlog.EventKnowledgePromoteFull, "info", &agentlog.BootPhasePayload{
		Phase: "promote_full",
	})
	ks.logger.Info("knowledge promoted to full",
		"searchers", ks.coordinator.ReadySearchers())
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

// Close releases all owned resources. Idempotent.
func (ks *KnowledgeStore) Close() error {
	var err error
	ks.closeOnce.Do(func() {
		ks.coordinator.Close()

		ks.mu.Lock()
		defer ks.mu.Unlock()

		if ks.closeable != nil {
			if e := ks.closeable.Close(); e != nil {
				err = e
			}
		}

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
	return err
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
