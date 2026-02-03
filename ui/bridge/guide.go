package bridge

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	guideBridgeName   = "bridge.guide"
	guideBufferSize   = 256
	guideDrainTimeout = 30 * time.Second
)

// GuideBridge subscribes to the Guide EventBus for RouteResponse messages
// and forwards them as msg.GuideResponseMsg to the Bubble Tea program.
type GuideBridge struct {
	bus          guide.EventBus
	scope        *concurrency.GoroutineScope
	buffer       chan *guide.RouteResponse
	dropped      atomic.Int64
	done         chan struct{}
	subscription guide.Subscription
}

// NewGuideBridge creates a bridge that converts Guide bus response messages
// into Bubble Tea messages.
func NewGuideBridge(bus guide.EventBus, scope *concurrency.GoroutineScope) *GuideBridge {
	return &GuideBridge{
		bus:    bus,
		scope:  scope,
		buffer: make(chan *guide.RouteResponse, guideBufferSize),
		done:   make(chan struct{}),
	}
}

// -- Bridge implementation --

// Start subscribes to the Guide response topic and launches the drain goroutine.
func (b *GuideBridge) Start(program TeaProgram) error {
	sub, err := b.bus.Subscribe(guide.TopicGuideResponses, b.onMessage)
	if err != nil {
		return err
	}
	b.subscription = sub
	return b.scope.Go(guideBridgeName, guideDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *GuideBridge) Stop() {
	if b.subscription != nil {
		_ = b.subscription.Unsubscribe()
	}
	close(b.done)
}

// Name returns the bridge identifier.
func (b *GuideBridge) Name() string { return guideBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *GuideBridge) DroppedCount() int64 { return b.dropped.Load() }

// onMessage is the guide.MessageHandler called by the EventBus.
// It extracts the RouteResponse payload and enqueues it into the bounded buffer.
func (b *GuideBridge) onMessage(busMsg *guide.Message) error {
	resp, ok := busMsg.GetRouteResponse()
	if !ok {
		return nil
	}
	select {
	case b.buffer <- resp:
	default:
		b.dropped.Add(1)
	}
	return nil
}

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *GuideBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			select {
			case resp := <-b.buffer:
				program.Send(toGuideMsg(resp))
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// toGuideMsg converts a RouteResponse into a GuideResponseMsg.
func toGuideMsg(resp *guide.RouteResponse) msg.GuideResponseMsg {
	m := msg.GuideResponseMsg{
		CorrelationID: resp.CorrelationID,
		AgentID:       resp.RespondingAgentID,
		AgentName:     resp.RespondingAgentName,
	}
	if resp.Success {
		m.Content, _ = resp.Data.(string)
		return m
	}
	if resp.Error != "" {
		m.Err = guideError(resp.Error)
	}
	return m
}

// guideError is a simple error type for guide response errors.
type guideError string

func (e guideError) Error() string { return string(e) }
