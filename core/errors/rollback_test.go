package errors

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/errors/mocks"
	"github.com/stretchr/testify/mock"
)

func TestRollbackManager_Rollback_AllLayers(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "pipeline-123").Return(5, nil)
	staging.On("DiscardStaging", "pipeline-123").Return(nil)

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "pipeline-123").Return(nil)

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "pipeline-123").Return(3, nil)
	git.On("ResetSoft", "pipeline-123", 3).Return(nil)
	git.On("HasPushedCommits", "pipeline-123").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-123", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
		IncludeGitLocal:   true,
		IncludeGitPushed:  false,
	}

	receipt, err := manager.Rollback(ctx, "pipeline-123", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if !receipt.WasSuccessful() {
		t.Error("WasSuccessful() = false, want true")
	}

	if receipt.StagedFilesCount != 5 {
		t.Errorf("StagedFilesCount = %d, want 5", receipt.StagedFilesCount)
	}

	if !receipt.AgentContextReset {
		t.Error("AgentContextReset = false, want true")
	}

	if receipt.LocalCommitsRemoved != 3 {
		t.Errorf("LocalCommitsRemoved = %d, want 3", receipt.LocalCommitsRemoved)
	}

}

func TestRollbackManager_Rollback_StagingOnly(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "pipeline-456").Return(10, nil)
	staging.On("DiscardStaging", "pipeline-456").Return(nil)

	checkpoint := mocks.NewCheckpointManager(t)

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "pipeline-456").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-456", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: false,
		IncludeGitLocal:   false,
		IncludeGitPushed:  false,
	}

	receipt, err := manager.Rollback(ctx, "pipeline-456", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if receipt.StagedFilesCount != 10 {
		t.Errorf("StagedFilesCount = %d, want 10", receipt.StagedFilesCount)
	}
}

func TestRollbackManager_Rollback_NeverAutoRevertPushed(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "pipeline-789").Return(2, nil)
	staging.On("DiscardStaging", "pipeline-789").Return(nil)

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "pipeline-789").Return(nil)

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "pipeline-789").Return(0, nil)
	git.On("HasPushedCommits", "pipeline-789").Return(true)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-789", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := DefaultRollbackOptions()

	receipt, err := manager.Rollback(ctx, "pipeline-789", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if !receipt.PushedCommitsPending {
		t.Error("PushedCommitsPending = false, want true")
	}

	if len(receipt.Warnings) == 0 {
		t.Error("should have warning about pushed commits")
	}

	hasWarning := false
	for _, w := range receipt.Warnings {
		if w == "pushed commits exist but were not reverted" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("missing expected warning about pushed commits")
	}
}

func TestRollbackManager_Rollback_WithForceGitPushed(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "pipeline-force").Return(1, nil)
	staging.On("DiscardStaging", "pipeline-force").Return(nil)

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "pipeline-force").Return(nil)

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "pipeline-force").Return(true)
	git.On("CreateRevertCommit", "pipeline-force").Return(nil)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-force", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
		IncludeGitLocal:   false,
		IncludeGitPushed:  true,
		ForceGitPushed:    true,
	}

	receipt, err := manager.Rollback(ctx, "pipeline-force", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if !receipt.WasSuccessful() {
		t.Error("WasSuccessful() = false, want true")
	}
}

func TestRollbackManager_Rollback_GitPushedWithoutForce(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "pipeline-no-force").Return(true)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-no-force", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{
		IncludeGitPushed: true,
		ForceGitPushed:   false,
	}

	receipt, err := manager.Rollback(ctx, "pipeline-no-force", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	var pushedResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitPushed {
			pushedResult = r
			break
		}
	}

	if pushedResult == nil {
		t.Fatal("missing git_pushed layer result")
	}

	if pushedResult.Message != "pushed commits require manual revert" {
		t.Errorf("Message = %s, want 'pushed commits require manual revert'", pushedResult.Message)
	}
}

func TestRollbackManager_Rollback_NoPushedCommits(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "pipeline-no-pushed").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "pipeline-no-pushed", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{
		IncludeGitPushed: true,
		ForceGitPushed:   true,
	}

	receipt, err := manager.Rollback(ctx, "pipeline-no-pushed", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	var pushedResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitPushed {
			pushedResult = r
			break
		}
	}

	if pushedResult == nil {
		t.Fatal("missing git_pushed layer result")
	}

	if pushedResult.Message != "no pushed commits to revert" {
		t.Errorf("Message = %s", pushedResult.Message)
	}
}

func TestRollbackManager_BuildReceipt(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "build-receipt-test").Return(7, nil)
	staging.On("DiscardStaging", "build-receipt-test").Return(nil)

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "build-receipt-test").Return(nil)

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "build-receipt-test").Return(2, nil)
	git.On("ResetSoft", "build-receipt-test", 2).Return(nil)
	git.On("HasPushedCommits", "build-receipt-test").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "build-receipt-test", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
		IncludeGitLocal:   true,
	}

	receipt, _ := manager.Rollback(ctx, "build-receipt-test", options)

	if receipt.PipelineID != "build-receipt-test" {
		t.Errorf("PipelineID = %s, want 'build-receipt-test'", receipt.PipelineID)
	}

	if len(receipt.LayerResults) != 3 {
		t.Errorf("LayerResults count = %d, want 3", len(receipt.LayerResults))
	}

	if receipt.StagedFilesCount != 7 {
		t.Errorf("StagedFilesCount = %d, want 7", receipt.StagedFilesCount)
	}

	if !receipt.AgentContextReset {
		t.Error("AgentContextReset = false, want true")
	}

	if receipt.LocalCommitsRemoved != 2 {
		t.Errorf("LocalCommitsRemoved = %d, want 2", receipt.LocalCommitsRemoved)
	}

	if !receipt.ArchivalistRecorded {
		t.Error("ArchivalistRecorded = false, want true")
	}
}

func TestRollbackManager_BuildReceipt_PartialFailure(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "partial-test").Return(0, errors.New("staging error"))

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "partial-test").Return(nil)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "partial-test", mock.Anything).Return(nil)

	config := RollbackConfig{}
	manager := NewRollbackManager(config, staging, checkpoint, nil, recorder)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
	}

	receipt, _ := manager.Rollback(ctx, "partial-test", options)

	if receipt.WasSuccessful() {
		t.Error("WasSuccessful() = true, want false (staging failed)")
	}
}

func TestRollbackOptions_Default(t *testing.T) {
	opts := DefaultRollbackOptions()

	if !opts.IncludeStaging {
		t.Error("IncludeStaging = false, want true")
	}
	if !opts.IncludeAgentState {
		t.Error("IncludeAgentState = false, want true")
	}
	if !opts.IncludeGitLocal {
		t.Error("IncludeGitLocal = false, want true")
	}
	if opts.IncludeGitPushed {
		t.Error("IncludeGitPushed = true, want false (never auto-revert)")
	}
	if opts.ForceGitPushed {
		t.Error("ForceGitPushed = true, want false")
	}
}

func TestRollbackManager_NilDependencies(t *testing.T) {
	ctx := context.Background()

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, nil, nil)

	options := RollbackOptions{
		IncludeStaging:    true,
		IncludeAgentState: true,
		IncludeGitLocal:   true,
		IncludeGitPushed:  true,
	}

	receipt, err := manager.Rollback(ctx, "nil-deps-test", options)
	if err != nil {
		t.Fatalf("Rollback() should not error with nil deps, got %v", err)
	}

	if receipt == nil {
		t.Fatal("receipt should not be nil")
	}

	if !receipt.WasSuccessful() {
		t.Error("WasSuccessful() = false, want true (graceful degradation)")
	}

	expectedMessages := map[RollbackLayer]string{
		LayerFileStaging: "staging manager not configured",
		LayerAgentState:  "checkpoint manager not configured",
		LayerGitLocal:    "git manager not configured",
		LayerGitPushed:   "git manager not configured",
	}

	for _, result := range receipt.LayerResults {
		expected, ok := expectedMessages[result.Layer]
		if !ok {
			continue
		}
		if result.Message != expected {
			t.Errorf("Layer %s message = %q, want %q", result.Layer, result.Message, expected)
		}
	}
}

func TestRollbackManager_StagingError(t *testing.T) {
	ctx := context.Background()

	staging := mocks.NewStagingManager(t)
	staging.On("GetStagedFileCount", "staging-err").Return(0, errors.New("count error"))

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "staging-err", mock.Anything).Return(nil)

	config := RollbackConfig{}
	manager := NewRollbackManager(config, staging, nil, nil, recorder)

	options := RollbackOptions{IncludeStaging: true}

	receipt, _ := manager.Rollback(ctx, "staging-err", options)

	if receipt.WasSuccessful() {
		t.Error("WasSuccessful() = true, want false")
	}

	var stagingResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerFileStaging {
			stagingResult = r
			break
		}
	}

	if stagingResult == nil {
		t.Fatal("missing staging layer result")
	}

	if stagingResult.Success {
		t.Error("staging result Success = true, want false")
	}

	if stagingResult.Message != "count error" {
		t.Errorf("staging result Message = %q, want 'count error'", stagingResult.Message)
	}
}

func TestRollbackManager_GitLocalError(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "git-err").Return(0, errors.New("git error"))
	git.On("HasPushedCommits", "git-err").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "git-err", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "git-err", options)

	var gitResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitLocal {
			gitResult = r
			break
		}
	}

	if gitResult == nil {
		t.Fatal("missing git_local layer result")
	}

	if gitResult.Success {
		t.Error("git result Success = true, want false")
	}

	if gitResult.Message != "git error" {
		t.Errorf("git result Message = %q, want 'git error'", gitResult.Message)
	}
}

func TestRollbackManager_GitResetSoftError(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "reset-err").Return(3, nil)
	git.On("ResetSoft", "reset-err", 3).Return(errors.New("reset failed"))
	git.On("HasPushedCommits", "reset-err").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "reset-err", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "reset-err", options)

	var gitResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitLocal {
			gitResult = r
			break
		}
	}

	if gitResult.Success {
		t.Error("Success = true, want false")
	}

	if gitResult.Message != "reset failed" {
		t.Errorf("Message = %q, want 'reset failed'", gitResult.Message)
	}
}

func TestRollbackManager_GitCreateRevertError(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "revert-err").Return(true)
	git.On("CreateRevertCommit", "revert-err").Return(errors.New("revert failed"))

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "revert-err", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{
		IncludeGitPushed: true,
		ForceGitPushed:   true,
	}

	receipt, _ := manager.Rollback(ctx, "revert-err", options)

	var pushedResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitPushed {
			pushedResult = r
			break
		}
	}

	if pushedResult.Success {
		t.Error("Success = true, want false")
	}

	if pushedResult.Message != "revert failed" {
		t.Errorf("Message = %q, want 'revert failed'", pushedResult.Message)
	}
}

func TestRollbackManager_CheckpointError(t *testing.T) {
	ctx := context.Background()

	checkpoint := mocks.NewCheckpointManager(t)
	checkpoint.On("RestoreCheckpoint", "checkpoint-err").Return(errors.New("checkpoint failed"))

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "checkpoint-err", mock.Anything).Return(nil)

	config := RollbackConfig{}
	manager := NewRollbackManager(config, nil, checkpoint, nil, recorder)

	options := RollbackOptions{IncludeAgentState: true}

	receipt, _ := manager.Rollback(ctx, "checkpoint-err", options)

	var agentResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerAgentState {
			agentResult = r
			break
		}
	}

	if agentResult == nil {
		t.Fatal("missing agent_state layer result")
	}

	if agentResult.Success {
		t.Error("Success = true, want false")
	}

	if agentResult.Message != "checkpoint failed" {
		t.Errorf("Message = %q, want 'checkpoint failed'", agentResult.Message)
	}
}

func TestRollbackManager_RecorderError(t *testing.T) {
	ctx := context.Background()

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "recorder-err", mock.Anything).Return(errors.New("record failed"))

	config := RollbackConfig{}
	manager := NewRollbackManager(config, nil, nil, nil, recorder)

	options := RollbackOptions{}

	receipt, _ := manager.Rollback(ctx, "recorder-err", options)

	if receipt.ArchivalistRecorded {
		t.Error("ArchivalistRecorded = true, want false")
	}

	hasWarning := false
	for _, w := range receipt.Warnings {
		if w == "failed to record rollback: record failed" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("missing expected warning, got: %v", receipt.Warnings)
	}
}

func TestRollbackManager_GitDisabled(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("HasPushedCommits", "git-disabled").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "git-disabled", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: false}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "git-disabled", options)

	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitLocal {
			t.Error("git_local layer should not be included when git is disabled")
		}
	}
}

func TestRollbackManager_NoUnpushedCommits(t *testing.T) {
	ctx := context.Background()

	git := mocks.NewGitManager(t)
	git.On("GetUnpushedCommits", "no-unpushed").Return(0, nil)
	git.On("HasPushedCommits", "no-unpushed").Return(false)

	recorder := mocks.NewRollbackRecorder(t)
	recorder.On("RecordRollback", mock.Anything, "no-unpushed", mock.Anything).Return(nil)

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "no-unpushed", options)

	var gitResult *LayerResult
	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitLocal {
			gitResult = r
			break
		}
	}

	if gitResult == nil {
		t.Fatal("missing git_local result")
	}

	if gitResult.Message != "no unpushed commits" {
		t.Errorf("Message = %q, want 'no unpushed commits'", gitResult.Message)
	}

	if !gitResult.Success {
		t.Error("Success = false, want true")
	}
}
