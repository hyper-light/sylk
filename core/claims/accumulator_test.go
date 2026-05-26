package claims

import (
	"context"
	"testing"
)

func TestTestamentAccumulatorFlushBlockingSubmitsBeforeReturn(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "board-acc-flush-blocking",
		SessionID: "sess-acc-flush-blocking",
		TaskID:    "task-acc-flush-blocking",
	})
	_ = board.Projection()

	acc := NewTestamentAccumulator("architect", "sess-acc-flush-blocking")
	acc.RecordResponseText("Plan ready.")
	if err := acc.FlushBlocking(context.Background(), board); err != nil {
		t.Fatalf("FlushBlocking returned error: %v", err)
	}

	proj := board.Projection()
	if proj.TotalTestaments != 1 {
		t.Fatalf("TotalTestaments = %d, want 1", proj.TotalTestaments)
	}
	if proj.TotalArtifacts != 2 {
		t.Fatalf("TotalArtifacts = %d, want response_text + duration", proj.TotalArtifacts)
	}
	if got := proj.Testaments[0].Summary; got != "Plan ready." {
		t.Fatalf("Summary = %q, want response text", got)
	}
	var response *Artifact
	for _, artifact := range proj.Testaments[0].Artifacts {
		if artifact != nil && artifact.Kind == ArtifactKindResponseText {
			response = artifact
			break
		}
	}
	if response == nil {
		t.Fatal("response_text artifact missing")
	}
	if !PresentationMatches(response.Presentation, string(PresentationAudienceUser), string(PresentationSurfaceChat)) {
		t.Fatalf("response_text missing default user/chat presentation: %+v", response.Presentation)
	}
	if response.Presentation.Placement != PresentationPlacementAfterResponse || response.Presentation.Format != PresentationFormatMarkdown {
		t.Fatalf("response_text default presentation = %+v", response.Presentation)
	}
}
