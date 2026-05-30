package claims

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// correctiveRateLimiter prevents flooding the board with corrective
// claims from the same (agent, tool) pair. Max 1 per 10-second window.
var correctiveRateLimiter = &correctiveRL{
	last: make(map[string]time.Time),
}

type correctiveRL struct {
	mu   sync.Mutex
	last map[string]time.Time
}

const correctiveWindow = 10 * time.Second

const (
	defaultMaxCorrectiveClaimsPerOutcome = 8
	systemLifecycleAgentID               = "system:lifecycle"
)

type RemediationPolicy struct {
	EnableMissingArtifactCorrectives   bool
	EnableValidationFailureCorrectives bool
	EnableValidationErroredCorrectives bool
	MaxCorrectiveClaimsPerOutcome      int
}

type correctiveClaimSpec struct {
	idempotencyKey string
	issuerID       string
	subjectID      string
	title          string
	description    string
	scope          []ClaimScopeEntry
	relations      []Relation
}

func normalizeRemediationPolicy(policy *RemediationPolicy) RemediationPolicy {
	if policy == nil {
		return RemediationPolicy{}
	}
	out := *policy
	if out.MaxCorrectiveClaimsPerOutcome <= 0 {
		out.MaxCorrectiveClaimsPerOutcome = defaultMaxCorrectiveClaimsPerOutcome
	}
	return out
}

func (p RemediationPolicy) enabled() bool {
	return p.EnableMissingArtifactCorrectives ||
		p.EnableValidationFailureCorrectives ||
		p.EnableValidationErroredCorrectives
}

func (p RemediationPolicy) maxClaims() int {
	if p.MaxCorrectiveClaimsPerOutcome > 0 {
		return p.MaxCorrectiveClaimsPerOutcome
	}
	return defaultMaxCorrectiveClaimsPerOutcome
}

func (p RemediationPolicy) allowsStatus(status TestamentLifecycleStatus) bool {
	switch status {
	case TestamentLifecycleValidationIncomplete:
		return p.EnableMissingArtifactCorrectives
	case TestamentLifecycleValidationFailed:
		return p.EnableValidationFailureCorrectives
	case TestamentLifecycleValidationErrored:
		return p.EnableValidationErroredCorrectives
	default:
		return false
	}
}

func (r *correctiveRL) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if t, ok := r.last[key]; ok && now.Sub(t) < correctiveWindow {
		return false
	}
	r.last[key] = now
	// Evict stale entries to prevent unbounded growth.
	if len(r.last) > 1000 {
		for k, t := range r.last {
			if now.Sub(t) > correctiveWindow {
				delete(r.last, k)
			}
		}
	}
	return true
}

// NewCorrectiveClaim builds a corrective claim directed at an agent.
// The system is the issuer; the agent is the subject expected to
// address the condition.
func NewCorrectiveClaim(agentID, title, description string, scope []ClaimScopeEntry) Claim {
	return Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  ActionTypeCorrective,
		Relations: []Relation{
			{Related: "system", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: strings.TrimSpace(agentID), RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}
}

// PostCorrectiveClaim posts a corrective claim to the board. Nil-safe
// — returns nil immediately when the board is unavailable. Rate-limited
// to max 1 per (agent, scope-key) per 10-second window to prevent
// flooding.
func PostCorrectiveClaim(board *ClaimsBoard, agentID, title, description string, scope []ClaimScopeEntry) error {
	if board == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}
	key := strings.TrimSpace(agentID)
	if len(scope) > 0 {
		key += ":" + scope[0].Key
	}
	if !correctiveRateLimiter.allow(key) {
		return nil
	}
	action := Action{AgentID: "system", Type: ActionTypeCorrective}
	claim := NewCorrectiveClaim(agentID, title, description, scope)
	if err := board.PostAction(context.Background(), action, []Claim{claim}); err != nil {
		slog.Error("corrective_claim_post_failed",
			"agent_id", agentID, "title", title, "error", err.Error())
		board.RecordNotificationError("corrective claim: " + err.Error())
		return err
	}
	return nil
}

func (b *ClaimsBoard) postTerminalTestamentCorrectives(ctx context.Context, testament *Testament, claim *Claim, status TestamentLifecycleStatus, actorID string) {
	if b == nil || testament == nil || claim == nil || claim.ActionType == ActionTypeCorrective {
		return
	}
	if !isTerminalTestamentValidationStatus(status) {
		return
	}
	policy := b.remediationPolicySnapshot()
	if !policy.enabled() {
		b.recordCorrectiveSkipped(testament, claim, status, "remediation policy disabled")
		return
	}
	if !policy.allowsStatus(status) {
		b.recordCorrectiveSkipped(testament, claim, status, "remediation policy disables "+string(status)+" correctives")
		return
	}
	specs := b.correctiveSpecsForTerminalTestament(policy, testament, claim, status, actorID)
	if len(specs) == 0 {
		b.recordCorrectiveSkipped(testament, claim, status, "no corrective targets matched terminal outcome")
		return
	}
	for _, spec := range specs {
		if err := b.postCorrectiveClaimSpec(contextWithoutCancellation(ctx), spec); err != nil {
			b.RecordNotificationError("corrective claim: " + err.Error())
		}
	}
}

func (b *ClaimsBoard) recordCorrectiveSkipped(testament *Testament, claim *Claim, status TestamentLifecycleStatus, reason string) {
	if b == nil || testament == nil || claim == nil {
		return
	}
	b.RecordNotificationError(strings.Join([]string{
		"corrective claim skipped",
		"claim=" + strings.TrimSpace(claim.ID),
		"testament=" + strings.TrimSpace(testament.ID),
		"status=" + strings.TrimSpace(string(status)),
		"reason=" + strings.TrimSpace(reason),
	}, " "))
}

func (b *ClaimsBoard) remediationPolicySnapshot() RemediationPolicy {
	if b == nil {
		return RemediationPolicy{}
	}
	b.mu.RLock()
	policy := b.remediationPolicy
	b.mu.RUnlock()
	return policy
}

func (b *ClaimsBoard) correctiveSpecsForTerminalTestament(policy RemediationPolicy, testament *Testament, claim *Claim, status TestamentLifecycleStatus, actorID string) []correctiveClaimSpec {
	switch status {
	case TestamentLifecycleValidationIncomplete:
		return b.missingArtifactCorrectiveSpecs(policy, testament, claim, actorID)
	case TestamentLifecycleValidationFailed:
		return b.validationFailureCorrectiveSpecs(policy, testament, claim, actorID, false)
	case TestamentLifecycleValidationErrored:
		return b.validationFailureCorrectiveSpecs(policy, testament, claim, actorID, true)
	default:
		return nil
	}
}

func (b *ClaimsBoard) missingArtifactCorrectiveSpecs(policy RemediationPolicy, testament *Testament, claim *Claim, actorID string) []correctiveClaimSpec {
	if !policy.EnableMissingArtifactCorrectives {
		return nil
	}
	artifacts := testamentArtifactNames(testament)
	var specs []correctiveClaimSpec
	for _, validation := range claim.Validations {
		if validation == nil || !validation.Required || strings.TrimSpace(validation.TargetArtifactName) == "" {
			continue
		}
		if _, ok := artifacts[validation.TargetArtifactName]; ok {
			continue
		}
		specs = appendBoundedCorrectiveSpec(specs, policy, b.missingArtifactCorrectiveSpec(testament, claim, validation, actorID))
	}
	return specs
}

func (b *ClaimsBoard) validationFailureCorrectiveSpecs(policy RemediationPolicy, testament *Testament, claim *Claim, actorID string, errored bool) []correctiveClaimSpec {
	if errored && !policy.EnableValidationErroredCorrectives {
		return nil
	}
	if !errored && !policy.EnableValidationFailureCorrectives {
		return nil
	}
	var specs []correctiveClaimSpec
	for _, validation := range claim.Validations {
		if !validationNeedsCorrective(validation, errored) {
			continue
		}
		artifactID := artifactIDForValidationTarget(testament, validation.TargetArtifactName)
		specs = appendBoundedCorrectiveSpec(specs, policy, b.validationFailureCorrectiveSpec(testament, claim, validation, artifactID, actorID, errored))
	}
	return specs
}

func appendBoundedCorrectiveSpec(specs []correctiveClaimSpec, policy RemediationPolicy, spec correctiveClaimSpec) []correctiveClaimSpec {
	if strings.TrimSpace(spec.idempotencyKey) == "" || len(specs) >= policy.maxClaims() {
		return specs
	}
	return append(specs, spec)
}

func validationNeedsCorrective(validation *Validation, errored bool) bool {
	if validation == nil || !validation.Required {
		return false
	}
	if errored {
		return validation.Status == ValidationStatusErrored || validation.Status == ValidationStatusQualityBarValidationFailed
	}
	return validation.Status == ValidationStatusValidationFailed ||
		validation.Status == ValidationStatusFailed ||
		validation.Status == ValidationStatusQualityBarValidationFailed
}

func (b *ClaimsBoard) missingArtifactCorrectiveSpec(testament *Testament, claim *Claim, validation *Validation, actorID string) correctiveClaimSpec {
	target := strings.TrimSpace(validation.TargetArtifactName)
	return b.baseCorrectiveSpec(testament, claim, validation, "", actorID,
		"Provide missing artifact "+target,
		"Submit required artifact "+target+" for claim "+strings.TrimSpace(claim.ID)+".")
}

func (b *ClaimsBoard) validationFailureCorrectiveSpec(testament *Testament, claim *Claim, validation *Validation, artifactID, actorID string, errored bool) correctiveClaimSpec {
	title := "Repair failed validation " + strings.TrimSpace(validation.ID)
	description := "Submit replacement evidence that satisfies validation " + strings.TrimSpace(validation.ID) + " for claim " + strings.TrimSpace(claim.ID) + "."
	if errored {
		title = "Repair errored validation " + strings.TrimSpace(validation.ID)
		description = "Submit evidence that can be validated without infrastructure errors for validation " + strings.TrimSpace(validation.ID) + "."
	}
	return b.baseCorrectiveSpec(testament, claim, validation, artifactID, actorID, title, description)
}

func (b *ClaimsBoard) baseCorrectiveSpec(testament *Testament, claim *Claim, validation *Validation, artifactID, actorID, title, description string) correctiveClaimSpec {
	issuerID := firstNonEmpty(strings.TrimSpace(actorID), IssuerAgentID(claim.Relations), systemLifecycleAgentID)
	subjectID := firstNonEmpty(SubjectAgentID(claim.Relations), testament.AgentID)
	validationID := ""
	target := ""
	if validation != nil {
		validationID = strings.TrimSpace(validation.ID)
		target = strings.TrimSpace(validation.TargetArtifactName)
	}
	relations := []Relation{
		{Related: issuerID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: subjectID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipCorrects},
		{Related: testament.ID, RelatedType: RelatedTypeTestament, Relationship: RelationshipCausedBy},
	}
	if validationID != "" {
		relations = append(relations, Relation{Related: validationID, RelatedType: RelatedTypeValidation, Relationship: RelationshipCausedBy})
	}
	if artifactID != "" {
		relations = append(relations,
			Relation{Related: artifactID, RelatedType: RelatedTypeArtifact, Relationship: RelationshipCausedBy},
			Relation{Related: artifactID, RelatedType: RelatedTypeArtifact, Relationship: RelationshipSupersedes},
		)
	}
	return correctiveClaimSpec{
		idempotencyKey: b.correctiveIdempotencyKey(claim.ID, testament.ID, validationID, artifactID, target),
		issuerID:       issuerID,
		subjectID:      subjectID,
		title:          strings.TrimSpace(title),
		description:    strings.TrimSpace(description),
		scope:          correctiveScopeForValidation(claim, validation, artifactID),
		relations:      relations,
	}
}

func (b *ClaimsBoard) correctiveIdempotencyKey(claimID, testamentID, validationID, artifactID, target string) string {
	return strings.Join([]string{
		"corrective",
		sanitizeSystemEvidenceSegment(b.boardID),
		sanitizeSystemEvidenceSegment(claimID),
		sanitizeSystemEvidenceSegment(testamentID),
		sanitizeSystemEvidenceSegment(validationID),
		sanitizeSystemEvidenceSegment(artifactID),
		sanitizeSystemEvidenceSegment(target),
	}, ":")
}

func correctiveScopeForValidation(claim *Claim, validation *Validation, artifactID string) []ClaimScopeEntry {
	scope := append([]ClaimScopeEntry(nil), claim.Scope...)
	if validation != nil && strings.TrimSpace(validation.TargetArtifactName) != "" {
		scope = append(scope, ClaimScopeEntry{Kind: "artifact", Key: strings.TrimSpace(validation.TargetArtifactName)})
	}
	if artifactID != "" {
		scope = append(scope, ClaimScopeEntry{Kind: "artifact_id", Key: artifactID})
	}
	return scope
}

func artifactIDForValidationTarget(testament *Testament, target string) string {
	target = strings.TrimSpace(target)
	if testament == nil || target == "" {
		return ""
	}
	for _, artifact := range testament.Artifacts {
		if artifact != nil && artifact.ArtifactName == target {
			return artifact.ID
		}
	}
	return ""
}

func (b *ClaimsBoard) postCorrectiveClaimSpec(ctx context.Context, spec correctiveClaimSpec) error {
	if strings.TrimSpace(spec.subjectID) == "" {
		return nil
	}
	claim := Claim{
		Title:       firstNonEmpty(spec.title, "Correct validation evidence"),
		Description: firstNonEmpty(spec.description, "Submit replacement evidence for failed validation."),
		ActionType:  ActionTypeCorrective,
		Scope:       append([]ClaimScopeEntry(nil), spec.scope...),
		Relations:   append([]Relation(nil), spec.relations...),
	}
	generated, err := b.GenerateClaimAction(ctx, Action{AgentID: spec.issuerID, Type: ActionTypeCorrective}, []Claim{claim}, GenerateClaimActionOptions{
		IdempotencyKey: spec.idempotencyKey,
		Reason:         "corrective claim generated from validation outcome",
	})
	if err != nil {
		return fmt.Errorf("generate corrective claim: %w", err)
	}
	if generated == nil || len(generated.Claims) == 0 {
		return fmt.Errorf("generate corrective claim returned no claims")
	}
	return b.PostGeneratedClaim(ctx, generated.Claims[0].ID, spec.issuerID, ClaimPostOptions{Reason: "corrective claim posted from validation outcome"})
}
