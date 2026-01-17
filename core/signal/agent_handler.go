package signal

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrHandlerNotRunning = errors.New("handler is not running")
	ErrInvalidTransition = errors.New("invalid agent state transition")
)

type AgentState int

const (
	AgentIdle AgentState = iota
	AgentRunning
	AgentPaused
	AgentCheckpointing
	AgentResuming
)

var agentStateNames = map[AgentState]string{
	AgentIdle:          "idle",
	AgentRunning:       "running",
	AgentPaused:        "paused",
	AgentCheckpointing: "checkpointing",
	AgentResuming:      "resuming",
}

func (s AgentState) String() string {
	if name, ok := agentStateNames[s]; ok {
		return name
	}
	return "unknown"
}

var validTransitions = map[AgentState][]AgentState{
	AgentIdle:          {AgentRunning},
	AgentRunning:       {AgentCheckpointing, AgentIdle},
	AgentCheckpointing: {AgentPaused},
	AgentPaused:        {AgentResuming, AgentIdle},
	AgentResuming:      {AgentRunning, AgentIdle},
}

func (s AgentState) CanTransitionTo(target AgentState) bool {
	allowed, ok := validTransitions[s]
	if !ok {
		return false
	}
	for _, t := range allowed {
		if t == target {
			return true
		}
	}
	return false
}

type CheckpointProvider interface {
	CreateCheckpoint() *AgentCheckpoint
	GetCurrentMessages() []Message
}

type AgentSignalHandler struct {
	bus        *SignalBus
	subscriber SignalSubscriber
	agentID    string
	agentType  string

	mu         sync.RWMutex
	state      AgentState
	checkpoint *AgentCheckpoint

	checkpointProvider CheckpointProvider
	callback           func(SignalMessage)
	onStateChange      func(AgentState, AgentState)

	stopCh  chan struct{}
	running bool
}

func NewAgentSignalHandler(bus *SignalBus, agentID, agentType string) *AgentSignalHandler {
	signals := []Signal{
		PauseAll,
		ResumeAll,
		PausePipeline,
		ResumePipeline,
		CancelTask,
		AbortSession,
	}

	return &AgentSignalHandler{
		bus:       bus,
		agentID:   agentID,
		agentType: agentType,
		subscriber: SignalSubscriber{
			ID:      uuid.New().String(),
			AgentID: agentID,
			Signals: signals,
			Channel: make(chan SignalMessage, bus.config.ChannelBufferSize),
		},
		state:  AgentIdle,
		stopCh: make(chan struct{}),
	}
}

func (h *AgentSignalHandler) SetCheckpointProvider(provider CheckpointProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkpointProvider = provider
}

func (h *AgentSignalHandler) OnSignal(callback func(SignalMessage)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callback = callback
}

func (h *AgentSignalHandler) OnStateChange(callback func(old, new AgentState)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onStateChange = callback
}

func (h *AgentSignalHandler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return nil
	}

	if err := h.bus.Subscribe(h.subscriber); err != nil {
		return err
	}

	h.running = true
	h.transitionLocked(AgentRunning)
	go h.listen()
	return nil
}

func (h *AgentSignalHandler) listen() {
	for {
		select {
		case <-h.stopCh:
			return
		case msg := <-h.subscriber.Channel:
			h.handleMessage(msg)
		}
	}
}

func (h *AgentSignalHandler) handleMessage(msg SignalMessage) {
	h.mu.Lock()
	cb := h.callback
	h.mu.Unlock()

	if h.isPauseSignal(msg.Signal) {
		h.handlePause(msg)
	} else if h.isResumeSignal(msg.Signal) {
		h.handleResume(msg)
	}

	if cb != nil {
		cb(msg)
	}
}

func (h *AgentSignalHandler) isPauseSignal(s Signal) bool {
	return s == PauseAll || s == PausePipeline
}

func (h *AgentSignalHandler) isResumeSignal(s Signal) bool {
	return s == ResumeAll || s == ResumePipeline
}

func (h *AgentSignalHandler) handlePause(msg SignalMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.state != AgentRunning {
		return
	}

	h.transitionLocked(AgentCheckpointing)
	h.createCheckpointLocked()
	h.transitionLocked(AgentPaused)

	if msg.RequiresAck {
		h.sendAckLocked(msg)
	}
}

func (h *AgentSignalHandler) createCheckpointLocked() {
	if h.checkpointProvider != nil {
		h.checkpoint = h.checkpointProvider.CreateCheckpoint()
	} else {
		h.checkpoint = h.createDefaultCheckpoint()
	}
}

func (h *AgentSignalHandler) createDefaultCheckpoint() *AgentCheckpoint {
	return NewCheckpointBuilder(uuid.New().String(), h.agentID, h.agentType).Build()
}

func (h *AgentSignalHandler) sendAckLocked(msg SignalMessage) {
	ack := SignalAck{
		SignalID:     msg.ID,
		SubscriberID: h.subscriber.ID,
		AgentID:      h.agentID,
		ReceivedAt:   time.Now(),
		State:        h.state.String(),
		Checkpoint:   h.checkpoint,
	}
	_ = h.bus.Acknowledge(ack)
}

func (h *AgentSignalHandler) handleResume(msg SignalMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.state != AgentPaused {
		return
	}

	h.transitionLocked(AgentResuming)

	result := h.evaluateResumeLocked()
	h.processResumeResult(result)

	h.checkpoint = nil
	h.transitionLocked(AgentRunning)

	if msg.RequiresAck {
		h.sendAckLocked(msg)
	}
}

func (h *AgentSignalHandler) evaluateResumeLocked() *ResumeResult {
	var currentMessages []Message
	if h.checkpointProvider != nil {
		currentMessages = h.checkpointProvider.GetCurrentMessages()
	}

	ctx := &ResumeContext{
		Checkpoint:      h.checkpoint,
		CurrentMessages: currentMessages,
	}

	return EvaluateResume(ctx)
}

func (h *AgentSignalHandler) processResumeResult(_ *ResumeResult) {
}

func (h *AgentSignalHandler) transitionLocked(target AgentState) {
	if !h.state.CanTransitionTo(target) {
		return
	}

	old := h.state
	h.state = target

	if h.onStateChange != nil {
		go h.onStateChange(old, target)
	}
}

func (h *AgentSignalHandler) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}

	close(h.stopCh)
	h.running = false
	_ = h.bus.Unsubscribe(h.subscriber.ID)

	if h.state != AgentIdle {
		h.transitionLocked(AgentIdle)
	}
}

func (h *AgentSignalHandler) State() AgentState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

func (h *AgentSignalHandler) Checkpoint() *AgentCheckpoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.checkpoint
}

func (h *AgentSignalHandler) SubscriberID() string {
	return h.subscriber.ID
}

func (h *AgentSignalHandler) AgentID() string {
	return h.agentID
}

func (h *AgentSignalHandler) Ack(msg SignalMessage, state string, checkpoint any) error {
	ack := SignalAck{
		SignalID:     msg.ID,
		SubscriberID: h.subscriber.ID,
		AgentID:      h.agentID,
		ReceivedAt:   time.Now(),
		State:        state,
		Checkpoint:   checkpoint,
	}
	return h.bus.Acknowledge(ack)
}
