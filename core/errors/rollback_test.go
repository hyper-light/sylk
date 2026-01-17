package errors

import (
	"context"
	"errors"
	"testing"
)

type mockStagingManager struct {
	stagedCount   int
	discardErr    error
	getCountErr   error
	discardCalls  int
	getCountCalls int
}

func (m *mockStagingManager) DiscardStaging(pipelineID string) error {
	m.discardCalls++
	return m.discardErr
}

func (m *mockStagingManager) GetStagedFileCount(pipelineID string) (int, error) {
	m.getCountCalls++
	if m.getCountErr != nil {
		return 0, m.getCountErr
	}
	return m.stagedCount, nil
}

type mockCheckpointManager struct {
	restoreErr   error
	restoreCalls int
}

func (m *mockCheckpointManager) RestoreCheckpoint(pipelineID string) error {
	m.restoreCalls++
	return m.restoreErr
}

type mockGitManager struct {
	unpushedCount     int
	hasPushed         bool
	getUnpushedErr    error
	resetSoftErr      error
	createRevertErr   error
	getUnpushedCalls  int
	resetSoftCalls    int
	hasPushedCalls    int
	createRevertCalls int
}

func (m *mockGitManager) GetUnpushedCommits(pipelineID string) (int, error) {
	m.getUnpushedCalls++
	if m.getUnpushedErr != nil {
		return 0, m.getUnpushedErr
	}
	return m.unpushedCount, nil
}

func (m *mockGitManager) ResetSoft(pipelineID string, count int) error {
	m.resetSoftCalls++
	return m.resetSoftErr
}

func (m *mockGitManager) HasPushedCommits(pipelineID string) bool {
	m.hasPushedCalls++
	return m.hasPushed
}

func (m *mockGitManager) CreateRevertCommit(pipelineID string) error {
	m.createRevertCalls++
	return m.createRevertErr
}

type mockRollbackRecorder struct {
	recordErr   error
	recordCalls int
	lastStatus  string
}

func (m *mockRollbackRecorder) RecordRollback(ctx context.Context, pipelineID string, status string) error {
	m.recordCalls++
	m.lastStatus = status
	return m.recordErr
}

func TestRollbackManager_Rollback_AllLayers(t *testing.T) {
	ctx := context.Background()

	staging := &mockStagingManager{stagedCount: 5}
	checkpoint := &mockCheckpointManager{}
	git := &mockGitManager{unpushedCount: 3}
	recorder := &mockRollbackRecorder{}

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

	if staging.discardCalls != 1 {
		t.Errorf("staging.discardCalls = %d, want 1", staging.discardCalls)
	}

	if checkpoint.restoreCalls != 1 {
		t.Errorf("checkpoint.restoreCalls = %d, want 1", checkpoint.restoreCalls)
	}

	if git.resetSoftCalls != 1 {
		t.Errorf("git.resetSoftCalls = %d, want 1", git.resetSoftCalls)
	}

	if recorder.recordCalls != 1 {
		t.Errorf("recorder.recordCalls = %d, want 1", recorder.recordCalls)
	}

	if recorder.lastStatus != "rolled_back" {
		t.Errorf("recorder.lastStatus = %s, want 'rolled_back'", recorder.lastStatus)
	}
}

func TestRollbackManager_Rollback_StagingOnly(t *testing.T) {
	ctx := context.Background()

	staging := &mockStagingManager{stagedCount: 10}
	checkpoint := &mockCheckpointManager{}
	git := &mockGitManager{unpushedCount: 5}
	recorder := &mockRollbackRecorder{}

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

	if staging.discardCalls != 1 {
		t.Errorf("staging.discardCalls = %d, want 1", staging.discardCalls)
	}

	if checkpoint.restoreCalls != 0 {
		t.Errorf("checkpoint.restoreCalls = %d, want 0", checkpoint.restoreCalls)
	}

	if git.resetSoftCalls != 0 {
		t.Errorf("git.resetSoftCalls = %d, want 0", git.resetSoftCalls)
	}
}

func TestRollbackManager_Rollback_NeverAutoRevertPushed(t *testing.T) {
	ctx := context.Background()

	staging := &mockStagingManager{stagedCount: 2}
	checkpoint := &mockCheckpointManager{}
	git := &mockGitManager{hasPushed: true}
	recorder := &mockRollbackRecorder{}

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, staging, checkpoint, git, recorder)

	options := DefaultRollbackOptions()

	receipt, err := manager.Rollback(ctx, "pipeline-789", options)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if git.createRevertCalls != 0 {
		t.Errorf("git.createRevertCalls = %d, want 0 (should not auto-revert)", git.createRevertCalls)
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

	staging := &mockStagingManager{stagedCount: 1}
	checkpoint := &mockCheckpointManager{}
	git := &mockGitManager{hasPushed: true}
	recorder := &mockRollbackRecorder{}

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

	if git.createRevertCalls != 1 {
		t.Errorf("git.createRevertCalls = %d, want 1", git.createRevertCalls)
	}

	if !receipt.WasSuccessful() {
		t.Error("WasSuccessful() = false, want true")
	}
}

func TestRollbackManager_Rollback_GitPushedWithoutForce(t *testing.T) {
	ctx := context.Background()

	git := &mockGitManager{hasPushed: true}
	recorder := &mockRollbackRecorder{}

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

	if git.createRevertCalls != 0 {
		t.Errorf("git.createRevertCalls = %d, want 0 (no force)", git.createRevertCalls)
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

	git := &mockGitManager{hasPushed: false}
	recorder := &mockRollbackRecorder{}

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

	if git.createRevertCalls != 0 {
		t.Errorf("createRevertCalls = %d, want 0 (no pushed commits)", git.createRevertCalls)
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

	staging := &mockStagingManager{stagedCount: 7}
	checkpoint := &mockCheckpointManager{}
	git := &mockGitManager{unpushedCount: 2}
	recorder := &mockRollbackRecorder{}

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

	staging := &mockStagingManager{discardErr: errors.New("staging error")}
	checkpoint := &mockCheckpointManager{}
	recorder := &mockRollbackRecorder{}

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

	if recorder.lastStatus != "rollback_partial" {
		t.Errorf("lastStatus = %s, want 'rollback_partial'", recorder.lastStatus)
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

	staging := &mockStagingManager{
		getCountErr: errors.New("count error"),
	}
	recorder := &mockRollbackRecorder{}

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

	git := &mockGitManager{getUnpushedErr: errors.New("git error")}
	recorder := &mockRollbackRecorder{}

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

	git := &mockGitManager{
		unpushedCount: 3,
		resetSoftErr:  errors.New("reset failed"),
	}
	recorder := &mockRollbackRecorder{}

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

	git := &mockGitManager{
		hasPushed:       true,
		createRevertErr: errors.New("revert failed"),
	}
	recorder := &mockRollbackRecorder{}

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

	checkpoint := &mockCheckpointManager{restoreErr: errors.New("checkpoint failed")}
	recorder := &mockRollbackRecorder{}

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

	recorder := &mockRollbackRecorder{recordErr: errors.New("record failed")}

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

	git := &mockGitManager{unpushedCount: 5}
	recorder := &mockRollbackRecorder{}

	config := RollbackConfig{GitEnabled: false}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "git-disabled", options)

	if git.getUnpushedCalls != 0 {
		t.Errorf("getUnpushedCalls = %d, want 0 (git disabled)", git.getUnpushedCalls)
	}

	for _, r := range receipt.LayerResults {
		if r.Layer == LayerGitLocal {
			t.Error("git_local layer should not be included when git is disabled")
		}
	}
}

func TestRollbackManager_NoUnpushedCommits(t *testing.T) {
	ctx := context.Background()

	git := &mockGitManager{unpushedCount: 0}
	recorder := &mockRollbackRecorder{}

	config := RollbackConfig{GitEnabled: true}
	manager := NewRollbackManager(config, nil, nil, git, recorder)

	options := RollbackOptions{IncludeGitLocal: true}

	receipt, _ := manager.Rollback(ctx, "no-unpushed", options)

	if git.resetSoftCalls != 0 {
		t.Errorf("resetSoftCalls = %d, want 0 (no commits to reset)", git.resetSoftCalls)
	}

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
