package claims

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/google/uuid"
)

// amplifierEmitTimeout bounds each tracked emission goroutine. Emissions
// are non-blocking on the hot path (activity sink enqueues; bus
// enqueues); this timeout guards against pathological stalls so
// scope.Shutdown can drain deterministically.
const amplifierEmitTimeout = 2 * time.Second

// BoardAmplifier dual-emits every claims board mutation as:
//
//   (1) a Fabric AgentActivity via activity.Append, and
//   (2) a structured Delta to a bus topic via DeltaPublisher.
//
// Both emissions are best-effort and non-blocking from the board
// mutation's perspective. Errors are logged via log/slog and recorded
// on the board's notificationErrors so agents see them in the next
// Projection() read as testament error artifacts (per the errors-as-
// artifacts principle). The board's state has already committed —
// emission failure never blocks or rolls back mutation.
//
// Thread safety: the amplifier itself is stateless after construction;
// all per-emission work happens on scope-tracked goroutines or inline.
// Fields are set only at construction via the With* options.
type BoardAmplifier struct {
	sessionID string
	taskID    string
	boardID   string

	deltaBus DeltaPublisher
	scope    ScopeProvider

	// errorSink receives emission failures. When nil, errors are
	// dropped silently (tests). Wired by the board so agents see
	// them on Projection().
	errorSink func(message string)
}

// NewBoardAmplifier constructs an amplifier with a safe-default
// (Noop) DeltaPublisher. Callers wire a real bus via WithDeltaBus.
func NewBoardAmplifier(sessionID, taskID, boardID string) *BoardAmplifier {
	return &BoardAmplifier{
		sessionID: sessionID,
		taskID:    taskID,
		boardID:   boardID,
		deltaBus:  NoopDeltaBus{},
	}
}

// WithDeltaBus wires the bus publisher. Nil is normalized to the
// NoopDeltaBus so every amplifier is safe to call.
func (a *BoardAmplifier) WithDeltaBus(bus DeltaPublisher) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.deltaBus = publishOrNoop(bus)
	return a
}

// WithScope wires a goroutine scope. When set, emissions are
// dispatched asynchronously via scope.Go under amplifierEmitTimeout.
// When nil, emissions are synchronous (tests, standalone tools).
func (a *BoardAmplifier) WithScope(scope ScopeProvider) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.scope = scope
	return a
}

// WithErrorSink wires a callback that receives emission-failure
// messages. Used by the board to forward into notificationErrors.
func (a *BoardAmplifier) WithErrorSink(sink func(message string)) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.errorSink = sink
	return a
}

// ────────────────────────────────────────────────────────────────────
// Fabric activity emissions (existing surface — preserved)
// ────────────────────────────────────────────────────────────────────

// EmitActionPosted emits a Fabric activity for a newly posted action.
func (a *BoardAmplifier) EmitActionPosted(ctx context.Context, action *Action) {
	if a == nil || action == nil {
		return
	}
	a.emit(ctx, activity.ActionActionPosted, action.AgentID, action.ID, map[string]any{
		"action_type": string(action.Type),
		"agent_id":    action.AgentID,
	})
}

// EmitClaimIssued emits a Fabric activity for a newly issued claim.
func (a *BoardAmplifier) EmitClaimIssued(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimIssued, claim.AgentID, claim.ID, map[string]any{
		"title":       claim.Title,
		"description": claim.Description,
		"action_type": string(claim.ActionType),
		"status":      string(claim.Status),
	})
}

// EmitClaimUpdated emits a Fabric activity for a claim progress update.
func (a *BoardAmplifier) EmitClaimUpdated(ctx context.Context, claim *Claim, agentID string) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimUpdated, agentID, claim.ID, map[string]any{
		"title":  claim.Title,
		"status": string(claim.Status),
	})
}

// EmitTestamentSubmitted emits a Fabric activity for a submitted testament.
func (a *BoardAmplifier) EmitTestamentSubmitted(ctx context.Context, testament *Testament) {
	if a == nil || testament == nil {
		return
	}
	a.emit(ctx, activity.ActionTestamentSubmitted, testament.AgentID, testament.ID, map[string]any{
		"summary":    testament.Summary,
		"confidence": testament.Confidence,
	})
}

// EmitArtifactPublished emits a Fabric activity for each artifact.
func (a *BoardAmplifier) EmitArtifactPublished(ctx context.Context, artifact *Artifact) {
	if a == nil || artifact == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimArtifactPublished, artifact.AgentID, artifact.ID, map[string]any{
		"kind":      artifact.Kind,
		"reference": artifact.Reference,
		"ephemeral": artifact.Ephemeral,
	})
}

// EmitClaimValidated emits a Fabric activity for a validation evaluation.
func (a *BoardAmplifier) EmitClaimValidated(ctx context.Context, validation *Validation, agentID string) {
	if a == nil || validation == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimValidated, agentID, validation.ID, map[string]any{
		"description": validation.Description,
		"status":      string(validation.Status),
		"type":        string(validation.Type),
	})
}

// EmitClaimAccepted emits a Fabric activity when a claim is accepted.
func (a *BoardAmplifier) EmitClaimAccepted(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimAccepted, claim.AgentID, claim.ID, map[string]any{
		"title": claim.Title,
	})
}

// EmitClaimRejected emits a Fabric activity when a claim is rejected.
func (a *BoardAmplifier) EmitClaimRejected(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimRejected, claim.AgentID, claim.ID, map[string]any{
		"title": claim.Title,
	})
}

// EmitCorrectiveIssued emits a Fabric activity for corrective claims.
func (a *BoardAmplifier) EmitCorrectiveIssued(ctx context.Context, action *Action) {
	if a == nil || action == nil {
		return
	}
	a.emit(ctx, activity.ActionCorrectiveIssued, action.AgentID, action.ID, map[string]any{
		"action_type": string(action.Type),
	})
}

// EmitBoardPhaseChanged emits a Fabric activity for phase transitions.
func (a *BoardAmplifier) EmitBoardPhaseChanged(ctx context.Context, phase BoardPhase, iteration int, agentID string) {
	if a == nil {
		return
	}
	a.emit(ctx, activity.ActionBoardPhaseChanged, agentID, a.boardID, map[string]any{
		"phase":     string(phase),
		"iteration": iteration,
	})
}

// EmitBoardComplete emits the terminal Fabric activity when the board completes.
func (a *BoardAmplifier) EmitBoardComplete(ctx context.Context, agentID string) {
	if a == nil {
		return
	}
	a.emit(ctx, activity.ActionBoardComplete, agentID, a.boardID, nil)
}

func (a *BoardAmplifier) emit(_ context.Context, kind activity.ActionKind, agentID, targetArtifact string, payload map[string]any) {
	var payloadJSON json.RawMessage
	if payload != nil {
		if data, err := json.Marshal(payload); err == nil {
			payloadJSON = data
		}
	}
	act := activity.AgentActivity{
		ID:         activity.ActivityID(generateActivityID()),
		SessionID:  activity.SessionID(a.sessionID),
		Timestamp:  time.Now().UTC(),
		Resolution: activity.ResolutionFor(kind),
		Actor: activity.Actor{
			AgentID:    agentID,
			PipelineID: a.boardID,
		},
		Action: kind,
		Subject: activity.Subject{
			TargetArtifact: targetArtifact,
			Coordinates: map[string]string{
				"task_id":  a.taskID,
				"board_id": a.boardID,
			},
		},
		Payload:     payloadJSON,
		State:       activity.StatePoint,
		SourceTable: "claims_board",
		SourceID:    targetArtifact,
	}
	activity.Append(context.Background(), act)
}

// ────────────────────────────────────────────────────────────────────
// Bus delta emissions
// ────────────────────────────────────────────────────────────────────

// PublishInboxDeltas emits one InboxDelta per directed-agent
// Relation on the claim (subject, evaluator). Each delta is published
// on the agent-specific inbox topic. The claim and action are the
// authoritative inputs — the amplifier does no board lookup.
func (a *BoardAmplifier) PublishInboxDeltas(ctx context.Context, action *Action, claim *Claim) {
	if a == nil || action == nil || claim == nil {
		return
	}
	deltas := a.buildInboxDeltas(action, claim)
	if len(deltas) == 0 {
		return
	}
	a.dispatchInbox(ctx, deltas)
}

// PublishTestamentDelta emits a TestamentDelta on the claim's
// status-testified topic.
func (a *BoardAmplifier) PublishTestamentDelta(ctx context.Context, testament *Testament, claim *Claim) {
	if a == nil || testament == nil || claim == nil {
		return
	}
	delta := a.buildTestamentDelta(testament, claim)
	topic := ClaimStatusTopic(a.sessionID, claim.ID, ClaimStatusTestified)
	a.dispatchSingle(ctx, topic, delta)
}

// PublishValidationDelta emits a ValidationDelta on the
// validation-verdict topic. Also mirrored on the claim-accepted or
// claim-rejected topic when the validation resolves the claim.
func (a *BoardAmplifier) PublishValidationDelta(ctx context.Context, delta ValidationDelta) {
	if a == nil {
		return
	}
	primary := ValidationTopic(a.sessionID, delta.ValidationID, ValidationStatus(delta.Verdict))
	a.dispatchSingle(ctx, primary, delta)

	if delta.ClaimAutoAccepted {
		a.dispatchSingle(
			ctx,
			ClaimStatusTopic(a.sessionID, delta.ClaimID, ClaimStatusAccepted),
			delta,
		)
	}
}

// PublishClaimStatusDelta emits a ClaimStatusDelta on the
// claim-status topic for the target status.
func (a *BoardAmplifier) PublishClaimStatusDelta(ctx context.Context, delta ClaimStatusDelta) {
	if a == nil {
		return
	}
	topic := ClaimStatusTopic(a.sessionID, delta.ClaimID, delta.ToStatus)
	a.dispatchSingle(ctx, topic, delta)
}

// PublishPhaseDelta emits a PhaseDelta on the phase topic.
func (a *BoardAmplifier) PublishPhaseDelta(ctx context.Context, delta PhaseDelta) {
	if a == nil {
		return
	}
	topic := PhaseTopic(a.sessionID, delta.ToPhase)
	a.dispatchSingle(ctx, topic, delta)
}

// ────────────────────────────────────────────────────────────────────
// Delta builders
// ────────────────────────────────────────────────────────────────────

func (a *BoardAmplifier) buildInboxDeltas(action *Action, claim *Claim) []inboxDispatch {
	issuerID := action.AgentID
	if issuerID == "" {
		issuerID = IssuerAgentID(claim.Relations)
	}
	now := time.Now().UTC()
	var out []inboxDispatch
	for _, r := range claim.Relations {
		if r.RelatedType != RelatedTypeAgent {
			continue
		}
		if !isDirectedAgentRelationship(r.Relationship) {
			continue
		}
		delta := InboxDelta{
			SessionID:       a.sessionID,
			BoardID:         a.boardID,
			ActionID:        action.ID,
			ClaimID:         claim.ID,
			Sequence:        claim.Sequence,
			EmittedAt:       now,
			Relationship:    r.Relationship,
			AgentID:         r.Related,
			ActionKind:      claim.ActionType,
			Priority:        claim.Priority,
			Scope:           copyScope(claim.Scope),
			ValidationCount: len(claim.Validations),
			DependsOn:       collectRelatedIDs(claim.Relations, RelationshipDependsOn),
			IssuerAgentID:   issuerID,
			Title:           claim.Title,
			Description:     claim.Description,
			Deadline:        claim.Deadline,
			Iteration:       claim.Iteration,
		}
		topic := InboxTopic(a.sessionID, r.Related, r.Relationship, claim.ActionType)
		out = append(out, inboxDispatch{topic: topic, delta: delta})
	}
	return out
}

type inboxDispatch struct {
	topic string
	delta InboxDelta
}

func (a *BoardAmplifier) buildTestamentDelta(testament *Testament, claim *Claim) TestamentDelta {
	verdict := DeriveTestamentVerdict(testament.Artifacts)
	kinds := CollectArtifactKinds(testament.Artifacts)
	issuer := IssuerAgentID(claim.Relations)
	return TestamentDelta{
		SessionID:      a.sessionID,
		BoardID:        a.boardID,
		ClaimID:        claim.ID,
		TestamentID:    testament.ID,
		Sequence:       testament.Sequence,
		EmittedAt:      time.Now().UTC(),
		Verdict:        verdict,
		ArtifactCount:  len(testament.Artifacts),
		ArtifactKinds:  kinds,
		SubjectAgentID: SubjectAgentID(claim.Relations),
		IssuerAgentID:  issuer,
		Summary:        testament.Summary,
		Confidence:     testament.Confidence,
		AutoAccepted:   false,
	}
}

// ────────────────────────────────────────────────────────────────────
// Dispatch helpers
// ────────────────────────────────────────────────────────────────────

func (a *BoardAmplifier) dispatchInbox(ctx context.Context, deltas []inboxDispatch) {
	publisher := a.deltaBus
	emit := func(runCtx context.Context) error {
		a.publishInboxBatch(runCtx, publisher, deltas)
		return nil
	}
	a.runTracked(ctx, "claims_amplifier_inbox", emit)
}

func (a *BoardAmplifier) publishInboxBatch(ctx context.Context, publisher DeltaPublisher, deltas []inboxDispatch) {
	for _, d := range deltas {
		if err := publisher.PublishDelta(ctx, d.topic, d.delta); err != nil {
			a.reportEmitError("inbox_publish_failed", d.topic, err)
		}
	}
}

func (a *BoardAmplifier) dispatchSingle(ctx context.Context, topic string, delta Delta) {
	publisher := a.deltaBus
	emit := func(runCtx context.Context) error {
		if err := publisher.PublishDelta(runCtx, topic, delta); err != nil {
			a.reportEmitError("delta_publish_failed", topic, err)
		}
		return nil
	}
	a.runTracked(ctx, "claims_amplifier_delta", emit)
}

// runTracked dispatches fn under scope (async, bounded) when a scope
// is wired, or runs it synchronously otherwise. Never blocks the
// caller for more than a goroutine launch.
func (a *BoardAmplifier) runTracked(ctx context.Context, description string, fn func(context.Context) error) {
	if a.scope == nil {
		// Synchronous path — tests and standalone tools.
		if err := fn(ctx); err != nil {
			a.reportEmitError("sync_emit_failed", description, err)
		}
		return
	}
	if err := a.scope.Go(description, amplifierEmitTimeout, fn); err != nil {
		a.reportEmitError("scope_dispatch_failed", description, err)
	}
}

func (a *BoardAmplifier) reportEmitError(event, topic string, err error) {
	slog.Error(event,
		"topic", topic,
		"session_id", a.sessionID,
		"board_id", a.boardID,
		"err", err.Error(),
	)
	if a.errorSink != nil {
		a.errorSink(event + ": " + topic + ": " + err.Error())
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// isDirectedAgentRelationship reports whether r should trigger an
// InboxDelta. Subject and evaluator relations are directed; issuer
// is the actor (not a recipient) and receives no delta.
func isDirectedAgentRelationship(relationship string) bool {
	return relationship == RelationshipSubject || relationship == RelationshipEvaluator
}

func collectRelatedIDs(relations []Relation, relationship string) []string {
	var out []string
	for _, r := range relations {
		if r.Relationship == relationship && r.Related != "" {
			out = append(out, r.Related)
		}
	}
	return out
}

func copyScope(src []ClaimScopeEntry) []ClaimScopeEntry {
	if len(src) == 0 {
		return nil
	}
	out := make([]ClaimScopeEntry, len(src))
	copy(out, src)
	return out
}

// EvaluatorAgentID returns the agent on the first evaluator Relation,
// or empty. Complements IssuerAgentID / SubjectAgentID in types.go.
func EvaluatorAgentID(relations []Relation) string {
	r := FindRelation(relations, RelationshipEvaluator)
	if r == nil {
		return ""
	}
	return r.Related
}

func generateActivityID() string {
	return "act_" + uuid.NewString()[:12]
}
