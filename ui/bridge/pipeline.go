package bridge

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/pipeline/variants"
	"github.com/adalundhe/sylk/ui/msg"
)

// BridgeDropKind identifies the kind of event that was dropped.
type BridgeDropKind string

const (
	BridgeDropKindVariant   BridgeDropKind = "variant"
	BridgeDropKindTaskState BridgeDropKind = "taskstate"
)

// BridgeDropEvent is fired to an installed DropListener when a pipeline
// bridge fails to enqueue an event because its inbound channel is full.
// Keep listeners fast — they run in the publish path (the variant registry
// subscriber callback or the bus message handler).
type BridgeDropEvent struct {
	Kind         BridgeDropKind
	TotalDropped int64  // cumulative drops on this bridge
	PipelineID   string // populated for taskstate drops where known
	TaskID       string // populated for taskstate drops where known
	VariantID    string // populated for variant drops where known
}

// DropListener is a callback fired on every backpressure drop. A nil listener
// disables callbacks; structured logs still fire regardless.
type DropListener func(BridgeDropEvent)

const (
	pipelineBridgeName   = "bridge.pipeline"
	pipelineEventBuffer  = 64
	variantEventBuffer   = 64
	pipelineDrainTimeout = 0 // Zero uses scope's max lifetime.
)

// PipelineBridge converts core pipeline and variant events into Bubble Tea
// messages. It follows the same drain-loop pattern as ActivityBridge.
type PipelineBridge struct {
	id              string
	scope           *concurrency.GoroutineScope
	bus             guide.EventBus
	variantRegistry variants.Registry
	taskStateCh     chan taskstate.Event
	variantCh       chan variants.VariantEvent
	dropped         atomic.Int64
	dropListener    atomic.Value // of DropListener (may be nil)
	done            chan struct{}
	stopOnce        sync.Once
	unsubVariant    func() // returned by Registry.Subscribe
	taskStateSub    guide.Subscription
}

// SetDropListener installs (or replaces) the drop-observation callback.
// Pass nil to clear. The listener must be fast — it runs in the publish path.
func (b *PipelineBridge) SetDropListener(fn DropListener) {
	if fn == nil {
		b.dropListener.Store(DropListener(nil))
		return
	}
	b.dropListener.Store(fn)
}

func (b *PipelineBridge) loadDropListener() DropListener {
	v := b.dropListener.Load()
	if v == nil {
		return nil
	}
	fn, _ := v.(DropListener)
	return fn
}

// NewPipelineBridge creates a bridge that converts pipeline/variant events
// into Bubble Tea messages. Call Start to begin draining.
func NewPipelineBridge(
	id string,
	bus guide.EventBus,
	variantRegistry variants.Registry,
	scope *concurrency.GoroutineScope,
) *PipelineBridge {
	return &PipelineBridge{
		id:              id,
		bus:             bus,
		scope:           scope,
		variantRegistry: variantRegistry,
		taskStateCh:     make(chan taskstate.Event, pipelineEventBuffer),
		variantCh:       make(chan variants.VariantEvent, variantEventBuffer),
		done:            make(chan struct{}),
	}
}

// onVariantEvent enqueues a variant lifecycle event. Non-blocking.
func (b *PipelineBridge) onVariantEvent(evt variants.VariantEvent) {
	select {
	case b.variantCh <- evt:
	default:
		b.recordDrop(BridgeDropEvent{
			Kind:      BridgeDropKindVariant,
			VariantID: string(evt.VariantID),
		})
	}
}

func (b *PipelineBridge) onTaskStateMessage(m *guide.Message) error {
	if m == nil {
		return nil
	}
	switch payload := m.Payload.(type) {
	case *taskstate.Event:
		b.enqueueTaskState(*payload)
	case taskstate.Event:
		b.enqueueTaskState(payload)
	case map[string]any:
		if evt, ok := extractTaskStateEvent(payload); ok {
			b.enqueueTaskState(evt)
		}
	}
	return nil
}

func (b *PipelineBridge) enqueueTaskState(evt taskstate.Event) {
	select {
	case b.taskStateCh <- evt:
	default:
		b.recordDrop(BridgeDropEvent{
			Kind:       BridgeDropKindTaskState,
			PipelineID: evt.PipelineID,
			TaskID:     evt.TaskID,
		})
	}
}

// recordDrop logs the drop and invokes the installed listener (if any). Keeps
// the drop path cyclomatically simple and centralises observability so we
// can't regress to silent drops.
func (b *PipelineBridge) recordDrop(evt BridgeDropEvent) {
	total := b.dropped.Add(1)
	evt.TotalDropped = total
	slog.Default().Warn("pipeline bridge drop: channel full",
		"bridge_id", b.id,
		"kind", string(evt.Kind),
		"pipeline_id", evt.PipelineID,
		"task_id", evt.TaskID,
		"variant_id", evt.VariantID,
		"total_dropped", total)
	if listener := b.loadDropListener(); listener != nil {
		listener(evt)
	}
}

// -- Bridge implementation --

// Start subscribes to the variant registry and launches the drain goroutine.
func (b *PipelineBridge) Start(program TeaProgram) error {
	if b.variantRegistry != nil {
		b.unsubVariant = b.variantRegistry.Subscribe(b.onVariantEvent)
	}
	if b.bus != nil {
		sub, err := b.bus.SubscribeAsync(taskstate.Topic, b.onTaskStateMessage)
		if err != nil {
			if b.unsubVariant != nil {
				b.unsubVariant()
				b.unsubVariant = nil
			}
			return err
		}
		b.taskStateSub = sub
	}
	return b.scope.Go(pipelineBridgeName, pipelineDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the variant registry and signals the drain to exit.
func (b *PipelineBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.taskStateSub != nil {
			_ = b.taskStateSub.Unsubscribe()
		}
		if b.unsubVariant != nil {
			b.unsubVariant()
		}
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *PipelineBridge) Name() string { return pipelineBridgeName }

// DroppedCount returns events dropped due to backpressure.
func (b *PipelineBridge) DroppedCount() int64 { return b.dropped.Load() }

// drainFunc returns the WorkFunc that drains both channels and sends tea messages.
func (b *PipelineBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case evt := <-b.taskStateCh:
				program.Send(toTaskPipelineStateMsg(evt))
			case evt := <-b.variantCh:
				program.Send(b.toVariantStateMsg(evt))
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func toTaskPipelineStateMsg(evt taskstate.Event) msg.PipelineStateMsg {
	pipelineID := firstNonEmpty(evt.PipelineID, evt.TaskID)
	return msg.PipelineStateMsg{
		PipelineID: pipelineID,
		TaskID:     firstNonEmpty(evt.TaskID, pipelineID),
		TaskSlug:   evt.TaskSlug,
		TaskLabel:  evt.TaskLabel,
		Status:     string(evt.Status),
		WorkerType: evt.WorkerType,
		LoopCount:  evt.LoopCount,
		MaxLoops:   evt.MaxLoops,
		Timestamp:  evt.Timestamp,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func extractTaskStateEvent(data map[string]any) (taskstate.Event, bool) {
	var evt taskstate.Event
	evt.PipelineID, _ = data["pipeline_id"].(string)
	evt.TaskID, _ = data["task_id"].(string)
	evt.TaskSlug, _ = data["task_slug"].(string)
	evt.TaskLabel, _ = data["task_label"].(string)
	if status, _ := data["status"].(string); status != "" {
		evt.Status = taskstate.Status(status)
	}
	evt.WorkerType, _ = data["worker_type"].(string)
	if loopCount, ok := pipelineIntFromAny(data["loop_count"]); ok {
		evt.LoopCount = loopCount
	}
	if maxLoops, ok := pipelineIntFromAny(data["max_loops"]); ok {
		evt.MaxLoops = maxLoops
	}
	return evt, evt.Status != ""
}

func pipelineIntFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

// toVariantStateMsg converts a core VariantEvent to a UI message.
// It looks up VariantInfo from the registry to populate PipelineID and Name.
func (b *PipelineBridge) toVariantStateMsg(evt variants.VariantEvent) msg.VariantStateMsg {
	m := msg.VariantStateMsg{
		VariantID: string(evt.VariantID),
		State:     evt.NewState.String(),
	}
	if b.variantRegistry != nil {
		if info, err := b.variantRegistry.GetInfo(evt.VariantID); err == nil && info != nil {
			m.PipelineID = info.BasePipelineID
			m.Name = info.Name
		}
	}
	return m
}
