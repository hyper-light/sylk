package errors

import (
	"strings"
	"testing"
	"time"
)

func TestRollbackReceipt_AddLayerResult(t *testing.T) {
	receipt := NewRollbackReceipt("pipeline-123")

	if len(receipt.LayerResults) != 0 {
		t.Fatal("new receipt should have empty LayerResults")
	}

	result1 := &LayerResult{Layer: LayerFileStaging, Success: true, ItemCount: 5}
	result2 := &LayerResult{Layer: LayerAgentState, Success: false, Message: "failed"}

	receipt.AddLayerResult(result1)
	if len(receipt.LayerResults) != 1 {
		t.Errorf("LayerResults count = %d, want 1", len(receipt.LayerResults))
	}

	receipt.AddLayerResult(result2)
	if len(receipt.LayerResults) != 2 {
		t.Errorf("LayerResults count = %d, want 2", len(receipt.LayerResults))
	}

	if receipt.LayerResults[0] != result1 {
		t.Error("first result mismatch")
	}
	if receipt.LayerResults[1] != result2 {
		t.Error("second result mismatch")
	}
}

func TestRollbackReceipt_WasSuccessful(t *testing.T) {
	tests := []struct {
		name    string
		results []*LayerResult
		want    bool
	}{
		{
			name:    "empty results is successful",
			results: []*LayerResult{},
			want:    true,
		},
		{
			name: "all successful",
			results: []*LayerResult{
				{Layer: LayerFileStaging, Success: true},
				{Layer: LayerAgentState, Success: true},
				{Layer: LayerGitLocal, Success: true},
			},
			want: true,
		},
		{
			name: "one failure",
			results: []*LayerResult{
				{Layer: LayerFileStaging, Success: true},
				{Layer: LayerAgentState, Success: false},
				{Layer: LayerGitLocal, Success: true},
			},
			want: false,
		},
		{
			name: "all failures",
			results: []*LayerResult{
				{Layer: LayerFileStaging, Success: false},
				{Layer: LayerAgentState, Success: false},
			},
			want: false,
		},
		{
			name: "single success",
			results: []*LayerResult{
				{Layer: LayerFileStaging, Success: true},
			},
			want: true,
		},
		{
			name: "single failure",
			results: []*LayerResult{
				{Layer: LayerFileStaging, Success: false},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := NewRollbackReceipt("test-pipeline")
			for _, r := range tt.results {
				receipt.AddLayerResult(r)
			}

			if got := receipt.WasSuccessful(); got != tt.want {
				t.Errorf("WasSuccessful() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRollbackReceipt_RequiresUserAction(t *testing.T) {
	tests := []struct {
		name                 string
		pushedCommitsPending bool
		want                 bool
	}{
		{
			name:                 "no pushed commits pending",
			pushedCommitsPending: false,
			want:                 false,
		},
		{
			name:                 "pushed commits pending",
			pushedCommitsPending: true,
			want:                 true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := NewRollbackReceipt("test")
			receipt.PushedCommitsPending = tt.pushedCommitsPending

			if got := receipt.RequiresUserAction(); got != tt.want {
				t.Errorf("RequiresUserAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRollbackReceipt_Summary(t *testing.T) {
	t.Run("basic summary structure", func(t *testing.T) {
		receipt := NewRollbackReceipt("pipeline-abc")
		receipt.StagedFilesCount = 3
		receipt.AgentContextReset = true
		receipt.LocalCommitsRemoved = 2
		receipt.ArchivalistRecorded = true

		summary := receipt.Summary()

		mustContain := []string{
			"Rollback Receipt [pipeline-abc]",
			"Timestamp:",
			"Statistics:",
			"Staged files discarded: 3",
			"Agent context reset: true",
			"Local commits removed: 2",
			"Archivalist recorded: true",
		}

		for _, s := range mustContain {
			if !strings.Contains(summary, s) {
				t.Errorf("Summary() missing %q", s)
			}
		}
	})

	t.Run("summary with layer results", func(t *testing.T) {
		receipt := NewRollbackReceipt("pipeline-xyz")
		receipt.AddLayerResult(&LayerResult{
			Layer:     LayerFileStaging,
			Success:   true,
			ItemCount: 5,
			Message:   "staged files discarded",
		})
		receipt.AddLayerResult(&LayerResult{
			Layer:     LayerGitLocal,
			Success:   false,
			ItemCount: 0,
			Message:   "git error",
		})

		summary := receipt.Summary()

		if !strings.Contains(summary, "Layers:") {
			t.Error("Summary() missing Layers section")
		}
		if !strings.Contains(summary, "file_staging: SUCCESS") {
			t.Error("Summary() missing file_staging result")
		}
		if !strings.Contains(summary, "git_local: FAILED") {
			t.Error("Summary() missing git_local result")
		}
	})

	t.Run("summary with warnings", func(t *testing.T) {
		receipt := NewRollbackReceipt("test")
		receipt.AddWarning("pushed commits exist")
		receipt.AddWarning("external state unchanged")

		summary := receipt.Summary()

		if !strings.Contains(summary, "Warnings:") {
			t.Error("Summary() missing Warnings section")
		}
		if !strings.Contains(summary, "pushed commits exist") {
			t.Error("Summary() missing first warning")
		}
		if !strings.Contains(summary, "external state unchanged") {
			t.Error("Summary() missing second warning")
		}
	})

	t.Run("summary without warnings", func(t *testing.T) {
		receipt := NewRollbackReceipt("test")

		summary := receipt.Summary()

		if strings.Contains(summary, "Warnings:") {
			t.Error("Summary() should not have Warnings section when empty")
		}
	})

	t.Run("summary without layer results", func(t *testing.T) {
		receipt := NewRollbackReceipt("test")

		summary := receipt.Summary()

		if strings.Contains(summary, "Layers:") {
			t.Error("Summary() should not have Layers section when empty")
		}
	})
}

func TestRollbackReceipt_AddWarning(t *testing.T) {
	receipt := NewRollbackReceipt("test")

	if len(receipt.Warnings) != 0 {
		t.Fatal("new receipt should have empty Warnings")
	}

	receipt.AddWarning("warning 1")
	receipt.AddWarning("warning 2")
	receipt.AddWarning("warning 3")

	if len(receipt.Warnings) != 3 {
		t.Errorf("Warnings count = %d, want 3", len(receipt.Warnings))
	}

	expected := []string{"warning 1", "warning 2", "warning 3"}
	for i, w := range expected {
		if receipt.Warnings[i] != w {
			t.Errorf("Warnings[%d] = %v, want %v", i, receipt.Warnings[i], w)
		}
	}
}

func TestRollbackReceipt_AddExternalCall(t *testing.T) {
	receipt := NewRollbackReceipt("test")

	if len(receipt.ExternalCalls) != 0 {
		t.Fatal("new receipt should have empty ExternalCalls")
	}

	call1 := &ExternalCall{
		Timestamp: time.Now(),
		Endpoint:  "https://api.example.com/deploy",
		Method:    "POST",
		Status:    "completed",
	}
	call2 := &ExternalCall{
		Timestamp: time.Now(),
		Endpoint:  "https://api.example.com/notify",
		Method:    "POST",
		Status:    "pending",
	}

	receipt.AddExternalCall(call1)
	receipt.AddExternalCall(call2)

	if len(receipt.ExternalCalls) != 2 {
		t.Errorf("ExternalCalls count = %d, want 2", len(receipt.ExternalCalls))
	}

	if receipt.ExternalCalls[0] != call1 {
		t.Error("first external call mismatch")
	}
	if receipt.ExternalCalls[1] != call2 {
		t.Error("second external call mismatch")
	}
}

func TestRollbackLayer_Values(t *testing.T) {
	tests := []struct {
		layer   RollbackLayer
		wantStr string
		wantVal int
	}{
		{LayerFileStaging, "file_staging", 1},
		{LayerAgentState, "agent_state", 2},
		{LayerGitLocal, "git_local", 3},
		{LayerGitPushed, "git_pushed", 4},
		{LayerExternalState, "external_state", 5},
	}

	for _, tt := range tests {
		t.Run(tt.wantStr, func(t *testing.T) {
			if got := tt.layer.String(); got != tt.wantStr {
				t.Errorf("String() = %v, want %v", got, tt.wantStr)
			}
			if int(tt.layer) != tt.wantVal {
				t.Errorf("value = %d, want %d", int(tt.layer), tt.wantVal)
			}
		})
	}
}

func TestRollbackLayer_UnknownValue(t *testing.T) {
	unknown := RollbackLayer(999)
	if got := unknown.String(); got != "unknown" {
		t.Errorf("unknown layer String() = %v, want 'unknown'", got)
	}
}

func TestNewRollbackReceipt(t *testing.T) {
	pipelineID := "my-pipeline-123"
	before := time.Now()
	receipt := NewRollbackReceipt(pipelineID)
	after := time.Now()

	if receipt.PipelineID != pipelineID {
		t.Errorf("PipelineID = %v, want %v", receipt.PipelineID, pipelineID)
	}

	if receipt.Timestamp.Before(before) || receipt.Timestamp.After(after) {
		t.Errorf("Timestamp not in expected range")
	}

	if receipt.LayerResults == nil {
		t.Error("LayerResults should be initialized")
	}
	if len(receipt.LayerResults) != 0 {
		t.Error("LayerResults should be empty")
	}

	if receipt.ExternalCalls == nil {
		t.Error("ExternalCalls should be initialized")
	}
	if len(receipt.ExternalCalls) != 0 {
		t.Error("ExternalCalls should be empty")
	}

	if receipt.Warnings == nil {
		t.Error("Warnings should be initialized")
	}
	if len(receipt.Warnings) != 0 {
		t.Error("Warnings should be empty")
	}
}

func TestLayerResult_Fields(t *testing.T) {
	result := &LayerResult{
		Layer:     LayerFileStaging,
		Success:   true,
		Message:   "5 files discarded",
		ItemCount: 5,
	}

	if result.Layer != LayerFileStaging {
		t.Errorf("Layer = %v, want %v", result.Layer, LayerFileStaging)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.Message != "5 files discarded" {
		t.Errorf("Message = %v, want '5 files discarded'", result.Message)
	}
	if result.ItemCount != 5 {
		t.Errorf("ItemCount = %v, want 5", result.ItemCount)
	}
}

func TestExternalCall_Fields(t *testing.T) {
	now := time.Now()
	call := &ExternalCall{
		Timestamp: now,
		Endpoint:  "https://api.example.com/action",
		Method:    "POST",
		Status:    "completed",
	}

	if call.Timestamp != now {
		t.Errorf("Timestamp mismatch")
	}
	if call.Endpoint != "https://api.example.com/action" {
		t.Errorf("Endpoint = %v", call.Endpoint)
	}
	if call.Method != "POST" {
		t.Errorf("Method = %v, want POST", call.Method)
	}
	if call.Status != "completed" {
		t.Errorf("Status = %v, want completed", call.Status)
	}
}
