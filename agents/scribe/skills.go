package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

const scribeStoreArchivalistSkillName = "store_archivalist"

type scribeToolBundle struct {
	registry *skills.Registry
	loader   *skills.Loader
	runtime  *toolruntime.Runtime
}

type scribeRunState struct {
	feed       shared.ScribeFeed
	tracker    *shared.MemoryForestTracker
	stored     bool
	commentary string
}

func (s *Scribe) newToolBundle(feed shared.ScribeFeed) (*scribeToolBundle, *scribeRunState, error) {
	registry := skills.NewRegistry()
	runState := &scribeRunState{
		feed:    feed,
		tracker: shared.NewMemoryForestTracker(),
	}
	if err := registry.Register(s.storeArchivalistSkill(runState)); err != nil {
		return nil, nil, fmt.Errorf("register scribe store skill: %w", err)
	}
	if err := shared.RegisterMemoryForestSkills(registry, s.AgentType(), s.config.Forest, runState.tracker); err != nil {
		return nil, nil, fmt.Errorf("register scribe forest skills: %w", err)
	}
	if err := shared.AttachForestOutcomeRecorder(
		registry,
		scribeStoreArchivalistSkillName,
		runState.tracker,
		s.config.Forest,
		s.AgentID,
		s.AgentType(),
		nil,
		shared.OutcomeOnSuccess("scribe stored commentary for future replay and handoff"),
	); err != nil {
		return nil, nil, fmt.Errorf("attach scribe forest outcome recorder: %w", err)
	}
	for _, name := range shared.AppendMemoryForestMutatingSkillNames(nil) {
		registry.Unload(name)
	}

	// Claims skills — the scribe is a full claims participant. It can
	// query board state for context-aware narration, submit testaments
	// about narration work, and post actions about precedent patterns.
	boardProvider := func() *claims.ClaimsBoard {
		return claims.DefaultSessionBoardRegistry().Lookup(s.sessionID)
	}
	inboxProvider := func() *claims.ClaimsInbox { return s.claimsInbox }
	for _, skill := range []*skills.Skill{
		claims.QueryClaimsBoardSkill(boardProvider),
		claims.PostActionSkill(boardProvider, inboxProvider),
		claims.SubmitTestamentsSkill(boardProvider),
		claims.EvaluateValidationSkill(boardProvider),
		claims.UpdateClaimProgressSkill(boardProvider),
		claims.InspectClaimConflictsSkill(boardProvider),
		claims.TraverseSkill(boardProvider),
	} {
		if err := registry.Register(skill); err != nil {
			return nil, nil, fmt.Errorf("register scribe claims skill %q: %w", skill.Name, err)
		}
	}

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = scribeVisibleSkillNamesForRegistry(registry, s.AgentType())
	loaderCfg.AutoLoadDomains = nil
	loader := skills.NewLoader(registry, loaderCfg)

	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Manifest: scribeToolManifest(registry, s.AgentType()),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize scribe tool runtime: %w", err)
	}
	runtime.SyncActiveFromLoaded()
	return &scribeToolBundle{
		registry: registry,
		loader:   loader,
		runtime:  runtime,
	}, runState, nil
}

func (b *scribeToolBundle) Close() {
	if b == nil || b.runtime == nil {
		return
	}
	b.runtime.Close()
}

func (b *scribeToolBundle) buildToolDefinitions() []providersTool {
	if b == nil || b.runtime == nil {
		return nil
	}
	b.runtime.SyncActiveFromLoaded()
	return b.runtime.BuildToolDefinitions()
}

func (b *scribeToolBundle) prepareSkillsForInput(input string) {
	if b == nil || b.loader == nil {
		return
	}
	b.loader.LoadForInput(input)
	b.loader.OptimizeForBudget()
}

func (b *scribeToolBundle) executeToolCall(ctx context.Context, agentID string, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	if b == nil || b.runtime == nil {
		return toolruntime.ExecutionResult{}, fmt.Errorf("scribe tool runtime is not configured")
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = strings.TrimSpace(agentID) + "-local"
	}
	return b.runtime.Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        call.ID,
			Name:      name,
			Arguments: raw,
		},
		AgentID:         b.runtime.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: b.runtime.CapabilityScope(),
	})
}

func (b *scribeToolBundle) toolInvocations(ctx context.Context, agentID string, calls []providers.ToolCall) []toolruntime.Invocation {
	if b == nil || b.runtime == nil || len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = strings.TrimSpace(agentID) + "-local"
	}
	scope := b.runtime.CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall: providers.ToolCall{
				ID:        call.ID,
				Name:      strings.TrimSpace(call.Name),
				Arguments: strings.TrimSpace(call.Arguments),
			},
			AgentID:         b.runtime.AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

type providersTool = providers.Tool

// storeArchivalistSkill persists the final structured commentary for the
// current observed turn. It is the required terminal action for a Scribe turn.
func (s *Scribe) storeArchivalistSkill(runState *scribeRunState) *skills.Skill {
	return skills.NewSkill(scribeStoreArchivalistSkillName).
		Description("Persist the final structured commentary for this observed turn into the Archivalist. This is the terminal step for a Scribe turn.").
		Domain("memory").
		Keywords("store", "archivalist", "scribe", "summary", "handoff", "commentary").
		Priority(100).
		Usage("Call exactly once after any needed forest lookups. Do not return free text instead of calling this tool.").
		BestPractice("Keep the commentary compact, factual, and archival. Preserve only the details that will matter in later retrieval, replay, or handoff.").
		ObjectParam("commentary", "Structured commentary object for the current observed turn", map[string]*skills.Property{
			"summary":          {Type: "string", Description: "REQUIRED. One-sentence factual description of what happened this turn. Must be non-empty."},
			"progress":         {Type: "string", Description: "Overall task progress after this turn"},
			"decisions":        {Type: "string", Description: "Key decisions or tradeoffs made this turn"},
			"state":            {Type: "string", Description: "Current working state after this turn"},
			"risk":             {Type: "string", Description: "Risks, regressions, or unresolved concerns"},
			"handoff_context":  {Type: "string", Description: "Compact context worth preserving for future handoff or replay"},
			"details":          {Type: "object", Description: "Optional role-specific details that carry high downstream value"},
			"precedent_worthy": {Type: "boolean", Description: "Optional: true when this batch's pattern is precedent-quality and should be harvested by Memory Forest. Reserve for genuinely high-signal patterns (successful cross-pipeline reconciliations, unusual failures with clean recoveries, etc.)."},
			"precedent_why":    {Type: "string", Description: "Optional: short prose explaining why this pattern is precedent-worthy. Required when precedent_worthy=true."},
		}, true).
		RequireNestedFields("commentary", "summary").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			commentary, err := parseScribeCommentary(input)
			if err != nil {
				return nil, err
			}
			if runState == nil {
				return nil, fmt.Errorf("scribe run state is not configured")
			}
			if runState.stored {
				return nil, fmt.Errorf("scribe commentary already stored for this turn")
			}
			summary := strings.TrimSpace(scribeStringFromAny(commentary["summary"]))
			if summary == "" {
				return nil, fmt.Errorf("commentary.summary is required")
			}
			payload, err := json.Marshal(commentary)
			if err != nil {
				return nil, fmt.Errorf("marshal commentary: %w", err)
			}
			if err := s.storeCommentaryInArchivalist(ctx, string(payload), runState.feed); err != nil {
				return nil, err
			}
			runState.stored = true
			runState.commentary = string(payload)

			// Submit narration as testament to the session claims board.
			// The archivalist publish (above) is long-term storage. The
			// testament puts the narration on the session board so other
			// agents can see it, the inspector can verify narration
			// accuracy, and cross-replica queries can reference it.
			archivalEntryID := fmt.Sprintf(
				"scribe_%s_rep%d_%d",
				s.parentAgentType, s.replicaGeneration, time.Now().UnixNano(),
			)
			s.submitNarrationTestament(ctx, summary, string(payload), archivalEntryID, commentary, runState.feed)

			// Phase 7 — precedent emission. Fabric activity emitted
			// via the board amplifier when the precedent validation is
			// recorded on the narration testament (above). The legacy
			// direct emission is kept as a fallback for boards without
			// amplifiers (test fixtures).
			if precedentWorthyFromCommentary(commentary) {
				s.emitPrecedentActivity(ctx, commentary, runState.feed)
			}

			return map[string]any{
				"stored":      true,
				"summary":     summary,
				"stored_at":   time.Now().UTC().Format(time.RFC3339),
				"target":      "archivalist",
				"correlation": strings.TrimSpace(runState.feed.ParentCorrelationID),
			}, nil
		}).
		Build()
}

// precedentWorthyFromCommentary inspects the LLM's structured
// commentary and reports whether it flagged the batch as precedent-
// worthy. Tolerant of multiple JSON-shape conventions: bool true,
// string "true", string "yes". A non-empty precedent_why is also
// treated as a positive signal even when the bool is missing or
// false (the LLM articulated a reason — that's good enough).
func precedentWorthyFromCommentary(commentary map[string]any) bool {
	if commentary == nil {
		return false
	}
	switch v := commentary["precedent_worthy"].(type) {
	case bool:
		if v {
			return true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1":
			return true
		}
	}
	if why := strings.TrimSpace(scribeStringFromAny(commentary["precedent_why"])); why != "" {
		return true
	}
	return false
}

func parseScribeCommentary(input json.RawMessage) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if nested, ok := payload["commentary"].(map[string]any); ok {
		return nested, nil
	}
	return payload, nil
}

func (s *Scribe) storeCommentaryInArchivalist(ctx context.Context, commentary string, feed shared.ScribeFeed) error {
	if s.bus == nil {
		return fmt.Errorf("scribe bus is not configured")
	}
	input, err := guide.ArchivalistStoreRouteInput(commentary)
	if err != nil {
		return fmt.Errorf("encode archivalist store route: %w", err)
	}

	parentCorrelationID := strings.TrimSpace(feed.ParentCorrelationID)
	branchCtx := ctx
	if parentCorrelationID != "" {
		branchCtx = shared.WithStreamContext(branchCtx, parentCorrelationID, s.parentAgentType)
	}
	branchCtx, branch := shared.BeginArchivalistStoreBranch(branchCtx, "stored scribe commentary", map[string]any{
		"parent_agent": s.parentAgentType,
	})

	// Deterministic archivalist entry ID per SCRIBE_FABRIC.md §9.4
	// Option B. Lets us reference the entry from the fabric activity
	// without a synchronous round-trip to the archivalist.
	archivalEntryID := fmt.Sprintf(
		"scribe_%s_rep%d_%d",
		s.parentAgentType, s.replicaGeneration, time.Now().UnixNano(),
	)

	metadata := branch.ApplyMetadata(branchCtx, map[string]any{
		"source_type":        "scribe",
		"parent_agent":       s.parentAgentType,
		"scribe_id":          s.id,
		"replica_generation": s.replicaGeneration,
		"session_id":         s.sessionID,
		"entry_id":           archivalEntryID,
	})

	req := &guide.RouteRequest{
		CorrelationID:       "scribe_" + uuid.NewString(),
		ParentCorrelationID: parentCorrelationID,
		SourceAgentID:       s.id,
		Input:               input,
		FireAndForget:       true,
		Timestamp:           time.Now(),
		Metadata:            metadata,
	}
	msg := guide.NewRequestMessage(
		fmt.Sprintf("scribe_%s_msg_%s", s.parentAgentType, uuid.New().String()[:8]),
		req,
	)
	msg.Metadata = metadata
	if err := s.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return fmt.Errorf("publish archivalist store request: %w", err)
	}
	branch.Complete(branchCtx, "stored scribe commentary", "", nil)

	// Activity Fabric narration projection (SCRIBE_FABRIC.md Phase 3).
	// Best-effort — emission failures must not fail the archivalist
	// publish, which already succeeded above.
	s.emitNarrationActivity(ctx, commentary, archivalEntryID, parentCorrelationID)
	return nil
}

func scribeVisibleSkillNames(agentType string) []string {
	return shared.AppendMemoryForestVisibleSkillNames([]string{
		scribeStoreArchivalistSkillName,
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"inspect_claim_conflicts",
		"traverse",
		// Claims skills: mutating operations are also visible for the scribe.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	}, agentType)
}

// submitNarrationTestament posts the narration to the session claims board
// as a testament with the commentary as an artifact. If precedent detection
// triggered, the precedent flag is a validation on the testament.
//
// Best-effort: board unavailability does not fail the narration. The
// archivalist publish already succeeded — the testament is supplementary
// visibility for the session board.
func (s *Scribe) submitNarrationTestament(
	ctx context.Context,
	summary, payload, archivalEntryID string,
	commentary map[string]any,
	feed shared.ScribeFeed,
) {
	board := claims.DefaultSessionBoardRegistry().Lookup(s.sessionID)
	if board == nil {
		return
	}

	testament := claims.Testament{
		AgentID:    "scribe-" + s.parentAgentType,
		SessionID:  s.sessionID,
		Summary:    summary,
		Confidence: "committed",
		Artifacts: []*claims.Artifact{
			{
				AgentID:   "scribe-" + s.parentAgentType,
				SessionID: s.sessionID,
				Kind:      "narration",
				Reference: payload,
			},
			{
				AgentID:   "scribe-" + s.parentAgentType,
				SessionID: s.sessionID,
				Kind:      "archivalist_receipt",
				Reference: archivalEntryID,
			},
		},
		Relations: []claims.Relation{
			{Related: "scribe-" + s.parentAgentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: s.parentAgentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
	}

	// Precedent detection as validation on the testament.
	if precedentWorthyFromCommentary(commentary) {
		why := strings.TrimSpace(scribeStringFromAny(commentary["precedent_why"]))
		if why == "" {
			why = "LLM flagged as precedent-worthy"
		}
		testament.Artifacts = append(testament.Artifacts, &claims.Artifact{
			AgentID:   "scribe-" + s.parentAgentType,
			SessionID: s.sessionID,
			Kind:      "precedent",
			Reference: why,
		})
	}

	action := claims.Action{
		AgentID: "scribe-" + s.parentAgentType,
		Type:    claims.ActionTypeTestament,
	}

	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		board.RecordNotificationError("scribe narration testament: " + err.Error())
	}
}

func scribeVisibleSkillNamesForRegistry(registry *skills.Registry, agentType string) []string {
	return shared.FilterRegisteredSkillNames(registry, scribeVisibleSkillNames(agentType))
}

func scribeMutatingSkillNames(agentType string) []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		scribeStoreArchivalistSkillName,
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	})
}

func scribeToolManifest(registry *skills.Registry, agentType string) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          strings.TrimSpace(agentType),
		CapabilityScope:  strings.TrimSpace(agentType) + ".default",
		Registry:         registry,
		VisibleByDefault: scribeVisibleSkillNamesForRegistry(registry, agentType),
		Mutating:         shared.FilterRegisteredSkillNames(registry, scribeMutatingSkillNames(agentType)),
	})
}

func scribeStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}
