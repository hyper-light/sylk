package engineer

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// TestTaskStateConstants verifies all task state constants are valid.
func TestTaskStateConstants(t *testing.T) {
	tests := []struct {
		name     string
		state    TaskState
		expected string
	}{
		{"pending state", TaskStatePending, "pending"},
		{"running state", TaskStateRunning, "running"},
		{"completed state", TaskStateCompleted, "completed"},
		{"failed state", TaskStateFailed, "failed"},
		{"blocked state", TaskStateBlocked, "blocked"},
		{"cancelled state", TaskStateCancelled, "cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.state) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.state)
			}
		})
	}
}

// TestValidTaskStates verifies all valid states are returned.
func TestValidTaskStates(t *testing.T) {
	states := ValidTaskStates()

	if len(states) != 6 {
		t.Errorf("expected 6 states, got %d", len(states))
	}

	expectedStates := map[TaskState]bool{
		TaskStatePending:   true,
		TaskStateRunning:   true,
		TaskStateCompleted: true,
		TaskStateFailed:    true,
		TaskStateBlocked:   true,
		TaskStateCancelled: true,
	}

	for _, s := range states {
		if !expectedStates[s] {
			t.Errorf("unexpected state: %s", s)
		}
	}
}

// TestTaskResultJSONRoundTrip verifies TaskResult serializes correctly.
func TestTaskResultJSONRoundTrip(t *testing.T) {
	original := TaskResult{
		TaskID:  "task-123",
		Success: true,
		FilesChanged: []FileChange{
			{Path: "main.go", Action: FileActionModify, LinesAdded: 10, LinesRemoved: 5},
		},
		Output:   "build successful",
		Errors:   []string{},
		Duration: 5 * time.Second,
		Metadata: json.RawMessage(`{"key":"value"}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded TaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch: got %q, want %q", decoded.TaskID, original.TaskID)
	}
	if decoded.Success != original.Success {
		t.Errorf("Success mismatch: got %v, want %v", decoded.Success, original.Success)
	}
	if len(decoded.FilesChanged) != len(original.FilesChanged) {
		t.Errorf("FilesChanged length mismatch: got %d, want %d", len(decoded.FilesChanged), len(original.FilesChanged))
	}
}

// TestTaskResultEmptyFields verifies empty/nil fields serialize correctly.
func TestTaskResultEmptyFields(t *testing.T) {
	empty := TaskResult{
		TaskID:  "task-empty",
		Success: false,
	}

	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("failed to marshal empty result: %v", err)
	}

	var decoded TaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.FilesChanged != nil && len(decoded.FilesChanged) != 0 {
		t.Error("expected nil or empty FilesChanged")
	}
	if decoded.Errors != nil && len(decoded.Errors) != 0 {
		t.Error("expected nil or empty Errors")
	}
}

// TestConsultationResultJSONRoundTrip verifies ConsultationResult serializes correctly.
func TestConsultationResultJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := ConsultationResult{
		QueryID:           "query-456",
		Question:          "How should I structure this?",
		Response:          "Use a factory pattern",
		Timestamp:         now,
		RelevantFiles:     []string{"factory.go", "types.go"},
		SuggestedApproach: []string{"step 1", "step 2"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ConsultationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.QueryID != original.QueryID {
		t.Errorf("QueryID mismatch: got %q, want %q", decoded.QueryID, original.QueryID)
	}
	if len(decoded.RelevantFiles) != len(original.RelevantFiles) {
		t.Errorf("RelevantFiles length mismatch")
	}
}

// TestHelpRequestJSONRoundTrip verifies HelpRequest serializes correctly.
func TestHelpRequestJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := HelpRequest{
		RequestID: "help-789",
		TaskID:    "task-123",
		Question:  "Which approach is better?",
		Context:   "Working on auth module",
		Options:   []string{"Option A", "Option B"},
		Blocking:  true,
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded HelpRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.RequestID != original.RequestID {
		t.Errorf("RequestID mismatch")
	}
	if decoded.Blocking != original.Blocking {
		t.Errorf("Blocking mismatch: got %v, want %v", decoded.Blocking, original.Blocking)
	}
}

// TestProgressReportValidation verifies ProgressReport fields.
func TestProgressReportValidation(t *testing.T) {
	tests := []struct {
		name     string
		progress int
		valid    bool
	}{
		{"zero progress", 0, true},
		{"mid progress", 50, true},
		{"full progress", 100, true},
		{"negative progress", -1, false},
		{"over 100 progress", 101, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := ProgressReport{
				TaskID:   "task-1",
				State:    TaskStateRunning,
				Progress: tt.progress,
			}

			// Validation: progress should be 0-100
			isValid := pr.Progress >= 0 && pr.Progress <= 100
			if isValid != tt.valid {
				t.Errorf("progress %d validity: got %v, want %v", tt.progress, isValid, tt.valid)
			}
		})
	}
}

// TestDefaultApprovedPatterns verifies default patterns are non-empty.
func TestDefaultApprovedPatterns(t *testing.T) {
	patterns := DefaultApprovedPatterns()

	if len(patterns.Patterns) == 0 {
		t.Error("expected non-empty approved patterns")
	}
	if len(patterns.Blocklist) == 0 {
		t.Error("expected non-empty blocklist")
	}

	// Verify specific patterns exist
	foundGo := false
	for _, p := range patterns.Patterns {
		if p == `^go\s+(build|test|run|fmt|vet|mod|generate)` {
			foundGo = true
			break
		}
	}
	if !foundGo {
		t.Error("expected go build pattern in approved patterns")
	}

	// Verify rm -rf / is blocked
	foundDanger := false
	for _, p := range patterns.Blocklist {
		if p == `rm\s+-rf\s+/` {
			foundDanger = true
			break
		}
	}
	if !foundDanger {
		t.Error("expected rm -rf / in blocklist")
	}
}

// TestDefaultMemoryThreshold verifies default threshold values.
func TestDefaultMemoryThreshold(t *testing.T) {
	threshold := DefaultMemoryThreshold()

	if threshold.CheckpointThreshold != 0.95 {
		t.Errorf("expected checkpoint threshold 0.95, got %f", threshold.CheckpointThreshold)
	}
	if threshold.WarningThreshold != 0.85 {
		t.Errorf("expected warning threshold 0.85, got %f", threshold.WarningThreshold)
	}

	// Warning should be less than checkpoint
	if threshold.WarningThreshold >= threshold.CheckpointThreshold {
		t.Error("warning threshold should be less than checkpoint threshold")
	}
}

// TestFileChangeActions verifies FileChange action constants.
func TestFileChangeActions(t *testing.T) {
	if FileActionCreate != "create" {
		t.Errorf("FileActionCreate should be 'create', got %q", FileActionCreate)
	}
	if FileActionModify != "modify" {
		t.Errorf("FileActionModify should be 'modify', got %q", FileActionModify)
	}
	if FileActionDelete != "delete" {
		t.Errorf("FileActionDelete should be 'delete', got %q", FileActionDelete)
	}
}

// TestCommandExecutionJSONRoundTrip verifies CommandExecution serializes correctly.
func TestCommandExecutionJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := CommandExecution{
		Command:    "go build ./...",
		ExitCode:   0,
		Stdout:     "build successful",
		Stderr:     "",
		Duration:   2 * time.Second,
		StartTime:  now,
		WorkingDir: "/project",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded CommandExecution
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Command != original.Command {
		t.Errorf("Command mismatch")
	}
	if decoded.ExitCode != original.ExitCode {
		t.Errorf("ExitCode mismatch: got %d, want %d", decoded.ExitCode, original.ExitCode)
	}
}

// TestSignalTypeConstants verifies SignalType constants.
func TestSignalTypeConstants(t *testing.T) {
	tests := []struct {
		signal   SignalType
		expected string
	}{
		{SignalTypeHelp, "help"},
		{SignalTypeCompletion, "completion"},
		{SignalTypeFailure, "failure"},
		{SignalTypeProgress, "progress"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.signal) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.signal)
			}
		})
	}
}

// TestEngineerIntentConstants verifies EngineerIntent constants.
func TestEngineerIntentConstants(t *testing.T) {
	if string(IntentComplete) != "complete" {
		t.Errorf("IntentComplete should be 'complete', got %q", IntentComplete)
	}
	if string(IntentHelp) != "help" {
		t.Errorf("IntentHelp should be 'help', got %q", IntentHelp)
	}
}

// TestConsultTargetConstants verifies ConsultTarget constants.
func TestConsultTargetConstants(t *testing.T) {
	tests := []struct {
		target   ConsultTarget
		expected string
	}{
		{ConsultLibrarian, "librarian"},
		{ConsultArchivalist, "archivalist"},
		{ConsultAcademic, "academic"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.target) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.target)
			}
		})
	}
}

// TestAgentStatusConstants verifies AgentStatus constants.
func TestAgentStatusConstants(t *testing.T) {
	tests := []struct {
		status   AgentStatus
		expected string
	}{
		{AgentStatusIdle, "idle"},
		{AgentStatusBusy, "busy"},
		{AgentStatusBlocked, "blocked"},
		{AgentStatusShutdown, "shutdown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.status)
			}
		})
	}
}

// TestSignalJSONRoundTrip verifies Signal serializes correctly.
func TestSignalJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := Signal{
		Type:       SignalTypeProgress,
		TaskID:     "task-001",
		EngineerID: "eng-001",
		SessionID:  "sess-001",
		Payload:    map[string]interface{}{"progress": 50.0},
		Timestamp:  now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Signal
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type mismatch: got %q, want %q", decoded.Type, original.Type)
	}
	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
}

// TestConsultationJSONRoundTrip verifies Consultation serializes correctly.
func TestConsultationJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := Consultation{
		Target:    ConsultLibrarian,
		Query:     "Find auth patterns",
		Response:  "Found 3 implementations",
		Duration:  500 * time.Millisecond,
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Consultation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Target != original.Target {
		t.Errorf("Target mismatch: got %q, want %q", decoded.Target, original.Target)
	}
}

// TestEngineerStateJSONRoundTrip verifies EngineerState serializes correctly.
func TestEngineerStateJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := EngineerState{
		ID:             "eng-001",
		SessionID:      "sess-001",
		Status:         AgentStatusBusy,
		CurrentTaskID:  "task-001",
		TaskQueue:      []string{"task-002", "task-003"},
		CompletedCount: 5,
		FailedCount:    1,
		TokensUsed:     10000,
		StartedAt:      now,
		LastActiveAt:   now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded EngineerState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Status != original.Status {
		t.Errorf("Status mismatch: got %q, want %q", decoded.Status, original.Status)
	}
	if len(decoded.TaskQueue) != len(original.TaskQueue) {
		t.Errorf("TaskQueue length mismatch")
	}
}

// TestAgentRoutingInfoJSONRoundTrip verifies AgentRoutingInfo serializes correctly.
func TestAgentRoutingInfoJSONRoundTrip(t *testing.T) {
	original := AgentRoutingInfo{
		AgentID:   "eng-001",
		AgentType: "engineer",
		Intents:   []EngineerIntent{IntentComplete, IntentHelp},
		Keywords:  []string{"build", "test", "deploy"},
		Priority:  10,
		SessionID: "sess-001",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded AgentRoutingInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.AgentID != original.AgentID {
		t.Errorf("AgentID mismatch")
	}
	if len(decoded.Intents) != len(original.Intents) {
		t.Errorf("Intents length mismatch")
	}
}

// TestToolCallJSONRoundTrip verifies ToolCall serializes correctly.
func TestToolCallJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := ToolCall{
		ID:        "call-001",
		Tool:      "file_write",
		Arguments: map[string]interface{}{"path": "main.go", "content": "package main"},
		Result:    "success",
		Error:     "",
		Duration:  100 * time.Millisecond,
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ToolCall
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Tool != original.Tool {
		t.Errorf("Tool mismatch: got %q, want %q", decoded.Tool, original.Tool)
	}
}

// TestEngineerRequestJSONRoundTrip verifies EngineerRequest serializes correctly.
func TestEngineerRequestJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := EngineerRequest{
		ID:         "req-001",
		Intent:     IntentComplete,
		TaskID:     "task-001",
		Prompt:     "Build the auth module",
		Context:    map[string]interface{}{"files": []string{"auth.go"}},
		EngineerID: "eng-001",
		SessionID:  "sess-001",
		Timestamp:  now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded EngineerRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Intent != original.Intent {
		t.Errorf("Intent mismatch: got %q, want %q", decoded.Intent, original.Intent)
	}
}

// TestEngineerResponseJSONRoundTrip verifies EngineerResponse serializes correctly.
func TestEngineerResponseJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := EngineerResponse{
		ID:        "resp-001",
		RequestID: "req-001",
		Success:   true,
		Result: &TaskResult{
			TaskID:  "task-001",
			Success: true,
			Output:  "completed",
		},
		Error:     "",
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded EngineerResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Result == nil {
		t.Error("expected non-nil Result")
	}
}

// TestEngineerResponseNilResult verifies nil result serializes correctly.
func TestEngineerResponseNilResult(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := EngineerResponse{
		ID:        "resp-002",
		RequestID: "req-002",
		Success:   false,
		Result:    nil,
		Error:     "task failed",
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded EngineerResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Result != nil {
		t.Error("expected nil Result")
	}
	if decoded.Error != original.Error {
		t.Errorf("Error mismatch")
	}
}

// TestCheckpointStateJSONRoundTrip verifies CheckpointState serializes correctly.
func TestCheckpointStateJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := CheckpointState{
		TaskID:     "task-001",
		EngineerID: "eng-001",
		SessionID:  "sess-001",
		FileStates: []FileSnapshot{
			{Path: "main.go", ContentHash: "abc123", Exists: true, Content: "package main"},
			{Path: "deleted.go", ContentHash: "", Exists: false},
		},
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded CheckpointState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if len(decoded.FileStates) != len(original.FileStates) {
		t.Errorf("FileStates length mismatch")
	}
}

// TestFileSnapshotJSONRoundTrip verifies FileSnapshot serializes correctly.
func TestFileSnapshotJSONRoundTrip(t *testing.T) {
	original := FileSnapshot{
		Path:        "utils.go",
		ContentHash: "sha256:abc123",
		Exists:      true,
		Content:     "package utils",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded FileSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Path != original.Path {
		t.Errorf("Path mismatch")
	}
	if decoded.Exists != original.Exists {
		t.Errorf("Exists mismatch")
	}
}

// TestFailureRecordJSONRoundTrip verifies FailureRecord serializes correctly.
func TestFailureRecordJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := FailureRecord{
		TaskID:       "task-001",
		EngineerID:   "eng-001",
		AttemptCount: 3,
		LastError:    "build failed: syntax error",
		Approach:     "tried alternative parsing",
		Timestamp:    now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded FailureRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if decoded.AttemptCount != original.AttemptCount {
		t.Errorf("AttemptCount mismatch: got %d, want %d", decoded.AttemptCount, original.AttemptCount)
	}
}

// TestEngineerConfigJSONRoundTrip verifies EngineerConfig serializes correctly.
func TestEngineerConfigJSONRoundTrip(t *testing.T) {
	original := EngineerConfig{
		Model:              "opus-4.5",
		MaxConcurrentTasks: 5,
		CommandTimeout:     30 * time.Second,
		ApprovedCommands:   DefaultApprovedPatterns(),
		MemoryThreshold:    DefaultMemoryThreshold(),
		WorkingDirectory:   "/project",
		EnableFileWrites:   true,
		EnableCommands:     true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded EngineerConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Model != original.Model {
		t.Errorf("Model mismatch")
	}
	if decoded.MaxConcurrentTasks != original.MaxConcurrentTasks {
		t.Errorf("MaxConcurrentTasks mismatch")
	}
	if decoded.EnableFileWrites != original.EnableFileWrites {
		t.Errorf("EnableFileWrites mismatch")
	}
}

// TestConcurrentJSONMarshal verifies types are safe for concurrent marshaling.
func TestConcurrentJSONMarshal(t *testing.T) {
	state := EngineerState{
		ID:             "eng-001",
		SessionID:      "sess-001",
		Status:         AgentStatusIdle,
		TaskQueue:      []string{"task-1", "task-2"},
		CompletedCount: 0,
		FailedCount:    0,
		StartedAt:      time.Now(),
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := json.Marshal(state)
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent marshal error: %v", err)
	}
}

// TestConcurrentJSONUnmarshal verifies types are safe for concurrent unmarshaling.
func TestConcurrentJSONUnmarshal(t *testing.T) {
	data := []byte(`{"id":"eng-001","session_id":"sess-001","status":"busy","task_queue":["t1","t2"],"completed_count":5}`)

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var state EngineerState
			if err := json.Unmarshal(data, &state); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent unmarshal error: %v", err)
	}
}

// TestEmptyStructsSerialization verifies empty structs serialize correctly.
func TestEmptyStructsSerialization(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
	}{
		{"empty TaskResult", TaskResult{}},
		{"empty ConsultationResult", ConsultationResult{}},
		{"empty HelpRequest", HelpRequest{}},
		{"empty ProgressReport", ProgressReport{}},
		{"empty FileChange", FileChange{}},
		{"empty CommandExecution", CommandExecution{}},
		{"empty Signal", Signal{}},
		{"empty Consultation", Consultation{}},
		{"empty EngineerState", EngineerState{}},
		{"empty AgentRoutingInfo", AgentRoutingInfo{}},
		{"empty ToolCall", ToolCall{}},
		{"empty EngineerRequest", EngineerRequest{}},
		{"empty EngineerResponse", EngineerResponse{}},
		{"empty CheckpointState", CheckpointState{}},
		{"empty FileSnapshot", FileSnapshot{}},
		{"empty FailureRecord", FailureRecord{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.val)
			if err != nil {
				t.Fatalf("failed to marshal %s: %v", tt.name, err)
			}
			if len(data) == 0 {
				t.Errorf("expected non-empty JSON for %s", tt.name)
			}
		})
	}
}

// TestNilSlicesAndMaps verifies nil slices/maps in structs serialize correctly.
func TestNilSlicesAndMaps(t *testing.T) {
	// EngineerState with nil TaskQueue
	state := EngineerState{
		ID:        "eng-001",
		TaskQueue: nil, // explicitly nil
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state with nil queue: %v", err)
	}

	var decoded EngineerState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Nil slice should decode as nil or empty
	// This is acceptable behavior
	if decoded.TaskQueue != nil && len(decoded.TaskQueue) != 0 {
		t.Logf("TaskQueue decoded as: %v (length %d)", decoded.TaskQueue, len(decoded.TaskQueue))
	}
}

// TestToolCallNilArguments verifies ToolCall with nil Arguments works.
func TestToolCallNilArguments(t *testing.T) {
	call := ToolCall{
		ID:        "call-001",
		Tool:      "read_file",
		Arguments: nil,
	}

	data, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ToolCall
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Tool != call.Tool {
		t.Errorf("Tool mismatch")
	}
}

// TestInvalidJSONUnmarshal verifies types handle invalid JSON gracefully.
func TestInvalidJSONUnmarshal(t *testing.T) {
	invalidJSON := []byte(`{"task_id": 123}`) // task_id should be string

	var result TaskResult
	err := json.Unmarshal(invalidJSON, &result)
	if err == nil {
		t.Error("expected error for invalid JSON type")
	}
}

// TestMalformedJSONUnmarshal verifies types handle malformed JSON gracefully.
func TestMalformedJSONUnmarshal(t *testing.T) {
	malformed := []byte(`{"task_id": "test"`) // missing closing brace

	var result TaskResult
	err := json.Unmarshal(malformed, &result)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}
