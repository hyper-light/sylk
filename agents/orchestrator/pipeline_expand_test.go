package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/core/dag"
)

func TestExpandPipelineNode_ProducesThreeSubNodes(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task-1",
		AgentType: "engineer",
		Prompt:    "implement feature X",
		Timeout:   10 * time.Minute,
		Retries:   2,
		Priority:  5,
		Context:   map[string]any{"key": "value"},
	})

	ep := ExpandPipelineNode(node)

	if len(ep.SubNodes) != 3 {
		t.Fatalf("expected 3 sub-nodes, got %d", len(ep.SubNodes))
	}

	// Verify sub-node IDs
	if ep.SubNodes[0].ID != "task-1:inspect" {
		t.Errorf("expected inspect sub-node ID task-1:inspect, got %s", ep.SubNodes[0].ID)
	}
	if ep.SubNodes[1].ID != "task-1:test" {
		t.Errorf("expected test sub-node ID task-1:test, got %s", ep.SubNodes[1].ID)
	}
	if ep.SubNodes[2].ID != "task-1:execute" {
		t.Errorf("expected execute sub-node ID task-1:execute, got %s", ep.SubNodes[2].ID)
	}

	// Verify agent types
	if ep.SubNodes[0].AgentType != "inspector-pipeline" {
		t.Errorf("inspect agent type: got %s", ep.SubNodes[0].AgentType)
	}
	if ep.SubNodes[1].AgentType != "tester-pipeline" {
		t.Errorf("test agent type: got %s", ep.SubNodes[1].AgentType)
	}
	if ep.SubNodes[2].AgentType != "engineer" {
		t.Errorf("execute agent type: got %s", ep.SubNodes[2].AgentType)
	}

	// Verify dependencies chain
	if len(ep.SubNodes[0].Dependencies) != 0 {
		t.Errorf("inspect should have no deps, got %v", ep.SubNodes[0].Dependencies)
	}
	if len(ep.SubNodes[1].Dependencies) != 1 || ep.SubNodes[1].Dependencies[0] != "task-1:inspect" {
		t.Errorf("test deps: got %v", ep.SubNodes[1].Dependencies)
	}
	if len(ep.SubNodes[2].Dependencies) != 1 || ep.SubNodes[2].Dependencies[0] != "task-1:test" {
		t.Errorf("execute deps: got %v", ep.SubNodes[2].Dependencies)
	}

	// Verify proportional timeouts
	parentTimeout := 10 * time.Minute
	inspectTimeout := time.Duration(float64(parentTimeout) * 0.15)
	testTimeout := time.Duration(float64(parentTimeout) * 0.25)
	executeTimeout := time.Duration(float64(parentTimeout) * 0.60)

	if ep.SubNodes[0].Timeout != inspectTimeout {
		t.Errorf("inspect timeout: got %v, want %v", ep.SubNodes[0].Timeout, inspectTimeout)
	}
	if ep.SubNodes[1].Timeout != testTimeout {
		t.Errorf("test timeout: got %v, want %v", ep.SubNodes[1].Timeout, testTimeout)
	}
	if ep.SubNodes[2].Timeout != executeTimeout {
		t.Errorf("execute timeout: got %v, want %v", ep.SubNodes[2].Timeout, executeTimeout)
	}

	// Verify retries inherited
	for i, sub := range ep.SubNodes {
		if sub.Retries != 2 {
			t.Errorf("sub-node %d retries: got %d, want 2", i, sub.Retries)
		}
	}

	// Verify metadata
	expectedStages := []string{"inspect", "test", "execute"}
	for i, sub := range ep.SubNodes {
		parentID, ok := sub.Metadata["pipeline_parent_id"].(string)
		if !ok || parentID != "task-1" {
			t.Errorf("sub-node %d: missing or wrong pipeline_parent_id in metadata", i)
		}

		// Verify pipeline_stage in context (used by handleTaskDispatch)
		stage, ok := sub.Context["pipeline_stage"].(string)
		if !ok || stage != expectedStages[i] {
			t.Errorf("sub-node %d: context pipeline_stage = %q, want %q", i, stage, expectedStages[i])
		}

		// Verify pipeline_parent_id in context
		ctxParent, ok := sub.Context["pipeline_parent_id"].(string)
		if !ok || ctxParent != "task-1" {
			t.Errorf("sub-node %d: missing or wrong pipeline_parent_id in context", i)
		}
	}

	// Verify sub-node map
	if len(ep.SubNodeMap) != 3 {
		t.Fatalf("expected 3 entries in SubNodeMap, got %d", len(ep.SubNodeMap))
	}
	for subID, parentID := range ep.SubNodeMap {
		if parentID != "task-1" {
			t.Errorf("SubNodeMap[%s] = %s, want task-1", subID, parentID)
		}
	}
}

func TestExpandPipelineNode_InheritsParentDependencies(t *testing.T) {
	node := dag.NewNode(dag.NodeConfig{
		ID:           "task-2",
		AgentType:    "designer",
		Prompt:       "design feature Y",
		Dependencies: []string{"task-0"},
	})

	ep := ExpandPipelineNode(node)

	// Inspect inherits the parent's external deps
	if len(ep.SubNodes[0].Dependencies) != 1 || ep.SubNodes[0].Dependencies[0] != "task-0" {
		t.Errorf("inspect should inherit parent deps, got %v", ep.SubNodes[0].Dependencies)
	}

	// Execute agent type should be "designer"
	if ep.SubNodes[2].AgentType != "designer" {
		t.Errorf("execute agent type for designer: got %s", ep.SubNodes[2].AgentType)
	}
}

func TestPipelineExpandable(t *testing.T) {
	cases := []struct {
		agentType string
		want      bool
	}{
		{"engineer", true},
		{"designer", true},
		{"inspector-pipeline", false},
		{"tester-pipeline", false},
		{"architect", false},
	}

	for _, tc := range cases {
		got := pipelineExpandable(tc.agentType)
		if got != tc.want {
			t.Errorf("pipelineExpandable(%q) = %v, want %v", tc.agentType, got, tc.want)
		}
	}
}

func TestSubNodeID_And_ParentNodeID(t *testing.T) {
	subID := SubNodeID("task-1", StageInspect)
	if subID != "task-1:inspect" {
		t.Errorf("SubNodeID: got %s", subID)
	}

	parentID, ok := ParentNodeID("task-1:inspect")
	if !ok || parentID != "task-1" {
		t.Errorf("ParentNodeID: got %s, %v", parentID, ok)
	}

	parentID, ok = ParentNodeID("task-1")
	if ok {
		t.Errorf("ParentNodeID should return false for non-sub-node")
	}
	if parentID != "task-1" {
		t.Errorf("ParentNodeID should return original ID for non-sub-node, got %s", parentID)
	}
}

func TestStageFromSubNodeID(t *testing.T) {
	if StageFromSubNodeID("task-1:inspect") != StageInspect {
		t.Errorf("expected StageInspect")
	}
	if StageFromSubNodeID("task-1:test") != StageTest {
		t.Errorf("expected StageTest")
	}
	if StageFromSubNodeID("task-1:execute") != StageExecute {
		t.Errorf("expected StageExecute")
	}
	if StageFromSubNodeID("task-1") != "" {
		t.Errorf("expected empty string for non-sub-node")
	}
}

func TestExpandDAG_MixedNodes(t *testing.T) {
	d := dag.NewDAG("test", dag.DefaultExecutionPolicy())

	// Non-pipeline node
	d.AddNode(dag.NodeConfig{
		ID:        "setup",
		AgentType: "architect",
		Prompt:    "setup",
	})

	// Pipeline node depending on setup
	d.AddNode(dag.NodeConfig{
		ID:           "impl",
		AgentType:    "engineer",
		Prompt:       "implement",
		Dependencies: []string{"setup"},
		Timeout:      10 * time.Minute,
	})

	// Non-pipeline node depending on pipeline node
	d.AddNode(dag.NodeConfig{
		ID:           "review",
		AgentType:    "architect",
		Prompt:       "review",
		Dependencies: []string{"impl"},
	})

	expanded, subMap := ExpandDAG(d)

	// Should have: setup(1) + impl:inspect,impl:test,impl:execute(3) + review(1) = 5
	if expanded.NodeCount() != 5 {
		t.Fatalf("expected 5 nodes in expanded DAG, got %d", expanded.NodeCount())
	}

	// Sub-node map should have 3 entries
	if len(subMap) != 3 {
		t.Fatalf("expected 3 sub-node map entries, got %d", len(subMap))
	}

	// Verify review now depends on impl:execute
	reviewNode, ok := expanded.GetNode("review")
	if !ok {
		t.Fatal("review node not found in expanded DAG")
	}
	deps := reviewNode.Dependencies()
	if len(deps) != 1 || deps[0] != "impl:execute" {
		t.Errorf("review deps should be [impl:execute], got %v", deps)
	}

	// Verify inspect depends on setup
	inspectNode, ok := expanded.GetNode("impl:inspect")
	if !ok {
		t.Fatal("impl:inspect not found")
	}
	inspectDeps := inspectNode.Dependencies()
	if len(inspectDeps) != 1 || inspectDeps[0] != "setup" {
		t.Errorf("impl:inspect deps should be [setup], got %v", inspectDeps)
	}
}

func TestValidateHandoff_RejectNonPipelineAgentTypes(t *testing.T) {
	cases := []struct {
		name      string
		agentType string
		wantErr   bool
	}{
		{"engineer is valid", "engineer", false},
		{"designer is valid", "designer", false},
		{"tester is rejected", "tester", true},
		{"inspector is rejected", "inspector", true},
		{"architect is rejected", "architect", true},
		{"tester-pipeline is rejected", "tester-pipeline", true},
		{"empty is rejected", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &architect.PlanHandoff{
				PlanID: "plan_test",
				Tasks: []*architect.HandoffTask{
					{ID: "task_1", AgentType: tc.agentType},
				},
			}
			err := validateHandoff(h)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for agent_type %q, got nil", tc.agentType)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for agent_type %q: %v", tc.agentType, err)
			}
		})
	}
}

func TestBuildNodeContext_IncludesStableTaskIdentity(t *testing.T) {
	ht := &architect.HandoffTask{
		ID:        "task_auth_checkout",
		Slug:      "auth-checkout",
		Name:      "Implement auth checkout",
		AgentType: "engineer",
	}

	ctx := buildNodeContext(ht)

	if got, _ := ctx["task_id"].(string); got != "task_auth_checkout" {
		t.Fatalf("task_id = %q, want task_auth_checkout", got)
	}
	if got, _ := ctx["task_slug"].(string); got != "auth-checkout" {
		t.Fatalf("task_slug = %q, want auth-checkout", got)
	}
}
