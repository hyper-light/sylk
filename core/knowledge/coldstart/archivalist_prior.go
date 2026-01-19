package coldstart

import (
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// AE.5.1 ActionTypePrior Type
// =============================================================================

// ActionTypePrior stores importance scores for different event types
// and provides cold-start scoring for events before decay parameters
// are learned from user behavior.
type ActionTypePrior struct {
	// TypeImportance maps event types to their importance scores [0,1]
	TypeImportance map[events.EventType]float64
}

// Weight constants for computing cold-start scores.
// These weights sum to 1.0 for normalized scoring.
const (
	// ActionWeight is the weight for action-based importance
	ActionWeight = 0.40
	// RecencyWeight is the weight for temporal recency
	RecencyWeight = 0.25
	// SemanticWeight is the weight for semantic similarity
	SemanticWeight = 0.35
)

// DefaultActionTypePrior creates an ActionTypePrior with empirically-tuned
// importance values for each event type.
func DefaultActionTypePrior() *ActionTypePrior {
	return &ActionTypePrior{
		TypeImportance: map[events.EventType]float64{
			// High importance: Critical decision points and outcomes
			events.EventTypeAgentDecision: 0.90,
			events.EventTypeFailure:       0.85,
			events.EventTypeSuccess:       0.75,

			// Medium-high importance: User interactions
			events.EventTypeUserPrompt: 0.80,

			// Medium importance: Agent and tool activity
			events.EventTypeAgentAction: 0.70,
			events.EventTypeToolResult:  0.60,
			events.EventTypeToolCall:    0.50,

			// Lower importance: Indexing and background events
			events.EventTypeIndexComplete: 0.40,

			// Default for all other types
			events.EventTypeUserClarification: 0.30,
			events.EventTypeAgentError:        0.85,
			events.EventTypeToolTimeout:       0.85,
			events.EventTypeLLMRequest:        0.30,
			events.EventTypeLLMResponse:       0.30,
			events.EventTypeIndexStart:        0.30,
			events.EventTypeIndexFileAdded:    0.30,
			events.EventTypeContextEviction:   0.30,
			events.EventTypeContextRestore:    0.30,
		},
	}
}

// =============================================================================
// AE.5.2 ComputeColdScore Method
// =============================================================================

// ComputeColdScore calculates a cold-start score for an event before
// decay parameters are learned. Combines action-based importance,
// temporal recency, and semantic similarity.
//
// Parameters:
//   - eventType: Type of the event being scored
//   - queryEmbed: Query embedding vector
//   - eventEmbed: Event embedding vector
//   - currentTurn: Current conversation turn number
//   - eventTurn: Turn number when the event occurred
//
// Returns: Score in range [0,1]
func (ap *ActionTypePrior) ComputeColdScore(
	eventType events.EventType,
	queryEmbed, eventEmbed []float32,
	currentTurn, eventTurn int,
) float64 {
	actionScore := ap.getActionScore(eventType)
	recencyScore := ap.calculateRecency(currentTurn, eventTurn)
	semanticScore := ap.calculateSemantic(queryEmbed, eventEmbed)

	return computeWeightedScore(actionScore, recencyScore, semanticScore)
}

// getActionScore retrieves the importance score for an event type.
// Returns default importance if type is not in the map.
func (ap *ActionTypePrior) getActionScore(eventType events.EventType) float64 {
	if score, exists := ap.TypeImportance[eventType]; exists {
		return score
	}
	return 0.30 // Default importance for unknown types
}

// calculateRecency computes temporal recency score based on turn distance.
func (ap *ActionTypePrior) calculateRecency(currentTurn, eventTurn int) float64 {
	return SessionRecencyCalculator{}.CalculateRecency(currentTurn, eventTurn)
}

// calculateSemantic computes semantic similarity between query and event.
func (ap *ActionTypePrior) calculateSemantic(queryEmbed, eventEmbed []float32) float64 {
	return hnsw.CosineSimilarityVectors(queryEmbed, eventEmbed)
}

// computeWeightedScore combines the three components using fixed weights.
func computeWeightedScore(actionScore, recencyScore, semanticScore float64) float64 {
	return ActionWeight*actionScore +
		RecencyWeight*recencyScore +
		SemanticWeight*semanticScore
}

// =============================================================================
// AE.5.3 SessionRecencyCalculator
// =============================================================================

// SessionRecencyCalculator computes temporal recency scores for events
// based on their distance from the current turn in the conversation.
type SessionRecencyCalculator struct{}

// CalculateRecency computes a recency score for an event based on
// how many turns have elapsed since it occurred.
//
// The formula uses gentle decay: 1 / (1 + turnsSince/10)
// This ensures recent events score higher while maintaining reasonable
// scores for events further in the past.
//
// Parameters:
//   - currentTurn: Current conversation turn number
//   - eventTurn: Turn number when the event occurred
//
// Returns: Recency score in range (0,1]
func (src SessionRecencyCalculator) CalculateRecency(currentTurn, eventTurn int) float64 {
	turnsSince := currentTurn - eventTurn
	if turnsSince < 0 {
		turnsSince = 0
	}
	return 1.0 / (1.0 + float64(turnsSince)/10.0)
}
