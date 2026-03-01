package container

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

// mockSwappableAgent implements both ContainerAgent and ModelSwappable.
type mockSwappableAgent struct {
	mockAgent
	model    string
	models   []ModelOption
	provider providers.ProviderAdapter
}

func (m *mockSwappableAgent) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	m.model = modelID
	m.provider = provider
	return nil
}

func (m *mockSwappableAgent) CurrentModel() string { return m.model }

func (m *mockSwappableAgent) SupportedModels() []ModelOption { return m.models }

// Verify interface compliance.
var _ ModelSwappable = (*mockSwappableAgent)(nil)

func TestModelSwappableInterface(t *testing.T) {
	agent := &mockSwappableAgent{
		mockAgent: mockAgent{id: "test", agentType: "engineer"},
		model:     "gpt-5.3-codex",
		models: []ModelOption{
			{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
			{ID: "gpt-5.2-codex", DisplayName: "GPT-5.2 Codex"},
		},
	}

	if got := agent.CurrentModel(); got != "gpt-5.3-codex" {
		t.Errorf("CurrentModel() = %q, want gpt-5.3-codex", got)
	}

	if err := agent.SwapModel(context.Background(), "gpt-5.2-codex", nil); err != nil {
		t.Fatalf("SwapModel error: %v", err)
	}

	if got := agent.CurrentModel(); got != "gpt-5.2-codex" {
		t.Errorf("CurrentModel() after swap = %q, want gpt-5.2-codex", got)
	}

	models := agent.SupportedModels()
	if len(models) != 2 {
		t.Fatalf("SupportedModels() len = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-5.3-codex" {
		t.Errorf("SupportedModels()[0].ID = %q, want gpt-5.3-codex", models[0].ID)
	}
}

func TestBuildModelSwapper_FindsAgent(t *testing.T) {
	reg := NewContainerRegistry()
	agent := &mockSwappableAgent{
		mockAgent: mockAgent{id: "eng-1", agentType: "engineer"},
		model:     "gpt-5.3-codex",
	}
	c := testContainerWithID(t, "eng-1", "engineer", nil)
	// Replace the mock agent with our swappable one.
	c.agent = agent
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Walk the registry the same way buildModelSwapper does.
	for _, ctr := range reg.ListByType("engineer") {
		a := ctr.Agent()
		swappable, ok := a.(ModelSwappable)
		if !ok {
			t.Fatal("agent does not implement ModelSwappable")
		}
		if err := swappable.SwapModel(context.Background(), "gpt-5.2-codex", nil); err != nil {
			t.Fatalf("SwapModel: %v", err)
		}
		if got := swappable.CurrentModel(); got != "gpt-5.2-codex" {
			t.Errorf("CurrentModel() = %q, want gpt-5.2-codex", got)
		}
		return
	}
	t.Fatal("no engineer containers found in registry")
}

func TestProviderForModel(t *testing.T) {
	cases := []struct {
		modelID string
		want    ProviderType
	}{
		{"claude-opus-4-6", ProviderAnthropic},
		{"claude-sonnet-4-6", ProviderAnthropic},
		{"gemini-3.1-pro-preview", ProviderGoogle},
		{"gemini-3-flash-preview", ProviderGoogle},
		{"gpt-5.3-codex", ProviderOpenAI},
		{"gpt-5.2-codex", ProviderOpenAI},
		{"unknown-model", ""},
	}
	for _, tc := range cases {
		got := ProviderForModel(tc.modelID)
		if got != tc.want {
			t.Errorf("ProviderForModel(%q) = %q, want %q", tc.modelID, got, tc.want)
		}
	}
}
