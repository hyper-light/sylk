package architect

import (
	"strings"
	"testing"
)

func TestFormatPlanThoughtMessage_AppendsEllipsisForIncompleteThought(t *testing.T) {
	got := formatPlanThoughtMessage("tasks", "Let me")
	if got != "Tasks: Let me..." {
		t.Fatalf("formatPlanThoughtMessage() = %q, want %q", got, "Tasks: Let me...")
	}
}

func TestFormatPlanThoughtMessage_LeavesCompleteThoughtUntouched(t *testing.T) {
	got := formatPlanThoughtMessage("design", "I should compare the main options.")
	if got != "Design: I should compare the main options." {
		t.Fatalf("formatPlanThoughtMessage() = %q, want %q", got, "Design: I should compare the main options.")
	}
}

func TestBuildPlanSnapshotData_IncludesGuidelinesAndExamples(t *testing.T) {
	plan := &DesignPlan{
		ID:     "plan-1",
		Status: PlanStatusReady,
		Tasks: []*AtomicTask{
			{
				ID:          "task-1",
				Name:        "Create CLI",
				AgentType:   "engineer",
				Status:      TaskStatusPending,
				Guidelines:  []string{"Follow existing CLI naming."},
				Examples:    []TaskExample{{Label: "CLI usage", Language: "sh", Code: "sylk plan approve --plan-id plan_123"}},
				Description: "Build the CLI entrypoint.",
			},
		},
	}

	snapshot := buildPlanSnapshotData(plan)
	rawTasks, ok := snapshot["Tasks"].([]map[string]any)
	if !ok || len(rawTasks) != 1 {
		t.Fatalf("unexpected task snapshot payload: %#v", snapshot["Tasks"])
	}
	if _, ok := rawTasks[0]["Guidelines"]; !ok {
		t.Fatalf("expected guidelines in snapshot: %#v", rawTasks[0])
	}
	if _, ok := rawTasks[0]["Examples"]; !ok {
		t.Fatalf("expected examples in snapshot: %#v", rawTasks[0])
	}
}

func TestFormatPlanForChat_RendersExamplesAndStepLists(t *testing.T) {
	plan := &DesignPlan{
		ID:     "plan-1",
		Status: PlanStatusReady,
		Tasks: []*AtomicTask{
			{
				ID:          "task-1",
				Name:        "Create CLI",
				AgentType:   "engineer",
				Status:      TaskStatusPending,
				Description: "Build the CLI entrypoint.",
				ImplementationGuide: "Step 1: add the command.\n" +
					"Step 2: wire the handler.",
				AcceptanceCriteria: []AcceptanceCriterion{
					{Given: "a clean checkout", When: "the command runs", Then: "the CLI starts", Priority: "must"},
				},
				Examples: []TaskExample{
					{
						Label:    "Execution flow",
						Language: "mermaid",
						Code: "flowchart TD\n" +
							"  Architect --> Orchestrator",
					},
				},
			},
		},
	}

	got := formatPlanForChat(plan)
	for _, want := range []string{
		"1. **Create CLI** `engineer`",
		"1. add the command.",
		"```mermaid",
		"**Must**",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted plan missing %q\nfull output:\n%s", want, got)
		}
	}
}
