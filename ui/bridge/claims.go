package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	claimsBridgeName    = "bridge.claims"
	claimsBridgeBuffer  = 256
	claimsBridgeTimeout = 0
	claimsBridgeAgentID = "tui"
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
	SuppressChat        bool
	UIState             string
}

type claimContextEvent struct {
	ClaimID           string
	AgentID           string
	Context           string
	ContextTransition int64
}

type testamentContextEvent struct {
	ClaimID           string
	TestamentID       string
	AgentID           string
	AccumulatorID     string
	Context           string
	ContextTransition int64
}

// ClaimsBridge projects the claims plane into claims-native Bubble Tea
// messages. It is the only UI component that walks claim relations: the
// chat and agent panels consume the resolved cycle/artifact messages
// mechanically.
type ClaimsBridge struct {
	id       string
	scope    *concurrency.GoroutineScope
	registry *claims.SessionBoardRegistry
	bus      guide.EventBus

	mu            sync.Mutex
	program       TeaProgram
	activeSession string
	board         *claims.ClaimsBoard
	inbox         *claims.ClaimsInbox
	resolver      *cycleResolver

	claimMeta                 map[string]claimMeta
	artifactByID              map[string]*claims.Artifact
	artifactClaim             map[string]string
	emittedStartedArtifacts   map[string]struct{}
	emittedPresentations      map[string]presentationEmissionState
	presentationReplacements  map[string]presentationEmissionState
	presentationMetrics       map[presentationMetricKey]int64
	presentationMetricSink    PresentationMetricSink
	presentationDiagnostics   map[string]struct{}
	completedStartedArtifacts map[string]struct{}
	claimToPeerRow            map[string]string
	latestStateByClaim        map[string]string

	lastAccepted int
	lastTotal    int

	outbox   chan any
	dropped  atomic.Int64
	done     chan struct{}
	stopOnce sync.Once

	prevArtifactSink claims.ArtifactProgressSink
	sinkRegistered   bool
	boardDeltaUnsub  func()
}

type presentationEmissionState struct {
	Sequence uint64
	SourceID string
}

func NewClaimsBridge(
	id string,
	registry *claims.SessionBoardRegistry,
	scope *concurrency.GoroutineScope,
	bus ...guide.EventBus,
) *ClaimsBridge {
	var eventBus guide.EventBus
	if len(bus) > 0 {
		eventBus = bus[0]
	}
	return &ClaimsBridge{
		id:                        id,
		scope:                     scope,
		registry:                  registry,
		bus:                       eventBus,
		resolver:                  newCycleResolver(),
		claimMeta:                 make(map[string]claimMeta),
		artifactByID:              make(map[string]*claims.Artifact),
		artifactClaim:             make(map[string]string),
		emittedStartedArtifacts:   make(map[string]struct{}),
		emittedPresentations:      make(map[string]presentationEmissionState),
		presentationReplacements:  make(map[string]presentationEmissionState),
		presentationMetrics:       make(map[presentationMetricKey]int64),
		presentationDiagnostics:   make(map[string]struct{}),
		completedStartedArtifacts: make(map[string]struct{}),
		claimToPeerRow:            make(map[string]string),
		latestStateByClaim:        make(map[string]string),
		outbox:                    make(chan any, claimsBridgeBuffer),
		done:                      make(chan struct{}),
	}
}

func (b *ClaimsBridge) debug(event string, fields ...any) {
	if b == nil {
		return
	}
	args := make([]any, 0, len(fields)+2)
	args = append(args, "bridge_id", b.id)
	args = append(args, fields...)
	guide.DebugFileLog().Info("CLAIMS_UI_DEBUG: "+event, args...)
}

func (b *ClaimsBridge) Start(program TeaProgram) error {
	b.mu.Lock()
	b.program = program
	if !b.sinkRegistered {
		b.prevArtifactSink = claims.SetArtifactProgressSink(b)
		b.sinkRegistered = true
	}
	b.debug("start",
		"program_present", program != nil,
		"scope_present", b.scope != nil,
		"registry_present", b.registry != nil,
		"bus_present", b.bus != nil,
	)
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
		b.stopBoardDeltaWatch()
		b.mu.Lock()
		if b.inbox != nil {
			_ = b.inbox.Close()
			b.inbox = nil
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
	b.stopBoardDeltaWatch()

	var board *claims.ClaimsBoard
	if b.registry != nil && sessionID != "" {
		board = b.registry.Lookup(sessionID)
	}
	boardID := ""
	if board != nil {
		boardID = board.BoardID()
	}
	b.debug("switch_session_lookup",
		"session_id", sessionID,
		"board_present", board != nil,
		"board_id", boardID,
	)

	b.mu.Lock()
	if b.inbox != nil {
		_ = b.inbox.Close()
		b.inbox = nil
	}
	b.activeSession = sessionID
	b.board = board
	b.resetSessionStateLocked()
	b.mu.Unlock()

	if board != nil {
		b.startClaimsIntake(sessionID, board)
		b.startBoardDeltaWatch(sessionID, board)
		b.replayProjection(sessionID, board.Projection())
	} else {
		b.debug("switch_session_no_board", "session_id", sessionID)
	}
}

func (b *ClaimsBridge) resetSessionStateLocked() {
	b.resolver = newCycleResolver()
	b.claimMeta = make(map[string]claimMeta)
	b.artifactByID = make(map[string]*claims.Artifact)
	b.artifactClaim = make(map[string]string)
	b.emittedStartedArtifacts = make(map[string]struct{})
	b.emittedPresentations = make(map[string]presentationEmissionState)
	b.presentationReplacements = make(map[string]presentationEmissionState)
	b.presentationMetrics = make(map[presentationMetricKey]int64)
	b.presentationDiagnostics = make(map[string]struct{})
	b.completedStartedArtifacts = make(map[string]struct{})
	b.claimToPeerRow = make(map[string]string)
	b.latestStateByClaim = make(map[string]string)
	b.lastAccepted = 0
	b.lastTotal = 0
}

func (b *ClaimsBridge) startClaimsIntake(sessionID string, board *claims.ClaimsBoard) {
	if b.bus == nil || board == nil || strings.TrimSpace(sessionID) == "" {
		b.debug("intake_skipped",
			"session_id", sessionID,
			"bus_present", b.bus != nil,
			"board_present", board != nil,
		)
		return
	}
	b.debug("intake_wiring",
		"session_id", sessionID,
		"board_id", board.BoardID(),
		"scope_present", b.scope != nil,
	)
	inbox := shared.WireClaimsIntake(shared.ClaimsIntakeConfig{
		AgentID:      claimsBridgeAgentID,
		SessionID:    sessionID,
		Role:         claims.RoleObserver | claims.RoleAuditor,
		Bus:          b.bus,
		Board:        board,
		Scope:        b.scope,
		ProcessEntry: b.processClaimsEntry,
		Identity:     nil,
		Factory:      nil,
	})
	if inbox == nil {
		b.debug("intake_nil", "session_id", sessionID, "board_id", board.BoardID())
		return
	}
	b.mu.Lock()
	if b.activeSession != sessionID || b.board != board {
		activeSession := b.activeSession
		b.mu.Unlock()
		b.debug("intake_stale",
			"session_id", sessionID,
			"active_session", activeSession,
			"board_id", board.BoardID(),
		)
		_ = inbox.Close()
		return
	}
	b.inbox = inbox
	b.mu.Unlock()
	if err := inbox.Start(nil); err != nil {
		slog.Error("claims bridge intake start failed",
			"bridge_id", b.id,
			"session_id", sessionID,
			"error", err.Error(),
		)
		b.debug("intake_start_failed",
			"session_id", sessionID,
			"board_id", board.BoardID(),
			"error", err.Error(),
		)
		b.mu.Lock()
		if b.inbox == inbox {
			b.inbox = nil
		}
		b.mu.Unlock()
		_ = inbox.Close()
		return
	}
	b.debug("intake_started", "session_id", sessionID, "board_id", board.BoardID())
}

func (b *ClaimsBridge) stopBoardDeltaWatch() {
	if b == nil {
		return
	}
	b.mu.Lock()
	unsub := b.boardDeltaUnsub
	b.boardDeltaUnsub = nil
	b.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

func (b *ClaimsBridge) startBoardDeltaWatch(sessionID string, board *claims.ClaimsBoard) {
	if b == nil || board == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	unsub := board.SubscribeDelta(func(delta claims.BoardMutationDelta) error {
		return b.processBoardMutationDelta(sessionID, board, delta)
	})
	b.mu.Lock()
	if b.activeSession != sessionID || b.board != board {
		b.mu.Unlock()
		unsub()
		return
	}
	b.boardDeltaUnsub = unsub
	b.mu.Unlock()
	b.debug("board_delta_watch_started", "session_id", sessionID, "board_id", board.BoardID())
}

func (b *ClaimsBridge) processBoardMutationDelta(sessionID string, board *claims.ClaimsBoard, delta claims.BoardMutationDelta) error {
	activeBoard, activeSession := b.currentBoard()
	if activeBoard != board || activeSession != sessionID {
		return nil
	}

	switch strings.TrimSpace(delta.Kind) {
	case "claim_created", "claim_posted":
		b.handleBoardMutationClaimPosted(sessionID, board, strings.TrimSpace(delta.ClaimID))
	case "testament_submitted", "testament_posted":
		b.handleBoardMutationTestamentPosted(sessionID, board, delta)
	}
	return nil
}

func (b *ClaimsBridge) handleBoardMutationClaimPosted(sessionID string, board *claims.ClaimsBoard, claimID string) {
	if board == nil || claimID == "" {
		return
	}
	if c, ok := board.CloneClaim(claimID); ok {
		b.handleClaimCreated(sessionID, c)
		return
	}
	if c := findClaim(board.Projection(), claimID); c != nil {
		b.handleClaimCreated(sessionID, c)
	}
}

func (b *ClaimsBridge) handleBoardMutationTestamentPosted(sessionID string, board *claims.ClaimsBoard, delta claims.BoardMutationDelta) {
	if board == nil || strings.TrimSpace(delta.TestamentID) == "" {
		return
	}
	// Claim-scoped testaments arrive through lifecycle deltas on the
	// claims bus. Free-floating presentation testaments do not, so this
	// in-process board subscription closes that visibility gap without
	// duplicating ordinary claim response rows.
	if strings.TrimSpace(delta.ClaimID) != "" {
		return
	}
	if t, ok := board.CloneTestament(strings.TrimSpace(delta.TestamentID)); ok {
		b.handleTestamentSubmitted(sessionID, t)
		return
	}
	if t := findTestament(board.Projection(), delta.TestamentID); t != nil {
		b.handleTestamentSubmitted(sessionID, t)
	}
}

func (b *ClaimsBridge) processClaimsEntry(_ context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil || entry.Delta == nil {
		b.debug("entry_drop_nil")
		return nil
	}
	board, sessionID := b.currentBoard()
	if board == nil || sessionID == "" || entry.Delta.DeltaSessionID() != sessionID {
		boardID := ""
		if board != nil {
			boardID = board.BoardID()
		}
		b.debug("entry_drop_session",
			"delta_kind", entry.Delta.DeltaKind(),
			"delta_key", entry.Delta.DeltaKey(),
			"delta_session", entry.Delta.DeltaSessionID(),
			"active_session", sessionID,
			"board_present", board != nil,
			"board_id", boardID,
		)
		return nil
	}
	b.debug("entry_received",
		"delta_kind", entry.Delta.DeltaKind(),
		"delta_key", entry.Delta.DeltaKey(),
		"delta_session", entry.Delta.DeltaSessionID(),
		"board_id", board.BoardID(),
		"node_claim", entry.Node.Claim != nil,
		"node_testament", entry.Node.Testament != nil,
		"node_validation", entry.Node.Validation != nil,
	)
	b.emitCounterSnapshot(sessionID, board)

	switch delta := entry.Delta.(type) {
	case claims.CanonicalDelta:
		b.handleCanonicalClaimsEntry(sessionID, board, entry, delta)
	case *claims.CanonicalDelta:
		if delta != nil {
			b.handleCanonicalClaimsEntry(sessionID, board, entry, *delta)
		}
	case claims.InboxDelta:
		b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
	case *claims.InboxDelta:
		if delta != nil {
			b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
		}
	case claims.ClaimStatusDelta:
		if delta.ToStatus.IsTerminal() {
			if !b.claimRegistered(delta.ClaimID) {
				b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
			}
			b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, terminalOutcome(delta.ToStatus), delta.Reason, "", "", delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
			b.handleClaimClosed(delta.ClaimID, terminalOutcome(delta.ToStatus))
		} else if delta.ToStatus.IsActive() {
			b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
			b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, "pending", delta.Reason, "", "", delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
		}
	case *claims.ClaimStatusDelta:
		if delta != nil {
			if delta.ToStatus.IsTerminal() {
				if !b.claimRegistered(delta.ClaimID) {
					b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
				}
				b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, terminalOutcome(delta.ToStatus), delta.Reason, "", "", delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
				b.handleClaimClosed(delta.ClaimID, terminalOutcome(delta.ToStatus))
			} else if delta.ToStatus.IsActive() {
				b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
				b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, "pending", delta.Reason, "", "", delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
			}
		}
	case claims.TestamentDelta:
		b.handleEntryTestament(sessionID, board, entry, delta.TestamentID)
	case *claims.TestamentDelta:
		if delta != nil {
			b.handleEntryTestament(sessionID, board, entry, delta.TestamentID)
		}
	case claims.ValidationDelta:
		b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, validationDeltaPeerStatus(delta), delta.Reason, "", delta.ValidationID, delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
		if delta.ClaimAutoAccepted {
			if !b.claimRegistered(delta.ClaimID) {
				b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
			}
			b.handleClaimClosed(delta.ClaimID, "success")
		}
	case *claims.ValidationDelta:
		if delta != nil {
			b.emitPeerInteractionForClaimID(sessionID, delta.ClaimID, validationDeltaPeerStatus(*delta), delta.Reason, "", delta.ValidationID, delta.Sequence, delta.DeltaKey(), delta.EmittedAt)
			if delta.ClaimAutoAccepted {
				if !b.claimRegistered(delta.ClaimID) {
					b.handleEntryClaim(sessionID, board, entry, delta.ClaimID)
				}
				b.handleClaimClosed(delta.ClaimID, "success")
			}
		}
	case claims.ClaimContextDelta:
		b.debug("legacy_claim_context_delta_ignored", "session_id", sessionID, "claim_id", delta.ClaimID)
	case *claims.ClaimContextDelta:
		if delta != nil {
			b.debug("legacy_claim_context_delta_ignored", "session_id", sessionID, "claim_id", delta.ClaimID)
		}
	case claims.TestamentContextDelta:
		b.handleTestamentContext(sessionID, testamentContextEvent{
			ClaimID:           delta.ClaimID,
			TestamentID:       delta.TestamentID,
			AgentID:           delta.AgentID,
			Context:           delta.Context,
			ContextTransition: delta.TransitionID,
			AccumulatorID:     delta.AccumulatorID,
		})
	case *claims.TestamentContextDelta:
		if delta != nil {
			b.handleTestamentContext(sessionID, testamentContextEvent{
				ClaimID:           delta.ClaimID,
				TestamentID:       delta.TestamentID,
				AgentID:           delta.AgentID,
				Context:           delta.Context,
				ContextTransition: delta.TransitionID,
				AccumulatorID:     delta.AccumulatorID,
			})
		}
	}
	return nil
}

func (b *ClaimsBridge) handleCanonicalClaimsEntry(sessionID string, board *claims.ClaimsBoard, entry *claims.GraphEntryPoint, delta claims.CanonicalDelta) {
	claimID := strings.TrimSpace(delta.ClaimID())
	switch delta.Action {
	case claims.DeltaActionClaimGenerated:
		b.handleEntryClaim(sessionID, board, entry, claimID)
	case claims.DeltaActionClaimPosted:
		b.handleEntryClaim(sessionID, board, entry, claimID)
		b.emitPeerInteractionForDelta(sessionID, claimID, "pending", canonicalClaimLifecycleDisplayMessage(delta), delta)
	case claims.DeltaActionTestamentPosted:
		b.handleEntryTestament(sessionID, board, entry, delta.TestamentID())
		b.emitPeerInteractionForDelta(sessionID, claimID, b.peerStatusForTestamentPosted(claimID), canonicalTestamentPostedContext(delta), delta)
	case claims.DeltaActionValidationEvaluated:
		b.emitPeerInteractionForDelta(sessionID, claimID, canonicalValidationPeerStatus(delta), canonicalValidationReason(delta), delta)
		if canonicalValidationAutoAccepted(delta) {
			if !b.claimRegistered(claimID) {
				b.handleEntryClaim(sessionID, board, entry, claimID)
			}
			b.handleClaimClosed(claimID, "success")
		}
	case claims.DeltaActionClaimSatisfied,
		claims.DeltaActionClaimValidationIncomplete,
		claims.DeltaActionClaimValidationFailed,
		claims.DeltaActionClaimValidationErrored,
		claims.DeltaActionClaimPostFailed,
		claims.DeltaActionClaimReceiptFailed,
		claims.DeltaActionClaimProgressFailed,
		claims.DeltaActionClaimTestamentGenerationFailed,
		claims.DeltaActionClaimTestamentAcknowledgementFailed:
		toStatus := delta.ClaimToStatus()
		if toStatus.IsTerminal() {
			if !b.claimRegistered(claimID) {
				b.handleEntryClaim(sessionID, board, entry, claimID)
			}
			b.emitPeerInteractionForDelta(sessionID, claimID, terminalOutcome(toStatus), canonicalClaimTransitionReason(delta), delta)
			b.handleClaimClosed(claimID, terminalOutcome(toStatus))
		} else if toStatus.IsActive() {
			b.handleEntryClaim(sessionID, board, entry, claimID)
			b.emitPeerInteractionForDelta(sessionID, claimID, "pending", canonicalClaimTransitionReason(delta), delta)
		}
	case claims.DeltaActionClaimProgressed,
		claims.DeltaActionClaimReceived,
		claims.DeltaActionClaimTestamentGenerated,
		claims.DeltaActionClaimTestamentAcknowledged,
		claims.DeltaActionClaimValidating:
		b.emitPeerInteractionForDelta(sessionID, claimID, "pending", canonicalClaimLifecycleDisplayMessage(delta), delta)
		b.handleClaimContext(sessionID, claimContextEvent{
			ClaimID:           claimID,
			AgentID:           delta.Actor.RouteKey(),
			Context:           canonicalClaimLifecycleDisplayMessage(delta),
			ContextTransition: canonicalProgressTransition(delta),
		})
	}
}

func canonicalValidationAutoAccepted(delta claims.CanonicalDelta) bool {
	validation, ok := delta.Context["validation"].(map[string]any)
	if !ok {
		return false
	}
	accepted, _ := validation["claim_auto_accepted"].(bool)
	return accepted
}

func canonicalValidationReason(delta claims.CanonicalDelta) string {
	validation, ok := delta.Context["validation"].(map[string]any)
	if !ok {
		return ""
	}
	reason, _ := validation["reason"].(string)
	return strings.TrimSpace(reason)
}

func canonicalValidationPeerStatus(delta claims.CanonicalDelta) string {
	validation, ok := delta.Context["validation"].(map[string]any)
	if !ok {
		return ""
	}
	status, _ := validation["status"].(string)
	switch strings.TrimSpace(status) {
	case string(claims.ValidationStatusFailed):
		return "failed"
	case string(claims.ValidationStatusPassed):
		return "done"
	default:
		return ""
	}
}

func validationDeltaPeerStatus(delta claims.ValidationDelta) string {
	switch strings.TrimSpace(delta.Verdict) {
	case string(claims.ValidationStatusFailed):
		return "failed"
	case string(claims.ValidationStatusPassed), string(claims.ValidationStatusSkipped):
		return "done"
	default:
		return ""
	}
}

func canonicalTestamentContext(delta claims.CanonicalDelta) string {
	raw, ok := delta.Context["testaments"].([]map[string]any)
	if ok && len(raw) > 0 {
		if contextValue, _ := raw[0]["context"].(string); strings.TrimSpace(contextValue) != "" {
			return strings.TrimSpace(contextValue)
		}
	}
	if generic, ok := delta.Context["testaments"].([]any); ok && len(generic) > 0 {
		if item, ok := generic[0].(map[string]any); ok {
			if contextValue, _ := item["context"].(string); strings.TrimSpace(contextValue) != "" {
				return strings.TrimSpace(contextValue)
			}
		}
	}
	return ""
}

func canonicalTestamentPostedContext(delta claims.CanonicalDelta) string {
	if contextValue := canonicalTestamentContext(delta); contextValue != "" {
		return contextValue
	}
	return "Response received"
}

func canonicalClaimTransitionReason(delta claims.CanonicalDelta) string {
	claim, ok := delta.Context["claim"].(map[string]any)
	if !ok {
		return ""
	}
	reason, _ := claim["reason"].(string)
	return strings.TrimSpace(reason)
}

func canonicalClaimLifecycleDisplayMessage(delta claims.CanonicalDelta) string {
	if message := canonicalProgressMessage(delta); message != "" {
		return message
	}
	if reason := canonicalClaimTransitionReason(delta); reason != "" {
		return reason
	}
	status, ok := delta.ClaimLifecycleStatus()
	if !ok {
		return ""
	}
	switch status {
	case claims.ClaimLifecycleGenerated:
		return "Generated"
	case claims.ClaimLifecyclePosted:
		return "Posted"
	case claims.ClaimLifecycleReceived:
		return "Received"
	case claims.ClaimLifecycleProgressed:
		return "Working"
	case claims.ClaimLifecycleTestamentGenerated:
		return "Response generated"
	case claims.ClaimLifecycleTestamentAcknowledged:
		return "Response received"
	case claims.ClaimLifecycleValidating:
		return "Validating"
	case claims.ClaimLifecycleSatisfied:
		return "Satisfied"
	case claims.ClaimLifecycleValidationIncomplete:
		return "Validation incomplete"
	case claims.ClaimLifecycleValidationFailed:
		return "Validation failed"
	case claims.ClaimLifecycleValidationErrored:
		return "Validation errored"
	case claims.ClaimLifecyclePostFailed:
		return "Post failed"
	case claims.ClaimLifecycleReceiptFailed:
		return "Receipt failed"
	case claims.ClaimLifecycleProgressFailed:
		return "Progress failed"
	case claims.ClaimLifecycleTestamentGenerationFailed:
		return "Response generation failed"
	case claims.ClaimLifecycleTestamentAcknowledgementFailed:
		return "Response acknowledgement failed"
	case claims.ClaimLifecycleGenerationFailed:
		return "Generation failed"
	default:
		return strings.ReplaceAll(string(status), "_", " ")
	}
}

func canonicalProgressMessage(delta claims.CanonicalDelta) string {
	progress, ok := delta.Context["progress"].(map[string]any)
	if !ok {
		return ""
	}
	if message, _ := progress["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	state, _ := progress["state"].(string)
	return strings.TrimSpace(state)
}

func canonicalProgressTransition(delta claims.CanonicalDelta) int64 {
	progress, ok := delta.Context["progress"].(map[string]any)
	if !ok {
		return 0
	}
	switch value := progress["transition"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func (b *ClaimsBridge) currentBoard() (*claims.ClaimsBoard, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.board, b.activeSession
}

// ResolveArtifact returns a defensive copy of an artifact from the active
// session's claims board. It first checks the canonical board storage so
// projection truncation never leaks into user-visible hydration, then falls
// back to the bridge's live artifact index and immutable projection.
func (b *ClaimsBridge) ResolveArtifact(sessionID, artifactID string) (*claims.Artifact, bool) {
	if b == nil {
		return nil, false
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil, false
	}
	b.mu.Lock()
	activeSession := b.activeSession
	if strings.TrimSpace(sessionID) == "" {
		sessionID = activeSession
	}
	if sessionID != activeSession {
		b.mu.Unlock()
		return nil, false
	}
	board := b.board
	b.mu.Unlock()
	if board != nil {
		if art, ok := board.CloneArtifact(artifactID); ok {
			b.mu.Lock()
			stillActive := sessionID == b.activeSession && board == b.board
			b.mu.Unlock()
			if stillActive {
				return art, true
			}
			return nil, false
		}
	}
	b.mu.Lock()
	if sessionID != b.activeSession {
		b.mu.Unlock()
		return nil, false
	}
	if art := b.artifactByID[artifactID]; art != nil {
		cp := cloneArtifact(art)
		b.mu.Unlock()
		return cp, true
	}
	board = b.board
	b.mu.Unlock()
	if board == nil {
		return nil, false
	}
	if art := findArtifact(board.Projection(), artifactID); art != nil {
		return cloneArtifact(art), true
	}
	return nil, false
}

func (b *ClaimsBridge) handleEntryClaim(sessionID string, board *claims.ClaimsBoard, entry *claims.GraphEntryPoint, claimID string) {
	if entry != nil && entry.Node.Claim != nil {
		b.handleClaimCreated(sessionID, entry.Node.Claim)
		return
	}
	if c := findClaim(board.Projection(), claimID); c != nil {
		b.handleClaimCreated(sessionID, c)
	}
}

func (b *ClaimsBridge) handleEntryTestament(sessionID string, board *claims.ClaimsBoard, entry *claims.GraphEntryPoint, testamentID string) {
	if board != nil {
		if t, ok := board.CloneTestament(strings.TrimSpace(testamentID)); ok {
			b.handleTestamentSubmitted(sessionID, t)
			return
		}
	}
	if entry != nil && entry.Node.Testament != nil {
		b.handleTestamentSubmitted(sessionID, entry.Node.Testament)
		return
	}
	if t := findTestament(board.Projection(), testamentID); t != nil {
		b.handleTestamentSubmitted(sessionID, t)
	}
}

func (b *ClaimsBridge) emitCounterSnapshot(sessionID string, board *claims.ClaimsBoard) {
	if board == nil {
		return
	}
	summary := board.Summary()
	b.mu.Lock()
	changed := summary.Accepted != b.lastAccepted || summary.Total != b.lastTotal
	b.lastAccepted = summary.Accepted
	b.lastTotal = summary.Total
	b.mu.Unlock()
	if !changed {
		return
	}
	b.enqueue(msg.ClaimsProjectionMsg{
		SessionID:     sessionID,
		AcceptedCount: summary.Accepted,
		TotalClaims:   summary.Total,
	})
}

func (b *ClaimsBridge) handleClaimCreated(sessionID string, c *claims.Claim) {
	if c == nil || strings.TrimSpace(c.ID) == "" {
		b.debug("claim_created_drop_nil", "session_id", sessionID)
		return
	}

	issuer := strings.TrimSpace(claims.IssuerAgentID(c.Relations))
	subject := strings.TrimSpace(claims.SubjectAgentID(c.Relations))
	causedBy := relationID(c.Relations, claims.RelationshipCausedBy)
	handoffFrom := claims.HandoffFromClaimID(c.Relations)
	ownerForResolver := cycleOwnerForClaim(c)

	var toEmit []any
	b.mu.Lock()
	if existing := b.claimMeta[c.ID]; existing.ClaimID != "" && b.resolver.CycleForClaim(c.ID) != "" {
		if isPeerActionType(existing.ActionType) && strings.TrimSpace(b.claimToPeerRow[c.ID]) == "" {
			meta := b.metaForClaimLocked(c.ID)
			if m := b.claimPeerInteractionMsgLocked(sessionID, c.ID, meta, "pending", "", "", "", c.Sequence, "", nonZeroTime(c.Created)); m != nil {
				toEmit = append(toEmit, *m)
			}
		}
		b.mu.Unlock()
		for _, m := range toEmit {
			b.enqueue(m)
		}
		b.debug("claim_created_duplicate",
			"session_id", sessionID,
			"claim_id", c.ID,
			"cycle_id", existing.CycleID,
			"owner", existing.OwnerAgentID,
		)
		return
	}
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
	suppressChat := claimSuppressChat(c)
	uiState := claimInitialUIState(c)
	panelReason := claimPanelReason(c)
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
		StreamCorrelationID: claimUIStreamCorrelation(c),
		SuppressChat:        suppressChat,
		UIState:             uiState,
	}
	b.claimMeta[c.ID] = meta

	if outcome.PredecessorClosed != nil {
		toEmit = append(toEmit, claimsAgentClosedMsg(sessionID, outcome.PredecessorClosed, ""))
	}
	if outcome.CycleOpened != nil {
		b.debug("claim_cycle_open",
			"session_id", sessionID,
			"claim_id", c.ID,
			"cycle_id", outcome.CycleOpened.CycleID,
			"owner", outcome.CycleOpened.OwnerAgentID,
			"subject", subject,
			"issuer", issuer,
			"title", strings.TrimSpace(c.Title),
			"action_type", string(c.ActionType),
			"suppress_chat", suppressChat,
			"ui_state", uiState,
		)
		toEmit = append(toEmit, msg.ClaimsAgentStatusMsg{
			AgentID:             outcome.CycleOpened.OwnerAgentID,
			SessionID:           sessionID,
			Active:              true,
			CycleID:             outcome.CycleOpened.CycleID,
			OpenCount:           len(outcome.CycleOpened.openClaims),
			Reason:              panelReason,
			State:               uiState,
			SuppressChat:        suppressChat,
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
	if m := b.claimPeerInteractionMsgLocked(sessionID, c.ID, meta, "pending", "", "", "", c.Sequence, "", nonZeroTime(c.Created)); m != nil {
		toEmit = append(toEmit, *m)
	}
	b.mu.Unlock()

	for _, m := range toEmit {
		b.enqueue(m)
	}
	if len(toEmit) == 0 {
		b.debug("claim_created_no_emit",
			"session_id", sessionID,
			"claim_id", c.ID,
			"cycle_id", cycleID,
			"owner", cycleOwner,
			"subject", subject,
			"caused_by", causedBy,
			"handoff_from", handoffFrom,
		)
	}
}

func (b *ClaimsBridge) handleClaimClosed(claimID, outcome string) {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return
	}
	var closeMsg *msg.ClaimsAgentStatusMsg
	var suppressChat bool
	var uiState string
	b.mu.Lock()
	if meta := b.claimMeta[claimID]; meta.ClaimID != "" {
		suppressChat = meta.SuppressChat
		uiState = meta.UIState
	}
	if st := b.resolver.onClaimClosed(claimID); st != nil {
		m := claimsAgentClosedMsg(b.activeSession, st, outcome)
		m.SuppressChat = suppressChat
		m.State = uiState
		closeMsg = &m
	}
	b.mu.Unlock()
	if closeMsg != nil {
		b.debug("claim_cycle_close",
			"session_id", closeMsg.SessionID,
			"claim_id", claimID,
			"cycle_id", closeMsg.CycleID,
			"owner", closeMsg.AgentID,
			"outcome", outcome,
		)
		b.enqueue(*closeMsg)
	} else {
		b.debug("claim_close_no_cycle", "claim_id", claimID, "outcome", outcome)
	}
}

func (b *ClaimsBridge) handleTestamentSubmitted(sessionID string, t *claims.Testament) {
	if t == nil {
		return
	}
	if !testamentLifecycleDisplayable(t.LifecycleStatus) {
		return
	}
	claimID := claims.ClaimIDFromRelations(t.Relations)
	b.ensureClaimRegisteredFromProjection(sessionID, claimID)
	if m := b.claimPresentationMsgForTestament(sessionID, claimID, t); m != nil {
		b.enqueue(*m)
	}
	for i := range t.Artifacts {
		art := t.Artifacts[i]
		b.OnArtifactAdded(claimID, t.AgentID, sessionID, art)
	}
	if m := b.claimTestamentResponseMsg(sessionID, claimID, t); m != nil {
		b.enqueue(*m)
	}
	b.emitPeerInteractionForClaimID(sessionID, claimID, peerCompletionOutcomeForTestament(t), strings.TrimSpace(t.Summary), strings.TrimSpace(t.ID), "", t.Sequence, "", nonZeroTime(t.Created))
}

func peerCompletionOutcomeForTestament(t *claims.Testament) string {
	if t == nil {
		return "success"
	}
	switch claims.DeriveTestamentVerdict(t.Artifacts) {
	case claims.TestamentVerdictError:
		return "failure"
	default:
		return "success"
	}
}

func (b *ClaimsBridge) emitPeerInteractionForDelta(sessionID, claimID, status, context string, delta claims.CanonicalDelta) {
	b.emitPeerInteractionForClaimID(
		sessionID,
		claimID,
		status,
		context,
		delta.TestamentID(),
		delta.ValidationID(),
		delta.Sequence,
		delta.Key,
		delta.OccurredAt,
	)
}

func (b *ClaimsBridge) emitPeerInteractionForClaimID(sessionID, claimID, status, context, testamentID, validationID string, sequence uint64, deltaKey string, occurredAt time.Time) {
	claimID = strings.TrimSpace(claimID)
	if b == nil || claimID == "" {
		return
	}
	b.ensureClaimRegisteredFromProjection(sessionID, claimID)
	var out *msg.ClaimPeerInteractionMsg
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	if m := b.claimPeerInteractionMsgLocked(sessionID, claimID, meta, status, context, testamentID, validationID, sequence, deltaKey, occurredAt); m != nil {
		out = m
	}
	b.mu.Unlock()
	if out != nil {
		b.enqueue(*out)
	}
}

func (b *ClaimsBridge) peerStatusForTestamentPosted(claimID string) string {
	if b == nil {
		return "done"
	}
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	b.mu.Unlock()
	switch strings.TrimSpace(meta.ActionType) {
	case string(claims.ActionTypeChallenge):
		return "pending"
	default:
		return "done"
	}
}

func (b *ClaimsBridge) claimPeerInteractionMsgLocked(sessionID, claimID string, meta claimMeta, status, contextValue, testamentID, validationID string, sequence uint64, deltaKey string, occurredAt time.Time) *msg.ClaimPeerInteractionMsg {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" || !isPeerActionType(meta.ActionType) || meta.SuppressChat {
		return nil
	}
	if meta.CycleID == "" || meta.CycleID == claimID {
		return nil
	}
	subject := strings.TrimSpace(meta.TargetAgentID)
	issuer := strings.TrimSpace(meta.IssuerAgentID)
	if subject == "" {
		return nil
	}
	if issuer != "" && issuer == subject {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	rowID := bridgeClaimPeerInteractionRowID(claimID)
	if rowID == "" {
		return nil
	}
	b.claimToPeerRow[claimID] = rowID
	return &msg.ClaimPeerInteractionMsg{
		SessionID:      firstNonBlank(strings.TrimSpace(sessionID), meta.SessionID, b.activeSession),
		CycleID:        meta.CycleID,
		ClaimID:        claimID,
		ActionType:     meta.ActionType,
		IssuerAgentID:  issuer,
		SubjectAgentID: subject,
		Title:          meta.Title,
		Context:        strings.TrimSpace(contextValue),
		Status:         firstNonBlank(strings.TrimSpace(status), "pending"),
		TestamentID:    strings.TrimSpace(testamentID),
		ValidationID:   strings.TrimSpace(validationID),
		Sequence:       sequence,
		DeltaKey:       strings.TrimSpace(deltaKey),
		OccurredAt:     occurredAt,
		SuppressChat:   meta.SuppressChat,
	}
}

func (b *ClaimsBridge) parentRowIDForClaimLocked(claimID string) string {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return ""
	}
	if rowID := strings.TrimSpace(b.claimToPeerRow[claimID]); rowID != "" {
		return rowID
	}
	meta := b.metaForClaimLocked(claimID)
	if b.claimPeerInteractionMsgLocked(meta.SessionID, claimID, meta, "pending", "", "", "", 0, "", time.Time{}) == nil {
		return ""
	}
	return strings.TrimSpace(b.claimToPeerRow[claimID])
}

func bridgeClaimPeerInteractionRowID(claimID string) string {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return ""
	}
	return "claim-peer:" + claimID
}

func isPeerActionType(actionType string) bool {
	switch strings.TrimSpace(actionType) {
	case string(claims.ActionTypeConsultation), string(claims.ActionTypeChallenge), string(claims.ActionTypeGuardianCheck):
		return true
	default:
		return false
	}
}

func (b *ClaimsBridge) handleClaimContext(sessionID string, event claimContextEvent) {
	claimID := strings.TrimSpace(event.ClaimID)
	if claimID == "" {
		return
	}
	b.ensureClaimRegisteredFromProjection(sessionID, claimID)
	var out *msg.ClaimContextMsg
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID != "" {
		state := firstNonBlank(b.latestStateByClaim[claimID], claimContextUIState(meta, event.Context))
		actor := claimContextActor(meta, event.AgentID)
		parentRowID := b.parentRowIDForClaimLocked(claimID)
		out = &msg.ClaimContextMsg{
			SessionID:         sessionID,
			ClaimID:           claimID,
			OwnerAgentID:      actor,
			CycleID:           meta.CycleID,
			ParentRowID:       parentRowID,
			Context:           event.Context,
			ContextTransition: event.ContextTransition,
			SuppressChat:      meta.SuppressChat,
			State:             state,
		}
	}
	b.mu.Unlock()
	if out != nil {
		b.debug("claim_context_emit",
			"session_id", sessionID,
			"claim_id", claimID,
			"cycle_id", out.CycleID,
			"owner", out.OwnerAgentID,
			"parent_row_id", out.ParentRowID,
			"context", event.Context,
			"transition", event.ContextTransition,
			"suppress_chat", out.SuppressChat,
			"state", out.State,
		)
		b.enqueue(*out)
	} else {
		b.debug("claim_context_drop_no_cycle",
			"session_id", sessionID,
			"claim_id", claimID,
			"context", event.Context,
			"transition", event.ContextTransition,
		)
	}
}

func (b *ClaimsBridge) handleTestamentContext(sessionID string, event testamentContextEvent) {
	claimID := strings.TrimSpace(event.ClaimID)
	if claimID == "" {
		return
	}
	b.ensureClaimRegisteredFromProjection(sessionID, claimID)
	var out *msg.TestamentContextMsg
	b.mu.Lock()
	meta := b.metaForClaimLocked(claimID)
	if meta.CycleID != "" {
		out = &msg.TestamentContextMsg{
			SessionID:         sessionID,
			AccumulatorID:     strings.TrimSpace(event.AccumulatorID),
			TestamentID:       strings.TrimSpace(event.TestamentID),
			ClaimID:           claimID,
			AgentID:           strings.TrimSpace(event.AgentID),
			CycleID:           meta.CycleID,
			ParentRowID:       b.parentRowIDForClaimLocked(claimID),
			Context:           event.Context,
			ContextTransition: event.ContextTransition,
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
	activeBoard, activeSession := b.currentBoard()
	if sessionID == "" {
		sessionID = activeSession
	}
	b.ensureClaimRegisteredFromProjection(sessionID, claimID)

	b.mu.Lock()
	if sessionID == "" || sessionID != b.activeSession {
		b.mu.Unlock()
		return
	}
	art := cloneArtifact(artifact)
	if claimMetadataBool(art.Metadata, claims.ArtifactMetadataContentTruncated) && activeBoard != nil {
		if full, ok := activeBoard.CloneArtifact(strings.TrimSpace(art.ID)); ok && full != nil {
			art = full
		}
	}
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
	var presentationErr error
	if art.Presentation != nil {
		presentationErr = claims.ValidatePresentation(art.Presentation)
	}

	var out []any
	var diagnostics []presentationDiagnosticRecord
	switch {
	case art.Kind == claims.ArtifactKindAgentState:
		out = append(out, b.handleAgentStateArtifactLocked(sessionID, claimID, art)...)
	case art.Kind == claims.ArtifactKindResponseText:
		if m := b.claimResponseTextMsgLocked(sessionID, claimID, art); m != nil {
			out = append(out, *m)
		}
	case presentationErr != nil:
		b.recordPresentationInvalidLocked("artifact", strings.TrimSpace(art.ID), art.Presentation, "invalid_presentation", presentationErr.Error())
		if diagnostic, fallback := b.presentationDiagnosticForArtifactLocked(sessionID, claimID, art, "invalid_presentation", "invalid presentation metadata"); diagnostic != nil {
			diagnostics = append(diagnostics, *diagnostic)
			if fallback != nil {
				out = append(out, *fallback)
			}
		}
	case claims.IsPresentableToUserChat(art.Presentation) && !isPresentationLifecycleArtifactKind(art.Kind):
		surface, format := presentationMetricLabels(art.Presentation)
		b.observePresentationMetricLocked(claimsPresentationArtifactsSeen, surface, format, "")
		if err := validateBridgeRenderablePresentation(art.Presentation); err != nil {
			b.recordPresentationInvalidLocked("artifact", strings.TrimSpace(art.ID), art.Presentation, "unsupported_format", err.Error())
			if diagnostic, fallback := b.presentationDiagnosticForArtifactLocked(sessionID, claimID, art, "unsupported_format", err.Error()); diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
				if fallback != nil {
					out = append(out, *fallback)
				}
			}
			break
		}
		if m, msgDiagnostics := b.claimPresentationMsgLocked(sessionID, claimID, art); m != nil {
			diagnostics = append(diagnostics, msgDiagnostics...)
			out = append(out, *m)
		}
	case isVisibleStartedArtifactKind(art.Kind):
		if m := b.routeStartedArtifactLocked(sessionID, claimID, art); m != nil {
			out = append(out, *m)
		}
	case isCompletionArtifact(art):
		out = append(out, b.routeCompletedArtifactLocked(sessionID, art)...)
	}
	b.mu.Unlock()

	b.recordPresentationDiagnostics(diagnostics)
	for _, m := range out {
		b.enqueue(m)
	}
}

func (b *ClaimsBridge) claimRegistered(claimID string) bool {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return false
	}
	b.mu.Lock()
	resolver := b.resolver
	b.mu.Unlock()
	if resolver == nil {
		return false
	}
	return resolver.CycleForClaim(claimID) != ""
}

func (b *ClaimsBridge) ensureClaimRegisteredFromProjection(sessionID, claimID string) {
	claimID = strings.TrimSpace(claimID)
	if b == nil || claimID == "" {
		return
	}
	board, activeSession := b.currentBoard()
	if sessionID == "" {
		sessionID = activeSession
	}
	if board == nil || activeSession == "" || sessionID != activeSession || b.claimRegistered(claimID) {
		return
	}
	if c := findClaim(board.Projection(), claimID); c != nil {
		b.handleClaimCreated(sessionID, c)
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
	parentRowID := b.parentRowIDForClaimLocked(claimID)
	b.emittedStartedArtifacts[artifactID] = struct{}{}
	return &msg.ClaimArtifactAddedMsg{
		ArtifactID:     artifactID,
		CycleID:        meta.CycleID,
		ParentRowID:    parentRowID,
		ClaimID:        claimID,
		OwnerAgentID:   meta.OwnerAgentID,
		OwnerAgentType: meta.OwnerAgentType,
		TargetAgentID:  meta.TargetAgentID,
		AgentID:        firstNonBlank(strings.TrimSpace(art.AgentID), claimContextActor(meta, ""), meta.OwnerAgentID),
		Kind:           strings.TrimSpace(art.Kind),
		Reference:      strings.TrimSpace(art.Reference),
		Metadata:       cloneMetadata(art.Metadata),
		CreatedAt:      art.Created,
		SuppressChat:   meta.SuppressChat,
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
	suppressChat := false
	if claimID := b.artifactClaim[startID]; claimID != "" {
		suppressChat = b.metaForClaimLocked(claimID).SuppressChat
	}
	out := []any{msg.ClaimArtifactCompletedMsg{
		StartArtifactID: startID,
		CycleID:         cycleID,
		Outcome:         artifactOutcome(art),
		Duration:        artifactDuration(art),
		Summary:         artifactSummary(art),
		Metadata:        cloneMetadata(art.Metadata),
		CompletedAt:     nonZeroTime(art.Created),
		SuppressChat:    suppressChat,
	}}
	if drained && cycle != nil {
		out = append(out, claimsAgentClosedMsg(sessionID, cycle, ""))
	}
	return out
}

func (b *ClaimsBridge) handleAgentStateArtifactLocked(sessionID, claimID string, art *claims.Artifact) []any {
	if artClaimID := claimMetadataString(art.Metadata, "claim_id"); artClaimID != "" {
		claimID = artClaimID
	}
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
	agentID := strings.TrimSpace(art.AgentID)
	if meta.UIState == "classifying" && meta.OwnerAgentID == "guide" && agentID != "" && agentID != "guide" {
		b.debug("agent_state_ignored_foreign_guide_classification",
			"session_id", sessionID,
			"claim_id", claimID,
			"artifact_id", strings.TrimSpace(art.ID),
			"artifact_agent_id", agentID,
		)
		return nil
	}
	return []any{msg.ClaimContextMsg{
		SessionID:    sessionID,
		ClaimID:      claimID,
		OwnerAgentID: claimContextActor(meta, agentID),
		CycleID:      meta.CycleID,
		ParentRowID:  b.parentRowIDForClaimLocked(claimID),
		Context:      detail,
		State:        state,
		SuppressChat: meta.SuppressChat,
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
		SessionID:    sessionID,
		CycleID:      meta.CycleID,
		ClaimID:      claimID,
		ParentRowID:  b.parentRowIDForClaimLocked(claimID),
		AgentID:      firstNonBlank(strings.TrimSpace(art.AgentID), claimContextActor(meta, ""), meta.OwnerAgentID),
		Content:      content,
		CreatedAt:    nonZeroTime(art.Created),
		SuppressChat: meta.SuppressChat,
	}
}

func (b *ClaimsBridge) claimPresentationMsgForTestament(sessionID, claimID string, t *claims.Testament) *msg.ClaimPresentationMsg {
	if b == nil || t == nil {
		return nil
	}
	b.mu.Lock()
	out, diagnostics := b.claimPresentationMsgForTestamentLocked(sessionID, claimID, t)
	b.mu.Unlock()
	b.recordPresentationDiagnostics(diagnostics)
	return out
}

func (b *ClaimsBridge) claimPresentationMsgForTestamentLocked(sessionID, claimID string, t *claims.Testament) (*msg.ClaimPresentationMsg, []presentationDiagnosticRecord) {
	if t == nil {
		return nil, nil
	}
	var diagnostics []presentationDiagnosticRecord
	if t.Presentation != nil {
		if err := claims.ValidatePresentation(t.Presentation); err != nil {
			b.recordPresentationInvalidLocked("testament", strings.TrimSpace(t.ID), t.Presentation, "invalid_presentation", err.Error())
			if diagnostic, fallback := b.presentationDiagnosticForTestamentLocked(sessionID, claimID, t, "invalid_presentation", err.Error()); diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
				return fallback, diagnostics
			}
			return nil, diagnostics
		}
	}
	if !claims.IsPresentableToUserChat(t.Presentation) {
		return nil, nil
	}
	sourceID := strings.TrimSpace(t.ID)
	if sourceID == "" {
		b.recordPresentationDropLocked("testament", "", t.Presentation, "missing_source_id")
		return nil, nil
	}
	if err := validateBridgeRenderablePresentation(t.Presentation); err != nil {
		b.recordPresentationInvalidLocked("testament", sourceID, t.Presentation, "unsupported_format", err.Error())
		if diagnostic, fallback := b.presentationDiagnosticForTestamentLocked(sessionID, claimID, t, "unsupported_format", err.Error()); diagnostic != nil {
			diagnostics = append(diagnostics, *diagnostic)
			return fallback, diagnostics
		}
		return nil, diagnostics
	}
	content := strings.TrimSpace(t.Summary)
	if content == "" {
		b.recordPresentationDropLocked("testament", sourceID, t.Presentation, "empty_content")
		b.debug("presentation_skip_empty_testament", "testament_id", sourceID)
		return nil, nil
	}
	content = safeClaimPresentationContent(sourceID, content, presentationFormat(claims.NormalizePresentation(t.Presentation)))
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		claimID = claims.ClaimIDFromRelations(t.Relations)
	}
	meta := b.metaForClaimLocked(claimID)
	if meta.SuppressChat {
		b.recordPresentationDropLocked("testament", sourceID, t.Presentation, "suppressed_chat")
		return nil, nil
	}
	p := claims.NormalizePresentation(t.Presentation)
	cycleID := firstNonBlank(meta.CycleID, claimID)
	if cycleID == "" {
		b.recordPresentationDropLocked("testament", sourceID, p, "missing_cycle")
		return nil, nil
	}
	if !b.shouldEmitPresentationLocked("testament", sourceID, presentationReplaceKey(p), t.Sequence, p) {
		return nil, nil
	}
	b.observePresentationMetricLocked(claimsPresentationMessagesEmitted, string(claims.PresentationSurfaceChat), presentationFormat(p), "")
	return &msg.ClaimPresentationMsg{
		SessionID:   sessionID,
		CycleID:     cycleID,
		ClaimID:     claimID,
		SourceType:  "testament",
		SourceID:    sourceID,
		TestamentID: sourceID,
		AgentID:     firstNonBlank(strings.TrimSpace(t.AgentID), claimContextActor(meta, ""), meta.OwnerAgentID),
		Title:       presentationTitle(p, "Testament"),
		Content:     content,
		Format:      presentationFormat(p),
		Placement:   presentationPlacement(p),
		ReplaceKey:  presentationReplaceKey(p),
		CreatedAt:   nonZeroTime(t.Created),
		Sequence:    t.Sequence,
	}, nil
}

func (b *ClaimsBridge) claimPresentationMsgLocked(sessionID, claimID string, art *claims.Artifact) (*msg.ClaimPresentationMsg, []presentationDiagnosticRecord) {
	if art == nil || !claims.IsPresentableToUserChat(art.Presentation) {
		return nil, nil
	}
	var diagnostics []presentationDiagnosticRecord
	sourceID := strings.TrimSpace(art.ID)
	if sourceID == "" {
		b.recordPresentationDropLocked("artifact", "", art.Presentation, "missing_source_id")
		return nil, nil
	}
	content, truncated := b.presentationArtifactContentLocked(art)
	if strings.TrimSpace(content) == "" {
		content = claimMetadataString(art.Metadata, "content", "text", "markdown", "body")
	}
	if strings.TrimSpace(content) == "" {
		b.recordPresentationDropLocked("artifact", sourceID, art.Presentation, "empty_content")
		b.debug("presentation_skip_empty_artifact", "artifact_id", sourceID, "kind", art.Kind)
		return nil, nil
	}
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		claimID = b.claimIDForArtifactLocked(art)
	}
	meta := b.metaForClaimLocked(claimID)
	if meta.SuppressChat {
		b.recordPresentationDropLocked("artifact", sourceID, art.Presentation, "suppressed_chat")
		return nil, nil
	}
	p := claims.NormalizePresentation(art.Presentation)
	cycleID := artifactPresentationCycleID(meta, claimID, art)
	if cycleID == "" {
		b.recordPresentationDropLocked("artifact", sourceID, p, "missing_cycle")
		return nil, nil
	}
	if !b.shouldEmitPresentationLocked("artifact", sourceID, presentationReplaceKey(p), art.Sequence, p) {
		return nil, nil
	}
	format := presentationFormat(p)
	if truncated {
		format = string(claims.PresentationFormatText)
		b.observePresentationMetricLocked(claimsPresentationDereferenceFailures, string(claims.PresentationSurfaceChat), format, "content_truncated")
		if diagnostic, _ := b.presentationDiagnosticForArtifactLocked(sessionID, claimID, art, "content_truncated", content); diagnostic != nil {
			diagnostics = append(diagnostics, *diagnostic)
		}
	}
	content = safeClaimPresentationContent(sourceID, content, format)
	b.observePresentationMetricLocked(claimsPresentationMessagesEmitted, string(claims.PresentationSurfaceChat), format, "")
	return &msg.ClaimPresentationMsg{
		SessionID:   sessionID,
		CycleID:     cycleID,
		ClaimID:     claimID,
		SourceType:  "artifact",
		SourceID:    sourceID,
		TestamentID: strings.TrimSpace(art.TestamentID),
		AgentID:     firstNonBlank(strings.TrimSpace(art.AgentID), claimContextActor(meta, ""), meta.OwnerAgentID),
		Title:       presentationTitle(p, strings.TrimSpace(art.Kind)),
		Content:     content,
		Format:      format,
		Placement:   presentationPlacement(p),
		ReplaceKey:  presentationReplaceKey(p),
		Metadata:    safePresentationMetadata(art.Metadata),
		CreatedAt:   nonZeroTime(art.Created),
		Sequence:    art.Sequence,
	}, diagnostics
}

func (b *ClaimsBridge) claimIDForArtifactLocked(art *claims.Artifact) string {
	if art == nil {
		return ""
	}
	if claimID := b.artifactClaim[strings.TrimSpace(art.ID)]; claimID != "" {
		return claimID
	}
	testamentID := strings.TrimSpace(art.TestamentID)
	if testamentID == "" || b.board == nil {
		return ""
	}
	if t, ok := b.board.CloneTestament(testamentID); ok {
		return claims.ClaimIDFromRelations(t.Relations)
	}
	if t := findTestament(b.board.Projection(), testamentID); t != nil {
		return claims.ClaimIDFromRelations(t.Relations)
	}
	return ""
}

func presentationSourceKey(sourceType, sourceID string) string {
	return strings.TrimSpace(sourceType) + "|" + strings.TrimSpace(sourceID)
}

func (b *ClaimsBridge) shouldEmitPresentationLocked(sourceType, sourceID, replaceKey string, sequence uint64, presentation *claims.Presentation) bool {
	sourceType = strings.TrimSpace(sourceType)
	sourceID = strings.TrimSpace(sourceID)
	if sourceType == "" || sourceID == "" {
		b.recordPresentationDropLocked(sourceType, sourceID, presentation, "missing_source_id")
		return false
	}
	sourceKey := presentationSourceKey(sourceType, sourceID)
	if _, emitted := b.emittedPresentations[sourceKey]; emitted {
		b.recordPresentationDropLocked(sourceType, sourceID, presentation, "duplicate_source")
		return false
	}
	state := presentationEmissionState{Sequence: sequence, SourceID: sourceID}
	replaceKey = strings.TrimSpace(replaceKey)
	if replaceKey != "" {
		if prior, ok := b.presentationReplacements[replaceKey]; ok && !presentationStateNewer(state, prior) {
			b.recordPresentationDropLocked(sourceType, sourceID, presentation, "stale_replacement")
			return false
		}
		if _, ok := b.presentationReplacements[replaceKey]; ok {
			surface, format := presentationMetricLabels(presentation)
			b.observePresentationMetricLocked(claimsPresentationReplacements, surface, format, "")
		}
		b.presentationReplacements[replaceKey] = state
	}
	b.emittedPresentations[sourceKey] = state
	return true
}

func presentationStateNewer(next, prior presentationEmissionState) bool {
	if next.Sequence != prior.Sequence {
		return next.Sequence > prior.Sequence
	}
	return strings.TrimSpace(next.SourceID) > strings.TrimSpace(prior.SourceID)
}

func presentationArtifactContent(art *claims.Artifact) (string, bool) {
	if art == nil {
		return "", false
	}
	if claimMetadataBool(art.Metadata, claims.ArtifactMetadataContentTruncated) {
		size := claimMetadataInt(art.Metadata, claims.ArtifactMetadataContentSize)
		if size > 0 {
			return fmt.Sprintf("Artifact %s is user-visible, but its inline projection was truncated (%d bytes).", strings.TrimSpace(art.ID), size), true
		}
		return fmt.Sprintf("Artifact %s is user-visible, but its inline projection was truncated.", strings.TrimSpace(art.ID)), true
	}
	return art.Reference, false
}

func (b *ClaimsBridge) presentationArtifactContentLocked(art *claims.Artifact) (string, bool) {
	if art == nil {
		return "", false
	}
	if claimMetadataBool(art.Metadata, claims.ArtifactMetadataContentTruncated) {
		if full, ok := b.fullArtifactCopyLocked(strings.TrimSpace(art.ID)); ok && full != nil {
			if strings.TrimSpace(full.Reference) != "" {
				return full.Reference, false
			}
		}
		if strings.TrimSpace(art.Kind) == claims.ArtifactKindPlanMarkdown {
			if strings.TrimSpace(art.Reference) != "" {
				return art.Reference, false
			}
			return claimMetadataString(art.Metadata, "content", "text", "markdown", "body"), false
		}
	}
	return presentationArtifactContent(art)
}

func (b *ClaimsBridge) fullArtifactCopyLocked(artifactID string) (*claims.Artifact, bool) {
	if b == nil || b.board == nil {
		return nil, false
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil, false
	}
	return b.board.CloneArtifact(artifactID)
}

func presentationTitle(p *claims.Presentation, fallback string) string {
	if p != nil && strings.TrimSpace(p.Title) != "" {
		return strings.TrimSpace(p.Title)
	}
	return strings.TrimSpace(fallback)
}

func presentationFormat(p *claims.Presentation) string {
	if p != nil && strings.TrimSpace(string(p.Format)) != "" {
		return strings.TrimSpace(string(p.Format))
	}
	return string(claims.PresentationFormatText)
}

func presentationPlacement(p *claims.Presentation) string {
	if p != nil && strings.TrimSpace(string(p.Placement)) != "" {
		return strings.TrimSpace(string(p.Placement))
	}
	return string(claims.PresentationPlacementInline)
}

func presentationReplaceKey(p *claims.Presentation) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.ReplaceKey)
}

func artifactPresentationCycleID(meta claimMeta, claimID string, art *claims.Artifact) string {
	if art != nil && strings.TrimSpace(art.Kind) == claims.ArtifactKindPlanMarkdown {
		if cycleID := claimMetadataString(art.Metadata, "stream_correlation_id", "correlation_id", "request_correlation_id", "cycle_id"); cycleID != "" {
			return cycleID
		}
	}
	if cycleID := firstNonBlank(meta.CycleID, claimID); cycleID != "" {
		return cycleID
	}
	if art == nil {
		return ""
	}
	if cycleID := claimMetadataString(art.Metadata, "stream_correlation_id", "correlation_id", "request_correlation_id", "cycle_id"); cycleID != "" {
		return cycleID
	}
	if strings.TrimSpace(art.Kind) == claims.ArtifactKindPlanMarkdown {
		if planID := claimMetadataString(art.Metadata, "plan_id"); planID != "" {
			return "plan:" + planID
		}
	}
	return ""
}

func (b *ClaimsBridge) claimTestamentResponseMsg(sessionID, claimID string, t *claims.Testament) *msg.ClaimResponseTextMsg {
	claimID = strings.TrimSpace(claimID)
	if t == nil || claimID == "" {
		return nil
	}
	content := strings.TrimSpace(t.Summary)
	if content == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta := b.metaForClaimLocked(claimID)
	parentRowID := b.parentRowIDForClaimLocked(claimID)
	if meta.CycleID == "" || parentRowID == "" || meta.CycleID == claimID {
		return nil
	}
	return &msg.ClaimResponseTextMsg{
		SessionID:    sessionID,
		CycleID:      meta.CycleID,
		ClaimID:      claimID,
		ParentRowID:  parentRowID,
		AgentID:      firstNonBlank(strings.TrimSpace(t.AgentID), claimContextActor(meta, ""), meta.OwnerAgentID),
		Content:      content,
		CreatedAt:    nonZeroTime(t.Created),
		SuppressChat: meta.SuppressChat,
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
	if resolvedCycle := b.resolver.CycleForClaim(claimID); resolvedCycle != "" && (meta.CycleID == "" || meta.CycleID == claimID) {
		meta.CycleID = resolvedCycle
	}
	if meta.OwnerAgentID == "" {
		meta.OwnerAgentID = b.resolver.OwnerForClaim(claimID)
		meta.OwnerAgentType = agentTypeFromID(meta.OwnerAgentID)
	}
	if meta.SessionID == "" {
		meta.SessionID = b.activeSession
	}
	meta = b.enrichMetaFromProjectionLocked(claimID, meta)
	if claimID != "" {
		b.claimMeta[claimID] = meta
	}
	return meta
}

func (b *ClaimsBridge) enrichMetaFromProjectionLocked(claimID string, meta claimMeta) claimMeta {
	if strings.TrimSpace(claimID) == "" || b.board == nil {
		return meta
	}
	c := findClaim(b.board.Projection(), claimID)
	if c == nil {
		return meta
	}
	if meta.OwnerAgentID == "" {
		meta.OwnerAgentID = cycleOwnerForClaim(c)
		meta.OwnerAgentType = agentTypeFromID(meta.OwnerAgentID)
	}
	if meta.TargetAgentID == "" {
		meta.TargetAgentID = strings.TrimSpace(claims.SubjectAgentID(c.Relations))
		meta.TargetAgentType = agentTypeFromID(meta.TargetAgentID)
	}
	if meta.IssuerAgentID == "" {
		meta.IssuerAgentID = strings.TrimSpace(claims.IssuerAgentID(c.Relations))
	}
	if meta.ActionType == "" {
		meta.ActionType = string(c.ActionType)
	}
	if meta.Title == "" {
		meta.Title = strings.TrimSpace(c.Title)
	}
	if meta.StreamCorrelationID == "" {
		meta.StreamCorrelationID = claimUIStreamCorrelation(c)
	}
	if claimSuppressChat(c) {
		meta.SuppressChat = true
	}
	if meta.UIState == "" {
		meta.UIState = claimInitialUIState(c)
	}
	return meta
}

func (b *ClaimsBridge) replayProjection(sessionID string, proj *claims.ClaimsBoardProjection) {
	if proj == nil {
		return
	}
	b.emitCounterProjection(sessionID, proj)
	for i := range proj.Claims {
		c := &proj.Claims[i]
		if c.Status.IsTerminal() {
			continue
		}
		b.handleClaimCreated(sessionID, c)
		if strings.TrimSpace(c.Context) != "" {
			b.handleClaimContext(sessionID, claimContextEvent{
				ClaimID:           c.ID,
				Context:           c.Context,
				ContextTransition: c.ContextTransition,
			})
		}
	}
	completed := completedStartedArtifactsInProjection(proj)
	testaments := testamentsBySequence(proj.Testaments)
	for _, t := range testaments {
		if !testamentLifecycleDisplayable(t.LifecycleStatus) {
			continue
		}
		if m := b.claimPresentationMsgForTestament(sessionID, claims.ClaimIDFromRelations(t.Relations), t); m != nil {
			b.enqueue(*m)
		}
		if strings.TrimSpace(t.Context) != "" {
			b.handleTestamentContext(sessionID, testamentContextEvent{
				ClaimID:           claims.ClaimIDFromRelations(t.Relations),
				TestamentID:       t.ID,
				AgentID:           t.AgentID,
				Context:           t.Context,
				ContextTransition: t.ContextTransition,
			})
		}
		claimID := claims.ClaimIDFromRelations(t.Relations)
		for _, art := range artifactsBySequence(t.Artifacts) {
			if art == nil {
				continue
			}
			if strings.TrimSpace(art.Kind) == claims.ArtifactKindResponseText {
				b.OnArtifactAdded(claimID, t.AgentID, sessionID, art)
				continue
			}
			if claims.IsPresentableToUserChat(art.Presentation) && !isPresentationLifecycleArtifactKind(art.Kind) {
				b.OnArtifactAdded(claimID, t.AgentID, sessionID, art)
				continue
			}
			if !isVisibleStartedArtifactKind(art.Kind) {
				continue
			}
			if _, done := completed[strings.TrimSpace(art.ID)]; done {
				continue
			}
			b.OnArtifactAdded(claimID, t.AgentID, sessionID, art)
		}
	}
}

func testamentLifecycleDisplayable(status claims.TestamentLifecycleStatus) bool {
	switch status {
	case claims.TestamentLifecyclePosted,
		claims.TestamentLifecycleReceived,
		claims.TestamentLifecycleValidating,
		claims.TestamentLifecycleValidated,
		claims.TestamentLifecycleValidationIncomplete,
		claims.TestamentLifecycleValidationFailed,
		claims.TestamentLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func testamentsBySequence(in []claims.Testament) []*claims.Testament {
	out := make([]*claims.Testament, 0, len(in))
	for i := range in {
		out = append(out, &in[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

func artifactsBySequence(in []*claims.Artifact) []*claims.Artifact {
	out := append([]*claims.Artifact(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == nil || out[j] == nil {
			return out[j] != nil
		}
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

func completedStartedArtifactsInProjection(proj *claims.ClaimsBoardProjection) map[string]struct{} {
	completed := make(map[string]struct{})
	if proj == nil {
		return completed
	}
	for i := range proj.Testaments {
		t := &proj.Testaments[i]
		for j := range t.Artifacts {
			art := t.Artifacts[j]
			if art == nil || !isVisibleCompletedArtifactKind(art.Kind) {
				continue
			}
			if startID := startedArtifactID(art); startID != "" {
				completed[startID] = struct{}{}
			}
		}
	}
	return completed
}

func (b *ClaimsBridge) emitCounterProjection(sessionID string, proj *claims.ClaimsBoardProjection) {
	if proj == nil {
		return
	}
	b.mu.Lock()
	changed := proj.AcceptedCount != b.lastAccepted || proj.TotalClaims != b.lastTotal
	b.lastAccepted = proj.AcceptedCount
	b.lastTotal = proj.TotalClaims
	b.mu.Unlock()
	if !changed {
		return
	}
	b.enqueue(msg.ClaimsProjectionMsg{
		SessionID:     sessionID,
		AcceptedCount: proj.AcceptedCount,
		TotalClaims:   proj.TotalClaims,
	})
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

func findArtifact(proj *claims.ClaimsBoardProjection, artifactID string) *claims.Artifact {
	if proj == nil {
		return nil
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil
	}
	for i := range proj.Testaments {
		for _, artifact := range proj.Testaments[i].Artifacts {
			if artifact == nil {
				continue
			}
			if strings.TrimSpace(artifact.ID) == artifactID {
				return artifact
			}
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

func claimUIStreamCorrelation(c *claims.Claim) string {
	if claimHasTag(c, claimTagGuideClassification) {
		return ""
	}
	return claimStreamCorrelation(c)
}

const (
	claimTagGuideClassification = "ui:guide_classification"
	claimTagAgentPanelOnly      = "ui_surface:agent_panel"
)

func claimSuppressChat(c *claims.Claim) bool {
	if c == nil {
		return false
	}
	return claimHasTag(c, claimTagAgentPanelOnly)
}

func claimInitialUIState(c *claims.Claim) string {
	if claimHasTag(c, claimTagGuideClassification) {
		return "classifying"
	}
	return ""
}

func claimPanelReason(c *claims.Claim) string {
	if c == nil {
		return ""
	}
	if claimHasTag(c, claimTagGuideClassification) {
		return firstNonBlank(strings.TrimSpace(c.Context), strings.TrimSpace(c.Title), "Classifying request")
	}
	return strings.TrimSpace(c.Title)
}

func claimContextUIState(meta claimMeta, context string) string {
	if meta.OwnerAgentID != "guide" || meta.UIState != "classifying" {
		return meta.UIState
	}
	lower := strings.ToLower(strings.TrimSpace(context))
	switch {
	case strings.Contains(lower, "request forwarded"), strings.Contains(lower, "routing to "):
		return "routing"
	case strings.Contains(lower, "failed"), strings.Contains(lower, "error"):
		return "errored"
	default:
		return firstNonBlank(meta.UIState, "classifying")
	}
}

func claimContextActor(meta claimMeta, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	switch claims.ActionType(strings.TrimSpace(meta.ActionType)) {
	case claims.ActionTypePrompt, claims.ActionTypeHandoff:
		return firstNonBlank(meta.OwnerAgentID, meta.TargetAgentID, fallback, meta.IssuerAgentID)
	case claims.ActionTypeConsultation, claims.ActionTypeChallenge, claims.ActionTypeGuardianCheck:
		if fallback != "" && fallback != meta.IssuerAgentID {
			return fallback
		}
		return firstNonBlank(meta.TargetAgentID, fallback, meta.OwnerAgentID, meta.IssuerAgentID)
	default:
		if fallback != "" && fallback != meta.IssuerAgentID {
			return fallback
		}
		return firstNonBlank(meta.OwnerAgentID, meta.TargetAgentID, fallback, meta.IssuerAgentID)
	}
}

func claimHasTag(c *claims.Claim, want string) bool {
	if c == nil {
		return false
	}
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, tag := range c.Tags {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func isVisibleStartedArtifactKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "tool_started":
		return true
	default:
		return false
	}
}

func isVisibleCompletedArtifactKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "tool_completed":
		return true
	default:
		return false
	}
}

func isCompletionArtifact(art *claims.Artifact) bool {
	if art == nil {
		return false
	}
	kind := strings.TrimSpace(art.Kind)
	return isVisibleCompletedArtifactKind(kind) ||
		(isBridgeErrorArtifactKind(kind) && startedArtifactID(art) != "")
}

func isBridgeErrorArtifactKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case claims.ArtifactKindError,
		claims.ArtifactKindErrorTrace,
		claims.ArtifactKindErrorDiagnostic,
		claims.ArtifactKindProjectionError,
		claims.ArtifactKindToolTimeout,
		claims.ArtifactKindPermissionDenied,
		claims.ArtifactKindPolicyDenied,
		claims.ArtifactKindMissingDependency,
		claims.ArtifactKindInvalidExpectedToolCall:
		return true
	default:
		return false
	}
}

func isPresentationLifecycleArtifactKind(kind string) bool {
	return strings.TrimSpace(kind) == claims.ArtifactKindAgentState ||
		strings.TrimSpace(kind) == claims.ArtifactKindResponseText ||
		isVisibleStartedArtifactKind(kind) ||
		isVisibleCompletedArtifactKind(kind)
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
	if isBridgeErrorArtifactKind(art.Kind) {
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
		claimMetadataString(art.Metadata, "summary", "output", "error"),
		strings.TrimSpace(art.Reference),
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

func claimMetadataBool(md map[string]any, key string) bool {
	if md == nil || strings.TrimSpace(key) == "" {
		return false
	}
	switch typed := md[key].(type) {
	case bool:
		return typed
	case string:
		v := strings.TrimSpace(strings.ToLower(typed))
		return v == "true" || v == "1" || v == "yes"
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}

func claimMetadataInt(md map[string]any, key string) int {
	if md == nil || strings.TrimSpace(key) == "" {
		return 0
	}
	switch typed := md[key].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	default:
		return 0
	}
}

type fmtStringer interface {
	String() string
}

func cloneArtifact(art *claims.Artifact) *claims.Artifact {
	return claims.CloneArtifact(art)
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
		b.debug("enqueue", "msg_type", fmt.Sprintf("%T", m))
	default:
		total := b.dropped.Add(1)
		slog.Warn("claims bridge drop: outbox full",
			"bridge_id", b.id,
			"total_dropped", total)
		b.debug("enqueue_drop", "msg_type", fmt.Sprintf("%T", m), "total_dropped", total)
	}
}
