package architect

import "testing"

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

