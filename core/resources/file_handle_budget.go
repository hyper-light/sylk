package resources

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

const (
	DefaultGlobalLimit    = 1024
	DefaultReservedRatio  = 0.20
	DefaultBaseAllocation = 10
)

type agentTypeConfig struct {
	baseAllocation int
}

var defaultAgentConfigs = map[string]agentTypeConfig{
	"editor":   {baseAllocation: 20},
	"terminal": {baseAllocation: 15},
	"search":   {baseAllocation: 10},
	"default":  {baseAllocation: DefaultBaseAllocation},
}

type FileHandleBudget struct {
	globalLimit     int64
	reserved        int64
	globalAllocated atomic.Int64
	globalUsed      atomic.Int64
	sessions        sync.Map
	pressureLevel   *atomic.Int32
	notifier        ResourceNotifier
	logger          *slog.Logger
	mu              sync.Mutex
}

type FileHandleBudgetConfig struct {
	GlobalLimit   int64
	ReservedRatio float64
	Notifier      ResourceNotifier
	Logger        *slog.Logger
}

func DefaultFileHandleBudgetConfig() FileHandleBudgetConfig {
	return FileHandleBudgetConfig{
		GlobalLimit:   DefaultGlobalLimit,
		ReservedRatio: DefaultReservedRatio,
		Notifier:      &NoOpNotifier{},
		Logger:        slog.Default(),
	}
}

func NewFileHandleBudget(cfg FileHandleBudgetConfig) *FileHandleBudget {
	if cfg.GlobalLimit <= 0 {
		cfg.GlobalLimit = DefaultGlobalLimit
	}
	if cfg.ReservedRatio <= 0 || cfg.ReservedRatio >= 1 {
		cfg.ReservedRatio = DefaultReservedRatio
	}
	if cfg.Notifier == nil {
		cfg.Notifier = &NoOpNotifier{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	pressureLevel := &atomic.Int32{}
	reserved := int64(float64(cfg.GlobalLimit) * cfg.ReservedRatio)

	return &FileHandleBudget{
		globalLimit:   cfg.GlobalLimit,
		reserved:      reserved,
		pressureLevel: pressureLevel,
		notifier:      cfg.Notifier,
		logger:        cfg.Logger,
	}
}

func (b *FileHandleBudget) Available() int64 {
	return b.globalLimit - b.reserved - b.globalAllocated.Load()
}

func (b *FileHandleBudget) GlobalLimit() int64 {
	return b.globalLimit
}

func (b *FileHandleBudget) Reserved() int64 {
	return b.reserved
}

func (b *FileHandleBudget) GlobalAllocated() int64 {
	return b.globalAllocated.Load()
}

func (b *FileHandleBudget) GlobalUsed() int64 {
	return b.globalUsed.Load()
}

func (b *FileHandleBudget) SetPressureLevel(level int32) {
	b.pressureLevel.Store(level)
}

func (b *FileHandleBudget) PressureLevel() int32 {
	return b.pressureLevel.Load()
}

func (b *FileHandleBudget) TryAllocate(amount int64) bool {
	for {
		current := b.globalAllocated.Load()
		available := b.globalLimit - b.reserved - current
		if amount > available {
			return false
		}
		if b.globalAllocated.CompareAndSwap(current, current+amount) {
			return true
		}
	}
}

func (b *FileHandleBudget) Deallocate(amount int64) {
	b.globalAllocated.Add(-amount)
}

func (b *FileHandleBudget) IncrementUsed() {
	b.globalUsed.Add(1)
}

func (b *FileHandleBudget) DecrementUsed() {
	b.globalUsed.Add(-1)
}

func (b *FileHandleBudget) RegisterSession(sessionID string) *SessionFileBudget {
	session := newSessionFileBudget(sessionID, b)
	actual, loaded := b.sessions.LoadOrStore(sessionID, session)
	if loaded {
		return actual.(*SessionFileBudget)
	}
	return session
}

func (b *FileHandleBudget) UnregisterSession(sessionID string) {
	if val, ok := b.sessions.LoadAndDelete(sessionID); ok {
		session := val.(*SessionFileBudget)
		session.cleanup()
	}
}

func (b *FileHandleBudget) GetSession(sessionID string) *SessionFileBudget {
	if val, ok := b.sessions.Load(sessionID); ok {
		return val.(*SessionFileBudget)
	}
	return nil
}

func (b *FileHandleBudget) Notifier() ResourceNotifier {
	return b.notifier
}

func (b *FileHandleBudget) Logger() *slog.Logger {
	return b.logger
}

// RangeSessions iterates over all sessions, calling fn for each.
// If fn returns false, iteration stops.
func (b *FileHandleBudget) RangeSessions(fn func(sessionID string, session *SessionFileBudget) bool) {
	b.sessions.Range(func(key, value any) bool {
		return fn(key.(string), value.(*SessionFileBudget))
	})
}

func (b *FileHandleBudget) Snapshot() FileHandleSnapshot {
	snapshot := FileHandleSnapshot{
		Global: GlobalSnapshot{
			Limit:       int(b.globalLimit),
			Reserved:    int(b.reserved),
			Allocated:   int(b.globalAllocated.Load()),
			Used:        int(b.globalUsed.Load()),
			Unallocated: int(b.Available()),
		},
		Sessions: make([]SessionSnapshot, 0),
	}

	b.sessions.Range(func(key, value any) bool {
		session := value.(*SessionFileBudget)
		snapshot.Sessions = append(snapshot.Sessions, session.snapshot())
		return true
	})

	return snapshot
}

type SessionFileBudget struct {
	sessionID string
	parent    *FileHandleBudget
	allocated atomic.Int64
	used      atomic.Int64
	agents    sync.Map
	mu        sync.Mutex
	cond      *sync.Cond
}

func newSessionFileBudget(sessionID string, parent *FileHandleBudget) *SessionFileBudget {
	s := &SessionFileBudget{
		sessionID: sessionID,
		parent:    parent,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *SessionFileBudget) SessionID() string {
	return s.sessionID
}

func (s *SessionFileBudget) Allocated() int64 {
	return s.allocated.Load()
}

func (s *SessionFileBudget) Used() int64 {
	return s.used.Load()
}

func (s *SessionFileBudget) Unallocated() int64 {
	return s.allocated.Load() - s.sumAgentAllocations()
}

func (s *SessionFileBudget) TryExpand(amount int64) bool {
	if !s.parent.TryAllocate(amount) {
		return false
	}
	s.allocated.Add(amount)
	return true
}

func (s *SessionFileBudget) Shrink(amount int64) {
	s.allocated.Add(-amount)
	s.parent.Deallocate(amount)
}

func (s *SessionFileBudget) IncrementUsed() {
	s.used.Add(1)
	s.parent.IncrementUsed()
}

func (s *SessionFileBudget) DecrementUsed() {
	s.used.Add(-1)
	s.parent.DecrementUsed()
}

func (s *SessionFileBudget) RegisterAgent(agentID, agentType string) *AgentFileBudget {
	cfg, ok := defaultAgentConfigs[agentType]
	if !ok {
		cfg = defaultAgentConfigs["default"]
	}

	agent := newAgentFileBudget(agentID, agentType, s, cfg.baseAllocation)
	actual, loaded := s.agents.LoadOrStore(agentID, agent)
	if loaded {
		return actual.(*AgentFileBudget)
	}

	if s.TryExpand(int64(cfg.baseAllocation)) {
		agent.allocated.Store(int64(cfg.baseAllocation))
	}

	return agent
}

func (s *SessionFileBudget) UnregisterAgent(agentID string) {
	if val, ok := s.agents.LoadAndDelete(agentID); ok {
		agent := val.(*AgentFileBudget)
		agent.cleanup()
	}
}

func (s *SessionFileBudget) GetAgent(agentID string) *AgentFileBudget {
	if val, ok := s.agents.Load(agentID); ok {
		return val.(*AgentFileBudget)
	}
	return nil
}

func (s *SessionFileBudget) tryExpandAgent(agent *AgentFileBudget, amount int64) bool {
	unallocated := s.Unallocated()
	if amount > unallocated {
		return false
	}
	agent.allocated.Add(amount)
	return true
}

func (s *SessionFileBudget) signalWaiters() {
	s.cond.Broadcast()
}

func (s *SessionFileBudget) sumAgentAllocations() int64 {
	var total int64
	s.agents.Range(func(key, value any) bool {
		agent := value.(*AgentFileBudget)
		total += agent.allocated.Load()
		return true
	})
	return total
}

func (s *SessionFileBudget) cleanup() {
	s.agents.Range(func(key, value any) bool {
		agent := value.(*AgentFileBudget)
		agent.cleanup()
		return true
	})
	s.parent.Deallocate(s.allocated.Load())
}

func (s *SessionFileBudget) snapshot() SessionSnapshot {
	snap := SessionSnapshot{
		SessionID:   s.sessionID,
		Allocated:   int(s.allocated.Load()),
		Used:        int(s.used.Load()),
		Unallocated: int(s.Unallocated()),
		Agents:      make([]AgentSnapshot, 0),
	}

	s.agents.Range(func(key, value any) bool {
		agent := value.(*AgentFileBudget)
		snap.Agents = append(snap.Agents, agent.snapshot())
		return true
	})

	return snap
}

type AgentFileBudget struct {
	agentID        string
	agentType      string
	parent         *SessionFileBudget
	allocated      atomic.Int64
	used           atomic.Int64
	baseAllocation int
	mu             sync.Mutex
	cond           *sync.Cond
	waiting        atomic.Bool
}

func newAgentFileBudget(agentID, agentType string, parent *SessionFileBudget, baseAllocation int) *AgentFileBudget {
	a := &AgentFileBudget{
		agentID:        agentID,
		agentType:      agentType,
		parent:         parent,
		baseAllocation: baseAllocation,
	}
	a.cond = sync.NewCond(&a.mu)
	return a
}

func (a *AgentFileBudget) AgentID() string {
	return a.agentID
}

func (a *AgentFileBudget) AgentType() string {
	return a.agentType
}

func (a *AgentFileBudget) Allocated() int64 {
	return a.allocated.Load()
}

func (a *AgentFileBudget) Used() int64 {
	return a.used.Load()
}

func (a *AgentFileBudget) Available() int64 {
	return a.allocated.Load() - a.used.Load()
}

func (a *AgentFileBudget) IsWaiting() bool {
	return a.waiting.Load()
}

func (a *AgentFileBudget) Acquire(ctx context.Context) error {
	notified := false

	for {
		if ctx.Err() != nil {
			a.clearWaiting(notified)
			return ctx.Err()
		}

		acquired, level := a.tryAcquire()
		if acquired {
			a.handleAcquired(notified, level)
			return nil
		}

		if !notified {
			a.startWaiting()
			notified = true
		}

		if err := a.waitWithContext(ctx); err != nil {
			a.clearWaiting(notified)
			return err
		}
	}
}

func (a *AgentFileBudget) tryAcquire() (acquired bool, level string) {
	if a.tryAcquireFromAgent() {
		return true, "agent"
	}

	if a.tryAcquireFromSession() {
		return true, "session"
	}

	if a.tryAcquireFromGlobal() {
		return true, "global"
	}

	return false, ""
}

func (a *AgentFileBudget) tryAcquireFromAgent() bool {
	if a.Available() <= 0 {
		return false
	}
	a.used.Add(1)
	a.parent.IncrementUsed()
	return true
}

func (a *AgentFileBudget) tryAcquireFromSession() bool {
	if !a.parent.tryExpandAgent(a, 1) {
		return false
	}
	a.used.Add(1)
	a.parent.IncrementUsed()
	return true
}

func (a *AgentFileBudget) tryAcquireFromGlobal() bool {
	if !a.parent.TryExpand(1) {
		return false
	}
	if !a.parent.tryExpandAgent(a, 1) {
		a.parent.Shrink(1)
		return false
	}
	a.used.Add(1)
	a.parent.IncrementUsed()
	return true
}

func (a *AgentFileBudget) startWaiting() {
	a.waiting.Store(true)
	notifier := a.parent.parent.notifier
	logger := a.parent.parent.logger

	logger.Info("agent waiting for file handle",
		"session_id", a.parent.sessionID,
		"agent_id", a.agentID,
	)
	notifier.AgentWaiting(a.parent.sessionID, a.agentID, "file_handle")
}

func (a *AgentFileBudget) handleAcquired(wasWaiting bool, level string) {
	if !wasWaiting {
		return
	}
	a.waiting.Store(false)
	notifier := a.parent.parent.notifier
	logger := a.parent.parent.logger

	logger.Info("agent acquired file handle",
		"session_id", a.parent.sessionID,
		"agent_id", a.agentID,
		"level", level,
	)
	notifier.AgentProceeding(a.parent.sessionID, a.agentID, "file_handle")
}

func (a *AgentFileBudget) clearWaiting(wasWaiting bool) {
	if !wasWaiting {
		return
	}
	a.waiting.Store(false)
}

func (a *AgentFileBudget) waitWithContext(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for a.Available() <= 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.cond.Wait()
	}

	return nil
}

func (a *AgentFileBudget) Release() {
	a.used.Add(-1)
	a.parent.DecrementUsed()
	a.signalWaiters()
}

func (a *AgentFileBudget) signalWaiters() {
	a.cond.Broadcast()
	a.parent.signalWaiters()
}

func (a *AgentFileBudget) cleanup() {
	allocated := a.allocated.Load()
	if allocated > 0 {
		a.parent.Shrink(allocated)
	}
}

func (a *AgentFileBudget) snapshot() AgentSnapshot {
	return AgentSnapshot{
		AgentID:   a.agentID,
		AgentType: a.agentType,
		Allocated: int(a.allocated.Load()),
		Used:      int(a.used.Load()),
		Waiting:   a.waiting.Load(),
	}
}
