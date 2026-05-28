package claims

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
)

const (
	benchmarkLifecycleBoardResetInterval = 1024
	benchmarkLifecycleIntegrationBatch   = 1000
)

func BenchmarkClaimLifecycleGeneratePostReceiveValidate(b *testing.B) {
	ctx := context.Background()
	board := newBenchmarkLifecycleBoard(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%benchmarkLifecycleBoardResetInterval == 0 {
			board = newBenchmarkLifecycleBoard(nil)
		}
		if err := runBenchmarkLifecycleRoundTrip(ctx, board, strconv.Itoa(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLifecycleOperationsWithMockBus(b *testing.B) {
	ctx := context.Background()
	bus := &benchmarkDeltaBus{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		board := newBenchmarkLifecycleBoard(bus)
		for op := 0; op < benchmarkLifecycleIntegrationBatch; op++ {
			idSuffix := strconv.Itoa(i) + "-" + strconv.Itoa(op)
			if err := runBenchmarkLifecycleRoundTrip(ctx, board, idSuffix); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func runBenchmarkLifecycleRoundTrip(ctx context.Context, board *ClaimsBoard, idSuffix string) error {
	claimID := "bench-claim-" + idSuffix
	testamentID := "bench-testament-" + idSuffix
	actionID := "bench-action-" + idSuffix
	testamentActionID := "bench-testament-action-" + idSuffix
	claim := Claim{
		ID:          claimID,
		Title:       "Benchmark claim " + idSuffix,
		Description: "Benchmark lifecycle claim",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          claimID + "-receipt",
			Description: "Receive benchmark response",
			QualityBar:  "response.received",
			Type:        ValidationTypeReceipt,
			Required:    true,
		}},
	}
	if _, err := board.GenerateClaimAction(ctx, Action{ID: actionID, AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: actionID}); err != nil {
		return err
	}
	if err := board.PostGeneratedClaim(ctx, claimID, "architect", ClaimPostOptions{}); err != nil {
		return err
	}
	if err := board.AcknowledgeClaimReceipt(ctx, claimID, "engineer-b"); err != nil {
		return err
	}
	if err := board.UpdateClaimProgress(ctx, claimID, ClaimProgressUpdate{WorkSummary: "benchmark work started"}, "engineer-b"); err != nil {
		return err
	}
	testament := Testament{
		ID:      testamentID,
		AgentID: "engineer-b",
		Summary: "Benchmark response complete",
		Relations: []Relation{
			{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{{
			ID:        testamentID + "-artifact",
			AgentID:   "engineer-b",
			Kind:      ArtifactKindResponseText,
			Reference: "benchmark response",
		}},
	}
	if _, err := board.GenerateTestamentAction(ctx, Action{ID: testamentActionID, AgentID: "engineer-b", Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{IdempotencyKey: testamentActionID}); err != nil {
		return err
	}
	if err := board.PostGeneratedTestament(ctx, testamentID, "engineer-b", TestamentPostOptions{}); err != nil {
		return err
	}
	if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, "architect"); err != nil {
		return err
	}
	if err := board.BeginTestamentValidation(ctx, testamentID, "architect"); err != nil {
		return err
	}
	if err := board.EvaluateValidation(ctx, claimID, claimID+"-receipt", StatusChange{AgentID: "architect", To: string(ValidationStatusPassed), Reason: "benchmark validation passed"}); err != nil {
		return err
	}
	return board.CompleteTestamentValidation(ctx, testamentID, "architect", TestamentLifecycleValidated, "benchmark validated")
}

func newBenchmarkLifecycleBoard(bus DeltaBus) *ClaimsBoard {
	return NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "benchmark-board",
		SessionID: "benchmark-session",
		TaskID:    "benchmark-task",
		DeltaBus:  bus,
	})
}

type benchmarkDeltaBus struct {
	published atomic.Uint64
}

func (b *benchmarkDeltaBus) PublishDelta(_ context.Context, _ string, _ Delta) error {
	b.published.Add(1)
	return nil
}

func (b *benchmarkDeltaBus) SubscribeDelta(pattern string, _ DeltaHandler) (DeltaSubscription, error) {
	return noopSubscription{topic: pattern}, nil
}
