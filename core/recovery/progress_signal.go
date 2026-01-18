package recovery

import (
	"sync"
	"sync/atomic"
	"time"
)

type ProgressSignalType int

const (
	SignalToolCompleted ProgressSignalType = iota
	SignalLLMResponse
	SignalFileModified
	SignalStateTransition
	SignalAgentRequest
)

type ProgressSignal struct {
	AgentID    string
	SessionID  string
	SignalType ProgressSignalType
	Timestamp  time.Time
	Operation  string
	Hash       uint64
	Details    map[string]any
}

type ProgressSubscriber interface {
	OnSignal(sig ProgressSignal)
}

type ProgressCollector struct {
	signals     sync.Map
	subscribers []ProgressSubscriber
	mu          sync.RWMutex
}

type AgentSignalBuffer struct {
	agentID     string
	sessionID   string
	buffer      *RingBuffer[ProgressSignal]
	lastSignal  atomic.Value
	signalCount atomic.Int64
	mu          sync.RWMutex
}

const defaultBufferSize = 100

func NewProgressCollector() *ProgressCollector {
	return &ProgressCollector{}
}

func (c *ProgressCollector) Signal(sig ProgressSignal) {
	buf := c.getOrCreateBuffer(sig.AgentID, sig.SessionID)
	c.recordSignal(buf, sig)
	c.notifySubscribers(sig)
}

func (c *ProgressCollector) getOrCreateBuffer(agentID, sessionID string) *AgentSignalBuffer {
	if existing, ok := c.signals.Load(agentID); ok {
		return existing.(*AgentSignalBuffer)
	}

	buf := &AgentSignalBuffer{
		agentID:   agentID,
		sessionID: sessionID,
		buffer:    NewRingBuffer[ProgressSignal](defaultBufferSize),
	}

	actual, _ := c.signals.LoadOrStore(agentID, buf)
	return actual.(*AgentSignalBuffer)
}

func (c *ProgressCollector) recordSignal(buf *AgentSignalBuffer, sig ProgressSignal) {
	buf.mu.Lock()
	buf.buffer.Push(sig)
	buf.lastSignal.Store(sig.Timestamp)
	buf.signalCount.Add(1)
	buf.mu.Unlock()
}

func (c *ProgressCollector) notifySubscribers(sig ProgressSignal) {
	c.mu.RLock()
	subs := c.subscribers
	c.mu.RUnlock()

	for _, sub := range subs {
		sub.OnSignal(sig)
	}
}

func (c *ProgressCollector) Subscribe(sub ProgressSubscriber) {
	c.mu.Lock()
	c.subscribers = append(c.subscribers, sub)
	c.mu.Unlock()
}

func (c *ProgressCollector) LastSignalTime(agentID string) (time.Time, bool) {
	buf, ok := c.signals.Load(agentID)
	if !ok {
		return time.Time{}, false
	}

	val := buf.(*AgentSignalBuffer).lastSignal.Load()
	if val == nil {
		return time.Time{}, false
	}
	return val.(time.Time), true
}

func (c *ProgressCollector) RecentSignals(agentID string, n int) []ProgressSignal {
	buf, ok := c.signals.Load(agentID)
	if !ok {
		return nil
	}
	return buf.(*AgentSignalBuffer).buffer.Last(n)
}

func (c *ProgressCollector) SignalCount(agentID string) int64 {
	buf, ok := c.signals.Load(agentID)
	if !ok {
		return 0
	}
	return buf.(*AgentSignalBuffer).signalCount.Load()
}

func (c *ProgressCollector) RemoveAgent(agentID string) {
	c.signals.Delete(agentID)
}
