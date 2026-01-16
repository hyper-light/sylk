package archivalist

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// ToolHandler handles tool calls from agents
type ToolHandler struct {
	archivalist *Archivalist
	synthesizer *Synthesizer
}

// NewToolHandler creates a new tool handler
func NewToolHandler(a *Archivalist, s *Synthesizer) *ToolHandler {
	return &ToolHandler{
		archivalist: a,
		synthesizer: s,
	}
}

// toolHandlerFunc is the signature for tool handlers
type toolHandlerFunc func(context.Context, json.RawMessage) (string, error)

// Handle processes a tool call and returns the result
func (h *ToolHandler) Handle(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
	handler := h.getHandler(toolName)
	if handler == nil {
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}

	var inputMap map[string]any
	if err := json.Unmarshal(input, &inputMap); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	toolData := &skills.ToolCallHookData{
		ToolName: toolName,
		Input:    inputMap,
	}
	if h.archivalist.hooks != nil {
		updated, result, err := h.archivalist.hooks.ExecutePreToolCallHooks(ctx, toolData)
		if err != nil {
			return "", err
		}
		if result.SkipExecution {
			return result.SkipResponse, nil
		}
		toolData = updated
	}

	updatedInput, err := json.Marshal(toolData.Input)
	if err != nil {
		return "", err
	}

	output, err := handler(ctx, updatedInput)
	toolData.Output = output
	toolData.Error = err
	if h.archivalist.hooks != nil {
		_, _, hookErr := h.archivalist.hooks.ExecutePostToolCallHooks(ctx, toolData)
		if hookErr != nil && err == nil {
			return "", hookErr
		}
	}

	return output, err
}

func (h *ToolHandler) getHandler(toolName string) toolHandlerFunc {
	handlers := map[string]toolHandlerFunc{
		ToolGetBriefing:     h.handleGetBriefing,
		ToolQueryPatterns:   h.handleQueryPatterns,
		ToolQueryFailures:   h.handleQueryFailures,
		ToolQueryContext:    h.handleQueryContext,
		ToolQueryFileState:  h.handleQueryFileState,
		ToolRecordPattern:   h.handleRecordPattern,
		ToolRecordFailure:   h.handleRecordFailure,
		ToolUpdateFileState: h.handleUpdateFileState,
		ToolDeclareIntent:   h.handleDeclareIntent,
		ToolCompleteIntent:  h.handleCompleteIntent,
		ToolGetConflicts:    h.handleGetConflicts,
	}
	return handlers[toolName]
}

func (h *ToolHandler) handleGetBriefing(ctx context.Context, input json.RawMessage) (string, error) {
	var params GetBriefingInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	tier := params.Tier
	if tier == "" {
		tier = "standard"
	}

	return h.getBriefingByTier(tier)
}

func (h *ToolHandler) getBriefingByTier(tier string) (string, error) {
	switch tier {
	case "micro":
		return h.getMicroBriefing()
	case "full":
		return h.getFullBriefing()
	default:
		return h.getStandardBriefing()
	}
}

func (h *ToolHandler) getMicroBriefing() (string, error) {
	resume := h.archivalist.GetResumeState()
	if resume == nil {
		return "no-task:0/0:none:block=none", nil
	}

	modFiles := h.archivalist.GetModifiedFiles()
	modPaths := extractFilePaths(modFiles)
	blocker := firstBlocker(resume.Blockers)
	totalSteps := len(resume.CompletedSteps) + len(resume.NextSteps)

	briefing := MicroBriefing(
		resume.CurrentTask,
		len(resume.CompletedSteps),
		totalSteps,
		modPaths,
		blocker,
	)

	return briefing, nil
}

func (h *ToolHandler) getStandardBriefing() (string, error) {
	briefing := h.archivalist.GetAgentBriefing()
	return toJSON(briefing)
}

func (h *ToolHandler) getFullBriefing() (string, error) {
	briefing := h.archivalist.GetAgentBriefing()
	snapshot := h.archivalist.GetSnapshot(context.Background())

	fullBriefing := map[string]any{
		"agent_briefing": briefing,
		"snapshot":       snapshot,
		"registry_stats": h.archivalist.GetRegistry().GetStats(),
		"event_stats":    h.archivalist.GetEventLog().Stats(),
	}

	return toJSON(fullBriefing)
}

func (h *ToolHandler) handleQueryPatterns(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryPatternsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	patterns := h.archivalist.GetPatternsByCategory(params.Category)
	return toJSON(patterns)
}

func (h *ToolHandler) handleQueryFailures(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryFailuresInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	failures := h.archivalist.GetRecentFailures(limit)
	return toJSON(failures)
}

func (h *ToolHandler) handleQueryContext(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryContextInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if h.synthesizer == nil {
		return h.fallbackQueryContext(params.Query)
	}

	sessionID := h.archivalist.GetCurrentSession().ID
	queryType := ClassifyQuery(params.Query)

	response, err := h.synthesizer.Answer(ctx, params.Query, sessionID, queryType)
	if err != nil {
		return "", fmt.Errorf("synthesis failed: %w", err)
	}

	return toJSON(map[string]any{
		"answer":    response.Answer,
		"source":    response.Source,
		"cache_hit": response.CacheHit,
	})
}

func (h *ToolHandler) fallbackQueryContext(query string) (string, error) {
	briefing := h.archivalist.GetAgentBriefing()
	return toJSON(map[string]any{
		"answer":  "Full RAG not available. Here is current context:",
		"context": briefing,
	})
}

func (h *ToolHandler) handleQueryFileState(ctx context.Context, input json.RawMessage) (string, error) {
	var params QueryFileStateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	state, ok := h.archivalist.GetFileState(params.Path)
	if !ok {
		return toJSON(map[string]any{
			"found": false,
			"path":  params.Path,
		})
	}

	return toJSON(map[string]any{
		"found": true,
		"state": state,
	})
}

func (h *ToolHandler) handleRecordPattern(ctx context.Context, input json.RawMessage) (string, error) {
	var params RecordPatternInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	h.archivalist.RegisterPattern(
		params.Category,
		params.Pattern,
		params.Pattern,
		"",
		SourceModelArchivalist,
	)

	return toJSON(map[string]any{
		"status":   "recorded",
		"category": params.Category,
	})
}

func (h *ToolHandler) handleRecordFailure(ctx context.Context, input json.RawMessage) (string, error) {
	var params RecordFailureInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	h.archivalist.RecordFailureWithResolution(
		params.Approach,
		params.Error,
		params.Context,
		params.Resolution,
		SourceModelArchivalist,
	)

	return toJSON(map[string]any{
		"status":  "recorded",
		"outcome": params.Outcome,
	})
}

func (h *ToolHandler) handleUpdateFileState(ctx context.Context, input json.RawMessage) (string, error) {
	var params UpdateFileStateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	h.recordFileAction(params)

	return toJSON(map[string]any{
		"status": "updated",
		"path":   params.Path,
		"action": params.Action,
	})
}

func (h *ToolHandler) recordFileAction(params UpdateFileStateInput) {
	switch params.Action {
	case "read":
		h.archivalist.RecordFileRead(params.Path, params.Summary, SourceModelArchivalist)
	case "created":
		h.archivalist.RecordFileCreated(params.Path, params.Summary, SourceModelArchivalist)
	case "modified":
		h.archivalist.RecordFileModified(params.Path, 0, 0, params.Summary, SourceModelArchivalist)
	}
}

func (h *ToolHandler) handleDeclareIntent(ctx context.Context, input json.RawMessage) (string, error) {
	var params DeclareIntentInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	return toJSON(map[string]any{
		"status":      "declared",
		"type":        params.Type,
		"description": params.Description,
	})
}

func (h *ToolHandler) handleCompleteIntent(ctx context.Context, input json.RawMessage) (string, error) {
	var params CompleteIntentInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	return toJSON(map[string]any{
		"status":    "completed",
		"intent_id": params.IntentID,
		"success":   params.Success,
	})
}

func (h *ToolHandler) handleGetConflicts(ctx context.Context, input json.RawMessage) (string, error) {
	var params GetConflictsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	conflicts := h.archivalist.GetUnresolvedConflicts()

	return toJSON(map[string]any{
		"conflicts": conflicts,
		"count":     len(conflicts),
	})
}

// Helper functions

func extractFilePaths(files []*FileState) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

func firstBlocker(blockers []string) string {
	if len(blockers) > 0 {
		return blockers[0]
	}
	return ""
}

func toJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
