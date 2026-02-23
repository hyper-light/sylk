package guide

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// MockClassifierClient is a ClassifierClient that returns a fixed classification
// without calling any LLM API. It routes all natural language queries to a
// configurable default target agent with high confidence.
type MockClassifierClient struct {
	// DefaultTarget is the agent ID to route all queries to.
	DefaultTarget string
}

// mockClassificationResponse is the JSON structure returned as classification.
type mockClassificationResponse struct {
	IsRetrospective bool    `json:"is_retrospective"`
	Intent          string  `json:"intent"`
	Domain          string  `json:"domain"`
	TargetAgent     string  `json:"target_agent"`
	Confidence      float64 `json:"confidence"`
}

// New returns a synthetic anthropic.Message containing a JSON classification
// that routes to the configured DefaultTarget.
func (m *MockClassifierClient) New(_ context.Context, _ anthropic.MessageNewParams) (*anthropic.Message, error) {
	target := m.DefaultTarget
	if target == "" {
		target = "guide"
	}
	intent, domain := mockIntentDomainForTarget(target)

	classification := mockClassificationResponse{
		IsRetrospective: false,
		Intent:          string(intent),
		Domain:          string(domain),
		TargetAgent:     target,
		Confidence:      0.95,
	}

	payload, _ := json.Marshal(classification)

	return &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{
				Type: "text",
				Text: string(payload),
			},
		},
		Role:       "assistant",
		StopReason: "end_turn",
	}, nil
}

func mockIntentDomainForTarget(target string) (Intent, Domain) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "architect", "planner", "designer":
		return IntentPlan, DomainPlanning
	case "orchestrator", "orch":
		return IntentStatus, DomainSystem
	default:
		return IntentHelp, DomainGeneral
	}
}
