package inspector

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestInspectorModeConstants(t *testing.T) {
	tests := []struct {
		mode     InspectorMode
		expected string
	}{
		{PipelineInternal, "pipeline_internal"},
		{SessionWide, "session_wide"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.mode) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.mode)
			}
		})
	}
}

func TestSeverityConstants(t *testing.T) {
	tests := []struct {
		sev      Severity
		expected string
	}{
		{Critical, "critical"},
		{High, "high"},
		{Medium, "medium"},
		{Low, "low"},
		{Info, "info"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.sev) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.sev)
			}
		})
	}
}

func TestInspectorIntentConstants(t *testing.T) {
	tests := []struct {
		intent   InspectorIntent
		expected string
	}{
		{IntentCheck, "check"},
		{IntentValidate, "validate"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.intent) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.intent)
			}
		})
	}
}

func TestValidSeverities(t *testing.T) {
	sevs := ValidSeverities()

	if len(sevs) != 5 {
		t.Errorf("expected 5 severities, got %d", len(sevs))
	}

	expectedOrder := []Severity{Critical, High, Medium, Low, Info}
	for i, expected := range expectedOrder {
		if sevs[i] != expected {
			t.Errorf("at index %d: expected %q, got %q", i, expected, sevs[i])
		}
	}
}

func TestValidInspectorModes(t *testing.T) {
	modes := ValidInspectorModes()

	if len(modes) != 2 {
		t.Errorf("expected 2 modes, got %d", len(modes))
	}

	expectedModes := map[InspectorMode]bool{
		PipelineInternal: true,
		SessionWide:      true,
	}

	for _, m := range modes {
		if !expectedModes[m] {
			t.Errorf("unexpected mode: %s", m)
		}
	}
}

func TestValidInspectorIntents(t *testing.T) {
	intents := ValidInspectorIntents()

	if len(intents) != 2 {
		t.Errorf("expected 2 intents, got %d", len(intents))
	}

	expectedIntents := map[InspectorIntent]bool{
		IntentCheck:    true,
		IntentValidate: true,
	}

	for _, i := range intents {
		if !expectedIntents[i] {
			t.Errorf("unexpected intent: %s", i)
		}
	}
}

func TestDefaultMemoryThreshold(t *testing.T) {
	threshold := DefaultMemoryThreshold()

	if threshold.MaxIssues != 1000 {
		t.Errorf("expected MaxIssues 1000, got %d", threshold.MaxIssues)
	}
	if threshold.MaxFeedbackLoops != 10 {
		t.Errorf("expected MaxFeedbackLoops 10, got %d", threshold.MaxFeedbackLoops)
	}
	if threshold.MaxCorrectionSize != 1024*1024 {
		t.Errorf("expected MaxCorrectionSize 1MB, got %d", threshold.MaxCorrectionSize)
	}
	if threshold.TotalMemoryLimit != 100*1024*1024 {
		t.Errorf("expected TotalMemoryLimit 100MB, got %d", threshold.TotalMemoryLimit)
	}
}

func TestDefaultInspectorConfig(t *testing.T) {
	config := DefaultInspectorConfig()

	if config.Model != "codex-5.2" {
		t.Errorf("expected Model codex-5.2, got %s", config.Model)
	}
	if config.Mode != PipelineInternal {
		t.Errorf("expected Mode PipelineInternal, got %s", config.Mode)
	}
	if config.CheckpointThreshold != 0.85 {
		t.Errorf("expected CheckpointThreshold 0.85, got %f", config.CheckpointThreshold)
	}
	if config.CompactionThreshold != 0.95 {
		t.Errorf("expected CompactionThreshold 0.95, got %f", config.CompactionThreshold)
	}
	if config.MaxValidationLoops != 3 {
		t.Errorf("expected MaxValidationLoops 3, got %d", config.MaxValidationLoops)
	}
	if config.ValidationTimeout != 5*time.Second {
		t.Errorf("expected ValidationTimeout 5s, got %v", config.ValidationTimeout)
	}
	if len(config.EnabledTools) != 3 {
		t.Errorf("expected 3 enabled tools, got %d", len(config.EnabledTools))
	}
}

func TestInspectorCriteriaJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorCriteria{
		TaskID: "task-001",
		SuccessCriteria: []SuccessCriterion{
			{ID: "sc-1", Description: "Tests pass", Verifiable: true, VerificationMethod: "go test"},
		},
		QualityGates: []QualityGate{
			{Name: "coverage", Threshold: 80.0, Metric: "coverage", Operator: ">="},
		},
		Constraints: []Constraint{
			{Type: "performance", Description: "Response under 100ms", Required: true},
		},
		CreatedAt: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorCriteria
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if len(decoded.SuccessCriteria) != len(original.SuccessCriteria) {
		t.Errorf("SuccessCriteria length mismatch")
	}
	if len(decoded.QualityGates) != len(original.QualityGates) {
		t.Errorf("QualityGates length mismatch")
	}
}

func TestValidationIssueJSONRoundTrip(t *testing.T) {
	original := ValidationIssue{
		ID:           "issue-001",
		Severity:     High,
		File:         "main.go",
		Line:         42,
		Column:       10,
		Message:      "unused variable",
		SuggestedFix: "remove or use the variable",
		RuleID:       "unused-var",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ValidationIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Severity != original.Severity {
		t.Errorf("Severity mismatch: got %q, want %q", decoded.Severity, original.Severity)
	}
	if decoded.Line != original.Line {
		t.Errorf("Line mismatch: got %d, want %d", decoded.Line, original.Line)
	}
}

func TestInspectorFeedbackJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorFeedback{
		Loop:      2,
		Timestamp: now,
		Issues: []ValidationIssue{
			{ID: "issue-001", Severity: Medium, File: "test.go", Line: 10, Message: "test issue"},
		},
		Passed: false,
		Corrections: []Correction{
			{IssueID: "issue-001", Description: "fix it", SuggestedFix: "do this", File: "test.go", LineStart: 10, LineEnd: 12},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorFeedback
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Loop != original.Loop {
		t.Errorf("Loop mismatch")
	}
	if decoded.Passed != original.Passed {
		t.Errorf("Passed mismatch")
	}
	if len(decoded.Issues) != len(original.Issues) {
		t.Errorf("Issues length mismatch")
	}
}

func TestInspectorResultJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorResult{
		TaskID:             "task-001",
		Mode:               PipelineInternal,
		Passed:             true,
		Issues:             []ValidationIssue{},
		CriteriaMet:        []string{"sc-1", "sc-2"},
		CriteriaFailed:     []string{},
		QualityGateResults: map[string]bool{"coverage": true, "complexity": true},
		FeedbackHistory:    []InspectorFeedback{},
		StartedAt:          now,
		CompletedAt:        now.Add(5 * time.Second),
		LoopCount:          2,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if decoded.Mode != original.Mode {
		t.Errorf("Mode mismatch")
	}
	if decoded.Passed != original.Passed {
		t.Errorf("Passed mismatch")
	}
	if len(decoded.QualityGateResults) != len(original.QualityGateResults) {
		t.Errorf("QualityGateResults length mismatch")
	}
}

func TestOverrideRequestJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := OverrideRequest{
		IssueID:     "issue-001",
		Reason:      "false positive",
		RequestedBy: "user-123",
		RequestedAt: now,
		Approved:    true,
		ApprovedBy:  "admin-456",
		ApprovedAt:  now.Add(time.Minute),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded OverrideRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.IssueID != original.IssueID {
		t.Errorf("IssueID mismatch")
	}
	if decoded.Approved != original.Approved {
		t.Errorf("Approved mismatch")
	}
}

func TestAgentRoutingInfoJSONRoundTrip(t *testing.T) {
	original := AgentRoutingInfo{
		AgentID:   "inspector-001",
		AgentType: "inspector",
		Intents:   []InspectorIntent{IntentCheck, IntentValidate},
		Keywords:  []string{"validate", "check", "lint"},
		Priority:  5,
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

func TestInspectorRequestJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorRequest{
		ID:          "req-001",
		Intent:      IntentValidate,
		TaskID:      "task-001",
		Files:       []string{"main.go", "utils.go"},
		Criteria:    nil,
		InspectorID: "inspector-001",
		SessionID:   "sess-001",
		Timestamp:   now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Intent != original.Intent {
		t.Errorf("Intent mismatch")
	}
	if len(decoded.Files) != len(original.Files) {
		t.Errorf("Files length mismatch")
	}
}

func TestInspectorResponseJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorResponse{
		ID:        "resp-001",
		RequestID: "req-001",
		Success:   true,
		Result: &InspectorResult{
			TaskID: "task-001",
			Passed: true,
		},
		Error:     "",
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Success != original.Success {
		t.Errorf("Success mismatch")
	}
	if decoded.Result == nil {
		t.Error("expected non-nil Result")
	}
}

func TestInspectorResponseNilResult(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorResponse{
		ID:        "resp-002",
		RequestID: "req-002",
		Success:   false,
		Result:    nil,
		Error:     "validation failed",
		Timestamp: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorResponse
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

func TestInspectorToolConfigJSONRoundTrip(t *testing.T) {
	original := InspectorToolConfig{
		Name:        "eslint",
		Command:     "eslint --format json",
		CheckOnly:   "eslint --format json",
		Languages:   []string{"javascript", "typescript"},
		ConfigFiles: []string{".eslintrc", ".eslintrc.js"},
		Severity:    "warning",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorToolConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("Name mismatch")
	}
	if len(decoded.Languages) != len(original.Languages) {
		t.Errorf("Languages length mismatch")
	}
}

func TestFindingJSONRoundTrip(t *testing.T) {
	original := Finding{
		ID:           "finding-001",
		Severity:     Critical,
		File:         "auth.go",
		Line:         100,
		Column:       5,
		Message:      "SQL injection vulnerability",
		RuleID:       "security/sql-injection",
		SuggestedFix: "use parameterized queries",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Finding
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Severity != original.Severity {
		t.Errorf("Severity mismatch")
	}
}

func TestResolvedIssueJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := ResolvedIssue{
		IssueID:    "issue-001",
		ResolvedBy: "engineer-001",
		ResolvedAt: now,
		Resolution: "fixed by refactoring",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ResolvedIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.IssueID != original.IssueID {
		t.Errorf("IssueID mismatch")
	}
}

func TestUnresolvedIssueJSONRoundTrip(t *testing.T) {
	original := UnresolvedIssue{
		IssueID:      "issue-002",
		Severity:     High,
		Description:  "memory leak in handler",
		AttemptCount: 2,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded UnresolvedIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.IssueID != original.IssueID {
		t.Errorf("IssueID mismatch")
	}
	if decoded.AttemptCount != original.AttemptCount {
		t.Errorf("AttemptCount mismatch")
	}
}

func TestOverriddenIssueJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := OverriddenIssue{
		IssueID:      "issue-003",
		OverriddenBy: "user-001",
		OverriddenAt: now,
		Reason:       "accepted risk",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded OverriddenIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.IssueID != original.IssueID {
		t.Errorf("IssueID mismatch")
	}
	if decoded.Reason != original.Reason {
		t.Errorf("Reason mismatch")
	}
}

func TestFixReferenceJSONRoundTrip(t *testing.T) {
	original := FixReference{
		IssueID:      "issue-001",
		Severity:     "critical",
		FilePath:     "handler.go",
		LineNumber:   50,
		Description:  "null pointer dereference",
		SuggestedFix: "add nil check",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded FixReference
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.IssueID != original.IssueID {
		t.Errorf("IssueID mismatch")
	}
	if decoded.LineNumber != original.LineNumber {
		t.Errorf("LineNumber mismatch")
	}
}

func TestInspectorCheckpointSummaryJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorCheckpointSummary{
		PipelineID:       "pipeline-001",
		SessionID:        "sess-001",
		Timestamp:        now,
		ContextUsage:     0.87,
		CheckpointIndex:  3,
		TotalIssuesFound: 15,
		ResolvedIssues: []ResolvedIssue{
			{IssueID: "i1", ResolvedBy: "eng-1", ResolvedAt: now, Resolution: "fixed"},
		},
		UnresolvedIssues: []UnresolvedIssue{
			{IssueID: "i2", Severity: High, Description: "pending", AttemptCount: 1},
		},
		OverriddenIssues: []OverriddenIssue{
			{IssueID: "i3", OverriddenBy: "user-1", OverriddenAt: now, Reason: "accepted"},
		},
		CriticalFixes: []FixReference{
			{IssueID: "i4", Severity: "critical", FilePath: "main.go", LineNumber: 10},
		},
		HighPriorityFixes: []FixReference{},
		LoopsCompleted:    5,
		AverageLoopTime:   2 * time.Second,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorCheckpointSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.PipelineID != original.PipelineID {
		t.Errorf("PipelineID mismatch")
	}
	if decoded.ContextUsage != original.ContextUsage {
		t.Errorf("ContextUsage mismatch")
	}
	if decoded.TotalIssuesFound != original.TotalIssuesFound {
		t.Errorf("TotalIssuesFound mismatch")
	}
}

func TestValidationRunJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := ValidationRun{
		RunID:        "run-001",
		StartedAt:    now,
		CompletedAt:  now.Add(3 * time.Second),
		FilesChecked: []string{"a.go", "b.go"},
		IssuesFound: []ValidationIssue{
			{ID: "i1", Severity: Low, File: "a.go", Line: 1, Message: "minor issue"},
		},
		Passed: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ValidationRun
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.RunID != original.RunID {
		t.Errorf("RunID mismatch")
	}
	if decoded.Passed != original.Passed {
		t.Errorf("Passed mismatch")
	}
}

func TestInspectorHandoffStateJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorHandoffState{
		PipelineID: "pipeline-001",
		CurrentFindings: []Finding{
			{ID: "f1", Severity: High, File: "main.go", Line: 10, Message: "issue"},
		},
		ResolvedByEngineer: []ResolvedIssue{
			{IssueID: "i1", ResolvedBy: "eng-1", ResolvedAt: now, Resolution: "done"},
		},
		PendingForEngineer: []FixReference{
			{IssueID: "i2", Severity: "high", FilePath: "util.go", LineNumber: 20},
		},
		ValidationHistory: []ValidationRun{
			{RunID: "run-1", StartedAt: now, CompletedAt: now, Passed: false},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorHandoffState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.PipelineID != original.PipelineID {
		t.Errorf("PipelineID mismatch")
	}
	if len(decoded.CurrentFindings) != len(original.CurrentFindings) {
		t.Errorf("CurrentFindings length mismatch")
	}
}

func TestInspectorConfigJSONRoundTrip(t *testing.T) {
	original := DefaultInspectorConfig()
	original.EnabledTools = append(original.EnabledTools, "run_complexity_check")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Model != original.Model {
		t.Errorf("Model mismatch")
	}
	if decoded.Mode != original.Mode {
		t.Errorf("Mode mismatch")
	}
	if len(decoded.EnabledTools) != len(original.EnabledTools) {
		t.Errorf("EnabledTools length mismatch")
	}
}

func TestInspectorStateJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorState{
		ID:             "inspector-001",
		SessionID:      "sess-001",
		Mode:           SessionWide,
		CurrentTaskID:  "task-001",
		IssuesFound:    25,
		IssuesResolved: 20,
		LoopsCompleted: 3,
		ContextUsage:   0.65,
		StartedAt:      now,
		LastActiveAt:   now.Add(10 * time.Minute),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if decoded.Mode != original.Mode {
		t.Errorf("Mode mismatch")
	}
	if decoded.IssuesFound != original.IssuesFound {
		t.Errorf("IssuesFound mismatch")
	}
}

func TestConcurrentJSONMarshal(t *testing.T) {
	state := InspectorState{
		ID:             "inspector-001",
		SessionID:      "sess-001",
		Mode:           PipelineInternal,
		IssuesFound:    10,
		IssuesResolved: 5,
		LoopsCompleted: 2,
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

func TestConcurrentJSONUnmarshal(t *testing.T) {
	data := []byte(`{"id":"inspector-001","session_id":"sess-001","mode":"pipeline_internal","issues_found":10}`)

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var state InspectorState
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

func TestEmptyStructsSerialization(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
	}{
		{"empty InspectorCriteria", InspectorCriteria{}},
		{"empty SuccessCriterion", SuccessCriterion{}},
		{"empty QualityGate", QualityGate{}},
		{"empty Constraint", Constraint{}},
		{"empty ValidationIssue", ValidationIssue{}},
		{"empty InspectorFeedback", InspectorFeedback{}},
		{"empty Correction", Correction{}},
		{"empty InspectorResult", InspectorResult{}},
		{"empty OverrideRequest", OverrideRequest{}},
		{"empty MemoryThreshold", MemoryThreshold{}},
		{"empty AgentRoutingInfo", AgentRoutingInfo{}},
		{"empty InspectorRequest", InspectorRequest{}},
		{"empty InspectorResponse", InspectorResponse{}},
		{"empty InspectorToolConfig", InspectorToolConfig{}},
		{"empty Finding", Finding{}},
		{"empty ResolvedIssue", ResolvedIssue{}},
		{"empty UnresolvedIssue", UnresolvedIssue{}},
		{"empty OverriddenIssue", OverriddenIssue{}},
		{"empty FixReference", FixReference{}},
		{"empty InspectorCheckpointSummary", InspectorCheckpointSummary{}},
		{"empty ValidationRun", ValidationRun{}},
		{"empty InspectorHandoffState", InspectorHandoffState{}},
		{"empty InspectorConfig", InspectorConfig{}},
		{"empty InspectorState", InspectorState{}},
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

func TestNilSlicesAndMaps(t *testing.T) {
	result := InspectorResult{
		TaskID:             "task-001",
		Issues:             nil,
		CriteriaMet:        nil,
		QualityGateResults: nil,
		FeedbackHistory:    nil,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result with nil slices: %v", err)
	}

	var decoded InspectorResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.TaskID != result.TaskID {
		t.Errorf("TaskID mismatch")
	}
}

func TestInvalidJSONUnmarshal(t *testing.T) {
	invalidJSON := []byte(`{"task_id": 123}`)

	var criteria InspectorCriteria
	err := json.Unmarshal(invalidJSON, &criteria)
	if err == nil {
		t.Error("expected error for invalid JSON type")
	}
}

func TestMalformedJSONUnmarshal(t *testing.T) {
	malformed := []byte(`{"task_id": "test"`)

	var criteria InspectorCriteria
	err := json.Unmarshal(malformed, &criteria)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestQualityGateOperators(t *testing.T) {
	validOperators := []string{">=", "<=", ">", "<", "==", "!="}

	for _, op := range validOperators {
		gate := QualityGate{
			Name:      "test",
			Threshold: 80.0,
			Metric:    "coverage",
			Operator:  op,
		}

		data, err := json.Marshal(gate)
		if err != nil {
			t.Errorf("failed to marshal gate with operator %q: %v", op, err)
		}

		var decoded QualityGate
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Errorf("failed to unmarshal gate with operator %q: %v", op, err)
		}

		if decoded.Operator != op {
			t.Errorf("operator mismatch: got %q, want %q", decoded.Operator, op)
		}
	}
}

func TestContextUsageThresholds(t *testing.T) {
	config := DefaultInspectorConfig()

	if config.CheckpointThreshold >= config.CompactionThreshold {
		t.Error("checkpoint threshold should be less than compaction threshold")
	}

	if config.CheckpointThreshold < 0 || config.CheckpointThreshold > 1 {
		t.Errorf("checkpoint threshold should be between 0 and 1, got %f", config.CheckpointThreshold)
	}

	if config.CompactionThreshold < 0 || config.CompactionThreshold > 1 {
		t.Errorf("compaction threshold should be between 0 and 1, got %f", config.CompactionThreshold)
	}
}

// TestValidSeveritiesOrder verifies severities are returned in order of importance
func TestValidSeveritiesOrder(t *testing.T) {
	severities := ValidSeverities()

	if len(severities) == 0 {
		t.Fatal("ValidSeverities returned empty slice")
	}

	// First should be most severe (Critical), last should be least (Info)
	if severities[0] != Critical {
		t.Errorf("first severity should be Critical, got %q", severities[0])
	}

	if severities[len(severities)-1] != Info {
		t.Errorf("last severity should be Info, got %q", severities[len(severities)-1])
	}
}

// TestValidInspectorModesOrder verifies modes are returned in expected order
func TestValidInspectorModesOrder(t *testing.T) {
	modes := ValidInspectorModes()

	if len(modes) != 2 {
		t.Fatalf("expected 2 modes, got %d", len(modes))
	}

	// First should be PipelineInternal, second SessionWide
	if modes[0] != PipelineInternal {
		t.Errorf("first mode should be PipelineInternal, got %q", modes[0])
	}

	if modes[1] != SessionWide {
		t.Errorf("second mode should be SessionWide, got %q", modes[1])
	}
}

// TestValidInspectorIntentsOrder verifies intents are returned in expected order
func TestValidInspectorIntentsOrder(t *testing.T) {
	intents := ValidInspectorIntents()

	if len(intents) != 2 {
		t.Fatalf("expected 2 intents, got %d", len(intents))
	}

	if intents[0] != IntentCheck {
		t.Errorf("first intent should be IntentCheck, got %q", intents[0])
	}

	if intents[1] != IntentValidate {
		t.Errorf("second intent should be IntentValidate, got %q", intents[1])
	}
}

// TestDefaultMemoryThresholdPositiveValues verifies all default values are positive
func TestDefaultMemoryThresholdPositiveValues(t *testing.T) {
	threshold := DefaultMemoryThreshold()

	if threshold.MaxIssues <= 0 {
		t.Error("MaxIssues should be positive")
	}
	if threshold.MaxFeedbackLoops <= 0 {
		t.Error("MaxFeedbackLoops should be positive")
	}
	if threshold.MaxCorrectionSize <= 0 {
		t.Error("MaxCorrectionSize should be positive")
	}
	if threshold.TotalMemoryLimit <= 0 {
		t.Error("TotalMemoryLimit should be positive")
	}
}

// TestDefaultInspectorConfigMemoryThreshold verifies embedded MemoryThreshold matches DefaultMemoryThreshold
func TestDefaultInspectorConfigMemoryThreshold(t *testing.T) {
	config := DefaultInspectorConfig()
	expected := DefaultMemoryThreshold()

	if config.MemoryThreshold != expected {
		t.Error("MemoryThreshold in DefaultInspectorConfig should match DefaultMemoryThreshold()")
	}
}

// TestSeverityJSONMarshal tests JSON marshaling of Severity type
func TestSeverityJSONMarshal(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{Critical, `"critical"`},
		{High, `"high"`},
		{Medium, `"medium"`},
		{Low, `"low"`},
		{Info, `"info"`},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			data, err := json.Marshal(tt.severity)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("got %s, want %s", data, tt.expected)
			}
		})
	}
}

// TestSeverityJSONUnmarshal tests JSON unmarshaling of Severity type
func TestSeverityJSONUnmarshal(t *testing.T) {
	tests := []struct {
		input    string
		expected Severity
	}{
		{`"critical"`, Critical},
		{`"high"`, High},
		{`"medium"`, Medium},
		{`"low"`, Low},
		{`"info"`, Info},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var sev Severity
			if err := json.Unmarshal([]byte(tt.input), &sev); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if sev != tt.expected {
				t.Errorf("got %q, want %q", sev, tt.expected)
			}
		})
	}
}

// TestInspectorModeJSONMarshal tests JSON marshaling of InspectorMode type
func TestInspectorModeJSONMarshal(t *testing.T) {
	tests := []struct {
		mode     InspectorMode
		expected string
	}{
		{PipelineInternal, `"pipeline_internal"`},
		{SessionWide, `"session_wide"`},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			data, err := json.Marshal(tt.mode)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("got %s, want %s", data, tt.expected)
			}
		})
	}
}

// TestInspectorModeJSONUnmarshal tests JSON unmarshaling of InspectorMode type
func TestInspectorModeJSONUnmarshal(t *testing.T) {
	tests := []struct {
		input    string
		expected InspectorMode
	}{
		{`"pipeline_internal"`, PipelineInternal},
		{`"session_wide"`, SessionWide},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var mode InspectorMode
			if err := json.Unmarshal([]byte(tt.input), &mode); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if mode != tt.expected {
				t.Errorf("got %q, want %q", mode, tt.expected)
			}
		})
	}
}

// TestInspectorIntentJSONMarshal tests JSON marshaling of InspectorIntent type
func TestInspectorIntentJSONMarshal(t *testing.T) {
	tests := []struct {
		intent   InspectorIntent
		expected string
	}{
		{IntentCheck, `"check"`},
		{IntentValidate, `"validate"`},
	}

	for _, tt := range tests {
		t.Run(string(tt.intent), func(t *testing.T) {
			data, err := json.Marshal(tt.intent)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("got %s, want %s", data, tt.expected)
			}
		})
	}
}

// TestInspectorIntentJSONUnmarshal tests JSON unmarshaling of InspectorIntent type
func TestInspectorIntentJSONUnmarshal(t *testing.T) {
	tests := []struct {
		input    string
		expected InspectorIntent
	}{
		{`"check"`, IntentCheck},
		{`"validate"`, IntentValidate},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var intent InspectorIntent
			if err := json.Unmarshal([]byte(tt.input), &intent); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if intent != tt.expected {
				t.Errorf("got %q, want %q", intent, tt.expected)
			}
		})
	}
}

// TestMemoryThresholdJSONFieldNames tests JSON field names for MemoryThreshold
func TestMemoryThresholdJSONFieldNames(t *testing.T) {
	threshold := MemoryThreshold{
		MaxIssues:         100,
		MaxFeedbackLoops:  5,
		MaxCorrectionSize: 1024,
		TotalMemoryLimit:  2048,
	}

	data, err := json.Marshal(threshold)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	expectedFields := []string{"max_issues", "max_feedback_loops", "max_correction_size", "total_memory_limit"}
	for _, field := range expectedFields {
		if _, ok := m[field]; !ok {
			t.Errorf("missing expected JSON field: %s", field)
		}
	}
}

// TestValidationIssueJSONOmitEmpty tests omitempty behavior for ValidationIssue
func TestValidationIssueJSONOmitEmpty(t *testing.T) {
	issue := ValidationIssue{
		ID:       "issue-002",
		Severity: Low,
		File:     "/path/to/file.go",
		Line:     10,
		Column:   5,
		Message:  "test message",
		// SuggestedFix and RuleID are empty
	}

	data, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	// These fields have omitempty and should not be present when empty
	if _, ok := m["suggested_fix"]; ok {
		t.Error("suggested_fix should be omitted when empty")
	}
	if _, ok := m["rule_id"]; ok {
		t.Error("rule_id should be omitted when empty")
	}
}

// TestInspectorRequestWithCriteria tests InspectorRequest with non-nil Criteria
func TestInspectorRequestWithCriteria(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	criteria := &InspectorCriteria{
		TaskID:          "task-001",
		SuccessCriteria: []SuccessCriterion{},
		QualityGates:    []QualityGate{},
		Constraints:     []Constraint{},
		CreatedAt:       now,
	}
	original := InspectorRequest{
		ID:          "req-001",
		Intent:      IntentValidate,
		TaskID:      "task-001",
		Criteria:    criteria,
		InspectorID: "inspector-001",
		SessionID:   "session-001",
		Timestamp:   now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Criteria == nil {
		t.Fatal("Criteria should not be nil after round-trip")
	}
	if decoded.Criteria.TaskID != criteria.TaskID {
		t.Errorf("Criteria.TaskID: got %q, want %q", decoded.Criteria.TaskID, criteria.TaskID)
	}
}

// TestOverrideRequestJSONOmitEmpty tests omitempty for OverrideRequest
func TestOverrideRequestJSONOmitEmpty(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	override := OverrideRequest{
		IssueID:     "issue-001",
		Reason:      "needs override",
		RequestedBy: "developer@example.com",
		RequestedAt: now,
		Approved:    false,
		// ApprovedBy and ApprovedAt are empty
	}

	data, err := json.Marshal(override)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, ok := m["approved_by"]; ok {
		t.Error("approved_by should be omitted when empty")
	}
}

// TestCorrectionLineRange tests Correction with various line ranges
func TestCorrectionLineRange(t *testing.T) {
	tests := []struct {
		name      string
		lineStart int
		lineEnd   int
	}{
		{"single line", 10, 10},
		{"multi line", 10, 20},
		{"start of file", 1, 5},
		{"large range", 1, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			correction := Correction{
				IssueID:      "issue-001",
				Description:  "fix issue",
				SuggestedFix: "new code",
				File:         "test.go",
				LineStart:    tt.lineStart,
				LineEnd:      tt.lineEnd,
			}

			data, err := json.Marshal(correction)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded Correction
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.LineStart != tt.lineStart {
				t.Errorf("LineStart: got %d, want %d", decoded.LineStart, tt.lineStart)
			}
			if decoded.LineEnd != tt.lineEnd {
				t.Errorf("LineEnd: got %d, want %d", decoded.LineEnd, tt.lineEnd)
			}
		})
	}
}

// TestSuccessCriterionVerifiable tests SuccessCriterion with different verifiable states
func TestSuccessCriterionVerifiable(t *testing.T) {
	tests := []struct {
		name       string
		verifiable bool
	}{
		{"verifiable", true},
		{"not verifiable", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			criterion := SuccessCriterion{
				ID:                 "crit-001",
				Description:        "test criterion",
				Verifiable:         tt.verifiable,
				VerificationMethod: "manual review",
			}

			data, err := json.Marshal(criterion)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded SuccessCriterion
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Verifiable != tt.verifiable {
				t.Errorf("Verifiable: got %v, want %v", decoded.Verifiable, tt.verifiable)
			}
		})
	}
}

// TestConstraintTypes tests various constraint type values
func TestConstraintTypes(t *testing.T) {
	types := []string{"performance", "security", "style", "complexity", "coverage"}

	for _, ctype := range types {
		t.Run(ctype, func(t *testing.T) {
			constraint := Constraint{
				Type:        ctype,
				Description: "test constraint",
				Required:    true,
			}

			data, err := json.Marshal(constraint)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded Constraint
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Type != ctype {
				t.Errorf("Type: got %q, want %q", decoded.Type, ctype)
			}
		})
	}
}

// TestZeroValuesJSON tests JSON handling of zero values
func TestZeroValuesJSON(t *testing.T) {
	var original ValidationIssue // all zero values

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ValidationIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != "" {
		t.Errorf("ID should be empty, got %q", decoded.ID)
	}
	if decoded.Severity != "" {
		t.Errorf("Severity should be empty, got %q", decoded.Severity)
	}
	if decoded.Line != 0 {
		t.Errorf("Line should be 0, got %d", decoded.Line)
	}
	if decoded.Column != 0 {
		t.Errorf("Column should be 0, got %d", decoded.Column)
	}
}

// TestDefaultInspectorConfigEnabledTools verifies the default enabled tools
func TestDefaultInspectorConfigEnabledTools(t *testing.T) {
	config := DefaultInspectorConfig()

	expectedTools := []string{"run_linter", "run_formatter_check", "run_type_checker"}

	if len(config.EnabledTools) != len(expectedTools) {
		t.Fatalf("EnabledTools: got %d items, want %d", len(config.EnabledTools), len(expectedTools))
	}

	for i, tool := range config.EnabledTools {
		if tool != expectedTools[i] {
			t.Errorf("EnabledTools[%d]: got %q, want %q", i, tool, expectedTools[i])
		}
	}
}

// TestInspectorCriteriaEmptyCollections tests InspectorCriteria with empty slices
func TestInspectorCriteriaEmptyCollections(t *testing.T) {
	original := InspectorCriteria{
		TaskID:          "task-001",
		SuccessCriteria: []SuccessCriterion{},
		QualityGates:    []QualityGate{},
		Constraints:     []Constraint{},
		CreatedAt:       time.Now().Truncate(time.Second),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded InspectorCriteria
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Empty slices may become nil after unmarshal, but should have length 0
	if len(decoded.SuccessCriteria) != 0 {
		t.Errorf("SuccessCriteria should be empty, got %d items", len(decoded.SuccessCriteria))
	}
	if len(decoded.QualityGates) != 0 {
		t.Errorf("QualityGates should be empty, got %d items", len(decoded.QualityGates))
	}
	if len(decoded.Constraints) != 0 {
		t.Errorf("Constraints should be empty, got %d items", len(decoded.Constraints))
	}
}

// TestInspectorStateNoCurrentTask tests InspectorState without a current task
func TestInspectorStateNoCurrentTask(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := InspectorState{
		ID:             "state-001",
		SessionID:      "session-001",
		Mode:           SessionWide,
		CurrentTaskID:  "", // empty - omitempty
		IssuesFound:    0,
		IssuesResolved: 0,
		LoopsCompleted: 0,
		ContextUsage:   0.0,
		StartedAt:      now,
		LastActiveAt:   now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	// current_task_id has omitempty and should not be present when empty
	if _, ok := m["current_task_id"]; ok {
		t.Error("current_task_id should be omitted when empty")
	}
}

// TestQualityGateThresholdBoundaries tests QualityGate with boundary threshold values
func TestQualityGateThresholdBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
	}{
		{"zero threshold", 0.0},
		{"hundred percent", 100.0},
		{"fractional", 80.5},
		{"negative (edge case)", -1.0},
		{"over hundred (edge case)", 150.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := QualityGate{
				Name:      "test-gate",
				Threshold: tt.threshold,
				Metric:    "coverage",
				Operator:  ">=",
			}

			data, err := json.Marshal(gate)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded QualityGate
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Threshold != tt.threshold {
				t.Errorf("Threshold: got %f, want %f", decoded.Threshold, tt.threshold)
			}
		})
	}
}

// TestInspectorFeedbackLoopNumber tests InspectorFeedback with various loop numbers
func TestInspectorFeedbackLoopNumber(t *testing.T) {
	tests := []struct {
		name string
		loop int
	}{
		{"first loop", 1},
		{"second loop", 2},
		{"max loops", 10},
		{"zero (edge case)", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feedback := InspectorFeedback{
				Loop:        tt.loop,
				Timestamp:   time.Now().Truncate(time.Second),
				Issues:      []ValidationIssue{},
				Passed:      true,
				Corrections: []Correction{},
			}

			data, err := json.Marshal(feedback)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded InspectorFeedback
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Loop != tt.loop {
				t.Errorf("Loop: got %d, want %d", decoded.Loop, tt.loop)
			}
		})
	}
}

// TestUnresolvedIssueAttemptCount tests UnresolvedIssue with various attempt counts
func TestUnresolvedIssueAttemptCount(t *testing.T) {
	tests := []struct {
		name         string
		attemptCount int
	}{
		{"first attempt", 1},
		{"multiple attempts", 5},
		{"zero attempts", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := UnresolvedIssue{
				IssueID:      "issue-001",
				Severity:     High,
				Description:  "test issue",
				AttemptCount: tt.attemptCount,
			}

			data, err := json.Marshal(issue)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded UnresolvedIssue
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.AttemptCount != tt.attemptCount {
				t.Errorf("AttemptCount: got %d, want %d", decoded.AttemptCount, tt.attemptCount)
			}
		})
	}
}

// TestAgentRoutingInfoPriority tests AgentRoutingInfo with various priority values
func TestAgentRoutingInfoPriority(t *testing.T) {
	tests := []struct {
		name     string
		priority int
	}{
		{"low priority", 1},
		{"medium priority", 5},
		{"high priority", 10},
		{"zero priority", 0},
		{"negative priority (edge case)", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routing := AgentRoutingInfo{
				AgentID:   "agent-001",
				AgentType: "inspector",
				Intents:   []InspectorIntent{IntentCheck},
				Keywords:  []string{"check"},
				Priority:  tt.priority,
				SessionID: "session-001",
			}

			data, err := json.Marshal(routing)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded AgentRoutingInfo
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Priority != tt.priority {
				t.Errorf("Priority: got %d, want %d", decoded.Priority, tt.priority)
			}
		})
	}
}

// TestInspectorResultQualityGateResults tests InspectorResult with various quality gate results
func TestInspectorResultQualityGateResults(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]bool
	}{
		{"all passed", map[string]bool{"coverage": true, "complexity": true}},
		{"all failed", map[string]bool{"coverage": false, "complexity": false}},
		{"mixed results", map[string]bool{"coverage": true, "complexity": false}},
		{"empty results", map[string]bool{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InspectorResult{
				TaskID:             "task-001",
				Mode:               PipelineInternal,
				Passed:             true,
				QualityGateResults: tt.results,
			}

			data, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded InspectorResult
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if len(decoded.QualityGateResults) != len(tt.results) {
				t.Errorf("QualityGateResults length: got %d, want %d", len(decoded.QualityGateResults), len(tt.results))
			}

			for k, v := range tt.results {
				if decoded.QualityGateResults[k] != v {
					t.Errorf("QualityGateResults[%s]: got %v, want %v", k, decoded.QualityGateResults[k], v)
				}
			}
		})
	}
}
