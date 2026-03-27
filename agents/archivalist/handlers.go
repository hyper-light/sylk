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

	inputMap, err := decodeToolInput(input)
	if err != nil {
		return "", err
	}

	toolData, result, err := h.runPreToolHooks(ctx, toolName, inputMap)
	if result != nil {
		return result.output, result.err
	}
	if err != nil {
		return "", err
	}

	output, err := h.handleToolCall(ctx, handler, toolData)
	hookErr := h.runPostToolHooks(ctx, toolData, output, err)
	if hookErr != nil && err == nil {
		return "", hookErr
	}

	return output, err
}

type toolHookResult struct {
	output string
	err    error
}

func decodeToolInput(input json.RawMessage) (map[string]any, error) {
	var inputMap map[string]any
	if err := json.Unmarshal(input, &inputMap); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	return inputMap, nil
}

func (h *ToolHandler) runPreToolHooks(ctx context.Context, toolName string, inputMap map[string]any) (*skills.ToolCallHookData, *toolHookResult, error) {
	toolData := &skills.ToolCallHookData{
		ToolName: toolName,
		Input:    inputMap,
	}
	if h.archivalist.hooks == nil {
		return toolData, nil, nil
	}
	updated, result, err := h.archivalist.hooks.ExecutePreToolCallHooks(ctx, toolData)
	if err != nil {
		return nil, nil, err
	}
	if result.SkipExecution {
		return nil, &toolHookResult{output: result.SkipResponse, err: nil}, nil
	}
	return updated, nil, nil
}

func (h *ToolHandler) handleToolCall(ctx context.Context, handler toolHandlerFunc, toolData *skills.ToolCallHookData) (string, error) {
	updatedInput, err := json.Marshal(toolData.Input)
	if err != nil {
		return "", err
	}
	return handler(ctx, updatedInput)
}

func (h *ToolHandler) runPostToolHooks(ctx context.Context, toolData *skills.ToolCallHookData, output string, err error) error {
	toolData.Output = output
	toolData.Error = err
	if h.archivalist.hooks == nil {
		return nil
	}
	_, _, hookErr := h.archivalist.hooks.ExecutePostToolCallHooks(ctx, toolData)
	return hookErr
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
