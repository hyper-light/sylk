package chat

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
)

func TestConsultationResponseSummary_GuardianPayloadPrefersHumanMessage(t *testing.T) {
	summary := consultationResponseSummary(map[string]any{
		"target": "guardian",
		"data": map[string]any{
			"user_message": "Safe to proceed once the risky command is approved.",
			"reason":       "guardian-approved deterministic control-plane grant",
		},
	})
	if summary != "Safe to proceed once the risky command is approved." {
		t.Fatalf("consultation summary = %q", summary)
	}
}

func TestConsultationResponseSummary_AcademicPayloadUsesContent(t *testing.T) {
	summary := consultationResponseSummary(map[string]any{
		"type":    "recall",
		"content": "Use Typer for most new Python CLIs and ground details against the official docs.",
	})
	if summary != "Use Typer for most new Python CLIs and ground details against the official docs." {
		t.Fatalf("consultation summary = %q", summary)
	}
}

func TestBuildInterAgentStartRecord_MergesMetadataWithFallbackTargets(t *testing.T) {
	startedAt := time.Now()
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-1",
		ToolCallKey:   "consult-1",
		ToolName:      "consult",
		FullArgs:      `{"target":"librarian","query":"Find relevant in-repo patterns."}`,
		Phase:         0,
		StartedAt:     startedAt,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:    "consult",
			Status:  "pending",
			Summary: "",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven consult start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "librarian" {
		t.Fatalf("agent types = %#v, want [librarian]", got)
	}
	if got := record.InterAgent.Summary; got != "Find relevant in-repo patterns." {
		t.Fatalf("summary = %q, want fallback query", got)
	}
	if got := record.ToolCallKey; got != "consult-1" {
		t.Fatalf("tool call key = %q, want consult-1", got)
	}
}

func TestBuildInterAgentStartRecord_MergesMetadataWithFallbackChallengeTargets(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-2",
		ToolCallKey:   "challenge-1",
		ToolName:      "challenge_architect",
		FullArgs:      `{"target_agent":"architect","request":"Reassess the plan."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:   "challenge",
			Status: "pending",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven challenge start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "architect" {
		t.Fatalf("agent types = %#v, want [architect]", got)
	}
	if got := record.InterAgent.Summary; got != "Reassess the plan." {
		t.Fatalf("summary = %q, want fallback request", got)
	}
}

func TestBuildInterAgentStartRecord_UsesApprovalMetadata(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-approval",
		ToolCallKey:   "approval-1",
		ToolName:      "approval_guardian",
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			AgentTypes: []string{"guardian"},
			Summary:    "Requesting Guardian approval for example.com",
			Status:     "pending",
		},
	})
	if !ok {
		t.Fatal("expected metadata-driven approval start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.Kind; got != InterAgentToolApproval {
		t.Fatalf("kind = %q, want %q", got, InterAgentToolApproval)
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "guardian" {
		t.Fatalf("agent types = %#v, want [guardian]", got)
	}
}

func TestBuildInterAgentStartRecord_PipelineChallengeNormalizesTesterLabel(t *testing.T) {
	record, ok := buildInterAgentStartRecord(msg.ToolCallEventMsg{
		CorrelationID: "corr-pipeline",
		ToolCallKey:   "challenge-pipeline-1",
		ToolName:      "challenge_agent",
		AgentType:     "inspector-pipeline",
		PipelineID:    "task_1",
		FullArgs:      `{"target_agents":["tester"],"request":"Audit the pipeline results."}`,
		Phase:         0,
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:   "challenge",
			Status: "pending",
		},
	})
	if !ok {
		t.Fatal("expected pipeline challenge start record")
	}
	if record.InterAgent == nil {
		t.Fatal("expected inter-agent row")
	}
	if got := record.InterAgent.AgentTypes; len(got) != 1 || got[0] != "tester-pipeline" {
		t.Fatalf("agent types = %#v, want [tester-pipeline]", got)
	}
}
