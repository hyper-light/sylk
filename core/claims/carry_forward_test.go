package claims

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCarryForwardPreviewSelectsDurableSourcesAndSkipsNoise(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Repository already has a Click CLI pattern", []*Artifact{
		{AgentID: "librarian", Kind: ArtifactKindResponseText, Reference: "Use the existing Python Click CLI pattern in cmd/hello."},
		{AgentID: "librarian", Kind: ArtifactKindAgentState, Reference: "Reasoning"},
		{AgentID: "librarian", Kind: ArtifactKindError, Reference: "Prior attempt failed because pyproject entry point was missing."},
	})

	before := board.Projection()
	result, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID:    "architect",
		Topic:      "python cli plan",
		Mode:       "preview",
		MaxSources: 4,
	})
	if err != nil {
		t.Fatalf("CarryForward preview failed: %v", err)
	}
	if result.Mutated {
		t.Fatal("preview mutated the board")
	}
	if result.SourceCount != 2 {
		t.Fatalf("SourceCount = %d, want 2; sources=%+v", result.SourceCount, result.Sources)
	}
	if hasForwardSourceKind(result.Sources, ArtifactKindAgentState) {
		t.Fatalf("agent_state noise was carried: %+v", result.Sources)
	}
	if !hasForwardSourceKind(result.Sources, ArtifactKindResponseText) {
		t.Fatalf("response_text source missing: %+v", result.Sources)
	}
	if !hasForwardSourceKind(result.Sources, ArtifactKindError) {
		t.Fatalf("error artifact source missing: %+v", result.Sources)
	}
	after := board.Projection()
	if after.TotalTestaments != before.TotalTestaments || after.TotalClaims != before.TotalClaims {
		t.Fatalf("preview changed projection counts: before=%+v after=%+v", before, after)
	}
}

func TestCarryForwardAdvanceWritesContinuityAndRecallReadsIt(t *testing.T) {
	board := carryForwardTestBoard()
	source := submitCarrySource(t, board, "librarian", "Librarian found existing CLI shape", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "pyproject.toml already declares console_scripts style entry points."},
	})

	result, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
		PlanID:  "plan-1",
	})
	if err != nil {
		t.Fatalf("CarryForward failed: %v", err)
	}
	if !result.Mutated || result.ClaimID == "" || result.TestamentID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	claim, ok := board.CloneClaim(result.ClaimID)
	if !ok {
		t.Fatalf("continuity claim %q not found", result.ClaimID)
	}
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("continuity claim status = %s, want accepted", claim.Status)
	}
	continuity, ok := board.CloneTestament(result.TestamentID)
	if !ok {
		t.Fatalf("continuity testament %q not found", result.TestamentID)
	}
	for _, kind := range []string{
		ArtifactKindWorkingContext,
		ArtifactKindEvidenceDigest,
		ArtifactKindSourceIndex,
		ArtifactKindContinuityCursor,
		ArtifactKindSessionCursor,
	} {
		if !carryForwardTestamentHasArtifactKind(continuity, kind) {
			t.Fatalf("continuity testament missing artifact kind %q: %+v", kind, continuity.Artifacts)
		}
	}
	if !HasRelation(continuity.Relations, RelationshipDerivedFrom, source.Artifacts[0].ID) {
		t.Fatalf("continuity testament missing derived_from artifact relation to %s: %+v", source.Artifacts[0].ID, continuity.Relations)
	}

	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:        "architect",
		Topic:          "python cli plan",
		IncludeSources: "source_index",
	})
	if err != nil {
		t.Fatalf("RecallForward failed: %v", err)
	}
	if recall.Partial {
		t.Fatalf("recall unexpectedly partial: %+v", recall.Diagnostics)
	}
	if len(recall.Items) != 1 {
		t.Fatalf("recall items = %d, want 1", len(recall.Items))
	}
	if !strings.Contains(recall.WorkingContext, "pyproject.toml") {
		t.Fatalf("working context did not include source digest: %q", recall.WorkingContext)
	}
	if len(recall.Sources) != 1 || recall.Sources[0].ArtifactID != source.Artifacts[0].ID {
		t.Fatalf("recall sources = %+v, want source artifact %s", recall.Sources, source.Artifacts[0].ID)
	}
}

func TestCarryForwardIdempotentWithoutNewSourcesAndSupersedesPriorWhenRequested(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Initial repository context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Initial CLI context."},
	})
	first, err := CarryForward(context.Background(), board, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("first carry failed: %v", err)
	}
	second, err := CarryForward(context.Background(), board, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("second carry failed: %v", err)
	}
	if second.Mutated {
		t.Fatalf("second carry without new sources mutated: %+v", second)
	}

	submitCarrySource(t, board, "tester", "Test evidence", []*Artifact{
		{AgentID: "tester", Kind: "test_output", Reference: "pytest passes for hello CLI."},
	})
	third, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID:        "architect",
		Topic:          "python cli plan",
		SupersedePrior: true,
	})
	if err != nil {
		t.Fatalf("third carry failed: %v", err)
	}
	if !third.Mutated || third.TestamentID == first.TestamentID {
		t.Fatalf("third carry did not create replacement: first=%+v third=%+v", first, third)
	}
	replacement, ok := board.CloneTestament(third.TestamentID)
	if !ok {
		t.Fatalf("replacement testament not found")
	}
	if !HasRelation(replacement.Relations, RelationshipSupersedes, first.TestamentID) {
		t.Fatalf("replacement does not supersede first: %+v", replacement.Relations)
	}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(recall.Items) != 1 || recall.Items[0].TestamentID != third.TestamentID {
		t.Fatalf("recall did not choose latest non-superseded item: %+v", recall.Items)
	}
}

func TestRecallForwardAmendsChainReturnsCumulativeContinuity(t *testing.T) {
	board := carryForwardTestBoard()
	firstSource := submitCarrySource(t, board, "librarian", "Initial repository context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Initial CLI context used Click."},
	})
	first, err := CarryForward(context.Background(), board, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("first carry failed: %v", err)
	}
	secondSource := submitCarrySource(t, board, "tester", "Test evidence", []*Artifact{
		{AgentID: "tester", Kind: "test_output", Reference: "pytest validates the Click command output."},
	})
	second, err := CarryForward(context.Background(), board, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("second carry failed: %v", err)
	}
	if second.TestamentID == first.TestamentID {
		t.Fatalf("second carry reused first testament: first=%+v second=%+v", first, second)
	}

	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:        "architect",
		Topic:          "python cli plan",
		IncludeSources: "source_index",
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if recall.Partial {
		t.Fatalf("recall unexpectedly partial: %+v", recall.Diagnostics)
	}
	if len(recall.Items) != 2 {
		t.Fatalf("recall items = %d, want latest + amended prior: %+v", len(recall.Items), recall.Items)
	}
	if recall.Items[0].TestamentID != second.TestamentID || recall.Items[1].TestamentID != first.TestamentID {
		t.Fatalf("recall order = %+v, want newest-to-oldest %s then %s", recall.Items, second.TestamentID, first.TestamentID)
	}
	if !hasForwardSourceArtifact(recall.Sources, firstSource.Artifacts[0].ID) ||
		!hasForwardSourceArtifact(recall.Sources, secondSource.Artifacts[0].ID) {
		t.Fatalf("recall sources missing amended chain sources: %+v", recall.Sources)
	}
	if !strings.Contains(recall.WorkingContext, "Initial CLI context") ||
		!strings.Contains(recall.WorkingContext, "pytest validates") {
		t.Fatalf("working context is not cumulative: %q", recall.WorkingContext)
	}
}

func TestRecallForwardUnknownTopicReturnsEmptySuccess(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Unrelated context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Unrelated evidence."},
	})
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID: "architect",
		Topic:   "missing topic",
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if recall.Partial || len(recall.Items) != 0 || len(recall.Sources) != 0 {
		t.Fatalf("unknown topic recall = partial=%v items=%d sources=%d diagnostics=%+v", recall.Partial, len(recall.Items), len(recall.Sources), recall.Diagnostics)
	}
}

func TestCarryForwardConcurrentSameTopicProducesSingleContinuityNode(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Concurrent context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "One durable source for concurrent carry."},
	})
	const workers = 8
	var wg sync.WaitGroup
	results := make(chan *CarryForwardResult, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := CarryForward(context.Background(), board, CarryForwardOptions{
				AgentID: "architect",
				Topic:   "python cli plan",
			})
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent carry failed: %v", err)
	}
	mutated := 0
	for res := range results {
		if res.Mutated {
			mutated++
		}
	}
	if mutated != 1 {
		t.Fatalf("mutated carries = %d, want 1", mutated)
	}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(recall.Items) != 1 {
		t.Fatalf("continuity nodes = %d, want 1: %+v", len(recall.Items), recall.Items)
	}
}

func TestCarryForwardSourceSelectionHonorsMaxSources(t *testing.T) {
	board := carryForwardTestBoard()
	for _, ref := range []string{
		"Workspace fact one for Python CLI planning.",
		"Workspace fact two for Python CLI planning.",
		"Workspace fact three for Python CLI planning.",
	} {
		submitCarrySource(t, board, "librarian", ref, []*Artifact{
			{AgentID: "librarian", Kind: "workspace_read", Reference: ref},
		})
	}
	result, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID:    "architect",
		Topic:      "python cli planning",
		Mode:       "preview",
		MaxSources: 2,
	})
	if err != nil {
		t.Fatalf("carry preview failed: %v", err)
	}
	if result.SourceCount != 2 || len(result.Sources) != 2 {
		t.Fatalf("selected sources = count %d len %d, want 2: %+v", result.SourceCount, len(result.Sources), result.Sources)
	}
}

func TestCarryForwardSkipsAlreadyIncorporatedDigestUnlessRevalidated(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Initial duplicate digest source", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "The CLI should keep the existing Click command shape."},
	})
	first, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli planning",
	})
	if err != nil {
		t.Fatalf("first carry failed: %v", err)
	}

	submitCarrySource(t, board, "librarian", "Duplicate duplicate digest source", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "The CLI should keep the existing Click command shape."},
	})
	duplicate, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli planning",
	})
	if err != nil {
		t.Fatalf("duplicate carry failed: %v", err)
	}
	if duplicate.Mutated {
		t.Fatalf("duplicate digest carried again: first=%+v duplicate=%+v", first, duplicate)
	}

	submitCarrySource(t, board, "librarian", "Revalidated duplicate digest source", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "The CLI should keep the existing Click command shape.", Metadata: map[string]any{"status": "revalidated"}},
	})
	revalidated, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli planning",
	})
	if err != nil {
		t.Fatalf("revalidated carry failed: %v", err)
	}
	if !revalidated.Mutated || revalidated.TestamentID == first.TestamentID {
		t.Fatalf("revalidated duplicate was not carried as a new source: first=%+v revalidated=%+v", first, revalidated)
	}
}

func TestRecallForwardCrossSessionUsesSessionCursorAndReportsPartialWithoutOpener(t *testing.T) {
	previous := carryForwardTestBoardWithIDs("prev-board", "prev-session")
	submitCarrySource(t, previous, "librarian", "Previous session context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Previous session picked Click."},
	})
	prevCarry, err := CarryForward(context.Background(), previous, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatalf("previous carry failed: %v", err)
	}

	current := carryForwardTestBoardWithIDs("current-board", "current-session")
	submitCarrySource(t, current, "architect", "Current session context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current session kept the Click approach."},
	})
	_, err = CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "python cli plan",
		PreviousSessionID:             "prev-session",
		PreviousBoardID:               "prev-board",
		PreviousContinuityTestamentID: prevCarry.TestamentID,
	})
	if err != nil {
		t.Fatalf("current carry failed: %v", err)
	}

	partial, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "python cli plan",
		LookbackSessions: 1,
	})
	if err != nil {
		t.Fatalf("partial recall failed: %v", err)
	}
	if !partial.Partial || len(partial.Items) != 1 {
		t.Fatalf("recall without opener = partial=%v items=%d diagnostics=%+v", partial.Partial, len(partial.Items), partial.Diagnostics)
	}

	full, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "python cli plan",
		LookbackSessions: 1,
		IncludeSources:   "full",
		OpenBoard: func(_ context.Context, sessionID string) (*ClaimsBoard, func(), error) {
			if sessionID != "prev-session" {
				t.Fatalf("opened session %q, want prev-session", sessionID)
			}
			return previous, func() {}, nil
		},
	})
	if err != nil {
		t.Fatalf("full recall failed: %v", err)
	}
	if full.Partial {
		t.Fatalf("full recall unexpectedly partial: %+v", full.Diagnostics)
	}
	if len(full.Items) != 2 {
		t.Fatalf("full recall items = %d, want current + previous", len(full.Items))
	}
	if !strings.Contains(full.WorkingContext, "Previous session picked Click") {
		t.Fatalf("full recall missing previous context: %q", full.WorkingContext)
	}
}

func TestRecallForwardCrossSessionUsesExactPreviousContinuityTestament(t *testing.T) {
	previous := carryForwardTestBoardWithIDs("prev-board", "prev-session")
	submitCarrySource(t, previous, "librarian", "Old previous context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Old previous session selected argparse."},
	})
	oldPrev, err := CarryForward(context.Background(), previous, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("old previous carry failed: %v", err)
	}
	submitCarrySource(t, previous, "librarian", "New previous context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "New previous session selected Click."},
	})
	newPrev, err := CarryForward(context.Background(), previous, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("new previous carry failed: %v", err)
	}
	if oldPrev.TestamentID == newPrev.TestamentID {
		t.Fatalf("expected separate previous continuity nodes")
	}

	current := carryForwardTestBoardWithIDs("current-board", "current-session")
	submitCarrySource(t, current, "architect", "Current exact context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current session deliberately links to old previous context."},
	})
	if _, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "python cli plan",
		PreviousSessionID:             "prev-session",
		PreviousContinuityTestamentID: oldPrev.TestamentID,
	}); err != nil {
		t.Fatalf("current carry failed: %v", err)
	}
	recall, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "python cli plan",
		LookbackSessions: 1,
		OpenBoard: func(_ context.Context, sessionID string) (*ClaimsBoard, func(), error) {
			if sessionID != "prev-session" {
				t.Fatalf("opened session %q, want prev-session", sessionID)
			}
			return previous, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if recall.Partial {
		t.Fatalf("recall unexpectedly partial: %+v", recall.Diagnostics)
	}
	if !strings.Contains(recall.WorkingContext, "Old previous session selected argparse") {
		t.Fatalf("recall did not hydrate exact previous testament: %q", recall.WorkingContext)
	}
	if strings.Contains(recall.WorkingContext, "New previous session selected Click") {
		t.Fatalf("recall used latest previous session node instead of exact cursor: %q", recall.WorkingContext)
	}
}

func TestRecallForwardCrossSessionFollowsOldestCurrentAmendsNodeToPreviousSession(t *testing.T) {
	previous := carryForwardTestBoardWithIDs("prev-board", "prev-session")
	submitCarrySource(t, previous, "librarian", "Previous session context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Previous session selected Click."},
	})
	prevCarry, err := CarryForward(context.Background(), previous, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatalf("previous carry failed: %v", err)
	}

	current := carryForwardTestBoardWithIDs("current-board", "current-session")
	submitCarrySource(t, current, "architect", "Current first context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current session linked to previous Click evidence."},
	})
	firstCurrent, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "python cli plan",
		PreviousSessionID:             "prev-session",
		PreviousContinuityTestamentID: prevCarry.TestamentID,
	})
	if err != nil {
		t.Fatalf("first current carry failed: %v", err)
	}
	submitCarrySource(t, current, "tester", "Current second context", []*Artifact{
		{AgentID: "tester", Kind: "test_output", Reference: "Current session test evidence keeps Click viable."},
	})
	secondCurrent, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatalf("second current carry failed: %v", err)
	}
	if secondCurrent.TestamentID == firstCurrent.TestamentID {
		t.Fatalf("expected second current carry to amend first: first=%+v second=%+v", firstCurrent, secondCurrent)
	}

	recall, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "python cli plan",
		LookbackSessions: 1,
		OpenBoard: func(_ context.Context, sessionID string) (*ClaimsBoard, func(), error) {
			if sessionID != "prev-session" {
				t.Fatalf("opened session %q, want prev-session", sessionID)
			}
			return previous, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if recall.Partial {
		t.Fatalf("recall unexpectedly partial: %+v", recall.Diagnostics)
	}
	if len(recall.Items) != 3 {
		t.Fatalf("recall items = %d, want current latest + current amended + previous: %+v", len(recall.Items), recall.Items)
	}
	if recall.Items[0].TestamentID != secondCurrent.TestamentID ||
		recall.Items[1].TestamentID != firstCurrent.TestamentID ||
		recall.Items[2].TestamentID != prevCarry.TestamentID {
		t.Fatalf("recall order = %+v, want second current, first current, previous", recall.Items)
	}
	if !strings.Contains(recall.WorkingContext, "Previous session selected Click") {
		t.Fatalf("recall did not cross the session boundary from the amended current node: %q", recall.WorkingContext)
	}
}

func TestRecallForwardCrossSessionBrokenExactLinkIsPartial(t *testing.T) {
	previous := carryForwardTestBoardWithIDs("prev-board", "prev-session")
	current := carryForwardTestBoardWithIDs("current-board", "current-session")
	submitCarrySource(t, current, "architect", "Current context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current session has a broken previous cursor."},
	})
	if _, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "python cli plan",
		PreviousSessionID:             "prev-session",
		PreviousContinuityTestamentID: "missing-continuity-testament",
	}); err != nil {
		t.Fatalf("current carry failed: %v", err)
	}
	recall, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "python cli plan",
		LookbackSessions: 1,
		OpenBoard: func(_ context.Context, sessionID string) (*ClaimsBoard, func(), error) {
			return previous, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if !recall.Partial || len(recall.Items) != 1 {
		t.Fatalf("broken link recall = partial=%v items=%d diagnostics=%+v", recall.Partial, len(recall.Items), recall.Diagnostics)
	}
	if !diagnosticsContain(recall.Diagnostics, "broken continuity spine") {
		t.Fatalf("missing broken-spine diagnostic: %+v", recall.Diagnostics)
	}
}

func TestRecallForwardCrossSessionCanBeDisabledByRollout(t *testing.T) {
	previous := carryForwardTestBoardWithIDs("prev-board", "prev-session")
	submitCarrySource(t, previous, "librarian", "Previous durable context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Previous board selected Cobra."},
	})
	prevCarry, err := CarryForward(context.Background(), previous, CarryForwardOptions{AgentID: "architect", Topic: "go cli plan"})
	if err != nil {
		t.Fatal(err)
	}
	rollout := DefaultRolloutConfig()
	rollout.RecallForwardCrossSession = false
	current := NewClaimsBoard(ClaimsBoardConfig{BoardID: "current-board", SessionID: "current-session", TaskID: "session", Rollout: rollout})
	submitCarrySource(t, current, "architect", "Current durable context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current board keeps Cobra."},
	})
	if _, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "go cli plan",
		PreviousSessionID:             "prev-session",
		PreviousContinuityTestamentID: prevCarry.TestamentID,
	}); err != nil {
		t.Fatal(err)
	}
	recall, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "go cli plan",
		LookbackSessions: 1,
		OpenBoard: func(context.Context, string) (*ClaimsBoard, func(), error) {
			return previous, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recall.Items) != 1 {
		t.Fatalf("recall items = %d, want current session only", len(recall.Items))
	}
	if !diagnosticsContain(recall.Diagnostics, EnvRecallForwardCrossSession) {
		t.Fatalf("missing cross-session rollout diagnostic: %+v", recall.Diagnostics)
	}
}

func TestDurableSessionBoardOpenerFromBoardHydratesPreviousSession(t *testing.T) {
	base := t.TempDir()
	prevDB, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "session-prev-session",
		SessionID:  "prev-session",
		TaskID:     "session",
		SessionDir: filepath.Join(base, "prev-session"),
	})
	if err != nil {
		t.Fatalf("open previous durable board: %v", err)
	}
	previous := prevDB.Board()
	submitCarrySource(t, previous, "librarian", "Previous durable context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Previous durable board selected Cobra."},
	})
	prevCarry, err := CarryForward(context.Background(), previous, CarryForwardOptions{AgentID: "architect", Topic: "go cli plan"})
	if err != nil {
		t.Fatalf("previous durable carry failed: %v", err)
	}
	if err := prevDB.Close(); err != nil {
		t.Fatalf("close previous durable board: %v", err)
	}

	currentDB, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "session-current-session",
		SessionID:  "current-session",
		TaskID:     "session",
		SessionDir: filepath.Join(base, "current-session"),
	})
	if err != nil {
		t.Fatalf("open current durable board: %v", err)
	}
	defer currentDB.Close()
	current := currentDB.Board()
	submitCarrySource(t, current, "architect", "Current durable context", []*Artifact{
		{AgentID: "architect", Kind: "decision", Reference: "Current durable board keeps Cobra."},
	})
	if _, err := CarryForward(context.Background(), current, CarryForwardOptions{
		AgentID:                       "architect",
		Topic:                         "go cli plan",
		PreviousSessionID:             "prev-session",
		PreviousContinuityTestamentID: prevCarry.TestamentID,
	}); err != nil {
		t.Fatalf("current durable carry failed: %v", err)
	}

	recall, err := RecallForward(context.Background(), current, RecallForwardOptions{
		AgentID:          "architect",
		Topic:            "go cli plan",
		LookbackSessions: 1,
		OpenBoard:        DurableSessionBoardOpenerFromBoard(current),
	})
	if err != nil {
		t.Fatalf("durable recall failed: %v", err)
	}
	if recall.Partial {
		t.Fatalf("durable recall unexpectedly partial: %+v", recall.Diagnostics)
	}
	if len(recall.Items) != 2 {
		t.Fatalf("durable recall items = %d, want 2", len(recall.Items))
	}
	if !strings.Contains(recall.WorkingContext, "Previous durable board selected Cobra") {
		t.Fatalf("durable recall missing previous board context: %q", recall.WorkingContext)
	}
}

func TestDurableSessionBoardOpenerFromBoardReportsLegacyMissingWAL(t *testing.T) {
	base := t.TempDir()
	currentDB, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "session-current-session",
		SessionID:  "current-session",
		TaskID:     "session",
		SessionDir: filepath.Join(base, "current-session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer currentDB.Close()
	opener := DurableSessionBoardOpenerFromBoard(currentDB.Board())
	if opener == nil {
		t.Fatal("opener is nil")
	}
	board, closeFn, err := opener(context.Background(), "legacy-session")
	if err == nil || !IsLegacySessionBoardError(err) {
		t.Fatalf("opener err = %v, want legacy session board error", err)
	}
	if board != nil || closeFn != nil {
		t.Fatalf("legacy opener returned board=%v closeFnSet=%v, want nils", board, closeFn != nil)
	}
	if DurableBoardWALExists(filepath.Join(base, "legacy-session"), "session-legacy-session") {
		t.Fatal("legacy opener created a WAL while checking a missing legacy session")
	}
}

func TestRecallForwardReportsStaleContradictedAndProjectionDiagnostics(t *testing.T) {
	board := carryForwardTestBoard()
	if err := board.SubmitTestaments(context.Background(), Action{AgentID: "claims-board", Type: ActionTypeTestament}, []Testament{{
		AgentID:    "claims-board",
		Summary:    "projection failed",
		Confidence: "committed",
		Artifacts: []*Artifact{{
			Kind:      ArtifactKindProjectionError,
			Reference: "projection_error projector=knowledge board=board-1 sequence=7 entity=artifact/a1: unavailable",
		}},
	}}); err != nil {
		t.Fatalf("submit projection diagnostic: %v", err)
	}
	if err := board.SubmitTestaments(context.Background(), Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID:    "architect",
		Summary:    "stale continuity",
		Confidence: "committed",
		Artifacts: []*Artifact{
			{Kind: ArtifactKindWorkingContext, Reference: "Old context", Metadata: map[string]any{"stale": true}},
			{Kind: ArtifactKindEvidenceDigest, Reference: "Old digest", Metadata: map[string]any{"contradicted": true}},
			{Kind: ArtifactKindSourceIndex, Reference: "0 source(s)", Metadata: map[string]any{"sources": []ForwardSource{}, "topic": "python cli plan", "agent_id": "architect"}},
			{Kind: ArtifactKindContinuityCursor, Reference: "0..1", Metadata: map[string]any{"topic": "python cli plan", "agent_id": "architect", "from_sequence": 0, "through_sequence": 1}},
			{Kind: ArtifactKindSessionCursor, Reference: "session=session-1 board=board-1 through=1", Metadata: map[string]any{"topic": "python cli plan", "agent_id": "architect", "session_id": "session-1", "board_id": "board-1", "through_sequence": 1}},
		},
	}}); err != nil {
		t.Fatalf("submit stale continuity: %v", err)
	}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if !recall.Stale || !recall.Contradicted {
		t.Fatalf("recall stale=%v contradicted=%v, want both true", recall.Stale, recall.Contradicted)
	}
	if !diagnosticsContain(recall.Diagnostics, "projection_error projector=knowledge") {
		t.Fatalf("projection diagnostic missing: %+v", recall.Diagnostics)
	}
}

func TestCarryForwardEvidenceDigestUsesStructuredSourceFindings(t *testing.T) {
	board := carryForwardTestBoard()
	source := submitCarrySource(t, board, "librarian", "Projected source", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Projected workspace evidence.", Metadata: map[string]any{"projection_document_id": "claims_board_artifact_a1"}},
	})
	result, err := CarryForward(context.Background(), board, CarryForwardOptions{AgentID: "architect", Topic: "python cli plan"})
	if err != nil {
		t.Fatalf("carry failed: %v", err)
	}
	continuity, ok := board.CloneTestament(result.TestamentID)
	if !ok {
		t.Fatalf("continuity testament not found")
	}
	digest := artifactByKind(continuity, ArtifactKindEvidenceDigest)
	if digest == nil {
		t.Fatal("evidence_digest artifact missing")
	}
	findings, ok := digest.Metadata["findings"].([]map[string]any)
	if !ok || len(findings) != 1 {
		t.Fatalf("findings metadata = %#v, want one structured finding", digest.Metadata["findings"])
	}
	if findings[0]["source_artifact_id"] != source.Artifacts[0].ID ||
		findings[0]["source_testament_id"] != source.ID ||
		findings[0]["selected_by"] == "" {
		t.Fatalf("structured finding missing source metadata: %#v", findings[0])
	}
	docs, ok := findings[0]["projection_document_ids"].([]string)
	if !ok || len(docs) != 1 || docs[0] != "claims_board_artifact_a1" {
		t.Fatalf("projection document IDs = %#v", findings[0]["projection_document_ids"])
	}
}

func TestCarryForwardAndRecallForwardSkillsInvoke(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "Skill wrapper context", []*Artifact{
		{AgentID: "librarian", Kind: ArtifactKindResponseText, Reference: "Skill wrappers can carry this source."},
	})
	bp := func() (*ClaimsBoard, error) { return board, nil }
	carry := CarryForwardSkill(bp, "architect")
	recall := RecallForwardSkill(bp, "architect")

	carryInput, _ := json.Marshal(map[string]any{
		"topic":       "skill wrapper topic",
		"max_sources": 2,
	})
	carryOut, err := carry.Handler(context.Background(), carryInput)
	if err != nil {
		t.Fatalf("carry skill failed: %v", err)
	}
	carryResult, ok := carryOut.(*CarryForwardResult)
	if !ok {
		t.Fatalf("carry result type = %T, want *CarryForwardResult", carryOut)
	}
	if !carryResult.Mutated {
		t.Fatalf("carry skill did not mutate: %+v", carryResult)
	}

	recallInput, _ := json.Marshal(map[string]any{
		"topic":           "skill wrapper topic",
		"include_sources": "source_index",
	})
	recallOut, err := recall.Handler(context.Background(), recallInput)
	if err != nil {
		t.Fatalf("recall skill failed: %v", err)
	}
	recallResult, ok := recallOut.(*RecallForwardResult)
	if !ok {
		t.Fatalf("recall result type = %T, want *RecallForwardResult", recallOut)
	}
	if len(recallResult.Sources) != 1 {
		t.Fatalf("recall skill sources = %+v, want one", recallResult.Sources)
	}
}

func TestRecallForwardUsesEnrichmentWithoutDirectContinuity(t *testing.T) {
	board := carryForwardTestBoard()
	provider := &fakeRecallEnrichmentProvider{hits: []RecallForwardEnrichment{{
		Source:     "archivalist_claims_knowledge",
		DocumentID: "doc-1",
		EntityType: "testament",
		EntityID:   "testament-1",
		AgentID:    "architect",
		Topic:      "python cli plan",
		EnrichedBy: "claims_knowledge_query",
	}}}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:            "architect",
		Topic:              "python cli plan",
		EnrichmentProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recall.Items) != 0 {
		t.Fatalf("direct continuity items = %d, want 0", len(recall.Items))
	}
	if len(recall.Enrichment) != 1 {
		t.Fatalf("enrichment = %+v, want one deterministic hit", recall.Enrichment)
	}
	if provider.last.SessionID != board.SessionID() || provider.last.BoardID != board.BoardID() {
		t.Fatalf("fallback enrichment query = %+v, want board/session scoped", provider.last)
	}
}

func TestRecallForwardSkillUsesDefaultEnrichmentProvider(t *testing.T) {
	board := carryForwardTestBoard()
	provider := &fakeRecallEnrichmentProvider{hits: []RecallForwardEnrichment{{
		Source:     "archivalist_claims_knowledge",
		DocumentID: "doc-default",
		EntityType: "testament",
		EntityID:   "testament-default",
	}}}
	old := DefaultRecallForwardEnrichmentProvider()
	SetDefaultRecallForwardEnrichmentProvider(provider)
	t.Cleanup(func() { SetDefaultRecallForwardEnrichmentProvider(old) })

	recall := RecallForwardSkill(func() (*ClaimsBoard, error) { return board, nil }, "architect")
	input, _ := json.Marshal(map[string]any{"topic": "python cli plan"})
	out, err := recall.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.(*RecallForwardResult)
	if !ok {
		t.Fatalf("result type = %T, want *RecallForwardResult", out)
	}
	if len(result.Enrichment) != 1 || result.Enrichment[0].DocumentID != "doc-default" {
		t.Fatalf("default enrichment not used: %+v", result.Enrichment)
	}
}

func TestRecallForwardEnrichmentErrorCreatesProjectionArtifact(t *testing.T) {
	board := carryForwardTestBoard()
	provider := &fakeRecallEnrichmentProvider{err: errors.New("committed knowledge unavailable")}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:            "architect",
		Topic:              "python cli plan",
		EnrichmentProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recall.Partial || !diagnosticsContain(recall.Diagnostics, "archivalist enrichment lookup") {
		t.Fatalf("recall diagnostics = %+v, want partial enrichment warning", recall.Diagnostics)
	}
	if !projectionArtifactExists(board, ArtifactKindProjectionError) {
		t.Fatal("recall enrichment failure did not create projection_error artifact")
	}
}

func TestLegacyRecall_NoDurableBoard(t *testing.T) {
	board := claimsBoardWithLegacyNoWAL()
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recall.Partial {
		t.Fatalf("legacy recall partial=%v diagnostics=%+v, want non-fatal empty recall", recall.Partial, recall.Diagnostics)
	}
	if len(recall.Items) != 0 {
		t.Fatalf("legacy direct items = %d, want 0", len(recall.Items))
	}
	if !diagnosticsContain(recall.Diagnostics, "legacy session has no pre-existing durable claims board WAL") {
		t.Fatalf("legacy diagnostic missing: %+v", recall.Diagnostics)
	}
}

func TestLegacyRecall_ReconstructedContinuityMarked(t *testing.T) {
	board := claimsBoardWithLegacyNoWAL()
	provider := &fakeRecallEnrichmentProvider{hits: []RecallForwardEnrichment{{
		Source:             "archivalist_legacy_entry",
		DocumentID:         "legacy-doc-1",
		Path:               "archivalist/scribe/architect/entry-1.md",
		SessionID:          "session-1",
		AgentID:            "architect",
		Topic:              "python cli plan",
		Summary:            "Previous planning selected Click and a pyproject console script.",
		AgenticNarrative:   true,
		ArchivalistEntryID: "entry-1",
		Legacy:             true,
	}}}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:            "architect",
		Topic:              "python cli plan",
		EnrichmentProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recall.Items) != 1 {
		t.Fatalf("reconstructed items = %d, want 1: %+v", len(recall.Items), recall.Items)
	}
	item := recall.Items[0]
	if !item.Reconstructed || item.ReconstructionSource != "archivalist_scribe_entries" {
		t.Fatalf("reconstructed marker missing: %+v", item)
	}
	if reconstructed, _ := item.Cursor["reconstructed"].(bool); !reconstructed {
		t.Fatalf("cursor missing reconstructed=true: %+v", item.Cursor)
	}
	if exact, _ := item.Cursor["exact_claim_sources"].(bool); exact {
		t.Fatalf("legacy narrative unexpectedly marked exact: %+v", item.Cursor)
	}
	if !strings.Contains(recall.WorkingContext, "Click") {
		t.Fatalf("working context not reconstructed from summary: %q", recall.WorkingContext)
	}
}

func TestLegacyRecall_DoesNotFabricateSources(t *testing.T) {
	board := claimsBoardWithLegacyNoWAL()
	provider := &fakeRecallEnrichmentProvider{hits: []RecallForwardEnrichment{{
		Source:             "archivalist_legacy_entry",
		DocumentID:         "legacy-doc-2",
		Path:               "archivalist/scribe/architect/entry-2.md",
		Summary:            "Legacy planning note without board entity IDs.",
		AgenticNarrative:   true,
		ArchivalistEntryID: "entry-2",
		Legacy:             true,
	}}}
	recall, err := RecallForward(context.Background(), board, RecallForwardOptions{
		AgentID:            "architect",
		Topic:              "python cli plan",
		EnrichmentProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recall.Items) != 1 {
		t.Fatalf("reconstructed items = %d, want 1", len(recall.Items))
	}
	item := recall.Items[0]
	if item.ClaimID != "" || item.TestamentID != "" || item.ExactClaimSources {
		t.Fatalf("legacy reconstruction fabricated exact sources: %+v", item)
	}
	if !diagnosticsContain(recall.Diagnostics, "no exact claim/testament/artifact source IDs") {
		t.Fatalf("missing source-integrity diagnostic: %+v", recall.Diagnostics)
	}
}

type fakeRecallEnrichmentProvider struct {
	hits []RecallForwardEnrichment
	err  error
	last RecallForwardEnrichmentQuery
}

func (f *fakeRecallEnrichmentProvider) LookupCarryForwardEnrichment(_ context.Context, query RecallForwardEnrichmentQuery) ([]RecallForwardEnrichment, error) {
	f.last = query
	if f.err != nil {
		return nil, f.err
	}
	return append([]RecallForwardEnrichment(nil), f.hits...), nil
}

func carryForwardTestBoard() *ClaimsBoard {
	return carryForwardTestBoardWithIDs("board-1", "session-1")
}

func claimsBoardWithLegacyNoWAL() *ClaimsBoard {
	return NewClaimsBoard(ClaimsBoardConfig{
		BoardID:            "legacy-board",
		SessionID:          "legacy-session",
		TaskID:             "session",
		LegacySessionNoWAL: true,
	})
}

func carryForwardTestBoardWithIDs(boardID, sessionID string) *ClaimsBoard {
	return NewClaimsBoard(ClaimsBoardConfig{BoardID: boardID, SessionID: sessionID, TaskID: "session"})
}

func submitCarrySource(t *testing.T, board *ClaimsBoard, agentID, summary string, artifacts []*Artifact) *Testament {
	t.Helper()
	testament := Testament{
		AgentID:    agentID,
		Summary:    summary,
		Confidence: "high",
		Artifacts:  artifacts,
	}
	if err := board.SubmitTestaments(context.Background(), Action{AgentID: agentID, Type: ActionTypeTestament}, []Testament{testament}); err != nil {
		t.Fatalf("SubmitTestaments failed: %v", err)
	}
	proj := board.Projection()
	if len(proj.Testaments) == 0 {
		t.Fatalf("no testaments after submit")
	}
	cloned, ok := board.CloneTestament(proj.Testaments[len(proj.Testaments)-1].ID)
	if !ok {
		t.Fatalf("submitted testament not found")
	}
	return cloned
}

func hasForwardSourceKind(sources []ForwardSource, kind string) bool {
	for _, source := range sources {
		if source.Kind == kind {
			return true
		}
	}
	return false
}

func hasForwardSourceArtifact(sources []ForwardSource, artifactID string) bool {
	for _, source := range sources {
		if source.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

func diagnosticsContain(diagnostics []string, needle string) bool {
	for _, msg := range diagnostics {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func artifactByKind(t *Testament, kind string) *Artifact {
	for _, artifact := range t.Artifacts {
		if artifact != nil && artifact.Kind == kind {
			return artifact
		}
	}
	return nil
}

func carryForwardTestamentHasArtifactKind(t *Testament, kind string) bool {
	for _, artifact := range t.Artifacts {
		if artifact != nil && artifact.Kind == kind {
			return true
		}
	}
	return false
}
