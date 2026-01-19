package coldstart

import (
	"math"
	"testing"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.5.1 Tests - ActionTypePrior Type
// =============================================================================

func TestDefaultActionTypePrior(t *testing.T) {
	prior := DefaultActionTypePrior()

	if prior == nil {
		t.Fatal("expected non-nil prior")
	}

	if prior.TypeImportance == nil {
		t.Fatal("expected non-nil TypeImportance map")
	}
}

func TestActionTypePriorAllTypesHaveValues(t *testing.T) {
	prior := DefaultActionTypePrior()

	// Test all defined event types have importance values
	testCases := []struct {
		eventType events.EventType
		expected  float64
	}{
		{events.EventTypeAgentDecision, 0.90},
		{events.EventTypeFailure, 0.85},
		{events.EventTypeSuccess, 0.75},
		{events.EventTypeAgentAction, 0.70},
		{events.EventTypeToolResult, 0.60},
		{events.EventTypeUserPrompt, 0.80},
		{events.EventTypeToolCall, 0.50},
		{events.EventTypeIndexComplete, 0.40},
	}

	for _, tc := range testCases {
		t.Run(tc.eventType.String(), func(t *testing.T) {
			score := prior.TypeImportance[tc.eventType]
			if score != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, score)
			}
		})
	}
}

// =============================================================================
// AE.5.2 Tests - ComputeColdScore Method
// =============================================================================

func TestComputeColdScoreWeightsSumToOne(t *testing.T) {
	total := ActionWeight + RecencyWeight + SemanticWeight
	if math.Abs(total-1.0) > 0.001 {
		t.Errorf("weights should sum to 1.0, got %v", total)
	}
}

func TestComputeColdScoreInRange(t *testing.T) {
	prior := DefaultActionTypePrior()

	// Create test embeddings
	queryEmbed := []float32{1.0, 0.0, 0.0}
	eventEmbed := []float32{1.0, 0.0, 0.0} // Perfect match

	score := prior.ComputeColdScore(
		events.EventTypeAgentDecision,
		queryEmbed,
		eventEmbed,
		10, // current turn
		5,  // event turn
	)

	if score < 0.0 || score > 1.0 {
		t.Errorf("score should be in [0,1], got %v", score)
	}
}

func TestComputeColdScoreComponents(t *testing.T) {
	prior := DefaultActionTypePrior()

	queryEmbed := []float32{1.0, 0.0, 0.0}
	eventEmbed := []float32{0.0, 1.0, 0.0} // Orthogonal vectors

	score := prior.ComputeColdScore(
		events.EventTypeAgentDecision,
		queryEmbed,
		eventEmbed,
		10,
		10, // Same turn = max recency
	)

	// With perfect recency, high action score, but zero semantic score
	// Expected: 0.40 * 0.90 + 0.25 * 1.0 + 0.35 * 0.0 = 0.61
	expected := 0.61
	if math.Abs(score-expected) > 0.01 {
		t.Errorf("expected score ~%v, got %v", expected, score)
	}
}

func TestGetActionScoreUnknownType(t *testing.T) {
	prior := DefaultActionTypePrior()

	// Use a very high enum value unlikely to be defined
	unknownType := events.EventType(9999)
	score := prior.getActionScore(unknownType)

	if score != 0.30 {
		t.Errorf("expected default score 0.30, got %v", score)
	}
}

func TestCalculateSemanticPerfectMatch(t *testing.T) {
	prior := DefaultActionTypePrior()

	embed := []float32{1.0, 0.0, 0.0}
	score := prior.calculateSemantic(embed, embed)

	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("expected similarity ~1.0, got %v", score)
	}
}

func TestCalculateSemanticOrthogonal(t *testing.T) {
	prior := DefaultActionTypePrior()

	embed1 := []float32{1.0, 0.0, 0.0}
	embed2 := []float32{0.0, 1.0, 0.0}
	score := prior.calculateSemantic(embed1, embed2)

	if math.Abs(score-0.0) > 0.001 {
		t.Errorf("expected similarity ~0.0, got %v", score)
	}
}

// =============================================================================
// AE.5.3 Tests - SessionRecencyCalculator
// =============================================================================

func TestCalculateRecencySameTurn(t *testing.T) {
	calc := SessionRecencyCalculator{}
	score := calc.CalculateRecency(10, 10)

	// turnsSince = 0, score = 1 / (1 + 0) = 1.0
	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("expected 1.0, got %v", score)
	}
}

func TestCalculateRecencyRecentEvent(t *testing.T) {
	calc := SessionRecencyCalculator{}
	score := calc.CalculateRecency(15, 10)

	// turnsSince = 5, score = 1 / (1 + 5/10) = 1/1.5 = 0.667
	expected := 1.0 / 1.5
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("expected %v, got %v", expected, score)
	}
}

func TestCalculateRecencyOldEvent(t *testing.T) {
	calc := SessionRecencyCalculator{}
	score := calc.CalculateRecency(100, 10)

	// turnsSince = 90, score = 1 / (1 + 90/10) = 1/10 = 0.1
	expected := 1.0 / 10.0
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("expected %v, got %v", expected, score)
	}
}

func TestCalculateRecencyGentleDecay(t *testing.T) {
	calc := SessionRecencyCalculator{}

	// Test that decay is gentle - scores don't drop too quickly
	score1 := calc.CalculateRecency(10, 10) // 0 turns
	score2 := calc.CalculateRecency(20, 10) // 10 turns
	score3 := calc.CalculateRecency(30, 10) // 20 turns

	// Verify scores are decreasing
	if score2 >= score1 {
		t.Errorf("expected score2 < score1")
	}
	if score3 >= score2 {
		t.Errorf("expected score3 < score2")
	}

	// Verify decay is gentle (score doesn't drop below 0.1 quickly)
	if score2 < 0.4 {
		t.Errorf("10 turns should still have reasonable score, got %v", score2)
	}
}

func TestCalculateRecencyFutureTurn(t *testing.T) {
	calc := SessionRecencyCalculator{}

	// If event is somehow in the future, should treat as current
	score := calc.CalculateRecency(10, 15)

	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("future event should have score 1.0, got %v", score)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestFullColdStartScoring(t *testing.T) {
	prior := DefaultActionTypePrior()

	testCases := []struct {
		name        string
		eventType   events.EventType
		queryEmbed  []float32
		eventEmbed  []float32
		currentTurn int
		eventTurn   int
		expectHigh  bool
	}{
		{
			name:        "high importance recent event with good match",
			eventType:   events.EventTypeAgentDecision,
			queryEmbed:  []float32{1.0, 0.0, 0.0},
			eventEmbed:  []float32{1.0, 0.0, 0.0},
			currentTurn: 10,
			eventTurn:   10,
			expectHigh:  true,
		},
		{
			name:        "low importance old event with poor match",
			eventType:   events.EventTypeIndexComplete,
			queryEmbed:  []float32{1.0, 0.0, 0.0},
			eventEmbed:  []float32{0.0, 1.0, 0.0},
			currentTurn: 100,
			eventTurn:   10,
			expectHigh:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			score := prior.ComputeColdScore(
				tc.eventType,
				tc.queryEmbed,
				tc.eventEmbed,
				tc.currentTurn,
				tc.eventTurn,
			)

			if tc.expectHigh && score < 0.7 {
				t.Errorf("expected high score, got %v", score)
			}
			if !tc.expectHigh && score > 0.5 {
				t.Errorf("expected low score, got %v", score)
			}
		})
	}
}
