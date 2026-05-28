package shared

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

func canonicalTestamentPostedDeltaForClaimsIntakeTest(sessionID, boardID, claimID, testamentID string, action claims.ActionType, responderID, receiverID, context string) claims.CanonicalDelta {
	return claims.NewCanonicalDelta(
		claims.DeltaActionTestamentPosted,
		sessionID,
		boardID,
		2,
		time.Now(),
		claims.DegradedAgentRef(responderID, "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID},
			{Role: "testament", Type: claims.RelatedTypeTestament, ID: testamentID},
		},
		&claims.DeltaDelivery{
			To:           []claims.AgentRef{claims.DegradedAgentRef(receiverID, "test")},
			Relationship: claims.RelationshipIssuer,
		},
		map[string]any{
			"claim": map[string]any{
				"id":               claimID,
				"action":           string(action),
				"status":           string(claims.ClaimStatusTestified),
				"lifecycle_status": string(claims.ClaimLifecycleTestamentGenerated),
			},
			"testaments": []map[string]any{{
				"id":      testamentID,
				"verdict": claims.TestamentVerdictWorkComplete,
				"context": context,
			}},
		},
	)
}

func TestClaimsIntakeAcknowledgesClaimPostedBeforeProcessing(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-receipt",
		SessionID: "sess-intake-receipt",
		TaskID:    "task-intake-receipt",
	})
	if err := board.PostAction(context.Background(), claims.Action{Type: claims.ActionTypeTask, AgentID: "architect"}, []claims.Claim{{
		Title:       "Do work",
		Description: "Perform the work",
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	claimID := board.Projection().Claims[0].ID
	entry := &claims.GraphEntryPoint{Delta: claims.NewCanonicalDelta(
		claims.DeltaActionClaimPosted,
		"sess-intake-receipt",
		board.BoardID(),
		2,
		time.Now(),
		claims.DegradedAgentRef("architect", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		&claims.DeltaDelivery{To: []claims.AgentRef{claims.DegradedAgentRef("librarian", "test")}, Relationship: claims.RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(claims.ActionTypeTask), "lifecycle_status": string(claims.ClaimLifecyclePosted)}},
	)}
	if !acknowledgeLifecycleReceipt(ClaimsIntakeConfig{AgentID: "librarian", SessionID: "sess-intake-receipt", Board: board}, claims.RoleSubject, entry) {
		t.Fatal("receipt acknowledgement rejected valid receiver")
	}
	stored, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %q not found", claimID)
	}
	if stored.LifecycleStatus != claims.ClaimLifecycleReceived {
		t.Fatalf("lifecycle = %q, want %q", stored.LifecycleStatus, claims.ClaimLifecycleReceived)
	}
}

func TestClaimsIntakeReceiptFailureStopsProcessing(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-receipt-failed",
		SessionID: "sess-intake-receipt-failed",
		TaskID:    "task-intake-receipt-failed",
	})
	if err := board.PostAction(context.Background(), claims.Action{Type: claims.ActionTypeTask, AgentID: "architect"}, []claims.Claim{{
		Title:       "Do work",
		Description: "Perform the work",
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	claimID := board.Projection().Claims[0].ID
	entry := &claims.GraphEntryPoint{Delta: claims.NewCanonicalDelta(
		claims.DeltaActionClaimPosted,
		"sess-intake-receipt-failed",
		board.BoardID(),
		2,
		time.Now(),
		claims.DegradedAgentRef("architect", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		&claims.DeltaDelivery{To: []claims.AgentRef{claims.DegradedAgentRef("librarian", "test")}, Relationship: claims.RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(claims.ActionTypeTask), "lifecycle_status": string(claims.ClaimLifecyclePosted)}},
	)}
	if acknowledgeLifecycleReceipt(ClaimsIntakeConfig{AgentID: "architect", SessionID: "sess-intake-receipt-failed", Board: board}, claims.RoleSubject, entry) {
		t.Fatal("receipt acknowledgement accepted mismatched receiver")
	}
	stored, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %q not found", claimID)
	}
	if stored.LifecycleStatus != claims.ClaimLifecycleReceiptFailed {
		t.Fatalf("lifecycle = %q, want %q", stored.LifecycleStatus, claims.ClaimLifecycleReceiptFailed)
	}
}

func TestClaimsIntakeExpectedConsultTestamentDeliversContinuation(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-consult",
		SessionID: "sess-intake-consult",
		TaskID:    "task-intake-consult",
	})
	var resumed map[string]*AwaitedClaimResult
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-consult",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed = results
			return nil
		},
	})
	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ConsultIDs:      []string{"consult-123"},
		AwaitToolCallID: "tool-call-1",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(time.Hour),
		Snapshot: &TurnSnapshot{
			Request: &providers.Request{},
		},
	})
	if !yielded || !IsConsultYielded(err) {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	entry := &claims.GraphEntryPoint{
		Delta: canonicalTestamentPostedDeltaForClaimsIntakeTest(
			"sess-intake-consult",
			"board-intake-consult",
			"claim-1",
			"testament-1",
			claims.ActionTypeConsultation,
			"librarian",
			"architect-1",
			"The repository is currently a Go project.",
		),
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "claim-1",
				Title:      "Consult librarian",
				ActionType: claims.ActionTypeConsultation,
				Scope: []claims.ClaimScopeEntry{
					{Kind: "consultation", Key: "librarian"},
					{Kind: "consult_id", Key: "consult-123"},
				},
			},
			Testament: &claims.Testament{
				ID:      "testament-1",
				AgentID: "librarian",
				Summary: "The repository is currently a Go project.",
				Artifacts: []*claims.Artifact{{
					ID:        "artifact-1",
					Kind:      "workspace_state",
					Reference: "No Python package metadata exists.",
				}},
			},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "claim-1",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	}
	if !deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-consult",
		ContinuationStore: store,
	}, entry) {
		t.Fatal("expected peer testament to be consumed as a continuation resolution")
	}
	if resumed == nil {
		t.Fatal("resume was not invoked")
	}
	got := resumed["consult-123"]
	if got == nil {
		t.Fatalf("results = %#v, want consult-123", resumed)
	}
	if got.Status != claims.ConsultStatusCompleted {
		t.Fatalf("status = %q", got.Status)
	}
	if got.ResponderAgentID != "librarian" {
		t.Fatalf("responder = %q", got.ResponderAgentID)
	}
	if got.ResponseSummary != "The repository is currently a Go project." {
		t.Fatalf("summary = %q", got.ResponseSummary)
	}
	if got.DeltaKey == "" || got.DeltaKey != entry.Delta.DeltaKey() {
		t.Fatalf("delta key = %q, want %q", got.DeltaKey, entry.Delta.DeltaKey())
	}
	var payload map[string]any
	if err := json.Unmarshal(got.ResponsePayload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["response"] != "The repository is currently a Go project." {
		t.Fatalf("payload.response = %#v", payload["response"])
	}
}

func TestClaimsIntakeLegacyConsultTestamentDoesNotDeliverContinuation(t *testing.T) {
	var resumed map[string]*AwaitedClaimResult
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-legacy-consult",
		Board: claims.NewClaimsBoard(claims.ClaimsBoardConfig{
			BoardID:   "board-intake-legacy-consult",
			SessionID: "sess-intake-legacy-consult",
		}),
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed = results
			return nil
		},
	})
	entry := &claims.GraphEntryPoint{
		Delta: claims.TestamentDelta{
			SessionID:      "sess-intake-legacy-consult",
			BoardID:        "board-intake-legacy-consult",
			ClaimID:        "claim-1",
			TestamentID:    "testament-1",
			ActionKind:     claims.ActionTypeConsultation,
			Verdict:        claims.TestamentVerdictWorkComplete,
			SubjectAgentID: "librarian",
			Summary:        "legacy path should not resume",
		},
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "claim-1",
				ActionType: claims.ActionTypeConsultation,
				Scope: []claims.ClaimScopeEntry{
					{Kind: "consult_id", Key: "consult-123"},
				},
			},
			Testament: &claims.Testament{ID: "testament-1", AgentID: "librarian"},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "claim-1",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	}
	if deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-legacy-consult",
		ContinuationStore: store,
	}, entry) {
		t.Fatal("legacy testament delta must not resolve lifecycle continuations")
	}
	if resumed != nil {
		t.Fatalf("resume invoked with %#v", resumed)
	}
}

func TestClaimsIntakeExpectedCanonicalConsultTestamentDeliversContinuation(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-canonical-consult",
		SessionID: "sess-intake-canonical-consult",
		TaskID:    "task-intake-canonical-consult",
	})
	var resumed map[string]*AwaitedClaimResult
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-canonical-consult",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed = results
			return nil
		},
	})
	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ConsultIDs:      []string{"consult-123"},
		AwaitToolCallID: "tool-call-1",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(time.Hour),
		Snapshot:        &TurnSnapshot{Request: &providers.Request{}},
	})
	if !yielded || !IsConsultYielded(err) {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	delta := claims.NewCanonicalDelta(
		claims.DeltaActionTestamentPosted,
		"sess-intake-canonical-consult",
		"board-intake-canonical-consult",
		2,
		time.Now(),
		claims.DegradedAgentRef("librarian", "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: "consult-123"},
			{Role: "testament", Type: claims.RelatedTypeTestament, ID: "testament-1"},
		},
		&claims.DeltaDelivery{To: []claims.AgentRef{claims.DegradedAgentRef("architect-1", "test")}, Relationship: claims.RelationshipIssuer},
		map[string]any{
			"claim": map[string]any{
				"id":     "consult-123",
				"action": string(claims.ActionTypeConsultation),
				"status": string(claims.ClaimStatusTestified),
			},
			"testaments": []map[string]any{{
				"id":      "testament-1",
				"verdict": claims.TestamentVerdictWorkComplete,
				"context": "No Python package metadata exists.",
			}},
		},
	)
	entry := &claims.GraphEntryPoint{
		Delta: delta,
		Expectation: &claims.Expectation{
			ClaimID:       "consult-123",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	}
	if !deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-canonical-consult",
		ContinuationStore: store,
	}, entry) {
		t.Fatal("expected canonical testament to be consumed as a continuation resolution")
	}
	got := resumed["consult-123"]
	if got == nil {
		t.Fatalf("results = %#v, want consult-123", resumed)
	}
	if got.Status != claims.ConsultStatusCompleted {
		t.Fatalf("status = %q", got.Status)
	}
	if got.ResponderAgentID != "librarian" {
		t.Fatalf("responder = %q", got.ResponderAgentID)
	}
	if got.ResponseSummary != "No Python package metadata exists." {
		t.Fatalf("summary = %q", got.ResponseSummary)
	}
}

func TestClaimsIntakeExpectedTerminalClaimDeliversContinuation(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-terminal-consult",
		SessionID: "sess-intake-terminal-consult",
		TaskID:    "task-intake-terminal-consult",
	})
	var resumed map[string]*AwaitedClaimResult
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-terminal-consult",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed = results
			return nil
		},
	})
	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ClaimRefs:       []string{"consult-terminal"},
		AwaitToolCallID: "tool-call-1",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(time.Hour),
		Snapshot:        &TurnSnapshot{Request: &providers.Request{}},
	})
	if !yielded || !IsConsultYielded(err) {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	delta := claims.NewCanonicalDelta(
		claims.DeltaActionClaimSatisfied,
		"sess-intake-terminal-consult",
		"board-intake-terminal-consult",
		3,
		time.Now(),
		claims.DegradedAgentRef("librarian", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "consult-terminal"}},
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":               "consult-terminal",
				"action":           string(claims.ActionTypeConsultation),
				"status":           string(claims.ClaimStatusAccepted),
				"to_status":        string(claims.ClaimStatusAccepted),
				"lifecycle_status": string(claims.ClaimLifecycleSatisfied),
				"context":          "receipt accepted",
			},
		},
	)
	entry := &claims.GraphEntryPoint{
		Delta: delta,
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:          "consult-terminal",
				Title:       "Consult librarian",
				Description: "Consult accepted",
				ActionType:  claims.ActionTypeConsultation,
			},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "consult-terminal",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	}
	if !deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-terminal-consult",
		ContinuationStore: store,
	}, entry) {
		t.Fatal("expected terminal claim transition to resolve the continuation")
	}
	got := resumed["consult-terminal"]
	if got == nil {
		t.Fatalf("results = %#v, want consult-terminal", resumed)
	}
	if got.Action != claims.DeltaActionClaimSatisfied || got.Status != claims.ConsultStatusCompleted {
		t.Fatalf("result = %#v", got)
	}
	if got.ResponseSummary != "receipt accepted" {
		t.Fatalf("summary = %q", got.ResponseSummary)
	}
}

func TestClaimsIntakeClaimValidationErroredDeliversContinuationButTestamentErroredDoesNot(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-validation-errored",
		SessionID: "sess-intake-validation-errored",
		TaskID:    "task-intake-validation-errored",
	})
	var resumed map[string]*AwaitedClaimResult
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-validation-errored",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed = results
			return nil
		},
	})
	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ClaimRefs:       []string{"consult-errored"},
		AwaitToolCallID: "tool-call-errored",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(time.Hour),
		Snapshot:        &TurnSnapshot{Request: &providers.Request{}},
	})
	if !yielded || !IsConsultYielded(err) {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	testamentOnly := claims.NewCanonicalDelta(
		claims.DeltaActionTestamentValidationErrored,
		"sess-intake-validation-errored",
		"board-intake-validation-errored",
		4,
		time.Now(),
		claims.DegradedAgentRef("architect-1", "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: "consult-errored"},
			{Role: "testament", Type: claims.RelatedTypeTestament, ID: "testament-errored"},
		},
		nil,
		map[string]any{
			"claim":     map[string]any{"id": "consult-errored", "action": string(claims.ActionTypeConsultation), "status": string(claims.ClaimStatusRejected), "lifecycle_status": string(claims.ClaimLifecycleValidationErrored)},
			"testament": map[string]any{"id": "testament-errored", "lifecycle_status": string(claims.TestamentLifecycleValidationErrored)},
		},
	)
	if deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-validation-errored",
		ContinuationStore: store,
	}, &claims.GraphEntryPoint{
		Delta: testamentOnly,
		Node: claims.GraphNode{Claim: &claims.Claim{
			ID:         "consult-errored",
			ActionType: claims.ActionTypeConsultation,
		}},
		Expectation: &claims.Expectation{ClaimID: "consult-errored", ExpectedDelta: claims.DeltaKindTestament},
	}) {
		t.Fatal("testament.validation_errored alone must not resolve a claim-level continuation")
	}
	if resumed != nil {
		t.Fatalf("resume invoked from testament-only error: %#v", resumed)
	}

	claimErrored := claims.NewCanonicalDelta(
		claims.DeltaActionClaimValidationErrored,
		"sess-intake-validation-errored",
		"board-intake-validation-errored",
		5,
		time.Now(),
		claims.DegradedAgentRef("architect-1", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "consult-errored"}},
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":               "consult-errored",
				"action":           string(claims.ActionTypeConsultation),
				"status":           string(claims.ClaimStatusRejected),
				"to_status":        string(claims.ClaimStatusRejected),
				"lifecycle_status": string(claims.ClaimLifecycleValidationErrored),
				"context":          "validator backend unavailable",
			},
		},
	)
	if !deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-validation-errored",
		ContinuationStore: store,
	}, &claims.GraphEntryPoint{
		Delta: claimErrored,
		Node: claims.GraphNode{Claim: &claims.Claim{
			ID:          "consult-errored",
			Title:       "Consult librarian",
			Description: "validation errored",
			ActionType:  claims.ActionTypeConsultation,
		}},
		Expectation: &claims.Expectation{ClaimID: "consult-errored", ExpectedDelta: claims.DeltaKindTestament},
	}) {
		t.Fatal("claim.validation_errored should resolve the claim-level continuation")
	}
	got := resumed["consult-errored"]
	if got == nil {
		t.Fatalf("results = %#v, want consult-errored", resumed)
	}
	if got.Action != claims.DeltaActionClaimValidationErrored || got.Status != claims.ConsultStatusError {
		t.Fatalf("result = %#v", got)
	}
	if got.DeltaKey != claimErrored.DeltaKey() {
		t.Fatalf("delta key = %q, want %q", got.DeltaKey, claimErrored.DeltaKey())
	}
}

func TestClaimsIntakeExpectedConsultTestamentWithoutResolutionIDIsConsumed(t *testing.T) {
	consumed := deliverExpectedPeerResultToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-consult-missing-id",
		ContinuationStore: &ContinuationStore{},
	}, &claims.GraphEntryPoint{
		Delta: canonicalTestamentPostedDeltaForClaimsIntakeTest(
			"sess-intake-consult-missing-id",
			"board-intake-consult-missing-id",
			"claim-1",
			"testament-1",
			claims.ActionTypeConsultation,
			"librarian",
			"architect-1",
			"done",
		),
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "",
				ActionType: claims.ActionTypeConsultation,
			},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	})
	if !consumed {
		t.Fatal("expected missing-id peer testament to be consumed rather than dispatched as fresh inference")
	}
}

func TestClaimsIntakeSuppressesGuideUserPromptForActionAgents(t *testing.T) {
	entry := guideUserPromptEntry()
	if !shouldSuppressForwardedPromptEntry(claims.RoleSubject, entry) {
		t.Fatal("expected guide user prompt claim to be suppressed for action agent")
	}
	if !shouldSuppressForwardedPromptEntry(claims.RoleSubject|claims.RoleAuditor, entry) {
		t.Fatal("expected guide user prompt claim to be suppressed for auditor action agent")
	}
}

func TestClaimsIntakeDoesNotSuppressGuideUserPromptForObserver(t *testing.T) {
	if shouldSuppressForwardedPromptEntry(claims.RoleObserver, guideUserPromptEntry()) {
		t.Fatal("observer inbox must still receive guide user prompt claims for rendering")
	}
}

func TestClaimsIntakeDoesNotSuppressNonPromptClaims(t *testing.T) {
	entry := guideUserPromptEntry()
	entry.Node.Claim.ActionType = claims.ActionTypeConsultation
	if shouldSuppressForwardedPromptEntry(claims.RoleSubject, entry) {
		t.Fatal("consultation claim should not be suppressed")
	}
}

type intakeExpectedToolExecutor struct {
	calls []claims.ExpectedToolCall
	err   error
}

func (e *intakeExpectedToolExecutor) ExecuteExpectedTool(_ context.Context, call claims.ExpectedToolCall) (claims.ExpectedToolExecutionOutput, error) {
	e.calls = append(e.calls, call)
	if e.err != nil {
		return claims.ExpectedToolExecutionOutput{}, e.err
	}
	return claims.ExpectedToolExecutionOutput{Summary: "validation probe passed", Output: "ok"}, nil
}

func TestClaimsIntakeExecutesOwnedExpectedValidationToolsFromTestament(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "board-expected-tools", SessionID: "sess-expected-tools", TaskID: "task"})
	claim := claims.Claim{
		ID:         "claim-expected-tools",
		Title:      "validate with tool",
		ActionType: claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			Description: "probe evidence",
			Type:        claims.ValidationTypeInspection,
			Required:    true,
			ExpectedToolCalls: []claims.ExpectedToolCall{{
				ID:       "probe-1",
				Tool:     "workspace_read",
				Required: true,
			}},
		}},
	}
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{claim}); err != nil {
		t.Fatal(err)
	}
	testaments := []claims.Testament{{
		AgentID: "engineer",
		Summary: "done",
		Relations: []claims.Relation{{
			Related:      claim.ID,
			RelatedType:  claims.RelatedTypeClaim,
			Relationship: claims.RelationshipClaim,
		}},
		Artifacts: []*claims.Artifact{{Kind: "workspace_observation", Reference: "done"}},
	}}
	if err := board.SubmitTestaments(context.Background(), claims.Action{AgentID: "engineer", Type: claims.ActionTypeTestament}, testaments); err != nil {
		t.Fatal(err)
	}
	projection := board.Projection()
	testamentID := projection.Testaments[0].ID
	entry := claims.ResolveEntryPoint(board, canonicalTestamentPostedDeltaForClaimsIntakeTest(
		"sess-expected-tools",
		"board-expected-tools",
		claim.ID,
		testamentID,
		claims.ActionTypeTask,
		"engineer",
		"architect",
		"done",
	), claims.PriorityEvaluation, nil)
	exec := &intakeExpectedToolExecutor{}
	cfg := ClaimsIntakeConfig{
		AgentID:              "architect",
		SessionID:            "sess-expected-tools",
		Board:                board,
		ExpectedToolExecutor: exec,
		ExpectedToolAllowlist: map[string]bool{
			"workspace_read": true,
		},
	}
	if !acknowledgeLifecycleReceipt(cfg, claims.RoleAuditor, entry) {
		t.Fatal("expected testament receipt acknowledgement before validation tools")
	}
	received, ok := board.CloneTestament(testamentID)
	if !ok {
		t.Fatalf("testament %q not found", testamentID)
	}
	if received.LifecycleStatus != claims.TestamentLifecycleReceived {
		t.Fatalf("testament lifecycle before validation tools = %q, want received", received.LifecycleStatus)
	}
	if !dispatchExpectedValidationTools(cfg, entry) {
		t.Fatal("expected validation tools to dispatch")
	}
	if len(exec.calls) != 1 || exec.calls[0].Tool != "workspace_read" {
		t.Fatalf("executor calls = %+v", exec.calls)
	}
	got, _ := board.CloneClaim(claim.ID)
	if got.Validations[0].Status != claims.ValidationStatusPassed {
		t.Fatalf("validation status = %s, want passed", got.Validations[0].Status)
	}
}

func TestClaimsIntakeDoesNotExecuteIssuerOwnedExpectedValidationToolsForOtherAgents(t *testing.T) {
	entry := &claims.GraphEntryPoint{
		Delta: canonicalTestamentPostedDeltaForClaimsIntakeTest(
			"sess-expected-tools",
			"board-expected-tools",
			"claim-1",
			"testament-1",
			claims.ActionTypeTask,
			"engineer",
			"architect",
			"done",
		),
		Node: claims.GraphNode{Claim: &claims.Claim{
			ID:         "claim-1",
			ActionType: claims.ActionTypeTask,
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{
				ID:          "validation-1",
				Description: "probe evidence",
				Type:        claims.ValidationTypeInspection,
				Status:      claims.ValidationStatusPending,
				Required:    true,
				ExpectedToolCalls: []claims.ExpectedToolCall{{
					ID:       "probe-1",
					Tool:     "workspace_read",
					Required: true,
				}},
			}},
		}},
	}
	exec := &intakeExpectedToolExecutor{}
	if dispatchExpectedValidationTools(ClaimsIntakeConfig{
		AgentID:              "inspector",
		SessionID:            "sess-expected-tools",
		Board:                claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: "sess-expected-tools"}),
		ExpectedToolExecutor: exec,
	}, entry) {
		t.Fatal("non-owner should not dispatch issuer-owned expected validation tools")
	}
	if len(exec.calls) != 0 {
		t.Fatalf("executor calls = %+v", exec.calls)
	}
}

func guideUserPromptEntry() *claims.GraphEntryPoint {
	return &claims.GraphEntryPoint{
		Delta: claims.InboxDelta{
			AgentID: "architect",
			ClaimID: "claim-guide-prompt",
		},
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "claim-guide-prompt",
				ActionType: claims.ActionTypePrompt,
				Tags:       []string{"guide_classification", "user_prompt"},
				Relations: []claims.Relation{
					{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
					{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				},
			},
		},
	}
}
