package claims

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func testBoard() *ClaimsBoard {
	return NewClaimsBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		PipelineID: "pipe-1",
		TaskID:     "task-1",
		SessionID:  "ses-1",
	})
}

func testClaim(id, title string) Claim {
	return Claim{
		ID:    id,
		Title: title,
		Relations: []Relation{
			{Related: "inspector-a", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Scope: []ClaimScopeEntry{{Kind: "file", Key: "services/auth/" + id + ".go"}},
	}
}

func testClaimWithValidations(id, title string) Claim {
	c := testClaim(id, title)
	c.Validations = []*Validation{
		{Description: "validation 1 for " + id, QualityBar: "must pass", Type: ValidationTypeTest, Required: true},
		{Description: "validation 2 for " + id, QualityBar: "must pass", Type: ValidationTypeInspection, Required: true},
	}
	return c
}

func TestNewClaimsBoard(t *testing.T) {
	b := testBoard()
	if b.BoardID() != "board-1" {
		t.Errorf("BoardID: %q", b.BoardID())
	}
	if b.Phase() != BoardPhaseImplementation {
		t.Errorf("Phase: %q", b.Phase())
	}
	if b.Iteration() != 0 {
		t.Errorf("Iteration: %d", b.Iteration())
	}
}

func TestPostAction(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	action := Action{AgentID: "inspector-a", Type: ActionTypeTask}
	claims := []Claim{
		testClaimWithValidations("c1", "first claim"),
		testClaimWithValidations("c2", "second claim"),
		testClaim("c3", "third claim (no validations)"),
	}

	if err := b.PostAction(ctx, action, claims); err != nil {
		t.Fatal(err)
	}

	p := b.Projection()
	if p.TotalClaims != 3 {
		t.Errorf("TotalClaims: %d, want 3", p.TotalClaims)
	}
	if p.PendingCount != 3 {
		t.Errorf("PendingCount: %d, want 3", p.PendingCount)
	}
	if p.TotalValidations != 4 {
		t.Errorf("TotalValidations: %d, want 4", p.TotalValidations)
	}

	// Verify ClaimID stamped on validations.
	c1, _ := b.ClaimByID("c1")
	for _, v := range c1.Validations {
		if v.ClaimID != "c1" {
			t.Errorf("validation ClaimID: %q, want c1", v.ClaimID)
		}
	}
}

func TestPostActionEmpty(t *testing.T) {
	b := testBoard()
	err := b.PostAction(context.Background(), Action{}, nil)
	if err == nil {
		t.Error("expected error for empty claims")
	}
}

func TestPostActionDuplicateID(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "first")})

	err := b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "duplicate")})
	if err == nil {
		t.Error("expected error for duplicate claim ID")
	}
	// Board should still have exactly 1 claim (no partial from second batch).
	if p := b.Projection(); p.TotalClaims != 1 {
		t.Errorf("TotalClaims: %d, want 1 (no partial mutation)", p.TotalClaims)
	}
}

func TestUpdateClaimProgress(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})

	if err := b.UpdateClaimProgress(ctx, "c1", ClaimProgressUpdate{WorkSummary: "started"}, "engineer-b"); err != nil {
		t.Fatal(err)
	}

	p := b.Projection()
	if p.InProgressCount != 1 {
		t.Errorf("InProgressCount: %d, want 1", p.InProgressCount)
	}
}

func TestUpdateClaimProgressTerminal(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	c := testClaimWithValidations("c1", "claim")
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})

	// Submit testament → testified → transition → evaluate → accepted.
	testament := Testament{
		AgentID: "engineer-b", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}
	b.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{testament})
	b.TransitionToValidation(ctx)

	for _, v := range b.claims["c1"].Validations {
		b.EvaluateValidation(ctx, "c1", v.ID, StatusChange{
			To: string(ValidationStatusPassed), AgentID: "inspector-a",
		})
	}

	// Claim should be accepted — update should fail.
	err := b.UpdateClaimProgress(ctx, "c1", ClaimProgressUpdate{}, "engineer-b")
	if err == nil {
		t.Error("expected error updating terminal claim")
	}
}

func TestSubmitTestaments(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})

	testament := Testament{
		AgentID: "engineer-b", Summary: "Implemented the thing",
		Relations: []Relation{
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{
			{Kind: "code_reference", Reference: "services/auth/jwk.go:47-89"},
			{Kind: "diff", Reference: "+42 lines"},
		},
	}

	if err := b.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{testament}); err != nil {
		t.Fatal(err)
	}

	p := b.Projection()
	if p.TestifiedCount != 1 {
		t.Errorf("TestifiedCount: %d, want 1", p.TestifiedCount)
	}
	if p.TotalTestaments != 1 {
		t.Errorf("TotalTestaments: %d, want 1", p.TotalTestaments)
	}
	if p.TotalArtifacts != 2 {
		t.Errorf("TotalArtifacts: %d, want 2", p.TotalArtifacts)
	}

	// Verify TestamentID stamped on artifacts.
	for _, t2 := range b.testaments {
		for _, a := range t2.Artifacts {
			if a.TestamentID != t2.ID {
				t.Errorf("artifact TestamentID: %q, want %q", a.TestamentID, t2.ID)
			}
		}
	}
}

func TestSubmitErrorTestamentPostsFailureEvidenceWithoutAutoResolvingReceipt(t *testing.T) {
	b := testBoard()
	ctx := context.Background()
	claim := testClaim("c1", "claim")
	claim.Validations = []*Validation{{
		Description: "response received",
		Type:        ValidationTypeReceipt,
		Required:    true,
	}}
	if err := b.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{claim}); err != nil {
		t.Fatal(err)
	}

	err := b.SubmitTestaments(ctx, Action{AgentID: "system", Type: ActionTypeTestament}, []Testament{{
		AgentID: "system",
		Summary: "consult deadline elapsed",
		Relations: []Relation{{
			Related:      "c1",
			RelatedType:  RelatedTypeClaim,
			Relationship: RelationshipClaim,
		}},
		Artifacts: []*Artifact{{
			Kind:      ArtifactKindToolTimeout,
			Reference: "consult deadline elapsed",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := b.CloneClaim("c1")
	if !ok {
		t.Fatal("claim not found")
	}
	if got.Status != ClaimStatusTestified {
		t.Fatalf("claim status = %s, want %s", got.Status, ClaimStatusTestified)
	}
	if got.LifecycleStatus != ClaimLifecycleTestamentGenerated {
		t.Fatalf("claim lifecycle = %s, want %s", got.LifecycleStatus, ClaimLifecycleTestamentGenerated)
	}
	if len(got.Validations) != 1 || got.Validations[0].Status != ValidationStatusPending {
		t.Fatalf("receipt validation should await evaluator acknowledgment: %+v", got.Validations)
	}
}

func TestEvaluateValidationAutoAccept(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	c := testClaimWithValidations("c1", "claim")
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})

	testament := Testament{
		AgentID: "engineer-b", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}
	b.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{testament})
	b.TransitionToValidation(ctx)

	// Get validation IDs from the claim.
	claim, _ := b.ClaimByID("c1")
	if len(claim.Validations) != 2 {
		t.Fatalf("expected 2 validations, got %d", len(claim.Validations))
	}

	// Pass first — should NOT accept yet.
	b.EvaluateValidation(ctx, "c1", claim.Validations[0].ID, StatusChange{
		To: string(ValidationStatusPassed), AgentID: "inspector-a",
	})
	claim, _ = b.ClaimByID("c1")
	if claim.Status == ClaimStatusAccepted {
		t.Error("claim should not be accepted with only 1 of 2 passed")
	}

	// Pass second — should auto-accept.
	b.EvaluateValidation(ctx, "c1", claim.Validations[1].ID, StatusChange{
		To: string(ValidationStatusPassed), AgentID: "inspector-a",
	})
	claim, _ = b.ClaimByID("c1")
	if claim.Status != ClaimStatusAccepted {
		t.Errorf("claim should be accepted, got %s", claim.Status)
	}
}

func TestRejectClaimWithReplacements(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	c := testClaimWithValidations("c1", "original")
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})

	testament := Testament{
		AgentID: "engineer-b", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}
	b.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{testament})
	b.TransitionToValidation(ctx)

	replacementAction := &Action{AgentID: "inspector-a", Type: ActionTypeCorrective}
	replacementClaims := []Claim{
		{ID: "c2", Title: "fix the issue", Relations: []Relation{
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		}},
	}

	err := b.RejectClaim(ctx, "c1", StatusChange{
		Reason: "validation failed", AgentID: "inspector-a",
	}, replacementAction, replacementClaims)
	if err != nil {
		t.Fatal(err)
	}

	c1, _ := b.ClaimByID("c1")
	if c1.Status != ClaimStatusRejected {
		t.Errorf("original: %s, want rejected", c1.Status)
	}

	c2, ok := b.ClaimByID("c2")
	if !ok {
		t.Fatal("replacement claim c2 not found")
	}
	if c2.Status != ClaimStatusPending {
		t.Errorf("replacement: %s, want pending", c2.Status)
	}
	if c2.Iteration != 1 {
		t.Errorf("replacement iteration: %d, want 1", c2.Iteration)
	}
	if !HasRelation(c2.Relations, RelationshipSupersedes, "c1") {
		t.Error("replacement should have supersedes relation to c1")
	}
}

func TestPhaseTransitions(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	c1 := testClaimWithValidations("c1", "claim1")
	c2 := testClaimWithValidations("c2", "claim2")
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c1, c2})

	// Can't transition yet — claims are pending.
	if err := b.TransitionToValidation(ctx); err == nil {
		t.Error("should not transition with pending claims")
	}

	// Submit testaments for both.
	for _, id := range []string{"c1", "c2"} {
		t1 := Testament{
			AgentID: "engineer-b", Summary: "done " + id,
			Relations: []Relation{{Related: id, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		}
		b.SubmitTestaments(ctx, Action{AgentID: "engineer-b"}, []Testament{t1})
	}

	if !b.ReadyForValidation() {
		t.Error("should be ready for validation")
	}

	if err := b.TransitionToValidation(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Phase() != BoardPhaseValidation {
		t.Errorf("phase: %s, want validation", b.Phase())
	}

	// Accept all validations.
	for _, id := range []string{"c1", "c2"} {
		claim := b.claims[id]
		for _, v := range claim.Validations {
			b.EvaluateValidation(ctx, id, v.ID, StatusChange{To: string(ValidationStatusPassed), AgentID: "inspector-a"})
		}
	}

	if !b.AllAccepted() {
		t.Error("should be all accepted")
	}

	if err := b.MarkComplete(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Phase() != BoardPhaseComplete {
		t.Errorf("phase: %s, want complete", b.Phase())
	}
}

func TestMaxIterationsBound(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{
		BoardID: "board-1", TaskID: "task-1", MaxIterations: 1,
	})
	ctx := context.Background()

	// Iteration 0: post, testify, validate, reject + replacement.
	c := testClaimWithValidations("c1", "claim")
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{c})
	b.SubmitTestaments(ctx, Action{AgentID: "e"}, []Testament{{
		AgentID: "e", Summary: "done",
		Relations: []Relation{{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}})
	b.TransitionToValidation(ctx)
	b.RejectClaim(ctx, "c1", StatusChange{Reason: "bad", AgentID: "i"},
		&Action{AgentID: "i", Type: ActionTypeCorrective},
		[]Claim{{ID: "c2", Title: "fix"}},
	)

	// Iteration 1: re-enter implementation.
	if err := b.TransitionToImplementation(ctx); err != nil {
		t.Fatal(err)
	}

	// Now testify + validate + reject again.
	b.SubmitTestaments(ctx, Action{AgentID: "e"}, []Testament{{
		AgentID: "e", Summary: "done again",
		Relations: []Relation{{Related: "c2", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
	}})
	b.TransitionToValidation(ctx)
	b.RejectClaim(ctx, "c2", StatusChange{Reason: "still bad", AgentID: "i"},
		&Action{AgentID: "i", Type: ActionTypeCorrective},
		[]Claim{{ID: "c3", Title: "fix again"}},
	)

	// Should fail: max iterations reached.
	err := b.TransitionToImplementation(ctx)
	if err == nil {
		t.Error("should fail: max iterations reached")
	}
}

func TestSubscription(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	var count int
	unsub := b.SubscribeProjection(func(p *ClaimsBoardProjection) error {
		count++
		return nil
	})

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})
	if count != 1 {
		t.Errorf("subscriber called %d times, want 1", count)
	}

	b.UpdateClaimProgress(ctx, "c1", ClaimProgressUpdate{WorkSummary: "started"}, "engineer-b")
	if count != 2 {
		t.Errorf("subscriber called %d times, want 2", count)
	}

	unsub()
	b.UpdateClaimProgress(ctx, "c1", ClaimProgressUpdate{WorkSummary: "more"}, "engineer-b")
	if count != 2 {
		t.Errorf("subscriber called %d times after unsub, want still 2", count)
	}
}

func TestSubscriberErrorLogged(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	var secondCalled bool
	b.SubscribeProjection(func(p *ClaimsBoardProjection) error {
		return fmt.Errorf("intentional test error")
	})
	b.SubscribeProjection(func(p *ClaimsBoardProjection) error {
		secondCalled = true
		return nil
	})

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})

	if !secondCalled {
		t.Error("second subscriber should still be called despite first returning error")
	}
}

func TestSubscriberCanReadBoard(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	var projFromSub *ClaimsBoardProjection
	b.SubscribeProjection(func(p *ClaimsBoardProjection) error {
		projFromSub = b.Projection()
		return nil
	})

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})

	if projFromSub == nil {
		t.Error("subscriber should have been able to read board (no deadlock)")
	}
	if projFromSub.TotalClaims != 1 {
		t.Errorf("projFromSub.TotalClaims: %d, want 1", projFromSub.TotalClaims)
	}
}

type testScope struct {
	mu      sync.Mutex
	spawned int
}

func (s *testScope) Go(desc string, _ time.Duration, fn func(context.Context) error) error {
	s.mu.Lock()
	s.spawned++
	s.mu.Unlock()
	// Run inline but track that it was dispatched through the scope.
	return fn(context.Background())
}

func TestSubscriberNotificationViaScope(t *testing.T) {
	scope := &testScope{}
	b := NewClaimsBoard(ClaimsBoardConfig{
		BoardID: "board-1", TaskID: "task-1", Scope: scope,
	})
	ctx := context.Background()

	var count int
	var mu sync.Mutex
	b.SubscribeProjection(func(p *ClaimsBoardProjection) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})

	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")})

	if count != 1 {
		t.Errorf("subscriber called %d times, want 1", count)
	}
	if scope.spawned != 1 {
		t.Errorf("scope.Go called %d times, want 1 (subscriber dispatched through scope)", scope.spawned)
	}
}

func TestConcurrentAccess(t *testing.T) {
	b := testBoard()
	ctx := context.Background()

	var claims []Claim
	for i := 0; i < 20; i++ {
		claims = append(claims, testClaim(fmt.Sprintf("c%d", i), fmt.Sprintf("claim %d", i)))
	}
	b.PostAction(ctx, Action{AgentID: "a", Type: ActionTypeTask}, claims)

	var wg sync.WaitGroup
	for _, id := range []string{"c0", "c1", "c2"} {
		wg.Add(1)
		go func(cid string) {
			defer wg.Done()
			for range 10 {
				b.UpdateClaimProgress(ctx, cid, ClaimProgressUpdate{WorkSummary: "work"}, "engineer-b")
			}
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 30 {
			_ = b.Projection()
		}
	}()
	wg.Wait()

	p := b.Projection()
	if p.TotalClaims != 20 {
		t.Errorf("TotalClaims: %d, want 20", p.TotalClaims)
	}
}
