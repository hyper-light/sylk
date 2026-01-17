package errors

import (
	"context"
)

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
	results := make([]*LayerResult, 0)

	results = m.rollbackLayers(pipelineID, options, results)
	results = m.handleGitLayers(pipelineID, options, results)

	receipt := m.buildReceipt(pipelineID, results, options)
	m.recordToArchivalist(ctx, pipelineID, receipt)

	return receipt, nil
}

// rollbackLayers handles staging and agent state rollback.
func (m *RollbackManager) rollbackLayers(
	pipelineID string,
	options RollbackOptions,
	results []*LayerResult,
) []*LayerResult {
	if options.IncludeStaging {
		results = append(results, m.rollbackStaging(pipelineID))
	}

	if options.IncludeAgentState {
		results = append(results, m.rollbackAgentState(pipelineID))
	}

	return results
}

// handleGitLayers handles git-related rollback layers.
func (m *RollbackManager) handleGitLayers(
	pipelineID string,
	options RollbackOptions,
	results []*LayerResult,
) []*LayerResult {
	if options.IncludeGitLocal && m.config.GitEnabled {
		results = append(results, m.rollbackGitLocal(pipelineID))
	}

	if options.IncludeGitPushed {
		results = append(results, m.handleGitPushed(pipelineID, options))
	}

	return results
}

// rollbackStaging discards staged files for the pipeline.
func (m *RollbackManager) rollbackStaging(pipelineID string) *LayerResult {
	result := &LayerResult{Layer: LayerFileStaging, Success: true}

	if m.staging == nil {
		result.Message = "staging manager not configured"
		return result
	}

	count, err := m.staging.GetStagedFileCount(pipelineID)
	if err != nil {
		return m.stagingError(err)
	}

	if err := m.staging.DiscardStaging(pipelineID); err != nil {
		return m.stagingError(err)
	}

	result.ItemCount = count
	result.Message = "staged files discarded"
	return result
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
func (m *RollbackManager) rollbackAgentState(pipelineID string) *LayerResult {
	result := &LayerResult{Layer: LayerAgentState, Success: true}

	if m.checkpoint == nil {
		result.Message = "checkpoint manager not configured"
		return result
	}

	if err := m.checkpoint.RestoreCheckpoint(pipelineID); err != nil {
		result.Success = false
		result.Message = err.Error()
		return result
	}

	result.ItemCount = 1
	result.Message = "agent context restored"
	return result
}

// rollbackGitLocal resets local unpushed commits.
func (m *RollbackManager) rollbackGitLocal(pipelineID string) *LayerResult {
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

	if err := m.git.ResetSoft(pipelineID, count); err != nil {
		return m.gitLocalError(err)
	}

	result.ItemCount = count
	result.Message = "local commits reset"
	return result
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
