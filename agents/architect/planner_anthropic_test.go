package architect

import (
	"strings"
	"testing"
)

func TestDecodeJSONPayload_FencedJSON(t *testing.T) {
	raw := "Here is the result:\n```json\n{\"goals\":[\"ship\"],\"scope\":\"api\"}\n```"
	var out map[string]any
	if err := decodeJSONPayload(raw, &out); err != nil {
		t.Fatalf("decodeJSONPayload() error = %v", err)
	}
	if out["scope"] != "api" {
		t.Fatalf("scope = %v, want api", out["scope"])
	}
}

func TestParseTaskPayload_Array(t *testing.T) {
	raw := `[{"name":"Implement auth","description":"desc","agent_type":"documenter","dependencies":[]}]`
	tasks, err := parseTaskPayload(raw)
	if err != nil {
		t.Fatalf("parseTaskPayload() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].AgentType != "engineer" {
		t.Fatalf("AgentType = %q, want engineer", tasks[0].AgentType)
	}
}

func TestNormalizeTaskGraph_MapsDependencyNameToID(t *testing.T) {
	tasks := []*AtomicTask{
		{ID: "task_1", Name: "Implement storage", AgentType: "engineer"},
		{ID: "task_2", Name: "Implement api", Dependencies: []string{"Implement storage"}, AgentType: "designer"},
	}
	normalized := normalizeTaskGraph(tasks)
	if len(normalized[1].Dependencies) != 1 {
		t.Fatalf("len(dependencies) = %d, want 1", len(normalized[1].Dependencies))
	}
	if normalized[1].Dependencies[0] != "task_1" {
		t.Fatalf("dependency = %q, want task_1", normalized[1].Dependencies[0])
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	if !containsIgnoreCase("Architect Planner", "planner") {
		t.Fatal("expected containsIgnoreCase to match case-insensitive substring")
	}
	if containsIgnoreCase("Architect", "tester") {
		t.Fatal("did not expect unrelated substring to match")
	}
}

func TestResolveThinkingBudget(t *testing.T) {
	t.Run("dynamic fallback when thinkingBudget is zero", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 0}
		budget := p.resolveThinkingBudget(6000)
		// maxTokens/3 = 2000, but floor is 1024, so expect 2000
		if budget != 2000 {
			t.Fatalf("expected 2000 for dynamic fallback, got %d", budget)
		}
	})

	t.Run("explicit budget used when set", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(16384)
		if budget != 8192 {
			t.Fatalf("expected 8192, got %d", budget)
		}
	})

	t.Run("clamps when budget >= maxTokens", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(4096)
		if budget != 4095 {
			t.Fatalf("expected 4095 (maxTokens-1), got %d", budget)
		}
	})

	t.Run("disabled for small maxTokens", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(1024)
		if budget != 0 {
			t.Fatalf("expected 0 for small maxTokens, got %d", budget)
		}
	})
}

func TestAppendThoughtDeltaAccumulatesFragments(t *testing.T) {
	var thought strings.Builder

	if got := appendThoughtDelta(&thought, "Clar"); got != "Clar" {
		t.Fatalf("appendThoughtDelta(Clar) = %q, want %q", got, "Clar")
	}
	if got := appendThoughtDelta(&thought, "ifying questions"); got != "Clarifying questions" {
		t.Fatalf("appendThoughtDelta(fragment) = %q, want %q", got, "Clarifying questions")
	}
	if got := appendThoughtDelta(&thought, "."); got != "Clarifying questions." {
		t.Fatalf("appendThoughtDelta(period) = %q, want %q", got, "Clarifying questions.")
	}
}
