package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestContextGovernorContextRoundTrip(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	ctx := WithContextGovernor(context.Background(), gov)
	got := ContextGovernorFromContext(ctx)
	if got != gov {
		t.Fatal("round-trip failed")
	}
}

func TestContextGovernorFromNilContext(t *testing.T) {
	if got := ContextGovernorFromContext(nil); got != nil {
		t.Fatal("expected nil from nil context")
	}
}

func TestContextGovernorFromEmptyContext(t *testing.T) {
	if got := ContextGovernorFromContext(context.Background()); got != nil {
		t.Fatal("expected nil from empty context")
	}
}

func TestContextGovernorBeginTurn_Green(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	req := &providers.Request{
		SystemPrompt: "You are a helper.",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}
	gov.Initialize(req)
	zone := gov.BeginTurn(context.Background(), 0, 10, req)
	if zone != ZoneGreen {
		t.Fatalf("expected Green, got %s", zone)
	}
}

func TestContextGovernorBeginTurn_UninitializedIsGreen(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	req := &providers.Request{Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}}}
	zone := gov.BeginTurn(context.Background(), 0, 10, req)
	if zone != ZoneGreen {
		t.Fatalf("expected Green for uninitialized governor, got %s", zone)
	}
}

func TestContextGovernorCalibrate(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	req := &providers.Request{
		SystemPrompt: "system",
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	}
	gov.Initialize(req)

	msgs := []providers.Message{{Role: providers.RoleUser, Content: makeString(400)}}
	resp := &providers.Response{Usage: providers.Usage{InputTokens: 200}}
	gov.Calibrate(context.Background(), resp, msgs)
	if gov.budget.CalibSamples() != 1 {
		t.Fatalf("expected 1 calibration sample, got %d", gov.budget.CalibSamples())
	}
}

func TestContextGovernorCalibrate_NilResponse(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	gov.Initialize(&providers.Request{SystemPrompt: "s"})
	gov.Calibrate(context.Background(), nil, nil)
	if gov.budget.CalibSamples() != 0 {
		t.Fatal("should not calibrate on nil response")
	}
}

func TestContextGovernorLimitToolOutput_WithinBudget(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	gov.Initialize(&providers.Request{SystemPrompt: "s"})
	gov.currentTurn = 0
	gov.maxTurns = 10

	result := "short output"
	got := gov.LimitToolOutput(context.Background(), result, "exec")
	if got != result {
		t.Fatalf("expected unchanged result, got %q", got)
	}
}

func TestContextGovernorLimitToolOutput_Uninitialized(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	result := "anything"
	got := gov.LimitToolOutput(context.Background(), result, "exec")
	if got != result {
		t.Fatal("uninitialized governor should pass through")
	}
}

// TestContextGovernorIntegration simulates a 10-turn tool loop with a small
// context window to verify compaction triggers and tool output limiting.
func TestContextGovernorIntegration(t *testing.T) {
	// Small context window: 4000 tokens.
	gov := &ContextGovernor{
		budget: &ContextBudget{
			modelLimit:    4000,
			reserveTokens: 200,
			fixedOverhead: 0,
			counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
			calibRatio:    1.0,
			compactAt:     0.80,
			evictAt:       0.90,
			criticalAt:    0.95,
		},
		initialized: true,
	}

	ctx := context.Background()

	req := &providers.Request{
		SystemPrompt: "system",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "initial prompt"},
		},
	}

	maxRuns := 10
	var compactionTriggered bool

	for turn := range maxRuns {
		zone := gov.BeginTurn(ctx, turn, maxRuns, req)
		if zone == ZoneCritical {
			// This is acceptable — we've exhausted the budget.
			break
		}
		if zone >= ZoneYellow {
			compactionTriggered = true
		}

		// Simulate LLM response with tool call.
		toolCallContent := "assistant turn " + makeString(100)
		req.Messages = append(req.Messages, providers.Message{
			Role:      providers.RoleAssistant,
			Content:   toolCallContent,
			Metadata:  map[string]any{"thinking": "block"},
			ToolCalls: []providers.ToolCall{{ID: "tc" + string(rune('0'+turn)), Name: "exec"}},
		})

		// Simulate tool result (800 chars ≈ 200 tokens).
		toolOutput := makeString(800)
		limited := gov.LimitToolOutput(ctx, toolOutput, "exec")
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: "tc" + string(rune('0'+turn)),
			ToolName:   "exec",
			Content:    limited,
		})

		// Simulate calibration feedback.
		estimated := gov.budget.EstimateMessages(req.Messages)
		gov.Calibrate(ctx, &providers.Response{
			Usage: providers.Usage{InputTokens: int(float64(estimated) * 1.1)},
		}, req.Messages)
	}

	if !compactionTriggered {
		t.Fatal("expected compaction to trigger within 10 turns with 4000 token budget")
	}

	// Verify: first user message is never compacted.
	if req.Messages[0].Content != "initial prompt" {
		t.Fatal("initial user prompt was modified")
	}

	// Verify: assistant Metadata is never modified.
	for _, msg := range req.Messages {
		if msg.Role == providers.RoleAssistant && msg.Metadata != nil {
			if msg.Metadata["thinking"] != "block" {
				t.Fatal("assistant metadata was modified")
			}
		}
	}
}

func TestContextGovernorCompactionIntegrity(t *testing.T) {
	gov := &ContextGovernor{
		budget: &ContextBudget{
			modelLimit:    2000,
			reserveTokens: 100,
			fixedOverhead: 0,
			counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
			calibRatio:    1.0,
			compactAt:     0.80,
			evictAt:       0.90,
			criticalAt:    0.95,
		},
		initialized: true,
	}

	// Build messages that fill ~85% of budget (Yellow zone).
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "prompt"},
			// Turn 1
			{Role: providers.RoleAssistant, Content: "t1",
				ToolCalls: []providers.ToolCall{{ID: "1", Name: "exec"}}},
			{Role: providers.RoleTool, ToolCallID: "1", ToolName: "exec",
				Content: makeString(2000)},
			// Turn 2
			{Role: providers.RoleAssistant, Content: "t2",
				ToolCalls: []providers.ToolCall{{ID: "2", Name: "exec"}}},
			{Role: providers.RoleTool, ToolCallID: "2", ToolName: "exec",
				Content: makeString(2000)},
			// Turn 3
			{Role: providers.RoleAssistant, Content: "t3",
				ToolCalls: []providers.ToolCall{{ID: "3", Name: "exec"}}},
			{Role: providers.RoleTool, ToolCallID: "3", ToolName: "exec",
				Content: makeString(2000)},
			// Turn 4 (recent, should be preserved)
			{Role: providers.RoleAssistant, Content: "t4",
				ToolCalls: []providers.ToolCall{{ID: "4", Name: "exec"}}},
			{Role: providers.RoleTool, ToolCallID: "4", ToolName: "exec",
				Content: "recent output"},
		},
	}

	zone := gov.BeginTurn(context.Background(), 4, 10, req)

	// After compaction/eviction, should be below Critical.
	if zone == ZoneCritical {
		t.Fatal("expected compaction to bring below Critical")
	}

	// First message should be preserved.
	if req.Messages[0].Content != "prompt" {
		t.Fatal("initial prompt was modified")
	}

	// Check that at least one tool result was compacted (starts with "[").
	anyCompacted := false
	for _, msg := range req.Messages {
		if msg.Role == providers.RoleTool && strings.HasPrefix(msg.Content, "[") {
			anyCompacted = true
			break
		}
	}
	if !anyCompacted {
		t.Fatal("expected at least one compacted tool result")
	}
}

func TestErrContextBudgetExhausted(t *testing.T) {
	if ErrContextBudgetExhausted == nil {
		t.Fatal("sentinel should not be nil")
	}
	if ErrContextBudgetExhausted.Error() != "context budget exhausted" {
		t.Fatalf("unexpected message: %s", ErrContextBudgetExhausted.Error())
	}
}

func TestContextGovernorLimitToolOutput_UsesLiveMessages(t *testing.T) {
	gov := NewContextGovernor("claude-opus-4-6", 4096, 0)
	req := &providers.Request{
		SystemPrompt: "system",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: makeString(400000)}, // ~100K tokens
		},
	}
	gov.Initialize(req)
	// BeginTurn stores the message reference.
	gov.BeginTurn(context.Background(), 0, 2, req)

	used := gov.currentMessageTokens()
	if used == 0 {
		t.Fatal("expected non-zero currentMessageTokens after BeginTurn")
	}
}

func TestTruncateWithImportance_NoSlicePanic(t *testing.T) {
	// budgetTokens that produces charBudget > len(summary).
	// SummarizeToolResult on short content produces ~20 chars.
	// budgetTokens=100 → charBudget=400 > len(summary).
	result := truncateWithImportance("short", "tool", 100)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// budgetTokens=0 → should return "[truncated]" not panic.
	result = truncateWithImportance("any content here", "tool", 0)
	if result != "[truncated]" {
		t.Fatalf("expected [truncated], got %q", result)
	}
}

// TestContextGovernorCritical_CompactsAllGroups verifies that when all groups
// are in the preserve-recent window, Critical zone compacts ALL groups
// (including recent) rather than immediately aborting.
func TestContextGovernorCritical_CompactsAllGroups(t *testing.T) {
	gov := &ContextGovernor{
		budget: &ContextBudget{
			modelLimit:    2000,
			reserveTokens: 100,
			fixedOverhead: 0,
			counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
			calibRatio:    1.0,
			compactAt:     0.80,
			evictAt:       0.90,
			criticalAt:    0.95,
		},
		initialized: true,
	}

	// 2 turn groups — both within preserveRecentGroups=3 window.
	// Each tool result is large enough to push past Critical.
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "prompt"},
			{Role: providers.RoleAssistant, Content: "t1",
				ToolCalls: []providers.ToolCall{{ID: "1", Name: "lint"}}},
			{Role: providers.RoleTool, ToolCallID: "1", ToolName: "lint",
				Content: makeString(4000)},
			{Role: providers.RoleAssistant, Content: "t2",
				ToolCalls: []providers.ToolCall{{ID: "2", Name: "test"}}},
			{Role: providers.RoleTool, ToolCallID: "2", ToolName: "test",
				Content: makeString(4000)},
		},
	}

	zone := gov.BeginTurn(context.Background(), 2, 10, req)

	// Should NOT be Critical — compacting all groups (including recent) should help.
	if zone == ZoneCritical {
		t.Fatal("expected compaction of all groups to bring below Critical")
	}

	// At least one tool result should be compacted.
	anyCompacted := false
	for _, msg := range req.Messages {
		if msg.Role == providers.RoleTool && strings.HasPrefix(msg.Content, "[") {
			anyCompacted = true
			break
		}
	}
	if !anyCompacted {
		t.Fatal("expected recent groups to be compacted in Critical zone")
	}
}
