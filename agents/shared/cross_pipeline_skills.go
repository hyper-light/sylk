package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

// CrossPipelineSkillConfig wires the cross-pipeline primitives onto
// per-agent identity.
//
// challenge_peer is fire-and-forget: it writes a challenge_emitted
// activity and returns. Delivery is handled by the fabric via ambient
// context envelopes.
//
// consult_peer has two modes governed by RouteSync:
//   - When RouteSync is non-nil, the skill dispatches the consult
//     synchronously via the provided transport, waits for the
//     terminal response, and renders the target's stream events as
//     nested children of the caller's consult_peer row. This is the
//     primary mode for interactive agents that need the answer to
//     proceed.
//   - When RouteSync is nil, the skill degrades to fire-and-forget:
//     it writes a consult_emitted activity and returns the consult_id
//     without waiting. The addressee is expected to observe the
//     emitted activity via ambient context and respond on its own
//     cadence. This preserves audit flow for agents that either lack
//     a route transport or explicitly want the async semantics.
//
// See docs/FABRIC.md Part 3: cross-pipeline collaboration.
type CrossPipelineSkillConfig struct {
	SessionID  func() string
	AgentID    func() string
	AgentType  func() string
	PipelineID func() string

	// RouteSync dispatches a RouteRequest to the target peer agent and
	// waits for the terminal response. Nil ⇒ consult_peer runs in
	// fire-and-forget mode (see type doc).
	RouteSync func(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error)

	// Inbox returns the calling agent's ClaimsInbox so the consult /
	// challenge dispatchers can register a just-in-time response
	// Expectation against the issuing agent immediately after a
	// successful PostAction (CLAIMS.md §5). Nil ⇒ no expectation is
	// registered; the response would have to flow through standing
	// subscriptions instead — discouraged but tolerated for legacy
	// callers.
	Inbox func() *claims.ClaimsInbox

	// Scope is the calling agent's tracked goroutine scope. In
	// claims-native ticket mode the peer runs from the posted claim;
	// the issuer's claims inbox converts the peer testament into a
	// continuation resolution. Nil degrades to legacy synchronous mode
	// if RouteSync is set, or fire-and-forget if not.
	Scope GoroutineScopeProxy
}

// RouteSyncFromBus builds a RouteSync using the caller-provided bus
// and response topic. Pass the agent's live bus and Responses topic
// (typically accessed via closures so nil-at-registration-time is
// tolerated). Returns a RouteSync that errors early when the bus or
// topic is unavailable, so the consult surfaces a real failure
// instead of silently blocking.
func RouteSyncFromBus(busFn func() guide.EventBus, topicFn func() string) func(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	return func(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
		if busFn == nil || topicFn == nil {
			return nil, fmt.Errorf("consult_peer: route transport is not configured")
		}
		bus := busFn()
		topic := strings.TrimSpace(topicFn())
		if bus == nil {
			return nil, fmt.Errorf("consult_peer: bus is not ready")
		}
		if topic == "" {
			return nil, fmt.Errorf("consult_peer: response topic is not configured")
		}
		return RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
			Bus:           bus,
			ResponseTopic: topic,
			Request:       req,
		})
	}
}

// CrossPipelineSkills returns challenge_peer and consult_peer,
// gated by the caller's authority.Profile. The caller's agent type
// (from cfg.AgentType()) drives which targets appear in the skill's
// target_agent_type enum AND which the runtime handler will accept.
// Both skills are OMITTED when the authority profile's corresponding
// list is empty — knowledge agents (reactive by design) and the
// orchestrator/guardian/guide (non-initiators) simply don't see
// these tools in their catalog.
//
// challenge_peer generalizes today's challenge_agent — targets a fabric
// activity (not an agent directly), routes through the activity's
// author + pipeline. Same-pipeline target collapses to today's
// deterministic protocol behavior; cross-pipeline target engages the
// new path.
//
// consult_peer generalizes today's knowledge-agent consults — targets
// any peer agent in the session that the authority profile permits.
//
// See docs/COMMS_MATRIX.md for the per-agent target matrix.
func CrossPipelineSkills(cfg CrossPipelineSkillConfig) []*skills.Skill {
	callerType := safeCallString(cfg.AgentType)
	consultTargets := authority.PermittedConsultTargets(callerType)
	challengeTargets := authority.PermittedChallengeTargets(callerType)
	crossPipelineAllowed := authority.ProfileFor(callerType).AllowsCrossPipelineConsult

	out := make([]*skills.Skill, 0, 2)
	if len(challengeTargets) > 0 {
		out = append(out, challengePeerSkill(cfg, challengeTargets))
	}
	if len(consultTargets) > 0 {
		out = append(out, consultPeerSkill(cfg, consultTargets, crossPipelineAllowed))
		// await_consults is no longer registered: consult_peer is now
		// a deterministic blocking-from-LLM-POV tool. It dispatches
		// the consult on a tracked goroutine, yields the agent via a
		// structured ToolOutcome, and the resume path injects the
		// peer's testament summary + artifact as the consult_peer
		// tool's result. The LLM never has to remember to await; one
		// tool call, one result. The await_consults skill type still
		// exists for backwards compatibility with any agent that
		// hasn't migrated, but it is no longer registered as part of
		// the canonical consult skill set.
	}
	return out
}

// ClaimsCrossPipelineSkills returns an empty slice — on claims-based
// pipelines, challenges and consultations are issued via post_action
// with ActionTypeChallenge or ActionTypeConsultation, not via dedicated
// challenge_peer/consult_peer skills. The claims board replaces the
// direct peer-dispatch mechanism with a uniform claim→testament flow.
//
// Agents on claims-based pipelines call this instead of CrossPipelineSkills
// during skill registration. The existing CrossPipelineSkills function
// is preserved for non-claims agents during the transition.
func ClaimsCrossPipelineSkills(_ CrossPipelineSkillConfig) []*skills.Skill {
	return nil
}

func challengePeerSkill(cfg CrossPipelineSkillConfig, permittedTargets []string) *skills.Skill {
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
		// target_agent_type is an authority-gated enum: each agent
		// only sees targets its authority.Profile permits it to
		// challenge. Self-targeting is already excluded by
		// authority.PermittedChallengeTargets. See
		// docs/COMMS_MATRIX.md.
		EnumParam("target_agent_type", "Agent type of the peer whose commitment you are challenging. Must be one of the permitted targets for your role.", permittedTargets, false).
		StringParam("alternative", "The alternative commitment you propose. Optional — challenges that surface evidence without proposing an alternative are still valuable.", false).
		StringParam("resolution_hint", "Optional hint to guide the responder: \"yield\", \"scope-split\", or \"escalate\". Empty = let them choose freely.", false).
		IntParam("deadline_seconds", "How long the challenge stays open before fabric emits a challenge_unresolved activity. Default 600 (10 min).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TargetActivityID string `json:"target_activity_id"`
				Evidence         string `json:"evidence"`
				TargetAgentType  string `json:"target_agent_type"`
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
			// Runtime authority guard. The enum at the schema layer
			// already constrains target_agent_type; this is defense-
			// in-depth for cached schemas, manual JSON, or future
			// bus-level injection. When target_agent_type is
			// explicitly passed, it must be in the caller's
			// authority.PermittedChallengeTargets list. When it's
			// omitted we resolve it below from the target activity's
			// author and re-check.
			callerID := safeCallString(cfg.AgentID)
			callerType := safeCallString(cfg.AgentType)
			ownPipelineID := safeCallString(cfg.PipelineID)
			if explicit := strings.TrimSpace(params.TargetAgentType); explicit != "" {
				if peerTargetIsCaller(callerID, callerType, ownPipelineID, explicit, explicit, "") {
					return nil, selfPeerTargetError("challenge_peer", callerID, callerType, explicit, explicit)
				}
				if !authority.CanChallenge(callerType, explicit) {
					return nil, unauthorizedChallengeError(callerType, explicit, permittedTargets)
				}
			}
			deadline := time.Duration(params.DeadlineSeconds) * time.Second
			if deadline <= 0 {
				deadline = 10 * time.Minute
			}

			// Resolve the challenged activity's author so the UI's
			// inter-agent-branch derivation has a target agent to hang
			// the child row on. Without this, interAgentChallengeTargets
			// can't extract a recipient from challenge_peer's args
			// (target_activity_id is an opaque UUID, not an agent
			// identifier) and the completion event gets silently
			// dropped. Graceful when the activity isn't retrievable —
			// the surrounding fabric append still runs, and the caller
			// gets the same challenge_id it would have gotten before.
			targetActivityID := activity.ActivityID(strings.TrimSpace(params.TargetActivityID))
			resolvedAgentID := ""
			resolvedAgentType := ""
			resolvedPipelineID := ""
			if target := lookupTargetActivity(ctx, targetActivityID); target != nil {
				resolvedAgentID = strings.TrimSpace(target.Actor.AgentID)
				resolvedAgentType = strings.TrimSpace(target.Actor.AgentType)
				resolvedPipelineID = strings.TrimSpace(target.Actor.PipelineID)
			}
			if peerTargetIsCaller(callerID, callerType, ownPipelineID, resolvedAgentID, resolvedAgentType, resolvedPipelineID) {
				return nil, selfPeerTargetError("challenge_peer", callerID, callerType, resolvedAgentID, resolvedAgentType)
			}
			// Second authority gate: even if the caller didn't
			// explicitly name target_agent_type, the resolved author
			// of the challenged activity must still be a permitted
			// target. Prevents silent hops into a disallowed scope
			// via an opaque target_activity_id.
			if resolvedAgentType != "" && !authority.CanChallenge(callerType, resolvedAgentType) {
				return nil, unauthorizedChallengeError(callerType, resolvedAgentType, permittedTargets)
			}

			payload, _ := json.Marshal(map[string]any{
				"target_activity_id": params.TargetActivityID,
				"evidence":           params.Evidence,
				"alternative":        params.Alternative,
				"resolution_hint":    params.ResolutionHint,
				"deadline_at":        time.Now().Add(deadline),
			})
			caused := targetActivityID
			challengeID := string(activity.NewActivityID())
			act := activity.AgentActivity{
				ID:         activity.ActivityID(challengeID),
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

			// Issuing-side claim: the challenging agent posts a challenge
			// claim against the target agent.
			challengeTarget := resolvedAgentType
			if challengeTarget == "" {
				challengeTarget = strings.TrimSpace(params.TargetAgentType)
			}
			challengeClaimID := ""
			if challengeTarget != "" {
				if board := claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID)); board != nil {
					agentType := safeCallString(cfg.AgentType)
					challengeRelations := AttachCausedByFromContext(ctx, []claims.Relation{
						{Related: agentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
						{Related: challengeTarget, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
					})
					challengeClaims := []claims.Claim{{
						ID:          challengeID,
						Title:       "Challenge " + challengeTarget + ": " + truncateSharedClaim(params.Evidence, 60),
						Description: "Cross-pipeline peer challenge",
						Scope: []claims.ClaimScopeEntry{
							{Kind: "challenge", Key: challengeTarget},
							{Kind: "challenge_id", Key: challengeID},
						},
						ActionType: claims.ActionTypeChallenge,
						Relations:  challengeRelations,
						Validations: []*claims.Validation{{
							Type: claims.ValidationTypeInspection, Required: true,
							Description: "Challenged peer responds (defend/yield/scope-split/escalate)", QualityBar: "resolution.received",
							Status: claims.ValidationStatusPending,
						}},
					}}
					challengeAction := claims.Action{AgentID: agentType, Type: claims.ActionTypeChallenge}
					if err := board.PostAction(ctx, challengeAction, challengeClaims); err != nil {
						slog.Error("challenge_peer_issuing_claim_failed", "error", err.Error())
					} else {
						challengeClaimID = challengeClaims[0].ID
						EmitPeerInteractionStarted(ctx, PeerInteractionKindChallenge, agentType, challengeClaimID, challengeTarget, params.Evidence)
						if cfg.Inbox != nil {
							claims.RegisterPostActionExpectations(cfg.Inbox(), challengeAction, challengeClaims)
						}
					}
				}
			}

			// Narrate the ChallengingPeer state on the issuer's claim
			// so the agent panel + chat row reflect the wait per
			// docs/CLAIMS_UI.md §5.2 (1-3).
			board := claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID))
			issuerClaimID := ""
			if a := claims.AccumulatorFromContext(ctx); a != nil {
				issuerClaimID = a.ClaimID()
			}
			peerRef := &PeerRef{
				AgentType: challengeTarget,
				ClaimID:   challengeClaimID,
			}
			RecordAgentState(ctx, board, issuerClaimID,
				"Challenging "+challengeTarget,
				AgentStateChallengingPeer, peerRef)

			// Yield path: per spec §5.2 step 4, treat the challenge_id
			// as a consult_id and use the existing
			// AwaitConsultsOrYield framework. The challenged peer's
			// response (defend/yield/scope-split/escalate) publishes a
			// ConsultResolvedDelta with the challenge_id; resume
			// injects the verdict as the challenge_peer tool's
			// result. Falls through to the legacy fire-and-forget
			// ticket return when no continuation store / turn context
			// is wired (user-facing turns) — matches consult_peer's
			// dual-path shape.
			store := ContinuationStoreFromContext(ctx)
			turn := TurnFromContext(ctx)
			if store != nil && turn != nil && turn.Request != nil {
				toolCallID, toolName := activeToolCallFromContext(ctx)
				if toolCallID == "" {
					toolCallID = "challenge_" + string(act.ID)
				}
				if toolName == "" {
					toolName = "challenge_peer"
				}
				snapshot := &TurnSnapshot{
					CorrelationID:    turn.CorrelationID,
					Request:          cloneProvidersRequest(turn.Request),
					AccumulatorState: snapshotAccumulator(ctx),
				}
				_, yielded, awaitErr := store.AwaitConsultsOrYield(ctx, AwaitOptions{
					ConsultIDs:      []string{string(act.ID)},
					AwaitToolCallID: toolCallID,
					AwaitToolName:   toolName,
					Deadline:        time.Now().Add(deadline),
					Snapshot:        snapshot,
				})
				if awaitErr != nil && !yielded {
					return nil, awaitErr
				}
				if yielded {
					if a := claims.AccumulatorFromContext(ctx); a != nil {
						a.SuppressFlush()
					}
					return skills.YieldToolOutcome(&skills.YieldContinuation{
						Kind:        "challenge",
						AwaitedIDs:  []string{string(act.ID)},
						ToolCallID:  toolCallID,
						ToolName:    toolName,
						Deadline:    time.Now().Add(deadline),
						Description: "challenge response pending",
					}), nil
				}
				// Not yielded: fall through to the fire-and-forget
				// ticket so the LLM still progresses.
			}

			result := map[string]any{
				"challenge_id": act.ID,
				"deadline_at":  time.Now().Add(deadline),
				"status":       "in_flight",
			}
			if resolvedAgentType != "" {
				// Read by interAgentChallengeTargets so the UI derivation
				// can attach the completion event to the right agent row.
				result["target_agent_type"] = resolvedAgentType
			}
			if resolvedPipelineID != "" {
				result["target_pipeline_id"] = resolvedPipelineID
			}
			return result, nil
		}).
		Build()
}

// lookupTargetActivity resolves the challenged activity from the
// ambient activity source. Returns nil when the source is unavailable,
// when the ID doesn't parse, when the activity isn't present, or when
// the lookup errors — every failure path is silent because the
// downstream challenge append still needs to succeed (the challenge's
// usefulness doesn't depend on UI attribution). Callers should treat a
// nil return as "no resolved target" and fall through.
func lookupTargetActivity(ctx context.Context, id activity.ActivityID) *activity.AgentActivity {
	if strings.TrimSpace(string(id)) == "" {
		return nil
	}
	source := activity.DefaultSource()
	if source == nil {
		return nil
	}
	target, err := source.GetActivity(ctx, id)
	if err != nil || target == nil {
		return nil
	}
	return target
}

func consultPeerSkill(cfg CrossPipelineSkillConfig, permittedTargets []string, allowsCrossPipeline bool) *skills.Skill {
	return skills.NewSkill("consult_peer").
		Description("Ask a peer agent (cross-pipeline specialist or knowledge agent) for their evidence on a shared concern. When a route transport is configured for the calling agent, this blocks on the peer's terminal response and renders their work as nested children of this tool call — the normal mode for interactive consultation. Without a transport, it degrades to fire-and-forget: an activity is emitted, the addressee responds via their own ambient_context envelope, and the caller gets a consult_id to causal_trace later. PREFER THIS over guessing when peer state matters.").
		Domain("fabric").
		Keywords("consult", "fabric", "cross-pipeline", "peer", "knowledge-agent", "ask", "question").
		Priority(91).
		Usage("Use when ambient context shows a peer working in adjacent or overlapping scope and you'd benefit from their live state — e.g., 'how are you handling fixtures for shared models?' Pass target_agent_type and (optional) target_pipeline_id; without pipeline_id the consult routes to the natural same-pipeline peer or knowledge agent.").
		Requirement("Frame the question concretely. Vague consults waste both parties' attention budget.").
		Satisfies("Records a consult_emitted activity addressed to the target; returns the peer's response inline when the route transport is available.").
		// target_agent_type is an authority-gated enum: each agent
		// only sees targets its authority.Profile permits it to
		// consult. Self-targeting is already excluded by
		// authority.PermittedConsultTargets. See
		// docs/COMMS_MATRIX.md.
		EnumParam("target_agent_type", "Agent type to address. Must be one of the permitted targets for your role.", permittedTargets, true).
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
			// Runtime authority guard. The enum at the schema layer
			// already constrains target_agent_type; this is defense-
			// in-depth for cached schemas, manual JSON, or future
			// bus-level injection.
			callerID := safeCallString(cfg.AgentID)
			callerType := safeCallString(cfg.AgentType)
			ownPipelineID := safeCallString(cfg.PipelineID)
			targetType := strings.TrimSpace(params.TargetAgentType)
			targetPipelineID := strings.TrimSpace(params.TargetPipelineID)
			if peerTargetIsCaller(callerID, callerType, ownPipelineID, targetType, targetType, targetPipelineID) {
				return nil, selfPeerTargetError("consult_peer", callerID, callerType, targetType, targetType)
			}
			if !authority.CanConsult(callerType, targetType) {
				return nil, unauthorizedConsultError(callerType, targetType, permittedTargets)
			}
			// Cross-pipeline gate: if the caller names a specific
			// target_pipeline_id that differs from its own pipeline,
			// its role must allow cross-pipeline consults. Without
			// this, a global agent could hop into per-task pipelines
			// through the pipeline_id parameter.
			if targetPipelineID != "" && !allowsCrossPipeline {
				ownPipelineID := strings.TrimSpace(safeCallString(cfg.PipelineID))
				if targetPipelineID != ownPipelineID {
					return nil, fmt.Errorf(
						"consult_peer: %q is not permitted to cross-pipeline consult (own pipeline=%q, requested=%q); leave target_pipeline_id empty to route to the natural same-scope peer",
						callerType, ownPipelineID, targetPipelineID,
					)
				}
			}
			deadline := time.Duration(params.DeadlineSeconds) * time.Second
			if deadline <= 0 {
				deadline = 3 * time.Minute
			}
			consultID := string(activity.NewActivityID())
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
				ID:         activity.ActivityID(consultID),
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

			// Issuing-side claim: the consulting agent posts a consultation
			// claim against the target agent. Capture the claim ID so it
			// can travel on the dispatched envelope as parent_claim_id —
			// the consultee binds its testament to this claim, and the
			// bridge nests its artifact tree under the issuer's
			// consult_started row via claimToInvocationArtifact lookup.
			consultationClaimID := ""
			if board := claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID)); board != nil {
				agentType := safeCallString(cfg.AgentType)
				consultRelations := AttachCausedByFromContext(ctx, []claims.Relation{
					{Related: agentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
					{Related: strings.TrimSpace(params.TargetAgentType), RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				})
				consultClaims := []claims.Claim{{
					ID:          consultID,
					Title:       "Consult peer " + targetAddress + ": " + truncateSharedClaim(params.Query, 60),
					Description: "Cross-pipeline peer consultation",
					Scope: []claims.ClaimScopeEntry{
						{Kind: "consultation", Key: targetAddress},
						{Kind: "consult_id", Key: consultID},
					},
					ActionType: claims.ActionTypeConsultation,
					Relations:  consultRelations,
					Validations: []*claims.Validation{{
						Type: claims.ValidationTypeReceipt, Required: true,
						Description: "Peer responds to consultation", QualityBar: "response.received",
						Status: claims.ValidationStatusPending,
					}},
				}}
				consultAction := claims.Action{AgentID: agentType, Type: claims.ActionTypeConsultation}
				if err := board.PostAction(ctx, consultAction, consultClaims); err != nil {
					slog.Error("consult_peer_issuing_claim_failed", "error", err.Error())
				} else {
					consultationClaimID = consultClaims[0].ID
					EmitPeerInteractionStarted(ctx, PeerInteractionKindConsult, agentType, consultationClaimID, targetAddress, params.Query)
					if cfg.Inbox != nil {
						claims.RegisterPostActionExpectations(cfg.Inbox(), consultAction, consultClaims)
					}
				}
			}

			// Fire-and-forget transport: caller has no route-sync, so
			// there is no peer response to wait for. Return the
			// activity envelope as the tool result so the LLM sees it
			// dispatched.
			ticket := map[string]any{
				"consult_id":  consultID,
				"deadline_at": time.Now().Add(deadline),
				"status":      "in_flight",
				"target":      targetAddress,
			}
			if cfg.RouteSync == nil {
				return ticket, nil
			}

			// Two paths:
			//   - WithContinuationStore + WithTurnContext stamped on
			//     ctx → ticket-mode yield/resume (claim-inbox-driven
			//     flows where no synchronous caller is waiting).
			//   - Otherwise → legacy synchronous wait (user-facing
			//     turns where the caller is blocked on the response;
			//     this is what sylk-clone has always done).
			//
			// The synchronous path posts the same consultation claim,
			// dispatches via cfg.RouteSync, and blocks inline until the
			// peer's testament arrives. The LLM continues with the
			// real response in the same tool-loop turn — no half-empty
			// answer reaches the user.
			store := ContinuationStoreFromContext(ctx)
			turn := TurnFromContext(ctx)
			if store == nil || turn == nil || turn.Request == nil {
				return runLegacyConsultWait(ctx, cfg, params, consultID, consultationClaimID)
			}
			if consultationClaimID == "" {
				return nil, fmt.Errorf("consult_peer: claims-native continuation requires a posted consultation claim")
			}

			board := claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID))

			// Push: narrate the dispatch transition on the issuer's
			// own claim (the claim the issuer is currently processing).
			// The UI surfaces this as the issuer's row's status text +
			// records an agent_state artifact for the durable trace.
			// docs/CLAIMS_UI.md "Agent narration discipline".
			issuerClaimID := ""
			if a := claims.AccumulatorFromContext(ctx); a != nil {
				issuerClaimID = a.ClaimID()
			}
			peerRef := &PeerRef{
				AgentType: strings.TrimSpace(params.TargetAgentType),
				ClaimID:   consultationClaimID,
			}
			RecordAgentState(ctx, board, issuerClaimID,
				"Dispatching to "+targetAddress,
				AgentStateDispatchingToPeer, peerRef)

			// Push: dispatch is in flight, agent's about to yield.
			RecordAgentState(ctx, board, issuerClaimID,
				"Awaiting "+targetAddress+" response",
				AgentStateAwaitingPeerResponse, peerRef)

			// Yield: persist a continuation snapshot whose awaited
			// consult_id is this single consult. The resume path will
			// inject the peer's response as this tool call's result —
			// transparent to the LLM.
			toolCallID, toolName := activeToolCallFromContext(ctx)
			if toolCallID == "" {
				toolCallID = "consult_" + string(act.ID)
			}
			if toolName == "" {
				toolName = "consult_peer"
			}
			snapshot := &TurnSnapshot{
				CorrelationID:    turn.CorrelationID,
				Request:          cloneProvidersRequest(turn.Request),
				AccumulatorState: snapshotAccumulator(ctx),
			}
			_, yielded, awaitErr := store.AwaitConsultsOrYield(ctx, AwaitOptions{
				ConsultIDs:      []string{consultID},
				AwaitToolCallID: toolCallID,
				AwaitToolName:   toolName,
				Deadline:        time.Now().Add(deadline),
				Snapshot:        snapshot,
			})
			if awaitErr != nil && !yielded {
				return nil, awaitErr
			}
			if yielded {
				// Suppress the original accumulator's deferred Flush so
				// the agent's processClaimsEntry doesn't submit a
				// premature partial testament on the way out. The
				// snapshot captured above carries the artifacts forward;
				// the resume path's RestoreAccumulator + its own Flush
				// produces the single authoritative testament. Without
				// this, the issuer's claim testifies mid-cycle, the
				// bridge's cycle resolver closes the cycle, and the
				// peer's nested artifacts can no longer attach to the
				// issuer's consult_started row in the chat tree.
				if acc := claims.AccumulatorFromContext(ctx); acc != nil {
					acc.SuppressFlush()
				}
				return skills.YieldToolOutcome(&skills.YieldContinuation{
					Kind:        "consult",
					AwaitedIDs:  []string{consultID},
					ToolCallID:  toolCallID,
					ToolName:    toolName,
					Deadline:    time.Now().Add(deadline),
					Description: "consult response pending",
				}), nil
			}
			// Pre-resolved: orphan delta was already in the store
			// (peer answered before consult_peer's persist landed).
			// Treat as fast-path return — resume not needed.
			return ticket, nil
		}).
		Build()
}

// runLegacyConsultWait is the synchronous behavior of consult_peer.
// The LLM blocks on the peer's response and the tool result IS the
// response payload. Used when no ContinuationStore + TurnContext is
// stamped on ctx (i.e. user-facing turns where a caller is awaiting
// the response synchronously). Same shape as sylk-clone.
func runLegacyConsultWait(
	ctx context.Context,
	cfg CrossPipelineSkillConfig,
	params struct {
		TargetAgentType  string `json:"target_agent_type"`
		TargetPipelineID string `json:"target_pipeline_id"`
		Scope            string `json:"scope"`
		Query            string `json:"query"`
		DeadlineSeconds  int    `json:"deadline_seconds"`
	},
	consultID string,
	consultationClaimID string,
) (any, error) {
	spec := InterAgentBranchSpec{
		Kind:       InterAgentToolEventKindConsult,
		ToolName:   "consult_peer",
		AgentTypes: []string{strings.TrimSpace(params.TargetAgentType)},
		Summary:    params.Query,
		Args: map[string]any{
			"target_agent_type":  params.TargetAgentType,
			"target_pipeline_id": params.TargetPipelineID,
			"scope":              params.Scope,
			"query":              params.Query,
		},
	}
	response, err := WithInterAgentBranchMessage(ctx, spec, func(branchCtx context.Context, branch InterAgentBranchHandle) (*guide.Message, error) {
		req := &guide.RouteRequest{
			Input:           params.Query,
			TargetAgentID:   strings.TrimSpace(params.TargetAgentType),
			SessionID:       safeCallString(cfg.SessionID),
			SourceAgentID:   safeCallString(cfg.AgentID),
			SourceAgentName: safeCallString(cfg.AgentType),
			ExplicitTarget:  true,
		}
		req.Metadata = branch.ApplyMetadata(branchCtx, req.Metadata)
		req.Metadata = consultRouteMetadata(ctx, req.Metadata, consultID)
		req.Metadata = CycleOptsToAnyMetadata(req.Metadata, ForwardedRequestCycleOpts{
			ParentClaimID: strings.TrimSpace(consultationClaimID),
		})
		if pipe := strings.TrimSpace(params.TargetPipelineID); pipe != "" {
			if req.Metadata == nil {
				req.Metadata = map[string]any{}
			}
			req.Metadata["target_pipeline_id"] = pipe
		}
		return cfg.RouteSync(branchCtx, req)
	})
	if err != nil {
		return nil, fmt.Errorf("consult_peer: %w", err)
	}
	result := map[string]any{
		"consult_id": consultID,
		"status":     "completed",
	}
	if response != nil {
		if resp, ok := response.GetRouteResponse(); ok && resp != nil {
			if !resp.Success {
				errText := strings.TrimSpace(resp.Error)
				if errText == "" {
					errText = "peer consultation failed"
				}
				return nil, fmt.Errorf("consult_peer: %s", errText)
			}
			result["response"] = resp.Data
		} else if errText, ok := response.GetError(); ok {
			trimmed := strings.TrimSpace(errText)
			if trimmed == "" {
				trimmed = "peer consultation failed"
			}
			return nil, fmt.Errorf("consult_peer: %s", trimmed)
		}
	}
	return result, nil
}

func consultRouteMetadata(ctx context.Context, metadata map[string]any, consultID string) map[string]any {
	if hasNestedInterAgentBranchMetadata(metadata) {
		return metadata
	}
	parentCorrelationID := ""
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		parentCorrelationID = strings.TrimSpace(stream.CorrelationID)
	}
	if parentCorrelationID == "" {
		if turn := TurnFromContext(ctx); turn != nil {
			parentCorrelationID = strings.TrimSpace(turn.CorrelationID)
		}
	}
	if parentCorrelationID == "" {
		return metadata
	}
	toolCallKey := ""
	if active, ok := ActiveToolCallFromContext(ctx); ok {
		toolCallKey = strings.TrimSpace(active.ToolCallKey)
	}
	if toolCallKey == "" {
		toolCallKey = "consult_" + strings.TrimSpace(consultID)
	}
	return RouteMetadataWithExplicitInterAgentBranch(ctx, metadata, InterAgentBranchMetadata{
		ParentCorrelationID: parentCorrelationID,
		ParentToolCallKey:   toolCallKey,
		ThreadKey:           strings.TrimSpace(consultID),
		Kind:                InterAgentToolEventKindConsult,
	})
}

// asString converts an arbitrary value into a string suitable for a
// truncated summary. Strings pass through; other types are
// JSON-encoded.
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
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
		"resolution":       string(in.Resolution),
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

// unauthorizedConsultError formats a role-aware rejection message for
// consult_peer. The error explains why the target is denied and lists
// the actual permitted targets so the LLM can pick a valid one on
// retry. For reactive roles (empty permitted list) the message calls
// out the role's pattern explicitly.
func unauthorizedConsultError(callerType, targetType string, permitted []string) error {
	if len(permitted) == 0 {
		return fmt.Errorf(
			"consult_peer: role %q is reactive — it does not initiate peer consults. Respond to incoming consults via the role's natural channels (knowledge queries, advisory emissions, etc.) instead",
			callerType,
		)
	}
	return fmt.Errorf(
		"consult_peer: %q is not permitted to consult %q. Permitted targets for %q: %s",
		callerType, targetType, callerType, strings.Join(permitted, ", "),
	)
}

// unauthorizedChallengeError is the challenge_peer analogue.
// Challenges are higher-stakes than consults, so the error spells
// this out explicitly to discourage escalation attempts.
func unauthorizedChallengeError(callerType, targetType string, permitted []string) error {
	if len(permitted) == 0 {
		return fmt.Errorf(
			"challenge_peer: role %q may not initiate challenges. Challenges cast doubt on peer commitments and belong to inspectors / quality roles; if you have disagreement to surface, respond on an existing consult or emit an advisory instead",
			callerType,
		)
	}
	return fmt.Errorf(
		"challenge_peer: %q is not permitted to challenge %q. Permitted targets for %q: %s",
		callerType, targetType, callerType, strings.Join(permitted, ", "),
	)
}

// truncateSharedClaim truncates a string for shared claim titles.
func truncateSharedClaim(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
