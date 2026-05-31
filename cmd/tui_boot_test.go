package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/charmbracelet/x/ansi"
)

func TestBootstrapProgressReporterStoresCompletionAndStopsAcceptingProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reporter := newBootstrapProgressReporter(ctx)
	reporter.ReportStage("infrastructure", 0)

	cleanup := func() error { return nil }
	reporter.Complete(bootstrapRunResult{cleanup: cleanup})
	result, ok := reporter.Result()
	if !ok {
		t.Fatal("Result ok = false, want true")
	}
	if result.cleanup == nil {
		t.Fatal("Result cleanup = nil, want retained cleanup")
	}

	assertBootstrapProgressEvent(t, reporter.events, bootstrapProgressStage)
	assertBootstrapProgressEvent(t, reporter.events, bootstrapProgressComplete)

	reporter.ReportStage("after complete", 1)
	select {
	case event := <-reporter.events:
		t.Fatalf("unexpected event after completion: %+v", event)
	case <-time.After(10 * time.Millisecond):
	}
}

func TestBootstrapBootModelCountsClaimsProjectionProgress(t *testing.T) {
	model := newBootstrapBootModel(context.Background(), nil, nil)
	model.health = boot.BootHealth{Phases: []boot.BootPhaseHealth{
		{Phase: boot.BootPhaseDurableSubstrate, Outcome: string(claims.ClaimLifecycleSatisfied)},
		{Phase: boot.BootPhaseSystemParticipants, Outcome: string(claims.ClaimLifecycleValidationFailed)},
		{Phase: boot.BootPhaseKnowledgeBackends, Outcome: "missing"},
	}}

	done, total := model.progressCounts()
	if done != 1 || total != 3 {
		t.Fatalf("progressCounts = %d/%d, want 1/3", done, total)
	}

	view := model.View()
	if !strings.Contains(view, "durable substrate") || !strings.Contains(view, "failed") {
		t.Fatalf("View() = %q, want claims phase rows with failed outcome", view)
	}
}

func TestBootstrapBootModelViewUsesThemedFrameAndHidesPendingWarnings(t *testing.T) {
	model := newBootstrapBootModel(context.Background(), nil, nil)
	model.width = 100
	model.height = 30
	model.health = boot.BootHealth{Phases: []boot.BootPhaseHealth{
		{Phase: boot.BootPhaseDurableSubstrate, Outcome: string(claims.ClaimLifecycleSatisfied)},
		{Phase: boot.BootPhaseKnowledgeBackends, Outcome: "missing", Warnings: []string{"phase claim missing"}},
	}}

	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╯") {
		t.Fatalf("View() = %q, want rounded themed frame", view)
	}
	if strings.Contains(view, "phase claim missing") {
		t.Fatalf("View() = %q, pending claim warnings should not be rendered in boot splash", view)
	}
}

func TestBootstrapBootModelViewCoversFullViewport(t *testing.T) {
	model := newBootstrapBootModel(context.Background(), nil, nil)
	model.width = 96
	model.height = 24
	model.health = boot.BootHealth{Phases: []boot.BootPhaseHealth{
		{Phase: boot.BootPhaseDurableSubstrate, Outcome: string(claims.ClaimLifecycleSatisfied)},
	}}

	lines := strings.Split(model.View(), "\n")
	if len(lines) != model.height {
		t.Fatalf("View height = %d, want %d", len(lines), model.height)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != model.width {
			t.Fatalf("View line %d width = %d, want %d", i, got, model.width)
		}
	}
}

func TestBootstrapBootModelViewUsesTerminalSurfaceBackground(t *testing.T) {
	model := newBootstrapBootModel(context.Background(), nil, nil)
	model.width = 96
	model.height = 24
	model.health = boot.BootHealth{Phases: []boot.BootPhaseHealth{
		{Phase: boot.BootPhaseDurableSubstrate, Outcome: string(claims.ClaimLifecycleSatisfied)},
	}}

	view := model.View()
	if strings.Contains(view, "\x1b[48;2;") {
		t.Fatalf("View() paints explicit truecolor backgrounds; boot surface should inherit the main TUI terminal background")
	}
}

func TestFitBootstrapTextBoundsOutputWidth(t *testing.T) {
	got := fitBootstrapText("abcdefghijklmnopqrstuvwxyz", 8)
	if len([]rune(got)) != 8 {
		t.Fatalf("fitBootstrapText length = %d, want 8", len([]rune(got)))
	}
	if got == "abcdefghijklmnopqrstuvwxyz" {
		t.Fatal("fitBootstrapText did not truncate over-width text")
	}
}

func assertBootstrapProgressEvent(t *testing.T, events <-chan bootstrapProgressEvent, want bootstrapProgressKind) {
	t.Helper()
	select {
	case event := <-events:
		if event.kind != want {
			t.Fatalf("event kind = %d, want %d", event.kind, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for event kind %d", want)
	}
}
