package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// These tests pin the four visibility/unification regressions the user
// flagged: llm_started leaking into the chat tree, system-internal
// cycles opening rows, the prompt cycle row being split between
// streaming and claims paths, and consult/challenge/guardian-check
// children rendering flat instead of nested.
//
// Per the user's directive ("Add failing tests before changing
// behavior"), these tests describe the contract that the bridge +
// chat panel must satisfy together. They fail today and turn green as
// the four targeted fixes land.

func newChatForVisibilityTest(t *testing.T) *Model {
	t.Helper()
	return New(theme.DefaultDark(), 256)
}

// Test 1: llm_started artifacts must NEVER produce a visible chat
// row. Provider instrumentation emits llm_started/llm_completed as
// telemetry only (model name, dispatch_id, token counts). The chat
// panel currently treats every non-agent_state artifact as a
// ToolCallRecord, so the model name (e.g. "claude-opus-4-6") shows
// up as a fake tool row. The bridge — and the chat as defense in
// depth — must drop llm_* visibility outright.
func TestVisibility_LLMStartedDoesNotRenderRow(t *testing.T) {
	m := newChatForVisibilityTest(t)

	// Cycle is open under the architect, prompt-classified.
	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch-1",
		ActionType: "prompt",
	})

	llm := msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-llm-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           "llm_started",
		Reference:      "claude-opus-4-6",
		Metadata: map[string]any{
			"dispatch_id": "disp-1",
			"provider":    "anthropic",
			"model":       "claude-opus-4-6",
		},
		CreatedAt: time.Now(),
	}
	m.Update(llm)

	if got := m.ArtifactRowByID("art-llm-1"); got != nil {
		t.Fatalf("llm_started created an ArtifactRow: %+v — telemetry must never become a tool row", got)
	}

	// And nothing on the cycle's ChatEntry either: no ToolCallRecord
	// representing the llm dispatch should be appended.
	cycleEntry := findChatEntryByCorrelation(m, "cycle-arch-1")
	if cycleEntry == nil {
		t.Fatal("cycle entry missing")
	}
	for _, tc := range cycleEntry.ToolCalls {
		if strings.EqualFold(tc.ToolName, "claude-opus-4-6") || strings.HasPrefix(tc.ToolCallKey, "art-llm-") {
			t.Fatalf("cycle entry still carries an llm dispatch as a ToolCallRecord: %+v", tc)
		}
	}

	// Same expectation for llm_completed.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:   "art-llmend-1",
		CycleID:      "cycle-arch-1",
		ClaimID:      "claim-arch-1",
		OwnerAgentID: "architect",
		AgentID:      "architect",
		Kind:         "llm_completed",
		Reference:    "claude-opus-4-6",
		CreatedAt:    time.Now(),
	})
	if got := m.ArtifactRowByID("art-llmend-1"); got != nil {
		t.Fatalf("llm_completed created an ArtifactRow: %+v — telemetry must never become a tool row", got)
	}
}

// Test 2: system-internal action types (claims.IsSystemInternalAction)
// must NOT open a chat row. activation/boot/shutdown/archival/
// testament/checkpoint/consult_continuation are housekeeping cycles —
// they belong on the claims/audit plane only. The current handler
// pushes a ChatEntry for every Active=true status regardless of
// ActionType, which is what produces the repeated "System: Activation"
// rows the user is seeing.
func TestVisibility_SystemInternalCyclesDoNotOpenChatRow(t *testing.T) {
	systemInternalActionTypes := []string{
		string(claims.ActionTypeActivation),
		string(claims.ActionTypeBoot),
		string(claims.ActionTypeShutdown),
		string(claims.ActionTypeArchival),
		string(claims.ActionTypeTestament),
		string(claims.ActionTypeCheckpoint),
		string(claims.ActionTypeConsultContinuation),
	}

	for _, action := range systemInternalActionTypes {
		t.Run(action, func(t *testing.T) {
			m := newChatForVisibilityTest(t)

			cycleID := "cycle-" + action
			m.Update(msg.ClaimsAgentStatusMsg{
				AgentID:    "system",
				SessionID:  "ses-1",
				Active:     true,
				CycleID:    cycleID,
				ActionType: action,
				Reason:     action,
			})

			if row := m.ClaimRowByCycleID(cycleID); row != nil {
				t.Fatalf("system-internal %q opened a ClaimRow: %+v", action, row)
			}
			if entry := findChatEntryByCorrelation(m, cycleID); entry != nil {
				t.Fatalf("system-internal %q pushed a ChatEntry: id=%s agent=%s", action, entry.ID, entry.AgentID)
			}
		})
	}
}

// Test 3: there is exactly ONE chat entry per prompt cycle, even
// when the legacy stream layer has already started a row under a
// different correlation ID. The user's split-row screenshot is
// caused by stream events keyed by route correlation X creating a
// "thinking" row, while the cycle opens under cycle ID Y and creates
// a separate "artifact" row. Once StreamCorrelationID is propagated
// through ClaimsAgentStatusMsg the chat must absorb the route
// correlation onto the cycle's row so subsequent stream chunks /
// completion land on it instead of producing a sibling.
func TestVisibility_OnePromptCycleEntryEvenWithStream(t *testing.T) {
	m := newChatForVisibilityTest(t)

	// Stream starts first, under route correlation "corr-X" — what
	// the legacy stream bridge produces today.
	m.Update(msg.StreamStartMsg{
		SessionID:     "ses-1",
		CorrelationID: "corr-X",
		AgentID:       "architect",
	})

	// Cycle opens with a propagated StreamCorrelationID linking
	// route correlation "corr-X" to cycle ID "cycle-Y".
	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:             "architect",
		SessionID:           "ses-1",
		Active:              true,
		CycleID:             "cycle-Y",
		ActionType:          "prompt",
		StreamCorrelationID: "corr-X",
	})

	// Subsequent stream chunk on the route correlation must land on
	// the SAME entry the cycle owns — not push a fresh one.
	m.Update(msg.StreamChunkMsg{
		SessionID:     "ses-1",
		CorrelationID: "corr-X",
		Text:          "thinking…",
	})

	if got := countAgentChatEntries(m); got != 1 {
		t.Fatalf("expected exactly 1 agent chat entry for the cycle, got %d (stream + cycle rows split)", got)
	}

	// Both IDs must resolve to the same entry.
	byCycle := findChatEntryByCorrelation(m, "cycle-Y")
	byStream := findChatEntryByCorrelation(m, "corr-X")
	if byCycle == nil || byStream == nil {
		t.Fatalf("entries missing: byCycle=%v byStream=%v", byCycle, byStream)
	}
	if byCycle.ID != byStream.ID {
		t.Fatalf("cycle and stream resolved to different entries: cycle=%s stream=%s", byCycle.ID, byStream.ID)
	}
}

// Test 4: the response_text artifact updates the cycle entry's
// Content rather than rendering as a tool row. Today response_text
// is squashed through projectClaimArtifactToHistory like every other
// non-agent_state artifact — it shows up as a "tool" row whose
// "ToolName" is the first 256 characters of the response, with no
// effect on the entry's actual rendered text. The user's "no final
// text" symptom comes from this: the row that produced the answer
// stays in a thinking state because nothing wrote the text to it.
func TestVisibility_ResponseTextLandsOnCycleEntryContent(t *testing.T) {
	m := newChatForVisibilityTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch-1",
		ActionType: "prompt",
	})

	final := "The answer is 42. Here is the reasoning: …"
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-resp-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		Kind:           claims.ArtifactKindResponseText,
		Reference:      final,
		CreatedAt:      time.Now(),
	})

	// Must NOT be an artifact row.
	if got := m.ArtifactRowByID("art-resp-1"); got != nil {
		t.Fatalf("response_text created an ArtifactRow: %+v — should write Content, not push a tool row", got)
	}

	entry := findChatEntryByCorrelation(m, "cycle-arch-1")
	if entry == nil {
		t.Fatal("cycle entry missing after response_text")
	}
	if !strings.Contains(entry.Content, "The answer is 42") {
		t.Fatalf("cycle entry Content does not carry the response text: %q", entry.Content)
	}
	for _, tc := range entry.ToolCalls {
		if tc.ToolCallKey == "art-resp-1" {
			t.Fatalf("response_text was projected as a ToolCallRecord: %+v", tc)
		}
	}
}

// Test 5: a consult_started artifact renders as a ToolCallRecord
// with InterAgent metadata, and a child tool_started artifact whose
// ParentRowID points at the consult must nest under
// InterAgent.Children — not append flat as a sibling row. This is
// the regression that flattens the librarian's tool tree under an
// architect → librarian consult into a sequence of unrelated rows
// under the architect's cycle.
//
// The pending status must propagate so the renderer's spinner
// continues to animate while the child tool is in-flight.
func TestVisibility_ConsultRendersInterAgentWithNestedChild(t *testing.T) {
	m := newChatForVisibilityTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch-1",
		ActionType: "prompt",
	})

	// Architect consults librarian.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-consult-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		TargetAgentID:  "librarian",
		Kind:           "consult_started",
		Reference:      "librarian",
		Metadata:       map[string]any{"claim_id": "claim-libr-1"},
		CreatedAt:      time.Now(),
	})

	// Librarian's tool_started under the consult.
	m.Update(msg.ClaimArtifactAddedMsg{
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
	})

	entry := findChatEntryByCorrelation(m, "cycle-arch-1")
	if entry == nil {
		t.Fatal("cycle entry missing")
	}

	var consultRecord *ToolCallRecord
	for i := range entry.ToolCalls {
		if entry.ToolCalls[i].ToolCallKey == "art-consult-1" {
			consultRecord = &entry.ToolCalls[i]
			break
		}
	}
	if consultRecord == nil {
		t.Fatalf("consult_started missing on cycle entry; tool calls: %+v", entry.ToolCalls)
	}
	if consultRecord.InterAgent == nil {
		t.Fatalf("consult_started rendered as a generic tool row (InterAgent is nil) — must use InterAgentTool")
	}
	if consultRecord.InterAgent.Kind != InterAgentToolConsult {
		t.Errorf("InterAgent.Kind = %q, want consult", consultRecord.InterAgent.Kind)
	}
	if !contains(consultRecord.InterAgent.AgentTypes, "librarian") {
		t.Errorf("InterAgent.AgentTypes = %v, want includes librarian", consultRecord.InterAgent.AgentTypes)
	}
	if consultRecord.InterAgent.Status != InterAgentToolPending {
		t.Errorf("InterAgent.Status = %q, want pending while child is in-flight", consultRecord.InterAgent.Status)
	}

	// The child librarian tool must nest under the consult's
	// InterAgent.Children — not appear as a flat sibling row on the
	// cycle entry.
	if len(consultRecord.InterAgent.Children) != 1 {
		t.Fatalf("expected exactly one nested child under consult; got %d (children=%+v)", len(consultRecord.InterAgent.Children), consultRecord.InterAgent.Children)
	}
	child := consultRecord.InterAgent.Children[0]
	if child.AgentType != "librarian" {
		t.Errorf("nested child.AgentType = %q, want librarian", child.AgentType)
	}
	foundChildTool := false
	for _, tc := range child.ToolCalls {
		if tc.ToolCallKey == "art-libtool-1" {
			foundChildTool = true
			if tc.Completed {
				t.Errorf("child tool prematurely Completed before completion arrived")
			}
		}
	}
	if !foundChildTool {
		t.Fatalf("nested librarian tool missing on InterAgent.Children[0].ToolCalls: %+v", child.ToolCalls)
	}

	// And critically: it must NOT also be a flat sibling on the
	// cycle entry.
	for _, tc := range entry.ToolCalls {
		if tc.ToolCallKey == "art-libtool-1" {
			t.Fatalf("librarian tool was also appended FLAT under the cycle entry — duplicate")
		}
	}
}

// Test 6: a challenge_started artifact renders as a ToolCallRecord
// with InterAgent{Kind: Challenge}. Sibling test to the consult
// variant — same nesting + pending-animation contract, different
// inter-agent kind.
func TestVisibility_ChallengeRendersInterAgentWithNestedChild(t *testing.T) {
	m := newChatForVisibilityTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch-1",
		ActionType: "prompt",
	})

	// Architect challenges tester.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-chall-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		TargetAgentID:  "tester",
		Kind:           "challenge_started",
		Reference:      "tester",
		Metadata:       map[string]any{"claim_id": "claim-tester-1"},
		CreatedAt:      time.Now(),
	})

	// Tester runs a tool inside the challenge.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-testtool-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    "art-chall-1",
		ClaimID:        "claim-tester-1",
		OwnerAgentID:   "tester",
		OwnerAgentType: "tester",
		AgentID:        "tester",
		Kind:           "tool_started",
		Reference:      "run_tests",
		CreatedAt:      time.Now(),
	})

	entry := findChatEntryByCorrelation(m, "cycle-arch-1")
	if entry == nil {
		t.Fatal("cycle entry missing")
	}
	rec := findToolCallByKey(entry, "art-chall-1")
	if rec == nil || rec.InterAgent == nil {
		t.Fatalf("challenge_started missing or rendered without InterAgent: %+v", rec)
	}
	if rec.InterAgent.Kind != InterAgentToolChallenge {
		t.Errorf("InterAgent.Kind = %q, want challenge", rec.InterAgent.Kind)
	}
	if rec.InterAgent.Status != InterAgentToolPending {
		t.Errorf("InterAgent.Status = %q, want pending while child is in-flight", rec.InterAgent.Status)
	}
	if len(rec.InterAgent.Children) != 1 || len(rec.InterAgent.Children[0].ToolCalls) != 1 {
		t.Fatalf("expected one nested child with one tool; got children=%+v", rec.InterAgent.Children)
	}
	if rec.InterAgent.Children[0].ToolCalls[0].ToolCallKey != "art-testtool-1" {
		t.Errorf("nested tool key = %q, want art-testtool-1", rec.InterAgent.Children[0].ToolCalls[0].ToolCallKey)
	}
}

// Test 7: a guardian_check_started artifact renders as a
// ToolCallRecord with InterAgent{Kind: Approval}. Same nesting +
// pending-animation contract; the guardian's own tool calls
// (e.g. command_grant evaluation) nest under it.
func TestVisibility_GuardianCheckRendersInterAgentWithNestedChild(t *testing.T) {
	m := newChatForVisibilityTest(t)

	m.Update(msg.ClaimsAgentStatusMsg{
		AgentID:    "architect",
		SessionID:  "ses-1",
		Active:     true,
		CycleID:    "cycle-arch-1",
		ActionType: "prompt",
	})

	// Architect requests guardian approval for a gated tool.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-gc-1",
		CycleID:        "cycle-arch-1",
		ClaimID:        "claim-arch-1",
		OwnerAgentID:   "architect",
		OwnerAgentType: "architect",
		AgentID:        "architect",
		TargetAgentID:  "guardian",
		Kind:           "guardian_check_started",
		Reference:      "guardian",
		Metadata:       map[string]any{"claim_id": "claim-guard-1"},
		CreatedAt:      time.Now(),
	})

	// Guardian evaluates with its own tool call.
	m.Update(msg.ClaimArtifactAddedMsg{
		ArtifactID:     "art-grant-1",
		CycleID:        "cycle-arch-1",
		ParentRowID:    "art-gc-1",
		ClaimID:        "claim-guard-1",
		OwnerAgentID:   "guardian",
		OwnerAgentType: "guardian",
		AgentID:        "guardian",
		Kind:           "tool_started",
		Reference:      "evaluate_grant",
		CreatedAt:      time.Now(),
	})

	entry := findChatEntryByCorrelation(m, "cycle-arch-1")
	if entry == nil {
		t.Fatal("cycle entry missing")
	}
	rec := findToolCallByKey(entry, "art-gc-1")
	if rec == nil || rec.InterAgent == nil {
		t.Fatalf("guardian_check_started missing or rendered without InterAgent: %+v", rec)
	}
	if rec.InterAgent.Kind != InterAgentToolApproval {
		t.Errorf("InterAgent.Kind = %q, want approval", rec.InterAgent.Kind)
	}
	if rec.InterAgent.Status != InterAgentToolPending {
		t.Errorf("InterAgent.Status = %q, want pending while guardian is evaluating", rec.InterAgent.Status)
	}
	if len(rec.InterAgent.Children) != 1 || len(rec.InterAgent.Children[0].ToolCalls) != 1 {
		t.Fatalf("expected one nested guardian child with one tool; got children=%+v", rec.InterAgent.Children)
	}
	if rec.InterAgent.Children[0].AgentType != "guardian" {
		t.Errorf("nested child AgentType = %q, want guardian", rec.InterAgent.Children[0].AgentType)
	}
	if rec.InterAgent.Children[0].ToolCalls[0].ToolCallKey != "art-grant-1" {
		t.Errorf("nested guardian tool key = %q, want art-grant-1", rec.InterAgent.Children[0].ToolCalls[0].ToolCallKey)
	}
}

// findToolCallByKey returns a pointer to the matching ToolCallRecord
// on the entry's top-level ToolCalls. Returns nil if no record
// matches; the helper makes test assertions read top-down.
func findToolCallByKey(entry *ChatEntry, key string) *ToolCallRecord {
	if entry == nil {
		return nil
	}
	for i := range entry.ToolCalls {
		if entry.ToolCalls[i].ToolCallKey == key {
			rec := entry.ToolCalls[i]
			return &rec
		}
	}
	return nil
}

// findChatEntryByCorrelation walks the chat history and returns the
// first entry whose CorrelationID OR AdditionalCorrelationIDs
// matches the given key. Returns nil if no entry matches. Used in
// place of poking at the unexported historyIndexForCorrelation so
// the test reads top-down off the same surface a renderer would
// see.
func findChatEntryByCorrelation(m *Model, key string) *ChatEntry {
	if m == nil || m.history == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	var found *ChatEntry
	m.history.Range(0, m.history.Len(), func(_ int, entry *ChatEntry) bool {
		if entry == nil {
			return true
		}
		if entry.CorrelationID == key {
			copy := *entry
			found = &copy
			return false
		}
		for _, alt := range entry.AdditionalCorrelationIDs {
			if alt == key {
				copy := *entry
				found = &copy
				return false
			}
		}
		return true
	})
	return found
}

// countAgentChatEntries returns the number of SourceAgent entries in
// the chat history. User input and system entries are excluded —
// only agent-owned rows are counted, since those are the ones the
// cycle/stream split duplicates.
func countAgentChatEntries(m *Model) int {
	if m == nil || m.history == nil {
		return 0
	}
	count := 0
	m.history.Range(0, m.history.Len(), func(_ int, entry *ChatEntry) bool {
		if entry != nil && entry.Source == SourceAgent {
			count++
		}
		return true
	})
	return count
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
