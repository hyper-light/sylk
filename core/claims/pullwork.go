package claims

import (
	"time"
)

// TurnEnvelope is the single object the agent turn loop reads each
// turn. Assembled by PullWork from (1) inbox-drained deltas, (2)
// board-resolved adjacent context, and (3) an optional ambient lens
// digest. The LLM's prompt is derived from this envelope alone.
type TurnEnvelope struct {
	AgentID    string    `json:"agent_id"`
	SessionID  string    `json:"session_id"`
	PipelineID string    `json:"pipeline_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`

	Phase     BoardPhase `json:"phase"`
	Iteration int        `json:"iteration"`

	// DirectedClaims is the set of live claims (status pending,
	// in_progress, rejected, or superseded-awaiting-remediation)
	// directed at this agent via a subject Relation.
	DirectedClaims []Claim `json:"directed_claims,omitempty"`

	// EvaluationQueue is the set of claims this agent issued that
	// are now testified and awaiting evaluate_validation.
	EvaluationQueue []Claim `json:"evaluation_queue,omitempty"`

	// InboundAsks is the set of actions (consult / challenge)
	// directed at this agent — distinct from DirectedClaims so the
	// LLM sees task claims separately from ad-hoc asks.
	InboundAsks []Action `json:"inbound_asks,omitempty"`

	// OutboundPending is the set of actions this agent issued that
	// are still awaiting a testament from the subject.
	OutboundPending []Action `json:"outbound_pending,omitempty"`

	// Deltas is the ordered, deduplicated set of bus deltas drained
	// this turn. Rendered into the prompt as a "what changed since
	// your last turn" digest.
	Deltas []Delta `json:"-"`

	// NotificationErrors surfaces recent amplifier / subscriber
	// failures so the LLM can capture them as testament error
	// artifacts.
	NotificationErrors []string `json:"notification_errors,omitempty"`

	// AssembledAt is the UTC time the envelope was built.
	AssembledAt time.Time `json:"assembled_at"`
}

// PullWorkConfig bundles inputs to PullWork. Board + Inbox are
// required; the rest are optional.
type PullWorkConfig struct {
	AgentID    string
	SessionID  string
	PipelineID string
	TaskID     string

	Board *ClaimsBoard
	Inbox *ClaimsInbox
}

// PullWork assembles the turn envelope from the board's durable state.
// Pure: no goroutines, no I/O beyond the board's RLock reads. Returns
// nil when the board is absent.
//
// Note: the inbox is no longer drained here. Agents use inbox.Next()
// for event-driven intake with expectation matching. PullWork provides
// a board-state snapshot for agents that need a full projection of
// directed claims, evaluation queues, and action queues — orthogonal
// to the delta-driven intake path.
func PullWork(cfg PullWorkConfig) *TurnEnvelope {
	if cfg.Board == nil {
		return nil
	}
	env := &TurnEnvelope{
		AgentID:     cfg.AgentID,
		SessionID:   cfg.SessionID,
		PipelineID:  cfg.PipelineID,
		TaskID:      cfg.TaskID,
		AssembledAt: time.Now().UTC(),
	}

	// Phase + iteration from the board.
	env.Phase = cfg.Board.Phase()
	env.Iteration = cfg.Board.Iteration()

	// Adjacent context from the board.
	populateDirectedClaims(env, cfg.Board, cfg.AgentID)
	populateEvaluationQueue(env, cfg.Board, cfg.AgentID)
	populateActionQueues(env, cfg.Board, cfg.AgentID)

	// Amplifier error drain — surface via the projection so agents
	// can see and record them as artifacts.
	proj := cfg.Board.Projection()
	if len(proj.NotificationErrors) > 0 {
		env.NotificationErrors = append(env.NotificationErrors, proj.NotificationErrors...)
	}

	return env
}

// ────────────────────────────────────────────────────────────────────
// Population helpers
// ────────────────────────────────────────────────────────────────────

func populateDirectedClaims(env *TurnEnvelope, board *ClaimsBoard, agentID string) {
	if agentID == "" {
		return
	}
	ids := board.ObjectIDsWithRelation(RelatedTypeAgent, RelationshipSubject, agentID)
	for _, id := range ids {
		c, ok := board.CloneClaim(id)
		if !ok {
			continue
		}
		if !isDirectedClaimActive(c) {
			continue
		}
		env.DirectedClaims = append(env.DirectedClaims, *c)
	}
}

// isDirectedClaimActive reports whether a directed claim should
// appear in DirectedClaims. Pending, in_progress, and rejected are
// "active" from the subject's perspective (pending = new work;
// in_progress = resume; rejected = remediation-required).
// Superseded claims are no longer the subject's concern.
func isDirectedClaimActive(c *Claim) bool {
	switch c.Status {
	case ClaimStatusPending, ClaimStatusInProgress, ClaimStatusRejected:
		return true
	}
	return false
}

func populateEvaluationQueue(env *TurnEnvelope, board *ClaimsBoard, agentID string) {
	if agentID == "" {
		return
	}
	ids := board.ObjectIDsWithRelation(RelatedTypeAgent, RelationshipIssuer, agentID)
	for _, id := range ids {
		c, ok := board.CloneClaim(id)
		if !ok {
			continue
		}
		if c.Status != ClaimStatusTestified {
			continue
		}
		env.EvaluationQueue = append(env.EvaluationQueue, *c)
	}
	// Also include claims where this agent is an explicit evaluator.
	evalIDs := board.ObjectIDsWithRelation(RelatedTypeAgent, RelationshipEvaluator, agentID)
	for _, id := range evalIDs {
		if containsClaimID(env.EvaluationQueue, id) {
			continue
		}
		c, ok := board.CloneClaim(id)
		if !ok || c.Status != ClaimStatusTestified {
			continue
		}
		env.EvaluationQueue = append(env.EvaluationQueue, *c)
	}
}

func containsClaimID(claims []Claim, id string) bool {
	for i := range claims {
		if claims[i].ID == id {
			return true
		}
	}
	return false
}

func populateActionQueues(env *TurnEnvelope, board *ClaimsBoard, agentID string) {
	if agentID == "" {
		return
	}
	proj := board.Projection()
	claimSubjects := indexClaimSubjectsByAction(proj.Claims)
	for i := range proj.Actions {
		a := proj.Actions[i]
		if !isAskAction(a.Type) {
			continue
		}
		if a.Status == ActionStatusComplete {
			continue
		}
		if subjects := claimSubjects[a.ID]; contains(subjects, agentID) {
			env.InboundAsks = append(env.InboundAsks, a)
			continue
		}
		if a.AgentID == agentID {
			env.OutboundPending = append(env.OutboundPending, a)
		}
	}
}

// indexClaimSubjectsByAction returns, for each claim action ID, the
// set of agent IDs that the action's claims are directed at via
// subject Relations. Used to classify inbound vs. outbound asks
// without requiring subject Relations on the action itself.
func indexClaimSubjectsByAction(claims []Claim) map[string][]string {
	out := make(map[string][]string, len(claims))
	for i := range claims {
		c := claims[i]
		actionID := ClaimActionID(c.Relations)
		if actionID == "" {
			continue
		}
		subject := SubjectAgentID(c.Relations)
		if subject == "" {
			continue
		}
		out[actionID] = appendUnique(out[actionID], subject)
	}
	return out
}

func isAskAction(t ActionType) bool {
	return t == ActionTypeConsultation || t == ActionTypeChallenge
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
