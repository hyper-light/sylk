package concurrency

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrTooManyOperations   = errors.New("too many concurrent operations")
	ErrSupervisorStopped   = errors.New("supervisor is not accepting work")
	ErrSupervisorCancelled = errors.New("supervisor cancelled")
)

type SupervisorState int32

const (
	SupervisorStateRunning SupervisorState = iota
	SupervisorStatePausing
	SupervisorStatePaused
	SupervisorStateStopping
	SupervisorStateStopped
)

type CheckpointProvider interface {
	SaveCheckpoint(agentID string, state any) error
	LoadCheckpoint(agentID string) (any, error)
}

type FileBudgetProvider interface {
	Acquire(ctx context.Context) error
	Release()
}

type FileMode int

const (
	FileModeRead FileMode = iota
	FileModeWrite
	FileModeReadWrite
	FileModeAppend
)

func (m FileMode) ToOSFlags() int {
	switch m {
	case FileModeRead:
		return os.O_RDONLY
	case FileModeWrite:
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case FileModeReadWrite:
		return os.O_RDWR | os.O_CREATE
	case FileModeAppend:
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND
	default:
		return os.O_RDONLY
	}
}

type AgentSupervisorConfig struct {
	MaxConcurrentOps   int
	DefaultOpTimeout   time.Duration
	MaxOpTimeout       time.Duration
	GracePeriod        time.Duration
	ForceCloseDeadline time.Duration
}

func DefaultAgentSupervisorConfig() AgentSupervisorConfig {
	return AgentSupervisorConfig{
		MaxConcurrentOps:   10,
		DefaultOpTimeout:   30 * time.Second,
		MaxOpTimeout:       5 * time.Minute,
		GracePeriod:        5 * time.Second,
		ForceCloseDeadline: 10 * time.Second,
	}
}

type AgentSupervisor struct {
	agentID    string
	agentType  string
	pipelineID string
	sessionID  string

	ctx    context.Context
	cancel context.CancelFunc
	state  atomic.Int32

	scope        *GoroutineScope
	operationsMu sync.RWMutex
	operations   map[string]*Operation
	resources    *ResourceTracker

	pauseBarrier *PauseBarrier
	checkpointer CheckpointProvider
	fileBudget   FileBudgetProvider

	config     AgentSupervisorConfig
	opSem      chan struct{}
	acceptWork atomic.Bool
}

func NewAgentSupervisor(
	ctx context.Context,
	agentID, agentType, pipelineID string,
	budget *GoroutineBudget,
	cfg AgentSupervisorConfig,
) *AgentSupervisor {
	cfg = normalizeConfig(cfg)
	supervisorCtx, cancel := context.WithCancel(ctx)

	s := &AgentSupervisor{
		agentID:      agentID,
		agentType:    agentType,
		pipelineID:   pipelineID,
		ctx:          supervisorCtx,
		cancel:       cancel,
		scope:        NewGoroutineScope(supervisorCtx, agentID, budget),
		operations:   make(map[string]*Operation),
		resources:    NewResourceTracker(agentID),
		pauseBarrier: NewPauseBarrier(),
		config:       cfg,
		opSem:        make(chan struct{}, cfg.MaxConcurrentOps),
	}

	s.state.Store(int32(SupervisorStateRunning))
	s.acceptWork.Store(true)

	return s
}

func normalizeConfig(cfg AgentSupervisorConfig) AgentSupervisorConfig {
	defaults := DefaultAgentSupervisorConfig()
	cfg.MaxConcurrentOps = normalizeInt(cfg.MaxConcurrentOps, defaults.MaxConcurrentOps)
	cfg.DefaultOpTimeout = normalizeDuration(cfg.DefaultOpTimeout, defaults.DefaultOpTimeout)
	cfg.MaxOpTimeout = normalizeDuration(cfg.MaxOpTimeout, defaults.MaxOpTimeout)
	cfg.GracePeriod = normalizeDuration(cfg.GracePeriod, defaults.GracePeriod)
	cfg.ForceCloseDeadline = normalizeDuration(cfg.ForceCloseDeadline, defaults.ForceCloseDeadline)
	return cfg
}

func normalizeInt(val, defaultVal int) int {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func normalizeDuration(val, defaultVal time.Duration) time.Duration {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func (s *AgentSupervisor) AgentID() string {
	return s.agentID
}

func (s *AgentSupervisor) BeginOperation(
	opType OperationType,
	description string,
	timeout time.Duration,
) (*Operation, error) {
	if err := s.checkCanBeginOperation(); err != nil {
		return nil, err
	}

	if err := s.pauseBarrier.Wait(s.ctx); err != nil {
		return nil, err
	}

	if err := s.acquireOpSlot(); err != nil {
		return nil, err
	}

	timeout = s.normalizeTimeout(timeout)
	op := NewOperation(s.ctx, opType, s.agentID, description, timeout)

	s.registerOperation(op)
	return op, nil
}

func (s *AgentSupervisor) checkCanBeginOperation() error {
	if !s.acceptWork.Load() {
		return ErrSupervisorStopped
	}

	select {
	case <-s.ctx.Done():
		return ErrSupervisorCancelled
	default:
		return nil
	}
}

func (s *AgentSupervisor) acquireOpSlot() error {
	select {
	case s.opSem <- struct{}{}:
		return nil
	case <-s.ctx.Done():
		return ErrSupervisorCancelled
	}
}

func (s *AgentSupervisor) normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return s.config.DefaultOpTimeout
	}
	if timeout > s.config.MaxOpTimeout {
		return s.config.MaxOpTimeout
	}
	return timeout
}

func (s *AgentSupervisor) registerOperation(op *Operation) {
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	s.operations[op.ID] = op
}

func (s *AgentSupervisor) EndOperation(op *Operation, result any, err error) {
	op.SetResult(result, err)
	op.Cancel()
	op.ReleaseResources(s.resources)
	op.MarkDone()

	s.removeOperation(op.ID)
	s.releaseOpSlot()
}

func (s *AgentSupervisor) removeOperation(opID string) {
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	delete(s.operations, opID)
}

func (s *AgentSupervisor) releaseOpSlot() {
	select {
	case <-s.opSem:
	default:
	}
}

func (s *AgentSupervisor) TrackResource(op *Operation, res TrackedResource) {
	s.resources.Track(res)
	op.AddResource(res)
}

func (s *AgentSupervisor) CancelAll() {
	s.cancel()
	s.cancelAllOperations()
}

func (s *AgentSupervisor) cancelAllOperations() {
	s.operationsMu.RLock()
	defer s.operationsMu.RUnlock()

	for _, op := range s.operations {
		op.Cancel()
	}
}

func (s *AgentSupervisor) ForceCloseResources() []error {
	s.forceCloseOperationResources()
	return s.resources.ForceCloseAll()
}

func (s *AgentSupervisor) forceCloseOperationResources() {
	s.operationsMu.RLock()
	defer s.operationsMu.RUnlock()

	for _, op := range s.operations {
		for _, res := range op.Resources() {
			_ = res.ForceClose()
		}
	}
}

func (s *AgentSupervisor) StopAcceptingWork() {
	s.acceptWork.Store(false)
	s.state.Store(int32(SupervisorStateStopping))
}

func (s *AgentSupervisor) WaitForCompletion(ctx context.Context) error {
	return pollUntilCondition(ctx, s.noOperationsRemaining)
}

func (s *AgentSupervisor) noOperationsRemaining() bool {
	if s.operationCount() == 0 {
		s.state.Store(int32(SupervisorStateStopped))
		return true
	}
	return false
}

func pollUntilCondition(ctx context.Context, condition func() bool) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for !condition() {
		if err := waitForTickOrCancel(ctx, ticker.C); err != nil {
			return err
		}
	}
	return nil
}

func waitForTickOrCancel(ctx context.Context, tick <-chan time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tick:
		return nil
	}
}

func (s *AgentSupervisor) operationCount() int {
	s.operationsMu.RLock()
	defer s.operationsMu.RUnlock()
	return len(s.operations)
}

func (s *AgentSupervisor) SignalPause() {
	s.state.Store(int32(SupervisorStatePausing))
	s.pauseBarrier.Engage()
}

func (s *AgentSupervisor) WaitForPause(ctx context.Context) error {
	return pollUntilCondition(ctx, s.operationsPausedOrNone)
}

func (s *AgentSupervisor) operationsPausedOrNone() bool {
	if s.allOperationsPaused() {
		s.state.Store(int32(SupervisorStatePaused))
		return true
	}
	return false
}

func (s *AgentSupervisor) allOperationsPaused() bool {
	return s.pauseBarrier.WaiterCount() == int32(s.operationCount())
}

func (s *AgentSupervisor) SignalResume() {
	s.pauseBarrier.Release()
	s.state.Store(int32(SupervisorStateRunning))
}

func (s *AgentSupervisor) MarkOrphansAndReport() []OrphanedOperation {
	s.operationsMu.RLock()
	defer s.operationsMu.RUnlock()

	orphans := make([]OrphanedOperation, 0, len(s.operations))
	for _, op := range s.operations {
		orphans = append(orphans, op.ToOrphaned())
	}
	return orphans
}

func (s *AgentSupervisor) State() SupervisorState {
	return SupervisorState(s.state.Load())
}

func (s *AgentSupervisor) OperationCount() int {
	return s.operationCount()
}

func (s *AgentSupervisor) SetCheckpointer(cp CheckpointProvider) {
	s.checkpointer = cp
}

func (s *AgentSupervisor) SetSessionID(sessionID string) {
	s.sessionID = sessionID
}

func (s *AgentSupervisor) Scope() *GoroutineScope {
	return s.scope
}

func (s *AgentSupervisor) Resources() *ResourceTracker {
	return s.resources
}

func (s *AgentSupervisor) SetFileBudget(budget FileBudgetProvider) {
	s.fileBudget = budget
}

func (s *AgentSupervisor) OpenFile(ctx context.Context, path string, mode FileMode) (*TrackedFileHandle, error) {
	if s.fileBudget != nil {
		if err := s.fileBudget.Acquire(ctx); err != nil {
			return nil, err
		}
	}

	file, err := os.OpenFile(path, mode.ToOSFlags(), 0644)
	if err != nil {
		if s.fileBudget != nil {
			s.fileBudget.Release()
		}
		return nil, err
	}

	handle := NewTrackedFileHandle(file, path, s.sessionID, s.agentID, s.fileBudget)
	s.resources.Track(handle)
	return handle, nil
}

func (s *AgentSupervisor) CloseFile(handle *TrackedFileHandle) error {
	if handle == nil {
		return nil
	}
	s.resources.Release(handle)
	return handle.ForceClose()
}

type TrackedFileHandle struct {
	file      *os.File
	path      string
	fd        int
	sessionID string
	agentID   string
	budget    FileBudgetProvider
	mu        sync.Mutex
	closed    bool
}

func NewTrackedFileHandle(
	file *os.File,
	path string,
	sessionID, agentID string,
	budget FileBudgetProvider,
) *TrackedFileHandle {
	return &TrackedFileHandle{
		file:      file,
		path:      path,
		fd:        int(file.Fd()),
		sessionID: sessionID,
		agentID:   agentID,
		budget:    budget,
	}
}

func (h *TrackedFileHandle) ID() string {
	return "file:" + h.path
}

func (h *TrackedFileHandle) Type() ResourceType {
	return ResourceTypeFile
}

func (h *TrackedFileHandle) ForceClose() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}
	h.closed = true

	if h.budget != nil {
		h.budget.Release()
	}
	return h.file.Close()
}

func (h *TrackedFileHandle) IsClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *TrackedFileHandle) File() *os.File {
	return h.file
}

func (h *TrackedFileHandle) Path() string {
	return h.path
}

func (h *TrackedFileHandle) Read(p []byte) (int, error) {
	return h.file.Read(p)
}

func (h *TrackedFileHandle) Write(p []byte) (int, error) {
	return h.file.Write(p)
}
