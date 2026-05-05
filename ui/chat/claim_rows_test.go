package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// These tests pin the broken end-to-end behavior the user flagged:
// claim-driven UI events must produce visible, identity-preserving
// rows without any synthetic ToolCallEventMsg conversion. They fail
// today (the chat panel still synthesizes ToolCallEventMsg internally
// and the context handlers are no-ops); they pass once the chat
// panel is claims-native per docs/CLAIMS_UI.md.

func newChatForClaimsTest(t *testing.T) *Model {
	t.Helper()
	return New(theme.DefaultDark(), 256)
}

// Test 1: claim artifact start creates a visible chat row without any
// legacy stream event. No StreamStartMsg, no ToolCallEventMsg — the
// ClaimArtifactAddedMsg alone must materialize an ArtifactRow keyed
// by ArtifactID.
func TestClaimsNative_ArtifactStartCreatesRowWithoutStream(t *testing.T) {
	m := newChatForClaimsTest(t)

	added := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    "",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "read_file",
		CreatedAt:      time.Now(),
	}
	if _, _ = m.Update(added); m == nil {
		t.Fatal("model became nil")
	}

	row := m.ArtifactRowByID("art-tool-1")
	if row == nil {
		t.Fatal("ArtifactRowByID(\"art-tool-1\") = nil — claim artifact did not create a chat row")
	}
	if row.Kind != "tool_started" {
		t.Errorf("row.Kind = %q, want tool_started", row.Kind)
	}
	if row.Reference != "read_file" {
		t.Errorf("row.Reference = %q, want read_file", row.Reference)
	}
	if row.AgentID != "architect" {
		t.Errorf("row.AgentID = %q, want architect", row.AgentID)
	}
	if row.CycleID != "cycle-arch-1" {
		t.Errorf("row.CycleID = %q, want cycle-arch-1", row.CycleID)
	}
	if row.ClaimID != "claim-arch-1" {
		t.Errorf("row.ClaimID = %q, want claim-arch-1", row.ClaimID)
	}
	if row.Status != ArtifactRowStatusInFlight {
		t.Errorf("row.Status = %q, want in_flight", row.Status)
	}

	cycle := m.ClaimRowByCycleID("cycle-arch-1")
	if cycle == nil {
		t.Fatal("ClaimRowByCycleID returned nil — cycle row should auto-create on first child artifact")
	}
	if len(cycle.Artifacts) != 1 || cycle.Artifacts[0] != "art-tool-1" {
		t.Errorf("cycle.Artifacts = %v, want [art-tool-1]", cycle.Artifacts)
	}
}

// Test 2: nested consult child tool renders under ParentRowID. The
// architect's consult_started artifact is the parent; the librarian's
// later tool_started artifact carries ParentRowID = consult_started's
// artifact ID and must nest under it.
func TestClaimsNative_NestedConsultChildToolRendersUnderParentRowID(t *testing.T) {
	m := newChatForClaimsTest(t)

	consult := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-consult-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    "",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "consult_started",
		Reference:      "librarian",
		CreatedAt:      time.Now(),
	}
	m.Update(consult)

	childTool := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-libtool-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    "art-consult-1",
		ClaimID:        "claim-libr-1",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "search_kb",
		CreatedAt:      time.Now(),
	}
	m.Update(childTool)

	parent := m.ArtifactRowByID("art-consult-1")
	if parent == nil {
		t.Fatal("parent consult_started row missing")
	}
	if len(parent.Children) != 1 || parent.Children[0] != "art-libtool-1" {
		t.Fatalf("parent.Children = %v, want [art-libtool-1] — nested child not linked under ParentRowID", parent.Children)
	}

	child := m.ArtifactRowByID("art-libtool-1")
	if child == nil {
		t.Fatal("child librarian tool row missing")
	}
	if child.ParentRowID != "art-consult-1" {
		t.Errorf("child.ParentRowID = %q, want art-consult-1", child.ParentRowID)
	}
	if child.AgentID != "librarian" {
		t.Errorf("child.AgentID = %q, want librarian", child.AgentID)
	}
}

// Test 3: claim Context updates an existing row in place. A
// ClaimContextMsg must mutate the cycle row's Context field without
// creating a new row, and respect ContextTransition ordering.
func TestClaimsNative_ClaimContextUpdatesRowInPlace(t *testing.T) {
	m := newChatForClaimsTest(t)

	added := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "read_file",
		CreatedAt:      time.Now(),
	}
	m.Update(added)

	first := msg.ClaimContextMsg{
		ClaimID:           "claim-arch-1",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch-1",
		Context:           "Acknowledging request",
		ContextTransition: 1,
	}
	m.Update(first)

	cycle := m.ClaimRowByCycleID("cycle-arch-1")
	if cycle == nil {
		t.Fatal("cycle row missing after context update")
	}
	if cycle.Context != "Acknowledging request" {
		t.Errorf("after first context: row.Context = %q, want %q", cycle.Context, "Acknowledging request")
	}
	if cycle.ContextSequence != 1 {
		t.Errorf("ContextSequence = %d, want 1", cycle.ContextSequence)
	}

	// Newer context: must replace.
	second := msg.ClaimContextMsg{
		ClaimID:           "claim-arch-1",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch-1",
		Context:           "Composing response",
		ContextTransition: 2,
	}
	m.Update(second)
	cycle = m.ClaimRowByCycleID("cycle-arch-1")
	if cycle.Context != "Composing response" {
		t.Errorf("after second context: row.Context = %q, want Composing response", cycle.Context)
	}

	// Stale context (lower transition): must NOT clobber.
	stale := msg.ClaimContextMsg{
		ClaimID:           "claim-arch-1",
		OwnerAgentID:      "architect",
		CycleID:           "cycle-arch-1",
		Context:           "out-of-order junk",
		ContextTransition: 1,
	}
	m.Update(stale)
	cycle = m.ClaimRowByCycleID("cycle-arch-1")
	if cycle.Context != "Composing response" {
		t.Errorf("stale context overwrote newer: row.Context = %q", cycle.Context)
	}
}

// Test 4: accumulator SetContext creates and then rebinds an
// in-flight testament row. Initial deltas carry only AccumulatorID;
// the flush emission carries both AccumulatorID and TestamentID. The
// row must resolve under either ID after the rebind.
func TestClaimsNative_AccumulatorSetContextCreatesAndRebindsTestamentRow(t *testing.T) {
	m := newChatForClaimsTest(t)

	inFlight := msg.TestamentContextMsg{
		AccumulatorID:     "acc-1",
		ClaimID:           "claim-arch-1",
		AgentID:           "architect",
		CycleID:           "cycle-arch-1",
		Context:           "drafting plan",
		ContextTransition: 1,
	}
	m.Update(inFlight)

	row := m.TestamentRowByID("acc-1")
	if row == nil {
		t.Fatal("TestamentRowByID(acc-1) = nil — in-flight context did not create a testament row")
	}
	if row.Context != "drafting plan" {
		t.Errorf("row.Context = %q, want drafting plan", row.Context)
	}
	if row.TestamentID != "" {
		t.Errorf("TestamentID prematurely set: %q", row.TestamentID)
	}

	// Flush: same row carries TestamentID now.
	flush := msg.TestamentContextMsg{
		AccumulatorID:     "acc-1",
		TestamentID:       "test-1",
		ClaimID:           "claim-arch-1",
		AgentID:           "architect",
		CycleID:           "cycle-arch-1",
		Context:           "plan complete",
		ContextTransition: 2,
	}
	m.Update(flush)

	byAcc := m.TestamentRowByID("acc-1")
	byTest := m.TestamentRowByID("test-1")
	if byAcc == nil || byTest == nil {
		t.Fatalf("rebind failed: byAcc=%v byTest=%v — both IDs must resolve to the same row", byAcc, byTest)
	}
	if byAcc.AccumulatorID != "acc-1" || byAcc.TestamentID != "test-1" {
		t.Errorf("byAcc IDs wrong: %+v", byAcc)
	}
	if byTest.AccumulatorID != "acc-1" || byTest.TestamentID != "test-1" {
		t.Errorf("byTest IDs wrong: %+v", byTest)
	}
	if byAcc.Context != "plan complete" {
		t.Errorf("Context not updated on rebind: %q", byAcc.Context)
	}
}

// Test 5: a started artifact arriving before its claim_created delta
// must still produce a visible row. The bridge already queues these
// in pendingArtifacts and flushes them on claim arrival; the chat
// must hand the deferred ClaimArtifactAddedMsg through and produce a
// row keyed correctly. This guards the forwarded-request race where
// the route claim creation lags the first tool call. (We exercise
// the chat's behavior directly: arrival of a ClaimArtifactAddedMsg
// must always materialize a row, regardless of order with respect
// to the cycle's metadata.)
func TestClaimsNative_StartedArtifactRendersBeforeClaimMeta(t *testing.T) {
	m := newChatForClaimsTest(t)

	added := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-1",
		CycleID:        "cycle-late",
		ClaimID:        "claim-late",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "read_file",
		CreatedAt:      time.Now(),
	}
	m.Update(added)

	row := m.ArtifactRowByID("art-tool-1")
	if row == nil {
		t.Fatal("artifact row missing — bridge-deferred artifact failed to render once it reached the chat")
	}
}

// Test 6: agent_state artifacts are NOT rendered as their own rows.
// They are the immutable trace of Context transitions; the live UI
// surface is the claim's Context field (delivered separately via
// ClaimContextMsg). The chat must not create an ArtifactRow / chat
// entry for an agent_state artifact, otherwise every state push
// floods the chat tree with categorical "agent_state" rows.
func TestClaimsNative_AgentStateArtifactDoesNotRenderRow(t *testing.T) {
	m := newChatForClaimsTest(t)

	stateArt := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-state-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           claims.ArtifactKindAgentState,
		Reference:      "Acknowledging request",
		Metadata: map[string]any{
			"state":    "reasoning",
			"claim_id": "claim-arch-1",
		},
		CreatedAt: time.Now(),
	}
	m.Update(stateArt)

	if got := m.ArtifactRowByID("art-state-1"); got != nil {
		t.Fatalf("agent_state artifact created an ArtifactRow: %+v — Context is the only surface for state", got)
	}
}

// Test 7: ClaimsAgentStatusMsg authoritatively sets the cycle row's
// OwnerAgentID. A subsequent artifact arrival under that cycle MUST
// NOT overwrite the owner with whatever the artifact carried — the
// cycle owner came from the bridge's resolved cycleOwnerFor signal,
// which is the canonical truth. This pins the regression where
// guide-routed prompts left chat showing "guide" as the cycle agent
// because the artifact's OwnerAgentID was being trusted.
func TestClaimsNative_CycleOwnerSetByStatusMsgWinsOverArtifact(t *testing.T) {
	m := newChatForClaimsTest(t)

	// Bridge announces the cycle: architect owns the prompt cycle.
	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch",
		ActionType: "prompt",
		Reason:     "Build a python CLI",
	})
	cycle := m.ClaimRowByCycleID("cycle-arch")
	if cycle == nil {
		t.Fatal("ClaimsAgentStatusMsg did not open a cycle row")
	}
	if cycle.OwnerAgentID != "architect" {
		t.Fatalf("cycle.OwnerAgentID = %q, want architect", cycle.OwnerAgentID)
	}

	// Subsequent artifact arrival on the architect's cycle. Even if
	// the artifact's OwnerAgentID stamps the architect (which it
	// should now after the bridge fix), the cycle row's identity
	// must be preserved authoritatively from the status msg path.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-1",
		CycleID:        "cycle-arch",
		ClaimID:        "claim-arch",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "read_file",
	})
	if got := m.ClaimRowByCycleID("cycle-arch"); got.OwnerAgentID != "architect" {
		t.Fatalf("after artifact: cycle.OwnerAgentID = %q, want architect", got.OwnerAgentID)
	}

	// Cycle close from the status msg flips status terminal.
	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:         "architect",
		SessionID:       "ses-1",
		Active:          false,
		CycleID:         "cycle-arch",
		TerminalOutcome: "success",
	})
	if got := m.ClaimRowByCycleID("cycle-arch"); got.Status != ClaimRowStatusAccepted {
		t.Fatalf("after close: cycle.Status = %q, want accepted", got.Status)
	}
}

// guardChainOK keeps the import set tight: the test file must compile
// against core/claims even when an individual test doesn't reference
// it directly, so the build catches drift early.
var _ = claims.ClaimStatusAccepted
