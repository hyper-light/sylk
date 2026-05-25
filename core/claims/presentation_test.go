package claims

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testUserChatPresentation() *Presentation {
	return &Presentation{
		Audiences:  []PresentationAudience{PresentationAudienceUser},
		Surfaces:   []PresentationSurface{PresentationSurfaceChat},
		Format:     PresentationFormatMarkdown,
		Title:      "Plan",
		Placement:  PresentationPlacementBeforeResponse,
		ReplaceKey: "plan:p1:review",
		Priority:   10,
	}
}

func TestClaimHasNoPresentationField(t *testing.T) {
	if _, ok := reflect.TypeOf(Claim{}).FieldByName("Presentation"); ok {
		t.Fatal("claims must not carry presentation metadata")
	}
}

func TestTestamentJSONRoundTrip_WithPresentation(t *testing.T) {
	original := Testament{
		ID:           "testament-1",
		Summary:      "Repository has no Python package infrastructure.",
		Presentation: testUserChatPresentation(),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Testament
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Presentation == nil {
		t.Fatal("presentation missing after round trip")
	}
	if decoded.Presentation.Audiences[0] != PresentationAudienceUser {
		t.Fatalf("audience = %q", decoded.Presentation.Audiences[0])
	}
	if decoded.Presentation.ReplaceKey != "plan:p1:review" {
		t.Fatalf("replace key = %q", decoded.Presentation.ReplaceKey)
	}
}

func TestArtifactJSONRoundTrip_WithPresentation(t *testing.T) {
	original := Artifact{
		ID:           "artifact-1",
		Kind:         ArtifactKindPlanMarkdown,
		Reference:    "### Plan\n\nShip it.",
		Presentation: testUserChatPresentation(),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Artifact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Presentation == nil {
		t.Fatal("presentation missing after round trip")
	}
	if decoded.Presentation.Format != PresentationFormatMarkdown {
		t.Fatalf("format = %q", decoded.Presentation.Format)
	}
}

func TestCloneTestament_DeepCopiesPresentation(t *testing.T) {
	original := &Testament{
		ID:           "testament-1",
		Summary:      "done",
		Presentation: testUserChatPresentation(),
		Artifacts: []*Artifact{{
			ID:           "artifact-1",
			Kind:         ArtifactKindPlanMarkdown,
			Reference:    "### Plan",
			Presentation: testUserChatPresentation(),
		}},
	}
	clone := CloneTestamentEntity(original)
	clone.Presentation.Audiences[0] = PresentationAudienceDeveloper
	clone.Artifacts[0].Presentation.Title = "Mutated"

	if original.Presentation.Audiences[0] != PresentationAudienceUser {
		t.Fatalf("testament presentation was not deep-copied")
	}
	if original.Artifacts[0].Presentation.Title != "Plan" {
		t.Fatalf("artifact presentation was not deep-copied")
	}
}

func TestProjection_IncludesTestamentPresentation(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	if err := board.SubmitTestaments(ctx, Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID:      "architect",
		Summary:      "Plan ready.",
		Presentation: testUserChatPresentation(),
		Relations: []Relation{
			{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
	}}); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	proj := board.Projection()
	if len(proj.Testaments) != 1 || proj.Testaments[0].Presentation == nil {
		t.Fatalf("projection missing testament presentation: %+v", proj.Testaments)
	}
	proj.Testaments[0].Presentation.Title = "Mutated projection"
	stored, ok := board.CloneTestament(proj.Testaments[0].ID)
	if !ok {
		t.Fatal("stored testament missing")
	}
	if stored.Presentation.Title != "Plan" {
		t.Fatalf("projection mutation leaked into board: %q", stored.Presentation.Title)
	}
}

func TestArtifactProjection_TruncatesReferenceButPreservesPresentation(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	if err := board.PostAction(ctx, Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("c1", "claim")}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	longReference := strings.Repeat("x", maxArtifactReferenceLen+20)
	if err := board.SubmitTestaments(ctx, Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID: "architect",
		Summary: "Plan ready.",
		Relations: []Relation{
			{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{{
			Kind:         ArtifactKindPlanMarkdown,
			Reference:    longReference,
			Presentation: testUserChatPresentation(),
		}},
	}}); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	artifact := board.Projection().Testaments[0].Artifacts[0]
	if len(artifact.Reference) >= len(longReference) {
		t.Fatal("projection reference was not truncated")
	}
	if artifact.Presentation == nil || artifact.Presentation.Title != "Plan" {
		t.Fatalf("presentation not preserved through truncation: %+v", artifact.Presentation)
	}
}

func TestSubmitTestaments_PresentationDoesNotChangeValidation(t *testing.T) {
	board := testBoard()
	ctx := context.Background()
	claim := testClaimWithValidations("c1", "claim")
	if err := board.PostAction(ctx, Action{AgentID: "inspector-a", Type: ActionTypeTask}, []Claim{claim}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	if err := board.SubmitTestaments(ctx, Action{AgentID: "engineer-b", Type: ActionTypeTestament}, []Testament{{
		AgentID:      "engineer-b",
		Summary:      "done",
		Presentation: testUserChatPresentation(),
		Relations: []Relation{
			{Related: "c1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
	}}); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	got, ok := board.ClaimByID("c1")
	if !ok {
		t.Fatal("claim missing")
	}
	if got.Status != ClaimStatusTestified {
		t.Fatalf("claim status = %q, want testified", got.Status)
	}
	for _, validation := range got.Validations {
		if validation.Status != ValidationStatusPending {
			t.Fatalf("validation status = %q, want pending", validation.Status)
		}
	}
}

func TestPresentableArtifactsHelpers(t *testing.T) {
	tm := &Testament{Artifacts: []*Artifact{
		{Kind: ArtifactKindPlanMarkdown, Reference: "visible", Presentation: testUserChatPresentation()},
		{Kind: ArtifactKindPlanHandoffPayload, Reference: "internal"},
		{Kind: "diagnostic", Reference: "dev", Presentation: &Presentation{
			Audiences: []PresentationAudience{PresentationAudienceDeveloper},
			Surfaces:  []PresentationSurface{PresentationSurfaceChat},
		}},
	}}
	artifacts := PresentableArtifacts(tm, string(PresentationAudienceUser), string(PresentationSurfaceChat))
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactKindPlanMarkdown {
		t.Fatalf("PresentableArtifacts = %+v", artifacts)
	}
	if !HasPresentableArtifact(tm, ArtifactKindPlanMarkdown, string(PresentationAudienceUser), string(PresentationSurfaceChat)) {
		t.Fatal("expected presentable plan artifact")
	}
	if HasPresentableArtifact(tm, ArtifactKindPlanHandoffPayload, string(PresentationAudienceUser), string(PresentationSurfaceChat)) {
		t.Fatal("internal handoff payload should not match")
	}
}

func TestArtifactHash_PresentationPolicy(t *testing.T) {
	base := Artifact{ID: "artifact-1", Kind: "diff", Reference: "+one"}
	withPresentation := base
	withPresentation.Presentation = &Presentation{
		Audiences: []PresentationAudience{PresentationAudienceUser},
		Surfaces:  []PresentationSurface{PresentationSurfaceChat},
		Format:    PresentationFormatDiff,
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}
	presentationJSON, err := json.Marshal(withPresentation)
	if err != nil {
		t.Fatalf("marshal presentation: %v", err)
	}
	if sha256.Sum256(baseJSON) == sha256.Sum256(presentationJSON) {
		t.Fatal("full-artifact hash would ignore presentation")
	}
}
