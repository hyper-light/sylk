package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
	"github.com/adalundhe/sylk/core/pipeline/variants"
	"github.com/adalundhe/sylk/ui/msg"
)

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
	pipelineCh      chan tdd.PipelineEvent
	taskStateCh     chan taskstate.Event
	variantCh       chan variants.VariantEvent
	dropped         atomic.Int64
	done            chan struct{}
	stopOnce        sync.Once
	unsubVariant    func() // returned by Registry.Subscribe
	taskStateSub    guide.Subscription
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
		pipelineCh:      make(chan tdd.PipelineEvent, pipelineEventBuffer),
		taskStateCh:     make(chan taskstate.Event, pipelineEventBuffer),
		variantCh:       make(chan variants.VariantEvent, variantEventBuffer),
		done:            make(chan struct{}),
	}
}

// OnPipelineEvent enqueues a pipeline status change. Non-blocking; drops on
// backpressure and increments the drop counter.
func (b *PipelineBridge) OnPipelineEvent(evt tdd.PipelineEvent) {
	select {
	case b.pipelineCh <- evt:
	default:
		b.dropped.Add(1)
	}
}

// onVariantEvent enqueues a variant lifecycle event. Non-blocking.
func (b *PipelineBridge) onVariantEvent(evt variants.VariantEvent) {
	select {
	case b.variantCh <- evt:
	default:
		b.dropped.Add(1)
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
		b.dropped.Add(1)
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
			case evt := <-b.pipelineCh:
				program.Send(toPipelineStateMsg(evt))
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
		TaskLabel:  evt.TaskLabel,
		Status:     string(evt.Status),
		WorkerType: evt.WorkerType,
		LoopCount:  evt.LoopCount,
		MaxLoops:   evt.MaxLoops,
	}
}

// toPipelineStateMsg converts a core PipelineEvent to a UI message.
func toPipelineStateMsg(evt tdd.PipelineEvent) msg.PipelineStateMsg {
	pipelineID := evt.TaskID
	if pipelineID == "" {
		pipelineID = evt.PipelineID
	}
	if pipelineID == "" {
		pipelineID = evt.RuntimePipelineID
	}
	return msg.PipelineStateMsg{
		PipelineID:        pipelineID,
		RuntimePipelineID: evt.RuntimePipelineID,
		TaskID:            firstNonEmpty(evt.TaskID, pipelineID),
		TaskLabel:         evt.TaskSlug,
		Status:            string(evt.NewStatus),
		LoopCount:         evt.LoopCount,
		MaxLoops:          evt.MaxLoops,
		WorkerType:        string(evt.WorkerType),
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
