package guide

import "testing"

// These tests pin the Guide's prompt-claim minting semantics. The
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
// The contract: only TUI-originated user prompts mint a new prompt
// claim. Every other route preserves whatever parent_claim_id the
// dispatching skill stamped (or remains unstamped if the dispatcher
// deliberately chose to).

func TestShouldPostPromptClaim_TUIPromptMints(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "build a python CLI",
	}
	if !shouldPostPromptClaim(req, nil) {
		t.Fatal("TUI-originated prompt should mint an ActionTypePrompt claim")
	}
}

func TestShouldPostPromptClaim_AgentRouteDoesNotMint(t *testing.T) {
	cases := []string{"architect", "librarian", "engineer", "guide", "guardian", "tester"}
	for _, agent := range cases {
		t.Run(agent, func(t *testing.T) {
			req := &RouteRequest{
				SourceAgentID: agent,
				Input:         "agent-internal request",
			}
			if shouldPostPromptClaim(req, nil) {
				t.Fatalf("%s-originated route minted a prompt claim — only TUI prompts should", agent)
			}
		})
	}
}

func TestShouldPostPromptClaim_PreservesExistingParentClaimID(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "follow-up under existing cycle",
	}
	metadata := map[string]any{"parent_claim_id": "claim-existing-1"}
	if shouldPostPromptClaim(req, metadata) {
		t.Fatal("metadata already carries parent_claim_id — Guide must not overwrite it with a fresh prompt claim")
	}
}

func TestShouldPostPromptClaim_FireAndForgetDoesNotMint(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "async sub-request",
		FireAndForget: true,
	}
	if shouldPostPromptClaim(req, nil) {
		t.Fatal("fire-and-forget routes are async work, not user intent — must not mint a visible prompt cycle")
	}
}

func TestShouldPostPromptClaim_ControlPlaneDoesNotMint(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID: "tui",
		Input:         "plan-handoff coordination",
	}
	metadata := map[string]any{"control_plane_kind": "plan_handoff"}
	if shouldPostPromptClaim(req, metadata) {
		t.Fatal("control-plane coordination must remain invisible — never opens a chat row")
	}
}

func TestShouldPostPromptClaim_EmptyParentClaimIDFalls(t *testing.T) {
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
		if !shouldPostPromptClaim(req, metadata) {
			t.Fatalf("case %d: shouldPostPromptClaim = false; whitespace/non-string parent_claim_id should be treated as unset (metadata=%+v)", i, metadata)
		}
	}
}

func TestShouldPostPromptClaim_NilRequestSafe(t *testing.T) {
	if shouldPostPromptClaim(nil, nil) {
		t.Fatal("nil request must not panic and must not mint a claim")
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
