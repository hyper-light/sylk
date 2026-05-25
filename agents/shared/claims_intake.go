package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
)

// ClaimsIntakeConfig bundles everything needed to wire event-driven
// claims intake on an agent.
type ClaimsIntakeConfig struct {
	// AgentID is the agent's unique identifier.
	AgentID string

	// SessionID scopes the inbox to one session's board.
	SessionID string

	// Role is the agent's claims-board role bitmask. Drives both the
	// bus subscription pattern set (claims.InboxPatternsFor) and the
	// receive-side standing-subscription gate. Zero defaults to
	// claims.RoleSubject so the agent at minimum receives directly-
	// addressed inbox deltas.
	Role claims.ClaimsRole

	// Bus is the agent's event bus. Used to subscribe to claims
	// delta topics via EventBusDeltaSubscriber.
	Bus guide.EventBus

	// Board is the session's claims board. Used for graph resolution
	// when a delta matches. Nil-safe.
	Board *claims.ClaimsBoard

	// Scope is the agent's goroutine scope. OnResolved dispatches
	// into this scope for tracked, async execution.
	Scope *concurrency.GoroutineScope

	// ProcessEntry is the agent's claims entry processing function.
	// Called under scope.Go for each matched delta. The agent
	// implements this to run its tool loop or handle the entry.
	ProcessEntry func(ctx context.Context, entry *claims.GraphEntryPoint) error

	// Identity is the agent's AgentIdentity. Stamped onto the
	// per-claim context before ProcessEntry runs, so downstream LLM
	// dispatches satisfy gateway.RequireIdentity. Required for any
	// agent whose ProcessEntry invokes the LLM provider — without
	// it, the gateway rejects the dispatch with
	// "no AgentIdentity on ctx" and the claim entry fails silently.
	Identity *identity.AgentIdentity

	// Factory mints a TaskRef per-claim that is stamped alongside
	// Identity, so the gateway also has a task on the context. The
	// task's correlation is the delta key, giving each claim entry
	// its own stable correlation in the gateway's accounting.
	// Optional but recommended; without it the gateway's
	// RequireDispatch fails on the missing task.
	Factory *identity.Factory

	// ContinuationStore handles ConsultResolvedDelta deliveries for
	// this agent. When set, deltas of kind consult_resolved are
	// routed to store.DeliverResolution INSTEAD of ProcessEntry —
	// resolutions feed pending continuations (waking yielded LLM
	// turns) rather than firing fresh inference.
	//
	// When nil, ConsultResolvedDeltas fall through to ProcessEntry
	// and would trigger LLM inference per resolution — almost
	// certainly wrong; agents that use ticket-mode consult_peer
	// MUST wire a store.
	ContinuationStore *ContinuationStore
}

// deltaConsultID extracts the ConsultID from a ConsultResolvedDelta
// (value or pointer form) for diagnostic logging. Returns empty when
// the delta is nil or not a ConsultResolvedDelta.
func deltaConsultID(d claims.Delta) string {
	switch v := d.(type) {
	case claims.ConsultResolvedDelta:
		return v.ConsultID
	case *claims.ConsultResolvedDelta:
		if v == nil {
			return ""
		}
		return v.ConsultID
	}
	return ""
}

func deliverExpectedPeerTestamentToContinuation(cfg ClaimsIntakeConfig, entry *claims.GraphEntryPoint) bool {
	if cfg.ContinuationStore == nil || entry == nil || entry.Expectation == nil || entry.Delta == nil {
		return false
	}
	if entry.Delta.DeltaKind() != claims.DeltaKindTestament {
		return false
	}
	actionKind := expectedPeerTestamentActionKind(entry)
	if actionKind != claims.ActionTypeConsultation && actionKind != claims.ActionTypeChallenge {
		return false
	}

	resolutionID := expectedPeerResolutionID(entry.Node.Claim, actionKind, entry.Expectation)
	if strings.TrimSpace(resolutionID) == "" {
		slog.Warn("claims_intake_expected_peer_testament_missing_resolution_id",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"claim_id", entry.Expectation.ClaimID,
			"action_kind", actionKind,
		)
		return true
	}

	testamentDelta, _ := testamentDeltaFromEntry(entry)
	testament := entry.Node.Testament
	summary := strings.TrimSpace(testamentDelta.Summary)
	if summary == "" && testament != nil {
		summary = strings.TrimSpace(testament.Summary)
	}
	status := claims.ConsultStatusCompleted
	errText := ""
	if testamentDelta.Verdict == claims.TestamentVerdictError {
		status = claims.ConsultStatusError
		errText = firstNonEmptyIntakeString(summary, "peer testament reported an error")
	}
	payload := peerTestamentResponsePayload(entry, summary)
	responder := strings.TrimSpace(testamentDelta.SubjectAgentID)
	if testament != nil && strings.TrimSpace(testament.AgentID) != "" {
		responder = strings.TrimSpace(testament.AgentID)
	}
	delta := claims.ConsultResolvedDelta{
		SessionID:         firstNonEmptyIntakeString(testamentDelta.SessionID, cfg.SessionID),
		BoardID:           testamentDelta.BoardID,
		ConsultID:         strings.TrimSpace(resolutionID),
		OriginatorAgentID: strings.TrimSpace(cfg.AgentID),
		ResponderAgentID:  responder,
		Status:            status,
		ResponsePayload:   payload,
		ResponseSummary:   truncatePromptString(summary, 240),
		ErrorMessage:      errText,
		EmittedAt:         time.Now().UTC(),
	}
	slog.Info("claims_intake_expected_peer_testament_delivered",
		"agent_id", cfg.AgentID,
		"session_id", cfg.SessionID,
		"claim_id", entry.Expectation.ClaimID,
		"testament_id", testamentDelta.TestamentID,
		"resolution_id", delta.ConsultID,
		"action_kind", actionKind,
		"status", status,
	)
	cfg.ContinuationStore.DeliverResolution(context.Background(), &delta)
	return true
}

func expectedPeerTestamentActionKind(entry *claims.GraphEntryPoint) claims.ActionType {
	if entry == nil {
		return ""
	}
	if entry.Node.Claim != nil && entry.Node.Claim.ActionType != "" {
		return entry.Node.Claim.ActionType
	}
	if delta, ok := testamentDeltaFromEntry(entry); ok {
		return delta.ActionKind
	}
	return ""
}

func expectedPeerResolutionID(c *claims.Claim, actionKind claims.ActionType, exp *claims.Expectation) string {
	if c != nil {
		switch actionKind {
		case claims.ActionTypeConsultation:
			if id := claimScopeValue(c.Scope, "consult_id"); id != "" {
				return id
			}
		case claims.ActionTypeChallenge:
			if id := claimScopeValue(c.Scope, "challenge_id"); id != "" {
				return id
			}
		}
		if id := claimScopeValue(c.Scope, "await_id"); id != "" {
			return id
		}
	}
	if exp != nil && strings.TrimSpace(exp.ActionID) != "" {
		return strings.TrimSpace(exp.ActionID)
	}
	if c != nil {
		return strings.TrimSpace(c.ID)
	}
	if exp != nil {
		return strings.TrimSpace(exp.ClaimID)
	}
	return ""
}

func claimScopeValue(scope []claims.ClaimScopeEntry, kind string) string {
	kind = strings.TrimSpace(kind)
	for _, entry := range scope {
		if strings.TrimSpace(entry.Kind) == kind {
			return strings.TrimSpace(entry.Key)
		}
	}
	return ""
}

func testamentDeltaFromEntry(entry *claims.GraphEntryPoint) (claims.TestamentDelta, bool) {
	if entry == nil || entry.Delta == nil {
		return claims.TestamentDelta{}, false
	}
	switch delta := entry.Delta.(type) {
	case claims.TestamentDelta:
		return delta, true
	case *claims.TestamentDelta:
		if delta != nil {
			return *delta, true
		}
	}
	return claims.TestamentDelta{}, false
}

func peerTestamentResponsePayload(entry *claims.GraphEntryPoint, summary string) json.RawMessage {
	payload := map[string]any{
		"response": summary,
		"summary":  summary,
	}
	if entry != nil {
		if c := entry.Node.Claim; c != nil {
			payload["claim_id"] = strings.TrimSpace(c.ID)
			payload["claim_title"] = strings.TrimSpace(c.Title)
		}
		if t := entry.Node.Testament; t != nil {
			payload["testament_id"] = strings.TrimSpace(t.ID)
			payload["agent_id"] = strings.TrimSpace(t.AgentID)
			payload["confidence"] = strings.TrimSpace(t.Confidence)
			if len(t.Artifacts) > 0 {
				payload["artifact_count"] = len(t.Artifacts)
				payload["artifacts"] = compactTestamentArtifacts(t.Artifacts)
			}
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"response":""}`)
	}
	return encoded
}

func compactTestamentArtifacts(artifacts []*claims.Artifact) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, art := range artifacts {
		if art == nil {
			continue
		}
		item := map[string]any{
			"id":        strings.TrimSpace(art.ID),
			"kind":      strings.TrimSpace(art.Kind),
			"reference": truncatePromptString(strings.TrimSpace(art.Reference), 800),
		}
		if len(art.Metadata) > 0 {
			item["metadata"] = art.Metadata
		}
		out = append(out, item)
	}
	return out
}

func firstNonEmptyIntakeString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// WireClaimsIntake creates a ClaimsInbox with event-driven dispatch.
// When a delta matches an expectation or standing subscription, the
// inbox resolves it into a GraphEntryPoint and dispatches it to
// ProcessEntry via scope.Go. Returns the inbox (caller must Start
// and Close it) or nil if the config is insufficient.
func WireClaimsIntake(cfg ClaimsIntakeConfig) *claims.ClaimsInbox {
	if cfg.AgentID == "" || cfg.SessionID == "" || cfg.Bus == nil {
		slog.Warn("claims_intake_skipped_insufficient_config",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"bus_nil", cfg.Bus == nil,
		)
		return nil
	}
	role := cfg.Role
	if role == 0 {
		role = claims.RoleSubject
	}
	// Identity stamping is required for any agent whose ProcessEntry
	// invokes the LLM gateway — without it, gateway.RequireIdentity
	// rejects the dispatch and the claim entry fails silently in a
	// background goroutine. Refuse to wire the intake when this
	// invariant cannot be satisfied so the breakage is loud at
	// startup instead of opaque under load.
	//
	// The narrow exemption: an intake whose ProcessEntry is itself
	// nil cannot dispatch anything, so a missing Identity there is
	// harmless. That carve-out is for tests that wire only the inbox
	// matching surface without a handler.
	if cfg.ProcessEntry != nil && cfg.Identity == nil && !role.Has(claims.RoleObserver) {
		slog.Error("claims_intake_missing_identity",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"reason", "ProcessEntry will run without AgentIdentity on ctx; gateway.RequireIdentity will reject every LLM dispatch",
		)
		return nil
	}
	// A missing scope means OnResolved would fall back to running
	// ProcessEntry inline on the bus subscriber's goroutine — which
	// (a) blocks bus delivery for the duration of the LLM call,
	// (b) prevents the accumulator from flushing testaments async
	//     because Flush also needs scope.Go,
	// (c) is exactly the symptom we just spent debugging:
	//     `claims_intake_wiring scope_present=false` followed by
	//     `accumulator_flush_scope_unwired` and dropped testaments.
	// Refuse to wire without scope so the breakage is loud at startup.
	if cfg.ProcessEntry != nil && cfg.Scope == nil && !role.Has(claims.RoleObserver) {
		slog.Error("claims_intake_missing_scope",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"reason", "ProcessEntry needs a scope to dispatch async; without it accumulator flushes drop and OnResolved blocks the bus",
		)
		return nil
	}

	subscriber := &EventBusDeltaSubscriber{bus: cfg.Bus}

	slog.Info("claims_intake_wiring",
		"agent_id", cfg.AgentID,
		"session_id", cfg.SessionID,
		"role", role,
		"board_present", cfg.Board != nil,
		"scope_present", cfg.Scope != nil,
		"patterns", claims.InboxPatternsFor(role, cfg.SessionID, cfg.AgentID),
	)

	inbox, err := claims.NewClaimsInbox(claims.InboxConfig{
		AgentID:    cfg.AgentID,
		SessionID:  cfg.SessionID,
		Role:       role,
		Subscriber: subscriber,
		Board:      cfg.Board,
		OnResolved: func(entry *claims.GraphEntryPoint) {
			if entry == nil {
				return
			}
			// Pre-empt: ConsultResolvedDelta entries route to the
			// ContinuationStore (waking pending continuations or
			// stashing as orphans) and MUST NOT fall through to
			// ProcessEntry. The ProcessEntry path triggers LLM
			// inference; firing inference on every resolution
			// would loop the agent's own consults back into its
			// own loop.
			if delta := entry.Delta; delta != nil && delta.DeltaKind() == claims.DeltaKindConsultResolved {
				if cfg.ContinuationStore == nil {
					slog.Warn("consult_resolved_dropped_no_continuation_store",
						"agent_id", cfg.AgentID,
						"session_id", cfg.SessionID,
						"consult_id", deltaConsultID(delta),
					)
					return
				}
				resolved, ok := delta.(claims.ConsultResolvedDelta)
				if !ok {
					if ptr, ok2 := delta.(*claims.ConsultResolvedDelta); ok2 && ptr != nil {
						resolved = *ptr
						ok = true
					}
				}
				if !ok {
					slog.Warn("consult_resolved_unexpected_payload_type",
						"agent_id", cfg.AgentID,
						"delta_kind", delta.DeltaKind(),
					)
					return
				}
				cfg.ContinuationStore.DeliverResolution(context.Background(), &resolved)
				return
			}
			if deliverExpectedPeerTestamentToContinuation(cfg, entry) {
				return
			}
			slog.Info("claims_intake_resolved",
				"agent_id", cfg.AgentID,
				"session_id", cfg.SessionID,
				"delta_kind", entry.Delta.DeltaKind(),
				"delta_key", entry.Delta.DeltaKey(),
				"node_has_claim", entry.Node.Claim != nil,
				"node_has_testament", entry.Node.Testament != nil,
				"node_has_validation", entry.Node.Validation != nil,
				"identity_present", cfg.Identity != nil,
				"factory_present", cfg.Factory != nil,
			)
			if cfg.Scope != nil && cfg.ProcessEntry != nil {
				if err := cfg.Scope.Go("process_claim", 0, func(ctx context.Context) error {
					return cfg.ProcessEntry(stampClaimsIntakeContext(ctx, cfg, entry), entry)
				}); err != nil {
					slog.Error("claims_intake_dispatch_failed",
						"agent_id", cfg.AgentID,
						"error", err.Error(),
					)
				}
				return
			}
			if cfg.ProcessEntry != nil {
				ctx := stampClaimsIntakeContext(context.Background(), cfg, entry)
				if err := cfg.ProcessEntry(ctx, entry); err != nil {
					slog.Error("claims_intake_process_failed",
						"agent_id", cfg.AgentID,
						"error", err.Error(),
					)
				}
			}
		},
	})
	if err != nil {
		slog.Error("claims_intake_create_failed",
			"agent_id", cfg.AgentID,
			"error", err.Error(),
		)
		return nil
	}
	// Register the inbox with the process-wide registry so peer
	// publishers can read its ConsultBudget before issuing consults.
	// Last-write-wins semantics handle agent re-wiring (credential
	// refresh, etc.); the agent's own Close path should call
	// DefaultSessionInboxRegistry().Remove(sessionID, agentID) to
	// avoid stale entries surviving a restart.
	claims.DefaultSessionInboxRegistry().Register(cfg.SessionID, cfg.AgentID, inbox)
	return inbox
}

// stampClaimsIntakeContext attaches the agent's AgentIdentity, a
// per-claim TaskRef, and a per-claim LogMeta to ctx so downstream
// LLM dispatches pass gateway.RequireDispatch and tool invocations
// pass toolruntime.validateInvocation's correlation_id check. The
// task and LogMeta both use the delta key as correlation, giving
// each claim entry a stable correlation across retries.
//
// LogMeta stamping is unconditional: every agent that resolves
// shared.LogMetaFromContext(ctx).CorrID for tool invocation needs a
// non-empty value, and the delta key is the natural per-entry
// correlation. Without it the architect's tool runtime rejects
// every tool call with "correlation_id is required for tool
// invocation".
//
// When Identity is nil the identity stamping is skipped — the agent
// is presumably fine with running without the gateway identity
// check (e.g. test-mode agents). When Factory is nil only identity
// is stamped; the task field stays absent and the gateway will
// reject the dispatch on RequireTask. Both nil is still a partial
// stamp because LogMeta runs first and unconditionally.
func stampClaimsIntakeContext(ctx context.Context, cfg ClaimsIntakeConfig, entry *claims.GraphEntryPoint) context.Context {
	correlation := ""
	if entry != nil {
		correlation = strings.TrimSpace(entry.Delta.DeltaKey())
	}
	if correlation == "" {
		correlation = "claims_intake_" + cfg.AgentID
	}
	ctx = WithLogMeta(ctx, LogMeta{
		CorrID:    correlation,
		AgentID:   cfg.AgentID,
		SessionID: cfg.SessionID,
	})
	// Stamp the parent claim ID so any TestamentAccumulator the agent
	// creates during this dispatch automatically inherits it. Per
	// CLAIMS.md §5.1, every artifact recorded during processing is
	// evidence on this claim — the chat panel uses ClaimID to route
	// child rows under the correct parent claim row.
	if entry != nil && entry.Node.Claim != nil {
		ctx = claims.WithParentClaimID(ctx, entry.Node.Claim.ID)
	}
	if cfg.Identity != nil {
		ctx = identity.WithIdentity(ctx, cfg.Identity)
	}
	if cfg.Factory != nil && entry != nil {
		task, taskErr := cfg.Factory.NewTask(identity.TaskOptions{
			DisplayID:   correlation,
			Correlation: identity.CorrelationID(correlation),
		})
		if taskErr != nil {
			slog.Warn("claims_intake_mint_task_failed",
				"agent_id", cfg.AgentID,
				"delta_key", correlation,
				"error", taskErr.Error(),
			)
			return ctx
		}
		ctx = identity.WithTask(ctx, task)
	}
	return ctx
}

// ComposeClaimsEntryPrompt builds a user-message prompt from a
// GraphEntryPoint. This is the prompt the agent's tool loop receives
// when a claims delta matches. The prompt includes the delta kind,
// the claim/testament/validation content, the node's edges for
// traversal context, and guidance on which skills to use.
func ComposeClaimsEntryPrompt(entry *claims.GraphEntryPoint) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Incoming Claims Event\n\n")
	b.WriteString("**Delta kind:** " + entry.Delta.DeltaKind() + "\n")
	if entry.Expectation != nil {
		b.WriteString("**Matches expectation:** claim " + entry.Expectation.ClaimID + " (action " + entry.Expectation.ActionID + ")\n")
	}
	b.WriteString("\n")

	node := entry.Node
	switch {
	case node.Claim != nil:
		c := node.Claim
		b.WriteString("### Claim: " + c.Title + "\n\n")
		if c.Description != "" {
			b.WriteString(c.Description + "\n\n")
		}
		b.WriteString("- **Status:** " + string(c.Status) + "\n")
		b.WriteString("- **Action type:** " + string(c.ActionType) + "\n")
		if issuer := claims.IssuerAgentID(c.Relations); issuer != "" {
			b.WriteString("- **Issuer:** " + issuer + "\n")
		}
		if subject := claims.SubjectAgentID(c.Relations); subject != "" {
			b.WriteString("- **Subject:** " + subject + "\n")
		}
		if len(c.Scope) > 0 {
			b.WriteString("- **Scope:** ")
			for i, s := range c.Scope {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(s.Kind + ":" + s.Key)
			}
			b.WriteString("\n")
		}
		if len(c.Validations) > 0 {
			b.WriteString("\n**Validations required:**\n")
			for _, v := range c.Validations {
				b.WriteString("- " + v.Description)
				if v.QualityBar != "" {
					b.WriteString(" (bar: " + v.QualityBar + ")")
				}
				b.WriteString("\n")
			}
		}

	case node.Testament != nil:
		t := node.Testament
		b.WriteString("### Testament Response\n\n")
		b.WriteString("**Summary:** " + t.Summary + "\n")
		b.WriteString("**Confidence:** " + t.Confidence + "\n")
		if len(t.Artifacts) > 0 {
			b.WriteString("\n**Artifacts:**\n")
			for _, a := range t.Artifacts {
				b.WriteString("- [" + a.Kind + "] " + truncatePromptString(a.Reference, 200) + "\n")
			}
		}

		// When the parent claim is available, show its pending validations
		// as explicit instructions for the agent to evaluate.
		if node.Claim != nil {
			composeValidationInstructions(&b, node.Claim)
		}

	case node.Validation != nil:
		v := node.Validation
		b.WriteString("### Validation Verdict\n\n")
		b.WriteString("**Status:** " + string(v.Status) + "\n")
		b.WriteString("**Description:** " + v.Description + "\n")
		if v.QualityBar != "" {
			b.WriteString("**Quality bar:** " + v.QualityBar + "\n")
		}

	default:
		b.WriteString("### Phase Transition or Board Event\n\n")
		b.WriteString("Use `traverse` to inspect the board state.\n")
	}

	if len(node.Edges) > 0 {
		b.WriteString("\n**Graph edges** (use `traverse` to explore):\n")
		limit := len(node.Edges)
		if limit > 10 {
			limit = 10
		}
		for _, e := range node.Edges[:limit] {
			b.WriteString("- " + e.Relationship + " → " + e.TargetType + ":" + e.TargetID + "\n")
		}
		if len(node.Edges) > 10 {
			b.WriteString("- ... and " + fmt.Sprintf("%d", len(node.Edges)-10) + " more\n")
		}
	}

	b.WriteString("\n---\n\n")
	b.WriteString("Process this event using your skills. Use `traverse` for more context, ")
	b.WriteString("`post_action` to issue sub-claims, `submit_testaments` to respond, ")
	b.WriteString("`evaluate_validation` to judge responses.\n")

	return b.String()
}

// composeValidationInstructions renders the parent claim's pending
// validations as explicit evaluation directives for the agent. Each
// validation's Description and QualityBar tell the agent what to check
// and what bar to meet. The agent uses its full skill surface to assess
// the testament artifacts, then calls evaluate_validation for each one.
func composeValidationInstructions(b *strings.Builder, claim *claims.Claim) {
	var pending []*claims.Validation
	for _, v := range claim.Validations {
		if v != nil && v.Status == claims.ValidationStatusPending && v.Type != claims.ValidationTypeReceipt {
			pending = append(pending, v)
		}
	}
	if len(pending) == 0 {
		return
	}

	b.WriteString("\n### Parent Claim: " + claim.Title + "\n\n")
	if claim.Description != "" {
		b.WriteString(claim.Description + "\n\n")
	}

	b.WriteString("**Pending validations — evaluate each using the testament artifacts above:**\n\n")
	for _, v := range pending {
		required := ""
		if v.Required {
			required = " **(required)**"
		}
		b.WriteString("- **" + v.Description + "**" + required + "\n")
		if v.QualityBar != "" {
			b.WriteString("  Quality bar: " + v.QualityBar + "\n")
		}
		b.WriteString("  Validation ID: `" + v.ID + "` | Claim ID: `" + claim.ID + "` | Type: " + string(v.Type) + "\n")
	}

	b.WriteString("\nFor each validation: use your skills to assess whether the artifacts satisfy the quality bar. ")
	b.WriteString("Then call `evaluate_validation` with the claim_id, validation_id, and your verdict (passed/failed) with a reason.\n")
}

func truncatePromptString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// EventBusDeltaSubscriber adapts a guide.EventBus to the
// claims.DeltaSubscriber interface. Subscribes to claims delta topics
// via SubscribeAsync and extracts claims.Delta from the message
// payload.
type EventBusDeltaSubscriber struct {
	bus guide.EventBus
}

// SubscribeDelta registers a claims delta handler on the bus topic
// pattern. The handler is called for every MessageTypeClaimsDelta
// message matching the pattern.
func (s *EventBusDeltaSubscriber) SubscribeDelta(pattern string, handler claims.DeltaHandler) (claims.DeltaSubscription, error) {
	if s.bus == nil {
		return claims.NoopDeltaBus{}.SubscribeDelta(pattern, handler)
	}
	sub, err := s.bus.SubscribeAsync(pattern, func(msg *guide.Message) error {
		delta, extractErr := guide.ExtractClaimsDelta(msg)
		if extractErr != nil || delta == nil {
			return nil
		}
		handler(delta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &eventBusDeltaSub{sub: sub}, nil
}

type eventBusDeltaSub struct {
	sub guide.Subscription
}

func (s *eventBusDeltaSub) Topic() string {
	if s.sub == nil {
		return ""
	}
	return s.sub.Topic()
}

func (s *eventBusDeltaSub) Unsubscribe() error {
	if s.sub == nil {
		return nil
	}
	return s.sub.Unsubscribe()
}
