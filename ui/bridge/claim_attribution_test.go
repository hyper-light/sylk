package bridge

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/ui/msg"
)

// TestBridgeIntegration_PromptCycleOwnerIsSubjectNotIssuer pins the
// regression: a guide-routed user-prompt claim opens a cycle owned by
// the *subject* agent (architect), per cycleOwnerFor semantics. The
// bridge must therefore stamp owner=architect on every artifact
// metadata path (claimMeta, ClaimArtifactAddedMsg, downstream rows).
// The previous bug cached claimMeta with owner=issuer=guide, so chat
// rendered "guide" as the agent header for the architect's tool calls.
func TestBridgeIntegration_PromptCycleOwnerIsSubjectNotIssuer(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-prompt-attribution")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt},
		[]claims.Claim{{
			Title:      "Build a python CLI",
			ActionType: claims.ActionTypePrompt,
			Relations: []claims.Relation{
				{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
		}},
	); err != nil {
		t.Fatalf("PostAction prompt claim: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	drainBridge(t, prog, "prompt cycle open")

	var openMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		if s, ok := m.(msg.ClaimsAgentStatusMsg); ok && s.Active {
			openMsg = &s
			break
		}
	}
	if openMsg == nil {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true}")
	}
	if openMsg.AgentID != "architect" {
		t.Fatalf("ClaimsAgentStatusMsg.AgentID = %q, want architect", openMsg.AgentID)
	}

	// Now an artifact emitted on the architect's cycle. Its
	// OwnerAgentID on the outgoing ClaimArtifactAddedMsg MUST be
	// architect — not guide (the issuer).
	br.OnArtifactAdded(claimID, "architect", "ses-prompt-attribution", &claims.Artifact{
		ID:        "art-tool-1",
		AgentID:   "architect",
		Kind:      "tool_started",
		Reference: "read_file",
	})
	drainBridge(t, prog, "first tool call")

	var added *msg.ClaimArtifactAddedMsg
	for _, m := range prog.Snapshot() {
		if a, ok := m.(msg.ClaimArtifactAddedMsg); ok && a.ArtifactID == "art-tool-1" {
			added = &a
			break
		}
	}
	if added == nil {
		t.Fatal("expected ClaimArtifactAddedMsg for art-tool-1")
	}
	if added.OwnerAgentID != "architect" {
		t.Fatalf("ClaimArtifactAddedMsg.OwnerAgentID = %q, want architect (cycle owner, not issuer)", added.OwnerAgentID)
	}
	if added.OwnerAgentType != "architect" {
		t.Fatalf("ClaimArtifactAddedMsg.OwnerAgentType = %q, want architect", added.OwnerAgentType)
	}
	// The recording-agent field carries the artifact's actual
	// emitter; here it should be the architect too.
	if added.AgentID != "architect" {
		t.Fatalf("ClaimArtifactAddedMsg.AgentID = %q, want architect", added.AgentID)
	}
}

// TestBridgeIntegration_HandoffCycleOwnerIsSubject mirrors the prompt
// case for ActionTypeHandoff: handoff transfers cycle ownership to
// the successor (subject), so claimMeta + emitted artifacts must
// attribute to the successor agent, not the handing-off issuer.
func TestBridgeIntegration_HandoffCycleOwnerIsSubject(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-handoff-attribution")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeHandoff},
		[]claims.Claim{{
			Title:      "Hand off to engineer",
			ActionType: claims.ActionTypeHandoff,
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				{Related: "prev-claim", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipHandoffFrom},
			},
		}},
	); err != nil {
		t.Fatalf("PostAction handoff claim: %v", err)
	}
	drainBridge(t, prog, "handoff cycle open")

	var openMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		if s, ok := m.(msg.ClaimsAgentStatusMsg); ok && s.Active && s.AgentID == "engineer" {
			openMsg = &s
			break
		}
	}
	if openMsg == nil {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true, AgentID=engineer}")
	}
}

// TestBridgeIntegration_ConsultedAgentArtifactsRenderAsResponder
// verifies that when the architect consults the librarian, the
// librarian's tool_started artifacts come through with AgentID=
// librarian (the actual emitter), not architect (the cycle owner).
// The chat panel must distinguish "who owns the top-level cycle"
// (architect) from "who emitted this specific tool call"
// (librarian) — otherwise nested librarian rows render as architect's.
func TestBridgeIntegration_ConsultedAgentArtifactsRenderAsResponder(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-consult-attribution")
	defer cleanup()

	// Architect's own cycle.
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt},
		[]claims.Claim{{
			Title:      "User prompt",
			ActionType: claims.ActionTypePrompt,
			Relations: []claims.Relation{
				{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
		}},
	); err != nil {
		t.Fatalf("PostAction prompt: %v", err)
	}
	architectClaimID := board.Projection().Claims[0].ID

	// Architect posts a consult claim; cycle resolver sees it as a
	// child of the prompt cycle.
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeConsultation},
		[]claims.Claim{{
			Title:      "Consult librarian",
			ActionType: claims.ActionTypeConsultation,
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				{Related: architectClaimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy},
			},
		}},
	); err != nil {
		t.Fatalf("PostAction consult: %v", err)
	}
	consultClaimID := board.Projection().Claims[1].ID
	drainBridge(t, prog, "consult registered")

	// Librarian later emits a tool call on ITS claim. Bridge should
	// nest it under the claim-backed peer row AND label the row with
	// AgentID=librarian (the emitter), not architect.
	br.OnArtifactAdded(consultClaimID, "librarian", "ses-consult-attribution", &claims.Artifact{
		ID:        "art-libtool",
		AgentID:   "librarian",
		Kind:      "tool_started",
		Reference: "search_kb",
	})
	drainBridge(t, prog, "librarian tool")

	var libRow *msg.ClaimArtifactAddedMsg
	for _, m := range prog.Snapshot() {
		if a, ok := m.(msg.ClaimArtifactAddedMsg); ok && a.ArtifactID == "art-libtool" {
			libRow = &a
			break
		}
	}
	if libRow == nil {
		t.Fatal("expected ClaimArtifactAddedMsg for librarian tool")
	}
	if libRow.AgentID != "librarian" {
		t.Fatalf("librarian tool row AgentID = %q, want librarian (the emitter)", libRow.AgentID)
	}
	wantParent := "claim-peer:" + consultClaimID
	if libRow.ParentRowID != wantParent {
		t.Fatalf("ParentRowID = %q, want %q", libRow.ParentRowID, wantParent)
	}
}
