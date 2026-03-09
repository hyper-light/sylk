package pipeline

import (
	"testing"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
)

func TestNew_UsesConfiguredAgentID(t *testing.T) {
	pt, err := New(testershared.PipelineTesterConfig{AgentID: "tester-pipeline"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := pt.AgentID(); got != "tester-pipeline" {
		t.Fatalf("AgentID() = %q, want tester-pipeline", got)
	}
}
