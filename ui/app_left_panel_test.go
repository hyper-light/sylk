package ui

import "testing"

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
