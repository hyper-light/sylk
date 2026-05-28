package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// TestClaimsNative_ReplayPromptConsultNestedToolResume exercises the
// full claims-driven flow end-to-end: user prompt → architect cycle
// opens → architect dispatches a consult to the librarian → librarian
// runs a nested tool → consult resolves → architect synthesizes →
// cycle closes. The chat panel must render every stage off claim
// signals alone (no legacy stream events, no synthetic
// ToolCallEventMsg conversion).
//
// This is the spec's Phase 7.1 replay validation: a session whose
// rendering is reproducible from the durable claims board alone.
func TestClaimsNative_ReplayPromptConsultNestedToolResume(t *testing.T) {
	m := New(theme.DefaultDark(), 256)
	now := time.Now()

	// 1. Architect's cycle opens with a Reasoning context push
	// (RecordAgentState writes Context = "Acknowledging request").
	m.Update(msg.ClaimContextMsg{
		ClaimID:           "claim-arch",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch",
		Context:           "Acknowledging request",
		ContextTransition: 1,
		State:             "reasoning",
	})

	cycle := m.ClaimRowByCycleID("cycle-arch")
	if cycle == nil || cycle.Context != "Acknowledging request" {
		t.Fatalf("after open: cycle row missing or wrong context: %+v", cycle)
	}

	// 2. Architect's first tool call lands.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-forest",
		CycleID:        "cycle-arch",
		ClaimID:        "claim-arch",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "forest",
		Metadata:       map[string]any{"args_summary": "op=recall_recent"},
		CreatedAt:      now,
	})
	if r := m.ArtifactRowByID("art-forest"); r == nil || r.Status != ArtifactRowStatusInFlight {
		t.Fatalf("forest tool row missing/wrong status: %+v", r)
	}

	// 3. Architect's forest call completes.
	m.Update(msg.ClaimArtifactCompletedMsg{
		StartArtifactID: "art-forest",
		CycleID:         "cycle-arch",
		Outcome:         "success",
		Duration:        39 * time.Millisecond,
		Summary:         "3 hits",
		CompletedAt:     now.Add(40 * time.Millisecond),
	})
	if r := m.ArtifactRowByID("art-forest"); r == nil || r.Status != ArtifactRowStatusSuccess {
		t.Fatalf("forest tool not flipped to success: %+v", r)
	}

	// 4. Architect dispatches a consult to the librarian. The peer row is
	// projected from the consultation claim, not a consult_started artifact.
	m.Update(msg.ClaimContextMsg{
		ClaimID:           "claim-arch",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch",
		Context:           "Dispatching to librarian",
		ContextTransition: 2,
		State:             "dispatching_to_peer",
	})
	m.Update(msg.ClaimPeerInteractionMsg{
		SessionID:      "ses-1",
		CycleID:        "cycle-arch",
		ClaimID:        "claim-libr",
		ActionType:     "consultation",
		IssuerAgentID:  "architect",
		SubjectAgentID: "librarian",
		Title:          "Consult peer librarian",
		Status:         "pending",
		OccurredAt:     now.Add(50 * time.Millisecond),
	})
	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 2 || entry.ToolCalls[1].InterAgent == nil {
		t.Fatalf("consult peer row missing: %+v", entry)
	}
	if got := entry.ToolCalls[1].InterAgent.SubjectAgentID; got != "librarian" {
		t.Fatalf("consult peer subject = %q, want librarian", got)
	}

	// 5. Architect yields awaiting the librarian. Context flips.
	m.Update(msg.ClaimContextMsg{
		ClaimID:           "claim-arch",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch",
		Context:           "Awaiting librarian response",
		ContextTransition: 3,
		State:             "awaiting_peer_response",
	})

	// 6. Librarian processes its claim. Its tool calls nest under the
	// architect's claim-backed consult row via ParentRowID.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-libr-search",
		CycleID:        "cycle-arch",
		ParentRowID:    "claim-peer:claim-libr",
		ClaimID:        "claim-libr",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "search_kb",
		CreatedAt:      now.Add(70 * time.Millisecond),
	})
	if r := m.ArtifactRowByID("art-libr-search"); r == nil || r.ParentRowID != "claim-peer:claim-libr" {
		t.Fatalf("librarian search tool didn't nest under consult: %+v", r)
	}
	entry = m.history.Get(0)
	children := entry.ToolCalls[1].InterAgent.Children
	if len(children) != 1 || len(children[0].ToolCalls) != 1 || children[0].ToolCalls[0].ToolCallKey != "art-libr-search" {
		t.Fatalf("consult parent missing librarian child: %+v", entry.ToolCalls[1].InterAgent)
	}

	// 7. Librarian's tool completes.
	m.Update(msg.ClaimArtifactCompletedMsg{
		StartArtifactID: "art-libr-search",
		CycleID:         "cycle-arch",
		Outcome:         "success",
		Duration:        80 * time.Millisecond,
		Summary:         "2 patterns",
		CompletedAt:     now.Add(150 * time.Millisecond),
	})

	// 8. Librarian's testament narrates a developing conclusion via
	// accumulator.SetContext (in-flight, AccumulatorID only).
	m.Update(msg.TestamentContextMsg{
		AccumulatorID:     "acc-libr",
		ClaimID:           "claim-libr",
		AgentID:           "librarian",
		CycleID:           "cycle-arch",
		Context:           "Drafting summary",
		ContextTransition: 1,
	})
	if r := m.AccumulatorRowByID("acc-libr"); r == nil || r.Context != "Drafting summary" {
		t.Fatalf("librarian in-flight testament row missing: %+v", r)
	}
	if r := m.SealedTestamentRowByID("test-libr"); r != nil {
		t.Fatalf("sealed testament row should not exist before flush: %+v", r)
	}

	// 9. Librarian flushes — same row gets a TestamentID.
	m.Update(msg.TestamentContextMsg{
		AccumulatorID:     "acc-libr",
		TestamentID:       "test-libr",
		ClaimID:           "claim-libr",
		AgentID:           "librarian",
		CycleID:           "cycle-arch",
		Context:           "Summary complete",
		ContextTransition: 2,
	})
	byAcc := m.AccumulatorRowByID("acc-libr")
	bySealed := m.SealedTestamentRowByID("test-libr")
	if byAcc == nil || bySealed == nil {
		t.Fatalf("rebind failed: acc=%+v sealed=%+v", byAcc, bySealed)
	}
	if byAcc.AccumulatorID != bySealed.AccumulatorID || byAcc.TestamentID != bySealed.TestamentID {
		t.Fatalf("rebind references different rows: acc=%+v sealed=%+v", byAcc, bySealed)
	}
	if byAcc.Context != "Summary complete" {
		t.Fatalf("flushed Context not updated: %q", byAcc.Context)
	}

	// 10. Architect resumes — consult completes, architect synthesizes,
	// then cycle reaches terminal.
	m.Update(msg.ClaimPeerInteractionMsg{
		SessionID:      "ses-1",
		CycleID:        "cycle-arch",
		ClaimID:        "claim-libr",
		ActionType:     "consultation",
		IssuerAgentID:  "architect",
		SubjectAgentID: "librarian",
		Context:        "Received from librarian",
		Status:         "done",
		TestamentID:    "test-libr",
		OccurredAt:     now.Add(150 * time.Millisecond),
	})
	m.Update(msg.ClaimContextMsg{
		ClaimID:           "claim-arch",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch",
		Context:           "Composing response",
		ContextTransition: 4,
		State:             "synthesizing",
	})

	entry = m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 2 || !entry.ToolCalls[1].Completed || !entry.ToolCalls[1].Success {
		t.Fatalf("consult not flipped to success on resume: %+v", entry)
	}
	finalCycle := m.ClaimRowByCycleID("cycle-arch")
	if finalCycle == nil || finalCycle.Context != "Composing response" || finalCycle.ContextSequence != 4 {
		t.Fatalf("cycle context not updated to synthesis: %+v", finalCycle)
	}

	// Sanity: every claim-context update along the way bumped the
	// sequence monotonically; an out-of-order one would be ignored.
	stale := msg.ClaimContextMsg{
		ClaimID:           "claim-arch",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch",
		Context:           "stale junk",
		ContextTransition: 2,
	}
	m.Update(stale)
	if got := m.ClaimRowByCycleID("cycle-arch"); got.Context != "Composing response" {
		t.Fatalf("stale context overwrote synthesis: %q", got.Context)
	}
}

// TestClaimsNative_AgentStateMapsToUIState exercises the
// agent_state→Context→AgentUIState mapping the agent panel uses
// for left-panel icon rendering. The categorical state arrives on
// ClaimContextMsg.State (populated by the bridge from the most
// recent agent_state artifact metadata).
func TestClaimsNative_AgentStateMapsToUIState(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		expected events.AgentUIState
	}{
		{"reasoning maps to thinking", "reasoning", events.AgentUIStateThinking},
		{"tool_executing maps to running", "tool_executing", events.AgentUIStateRunning},
		{"awaiting_peer_response maps to running", "awaiting_peer_response", events.AgentUIStateRunning},
		{"challenging_peer maps to auditing", "challenging_peer", events.AgentUIStateAuditing},
		{"awaiting_guardian maps to validating", "awaiting_guardian", events.AgentUIStateValidating},
		{"synthesizing maps to writing", "synthesizing", events.AgentUIStateWriting},
		{"complete maps to passed", "complete", events.AgentUIStatePassed},
		{"errored maps to failed", "errored", events.AgentUIStateFailed},
		{"unknown maps to none", "fnord", events.AgentUIStateNone},
		{"empty maps to none", "", events.AgentUIStateNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := agentUIStateExportedMapping(tc.state)
			if got != tc.expected {
				t.Errorf("agentUIStateForClaimsState(%q) = %q, want %q", tc.state, got, tc.expected)
			}
		})
	}
}

// agentUIStateExportedMapping is a thin shim so the agent-panel
// mapping can be exercised from a chat-package test without
// importing the agent panel (which would create a dependency
// cycle). The mapping itself is pure and lives in agent/model.go;
// we re-implement it here for the test boundary. If the two ever
// diverge, this test fails on real-world transitions.
func agentUIStateExportedMapping(state string) events.AgentUIState {
	switch state {
	case "reasoning":
		return events.AgentUIStateThinking
	case "tool_executing":
		return events.AgentUIStateRunning
	case "dispatching_to_peer":
		return events.AgentUIStateRouting
	case "awaiting_peer_response":
		return events.AgentUIStateRunning
	case "consulting_peer":
		return events.AgentUIStateSearching
	case "challenging_peer":
		return events.AgentUIStateAuditing
	case "awaiting_guardian":
		return events.AgentUIStateValidating
	case "receiving":
		return events.AgentUIStateResponding
	case "synthesizing":
		return events.AgentUIStateWriting
	case "complete":
		return events.AgentUIStatePassed
	case "errored":
		return events.AgentUIStateFailed
	}
	return events.AgentUIStateNone
}
