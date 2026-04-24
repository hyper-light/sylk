package claims

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Tunable defaults. No magic numbers scattered through the code —
// everything with a policy implication lives here and can be adjusted
// in one place.
const (
	defaultMaxIterations      = 3
	subscriberNotifyTimeout   = 5 * time.Second
	subscriberNotifyTaskLabel = "claims_board_notify"
)

// ClaimsBoard is the per-pipeline (or per-session) sovereign store that
// agents work against collaboratively. Thread-safe for concurrent reads
// (RLock) and atomic writes (Lock).
//
// Structural ownership: Claims own Validations. Testaments own Artifacts.
// No separate maps for validations or artifacts — they are accessed
// through their parent entity.
type ClaimsBoard struct {
	mu sync.RWMutex

	boardID    string
	pipelineID string
	taskID     string
	sessionID  string

	phase         BoardPhase
	iteration     int
	maxIterations int

	actions    map[string]*Action
	claims     map[string]*Claim
	testaments map[string]*Testament
	claimOrder []string
	phaseLog   []StatusChange

	// relationsIdx is a secondary index over every Relation carried
	// by every object. Serves the named board-context queries
	// (trace_claim_ancestry, list_action_claims, find_overlapping_claims,
	// etc.) in O(1) lookup instead of full-table scans.
	relationsIdx *relationsIndex

	seq atomic.Uint64

	amplifier *BoardAmplifier
	scope     ScopeProvider // nil = synchronous (tests)

	subscribersMu sync.Mutex
	subscribers   []boardSubscription
	subscriberSeq int64

	// notificationErrors accumulates subscriber notification + emission
	// failures. Exposed in the projection so agents see them on the
	// next board query and can record them as testament error
	// artifacts. Drained on read (projection) to prevent unbounded
	// growth.
	notificationErrors []string
}

type boardSubscription struct {
	id int64
	fn ClaimsBoardSubscriber
}

// NewClaimsBoard creates a new board. Amplifier, scope, and delta bus
// are wired from cfg; each is independently optional — a test board
// can run with none. A production board passes Scope + DeltaBus to
// get tracked goroutines and bus projection.
func NewClaimsBoard(cfg ClaimsBoardConfig) *ClaimsBoard {
	boardID := firstNonEmpty(cfg.BoardID, uuid.NewString())
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}
	b := &ClaimsBoard{
		boardID:       boardID,
		pipelineID:    cfg.PipelineID,
		taskID:        cfg.TaskID,
		sessionID:     cfg.SessionID,
		phase:         BoardPhaseImplementation,
		maxIterations: maxIter,
		actions:       make(map[string]*Action),
		claims:        make(map[string]*Claim),
		testaments:    make(map[string]*Testament),
		relationsIdx:  newRelationsIndex(),
		scope:         cfg.Scope,
	}
	if cfg.SessionID != "" {
		amp := NewBoardAmplifier(cfg.SessionID, cfg.TaskID, boardID).
			WithDeltaBus(cfg.DeltaBus).
			WithScope(cfg.Scope).
			WithErrorSink(b.RecordNotificationError)
		b.amplifier = amp
	}
	return b
}

// ── Accessors ───────────────────────────────────────────────────────

func (b *ClaimsBoard) BoardID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.boardID
}

func (b *ClaimsBoard) TaskID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.taskID
}

func (b *ClaimsBoard) SessionID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionID
}

func (b *ClaimsBoard) Phase() BoardPhase {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.phase
}

func (b *ClaimsBoard) Iteration() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.iteration
}

// Amplifier exposes the board's amplifier for callers that need to
// publish derived deltas (e.g. the inbox bootstrap replay). nil when
// the board was constructed without a session.
func (b *ClaimsBoard) Amplifier() *BoardAmplifier {
	return b.amplifier
}

// ── PostAction ──────────────────────────────────────────────────────

// PostAction issues a set of claims as a claim action. Validates all
// claims for duplicate IDs BEFORE inserting any (no partial mutation).
// Each claim's Validations carry the correct ClaimID after stamping.
func (b *ClaimsBoard) PostAction(_ context.Context, action Action, inputClaims []Claim) error {
	if len(inputClaims) == 0 {
		return fmt.Errorf("action must contain at least one claim")
	}

	b.mu.Lock()

	now := time.Now().UTC()

	if err := b.validatePostActionLocked(inputClaims); err != nil {
		b.mu.Unlock()
		return err
	}

	b.stampActionLocked(&action, now)
	b.indexRelations(action.ID, action.Relations)

	for i := range inputClaims {
		c := &inputClaims[i]
		b.stampClaimLocked(c, &action, now)
		b.claims[c.ID] = c
		b.claimOrder = append(b.claimOrder, c.ID)
		b.indexRelations(c.ID, c.Relations)
		b.relationsIdx.addScope(c.ID, c.Scope)
	}

	// Release lock BEFORE notifying subscribers (prevents deadlock).
	b.mu.Unlock()

	// Amplify: fabric + bus.
	ctx := context.Background()
	b.amplifier.EmitActionPosted(ctx, &action)
	for i := range inputClaims {
		b.amplifier.EmitClaimIssued(ctx, &inputClaims[i])
		b.amplifier.PublishInboxDeltas(ctx, &action, &inputClaims[i])
	}

	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) validatePostActionLocked(inputClaims []Claim) error {
	for i := range inputClaims {
		id := inputClaims[i].ID
		if id == "" {
			continue // will be generated on stamp
		}
		if _, exists := b.claims[id]; exists {
			return fmt.Errorf("duplicate claim ID %q", id)
		}
		for j := 0; j < i; j++ {
			if inputClaims[j].ID == id {
				return fmt.Errorf("duplicate claim ID %q in batch", id)
			}
		}
	}
	return nil
}

func (b *ClaimsBoard) stampActionLocked(action *Action, now time.Time) {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.SessionID = b.sessionID
	action.PipelineID = b.pipelineID
	action.TaskID = b.taskID
	action.Sequence = b.nextSeq()
	action.Created = now
	action.Accessed = now
	if action.Status == "" {
		action.Status = ActionStatusPending
	}
	action.StatusHistory = append(action.StatusHistory, StatusChange{
		To:      string(action.Status),
		Reason:  "action posted",
		AgentID: action.AgentID,
		Changed: now,
	})
	b.actions[action.ID] = action
}

func (b *ClaimsBoard) stampClaimLocked(c *Claim, action *Action, now time.Time) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	c.SessionID = b.sessionID
	c.PipelineID = b.pipelineID
	c.TaskID = b.taskID
	c.Sequence = b.nextSeq()
	c.Created = now
	c.Accessed = now
	c.Status = ClaimStatusPending
	c.ActionType = action.Type
	c.StatusHistory = append(c.StatusHistory, StatusChange{
		To:      string(ClaimStatusPending),
		Reason:  "claim posted",
		AgentID: action.AgentID,
		Changed: now,
	})
	if !HasRelation(c.Relations, RelationshipClaimAction, action.ID) {
		c.Relations = append(c.Relations, Relation{
			Related:      action.ID,
			RelatedType:  RelatedTypeAction,
			Relationship: RelationshipClaimAction,
		})
	}
	b.stampValidationsLocked(c, now)
}

func (b *ClaimsBoard) stampValidationsLocked(c *Claim, now time.Time) {
	for _, v := range c.Validations {
		if v.ID == "" {
			v.ID = uuid.NewString()
		}
		v.ClaimID = c.ID
		v.SessionID = b.sessionID
		v.PipelineID = b.pipelineID
		v.TaskID = b.taskID
		v.Sequence = b.nextSeq()
		v.Created = now
		v.Accessed = now
		if v.Status == "" {
			v.Status = ValidationStatusPending
		}
	}
}

func (b *ClaimsBoard) indexRelations(objID string, relations []Relation) {
	b.relationsIdx.addRelations(objID, relations)
}

// ── UpdateClaimProgress ─────────────────────────────────────────────

func (b *ClaimsBoard) UpdateClaimProgress(_ context.Context, claimID string, _ ClaimProgressUpdate, agentID string) error {
	b.mu.Lock()

	c, ok := b.claims[claimID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("claim %q not found", claimID)
	}
	if c.Status.IsTerminal() {
		b.mu.Unlock()
		return fmt.Errorf("claim %q is in terminal status %q", claimID, c.Status)
	}

	now := time.Now().UTC()
	fromStatus := c.Status
	statusChanged := false
	c.Accessed = now
	if c.Status == ClaimStatusPending {
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(ClaimStatusPending),
			To:      string(ClaimStatusInProgress),
			Reason:  "work started",
			AgentID: agentID,
			Changed: now,
		})
		c.Status = ClaimStatusInProgress
		statusChanged = true
	}

	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitClaimUpdated(ctx, c, agentID)
	if statusChanged {
		b.amplifier.PublishClaimStatusDelta(ctx, ClaimStatusDelta{
			SessionID:      b.sessionID,
			BoardID:        b.boardID,
			ClaimID:        c.ID,
			Sequence:       b.seq.Load(),
			EmittedAt:      now,
			FromStatus:     fromStatus,
			ToStatus:       c.Status,
			Reason:         "work started",
			AgentID:        agentID,
			SubjectAgentID: SubjectAgentID(c.Relations),
			IssuerAgentID:  IssuerAgentID(c.Relations),
		})
	}
	b.notifySubscribers()
	return nil
}

// ── SubmitTestaments ────────────────────────────────────────────────

// SubmitTestaments records testaments with their artifacts. Each
// testament's Artifacts field carries the proof. Artifacts get stamped
// with TestamentID. Claims transition to testified.
func (b *ClaimsBoard) SubmitTestaments(_ context.Context, action Action, testaments []Testament) error {
	if len(testaments) == 0 {
		return fmt.Errorf("testament action must contain at least one testament")
	}

	b.mu.Lock()
	now := time.Now().UTC()
	b.stampTestamentActionLocked(&action, now)

	// Capture post-stamp claim snapshots so the amplifier gets
	// authoritative references once the lock is released. We resolve
	// the claim referenced by each testament BEFORE unlocking.
	claimRefs := make([]*Claim, len(testaments))
	for i := range testaments {
		b.stampTestamentLocked(&testaments[i], &action, now)
		claimRefs[i] = b.resolveClaimForTestamentLocked(&testaments[i], now)
	}

	b.mu.Unlock()

	// Amplify.
	ctx := context.Background()
	for i := range testaments {
		b.amplifier.EmitTestamentSubmitted(ctx, &testaments[i])
		for _, artifact := range testaments[i].Artifacts {
			b.amplifier.EmitArtifactPublished(ctx, artifact)
		}
		if claimRefs[i] != nil {
			b.amplifier.PublishTestamentDelta(ctx, &testaments[i], claimRefs[i])
		}
	}

	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) stampTestamentActionLocked(action *Action, now time.Time) {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.SessionID = b.sessionID
	action.PipelineID = b.pipelineID
	action.TaskID = b.taskID
	action.Sequence = b.nextSeq()
	action.Created = now
	action.Accessed = now
	if action.Status == "" {
		action.Status = ActionStatusComplete
	}
	b.actions[action.ID] = action
	b.indexRelations(action.ID, action.Relations)
}

func (b *ClaimsBoard) stampTestamentLocked(t *Testament, action *Action, now time.Time) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.SessionID = b.sessionID
	t.PipelineID = b.pipelineID
	t.TaskID = b.taskID
	t.Sequence = b.nextSeq()
	t.Created = now
	t.Accessed = now

	if !HasRelation(t.Relations, RelationshipTestamentAction, action.ID) {
		t.Relations = append(t.Relations, Relation{
			Related:      action.ID,
			RelatedType:  RelatedTypeAction,
			Relationship: RelationshipTestamentAction,
		})
	}

	for _, artifact := range t.Artifacts {
		if artifact.ID == "" {
			artifact.ID = uuid.NewString()
		}
		artifact.TestamentID = t.ID
		artifact.SessionID = b.sessionID
		artifact.PipelineID = b.pipelineID
		artifact.TaskID = b.taskID
		artifact.Sequence = b.nextSeq()
		artifact.Created = now
		artifact.Accessed = now
	}

	b.testaments[t.ID] = t
	b.indexRelations(t.ID, t.Relations)
}

// resolveClaimForTestamentLocked returns the Claim referenced by the
// testament's "claim" Relation and transitions it to Testified. Nil
// when the relation is absent or the target claim no longer exists.
// Caller holds b.mu (write-locked).
func (b *ClaimsBoard) resolveClaimForTestamentLocked(t *Testament, now time.Time) *Claim {
	claimRel := FindRelation(t.Relations, RelationshipClaim)
	if claimRel == nil {
		return nil
	}
	c, ok := b.claims[claimRel.Related]
	if !ok {
		return nil
	}
	if !c.Status.IsTerminal() && c.Status != ClaimStatusTestified {
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(c.Status),
			To:      string(ClaimStatusTestified),
			Reason:  "testament submitted",
			AgentID: t.AgentID,
			Changed: now,
		})
		c.Status = ClaimStatusTestified
		c.Accessed = now
	}
	return c
}

// ── EvaluateValidation ──────────────────────────────────────────────

// EvaluateValidation transitions a validation on a specific claim. If
// all required validations on the claim pass, the claim auto-accepts.
func (b *ClaimsBoard) EvaluateValidation(_ context.Context, claimID, validationID string, change StatusChange) error {
	b.mu.Lock()

	c, ok := b.claims[claimID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("claim %q not found", claimID)
	}

	v := findValidationOnClaim(c, validationID)
	if v == nil {
		b.mu.Unlock()
		return fmt.Errorf("validation %q not found on claim %q", validationID, claimID)
	}

	now := time.Now().UTC()
	change.Changed = now
	change.From = string(v.Status)
	v.StatusHistory = append(v.StatusHistory, change)
	v.Status = ValidationStatus(change.To)
	v.Accessed = now

	accepted := c.AllValidationsPassed()
	if accepted {
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(c.Status),
			To:      string(ClaimStatusAccepted),
			Reason:  "all required validations passed",
			AgentID: change.AgentID,
			Changed: now,
		})
		c.Status = ClaimStatusAccepted
		c.Accessed = now
	}

	validationDelta := b.buildValidationDeltaLocked(c, v, change, accepted, now)

	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitClaimValidated(ctx, v, change.AgentID)
	if accepted {
		b.amplifier.EmitClaimAccepted(ctx, c)
	}
	b.amplifier.PublishValidationDelta(ctx, validationDelta)
	b.notifySubscribers()
	return nil
}

func findValidationOnClaim(c *Claim, validationID string) *Validation {
	for _, candidate := range c.Validations {
		if candidate.ID == validationID {
			return candidate
		}
	}
	return nil
}

func (b *ClaimsBoard) buildValidationDeltaLocked(c *Claim, v *Validation, change StatusChange, accepted bool, now time.Time) ValidationDelta {
	remaining := c.PendingValidationCount()
	failed := countFailedValidations(c)
	return ValidationDelta{
		SessionID:          b.sessionID,
		BoardID:            b.boardID,
		ClaimID:            c.ID,
		ValidationID:       v.ID,
		Sequence:           v.Sequence,
		EmittedAt:          now,
		Verdict:            change.To,
		EvaluatorAgentID:   change.AgentID,
		Reason:             change.Reason,
		RemainingOnClaim:   remaining,
		FailedCountOnClaim: failed,
		ClaimAutoAccepted:  accepted,
		SubjectAgentID:     SubjectAgentID(c.Relations),
		IssuerAgentID:      IssuerAgentID(c.Relations),
	}
}

func countFailedValidations(c *Claim) int {
	count := 0
	for _, v := range c.Validations {
		if v.Status == ValidationStatusFailed {
			count++
		}
	}
	return count
}

// ── RejectClaim ─────────────────────────────────────────────────────

func (b *ClaimsBoard) RejectClaim(_ context.Context, claimID string, change StatusChange, replacements *Action, replacementClaims []Claim) error {
	b.mu.Lock()

	c, ok := b.claims[claimID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("claim %q not found", claimID)
	}
	if c.Status.IsTerminal() {
		b.mu.Unlock()
		return fmt.Errorf("claim %q already terminal (%s)", claimID, c.Status)
	}

	now := time.Now().UTC()
	fromStatus := c.Status
	change.From = string(c.Status)
	change.To = string(ClaimStatusRejected)
	change.Changed = now
	c.StatusHistory = append(c.StatusHistory, change)
	c.Status = ClaimStatusRejected
	c.Accessed = now

	remediationIDs := b.applyRemediationLocked(claimID, replacements, replacementClaims, change.AgentID, now)

	rejectedDelta := ClaimStatusDelta{
		SessionID:           b.sessionID,
		BoardID:             b.boardID,
		ClaimID:             c.ID,
		Sequence:            c.Sequence,
		EmittedAt:           now,
		FromStatus:          fromStatus,
		ToStatus:            ClaimStatusRejected,
		Reason:              change.Reason,
		AgentID:             change.AgentID,
		SubjectAgentID:      SubjectAgentID(c.Relations),
		IssuerAgentID:       IssuerAgentID(c.Relations),
		RemediationClaimIDs: remediationIDs,
	}

	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitClaimRejected(ctx, c)
	b.amplifier.PublishClaimStatusDelta(ctx, rejectedDelta)

	// Emit InboxDeltas for each replacement claim so their subjects
	// see the remediation.
	if replacements != nil && len(replacementClaims) > 0 {
		b.amplifier.EmitCorrectiveIssued(ctx, replacements)
		for i := range replacementClaims {
			b.amplifier.EmitClaimIssued(ctx, &replacementClaims[i])
			b.amplifier.PublishInboxDeltas(ctx, replacements, &replacementClaims[i])
		}
	}

	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) applyRemediationLocked(rejectedClaimID string, replacements *Action, replacementClaims []Claim, agentID string, now time.Time) []string {
	if replacements == nil || len(replacementClaims) == 0 {
		return nil
	}
	b.stampRemediationActionLocked(replacements, rejectedClaimID, agentID, now)
	ids := make([]string, 0, len(replacementClaims))
	for i := range replacementClaims {
		rc := &replacementClaims[i]
		b.stampRemediationClaimLocked(rc, replacements, rejectedClaimID, agentID, now)
		b.claims[rc.ID] = rc
		b.claimOrder = append(b.claimOrder, rc.ID)
		b.indexRelations(rc.ID, rc.Relations)
		b.relationsIdx.addScope(rc.ID, rc.Scope)
		ids = append(ids, rc.ID)
	}
	return ids
}

func (b *ClaimsBoard) stampRemediationActionLocked(action *Action, rejectedID, agentID string, now time.Time) {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.SessionID = b.sessionID
	action.PipelineID = b.pipelineID
	action.TaskID = b.taskID
	action.Sequence = b.nextSeq()
	action.Created = now
	action.Accessed = now
	if action.Status == "" {
		action.Status = ActionStatusPending
	}
	action.StatusHistory = append(action.StatusHistory, StatusChange{
		To:      string(action.Status),
		Reason:  "remediation for rejected claim " + rejectedID,
		AgentID: agentID,
		Changed: now,
	})
	b.actions[action.ID] = action
	b.indexRelations(action.ID, action.Relations)
}

func (b *ClaimsBoard) stampRemediationClaimLocked(rc *Claim, replacements *Action, rejectedID, agentID string, now time.Time) {
	if rc.ID == "" {
		rc.ID = uuid.NewString()
	}
	rc.SessionID = b.sessionID
	rc.PipelineID = b.pipelineID
	rc.TaskID = b.taskID
	rc.Sequence = b.nextSeq()
	rc.Created = now
	rc.Accessed = now
	rc.Status = ClaimStatusPending
	rc.Iteration = b.iteration + 1
	rc.ActionType = replacements.Type
	rc.StatusHistory = append(rc.StatusHistory, StatusChange{
		To:      string(ClaimStatusPending),
		Reason:  "remediation for rejected claim " + rejectedID,
		AgentID: agentID,
		Changed: now,
	})
	if !HasRelation(rc.Relations, RelationshipSupersedes, rejectedID) {
		rc.Relations = append(rc.Relations, Relation{
			Related:      rejectedID,
			RelatedType:  RelatedTypeClaim,
			Relationship: RelationshipSupersedes,
		})
	}
	if !HasRelation(rc.Relations, RelationshipClaimAction, replacements.ID) {
		rc.Relations = append(rc.Relations, Relation{
			Related:      replacements.ID,
			RelatedType:  RelatedTypeAction,
			Relationship: RelationshipClaimAction,
		})
	}
	b.stampValidationsLocked(rc, now)
}

// ── Phase Transitions ───────────────────────────────────────────────

func (b *ClaimsBoard) TransitionToValidation(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseImplementation {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition to validation from %s", b.phase)
	}
	if err := b.ensureAllTestifiedLocked(); err != nil {
		b.mu.Unlock()
		return err
	}
	fromPhase := b.phase
	b.phase = BoardPhaseValidation
	b.logPhaseTransitionLocked(fromPhase, b.phase, "all claims testified", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitBoardPhaseChanged(ctx, phase, iteration, "")
	b.amplifier.PublishPhaseDelta(ctx, PhaseDelta{
		SessionID: b.sessionID,
		BoardID:   b.boardID,
		TaskID:    b.taskID,
		Sequence:  b.seq.Load(),
		EmittedAt: time.Now().UTC(),
		FromPhase: fromPhase,
		ToPhase:   phase,
		Iteration: iteration,
		Reason:    "all claims testified",
	})
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) TransitionToImplementation(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseValidation {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition to implementation from %s", b.phase)
	}
	if err := b.requirePendingClaimLocked(); err != nil {
		b.mu.Unlock()
		return err
	}
	if b.iteration >= b.maxIterations {
		b.mu.Unlock()
		return fmt.Errorf("max iterations (%d) reached, cannot re-enter implementation", b.maxIterations)
	}
	fromPhase := b.phase
	b.iteration++
	b.phase = BoardPhaseImplementation
	b.logPhaseTransitionLocked(fromPhase, b.phase, "remediation re-entry", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitBoardPhaseChanged(ctx, phase, iteration, "")
	b.amplifier.PublishPhaseDelta(ctx, PhaseDelta{
		SessionID: b.sessionID,
		BoardID:   b.boardID,
		TaskID:    b.taskID,
		Sequence:  b.seq.Load(),
		EmittedAt: time.Now().UTC(),
		FromPhase: fromPhase,
		ToPhase:   phase,
		Iteration: iteration,
		Reason:    "remediation re-entry",
	})
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) MarkComplete(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseValidation {
		b.mu.Unlock()
		return fmt.Errorf("cannot mark complete from %s", b.phase)
	}
	if err := b.ensureAllAcceptedLocked(); err != nil {
		b.mu.Unlock()
		return err
	}
	fromPhase := b.phase
	b.phase = BoardPhaseComplete
	b.logPhaseTransitionLocked(fromPhase, b.phase, "all claims accepted", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	ctx := context.Background()
	b.amplifier.EmitBoardComplete(ctx, "")
	b.amplifier.PublishPhaseDelta(ctx, PhaseDelta{
		SessionID: b.sessionID,
		BoardID:   b.boardID,
		TaskID:    b.taskID,
		Sequence:  b.seq.Load(),
		EmittedAt: time.Now().UTC(),
		FromPhase: fromPhase,
		ToPhase:   phase,
		Iteration: iteration,
		Reason:    "all claims accepted",
	})
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) ensureAllTestifiedLocked() error {
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusTestified && c.Status != ClaimStatusAccepted {
			return fmt.Errorf("claim %q has status %s, expected testified or accepted", c.ID, c.Status)
		}
	}
	return nil
}

func (b *ClaimsBoard) requirePendingClaimLocked() error {
	for _, c := range b.claims {
		if c.Status == ClaimStatusPending {
			return nil
		}
	}
	return fmt.Errorf("no pending claims exist for re-entry to implementation")
}

func (b *ClaimsBoard) ensureAllAcceptedLocked() error {
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusAccepted {
			return fmt.Errorf("claim %q has status %s, expected accepted", c.ID, c.Status)
		}
	}
	return nil
}

func (b *ClaimsBoard) logPhaseTransitionLocked(from, to BoardPhase, reason, agentID string) {
	b.phaseLog = append(b.phaseLog, StatusChange{
		From:    string(from),
		To:      string(to),
		Reason:  reason,
		AgentID: agentID,
		Changed: time.Now().UTC(),
	})
}

// ── Queries ─────────────────────────────────────────────────────────

func (b *ClaimsBoard) Projection() *ClaimsBoardProjection {
	b.mu.RLock()
	hasErrors := len(b.notificationErrors) > 0
	b.mu.RUnlock()

	if !hasErrors {
		b.mu.RLock()
		p := b.projectionLocked()
		b.mu.RUnlock()
		return p
	}

	// Slow path: projection + drain atomically under write lock.
	b.mu.Lock()
	p := b.projectionLocked()
	b.notificationErrors = b.notificationErrors[:0]
	b.mu.Unlock()
	return p
}

func (b *ClaimsBoard) projectionLocked() *ClaimsBoardProjection {
	p := &ClaimsBoardProjection{
		BoardID:   b.boardID,
		TaskID:    b.taskID,
		Phase:     b.phase,
		Iteration: b.iteration,
		Updated:   time.Now().UTC(),
	}

	b.populateClaimsProjectionLocked(p)
	b.populateActionsProjectionLocked(p)
	b.populateTestamentsProjectionLocked(p)

	if len(b.notificationErrors) > 0 {
		p.NotificationErrors = make([]string, len(b.notificationErrors))
		copy(p.NotificationErrors, b.notificationErrors)
	}

	return p
}

func (b *ClaimsBoard) populateClaimsProjectionLocked(p *ClaimsBoardProjection) {
	for _, id := range b.claimOrder {
		c, ok := b.claims[id]
		if !ok {
			continue
		}
		clone := *c
		p.Claims = append(p.Claims, clone)
		p.TotalClaims++
		incrementClaimStatusCount(p, c.Status)
		incrementValidationCounts(p, c.Validations)
	}
}

func incrementClaimStatusCount(p *ClaimsBoardProjection, status ClaimStatus) {
	switch status {
	case ClaimStatusPending:
		p.PendingCount++
	case ClaimStatusInProgress:
		p.InProgressCount++
	case ClaimStatusTestified:
		p.TestifiedCount++
	case ClaimStatusAccepted:
		p.AcceptedCount++
	case ClaimStatusRejected:
		p.RejectedCount++
	}
}

func incrementValidationCounts(p *ClaimsBoardProjection, validations []*Validation) {
	for _, v := range validations {
		p.TotalValidations++
		switch v.Status {
		case ValidationStatusPassed:
			p.PassedValidations++
		case ValidationStatusFailed:
			p.FailedValidations++
		case ValidationStatusSkipped:
			p.SkippedValidations++
		}
	}
}

func (b *ClaimsBoard) populateActionsProjectionLocked(p *ClaimsBoardProjection) {
	for _, a := range b.actions {
		clone := *a
		p.Actions = append(p.Actions, clone)
		p.TotalClaimActions++
	}
}

func (b *ClaimsBoard) populateTestamentsProjectionLocked(p *ClaimsBoardProjection) {
	for _, t := range b.testaments {
		clone := *t
		p.Testaments = append(p.Testaments, clone)
		p.TotalTestaments++
		p.TotalArtifacts += len(t.Artifacts)
	}
}

func (b *ClaimsBoard) ReadyForValidation() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.claims) == 0 {
		return false
	}
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusTestified && c.Status != ClaimStatusAccepted {
			return false
		}
	}
	return true
}

func (b *ClaimsBoard) AllAccepted() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.claims) == 0 {
		return false
	}
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusAccepted {
			return false
		}
	}
	return true
}

func (b *ClaimsBoard) ClaimByID(id string) (*Claim, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.claims[id]
	if !ok {
		return nil, false
	}
	clone := *c
	return &clone, true
}

func (b *ClaimsBoard) ClaimsByRelation(relationship, relatedID string) []*Claim {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*Claim
	for _, c := range b.claims {
		if HasRelation(c.Relations, relationship, relatedID) {
			clone := *c
			out = append(out, &clone)
		}
	}
	return out
}

func (b *ClaimsBoard) IncompleteClaims() []*Claim {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*Claim
	for _, c := range b.claims {
		if c.Status != ClaimStatusAccepted && c.Status != ClaimStatusSuperseded {
			clone := *c
			out = append(out, &clone)
		}
	}
	return out
}

func (b *ClaimsBoard) TestamentsByClaim(claimID string) []*Testament {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*Testament
	for _, t := range b.testaments {
		if HasRelation(t.Relations, RelationshipClaim, claimID) {
			clone := *t
			out = append(out, &clone)
		}
	}
	return out
}

func (b *ClaimsBoard) FailedValidations() []*Validation {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*Validation
	for _, c := range b.claims {
		for _, v := range c.Validations {
			if v.Status == ValidationStatusFailed {
				clone := *v
				out = append(out, &clone)
			}
		}
	}
	return out
}

// PhaseHistory returns a defensive copy of the phase transition log.
// Used by the show_phase_history skill.
func (b *ClaimsBoard) PhaseHistory() []StatusChange {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.phaseLog) == 0 {
		return nil
	}
	out := make([]StatusChange, len(b.phaseLog))
	copy(out, b.phaseLog)
	return out
}

// ── Subscription ────────────────────────────────────────────────────

func (b *ClaimsBoard) SubscribeProjection(fn ClaimsBoardSubscriber) func() {
	if b == nil || fn == nil {
		return func() {}
	}
	b.subscribersMu.Lock()
	b.subscriberSeq++
	id := b.subscriberSeq
	b.subscribers = append(b.subscribers, boardSubscription{id: id, fn: fn})
	b.subscribersMu.Unlock()

	return func() {
		b.subscribersMu.Lock()
		defer b.subscribersMu.Unlock()
		for i, s := range b.subscribers {
			if s.id == id {
				b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
				return
			}
		}
	}
}

// notifySubscribers computes the projection under read lock, then
// notifies subscribers WITHOUT holding any board lock (prevents
// deadlock if a subscriber reads the board).
//
// When a GoroutineScope is available, subscribers are notified
// concurrently via tracked goroutines. When scope is nil (tests),
// subscribers are notified synchronously.
//
// Subscriber errors are accumulated on notificationErrors and
// surfaced in the next Projection() call. They do not block the
// mutation. Panics are NOT recovered — they are bugs.
func (b *ClaimsBoard) notifySubscribers() {
	b.mu.RLock()
	proj := b.projectionLocked()
	b.mu.RUnlock()

	b.subscribersMu.Lock()
	subs := make([]ClaimsBoardSubscriber, len(b.subscribers))
	for i, s := range b.subscribers {
		subs[i] = s.fn
	}
	b.subscribersMu.Unlock()

	if len(subs) == 0 {
		return
	}

	for _, fn := range subs {
		b.dispatchSubscriber(fn, proj)
	}
}

func (b *ClaimsBoard) dispatchSubscriber(fn ClaimsBoardSubscriber, proj *ClaimsBoardProjection) {
	if b.scope == nil {
		if err := fn(proj); err != nil {
			b.RecordNotificationError("subscriber callback: " + err.Error())
		}
		return
	}
	err := b.scope.Go(subscriberNotifyTaskLabel, subscriberNotifyTimeout, func(_ context.Context) error {
		if cbErr := fn(proj); cbErr != nil {
			b.RecordNotificationError("subscriber callback: " + cbErr.Error())
			return cbErr
		}
		return nil
	})
	if err != nil {
		b.RecordNotificationError("dispatch: " + err.Error())
	}
}

// RecordNotificationError appends a notification or operational failure
// to the board. Surfaced in the next Projection() call so agents can
// record them as testament error artifacts. Thread-safe.
func (b *ClaimsBoard) RecordNotificationError(msg string) {
	b.mu.Lock()
	b.notificationErrors = append(b.notificationErrors, msg)
	b.mu.Unlock()
}

// ── Read-path accessors used by pull_work / context queries ─────────

// CloneClaim returns a defensive copy of the claim by id. ok=false
// when the claim isn't on the board.
func (b *ClaimsBoard) CloneClaim(id string) (*Claim, bool) {
	return b.ClaimByID(id)
}

// CloneAction returns a defensive copy of the action by id.
func (b *ClaimsBoard) CloneAction(id string) (*Action, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	a, ok := b.actions[id]
	if !ok {
		return nil, false
	}
	clone := *a
	return &clone, true
}

// CloneTestament returns a defensive copy of the testament by id.
func (b *ClaimsBoard) CloneTestament(id string) (*Testament, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t, ok := b.testaments[id]
	if !ok {
		return nil, false
	}
	clone := *t
	return &clone, true
}

// ObjectIDsWithRelation returns every object ID whose Relations
// contain the queried (RelatedType, Relationship, RelatedID) triple.
// Backed by the relationsIdx so the query is O(1) + hits in index.
func (b *ClaimsBoard) ObjectIDsWithRelation(relatedType, relationship, relatedID string) []string {
	return b.relationsIdx.objectsWithRelation(relatedType, relationship, relatedID)
}

// ClaimIDsWithScope returns claim IDs whose Scope matches.
func (b *ClaimsBoard) ClaimIDsWithScope(scopeKind, key string) []string {
	return b.relationsIdx.claimsWithScope(scopeKind, key)
}

// ── Internal helpers ────────────────────────────────────────────────

func (b *ClaimsBoard) nextSeq() uint64 {
	return b.seq.Add(1)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
