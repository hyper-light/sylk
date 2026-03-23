package chat

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/msg"
)

func TestFormatPlanMarkdown_RendersStructuredPlanSections(t *testing.T) {
	update := msg.PlanUpdateMsg{
		PlanID: "plan-1",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{
				ID:           "task-1",
				Name:         "Create CLI",
				Description:  "Build the CLI entrypoint.",
				AgentType:    "engineer",
				Status:       "pending",
				Dependencies: []string{"task-0"},
				ImplementationGuide: "Step 1: add the command.\n" +
					"Step 2: wire the handler.",
				AcceptanceCriteria: []msg.PlanAcceptanceCriterion{
					{Given: "a clean checkout", When: "the command runs", Then: "the CLI starts", Priority: "must"},
					{Given: "help is requested", When: "the user passes --help", Then: "usage text renders", Priority: "should"},
				},
				AffectedFiles: []msg.PlanFileTarget{
					{Path: "cmd/sylk/main.go", Operation: "modify", Reason: "wire the CLI entrypoint"},
				},
				Examples: []msg.PlanTaskExample{
					{
						Label:       "CLI usage",
						Language:    "sh",
						Code:        "sylk plan approve --plan-id plan_123",
						Explanation: "Shows the approval entrypoint.",
					},
					{
						Label:    "Execution flow",
						Language: "mermaid",
						Code: "flowchart TD\n" +
							"  Architect --> Orchestrator",
					},
				},
				Guidelines: []string{"Follow existing CLI naming."},
			},
		},
		ExecutionLayers: [][]string{{"task-1"}},
	}

	got := formatPlanMarkdown(update)
	for _, want := range []string{
		"1. **Create CLI** `engineer`",
		"**Must**",
		"- [must] Given a clean checkout / When the command runs / Then the CLI starts",
		"1. add the command.",
		"```sh",
		"```mermaid",
		"**Guidelines**",
		"`cmd/sylk/main.go` (modify): wire the CLI entrypoint",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted plan missing %q\nfull output:\n%s", want, got)
		}
	}
}
