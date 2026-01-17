package signal

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type SignalHandler struct {
	bus        *SignalBus
	subscriber SignalSubscriber
	callback   func(SignalMessage)
	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
}

func NewSignalHandler(bus *SignalBus, agentID string, signals []Signal) *SignalHandler {
	return &SignalHandler{
		bus: bus,
		subscriber: SignalSubscriber{
			ID:      uuid.New().String(),
			AgentID: agentID,
			Signals: signals,
			Channel: make(chan SignalMessage, bus.config.ChannelBufferSize),
		},
		stopCh: make(chan struct{}),
	}
}

func (h *SignalHandler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return nil
	}

	if err := h.bus.Subscribe(h.subscriber); err != nil {
		return err
	}

	h.running = true
	go h.listen()
	return nil
}

func (h *SignalHandler) listen() {
	for {
		select {
		case <-h.stopCh:
			return
		case msg := <-h.subscriber.Channel:
			h.handleMessage(msg)
		}
	}
}

func (h *SignalHandler) handleMessage(msg SignalMessage) {
	h.mu.Lock()
	cb := h.callback
	h.mu.Unlock()

	if cb != nil {
		cb(msg)
	}
}

func (h *SignalHandler) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}

	close(h.stopCh)
	h.running = false
	_ = h.bus.Unsubscribe(h.subscriber.ID)
}

func (h *SignalHandler) OnSignal(callback func(SignalMessage)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callback = callback
}

func (h *SignalHandler) Ack(msg SignalMessage, state string, checkpoint any) error {
	ack := SignalAck{
		SignalID:     msg.ID,
		SubscriberID: h.subscriber.ID,
		AgentID:      h.subscriber.AgentID,
		ReceivedAt:   time.Now(),
		State:        state,
		Checkpoint:   checkpoint,
	}
	return h.bus.Acknowledge(ack)
}

func (h *SignalHandler) SubscriberID() string {
	return h.subscriber.ID
}
