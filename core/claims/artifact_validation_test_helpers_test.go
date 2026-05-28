package claims

import (
	"testing"
	"time"
)

const (
	artifactFixtureClaimID      = "claim-artifact-fixture"
	artifactFixtureArtifactID   = "artifact-fixture"
	artifactFixtureValidationID = "validation-fixture"
	artifactFixtureTestamentID  = "testament-fixture"
)

func fixtureClock() time.Time {
	return time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
}

func fixtureTypedArtifact(t *testing.T, registry *TypeRegistry) *Artifact {
	t.Helper()
	artifact := &Artifact{
		ID:            artifactFixtureArtifactID,
		ClaimID:       artifactFixtureClaimID,
		TestamentID:   artifactFixtureTestamentID,
		ArtifactName:  "plan",
		Kind:          ArtifactKindPlanMarkdown,
		AgentID:       "architect",
		ParticipantID: "architect",
		Status:        ArtifactStatusGenerated,
		StatusHistory: []StatusChange{{To: string(ArtifactStatusGenerated), AgentID: "architect", ParticipantID: "architect", Changed: fixtureClock()}},
		Presentation:  testUserChatPresentation(),
		Reference:     "# Plan",
		Metadata:      map[string]any{"fixture": true},
		Created:       fixtureClock(),
		Accessed:      fixtureClock(),
	}
	if err := SetArtifactDataWithRegistry(registry, artifact, PlanMarkdownArtifactData{Markdown: "# Plan", Title: "Plan"}); err != nil {
		t.Fatalf("SetArtifactDataWithRegistry: %v", err)
	}
	return artifact
}

func fixtureTypedValidation() *Validation {
	return &Validation{
		ID:                 artifactFixtureValidationID,
		ClaimID:            artifactFixtureClaimID,
		TargetArtifactName: "plan",
		ValidatorID:        "plan.markdown",
		ArtifactDataType:   ArtifactDataTypePlanMarkdown,
		ResultDataType:     ArtifactDataTypePresentationEvidence,
		Description:        "plan is inspectable",
		QualityBar:         "plan must be clear",
		Type:               ValidationTypeInspection,
		Status:             ValidationStatusReady,
		StatusHistory:      []StatusChange{{To: string(ValidationStatusReady), AgentID: "architect", ParticipantID: "architect", Changed: fixtureClock()}},
		Required:           true,
		Timeout:            time.Minute,
		Created:            fixtureClock(),
		Accessed:           fixtureClock(),
	}
}

func fixtureClaimWithTypedValidation() Claim {
	return Claim{
		ID:          artifactFixtureClaimID,
		AgentID:     "architect",
		Title:       "produce plan",
		Description: "produce plan",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{fixtureTypedValidation()},
	}
}

func fixtureMultiArtifactTestament(t *testing.T, registry *TypeRegistry) Testament {
	t.Helper()
	plan := fixtureTypedArtifact(t, registry)
	plan.ID = artifactFixtureArtifactID + "-plan"
	plan.ArtifactName = "plan"
	evidence := fixtureTypedArtifact(t, registry)
	evidence.ID = artifactFixtureArtifactID + "-evidence"
	evidence.ArtifactName = "evidence"
	return Testament{
		ID:      artifactFixtureTestamentID,
		AgentID: "engineer",
		Summary: "done",
		Relations: []Relation{
			{Related: artifactFixtureClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{plan, evidence},
	}
}

func fixtureMalformedLegacyArtifact() Artifact {
	return Artifact{
		ID:        "legacy-artifact",
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "legacy content",
	}
}
