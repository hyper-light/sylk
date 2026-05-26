package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func newChatForPresentationTest(t *testing.T) *Model {
	t.Helper()
	return New(theme.DefaultDark(), 32)
}

func TestClaimPresentation_RendersMarkdownAndPreservesSource(t *testing.T) {
	m := newChatForPresentationTest(t)

	m.Update(msg.ClaimPresentationMsg{
		SessionID:   "ses-1",
		CycleID:     "cycle-1",
		SourceType:  "artifact",
		SourceID:    "artifact-plan",
		TestamentID: "testament-1",
		AgentID:     "architect",
		Content:     "### Plan\n\n1. Build it.",
		Format:      "markdown",
		Placement:   "inline",
		CreatedAt:   time.Now(),
		Sequence:    1,
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("expected presentation entry")
	}
	if !strings.Contains(entry.Content, "### Plan") {
		t.Fatalf("entry.Content = %q, want markdown plan", entry.Content)
	}
	if entry.PresentationSourceID != "artifact-plan" {
		t.Fatalf("PresentationSourceID = %q, want artifact-plan", entry.PresentationSourceID)
	}
	if entry.PresentationTestamentID != "testament-1" {
		t.Fatalf("PresentationTestamentID = %q, want testament-1", entry.PresentationTestamentID)
	}
}

func TestClaimPresentation_ReplacesByKey(t *testing.T) {
	m := newChatForPresentationTest(t)

	m.Update(msg.ClaimPresentationMsg{
		SourceType: "artifact",
		SourceID:   "old",
		AgentID:    "architect",
		Content:    "old plan",
		Format:     "markdown",
		Placement:  "inline",
		ReplaceKey: "plan:p1:review",
		Sequence:   1,
	})
	m.Update(msg.ClaimPresentationMsg{
		SourceType: "artifact",
		SourceID:   "new",
		AgentID:    "architect",
		Content:    "new plan",
		Format:     "markdown",
		Placement:  "inline",
		ReplaceKey: "plan:p1:review",
		Sequence:   2,
	})

	if m.history.Len() != 1 {
		t.Fatalf("history len = %d, want 1", m.history.Len())
	}
	entry := m.history.Get(0)
	if entry == nil || entry.Content != "new plan" {
		t.Fatalf("entry = %+v, want replacement content", entry)
	}
	if entry.PresentationSourceID != "new" {
		t.Fatalf("PresentationSourceID = %q, want new", entry.PresentationSourceID)
	}
}

func TestClaimPresentation_UnknownFormatRendersAsPlainText(t *testing.T) {
	m := newChatForPresentationTest(t)

	m.Update(msg.ClaimPresentationMsg{
		SourceType: "artifact",
		SourceID:   "plain",
		AgentID:    "architect",
		Content:    "# not a heading",
		Format:     "future-format",
		Placement:  "inline",
		Sequence:   1,
	})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("expected presentation entry")
	}
	if entry.Content != `\# not a heading` {
		t.Fatalf("entry.Content = %q, want escaped plain text", entry.Content)
	}
}

func TestClaimPresentation_ActiveStreamBeforeResponseFinalizesOnce(t *testing.T) {
	m := newChatForPresentationTest(t)
	start := msg.StreamStartMsg{
		SessionID:     "ses-1",
		CorrelationID: "cycle-stream",
		AgentID:       "architect",
		AgentType:     "architect",
	}
	m.handleStreamStart(start)
	m.handleStreamChunk(msg.StreamChunkMsg{CorrelationID: "cycle-stream", Text: "Assessment prose."})

	m.Update(msg.ClaimPresentationMsg{
		SessionID:  "ses-1",
		CycleID:    "cycle-stream",
		SourceType: "artifact",
		SourceID:   "plan-artifact",
		AgentID:    "architect",
		Content:    "### Plan\n\n1. Build it.",
		Format:     "markdown",
		Placement:  "before_response",
		ReplaceKey: "plan:p1:review",
		Metadata:   map[string]any{"plan_id": "p1"},
		Sequence:   1,
	})
	m.handleStreamComplete(msg.StreamCompleteMsg{CorrelationID: "cycle-stream"})

	entry := m.history.Get(0)
	if entry == nil {
		t.Fatal("expected stream entry")
	}
	if strings.Count(entry.Content, "### Plan") != 1 {
		t.Fatalf("entry.Content = %q, want one embedded plan", entry.Content)
	}
	if !strings.Contains(entry.Content, "### Plan\n\n1. Build it.\n\nAssessment prose.") {
		t.Fatalf("entry.Content = %q, want plan before prose", entry.Content)
	}
}
