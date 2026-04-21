package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
)

func TestConsultationTargets_ConsultPeerReadsTargetAgentType(t *testing.T) {
	targets := consultationTargets("consult_peer", map[string]any{
		"target_agent_type": "librarian",
		"query":             "Any prior art?",
	})
	if len(targets) != 1 || targets[0] != "librarian" {
		t.Fatalf("consult_peer targets = %#v, want [librarian]", targets)
	}
}

func TestConsultationTargets_ConsultPrePlanningExpandsTargets(t *testing.T) {
	targets := consultationTargets("consult", map[string]any{
		"mode":             "pre_planning",
		"query":            "Build the plan",
		"include_academic": true,
	})
	if len(targets) != 3 || targets[0] != "librarian" || targets[1] != "archivalist" || targets[2] != "academic" {
		t.Fatalf("pre-planning targets = %#v", targets)
	}

	// Without include_academic, only librarian+archivalist.
	targets = consultationTargets("consult", map[string]any{
		"mode":  "pre_planning",
		"query": "Build the plan",
	})
	if len(targets) != 2 || targets[0] != "librarian" || targets[1] != "archivalist" {
		t.Fatalf("pre-planning (no academic) targets = %#v", targets)
	}

	// Non-pre_planning consult without a target arg falls through to nil.
	targets = consultationTargets("consult", map[string]any{
		"mode":  "single",
		"query": "Build the plan",
	})
	if targets != nil {
		t.Fatalf("consult mode=single without target should be nil, got %#v", targets)
	}
}

func TestConsultationTargets_RequestArchitectResearchAndValidateApproach(t *testing.T) {
	// Emission-side recognizes these, UI fallback must match to avoid
	// silent misclassification when metadata is absent.
	if targets := consultationTargets("request_architect_research", nil); len(targets) != 1 || targets[0] != "architect" {
		t.Fatalf("request_architect_research targets = %#v", targets)
	}
	if targets := consultationTargets("validate_approach", nil); len(targets) != 1 || targets[0] != "librarian" {
		t.Fatalf("validate_approach targets = %#v", targets)
	}
}

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
	if !handleInterAgentToolCallInList(&calls, "engineer", complete, interAgentDispatchCallbacks{}) {
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

// TestHandleInterAgentToolCallInList_OriginUpdateDropsOnMiss verifies the
// regression fix for the duplicate-agent-row bug class: when an origin-update
// event (validate_work, process_validation, etc.) arrives at a list scope
// that does NOT contain the originator row, the handler must NOT fallback-
// append a phantom duplicate. It must either route cross-scope via the
// provided callback or drop the event. Previous behavior appended a new
// row to the nested list, producing visible duplicates like a second
// "tester-pipeline" row under the tester's own nested slot.
func TestHandleInterAgentToolCallInList_OriginUpdateDropsOnMiss(t *testing.T) {
	calls := []ToolCallRecord{{
		ToolCallKey: "tester_tool_abc",
		ToolName:    "run_test_suite",
		InterAgent:  nil, // ordinary local tool, not the originator row
	}}
	origUpdate := msg.ToolCallEventMsg{
		ToolCallKey: "validate_work_xyz",
		ToolName:    "validate_work",
		Phase:       1,
		Success:     true,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:         "challenge",
			AgentTypes:   []string{"inspector-pipeline"},
			Summary:      "Audit result: pass",
			Status:       "done",
			ThreadKey:    "pipeline:challenge-does-not-exist-in-this-list",
			UpdateOrigin: true,
		},
	}
	crossScopeCalled := false
	cb := interAgentDispatchCallbacks{
		crossScopeOriginUpdate: func(ev msg.ToolCallEventMsg) bool {
			crossScopeCalled = true
			return false // simulate: originator row doesn't exist anywhere
		},
	}
	if !handleInterAgentToolCallInList(&calls, "tester-pipeline", origUpdate, cb) {
		t.Fatal("expected origin-update to be consumed")
	}
	if !crossScopeCalled {
		t.Fatal("expected cross-scope callback to be invoked for origin-update miss")
	}
	if len(calls) != 1 {
		t.Fatalf("calls len = %d, want 1 (no fallback-append permitted for origin-updates)", len(calls))
	}
	// The single pre-existing record must be untouched.
	if calls[0].ToolName != "run_test_suite" || calls[0].ToolCallKey != "tester_tool_abc" {
		t.Fatalf("existing record mutated: %#v", calls[0])
	}
}

// TestHandleInterAgentToolCallInList_OriginUpdateRoutesCrossScope verifies
// that origin-updates with a matching cross-scope target do NOT fallback-
// append even if the callback returns true. They are consumed by the
// cross-scope handler and never touch the local list.
func TestHandleInterAgentToolCallInList_OriginUpdateRoutesCrossScope(t *testing.T) {
	calls := []ToolCallRecord{}
	origUpdate := msg.ToolCallEventMsg{
		ToolCallKey: "validate_work_abc",
		ToolName:    "validate_work",
		Phase:       1,
		Success:     true,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:         "challenge",
			AgentTypes:   []string{"inspector-pipeline"},
			Summary:      "Pass",
			Status:       "done",
			ThreadKey:    "pipeline:challenge-abc",
			UpdateOrigin: true,
		},
	}
	cb := interAgentDispatchCallbacks{
		crossScopeOriginUpdate: func(ev msg.ToolCallEventMsg) bool {
			return true // simulate: originator row found and patched elsewhere
		},
	}
	if !handleInterAgentToolCallInList(&calls, "tester-pipeline", origUpdate, cb) {
		t.Fatal("expected origin-update to be consumed")
	}
	if len(calls) != 0 {
		t.Fatalf("calls len = %d, want 0 (cross-scope handled the update, no local record created)", len(calls))
	}
}

// TestHandleInterAgentToolCallInList_OriginUpdatePhase0Ignored verifies that
// Phase 0 events for response tools (validate_work start) never open a row
// in any scope. Classification now keys off either the UpdateOrigin flag
// (Phase 1) or the shared tool-name classifier (Phase 0) — both routes
// converge through eventIsInterAgentOriginUpdate.
func TestHandleInterAgentToolCallInList_OriginUpdatePhase0Ignored(t *testing.T) {
	calls := []ToolCallRecord{}
	start := msg.ToolCallEventMsg{
		ToolCallKey: "validate_work_abc",
		ToolName:    "validate_work",
		Phase:       0,
		// Phase 0 events for response tools carry no InterAgent metadata
		// (emission side returns nil from DeriveInterAgentToolEvent); the
		// UI-side classifier falls back on tool name.
		InterAgent: nil,
	}
	if !handleInterAgentToolCallInList(&calls, "tester-pipeline", start, interAgentDispatchCallbacks{}) {
		t.Fatal("expected Phase 0 response-tool event to be consumed (swallowed)")
	}
	if len(calls) != 0 {
		t.Fatalf("calls len = %d, want 0 (response tool must never open a row)", len(calls))
	}
}

// TestApplyInterAgentOriginUpdateToList_MatchesChildInsideInterAgentChildren
// verifies that the recursive walker finds a ThreadKey living only inside
// tc.InterAgent.Children[j].ToolCalls — the post-merge scope that occurs
// after a nested slot is upserted into its parent row and the slot is
// cleared. findInterAgentThread only scans top-level entry.ToolCalls, so
// without this recursion the origin-update would be silently dropped.
func TestApplyInterAgentOriginUpdateToList_MatchesChildInsideInterAgentChildren(t *testing.T) {
	const targetKey = "pipeline:challenge-nested-child"
	calls := []ToolCallRecord{{
		ToolCallKey: "parent-row",
		InterAgent: &InterAgentTool{
			Kind:       InterAgentToolChallenge,
			AgentTypes: []string{"inspector-pipeline"},
			ThreadKey:  "pipeline:parent-thread",
			Status:     InterAgentToolPending,
			Children: []InterAgentChildActivity{{
				CorrelationID: "nested-cid",
				AgentType:     "tester-pipeline",
				ToolCalls: []ToolCallRecord{{
					ToolCallKey: "nested-child-row",
					InterAgent: &InterAgentTool{
						Kind:       InterAgentToolChallenge,
						AgentTypes: []string{"inspector-pipeline"},
						ThreadKey:  targetKey,
						Status:     InterAgentToolPending,
					},
				}},
			}},
		},
	}}
	row := &InterAgentTool{
		Kind:       InterAgentToolChallenge,
		AgentTypes: []string{"inspector-pipeline"},
		Summary:    "Pass",
		Status:     InterAgentToolDone,
		ThreadKey:  targetKey,
	}
	if !applyInterAgentOriginUpdateToList(&calls, targetKey, row) {
		t.Fatal("expected origin-update to match nested child ThreadKey")
	}
	got := calls[0].InterAgent.Children[0].ToolCalls[0]
	if got.InterAgent.Status != InterAgentToolDone {
		t.Fatalf("nested child status = %q, want Done", got.InterAgent.Status)
	}
	if !got.Completed {
		t.Fatal("nested child not marked completed")
	}
	if got.InterAgent.Summary != "Pass" {
		t.Fatalf("nested child summary = %q, want Pass", got.InterAgent.Summary)
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
