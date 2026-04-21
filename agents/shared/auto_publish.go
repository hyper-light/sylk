package shared

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// AutoPublishDecision is the small helper every agent skill calls
// after performing its primary work, to emit a typed decision
// projection to the Activity Fabric. The skill's result is the
// source of truth; the fabric activity carries the typed projection
// for cross-pipeline visibility, ambient context, knowledge-agent
// learning, and Memory Forest precedent harvest.
//
// Per the FABRIC.md no-gates principle: this MUST be called AFTER
// the skill's primary work succeeds. It is never a precondition.
// Failure to emit is silently swallowed — the fabric is additive.
func AutoPublishDecision(ctx context.Context, in AutoPublishInput) {
	if strings.TrimSpace(in.Domain) == "" || strings.TrimSpace(in.Value) == "" {
		return
	}
	confidence := normalizeConfidence(in.Confidence)
	subject := activity.Subject{
		Domain:      strings.TrimSpace(in.Domain),
		PathPrefix:  strings.TrimSpace(in.Scope),
		Coordinates: cloneCoordinates(in.Coordinates),
	}
	payload, _ := json.Marshal(map[string]any{
		"value":         strings.TrimSpace(in.Value),
		"author":        in.AuthorAgentType + "/" + in.AuthorAgentID,
		"trigger_skill": in.TriggerSkill,
		"evidence":      in.Evidence,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionDecisionDeclared),
		Action:     activity.ActionDecisionDeclared,
		Actor: activity.Actor{
			AgentID:    in.AuthorAgentID,
			AgentType:  in.AuthorAgentType,
			PipelineID: in.AuthorPipelineID,
		},
		Subject:    subject,
		Payload:    payload,
		State:      activity.StatePoint,
		Confidence: confidence,
	}
	activity.Append(ctx, act)
}

// AutoPublishInput is the typed input to AutoPublishDecision.
type AutoPublishInput struct {
	SessionID        string
	AuthorAgentID    string
	AuthorAgentType  string
	AuthorPipelineID string
	TriggerSkill     string
	Domain           string // e.g., "test_framework", "build_backend", "ui_framework"
	Value            string // e.g., "pytest", "hatchling", "react"
	Scope            string // path prefix
	Confidence       string // "hint" | "tentative" | "committed" | "consensus"
	Coordinates      map[string]string
	Evidence         []string
}

// AutoPublishHint is a convenience wrapper that emits at Hint
// confidence — for discovery skills (discover_project_tools,
// component_search, etc.) that observe pre-existing facts.
func AutoPublishHint(ctx context.Context, in AutoPublishInput) {
	in.Confidence = "hint"
	AutoPublishDecision(ctx, in)
}

// AutoPublishTentative is a convenience wrapper that emits at
// Tentative confidence — for planning skills (plan_tests,
// detect_test_harness) that intend a direction without yet acting.
func AutoPublishTentative(ctx context.Context, in AutoPublishInput) {
	in.Confidence = "tentative"
	AutoPublishDecision(ctx, in)
}

// AutoPublishCommitted is a convenience wrapper that emits at
// Committed confidence — for mutation skills (write_test, format,
// lint, write_pipeline_file, component_create) that have actually
// performed work.
func AutoPublishCommitted(ctx context.Context, in AutoPublishInput) {
	in.Confidence = "committed"
	AutoPublishDecision(ctx, in)
}

// AutoPublishConsensus is a convenience wrapper that emits at
// Consensus confidence — for acceptance skills (finalize_pipeline,
// accept_checkpoint, validate_work success) that ratify prior work.
func AutoPublishConsensus(ctx context.Context, in AutoPublishInput) {
	in.Confidence = "consensus"
	AutoPublishDecision(ctx, in)
}

// ─── Claim emitters ──────────────────────────────────────────────────

// AutoPublishClaimInput drives AutoPublishClaimAcquired /
// AutoPublishClaimReleased.
type AutoPublishClaimInput struct {
	SessionID       string
	ActorAgentID    string
	ActorAgentType  string
	ActorPipelineID string
	TriggerSkill    string
	ClaimID         string
	ScopeKind       string
	ScopeKey        string
	Mode            string // "exclusive", "shared", "review"
	Purpose         string
	LeaseSeconds    int
	Resolution      string // release reason
	Evidence        []string
}

// AutoPublishClaimAcquired emits ActionClaimAcquired directly from the
// skill handler, skipping the orchestrator round-trip. Returns the
// minted claim ID (same as the activity ID so lookups are O(1)).
func AutoPublishClaimAcquired(ctx context.Context, in AutoPublishClaimInput) activity.ActivityID {
	claimID := strings.TrimSpace(in.ClaimID)
	if claimID == "" {
		claimID = string(activity.NewActivityID())
	}
	leaseExpires := time.Time{}
	if in.LeaseSeconds > 0 {
		leaseExpires = time.Now().Add(time.Duration(in.LeaseSeconds) * time.Second)
	}
	payload := mustMarshal(map[string]any{
		"claim_id":         claimID,
		"scope_kind":       strings.TrimSpace(in.ScopeKind),
		"scope_key":        strings.TrimSpace(in.ScopeKey),
		"mode":             strings.TrimSpace(in.Mode),
		"purpose":          strings.TrimSpace(in.Purpose),
		"lease_expires_at": leaseExpires,
		"evidence":         in.Evidence,
		"trigger_skill":    in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionClaimAcquired),
		Action:     activity.ActionClaimAcquired,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			PathPrefix:     strings.TrimSpace(in.ScopeKey),
			TargetArtifact: claimID,
		},
		Payload: payload,
		State:   activity.StateInFlight,
	}
	activity.Append(ctx, act)
	return activity.ActivityID(claimID)
}

// AutoPublishClaimReleased emits ActionClaimReleased resolving a prior
// claim. The `resolves` argument is the activity ID returned from the
// acquire call (when available) so the fabric DAG links the pair.
func AutoPublishClaimReleased(ctx context.Context, in AutoPublishClaimInput, resolves activity.ActivityID) activity.ActivityID {
	payload := mustMarshal(map[string]any{
		"claim_id":      strings.TrimSpace(in.ClaimID),
		"scope_kind":    strings.TrimSpace(in.ScopeKind),
		"scope_key":     strings.TrimSpace(in.ScopeKey),
		"resolution":    strings.TrimSpace(in.Resolution),
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionClaimReleased),
		Action:     activity.ActionClaimReleased,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			PathPrefix:     strings.TrimSpace(in.ScopeKey),
			TargetArtifact: strings.TrimSpace(in.ClaimID),
		},
		Payload:  payload,
		State:    activity.StateResolved,
		Resolves: resolvesPtr(resolves),
	}
	activity.Append(ctx, act)
	return act.ID
}

// ─── Artifact / review / validation / advisory emitters ──────────────

// AutoPublishArtifactInput drives AutoPublishArtifactPublished. The
// activity Subject.TargetArtifact carries the artifact ID; Payload
// carries kind + summary + evidence for ambient rendering.
type AutoPublishArtifactInput struct {
	SessionID        string
	AuthorAgentID    string
	AuthorAgentType  string
	AuthorPipelineID string
	TriggerSkill     string
	ArtifactID       string
	Kind             string // "risk_map", "test_plan", "implementation_plan", etc.
	Title            string
	Summary          string
	Scope            string
	Evidence         []string
	Payload          map[string]any
}

// AutoPublishArtifactPublished emits an ActionArtifactPublished
// activity so peers see published work in their ambient context. Use
// from handlers that produce a structured artifact (risk_map,
// test_plan, etc.).
func AutoPublishArtifactPublished(ctx context.Context, in AutoPublishArtifactInput) activity.ActivityID {
	if strings.TrimSpace(in.Kind) == "" {
		return ""
	}
	artifactID := strings.TrimSpace(in.ArtifactID)
	if artifactID == "" {
		artifactID = string(activity.NewActivityID())
	}
	payload := mustMarshal(map[string]any{
		"kind":          strings.TrimSpace(in.Kind),
		"title":         strings.TrimSpace(in.Title),
		"summary":       strings.TrimSpace(in.Summary),
		"evidence":      in.Evidence,
		"payload":       in.Payload,
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionArtifactPublished),
		Action:     activity.ActionArtifactPublished,
		Actor: activity.Actor{
			AgentID:    in.AuthorAgentID,
			AgentType:  in.AuthorAgentType,
			PipelineID: in.AuthorPipelineID,
		},
		Subject: activity.Subject{
			PathPrefix:     strings.TrimSpace(in.Scope),
			TargetArtifact: artifactID,
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
	return act.ID
}

// AutoPublishReviewInput drives AutoPublishReviewRequested /
// AutoPublishReviewCompleted.
type AutoPublishReviewInput struct {
	SessionID         string
	ActorAgentID      string
	ActorAgentType    string
	ActorPipelineID   string
	TriggerSkill      string
	ReviewID          string
	ArtifactID        string
	ReviewerAgentType string
	Status            string // for completion: "accepted", "changes_requested", "rejected"
	Summary           string
	Criteria          []string
}

// AutoPublishReviewRequested emits ActionReviewRequested. Peers see
// the pending review in ambient context.
func AutoPublishReviewRequested(ctx context.Context, in AutoPublishReviewInput) activity.ActivityID {
	reviewID := strings.TrimSpace(in.ReviewID)
	if reviewID == "" {
		reviewID = string(activity.NewActivityID())
	}
	payload := mustMarshal(map[string]any{
		"review_id":           reviewID,
		"artifact_id":         strings.TrimSpace(in.ArtifactID),
		"reviewer_agent_type": strings.TrimSpace(in.ReviewerAgentType),
		"summary":             strings.TrimSpace(in.Summary),
		"criteria":            in.Criteria,
		"trigger_skill":       in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionReviewRequested),
		Action:     activity.ActionReviewRequested,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			TargetAgent:    strings.TrimSpace(in.ReviewerAgentType),
			TargetArtifact: strings.TrimSpace(in.ArtifactID),
		},
		Payload: payload,
		State:   activity.StateInFlight,
	}
	activity.Append(ctx, act)
	return act.ID
}

// AutoPublishReviewCompleted emits ActionReviewCompleted resolving a
// previous request.
func AutoPublishReviewCompleted(ctx context.Context, in AutoPublishReviewInput, resolves activity.ActivityID) activity.ActivityID {
	payload := mustMarshal(map[string]any{
		"review_id":     strings.TrimSpace(in.ReviewID),
		"artifact_id":   strings.TrimSpace(in.ArtifactID),
		"status":        strings.TrimSpace(in.Status),
		"summary":       strings.TrimSpace(in.Summary),
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionReviewCompleted),
		Action:     activity.ActionReviewCompleted,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			TargetArtifact: strings.TrimSpace(in.ArtifactID),
		},
		Payload:  payload,
		State:    activity.StateResolved,
		Resolves: resolvesPtr(resolves),
	}
	activity.Append(ctx, act)
	return act.ID
}

// AutoPublishValidationInput drives the validation trio.
type AutoPublishValidationInput struct {
	SessionID        string
	ActorAgentID     string
	ActorAgentType   string
	ActorPipelineID  string
	TriggerSkill     string
	EpochID          string
	Scope            string
	Summary          string
	Evidence         []string
	FailureCount     int
	IssueCount       int
}

// AutoPublishValidationStarted emits ActionValidationStarted (in-flight).
func AutoPublishValidationStarted(ctx context.Context, in AutoPublishValidationInput) activity.ActivityID {
	payload := mustMarshal(map[string]any{
		"epoch_id":      strings.TrimSpace(in.EpochID),
		"summary":       strings.TrimSpace(in.Summary),
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionValidationStarted),
		Action:     activity.ActionValidationStarted,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			PathPrefix:     strings.TrimSpace(in.Scope),
			TargetArtifact: strings.TrimSpace(in.EpochID),
		},
		Payload: payload,
		State:   activity.StateInFlight,
	}
	activity.Append(ctx, act)
	return act.ID
}

// AutoPublishValidationAccepted emits ActionValidationAccepted resolving
// a prior validation_started.
func AutoPublishValidationAccepted(ctx context.Context, in AutoPublishValidationInput, resolves activity.ActivityID) activity.ActivityID {
	return autoPublishValidationOutcome(ctx, in, resolves, activity.ActionValidationAccepted)
}

// AutoPublishValidationRejected emits ActionValidationRejected resolving
// a prior validation_started.
func AutoPublishValidationRejected(ctx context.Context, in AutoPublishValidationInput, resolves activity.ActivityID) activity.ActivityID {
	return autoPublishValidationOutcome(ctx, in, resolves, activity.ActionValidationRejected)
}

func autoPublishValidationOutcome(ctx context.Context, in AutoPublishValidationInput, resolves activity.ActivityID, kind activity.ActionKind) activity.ActivityID {
	payload := mustMarshal(map[string]any{
		"epoch_id":      strings.TrimSpace(in.EpochID),
		"summary":       strings.TrimSpace(in.Summary),
		"evidence":      in.Evidence,
		"failure_count": in.FailureCount,
		"issue_count":   in.IssueCount,
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(kind),
		Action:     kind,
		Actor: activity.Actor{
			AgentID:    in.ActorAgentID,
			AgentType:  in.ActorAgentType,
			PipelineID: in.ActorPipelineID,
		},
		Subject: activity.Subject{
			PathPrefix:     strings.TrimSpace(in.Scope),
			TargetArtifact: strings.TrimSpace(in.EpochID),
		},
		Payload:  payload,
		State:    activity.StateResolved,
		Resolves: resolvesPtr(resolves),
	}
	activity.Append(ctx, act)
	return act.ID
}

// AutoPublishAdvisoryInput drives AutoPublishAdvisory.
type AutoPublishAdvisoryInput struct {
	SessionID        string
	AuthorAgentID    string
	AuthorAgentType  string
	AuthorPipelineID string
	TriggerSkill     string
	Domain           string
	Value            string
	Scope            string
	Summary          string
	Evidence         []string
}

// AutoPublishAdvisory emits ActionAdvisoryEmitted — a peer-visible
// recommendation that isn't a typed commitment. Use for engineer
// audit output, tester diagnose_failure, etc.
func AutoPublishAdvisory(ctx context.Context, in AutoPublishAdvisoryInput) activity.ActivityID {
	if strings.TrimSpace(in.Domain) == "" {
		return ""
	}
	payload := mustMarshal(map[string]any{
		"domain":        strings.TrimSpace(in.Domain),
		"value":         strings.TrimSpace(in.Value),
		"summary":       strings.TrimSpace(in.Summary),
		"evidence":      in.Evidence,
		"trigger_skill": in.TriggerSkill,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(in.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionAdvisoryEmitted),
		Action:     activity.ActionAdvisoryEmitted,
		Actor: activity.Actor{
			AgentID:    in.AuthorAgentID,
			AgentType:  in.AuthorAgentType,
			PipelineID: in.AuthorPipelineID,
		},
		Subject: activity.Subject{
			Domain:     strings.TrimSpace(in.Domain),
			PathPrefix: strings.TrimSpace(in.Scope),
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
	return act.ID
}

// ─── Helpers ──────────────────────────────────────────────────────────

func mustMarshal(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func resolvesPtr(id activity.ActivityID) *activity.ActivityID {
	if strings.TrimSpace(string(id)) == "" {
		return nil
	}
	out := id
	return &out
}

func normalizeConfidence(s string) activity.Confidence {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hint":
		return activity.ConfidenceHint
	case "tentative":
		return activity.ConfidenceTentative
	case "committed":
		return activity.ConfidenceCommitted
	case "consensus":
		return activity.ConfidenceConsensus
	}
	return ""
}

func cloneCoordinates(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
