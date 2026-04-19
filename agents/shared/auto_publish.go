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
