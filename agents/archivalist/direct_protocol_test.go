package archivalist

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/handoff"
)

func TestProcessForwardedRequest_StoreBypassesLLMAndUsesStructuredContent(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		Input:  "@archivalist:store:history{\"content\":\"stored child event\"}",
		Intent: guide.IntentStore,
		Domain: guide.DomainHistory,
		Entities: &guide.ExtractedEntities{
			Data: map[string]any{"content": "stored child event"},
		},
		Metadata: map[string]any{
			"event_type": "child_store",
		},
	})
	if err != nil {
		t.Fatalf("processForwardedRequest returned error: %v", err)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.CallCount())
	}
	if result == nil {
		t.Fatal("expected store result")
	}
}

func TestProcessForwardedRequest_DeclareAndCompleteBypassLLM(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true

	tests := []guide.Intent{guide.IntentDeclare, guide.IntentComplete}
	for _, intent := range tests {
		t.Run(string(intent), func(t *testing.T) {
			provider.Reset()
			_, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
				Input:  "structured control intent",
				Intent: intent,
				Domain: guide.DomainHistory,
			})
			if err != nil {
				t.Fatalf("processForwardedRequest returned error: %v", err)
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestProcessForwardedRequest_BriefingRequestBypassesLLMAndReturnsContextBrief(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true
	a.SetCurrentTask("Implement handoff persistence", "keep scribe continuity", SourceModelClaudeOpus)
	a.SetCurrentStep("Restore archivalist briefing contract")
	a.SetNextSteps([]string{"Wire shared brief source", "Verify handoff tests"})
	a.AddBlocker("Need canonical briefing surface")

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		Input:  "archivalist_get_briefing",
		Intent: guide.IntentRecall,
		Metadata: map[string]any{
			"request_type": "briefing",
			"tool_name":    ToolGetBriefing,
			"brief_format": "context_brief",
			"brief_tier":   "standard",
			"agent_type":   "engineer",
			"context_size": 4096,
			"turn_number":  7,
		},
	})
	if err != nil {
		t.Fatalf("processForwardedRequest returned error: %v", err)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.CallCount())
	}
	brief, ok := result.(*handoff.ContextBrief)
	if !ok || brief == nil {
		t.Fatalf("briefing result = %#v, want *handoff.ContextBrief", result)
	}
	if brief.TaskSummary == "" {
		t.Fatal("expected task summary")
	}
	if brief.GeneratedAt.Before(time.Now().Add(-5 * time.Second)) {
		t.Fatalf("generated_at = %v, want recent timestamp", brief.GeneratedAt)
	}
	if brief.ContextSize != 4096 || brief.TurnNumber != 7 {
		t.Fatalf("brief metadata = (%d,%d), want (4096,7)", brief.ContextSize, brief.TurnNumber)
	}
}
