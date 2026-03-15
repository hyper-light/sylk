package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/stretchr/testify/require"
)

func TestDefaultDAGBridgeConfig_DisablesAutomaticRetries(t *testing.T) {
	cfg := DefaultDAGBridgeConfig()
	require.Zero(t, cfg.DefaultRetries)
}

func TestBuildDAGFromHandoff_DisablesAutomaticRetries(t *testing.T) {
	handoff := &architect.PlanHandoff{
		PlanID:    "plan-1",
		SessionID: "session-1",
		Query:     "implement feature",
		Constraints: &architect.PlanConstraints{
			MaxConcurrency: 2,
			Timeout:        time.Minute,
		},
		Tasks: []*architect.HandoffTask{
			{
				ID:          "task-1",
				Name:        "Implement feature",
				Description: "do the work",
				AgentType:   "engineer",
			},
		},
	}

	d, err := buildDAGFromHandoff(handoff)
	require.NoError(t, err)
	require.Zero(t, d.Policy().DefaultRetries)
}
