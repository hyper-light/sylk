package ui

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestComputeLeftPanelSections_AgentsExpandToSelector(t *testing.T) {
	sections := computeLeftPanelSections(40, 30, 1, 1)

	if sections.agentsRect.H <= sections.sessionRect.H {
		t.Fatalf("expected agents rect to expand beyond minimal session height: session=%d agents=%d", sections.sessionRect.H, sections.agentsRect.H)
	}
	if got, want := sections.agentsRect.Y+sections.agentsRect.H, sections.selectorY; got != want {
		t.Fatalf("agents rect bottom = %d, want selectorY %d", got, want)
	}
}

func TestComputeLeftPanelSections_ReservesMinimumAgentHeight(t *testing.T) {
	sections := computeLeftPanelSections(40, 20, 1, 20)

	if sections.agentsRect.H < minAgentSectionHeight {
		t.Fatalf("agent height = %d, want >= %d", sections.agentsRect.H, minAgentSectionHeight)
	}
}

func TestPadLeftPanelBody_ClipsAndPadsToExactHeight(t *testing.T) {
	if got := len(strings.Split(padLeftPanelBody("a\nb\nc\nd", 3), "\n")); got != 3 {
		t.Fatalf("clipped line count = %d, want 3", got)
	}
	if got := len(strings.Split(padLeftPanelBody("a", 3), "\n")); got != 3 {
		t.Fatalf("padded line count = %d, want 3", got)
	}
}

func TestComposeLeftPanelContent_ClampsSectionsBeforeStacking(t *testing.T) {
	sections := computeLeftPanelSections(40, 16, 1, 3)
	sessionContent := strings.Join([]string{
		"session-1",
		"session-2",
		"session-3",
		"session-4",
		"session-5",
		"session-6",
	}, "\n")
	agentContent := strings.Join([]string{
		"agent-1",
		"agent-2",
		"agent-3",
		"agent-4",
		"agent-5",
		"agent-6",
		"agent-7",
		"agent-8",
	}, "\n")

	rendered := stripANSI(composeLeftPanelContent(
		39,
		15,
		sections,
		sessionContent,
		agentContent,
		false,
		false,
		"SELECTOR",
		1,
		theme.DefaultDark(),
	))
	lines := strings.Split(rendered, "\n")

	if got, want := len(lines), 15; got != want {
		t.Fatalf("rendered line count = %d, want %d", got, want)
	}
	if !strings.Contains(rendered, "Sessions") {
		t.Fatalf("expected Sessions header to remain visible, got %q", rendered)
	}
	if !strings.Contains(rendered, "Agents") {
		t.Fatalf("expected Agents header to remain visible, got %q", rendered)
	}
	if !strings.Contains(rendered, "agent-1") {
		t.Fatalf("expected agent content to remain visible, got %q", rendered)
	}
	if got := lines[len(lines)-1]; got != "SELECTOR" {
		t.Fatalf("selector line = %q, want %q", got, "SELECTOR")
	}
}

func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if i < len(s)-1 && s[i] == '\033' && s[i+1] == '[' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
