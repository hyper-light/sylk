package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
)

func TestConsultationResponseSummary_GuardianPayloadPrefersHumanMessage(t *testing.T) {
	summary := consultationResponseSummary(map[string]any{
		"target": "guardian",
		"data": map[string]any{
			"user_message": "Safe to proceed once the risky command is approved.",
			"reason":       "guardian-approved deterministic control-plane grant",
		},
	})
	if summary != "Safe to proceed once the risky command is approved." {
		t.Fatalf("consultation summary = %q", summary)
	}
}

func TestConsultationResponseSummary_AcademicPayloadUsesContent(t *testing.T) {
	summary := consultationResponseSummary(map[string]any{
		"type":    "recall",
		"content": "Use Typer for most new Python CLIs and ground details against the official docs.",
	})
	if summary != "Use Typer for most new Python CLIs and ground details against the official docs." {
		t.Fatalf("consultation summary = %q", summary)
	}
}

func TestBuildInterAgentStartRecord_MergesMetadataWithFallbackTargets(t *testing.T) {
	startedAt := time.Now()
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-1",
		ToolCallKey:   "consult-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find relevant in-repo patterns."}`,
		Phase:         0,
		StartedAt:     startedAt,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:    "consult",
			Status:  "pending",
			Summary: "",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven consult start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "librarian" {
		t.Fatalf("agent types = %#v, want [librarian]", got)
	}
	if got := record.InterAgent.Summary; got != "Find relevant in-repo patterns." {
		t.Fatalf("summary = %q, want fallback query", got)
	}
	if got := record.ToolCallKey; got != "consult-1" {
		t.Fatalf("tool call key = %q, want consult-1", got)
	}
}

func TestBuildInterAgentStartRecord_MergesMetadataWithFallbackChallengeTargets(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-2",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_architect",
		FullArgs:      `{"target_agent":"architect","request":"Reassess the plan."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:   "challenge",
			Status: "pending",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven challenge start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "architect" {
		t.Fatalf("agent types = %#v, want [architect]", got)
	}
	if got := record.InterAgent.Summary; got != "Reassess the plan." {
		t.Fatalf("summary = %q, want fallback request", got)
	}
}

func TestBuildInterAgentStartRecord_UsesApprovalMetadata(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-approval",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			AgentTypes: []string{"guardian"},
			Summary:    "Requesting Guardian approval for example.com",
			Status:     "pending",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven approval start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.Kind; got != InterAgentToolApproval {
		t.Fatalf("kind = %q, want %q", got, InterAgentToolApproval)
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "guardian" {
		t.Fatalf("agent types = %#v, want [guardian]", got)
	}
}

func TestBuildInterAgentStartRecord_PipelineChallengeNormalizesTesterLabel(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline",
		ToolCallKey:   "challenge-pipeline-1",
		ToolName:      "challenge_agent",
		AgentType:     "inspector-pipeline",
		PipelineID:    "task_1",
		FullArgs:      `{"target_agents":["tester"],"request":"Audit the pipeline results."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:   "challenge",
			Status: "pending",
		},
	})
	if !ok {
		t.Fatal("expected pipeline challenge start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("agent types = %#v, want [tester-pipeline]", got)
	}
}

func TestEnsureInterAgentTerminalStatus_NormalizesApprovalPendingOnSuccess(t *testing.T) {
	// Reproduces the "guardian - approved by user" stuck-pending bug: a
	// completion event that left InterAgent.Status at the default "pending"
	// must be normalized to Done so the renderer freezes the duration.
	record := ToolCallRecord{
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:   InterAgentToolApproval,
			Status: InterAgentToolPending,
		},
	}
	ensureInterAgentTerminalStatus(&record, msg.ToolCallEventMsg{Success: true})
	if record.InterAgent.Status != InterAgentToolDone {
		t.Fatalf("approval Status = %q, want Done after Success completion", record.InterAgent.Status)
	}
}

func TestEnsureInterAgentTerminalStatus_NormalizesApprovalPendingOnFailure(t *testing.T) {
	record := ToolCallRecord{
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:   InterAgentToolApproval,
			Status: InterAgentToolPending,
		},
	}
	ensureInterAgentTerminalStatus(&record, msg.ToolCallEventMsg{Success: false})
	if record.InterAgent.Status != InterAgentToolFailed {
		t.Fatalf("approval Status = %q, want Failed after non-Success completion", record.InterAgent.Status)
	}
}

func TestEnsureInterAgentTerminalStatus_LeavesChallengePendingAlone(t *testing.T) {
	// Challenges deliberately stay Pending after dispatch — must not be
	// flipped to Done by the normalization guard.
	record := ToolCallRecord{
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:   InterAgentToolChallenge,
			Status: InterAgentToolPending,
		},
	}
	ensureInterAgentTerminalStatus(&record, msg.ToolCallEventMsg{Success: true})
	if record.InterAgent.Status != InterAgentToolPending {
		t.Fatalf("challenge Status = %q, want Pending preserved", record.InterAgent.Status)
	}
}

func TestHandleInterAgentToolCallInList_ApprovalPairsByLifecycleID(t *testing.T) {
	// After the lifecycle-ID refactor, a synthetic approval branch's Start
	// and its Complete (emitted by the same InterAgentBranchHandle via
	// h.Complete, see agents/shared/inter_agent_branch.go) share one
	// ToolCallKey by construction. Verify pairing is a straight ID match
	// and no Kind+AgentTypes fallback is needed.
	calls := []ToolCallRecord{{
		ToolCallKey: "approval_stable",
		ToolName:    "approval_guardian",
		StartedAt:   time.Now().Add(-2 * time.Second),
		InterAgent: &InterAgentTool{
			Kind:       InterAgentToolApproval,
			AgentTypes: []string{"guardian"},
			Summary:    "Requesting Guardian approval for run_command",
			Status:     InterAgentToolPending,
		},
	}}
	complete := msg.ToolCallEventMsg{
		ToolCallKey: "approval_stable",
		ToolName:    "approval_guardian",
		Phase:       1,
		Success:     true,
		Duration:    250 * time.Millisecond,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			AgentTypes: []string{"guardian"},
			Summary:    "approved by user",
			Status:     "done",
		},
	}
	if !handleInterAgentToolCallInList(&calls, "engineer", complete) {
		t.Fatal("expected complete event to be handled")
	}
	if len(calls) != 1 {
		t.Fatalf("expected single record (no duplicate), got %d: %#v", len(calls), calls)
	}
	if !calls[0].Completed {
		t.Fatal("expected record to be marked completed")
	}
	if calls[0].InterAgent.Status != InterAgentToolDone {
		t.Fatalf("status = %q, want Done", calls[0].InterAgent.Status)
	}
	if calls[0].InterAgent.Summary != "approved by user" {
		t.Fatalf("summary = %q, want updated to %q", calls[0].InterAgent.Summary, "approved by user")
	}
}

// TestEnsureInterAgentTerminalStatus_FailedChallengeFinalizes verifies the fix
// for the "orchestrator challenge stuck at 486s" escape: when a challenge is
// rejected (ev.Success=false) the row must finalize as Failed instead of
// staying Pending. The Challenge-kind exception only applies to successful
// challenges which legitimately wait for a peer response.
func TestEnsureInterAgentTerminalStatus_FailedChallengeFinalizes(t *testing.T) {
	record := &ToolCallRecord{
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:       InterAgentToolChallenge,
			AgentTypes: []string{"orchestrator"},
			Status:     InterAgentToolPending,
		},
	}
	ev := msg.ToolCallEventMsg{
		Phase:   1,
		Success: false,
	}
	ensureInterAgentTerminalStatus(record, ev)
	if record.InterAgent.Status != InterAgentToolFailed {
		t.Fatalf("status = %q, want Failed for rejected challenge", record.InterAgent.Status)
	}
}

// TestEnsureInterAgentTerminalStatus_SuccessfulChallengeStaysPending confirms
// successful challenge rows still stay pending, waiting on the peer response.
func TestEnsureInterAgentTerminalStatus_SuccessfulChallengeStaysPending(t *testing.T) {
	record := &ToolCallRecord{
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:       InterAgentToolChallenge,
			AgentTypes: []string{"tester-pipeline"},
			Status:     InterAgentToolPending,
		},
	}
	ev := msg.ToolCallEventMsg{
		Phase:   1,
		Success: true,
	}
	ensureInterAgentTerminalStatus(record, ev)
	if record.InterAgent.Status != InterAgentToolPending {
		t.Fatalf("status = %q, want still Pending for dispatched challenge", record.InterAgent.Status)
	}
}
