package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/prompts"
	"github.com/google/uuid"
)

// scribeProvider is the minimal LLM interface the Scribe needs.
type scribeProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Config configures a Scribe sidecar.
type Config struct {
	ParentAgentType string
	Provider        scribeProvider
	Model           string
	Bus             guide.EventBus
	Scope           *concurrency.GoroutineScope
	Logger          *slog.Logger
	Forest          shared.MemoryForestService
	MaxMessages     int // 0 = default (50 turn pairs per workstream)
	MaxToolRuns     int // 0 = default bounded tool-loop budget

	// SessionID identifies the per-session scope this scribe runs
	// under. Surfaced onto archivalist + fabric narration metadata.
	SessionID string

	// SessionDir is the per-session storage directory used to persist
	// the replica-generation counter and surface it onto narrations.
	// When empty, replica generation stays at zero and consumers
	// treat it as "unknown" (the scribe still functions).
	SessionDir string
}

// defaultMaxMessages is the default ring buffer capacity in turn pairs.
const defaultMaxMessages = 50

const defaultMaxToolRuns = 6

// feedBufferSize is the bounded feed channel capacity.
const feedBufferSize = 32

const (
	scribeMaxWorkstreams = 64
	scribeWorkstreamTTL  = 15 * time.Minute
)

var scribeClaimsNative = prompts.MustLoad("shared", "claims_native")

// Scribe is a per-agent summarization sidecar that maintains running
// commentary on a parent agent's work and forwards it to the Archivalist
// for long-term memory.
type Scribe struct {
	id              string
	parentAgentType string
	provider        scribeProvider
	model           string
	bus             guide.EventBus
	channels        *guide.AgentChannels
	logger          *slog.Logger
	scope           *concurrency.GoroutineScope
	claimsInbox     *claims.ClaimsInbox
	config          Config

	// Workstream state is isolated by parent correlation ID so concurrent
	// parent requests do not share one mutable scribe conversation.
	workstreamsMu sync.Mutex
	workstreams   map[string]*scribeWorkstream
	maxMessages   int
	maxToolRuns   int

	// Handoff bridge (GP-based quality detection).
	handoffBridge *handoff.HandoffBridge
	handoffMu     sync.Mutex
	handoffSeed   *scribeHandoffSeed

	// Bus subscriptions.
	requestSub  guide.Subscription
	responseSub guide.Subscription

	// Lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc
	running   atomic.Bool

	// Feed channel: bounded, non-blocking ingest from parent agent.
	feedCh chan shared.ScribeFeed

	// replicaGeneration is the monotonic counter for this scribe's
	// (sessionID, parentAgentType) tuple. Acquired in New(); surfaced
	// on every narration's metadata so cross-replica queries can
	// distinguish "this current life" from "prior lives" of the same
	// logical agent. Zero when SessionDir is empty (test fixtures).
	replicaGeneration int
	sessionID         string
	sessionDir        string

	// batchMapPtr / batchMapInit hold the per-workstream fabric batch
	// buffers (keyed by pipeline_id or actor agent_id). Lazily
	// initialized in fabricBatches() so test fixtures that never
	// trigger Receive don't allocate.
	batchMapPtr  *sync.Map
	batchMapInit sync.Once

	// fabricUnsubscribe releases this scribe's Subscriber registration
	// against the SubscribingSink. Set by Start; called by Stop.
	fabricUnsubscribe func()

	// continuityNarrated deduplicates deterministic narration of
	// carry-forward continuity testaments observed through claims fabric
	// projection. Keyed by source continuity testament ID.
	continuityNarrated sync.Map
}

type scribeWorkstream struct {
	mu        sync.Mutex
	messages  []providers.Message
	updatedAt time.Time
}

type scribeHandoffSeed struct {
	workstreamKey string
	messages      []providers.Message
}

// compile-time assertion
var _ shared.Scribe = (*Scribe)(nil)
var _ handoff.HandoffInjectable = (*Scribe)(nil)

// New creates a Scribe for the given parent agent type.
func New(cfg Config) *Scribe {
	id := fmt.Sprintf("scribe-%s-%s", cfg.ParentAgentType, uuid.New().String()[:8])
	maxMsgs := cfg.MaxMessages
	if maxMsgs == 0 {
		maxMsgs = defaultMaxMessages
	}
	maxToolRuns := cfg.MaxToolRuns
	if maxToolRuns == 0 {
		maxToolRuns = defaultMaxToolRuns
	}
	// Replica generation is acquired in Start (not here) so
	// agent_pod has a chance to inject per-session state via
	// SetSession between construction and start. The factory is
	// process-wide and doesn't know per-session paths.
	return &Scribe{
		id:              id,
		parentAgentType: cfg.ParentAgentType,
		provider:        cfg.Provider,
		model:           cfg.Model,
		bus:             cfg.Bus,
		logger:          cfg.Logger,
		scope:           cfg.Scope,
		config:          cfg,
		workstreams:     make(map[string]*scribeWorkstream),
		maxMessages:     maxMsgs,
		maxToolRuns:     maxToolRuns,
		feedCh:          make(chan shared.ScribeFeed, feedBufferSize),
		sessionID:       cfg.SessionID,
		sessionDir:      cfg.SessionDir,
	}
}

// ReplicaGeneration returns this scribe's replica generation. Zero
// when no session dir was wired (test fixtures). Used by tests and
// by the fabric narration emitter to surface the value on activity
// payloads + metadata.
func (s *Scribe) ReplicaGeneration() int {
	if s == nil {
		return 0
	}
	return s.replicaGeneration
}

// SetSession lets agent_pod.startScribes inject per-session state
// (sessionID + sessionDir) after the scribe is constructed by the
// process-wide ScribeFactory but before Start. Acquires the replica
// generation counter at Start time so test fixtures that don't wire
// session info still work cleanly.
//
// Best-effort: empty values are ignored. Calling this after Start is
// also a no-op — the replica counter has already been acquired.
func (s *Scribe) SetSession(sessionID, sessionDir string) {
	if s == nil || s.running.Load() {
		return
	}
	if sessionID != "" {
		s.sessionID = sessionID
	}
	if sessionDir != "" {
		s.sessionDir = sessionDir
	}
}

// Start begins the Scribe's feed processing loop and bus subscriptions.
func (s *Scribe) Start() error {
	if s.running.Swap(true) {
		return nil
	}
	if s.bus == nil {
		s.running.Store(false)
		return fmt.Errorf("scribe %s: bus is required", s.id)
	}
	// Acquire replica generation now — SetSession may have populated
	// sessionDir between New() and here. Best-effort; empty session
	// dir yields zero (test fixtures).
	if s.replicaGeneration == 0 {
		s.replicaGeneration, _ = AcquireReplicaGeneration(s.sessionDir, s.parentAgentType)
	}
	s.runCtx, s.runCancel = context.WithCancel(context.Background())
	s.channels = guide.NewAgentChannels("scribe-"+s.parentAgentType, s.id)

	var err error
	s.requestSub, err = s.bus.SubscribeAsync(s.channels.Requests, s.handleBusRequest)
	if err != nil {
		s.running.Store(false)
		return fmt.Errorf("scribe %s: subscribe requests: %w", s.id, err)
	}
	s.responseSub, err = s.bus.SubscribeAsync(s.channels.Responses, s.handleBusResponse)
	if err != nil {
		s.requestSub.Unsubscribe()
		s.running.Store(false)
		return fmt.Errorf("scribe %s: subscribe responses: %w", s.id, err)
	}

	if s.scope != nil {
		_ = s.scope.Go("scribe-"+s.parentAgentType+"-feed", 0, func(ctx context.Context) error {
			s.feedLoop(ctx)
			return nil
		})
		_ = s.scope.Go("scribe-"+s.parentAgentType+"-periodic", 0, func(ctx context.Context) error {
			s.periodicSynthesisLoop(ctx)
			return nil
		})
	} else {
		go s.feedLoop(s.runCtx)
		go s.periodicSynthesisLoop(s.runCtx)
	}

	// Register with the Activity Fabric SubscribingSink (Phase 1 of
	// SCRIBE_FABRIC.md). When the production sink isn't a
	// SubscribingSink (test fixtures with a TestCollector), this is
	// a silent no-op — the FeedScribe push path stays operational
	// for that environment.
	s.fabricUnsubscribe = activitystore.SubscribeToDefault(s)

	// Claims intake: event-driven delta processing.
	if inbox := shared.WireClaimsIntake(shared.ClaimsIntakeConfig{
		AgentID:      s.id,
		SessionID:    s.sessionID,
		Bus:          s.bus,
		Board:        s.scribeBoard(),
		Scope:        s.scope,
		ProcessEntry: s.processClaimsEntry,
	}); inbox != nil {
		if err := inbox.Start(nil); err != nil {
			slog.Warn("scribe_claims_inbox_start_failed", "error", err.Error())
		}
		s.claimsInbox = inbox
	}

	// Lifecycle claim: replica started.
	s.scribeSubmitTestament(s.runCtx, s.scribeTestament(
		fmt.Sprintf("Scribe replica %d started for %s", s.replicaGeneration, s.parentAgentType),
		"committed",
		[]*claims.Artifact{
			s.scribeArtifact("session_id", s.sessionID),
			s.scribeArtifact("replica_generation", fmt.Sprintf("%d", s.replicaGeneration)),
			s.scribeArtifact("scribe_id", s.id),
		},
	))

	// Phase 8: cross-replica continuity via fabric query. Best-effort
	// — when no prior replica produced narrations or the fabric
	// source isn't yet wired, the scribe boots cold. The handoff
	// bridge (when present) still seeds additional in-memory state
	// via InjectPreparedContext as a warm fast-path.
	s.inheritPriorReplicaNarrative(s.runCtx)
	return nil
}

// Stop shuts down the Scribe, closing the feed channel and unsubscribing.
func (s *Scribe) Stop() error {
	if !s.running.Swap(false) {
		return nil
	}

	// Lifecycle testament: replica stopping. Posted before context
	// cancel so the board operation can complete.
	s.workstreamsMu.Lock()
	wsCount := len(s.workstreams)
	s.workstreamsMu.Unlock()
	// Use background context — runCtx may be cancelled already.
	s.scribeSubmitTestament(context.Background(), s.scribeTestament(
		fmt.Sprintf("Scribe replica %d stopped — %d workstreams", s.replicaGeneration, wsCount),
		"committed",
		[]*claims.Artifact{
			s.scribeArtifact("replica_generation", fmt.Sprintf("%d", s.replicaGeneration)),
			s.scribeArtifact("workstream_count", fmt.Sprintf("%d", wsCount)),
		},
	))

	if s.claimsInbox != nil {
		_ = s.claimsInbox.Close()
		s.claimsInbox = nil
	}

	if s.fabricUnsubscribe != nil {
		s.fabricUnsubscribe()
		s.fabricUnsubscribe = nil
	}
	if s.runCancel != nil {
		s.runCancel()
	}
	close(s.feedCh)
	if s.requestSub != nil {
		s.requestSub.Unsubscribe()
	}
	if s.responseSub != nil {
		s.responseSub.Unsubscribe()
	}
	s.workstreamsMu.Lock()
	clear(s.workstreams)
	s.workstreamsMu.Unlock()
	s.handoffMu.Lock()
	s.handoffSeed = nil
	s.handoffMu.Unlock()
	return nil
}

// Feed sends turn data from the parent agent. Non-blocking; drops the
// oldest entry if the buffer is full.
func (s *Scribe) Feed(feed shared.ScribeFeed) {
	select {
	case s.feedCh <- feed:
	default:
		// Buffer full — drain one and retry.
		select {
		case <-s.feedCh:
		default:
		}
		select {
		case s.feedCh <- feed:
		default:
		}
	}
}

// feedLoop drains the feed channel and processes each turn.
func (s *Scribe) feedLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case feed, ok := <-s.feedCh:
			if !ok {
				return
			}
			s.dispatchFeed(ctx, feed)
		}
	}
}

func (s *Scribe) dispatchFeed(ctx context.Context, feed shared.ScribeFeed) {
	// Keep feed processing single-threaded behind the bounded feed buffer.
	// Spawning per-feed goroutines allows blocked workstreams to accumulate an
	// unbounded backlog of parked goroutines under sustained agent traffic.
	s.processFeed(ctx, feed)
}

// processFeed builds a workstream-isolated tool loop for the current parent
// correlation, then commits the resulting stored commentary into that
// workstream's rolling history.
func (s *Scribe) processFeed(ctx context.Context, feed shared.ScribeFeed) {
	workstream := s.workstreamForFeed(feed)
	workstream.mu.Lock()
	defer workstream.mu.Unlock()

	ctx = s.contextForFeed(ctx, feed)
	history := workstream.snapshot()
	turnMessage := s.buildTurnMessage(feed)
	history = append(history, turnMessage)

	commentary, err := s.generateCommentary(ctx, history, feed)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("scribe commentary generation failed",
				"parent", s.parentAgentType,
				"correlation_id", strings.TrimSpace(feed.ParentCorrelationID),
				"error", err)
		}
		return
	}

	workstream.commitTurn(turnMessage, providers.Message{
		Role:    providers.RoleAssistant,
		Content: commentary,
	}, s.maxMessages)
}

func (s *Scribe) workstreamForFeed(feed shared.ScribeFeed) *scribeWorkstream {
	key := s.workstreamKey(feed)
	now := time.Now()
	s.workstreamsMu.Lock()
	defer s.workstreamsMu.Unlock()
	s.pruneWorkstreamsLocked(now, key)
	if existing := s.workstreams[key]; existing != nil {
		return existing
	}
	workstream := &scribeWorkstream{
		messages: make([]providers.Message, 0, max(2, s.maxMessages*2)),
	}
	if seed := s.consumeHandoffSeed(key); len(seed) > 0 {
		workstream.messages = append(workstream.messages, seed...)
	}
	workstream.updatedAt = now
	s.workstreams[key] = workstream
	s.pruneWorkstreamsLocked(now, key)
	return workstream
}

func (s *Scribe) pruneWorkstreamsLocked(now time.Time, keepKey string) {
	if s == nil || len(s.workstreams) == 0 {
		return
	}
	for key, workstream := range s.workstreams {
		if strings.TrimSpace(key) == strings.TrimSpace(keepKey) {
			continue
		}
		if workstream == nil {
			delete(s.workstreams, key)
			continue
		}
		if !workstream.updatedAt.IsZero() && now.Sub(workstream.updatedAt) > scribeWorkstreamTTL {
			delete(s.workstreams, key)
		}
	}
	if len(s.workstreams) <= scribeMaxWorkstreams {
		return
	}
	type agedWorkstream struct {
		key       string
		updatedAt time.Time
	}
	oldest := make([]agedWorkstream, 0, len(s.workstreams))
	for key, workstream := range s.workstreams {
		if strings.TrimSpace(key) == strings.TrimSpace(keepKey) {
			continue
		}
		updatedAt := time.Time{}
		if workstream != nil {
			updatedAt = workstream.updatedAt
		}
		oldest = append(oldest, agedWorkstream{key: key, updatedAt: updatedAt})
	}
	sort.Slice(oldest, func(i, j int) bool {
		return oldest[i].updatedAt.Before(oldest[j].updatedAt)
	})
	overflow := len(s.workstreams) - scribeMaxWorkstreams
	if overflow <= 0 {
		return
	}
	for _, item := range oldest {
		if overflow == 0 {
			return
		}
		delete(s.workstreams, item.key)
		overflow--
	}
}

func (s *Scribe) workstreamKey(feed shared.ScribeFeed) string {
	if key := strings.TrimSpace(feed.ParentCorrelationID); key != "" {
		return key
	}
	return "scribe:" + strings.TrimSpace(s.parentAgentType)
}

func (s *Scribe) contextForFeed(ctx context.Context, feed shared.ScribeFeed) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	parentCorrelationID := strings.TrimSpace(feed.ParentCorrelationID)
	if parentCorrelationID != "" {
		ctx = shared.WithStreamContext(ctx, parentCorrelationID, s.parentAgentType)
	}
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		CorrID:    s.workstreamKey(feed),
		AgentID:   s.id,
		SessionID: "",
	})
	return handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       true,
		Bridge:        s.handoffBridge,
		AgentID:       s.id,
		AgentType:     s.AgentType(),
		UserRequest:   s.buildTurnMessage(feed).Content,
		CorrelationID: strings.TrimSpace(feed.ParentCorrelationID),
	})
}

func (s *Scribe) buildTurnMessage(feed shared.ScribeFeed) providers.Message {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(s.parentAgentType)
	b.WriteString(" turn]\n")
	if correlationID := strings.TrimSpace(feed.ParentCorrelationID); correlationID != "" {
		b.WriteString("correlation_id: ")
		b.WriteString(correlationID)
		b.WriteString("\n")
	}
	b.WriteString("request:\n")
	b.WriteString(strings.TrimSpace(feed.UserRequest))
	b.WriteString("\n\nresponse:\n")
	b.WriteString(strings.TrimSpace(feed.AgentResponse))
	return providers.Message{
		Role:    providers.RoleUser,
		Content: b.String(),
	}
}

func (w *scribeWorkstream) snapshot() []providers.Message {
	if w == nil || len(w.messages) == 0 {
		return nil
	}
	out := make([]providers.Message, len(w.messages))
	copy(out, w.messages)
	return out
}

func (w *scribeWorkstream) commitTurn(userMsg, assistantMsg providers.Message, maxTurns int) {
	if w == nil {
		return
	}
	w.messages = append(w.messages, userMsg, assistantMsg)
	limit := maxTurns * 2
	if limit > 0 && len(w.messages) > limit {
		start := len(w.messages) - limit
		w.messages = append([]providers.Message(nil), w.messages[start:]...)
	}
	w.updatedAt = time.Now()
}

func scribeSystemPrompt(parentAgentType string, forestEnabled bool) string {
	baseSchema := `{
  "summary": "What happened this turn",
  "progress": "Overall task progress",
  "decisions": "Key decisions or trade-offs made",
  "state": "Current working state",
  "risk": "Any risks, regressions, or concerns",
  "handoff_context": "Compact context worth preserving for later replay or handoff",
  "details": {
    "domain_specific": "Optional role-specific details that matter downstream"
  }
}`
	focus := "Capture the specialist's concrete work and preserve only the details another agent would need later."
	detailsGuidance := `"details": {"notes": "Optional role-specific details"}`

	switch strings.ToLower(strings.TrimSpace(parentAgentType)) {
	case "academic":
		detailsGuidance = `"details": {"thesis": "...", "findings": "...", "tradeoffs": "...", "handoff_delta": "..."}`
		focus = "Preserve proposal quality, evidence, and architectural trade-offs for downstream planning."
	case "architect":
		detailsGuidance = `"details": {"plan_delta": "...", "dependencies": "...", "assumptions": "..."}`
		focus = "Track plan evolution, assumptions, and anything that affects execution readiness."
	case "engineer", "designer":
		detailsGuidance = `"details": {"files": "...", "tests": "...", "implementation_delta": "..."}`
		focus = "Preserve concrete implementation deltas, touched files, and validation evidence."
	case "inspector", "inspector-pipeline", "inspector-global":
		detailsGuidance = `"details": {"criteria": "...", "issues": "...", "corrections": "..."}`
		focus = "Capture failures, remediation guidance, and what remains unsafe to proceed."
	case "guardian":
		detailsGuidance = `"details": {"assessments": "...", "decisions": "...", "approvals": "..."}`
		focus = "Preserve safety decisions, approval requirements, and risk signals."
	case "orchestrator":
		detailsGuidance = `"details": {"critical_path": "...", "coordination": "...", "interventions": "..."}`
		focus = "Track workflow health, critical path changes, and interventions that affect execution."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are a Scribe, a silent observer maintaining high-signal notes on a %s agent's work. Your output must be stored in the Archivalist for long-term memory.\n\n", parentAgentType)
	b.WriteString(focus)
	b.WriteString("\n\n")
	b.WriteString(scribeClaimsNative)
	b.WriteString("\n\n")
	b.WriteString("You operate through tools, not free-text completion.\n")
	if forestEnabled {
		b.WriteString("Use `scribe_forest_consult(purpose=get_capture_targets, query=…)` when prior intent, decisions, conflicts, or preferences should shape what you preserve.\n")
		b.WriteString("Use `scribe_forest_consult(purpose=prepare_handoff_context, query=…)` when future handoff or replay context needs to be compacted before storage.\n")
	}
	b.WriteString("End every Scribe turn by calling `store_archivalist` exactly once with the final structured commentary object.\n")
	b.WriteString("Do not return the commentary as prose instead of storing it.\n\n")
	b.WriteString("Commentary object shape:\n")
	b.WriteString(baseSchema)
	b.WriteString("\n")
	b.WriteString(detailsGuidance)
	b.WriteString("\n\n")
	b.WriteString("Be specific, factual, compact, and archival. No suggestions, no cheering, and no speculation beyond what is evidenced in the observed turn.\n")
	return b.String()
}

// handleBusRequest processes incoming bus messages (bus subscription handler).
func (s *Scribe) handleBusRequest(_ *guide.Message) error {
	return nil
}

// handleBusResponse processes response bus messages (bus subscription handler).
func (s *Scribe) handleBusResponse(_ *guide.Message) error {
	return nil
}

// --------------------------------------------------------------------------
// HandoffableAgent implementation
// --------------------------------------------------------------------------

// AgentID returns the Scribe's unique identifier.
func (s *Scribe) AgentID() string { return s.id }

// AgentType returns the Scribe's agent type.
func (s *Scribe) AgentType() string { return "scribe-" + s.parentAgentType }

// SetCanonicalID preserves the routing identity across handoff replacement.
func (s *Scribe) SetCanonicalID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	s.id = id
}

// Descriptor returns the Scribe's agent descriptor for handoff.
func (s *Scribe) Descriptor() handoff.AgentDescriptor {
	modelID := s.model
	return handoff.AgentDescriptor{
		AgentType:             "scribe-" + s.parentAgentType,
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryStandalone,
		RuntimeProfiles:       scribeRuntimeProfiles(),
		DefaultRuntimeProfile: scribeDefaultRuntimeProfile(),
	}
}

// ExtractArchivableState returns the Scribe's archivable state.
func (s *Scribe) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   s.id,
		AgentType: "scribe-" + s.parentAgentType,
		Timestamp: time.Now(),
	}
}

// Terminate stops the Scribe.
func (s *Scribe) Terminate(_ context.Context) error { return s.Stop() }

// SetHandoffBridge sets the handoff bridge for GP-based quality detection.
func (s *Scribe) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	s.handoffBridge = bridge
}

// InjectPreparedContext accepts prepared context from a prior Scribe instance
// and seeds the next workstream with the Archivalist-generated handoff brief.
func (s *Scribe) InjectPreparedContext(pc *handoff.PreparedContext) error {
	if pc == nil {
		return nil
	}

	brief, err := s.handoffBriefFromPreparedContext(pc)
	if err != nil {
		return err
	}

	correlationID, _ := pc.GetMetadata("correlation_id")
	correlationID = strings.TrimSpace(correlationID)
	workstreamKey := "scribe:" + strings.TrimSpace(s.parentAgentType)
	if correlationID != "" {
		workstreamKey = correlationID
	}

	assistantContent, err := s.handoffCommentaryJSON(brief, pc)
	if err != nil {
		return err
	}

	var userContent strings.Builder
	userContent.WriteString("[scribe handoff]\n")
	if correlationID != "" {
		userContent.WriteString("correlation_id: ")
		userContent.WriteString(correlationID)
		userContent.WriteString("\n")
	}
	userContent.WriteString("A prior scribe instance rolled over. Continue from this preserved Archivalist handoff brief.")

	s.handoffMu.Lock()
	s.handoffSeed = &scribeHandoffSeed{
		workstreamKey: workstreamKey,
		messages: []providers.Message{
			{
				Role:    providers.RoleUser,
				Content: userContent.String(),
			},
			{
				Role:    providers.RoleAssistant,
				Content: assistantContent,
			},
		},
	}
	s.handoffMu.Unlock()

	// Lifecycle claim: handoff context injected.
	s.scribePostClaim(context.Background(),
		s.scribeClaimAction(claims.ActionTypeArchival),
		s.scribeClaim(
			"Scribe handoff: prior replica context injected",
			"Prepared context from prior scribe instance seeded into workstream",
			[]claims.ClaimScopeEntry{{Kind: "workstream", Key: workstreamKey}},
			claims.ActionTypeArchival, nil,
		),
	)
	s.scribeSubmitTestament(context.Background(), s.scribeTestament(
		"Handoff context injected from prior scribe replica",
		"committed",
		[]*claims.Artifact{
			s.scribeArtifact("correlation_id", correlationID),
			s.scribeArtifact("workstream_key", workstreamKey),
		},
	))

	return nil
}

func (s *Scribe) consumeHandoffSeed(workstreamKey string) []providers.Message {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()

	if s.handoffSeed == nil {
		return nil
	}
	if expected := strings.TrimSpace(s.handoffSeed.workstreamKey); expected != "" && expected != strings.TrimSpace(workstreamKey) {
		return nil
	}

	cloned := cloneScribeMessages(s.handoffSeed.messages)
	s.handoffSeed = nil
	return cloned
}

func (s *Scribe) handoffBriefFromPreparedContext(pc *handoff.PreparedContext) (*handoff.ContextBrief, error) {
	if raw, ok := pc.GetMetadata("context_brief"); ok && strings.TrimSpace(raw) != "" {
		var brief handoff.ContextBrief
		if err := json.Unmarshal([]byte(raw), &brief); err != nil {
			return nil, fmt.Errorf("decode scribe handoff brief: %w", err)
		}
		return &brief, nil
	}

	summary := strings.TrimSpace(pc.Summary())
	if summary == "" {
		return &handoff.ContextBrief{}, nil
	}

	return &handoff.ContextBrief{
		TaskSummary: summary,
		GeneratedAt: time.Now(),
		ContextSize: pc.TokenCount(),
		TurnNumber:  len(pc.RecentMessages()),
	}, nil
}

func (s *Scribe) handoffCommentaryJSON(brief *handoff.ContextBrief, pc *handoff.PreparedContext) (string, error) {
	if brief == nil {
		brief = &handoff.ContextBrief{}
	}

	details := map[string]any{
		"source":           "scribe_handoff_brief",
		"parent_agent":     s.parentAgentType,
		"context_size":     brief.ContextSize,
		"turn_number":      brief.TurnNumber,
		"prepared_summary": strings.TrimSpace(pc.Summary()),
	}
	if !brief.GeneratedAt.IsZero() {
		details["generated_at"] = brief.GeneratedAt.UTC().Format(time.RFC3339Nano)
	}

	payload := map[string]any{
		"summary":         firstNonEmpty(strings.TrimSpace(brief.TaskSummary), "Continuing from preserved prior scribe context."),
		"progress":        firstNonEmpty(strings.TrimSpace(brief.ActiveState), strings.TrimSpace(brief.NextSteps), "Handoff continuation in progress."),
		"decisions":       strings.TrimSpace(brief.KeyDecisions),
		"state":           firstNonEmpty(strings.TrimSpace(brief.ActiveState), strings.TrimSpace(pc.Summary())),
		"risk":            strings.TrimSpace(brief.Blockers),
		"handoff_context": firstNonEmpty(strings.TrimSpace(brief.NextSteps), strings.TrimSpace(brief.TaskSummary)),
		"details":         details,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal scribe handoff commentary: %w", err)
	}
	return string(raw), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneScribeMessages(messages []providers.Message) []providers.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]providers.Message, len(messages))
	for i, msg := range messages {
		cloned[i] = msg
		if len(msg.ToolCalls) > 0 {
			cloned[i].ToolCalls = append([]providers.ToolCall(nil), msg.ToolCalls...)
		}
		if msg.Metadata != nil {
			metadata := make(map[string]any, len(msg.Metadata))
			for key, value := range msg.Metadata {
				metadata[key] = value
			}
			cloned[i].Metadata = metadata
		}
	}
	return cloned
}
