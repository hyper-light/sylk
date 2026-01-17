package tools

import (
	"context"
	"sync"
	"time"
)

// =============================================================================
// Configuration
// =============================================================================

const (
	DefaultTotalBudget   = 30 * time.Second
	DefaultAgentBudget   = 25 * time.Second
	DefaultToolBudget    = 20 * time.Second
	DefaultCleanupBudget = 5 * time.Second
)

// CancellationConfig holds timeout budgets for cascading cancellation
type CancellationConfig struct {
	TotalBudget   time.Duration
	AgentBudget   time.Duration
	ToolBudget    time.Duration
	CleanupBudget time.Duration
}

// DefaultCancellationConfig returns sensible defaults
func DefaultCancellationConfig() CancellationConfig {
	return CancellationConfig{
		TotalBudget:   DefaultTotalBudget,
		AgentBudget:   DefaultAgentBudget,
		ToolBudget:    DefaultToolBudget,
		CleanupBudget: DefaultCleanupBudget,
	}
}

func normalizeCancellationConfig(cfg CancellationConfig) CancellationConfig {
	cfg.TotalBudget = defaultCancelDuration(cfg.TotalBudget, DefaultTotalBudget)
	cfg.AgentBudget = defaultCancelDuration(cfg.AgentBudget, DefaultAgentBudget)
	cfg.ToolBudget = defaultCancelDuration(cfg.ToolBudget, DefaultToolBudget)
	cfg.CleanupBudget = defaultCancelDuration(cfg.CleanupBudget, DefaultCleanupBudget)
	return cfg
}

func defaultCancelDuration(val, def time.Duration) time.Duration {
	if val == 0 {
		return def
	}
	return val
}

// =============================================================================
// Cancellation Stage
// =============================================================================

// CancellationStage represents the current stage of cancellation
type CancellationStage string

const (
	StageIdle          CancellationStage = "idle"
	StageCancelling    CancellationStage = "cancelling"
	StageStopping      CancellationStage = "stopping"
	StageForceStopping CancellationStage = "force_stopping"
	StageKilling       CancellationStage = "killing"
	StageCleanup       CancellationStage = "cleanup"
	StageComplete      CancellationStage = "complete"
)

// =============================================================================
// Running Tool
// =============================================================================

// RunningTool tracks a tool that can be cancelled
type RunningTool struct {
	ID           string
	Name         string
	ProcessGroup *ProcessGroup
	StartedAt    time.Time
	PartialOut   []byte
	PartialErr   []byte
	waitDone     chan struct{}
}

// =============================================================================
// Cancellation Result
// =============================================================================

// CancellationResult contains the outcome of a cancellation operation
type CancellationResult struct {
	ToolsCancelled int
	ToolsKilled    int
	CleanupsDone   int
	PartialResults map[string]*ToolResult
	Duration       time.Duration
	Errors         []error
}

// =============================================================================
// Progress Callback
// =============================================================================

// CancellationProgress represents progress during cancellation
type CancellationProgress struct {
	Stage   CancellationStage
	ToolID  string
	Message string
}

// CancellationProgressCallback is called during cancellation progress
type CancellationProgressCallback func(CancellationProgress)

// =============================================================================
// Cleanup Handler
// =============================================================================

// CleanupHandler performs cleanup after tool cancellation
type CleanupHandler func(ctx context.Context, toolID string) error

// =============================================================================
// Cancellation Manager
// =============================================================================

// CancellationManager handles cascading cancellation of running tools
type CancellationManager struct {
	config       CancellationConfig
	killSequence *KillSequenceManager

	mu           sync.Mutex
	runningTools map[string]*RunningTool
	stage        CancellationStage
	onProgress   CancellationProgressCallback
	cleanups     []CleanupHandler
}

// NewCancellationManager creates a new cancellation manager
func NewCancellationManager(cfg CancellationConfig) *CancellationManager {
	cfg = normalizeCancellationConfig(cfg)

	return &CancellationManager{
		config:       cfg,
		killSequence: NewKillSequenceManager(DefaultKillSequenceConfig()),
		runningTools: make(map[string]*RunningTool),
		stage:        StageIdle,
	}
}

// =============================================================================
// Tool Registration
// =============================================================================

// RegisterTool registers a running tool for cancellation tracking
func (m *CancellationManager) RegisterTool(id, name string, pg *ProcessGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()

	waitDone := make(chan struct{})
	m.runningTools[id] = &RunningTool{
		ID:           id,
		Name:         name,
		ProcessGroup: pg,
		StartedAt:    time.Now(),
		waitDone:     waitDone,
	}
}

// UnregisterTool removes a tool from cancellation tracking
func (m *CancellationManager) UnregisterTool(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.runningTools, id)
}

// SetPartialOutput sets partial output for a tool
func (m *CancellationManager) SetPartialOutput(id string, stdout, stderr []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tool, ok := m.runningTools[id]; ok {
		tool.PartialOut = stdout
		tool.PartialErr = stderr
	}
}

// =============================================================================
// Configuration
// =============================================================================

// SetProgressCallback sets the progress callback
func (m *CancellationManager) SetProgressCallback(cb CancellationProgressCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onProgress = cb
}

// AddCleanupHandler adds a cleanup handler
func (m *CancellationManager) AddCleanupHandler(handler CleanupHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups = append(m.cleanups, handler)
}

// =============================================================================
// Cancellation
// =============================================================================

// Cancel cancels all running tools with cascading timeouts
func (m *CancellationManager) Cancel(ctx context.Context) *CancellationResult {
	startTime := time.Now()
	result := &CancellationResult{
		PartialResults: make(map[string]*ToolResult),
	}

	m.setStage(StageCancelling)
	m.notifyProgress(StageCancelling, "", "Cancelling...")

	tools := m.getRunningTools()
	if len(tools) == 0 {
		m.setStage(StageComplete)
		result.Duration = time.Since(startTime)
		return result
	}

	m.cancelAllTools(ctx, tools, result)
	m.runCleanupPhase(ctx, tools, result)

	m.setStage(StageComplete)
	result.Duration = time.Since(startTime)
	return result
}

func (m *CancellationManager) getRunningTools() []*RunningTool {
	m.mu.Lock()
	defer m.mu.Unlock()

	tools := make([]*RunningTool, 0, len(m.runningTools))
	for _, tool := range m.runningTools {
		tools = append(tools, tool)
	}
	return tools
}

func (m *CancellationManager) cancelAllTools(ctx context.Context, tools []*RunningTool, result *CancellationResult) {
	var wg sync.WaitGroup

	for _, tool := range tools {
		wg.Add(1)
		go func(t *RunningTool) {
			defer wg.Done()
			m.cancelSingleTool(ctx, t, result)
		}(tool)
	}

	wg.Wait()
}

func (m *CancellationManager) cancelSingleTool(ctx context.Context, tool *RunningTool, result *CancellationResult) {
	m.notifyProgress(StageStopping, tool.ID, "Stopping "+tool.Name+"...")

	toolCtx, cancel := context.WithTimeout(ctx, m.config.ToolBudget)
	defer cancel()

	killResult := m.executeKillSequence(toolCtx, tool)
	m.recordToolResult(tool, killResult, result)
}

func (m *CancellationManager) executeKillSequence(ctx context.Context, tool *RunningTool) KillResult {
	waitDone := make(chan struct{})
	go m.monitorToolExit(tool, waitDone)

	resultChan := make(chan KillResult, 1)
	go func() {
		resultChan <- m.killSequence.ExecuteWithProgress(
			tool.ProcessGroup,
			waitDone,
			m.createKillProgressCallback(tool),
		)
	}()

	return m.waitForKillResult(ctx, resultChan)
}

func (m *CancellationManager) monitorToolExit(tool *RunningTool, waitDone chan<- struct{}) {
	if tool.ProcessGroup != nil {
		tool.ProcessGroup.Wait()
	}
	close(waitDone)
}

func (m *CancellationManager) createKillProgressCallback(tool *RunningTool) ProgressCallback {
	return func(stage string) {
		switch stage {
		case "stopping":
			m.notifyProgress(StageStopping, tool.ID, "Stopping "+tool.Name+"...")
		case "force_stopping":
			m.notifyProgress(StageForceStopping, tool.ID, "Force stopping "+tool.Name+"...")
		case "killed":
			m.notifyProgress(StageKilling, tool.ID, "Killed "+tool.Name)
		}
	}
}

func (m *CancellationManager) waitForKillResult(ctx context.Context, resultChan <-chan KillResult) KillResult {
	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return KillResult{SentSIGKILL: true, ExitedAfter: "timeout"}
	}
}

func (m *CancellationManager) recordToolResult(tool *RunningTool, killResult KillResult, result *CancellationResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result.ToolsCancelled++
	if killResult.SentSIGKILL {
		result.ToolsKilled++
	}

	result.PartialResults[tool.ID] = &ToolResult{
		ExitCode:   -1,
		Stdout:     tool.PartialOut,
		Stderr:     tool.PartialErr,
		Killed:     true,
		KillSignal: killResult.ExitedAfter,
		Partial:    true,
	}
}

// =============================================================================
// Cleanup Phase
// =============================================================================

func (m *CancellationManager) runCleanupPhase(ctx context.Context, tools []*RunningTool, result *CancellationResult) {
	m.setStage(StageCleanup)
	m.notifyProgress(StageCleanup, "", "Running cleanup...")

	cleanupCtx, cancel := context.WithTimeout(ctx, m.config.CleanupBudget)
	defer cancel()

	handlers := m.getCleanupHandlers()
	m.executeCleanups(cleanupCtx, tools, handlers, result)
}

func (m *CancellationManager) getCleanupHandlers() []CleanupHandler {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]CleanupHandler{}, m.cleanups...)
}

func (m *CancellationManager) executeCleanups(ctx context.Context, tools []*RunningTool, handlers []CleanupHandler, result *CancellationResult) {
	for _, tool := range tools {
		m.executeToolCleanups(ctx, tool, handlers, result)
	}
}

func (m *CancellationManager) executeToolCleanups(ctx context.Context, tool *RunningTool, handlers []CleanupHandler, result *CancellationResult) {
	for _, handler := range handlers {
		if err := handler(ctx, tool.ID); err != nil {
			m.mu.Lock()
			result.Errors = append(result.Errors, err)
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			result.CleanupsDone++
			m.mu.Unlock()
		}
	}
}

// =============================================================================
// State Management
// =============================================================================

func (m *CancellationManager) setStage(stage CancellationStage) {
	m.mu.Lock()
	m.stage = stage
	m.mu.Unlock()
}

// Stage returns the current cancellation stage
func (m *CancellationManager) Stage() CancellationStage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stage
}

func (m *CancellationManager) notifyProgress(stage CancellationStage, toolID, message string) {
	m.mu.Lock()
	cb := m.onProgress
	m.mu.Unlock()

	if cb != nil {
		cb(CancellationProgress{Stage: stage, ToolID: toolID, Message: message})
	}
}

// =============================================================================
// State Accessors
// =============================================================================

// RunningToolCount returns the number of running tools
func (m *CancellationManager) RunningToolCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runningTools)
}

// IsIdle returns true if no cancellation is in progress
func (m *CancellationManager) IsIdle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stage == StageIdle || m.stage == StageComplete
}

// Config returns the cancellation configuration
func (m *CancellationManager) Config() CancellationConfig {
	return m.config
}
