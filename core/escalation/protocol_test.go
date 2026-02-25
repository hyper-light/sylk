package escalation

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

func TestNewEscalator_DefaultPolicy(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())

	if esc.policy == nil {
		t.Fatal("policy is nil, want default policy")
	}
	if esc.policy.MaxEscalationDepth != 3 {
		t.Errorf("MaxEscalationDepth = %d, want 3", esc.policy.MaxEscalationDepth)
	}
	if esc.history == nil {
		t.Fatal("history is nil, want initialized")
	}
	if esc.pending == nil {
		t.Fatal("pending map is nil, want initialized")
	}
}

func TestNewEscalator_CustomPolicy(t *testing.T) {
	p := &EscalationPolicy{
		MaxEscalationDepth: 5,
		CooldownDuration:   10 * time.Second,
	}
	esc := NewEscalator(p, DefaultWeights())

	if esc.policy.MaxEscalationDepth != 5 {
		t.Errorf("MaxEscalationDepth = %d, want 5", esc.policy.MaxEscalationDepth)
	}
}

func TestReportConfidence_HighConfidence_NilTarget(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
		TaskID:       "task-ok",
	}

	target, ok := esc.ReportConfidence(cl, handoff.CategoryPipeline, 0)
	if ok || target != nil {
		t.Errorf("high confidence: got (%v, %v), want (nil, false)", target, ok)
	}
}

func TestReportConfidence_LowConfidence_ReturnsTarget(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	cl := &ConfidenceLevel{
		Correctness:  0.2,
		Completeness: 0.2,
		Quality:      0.2,
		Integration:  0.2,
		TaskID:       "task-low",
	}

	target, ok := esc.ReportConfidence(cl, handoff.CategoryPipeline, 0)
	if !ok {
		t.Fatal("low confidence: want escalation target")
	}
	if target == nil {
		t.Fatal("target is nil, want non-nil")
	}
}

func TestEscalate_RecordsEvent(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	cl := &ConfidenceLevel{
		Correctness:  0.2,
		Completeness: 0.2,
		Quality:      0.2,
		Integration:  0.2,
		TaskID:       "task-esc",
	}

	target, err := esc.Escalate(context.Background(), cl, handoff.CategoryPipeline, 0)
	if err != nil {
		t.Fatalf("Escalate error: %v", err)
	}
	if target == nil {
		t.Fatal("target is nil, want non-nil")
	}

	// Verify event was recorded in history.
	last := esc.history.LastEscalationFor("task-esc")
	if last == nil {
		t.Fatal("no event recorded in history")
	}
	if last.TaskID != "task-esc" {
		t.Errorf("recorded TaskID = %q, want %q", last.TaskID, "task-esc")
	}
}

func TestEscalate_NotWarranted_ReturnsNil(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
		TaskID:       "task-fine",
	}

	target, err := esc.Escalate(context.Background(), cl, handoff.CategoryPipeline, 0)
	if err != nil {
		t.Fatalf("Escalate error: %v", err)
	}
	if target != nil {
		t.Errorf("target = %v, want nil (not warranted)", target)
	}
}

func TestRegisterPending_DeliverResponse_ClearPending(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	taskID := "task-pending"

	ch := esc.RegisterPending(taskID)
	if ch == nil {
		t.Fatal("RegisterPending returned nil channel")
	}

	resp := &EscalationResponse{
		Accepted:   true,
		Resolution: "reassigned",
		Timestamp:  time.Now(),
	}
	esc.DeliverResponse(taskID, resp)

	select {
	case got := <-ch:
		if got == nil {
			t.Fatal("received nil response")
		}
		if !got.Accepted {
			t.Errorf("Accepted = false, want true")
		}
		if got.Resolution != "reassigned" {
			t.Errorf("Resolution = %q, want %q", got.Resolution, "reassigned")
		}
	default:
		t.Fatal("no response delivered to channel")
	}

	// Clear the pending entry.
	esc.ClearPending(taskID)

	// Deliver to a cleared task should not panic.
	esc.DeliverResponse(taskID, resp)
}

func TestDeliverResponse_NoPending(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	resp := &EscalationResponse{Accepted: true, Timestamp: time.Now()}

	// Should not panic when delivering to a non-existent task.
	esc.DeliverResponse("nonexistent", resp)
}

func TestWaitForResponse_ReceivesResponse(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	taskID := "task-wait"

	// Deliver asynchronously after a short delay.
	go func() {
		time.Sleep(10 * time.Millisecond)
		esc.DeliverResponse(taskID, &EscalationResponse{
			Accepted:   true,
			Resolution: "completed",
			Timestamp:  time.Now(),
		})
	}()

	resp, err := esc.WaitForResponse(context.Background(), taskID, 1*time.Second)
	if err != nil {
		t.Fatalf("WaitForResponse error: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
	if !resp.Accepted {
		t.Errorf("Accepted = false, want true")
	}
	if resp.Resolution != "completed" {
		t.Errorf("Resolution = %q, want %q", resp.Resolution, "completed")
	}
}

func TestWaitForResponse_Timeout(t *testing.T) {
	esc := NewEscalator(nil, DefaultWeights())
	taskID := "task-timeout"

	resp, err := esc.WaitForResponse(context.Background(), taskID, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if resp != nil {
		t.Errorf("response = %v, want nil on timeout", resp)
	}
}

func TestBuildEscalationRequest(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.5,
		Completeness: 0.5,
		Quality:      0.5,
		Integration:  0.5,
		AgentID:      "agent-src",
		AgentType:    "coder",
		TaskID:       "task-req",
	}
	target := &EscalationTarget{
		TargetAgent:     "orchestrator",
		Reason:          ReasonLowConfidence,
		Priority:        80,
		SuggestedAction: ActionReplan,
	}

	before := time.Now()
	req := BuildEscalationRequest("agent-src", cl, target, 2)
	after := time.Now()

	if req.SourceAgent != "agent-src" {
		t.Errorf("SourceAgent = %q, want %q", req.SourceAgent, "agent-src")
	}
	if req.TaskID != "task-req" {
		t.Errorf("TaskID = %q, want %q", req.TaskID, "task-req")
	}
	if req.Confidence != cl {
		t.Error("Confidence pointer does not match input")
	}
	if req.Target != target {
		t.Error("Target pointer does not match input")
	}
	if req.Depth != 2 {
		t.Errorf("Depth = %d, want 2", req.Depth)
	}
	if req.Timestamp.Before(before) || req.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in [%v, %v]", req.Timestamp, before, after)
	}
}
