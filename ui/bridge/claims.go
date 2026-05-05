package bridge

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	claimsBridgeName    = "bridge.claims"
	claimsBridgeBuffer  = 256
	claimsBridgeTimeout = 0
)

type claimMeta struct {
	ClaimID             string
	SessionID           string
	CycleID             string
	OwnerAgentID        string
	OwnerAgentType      string
	TargetAgentID       string
	TargetAgentType     string
	IssuerAgentID       string
	ActionType          string
	Title               string
	StreamCorrelationID string
}

// ClaimsBridge projects the claims plane into claims-native Bubble Tea
// messages. It is the only UI component that walks claim relations: the
// chat and agent panels consume the resolved cycle/artifact messages
// mechanically.
type ClaimsBridge struct {
	id       string
	scope    *concurrency.GoroutineScope
	registry *claims.SessionBoardRegistry

	mu            sync.Mutex
	program       TeaProgram
	activeSession string
	board         *claims.ClaimsBoard
	unsub         func()
	resolver      *cycleResolver

	claimMeta                 map[string]claimMeta
	artifactByID              map[string]*claims.Artifact
	artifactClaim             map[string]string
	emittedStartedArtifacts   map[string]struct{}
	completedStartedArtifacts map[string]struct{}
	claimToInvocationArtifact map[string]string
	latestStateByClaim        map[string]string

	lastAccepted int
	lastTotal    int

	outbox   chan any
	dropped  atomic.Int64
	done     chan struct{}
	stopOnce sync.Once

	prevArtifactSink claims.ArtifactProgressSink
	sinkRegistered   bool
}

func NewClaimsBridge(
	id string,
	registry *claims.SessionBoardRegistry,
	scope *concurrency.GoroutineScope,
) *ClaimsBridge {
	return &ClaimsBridge{
		id:                        id,
		scope:                     scope,
		registry:                  registry,
		resolver:                  newCycleResolver(),
		claimMeta:                 make(map[string]claimMeta),
		artifactByID:              make(map[string]*claims.Artifact),
		artifactClaim:             make(map[string]string),
		emittedStartedArtifacts:   make(map[string]struct{}),
		completedStartedArtifacts: make(map[string]struct{}),
		claimToInvocationArtifact: make(map[string]string),
		latestStateByClaim:        make(map[string]string),
		outbox:                    make(chan any, claimsBridgeBuffer),
		done:                      make(chan struct{}),
	}
}

func (b *ClaimsBridge) Start(program TeaProgram) error {
	b.mu.Lock()
	b.program = program
	if !b.sinkRegistered {
		b.prevArtifactSink = claims.SetArtifactProgressSink(b)
		b.sinkRegistered = true
	}
	b.mu.Unlock()

	if b.scope == nil {
		return nil
	}
	return b.scope.Go(claimsBridgeName, claimsBridgeTimeout, b.drainFunc(program))
}

func (b *ClaimsBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case m := <-b.outbox:
				program.Send(m)
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (b *ClaimsBridge) Stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		if b.unsub != nil {
			b.unsub()
			b.unsub = nil
		}
		if b.sinkRegistered {
			claims.SetArtifactProgressSink(b.prevArtifactSink)
			b.sinkRegistered = false
			b.prevArtifactSink = nil
		}
		b.mu.Unlock()
		close(b.done)
	})
}

func (b *ClaimsBridge) Name() string { return claimsBridgeName }

func (b *ClaimsBridge) DroppedCount() int64 { return b.dropped.Load() }

func (b *ClaimsBridge) SwitchSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)

	var board *claims.ClaimsBoard
	if b.registry != nil && sessionID != "" {
		board = b.registry.Lookup(sessionID)
	}

	b.mu.Lock()
	if b.unsub != nil {
		b.unsub()
		b.unsub = nil
	}
	b.activeSession = sessionID
	b.board = board
	b.resetSessionStateLocked()
	if board != nil {
		b.unsub = board.SubscribeDelta(func(delta claims.BoardMutationDelta) error {
			b.onDelta(delta)
			return nil
		})
	}
	b.mu.Unlock()

	if board != nil {
		b.replayProjection(sessionID, board.Projection())
	}
}

func (b *ClaimsBridge) resetSessionStateLocked() {
	b.resolver = newCycleResolver()
	b.claimMeta = make(map[string]claimMeta)
	b.artifactByID = make(map[string]*claims.Artifact)
	b.artifactClaim = make(map[string]string)
	b.emittedStartedArtifacts = make(map[string]struct{})
	b.completedStartedArtifacts = make(map[string]struct{})
	b.claimToInvocationArtifact = make(map[string]string)
	b.latestStateByClaim = make(map[string]string)
	b.lastAccepted = 0
	b.lastTotal = 0
}

func (b *ClaimsBridge) onDelta(delta claims.BoardMutationDelta) {
	board, sessionID := b.currentBoard()
	if board == nil || sessionID == "" {
		return
	}
	b.emitCounterDelta(delta)

	switch delta.Kind {
	case "claim_created":
		if c := findClaim(board.Projection(), delta.ClaimID); c != nil {
			b.handleClaimCreated(sessionID, c)
		}
	case "claim_status_changed":
		if delta.ToStatus.IsTerminal() {
			b.handleClaimClosed(delta.ClaimID, terminalOutcome(delta.ToStatus))
		}
	case "validation_evaluated":
		if delta.ToStatus == claims.ClaimStatusAccepted {
			b.handleClaimClosed(delta.ClaimID, "success")
		}
	case "claim_rejected":
		b.handleClaimClosed(delta.ClaimID, "failure")
	case "testament_submitted":
		if t := findTestament(board.Projection(), delta.TestamentID); t != nil {
			b.handleTestamentSubmitted(sessionID, t)
		}
	case "claim_context_changed":
		b.handleClaimContext(sessionID, delta)
	case "testament_context_changed":
		b.handleTestamentContext(sessionID, delta)
	}
}

func (b *ClaimsBridge) currentBoard() (*claims.ClaimsBoard, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.board, b.activeSession
}

func (b *ClaimsBridge) emitCounterDelta(delta claims.BoardMutationDelta) {
	b.mu.Lock()
	changed := delta.Summary.Accepted != b.lastAccepted || delta.Summary.Total != b.lastTotal
	b.lastAccepted = delta.Summary.Accepted
	b.lastTotal = delta.Summary.Total
	b.mu.Unlock()
	if !changed {
		return
	}
	b.enqueue(msg.ClaimsProjectionMsg{
		SessionID:     b.activeSessionID(),
		AcceptedCount: delta.Summary.Accepted,
		TotalClaims:   delta.Summary.Total,
	})
}

func (b *ClaimsBridge) activeSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeSession
}

func (b *ClaimsBridge) handleClaimCreated(sessionID string, c *claims.Claim) {
	if c == nil || strings.TrimSpace(c.ID) == "" {
		return
	}

	issuer := strings.TrimSpace(claims.IssuerAgentID(c.Relations))
	subject := strings.TrimSpace(claims.SubjectAgentID(c.Relations))
	causedBy := relationID(c.Relations, claims.RelationshipCausedBy)
	handoffFrom := claims.HandoffFromClaimID(c.Relations)
	ownerForResolver := cycleOwnerForClaim(c)

	var toEmit []any
	b.mu.Lock()
	outcome := b.resolver.onClaimCreated(c.ID, ownerForResolver, subject, causedBy, handoffFrom)
	cycleID := b.resolver.CycleForClaim(c.ID)
	cycleOwner := ownerForResolver
	if outcome.CycleOpened != nil {
		cycleOwner = outcome.CycleOpened.OwnerAgentID
	}
	if outcome.AttachedToCycle != nil {
		cycleOwner = outcome.AttachedToCycle.OwnerAgentID
	}
	if cycleOwner == "" {
		cycleOwner = ownerForResolver
	}
	meta := claimMeta{
		ClaimID:             c.ID,
		SessionID:           sessionID,
		CycleID:             cycleID,
		OwnerAgentID:        cycleOwner,
		OwnerAgentType:      agentTypeFromID(cycleOwner),
		TargetAgentID:       subject,
		TargetAgentType:     agentTypeFromID(subject),
		IssuerAgentID:       issuer,
		ActionType:          string(c.ActionType),
		Title:               strings.TrimSpace(c.Title),
		StreamCorrelationID: claimStreamCorrelation(c),
	}
	b.claimMeta[c.ID] = meta

	if outcome.PredecessorClosed != nil {
		toEmit = append(toEmit, claimsAgentClosedMsg(sessionID, outcome.PredecessorClosed, ""))
	}
	if outcome.CycleOpened != nil {
		toEmit = append(toEmit, msg.ClaimsAgentStatusMsg{
			AgentID:             outcome.CycleOpened.OwnerAgentID,
			SessionID:           sessionID,
			Active:              true,
			CycleID:             outcome.CycleOpened.CycleID,
			OpenCount:           len(outcome.CycleOpened.openClaims),
			Reason:              strings.TrimSpace(c.Title),
			ActionType:          string(c.ActionType),
			StreamCorrelationID: meta.StreamCorrelationID,
		})
	}
	for _, pending := range outcome.PendingArtifacts {
		if art := b.artifactByID[pending.ArtifactID]; art != nil {
			if m := b.claimArtifactAddedMsgLocked(sessionID, c.ID, art, pending.Cycle); m != nil {
				toEmit = append(toEmit, *m)
			}
		}
	}
	b.mu.Unlock()

	for _, m := range toEmit {
		b.enqueue(m)
	}
}

func (b *ClaimsBridge) handleClaimClosed(claimID, outcome string) {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return
	}
	var closeMsg *msg.ClaimsAgentStatusMsg
	b.mu.Lock()
	if st := b.resolver.onClaimClosed(claimID); st != nil {
		m := claimsAgentClosedMsg(b.activeSession, st, outcome)
		closeMsg = &m
	}
	b.mu.Unlock()
	if closeMsg != nil {
		b.enqueue(*closeMsg)
	}
}

func (b *ClaimsBridge) handleTestamentSubmitted(sessionID string, t *claims.Testament) {
	if t == nil {
		return
	}
	claimID := claims.ClaimIDFromRelations(t.Relations)
	for i := range t.Artifacts {
		art := t.Artifacts[i]
		b.OnArtifactAdded(claimID, t.AgentID, sessionID, art)
	}
	b.completePeerInvocationForClaim(claimID, "success", strings.TrimSpace(t.Summary))
}

func (b *ClaimsBridge) handleClaimContext(sessionID string, delta claims.BoardMutationDelta) {
	claimID := strings.TrimSpace(delta.ClaimID)
	if claimID == "" {
		return
	}
	var out *msg.ClaimContextMsg
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID != "" {
		out = &msg.ClaimContextMsg{
			SessionID:         sessionID,
			ClaimID:           claimID,
			OwnerAgentID:      meta.OwnerAgentID,
			CycleID:           meta.CycleID,
			Context:           delta.Context,
			ContextTransition: delta.ContextTransition,
			State:             b.latestStateByClaim[claimID],
		}
	}
	b.mu.Unlock()
	if out != nil {
		b.enqueue(*out)
	}
}

func (b *ClaimsBridge) handleTestamentContext(sessionID string, delta claims.BoardMutationDelta) {
	claimID := strings.TrimSpace(delta.ClaimID)
	if claimID == "" {
		return
	}
	var out *msg.TestamentContextMsg
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID != "" {
		out = &msg.TestamentContextMsg{
			SessionID:         sessionID,
			AccumulatorID:     strings.TrimSpace(delta.AccumulatorID),
			TestamentID:       strings.TrimSpace(delta.TestamentID),
			ClaimID:           claimID,
			AgentID:           strings.TrimSpace(delta.AgentID),
			CycleID:           meta.CycleID,
			Context:           delta.Context,
			ContextTransition: delta.ContextTransition,
		}
	}
	b.mu.Unlock()
	if out != nil {
		b.enqueue(*out)
	}
}

// OnArtifactAdded implements claims.ArtifactProgressSink.
func (b *ClaimsBridge) OnArtifactAdded(claimID, agentID, sessionID string, artifact *claims.Artifact) {
	if artifact == nil {
		return
	}
	claimID = strings.TrimSpace(claimID)
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)

	b.mu.Lock()
	if sessionID == "" {
		sessionID = b.activeSession
	}
	if sessionID == "" || sessionID != b.activeSession {
		b.mu.Unlock()
		return
	}
	art := cloneArtifact(artifact)
	if art.AgentID == "" {
		art.AgentID = agentID
	}
	if art.Created.IsZero() {
		art.Created = time.Now().UTC()
	}
	if art.ID == "" {
		art.ID = "artifact-" + strconv.FormatInt(art.Created.UnixNano(), 36)
	}
	b.artifactByID[art.ID] = art
	if claimID != "" {
		b.artifactClaim[art.ID] = claimID
	}

	var out []any
	switch {
	case art.Kind == claims.ArtifactKindAgentState:
		out = append(out, b.handleAgentStateArtifactLocked(sessionID, claimID, art)...)
	case art.Kind == claims.ArtifactKindResponseText:
		if m := b.claimResponseTextMsgLocked(sessionID, claimID, art); m != nil {
			out = append(out, *m)
		}
	case isVisibleStartedArtifactKind(art.Kind):
		if m := b.routeStartedArtifactLocked(sessionID, claimID, art); m != nil {
			out = append(out, *m)
		}
	case isVisibleCompletedArtifactKind(art.Kind):
		out = append(out, b.routeCompletedArtifactLocked(sessionID, art)...)
	}
	b.mu.Unlock()

	for _, m := range out {
		b.enqueue(m)
	}
}

func (b *ClaimsBridge) routeStartedArtifactLocked(sessionID, claimID string, art *claims.Artifact) *msg.ClaimArtifactAddedMsg {
	artifactID := strings.TrimSpace(art.ID)
	if artifactID == "" {
		return nil
	}
	if _, emitted := b.emittedStartedArtifacts[artifactID]; emitted {
		return nil
	}
	cycle := b.resolver.onArtifactStarted(artifactID, claimID)
	if cycle == nil {
		return nil
	}
	return b.claimArtifactAddedMsgLocked(sessionID, claimID, art, cycle)
}

func (b *ClaimsBridge) claimArtifactAddedMsgLocked(sessionID, claimID string, art *claims.Artifact, cycle *cycleState) *msg.ClaimArtifactAddedMsg {
	if art == nil || cycle == nil {
		return nil
	}
	artifactID := strings.TrimSpace(art.ID)
	if artifactID == "" {
		return nil
	}
	if _, emitted := b.emittedStartedArtifacts[artifactID]; emitted {
		return nil
	}
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID == "" {
		meta.CycleID = cycle.CycleID
	}
	if meta.OwnerAgentID == "" {
		meta.OwnerAgentID = cycle.OwnerAgentID
		meta.OwnerAgentType = agentTypeFromID(cycle.OwnerAgentID)
	}
	parentRowID := b.claimToInvocationArtifact[claimID]
	childClaimID := artifactChildClaimID(art)
	if childClaimID != "" {
		b.claimToInvocationArtifact[childClaimID] = artifactID
	}
	b.emittedStartedArtifacts[artifactID] = struct{}{}
	return &msg.ClaimArtifactAddedMsg{
		ArtifactID:     artifactID,
		CycleID:        meta.CycleID,
		ParentRowID:    parentRowID,
		ClaimID:        claimID,
		OwnerAgentID:   meta.OwnerAgentID,
		OwnerAgentType: meta.OwnerAgentType,
		TargetAgentID:  meta.TargetAgentID,
		AgentID:        firstNonBlank(strings.TrimSpace(art.AgentID), meta.OwnerAgentID),
		Kind:           strings.TrimSpace(art.Kind),
		Reference:      strings.TrimSpace(art.Reference),
		Metadata:       cloneMetadata(art.Metadata),
		CreatedAt:      art.Created,
	}
}

func (b *ClaimsBridge) routeCompletedArtifactLocked(sessionID string, art *claims.Artifact) []any {
	startID := startedArtifactID(art)
	if startID == "" {
		return nil
	}
	if _, completed := b.completedStartedArtifacts[startID]; completed {
		return nil
	}
	b.completedStartedArtifacts[startID] = struct{}{}

	cycle, drained := b.resolver.onArtifactCompleted(startID)
	cycleID := ""
	if cycle != nil {
		cycleID = cycle.CycleID
	} else {
		cycleID = b.resolver.CycleForArtifact(startID)
	}
	out := []any{msg.ClaimArtifactCompletedMsg{
		StartArtifactID: startID,
		CycleID:         cycleID,
		Outcome:         artifactOutcome(art),
		Duration:        artifactDuration(art),
		Summary:         artifactSummary(art),
		Metadata:        cloneMetadata(art.Metadata),
		CompletedAt:     nonZeroTime(art.Created),
	}}
	if drained && cycle != nil {
		out = append(out, claimsAgentClosedMsg(sessionID, cycle, ""))
	}
	return out
}

func (b *ClaimsBridge) completePeerInvocationForClaim(claimID, outcome, summary string) {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return
	}
	var out []any
	b.mu.Lock()
	startID := b.claimToInvocationArtifact[claimID]
	if startID != "" {
		if _, completed := b.completedStartedArtifacts[startID]; !completed {
			b.completedStartedArtifacts[startID] = struct{}{}
			cycle, drained := b.resolver.onArtifactCompleted(startID)
			cycleID := ""
			if cycle != nil {
				cycleID = cycle.CycleID
			}
			out = append(out, msg.ClaimArtifactCompletedMsg{
				StartArtifactID: startID,
				CycleID:         cycleID,
				Outcome:         firstNonBlank(outcome, "success"),
				Summary:         summary,
				CompletedAt:     time.Now().UTC(),
			})
			if drained && cycle != nil {
				out = append(out, claimsAgentClosedMsg(b.activeSession, cycle, ""))
			}
		}
	}
	b.mu.Unlock()
	for _, m := range out {
		b.enqueue(m)
	}
}

func (b *ClaimsBridge) handleAgentStateArtifactLocked(sessionID, claimID string, art *claims.Artifact) []any {
	state := claimMetadataString(art.Metadata, "state")
	if state != "" {
		b.latestStateByClaim[claimID] = state
	}
	detail := strings.TrimSpace(art.Reference)
	if detail == "" {
		return nil
	}
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID == "" {
		return nil
	}
	return []any{msg.ClaimContextMsg{
		SessionID:    sessionID,
		ClaimID:      claimID,
		OwnerAgentID: meta.OwnerAgentID,
		CycleID:      meta.CycleID,
		Context:      detail,
		State:        state,
	}}
}

func (b *ClaimsBridge) claimResponseTextMsgLocked(sessionID, claimID string, art *claims.Artifact) *msg.ClaimResponseTextMsg {
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID == "" {
		return nil
	}
	content := strings.TrimSpace(art.Reference)
	if content == "" {
		content = claimMetadataString(art.Metadata, "text", "content", "response")
	}
	if content == "" {
		return nil
	}
	return &msg.ClaimResponseTextMsg{
		SessionID: sessionID,
		CycleID:   meta.CycleID,
		ClaimID:   claimID,
		AgentID:   firstNonBlank(strings.TrimSpace(art.AgentID), meta.OwnerAgentID),
		Content:   content,
		CreatedAt: nonZeroTime(art.Created),
	}
}

func (b *ClaimsBridge) metaForClaimLocked(claimID string) claimMeta {
	claimID = strings.TrimSpace(claimID)
	meta := b.claimMeta[claimID]
	if meta.ClaimID == "" {
		meta.ClaimID = claimID
		meta.SessionID = b.activeSession
		meta.CycleID = b.resolver.CycleForClaim(claimID)
		if meta.CycleID == "" {
			meta.CycleID = claimID
		}
	}
	if meta.OwnerAgentID == "" {
		meta.OwnerAgentID = b.resolver.OwnerForClaim(claimID)
		meta.OwnerAgentType = agentTypeFromID(meta.OwnerAgentID)
	}
	if meta.SessionID == "" {
		meta.SessionID = b.activeSession
	}
	if claimID != "" {
		b.claimMeta[claimID] = meta
	}
	return meta
}

func (b *ClaimsBridge) replayProjection(sessionID string, proj *claims.ClaimsBoardProjection) {
	if proj == nil {
		return
	}
	for i := range proj.Claims {
		c := &proj.Claims[i]
		if c.Status.IsTerminal() {
			continue
		}
		b.handleClaimCreated(sessionID, c)
		if strings.TrimSpace(c.Context) != "" {
			b.handleClaimContext(sessionID, claims.BoardMutationDelta{
				Kind:              "claim_context_changed",
				ClaimID:           c.ID,
				Context:           c.Context,
				ContextTransition: c.ContextTransition,
			})
		}
	}
	for i := range proj.Testaments {
		t := &proj.Testaments[i]
		if strings.TrimSpace(t.Context) != "" {
			b.handleTestamentContext(sessionID, claims.BoardMutationDelta{
				Kind:              "testament_context_changed",
				ClaimID:           claims.ClaimIDFromRelations(t.Relations),
				TestamentID:       t.ID,
				AgentID:           t.AgentID,
				Context:           t.Context,
				ContextTransition: t.ContextTransition,
			})
		}
	}
}

func claimsAgentClosedMsg(sessionID string, st *cycleState, outcome string) msg.ClaimsAgentStatusMsg {
	if st == nil {
		return msg.ClaimsAgentStatusMsg{}
	}
	return msg.ClaimsAgentStatusMsg{
		AgentID:         st.OwnerAgentID,
		SessionID:       sessionID,
		Active:          false,
		CycleID:         st.CycleID,
		OpenCount:       len(st.openClaims),
		TerminalOutcome: outcome,
	}
}

func terminalOutcome(status claims.ClaimStatus) string {
	switch status {
	case claims.ClaimStatusAccepted:
		return "success"
	case claims.ClaimStatusRejected, claims.ClaimStatusSuperseded:
		return "failure"
	default:
		return ""
	}
}

func findClaim(proj *claims.ClaimsBoardProjection, claimID string) *claims.Claim {
	if proj == nil {
		return nil
	}
	claimID = strings.TrimSpace(claimID)
	for i := range proj.Claims {
		if proj.Claims[i].ID == claimID {
			return &proj.Claims[i]
		}
	}
	return nil
}

func findTestament(proj *claims.ClaimsBoardProjection, testamentID string) *claims.Testament {
	if proj == nil {
		return nil
	}
	testamentID = strings.TrimSpace(testamentID)
	for i := range proj.Testaments {
		if proj.Testaments[i].ID == testamentID {
			return &proj.Testaments[i]
		}
	}
	return nil
}

func cycleOwnerForClaim(c *claims.Claim) string {
	if c == nil {
		return ""
	}
	issuer := strings.TrimSpace(claims.IssuerAgentID(c.Relations))
	subject := strings.TrimSpace(claims.SubjectAgentID(c.Relations))
	switch c.ActionType {
	case claims.ActionTypePrompt, claims.ActionTypeHandoff:
		return firstNonBlank(subject, issuer)
	default:
		return firstNonBlank(issuer, subject)
	}
}

func relationID(relations []claims.Relation, relationship string) string {
	if r := claims.FindRelation(relations, relationship); r != nil {
		return strings.TrimSpace(r.Related)
	}
	return ""
}

func artifactChildClaimID(art *claims.Artifact) string {
	if art == nil {
		return ""
	}
	return firstNonBlank(
		claimMetadataString(art.Metadata, "claim_id"),
		claimMetadataString(art.Metadata, "peer_claim_id"),
		claimMetadataString(art.Metadata, "guardian_claim_id"),
	)
}

func startedArtifactID(art *claims.Artifact) string {
	if art == nil {
		return ""
	}
	return firstNonBlank(
		claims.CompletesArtifactID(art.Relations),
		claimMetadataString(art.Metadata, "started_artifact_id", "start_artifact_id", "completes"),
	)
}

func claimStreamCorrelation(c *claims.Claim) string {
	if c == nil {
		return ""
	}
	const tagPrefix = "stream_corr_id:"
	for _, tag := range c.Tags {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(tag, tagPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(tag, tagPrefix))
		}
	}
	for _, scope := range c.Scope {
		if strings.TrimSpace(scope.Kind) == "correlation" {
			return strings.TrimSpace(scope.Key)
		}
	}
	return ""
}

func isVisibleStartedArtifactKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "tool_started", "consult_started", "challenge_started", "guardian_check_started":
		return true
	default:
		return false
	}
}

func isVisibleCompletedArtifactKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "tool_completed", "consult_completed", "challenge_completed", "guardian_check_completed":
		return true
	default:
		return false
	}
}

func artifactOutcome(art *claims.Artifact) string {
	if art == nil {
		return "success"
	}
	if outcome := claimMetadataString(art.Metadata, "outcome", "status"); outcome != "" {
		return outcome
	}
	if claimMetadataString(art.Metadata, "error") != "" {
		return "failure"
	}
	return "success"
}

func artifactDuration(art *claims.Artifact) time.Duration {
	if art == nil || art.Metadata == nil {
		return 0
	}
	if d, ok := durationFromMetadata(art.Metadata["duration"]); ok {
		return d
	}
	if d, ok := durationFromMetadata(art.Metadata["duration_ms"]); ok {
		return d * time.Millisecond
	}
	return 0
}

func durationFromMetadata(v any) (time.Duration, bool) {
	switch typed := v.(type) {
	case time.Duration:
		return typed, true
	case int:
		return time.Duration(typed), true
	case int64:
		return time.Duration(typed), true
	case float64:
		return time.Duration(typed), true
	case string:
		if d, err := time.ParseDuration(typed); err == nil {
			return d, true
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return time.Duration(n), true
		}
	}
	return 0, false
}

func artifactSummary(art *claims.Artifact) string {
	if art == nil {
		return ""
	}
	return firstNonBlank(
		strings.TrimSpace(art.Reference),
		claimMetadataString(art.Metadata, "summary", "output", "error"),
	)
}

func claimMetadataString(md map[string]any, keys ...string) string {
	if md == nil {
		return ""
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if v, ok := md[key]; ok {
			switch typed := v.(type) {
			case string:
				if s := strings.TrimSpace(typed); s != "" {
					return s
				}
			case []byte:
				if s := strings.TrimSpace(string(typed)); s != "" {
					return s
				}
			case fmtStringer:
				if s := strings.TrimSpace(typed.String()); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

type fmtStringer interface {
	String() string
}

func cloneArtifact(art *claims.Artifact) *claims.Artifact {
	if art == nil {
		return nil
	}
	cp := *art
	cp.Metadata = cloneMetadata(art.Metadata)
	if len(art.Relations) > 0 {
		cp.Relations = append([]claims.Relation(nil), art.Relations...)
	}
	return &cp
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func agentTypeFromID(agentID string) string {
	return strings.TrimSpace(agentID)
}

func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (b *ClaimsBridge) enqueue(m any) {
	select {
	case b.outbox <- m:
	default:
		total := b.dropped.Add(1)
		slog.Warn("claims bridge drop: outbox full",
			"bridge_id", b.id,
			"total_dropped", total)
	}
}
