package claims

import (
	"context"
	"fmt"
	"sort"
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

	boardID       string
	pipelineID    string
	taskID        string
	sessionID     string
	sessionDir    string
	parentBoardID string

	phase         BoardPhase
	iteration     int
	maxIterations int

	actions                   map[string]*Action
	claims                    map[string]*Claim
	testaments                map[string]*Testament
	claimOrder                []string
	phaseLog                  []StatusChange
	claimGenerationKeys       map[string]string
	singleClaimGenerationKeys map[string]string
	testamentGenerationKeys   map[string]string

	// relationsIdx is a secondary index over every Relation carried
	// by every object. Serves the named board-context queries
	// (trace_claim_ancestry, list_action_claims, find_overlapping_claims,
	// etc.) in O(1) lookup instead of full-table scans.
	relationsIdx *relationsIndex

	seq atomic.Uint64

	amplifier        *BoardAmplifier
	scope            ScopeProvider // nil = synchronous (tests)
	agentRefResolver AgentRefResolver
	claimPostPolicy  ClaimPostPolicy

	// Cached projection: recomputed only when projectionDirty is set.
	// Multiple readers share the same immutable pointer.
	cachedProjection atomic.Pointer[ClaimsBoardProjection]
	projectionDirty  atomic.Bool

	subscribersMu      sync.Mutex
	subscribers        []boardSubscription
	subscriberSeq      int64
	deltaSubscribers   []boardDeltaSubscription
	deltaSubscriberSeq int64

	// Atomic summary counters — updated inline on every mutation,
	// readable without lock for the query_board op=summary path.
	countTotal      atomic.Int64
	countPending    atomic.Int64
	countInProgress atomic.Int64
	countTestified  atomic.Int64
	countAccepted   atomic.Int64
	countRejected   atomic.Int64

	// notificationErrors accumulates subscriber notification + emission
	// failures. Exposed in the projection so agents see them on the
	// next board query and can record them as testament error
	// artifacts. Drained on read (projection) to prevent unbounded
	// growth.
	notificationErrors []string
	projectionErrors   map[string]string

	legacySessionNoWAL bool
	rollout            RolloutConfig
	canonicalViaOutbox bool
	durable            *DurableBoard
}

type boardSubscription struct {
	id int64
	fn ClaimsBoardSubscriber
}

type boardDeltaSubscription struct {
	id int64
	fn BoardDeltaSubscriber
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
		boardID:                   boardID,
		pipelineID:                cfg.PipelineID,
		taskID:                    cfg.TaskID,
		sessionID:                 cfg.SessionID,
		sessionDir:                cfg.SessionDir,
		parentBoardID:             cfg.ParentBoardID,
		phase:                     BoardPhaseImplementation,
		maxIterations:             maxIter,
		actions:                   make(map[string]*Action),
		claims:                    make(map[string]*Claim),
		testaments:                make(map[string]*Testament),
		claimGenerationKeys:       make(map[string]string),
		singleClaimGenerationKeys: make(map[string]string),
		testamentGenerationKeys:   make(map[string]string),
		relationsIdx:              newRelationsIndex(),
		projectionErrors:          make(map[string]string),
		scope:                     cfg.Scope,
		agentRefResolver:          cfg.AgentRefResolver,
		claimPostPolicy:           cfg.ClaimPostPolicy,
		legacySessionNoWAL:        cfg.LegacySessionNoWAL,
		rollout:                   boardRolloutConfig(cfg.Rollout),
	}
	if cfg.SessionID != "" {
		amp := NewBoardAmplifier(cfg.SessionID, cfg.TaskID, boardID).
			WithDeltaBus(cfg.DeltaBus).
			WithAgentRefResolver(cfg.AgentRefResolver).
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

func (b *ClaimsBoard) SessionDir() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionDir
}

func (b *ClaimsBoard) LegacySessionNoWAL() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.legacySessionNoWAL
}

func (b *ClaimsBoard) RolloutConfig() RolloutConfig {
	if b == nil {
		return CurrentRolloutConfig()
	}
	b.mu.RLock()
	cfg := b.rollout
	b.mu.RUnlock()
	return cfg.Normalized()
}

// ParentBoardID returns the parent board's ID (empty for root boards).
func (b *ClaimsBoard) ParentBoardID() string {
	return b.parentBoardID
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

// HighWaterSequence returns the latest committed board sequence. It is
// the deterministic scan boundary used by carry-forward cursors.
func (b *ClaimsBoard) HighWaterSequence() uint64 {
	if b == nil {
		return 0
	}
	return b.seq.Load()
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
//
// ctx is honored: amplifier emissions (which publish to the delta
// bus and may block on a slow consumer) propagate ctx so a caller's
// cancellation aborts the publish chain instead of leaving the
// caller's worker goroutine pinned in board IO. Pre-cancellation
// returns ctx.Err() before any mutation; post-mutation cancellation
// short-circuits the amplifier emissions but the in-memory claim
// state is already committed (consistent with append-only semantics).
func (b *ClaimsBoard) PostAction(ctx context.Context, action Action, inputClaims []Claim) error {
	if len(inputClaims) == 0 {
		return fmt.Errorf("action must contain at least one claim")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()

	now := time.Now().UTC()

	if err := b.validatePostActionLocked(action.Type, inputClaims); err != nil {
		b.mu.Unlock()
		return err
	}

	// Handoff precondition guard (UI_DESIGN.md §4.3 + §7 P2.3): when
	// the action is a handoff, the issuing agent must have no open
	// child work, must not currently be the subject of a peer's open
	// claim, and must have no in-flight tool/peer-interaction
	// artifacts. Belt-and-suspenders against an agent posting a
	// handoff action that bypasses the skill-side check.
	if action.Type == ActionTypeHandoff {
		if err := b.handoffEligibleLocked(strings.TrimSpace(action.AgentID)); err != nil {
			b.mu.Unlock()
			return err
		}
	}

	prevSeq := b.seq.Load()
	b.stampActionLocked(&action, now)

	for i := range inputClaims {
		c := &inputClaims[i]
		b.stampClaimLocked(c, &action, now)
	}

	outboxRecords := b.outboxRecordsForPostActionLocked(action, inputClaims, now)
	if err := b.appendDurableEventLocked(walEventActionPosted, action.AgentID, map[string]any{
		"action": action, "claims": inputClaims,
	}, outboxRecords); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}

	b.actions[action.ID] = &action
	b.indexRelations(action.ID, action.Relations)
	for i := range inputClaims {
		c := &inputClaims[i]
		b.claims[c.ID] = c
		b.claimOrder = append(b.claimOrder, c.ID)
		b.indexRelations(c.ID, c.Relations)
		b.relationsIdx.addScope(c.ID, c.Scope)
		b.countTotal.Add(1)
		b.countPending.Add(1)
	}

	// Release lock BEFORE notifying subscribers (prevents deadlock).
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	// Amplify: fabric + bus. Threaded ctx so cancellation aborts the
	// emission chain rather than leaving callers blocked on bus IO.
	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitActionPosted(ctx, &action)
	}
	for i := range inputClaims {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.shouldEmitFabricDirect() {
			b.amplifier.EmitClaimIssued(ctx, &inputClaims[i])
		}
		b.amplifier.PublishInboxDeltas(ctx, &action, &inputClaims[i])
		b.notifyDelta(BoardMutationDelta{
			Kind:    "claim_created",
			ClaimID: inputClaims[i].ID,
			AgentID: SubjectAgentID(inputClaims[i].Relations),
		})
	}

	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) validatePostActionLocked(actionType ActionType, inputClaims []Claim) error {
	for i := range inputClaims {
		id := inputClaims[i].ID
		claimActionType := inputClaims[i].ActionType
		if claimActionType == "" {
			claimActionType = actionType
		}
		if isPeerDirectedActionType(claimActionType) && claimHasSelfIssuerSubject(inputClaims[i].Relations) {
			return fmt.Errorf("claim %q invalid self-target for peer-directed action %q", firstNonEmpty(id, inputClaims[i].Title), claimActionType)
		}
		if err := ValidateExpectedToolCalls(inputClaims[i].ExpectedToolCalls, nil); err != nil {
			return fmt.Errorf("claim %q expected tool calls: %w", firstNonEmpty(id, inputClaims[i].Title), err)
		}
		for _, validation := range inputClaims[i].Validations {
			if validation == nil {
				continue
			}
			if err := ValidateExpectedToolCalls(validation.ExpectedToolCalls, nil); err != nil {
				return fmt.Errorf("claim %q validation %q expected tool calls: %w", firstNonEmpty(id, inputClaims[i].Title), firstNonEmpty(validation.ID, validation.Description), err)
			}
		}
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

func isPeerDirectedActionType(actionType ActionType) bool {
	switch actionType {
	case ActionTypeConsultation, ActionTypeChallenge, ActionTypeGuardianCheck:
		return true
	default:
		return false
	}
}

func claimHasSelfIssuerSubject(relations []Relation) bool {
	issuer := strings.TrimSpace(IssuerAgentID(relations))
	subject := strings.TrimSpace(SubjectAgentID(relations))
	return issuer != "" && subject != "" && issuer == subject
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
	c.ExpectedToolCalls = stampExpectedToolCalls(c.ExpectedToolCalls)
	c.LifecycleStatus = ClaimLifecyclePosted
	c.LifecycleHistory = append(c.LifecycleHistory,
		StatusChange{To: string(ClaimLifecycleGenerated), Reason: "claim generated", AgentID: action.AgentID, Changed: now},
		StatusChange{From: string(ClaimLifecycleGenerated), To: string(ClaimLifecyclePosted), Reason: "claim posted for action", AgentID: action.AgentID, Changed: now},
	)
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
		v.ExpectedToolCalls = stampExpectedToolCalls(v.ExpectedToolCalls)
	}
}

func (b *ClaimsBoard) indexRelations(objID string, relations []Relation) {
	b.relationsIdx.addRelations(objID, relations)
}

// ── UpdateClaimProgress ─────────────────────────────────────────────

func (b *ClaimsBoard) UpdateClaimProgress(ctx context.Context, claimID string, update ClaimProgressUpdate, agentID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	outboxRecords := []ClaimsOutboxRecord{b.outboxRecordLocked(c.Sequence, "claim", c.ID, walEventClaimUpdated, now)}
	if err := b.appendDurableEventLocked(walEventClaimUpdated, agentID, map[string]any{
		"claim_id": claimID, "agent_id": agentID, "accessed": now,
		"from_status": fromStatus,
	}, outboxRecords); err != nil {
		b.mu.Unlock()
		return err
	}
	c.Accessed = now
	if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleProgressed) {
		b.transitionClaimLifecycleLocked(c, ClaimLifecycleProgressed, agentID, "work progressed", now)
	}
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
		b.adjustStatusCounter(ClaimStatusPending, ClaimStatusInProgress)
	}
	claimSnapshot := CloneClaimEntity(c)

	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitClaimUpdated(ctx, c, agentID)
	}
	if statusChanged {
		b.amplifier.PublishClaimStatusDelta(ctx, ClaimStatusDelta{
			SessionID:      b.sessionID,
			BoardID:        b.boardID,
			ClaimID:        c.ID,
			Sequence:       b.seq.Load(),
			EmittedAt:      now,
			ActionKind:     c.ActionType,
			FromStatus:     fromStatus,
			ToStatus:       c.Status,
			Reason:         "work started",
			AgentID:        agentID,
			SubjectAgentID: SubjectAgentID(claimSnapshot.Relations),
			IssuerAgentID:  IssuerAgentID(claimSnapshot.Relations),
		})
	}
	progressMessage := firstNonEmpty(update.WorkSummary, "work started")
	b.amplifier.PublishCanonicalClaimProgressed(ctx, claimSnapshot, agentID, string(claimSnapshot.Status), progressMessage, claimSnapshot.ContextTransition, now)
	b.notifySubscribers()
	return nil
}

// ── SetClaimContext / SetTestamentContext ──────────────────────────

// SetClaimContext updates a claim's mutable Context narrative — the
// agent's current "what am I doing right now" status. Replaces the
// prior value in place. Increments the claim's monotonic
// ContextTransition counter for deterministic UI ordering. Emits a
// ClaimContextDelta on the amplifier; the UI consumes this to refresh
// the row's status text without creating a new row.
//
// Best-effort on terminal claims: if the claim is terminal,
// SetClaimContext returns nil without mutating — the narrative is
// sealed at terminal-status transition, and subsequent updates from
// late-arriving emissions are dropped silently. See docs/CLAIMS_UI.md.
func (b *ClaimsBoard) SetClaimContext(ctx context.Context, claimID, value string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return fmt.Errorf("SetClaimContext: empty claimID")
	}

	b.mu.Lock()
	c, ok := b.claims[claimID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("SetClaimContext: claim %q not found", claimID)
	}
	if c.Status.IsTerminal() {
		// Sealed. Drop silently — late narration emission is benign.
		b.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	transitionID := c.ContextTransition + 1
	if err := b.appendDurableEventLocked(walEventClaimContextSet, IssuerAgentID(c.Relations), map[string]any{
		"claim_id": claimID, "context": value, "transition_id": transitionID, "accessed": now,
	}, []ClaimsOutboxRecord{b.outboxRecordLocked(c.Sequence, "claim", c.ID, walEventClaimUpdated, now)}); err != nil {
		b.mu.Unlock()
		return err
	}
	c.Context = value
	c.ContextTransition = transitionID
	c.Accessed = now
	actionKind := c.ActionType
	owner := IssuerAgentID(c.Relations)
	subject := SubjectAgentID(c.Relations)
	b.invalidateProjectionCache()
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	b.amplifier.PublishClaimContextDelta(ctx, ClaimContextDelta{
		SessionID:      b.sessionID,
		BoardID:        b.boardID,
		ClaimID:        claimID,
		Sequence:       b.seq.Load(),
		EmittedAt:      now,
		TransitionID:   transitionID,
		Context:        value,
		ActionKind:     actionKind,
		OwnerAgentID:   owner,
		SubjectAgentID: subject,
		IssuerAgentID:  owner,
	})

	// Notify BoardMutationDelta subscribers (UI bridge) so the agent /
	// chat panels can refresh row status text from Context. Skipped for
	// system-internal action types — same filter the amplifier uses for
	// its per-claim topic.
	if !IsSystemInternalAction(actionKind) {
		b.notifyDelta(BoardMutationDelta{
			Kind:              "claim_context_changed",
			ClaimID:           claimID,
			AgentID:           owner,
			Context:           value,
			ContextTransition: transitionID,
		})
	}
	return nil
}

// SetTestamentContext updates a submitted testament's Context. Used
// rarely — the typical narration path is via the in-flight
// accumulator's SetContext, which seals onto the testament at flush.
// This method covers the corner case where a post-flush correction
// or supersession needs to update the testament's recorded
// conclusion.
func (b *ClaimsBoard) SetTestamentContext(ctx context.Context, testamentID, value string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	testamentID = strings.TrimSpace(testamentID)
	if testamentID == "" {
		return fmt.Errorf("SetTestamentContext: empty testamentID")
	}

	b.mu.Lock()
	t, ok := b.testaments[testamentID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("SetTestamentContext: testament %q not found", testamentID)
	}
	now := time.Now().UTC()
	transitionID := t.ContextTransition + 1
	if err := b.appendDurableEventLocked(walEventTestamentContextSet, t.AgentID, map[string]any{
		"testament_id": testamentID, "context": value, "transition_id": transitionID, "accessed": now,
	}, []ClaimsOutboxRecord{b.outboxRecordLocked(t.Sequence, "testament", t.ID, walEventTestamentSubmitted, now)}); err != nil {
		b.mu.Unlock()
		return err
	}
	t.Context = value
	t.ContextTransition = transitionID
	t.Accessed = now
	agentID := t.AgentID
	claimID := ClaimIDFromRelations(t.Relations)
	b.invalidateProjectionCache()
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	b.amplifier.PublishTestamentContextDelta(ctx, TestamentContextDelta{
		SessionID:    b.sessionID,
		BoardID:      b.boardID,
		TestamentID:  testamentID,
		Sequence:     b.seq.Load(),
		EmittedAt:    now,
		TransitionID: transitionID,
		Context:      value,
		AgentID:      agentID,
		ClaimID:      claimID,
	})

	// Notify BoardMutationDelta subscribers (UI bridge) so the agent /
	// chat panels can update an in-flight testament row's status text.
	b.notifyDelta(BoardMutationDelta{
		Kind:              "testament_context_changed",
		ClaimID:           claimID,
		TestamentID:       testamentID,
		AgentID:           agentID,
		Context:           value,
		ContextTransition: transitionID,
	})
	return nil
}

// ── SubmitTestaments ────────────────────────────────────────────────

// SubmitTestaments records testaments with their artifacts. Each
// testament's Artifacts field carries the proof. Artifacts get stamped
// with TestamentID. Claims transition to testified.
//
// ctx is honored on the amplifier emission chain: a caller's
// cancellation aborts the publish loop instead of leaving the worker
// blocked on bus IO.
func (b *ClaimsBoard) SubmitTestaments(ctx context.Context, action Action, testaments []Testament) error {
	if len(testaments) == 0 {
		return fmt.Errorf("testament action must contain at least one testament")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	now := time.Now().UTC()
	prevSeq := b.seq.Load()
	b.stampTestamentActionLocked(&action, now)

	// Capture post-stamp claim snapshots so the amplifier gets
	// authoritative references once the lock is released. We resolve
	// the claim referenced by each testament BEFORE unlocking.
	resolutions := make([]claimResolution, len(testaments))
	for i := range testaments {
		b.stampTestamentLocked(&testaments[i], &action, now)
		b.transitionTestamentLifecycleLocked(&testaments[i], TestamentLifecycleGenerated, action.AgentID, "testament generated", now)
		b.transitionTestamentLifecycleLocked(&testaments[i], TestamentLifecyclePosted, action.AgentID, "testament posted", now)
	}

	outboxRecords := b.outboxRecordsForSubmitTestamentsLocked(action, testaments, now)
	if err := b.appendDurableEventLocked(walEventTestamentSubmitted, action.AgentID, map[string]any{
		"action": action, "testaments": testaments,
	}, outboxRecords); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}

	b.actions[action.ID] = &action
	b.indexRelations(action.ID, action.Relations)
	for i := range testaments {
		b.testaments[testaments[i].ID] = &testaments[i]
		b.indexRelations(testaments[i].ID, testaments[i].Relations)
		for _, artifact := range testaments[i].Artifacts {
			if artifact != nil {
				b.indexRelations(artifact.ID, artifact.Relations)
			}
		}
		resolutions[i] = b.resolveClaimForTestamentLocked(&testaments[i], now)
	}

	b.invalidateProjectionCache()
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	// Amplify, threading ctx so cancellation aborts the chain.
	for i := range testaments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.shouldEmitFabricDirect() {
			b.amplifier.EmitTestamentSubmitted(ctx, &testaments[i])
			for _, artifact := range testaments[i].Artifacts {
				b.amplifier.EmitArtifactPublished(ctx, artifact)
			}
		}
		if resolutions[i].claim != nil {
			b.amplifier.PublishTestamentDelta(ctx, &testaments[i], resolutions[i].claim)
			for _, validation := range resolutions[i].validations {
				change := StatusChange{
					From:    validation.from,
					To:      validation.to,
					Reason:  validation.reason,
					AgentID: validation.agentID,
					Changed: validation.changed,
				}
				b.amplifier.PublishCanonicalValidationEvaluated(ctx, resolutions[i].claim, validation.validation, change, resolutions[i].claim.Status == ClaimStatusAccepted, validation.changed)
			}
			for _, transition := range resolutions[i].transitions {
				b.amplifier.PublishClaimStatusDelta(ctx, ClaimStatusDelta{
					SessionID:      b.sessionID,
					BoardID:        b.boardID,
					ClaimID:        resolutions[i].claim.ID,
					Sequence:       resolutions[i].claim.Sequence,
					EmittedAt:      transition.changed,
					ActionKind:     resolutions[i].claim.ActionType,
					FromStatus:     transition.from,
					ToStatus:       transition.to,
					Reason:         transition.reason,
					AgentID:        transition.agentID,
					SubjectAgentID: SubjectAgentID(resolutions[i].claim.Relations),
					IssuerAgentID:  IssuerAgentID(resolutions[i].claim.Relations),
				})
			}
		}
		b.notifyDelta(BoardMutationDelta{
			Kind:        "testament_submitted",
			TestamentID: testaments[i].ID,
			ClaimID:     claimIDFromTestament(&testaments[i]),
			AgentID:     testaments[i].AgentID,
		})
	}

	b.notifySubscribers()
	return nil
}

func claimIDFromTestament(t *Testament) string {
	if r := FindRelation(t.Relations, RelationshipClaim); r != nil {
		return r.Related
	}
	return ""
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
	t.Presentation = NormalizePresentation(t.Presentation)

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
		ApplyDefaultArtifactPresentation(artifact)
		artifact.Presentation = NormalizePresentation(artifact.Presentation)
	}
}

type claimResolution struct {
	claim       *Claim
	transitions []claimStatusTransition
	validations []validationStatusTransition
}

type claimStatusTransition struct {
	from    ClaimStatus
	to      ClaimStatus
	reason  string
	agentID string
	changed time.Time
}

type validationStatusTransition struct {
	validation *Validation
	from       string
	to         string
	reason     string
	agentID    string
	changed    time.Time
}

// resolveClaimForTestamentLocked returns the Claim referenced by the
// testament's "claim" Relation and transitions it to Testified. Empty
// when the relation is absent or the target claim no longer exists.
// Caller holds b.mu (write-locked).
func (b *ClaimsBoard) resolveClaimForTestamentLocked(t *Testament, now time.Time) claimResolution {
	claimRel := FindRelation(t.Relations, RelationshipClaim)
	if claimRel == nil {
		return claimResolution{}
	}
	c, ok := b.claims[claimRel.Related]
	if !ok {
		return claimResolution{}
	}
	var result claimResolution
	if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleTestamentGenerated) {
		b.transitionClaimLifecycleLocked(c, ClaimLifecycleTestamentGenerated, t.AgentID, "testament generated", now)
	}
	if !c.Status.IsTerminal() && c.Status != ClaimStatusTestified {
		prevStatus := c.Status
		transition := claimStatusTransition{
			from:    prevStatus,
			to:      ClaimStatusTestified,
			reason:  "testament submitted",
			agentID: t.AgentID,
			changed: now,
		}
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(c.Status),
			To:      string(ClaimStatusTestified),
			Reason:  transition.reason,
			AgentID: t.AgentID,
			Changed: now,
		})
		c.Status = ClaimStatusTestified
		c.Accessed = now
		b.adjustStatusCounter(prevStatus, ClaimStatusTestified)
		result.transitions = append(result.transitions, transition)
	}

	if DeriveTestamentVerdict(t.Artifacts) == TestamentVerdictError {
		result.validations = append(result.validations, failReceiptValidationsLocked(c, t.AgentID, now, "receipt failed: error testament submitted")...)
		if claimHasRequiredFailedValidation(c) && !c.Status.IsTerminal() {
			prevStatus := c.Status
			transition := claimStatusTransition{
				from:    prevStatus,
				to:      ClaimStatusRejected,
				reason:  "required receipt validation failed on error testament",
				agentID: t.AgentID,
				changed: now,
			}
			c.StatusHistory = append(c.StatusHistory, StatusChange{
				From:    string(prevStatus),
				To:      string(ClaimStatusRejected),
				Reason:  transition.reason,
				AgentID: t.AgentID,
				Changed: now,
			})
			c.Status = ClaimStatusRejected
			c.Accessed = now
			b.adjustStatusCounter(prevStatus, ClaimStatusRejected)
			if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleValidationFailed) {
				b.transitionClaimLifecycleLocked(c, ClaimLifecycleValidationFailed, t.AgentID, "required receipt validation failed on error testament", now)
			}
			result.transitions = append(result.transitions, transition)
		}
	} else {
		// Auto-pass receipt validations: a non-error testament arriving
		// IS the proof. Error testaments remain evidence, but they do not
		// satisfy receipt gates.
		result.validations = append(result.validations, autoPassReceiptValidationsLocked(c, t.AgentID, now)...)
		if c.AllValidationsPassed() && c.Status == ClaimStatusTestified {
			transition := claimStatusTransition{
				from:    c.Status,
				to:      ClaimStatusAccepted,
				reason:  "all receipt validations auto-passed on testament",
				agentID: t.AgentID,
				changed: now,
			}
			c.StatusHistory = append(c.StatusHistory, StatusChange{
				From:    string(c.Status),
				To:      string(ClaimStatusAccepted),
				Reason:  transition.reason,
				AgentID: t.AgentID,
				Changed: now,
			})
			b.adjustStatusCounter(ClaimStatusTestified, ClaimStatusAccepted)
			c.Status = ClaimStatusAccepted
			c.Accessed = now
			if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleSatisfied) {
				b.transitionClaimLifecycleLocked(c, ClaimLifecycleSatisfied, t.AgentID, "all required validations passed", now)
			}
			result.transitions = append(result.transitions, transition)
		}
	}

	result.claim = CloneClaimEntity(c)
	return result
}

// autoPassReceiptValidationsLocked passes all pending receipt-type
// validations on a claim. Receipt validations assert "testimony was
// delivered" — the testament existing is sufficient proof.
func autoPassReceiptValidationsLocked(c *Claim, agentID string, now time.Time) []validationStatusTransition {
	var changed []validationStatusTransition
	for _, v := range c.Validations {
		if v == nil || v.Type != ValidationTypeReceipt || v.Status != ValidationStatusPending {
			continue
		}
		from := v.Status
		v.StatusHistory = append(v.StatusHistory, StatusChange{
			From:    string(v.Status),
			To:      string(ValidationStatusPassed),
			Reason:  "receipt auto-passed: testament submitted",
			AgentID: agentID,
			Changed: now,
		})
		v.Status = ValidationStatusPassed
		v.Accessed = now
		clone := *v
		if len(v.ExpectedToolCalls) > 0 {
			clone.ExpectedToolCalls = append([]ExpectedToolCall(nil), v.ExpectedToolCalls...)
		}
		changed = append(changed, validationStatusTransition{
			validation: &clone,
			from:       string(from),
			to:         string(ValidationStatusPassed),
			reason:     "receipt auto-passed: testament submitted",
			agentID:    agentID,
			changed:    now,
		})
	}
	return changed
}

func failReceiptValidationsLocked(c *Claim, agentID string, now time.Time, reason string) []validationStatusTransition {
	var changed []validationStatusTransition
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "receipt failed"
	}
	for _, v := range c.Validations {
		if v == nil || v.Type != ValidationTypeReceipt || v.Status != ValidationStatusPending {
			continue
		}
		from := v.Status
		v.StatusHistory = append(v.StatusHistory, StatusChange{
			From:    string(v.Status),
			To:      string(ValidationStatusFailed),
			Reason:  reason,
			AgentID: agentID,
			Changed: now,
		})
		v.Status = ValidationStatusFailed
		v.Accessed = now
		clone := *v
		if len(v.ExpectedToolCalls) > 0 {
			clone.ExpectedToolCalls = append([]ExpectedToolCall(nil), v.ExpectedToolCalls...)
		}
		changed = append(changed, validationStatusTransition{
			validation: &clone,
			from:       string(from),
			to:         string(ValidationStatusFailed),
			reason:     reason,
			agentID:    agentID,
			changed:    now,
		})
	}
	return changed
}

func claimHasRequiredFailedValidation(c *Claim) bool {
	if c == nil {
		return false
	}
	for _, v := range c.Validations {
		if v != nil && v.Required && v.Status == ValidationStatusFailed {
			return true
		}
	}
	return false
}

// ── EvaluateValidation ──────────────────────────────────────────────

// EvaluateValidation transitions a validation on a specific claim. If
// all required validations on the claim pass, the claim auto-accepts.
// ctx is threaded into amplifier emissions so cancellation aborts the
// publish chain instead of pinning the caller on bus IO.
func (b *ClaimsBoard) EvaluateValidation(ctx context.Context, claimID, validationID string, change StatusChange) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	toStatus := ValidationStatus(change.To)
	if v.Status.IsTerminal() {
		if v.Status == toStatus {
			b.mu.Unlock()
			return nil
		}
		b.mu.Unlock()
		return fmt.Errorf("validation %q on claim %q is already terminal (%s)", validationID, claimID, v.Status)
	}
	accepted := claimAcceptedAfterValidation(c, validationID, toStatus)
	acceptedFromStatus := c.Status
	outboxRecords := []ClaimsOutboxRecord{
		b.outboxRecordLocked(v.Sequence, "validation", v.ID, walEventValidationEvaluated, now),
	}
	if accepted {
		outboxRecords = append(outboxRecords, b.outboxRecordLocked(c.Sequence, "claim", c.ID, walEventClaimAccepted, now))
	}
	if err := b.appendDurableEventLocked(walEventValidationEvaluated, change.AgentID, map[string]any{
		"claim_id": claimID, "validation_id": validationID, "status": change.To, "change": change,
	}, outboxRecords); err != nil {
		b.mu.Unlock()
		return err
	}
	v.StatusHistory = append(v.StatusHistory, change)
	v.Status = toStatus
	v.Accessed = now
	if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleValidating) {
		b.transitionClaimLifecycleLocked(c, ClaimLifecycleValidating, change.AgentID, "validation started", now)
	}

	if accepted {
		prevStatus := c.Status
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(c.Status),
			To:      string(ClaimStatusAccepted),
			Reason:  "all required validations passed",
			AgentID: change.AgentID,
			Changed: now,
		})
		c.Status = ClaimStatusAccepted
		c.Accessed = now
		b.adjustStatusCounter(prevStatus, ClaimStatusAccepted)
		if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleSatisfied) {
			b.transitionClaimLifecycleLocked(c, ClaimLifecycleSatisfied, change.AgentID, "all required validations passed", now)
		}
	}

	claimSnapshot := CloneClaimEntity(c)
	validationSnapshot := *v
	if len(v.ExpectedToolCalls) > 0 {
		validationSnapshot.ExpectedToolCalls = append([]ExpectedToolCall(nil), v.ExpectedToolCalls...)
	}
	var acceptedTransition *claimStatusTransition
	if accepted {
		acceptedTransition = &claimStatusTransition{
			from:    acceptedFromStatus,
			to:      ClaimStatusAccepted,
			reason:  "all required validations passed",
			agentID: change.AgentID,
			changed: now,
		}
	}

	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitClaimValidated(ctx, v, change.AgentID)
		if accepted {
			b.amplifier.EmitClaimAccepted(ctx, c)
		}
	}
	b.amplifier.PublishCanonicalValidationEvaluated(ctx, claimSnapshot, &validationSnapshot, change, accepted, now)
	if acceptedTransition != nil {
		b.amplifier.PublishClaimStatusDelta(ctx, ClaimStatusDelta{
			SessionID:      b.sessionID,
			BoardID:        b.boardID,
			ClaimID:        claimID,
			Sequence:       claimSnapshot.Sequence,
			EmittedAt:      now,
			ActionKind:     claimSnapshot.ActionType,
			FromStatus:     acceptedTransition.from,
			ToStatus:       acceptedTransition.to,
			Reason:         acceptedTransition.reason,
			AgentID:        acceptedTransition.agentID,
			SubjectAgentID: SubjectAgentID(claimSnapshot.Relations),
			IssuerAgentID:  IssuerAgentID(claimSnapshot.Relations),
		})
	}
	claimToStatus := ClaimStatus(change.To)
	if accepted {
		claimToStatus = ClaimStatusAccepted
	}
	b.notifyDelta(BoardMutationDelta{
		Kind:       "validation_evaluated",
		ClaimID:    claimID,
		FromStatus: ClaimStatus(change.From),
		ToStatus:   claimToStatus,
		AgentID:    change.AgentID,
	})
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

// ── RejectClaim ─────────────────────────────────────────────────────

func (b *ClaimsBoard) RejectClaim(ctx context.Context, claimID string, change StatusChange, replacements *Action, replacementClaims []Claim) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if replacements != nil && len(replacementClaims) > 0 {
		if err := b.validatePostActionLocked(ActionTypeCorrective, replacementClaims); err != nil {
			b.mu.Unlock()
			return err
		}
	}
	prevSeq := b.seq.Load()
	remediationIDs := b.prepareRemediationLocked(claimID, replacements, replacementClaims, change.AgentID, now)
	outboxRecords := []ClaimsOutboxRecord{
		b.outboxRecordLocked(c.Sequence, "claim", c.ID, walEventClaimRejected, now),
	}
	if replacements != nil && len(replacementClaims) > 0 {
		outboxRecords = append(outboxRecords, b.outboxRecordLocked(replacements.Sequence, "action", replacements.ID, walEventActionPosted, now))
		for i := range replacementClaims {
			outboxRecords = append(outboxRecords, b.outboxRecordLocked(replacementClaims[i].Sequence, "claim", replacementClaims[i].ID, "claim_issued", now))
		}
	}
	if err := b.appendDurableEventLocked(walEventClaimRejected, change.AgentID, map[string]any{
		"claim_id": claimID, "change": change, "action": replacements, "claims": replacementClaims,
	}, outboxRecords); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	c.StatusHistory = append(c.StatusHistory, change)
	c.Status = ClaimStatusRejected
	c.Accessed = now
	if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleValidationFailed) {
		b.transitionClaimLifecycleLocked(c, ClaimLifecycleValidationFailed, change.AgentID, change.Reason, now)
	}
	b.adjustStatusCounter(fromStatus, ClaimStatusRejected)

	b.applyPreparedRemediationLocked(replacements, replacementClaims)

	rejectedDelta := ClaimStatusDelta{
		SessionID:           b.sessionID,
		BoardID:             b.boardID,
		ClaimID:             c.ID,
		Sequence:            c.Sequence,
		EmittedAt:           now,
		ActionKind:          c.ActionType,
		FromStatus:          fromStatus,
		ToStatus:            ClaimStatusRejected,
		Reason:              change.Reason,
		AgentID:             change.AgentID,
		SubjectAgentID:      SubjectAgentID(c.Relations),
		IssuerAgentID:       IssuerAgentID(c.Relations),
		RemediationClaimIDs: remediationIDs,
	}

	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitClaimRejected(ctx, c)
	}
	b.amplifier.PublishClaimStatusDelta(ctx, rejectedDelta)

	// Emit InboxDeltas for each replacement claim so their subjects
	// see the remediation.
	if replacements != nil && len(replacementClaims) > 0 {
		if b.shouldEmitFabricDirect() {
			b.amplifier.EmitCorrectiveIssued(ctx, replacements)
		}
		for i := range replacementClaims {
			if b.shouldEmitFabricDirect() {
				b.amplifier.EmitClaimIssued(ctx, &replacementClaims[i])
			}
			b.amplifier.PublishInboxDeltas(ctx, replacements, &replacementClaims[i])
		}
	}

	b.notifyDelta(BoardMutationDelta{
		Kind:       "claim_rejected",
		ClaimID:    claimID,
		FromStatus: fromStatus,
		ToStatus:   ClaimStatusRejected,
		AgentID:    change.AgentID,
	})
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) prepareRemediationLocked(rejectedClaimID string, replacements *Action, replacementClaims []Claim, agentID string, now time.Time) []string {
	if replacements == nil || len(replacementClaims) == 0 {
		return nil
	}
	b.stampRemediationActionLocked(replacements, rejectedClaimID, agentID, now)
	ids := make([]string, 0, len(replacementClaims))
	for i := range replacementClaims {
		rc := &replacementClaims[i]
		b.stampRemediationClaimLocked(rc, replacements, rejectedClaimID, agentID, now)
		ids = append(ids, rc.ID)
	}
	return ids
}

func (b *ClaimsBoard) applyPreparedRemediationLocked(replacements *Action, replacementClaims []Claim) {
	if replacements == nil || len(replacementClaims) == 0 {
		return
	}
	b.actions[replacements.ID] = replacements
	b.indexRelations(replacements.ID, replacements.Relations)
	for i := range replacementClaims {
		rc := &replacementClaims[i]
		b.claims[rc.ID] = rc
		b.claimOrder = append(b.claimOrder, rc.ID)
		b.indexRelations(rc.ID, rc.Relations)
		b.relationsIdx.addScope(rc.ID, rc.Scope)
		b.countTotal.Add(1)
		b.countPending.Add(1)
	}
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
	rc.ExpectedToolCalls = stampExpectedToolCalls(rc.ExpectedToolCalls)
	rc.LifecycleStatus = ClaimLifecyclePosted
	rc.LifecycleHistory = append(rc.LifecycleHistory,
		StatusChange{To: string(ClaimLifecycleGenerated), Reason: "remediation claim generated", AgentID: agentID, Changed: now},
		StatusChange{From: string(ClaimLifecycleGenerated), To: string(ClaimLifecyclePosted), Reason: "remediation claim posted", AgentID: agentID, Changed: now},
	)
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

func (b *ClaimsBoard) TransitionToValidation(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	prevSeq := b.seq.Load()
	eventSeq := b.nextSeq()
	if err := b.appendDurableEventLocked(walEventPhaseTransition, "", map[string]any{
		"phase": string(BoardPhaseValidation), "iteration": b.iteration, "sequence": eventSeq,
	}, []ClaimsOutboxRecord{b.outboxRecordLocked(eventSeq, "board", b.boardID, walEventPhaseTransition, time.Now().UTC())}); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	b.phase = BoardPhaseValidation
	b.logPhaseTransitionLocked(fromPhase, b.phase, "all claims testified", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitBoardPhaseChanged(ctx, phase, iteration, "")
	}
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

func (b *ClaimsBoard) TransitionToImplementation(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	prevSeq := b.seq.Load()
	eventSeq := b.nextSeq()
	if err := b.appendDurableEventLocked(walEventPhaseTransition, "", map[string]any{
		"phase": string(BoardPhaseImplementation), "iteration": b.iteration + 1, "sequence": eventSeq,
	}, []ClaimsOutboxRecord{b.outboxRecordLocked(eventSeq, "board", b.boardID, walEventPhaseTransition, time.Now().UTC())}); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	b.iteration++
	b.phase = BoardPhaseImplementation
	b.logPhaseTransitionLocked(fromPhase, b.phase, "remediation re-entry", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitBoardPhaseChanged(ctx, phase, iteration, "")
	}
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

func (b *ClaimsBoard) MarkComplete(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	prevSeq := b.seq.Load()
	eventSeq := b.nextSeq()
	if err := b.appendDurableEventLocked(walEventBoardComplete, "", map[string]any{
		"sequence": eventSeq,
	}, []ClaimsOutboxRecord{b.outboxRecordLocked(eventSeq, "board", b.boardID, walEventBoardComplete, time.Now().UTC())}); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	b.phase = BoardPhaseComplete
	b.logPhaseTransitionLocked(fromPhase, b.phase, "all claims accepted", "")
	phase, iteration := b.phase, b.iteration
	b.mu.Unlock()

	b.projectDurableOutbox(ctx)

	if b.shouldEmitFabricDirect() {
		b.amplifier.EmitBoardComplete(ctx, "")
	}
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
	// Fast path: return cached projection if not dirty.
	if !b.projectionDirty.Load() {
		if cached := b.cachedProjection.Load(); cached != nil {
			return cached
		}
	}

	// Slow path: recompute and cache.
	b.mu.RLock()
	hasErrors := len(b.notificationErrors) > 0
	b.mu.RUnlock()

	var p *ClaimsBoardProjection
	if !hasErrors {
		b.mu.RLock()
		p = b.projectionLocked()
		b.mu.RUnlock()
	} else {
		b.mu.Lock()
		p = b.projectionLocked()
		b.notificationErrors = b.notificationErrors[:0]
		b.mu.Unlock()
	}

	b.cachedProjection.Store(p)
	b.projectionDirty.Store(false)
	return p
}

// invalidateProjectionCache marks the cached projection as stale.
// Called by every mutation path so the next Projection() recomputes.
func (b *ClaimsBoard) invalidateProjectionCache() {
	b.projectionDirty.Store(true)
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
		clone.StatusHistory = capStatusHistory(clone.StatusHistory)
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
	actions := make([]*Action, 0, len(b.actions))
	for _, a := range b.actions {
		if a == nil {
			continue
		}
		actions = append(actions, a)
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Sequence != actions[j].Sequence {
			return actions[i].Sequence < actions[j].Sequence
		}
		return actions[i].ID < actions[j].ID
	})
	for _, a := range actions {
		clone := *a
		p.Actions = append(p.Actions, clone)
		p.TotalClaimActions++
	}
}

func (b *ClaimsBoard) populateTestamentsProjectionLocked(p *ClaimsBoardProjection) {
	testaments := make([]*Testament, 0, len(b.testaments))
	for _, t := range b.testaments {
		if t != nil {
			testaments = append(testaments, t)
		}
	}
	sort.SliceStable(testaments, func(i, j int) bool {
		if testaments[i].Sequence != testaments[j].Sequence {
			return testaments[i].Sequence < testaments[j].Sequence
		}
		return testaments[i].ID < testaments[j].ID
	})
	for _, t := range testaments {
		clonePtr := CloneTestamentEntity(t)
		if clonePtr == nil {
			continue
		}
		clone := *clonePtr
		sort.SliceStable(clone.Artifacts, func(i, j int) bool {
			if clone.Artifacts[i] == nil || clone.Artifacts[j] == nil {
				return clone.Artifacts[i] != nil
			}
			if clone.Artifacts[i].Sequence != clone.Artifacts[j].Sequence {
				return clone.Artifacts[i].Sequence < clone.Artifacts[j].Sequence
			}
			return clone.Artifacts[i].ID < clone.Artifacts[j].ID
		})
		// Truncate large artifact references in projection copies.
		// Full content preserved in board's internal storage.
		for i, a := range clone.Artifacts {
			if a != nil && len(a.Reference) > maxArtifactReferenceLen {
				truncated := *a
				truncated.Reference = TruncateArtifactReference(a.Reference)
				truncated.Metadata = cloneAnyMap(a.Metadata)
				if truncated.Metadata == nil {
					truncated.Metadata = make(map[string]any, 3)
				}
				truncated.Metadata[ArtifactMetadataContentTruncated] = true
				truncated.Metadata[ArtifactMetadataContentInline] = false
				truncated.Metadata[ArtifactMetadataContentSize] = len(a.Reference)
				clone.Artifacts[i] = &truncated
			}
		}
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
	return CloneClaimEntity(c), true
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
			out = append(out, CloneTestamentEntity(t))
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

// SubscribeDelta registers a lightweight delta subscriber. Returns an
// unsubscribe function. Delta subscribers receive BoardMutationDelta
// (what changed + current summary counts) instead of full projections.
func (b *ClaimsBoard) SubscribeDelta(fn BoardDeltaSubscriber) func() {
	if b == nil || fn == nil {
		return func() {}
	}
	b.subscribersMu.Lock()
	b.deltaSubscriberSeq++
	id := b.deltaSubscriberSeq
	b.deltaSubscribers = append(b.deltaSubscribers, boardDeltaSubscription{id: id, fn: fn})
	b.subscribersMu.Unlock()

	return func() {
		b.subscribersMu.Lock()
		defer b.subscribersMu.Unlock()
		for i, s := range b.deltaSubscribers {
			if s.id == id {
				b.deltaSubscribers = append(b.deltaSubscribers[:i], b.deltaSubscribers[i+1:]...)
				return
			}
		}
	}
}

// notifyDelta dispatches a lightweight mutation delta to all delta
// subscribers. No projection copy — just the delta struct (what changed)
// plus the current summary counters. Best-effort: subscriber errors
// are logged but do not block the mutation.
func (b *ClaimsBoard) notifyDelta(delta BoardMutationDelta) {
	// Invalidate the projection cache BEFORE notifying delta
	// subscribers so any subscriber that calls board.Projection()
	// (e.g., the UI bridge looking up the freshly-created claim)
	// sees the post-mutation state. notifySubscribers also
	// invalidates, but it runs after the delta loop in PostAction —
	// which would leave the bridge unable to look up the claim that
	// just triggered the delta.
	b.invalidateProjectionCache()
	delta.Summary = b.Summary()

	b.subscribersMu.Lock()
	subs := make([]BoardDeltaSubscriber, len(b.deltaSubscribers))
	for i, s := range b.deltaSubscribers {
		subs[i] = s.fn
	}
	b.subscribersMu.Unlock()

	for _, fn := range subs {
		if err := fn(delta); err != nil {
			b.RecordNotificationError("delta subscriber: " + err.Error())
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
	b.invalidateProjectionCache()

	b.subscribersMu.Lock()
	hasSubs := len(b.subscribers) > 0
	b.subscribersMu.Unlock()
	if !hasSubs {
		return // skip projection computation when no legacy subscribers
	}

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
	b.invalidateProjectionCache()
	b.mu.Unlock()
}

func (b *ClaimsBoard) RecordProjectionError(record ClaimsOutboxRecord, projector string, err error) {
	if b == nil || err == nil {
		return
	}
	key := projectionDiagnosticKey(record, projector)
	msg := projectionDiagnosticMessage(record, projector, err)
	shouldSubmit := false
	b.mu.Lock()
	if b.projectionErrors == nil {
		b.projectionErrors = make(map[string]string)
	}
	if b.projectionErrors[key] != msg {
		b.projectionErrors[key] = msg
		b.notificationErrors = append(b.notificationErrors, msg)
		b.invalidateProjectionCache()
		shouldSubmit = true
	}
	b.mu.Unlock()
	if shouldSubmit {
		b.submitProjectionDiagnostic(context.Background(), record, projector, ArtifactKindProjectionError, msg, err.Error())
	}
}

func (b *ClaimsBoard) RecordProjectionSuccess(record ClaimsOutboxRecord, projector string) {
	if b == nil {
		return
	}
	key := projectionDiagnosticKey(record, projector)
	b.mu.Lock()
	oldMessage, hadError := b.projectionErrors[key]
	if hadError {
		delete(b.projectionErrors, key)
		b.notificationErrors = removeNotificationError(b.notificationErrors, oldMessage)
		b.invalidateProjectionCache()
	}
	b.mu.Unlock()
	if hadError {
		msg := fmt.Sprintf("projection_receipt projector=%s board=%s sequence=%d entity=%s/%s",
			projector, record.BoardID, record.Sequence, record.EntityType, record.EntityID)
		b.submitProjectionDiagnostic(context.Background(), record, projector, ArtifactKindProjectionReceipt, msg, "")
	}
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
	return CloneTestamentEntity(t), true
}

// CloneArtifact returns a defensive copy of the artifact by id.
func (b *ClaimsBoard) CloneArtifact(id string) (*Artifact, bool) {
	return b.cloneArtifact(id)
}

func (b *ClaimsBoard) cloneArtifact(id string) (*Artifact, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, t := range b.testaments {
		for _, a := range t.Artifacts {
			if a == nil || a.ID != id {
				continue
			}
			return CloneArtifact(a), true
		}
	}
	return nil, false
}

// CloneValidation returns a defensive copy of the validation and its parent claim.
func (b *ClaimsBoard) CloneValidation(id string) (*Validation, *Claim, bool) {
	return b.cloneValidation(id)
}

func (b *ClaimsBoard) cloneValidation(id string) (*Validation, *Claim, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, c := range b.claims {
		for _, v := range c.Validations {
			if v == nil || v.ID != id {
				continue
			}
			vClone := *v
			if v.Relations != nil {
				vClone.Relations = append([]Relation(nil), v.Relations...)
			}
			if v.StatusHistory != nil {
				vClone.StatusHistory = append([]StatusChange(nil), v.StatusHistory...)
			}
			cClone := *c
			return &vClone, &cClone, true
		}
	}
	return nil, nil, false
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

// BoardSummary is the lightweight read of board state — counts only,
// no entity copies. Readable without lock via atomic counters.
type BoardSummary struct {
	Phase      BoardPhase `json:"phase"`
	Iteration  int        `json:"iteration"`
	Total      int        `json:"total"`
	Pending    int        `json:"pending"`
	InProgress int        `json:"in_progress"`
	Testified  int        `json:"testified"`
	Accepted   int        `json:"accepted"`
	Rejected   int        `json:"rejected"`
}

// Summary returns the board's status counters without copying any
// entities. Lock-free — reads atomic counters.
func (b *ClaimsBoard) Summary() BoardSummary {
	b.mu.RLock()
	phase := b.phase
	iteration := b.iteration
	b.mu.RUnlock()
	return BoardSummary{
		Phase:      phase,
		Iteration:  iteration,
		Total:      int(b.countTotal.Load()),
		Pending:    int(b.countPending.Load()),
		InProgress: int(b.countInProgress.Load()),
		Testified:  int(b.countTestified.Load()),
		Accepted:   int(b.countAccepted.Load()),
		Rejected:   int(b.countRejected.Load()),
	}
}

// adjustStatusCounter decrements the old status counter and increments
// the new one. Called under write lock during status transitions.
func (b *ClaimsBoard) adjustStatusCounter(from, to ClaimStatus) {
	b.decrementStatusCounter(from)
	b.incrementStatusCounter(to)
}

func (b *ClaimsBoard) incrementStatusCounter(status ClaimStatus) {
	switch status {
	case ClaimStatusPending:
		b.countPending.Add(1)
	case ClaimStatusInProgress:
		b.countInProgress.Add(1)
	case ClaimStatusTestified:
		b.countTestified.Add(1)
	case ClaimStatusAccepted:
		b.countAccepted.Add(1)
	case ClaimStatusRejected:
		b.countRejected.Add(1)
	}
}

func (b *ClaimsBoard) decrementStatusCounter(status ClaimStatus) {
	switch status {
	case ClaimStatusPending:
		b.countPending.Add(-1)
	case ClaimStatusInProgress:
		b.countInProgress.Add(-1)
	case ClaimStatusTestified:
		b.countTestified.Add(-1)
	case ClaimStatusAccepted:
		b.countAccepted.Add(-1)
	case ClaimStatusRejected:
		b.countRejected.Add(-1)
	}
}

func (b *ClaimsBoard) rebuildDerivedState() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.relationsIdx = newRelationsIndex()
	b.claimGenerationKeys = make(map[string]string)
	b.singleClaimGenerationKeys = make(map[string]string)
	b.testamentGenerationKeys = make(map[string]string)
	b.countTotal.Store(0)
	b.countPending.Store(0)
	b.countInProgress.Store(0)
	b.countTestified.Store(0)
	b.countAccepted.Store(0)
	b.countRejected.Store(0)
	b.claimOrder = b.claimOrder[:0]
	var high uint64
	for _, a := range b.actions {
		if a == nil {
			continue
		}
		b.indexRelations(a.ID, a.Relations)
		if a.IdempotencyKey != "" {
			switch a.Type {
			case ActionTypeTestament:
				b.testamentGenerationKeys[a.IdempotencyKey] = a.ID
			default:
				b.claimGenerationKeys[a.IdempotencyKey] = a.ID
			}
		}
		if a.Sequence > high {
			high = a.Sequence
		}
	}
	for id, c := range b.claims {
		if c == nil {
			continue
		}
		b.claimOrder = append(b.claimOrder, id)
		b.indexRelations(c.ID, c.Relations)
		b.relationsIdx.addScope(c.ID, c.Scope)
		if c.IdempotencyKey != "" && !b.claimKeyBelongsToActionGenerationLocked(c) {
			b.singleClaimGenerationKeys[c.IdempotencyKey] = c.ID
		}
		b.countTotal.Add(1)
		b.incrementStatusCounter(c.Status)
		if c.Sequence > high {
			high = c.Sequence
		}
		for _, v := range c.Validations {
			if v != nil && v.Sequence > high {
				high = v.Sequence
			}
		}
	}
	sort.SliceStable(b.claimOrder, func(i, j int) bool {
		ci := b.claims[b.claimOrder[i]]
		cj := b.claims[b.claimOrder[j]]
		if ci == nil || cj == nil {
			return b.claimOrder[i] < b.claimOrder[j]
		}
		if ci.Sequence != cj.Sequence {
			return ci.Sequence < cj.Sequence
		}
		return ci.ID < cj.ID
	})
	for _, t := range b.testaments {
		if t == nil {
			continue
		}
		b.indexRelations(t.ID, t.Relations)
		if t.Sequence > high {
			high = t.Sequence
		}
		for _, a := range t.Artifacts {
			if a != nil {
				b.indexRelations(a.ID, a.Relations)
			}
			if a != nil && a.Sequence > high {
				high = a.Sequence
			}
		}
	}
	if b.seq.Load() < high {
		b.seq.Store(high)
	}
	b.invalidateProjectionCache()
}

// ClaimsForAgent returns claims where the given agent has the specified
// relationship (typically "subject" or "evaluator"). Index-backed O(1)
// lookup + O(k) clones where k = matching claims.
func (b *ClaimsBoard) ClaimsForAgent(agentID, relationship string) []*Claim {
	ids := b.ObjectIDsWithRelation(RelatedTypeAgent, relationship, agentID)
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*Claim, 0, len(ids))
	for _, id := range ids {
		if c, ok := b.claims[id]; ok {
			clone := *c
			result = append(result, &clone)
		}
	}
	return result
}

// ClaimsForAgentByStatus returns claims for the agent filtered by status.
func (b *ClaimsBoard) ClaimsForAgentByStatus(agentID, relationship string, status ClaimStatus) []*Claim {
	all := b.ClaimsForAgent(agentID, relationship)
	filtered := make([]*Claim, 0, len(all))
	for _, c := range all {
		if c.Status == status {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// PendingValidationsForClaim returns pending non-receipt validations on a claim.
func (b *ClaimsBoard) PendingValidationsForClaim(claimID string) []*Validation {
	c, ok := b.CloneClaim(claimID)
	if !ok {
		return nil
	}
	var pending []*Validation
	for _, v := range c.Validations {
		if v != nil && v.Status == ValidationStatusPending && v.Type != ValidationTypeReceipt {
			pending = append(pending, v)
		}
	}
	return pending
}

// ── Internal helpers ────────────────────────────────────────────────

func (b *ClaimsBoard) nextSeq() uint64 {
	return b.seq.Add(1)
}

func (b *ClaimsBoard) appendDurableEventLocked(kind, agentID string, payload any, outboxRecords []ClaimsOutboxRecord) error {
	if b == nil || b.durable == nil {
		return nil
	}
	if err := b.durable.appendCommittedEvent(kind, agentID, payload, outboxRecords); err != nil {
		return err
	}
	return nil
}

func (b *ClaimsBoard) projectDurableOutbox(ctx context.Context) {
	if b == nil || b.durable == nil {
		return
	}
	b.durable.projectOutbox(ctx)
}

func (b *ClaimsBoard) shouldEmitFabricDirect() bool {
	if b == nil || b.durable == nil {
		return true
	}
	return !b.durable.hasProjector(ProjectorFabric)
}

func (b *ClaimsBoard) shouldEmitCanonicalDirect() bool {
	if b == nil || b.durable == nil {
		return true
	}
	return !b.durable.hasProjector(ProjectorCanonicalDelta)
}

func (b *ClaimsBoard) outboxRecordLocked(sequence uint64, entityType, entityID, mutationKind string, createdAt time.Time) ClaimsOutboxRecord {
	return ClaimsOutboxRecord{
		BoardID:      b.boardID,
		SessionID:    b.sessionID,
		TaskID:       b.taskID,
		Sequence:     sequence,
		EntityType:   entityType,
		EntityID:     entityID,
		MutationKind: mutationKind,
		CreatedAt:    createdAt,
	}
}

func (b *ClaimsBoard) outboxRecordsForPostActionLocked(action Action, claims []Claim, now time.Time) []ClaimsOutboxRecord {
	records := make([]ClaimsOutboxRecord, 0, 1+len(claims))
	records = append(records, b.outboxRecordLocked(action.Sequence, "action", action.ID, walEventActionPosted, now))
	for i := range claims {
		records = append(records, b.outboxRecordLocked(claims[i].Sequence, "claim", claims[i].ID, string(DeltaActionClaimPosted), now))
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForSubmitTestamentsLocked(action Action, testaments []Testament, now time.Time) []ClaimsOutboxRecord {
	records := make([]ClaimsOutboxRecord, 0, 1+len(testaments)*2)
	records = append(records, b.outboxRecordLocked(action.Sequence, "action", action.ID, walEventActionPosted, now))
	for i := range testaments {
		t := testaments[i]
		records = append(records, b.outboxRecordLocked(t.Sequence, "testament", t.ID, string(DeltaActionTestamentPosted), now))
		for _, a := range t.Artifacts {
			if a == nil {
				continue
			}
			records = append(records, b.outboxRecordLocked(a.Sequence, "artifact", a.ID, "artifact_published", now))
		}
	}
	return records
}

func claimAcceptedAfterValidation(c *Claim, validationID string, next ValidationStatus) bool {
	if c == nil || len(c.Validations) == 0 {
		return false
	}
	for _, v := range c.Validations {
		if v == nil || !v.Required {
			continue
		}
		status := v.Status
		if v.ID == validationID {
			status = next
		}
		if status != ValidationStatusPassed {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
