package agent

import (
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Selector state
// ---------------------------------------------------------------------------

// selectorFocus tracks which arrow has keyboard focus in the model selector.
type selectorFocus int

const (
	selectorFocusNone  selectorFocus = iota
	selectorFocusLeft                // Left arrow has focus.
	selectorFocusRight               // Right arrow has focus.
)

// ModelEntry describes a single LLM model available for selection.
type ModelEntry struct {
	ID          string // Provider model ID (e.g. "claude-opus-4-6").
	DisplayName string // Human-friendly name (e.g. "Claude Opus 4.6").
}

// modelSelector holds the transient UI state for the model toggle widget.
type modelSelector struct {
	active     bool          // Selector mode entered; intercepts keys.
	focus      selectorFocus // Which arrow has keyboard focus.
	hoverLeft  bool          // Mouse hovering over left arrow.
	hoverRight bool          // Mouse hovering over right arrow.
	flash      int           // >0 = bold flash frames remaining after model change.
}

// flashFrames is the number of animation frames the model name stays bold
// after a model change. At ~10 fps decor tick rate this yields ~0.3s flash.
const flashFrames = 3

// ---------------------------------------------------------------------------
// Static data tables (UI layer — no import cycle with core/providers)
// ---------------------------------------------------------------------------

// agentModelTable maps agent type directly to its available models.
// Each agent can mix models from different providers.
var agentModelTable = map[string][]ModelEntry{
	"guide": {
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	},
	"engineer": {
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	},
	"designer": {
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash"},
	},
	"inspector": {
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	},
	"tester": {
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	},
	"orchestrator": {
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	},
	"architect": {
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	},
	"librarian": {
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
	},
	"archivalist": {
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
	},
	"academic": {
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
	},
}

// DefaultModelForAgentType returns the default model ID for the given agent type.
// Returns "" for unknown agent types. Exported for use by app.go on swap revert.
func DefaultModelForAgentType(agentType string) string {
	return defaultModelForAgent(agentType)
}

// defaultModelForAgent returns the default model ID for the given agent type.
// Returns "" for unknown agent types.
func defaultModelForAgent(agentType string) string {
	models := modelsForAgent(agentType)
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

// ---------------------------------------------------------------------------
// Pure functions
// ---------------------------------------------------------------------------

// agentModels returns the raw model list for an agent. Prefers the per-agent
// SupportedModels list when populated; falls back to the static provider table.
func agentModels(agent *AgentState) []ModelEntry {
	if agent == nil {
		return nil
	}
	if len(agent.SupportedModels) > 0 {
		return agent.SupportedModels
	}
	return modelsForAgent(agent.AgentType)
}

func modelsForAgentForAuth(agentType, openAIAuthMethod string) []ModelEntry {
	return authAwareModelEntries(modelsForAgent(agentType), openAIAuthMethod)
}

func agentModelsForAuth(agent *AgentState, openAIAuthMethod string) []ModelEntry {
	if agent == nil {
		return nil
	}
	if len(agent.SupportedModels) > 0 {
		return authAwareModelEntries(agent.SupportedModels, openAIAuthMethod)
	}
	return modelsForAgentForAuth(agent.AgentType, openAIAuthMethod)
}

// modelsForAgent returns the static model list for the given agent type, or nil.
// Used as a fallback for dynamically discovered agents without SupportedModels.
func modelsForAgent(agentType string) []ModelEntry {
	return agentModelTable[agentType]
}

func authAwareModelEntries(entries []ModelEntry, openAIAuthMethod string) []ModelEntry {
	if credentials.CanonicalAuthMethod("openai", openAIAuthMethod) != "chatgpt" {
		return entries
	}
	filtered := make([]ModelEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		mappedID := resolveModelForOpenAIAuth(entry.ID, openAIAuthMethod)
		displayName := entry.DisplayName
		if mappedID == string(providers.GPT_5_4) && entry.ID != mappedID {
			displayName = "GPT-5.4"
		}
		if _, ok := seen[mappedID]; ok {
			continue
		}
		filtered = append(filtered, ModelEntry{
			ID:          mappedID,
			DisplayName: displayName,
		})
		seen[mappedID] = struct{}{}
	}
	return filtered
}

func resolveModelForOpenAIAuth(modelID, openAIAuthMethod string) string {
	if deriveProvider(modelID) != "openai" {
		return modelID
	}
	return providers.ResolveOpenAIModelForAuth(modelID, openAIAuthMethod)
}

// modelIndex returns the index of currentID in models, or 0 if not found.
func modelIndex(models []ModelEntry, currentID string) int {
	for i, m := range models {
		if m.ID == currentID {
			return i
		}
	}
	return 0
}

// cyclePrev returns the previous index with wraparound.
func cyclePrev(current, count int) int {
	if count <= 1 {
		return 0
	}
	return (current - 1 + count) % count
}

// cycleNext returns the next index with wraparound.
func cycleNext(current, count int) int {
	if count <= 1 {
		return 0
	}
	return (current + 1) % count
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// selectorLineCount is the number of lines the selector occupies.
const selectorLineCount = 1

// renderModelSelector renders a centered single-line model selector:
//
//	< Claude Opus 4.6 >
func renderModelSelector(models []ModelEntry, currentIdx, width int, sel modelSelector, th *theme.Theme) string {
	if len(models) == 0 {
		return ""
	}

	disabled := len(models) <= 1

	// Arrow styles.
	leftColor := arrowColor(selectorFocusLeft, sel, disabled, th)
	rightColor := arrowColor(selectorFocusRight, sel, disabled, th)

	leftStyle := lipgloss.NewStyle().Foreground(leftColor)
	rightStyle := lipgloss.NewStyle().Foreground(rightColor)
	if sel.active && sel.focus == selectorFocusLeft {
		leftStyle = leftStyle.Bold(true)
	}
	if sel.active && sel.focus == selectorFocusRight {
		rightStyle = rightStyle.Bold(true)
	}
	leftArrow := leftStyle.Render(theme.IconArrowLeft)
	rightArrow := rightStyle.Render(theme.IconArrowRight)

	// Model name.
	name := models[clampIndex(currentIdx, len(models))].DisplayName
	nameStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	if sel.active || sel.flash > 0 {
		nameStyle = nameStyle.Bold(true)
	}
	nameStr := nameStyle.Render(name)

	content := leftArrow + " " + nameStr + " " + rightArrow
	contentWidth := lipgloss.Width(content)

	if contentWidth >= width {
		return content
	}
	pad := (width - contentWidth) / 2
	return lipgloss.NewStyle().PaddingLeft(pad).Render(content)
}

// arrowColor returns the color for an arrow based on selector state.
func arrowColor(side selectorFocus, sel modelSelector, disabled bool, th *theme.Theme) lipgloss.Color {
	if disabled {
		return th.Palette.Subtle
	}
	if sel.active && sel.focus == side {
		return th.Palette.HoverAccent
	}
	if (side == selectorFocusLeft && sel.hoverLeft) || (side == selectorFocusRight && sel.hoverRight) {
		return th.Palette.HoverAccentDim
	}
	return th.Palette.Muted
}

// ---------------------------------------------------------------------------
// Hit testing
// ---------------------------------------------------------------------------

// selectorArrowHitTest determines which arrow (if any) was clicked at position x
// within a line of the given width. Returns selectorFocusNone on miss.
func selectorArrowHitTest(x, width int, models []ModelEntry, currentIdx int) selectorFocus {
	if len(models) <= 1 {
		return selectorFocusNone
	}

	name := models[clampIndex(currentIdx, len(models))].DisplayName
	// Layout: pad + leftArrow(1) + space(1) + name + space(1) + rightArrow(1)
	arrowW := 1
	nameW := lipgloss.Width(name)
	contentW := arrowW + 1 + nameW + 1 + arrowW
	pad := max((width-contentW)/2, 0)

	leftStart := pad
	leftEnd := leftStart + arrowW
	rightStart := leftEnd + 1 + nameW + 1
	rightEnd := rightStart + arrowW

	if x >= leftStart && x < leftEnd {
		return selectorFocusLeft
	}
	if x >= rightStart && x < rightEnd {
		return selectorFocusRight
	}
	return selectorFocusNone
}
