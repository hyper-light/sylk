package architect

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

func testReviewPlan() *DesignPlan {
	return &DesignPlan{
		ID:       "plan-review",
		Status:   PlanStatusReady,
		Revision: 2,
		Epoch:    7,
		Tasks: []*AtomicTask{{
			ID:          "task-1",
			Name:        "Build CLI",
			AgentType:   "engineer",
			Status:      TaskStatusPending,
			Description: "Create the command entrypoint.",
		}},
	}
}

func TestBuildPlanMarkdownArtifactUserPresentation(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "ses-review"})
	plan := testReviewPlan()
	plan.RequestCorrelationID = "corr-plan-review"
	artifact, replaceKey, contentHash, epoch, err := a.buildPlanMarkdownArtifact(plan, "")
	if err != nil {
		t.Fatalf("buildPlanMarkdownArtifact: %v", err)
	}
	if artifact.Kind != claims.ArtifactKindPlanMarkdown {
		t.Fatalf("artifact.Kind = %q, want %q", artifact.Kind, claims.ArtifactKindPlanMarkdown)
	}
	if strings.TrimSpace(artifact.Reference) == "" {
		t.Fatal("artifact.Reference must contain markdown")
	}
	if !claims.PresentationMatches(artifact.Presentation, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceChat)) {
		t.Fatalf("artifact presentation missing user/chat: %+v", artifact.Presentation)
	}
	if !claims.PresentationMatches(artifact.Presentation, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceApproval)) {
		t.Fatalf("artifact presentation missing user/approval: %+v", artifact.Presentation)
	}
	if artifact.Presentation.Format != claims.PresentationFormatMarkdown {
		t.Fatalf("format = %q, want markdown", artifact.Presentation.Format)
	}
	if artifact.Presentation.ReplaceKey != replaceKey || replaceKey != "plan:plan-review:review" {
		t.Fatalf("replace key = %q / %q", artifact.Presentation.ReplaceKey, replaceKey)
	}
	if contentHash == "" || artifact.Metadata["content_hash"] != contentHash {
		t.Fatalf("content hash metadata mismatch: %q / %#v", contentHash, artifact.Metadata["content_hash"])
	}
	if epoch != 7 || artifact.Metadata["epoch"] != uint64(7) {
		t.Fatalf("epoch = %d metadata=%#v, want 7", epoch, artifact.Metadata["epoch"])
	}
	if artifact.Metadata["task_count"] != 1 {
		t.Fatalf("task_count metadata = %#v, want 1", artifact.Metadata["task_count"])
	}
	if artifact.Metadata["stream_correlation_id"] != "corr-plan-review" ||
		artifact.Metadata["correlation_id"] != "corr-plan-review" ||
		artifact.Metadata["request_correlation_id"] != "corr-plan-review" {
		t.Fatalf("correlation metadata missing: %#v", artifact.Metadata)
	}
}

func TestBuildPlanMarkdownArtifactRejectsEmptyPlan(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "ses-review-empty"})
	if _, _, _, _, err := a.buildPlanMarkdownArtifact(&DesignPlan{ID: "empty"}, ""); err == nil {
		t.Fatal("expected empty plan to fail review artifact guard")
	}
}

func TestBuildPlanMarkdownArtifactSupersedesPrior(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "ses-review-supersede"})
	artifact, _, _, _, err := a.buildPlanMarkdownArtifact(testReviewPlan(), "artifact-old")
	if err != nil {
		t.Fatalf("buildPlanMarkdownArtifact: %v", err)
	}
	found := false
	for _, rel := range artifact.Relations {
		if rel.Related == "artifact-old" &&
			rel.RelatedType == claims.RelatedTypeArtifact &&
			rel.Relationship == claims.RelationshipSupersedes {
			found = true
		}
	}
	if !found {
		t.Fatalf("supersedes relation missing: %+v", artifact.Relations)
	}
}

func TestApplyPlanRevisionIncrementsReviewArtifactEpoch(t *testing.T) {
	a := newTestArchitect(t, Config{SessionID: "ses-review-revision"})
	plan := testReviewPlan()
	plan.Status = PlanStatusDesigning
	plan.sm = NewPlanStateMachineWithEpoch(plan.ID, plan.Status, plan.Epoch)
	if err := a.planStore.Upsert(plan); err != nil {
		t.Fatalf("upsert plan: %v", err)
	}
	priorRevision := plan.Revision
	updated := a.applyPlanRevision(plan, "user requested a change", nil)
	if updated.Revision != priorRevision+1 {
		t.Fatalf("revision = %d, want %d", updated.Revision, priorRevision+1)
	}
	if updated.Epoch != 8 {
		t.Fatalf("epoch = %d, want 8", updated.Epoch)
	}
	if updated.SM().Epoch() != updated.Epoch {
		t.Fatalf("state machine epoch = %d, want %d", updated.SM().Epoch(), updated.Epoch)
	}
}

func TestValidateCurrentPlanApprovalContinuationRejectsStaleArtifact(t *testing.T) {
	plan := testReviewPlan()
	currentMarkdown := strings.TrimSpace(formatPlanForChat(plan))
	plan.PlanMarkdownArtifactID = "artifact-current"
	plan.PlanMarkdownReplaceKey = planMarkdownReplaceKey(plan.ID)
	plan.PlanMarkdownContentHash = planMarkdownContentHash(currentMarkdown)
	plan.PlanMarkdownArtifactEpoch = planMarkdownArtifactEpoch(plan)
	plan.PendingWork = &PendingContinuation{
		Kind:          string(continuationKindPlanApproval),
		Status:        string(continuationStatusPending),
		CorrelationID: "corr-stale",
	}
	request := planApprovalGateRequest{
		PlanID:                 plan.ID,
		PlanArtifactID:         "artifact-old",
		PlanArtifactReplaceKey: plan.PlanMarkdownReplaceKey,
		Metadata: map[string]any{
			"epoch":                      plan.PlanMarkdownArtifactEpoch,
			"plan_artifact_content_hash": plan.PlanMarkdownContentHash,
		},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	err = validateCurrentPlanApprovalContinuation(plan, &ArchitectContinuation{
		Kind:                  continuationKindPlanApproval,
		ResponseCorrelationID: "corr-stale",
		RequestJSON:           string(raw),
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale artifact rejection", err)
	}
}

func TestPublishPreparedHandoffSubmitsReviewAndInternalArtifacts(t *testing.T) {
	sessionID := "ses-review-publish-" + uuid.NewString()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-" + sessionID,
		SessionID: sessionID,
	})
	registry := claims.DefaultSessionBoardRegistry()
	registry.ReplaceForReason(sessionID, board, "test plan review artifact publish")
	t.Cleanup(func() { registry.Remove(sessionID) })

	plan := testReviewPlan()
	plan.SessionID = sessionID
	a := &Architect{
		config:    Config{SessionID: sessionID},
		planStore: testPlanStore(t),
		logger:    slog.Default(),
	}

	if err := a.publishPreparedHandoff(context.Background(), plan); err != nil {
		t.Fatalf("publishPreparedHandoff: %v", err)
	}
	if strings.TrimSpace(plan.PlanMarkdownArtifactID) == "" {
		t.Fatal("PlanMarkdownArtifactID was not recorded")
	}
	if strings.TrimSpace(plan.HandoffPayloadArtifactID) == "" {
		t.Fatal("HandoffPayloadArtifactID was not recorded")
	}
	proj := board.Projection()
	if len(proj.Testaments) != 1 {
		t.Fatalf("testament count = %d, want 1", len(proj.Testaments))
	}
	var sawPlan, sawHandoff bool
	for _, artifact := range proj.Testaments[0].Artifacts {
		switch artifact.Kind {
		case claims.ArtifactKindPlanMarkdown:
			sawPlan = true
			if !claims.PresentationMatches(artifact.Presentation, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceChat)) {
				t.Fatalf("plan artifact is not user/chat presentable: %+v", artifact.Presentation)
			}
		case claims.ArtifactKindPlanHandoffPayload:
			sawHandoff = true
			if artifact.Presentation != nil {
				t.Fatalf("handoff payload must remain internal, got presentation %+v", artifact.Presentation)
			}
		}
	}
	if !sawPlan || !sawHandoff {
		t.Fatalf("artifacts sawPlan=%t sawHandoff=%t", sawPlan, sawHandoff)
	}
}

func TestPublishPreparedHandoffRepublishesCurrentMetadataWhenBoardMissingArtifact(t *testing.T) {
	sessionID := "ses-review-republish-" + uuid.NewString()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-" + sessionID,
		SessionID: sessionID,
	})
	registry := claims.DefaultSessionBoardRegistry()
	registry.ReplaceForReason(sessionID, board, "test plan review artifact republish")
	t.Cleanup(func() { registry.Remove(sessionID) })

	plan := testReviewPlan()
	plan.SessionID = sessionID
	markdown := strings.TrimSpace(formatPlanForChat(plan))
	plan.PlanMarkdownArtifactID = "artifact-missing-from-board"
	plan.PlanMarkdownReplaceKey = planMarkdownReplaceKey(plan.ID)
	plan.PlanMarkdownContentHash = planMarkdownContentHash(markdown)
	plan.PlanMarkdownArtifactEpoch = planMarkdownArtifactEpoch(plan)

	a := &Architect{
		config:    Config{SessionID: sessionID},
		planStore: testPlanStore(t),
		logger:    slog.Default(),
	}
	if a.planHasCurrentMarkdownArtifactOnBoard(plan) {
		t.Fatal("test setup expected plan metadata to be current but board artifact missing")
	}

	if err := a.publishPreparedHandoff(context.Background(), plan); err != nil {
		t.Fatalf("publishPreparedHandoff: %v", err)
	}
	if plan.PlanMarkdownArtifactID == "artifact-missing-from-board" {
		t.Fatal("PlanMarkdownArtifactID should be replaced when the recorded artifact is absent from the board")
	}
	if !a.planHasCurrentMarkdownArtifactOnBoard(plan) {
		t.Fatal("republished plan markdown artifact was not present/current on board")
	}
	proj := board.Projection()
	if len(proj.Testaments) != 1 {
		t.Fatalf("testament count = %d, want 1", len(proj.Testaments))
	}
	var sawPlan, sawHandoff, sawSupersedes bool
	for _, artifact := range proj.Testaments[0].Artifacts {
		switch artifact.Kind {
		case claims.ArtifactKindPlanMarkdown:
			sawPlan = true
			if artifact.ID != plan.PlanMarkdownArtifactID {
				t.Fatalf("plan artifact ID = %q, want %q", artifact.ID, plan.PlanMarkdownArtifactID)
			}
			if artifact.Reference != markdown {
				t.Fatalf("plan artifact reference mismatch:\n got %q\nwant %q", artifact.Reference, markdown)
			}
			for _, rel := range artifact.Relations {
				if rel.Relationship == claims.RelationshipSupersedes && rel.Related == "artifact-missing-from-board" {
					sawSupersedes = true
				}
			}
		case claims.ArtifactKindPlanHandoffPayload:
			sawHandoff = true
		}
	}
	if !sawPlan || !sawHandoff || !sawSupersedes {
		t.Fatalf("sawPlan=%t sawHandoff=%t sawSupersedes=%t", sawPlan, sawHandoff, sawSupersedes)
	}
}
