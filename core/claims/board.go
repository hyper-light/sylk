package claims

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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

	seq atomic.Uint64

	amplifier *BoardAmplifier
	scope     ScopeProvider // nil = synchronous (tests)

	subscribersMu sync.Mutex
	subscribers   []boardSubscription
	subscriberSeq int64
}

type boardSubscription struct {
	id int64
	fn ClaimsBoardSubscriber
}

// NewClaimsBoard creates a new board. If SessionID is provided, a
// BoardAmplifier is created for Fabric emission.
func NewClaimsBoard(cfg ClaimsBoardConfig) *ClaimsBoard {
	boardID := firstNonEmpty(cfg.BoardID, uuid.NewString())
	var amp *BoardAmplifier
	if cfg.SessionID != "" {
		amp = NewBoardAmplifier(cfg.SessionID, cfg.TaskID, boardID)
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 3 // default bound
	}
	return &ClaimsBoard{
		boardID:       boardID,
		pipelineID:    cfg.PipelineID,
		taskID:        cfg.TaskID,
		sessionID:     cfg.SessionID,
		phase:         BoardPhaseImplementation,
		maxIterations: maxIter,
		actions:       make(map[string]*Action),
		claims:        make(map[string]*Claim),
		testaments:    make(map[string]*Testament),
		amplifier:     amp,
		scope:         cfg.Scope,
	}
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

	// Validate: no duplicate IDs in batch or existing.
	for i := range inputClaims {
		id := inputClaims[i].ID
		if id == "" {
			continue // will be generated
		}
		if _, exists := b.claims[id]; exists {
			b.mu.Unlock()
			return fmt.Errorf("duplicate claim ID %q", id)
		}
		for j := 0; j < i; j++ {
			if inputClaims[j].ID == id {
				b.mu.Unlock()
				return fmt.Errorf("duplicate claim ID %q in batch", id)
			}
		}
	}

	// Stamp action.
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
		To: string(action.Status), Reason: "action posted",
		AgentID: action.AgentID, Changed: now,
	})
	b.actions[action.ID] = &action

	// Stamp and insert claims + their validations.
	for i := range inputClaims {
		c := &inputClaims[i]
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
			To: string(ClaimStatusPending), Reason: "claim posted",
			AgentID: action.AgentID, Changed: now,
		})
		if !HasRelation(c.Relations, RelationshipClaimAction, action.ID) {
			c.Relations = append(c.Relations, Relation{
				Related: action.ID, RelatedType: RelatedTypeAction,
				Relationship: RelationshipClaimAction,
			})
		}
		// Stamp validations with parent ClaimID.
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
		b.claims[c.ID] = c
		b.claimOrder = append(b.claimOrder, c.ID)
	}

	// Release lock BEFORE notifying subscribers (prevents deadlock).
	b.mu.Unlock()

	// Amplify.
	ctx := context.Background()
	b.amplifier.EmitActionPosted(ctx, &action)
	for i := range inputClaims {
		b.amplifier.EmitClaimIssued(ctx, &inputClaims[i])
	}

	b.notifySubscribers()
	return nil
}

// ── UpdateClaimProgress ─────────────────────────────────────────────

func (b *ClaimsBoard) UpdateClaimProgress(_ context.Context, claimID string, update ClaimProgressUpdate, agentID string) error {
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
	c.Accessed = now
	if c.Status == ClaimStatusPending {
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From: string(ClaimStatusPending), To: string(ClaimStatusInProgress),
			Reason: "work started", AgentID: agentID, Changed: now,
		})
		c.Status = ClaimStatusInProgress
	}

	b.mu.Unlock()

	b.amplifier.EmitClaimUpdated(context.Background(), c, agentID)
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

	// Stamp testament action.
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
	b.actions[action.ID] = &action

	for i := range testaments {
		t := &testaments[i]
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
				Related: action.ID, RelatedType: RelatedTypeAction,
				Relationship: RelationshipTestamentAction,
			})
		}

		// Stamp artifacts with parent TestamentID.
		for _, a := range t.Artifacts {
			if a.ID == "" {
				a.ID = uuid.NewString()
			}
			a.TestamentID = t.ID
			a.SessionID = b.sessionID
			a.PipelineID = b.pipelineID
			a.TaskID = b.taskID
			a.Sequence = b.nextSeq()
			a.Created = now
			a.Accessed = now
		}

		b.testaments[t.ID] = t

		// Transition the referenced claim to testified.
		claimRel := FindRelation(t.Relations, RelationshipClaim)
		if claimRel != nil {
			if c, ok := b.claims[claimRel.Related]; ok && !c.Status.IsTerminal() && c.Status != ClaimStatusTestified {
				c.StatusHistory = append(c.StatusHistory, StatusChange{
					From: string(c.Status), To: string(ClaimStatusTestified),
					Reason: "testament submitted", AgentID: t.AgentID, Changed: now,
				})
				c.Status = ClaimStatusTestified
				c.Accessed = now
			}
		}
	}

	b.mu.Unlock()

	// Amplify.
	ampCtx := context.Background()
	for i := range testaments {
		b.amplifier.EmitTestamentSubmitted(ampCtx, &testaments[i])
		for _, a := range testaments[i].Artifacts {
			b.amplifier.EmitArtifactPublished(ampCtx, a)
		}
	}

	b.notifySubscribers()
	return nil
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

	var v *Validation
	for _, candidate := range c.Validations {
		if candidate.ID == validationID {
			v = candidate
			break
		}
	}
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
			From: string(c.Status), To: string(ClaimStatusAccepted),
			Reason: "all required validations passed", AgentID: change.AgentID, Changed: now,
		})
		c.Status = ClaimStatusAccepted
		c.Accessed = now
	}

	b.mu.Unlock()

	ampCtx := context.Background()
	b.amplifier.EmitClaimValidated(ampCtx, v, change.AgentID)
	if accepted {
		b.amplifier.EmitClaimAccepted(ampCtx, c)
	}

	b.notifySubscribers()
	return nil
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
	change.From = string(c.Status)
	change.To = string(ClaimStatusRejected)
	change.Changed = now
	c.StatusHistory = append(c.StatusHistory, change)
	c.Status = ClaimStatusRejected
	c.Accessed = now

	if replacements != nil && len(replacementClaims) > 0 {
		if replacements.ID == "" {
			replacements.ID = uuid.NewString()
		}
		replacements.SessionID = b.sessionID
		replacements.PipelineID = b.pipelineID
		replacements.TaskID = b.taskID
		replacements.Sequence = b.nextSeq()
		replacements.Created = now
		replacements.Accessed = now
		if replacements.Status == "" {
			replacements.Status = ActionStatusPending
		}
		replacements.StatusHistory = append(replacements.StatusHistory, StatusChange{
			To: string(replacements.Status),
			Reason: "remediation for rejected claim " + claimID,
			AgentID: change.AgentID, Changed: now,
		})
		b.actions[replacements.ID] = replacements

		for i := range replacementClaims {
			rc := &replacementClaims[i]
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
				To: string(ClaimStatusPending),
				Reason: "remediation for rejected claim " + claimID,
				AgentID: change.AgentID, Changed: now,
			})
			if !HasRelation(rc.Relations, RelationshipSupersedes, claimID) {
				rc.Relations = append(rc.Relations, Relation{
					Related: claimID, RelatedType: RelatedTypeClaim,
					Relationship: RelationshipSupersedes,
				})
			}
			if !HasRelation(rc.Relations, RelationshipClaimAction, replacements.ID) {
				rc.Relations = append(rc.Relations, Relation{
					Related: replacements.ID, RelatedType: RelatedTypeAction,
					Relationship: RelationshipClaimAction,
				})
			}
			// Stamp validations on replacement claims.
			for _, v := range rc.Validations {
				if v.ID == "" {
					v.ID = uuid.NewString()
				}
				v.ClaimID = rc.ID
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
			b.claims[rc.ID] = rc
			b.claimOrder = append(b.claimOrder, rc.ID)
		}
	}

	b.mu.Unlock()

	b.amplifier.EmitClaimRejected(context.Background(), c)
	b.notifySubscribers()
	return nil
}

// ── Phase Transitions ───────────────────────────────────────────────

func (b *ClaimsBoard) TransitionToValidation(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseImplementation {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition to validation from %s", b.phase)
	}
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusTestified && c.Status != ClaimStatusAccepted {
			b.mu.Unlock()
			return fmt.Errorf("claim %q has status %s, expected testified or accepted", c.ID, c.Status)
		}
	}
	b.phase = BoardPhaseValidation
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()
	b.amplifier.EmitBoardPhaseChanged(context.Background(), phase, iteration, "")
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) TransitionToImplementation(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseValidation {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition to implementation from %s", b.phase)
	}
	hasPending := false
	for _, c := range b.claims {
		if c.Status == ClaimStatusPending {
			hasPending = true
			break
		}
	}
	if !hasPending {
		b.mu.Unlock()
		return fmt.Errorf("no pending claims exist for re-entry to implementation")
	}
	if b.iteration >= b.maxIterations {
		b.mu.Unlock()
		return fmt.Errorf("max iterations (%d) reached, cannot re-enter implementation", b.maxIterations)
	}
	b.iteration++
	b.phase = BoardPhaseImplementation
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()
	b.amplifier.EmitBoardPhaseChanged(context.Background(), phase, iteration, "")
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) MarkComplete(_ context.Context) error {
	b.mu.Lock()
	if b.phase != BoardPhaseValidation {
		b.mu.Unlock()
		return fmt.Errorf("cannot mark complete from %s", b.phase)
	}
	for _, c := range b.claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if c.Status != ClaimStatusAccepted {
			b.mu.Unlock()
			return fmt.Errorf("claim %q has status %s, expected accepted", c.ID, c.Status)
		}
	}
	b.phase = BoardPhaseComplete
	b.mu.Unlock()
	b.amplifier.EmitBoardComplete(context.Background(), "")
	b.notifySubscribers()
	return nil
}

// ── Queries ─────────────────────────────────────────────────────────

func (b *ClaimsBoard) Projection() *ClaimsBoardProjection {
	b.mu.RLock()
	p := b.projectionLocked()
	b.mu.RUnlock()
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

	for _, id := range b.claimOrder {
		c, ok := b.claims[id]
		if !ok {
			continue
		}
		clone := *c
		p.Claims = append(p.Claims, clone)
		p.TotalClaims++
		switch c.Status {
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
		for _, v := range c.Validations {
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

	for _, a := range b.actions {
		clone := *a
		p.Actions = append(p.Actions, clone)
		p.TotalClaimActions++
	}

	for _, t := range b.testaments {
		clone := *t
		p.Testaments = append(p.Testaments, clone)
		p.TotalTestaments++
		p.TotalArtifacts += len(t.Artifacts)
	}

	return p
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
// Subscriber errors are logged but do not block the mutation.
// Panics are NOT recovered — they are bugs.
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
		fn := fn // capture for goroutine
		if b.scope != nil {
			if err := b.scope.Go("claims_board_notify", 5*time.Second, func(_ context.Context) error {
				return fn(proj)
			}); err != nil {
				slog.Error("claims_board_notify_dispatch_failed",
					"board_id", b.boardID,
					"error", err.Error(),
				)
			}
		} else {
			// Synchronous fallback (tests, standalone).
			if err := fn(proj); err != nil {
				slog.Error("claims_board_subscriber_error",
					"board_id", b.boardID,
					"error", err.Error(),
				)
			}
		}
	}
}

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
