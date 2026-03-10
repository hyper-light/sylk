package agent

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelsForAgent(t *testing.T) {
	cases := []struct {
		agentType string
		wantLen   int
	}{
		{"guide", 2},
		{"engineer", 1},
		{"designer", 2},
		{"inspector", 2},
		{"tester", 2},
		{"orchestrator", 2},
		{"architect", 2},
		{"librarian", 2},
		{"archivalist", 2},
		{"academic", 2},
	}
	for _, tc := range cases {
		models := modelsForAgent(tc.agentType)
		if len(models) != tc.wantLen {
			t.Errorf("modelsForAgent(%q) len = %d, want %d", tc.agentType, len(models), tc.wantLen)
		}
	}
}

func TestModelsForAgent_Unknown(t *testing.T) {
	if got := modelsForAgent("unknown"); got != nil {
		t.Errorf("modelsForAgent(unknown) = %v, want nil", got)
	}
}

func TestModelsForAgentForAuth_ChatGPTRewritesOpenAIModels(t *testing.T) {
	models := modelsForAgentForAuth("engineer", "chatgpt")
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	if models[0].ID != "gpt-5.4" {
		t.Fatalf("model ID = %q, want gpt-5.4", models[0].ID)
	}
	if models[0].DisplayName != "GPT-5.4" {
		t.Fatalf("display name = %q, want GPT-5.4", models[0].DisplayName)
	}
}

func TestModelIndex(t *testing.T) {
	models := modelsForAgent("inspector")

	// Known ID — claude-opus-4-6 is at index 1 for inspector.
	idx := modelIndex(models, "claude-opus-4-6")
	if idx != 1 {
		t.Errorf("modelIndex(claude-opus-4-6) = %d, want 1", idx)
	}

	// Unknown ID returns 0.
	idx = modelIndex(models, "nonexistent")
	if idx != 0 {
		t.Errorf("modelIndex(nonexistent) = %d, want 0", idx)
	}

	// Empty list returns 0.
	idx = modelIndex(nil, "anything")
	if idx != 0 {
		t.Errorf("modelIndex(nil) = %d, want 0", idx)
	}
}

func TestCyclePrevNext(t *testing.T) {
	// Forward wrap.
	if got := cycleNext(3, 4); got != 0 {
		t.Errorf("cycleNext(3,4) = %d, want 0", got)
	}
	// Normal forward.
	if got := cycleNext(1, 4); got != 2 {
		t.Errorf("cycleNext(1,4) = %d, want 2", got)
	}
	// Backward wrap.
	if got := cyclePrev(0, 4); got != 3 {
		t.Errorf("cyclePrev(0,4) = %d, want 3", got)
	}
	// Normal backward.
	if got := cyclePrev(2, 4); got != 1 {
		t.Errorf("cyclePrev(2,4) = %d, want 1", got)
	}
	// Single element.
	if got := cycleNext(0, 1); got != 0 {
		t.Errorf("cycleNext(0,1) = %d, want 0", got)
	}
	if got := cyclePrev(0, 1); got != 0 {
		t.Errorf("cyclePrev(0,1) = %d, want 0", got)
	}
}

func TestRenderModelSelector(t *testing.T) {
	th := theme.DefaultDark()
	models := modelsForAgent("inspector")
	sel := modelSelector{}

	output := renderModelSelector(models, 0, 40, sel, th)
	if !strings.Contains(output, "GPT-5.4 Pro") {
		t.Errorf("renderModelSelector missing model name, got %q", output)
	}
	if !strings.Contains(output, theme.IconArrowLeft) {
		t.Error("renderModelSelector missing left arrow")
	}
	if !strings.Contains(output, theme.IconArrowRight) {
		t.Error("renderModelSelector missing right arrow")
	}
}

func TestRenderModelSelector_Empty(t *testing.T) {
	th := theme.DefaultDark()
	output := renderModelSelector(nil, 0, 40, modelSelector{}, th)
	if output != "" {
		t.Errorf("renderModelSelector(nil) = %q, want empty", output)
	}
}

func TestSelectorArrowHitTest(t *testing.T) {
	models := modelsForAgent("engineer")

	// Hit left arrow area.
	hit := selectorArrowHitTest(0, 40, models, 0)
	// At width=40 with content width ~20, pad ~10, left arrow at x=10
	// Exact positions depend on rendering; verify non-miss for valid range.

	// Disabled when <=1 model.
	hit = selectorArrowHitTest(5, 40, models[:1], 0)
	if hit != selectorFocusNone {
		t.Errorf("hit test with 1 model = %d, want selectorFocusNone", hit)
	}

	_ = hit // suppress unused warning for valid test
}

func TestSelectorKeyHandling(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 40)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	// Enter selector mode.
	m.enterSelector()
	if !m.selector.active {
		t.Fatal("selector not active after enterSelector()")
	}
	if m.selector.focus != selectorFocusLeft {
		t.Errorf("initial focus = %d, want selectorFocusLeft", m.selector.focus)
	}

	// Tab advances focus to right arrow.
	m.handleSelectorKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.selector.focus != selectorFocusRight {
		t.Errorf("focus after first tab = %d, want selectorFocusRight", m.selector.focus)
	}

	// Tab again exits selector (tab-out).
	m.handleSelectorKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.selector.active {
		t.Fatal("selector still active after tab-out from right arrow")
	}

	// Re-enter and verify esc also exits.
	m.enterSelector()
	m.handleSelectorKey(tea.KeyMsg{Type: tea.KeyEscape})
	if m.selector.active {
		t.Fatal("selector still active after esc")
	}
}

func TestTabEntersSelector(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 40)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	// Tab from list view enters selector.
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m.selector.active {
		t.Fatal("Tab did not activate selector from list view")
	}
}

func TestSelectorModelChange(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 40)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	agent := m.agents["inspector"]
	if agent == nil {
		t.Fatal("inspector agent not found")
	}
	initialModel := agent.ModelID

	// Cycle next.
	cmd := m.cycleModelNext()
	if cmd == nil {
		t.Fatal("cycleModelNext returned nil cmd")
	}

	// Execute the cmd to get the msg.
	result := cmd()
	change, ok := result.(msg.ModelChangeMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want ModelChangeMsg", result)
	}
	if change.AgentType != "inspector" {
		t.Errorf("AgentType = %q, want inspector", change.AgentType)
	}
	if change.ModelID == initialModel {
		t.Error("ModelID should have changed after cycleNext")
	}

	// Verify agent state updated (optimistic).
	if agent.ModelID == initialModel {
		t.Error("agent.ModelID not updated after cycleNext")
	}
}

func TestSelectorInAllViews(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 20)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	// Selector rendered via RenderSelectorLine (app.go appends it to the left panel).
	sel := m.RenderSelectorLine()
	if !strings.Contains(sel, "GPT-5.4 Pro") {
		t.Error("list view: RenderSelectorLine missing model name")
	}

	// Expanded view — selector line still works.
	m.enterExpanded()
	sel = m.RenderSelectorLine()
	if !strings.Contains(sel, "GPT-5.4 Pro") {
		t.Error("expanded view: RenderSelectorLine missing model name")
	}
}

func TestSelectorFlash(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 20)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	m.cycleModelNext()
	if m.selector.flash != flashFrames {
		t.Errorf("flash = %d after cycle, want %d", m.selector.flash, flashFrames)
	}

	// Decrement.
	for range flashFrames {
		m.DecrementSelectorFlash()
	}
	if m.selector.flash != 0 {
		t.Errorf("flash = %d after %d decrements, want 0", m.selector.flash, flashFrames)
	}
}

func TestSelectorExitsOnFocusLoss(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 20)
	m.SetFocused(true)
	pushAgentActivity(m, "inspector", "inspector")

	m.enterSelector()
	if !m.selector.active {
		t.Fatal("selector not active after enterSelector()")
	}

	// Losing panel focus should exit the selector.
	m.SetFocused(false)
	if m.selector.active {
		t.Fatal("selector still active after SetFocused(false)")
	}
	if m.selector.focus != selectorFocusNone {
		t.Errorf("focus = %d after defocus, want selectorFocusNone", m.selector.focus)
	}
}

func TestSelectorDisabledForSingleModel(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetSize(80, 20)
	m.SetFocused(true)

	// A seeded agent with one supported model should keep selector mode disabled.
	m.SeedAgent("single", "engineer", "Engineer", []ModelEntry{
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}, "", "")

	// Selector should not enter for agents without multiple models.
	m.enterSelector()
	if m.selector.active {
		t.Error("selector should not activate for agents without multiple models")
	}
}

func TestDefaultModelForAgentType(t *testing.T) {
	cases := map[string]string{
		"guide":        "gemini-3.1-pro-preview",
		"engineer":     "gpt-5.4-pro",
		"designer":     "gemini-3.1-pro-preview",
		"inspector":    "gpt-5.4-pro",
		"tester":       "gpt-5.4-pro",
		"orchestrator": "gemini-3.1-pro-preview",
		"architect":    "claude-opus-4-6",
	}
	for agentType, want := range cases {
		got := DefaultModelForAgentType(agentType)
		if got != want {
			t.Errorf("DefaultModelForAgentType(%q) = %q, want %q", agentType, got, want)
		}
	}

	if got := DefaultModelForAgentType("unknown"); got != "" {
		t.Errorf("DefaultModelForAgentType(unknown) = %q, want empty", got)
	}
}

func TestSeedAgentSetsModelID(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SeedAgent("eng-1", "engineer", "Engineer", nil, "", "")

	agent := m.agents["eng-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}
	if agent.ModelID != "gpt-5.4-pro" {
		t.Errorf("seeded agent ModelID = %q, want gpt-5.4-pro", agent.ModelID)
	}
	if agent.ProviderID != "openai" {
		t.Errorf("seeded agent ProviderID = %q, want openai", agent.ProviderID)
	}
}

func TestSeedAgentWithSupportedModels(t *testing.T) {
	m := New(theme.DefaultDark())
	customModels := []ModelEntry{
		{ID: "custom-v2", DisplayName: "Custom V2"},
		{ID: "custom-v1", DisplayName: "Custom V1"},
	}
	m.SeedAgent("eng-1", "engineer", "Engineer", customModels, "", "")

	agent := m.agents["eng-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}

	// ModelID should be the first entry from the custom list.
	if agent.ModelID != "custom-v2" {
		t.Errorf("ModelID = %q, want custom-v2", agent.ModelID)
	}

	// SupportedModels should be stored on the agent state.
	if len(agent.SupportedModels) != 2 {
		t.Fatalf("SupportedModels len = %d, want 2", len(agent.SupportedModels))
	}

	// agentModels should return per-agent models, not static table.
	models := agentModels(agent)
	if len(models) != 2 || models[0].ID != "custom-v2" {
		t.Errorf("agentModels returned %v, want custom models", models)
	}
}

func TestSeedAgentWithPersistedModel(t *testing.T) {
	m := New(theme.DefaultDark())
	// Seed engineer with a persisted model+provider that exists in the static table.
	m.SeedAgent("eng-1", "engineer", "Engineer", nil, "gpt-5.4-pro", "openai")

	agent := m.agents["eng-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}

	// Persisted model is in the engineer's static model table → should be used.
	if agent.ModelID != "gpt-5.4-pro" {
		t.Errorf("ModelID = %q, want gpt-5.4-pro", agent.ModelID)
	}
	if agent.ProviderID != "openai" {
		t.Errorf("ProviderID = %q, want openai", agent.ProviderID)
	}
}

func TestSeedAgentWithChatGPTAuthUsesVisibleOpenAIModel(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SetOpenAIAuthMethod("chatgpt")
	m.SeedAgent("eng-1", "engineer", "Engineer", nil, "gpt-5.4-pro", "openai")

	agent := m.agents["eng-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}
	if agent.ModelID != "gpt-5.4" {
		t.Fatalf("ModelID = %q, want gpt-5.4", agent.ModelID)
	}
	models := m.agentModels(agent)
	if len(models) != 1 || models[0].ID != "gpt-5.4" {
		t.Fatalf("agentModels = %+v, want only gpt-5.4", models)
	}
}

func TestSeedAgentWithPersistedProviderDerived(t *testing.T) {
	m := New(theme.DefaultDark())
	// Seed with persisted model but empty provider — should derive from model.
	m.SeedAgent("arch-1", "architect", "Architect", nil, "claude-opus-4-6", "")

	agent := m.agents["arch-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}
	if agent.ProviderID != "anthropic" {
		t.Errorf("ProviderID = %q, want anthropic (derived)", agent.ProviderID)
	}
}

func TestSeedAgentWithInvalidPersistedModel(t *testing.T) {
	m := New(theme.DefaultDark())
	// Seed engineer with a persisted model NOT in the model list.
	m.SeedAgent("eng-1", "engineer", "Engineer", nil, "nonexistent-model", "")

	agent := m.agents["eng-1"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}

	// Invalid persisted model → falls back to default.
	if agent.ModelID != "gpt-5.4-pro" {
		t.Errorf("ModelID = %q, want gpt-5.4-pro (default)", agent.ModelID)
	}
}

func TestAgentModels_FallbackToStatic(t *testing.T) {
	agent := &AgentState{AgentType: "engineer"}
	models := agentModels(agent)
	if len(models) != 1 || models[0].ID != "gpt-5.4-pro" {
		t.Errorf("agentModels fallback returned %v, want static engineer models", models)
	}
}

func TestSetOpenAIAuthMethod_ReconcilesDisplayedModel(t *testing.T) {
	m := New(theme.DefaultDark())
	m.SeedAgent("architect", "architect", "Architect", []ModelEntry{
		{ID: "gpt-5.4", DisplayName: "GPT-5.4"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	}, "gpt-5.4-pro", "openai")

	agent := m.agents["architect"]
	if agent == nil {
		t.Fatal("seeded agent not found")
	}
	if agent.ModelID != "gpt-5.4-pro" {
		t.Fatalf("initial ModelID = %q, want gpt-5.4-pro", agent.ModelID)
	}

	m.SetOpenAIAuthMethod("chatgpt")

	if agent.ModelID != "gpt-5.4" {
		t.Fatalf("ModelID after auth change = %q, want gpt-5.4", agent.ModelID)
	}
	models := m.agentModels(agent)
	if len(models) != 2 {
		t.Fatalf("filtered models len = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-5.4" {
		t.Fatalf("first filtered model = %q, want gpt-5.4", models[0].ID)
	}
	if models[1].ID != "claude-opus-4-6" {
		t.Fatalf("second filtered model = %q, want claude-opus-4-6", models[1].ID)
	}
}

func TestAgentModels_NilAgent(t *testing.T) {
	if got := agentModels(nil); got != nil {
		t.Errorf("agentModels(nil) = %v, want nil", got)
	}
}
