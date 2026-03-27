package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

func getBriefingToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolGetBriefing).
		Description("Get a handoff briefing for continuing work.").
		Domain("memory").
		Keywords("briefing", "handoff", "resume", "context", "state").
		Priority(100).
		EnumParam("tier", "Briefing detail level", []string{"micro", "standard", "full"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params GetBriefingInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.getBriefingToolData(params.Tier)
		}).
		Build()
}

func queryPatternsToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolQueryPatterns).
		Description("Query coding patterns by category.").
		Domain("memory").
		Keywords("pattern", "convention", "style", "recall", "history").
		Priority(90).
		StringParam("category", "Hierarchical category (for example, auth.errors)", false).
		ArrayParam("scope", "Optional path scopes to filter patterns", "string", false).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params QueryPatternsInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.queryPatternsToolData(ctx, params), nil
		}).
		Build()
}

func queryFailuresToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolQueryFailures).
		Description("Search failures and their resolutions.").
		Domain("memory").
		Keywords("failure", "error", "incident", "resolution", "learned").
		Priority(90).
		StringParam("error_type", "Optional error-type or keyword filter", false).
		StringParam("file_pattern", "Optional file path filter", false).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params QueryFailuresInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.queryFailuresToolData(ctx, params), nil
		}).
		Build()
}

func queryContextToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolQueryContext).
		Description("Free-form RAG query for historical context.").
		Domain("memory").
		Keywords("query", "context", "history", "recall", "rag", "search").
		Priority(95).
		StringParam("query", "Natural-language query", true).
		EnumParam("scope", "Search scope", []string{"session", "global", "all"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params QueryContextInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.queryContextToolData(ctx, params)
		}).
		Build()
}

func queryFileStateToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolQueryFileState).
		Description("Get file state across the current handoff context.").
		Domain("memory").
		Keywords("file", "state", "history", "modified", "path").
		Priority(85).
		StringParam("path", "File path or glob pattern", true).
		BoolParam("include_history", "Include recorded file history when available", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params QueryFileStateInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.queryFileStateToolData(params), nil
		}).
		Build()
}

func recordPatternToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolRecordPattern).
		Description("Record a new coding pattern.").
		Domain("chronicle").
		Keywords("record", "pattern", "convention", "standard", "remember").
		Priority(85).
		StringParam("pattern", "Pattern description", true).
		StringParam("category", "Pattern category", true).
		ArrayParam("scope", "Optional path scopes for the pattern", "string", false).
		ArrayParam("supersedes", "Pattern IDs superseded by this one", "string", false).
		StringParam("reason", "Reason for superseding or adopting the pattern", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params RecordPatternInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.recordPatternToolData(params), nil
		}).
		Build()
}

func recordFailureToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolRecordFailure).
		Description("Record a failure and its resolution.").
		Domain("chronicle").
		Keywords("record", "failure", "error", "incident", "resolution").
		Priority(85).
		StringParam("error", "Observed error or failure reason", true).
		StringParam("context", "Task context", true).
		StringParam("approach", "Failed approach", true).
		StringParam("resolution", "Working resolution", false).
		EnumParam("outcome", "Outcome quality", []string{"success", "partial", "failed"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params RecordFailureInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.recordFailureToolData(params), nil
		}).
		Build()
}

func updateFileStateToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolUpdateFileState).
		Description("Update file state after reading or modifying a file.").
		Domain("chronicle").
		Keywords("file", "state", "read", "modified", "created", "deleted").
		Priority(85).
		StringParam("path", "File path", true).
		EnumParam("action", "File action", []string{"read", "modified", "created", "deleted"}, true).
		StringParam("summary", "What changed or what the file does", false).
		StringParam("lines_changed", "Optional changed-line span", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params UpdateFileStateInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.updateFileStateToolData(params), nil
		}).
		Build()
}

func declareIntentToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolDeclareIntent).
		Description("Declare intent for cross-cutting work.").
		Domain("control").
		Keywords("declare", "intent", "coordination", "cross-cutting", "conflict").
		Priority(85).
		StringParam("type", "Intent type", true).
		StringParam("description", "Intent description", true).
		ArrayParam("affected_paths", "Affected paths", "string", true).
		ArrayParam("affected_apis", "Affected APIs", "string", false).
		EnumParam("priority", "Priority", []string{"low", "medium", "high", "critical"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params DeclareIntentInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.declareWorkIntent(params, a.id), nil
		}).
		Build()
}

func completeIntentToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolCompleteIntent).
		Description("Mark a declared intent as completed.").
		Domain("control").
		Keywords("complete", "intent", "done", "finished", "resolved").
		Priority(82).
		StringParam("intent_id", "Intent identifier", true).
		BoolParam("success", "Whether the intent completed successfully", true).
		ArrayParam("files_changed", "Files changed while completing the intent", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params CompleteIntentInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.completeWorkIntent(params), nil
		}).
		Build()
}

func getConflictsToolSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill(ToolGetConflicts).
		Description("Check for overlapping active intents or unresolved conflicts.").
		Domain("control").
		Keywords("conflict", "overlap", "intent", "coordination", "blocked").
		Priority(82).
		ArrayParam("paths", "Paths to check for overlap", "string", false).
		BoolParam("check_intents", "Include active declared intents in the response", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params GetConflictsInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			return a.getConflictsToolData(params), nil
		}).
		Build()
}

func (a *Archivalist) getBriefingToolData(tier string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "micro":
		resp := a.getMicroBriefing()
		return resp.Briefing, nil
	case "", "standard":
		return a.GetAgentBriefing(), nil
	case "full":
		return map[string]any{
			"agent_briefing": a.GetAgentBriefing(),
			"snapshot":       a.GetSnapshot(context.Background()),
			"registry_stats": a.GetRegistry().GetStats(),
			"event_stats":    a.GetEventLog().Stats(),
			"conflicts":      a.GetUnresolvedConflicts(),
		}, nil
	default:
		return nil, fmt.Errorf("unknown briefing tier: %s", tier)
	}
}

func (a *Archivalist) queryPatternsToolData(_ context.Context, params QueryPatternsInput) any {
	limit := clampLegacyLimit(params.Limit, 10, 100)
	category := strings.ToLower(strings.TrimSpace(params.Category))
	scopes := normalizeStringSlice(params.Scope)

	patterns := a.GetPatterns()
	filtered := make([]*Pattern, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		if category != "" {
			patternCategory := strings.ToLower(strings.TrimSpace(pattern.Category))
			if patternCategory != category && !strings.HasPrefix(patternCategory, category+".") {
				continue
			}
		}
		if !patternMatchesScopes(pattern, scopes) {
			continue
		}
		filtered = append(filtered, pattern)
		if len(filtered) >= limit {
			break
		}
	}

	return map[string]any{
		"patterns": filtered,
		"count":    len(filtered),
		"category": params.Category,
		"scope":    scopes,
	}
}

func (a *Archivalist) queryFailuresToolData(_ context.Context, params QueryFailuresInput) any {
	limit := clampLegacyLimit(params.Limit, 10, 100)
	errorType := strings.ToLower(strings.TrimSpace(params.ErrorType))
	filePattern := strings.TrimSpace(params.FilePattern)

	candidates := a.agentContext.GetAllFailures()
	filtered := make([]*Failure, 0, len(candidates))
	for _, failure := range candidates {
		if failure == nil {
			continue
		}
		if errorType != "" && !failureMatchesKeyword(failure, errorType) {
			continue
		}
		if filePattern != "" && !failureMatchesFilePattern(failure, filePattern) {
			continue
		}
		filtered = append(filtered, failure)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	return map[string]any{
		"failures":     filtered,
		"count":        len(filtered),
		"error_type":   params.ErrorType,
		"file_pattern": params.FilePattern,
	}
}

func (a *Archivalist) queryContextToolData(ctx context.Context, params QueryContextInput) (any, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, fmt.Errorf("query is required")
	}

	scope := strings.ToLower(strings.TrimSpace(params.Scope))
	if scope == "" {
		scope = "session"
	}

	switch scope {
	case "session":
		if a.synthesizer != nil {
			return a.QueryContext(ctx, queryText)
		}
		return map[string]any{
			"answer":  "RAG synthesis is unavailable. Returning the current handoff context instead.",
			"scope":   scope,
			"context": a.GetAgentBriefing(),
		}, nil
	case "global", "all":
		query := ArchiveQuery{
			SearchText:      queryText,
			Limit:           10,
			IncludeArchived: true,
		}
		results, err := a.QueryCrossSession(ctx, query)
		if err != nil {
			return nil, err
		}
		if scope == "global" {
			currentSessionID := a.GetCurrentSession().ID
			filtered := make([]CrossSessionResult, 0, len(results))
			for _, result := range results {
				if result.SessionID == currentSessionID {
					continue
				}
				filtered = append(filtered, result)
			}
			results = filtered
		}

		contextResults := crossSessionResultsToRetrievalResults(results)
		if a.synthesizer != nil && len(contextResults) > 0 {
			synth, err := a.synthesizer.AnswerWithContext(ctx, queryText, contextResults)
			if err == nil {
				return map[string]any{
					"answer":   synth.Answer,
					"source":   synth.Source,
					"cache_hit": synth.CacheHit,
					"scope":    scope,
					"matches":  results,
				}, nil
			}
		}

		return map[string]any{
			"scope":   scope,
			"results": results,
			"count":   len(results),
		}, nil
	default:
		return nil, fmt.Errorf("unknown query scope: %s", scope)
	}
}

func (a *Archivalist) queryFileStateToolData(params QueryFileStateInput) any {
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return map[string]any{"found": false, "message": "path is required"}
	}

	if pathLooksLikePattern(path) {
		matches := make([]*FileState, 0)
		for _, state := range a.agentContext.GetAllFiles() {
			if state == nil || !pathPatternsOverlap(path, state.Path) {
				continue
			}
			matches = append(matches, state)
		}
		return map[string]any{
			"found":   len(matches) > 0,
			"path":    path,
			"matches": matches,
			"count":   len(matches),
		}
	}

	state, ok := a.GetFileState(path)
	if !ok {
		return map[string]any{
			"found": false,
			"path":  path,
		}
	}

	result := map[string]any{
		"found": true,
		"path":  path,
		"state": state,
	}
	if params.IncludeHistory {
		result["history"] = fileStateHistory(state)
	}
	return result
}

func (a *Archivalist) recordPatternToolData(params RecordPatternInput) any {
	category := strings.TrimSpace(params.Category)
	patternText := strings.TrimSpace(params.Pattern)
	if category == "" || patternText == "" {
		return map[string]any{
			"status":  "error",
			"message": "category and pattern are required",
		}
	}

	existing := a.agentContext.GetPattern(category)
	if existing != nil {
		if strings.EqualFold(strings.TrimSpace(existing.Pattern), patternText) || strings.EqualFold(strings.TrimSpace(existing.Description), patternText) {
			return map[string]any{
				"status":  "already_recorded",
				"pattern": existing,
			}
		}
		if len(params.Supersedes) == 0 {
			return map[string]any{
				"status":   "conflict",
				"existing": existing,
				"message":  fmt.Sprintf("Conflicting pattern. Specify 'supersedes': ['%s'] with reason.", existing.ID),
			}
		}
		if !stringSliceContains(params.Supersedes, existing.ID) {
			return map[string]any{
				"status":  "conflict",
				"existing": existing,
				"message": fmt.Sprintf("Pattern conflict targets %s; include that ID in supersedes.", existing.ID),
			}
		}
		updated := a.agentContext.UpdatePattern(existing.ID, func(pattern *Pattern) {
			pattern.Name = patternTitle(patternText)
			pattern.Pattern = patternText
			if reason := strings.TrimSpace(params.Reason); reason != "" {
				pattern.Description = reason
			} else {
				pattern.Description = patternText
			}
			pattern.Example = strings.Join(normalizeStringSlice(params.Scope), ", ")
			pattern.Source = SourceModelArchivalist
		})
		if updated == nil {
			return map[string]any{
				"status":  "error",
				"message": "failed to update superseded pattern",
			}
		}
		return map[string]any{
			"status":     "superseded",
			"pattern":    updated,
			"supersedes": normalizeStringSlice(params.Supersedes),
		}
	}

	pattern := &Pattern{
		Category:    category,
		Name:        patternTitle(patternText),
		Pattern:     patternText,
		Description: firstNonEmptyString(strings.TrimSpace(params.Reason), patternText),
		Example:     strings.Join(normalizeStringSlice(params.Scope), ", "),
		Source:      SourceModelArchivalist,
	}
	a.agentContext.RegisterPattern(pattern)
	return map[string]any{
		"status":  "recorded",
		"pattern": pattern,
	}
}

func (a *Archivalist) recordFailureToolData(params RecordFailureInput) any {
	approach := strings.TrimSpace(params.Approach)
	reason := strings.TrimSpace(params.Error)
	taskContext := strings.TrimSpace(params.Context)
	if approach == "" || reason == "" || taskContext == "" {
		return map[string]any{
			"status":  "error",
			"message": "approach, error, and context are required",
		}
	}

	if resolution := strings.TrimSpace(params.Resolution); resolution != "" {
		a.RecordFailureWithResolution(approach, reason, taskContext, resolution, SourceModelArchivalist)
	} else {
		a.RecordFailure(approach, reason, taskContext, SourceModelArchivalist)
	}

	return map[string]any{
		"status":     "recorded",
		"outcome":    firstNonEmptyString(strings.TrimSpace(params.Outcome), "failed"),
		"approach":   approach,
		"resolution": strings.TrimSpace(params.Resolution),
	}
}

func (a *Archivalist) updateFileStateToolData(params UpdateFileStateInput) any {
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return map[string]any{
			"status":  "error",
			"message": "path is required",
		}
	}

	summary := strings.TrimSpace(params.Summary)
	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "read":
		a.RecordFileRead(path, summary, SourceModelArchivalist)
	case "created":
		a.RecordFileCreated(path, summary, SourceModelArchivalist)
	case "modified":
		change := parseFileChange(params.LinesChanged, summary)
		a.agentContext.RecordFileModified(path, change, SourceModelArchivalist)
	case "deleted":
		change := parseFileChange(params.LinesChanged, firstNonEmptyString(summary, "deleted"))
		change.Description = firstNonEmptyString(change.Description, "deleted")
		a.agentContext.RecordFileModified(path, change, SourceModelArchivalist)
	default:
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("unsupported action: %s", params.Action),
		}
	}

	return map[string]any{
		"status": "updated",
		"path":   path,
		"action": params.Action,
	}
}

func (a *Archivalist) getConflictsToolData(params GetConflictsInput) any {
	paths := normalizeStringSlice(params.Paths)
	checkIntents := params.CheckIntents
	conflicts := a.GetUnresolvedConflicts()
	filtered := make([]*ConflictRecord, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict == nil {
			continue
		}
		if !checkIntents && conflict.Type == ConflictTypeIntent {
			continue
		}
		if len(paths) > 0 && !conflictMatchesPaths(conflict, paths) {
			continue
		}
		filtered = append(filtered, conflict)
	}

	result := map[string]any{
		"conflicts": filtered,
		"count":     len(filtered),
	}
	if checkIntents {
		result["active_intents"] = a.conflictingWorkIntentsForPaths(paths)
	}
	return result
}

func crossSessionResultsToRetrievalResults(results []CrossSessionResult) []*RetrievalResult {
	contextResults := make([]*RetrievalResult, 0, len(results))
	for _, result := range results {
		if result.Entry == nil {
			continue
		}
		contextResults = append(contextResults, &RetrievalResult{
			ID:       result.Entry.ID,
			Content:  result.Entry.Content,
			Score:    1.0,
			Source:   result.SessionID,
			Category: string(result.Entry.Category),
			Type:     "entry",
		})
	}
	return contextResults
}

func clampLegacyLimit(limit, fallback, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func patternMatchesScopes(pattern *Pattern, scopes []string) bool {
	if pattern == nil || len(scopes) == 0 {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		pattern.Category,
		pattern.Name,
		pattern.Pattern,
		pattern.Description,
		pattern.Example,
	}, " "))
	for _, scope := range scopes {
		trimmed := strings.ToLower(strings.TrimSpace(scope))
		if trimmed == "" {
			continue
		}
		if strings.Contains(haystack, trimmed) {
			return true
		}
	}
	return false
}

func failureMatchesKeyword(failure *Failure, keyword string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		failure.Approach,
		failure.Reason,
		failure.Context,
		failure.Resolution,
	}, " "))
	return strings.Contains(haystack, keyword)
}

func failureMatchesFilePattern(failure *Failure, filePattern string) bool {
	for _, affected := range failure.FilesAffected {
		if pathPatternsOverlap(filePattern, affected) {
			return true
		}
	}
	return false
}

func pathLooksLikePattern(path string) bool {
	return strings.ContainsAny(path, "*?[]")
}

func fileStateHistory(state *FileState) []map[string]any {
	if state == nil {
		return nil
	}
	history := make([]map[string]any, 0, len(state.Changes)+1)
	if !state.ReadAt.IsZero() {
		history = append(history, map[string]any{
			"action":    "read",
			"timestamp": state.ReadAt,
			"summary":   state.Summary,
		})
	}
	for _, change := range state.Changes {
		history = append(history, map[string]any{
			"action":      "modified",
			"timestamp":   change.Timestamp,
			"start_line":  change.StartLine,
			"end_line":    change.EndLine,
			"description": change.Description,
		})
	}
	if state.Status == FileStatusCreated {
		history = append(history, map[string]any{
			"action":    "created",
			"timestamp": state.ModifiedAt,
			"summary":   state.Summary,
		})
	}
	return history
}

func parseFileChange(linesChanged, description string) FileChange {
	change := FileChange{
		Description: strings.TrimSpace(description),
		Lines:       strings.TrimSpace(linesChanged),
	}
	trimmed := strings.TrimSpace(linesChanged)
	if trimmed == "" {
		return change
	}
	var start, end int
	if _, err := fmt.Sscanf(trimmed, "%d-%d", &start, &end); err == nil {
		change.StartLine = start
		change.EndLine = end
		return change
	}
	if _, err := fmt.Sscanf(trimmed, "%d", &start); err == nil {
		change.StartLine = start
		change.EndLine = start
	}
	return change
}

func conflictMatchesPaths(conflict *ConflictRecord, paths []string) bool {
	if conflict == nil || len(paths) == 0 {
		return true
	}
	for _, path := range paths {
		if pathPatternsOverlap(path, conflict.Key) {
			return true
		}
		for _, keyPart := range strings.Split(conflict.Key, ",") {
			if pathPatternsOverlap(path, keyPart) {
				return true
			}
		}
	}
	return false
}

func patternTitle(pattern string) string {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return "Pattern"
	}
	if len(trimmed) > 80 {
		return trimmed[:80]
	}
	return trimmed
}

func stringSliceContains(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (h *ToolHandler) handleGetBriefing(ctx context.Context, input json.RawMessage) (string, error) {
	var params GetBriefingInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	result, err := h.archivalist.getBriefingToolData(params.Tier)
	if err != nil {
		return "", err
	}
	return legacyToolResultString(result)
}

func (h *ToolHandler) handleQueryPatterns(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryPatternsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.queryPatternsToolData(ctx, params))
}

func (h *ToolHandler) handleQueryFailures(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryFailuresInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.queryFailuresToolData(ctx, params))
}

func (h *ToolHandler) handleQueryContext(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryContextInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	result, err := h.archivalist.queryContextToolData(ctx, params)
	if err != nil {
		return "", err
	}
	return legacyToolResultString(result)
}

func (h *ToolHandler) handleQueryFileState(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryFileStateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.queryFileStateToolData(params))
}

func (h *ToolHandler) handleRecordPattern(ctx context.Context, input json.RawMessage) (string, error) {
	var params RecordPatternInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.recordPatternToolData(params))
}

func (h *ToolHandler) handleRecordFailure(ctx context.Context, input json.RawMessage) (string, error) {
	var params RecordFailureInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.recordFailureToolData(params))
}

func (h *ToolHandler) handleUpdateFileState(ctx context.Context, input json.RawMessage) (string, error) {
	var params UpdateFileStateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.updateFileStateToolData(params))
}

func (h *ToolHandler) handleDeclareIntent(ctx context.Context, input json.RawMessage) (string, error) {
	var params DeclareIntentInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.declareWorkIntent(params, h.archivalist.id))
}

func (h *ToolHandler) handleCompleteIntent(ctx context.Context, input json.RawMessage) (string, error) {
	var params CompleteIntentInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.completeWorkIntent(params))
}

func (h *ToolHandler) handleGetConflicts(ctx context.Context, input json.RawMessage) (string, error) {
	var params GetConflictsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return legacyToolResultString(h.archivalist.getConflictsToolData(params))
}

func legacyToolResultString(result any) (string, error) {
	switch typed := result.(type) {
	case string:
		return typed, nil
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
}
