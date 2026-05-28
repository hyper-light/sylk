package claims

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var artifactTerminalStatuses = map[ArtifactStatus]bool{
	ArtifactStatusGenerationFailed: true,
	ArtifactStatusReceiptFailed:    true,
	ArtifactStatusValidationFailed: true,
	ArtifactStatusValidated:        true,
}

var artifactFailureStatuses = map[ArtifactStatus]bool{
	ArtifactStatusGenerationFailed: true,
	ArtifactStatusReceiptFailed:    true,
	ArtifactStatusValidationFailed: true,
}

var artifactStatusTransitions = map[ArtifactStatus]map[ArtifactStatus]bool{
	"": {
		ArtifactStatusGenerated:        true,
		ArtifactStatusGenerationFailed: true,
	},
	ArtifactStatusGenerated: {
		ArtifactStatusReceived:      true,
		ArtifactStatusReceiptFailed: true,
		ArtifactStatusAttached:      true,
	},
	ArtifactStatusReceived: {
		ArtifactStatusAttached:      true,
		ArtifactStatusReceiptFailed: true,
	},
	ArtifactStatusAttached: {
		ArtifactStatusValidating: true,
	},
	ArtifactStatusValidating: {
		ArtifactStatusValidated:        true,
		ArtifactStatusValidationFailed: true,
	},
}

var validationTerminalStatuses = map[ValidationStatus]bool{
	ValidationStatusValidationFailed:                      true,
	ValidationStatusValidationFailedNotRequired:           true,
	ValidationStatusErrored:                               true,
	ValidationStatusErroredNotRequired:                    true,
	ValidationStatusQualityBarValidationFailed:            true,
	ValidationStatusQualityBarValidationFailedNotRequired: true,
	ValidationStatusValidated:                             true,
	ValidationStatusPassed:                                true,
	ValidationStatusIncomplete:                            true,
	ValidationStatusFailed:                                true,
	ValidationStatusSkipped:                               true,
}

var validationNegativeTerminalStatuses = map[ValidationStatus]bool{
	ValidationStatusValidationFailed:                      true,
	ValidationStatusValidationFailedNotRequired:           true,
	ValidationStatusErrored:                               true,
	ValidationStatusErroredNotRequired:                    true,
	ValidationStatusQualityBarValidationFailed:            true,
	ValidationStatusQualityBarValidationFailedNotRequired: true,
	ValidationStatusIncomplete:                            true,
	ValidationStatusFailed:                                true,
}

var validationLifecycleStatuses = map[ValidationStatus]bool{
	ValidationStatusReady:                                 true,
	ValidationStatusValidating:                            true,
	ValidationStatusValidationFailed:                      true,
	ValidationStatusValidationFailedNotRequired:           true,
	ValidationStatusErrored:                               true,
	ValidationStatusErroredNotRequired:                    true,
	ValidationStatusValidatingQualityBar:                  true,
	ValidationStatusQualityBarValidationFailed:            true,
	ValidationStatusQualityBarValidationFailedNotRequired: true,
	ValidationStatusValidated:                             true,
}

var legacyValidationStatuses = map[ValidationStatus]bool{
	ValidationStatusPending:    true,
	ValidationStatusInProgress: true,
	ValidationStatusPassed:     true,
	ValidationStatusIncomplete: true,
	ValidationStatusFailed:     true,
	ValidationStatusErrored:    true,
	ValidationStatusSkipped:    true,
}

var validationStatusTransitions = map[ValidationStatus]map[ValidationStatus]bool{
	"": {
		ValidationStatusReady: true,
	},
	ValidationStatusReady: {
		ValidationStatusValidating: true,
	},
	ValidationStatusValidating: {
		ValidationStatusValidated:                   true,
		ValidationStatusValidatingQualityBar:        true,
		ValidationStatusValidationFailed:            true,
		ValidationStatusValidationFailedNotRequired: true,
		ValidationStatusErrored:                     true,
		ValidationStatusErroredNotRequired:          true,
	},
	ValidationStatusValidatingQualityBar: {
		ValidationStatusValidated:                             true,
		ValidationStatusQualityBarValidationFailed:            true,
		ValidationStatusQualityBarValidationFailedNotRequired: true,
	},
}

// KnownArtifactStatuses returns the closed artifact lifecycle state set.
func KnownArtifactStatuses() []ArtifactStatus {
	return []ArtifactStatus{
		ArtifactStatusGenerated,
		ArtifactStatusGenerationFailed,
		ArtifactStatusReceived,
		ArtifactStatusReceiptFailed,
		ArtifactStatusAttached,
		ArtifactStatusValidating,
		ArtifactStatusValidationFailed,
		ArtifactStatusValidated,
	}
}

// KnownValidationLifecycleStatuses returns the typed validation lifecycle
// state set, excluding legacy compatibility projections.
func KnownValidationLifecycleStatuses() []ValidationStatus {
	return []ValidationStatus{
		ValidationStatusReady,
		ValidationStatusValidating,
		ValidationStatusValidationFailed,
		ValidationStatusValidationFailedNotRequired,
		ValidationStatusErrored,
		ValidationStatusErroredNotRequired,
		ValidationStatusValidatingQualityBar,
		ValidationStatusQualityBarValidationFailed,
		ValidationStatusQualityBarValidationFailedNotRequired,
		ValidationStatusValidated,
	}
}

// KnownValidationStatuses returns both typed lifecycle states and legacy
// compatibility states accepted on durable read paths.
func KnownValidationStatuses() []ValidationStatus {
	return append(KnownValidationLifecycleStatuses(),
		ValidationStatusPending,
		ValidationStatusInProgress,
		ValidationStatusPassed,
		ValidationStatusIncomplete,
		ValidationStatusFailed,
		ValidationStatusSkipped,
	)
}

func (s ArtifactStatus) Valid() bool {
	return s == "" || oneOfArtifactStatus(s, KnownArtifactStatuses()...)
}

func (s ArtifactStatus) IsTerminal() bool {
	return artifactTerminalStatuses[s]
}

func (s ArtifactStatus) IsFailure() bool {
	return artifactFailureStatuses[s]
}

func (s ValidationStatus) Valid() bool {
	return s == "" || validationLifecycleStatuses[s] || legacyValidationStatuses[s]
}

func (s ValidationStatus) IsLifecycleStatus() bool {
	return validationLifecycleStatuses[s]
}

func (s ValidationStatus) IsQualityBarState() bool {
	return s == ValidationStatusValidatingQualityBar ||
		s == ValidationStatusQualityBarValidationFailed ||
		s == ValidationStatusQualityBarValidationFailedNotRequired
}

func (s ValidationStatus) IsBlockingFailure() bool {
	return s == ValidationStatusValidationFailed ||
		s == ValidationStatusErrored ||
		s == ValidationStatusQualityBarValidationFailed ||
		s == ValidationStatusIncomplete ||
		s == ValidationStatusFailed
}

func (s ValidationStatus) IsOptionalFailure() bool {
	return s == ValidationStatusValidationFailedNotRequired ||
		s == ValidationStatusErroredNotRequired ||
		s == ValidationStatusQualityBarValidationFailedNotRequired ||
		s == ValidationStatusSkipped
}

func ValidationStatusPassesRequired(status ValidationStatus) bool {
	return status == ValidationStatusPassed || status == ValidationStatusValidated
}

func ValidationStatusPendingRequired(status ValidationStatus) bool {
	return status == ValidationStatusPending || status == ValidationStatusReady || status == ""
}

func ArtifactStatusCompatibilityProjection(status ArtifactStatus) ArtifactStatus {
	if status == "" {
		return ArtifactStatusAttached
	}
	return status
}

func ValidationStatusCompatibilityProjection(status ValidationStatus) ValidationStatus {
	switch status {
	case ValidationStatusPending:
		return ValidationStatusReady
	case ValidationStatusInProgress:
		return ValidationStatusValidating
	case ValidationStatusPassed, ValidationStatusSkipped:
		return ValidationStatusValidated
	case ValidationStatusIncomplete, ValidationStatusFailed:
		return ValidationStatusValidationFailed
	default:
		return status
	}
}

func CanTransitionArtifactStatus(from, to ArtifactStatus) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	return canTransitionStatus(from, to, artifactTerminalStatuses, artifactStatusTransitions)
}

func CanTransitionValidationStatus(from, to ValidationStatus) bool {
	if !from.IsLifecycleStatus() && from != "" {
		return false
	}
	if !to.IsLifecycleStatus() {
		return false
	}
	return canTransitionStatus(from, to, validationTerminalStatuses, validationStatusTransitions)
}

func TransitionArtifactStatus(a *Artifact, to ArtifactStatus, actor, reason string, changed time.Time) (bool, error) {
	if a == nil {
		return false, fmt.Errorf("artifact is required")
	}
	from := a.Status
	if from == to {
		return false, nil
	}
	if !CanTransitionArtifactStatus(from, to) {
		return false, newArtifactLifecycleTransitionError(a.ID, from, to, actor, "artifact status transition is not allowed")
	}
	a.Status = to
	a.StatusHistory = capStatusHistory(append(a.StatusHistory, statusChange(from, to, actor, reason, changed)))
	return true, nil
}

func TransitionValidationStatus(v *Validation, to ValidationStatus, actor, reason string, changed time.Time) (bool, error) {
	if v == nil {
		return false, fmt.Errorf("validation is required")
	}
	from := v.Status
	if from == to {
		return false, nil
	}
	if !CanTransitionValidationStatus(from, to) {
		return false, newValidationLifecycleTransitionError(v.ID, from, to, actor, "validation status transition is not allowed")
	}
	v.Status = to
	v.StatusHistory = capStatusHistory(append(v.StatusHistory, statusChange(from, to, actor, reason, changed)))
	if to.IsTerminal() {
		v.EvaluatedAt = changeTime(changed)
	}
	return true, nil
}

func ValidateTypedValidationDeclaration(v *Validation) error {
	if v == nil {
		return fmt.Errorf("validation declaration is required")
	}
	if !validationDeclaresTypedHandler(v) {
		return nil
	}
	if strings.TrimSpace(v.TargetArtifactName) == "" {
		return fmt.Errorf("typed validation %q requires TargetArtifactName", v.ID)
	}
	if strings.TrimSpace(v.ValidatorID) == "" {
		return fmt.Errorf("typed validation %q requires ValidatorID", v.ID)
	}
	if strings.TrimSpace(v.ArtifactDataType) == "" {
		return fmt.Errorf("typed validation %q requires ArtifactDataType", v.ID)
	}
	return nil
}

func (s *ArtifactStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	status := ArtifactStatus(strings.TrimSpace(raw))
	if !status.Valid() {
		return fmt.Errorf("unknown artifact status %q", raw)
	}
	*s = status
	return nil
}

func (s *ValidationStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	status := ValidationStatus(strings.TrimSpace(raw))
	if !status.Valid() {
		return fmt.Errorf("unknown validation status %q", raw)
	}
	*s = status
	return nil
}

func canTransitionStatus[S comparable](from, to S, terminal map[S]bool, transitions map[S]map[S]bool) bool {
	var zero S
	if to == zero {
		return false
	}
	if from == to {
		return true
	}
	if terminal[from] {
		return false
	}
	return transitions[from][to]
}

func oneOfArtifactStatus(status ArtifactStatus, values ...ArtifactStatus) bool {
	for _, value := range values {
		if status == value {
			return true
		}
	}
	return false
}

func validationDeclaresTypedHandler(v *Validation) bool {
	return strings.TrimSpace(v.TargetArtifactName) != "" ||
		strings.TrimSpace(v.ValidatorID) != "" ||
		strings.TrimSpace(v.ArtifactDataType) != "" ||
		strings.TrimSpace(v.ResultDataType) != ""
}

func statusChange[S ~string](from, to S, actor, reason string, changed time.Time) StatusChange {
	actor = strings.TrimSpace(actor)
	return StatusChange{
		From:          string(from),
		To:            string(to),
		Reason:        strings.TrimSpace(reason),
		AgentID:       actor,
		ParticipantID: actor,
		Changed:       changeTime(changed),
	}
}

func changeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func newArtifactLifecycleTransitionError(artifactID string, from, to ArtifactStatus, actor, reason string) error {
	return &LifecycleTransitionError{
		EntityType: RelatedTypeArtifact,
		EntityID:   strings.TrimSpace(artifactID),
		From:       string(from),
		To:         string(to),
		Actor:      strings.TrimSpace(actor),
		Reason:     strings.TrimSpace(reason),
	}
}

func newValidationLifecycleTransitionError(validationID string, from, to ValidationStatus, actor, reason string) error {
	return &LifecycleTransitionError{
		EntityType: RelatedTypeValidation,
		EntityID:   strings.TrimSpace(validationID),
		From:       string(from),
		To:         string(to),
		Actor:      strings.TrimSpace(actor),
		Reason:     strings.TrimSpace(reason),
	}
}
