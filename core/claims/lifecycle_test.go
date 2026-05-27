package claims

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
)

func TestClaimLifecycleTransitionGraph(t *testing.T) {
	allowed := []struct {
		from ClaimLifecycleStatus
		to   ClaimLifecycleStatus
	}{
		{"", ClaimLifecycleGenerated},
		{ClaimLifecycleGenerated, ClaimLifecyclePosted},
		{ClaimLifecycleGenerated, ClaimLifecyclePostFailed},
		{ClaimLifecyclePosted, ClaimLifecycleReceived},
		{ClaimLifecycleReceived, ClaimLifecycleProgressed},
		{ClaimLifecycleProgressed, ClaimLifecycleTestamentGenerated},
		{ClaimLifecycleTestamentGenerated, ClaimLifecycleTestamentAcknowledged},
		{ClaimLifecycleTestamentAcknowledged, ClaimLifecycleValidating},
		{ClaimLifecycleValidating, ClaimLifecycleSatisfied},
		{ClaimLifecycleValidating, ClaimLifecycleValidationIncomplete},
		{ClaimLifecycleValidating, ClaimLifecycleValidationFailed},
		{ClaimLifecycleValidating, ClaimLifecycleValidationErrored},
	}
	for _, tc := range allowed {
		if !CanTransitionClaimLifecycle(tc.from, tc.to) {
			t.Fatalf("expected claim lifecycle transition %q -> %q to be allowed", tc.from, tc.to)
		}
	}
	denied := []struct {
		from ClaimLifecycleStatus
		to   ClaimLifecycleStatus
	}{
		{ClaimLifecycleGenerated, ClaimLifecycleReceived},
		{ClaimLifecyclePosted, ClaimLifecycleSatisfied},
		{ClaimLifecycleSatisfied, ClaimLifecycleProgressed},
		{ClaimLifecycleValidationFailed, ClaimLifecycleProgressed},
	}
	for _, tc := range denied {
		if CanTransitionClaimLifecycle(tc.from, tc.to) {
			t.Fatalf("expected claim lifecycle transition %q -> %q to be denied", tc.from, tc.to)
		}
	}
}

func TestTestamentLifecycleTransitionGraph(t *testing.T) {
	allowed := []struct {
		from TestamentLifecycleStatus
		to   TestamentLifecycleStatus
	}{
		{"", TestamentLifecycleGenerated},
		{TestamentLifecycleGenerated, TestamentLifecyclePosted},
		{TestamentLifecyclePosted, TestamentLifecycleReceived},
		{TestamentLifecycleReceived, TestamentLifecycleValidating},
		{TestamentLifecycleValidating, TestamentLifecycleValidated},
		{TestamentLifecycleValidating, TestamentLifecycleValidationIncomplete},
		{TestamentLifecycleValidating, TestamentLifecycleValidationFailed},
	}
	for _, tc := range allowed {
		if !CanTransitionTestamentLifecycle(tc.from, tc.to) {
			t.Fatalf("expected testament lifecycle transition %q -> %q to be allowed", tc.from, tc.to)
		}
	}
	if CanTransitionTestamentLifecycle(TestamentLifecycleValidated, TestamentLifecyclePosted) {
		t.Fatal("terminal testament lifecycle state allowed transition back to posted")
	}
}

func TestLifecycleJSONRejectsUnknownStatus(t *testing.T) {
	var claim struct {
		Status ClaimLifecycleStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(`{"status":"teleported"}`), &claim); err == nil {
		t.Fatal("expected unknown claim lifecycle status to fail JSON decode")
	}
	var testament struct {
		Status TestamentLifecycleStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(`{"status":"misplaced"}`), &testament); err == nil {
		t.Fatal("expected unknown testament lifecycle status to fail JSON decode")
	}
}

func TestLockedLifecycleTransitionRejectsIllegalReplayTransition(t *testing.T) {
	board := testBoard()
	claim := &Claim{ID: "terminal-claim", LifecycleStatus: ClaimLifecycleSatisfied}
	if board.transitionClaimLifecycleLocked(claim, ClaimLifecycleProgressed, "architect", "illegal replay", claim.Created) {
		t.Fatal("illegal terminal claim transition mutated state")
	}
	if claim.LifecycleStatus != ClaimLifecycleSatisfied || len(claim.LifecycleHistory) != 0 {
		t.Fatalf("claim mutated after illegal transition: %+v", claim)
	}
	testament := &Testament{ID: "terminal-testament", LifecycleStatus: TestamentLifecycleValidated}
	if board.transitionTestamentLifecycleLocked(testament, TestamentLifecyclePosted, "architect", "illegal replay", testament.Created) {
		t.Fatal("illegal terminal testament transition mutated state")
	}
	if testament.LifecycleStatus != TestamentLifecycleValidated || len(testament.LifecycleHistory) != 0 {
		t.Fatalf("testament mutated after illegal transition: %+v", testament)
	}
}

func TestGenerateClaimActionDoesNotWakeTargetUntilPosted(t *testing.T) {
	bus := newCaptureBus()
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "board-life",
		SessionID: "session-life",
		TaskID:    "task-life",
		DeltaBus:  bus,
	})
	claim := lifecycleTestClaim("claim-life", "Lifecycle claim")
	result, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: "gen-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) != 1 {
		t.Fatalf("generated claims = %d, want 1", len(result.Claims))
	}
	stored, ok := board.CloneClaim("claim-life")
	if !ok {
		t.Fatal("generated claim missing from board")
	}
	if stored.LifecycleStatus != ClaimLifecycleGenerated {
		t.Fatalf("claim lifecycle = %q, want generated", stored.LifecycleStatus)
	}
	if len(bus.Published()) != 0 {
		t.Fatalf("generated claim published bus deltas: %+v", bus.Published())
	}
	env := PullWork(PullWorkConfig{AgentID: "engineer-b", Board: board})
	if env == nil {
		t.Fatal("expected turn envelope")
	}
	if len(env.DirectedClaims) != 0 || len(env.InboundAsks) != 0 {
		t.Fatalf("generated claim surfaced as work: directed=%d asks=%d", len(env.DirectedClaims), len(env.InboundAsks))
	}
	if err := board.PostGeneratedClaim(context.Background(), "claim-life", "architect", ClaimPostOptions{}); err != nil {
		t.Fatal(err)
	}
	stored, _ = board.CloneClaim("claim-life")
	if stored.LifecycleStatus != ClaimLifecyclePosted {
		t.Fatalf("claim lifecycle after post = %q, want posted", stored.LifecycleStatus)
	}
	env = PullWork(PullWorkConfig{AgentID: "engineer-b", Board: board})
	if len(env.DirectedClaims) != 1 {
		t.Fatalf("posted claim directed work count = %d, want 1", len(env.DirectedClaims))
	}
}

func TestGenerateClaimActionIdempotency(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	claim := lifecycleTestClaim("", "Idempotent claim")
	first, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Action.ID != second.Action.ID {
		t.Fatalf("idempotent action IDs differ: %q vs %q", first.Action.ID, second.Action.ID)
	}
	if first.Claims[0].ID != second.Claims[0].ID {
		t.Fatalf("idempotent claim IDs differ: %q vs %q", first.Claims[0].ID, second.Claims[0].ID)
	}
	if got := board.Projection().TotalClaims; got != 1 {
		t.Fatalf("total claims = %d, want 1", got)
	}
}

func TestGenerateClaimUnderExistingActionPreservesAction(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	action := Action{ID: "existing-action", AgentID: "architect", Type: ActionTypeTask, Status: ActionStatusPending}
	if err := board.PostAction(ctx, action, []Claim{lifecycleTestClaim("existing-root", "Existing root")}); err != nil {
		t.Fatal(err)
	}
	before, ok := board.CloneAction("existing-action")
	if !ok {
		t.Fatal("existing action missing")
	}
	generated, err := board.GenerateClaim(ctx, "existing-action", lifecycleTestClaim("existing-child", "Existing child"), GenerateClaimActionOptions{IdempotencyKey: "single-child"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := board.GenerateClaim(ctx, "existing-action", lifecycleTestClaim("other-child", "Other child"), GenerateClaimActionOptions{IdempotencyKey: "single-child"})
	if err != nil {
		t.Fatal(err)
	}
	if generated.ID != again.ID {
		t.Fatalf("single-claim idempotency returned %q then %q", generated.ID, again.ID)
	}
	after, ok := board.CloneAction("existing-action")
	if !ok {
		t.Fatal("existing action missing after generation")
	}
	if before.Sequence != after.Sequence || !before.Created.Equal(after.Created) || len(before.StatusHistory) != len(after.StatusHistory) {
		t.Fatalf("existing action was restamped: before=%+v after=%+v", before, after)
	}
	child, ok := board.CloneClaim("existing-child")
	if !ok {
		t.Fatal("generated child claim missing")
	}
	if child.LifecycleStatus != ClaimLifecycleGenerated {
		t.Fatalf("child lifecycle = %q, want generated", child.LifecycleStatus)
	}
}

func TestGenerateClaimActionValidatesStructure(t *testing.T) {
	base := lifecycleTestClaim("validate-claim", "Validate claim")
	cases := []struct {
		name   string
		action Action
		claim  Claim
	}{
		{name: "missing action agent", action: Action{Type: ActionTypeTask}, claim: base},
		{name: "missing action type", action: Action{AgentID: "architect"}, claim: base},
		{name: "missing title", action: Action{AgentID: "architect", Type: ActionTypeTask}, claim: func() Claim {
			c := base
			c.Title = ""
			return c
		}()},
		{name: "missing description", action: Action{AgentID: "architect", Type: ActionTypeTask}, claim: func() Claim {
			c := base
			c.Description = ""
			return c
		}()},
		{name: "missing validation id", action: Action{AgentID: "architect", Type: ActionTypeTask}, claim: func() Claim {
			c := base
			c.Validations = []*Validation{{Description: "validation", QualityBar: "must pass", Type: ValidationTypeInspection, Required: true}}
			return c
		}()},
		{name: "missing validation quality bar", action: Action{AgentID: "architect", Type: ActionTypeTask}, claim: func() Claim {
			c := base
			c.Validations = []*Validation{{ID: "validation-id", Description: "validation", Type: ValidationTypeInspection, Required: true}}
			return c
		}()},
		{name: "invalid expected tool", action: Action{AgentID: "architect", Type: ActionTypeTask}, claim: func() Claim {
			c := base
			c.ExpectedToolCalls = []ExpectedToolCall{{Required: true}}
			return c
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board := testBoard()
			_, err := board.GenerateClaimAction(context.Background(), tc.action, []Claim{tc.claim}, GenerateClaimActionOptions{})
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGenerateClaimActionAllowsMissingSubjectOnlyWhenExplicit(t *testing.T) {
	board := testBoard()
	claim := lifecycleTestClaim("draft-claim", "Draft claim")
	claim.Relations = []Relation{{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer}}
	if _, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypePrompt}, []Claim{claim}, GenerateClaimActionOptions{}); err == nil {
		t.Fatal("expected missing subject to fail without explicit option")
	}
	if _, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypePrompt}, []Claim{claim}, GenerateClaimActionOptions{AllowMissingSubject: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPostGeneratedClaimSelfTargetFailsWithErrorArtifact(t *testing.T) {
	board := testBoard()
	claim := lifecycleTestClaim("self-claim", "Self consult")
	claim.ActionType = ActionTypeConsultation
	claim.Relations = []Relation{
		{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}
	_, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = board.PostGeneratedClaim(context.Background(), "self-claim", "architect", ClaimPostOptions{})
	if err == nil {
		t.Fatal("expected self-target post to fail")
	}
	stored, _ := board.CloneClaim("self-claim")
	if stored.LifecycleStatus != ClaimLifecyclePostFailed {
		t.Fatalf("claim lifecycle = %q, want post_failed", stored.LifecycleStatus)
	}
	if stored.Status != ClaimStatusRejected {
		t.Fatalf("claim status = %q, want rejected", stored.Status)
	}
	if !boardHasErrorArtifactForClaim(board, "self-claim") {
		t.Fatal("post failure did not create a linked error artifact")
	}
}

func TestPostGeneratedClaimUsesCanonicalIdentityAndPolicy(t *testing.T) {
	resolver := AgentRefResolverFunc(func(_ context.Context, sessionID, agentID string) (AgentRef, bool) {
		if sessionID != "ses-1" {
			return AgentRef{}, false
		}
		switch agentID {
		case "architect", "architect-alias":
			return AgentRef{UID: "uid-architect", Type: "architect", Name: agentID, Generation: 1, Model: "claude"}.Normalized(), true
		case "librarian":
			return AgentRef{UID: "uid-librarian", Type: "librarian", Name: "librarian", Generation: 1, Model: "claude"}.Normalized(), true
		default:
			return AgentRef{}, false
		}
	})
	t.Run("canonical self target fails", func(t *testing.T) {
		board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-1", PipelineID: "pipe-1", TaskID: "task-1", SessionID: "ses-1", AgentRefResolver: resolver})
		claim := lifecyclePeerClaim("canonical-self", "Canonical self", "architect", "architect-alias")
		if _, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := board.PostGeneratedClaim(context.Background(), "canonical-self", "architect", ClaimPostOptions{}); err == nil {
			t.Fatal("expected canonical self-target failure")
		}
		if got := errorArtifactKindForClaim(board, "canonical-self"); got != ArtifactKindPolicyDenied {
			t.Fatalf("error artifact kind = %q, want policy_denied", got)
		}
	})
	t.Run("unresolved subject fails before post", func(t *testing.T) {
		board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-1", PipelineID: "pipe-1", TaskID: "task-1", SessionID: "ses-1", AgentRefResolver: resolver})
		claim := lifecyclePeerClaim("missing-subject", "Missing subject", "architect", "unknown")
		if _, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := board.PostGeneratedClaim(context.Background(), "missing-subject", "architect", ClaimPostOptions{}); err == nil {
			t.Fatal("expected unresolved subject failure")
		}
		if got := errorArtifactKindForClaim(board, "missing-subject"); got != ArtifactKindMissingDependency {
			t.Fatalf("error artifact kind = %q, want missing_dependency", got)
		}
	})
	t.Run("policy denial records policy artifact", func(t *testing.T) {
		var seenSubject string
		board := NewClaimsBoard(ClaimsBoardConfig{
			BoardID:          "board-1",
			PipelineID:       "pipe-1",
			TaskID:           "task-1",
			SessionID:        "ses-1",
			AgentRefResolver: resolver,
			ClaimPostPolicy: ClaimPostPolicyFunc(func(_ context.Context, req ClaimPostPolicyRequest) ClaimPostPolicyDecision {
				seenSubject = req.Subject.UID
				return ClaimPostPolicyDecision{Allowed: false, Reason: "denied by test policy", FailureKind: ArtifactKindPolicyDenied}
			}),
		})
		claim := lifecyclePeerClaim("policy-denied", "Policy denied", "architect", "librarian")
		if _, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := board.PostGeneratedClaim(context.Background(), "policy-denied", "architect", ClaimPostOptions{}); err == nil {
			t.Fatal("expected policy denial")
		}
		if seenSubject != "uid-librarian" {
			t.Fatalf("policy saw subject UID %q, want uid-librarian", seenSubject)
		}
		if got := errorArtifactKindForClaim(board, "policy-denied"); got != ArtifactKindPolicyDenied {
			t.Fatalf("error artifact kind = %q, want policy_denied", got)
		}
	})
}

func TestRecordClaimLifecycleFailureCreatesStructuredErrorArtifact(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if _, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("progress-failure", "Progress failure")}, GenerateClaimActionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.PostGeneratedClaim(ctx, "progress-failure", "architect", ClaimPostOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.RecordClaimProgressFailure(ctx, "progress-failure", "engineer-b", LifecycleFailureOptions{
		Reason:       "tool timed out",
		ArtifactKind: ArtifactKindToolTimeout,
		Metadata:     map[string]any{"tool": "workspace_read"},
	}); err != nil {
		t.Fatal(err)
	}
	claim, _ := board.CloneClaim("progress-failure")
	if claim.LifecycleStatus != ClaimLifecycleProgressFailed {
		t.Fatalf("claim lifecycle = %q, want progress_failed", claim.LifecycleStatus)
	}
	artifact := errorArtifactForClaim(board, "progress-failure")
	if artifact == nil {
		t.Fatal("missing error artifact")
	}
	if artifact.Kind != ArtifactKindToolTimeout {
		t.Fatalf("artifact kind = %q, want tool_timeout", artifact.Kind)
	}
	if artifact.Metadata[lifecycleFailureStatusKey] != string(ClaimLifecycleProgressFailed) || artifact.Metadata["tool"] != "workspace_read" {
		t.Fatalf("artifact metadata missing lifecycle structure: %#v", artifact.Metadata)
	}
}

func TestGeneratedTestamentPostsBeforeClaimResolution(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{lifecycleReceiptClaim("claim-consult")}); err != nil {
		t.Fatal(err)
	}
	generated, err := board.GenerateTestamentAction(ctx, Action{AgentID: "librarian", Type: ActionTypeTestament}, []Testament{{
		ID:        "testament-consult",
		AgentID:   "librarian",
		Summary:   "No Python project exists.",
		Relations: []Relation{{Related: "claim-consult", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts: []*Artifact{{Kind: "response_text", Reference: "No Python project exists."}},
	}}, GenerateTestamentActionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if generated.Testaments[0].LifecycleStatus != TestamentLifecycleGenerated {
		t.Fatalf("testament lifecycle = %q, want generated", generated.Testaments[0].LifecycleStatus)
	}
	claim, _ := board.CloneClaim("claim-consult")
	if claim.Status != ClaimStatusPending {
		t.Fatalf("claim status after generated testament = %q, want pending", claim.Status)
	}
	if err := board.PostGeneratedTestament(ctx, "testament-consult", "librarian", TestamentPostOptions{}); err != nil {
		t.Fatal(err)
	}
	claim, _ = board.CloneClaim("claim-consult")
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status after posted testament = %q, want accepted", claim.Status)
	}
	if claim.LifecycleStatus != ClaimLifecycleSatisfied {
		t.Fatalf("claim lifecycle after posted testament = %q, want satisfied", claim.LifecycleStatus)
	}
	testament, _ := board.CloneTestament("testament-consult")
	if testament.LifecycleStatus != TestamentLifecyclePosted {
		t.Fatalf("testament lifecycle = %q, want posted", testament.LifecycleStatus)
	}
}

func TestGeneratedTestamentValidatesActionAndArtifactHeaders(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{lifecycleReceiptClaim("artifact-claim")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		action     Action
		artifact   *Artifact
		standalone bool
	}{
		{name: "missing action agent", action: Action{Type: ActionTypeTestament}, artifact: &Artifact{Kind: "response_text", Reference: "ok"}},
		{name: "invalid action type", action: Action{AgentID: "librarian", Type: ActionTypeTask}, artifact: &Artifact{Kind: "response_text", Reference: "ok"}},
		{name: "nil artifact", action: Action{AgentID: "librarian", Type: ActionTypeTestament}},
		{name: "missing artifact kind", action: Action{AgentID: "librarian", Type: ActionTypeTestament}, artifact: &Artifact{Reference: "ok"}},
		{name: "missing artifact reference and hash", action: Action{AgentID: "librarian", Type: ActionTypeTestament}, artifact: &Artifact{Kind: "response_text"}},
		{name: "negative artifact size", action: Action{AgentID: "librarian", Type: ActionTypeTestament}, artifact: &Artifact{Kind: "response_text", Reference: "ok", Size: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testament := Testament{
				ID:        "testament-" + tc.name,
				AgentID:   "librarian",
				Summary:   "answer",
				Relations: []Relation{{Related: "artifact-claim", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
			}
			testament.Artifacts = []*Artifact{tc.artifact}
			if _, err := board.GenerateTestamentAction(ctx, tc.action, []Testament{testament}, GenerateTestamentActionOptions{AllowStandalone: tc.standalone}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReceiptAcknowledgementIsReceiverCommitted(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	claim := lifecycleTestClaim("receipt-claim", "Receipt claim")
	if _, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.AcknowledgeClaimReceipt(ctx, "receipt-claim", "engineer-b"); err == nil {
		t.Fatal("expected receipt before post to fail")
	}
	if err := board.PostGeneratedClaim(ctx, "receipt-claim", "architect", ClaimPostOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.AcknowledgeClaimReceipt(ctx, "receipt-claim", "architect"); err == nil {
		t.Fatal("expected sender-side claim receipt to fail")
	}
	if err := board.AcknowledgeClaimReceipt(ctx, "receipt-claim", "engineer-b"); err != nil {
		t.Fatal(err)
	}
	if err := board.AcknowledgeClaimReceipt(ctx, "receipt-claim", "engineer-b"); err != nil {
		t.Fatal(err)
	}
	stored, _ := board.CloneClaim("receipt-claim")
	if stored.LifecycleStatus != ClaimLifecycleReceived {
		t.Fatalf("claim lifecycle = %q, want received", stored.LifecycleStatus)
	}
	receivedCount := 0
	for _, change := range stored.LifecycleHistory {
		if change.To == string(ClaimLifecycleReceived) {
			receivedCount++
		}
	}
	if receivedCount != 1 {
		t.Fatalf("received lifecycle transitions = %d, want 1", receivedCount)
	}
}

func TestTestamentReceiptIsSourceCommitted(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	claim := lifecyclePeerClaim("consult-receipt", "Consult receipt", "architect", "librarian")
	if _, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.PostGeneratedClaim(ctx, "consult-receipt", "architect", ClaimPostOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := board.GenerateTestamentAction(ctx, Action{AgentID: "librarian", Type: ActionTypeTestament}, []Testament{{
		ID:        "consult-testament",
		AgentID:   "librarian",
		Summary:   "Workspace has no Python project.",
		Relations: []Relation{{Related: "consult-receipt", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts: []*Artifact{{Kind: "response_text", Reference: "Workspace has no Python project."}},
	}}, GenerateTestamentActionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.PostGeneratedTestament(ctx, "consult-testament", "librarian", TestamentPostOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := board.AcknowledgeTestamentReceipt(ctx, "consult-testament", "librarian"); err == nil {
		t.Fatal("expected target-side testament receipt to fail")
	}
	if err := board.AcknowledgeTestamentReceipt(ctx, "consult-testament", "architect"); err != nil {
		t.Fatal(err)
	}
	testament, _ := board.CloneTestament("consult-testament")
	if testament.LifecycleStatus != TestamentLifecycleReceived {
		t.Fatalf("testament lifecycle = %q, want received", testament.LifecycleStatus)
	}
	stored, _ := board.CloneClaim("consult-receipt")
	if stored.LifecycleStatus != ClaimLifecycleTestamentAcknowledged {
		t.Fatalf("claim lifecycle = %q, want testament_acknowledged", stored.LifecycleStatus)
	}
}

func TestTestamentValidationLifecycleUpdatesParentClaim(t *testing.T) {
	cases := []struct {
		name         string
		to           TestamentLifecycleStatus
		claimTo      ClaimLifecycleStatus
		coarseStatus ClaimStatus
		passFirst    bool
	}{
		{name: "validated", to: TestamentLifecycleValidated, claimTo: ClaimLifecycleSatisfied, coarseStatus: ClaimStatusAccepted, passFirst: true},
		{name: "incomplete", to: TestamentLifecycleValidationIncomplete, claimTo: ClaimLifecycleValidationIncomplete, coarseStatus: ClaimStatusRejected},
		{name: "failed", to: TestamentLifecycleValidationFailed, claimTo: ClaimLifecycleValidationFailed, coarseStatus: ClaimStatusRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board := testBoard()
			ctx := context.Background()
			claim := lifecyclePeerClaim("validation-"+tc.name, "Validation "+tc.name, "architect", "librarian")
			if _, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}, GenerateClaimActionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := board.PostGeneratedClaim(ctx, claim.ID, "architect", ClaimPostOptions{}); err != nil {
				t.Fatal(err)
			}
			testamentID := "testament-" + tc.name
			if _, err := board.GenerateTestamentAction(ctx, Action{AgentID: "librarian", Type: ActionTypeTestament}, []Testament{{
				ID:        testamentID,
				AgentID:   "librarian",
				Summary:   "answer",
				Relations: []Relation{{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
				Artifacts: []*Artifact{{Kind: "response_text", Reference: "answer"}},
			}}, GenerateTestamentActionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := board.PostGeneratedTestament(ctx, testamentID, "librarian", TestamentPostOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, "architect"); err != nil {
				t.Fatal(err)
			}
			if err := board.BeginTestamentValidation(ctx, testamentID, "architect"); err != nil {
				t.Fatal(err)
			}
			if tc.passFirst {
				if err := board.EvaluateValidation(ctx, claim.ID, claim.Validations[0].ID, StatusChange{AgentID: "architect", To: string(ValidationStatusPassed), Reason: "passes"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := board.CompleteTestamentValidation(ctx, testamentID, "architect", tc.to, "done"); err != nil {
				t.Fatal(err)
			}
			testament, _ := board.CloneTestament(testamentID)
			if testament.LifecycleStatus != tc.to {
				t.Fatalf("testament lifecycle = %q, want %q", testament.LifecycleStatus, tc.to)
			}
			stored, _ := board.CloneClaim(claim.ID)
			if stored.LifecycleStatus != tc.claimTo {
				t.Fatalf("claim lifecycle = %q, want %q", stored.LifecycleStatus, tc.claimTo)
			}
			if stored.Status != tc.coarseStatus {
				t.Fatalf("claim status = %q, want %q", stored.Status, tc.coarseStatus)
			}
		})
	}
}

func TestGeneratedLifecycleDurableReplay(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "durable-life", SessionID: "session-life", TaskID: "task-life", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Board().GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("durable-claim", "Durable claim")}, GenerateClaimActionOptions{IdempotencyKey: "durable-key"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostGeneratedClaim(context.Background(), "durable-claim", "architect", ClaimPostOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "durable-life", SessionID: "session-life", TaskID: "task-life", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	claim, ok := reopened.Board().CloneClaim("durable-claim")
	if !ok {
		t.Fatal("durable claim missing after replay")
	}
	if claim.LifecycleStatus != ClaimLifecyclePosted {
		t.Fatalf("replayed lifecycle = %q, want posted", claim.LifecycleStatus)
	}
	again, err := reopened.Board().GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("", "Duplicate durable claim")}, GenerateClaimActionOptions{IdempotencyKey: "durable-key"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Claims[0].ID != "durable-claim" {
		t.Fatalf("idempotency after replay returned claim %q, want durable-claim", again.Claims[0].ID)
	}
}

func TestGenerateClaimUnderExistingActionIdempotencySurvivesReplay(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "durable-single", SessionID: "session-life", TaskID: "task-life", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostAction(ctx, Action{ID: "durable-action", AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("durable-root", "Durable root")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Board().GenerateClaim(ctx, "durable-action", lifecycleTestClaim("durable-child", "Durable child"), GenerateClaimActionOptions{IdempotencyKey: "durable-single-key"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "durable-single", SessionID: "session-life", TaskID: "task-life", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	again, err := reopened.Board().GenerateClaim(ctx, "durable-action", lifecycleTestClaim("durable-other", "Durable other"), GenerateClaimActionOptions{IdempotencyKey: "durable-single-key"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != "durable-child" {
		t.Fatalf("replayed single generation returned %q, want durable-child", again.ID)
	}
	if got := reopened.Board().Projection().TotalClaims; got != 2 {
		t.Fatalf("total claims after replayed idempotency = %d, want 2", got)
	}
}

func TestConcurrentPostGeneratedClaimIsIdempotent(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if _, err := board.GenerateClaimAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("race-claim", "Race claim")}, GenerateClaimActionOptions{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- board.PostGeneratedClaim(ctx, "race-claim", "architect", ClaimPostOptions{})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	claim, _ := board.CloneClaim("race-claim")
	if claim.LifecycleStatus != ClaimLifecyclePosted {
		t.Fatalf("claim lifecycle = %q, want posted", claim.LifecycleStatus)
	}
	count := 0
	for _, change := range claim.LifecycleHistory {
		if change.To == string(ClaimLifecyclePosted) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("posted lifecycle transitions = %d, want 1", count)
	}
}

func TestGeneratedLifecycleSnapshotReplay(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "snapshot-life", SessionID: "session-life", TaskID: "task-life", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Board().GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{lifecycleTestClaim("snapshot-claim", "Snapshot claim")}, GenerateClaimActionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDurableBoard(ClaimsBoardConfig{BoardID: "snapshot-life", SessionID: "session-life", TaskID: "task-life", SessionDir: filepath.Clean(dir)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	claim, ok := reopened.Board().CloneClaim("snapshot-claim")
	if !ok {
		t.Fatal("snapshot claim missing")
	}
	if claim.LifecycleStatus != ClaimLifecycleGenerated {
		t.Fatalf("snapshot lifecycle = %q, want generated", claim.LifecycleStatus)
	}
}

func lifecycleTestClaim(id, title string) Claim {
	claim := testClaim(id, title)
	claim.Description = title + " description"
	claim.Validations = []*Validation{{ID: id + "-validation", Description: "Validate " + title, QualityBar: "must pass", Type: ValidationTypeInspection, Required: true}}
	return claim
}

func lifecycleReceiptClaim(id string) Claim {
	claim := lifecycleTestClaim(id, "Receipt "+id)
	claim.Validations = []*Validation{{ID: id + "-receipt", Description: "Receive response", QualityBar: "response.received", Type: ValidationTypeReceipt, Required: true}}
	return claim
}

func lifecyclePeerClaim(id, title, issuer, subject string) Claim {
	claim := lifecycleTestClaim(id, title)
	claim.ActionType = ActionTypeConsultation
	claim.Relations = []Relation{
		{Related: issuer, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}
	return claim
}

func boardHasErrorArtifactForClaim(board *ClaimsBoard, claimID string) bool {
	return errorArtifactForClaim(board, claimID) != nil
}

func errorArtifactKindForClaim(board *ClaimsBoard, claimID string) string {
	if artifact := errorArtifactForClaim(board, claimID); artifact != nil {
		return artifact.Kind
	}
	return ""
}

func errorArtifactForClaim(board *ClaimsBoard, claimID string) *Artifact {
	for _, testament := range board.Projection().Testaments {
		if !HasRelation(testament.Relations, RelationshipClaim, claimID) {
			continue
		}
		for _, artifact := range testament.Artifacts {
			if artifact != nil && isErrorArtifactKind(artifact.Kind) {
				return artifact
			}
		}
	}
	return nil
}
