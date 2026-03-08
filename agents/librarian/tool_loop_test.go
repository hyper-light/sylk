package librarian

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// mockProvider is a test double that replays a sequence of LLM responses.
type mockProvider struct {
	responses []*providers.Response
	calls     int
}

func (m *mockProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	if m.calls >= len(m.responses) {
		return nil, fmt.Errorf("mock provider: no more responses (called %d times)", m.calls+1)
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

// newTestLibrarian creates a minimal Librarian with a mock provider and
// an echo skill for tool loop testing.
func newTestLibrarian(provider LibrarianProvider) *Librarian {
	cfg := Config{
		ID:               "test-librarian",
		EnableLLM:        true,
		Model:            "test-model",
		MaxToolRuns:      5,
		MaxTokens:        1024,
		WorkingDirectory: "/tmp",
	}
	cfg = applyConfigDefaults(cfg)

	l := &Librarian{
		id:     cfg.ID,
		config: cfg,
		logger: cfg.Logger,
		skills: skills.NewRegistry(),
	}
	l.provider = provider

	// Register a trivial "echo" skill that returns its input.
	l.skills.Register(skills.NewSkill("echo").
		Description("Echo input back").
		Domain("test").
		StringParam("text", "text to echo", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return map[string]string{"echoed": params.Text}, nil
		}).
		Build())
	l.skills.Load("echo")
	l.tools = newTestToolRuntime(l.skills, "echo")

	return l
}

func newTestToolRuntime(registry *skills.Registry, visible ...string) *toolruntime.Runtime {
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Manifest: toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
			AgentID:          "test-librarian",
			CapabilityScope:  "librarian.test",
			Registry:         registry,
			VisibleByDefault: visible,
		}),
		State: toolruntime.NewState(),
	})
	if err != nil {
		panic(fmt.Sprintf("initialize test tool runtime: %v", err))
	}
	return tools
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestExecuteToolLoop_TextOnlyResponse(t *testing.T) {
	mp := &mockProvider{
		responses: []*providers.Response{
			{Content: "  Here are the results.\n", Usage: providers.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}
	l := newTestLibrarian(mp)

	result, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "find something"}},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Here are the results." {
		t.Errorf("expected trimmed content, got %q", result)
	}
	if mp.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", mp.calls)
	}
}

func TestExecuteToolLoop_ToolCallThenText(t *testing.T) {
	mp := &mockProvider{
		responses: []*providers.Response{
			{
				Content: "",
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "echo", Arguments: `{"text":"hello"}`},
				},
				Usage: providers.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{
				Content: "Done searching.",
				Usage:   providers.Usage{InputTokens: 20, OutputTokens: 10},
			},
		},
	}
	l := newTestLibrarian(mp)

	result, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "find something"}},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Done searching." {
		t.Errorf("expected final text, got %q", result)
	}
	if mp.calls != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mp.calls)
	}
}

func TestExecuteToolLoop_DuplicateDetection_ForcesSynthesis(t *testing.T) {
	// After a duplicate is detected, tools are stripped and the LLM
	// is called again. The third response has no tool calls — it should
	// produce the final answer.
	sameCall := providers.ToolCall{ID: "tc1", Name: "echo", Arguments: `{"text":"same"}`}
	mp := &mockProvider{
		responses: []*providers.Response{
			{ToolCalls: []providers.ToolCall{sameCall}, Usage: providers.Usage{}},
			{ToolCalls: []providers.ToolCall{{ID: "tc2", Name: "echo", Arguments: `{"text":"same"}`}}, Usage: providers.Usage{}},
			{Content: "Synthesized answer from prior results.", Usage: providers.Usage{}},
		},
	}
	l := newTestLibrarian(mp)

	result, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Synthesized answer from prior results." {
		t.Errorf("expected synthesized answer, got %q", result)
	}
	if mp.calls != 3 {
		t.Errorf("expected 3 LLM calls (search, dup, synthesis), got %d", mp.calls)
	}
}

func TestExecuteToolLoop_ExceedsMaxToolRuns(t *testing.T) {
	// Create a provider that always returns tool calls, exceeding MaxToolRuns.
	responses := make([]*providers.Response, 7)
	for i := range responses {
		responses[i] = &providers.Response{
			ToolCalls: []providers.ToolCall{
				{ID: fmt.Sprintf("tc%d", i), Name: "echo", Arguments: fmt.Sprintf(`{"text":"call_%d"}`, i)},
			},
			Usage: providers.Usage{},
		}
	}
	mp := &mockProvider{responses: responses}
	l := newTestLibrarian(mp)

	_, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}},
	}, nil)

	if err == nil {
		t.Fatal("expected tool-call limit error, got nil")
	}
	if got := err.Error(); !contains(got, "tool-call limit") {
		t.Errorf("expected 'tool-call limit' error, got %q", got)
	}
}

func TestExecuteToolLoop_NoProvider(t *testing.T) {
	l := newTestLibrarian(nil)
	l.provider = nil

	_, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}},
	}, nil)

	if err == nil {
		t.Fatal("expected 'no LLM provider' error, got nil")
	}
	if got := err.Error(); !contains(got, "no LLM provider") {
		t.Errorf("expected 'no LLM provider' error, got %q", got)
	}
}

func TestExecuteToolLoop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	mp := &mockProvider{
		responses: []*providers.Response{
			{Content: "should not reach", Usage: providers.Usage{}},
		},
	}
	l := newTestLibrarian(mp)

	_, err := l.executeToolLoop(ctx, &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}},
	}, nil)

	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
}

func TestExecuteToolLoop_ConsecutiveErrors(t *testing.T) {
	// Register a skill that always fails.
	l := newTestLibrarian(nil)
	l.skills.Register(skills.NewSkill("fail_always").
		Description("Always fails").
		Domain("test").
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, fmt.Errorf("intentional failure")
		}).
		Build())
	l.tools = newTestToolRuntime(l.skills, "echo", "fail_always")

	failCall := func(id string) providers.ToolCall {
		return providers.ToolCall{
			ID:        id,
			Name:      "fail_always",
			Arguments: fmt.Sprintf(`{"attempt":"%s"}`, id),
		}
	}

	mp := &mockProvider{
		responses: []*providers.Response{
			{ToolCalls: []providers.ToolCall{failCall("tc1")}, Usage: providers.Usage{}},
			{ToolCalls: []providers.ToolCall{failCall("tc2")}, Usage: providers.Usage{}},
			{ToolCalls: []providers.ToolCall{failCall("tc3")}, Usage: providers.Usage{}},
		},
	}
	l.provider = mp

	_, err := l.executeToolLoop(context.Background(), &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}},
	}, nil)

	if err == nil {
		t.Fatal("expected consecutive errors abort, got nil")
	}
	if got := err.Error(); !contains(got, "consecutive turns") {
		t.Errorf("expected 'consecutive turns' error, got %q", got)
	}
}

func TestBuildToolDefinitions(t *testing.T) {
	l := newTestLibrarian(nil)
	tools := l.buildToolDefinitions()

	if len(tools) == 0 {
		t.Fatal("expected at least one tool definition")
	}

	found := false
	for _, tool := range tools {
		if tool.Name == "echo" {
			found = true
			if tool.Description == "" {
				t.Error("echo tool missing description")
			}
			if len(tool.Parameters) == 0 {
				t.Error("echo tool missing parameters")
			}
			break
		}
	}
	if !found {
		t.Error("echo tool not found in definitions")
	}
}

func TestExecuteToolCall_UnknownTool(t *testing.T) {
	l := newTestLibrarian(nil)
	_, err := l.executeToolCall(context.Background(), providers.ToolCall{
		ID: "tc1", Name: "nonexistent", Arguments: "{}",
	})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestExecuteToolCall_InvalidJSON(t *testing.T) {
	l := newTestLibrarian(nil)
	_, err := l.executeToolCall(context.Background(), providers.ToolCall{
		ID: "tc1", Name: "echo", Arguments: "not json",
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON arguments")
	}
}

func TestExecuteToolCall_EmptyName(t *testing.T) {
	l := newTestLibrarian(nil)
	_, err := l.executeToolCall(context.Background(), providers.ToolCall{
		ID: "tc1", Name: "", Arguments: "{}",
	})
	if err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestExecuteToolCall_Success(t *testing.T) {
	l := newTestLibrarian(nil)
	result, err := l.executeToolCall(context.Background(), providers.ToolCall{
		ID: "tc1", Name: "echo", Arguments: `{"text":"hello"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty result")
	}
	if !contains(result.Output, "hello") {
		t.Errorf("expected result to contain 'hello', got %q", result.Output)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
