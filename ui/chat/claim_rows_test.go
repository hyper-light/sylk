package chat

import (
	"strings"
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

func addPeerInteractionForClaimsTest(m *Model, cycleID, claimID, issuer, subject string) string {
	rowID := "claim-peer:" + claimID
	m.Update(msg.ClaimPeerInteractionMsg{
		SessionID:      "ses-1",
		CycleID:        cycleID,
		ClaimID:        claimID,
		ActionType:     "consultation",
		IssuerAgentID:  issuer,
		SubjectAgentID: subject,
		Title:          "Consult peer " + subject,
		Status:         "pending",
		OccurredAt:     time.Now(),
	})
	return rowID
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

func TestClaimsNative_ToolArtifactUsesArgsMetadataForInputSummary(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-args",
		CycleID:        "cycle-args",
		ClaimID:        "claim-args",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "workspace_read",
		Metadata: map[string]any{
			"args": `{"op":"read","path":"README.md","include_tool_output":true}`,
		},
		CreatedAt: time.Now(),
	})

	row := m.ArtifactRowByID("art-tool-args")
	if row == nil {
		t.Fatal("tool artifact row missing")
	}
	if row.ArgsSummary != "op=read path=README.md" {
		t.Fatalf("row.ArgsSummary = %q, want op=read path=README.md", row.ArgsSummary)
	}

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("missing projected tool call: %+v", entry)
	}
	call := entry.ToolCalls[0]
	if call.ToolName != "workspace_read" {
		t.Fatalf("ToolName = %q, want workspace_read", call.ToolName)
	}
	if call.ArgsSummary == call.ToolName {
		t.Fatalf("ArgsSummary duplicated ToolName: %q", call.ArgsSummary)
	}
	if !strings.Contains(call.FullArgs, `"path": "README.md"`) {
		t.Fatalf("FullArgs = %q, want pretty args with path", call.FullArgs)
	}
}

func TestClaimsNative_LifecycleArtifactRowsAreIgnored(t *testing.T) {
	m := newChatForClaimsTest(t)

	base := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-lifecycle",
		CycleID:        "cycle-lifecycle",
		ClaimID:        "claim-lifecycle",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "artifact_lifecycle",
		Reference:      "plan",
		Metadata:       map[string]any{"lifecycle_status": "generated", "args_summary": "artifact generated"},
		CreatedAt:      time.Now(),
	}
	m.Update(base)
	base.Metadata = map[string]any{"lifecycle_status": "validating", "args_summary": "artifact validating"}
	m.Update(base)

	row := m.ArtifactRowByID("art-lifecycle")
	if row != nil {
		t.Fatalf("lifecycle artifact row should not render as a tool row: %+v", row)
	}
	if cycle := m.ClaimRowByCycleID("cycle-lifecycle"); cycle != nil && len(cycle.Artifacts) != 0 {
		t.Fatalf("cycle artifacts = %+v, want no lifecycle pseudo rows", cycle)
	}
}

func TestClaimsNative_ToolArtifactCompletionUsesOutputMetadata(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-tool-output",
		CycleID:        "cycle-output",
		ClaimID:        "claim-output",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "workspace_read",
		Metadata:       map[string]any{"args": `{"path":"README.md"}`},
		CreatedAt:      time.Now(),
	})
	m.Update(msg.ClaimArtifactCompletedMsg{
		StartArtifactID: "art-tool-output",
		CycleID:         "cycle-output",
		Outcome:         "success",
		Duration:        10 * time.Millisecond,
		Metadata:        map[string]any{"output": "read 3 files"},
		CompletedAt:     time.Now().Add(10 * time.Millisecond),
	})

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 1 {
		t.Fatalf("missing projected tool call: %+v", entry)
	}
	call := entry.ToolCalls[0]
	if !call.Completed || !call.Success {
		t.Fatalf("tool call terminal state = completed:%v success:%v", call.Completed, call.Success)
	}
	if call.Output != "read 3 files" {
		t.Fatalf("call.Output = %q, want metadata output", call.Output)
	}
}

// Test 2: nested consult child tool renders under ParentRowID. The
// architect's consult row is claim-backed; the librarian's later
// tool_started artifact carries ParentRowID = claim-peer:<claimID>.
func TestClaimsNative_NestedConsultChildToolRendersUnderParentRowID(t *testing.T) {
	m := newChatForClaimsTest(t)

	parentRowID := addPeerInteractionForClaimsTest(m, "cycle-arch-1", "claim-libr-1", "architect", "librarian")

	childTool := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-libtool-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    parentRowID,
		ClaimID:        "claim-libr-1",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "search_kb",
		CreatedAt:      time.Now(),
	}
	m.Update(childTool)

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("parent consult row missing: %+v", entry)
	}
	children := entry.ToolCalls[0].InterAgent.Children
	if len(children) != 1 || len(children[0].ToolCalls) != 1 || children[0].ToolCalls[0].ToolCallKey != "art-libtool-1" {
		t.Fatalf("nested child not linked under ParentRowID: %+v", entry.ToolCalls[0].InterAgent)
	}

	child := m.ArtifactRowByID("art-libtool-1")
	if child == nil {
		t.Fatal("child librarian tool row missing")
	}
	if child.ParentRowID != parentRowID {
		t.Errorf("child.ParentRowID = %q, want %s", child.ParentRowID, parentRowID)
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

func TestClaimsNative_GuideClassificationDoesNotReserveRouteCorrelation(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-guide-classify",
		ActionType: "prompt",
		Reason:     "Classifying request",
		State:      "classifying",
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		ClaimID:      "claim-guide-classify",
		OwnerAgentID: "guide",
		CycleID:      "cycle-guide-classify",
		Context:      "Request forwarded",
		State:        "routing",
	})

	if m.history.Len() != 1 {
		t.Fatalf("guide classification created %d chat entries, want 1", m.history.Len())
	}
	guideEntry := m.history.Get(0)
	if guideEntry == nil {
		t.Fatal("missing guide classification chat entry")
	}
	if guideEntry.AgentID != "guide" {
		t.Fatalf("guide entry AgentID = %q, want guide", guideEntry.AgentID)
	}
	if guideEntry.CorrelationID != "cycle-guide-classify" {
		t.Fatalf("guide entry CorrelationID = %q, want cycle-guide-classify", guideEntry.CorrelationID)
	}
	for _, id := range guideEntry.AdditionalCorrelationIDs {
		if id == "route-123" {
			t.Fatal("guide classification entry reserved route stream correlation")
		}
	}

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:             "architect",
		SessionID:           "ses-1",
		Active:              true,
		CycleID:             "cycle-architect",
		ActionType:          "prompt",
		Reason:              "Build a python CLI",
		StreamCorrelationID: "route-123",
	})
	if m.history.Len() != 2 {
		t.Fatalf("architect cycle created %d chat entries, want 2", m.history.Len())
	}
	entry := m.history.Get(1)
	if entry == nil {
		t.Fatal("missing architect chat entry")
	}
	if entry.AgentID != "architect" {
		t.Fatalf("entry.AgentID = %q, want architect", entry.AgentID)
	}
	if entry.CorrelationID != "cycle-architect" {
		t.Fatalf("entry.CorrelationID = %q, want cycle-architect", entry.CorrelationID)
	}
}

func TestClaimsNative_ChildResponseTextDoesNotBecomeCycleAnswer(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Plan the CLI",
	})
	parentRowID := addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian", "architect", "librarian")
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "librarian-tool",
		CycleID:        "claim-architect",
		ParentRowID:    parentRowID,
		ClaimID:        "claim-librarian",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "workspace_read",
		CreatedAt:      time.Now(),
	})

	m.Update(msg.ClaimResponseTextMsg{
		SessionID:   "ses-1",
		CycleID:     "claim-architect",
		ClaimID:     "claim-librarian",
		ParentRowID: parentRowID,
		AgentID:     "librarian",
		Content:     "Consultation response intended for the architect only.",
		CreatedAt:   time.Now(),
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry")
	}
	if entry.Content != "" {
		t.Fatalf("child response_text wrote cycle answer = %q", entry.Content)
	}
	if len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("missing consult inter-agent row: %+v", entry.ToolCalls)
	}
	children := entry.ToolCalls[0].InterAgent.Children
	if len(children) != 1 {
		t.Fatalf("consult children = %d, want 1", len(children))
	}
	if !children[0].Completed {
		t.Fatal("child response_text should mark nested child activity completed")
	}
	if children[0].ResultSummary != "Consultation response intended for the architect only." {
		t.Fatalf("child ResultSummary = %q", children[0].ResultSummary)
	}
}

func TestClaimsNative_RootResponseTextBecomesCycleAnswer(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Plan the CLI",
	})
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "pending-tool",
		CycleID:        "claim-architect",
		ClaimID:        "claim-architect",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "tool_started",
		Reference:      "search_skills",
		CreatedAt:      time.Now().Add(-2 * time.Second),
	})
	m.Update(msg.ClaimResponseTextMsg{
		SessionID: "ses-1",
		CycleID:   "claim-architect",
		ClaimID:   "claim-architect",
		AgentID:   "architect",
		Content:   "Here is the final answer.",
		CreatedAt: time.Now(),
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry")
	}
	if entry.Content != "Here is the final answer." {
		t.Fatalf("entry.Content = %q, want final answer", entry.Content)
	}
	if len(entry.ToolCalls) != 1 || !entry.ToolCalls[0].Completed || !entry.ToolCalls[0].SyntheticCompletion {
		t.Fatalf("root response_text should terminalize pending tool rows: %+v", entry.ToolCalls)
	}
	if entry.Streaming {
		t.Fatal("root response_text should stop the cycle entry streaming state")
	}
	if entry.ThinkingStatus != "Complete" || entry.ThinkingText == "" {
		t.Fatalf("root response_text should leave a terminal thinking block, got text=%q status=%q", entry.ThinkingText, entry.ThinkingStatus)
	}
}

func TestClaimsNative_ClaimCycleDrivesThinkingAnimation(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Acknowledging request",
	})

	if !m.HasActiveAnimation() {
		t.Fatal("active claim cycle should keep the chat decor tick chain alive")
	}
	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry")
	}
	if entry.ThinkingText == "" {
		t.Fatal("claim cycle entry did not get placeholder thinking text")
	}
	if entry.ThinkingStatus != "Acknowledging request" {
		t.Fatalf("ThinkingStatus = %q, want Acknowledging request", entry.ThinkingStatus)
	}
	first := entry.ThinkingText

	started := m.claimCycleAnimations["claim-architect"].started
	m.Update(msg.DecorTickMsg{Time: started.Add(250 * time.Millisecond)})
	entry = m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry after decor tick")
	}
	if entry.ThinkingText == first {
		t.Fatalf("ThinkingText did not advance on decor tick: %q", entry.ThinkingText)
	}

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:         "architect",
		SessionID:       "ses-1",
		Active:          false,
		CycleID:         "claim-architect",
		TerminalOutcome: "success",
	})
	entry = m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry after close")
	}
	if entry.Streaming {
		t.Fatal("claim close did not stop streaming state")
	}
	if entry.ThinkingStatus != "Complete" || entry.ThinkingText == "" {
		t.Fatalf("claim close did not leave terminal thinking state: text=%q status=%q", entry.ThinkingText, entry.ThinkingStatus)
	}
}

func TestClaimsNative_RootTerminalClaimContextFinalizesCycleEntry(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-guide",
		ActionType: "prompt",
		Reason:     "Classifying request",
	})
	started := m.claimCycleAnimations["claim-guide"].started
	m.Update(msg.DecorTickMsg{Time: started.Add(250 * time.Millisecond)})

	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		CycleID:      "claim-guide",
		ClaimID:      "claim-guide",
		OwnerAgentID: "guide",
		Context:      "Complete",
		State:        "complete",
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry after terminal context")
	}
	if entry.Streaming {
		t.Fatal("terminal root claim context did not stop streaming state")
	}
	if entry.ThinkingStatus != "Complete" || entry.ThinkingText == "" {
		t.Fatalf("terminal root claim context did not leave terminal thinking state: text=%q status=%q", entry.ThinkingText, entry.ThinkingStatus)
	}
	if _, ok := m.claimCycleAnimations["claim-guide"]; ok {
		t.Fatal("terminal root claim context did not stop claim-cycle animation")
	}

	terminalText := entry.ThinkingText
	m.Update(msg.DecorTickMsg{Time: started.Add(time.Second)})
	entry = m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry after post-terminal tick")
	}
	if entry.ThinkingText != terminalText {
		t.Fatalf("terminal thinking text changed after post-terminal tick: before=%q after=%q", terminalText, entry.ThinkingText)
	}
}

func TestClaimsNative_TerminalThinkingStatusIgnoresLateInProgressContext(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-guide",
		ActionType: "prompt",
		Reason:     "Classifying request",
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		CycleID:      "claim-guide",
		ClaimID:      "claim-guide",
		OwnerAgentID: "guide",
		Context:      "Complete",
		State:        "complete",
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		CycleID:      "claim-guide",
		ClaimID:      "claim-guide",
		OwnerAgentID: "guide",
		Context:      "Request forwarded",
		State:        "routing",
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry")
	}
	if entry.Streaming {
		t.Fatal("late in-progress context restarted streaming state")
	}
	if entry.ThinkingStatus != "Complete" {
		t.Fatalf("late in-progress context overwrote terminal status: %q", entry.ThinkingStatus)
	}
}

func TestClaimsNative_DuplicateActiveStatusDoesNotRestartTerminalCycleEntry(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-guide",
		ActionType: "prompt",
		Reason:     "Classifying request",
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		CycleID:      "claim-guide",
		ClaimID:      "claim-guide",
		OwnerAgentID: "guide",
		Context:      "Complete",
		State:        "complete",
	})
	entry := m.history.Get(0)
	if entry == nil || entry.Streaming {
		t.Fatalf("terminal context did not finalize entry before duplicate active: %+v", entry)
	}

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-guide",
		ActionType: "prompt",
		Reason:     "Classifying request",
	})
	m.Update(msg.DecorTickMsg{Time: time.Now().Add(time.Second)})

	entry = m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry")
	}
	if entry.Streaming {
		t.Fatal("duplicate active status restarted terminal cycle entry")
	}
	if entry.ThinkingStatus != "Complete" {
		t.Fatalf("duplicate active status changed terminal status: %q", entry.ThinkingStatus)
	}
	if _, ok := m.claimCycleAnimations["claim-guide"]; ok {
		t.Fatal("duplicate active status restarted claim-cycle animation")
	}
}

func TestClaimsNative_CompleteStatusInvariantStopsClaimCycleSpinner(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "guide",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-guide",
		ActionType: "prompt",
		Reason:     "Classifying request",
	})
	m.applyClaimContextToHistory("claim-guide", "Complete")
	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry")
	}
	if !entry.Streaming {
		t.Fatal("test setup expected a streaming entry with terminal status")
	}

	m.Update(msg.DecorTickMsg{Time: time.Now().Add(time.Second)})

	entry = m.history.Get(0)
	if entry == nil {
		t.Fatal("missing guide cycle entry after decor tick")
	}
	if entry.Streaming {
		t.Fatal("claim-cycle tick kept spinning after terminal Complete status")
	}
	if entry.ThinkingStatus != "Complete" {
		t.Fatalf("terminal invariant changed status: %q", entry.ThinkingStatus)
	}
	if _, ok := m.claimCycleAnimations["claim-guide"]; ok {
		t.Fatal("claim-cycle tick left animation active after terminal Complete status")
	}
}

func TestClaimsNative_StaleTerminalClaimContextDoesNotFinalizeCycleEntry(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Planning",
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:         "ses-1",
		CycleID:           "claim-architect",
		ClaimID:           "claim-architect",
		OwnerAgentID:      "architect",
		Context:           "Writing answer",
		State:             "synthesizing",
		ContextTransition: 2,
	})
	m.Update(msg.ClaimContextMsg{
		SessionID:         "ses-1",
		CycleID:           "claim-architect",
		ClaimID:           "claim-architect",
		OwnerAgentID:      "architect",
		Context:           "Complete",
		State:             "complete",
		ContextTransition: 1,
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing architect cycle entry")
	}
	if !entry.Streaming {
		t.Fatal("stale terminal context finalized active cycle entry")
	}
	if entry.ThinkingStatus != "Writing answer" {
		t.Fatalf("stale terminal context changed thinking status: %q", entry.ThinkingStatus)
	}
	if _, ok := m.claimCycleAnimations["claim-architect"]; !ok {
		t.Fatal("stale terminal context stopped active claim-cycle animation")
	}
}

func TestClaimsNative_ChildClaimContextTargetsExactParentRow(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Planning",
	})
	addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian-one", "architect", "librarian")
	secondRowID := addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian-two", "architect", "librarian")

	m.Update(msg.ClaimContextMsg{
		SessionID:    "ses-1",
		CycleID:      "claim-architect",
		ClaimID:      "claim-librarian-two",
		ParentRowID:  secondRowID,
		OwnerAgentID: "librarian",
		Context:      "Running workspace_read",
		State:        "tool_executing",
	})

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 2 {
		t.Fatalf("missing duplicated consult rows: %+v", entry)
	}
	firstChildren := entry.ToolCalls[0].InterAgent.Children
	if len(firstChildren) != 0 {
		t.Fatalf("first consult received child context meant for second: %+v", firstChildren)
	}
	secondChildren := entry.ToolCalls[1].InterAgent.Children
	if len(secondChildren) != 1 {
		t.Fatalf("second consult children = %d, want 1", len(secondChildren))
	}
	if secondChildren[0].ThinkingStatus != "Running workspace_read" {
		t.Fatalf("second child ThinkingStatus = %q", secondChildren[0].ThinkingStatus)
	}
	if entry.ThinkingStatus != "Planning" {
		t.Fatalf("child claim context leaked onto root ThinkingStatus = %q", entry.ThinkingStatus)
	}
}

func TestClaimsNative_ChildResponseTextTargetsExactParentRow(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Planning",
	})
	addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian-one", "architect", "librarian")
	secondRowID := addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian-two", "architect", "librarian")

	m.Update(msg.ClaimResponseTextMsg{
		SessionID:   "ses-1",
		CycleID:     "claim-architect",
		ClaimID:     "claim-librarian-two",
		ParentRowID: secondRowID,
		AgentID:     "librarian",
		Content:     "Second consult response.",
		CreatedAt:   time.Now(),
	})

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 2 {
		t.Fatalf("missing duplicated consult rows: %+v", entry)
	}
	if len(entry.ToolCalls[0].InterAgent.Children) != 0 {
		t.Fatalf("first consult received response meant for second: %+v", entry.ToolCalls[0].InterAgent.Children)
	}
	children := entry.ToolCalls[1].InterAgent.Children
	if len(children) != 1 {
		t.Fatalf("second consult children = %d, want 1", len(children))
	}
	if !children[0].Completed || children[0].ResultSummary != "Second consult response." {
		t.Fatalf("second child response not completed correctly: %+v", children[0])
	}
	if entry.Content != "" {
		t.Fatalf("child response_text leaked onto cycle answer = %q", entry.Content)
	}
}

func TestClaimsNative_TestamentContextUpdatesNestedChildStatus(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "claim-architect",
		ActionType: "prompt",
		Reason:     "Planning",
	})
	parentRowID := addPeerInteractionForClaimsTest(m, "claim-architect", "claim-librarian", "architect", "librarian")
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "librarian-tool",
		CycleID:        "claim-architect",
		ParentRowID:    parentRowID,
		ClaimID:        "claim-librarian",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "workspace_read",
		CreatedAt:      time.Now(),
	})
	m.Update(msg.TestamentContextMsg{
		SessionID:         "ses-1",
		AccumulatorID:     "acc-librarian",
		ClaimID:           "claim-librarian",
		AgentID:           "librarian",
		CycleID:           "claim-architect",
		ParentRowID:       parentRowID,
		Context:           "Building repository inventory",
		ContextTransition: 1,
	})

	entry := m.history.Get(0)
	if entry == nil || len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("missing consult row after testament context: %+v", entry)
	}
	children := entry.ToolCalls[0].InterAgent.Children
	if len(children) != 1 {
		t.Fatalf("consult children = %d, want 1", len(children))
	}
	if children[0].ThinkingStatus != "Building repository inventory" {
		t.Fatalf("child ThinkingStatus = %q", children[0].ThinkingStatus)
	}
	if children[0].ThinkingStartedAt.IsZero() {
		t.Fatal("child testament context should start nested thinking animation")
	}
}

func TestClaimsNative_InterruptTargetPrefersRouteCorrelationForClaimCycle(t *testing.T) {
	m := newChatForClaimsTest(t)

	m.Update(msg.StreamStartMsg{
		SessionID:     "ses-1",
		CorrelationID: "route-architect",
		AgentID:       "architect",
		AgentType:     "architect",
	})
	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:             "architect",
		SessionID:           "ses-1",
		Active:              true,
		CycleID:             "cycle-architect",
		ActionType:          "prompt",
		Reason:              "Building a plan",
		StreamCorrelationID: "route-architect",
	})
	parentRowID := addPeerInteractionForClaimsTest(m, "cycle-architect", "claim-librarian", "architect", "librarian")
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "workspace-read",
		ParentRowID:    parentRowID,
		CycleID:        "cycle-architect",
		ClaimID:        "claim-librarian",
		OwnerAgentID:   "librarian",
		OwnerAgentType: "librarian",
		AgentID:        "librarian",
		Kind:           "tool_started",
		Reference:      "workspace_read",
		CreatedAt:      time.Now(),
	})

	target, ok := m.LatestActiveInterruptTarget()
	if !ok {
		t.Fatal("LatestActiveInterruptTarget returned no target")
	}
	if target.CorrelationID != "route-architect" {
		t.Fatalf("interrupt correlation = %q, want route-architect", target.CorrelationID)
	}
	if !m.MarkInterrupted("route-architect") {
		t.Fatal("MarkInterrupted(route-architect) returned false")
	}
	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("missing chat entry")
	}
	if entry.Streaming {
		t.Fatal("entry still streaming after MarkInterrupted")
	}
	if entry.ThinkingStatus != "Interrupted" {
		t.Fatalf("ThinkingStatus = %q, want Interrupted", entry.ThinkingStatus)
	}
	if len(entry.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(entry.ToolCalls))
	}
	consult := entry.ToolCalls[0]
	if !consult.Completed || consult.Success {
		t.Fatalf("consult completion = completed:%v success:%v, want interrupted failure", consult.Completed, consult.Success)
	}
	if consult.InterAgent == nil || consult.InterAgent.Status != InterAgentToolFailed {
		t.Fatalf("consult InterAgent status = %+v, want failed", consult.InterAgent)
	}
	if len(consult.InterAgent.Children) != 1 {
		t.Fatalf("consult children len = %d, want 1", len(consult.InterAgent.Children))
	}
	child := consult.InterAgent.Children[0]
	if !child.Failed || child.ThinkingText != "" || child.ThinkingStatus != "" {
		t.Fatalf("child after interrupt = %+v, want failed with cleared thinking", child)
	}
	if len(child.ToolCalls) != 1 || !child.ToolCalls[0].Completed || child.ToolCalls[0].Success {
		t.Fatalf("nested child tool after interrupt = %+v, want completed failure", child.ToolCalls)
	}
	if len(m.claimCycleAnimations) != 0 {
		t.Fatalf("claimCycleAnimations len = %d, want 0", len(m.claimCycleAnimations))
	}
}

// guardChainOK keeps the import set tight: the test file must compile
// against core/claims even when an individual test doesn't reference
// it directly, so the build catches drift early.
var _ = claims.ClaimStatusAccepted
