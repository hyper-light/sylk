package errors

import (
	"context"
	"sync"
)

// IntermediateResource represents a resource created during rollback.
type IntermediateResource struct {
	Layer       RollbackLayer
	ResourceID  string
	Description string
	Cleanup     func() error
}

// ResourceTracker tracks intermediate resources for cleanup on failure.
type ResourceTracker struct {
	mu        sync.Mutex
	resources []*IntermediateResource
}

// NewResourceTracker creates a new resource tracker.
func NewResourceTracker() *ResourceTracker {
	return &ResourceTracker{
		resources: make([]*IntermediateResource, 0),
	}
}

// Track adds a resource to be cleaned up on failure.
func (rt *ResourceTracker) Track(res *IntermediateResource) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.resources = append(rt.resources, res)
}

// CleanupAll cleans up all tracked resources in reverse order.
func (rt *ResourceTracker) CleanupAll() []error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	errors := make([]error, 0)
	for i := len(rt.resources) - 1; i >= 0; i-- {
		if rt.resources[i].Cleanup != nil {
			if err := rt.resources[i].Cleanup(); err != nil {
				errors = append(errors, err)
			}
		}
	}
	rt.resources = rt.resources[:0]
	return errors
}

// Clear removes all tracked resources without cleanup.
func (rt *ResourceTracker) Clear() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.resources = rt.resources[:0]
}

// Resources returns a copy of tracked resources.
func (rt *ResourceTracker) Resources() []*IntermediateResource {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	result := make([]*IntermediateResource, len(rt.resources))
	copy(result, rt.resources)
	return result
}

// RollbackOptions configures which layers to include in a rollback operation.
type RollbackOptions struct {
	IncludeStaging    bool
	IncludeAgentState bool
	IncludeGitLocal   bool
	IncludeGitPushed  bool // requires explicit opt-in, never auto-revert
	ForceGitPushed    bool // dangerous, requires confirmation
}

// DefaultRollbackOptions returns safe default options.
func DefaultRollbackOptions() RollbackOptions {
	return RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
		IncludeGitLocal:   true,
		IncludeGitPushed:  false,
		ForceGitPushed:    false,
	}
}

// RollbackConfig contains configuration for the rollback manager.
type RollbackConfig struct {
	StagingDir string `yaml:"staging_dir"`
	GitEnabled bool   `yaml:"git_enabled"`
}

// StagingManager handles staged file operations.
type StagingManager interface {
	DiscardStaging(pipelineID string) error
	GetStagedFileCount(pipelineID string) (int, error)
}

// CheckpointManager handles agent state checkpoints.
type CheckpointManager interface {
	RestoreCheckpoint(pipelineID string) error
}

// GitManager handles git operations for rollback.
type GitManager interface {
	GetUnpushedCommits(pipelineID string) (int, error)
	ResetSoft(pipelineID string, count int) error
	HasPushedCommits(pipelineID string) bool
	CreateRevertCommit(pipelineID string) error
}

// RollbackRecorder records rollback events to archivalist.
type RollbackRecorder interface {
	RecordRollback(ctx context.Context, pipelineID string, status string) error
}

// RollbackManager orchestrates rollback operations across multiple layers.
type RollbackManager struct {
	config     RollbackConfig
	staging    StagingManager
	checkpoint CheckpointManager
	git        GitManager
	recorder   RollbackRecorder
}

// NewRollbackManager creates a new RollbackManager with the given dependencies.
func NewRollbackManager(
	config RollbackConfig,
	staging StagingManager,
	checkpoint CheckpointManager,
	git GitManager,
	recorder RollbackRecorder,
) *RollbackManager {
	return &RollbackManager{
		config:     config,
		staging:    staging,
		checkpoint: checkpoint,
		git:        git,
		recorder:   recorder,
	}
}

// Rollback performs a rollback operation for the given pipeline.
func (m *RollbackManager) Rollback(
	ctx context.Context,
	pipelineID string,
	options RollbackOptions,
) (*RollbackReceipt, error) {
	tracker := NewResourceTracker()
	results := make([]*LayerResult, 0)

	results, failed := m.rollbackWithTracking(pipelineID, options, results, tracker)
	if failed {
		m.handlePartialFailure(tracker)
	}

	receipt := m.buildReceipt(pipelineID, results, options)
	m.addCleanupWarnings(receipt, tracker)
	m.recordToArchivalist(ctx, pipelineID, receipt)
	tracker.Clear()

	return receipt, nil
}

// rollbackWithTracking executes rollback with resource tracking.
func (m *RollbackManager) rollbackWithTracking(
	pipelineID string,
	options RollbackOptions,
	results []*LayerResult,
	tracker *ResourceTracker,
) ([]*LayerResult, bool) {
	results, failed := m.rollbackLayers(pipelineID, options, results, tracker)
	if failed {
		return results, true
	}
	return m.handleGitLayers(pipelineID, options, results, tracker)
}

// handlePartialFailure cleans up intermediate resources on failure.
func (m *RollbackManager) handlePartialFailure(tracker *ResourceTracker) {
	tracker.CleanupAll()
}

// addCleanupWarnings adds warnings for any tracked resources.
func (m *RollbackManager) addCleanupWarnings(
	receipt *RollbackReceipt,
	tracker *ResourceTracker,
) {
	resources := tracker.Resources()
	for _, res := range resources {
		receipt.AddWarning("intermediate resource: " + res.Description)
	}
}

// rollbackLayers handles staging and agent state rollback.
func (m *RollbackManager) rollbackLayers(
	pipelineID string,
	options RollbackOptions,
	results []*LayerResult,
	tracker *ResourceTracker,
) ([]*LayerResult, bool) {
	if options.IncludeStaging {
		result := m.rollbackStaging(pipelineID, tracker)
		results = append(results, result)
		if !result.Success {
			return results, true
		}
	}

	if options.IncludeAgentState {
		result := m.rollbackAgentState(pipelineID, tracker)
		results = append(results, result)
		if !result.Success {
			return results, true
		}
	}

	return results, false
}

// handleGitLayers handles git-related rollback layers.
func (m *RollbackManager) handleGitLayers(
	pipelineID string,
	options RollbackOptions,
	results []*LayerResult,
	tracker *ResourceTracker,
) ([]*LayerResult, bool) {
	if options.IncludeGitLocal && m.config.GitEnabled {
		result := m.rollbackGitLocal(pipelineID, tracker)
		results = append(results, result)
		if !result.Success {
			return results, true
		}
	}

	if options.IncludeGitPushed {
		results = append(results, m.handleGitPushed(pipelineID, options))
	}

	return results, false
}

// rollbackStaging discards staged files for the pipeline.
func (m *RollbackManager) rollbackStaging(
	pipelineID string,
	tracker *ResourceTracker,
) *LayerResult {
	result := &LayerResult{Layer: LayerFileStaging, Success: true}

	if m.staging == nil {
		result.Message = "staging manager not configured"
		return result
	}

	count, err := m.staging.GetStagedFileCount(pipelineID)
	if err != nil {
		return m.stagingError(err)
	}

	m.trackStagingResource(pipelineID, count, tracker)

	if err := m.staging.DiscardStaging(pipelineID); err != nil {
		return m.stagingError(err)
	}

	result.ItemCount = count
	result.Message = "staged files discarded"
	return result
}

// trackStagingResource tracks staging operation for cleanup.
func (m *RollbackManager) trackStagingResource(
	pipelineID string,
	count int,
	tracker *ResourceTracker,
) {
	if count == 0 {
		return
	}
	tracker.Track(&IntermediateResource{
		Layer:       LayerFileStaging,
		ResourceID:  pipelineID,
		Description: "staging discard in progress",
		Cleanup:     nil, // No rollback for staging discard
	})
}

// stagingError creates a failed layer result for staging errors.
func (m *RollbackManager) stagingError(err error) *LayerResult {
	return &LayerResult{
		Layer:   LayerFileStaging,
		Success: false,
		Message: err.Error(),
	}
}

// rollbackAgentState restores agent checkpoint for the pipeline.
func (m *RollbackManager) rollbackAgentState(
	pipelineID string,
	tracker *ResourceTracker,
) *LayerResult {
	result := &LayerResult{Layer: LayerAgentState, Success: true}

	if m.checkpoint == nil {
		result.Message = "checkpoint manager not configured"
		return result
	}

	m.trackAgentStateResource(pipelineID, tracker)

	if err := m.checkpoint.RestoreCheckpoint(pipelineID); err != nil {
		result.Success = false
		result.Message = err.Error()
		return result
	}

	result.ItemCount = 1
	result.Message = "agent context restored"
	return result
}

// trackAgentStateResource tracks checkpoint restore for cleanup.
func (m *RollbackManager) trackAgentStateResource(
	pipelineID string,
	tracker *ResourceTracker,
) {
	tracker.Track(&IntermediateResource{
		Layer:       LayerAgentState,
		ResourceID:  pipelineID,
		Description: "checkpoint restore in progress",
		Cleanup:     nil, // Checkpoints are idempotent
	})
}

// rollbackGitLocal resets local unpushed commits.
func (m *RollbackManager) rollbackGitLocal(
	pipelineID string,
	tracker *ResourceTracker,
) *LayerResult {
	result := &LayerResult{Layer: LayerGitLocal, Success: true}

	if m.git == nil {
		result.Message = "git manager not configured"
		return result
	}

	count, err := m.git.GetUnpushedCommits(pipelineID)
	if err != nil {
		return m.gitLocalError(err)
	}

	if count == 0 {
		result.Message = "no unpushed commits"
		return result
	}

	m.trackGitLocalResource(pipelineID, count, tracker)

	if err := m.git.ResetSoft(pipelineID, count); err != nil {
		return m.gitLocalError(err)
	}

	result.ItemCount = count
	result.Message = "local commits reset"
	return result
}

// trackGitLocalResource tracks git reset for cleanup.
func (m *RollbackManager) trackGitLocalResource(
	pipelineID string,
	count int,
	tracker *ResourceTracker,
) {
	tracker.Track(&IntermediateResource{
		Layer:       LayerGitLocal,
		ResourceID:  pipelineID,
		Description: "git reset in progress",
		Cleanup:     nil, // Git reset is atomic
	})
}

// gitLocalError creates a failed layer result for git local errors.
func (m *RollbackManager) gitLocalError(err error) *LayerResult {
	return &LayerResult{
		Layer:   LayerGitLocal,
		Success: false,
		Message: err.Error(),
	}
}

// handleGitPushed handles pushed commits (requires user action).
func (m *RollbackManager) handleGitPushed(
	pipelineID string,
	options RollbackOptions,
) *LayerResult {
	result := &LayerResult{Layer: LayerGitPushed, Success: true}

	if m.git == nil {
		result.Message = "git manager not configured"
		return result
	}

	if !m.git.HasPushedCommits(pipelineID) {
		result.Message = "no pushed commits to revert"
		return result
	}

	if !options.ForceGitPushed {
		result.Message = "pushed commits require manual revert"
		return result
	}

	if err := m.git.CreateRevertCommit(pipelineID); err != nil {
		result.Success = false
		result.Message = err.Error()
		return result
	}

	result.ItemCount = 1
	result.Message = "revert commit created"
	return result
}

// buildReceipt constructs the rollback receipt from results.
func (m *RollbackManager) buildReceipt(
	pipelineID string,
	results []*LayerResult,
	options RollbackOptions,
) *RollbackReceipt {
	receipt := NewRollbackReceipt(pipelineID)

	for _, result := range results {
		receipt.AddLayerResult(result)
		m.updateReceiptFromResult(receipt, result)
	}

	m.checkPushedCommitsPending(receipt, options)
	return receipt
}

// updateReceiptFromResult updates receipt statistics from a layer result.
func (m *RollbackManager) updateReceiptFromResult(
	receipt *RollbackReceipt,
	result *LayerResult,
) {
	switch result.Layer {
	case LayerFileStaging:
		receipt.StagedFilesCount = result.ItemCount
	case LayerAgentState:
		receipt.AgentContextReset = result.Success
	case LayerGitLocal:
		receipt.LocalCommitsRemoved = result.ItemCount
	}
}

// checkPushedCommitsPending sets the pending flag if pushed commits exist.
func (m *RollbackManager) checkPushedCommitsPending(
	receipt *RollbackReceipt,
	options RollbackOptions,
) {
	if m.git == nil {
		return
	}

	if !options.IncludeGitPushed && m.git.HasPushedCommits(receipt.PipelineID) {
		receipt.PushedCommitsPending = true
		receipt.AddWarning("pushed commits exist but were not reverted")
	}
}

// recordToArchivalist records the rollback to the archivalist service.
func (m *RollbackManager) recordToArchivalist(
	ctx context.Context,
	pipelineID string,
	receipt *RollbackReceipt,
) {
	if m.recorder == nil {
		return
	}

	status := "rolled_back"
	if !receipt.WasSuccessful() {
		status = "rollback_partial"
	}

	if err := m.recorder.RecordRollback(ctx, pipelineID, status); err != nil {
		receipt.AddWarning("failed to record rollback: " + err.Error())
		return
	}

	receipt.ArchivalistRecorded = true
}
