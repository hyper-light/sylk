package agent

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

func TestRenderCard_ContextPctStaysPinnedRight(t *testing.T) {
	th := theme.DefaultDark()
	width := 64
	cases := []struct {
		name   string
		agent  AgentState
		anim   AnimState
		prefix string
	}{
		{
			name: "nerd-font search icon",
			agent: AgentState{
				ID:            "librarian",
				Name:          "Librarian",
				AgentType:     "librarian",
				Status:        StatusThinking,
				ActivityState: events.AgentUIStateSearching,
				TaskSummary:   "Searching the codebase for prior packaging patterns and conventions.",
				ContextUsage:  0.57,
			},
			anim: AnimState{NerdFonts: true},
		},
		{
			name: "guardian nerd-font icon",
			agent: AgentState{
				ID:            "guardian",
				Name:          "Guardian",
				AgentType:     "guardian",
				Status:        StatusThinking,
				ActivityState: events.AgentUIStateValidating,
				TaskSummary:   "Waiting on explicit user approval for an installation command.",
				ContextUsage:  0.42,
			},
			anim: AnimState{NerdFonts: true},
		},
		{
			name: "pipeline member prefix with nerd-font icon",
			agent: AgentState{
				ID:            "task_4:tester-pipeline",
				Name:          "Tester",
				AgentType:     "tester-pipeline",
				Status:        StatusThinking,
				ActivityState: events.AgentUIStateTestingRunning,
				TaskSummary:   "Running the next round of integration checks against the merged checkpoint.",
				ContextUsage:  0.83,
			},
			anim:   AnimState{NerdFonts: true},
			prefix: renderTreePrefix(pipelinePrefix, lipgloss.Color("#7D56F4"), th),
		},
		{
			name: "plain fallback icon",
			agent: AgentState{
				ID:            "architect",
				Name:          "Architect",
				AgentType:     "architect",
				Status:        StatusThinking,
				ActivityState: events.AgentUIStatePlanning,
				TaskSummary:   "Reworking the execution plan to preserve accepted work and version revisions.",
				ContextUsage:  1.0,
			},
			anim: AnimState{NerdFonts: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := RenderCard(tc.agent, width, th, false, false, tc.prefix, tc.anim)
			pct := formatContextPct(tc.agent.ContextUsage)
			plain := stripANSI(card)
			if !strings.HasSuffix(plain, pct) {
				t.Fatalf("card = %q, want suffix %q", plain, pct)
			}
			prefix := strings.TrimSuffix(plain, pct)
			if got := displayWidth(prefix, tc.anim.NerdFonts); got != width-contextBarWidth {
				t.Fatalf("prefix display width = %d, want %d for card %q", got, width-contextBarWidth, plain)
			}
			if got := displayWidth(card, tc.anim.NerdFonts); got != width {
				t.Fatalf("card display width = %d, want %d for card %q", got, width, plain)
			}
		})
	}
}

func TestRenderCard_EmojiSummaryStillFitsWidth(t *testing.T) {
	th := theme.DefaultDark()
	width := 48
	agent := AgentState{
		ID:            "archivalist",
		Name:          "Archivalist",
		AgentType:     "archivalist",
		Status:        StatusThinking,
		ActivityState: events.AgentUIStateSearching,
		TaskSummary:   "Searching historical decisions 📚 for prior packaging failures.",
		ContextUsage:  0.33,
	}

	card := RenderCard(agent, width, th, false, false, "", AnimState{NerdFonts: true})
	if got := displayWidth(card, true); got != width {
		t.Fatalf("card display width = %d, want %d for %q", got, width, stripANSI(card))
	}
}
