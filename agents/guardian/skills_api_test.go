package guardian

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/commandapproval"
)

func TestGuardianInvokeSkill_CommandExecutionControlAllowedByManifest(t *testing.T) {
	g := newTestGuardian(t, &stubGuardianProvider{})

	input, err := json.Marshal(commandapproval.Request{
		Command:       "go test ./...",
		WorkspaceRoot: "/workspace/repo",
		ToolName:      "run_command",
		AgentID:       "engineer-1",
		AgentType:     "engineer",
		SessionID:     "sess-1",
	})
	if err != nil {
		t.Fatalf("Marshal(request): %v", err)
	}

	result := g.InvokeSkill(context.Background(), "command_execution_control", input)
	if result == nil {
		t.Fatal("expected command execution control result")
	}
	if !result.Success {
		t.Fatalf("InvokeSkill() error = %q", result.Error)
	}

	eval, ok := result.Data.(commandapproval.Evaluation)
	if !ok {
		t.Fatalf("result.Data = %T, want commandapproval.Evaluation", result.Data)
	}
	if eval.Decision != commandapproval.DecisionAllow {
		t.Fatalf("decision = %q, want %q", eval.Decision, commandapproval.DecisionAllow)
	}
}
