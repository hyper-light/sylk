package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

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
// consult_peer and challenge_peer are claim-backed. They post directed
// claims to the session board and then either yield the current LLM
// turn on canonical testament/claim deltas or return an in-flight
// ticket when no continuation context is available. They do not
// synchronously RouteSync peer execution after posting the claim.
//
// See docs/FABRIC.md Part 3: cross-pipeline collaboration.
type CrossPipelineSkillConfig struct {
	SessionID  func() string
	AgentID    func() string
	AgentType  func() string
	PipelineID func() string

	// Inbox returns the calling agent's ClaimsInbox so the consult /
	// challenge dispatchers can register a just-in-time response
	// Expectation against the issuing agent immediately after a
	// successful PostAction (CLAIMS.md §5). Nil ⇒ no expectation is
	// registered; the response would have to flow through standing
	// subscriptions instead — discouraged but tolerated for legacy
	// callers.
	Inbox func() *claims.ClaimsInbox

	// Scope is the calling agent's tracked goroutine scope. The peer
	// runs from the posted claim; the issuer's claims inbox converts
	// the peer testament into a continuation resolution. Nil returns an
	// in-flight ticket instead of yielding the current turn.
	Scope GoroutineScopeProxy
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
				explicitSelf := peerTargetIsCaller(callerID, callerType, ownPipelineID, "", explicit, "")
				if explicitSelf && claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID)) == nil {
					return nil, selfPeerTargetError("challenge_peer", callerID, callerType, explicit, explicit)
				}
				if !explicitSelf && !authority.CanChallenge(callerType, explicit) {
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
			// Second authority gate: even if the caller didn't
			// explicitly name target_agent_type, the resolved author
			// of the challenged activity must still be a permitted
			// target. Prevents silent hops into a disallowed scope
			// via an opaque target_activity_id.
			selfTargeted := peerTargetIsCaller(callerID, callerType, ownPipelineID, resolvedAgentID, resolvedAgentType, resolvedPipelineID)
			if selfTargeted && claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID)) == nil {
				return nil, selfPeerTargetError("challenge_peer", callerID, callerType, resolvedAgentID, resolvedAgentType)
			}
			if resolvedAgentType != "" && !selfTargeted && !authority.CanChallenge(callerType, resolvedAgentType) {
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
							ID:   challengeID + ".inspection",
							Type: claims.ValidationTypeInspection, Required: true,
							Description: "Challenged peer responds (defend/yield/scope-split/escalate)", QualityBar: "resolution.received",
							Status: claims.ValidationStatusPending,
						}},
					}}
					challengeAction := claims.Action{AgentID: agentType, Type: claims.ActionTypeChallenge}
					if err := postGeneratedPeerClaim(ctx, board, challengeAction, challengeClaims, agentType, "challenge_peer"); err != nil {
						slog.Error("challenge_peer_issuing_claim_failed", "error", err.Error())
						return nil, err
					} else {
						challengeClaimID = challengeClaims[0].ID
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

			// Yield path: await the challenge claim's canonical lifecycle
			// deltas. The challenged peer answers by posting a testament
			// against challengeClaimID; the continuation resumes from
			// testament.posted or a terminal claim lifecycle delta.
			store := ContinuationStoreFromContext(ctx)
			turn := TurnFromContext(ctx)
			if store != nil {
				if challengeClaimID == "" {
					return nil, fmt.Errorf("challenge_peer: claims-native continuation requires a posted challenge claim")
				}
				if turn == nil || turn.Request == nil {
					results, waitErr := store.AwaitClaimResults(ctx, []string{challengeClaimID}, time.Now().Add(deadline))
					if waitErr != nil {
						return nil, waitErr
					}
					return FormatConsultResults(results), nil
				}
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
					ClaimRefs:       []string{challengeClaimID},
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
						AwaitedIDs:  []string{challengeClaimID},
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

func postGeneratedPeerClaim(ctx context.Context, board *claims.ClaimsBoard, action claims.Action, claimSet []claims.Claim, actorID, skillName string) error {
	if board == nil {
		return fmt.Errorf("%s: claims board is required", skillName)
	}
	if len(claimSet) == 0 {
		return fmt.Errorf("%s: no claims to post", skillName)
	}
	idempotencyKey := skillName + ":" + strings.TrimSpace(claimSet[0].ID)
	generated, err := board.GenerateClaimAction(ctx, action, claimSet, claims.GenerateClaimActionOptions{
		IdempotencyKey: idempotencyKey,
		Reason:         skillName + " generated peer claim",
	})
	if err != nil {
		return err
	}
	if len(generated.Claims) == 0 {
		return fmt.Errorf("%s: generated peer claim action returned no claims", skillName)
	}
	for i := range generated.Claims {
		if i < len(claimSet) {
			claimSet[i].ID = generated.Claims[i].ID
		}
	}
	return board.PostGeneratedClaim(ctx, generated.Claims[0].ID, actorID, claims.ClaimPostOptions{
		Reason: skillName + " posted peer claim",
	})
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
		Description("Ask a peer agent (cross-pipeline specialist or knowledge agent) for their evidence on a shared concern. This posts a directed consultation claim; the peer receives the claim through the Guide bus and answers by submitting a testament with artifacts. When the current turn can be parked, the tool yields and resumes from canonical deltas. Otherwise it returns an in-flight ticket. PREFER THIS over guessing when peer state matters.").
		Domain("fabric").
		Keywords("consult", "fabric", "cross-pipeline", "peer", "knowledge-agent", "ask", "question").
		Priority(91).
		Usage("Use when ambient context shows a peer working in adjacent or overlapping scope and you'd benefit from their live state — e.g., 'how are you handling fixtures for shared models?' Pass target_agent_type and (optional) target_pipeline_id; without pipeline_id the consult routes to the natural same-pipeline peer or knowledge agent.").
		Requirement("Frame the question concretely. Vague consults waste both parties' attention budget.").
		Satisfies("Posts a consultation claim addressed to the target and waits/yields on the peer's testament when continuation context is available.").
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
			targetType := strings.TrimSpace(params.TargetAgentType)
			targetPipelineID := strings.TrimSpace(params.TargetPipelineID)
			selfTargeted := peerTargetIsCaller(callerID, callerType, safeCallString(cfg.PipelineID), "", targetType, targetPipelineID)
			if selfTargeted && claims.DefaultSessionBoardRegistry().Lookup(safeCallString(cfg.SessionID)) == nil {
				return nil, selfPeerTargetError("consult_peer", callerID, callerType, targetType, targetType)
			}
			if !selfTargeted && !authority.CanConsult(callerType, targetType) {
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
			// claim against the target agent. The consultee binds its
			// testament and artifacts to this claim, and the bridge projects
			// the exchange from the claim graph.
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
						ID:   consultID + ".receipt",
						Type: claims.ValidationTypeReceipt, Required: true,
						Description: "Peer responds to consultation", QualityBar: "response.received",
						Status: claims.ValidationStatusPending,
					}},
				}}
				consultAction := claims.Action{AgentID: agentType, Type: claims.ActionTypeConsultation}
				if err := postGeneratedPeerClaim(ctx, board, consultAction, consultClaims, agentType, "consult_peer"); err != nil {
					slog.Error("consult_peer_issuing_claim_failed", "error", err.Error())
					return nil, err
				} else {
					consultationClaimID = consultClaims[0].ID
					if cfg.Inbox != nil {
						claims.RegisterPostActionExpectations(cfg.Inbox(), consultAction, consultClaims)
					}
				}
			}

			ticket := map[string]any{
				"consult_id":  consultID,
				"deadline_at": time.Now().Add(deadline),
				"status":      "in_flight",
				"target":      targetAddress,
			}

			store := ContinuationStoreFromContext(ctx)
			turn := TurnFromContext(ctx)
			if store == nil {
				return ticket, nil
			}
			if consultationClaimID == "" {
				return nil, fmt.Errorf("consult_peer: claims-native continuation requires a posted consultation claim")
			}
			if turn == nil || turn.Request == nil {
				results, waitErr := store.AwaitClaimResults(ctx, []string{consultationClaimID}, time.Now().Add(deadline))
				if waitErr != nil {
					return nil, waitErr
				}
				return FormatConsultResults(results), nil
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
				ClaimRefs:       []string{consultationClaimID},
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
				// claim-backed peer row in the chat tree.
				if acc := claims.AccumulatorFromContext(ctx); acc != nil {
					acc.SuppressFlush()
				}
				return skills.YieldToolOutcome(&skills.YieldContinuation{
					Kind:        "consult",
					AwaitedIDs:  []string{consultationClaimID},
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

func peerTargetIsCaller(callerID, callerType, callerPipelineID, targetAgentID, targetAgentType, targetPipelineID string) bool {
	callerID = strings.TrimSpace(callerID)
	callerType = strings.TrimSpace(callerType)
	callerPipelineID = strings.TrimSpace(callerPipelineID)
	targetAgentID = strings.TrimSpace(targetAgentID)
	targetAgentType = strings.TrimSpace(targetAgentType)
	targetPipelineID = strings.TrimSpace(targetPipelineID)
	if callerID != "" && targetAgentID != "" && strings.EqualFold(callerID, targetAgentID) {
		return true
	}
	if callerType == "" || targetAgentType == "" || !strings.EqualFold(callerType, targetAgentType) {
		return false
	}
	// A type-only target with no target pipeline resolves to the
	// caller's natural same-pipeline peer. For same-type calls that is
	// the caller itself; different target_pipeline_id is the explicit
	// cross-pipeline case and may be legitimate.
	return targetPipelineID == "" || strings.EqualFold(callerPipelineID, targetPipelineID)
}

func selfPeerTargetError(toolName, callerID, callerType, targetAgentID, targetAgentType string) error {
	caller := strings.TrimSpace(callerID)
	if caller == "" {
		caller = strings.TrimSpace(callerType)
	}
	target := strings.TrimSpace(targetAgentID)
	if target == "" {
		target = strings.TrimSpace(targetAgentType)
	}
	return fmt.Errorf("%s: target %q resolves to caller %q; use local claims/testaments instead of consulting or challenging yourself", toolName, target, caller)
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
