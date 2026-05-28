package guide

import (
	"context"
	"strings"
	"testing"
	"time"

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
// claim-backed consult row), and overwrote the real parent_claim_id —
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

func TestPostRoutedWorkClaimLinksClassificationClaimAndRouteTestament(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	req := &RouteRequest{
		SourceAgentID: "tui",
		SessionID:     "sess",
		CorrelationID: "corr-linked",
		Input:         "build a cli",
		Metadata: map[string]any{
			"guide_classification_claim_id": "route-claim-1",
			"guide_route_testament_id":      "route-testament-1",
		},
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
	if !claims.HasRelation(claim.Relations, claims.RelationshipClaim, "route-claim-1") {
		t.Fatalf("routed work claim missing classification claim relation: %+v", claim.Relations)
	}
	if !claims.HasRelation(claim.Relations, claims.RelationshipCausedBy, "route-testament-1") {
		t.Fatalf("routed work claim missing route testament caused_by relation: %+v", claim.Relations)
	}
}

func TestBuildForwardedRequestRequiresSessionBoardForClaimNativeTUIRoute(t *testing.T) {
	g := &Guide{}
	req := &RouteRequest{
		SourceAgentID: "tui",
		SessionID:     "sess",
		CorrelationID: "corr-no-board",
		Input:         "build a cli",
	}
	result := &RouteResult{TargetAgent: TargetAgent("architect"), Intent: IntentExecute, Confidence: 0.91}

	if _, err := g.buildForwardedRequest(req, result, req.CorrelationID); err == nil || !strings.Contains(err.Error(), "session claims board") {
		t.Fatalf("buildForwardedRequest error = %v, want missing session claims board", err)
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

func TestShouldOpenGuideClassificationClaim_NestedRoutesDoNotOpen(t *testing.T) {
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
				t.Fatal("nested/protocol route opened a Guide classification claim")
			}
		})
	}
}

func TestShouldOpenGuideClassificationClaim_DirectTargetOpens(t *testing.T) {
	req := &RouteRequest{
		SourceAgentID:  "tui",
		Input:          "@architect plan",
		TargetAgentID:  "architect",
		ExplicitTarget: true,
	}
	if !shouldOpenGuideClassificationClaim(req) {
		t.Fatal("top-level direct target route should still produce Guide route-decision lifecycle evidence")
	}
}

func TestGuideClassificationTestamentDirectAddressIncludesArtifactAndRelation(t *testing.T) {
	h := &guideClassificationClaim{
		sessionID:       "sess",
		claimID:         "classification-claim-1",
		requestedTarget: "architect",
		explicitTarget:  true,
		started:         time.Now(),
	}
	result := &RouteResult{
		TargetAgent:          TargetAgent("architect"),
		Intent:               IntentPlan,
		Domain:               DomainPlanning,
		Confidence:           0.95,
		ClassificationMethod: "direct_address",
		Reason:               "user addressed @architect",
	}

	testament := guideClassificationTestament(h, result, "architect", nil)
	if guideArtifactReference(testament.Artifacts, "direct_address") != "architect" {
		t.Fatalf("direct address artifact missing: %+v", testament.Artifacts)
	}
	if !claims.HasRelation(testament.Relations, claims.RelationshipDirectAddressed, "architect") {
		t.Fatalf("direct addressed relation missing: %+v", testament.Relations)
	}
	if guideArtifactReference(testament.Artifacts, "route_reason") == "" {
		t.Fatalf("route reason artifact missing: %+v", testament.Artifacts)
	}
}

func TestGuideClassificationReplayUsesValidatedRouteTestament(t *testing.T) {
	ctx := context.Background()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	generated, err := board.GenerateClaimAction(ctx, claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt}, []claims.Claim{{
		Title:       "Classifying request",
		Description: "build a cli",
		ActionType:  claims.ActionTypePrompt,
		Relations: []claims.Relation{
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			ID:          "route-validation-1",
			Type:        claims.ValidationTypeInspection,
			Required:    true,
			Description: "Guide selected route",
			QualityBar:  "target recorded",
		}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "route-classification-1"})
	if err != nil {
		t.Fatal(err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(ctx, claimID, "guide", claims.ClaimPostOptions{Reason: "posted"}); err != nil {
		t.Fatal(err)
	}
	if err := board.AcknowledgeClaimReceipt(ctx, claimID, "guide"); err != nil {
		t.Fatal(err)
	}
	if err := board.UpdateClaimProgress(ctx, claimID, claims.ClaimProgressUpdate{WorkSummary: "Classifying request"}, "guide"); err != nil {
		t.Fatal(err)
	}

	h := &guideClassificationClaim{
		board:        board,
		sessionID:    "sess",
		claimID:      claimID,
		validationID: generated.Claims[0].Validations[0].ID,
		input:        "build a cli",
		started:      time.Now(),
	}
	expected := &RouteResult{
		TargetAgent:          TargetAgent("architect"),
		Intent:               IntentPlan,
		Domain:               DomainPlanning,
		Confidence:           0.95,
		ClassificationMethod: "classifier",
		Reason:               "matched planning work",
	}
	guide := &Guide{}
	testamentID := guide.completeGuideClassificationClaim(ctx, h, expected, "architect", nil)
	if testamentID == "" {
		t.Fatal("classification completion did not post a route testament")
	}

	replayed, target, replayedTestamentID, ok := guide.replayGuideClassificationFromTestament(&RouteRequest{SessionID: "sess", Input: "build a cli"}, h)
	if !ok {
		t.Fatal("validated route testament was not replayed")
	}
	if replayedTestamentID != testamentID {
		t.Fatalf("replayed testament id = %q, want %q", replayedTestamentID, testamentID)
	}
	if target != "architect" || replayed.TargetAgent != TargetAgent("architect") {
		t.Fatalf("replayed target = %q/%q, want architect", target, replayed.TargetAgent)
	}
	if replayed.Intent != IntentPlan || replayed.Domain != DomainPlanning {
		t.Fatalf("replayed classification = intent %s domain %s", replayed.Intent, replayed.Domain)
	}
}

func TestBeginGuideClassificationClaimDoesNotRepostTerminalReplayClaim(t *testing.T) {
	ctx := context.Background()
	sessionID := "sess-route-replay-terminal"
	g := &Guide{conversation: NewConversationFlowManager(ConversationFlowConfig{})}
	req := &RouteRequest{
		SourceAgentID: "tui",
		SessionID:     sessionID,
		CorrelationID: "corr-route-replay-terminal",
		Input:         "build a cli",
	}
	h := g.beginGuideClassificationClaim(ctx, req)
	if h == nil {
		t.Fatal("classification claim was not opened")
	}
	result := &RouteResult{
		TargetAgent:          TargetAgent("architect"),
		Intent:               IntentPlan,
		Domain:               DomainPlanning,
		Confidence:           0.95,
		ClassificationMethod: "classifier",
	}
	if testamentID := g.completeGuideClassificationClaim(ctx, h, result, "architect", nil); testamentID == "" {
		t.Fatal("classification claim was not completed")
	}

	replayReq := &RouteRequest{
		SourceAgentID: "tui",
		SessionID:     sessionID,
		CorrelationID: "corr-route-replay-terminal",
		Input:         "build a cli",
	}
	replayHandle := g.beginGuideClassificationClaim(ctx, replayReq)
	if replayHandle == nil {
		t.Fatal("terminal classification claim replay should still return a graph handle")
	}
	claim, ok := h.board.CloneClaim(h.claimID)
	if !ok {
		t.Fatal("classification claim disappeared")
	}
	if claim.LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("classification claim lifecycle = %s, want satisfied; replay must not repost terminal claims", claim.LifecycleStatus)
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
