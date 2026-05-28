package guide

import (
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

// These tests pin the Guide's routed-work claim minting semantics. The
// regression they guard against: buildForwardedRequest used to mint
// an ActionTypePrompt claim for EVERY routed request, including
// agent-originated consults, challenges, direct protocol exchanges,
// archivalist briefs, fire-and-forget sub-requests, and control-
// plane coordination. That produced phantom top-level user-prompt
// cycles owned by the target agent (so e.g. the librarian became a
// top-level chat row instead of nesting under the architect's
// consult_started row), and overwrote the real parent_claim_id —
// severing the responder's testament from the originating
// consultation/challenge claim and breaking the bridge's nested
// row attribution.
//
// The contract: only TUI-originated user prompts mint a new routed-work
// claim. Every other route preserves whatever parent_claim_id the
// dispatching skill stamped (or remains unstamped if the dispatcher
// deliberately chose to).

func TestShouldPostRoutedWorkClaim_TUIPromptMints(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "build a python CLI",
	}
	if !shouldPostRoutedWorkClaim(req, nil) {
		t.Fatal("TUI-originated prompt should mint a routed work claim")
	}
}

func TestShouldPostRoutedWorkClaim_AgentRouteDoesNotMint(t *testing.T) {
	cases := []string{"architect", "librarian", "engineer", "guide", "guardian", "tester"}
	for _, agent := range cases {
		t.Run(agent, func(t *testing.T) {
			req := &RouteRequest{
				SourceAgentID: agent,
				Input:         "agent-internal request",
			}
			if shouldPostRoutedWorkClaim(req, nil) {
				t.Fatalf("%s-originated route minted a routed work claim — only TUI prompts should", agent)
			}
		})
	}
}

func TestShouldPostRoutedWorkClaim_PreservesExistingParentClaimID(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "follow-up under existing cycle",
	}
	metadata := map[string]any{"parent_claim_id": "claim-existing-1"}
	if shouldPostRoutedWorkClaim(req, metadata) {
		t.Fatal("metadata already carries parent_claim_id — Guide must not overwrite it with a fresh prompt claim")
	}
}

func TestShouldPostRoutedWorkClaim_FireAndForgetDoesNotMint(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "async sub-request",
		FireAndForget: true,
	}
	if shouldPostRoutedWorkClaim(req, nil) {
		t.Fatal("fire-and-forget routes are async work, not user intent — must not mint a visible prompt cycle")
	}
}

func TestShouldPostRoutedWorkClaim_ControlPlaneDoesNotMint(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "plan-handoff coordination",
	}
	metadata := map[string]any{"control_plane_kind": "plan_handoff"}
	if shouldPostRoutedWorkClaim(req, metadata) {
		t.Fatal("control-plane coordination must remain invisible — never opens a chat row")
	}
}

func TestShouldPostRoutedWorkClaim_EmptyParentClaimIDFalls(t *testing.T) {
	// Whitespace-only / non-string parent_claim_id values are
	// treated as "unset" so trivially-malformed metadata still
	// allows a valid TUI prompt to mint its claim.
	cases := []map[string]any{
		{"parent_claim_id": ""},
		{"parent_claim_id": "   "},
		{"parent_claim_id": 42},
		{"parent_claim_id": nil},
	}
	for i, metadata := range cases {
		req := &RouteRequest{SourceAgentID: "tui", Input: "user prompt"}
		if !shouldPostRoutedWorkClaim(req, metadata) {
			t.Fatalf("case %d: shouldPostRoutedWorkClaim = false; whitespace/non-string parent_claim_id should be treated as unset (metadata=%+v)", i, metadata)
		}
	}
}

func TestShouldPostRoutedWorkClaim_NilRequestSafe(t *testing.T) {
	if shouldPostRoutedWorkClaim(nil, nil) {
		t.Fatal("nil request must not panic and must not mint a claim")
	}
}

func TestPostRoutedWorkClaimUsesLifecycleAndDoesNotUsePromptSuppressionTag(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	req := &RouteRequest{
		SourceAgentID: "tui",
		SessionID:     "sess",
		CorrelationID: "corr-1",
		Input:         "build a cli",
	}
	result := &RouteResult{TargetAgent: TargetAgent("architect"), Intent: IntentExecute, Confidence: 0.91}

	claimID, err := postRoutedWorkClaim(board, req, result)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("routed work claim %q not posted", claimID)
	}
	if claim.ActionType != claims.ActionTypeTask {
		t.Fatalf("claim action type = %s, want task", claim.ActionType)
	}
	if claim.LifecycleStatus != claims.ClaimLifecyclePosted {
		t.Fatalf("claim lifecycle = %s, want posted", claim.LifecycleStatus)
	}
	if got := claims.SubjectAgentID(claim.Relations); got != "architect" {
		t.Fatalf("subject = %q, want architect", got)
	}
	for _, tag := range claim.Tags {
		if tag == "user_prompt" {
			t.Fatal("routed work claims must not carry user_prompt; target inbox would suppress them")
		}
	}
}

func TestShouldSuppressForwardedExecutionOnlyForClaimNativeTUIRoutes(t *testing.T) {
	forwarded := &ForwardedRequest{
		SourceAgentID: sourceAgentTUI,
		Metadata: map[string]any{
			"claim_native_routing": true,
			"routed_work_claim_id": "claim-1",
		},
	}
	if !shouldSuppressForwardedExecution(forwarded) {
		t.Fatal("claim-native TUI route should suppress forwarded execution")
	}
	forwarded.SourceAgentID = "architect"
	if shouldSuppressForwardedExecution(forwarded) {
		t.Fatal("agent-originated routes must not suppress forwarded execution")
	}
}

func TestShouldOpenGuideClassificationClaim_TUITopLevelOnly(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "build a python CLI",
	}
	if !shouldOpenGuideClassificationClaim(req) {
		t.Fatal("top-level TUI prompt should open a Guide classification claim while classification is in flight")
	}
}

func TestShouldOpenGuideClassificationClaim_DirectAndNestedRoutesDoNotOpen(t *testing.T) {
	cases := []struct {
		name string
		req  *RouteRequest
	}{
		{
			name: "agent_originated",
			req:  &RouteRequest{SourceAgentID: "architect", Input: "consult librarian"},
		},
		{
			name: "existing_parent_claim",
			req: &RouteRequest{
				SourceAgentID: "tui",
				Input:         "nested follow-up",
				Metadata:      map[string]any{"parent_claim_id": "claim-parent"},
			},
		},
		{
			name: "control_plane",
			req: &RouteRequest{
				SourceAgentID: "tui",
				Input:         "protocol route",
				Metadata:      map[string]any{"control_plane_kind": "protocol"},
			},
		},
		{
			name: "explicit_target",
			req: &RouteRequest{
				SourceAgentID:  "tui",
				Input:          "@architect plan",
				TargetAgentID:  "architect",
				ExplicitTarget: true,
			},
		},
		{
			name: "fire_and_forget",
			req: &RouteRequest{
				SourceAgentID: "tui",
				Input:         "background",
				FireAndForget: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if shouldOpenGuideClassificationClaim(tc.req) {
				t.Fatal("nested/direct/protocol route opened a Guide classification claim")
			}
		})
	}
}

func TestMetadataHasNonEmptyString_Cases(t *testing.T) {
	cases := []struct {
		name string
		md   map[string]any
		key  string
		want bool
	}{
		{"nil_map", nil, "k", false},
		{"missing_key", map[string]any{"x": "y"}, "k", false},
		{"empty_string", map[string]any{"k": ""}, "k", false},
		{"whitespace", map[string]any{"k": "   \t"}, "k", false},
		{"int_value", map[string]any{"k": 7}, "k", false},
		{"nil_value", map[string]any{"k": nil}, "k", false},
		{"valid", map[string]any{"k": "value"}, "k", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadataHasNonEmptyString(tc.md, tc.key); got != tc.want {
				t.Errorf("metadataHasNonEmptyString(%+v, %q) = %v, want %v", tc.md, tc.key, got, tc.want)
			}
		})
	}
}
