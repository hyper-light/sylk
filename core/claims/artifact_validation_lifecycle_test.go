package claims

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestArtifactJSONRoundTripPreservesLifecycleFields(t *testing.T) {
	registry := NewTypeRegistry()
	if err := RegisterBuiltinArtifactDataTypes(registry); err != nil {
		t.Fatal(err)
	}
	artifact := fixtureTypedArtifact(t, registry)
	artifact.Errors = []*ArtifactError{{
		Category:    ArtifactErrorCategoryValidation,
		Description: "validation failed",
		Payload:     map[string]any{"validation_id": artifactFixtureValidationID},
		Source:      DegradedAgentRef("architect", "test"),
		OccurredAt:  fixtureClock(),
	}}

	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Artifact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClaimID != artifact.ClaimID || decoded.ArtifactName != artifact.ArtifactName || decoded.DataType != artifact.DataType {
		t.Fatalf("decoded artifact lost typed fields: %+v", decoded)
	}
	if decoded.Status != ArtifactStatusGenerated || len(decoded.StatusHistory) != 1 || len(decoded.Errors) != 1 {
		t.Fatalf("decoded artifact lost lifecycle fields: %+v", decoded)
	}
}

func TestArtifactInvalidStatusRejectedAndLegacyDeserializesZero(t *testing.T) {
	var invalid Artifact
	if err := json.Unmarshal([]byte(`{"id":"a","status":"bogus"}`), &invalid); err == nil {
		t.Fatal("expected invalid artifact status error")
	}
	var legacy Artifact
	if err := json.Unmarshal([]byte(`{"id":"a","kind":"plan_markdown","reference":"# Plan"}`), &legacy); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if legacy.Status != "" || ArtifactStatusCompatibilityProjection(legacy.Status) != ArtifactStatusAttached {
		t.Fatalf("legacy status projection = %q/%q", legacy.Status, ArtifactStatusCompatibilityProjection(legacy.Status))
	}
}

func TestValidationJSONCloneAndDeclarationValidation(t *testing.T) {
	validation := fixtureTypedValidation()
	validation.Error = &ValidationError{
		Category:    ValidationErrorCategoryHandler,
		Description: "bad data",
		Payload:     map[string]any{"field": "markdown"},
		Source:      DegradedAgentRef("architect", "test"),
		OccurredAt:  fixtureClock(),
	}
	validation.EvaluatorRef = ptrParticipantRef(DegradedAgentRef("architect", "test"))
	validation.EvaluatedAt = fixtureClock().Add(time.Second)

	data, err := json.Marshal(validation)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Validation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ValidateTypedValidationDeclaration(&decoded); err != nil {
		t.Fatalf("typed declaration: %v", err)
	}
	claim := &Claim{ID: artifactFixtureClaimID, Validations: []*Validation{&decoded}}
	clone := CloneClaimEntity(claim)
	clone.Validations[0].StatusHistory[0].Reason = "mutated"
	clone.Validations[0].Error.Payload["field"] = "changed"
	if decoded.StatusHistory[0].Reason == "mutated" || decoded.Error.Payload["field"] == "changed" {
		t.Fatal("CloneClaimEntity exposed validation mutable fields")
	}
}

func TestValidationDeclarationAllowsQualityBarOnlyLegacy(t *testing.T) {
	legacy := &Validation{
		ID:          "legacy",
		Description: "review",
		QualityBar:  "looks correct",
		Type:        ValidationTypeInspection,
		Status:      ValidationStatusPending,
		Required:    true,
	}
	if err := ValidateTypedValidationDeclaration(legacy); err != nil {
		t.Fatalf("legacy quality-bar validation rejected: %v", err)
	}
	typed := &Validation{ID: "typed", ValidatorID: "plan.markdown", ArtifactDataType: ArtifactDataTypePlanMarkdown}
	if err := ValidateTypedValidationDeclaration(typed); err == nil {
		t.Fatal("expected missing TargetArtifactName error")
	}
}

func TestArtifactAndValidationTransitionTables(t *testing.T) {
	for _, step := range []struct {
		from ArtifactStatus
		to   ArtifactStatus
	}{
		{ArtifactStatusGenerated, ArtifactStatusReceived},
		{ArtifactStatusGenerated, ArtifactStatusAttached},
		{ArtifactStatusReceived, ArtifactStatusAttached},
		{ArtifactStatusAttached, ArtifactStatusValidating},
		{ArtifactStatusValidating, ArtifactStatusValidated},
		{ArtifactStatusValidating, ArtifactStatusValidationFailed},
	} {
		if !CanTransitionArtifactStatus(step.from, step.to) {
			t.Fatalf("artifact transition %s -> %s rejected", step.from, step.to)
		}
	}
	if CanTransitionArtifactStatus(ArtifactStatusGenerated, ArtifactStatusValidated) {
		t.Fatal("generated -> validated should be rejected")
	}

	for _, step := range []struct {
		from ValidationStatus
		to   ValidationStatus
	}{
		{ValidationStatusReady, ValidationStatusValidating},
		{ValidationStatusValidating, ValidationStatusValidated},
		{ValidationStatusValidating, ValidationStatusValidatingQualityBar},
		{ValidationStatusValidating, ValidationStatusValidationFailedNotRequired},
		{ValidationStatusValidatingQualityBar, ValidationStatusQualityBarValidationFailed},
	} {
		if !CanTransitionValidationStatus(step.from, step.to) {
			t.Fatalf("validation transition %s -> %s rejected", step.from, step.to)
		}
	}
	if CanTransitionValidationStatus(ValidationStatusReady, ValidationStatusValidated) {
		t.Fatal("ready -> validated should be rejected")
	}
}

func TestDuplicateTerminalTransitionIsNoopAndInvalidReturnsTypedError(t *testing.T) {
	artifact := &Artifact{ID: "a", Status: ArtifactStatusValidating}
	applied, err := TransitionArtifactStatus(artifact, ArtifactStatusValidated, "architect", "done", fixtureClock())
	if err != nil || !applied {
		t.Fatalf("first transition applied=%v err=%v", applied, err)
	}
	applied, err = TransitionArtifactStatus(artifact, ArtifactStatusValidated, "architect", "duplicate", fixtureClock().Add(time.Second))
	if err != nil || applied || len(artifact.StatusHistory) != 1 {
		t.Fatalf("duplicate transition applied=%v err=%v history=%d", applied, err, len(artifact.StatusHistory))
	}
	_, err = TransitionArtifactStatus(artifact, ArtifactStatusValidating, "architect", "reopen", fixtureClock())
	var transitionErr *LifecycleTransitionError
	if !errors.As(err, &transitionErr) || transitionErr.Actor != "architect" {
		t.Fatalf("expected typed transition error with actor, got %v", err)
	}
}

func TestCloneArtifactDefensiveCopiesLifecycleData(t *testing.T) {
	registry := NewTypeRegistry()
	if err := RegisterBuiltinArtifactDataTypes(registry); err != nil {
		t.Fatal(err)
	}
	artifact := fixtureTypedArtifact(t, registry)
	artifact.Errors = []*ArtifactError{{Payload: map[string]any{"x": "y"}}}
	clone := CloneArtifact(artifact)
	clone.Data[0] = 0
	clone.StatusHistory[0].Reason = "changed"
	clone.Errors[0].Payload["x"] = "z"
	if artifact.Data[0] == 0 || artifact.StatusHistory[0].Reason == "changed" || artifact.Errors[0].Payload["x"] == "z" {
		t.Fatal("CloneArtifact exposed mutable fields")
	}
}

func ptrParticipantRef(ref ParticipantRef) *ParticipantRef {
	return &ref
}
