package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
	"github.com/adalundhe/sylk/core/pipeline/variants"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	pipelineBridgeName     = "bridge.pipeline"
	pipelineEventBuffer    = 64
	variantEventBuffer     = 64
	pipelineDrainTimeout   = 0 // Zero uses scope's max lifetime.
)

// PipelineBridge converts core pipeline and variant events into Bubble Tea
// messages. It follows the same drain-loop pattern as ActivityBridge.
type PipelineBridge struct {
	id              string
	scope           *concurrency.GoroutineScope
	variantRegistry variants.Registry
	pipelineCh      chan tdd.PipelineEvent
	variantCh       chan variants.VariantEvent
	dropped         atomic.Int64
	done            chan struct{}
	stopOnce        sync.Once
	unsubVariant    func() // returned by Registry.Subscribe
}

// NewPipelineBridge creates a bridge that converts pipeline/variant events
// into Bubble Tea messages. Call Start to begin draining.
func NewPipelineBridge(
	id string,
	variantRegistry variants.Registry,
	scope *concurrency.GoroutineScope,
) *PipelineBridge {
	return &PipelineBridge{
		id:              id,
		scope:           scope,
		variantRegistry: variantRegistry,
		pipelineCh:      make(chan tdd.PipelineEvent, pipelineEventBuffer),
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

// -- Bridge implementation --

// Start subscribes to the variant registry and launches the drain goroutine.
func (b *PipelineBridge) Start(program TeaProgram) error {
	if b.variantRegistry != nil {
		b.unsubVariant = b.variantRegistry.Subscribe(b.onVariantEvent)
	}
	return b.scope.Go(pipelineBridgeName, pipelineDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the variant registry and signals the drain to exit.
func (b *PipelineBridge) Stop() {
	b.stopOnce.Do(func() {
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

// toPipelineStateMsg converts a core PipelineEvent to a UI message.
func toPipelineStateMsg(evt tdd.PipelineEvent) msg.PipelineStateMsg {
	return msg.PipelineStateMsg{
		PipelineID: evt.PipelineID,
		TaskID:     evt.TaskID,
		Status:     string(evt.NewStatus),
		LoopCount:  evt.LoopCount,
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
