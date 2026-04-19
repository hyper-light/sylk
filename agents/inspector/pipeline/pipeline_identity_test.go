package pipeline

import (
	"testing"

	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
)

func TestNew_UsesConfiguredAgentID(t *testing.T) {
	pi, err := New(inspectorshared.PipelineInspectorConfig{Factory: newTestFactory(t), AgentID: "inspector-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := pi.AgentID(); got != "inspector-pipeline" {
		t.Fatalf("AgentID() = %q, want inspector-pipeline", got)
	}
}
