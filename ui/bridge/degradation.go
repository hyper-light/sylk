package bridge

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/adalundhe/sylk/ui/msg"
)

const timePressureBridgeName = "bridge.time_pressure"

// TimePressureBridge subscribes to the signal bus for TimePressure signals
// and forwards them to the Bubble Tea program as msg.TimePressureMsg.
// This keeps the user informed when agent operations approach their deadline.
type TimePressureBridge struct {
	signalBus signal.SignalBusInterface
	subID     string
	dropped   atomic.Int64
	stopOnce  sync.Once
	done      chan struct{}
}

// NewTimePressureBridge creates a bridge that converts signal bus
// TimePressure broadcasts into Bubble Tea messages.
func NewTimePressureBridge(signalBus signal.SignalBusInterface) *TimePressureBridge {
	return &TimePressureBridge{
		signalBus: signalBus,
		subID:     "tp_bridge_" + time.Now().Format("150405.000"),
		done:      make(chan struct{}),
	}
}

// Start subscribes to TimePressure signals on the signal bus and forwards
// matching signals to the Bubble Tea program.
func (b *TimePressureBridge) Start(program TeaProgram) error {
	ch := make(chan signal.SignalMessage, b.signalBus.ChannelBufferSize())
	sub := signal.SignalSubscriber{
		ID:      b.subID,
		AgentID: "ui",
		Signals: []signal.Signal{signal.TimePressure},
		Channel: ch,
	}
	if err := b.signalBus.Subscribe(sub); err != nil {
		return err
	}

	go b.forwardLoop(program, ch)
	return nil
}

func (b *TimePressureBridge) forwardLoop(program TeaProgram, ch <-chan signal.SignalMessage) {
	for {
		select {
		case <-b.done:
			return
		case sigMsg, ok := <-ch:
			if !ok {
				return
			}
			payload, valid := sigMsg.Payload.(signal.TimePressurePayload)
			if !valid {
				b.dropped.Add(1)
				continue
			}
			program.Send(msg.TimePressureMsg{
				AgentType: payload.AgentType,
				Stage:     payload.Stage,
				Elapsed:   payload.Elapsed,
				Remaining: payload.Remaining,
				Message:   payload.Suggestion,
			})
		}
	}
}

// Stop unsubscribes from the signal bus and stops the forward goroutine.
func (b *TimePressureBridge) Stop() {
	b.stopOnce.Do(func() {
		close(b.done)
		_ = b.signalBus.Unsubscribe(b.subID)
	})
}

// Name returns the bridge identifier.
func (b *TimePressureBridge) Name() string { return timePressureBridgeName }

// DroppedCount returns the total number of signals dropped due to type mismatch.
func (b *TimePressureBridge) DroppedCount() int64 { return b.dropped.Load() }
