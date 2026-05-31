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
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
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

	// ContinuationStore handles canonical response deltas for this
	// agent. Expected testament.posted and terminal claim lifecycle
	// deltas are routed to the store INSTEAD of ProcessEntry —
	// responses feed pending continuations (waking yielded LLM turns)
	// rather than firing fresh inference.
	ContinuationStore *ContinuationStore

	// ExpectedToolRuntime is the agent's ordinary Sylk skill runtime.
	// When a testament lands on a claim with pending validation
	// ExpectedToolCalls owned by this agent, the intake executes those
	// tools through a transient request view and records the results as
	// validation evidence artifacts.
	ExpectedToolRuntime *toolruntime.Runtime

	// ExpectedToolExecutor is an optional test seam or specialized
	// executor. When set, it takes precedence over ExpectedToolRuntime.
	ExpectedToolExecutor claims.ExpectedToolExecutor

	ExpectedToolPolicy    claims.ExpectedToolPolicy
	ExpectedToolRedactor  claims.ExpectedToolArgumentRedactor
	ExpectedToolAllowlist map[string]bool
	ExpectedToolApprovals map[string]bool
	ExpectedToolRemediate claims.ValidationExpectedToolRemediationPoster
	CancelRegistry        *claims.ClaimCancelRegistry
}

func shouldSuppressForwardedPromptEntry(role claims.ClaimsRole, entry *claims.GraphEntryPoint) bool {
	if role.Has(claims.RoleObserver) || entry == nil || entry.Node.Claim == nil {
		return false
	}
	claim := entry.Node.Claim
	if claim.ActionType != claims.ActionTypePrompt {
		return false
	}
	if claims.IssuerAgentID(claim.Relations) != "guide" {
		return false
	}
	return claimHasTag(claim.Tags, "user_prompt")
}

func claimHasTag(tags []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func deliverExpectedPeerResultToContinuation(cfg ClaimsIntakeConfig, entry *claims.GraphEntryPoint) bool {
	if cfg.ContinuationStore == nil || entry == nil || entry.Expectation == nil || entry.Delta == nil {
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

	result, ok := awaitedClaimResultFromEntry(entry, resolutionID, cfg)
	if !ok {
		return false
	}
	slog.Info("claims_intake_expected_peer_result_delivered",
		"agent_id", cfg.AgentID,
		"session_id", cfg.SessionID,
		"claim_id", entry.Expectation.ClaimID,
		"testament_id", result.TestamentID,
		"resolution_id", result.ClaimID,
		"action_kind", actionKind,
		"delta_action", result.Action,
		"status", result.Status,
	)
	cfg.ContinuationStore.DeliverClaimResult(context.Background(), result)
	return true
}

func dispatchExpectedValidationTools(cfg ClaimsIntakeConfig, entry *claims.GraphEntryPoint) bool {
	if entry == nil || entry.Node.Claim == nil || !entryIsTestamentSubmitted(entry) {
		return false
	}
	executor := expectedToolExecutorFromConfig(cfg)
	if executor == nil {
		return false
	}
	var validationIDs []string
	for _, validation := range entry.Node.Claim.Validations {
		if shouldExecuteExpectedValidationTools(cfg, entry.Node.Claim, validation) {
			validationIDs = append(validationIDs, validation.ID)
		}
	}
	if len(validationIDs) == 0 {
		return false
	}
	run := func(ctx context.Context) error {
		claimID := entry.Node.Claim.ID
		runCtx, reg := claimIntakeContext(ctx, cfg, claimID)
		defer reg.Done()
		for _, validationID := range validationIDs {
			result, err := claims.ExecuteValidationExpectedTools(runCtx, cfg.Board, claimID, validationID, claims.ExpectedToolExecutionOptions{
				AgentID:           cfg.AgentID,
				Executor:          executor,
				Policy:            cfg.ExpectedToolPolicy,
				Redactor:          cfg.ExpectedToolRedactor,
				AllowedTools:      cfg.ExpectedToolAllowlist,
				ApprovedToolIDs:   cfg.ExpectedToolApprovals,
				RemediationPoster: cfg.ExpectedToolRemediate,
			})
			if err != nil {
				slog.Error("claims_intake_expected_validation_tools_failed",
					"agent_id", cfg.AgentID,
					"session_id", cfg.SessionID,
					"claim_id", entry.Node.Claim.ID,
					"validation_id", validationID,
					"error", err.Error(),
				)
				continue
			}
			slog.Info("claims_intake_expected_validation_tools_completed",
				"agent_id", cfg.AgentID,
				"session_id", cfg.SessionID,
				"claim_id", result.ClaimID,
				"validation_id", result.ValidationID,
				"attempts", len(result.Attempts),
				"status", result.ValidationStatus,
				"already_terminal", result.AlreadyTerminal,
			)
		}
		return nil
	}
	if cfg.Scope != nil {
		if err := cfg.Scope.Go("expected_validation_tools", 0, run); err != nil {
			slog.Error("claims_intake_expected_validation_tools_dispatch_failed",
				"agent_id", cfg.AgentID,
				"session_id", cfg.SessionID,
				"claim_id", entry.Node.Claim.ID,
				"error", err.Error(),
			)
			return false
		}
		return true
	}
	if err := run(context.Background()); err != nil {
		slog.Error("claims_intake_expected_validation_tools_inline_failed",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"claim_id", entry.Node.Claim.ID,
			"error", err.Error(),
		)
	}
	return true
}

func shouldExecuteExpectedValidationTools(cfg ClaimsIntakeConfig, claim *claims.Claim, validation *claims.Validation) bool {
	if claim == nil || validation == nil || validation.Status != claims.ValidationStatusPending || len(validation.ExpectedToolCalls) == 0 {
		return false
	}
	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		return false
	}
	if validationAgent := strings.TrimSpace(validation.AgentID); validationAgent != "" {
		return sameAgentID(agentID, validationAgent)
	}
	issuer := claims.IssuerAgentID(claim.Relations)
	return issuer != "" && sameAgentID(agentID, issuer)
}

func sameAgentID(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func entryIsTestamentSubmitted(entry *claims.GraphEntryPoint) bool {
	if entry == nil || entry.Delta == nil {
		return false
	}
	switch delta := entry.Delta.(type) {
	case claims.CanonicalDelta:
		return delta.Action == claims.DeltaActionTestamentPosted
	case *claims.CanonicalDelta:
		return delta != nil && delta.Action == claims.DeltaActionTestamentPosted
	default:
		return false
	}
}

func expectedToolExecutorFromConfig(cfg ClaimsIntakeConfig) claims.ExpectedToolExecutor {
	if cfg.ExpectedToolExecutor != nil {
		return cfg.ExpectedToolExecutor
	}
	if cfg.ExpectedToolRuntime == nil {
		return nil
	}
	return runtimeExpectedToolExecutor{
		runtime: cfg.ExpectedToolRuntime,
		agentID: cfg.AgentID,
	}
}

type runtimeExpectedToolExecutor struct {
	runtime *toolruntime.Runtime
	agentID string
}

func (e runtimeExpectedToolExecutor) ExecuteExpectedTool(ctx context.Context, call claims.ExpectedToolCall) (claims.ExpectedToolExecutionOutput, error) {
	if e.runtime == nil {
		return claims.ExpectedToolExecutionOutput{}, fmt.Errorf("tool runtime is not configured")
	}
	toolName := strings.TrimSpace(call.Tool)
	if toolName == "" {
		return claims.ExpectedToolExecutionOutput{}, fmt.Errorf("expected tool name is required")
	}
	view, err := e.runtime.RequestView(toolName)
	if err != nil {
		return claims.ExpectedToolExecutionOutput{}, err
	}
	arguments := call.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	rawArgs, err := json.Marshal(arguments)
	if err != nil {
		return claims.ExpectedToolExecutionOutput{}, fmt.Errorf("marshal expected tool arguments: %w", err)
	}
	toolID := strings.TrimSpace(call.ID)
	if toolID == "" {
		toolID = "expected_" + toolName
	}
	agentID := firstNonEmptyIntakeString(e.runtime.AgentID(), e.agentID)
	result, err := view.Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        toolID,
			Name:      toolName,
			Arguments: string(rawArgs),
		},
		AgentID:         agentID,
		CorrelationID:   "expected_validation:" + toolID,
		CapabilityScope: view.CapabilityScope(),
	})
	if err != nil {
		return claims.ExpectedToolExecutionOutput{}, err
	}
	if result.Yielded() {
		return claims.ExpectedToolExecutionOutput{}, fmt.Errorf("expected validation tool %q yielded; validation expected tools must complete synchronously", toolName)
	}
	return claims.ExpectedToolExecutionOutput{
		Output:  result.Output,
		Summary: truncatePromptString(result.Output, 240),
		Metadata: map[string]any{
			"tool_name":        result.ToolName,
			"activated_skills": append([]string(nil), result.ActivatedSkills...),
			"tool_defs_dirty":  result.ToolDefsDirty,
		},
	}, nil
}

func awaitedClaimResultFromEntry(entry *claims.GraphEntryPoint, resolutionID string, cfg ClaimsIntakeConfig) (*AwaitedClaimResult, bool) {
	if entry == nil || entry.Delta == nil {
		return nil, false
	}
	if signal, ok := peerTestamentSignalFromEntry(entry); ok {
		return awaitedClaimResultFromPeerTestament(entry, signal, resolutionID, cfg), true
	}
	if signal, ok := terminalClaimSignalFromEntry(entry); ok {
		return awaitedClaimResultFromTerminalClaim(entry, signal, resolutionID, cfg), true
	}
	return nil, false
}

func awaitedClaimResultFromPeerTestament(entry *claims.GraphEntryPoint, signal peerTestamentSignal, resolutionID string, cfg ClaimsIntakeConfig) *AwaitedClaimResult {
	testament := entry.Node.Testament
	summary := strings.TrimSpace(signal.Context)
	if summary == "" && testament != nil {
		summary = firstNonEmptyIntakeString(testament.Context, testament.Summary)
	}
	status := claims.ConsultStatusCompleted
	errText := ""
	if signal.Verdict == claims.TestamentVerdictError {
		status = claims.ConsultStatusError
		errText = firstNonEmptyIntakeString(summary, "peer testament reported an error")
	}
	payload := peerTestamentResponsePayload(entry, summary)
	responder := strings.TrimSpace(signal.ResponderAgentID)
	if testament != nil && strings.TrimSpace(testament.AgentID) != "" {
		responder = strings.TrimSpace(testament.AgentID)
	}
	return (&AwaitedClaimResult{
		SessionID:        firstNonEmptyIntakeString(signal.SessionID, cfg.SessionID),
		BoardID:          signal.BoardID,
		ClaimID:          strings.TrimSpace(resolutionID),
		TestamentID:      signal.TestamentID,
		DeltaKey:         entry.Delta.DeltaKey(),
		Action:           claims.DeltaActionTestamentPosted,
		Verdict:          signal.Verdict,
		Context:          summary,
		ResponderAgentID: responder,
		Status:           status,
		ResponsePayload:  payload,
		ResponseSummary:  truncatePromptString(summary, 240),
		ErrorMessage:     errText,
		EmittedAt:        time.Now().UTC(),
	}).normalized()
}

type terminalClaimSignal struct {
	SessionID       string
	BoardID         string
	ClaimID         string
	Status          claims.ClaimStatus
	LifecycleStatus claims.ClaimLifecycleStatus
	Context         string
	ActorID         string
}

func terminalClaimSignalFromEntry(entry *claims.GraphEntryPoint) (terminalClaimSignal, bool) {
	if entry == nil || entry.Delta == nil {
		return terminalClaimSignal{}, false
	}
	switch delta := entry.Delta.(type) {
	case claims.CanonicalDelta:
		return terminalClaimSignalFromCanonical(delta)
	case *claims.CanonicalDelta:
		if delta != nil {
			return terminalClaimSignalFromCanonical(*delta)
		}
	}
	return terminalClaimSignal{}, false
}

func terminalClaimSignalFromCanonical(delta claims.CanonicalDelta) (terminalClaimSignal, bool) {
	if !canonicalClaimLifecycleResolvesAwait(delta) {
		return terminalClaimSignal{}, false
	}
	lifecycle, _ := claims.DeltaActionClaimLifecycleStatus(delta.Action)
	status := delta.ClaimToStatus()
	if !claimStatusResolvesAwait(status) {
		return terminalClaimSignal{}, false
	}
	context := ""
	if claim, ok := delta.Context["claim"].(map[string]any); ok {
		context = firstNonEmptyIntakeString(stringFromAny(claim["context"]), stringFromAny(claim["reason"]))
	}
	return terminalClaimSignal{
		SessionID:       delta.SessionID,
		BoardID:         delta.BoardID,
		ClaimID:         delta.ClaimID(),
		Status:          status,
		LifecycleStatus: lifecycle,
		Context:         context,
		ActorID:         delta.Actor.RouteKey(),
	}, true
}

func claimStatusResolvesAwait(status claims.ClaimStatus) bool {
	switch status {
	case claims.ClaimStatusTestified, claims.ClaimStatusAccepted:
		return true
	case claims.ClaimStatusRejected, claims.ClaimStatusSuperseded:
		return true
	default:
		return status.IsTerminal()
	}
}

func canonicalClaimLifecycleResolvesAwait(delta claims.CanonicalDelta) bool {
	status, ok := claims.DeltaActionClaimLifecycleStatus(delta.Action)
	if !ok {
		return false
	}
	switch status {
	case claims.ClaimLifecycleTestamentAcknowledged,
		claims.ClaimLifecycleSatisfied,
		claims.ClaimLifecycleValidationIncomplete,
		claims.ClaimLifecycleValidationFailed,
		claims.ClaimLifecycleValidationErrored,
		claims.ClaimLifecycleTestamentGenerationFailed,
		claims.ClaimLifecycleTestamentAcknowledgementFailed:
		return true
	default:
		return false
	}
}

func claimStatusResultAction(signal terminalClaimSignal) claims.DeltaAction {
	switch signal.LifecycleStatus {
	case claims.ClaimLifecycleTestamentAcknowledged:
		return claims.DeltaActionClaimTestamentAcknowledged
	case claims.ClaimLifecycleSatisfied:
		return claims.DeltaActionClaimSatisfied
	case claims.ClaimLifecycleValidationIncomplete:
		return claims.DeltaActionClaimValidationIncomplete
	case claims.ClaimLifecycleValidationFailed:
		return claims.DeltaActionClaimValidationFailed
	case claims.ClaimLifecycleValidationErrored:
		return claims.DeltaActionClaimValidationErrored
	case claims.ClaimLifecycleTestamentGenerationFailed:
		return claims.DeltaActionClaimTestamentGenerationFailed
	case claims.ClaimLifecycleTestamentAcknowledgementFailed:
		return claims.DeltaActionClaimTestamentAcknowledgementFailed
	}
	switch signal.Status {
	case claims.ClaimStatusTestified:
		return claims.DeltaActionClaimTestamentAcknowledged
	case claims.ClaimStatusAccepted:
		return claims.DeltaActionClaimSatisfied
	default:
		return claims.DeltaActionClaimValidationFailed
	}
}

func awaitedClaimResultFromTerminalClaim(entry *claims.GraphEntryPoint, signal terminalClaimSignal, resolutionID string, cfg ClaimsIntakeConfig) *AwaitedClaimResult {
	summary := strings.TrimSpace(signal.Context)
	if summary == "" && entry.Node.Claim != nil {
		summary = firstNonEmptyIntakeString(entry.Node.Claim.Context, entry.Node.Claim.Description, entry.Node.Claim.Title)
	}
	status := claims.ConsultStatusCompleted
	errText := ""
	if signal.Status == claims.ClaimStatusRejected || signal.Status == claims.ClaimStatusSuperseded {
		status = claims.ConsultStatusError
		errText = firstNonEmptyIntakeString(summary, "peer claim transitioned to "+string(signal.Status))
	}
	payload, _ := json.Marshal(map[string]any{
		"claim_id":     firstNonEmptyIntakeString(signal.ClaimID, resolutionID),
		"claim_status": string(signal.Status),
		"summary":      summary,
	})
	return (&AwaitedClaimResult{
		SessionID:        firstNonEmptyIntakeString(signal.SessionID, cfg.SessionID),
		BoardID:          signal.BoardID,
		ClaimID:          strings.TrimSpace(resolutionID),
		DeltaKey:         entry.Delta.DeltaKey(),
		Action:           claimStatusResultAction(signal),
		Context:          summary,
		ResponderAgentID: signal.ActorID,
		Status:           status,
		ResponsePayload:  payload,
		ResponseSummary:  truncatePromptString(summary, 240),
		ErrorMessage:     errText,
		EmittedAt:        time.Now().UTC(),
	}).normalized()
}

func expectedPeerTestamentActionKind(entry *claims.GraphEntryPoint) claims.ActionType {
	if entry == nil {
		return ""
	}
	if entry.Node.Claim != nil && entry.Node.Claim.ActionType != "" {
		return entry.Node.Claim.ActionType
	}
	if signal, ok := peerTestamentSignalFromEntry(entry); ok {
		return signal.ActionKind
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

type peerTestamentSignal struct {
	SessionID        string
	BoardID          string
	ClaimID          string
	TestamentID      string
	ActionKind       claims.ActionType
	Verdict          string
	ResponderAgentID string
	Context          string
}

func peerTestamentSignalFromEntry(entry *claims.GraphEntryPoint) (peerTestamentSignal, bool) {
	if entry == nil || entry.Delta == nil {
		return peerTestamentSignal{}, false
	}
	switch delta := entry.Delta.(type) {
	case claims.CanonicalDelta:
		return peerTestamentSignalFromCanonicalDelta(delta)
	case *claims.CanonicalDelta:
		if delta != nil {
			return peerTestamentSignalFromCanonicalDelta(*delta)
		}
	}
	return peerTestamentSignal{}, false
}

func peerTestamentSignalFromCanonicalDelta(delta claims.CanonicalDelta) (peerTestamentSignal, bool) {
	if delta.Action != claims.DeltaActionTestamentPosted {
		return peerTestamentSignal{}, false
	}
	signal := peerTestamentSignal{
		SessionID:        delta.SessionID,
		BoardID:          delta.BoardID,
		ClaimID:          delta.ClaimID(),
		TestamentID:      delta.TestamentID(),
		ActionKind:       delta.ClaimActionType(),
		ResponderAgentID: delta.Actor.RouteKey(),
	}
	if testament := firstCanonicalTestamentContext(delta.Context); testament != nil {
		signal.TestamentID = firstNonEmptyIntakeString(stringFromAny(testament["id"]), signal.TestamentID)
		signal.Verdict = stringFromAny(testament["verdict"])
		signal.Context = firstNonEmptyIntakeString(
			stringFromAny(testament["context"]),
			stringFromAny(testament["summary"]),
		)
	}
	return signal, true
}

func firstCanonicalTestamentContext(context map[string]any) map[string]any {
	raw, ok := context["testaments"]
	if !ok {
		return nil
	}
	switch testaments := raw.(type) {
	case []map[string]any:
		if len(testaments) > 0 {
			return testaments[0]
		}
	case []any:
		for _, item := range testaments {
			if m, ok := item.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
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
	if cfg.CancelRegistry == nil {
		cfg.CancelRegistry = claims.NewClaimCancelRegistry()
	}

	slog.Info("claims_intake_wiring",
		"agent_id", cfg.AgentID,
		"session_id", cfg.SessionID,
		"role", role,
		"board_present", cfg.Board != nil,
		"scope_present", cfg.Scope != nil,
		"patterns", claims.InboxPatternsFor(role, cfg.SessionID, cfg.AgentID),
	)

	inbox, err := claims.NewClaimsInbox(claims.InboxConfig{
		AgentID:        cfg.AgentID,
		SessionID:      cfg.SessionID,
		Role:           role,
		Subscriber:     subscriber,
		Board:          cfg.Board,
		CancelRegistry: cfg.CancelRegistry,
		OnResolved: func(entry *claims.GraphEntryPoint) {
			if entry == nil {
				return
			}
			if !acknowledgeLifecycleReceipt(cfg, role, entry) {
				return
			}
			expectedValidationToolsScheduled := dispatchExpectedValidationTools(cfg, entry)
			if deliverExpectedPeerResultToContinuation(cfg, entry) {
				return
			}
			if expectedValidationToolsScheduled {
				return
			}
			if shouldSuppressForwardedPromptEntry(role, entry) {
				claimID := ""
				if entry.Node.Claim != nil {
					claimID = entry.Node.Claim.ID
				}
				slog.Info("claims_intake_suppressed_forwarded_prompt_entry",
					"agent_id", cfg.AgentID,
					"session_id", cfg.SessionID,
					"role", role,
					"claim_id", claimID,
				)
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
					runCtx, reg := claimIntakeContext(ctx, cfg, entryClaimID(entry))
					defer reg.Done()
					return cfg.ProcessEntry(stampClaimsIntakeContext(runCtx, cfg, entry), entry)
				}); err != nil {
					slog.Error("claims_intake_dispatch_failed",
						"agent_id", cfg.AgentID,
						"error", err.Error(),
					)
				}
				return
			}
			if cfg.ProcessEntry != nil {
				runCtx, reg := claimIntakeContext(context.Background(), cfg, entryClaimID(entry))
				defer reg.Done()
				ctx := stampClaimsIntakeContext(runCtx, cfg, entry)
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

func claimIntakeContext(ctx context.Context, cfg ClaimsIntakeConfig, claimID string) (context.Context, claims.ClaimCancelRegistration) {
	if cfg.CancelRegistry == nil || strings.TrimSpace(claimID) == "" {
		if ctx == nil {
			ctx = context.Background()
		}
		return ctx, claims.ClaimCancelRegistration{}
	}
	return cfg.CancelRegistry.Context(ctx, claimID)
}

func entryClaimID(entry *claims.GraphEntryPoint) string {
	if entry == nil {
		return ""
	}
	if entry.Node.Claim != nil && strings.TrimSpace(entry.Node.Claim.ID) != "" {
		return strings.TrimSpace(entry.Node.Claim.ID)
	}
	if entry.Expectation != nil && strings.TrimSpace(entry.Expectation.ClaimID) != "" {
		return strings.TrimSpace(entry.Expectation.ClaimID)
	}
	switch delta := entry.Delta.(type) {
	case claims.InboxDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.InboxDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.TestamentDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.TestamentDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.ValidationDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.ValidationDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.ClaimStatusDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.ClaimStatusDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.ClaimContextDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.ClaimContextDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.TestamentContextDelta:
		return strings.TrimSpace(delta.ClaimID)
	case *claims.TestamentContextDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID)
		}
	case claims.CanonicalDelta:
		return strings.TrimSpace(delta.ClaimID())
	case *claims.CanonicalDelta:
		if delta != nil {
			return strings.TrimSpace(delta.ClaimID())
		}
	}
	return ""
}

func acknowledgeLifecycleReceipt(cfg ClaimsIntakeConfig, role claims.ClaimsRole, entry *claims.GraphEntryPoint) bool {
	if cfg.Board == nil || entry == nil || entry.Delta == nil || role.Has(claims.RoleObserver) {
		return true
	}
	delta, ok := canonicalDeltaFromEntry(entry)
	if !ok {
		return true
	}
	switch delta.Action {
	case claims.DeltaActionClaimPosted:
		return acknowledgeClaimPosted(cfg, delta)
	case claims.DeltaActionTestamentPosted:
		return acknowledgeTestamentPosted(cfg, delta)
	default:
		return true
	}
}

func canonicalDeltaFromEntry(entry *claims.GraphEntryPoint) (claims.CanonicalDelta, bool) {
	switch delta := entry.Delta.(type) {
	case claims.CanonicalDelta:
		return delta, true
	case *claims.CanonicalDelta:
		if delta != nil {
			return *delta, true
		}
	}
	return claims.CanonicalDelta{}, false
}

func acknowledgeClaimPosted(cfg ClaimsIntakeConfig, delta claims.CanonicalDelta) bool {
	claimID := strings.TrimSpace(delta.ClaimID())
	if claimID == "" {
		return true
	}
	if err := cfg.Board.AcknowledgeClaimReceipt(context.Background(), claimID, cfg.AgentID); err != nil {
		_ = cfg.Board.RecordClaimReceiptFailure(context.Background(), claimID, cfg.AgentID, claims.LifecycleFailureOptions{
			Reason:       err.Error(),
			ArtifactKind: claims.ArtifactKindErrorDiagnostic,
		})
		slog.Error("claims_intake_claim_receipt_failed",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
		return false
	}
	return true
}

func acknowledgeTestamentPosted(cfg ClaimsIntakeConfig, delta claims.CanonicalDelta) bool {
	testamentID := strings.TrimSpace(delta.TestamentID())
	if testamentID == "" {
		return true
	}
	if err := cfg.Board.AcknowledgeTestamentReceipt(context.Background(), testamentID, cfg.AgentID); err != nil {
		claimID := strings.TrimSpace(delta.ClaimID())
		if claimID != "" {
			_ = cfg.Board.RecordClaimTestamentAcknowledgementFailure(context.Background(), claimID, cfg.AgentID, claims.LifecycleFailureOptions{
				Reason:       err.Error(),
				ArtifactKind: claims.ArtifactKindErrorDiagnostic,
			})
		}
		slog.Error("claims_intake_testament_receipt_failed",
			"agent_id", cfg.AgentID,
			"session_id", cfg.SessionID,
			"testament_id", testamentID,
			"error", err.Error(),
		)
		return false
	}
	return true
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
		if len(v.ExpectedToolCalls) > 0 {
			b.WriteString("  Expected validation tools:\n")
			for _, call := range v.ExpectedToolCalls {
				b.WriteString("  - `" + strings.TrimSpace(call.Tool) + "`")
				if call.Required {
					b.WriteString(" required")
				}
				if purpose := strings.TrimSpace(call.Purpose); purpose != "" {
					b.WriteString(" - " + purpose)
				}
				if len(call.Arguments) > 0 {
					if raw, err := json.Marshal(call.Arguments); err == nil {
						b.WriteString(" args=" + truncatePromptString(string(raw), 240))
					}
				}
				b.WriteString("\n")
			}
		}
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
