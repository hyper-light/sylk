package claims_test

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestClaimsMetricsSinkMockeryIntegration(t *testing.T) {
	sink := &claimsmocks.ClaimsMetricsSink{}
	seenTransition := false
	sink.On("ObserveClaimsMetric", mock.Anything, mock.MatchedBy(func(metric claims.ClaimsMetric) bool {
		if metric.Name == "claims_board_transitions_total" && metric.Kind == claims.ClaimsMetricCounter {
			seenTransition = true
		}
		return true
	})).Return(nil)

	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "mockery-metrics-board", SessionID: "mockery-metrics-session", TaskID: "task", Metrics: sink})
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:         "claim-1",
		Title:      "mockery metrics",
		ActionType: claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "worker", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	if !seenTransition {
		t.Fatal("metrics sink did not observe claims_board_transitions_total")
	}
	sink.AssertExpectations(t)
}
