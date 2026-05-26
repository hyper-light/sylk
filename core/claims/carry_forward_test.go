package claims

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
		if !testamentHasArtifactKind(continuity, kind) {
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

func carryForwardTestBoard() *ClaimsBoard {
	return carryForwardTestBoardWithIDs("board-1", "session-1")
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

func testamentHasArtifactKind(t *Testament, kind string) bool {
	for _, artifact := range t.Artifacts {
		if artifact != nil && artifact.Kind == kind {
			return true
		}
	}
	return false
}
