package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/skills"
)

// CrossPipelineSkillConfig wires the cross-pipeline primitives onto
// per-agent identity. The skills are pure fabric emitters — they
// produce challenge_emitted / consult_emitted activities and let the
// fabric handle delivery (via ambient context envelope on the
// addressee's next tool result).
//
// See docs/FABRIC.md Part 3: cross-pipeline collaboration.
type CrossPipelineSkillConfig struct {
	SessionID func() string
	AgentID   func() string
	AgentType func() string
	PipelineID func() string
}

// CrossPipelineSkills returns challenge_peer and consult_peer.
//
// challenge_peer generalizes today's challenge_agent — targets a fabric
// activity (not an agent directly), routes through the activity's
// author + pipeline. Same-pipeline target collapses to today's
// deterministic protocol behavior; cross-pipeline target engages the
// new path.
//
// consult_peer generalizes today's knowledge-agent consults — targets
// any peer agent in the session, including knowledge agents and
// cross-pipeline specialists.
func CrossPipelineSkills(cfg CrossPipelineSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		challengePeerSkill(cfg),
		consultPeerSkill(cfg),
	}
}

func challengePeerSkill(cfg CrossPipelineSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_peer").
		Description("Dispute a peer agent's commitment with concrete evidence. Targets a specific fabric activity (not an agent directly). The challenged peer will see your challenge in their next ambient_context envelope and respond with defend / yield / scope-split / escalate. Asynchronous — neither pipeline blocks. The dispute lives durably until resolved or it passes its deadline. PREFER THIS over silent divergence — adopt-or-challenge is the binary; never just diverge. SUPERSEDES the narrower `challenge_agent` for cross-pipeline disputes (challenge_agent stays for same-pipeline protocol).").
		Domain("fabric").
		Keywords("challenge", "fabric", "cross-pipeline", "dispute", "peer", "disagree", "evidence", "diverge").
		Priority(92).
		Usage("Use when you genuinely disagree with a peer's commitment (decision, claim, plan step) — typically because you have evidence they didn't have or a constraint they didn't model. Pass the activity_id (commonly surfaced in ambient_context) and your concrete evidence.").
		Requirement("Carry a specific target_activity_id and concrete evidence. Vague disagreement is not actionable — be precise.").
		Satisfies("Records a challenge_emitted activity in the fabric; the addressee sees it in their next ambient_context envelope.").
		Avoid("Don't challenge the same activity multiple times. Don't issue a challenge without evidence — the system requires concrete grounds.").
		StringParam("target_activity_id", "The activity ID being challenged (commonly from ambient_context).", true).
		StringParam("evidence", "Concrete evidence supporting your alternative position (file paths, prior conventions, downstream constraints, etc.).", true).
		StringParam("alternative", "The alternative commitment you propose. Optional — challenges that surface evidence without proposing an alternative are still valuable.", false).
		StringParam("resolution_hint", "Optional hint to guide the responder: \"yield\", \"scope-split\", or \"escalate\". Empty = let them choose freely.", false).
		IntParam("deadline_seconds", "How long the challenge stays open before fabric emits a challenge_unresolved activity. Default 600 (10 min).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TargetActivityID string `json:"target_activity_id"`
				Evidence         string `json:"evidence"`
				Alternative      string `json:"alternative"`
				ResolutionHint   string `json:"resolution_hint"`
				DeadlineSeconds  int    `json:"deadline_seconds"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.TargetActivityID) == "" {
				return nil, fmt.Errorf("target_activity_id is required")
			}
			if strings.TrimSpace(params.Evidence) == "" {
				return nil, fmt.Errorf("evidence is required")
			}
			deadline := time.Duration(params.DeadlineSeconds) * time.Second
			if deadline <= 0 {
				deadline = 10 * time.Minute
			}
			payload, _ := json.Marshal(map[string]any{
				"target_activity_id": params.TargetActivityID,
				"evidence":           params.Evidence,
				"alternative":        params.Alternative,
				"resolution_hint":    params.ResolutionHint,
				"deadline_at":        time.Now().Add(deadline),
			})
			caused := activity.ActivityID(strings.TrimSpace(params.TargetActivityID))
			act := activity.AgentActivity{
				ID:         activity.NewActivityID(),
				SessionID:  activity.SessionID(safeCallString(cfg.SessionID)),
				Timestamp:  time.Now(),
				Resolution: activity.ResolutionFor(activity.ActionChallengeEmitted),
				Action:     activity.ActionChallengeEmitted,
				Actor: activity.Actor{
					AgentID:    safeCallString(cfg.AgentID),
					AgentType:  safeCallString(cfg.AgentType),
					PipelineID: safeCallString(cfg.PipelineID),
				},
				Subject: activity.Subject{
					TargetArtifact: params.TargetActivityID,
				},
				Payload: payload,
				Caused:  &caused,
				State:   activity.StateInFlight,
				Evidence: []activity.EvidenceRef{
					{Kind: activity.EvidenceActivity, Ref: params.TargetActivityID},
				},
			}
			activity.Append(ctx, act)
			return map[string]any{
				"challenge_id": act.ID,
				"deadline_at":  time.Now().Add(deadline),
				"status":       "in_flight",
			}, nil
		}).
		Build()
}

func consultPeerSkill(cfg CrossPipelineSkillConfig) *skills.Skill {
	return skills.NewSkill("consult_peer").
		Description("Ask a peer agent (cross-pipeline specialist or knowledge agent) for their evidence on a shared concern. Asynchronous — the addressee sees your consult in their next ambient_context envelope and responds at their own cadence. Returns a consult_id you can causal_trace later to find the response. PREFER THIS over guessing when peer state matters.").
		Domain("fabric").
		Keywords("consult", "fabric", "cross-pipeline", "peer", "knowledge-agent", "ask", "question").
		Priority(91).
		Usage("Use when ambient context shows a peer working in adjacent or overlapping scope and you'd benefit from their live state — e.g., 'how are you handling fixtures for shared models?' Pass target_agent_type and (optional) target_pipeline_id; without pipeline_id the consult routes to the natural same-pipeline peer or knowledge agent.").
		Requirement("Frame the question concretely. Vague consults waste both parties' attention budget.").
		Satisfies("Records a consult_emitted activity addressed to the target; the addressee sees it in their next ambient_context envelope.").
		StringParam("target_agent_type", "Agent type to address (e.g., \"librarian\", \"academic\", \"tester-pipeline\", \"engineer\").", true).
		StringParam("target_pipeline_id", "Specific pipeline_id to address (cross-pipeline routing). Empty = natural same-pipeline peer or knowledge agent.", false).
		StringParam("scope", "Path scope context (e.g., \"services/billing/tests/\").", false).
		StringParam("query", "Your concrete question.", true).
		IntParam("deadline_seconds", "How long the consult stays open before fabric emits a consult_unanswered activity. Default 180 (3 min).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TargetAgentType  string `json:"target_agent_type"`
				TargetPipelineID string `json:"target_pipeline_id"`
				Scope            string `json:"scope"`
				Query            string `json:"query"`
				DeadlineSeconds  int    `json:"deadline_seconds"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			if strings.TrimSpace(params.TargetAgentType) == "" {
				return nil, fmt.Errorf("target_agent_type is required")
			}
			deadline := time.Duration(params.DeadlineSeconds) * time.Second
			if deadline <= 0 {
				deadline = 3 * time.Minute
			}
			targetAddress := strings.TrimSpace(params.TargetAgentType)
			if pipe := strings.TrimSpace(params.TargetPipelineID); pipe != "" {
				targetAddress += "/" + pipe
			}
			payload, _ := json.Marshal(map[string]any{
				"target_agent_type":  params.TargetAgentType,
				"target_pipeline_id": params.TargetPipelineID,
				"scope":              params.Scope,
				"query":              params.Query,
				"deadline_at":        time.Now().Add(deadline),
			})
			act := activity.AgentActivity{
				ID:         activity.NewActivityID(),
				SessionID:  activity.SessionID(safeCallString(cfg.SessionID)),
				Timestamp:  time.Now(),
				Resolution: activity.ResolutionFor(activity.ActionConsultEmitted),
				Action:     activity.ActionConsultEmitted,
				Actor: activity.Actor{
					AgentID:    safeCallString(cfg.AgentID),
					AgentType:  safeCallString(cfg.AgentType),
					PipelineID: safeCallString(cfg.PipelineID),
				},
				Subject: activity.Subject{
					TargetAgent: targetAddress,
					PathPrefix:  strings.TrimSpace(params.Scope),
				},
				Payload: payload,
				State:   activity.StateInFlight,
			}
			activity.Append(ctx, act)
			return map[string]any{
				"consult_id":  act.ID,
				"deadline_at": time.Now().Add(deadline),
				"status":      "in_flight",
			}, nil
		}).
		Build()
}

// RespondToChallenge emits a challenge_response activity that resolves
// a previously-issued challenge_emitted. The four resolutions are
// defend / yield / scope-split / escalate; see docs/FABRIC.md Part 3
// for semantics. Programmatic helper — not surfaced as a skill since
// the response shape is shaped by the original challenge.
func RespondToChallenge(ctx context.Context, cfg CrossPipelineSkillConfig, in ChallengeResponseInput) (activity.ActivityID, error) {
	if strings.TrimSpace(string(in.ChallengeID)) == "" {
		return "", fmt.Errorf("challenge_id is required")
	}
	if in.Resolution == "" {
		return "", fmt.Errorf("resolution is required")
	}
	payload, _ := json.Marshal(map[string]any{
		"resolution":      string(in.Resolution),
		"counter_evidence": in.CounterEvidence,
		"narrowed_scope":   in.NarrowedScope,
		"escalation_to":    in.EscalationTo,
	})
	resolves := in.ChallengeID
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(safeCallString(cfg.SessionID)),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionChallengeResponse),
		Action:     activity.ActionChallengeResponse,
		Actor: activity.Actor{
			AgentID:    safeCallString(cfg.AgentID),
			AgentType:  safeCallString(cfg.AgentType),
			PipelineID: safeCallString(cfg.PipelineID),
		},
		Subject: activity.Subject{
			TargetArtifact: string(in.ChallengeID),
		},
		Payload:  payload,
		State:    activity.StateResolved,
		Resolves: &resolves,
	}
	activity.Append(ctx, act)
	return act.ID, nil
}

// ChallengeResponseInput is the typed input to RespondToChallenge.
type ChallengeResponseInput struct {
	ChallengeID     activity.ActivityID
	Resolution      ChallengeResolution
	CounterEvidence string
	NarrowedScope   string
	EscalationTo    string
}

// ChallengeResolution is the typed enum of cross-pipeline challenge
// outcomes.
type ChallengeResolution string

const (
	ChallengeResolutionDefend     ChallengeResolution = "defend"
	ChallengeResolutionYield      ChallengeResolution = "yield"
	ChallengeResolutionScopeSplit ChallengeResolution = "scope-split"
	ChallengeResolutionEscalate   ChallengeResolution = "escalate"
)

// RespondToConsult emits a consult_response activity resolving a
// consult_emitted. Programmatic helper.
func RespondToConsult(ctx context.Context, cfg CrossPipelineSkillConfig, consultID activity.ActivityID, response string) (activity.ActivityID, error) {
	if strings.TrimSpace(string(consultID)) == "" {
		return "", fmt.Errorf("consult_id is required")
	}
	payload, _ := json.Marshal(map[string]any{
		"response": response,
	})
	resolves := consultID
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(safeCallString(cfg.SessionID)),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionConsultResponse),
		Action:     activity.ActionConsultResponse,
		Actor: activity.Actor{
			AgentID:    safeCallString(cfg.AgentID),
			AgentType:  safeCallString(cfg.AgentType),
			PipelineID: safeCallString(cfg.PipelineID),
		},
		Subject: activity.Subject{
			TargetArtifact: string(consultID),
		},
		Payload:  payload,
		State:    activity.StateResolved,
		Resolves: &resolves,
	}
	activity.Append(ctx, act)
	return act.ID, nil
}
