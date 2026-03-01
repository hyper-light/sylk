package tdd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/google/uuid"
)

// PipelineManagerConfig configures the PipelineManager.
type PipelineManagerConfig struct {
	ActivityPub            events.ActivityPublisher
	MaxActivePipelines     int
	DefaultMaxLoops        int
	DefaultPhaseTimeout    time.Duration
	DefaultPipelineTimeout time.Duration
	Logger                 *slog.Logger
}

const (
	defaultMaxActivePipelines = 8
)

// PipelineManager creates, starts, cancels, and queries TDD pipelines.
type PipelineManager struct {
	config    PipelineManagerConfig
	factory   *AgentFactory
	pipelines map[string]*managedPipeline
	bySession map[string][]string
	byDAG     map[string][]string
	onEvent   func(PipelineEvent)
	mu        sync.RWMutex
	closed    bool
}

type managedPipeline struct {
	executor *TDDExecutor
	bus      *PipelineBus
	cancel   context.CancelFunc
}

// NewPipelineManager creates a new manager.
func NewPipelineManager(cfg PipelineManagerConfig, factory *AgentFactory, onEvent func(PipelineEvent)) *PipelineManager {
	if cfg.MaxActivePipelines <= 0 {
		cfg.MaxActivePipelines = defaultMaxActivePipelines
	}
	if cfg.DefaultMaxLoops <= 0 {
		cfg.DefaultMaxLoops = defaultMaxLoops
	}
	if cfg.DefaultPhaseTimeout == 0 {
		cfg.DefaultPhaseTimeout = defaultPhaseTimeout
	}
	if cfg.DefaultPipelineTimeout == 0 {
		cfg.DefaultPipelineTimeout = 30 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &PipelineManager{
		config:    cfg,
		factory:   factory,
		pipelines: make(map[string]*managedPipeline),
		bySession: make(map[string][]string),
		byDAG:     make(map[string][]string),
		onEvent:   onEvent,
	}
}

// SetOnEvent replaces the event callback. It is safe to call before any
// pipeline is started but must not race with concurrent Create/Start calls.
// This allows deferred wiring when the callback target (e.g. a UI bridge)
// is created after the manager.
func (m *PipelineManager) SetOnEvent(fn func(PipelineEvent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = fn
}

// Create builds a new Pipeline and TDDExecutor from config without starting it.
func (m *PipelineManager) Create(ctx context.Context, cfg PipelineConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return "", ErrPipelineClosed
	}
	if m.activeCountLocked() >= m.config.MaxActivePipelines {
		return "", ErrCapacityExhausted
	}

	id := uuid.New().String()[:12]

	maxLoops := cfg.MaxLoops
	if maxLoops <= 0 {
		maxLoops = m.config.DefaultMaxLoops
	}
	phaseTimeout := cfg.PhaseTimeout
	if phaseTimeout == 0 {
		phaseTimeout = m.config.DefaultPhaseTimeout
	}

	pipeline := &Pipeline{
		ID:                id,
		TaskID:            cfg.TaskID,
		SessionID:         cfg.SessionID,
		DAGNodeID:         cfg.DAGNodeID,
		WorkerType:        cfg.WorkerType,
		Status:            StatusPending,
		MaxLoops:          maxLoops,
		InspectorCriteria: cfg.InitialCriteria,
		TaskPrompt:        cfg.TaskPrompt,
		CoWorkerTypes:     cfg.CoWorkerTypes,
		AgentPrompts:      cfg.AgentPrompts,
		CreatedAt:         time.Now(),
	}

	insp, err := m.factory.CreateInspector(cfg.WorkerType)
	if err != nil {
		return "", fmt.Errorf("create inspector: %w", err)
	}
	tst, err := m.factory.CreateTester()
	if err != nil {
		insp.Close()
		return "", fmt.Errorf("create tester: %w", err)
	}
	worker, err := m.factory.CreateWorker(ctx, cfg.WorkerType)
	if err != nil {
		insp.Close()
		tst.Close()
		return "", fmt.Errorf("create worker: %w", err)
	}

	var coWorkers []WorkerAgent
	if len(cfg.CoWorkerTypes) > 0 {
		coWorkers, err = m.factory.CreateCoWorkers(ctx, cfg.CoWorkerTypes)
		if err != nil {
			insp.Close()
			tst.Close()
			worker.Close()
			return "", fmt.Errorf("create co-workers: %w", err)
		}
	}

	bus := NewPipelineBus()
	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          bus,
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		CoWorkers:    coWorkers,
		ActivityPub:  m.config.ActivityPub,
		PhaseTimeout: phaseTimeout,
		MaxLoops:     maxLoops,
		OnEvent:      m.onEvent,
		Logger:       m.config.Logger,
	})

	mp := &managedPipeline{
		executor: exec,
		bus:      bus,
	}
	m.pipelines[id] = mp
	m.bySession[cfg.SessionID] = append(m.bySession[cfg.SessionID], id)
	if cfg.DAGNodeID != "" {
		m.byDAG[cfg.DAGNodeID] = append(m.byDAG[cfg.DAGNodeID], id)
	}

	return id, nil
}

// Start begins pipeline execution with the configured PipelineTimeout.
func (m *PipelineManager) Start(ctx context.Context, id string) error {
	m.mu.Lock()
	mp, ok := m.pipelines[id]
	if !ok {
		m.mu.Unlock()
		return ErrPipelineNotFound
	}
	if m.closed {
		m.mu.Unlock()
		return ErrPipelineClosed
	}

	// Read pipeline status via executor (thread-safe).
	snap := mp.executor.Pipeline()
	if IsTerminalStatus(snap.Status) {
		m.mu.Unlock()
		return ErrPipelineTerminated
	}

	pipelineTimeout := m.config.DefaultPipelineTimeout
	pCtx, cancel := context.WithTimeout(ctx, pipelineTimeout)
	mp.cancel = cancel
	m.mu.Unlock()

	return mp.executor.Start(pCtx)
}

// Cancel stops a running pipeline.
func (m *PipelineManager) Cancel(_ context.Context, id string) error {
	m.mu.RLock()
	mp, ok := m.pipelines[id]
	if !ok {
		m.mu.RUnlock()
		return ErrPipelineNotFound
	}
	cancelFn := mp.cancel
	m.mu.RUnlock()

	// Check terminal via executor (thread-safe).
	snap := mp.executor.Pipeline()
	if IsTerminalStatus(snap.Status) {
		return nil
	}

	if cancelFn != nil {
		cancelFn()
	}
	_ = mp.executor.Stop()

	// Mark cancelled via executor's lock.
	mp.executor.markCancelled()
	mp.executor.CloseCoWorkers()
	mp.bus.Close()
	return nil
}

// Get returns a snapshot of a pipeline by ID.
func (m *PipelineManager) Get(id string) (*Pipeline, error) {
	m.mu.RLock()
	mp, ok := m.pipelines[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrPipelineNotFound
	}
	// Read via executor's lock.
	snap := mp.executor.Pipeline()
	return &snap, nil
}

// GetBySession returns pipeline IDs for a session.
func (m *PipelineManager) GetBySession(sessionID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := m.bySession[sessionID]
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// GetByDAG returns pipeline IDs for a DAG node.
func (m *PipelineManager) GetByDAG(dagNodeID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := m.byDAG[dagNodeID]
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// GetActive returns IDs of all non-terminal pipelines.
func (m *PipelineManager) GetActive() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var active []string
	for id, mp := range m.pipelines {
		snap := mp.executor.Pipeline()
		if !IsTerminalStatus(snap.Status) {
			active = append(active, id)
		}
	}
	return active
}

// RouteUserMessage sends a user override message to a specific pipeline.
func (m *PipelineManager) RouteUserMessage(ctx context.Context, id string, msg any) error {
	m.mu.RLock()
	mp, ok := m.pipelines[id]
	m.mu.RUnlock()
	if !ok {
		return ErrPipelineNotFound
	}
	return mp.bus.SendUserMessage(ctx, msg)
}

// GetResult builds a PipelineResult from the pipeline's current state.
func (m *PipelineManager) GetResult(id string) (*PipelineResult, error) {
	m.mu.RLock()
	mp, ok := m.pipelines[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrPipelineNotFound
	}

	p := mp.executor.Pipeline()

	var duration time.Duration
	if !p.CompletedAt.IsZero() {
		duration = p.CompletedAt.Sub(p.StartedAt)
	}

	return &PipelineResult{
		PipelineID:        p.ID,
		TaskID:            p.TaskID,
		Status:            p.Status,
		LoopCount:         p.LoopCount,
		InspectorCriteria: p.InspectorCriteria,
		TesterTests:       p.TesterTests,
		WorkerOutput:      p.WorkerOutput,
		InspectorResult:   p.InspectorResult,
		TesterResult:      p.TesterResult,
		CoWorkerOutputs:   p.CoWorkerOutputs,
		Duration:          duration,
		Error:             p.LastError,
	}, nil
}

// CloseAll cancels all active pipelines and marks the manager as closed.
func (m *PipelineManager) CloseAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true

	var toCancel []*managedPipeline
	for _, mp := range m.pipelines {
		snap := mp.executor.Pipeline()
		if !IsTerminalStatus(snap.Status) {
			toCancel = append(toCancel, mp)
		}
	}
	m.mu.Unlock()

	for _, mp := range toCancel {
		if mp.cancel != nil {
			mp.cancel()
		}
		_ = mp.executor.Stop()
		mp.executor.markCancelled()
		mp.executor.CloseCoWorkers()
		mp.bus.Close()
	}
}

func (m *PipelineManager) activeCountLocked() int {
	count := 0
	for _, mp := range m.pipelines {
		snap := mp.executor.Pipeline()
		if !IsTerminalStatus(snap.Status) {
			count++
		}
	}
	return count
}
