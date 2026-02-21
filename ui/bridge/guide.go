package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	guideBridgeName = "bridge.guide"
	guideBufferSize = 256
	// Zero uses the scope's max lifetime; guide bridge is long-lived for the UI session.
	guideDrainTimeout = 0

	// tuiAgentType and tuiAgentID identify the TUI as a source agent.
	// The Guide routes responses back to TopicResponses(tuiAgentType, tuiAgentID).
	tuiAgentType = "tui"
	tuiAgentID   = "tui"
)

// GuideBridge subscribes to the TUI's response topic on the Guide EventBus
// and forwards both RouteResponse and StreamResponse messages as Bubble Tea
// messages to the program.
type GuideBridge struct {
	bus          guide.EventBus
	scope        *concurrency.GoroutineScope
	buffer       chan *guide.Message
	dropped      atomic.Int64
	done         chan struct{}
	subscription guide.Subscription
	sessionID    string
	stopOnce     sync.Once
}

// NewGuideBridge creates a bridge that converts Guide bus response messages
// into Bubble Tea messages.
func NewGuideBridge(bus guide.EventBus, scope *concurrency.GoroutineScope, sessionID string) *GuideBridge {
	return &GuideBridge{
		bus:       bus,
		scope:     scope,
		buffer:    make(chan *guide.Message, guideBufferSize),
		done:      make(chan struct{}),
		sessionID: sessionID,
	}
}

// -- Bridge implementation --

// Start subscribes to the TUI response topic and launches the drain goroutine.
func (b *GuideBridge) Start(program TeaProgram) error {
	topic := guide.TopicResponses(tuiAgentType, tuiAgentID)
	sub, err := b.bus.Subscribe(topic, b.onMessage)
	if err != nil {
		return err
	}
	b.subscription = sub
	return b.scope.Go(guideBridgeName, guideDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *GuideBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.subscription != nil {
			_ = b.subscription.Unsubscribe()
		}
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *GuideBridge) Name() string { return guideBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *GuideBridge) DroppedCount() int64 { return b.dropped.Load() }

// onMessage is the guide.MessageHandler called by the EventBus.
// It enqueues the raw message into the bounded buffer for type dispatch.
func (b *GuideBridge) onMessage(busMsg *guide.Message) error {
	select {
	case b.buffer <- busMsg:
	default:
		b.dropped.Add(1)
	}
	return nil
}

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *GuideBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case busMsg := <-b.buffer:
				b.dispatch(busMsg, program)
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// dispatch converts a bus message into the appropriate Bubble Tea message(s).
func (b *GuideBridge) dispatch(busMsg *guide.Message, program TeaProgram) {
	if busMsg == nil {
		return
	}
	if resp, ok := busMsg.GetRouteResponse(); ok {
		program.Send(toGuideMsg(resp))
		return
	}
	if stream, ok := busMsg.GetStreamResponse(); ok {
		b.dispatchStream(stream, program)
		return
	}
	if errText, ok := busMsg.GetError(); ok {
		program.Send(msg.StreamErrorMsg{
			SessionID:     b.sessionID,
			CorrelationID: busMsg.CorrelationID,
			Err:           guideError(errText),
		})
	}
}

// dispatchStream converts a StreamResponse into the matching stream tea message.
func (b *GuideBridge) dispatchStream(stream *guide.StreamResponse, program TeaProgram) {
	if stream.Event == nil {
		return
	}
	sid := b.sessionID
	cid := stream.CorrelationID

	switch stream.Event.Type {
	case guide.StreamEventStart:
		program.Send(msg.StreamStartMsg{SessionID: sid, CorrelationID: cid, AgentID: stream.RespondingAgentID})
	case guide.StreamEventData:
		program.Send(msg.StreamChunkMsg{SessionID: sid, CorrelationID: cid, Text: stream.Event.Text})
	case guide.StreamEventComplete:
		program.Send(msg.StreamCompleteMsg{SessionID: sid, CorrelationID: cid, Result: stream.Event.Data})
	case guide.StreamEventError:
		program.Send(msg.StreamErrorMsg{SessionID: sid, CorrelationID: cid, Err: extractStreamError(stream.Event)})
	case guide.StreamEventRetry:
		status, _ := stream.Event.Data.(guide.RetryStatus)
		errText := ""
		if status.Err != nil {
			errText = status.Err.Error()
		}
		program.Send(msg.RetryStatusMsg{
			SessionID:     sid,
			CorrelationID: cid,
			Attempt:       status.Attempt,
			MaxAttempts:   status.MaxAttempts,
			Error:         errText,
		})
	}
}

// extractStreamError pulls an error from a StreamEvent.
func extractStreamError(event *guide.StreamEvent) error {
	if e, ok := event.Data.(error); ok {
		return e
	}
	return guideError("stream error")
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
