package activity

// ActionKind is the typed taxonomy of actions the fabric records. Each
// kind has a fixed Resolution (see ResolutionFor); chokepoints set the
// kind based on what they instrument, and per-system amplifiers set the
// kind based on what semantic work the sovereign store performed.
//
// New ActionKinds are added when new semantics need to be representable.
// Adding an infrastructural kind requires also wiring its Resolution in
// ResolutionFor and its chokepoint in core/activity/span.go's caller.
type ActionKind string

const (
	// ─── Infrastructural kinds (Tier 1.5 chokepoints) ───────────────

	// ActionStoreWriteCommitted is emitted by database.RunInWriteTx
	// once for every successfully-committed transaction across every
	// sovereign store. The payload carries store name, table set, and
	// row counts; the Subject's TargetArtifact identifies the affected
	// store.
	ActionStoreWriteCommitted ActionKind = "store_write_committed"

	// ActionStoreWriteFailed is emitted when a transaction failed
	// (after retry exhaustion). Captures the failure path so error
	// analysis is queryable.
	ActionStoreWriteFailed ActionKind = "store_write_failed"

	// ActionBusMessageEmitted is emitted by the channel bus's Publish
	// path for every cross-agent message.
	ActionBusMessageEmitted ActionKind = "bus_message_emitted"

	// ActionBusMessageDelivered is emitted when a subscribed handler
	// completes processing of a bus message. Pairs with
	// ActionBusMessageEmitted via Resolves.
	ActionBusMessageDelivered ActionKind = "bus_message_delivered"

	// ActionLLMRequestEmitted is emitted when an LLM provider call
	// starts. Carries model, prompt size, tools available, retry
	// attempt number.
	ActionLLMRequestEmitted ActionKind = "llm_request_emitted"

	// ActionLLMResponseCompleted is emitted when an LLM provider call
	// completes (success, error, or final retry exhaustion). Carries
	// token usage, finish reason, retry count, model swap if any.
	ActionLLMResponseCompleted ActionKind = "llm_response_completed"

	// ActionLLMChunkReceived is emitted per streaming chunk. Highest-
	// volume infrastructural kind; Atomic resolution.
	ActionLLMChunkReceived ActionKind = "llm_chunk_received"

	// ActionFileRead is emitted by versioning.FileAccess.ReadFile.
	// Atomic resolution.
	ActionFileRead ActionKind = "file_read"

	// ActionFileWritten is emitted by versioning.FileAccess.WriteFile
	// or EditFile. Fine resolution.
	ActionFileWritten ActionKind = "file_written"

	// ActionFileDeleted is emitted by versioning.FileAccess.DeleteFile.
	// Fine resolution.
	ActionFileDeleted ActionKind = "file_deleted"

	// ActionCommandExecuted is emitted by purevfs.ExecutionBroker.Run
	// for every command execution under strict-disk. Carries argv,
	// exit code, duration, scope.
	ActionCommandExecuted ActionKind = "command_executed"

	// ActionCommandApproved is emitted by commandapproval.Authorize
	// when a command is approved.
	ActionCommandApproved ActionKind = "command_approved"

	// ActionCommandDenied is emitted by commandapproval.Authorize when
	// a command is denied. Carries rationale + scope; peers see this
	// proactively in ambient context.
	ActionCommandDenied ActionKind = "command_denied"

	// ActionToolCallStarted is emitted by tool_publish.go when a tool
	// invocation starts.
	ActionToolCallStarted ActionKind = "tool_call_started"

	// ActionToolCallCompleted is emitted when a tool invocation
	// completes (success or error). Pairs with ActionToolCallStarted
	// via Resolves.
	ActionToolCallCompleted ActionKind = "tool_call_completed"

	// ActionAgentPromoted is emitted by activation tier transitions
	// when an agent moves up a tier (Cold→Cool→Warm→Hot).
	ActionAgentPromoted ActionKind = "agent_promoted"

	// ActionAgentDemoted is emitted when an agent moves down a tier.
	ActionAgentDemoted ActionKind = "agent_demoted"

	// ActionGoroutineStarted is emitted by tracked-goroutine helpers
	// for every long-lived goroutine spawn.
	ActionGoroutineStarted ActionKind = "goroutine_started"

	// ActionGoroutineStopped is emitted when a tracked goroutine
	// completes. Pairs via Resolves.
	ActionGoroutineStopped ActionKind = "goroutine_stopped"

	// ActionCacheHit is emitted by cache wrappers on a hit. Atomic
	// resolution.
	ActionCacheHit ActionKind = "cache_hit"

	// ActionCacheMiss is emitted by cache wrappers on a miss. Atomic
	// resolution (the resulting fetch typically emits Fine).
	ActionCacheMiss ActionKind = "cache_miss"

	// ActionErrorEmitted is emitted by error-wrapping helpers.
	ActionErrorEmitted ActionKind = "error_emitted"

	// ─── Semantic kinds (Tier 4 amplifiers) ────────────────────────

	// ActionDecisionDeclared is emitted when the Decision Manifest
	// records a typed decision (Hint/Tentative/Committed).
	ActionDecisionDeclared ActionKind = "decision_declared"

	// ActionDecisionPromoted is emitted when a decision is promoted
	// (Tentative→Committed→Consensus). Resolves the prior declaration
	// activity.
	ActionDecisionPromoted ActionKind = "decision_promoted"

	// ActionDecisionSuperseded is emitted when a decision is replaced
	// (e.g., scope-split narrows a broad decision into two narrower
	// ones).
	ActionDecisionSuperseded ActionKind = "decision_superseded"

	// ActionClaimAcquired is emitted by the coordination service when
	// a scope claim is acquired or renewed.
	ActionClaimAcquired ActionKind = "claim_acquired"

	// ActionClaimReleased is emitted when a claim is released or
	// expires.
	ActionClaimReleased ActionKind = "claim_released"

	// ActionArtifactPublished is emitted when the coordination service
	// records a published artifact.
	ActionArtifactPublished ActionKind = "artifact_published"

	// ActionReviewRequested is emitted when an artifact review is
	// requested.
	ActionReviewRequested ActionKind = "review_requested"

	// ActionReviewCompleted is emitted when a review completes.
	// Resolves the matching ActionReviewRequested.
	ActionReviewCompleted ActionKind = "review_completed"

	// ActionValidationStarted is emitted when a validation epoch
	// begins.
	ActionValidationStarted ActionKind = "validation_started"

	// ActionValidationAccepted is emitted when validation accepts the
	// pipeline's work. Resolves the matching ActionValidationStarted.
	ActionValidationAccepted ActionKind = "validation_accepted"

	// ActionValidationRejected is emitted when validation rejects.
	// Resolves the matching ActionValidationStarted.
	ActionValidationRejected ActionKind = "validation_rejected"

	// ActionHoldAcquired is emitted when an execution hold is taken.
	ActionHoldAcquired ActionKind = "hold_acquired"

	// ActionHoldReleased is emitted when an execution hold is
	// released.
	ActionHoldReleased ActionKind = "hold_released"

	// ActionRemediationOpened is emitted when a remediation case is
	// opened.
	ActionRemediationOpened ActionKind = "remediation_opened"

	// ActionRemediationResolved is emitted when a remediation case is
	// resolved. Resolves the matching ActionRemediationOpened.
	ActionRemediationResolved ActionKind = "remediation_resolved"

	// ActionPlanProposed is emitted by the architect for each plan
	// proposal step.
	ActionPlanProposed ActionKind = "plan_proposed"

	// ActionPlanRatified is emitted on plan acceptance. Carries the
	// high-level decisions inside the plan as Charter entries.
	ActionPlanRatified ActionKind = "plan_ratified"

	// ActionCharterRatified is emitted by plan acceptance for each
	// Charter-level decision the plan implies. Pipeline agents see
	// these in ambient context with elevated weight.
	ActionCharterRatified ActionKind = "charter_ratified"

	// ActionDAGIngested is emitted by the orchestrator on plan
	// ingestion.
	ActionDAGIngested ActionKind = "dag_ingested"

	// ActionLayerDispatched is emitted on each DAG layer dispatch.
	ActionLayerDispatched ActionKind = "layer_dispatched"

	// ActionDAGAccepted is emitted on DAG completion.
	ActionDAGAccepted ActionKind = "dag_accepted"

	// ActionRouteClassified is emitted by the guide for each request
	// classification.
	ActionRouteClassified ActionKind = "route_classified"

	// ActionSteeringEmitted is emitted by the guide for each steering
	// action.
	ActionSteeringEmitted ActionKind = "steering_emitted"

	// ─── Cross-pipeline collaboration kinds (Tier 6) ───────────────

	// ActionChallengeEmitted is emitted by an agent issuing a
	// challenge against a peer activity. State=in_flight until
	// resolved.
	ActionChallengeEmitted ActionKind = "challenge_emitted"

	// ActionChallengeResponse is emitted when the challenged agent
	// responds (defend / yield / scope-split / escalate). Resolves
	// the matching ActionChallengeEmitted.
	ActionChallengeResponse ActionKind = "challenge_response"

	// ActionChallengeUnresolved is emitted when a challenge passes
	// its deadline without response.
	ActionChallengeUnresolved ActionKind = "challenge_unresolved"

	// ActionConsultEmitted is emitted by an agent consulting a peer
	// (knowledge agent or another pipeline's specialist).
	ActionConsultEmitted ActionKind = "consult_emitted"

	// ActionConsultResponse is emitted when the consulted agent
	// responds. Resolves the matching ActionConsultEmitted.
	ActionConsultResponse ActionKind = "consult_response"

	// ActionConsultUnanswered is emitted when a consult passes its
	// deadline without response.
	ActionConsultUnanswered ActionKind = "consult_unanswered"

	// ActionScopePartitioned is emitted on a scope-split resolution
	// of a cross-pipeline challenge.
	ActionScopePartitioned ActionKind = "scope_partitioned"

	// ActionEscalationRequested is emitted on an escalate resolution
	// of a cross-pipeline challenge.
	ActionEscalationRequested ActionKind = "escalation_requested"

	// ─── Knowledge agent kinds (Tier 8) ────────────────────────────

	// ActionAdvisoryEmitted is emitted by knowledge agents on every
	// consult response — peer pipelines tackling adjacent work see
	// the advisory in ambient context.
	ActionAdvisoryEmitted ActionKind = "advisory_emitted"

	// ActionProactiveAdvisory is emitted by knowledge agents tailing
	// the fabric when they observe a pipeline operating in a scope
	// that matches a known precedent or anti-pattern.
	ActionProactiveAdvisory ActionKind = "proactive_advisory"

	// ActionNotificationEmitted is emitted by inspector or knowledge
	// agents to push a notification to a specific peer + scope.
	ActionNotificationEmitted ActionKind = "notification_emitted"

	// ActionPrecedentEmitted is emitted by Memory Forest harvest
	// candidates — typically promoted from Consensus + accepted
	// artifacts with a complete causal chain.
	ActionPrecedentEmitted ActionKind = "precedent_emitted"

	// ActionConsulted is emitted on every awareness-skill query so
	// causal traceability captures who-asked-what.
	ActionConsulted ActionKind = "consulted"
)

// ResolutionFor returns the canonical Resolution for the given
// ActionKind. The mapping is small and fixed; per-callsite override is
// not supported by design.
func ResolutionFor(kind ActionKind) Resolution {
	switch kind {
	case ActionLLMChunkReceived,
		ActionFileRead,
		ActionCacheHit,
		ActionCacheMiss:
		return ResolutionAtomic

	case ActionStoreWriteCommitted,
		ActionStoreWriteFailed,
		ActionBusMessageDelivered,
		ActionFileWritten,
		ActionFileDeleted,
		ActionCommandExecuted,
		ActionCommandApproved,
		ActionCommandDenied,
		ActionToolCallStarted,
		ActionToolCallCompleted,
		ActionGoroutineStarted,
		ActionGoroutineStopped,
		ActionAgentPromoted,
		ActionAgentDemoted,
		ActionErrorEmitted,
		ActionConsulted,
		ActionRouteClassified:
		return ResolutionFine

	case ActionBusMessageEmitted,
		ActionLLMRequestEmitted,
		ActionLLMResponseCompleted,
		ActionClaimAcquired,
		ActionClaimReleased,
		ActionArtifactPublished,
		ActionReviewRequested,
		ActionReviewCompleted,
		ActionValidationStarted,
		ActionValidationAccepted,
		ActionValidationRejected,
		ActionHoldAcquired,
		ActionHoldReleased,
		ActionRemediationOpened,
		ActionRemediationResolved,
		ActionDAGIngested,
		ActionLayerDispatched,
		ActionDAGAccepted,
		ActionSteeringEmitted,
		ActionConsultEmitted,
		ActionConsultResponse,
		ActionConsultUnanswered,
		ActionAdvisoryEmitted,
		ActionNotificationEmitted:
		return ResolutionMedium

	case ActionDecisionDeclared,
		ActionDecisionPromoted,
		ActionDecisionSuperseded,
		ActionPlanProposed,
		ActionPlanRatified,
		ActionCharterRatified,
		ActionChallengeEmitted,
		ActionChallengeResponse,
		ActionChallengeUnresolved,
		ActionScopePartitioned,
		ActionEscalationRequested,
		ActionProactiveAdvisory,
		ActionPrecedentEmitted:
		return ResolutionCoarse
	}

	// Unknown kind — default to Medium so it lands in durable storage
	// and is visible during diagnosis.
	return ResolutionMedium
}

// IsTerminalState reports whether the action kind represents a
// resolution of a previously in-flight activity.
func (k ActionKind) IsTerminal() bool {
	switch k {
	case ActionBusMessageDelivered,
		ActionToolCallCompleted,
		ActionLLMResponseCompleted,
		ActionGoroutineStopped,
		ActionClaimReleased,
		ActionReviewCompleted,
		ActionValidationAccepted,
		ActionValidationRejected,
		ActionHoldReleased,
		ActionRemediationResolved,
		ActionDAGAccepted,
		ActionChallengeResponse,
		ActionChallengeUnresolved,
		ActionConsultResponse,
		ActionConsultUnanswered,
		ActionScopePartitioned,
		ActionEscalationRequested,
		ActionStoreWriteFailed:
		return true
	}
	return false
}
