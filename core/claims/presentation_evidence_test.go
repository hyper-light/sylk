package claims

import (
	"context"
	"testing"
)

func boardWithPlanArtifact(t *testing.T, artifact *Artifact) (*ClaimsBoard, string, string) {
	t.Helper()
	board := testBoard()
	ctx := context.Background()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("c-plan", "plan")}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	if err := board.SubmitTestaments(ctx, Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID: "architect",
		Summary: "Plan ready.",
		Relations: []Relation{
			{Related: "c-plan", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{artifact},
	}}); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	testamentID := board.Projection().Testaments[0].ID
	artifactID := board.Projection().Testaments[0].Artifacts[0].ID
	return board, testamentID, artifactID
}

func boardWithPlanValidationArtifact(t *testing.T, artifact *Artifact) (*ClaimsBoard, string) {
	t.Helper()
	board := testBoard()
	ctx := context.Background()
	claim := testClaim("c-plan", "plan")
	claim.Validations = []*Validation{{
		Description: "Verify plan_markdown artifact is present and renderable.",
		QualityBar:  "plan_markdown artifact matches the plan metadata and targets chat",
		Type:        ValidationTypeInspection,
		Required:    true,
	}}
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	validationID := board.Projection().Claims[0].Validations[0].ID
	if err := board.SubmitTestaments(ctx, Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID: "architect",
		Summary: "Plan ready.",
		Relations: []Relation{
			{Related: "c-plan", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{artifact},
	}}); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	return board, validationID
}

func TestValidatePlanMarkdownEvidenceFindsArtifactAndChecksMetadata(t *testing.T) {
	board, testamentID, _ := boardWithPlanArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1", "epoch": 2, "task_count": 1},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat, PresentationSurfaceApproval},
			Format:    PresentationFormatMarkdown,
		},
	})

	result := ValidatePlanMarkdownEvidence(board, PlanMarkdownEvidenceCheck{
		PlanID:            "p1",
		Epoch:             2,
		RequireChat:       true,
		RequireApproval:   true,
		ExpectedTaskCount: 1,
		RequiredTaskText:  []string{"Task A"},
	})
	if !result.Passed {
		t.Fatalf("ValidatePlanMarkdownEvidence failed: %+v", result.Reasons)
	}
	if result.Artifact == nil || result.Artifact.Kind != ArtifactKindPlanMarkdown {
		t.Fatalf("missing artifact in result: %+v", result.Artifact)
	}
	if result.TestamentID != testamentID {
		t.Fatalf("TestamentID = %q, want %q", result.TestamentID, testamentID)
	}
}

func TestValidatePlanMarkdownEvidenceDetectsMissingChatSurface(t *testing.T) {
	board, _, _ := boardWithPlanArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1", "epoch": 1},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceApproval},
			Format:    PresentationFormatMarkdown,
		},
	})

	result := ValidatePlanMarkdownEvidence(board, PlanMarkdownEvidenceCheck{PlanID: "p1", Epoch: 1, RequireChat: true})
	if result.Passed {
		t.Fatalf("missing chat surface should fail: %+v", result)
	}
}

func TestValidatePlanMarkdownEvidenceAndMaybeRemediatePostsCorrectiveClaim(t *testing.T) {
	board, validationID := boardWithPlanValidationArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1", "epoch": 1},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceApproval},
			Format:    PresentationFormatMarkdown,
		},
	})

	result, err := ValidatePlanMarkdownEvidenceAndMaybeRemediate(context.Background(), board, PlanMarkdownEvidenceRemediationRequest{
		ClaimID:          "c-plan",
		ValidationID:     validationID,
		EvaluatorAgentID: "inspector-a",
		Check: PlanMarkdownEvidenceCheck{
			PlanID:      "p1",
			Epoch:       1,
			RequireChat: true,
		},
	})
	if err != nil {
		t.Fatalf("ValidatePlanMarkdownEvidenceAndMaybeRemediate: %v", err)
	}
	if result.Evidence.Passed {
		t.Fatalf("evidence should fail missing chat: %+v", result.Evidence)
	}
	if !result.ValidationEvaluated || result.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("validation not recorded as failed: %+v", result)
	}
	if !result.RemediationPosted || result.RemediationActionID == "" || len(result.RemediationClaimIDs) != 1 {
		t.Fatalf("remediation not posted: %+v", result)
	}

	original, ok := board.CloneClaim("c-plan")
	if !ok {
		t.Fatal("original claim missing")
	}
	if original.Status != ClaimStatusRejected {
		t.Fatalf("original status = %s, want rejected", original.Status)
	}
	if original.Validations[0].Status != ValidationStatusFailed {
		t.Fatalf("original validation status = %s, want failed", original.Validations[0].Status)
	}

	action, ok := board.CloneAction(result.RemediationActionID)
	if !ok {
		t.Fatalf("remediation action %q missing", result.RemediationActionID)
	}
	if action.Type != ActionTypeCorrective {
		t.Fatalf("remediation action type = %s, want corrective", action.Type)
	}

	replacement, ok := board.CloneClaim(result.RemediationClaimIDs[0])
	if !ok {
		t.Fatalf("remediation claim %q missing", result.RemediationClaimIDs[0])
	}
	if replacement.Status != ClaimStatusPending {
		t.Fatalf("replacement status = %s, want pending", replacement.Status)
	}
	if replacement.ActionType != ActionTypeCorrective {
		t.Fatalf("replacement action type = %s, want corrective", replacement.ActionType)
	}
	if !HasRelation(replacement.Relations, RelationshipSupersedes, "c-plan") {
		t.Fatalf("replacement missing supersedes relation: %+v", replacement.Relations)
	}
	if !HasRelation(replacement.Relations, RelationshipCausedBy, "c-plan") {
		t.Fatalf("replacement missing caused_by relation: %+v", replacement.Relations)
	}
	if SubjectAgentID(replacement.Relations) != SubjectAgentID(original.Relations) {
		t.Fatalf("replacement subject = %q, want %q", SubjectAgentID(replacement.Relations), SubjectAgentID(original.Relations))
	}
	if !claimHasTag(replacement.Tags, claimTagEvidenceRemediation) || !claimHasTag(replacement.Tags, claimTagPlanMarkdownRemediation) {
		t.Fatalf("replacement tags missing evidence markers: %+v", replacement.Tags)
	}
	if len(replacement.Validations) != 1 || replacement.Validations[0].Type != ValidationTypeInspection || !replacement.Validations[0].Required {
		t.Fatalf("replacement validation malformed: %+v", replacement.Validations)
	}
}

func TestValidatePlanMarkdownEvidenceAndMaybeRemediatePassesValidationWithoutRemediation(t *testing.T) {
	board, validationID := boardWithPlanValidationArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1", "epoch": 1, "task_count": 1},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat, PresentationSurfaceApproval},
			Format:    PresentationFormatMarkdown,
		},
	})

	result, err := ValidatePlanMarkdownEvidenceAndMaybeRemediate(context.Background(), board, PlanMarkdownEvidenceRemediationRequest{
		ClaimID:          "c-plan",
		ValidationID:     validationID,
		EvaluatorAgentID: "inspector-a",
		Check: PlanMarkdownEvidenceCheck{
			PlanID:            "p1",
			Epoch:             1,
			RequireChat:       true,
			RequireApproval:   true,
			ExpectedTaskCount: 1,
			RequiredTaskText:  []string{"Task A"},
		},
	})
	if err != nil {
		t.Fatalf("ValidatePlanMarkdownEvidenceAndMaybeRemediate: %v", err)
	}
	if !result.Evidence.Passed {
		t.Fatalf("evidence should pass: %+v", result.Evidence.Reasons)
	}
	if !result.ValidationEvaluated || result.ValidationStatus != ValidationStatusPassed {
		t.Fatalf("validation not recorded as passed: %+v", result)
	}
	if result.RemediationPosted || len(result.RemediationClaimIDs) != 0 {
		t.Fatalf("unexpected remediation: %+v", result)
	}
	claim, ok := board.CloneClaim("c-plan")
	if !ok {
		t.Fatal("claim missing")
	}
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status = %s, want accepted", claim.Status)
	}
}

func TestValidatePlanMarkdownEvidenceAndMaybeRemediateIsIdempotentAfterRejection(t *testing.T) {
	board, validationID := boardWithPlanValidationArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1"},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceApproval},
			Format:    PresentationFormatMarkdown,
		},
	})
	req := PlanMarkdownEvidenceRemediationRequest{
		ClaimID:          "c-plan",
		ValidationID:     validationID,
		EvaluatorAgentID: "inspector-a",
		Check:            PlanMarkdownEvidenceCheck{PlanID: "p1", RequireChat: true},
	}
	first, err := ValidatePlanMarkdownEvidenceAndMaybeRemediate(context.Background(), board, req)
	if err != nil {
		t.Fatalf("first remediation: %v", err)
	}
	second, err := ValidatePlanMarkdownEvidenceAndMaybeRemediate(context.Background(), board, req)
	if err != nil {
		t.Fatalf("second remediation: %v", err)
	}
	if !second.ExistingRemediation {
		t.Fatalf("second call should find existing remediation: %+v", second)
	}
	if len(second.RemediationClaimIDs) != 1 || second.RemediationClaimIDs[0] != first.RemediationClaimIDs[0] {
		t.Fatalf("second call changed remediation IDs: first=%+v second=%+v", first.RemediationClaimIDs, second.RemediationClaimIDs)
	}
}

func TestValidatePlanMarkdownEvidenceVisibleMalformedPlanFails(t *testing.T) {
	board, _, _ := boardWithPlanArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "This is visible but not a plan.",
		Metadata:  map[string]any{"plan_id": "p1"},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat},
			Format:    PresentationFormatMarkdown,
		},
	})

	result := ValidatePlanMarkdownEvidence(board, PlanMarkdownEvidenceCheck{PlanID: "p1", RequireChat: true})
	if result.Passed {
		t.Fatalf("visible malformed plan should fail: %+v", result)
	}
}

func TestValidateArtifactEvidenceInternalPayloadCanPass(t *testing.T) {
	result := ValidateArtifactEvidence(&Artifact{
		Kind:      ArtifactKindPlanHandoffPayload,
		Reference: `{"plan_id":"p1"}`,
		Metadata:  map[string]any{"plan_id": "p1"},
	}, ArtifactEvidenceCheck{
		Kind:             ArtifactKindPlanHandoffPayload,
		RequireReference: true,
		RequiredMetadata: map[string]any{"plan_id": "p1"},
	})
	if !result.Passed {
		t.Fatalf("internal valid payload should pass: %+v", result.Reasons)
	}
}

func TestValidatePlanMarkdownEvidencePresentationOnlyDoesNotSatisfyContent(t *testing.T) {
	board, _, _ := boardWithPlanArtifact(t, &Artifact{
		Kind:     ArtifactKindPlanMarkdown,
		Metadata: map[string]any{"plan_id": "p1", "epoch": 1},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat},
			Format:    PresentationFormatMarkdown,
			Title:     "Plan",
		},
	})

	result := ValidatePlanMarkdownEvidence(board, PlanMarkdownEvidenceCheck{PlanID: "p1", Epoch: 1, RequireChat: true})
	if result.Passed {
		t.Fatalf("presentation-only plan should fail content validation: %+v", result)
	}
}

func TestTraverseFromClaimReachesTestamentAndPresentableArtifact(t *testing.T) {
	board, testamentID, artifactID := boardWithPlanArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Metadata:  map[string]any{"plan_id": "p1"},
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat},
			Format:    PresentationFormatMarkdown,
		},
	})

	nodes := Traverse(board, "c-plan", RelationshipTestament, 2)
	var sawTestament, sawArtifact bool
	for _, node := range nodes {
		if node.Testament != nil && node.Testament.ID == testamentID {
			sawTestament = true
		}
		if node.Artifact != nil && node.Artifact.ID == artifactID {
			sawArtifact = true
		}
	}
	if !sawTestament || !sawArtifact {
		t.Fatalf("traverse nodes missing testament/artifact: testament=%t artifact=%t nodes=%+v", sawTestament, sawArtifact, nodes)
	}
}

func TestQueryBoardArtifactReturnsPresentation(t *testing.T) {
	board, _, artifactID := boardWithPlanArtifact(t, &Artifact{
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceUser},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat},
			Format:    PresentationFormatMarkdown,
			Title:     "Plan",
		},
	})

	result, err := dispatchBoardQuery(board, "artifact", "", "", "", artifactID, "", "", "")
	if err != nil {
		t.Fatalf("dispatchBoardQuery artifact: %v", err)
	}
	artifact, ok := result.(*Artifact)
	if !ok {
		t.Fatalf("result type = %T, want *Artifact", result)
	}
	if artifact.Presentation == nil || artifact.Presentation.Title != "Plan" {
		t.Fatalf("artifact presentation missing: %+v", artifact.Presentation)
	}
}

func TestBackfillLegacyPlanMarkdownArtifactsWritesDurableArtifact(t *testing.T) {
	board, _, _ := boardWithPlanArtifact(t, &Artifact{
		Kind: ArtifactKindPlanHandoffPayload,
		Metadata: map[string]any{
			"plan_id":       "legacy-p1",
			"epoch":         3,
			"plan_markdown": "### Plan\n\n- Legacy task",
		},
	})

	records, err := BackfillLegacyPlanMarkdownArtifacts(context.Background(), board, "claims-migration")
	if err != nil {
		t.Fatalf("BackfillLegacyPlanMarkdownArtifacts: %v", err)
	}
	if len(records) != 1 || records[0].PlanArtifactID == "" {
		t.Fatalf("unexpected backfill records: %+v", records)
	}
	artifact, ok := board.CloneArtifact(records[0].PlanArtifactID)
	if !ok {
		t.Fatalf("backfilled artifact %q not found", records[0].PlanArtifactID)
	}
	if artifact.Kind != ArtifactKindPlanMarkdown || artifact.Presentation == nil {
		t.Fatalf("unexpected backfilled artifact: %+v", artifact)
	}
}
