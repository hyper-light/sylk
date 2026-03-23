package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

func TestDisableProtocolPipelineNodeTimeouts_DisablesInheritedTimeoutOnly(t *testing.T) {
	d, err := dag.NewBuilder("protocol-timeouts").
		AddNode(dag.NodeConfig{
			ID:        "engineer-default",
			AgentType: "engineer",
		}).
		AddNode(dag.NodeConfig{
			ID:        "designer-explicit",
			AgentType: "designer",
			Timeout:   2 * time.Minute,
		}).
		AddNode(dag.NodeConfig{
			ID:        "tester-default",
			AgentType: "tester-pipeline",
		}).
		AddNode(dag.NodeConfig{
			ID:        "engineer-inspect",
			AgentType: "engineer",
			Context: map[string]any{
				"pipeline_stage": string(StageInspect),
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}

	if disabled := disableProtocolPipelineNodeTimeouts(d); disabled != 1 {
		t.Fatalf("disabled = %d, want 1", disabled)
	}

	engineerDefault, ok := d.GetNode("engineer-default")
	if !ok {
		t.Fatal("missing engineer-default node")
	}
	if engineerDefault.Timeout() != dag.NoTimeout {
		t.Fatalf("engineer-default timeout = %v, want %v", engineerDefault.Timeout(), dag.NoTimeout)
	}

	designerExplicit, ok := d.GetNode("designer-explicit")
	if !ok {
		t.Fatal("missing designer-explicit node")
	}
	if designerExplicit.Timeout() != 2*time.Minute {
		t.Fatalf("designer-explicit timeout = %v, want 2m", designerExplicit.Timeout())
	}

	testerDefault, ok := d.GetNode("tester-default")
	if !ok {
		t.Fatal("missing tester-default node")
	}
	if testerDefault.Timeout() != 0 {
		t.Fatalf("tester-default timeout = %v, want 0", testerDefault.Timeout())
	}

	engineerInspect, ok := d.GetNode("engineer-inspect")
	if !ok {
		t.Fatal("missing engineer-inspect node")
	}
	if engineerInspect.Timeout() != 0 {
		t.Fatalf("engineer-inspect timeout = %v, want 0", engineerInspect.Timeout())
	}
}
