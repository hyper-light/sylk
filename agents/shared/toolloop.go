package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
)

// MaxConsecutiveToolErrors is the threshold of consecutive all-error tool-call
// turns before the tool loop aborts. Derived from the UpdateToolErrors contract:
// a single successful call resets the counter to 0, so this represents two
// complete turns where every call in the batch errored — strong evidence of a
// systematic rather than transient failure.
const MaxConsecutiveToolErrors = 2

// ToolCallSignature identifies a tool call by its name and serialized arguments.
type ToolCallSignature struct {
	Name      string
	Arguments string
}

const (
	preparePipelineWriteContextTool = "prepare_pipeline_write_context"
	prepareGlobalWriteContextTool   = "prepare_global_write_context"
	staleWriteBasisMarker           = "write basis is stale"
	basisPathMismatchMarker         = "basis path"
	maxIdenticalToolBatchStreak     = 2
)

// MarshalToolOutput converts arbitrary tool output to a string representation.
// Strings pass through directly, fmt.Stringer values use their String method,
// and all other types are JSON-marshaled.
func MarshalToolOutput(data any) (string, error) {
	switch typed := data.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("marshal tool output: %w", err)
		}
		return string(payload), nil
	}
}

// ToolErrorPayload returns a JSON-encoded error payload for a tool execution failure.
// Returns an empty string when err is nil.
func ToolErrorPayload(err error) string {
	if err == nil {
		return ""
	}
	detail := toolErrorDetail(err)
	payload := map[string]any{
		"error": strings.TrimSpace(err.Error()),
	}
	if detail.Kind != "" {
		payload["error_kind"] = detail.Kind
	}
	if len(detail.Recovery) > 0 {
		payload["recovery"] = append([]string(nil), detail.Recovery...)
	}
	if detail.Retryable {
		payload["retryable"] = true
	}
	payloadJSON, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(payloadJSON)
}

// CoerceMap converts an arbitrary value to map[string]any via JSON round-trip.
// Returns nil if the value is nil, already a map[string]any (returned directly),
// or cannot be marshaled/unmarshaled.
func CoerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return nil
	}
	return mapped
}

// DetectToolCallDuplicate checks whether the current batch is a genuinely stuck
// repeat of the immediately prior tool-call batch. It no longer treats "seen at
// any point earlier in the run" as a duplicate, because agents regularly need
// to reread or revisit the same surface later with new state or intent.
//
// A batch is considered duplicate only after the same exact batch (same tool
// order and same serialized arguments) has repeated more than
// maxIdenticalToolBatchStreak times consecutively without an intervening batch
// change. The second return value is the first duplicate signature encountered
// (zero value if none).
func DetectToolCallDuplicate(
	calls []providers.ToolCall,
	seen map[ToolCallSignature]int,
	history ...[]providers.Message,
) (bool, ToolCallSignature) {
	batch := buildToolCallSignatures(calls)
	if len(batch) == 0 {
		return true, ToolCallSignature{}
	}
	messages := firstToolHistory(history)
	if !currentBatchRepeatsPrevious(batch, messages) {
		resetToolCallStreak(seen, batch)
		return false, ToolCallSignature{}
	}
	if batchAllowsImmediateRetry(batch, messages) {
		resetToolCallStreak(seen, batch)
		return false, ToolCallSignature{}
	}
	return incrementToolCallStreak(seen, batch)
}

func buildToolCallSignatures(calls []providers.ToolCall) []ToolCallSignature {
	batch := make([]ToolCallSignature, 0, len(calls))
	for _, call := range calls {
		batch = append(batch, ToolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		})
	}
	return batch
}

func firstToolHistory(history [][]providers.Message) []providers.Message {
	if len(history) == 0 {
		return nil
	}
	return history[0]
}

func currentBatchRepeatsPrevious(batch []ToolCallSignature, messages []providers.Message) bool {
	previous := previousToolCallBatch(messages)
	if len(previous) != len(batch) {
		return false
	}
	for i := range batch {
		if batch[i] != previous[i] {
			return false
		}
	}
	return len(batch) > 0
}

func previousToolCallBatch(messages []providers.Message) []ToolCallSignature {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		msg := messages[idx]
		if msg.Role != providers.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		return buildToolCallSignatures(msg.ToolCalls)
	}
	return nil
}

func batchAllowsImmediateRetry(batch []ToolCallSignature, messages []providers.Message) bool {
	for _, sig := range batch {
		if !allowImmediateRepeatedToolCall(sig, messages) {
			return false
		}
	}
	return len(batch) > 0
}

func incrementToolCallStreak(seen map[ToolCallSignature]int, batch []ToolCallSignature) (bool, ToolCallSignature) {
	var firstDup ToolCallSignature
	allDup := true
	for _, sig := range batch {
		seen[sig]++
		if seen[sig] <= maxIdenticalToolBatchStreak {
			allDup = false
		}
		if firstDup.Name == "" {
			firstDup = sig
		}
	}
	if !allDup {
		return false, ToolCallSignature{}
	}
	return true, firstDup
}

func resetToolCallStreak(seen map[ToolCallSignature]int, batch []ToolCallSignature) {
	clear(seen)
	for _, sig := range batch {
		seen[sig] = 1
	}
}

func allowImmediateRepeatedToolCall(sig ToolCallSignature, messages []providers.Message) bool {
	if !isWriteContextPrepareTool(sig.Name) {
		return false
	}
	return trailingToolTurnAllowsPrepareRefresh(messages, sig.Name)
}

func isWriteContextPrepareTool(name string) bool {
	switch strings.TrimSpace(name) {
	case preparePipelineWriteContextTool, prepareGlobalWriteContextTool:
		return true
	default:
		return false
	}
}

func trailingToolTurnAllowsPrepareRefresh(messages []providers.Message, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || len(messages) == 0 {
		return false
	}
	for _, msg := range trailingToolTurn(messages) {
		if toolMessageRequestsRetry(msg, toolName) || toolMessageRefreshesWriteContext(msg) {
			return true
		}
	}
	return false
}

func trailingToolTurn(messages []providers.Message) []providers.Message {
	start := len(messages)
	for start > 0 && messages[start-1].Role == providers.RoleTool {
		start--
	}
	return messages[start:]
}

func toolMessageRequestsRetry(msg providers.Message, toolName string) bool {
	content := strings.ToLower(strings.TrimSpace(msg.Content))
	return msg.Role == providers.RoleTool &&
		msg.IsError &&
		content != "" &&
		strings.Contains(content, staleWriteBasisMarker) &&
		strings.Contains(content, "rerun "+strings.ToLower(strings.TrimSpace(toolName)))
}

func toolMessageRefreshesWriteContext(msg providers.Message) bool {
	if msg.Role != providers.RoleTool || !isWriteContextMutationTool(msg.ToolName) {
		return false
	}
	if !msg.IsError {
		return true
	}
	return toolMessageHasBasisRefreshHint(msg.Content)
}

func isWriteContextMutationTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "write_pipeline_file", "edit_pipeline_file", "delete_pipeline_file", "create_pipeline_directory":
		return true
	case "write_global_file", "edit_global_file", "delete_global_file", "create_global_directory":
		return true
	case "write_test", "write_integration_test", "write_e2e_test":
		return true
	default:
		return false
	}
}

func toolMessageHasBasisRefreshHint(content string) bool {
	lowered := strings.ToLower(strings.TrimSpace(content))
	if lowered == "" {
		return false
	}
	return strings.Contains(lowered, staleWriteBasisMarker) || strings.Contains(lowered, basisPathMismatchMarker)
}

// UpdateToolErrors increments the consecutive-error counter when every call in a
// batch failed, and resets it to zero otherwise.
func UpdateToolErrors(current, errCount, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}

func ToolErrorCountsTowardAbort(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, commandapproval.ErrApprovalDenied)
}

type toolErrorDetailPayload struct {
	Kind      string
	Retryable bool
	Recovery  []string
}

func toolErrorDetail(err error) toolErrorDetailPayload {
	if err == nil {
		return toolErrorDetailPayload{}
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "only accepts one plain command"),
		strings.Contains(message, "shell control operators are not allowed in run_command"):
		return toolErrorDetailPayload{
			Kind: "single_command_only",
			Recovery: []string{
				"Use working_dir instead of cd when you only need a different directory",
				"Split chained work into separate run_command calls",
				"Use run_shell_script for &&, ||, ;, pipes, redirection, shell variables, or multi-line shell",
			},
		}
	case strings.Contains(message, staleWriteBasisMarker),
		strings.Contains(message, basisPathMismatchMarker):
		return toolErrorDetailPayload{
			Kind:      "stale_write_basis",
			Retryable: true,
			Recovery: []string{
				"Rerun the matching prepare write-context tool for the target path",
				"Retry the write/edit/delete call with the refreshed basis instead of repeating the stale invocation",
			},
		}
	case errors.Is(err, purevfs.ErrStrictExecutionUnavailable):
		return toolErrorDetailPayload{
			Kind: "strict_execution_unavailable",
			Recovery: []string{
				"Use a simpler command, a dedicated higher-level tool, or a workspace mode that supports strict broker execution",
			},
		}
	case errors.Is(err, commandapproval.ErrApprovalRequired):
		return toolErrorDetailPayload{
			Kind: "approval_required",
			Recovery: []string{
				"Wait for Guardian approval or choose a less sensitive command that fits the pre-approved path",
			},
		}
	case errors.Is(err, commandapproval.ErrApprovalDenied):
		return toolErrorDetailPayload{
			Kind: "approval_denied",
			Recovery: []string{
				"The user denied this command, so do not retry the same invocation",
				"Choose a different safe approach or a less sensitive command if one exists",
				"If the task is blocked on the user's preference, explain the blockage and ask what they want to do instead",
			},
		}
	default:
		return toolErrorDetailPayload{}
	}
}

func ToolRecoveryHint(toolName string, err error) string {
	detail := toolErrorDetail(err)
	if len(detail.Recovery) == 0 {
		return ""
	}
	toolName = strings.TrimSpace(toolName)
	prefix := "Adapt after the failed tool call"
	if toolName != "" {
		prefix = "Adapt after the failed " + toolName + " call"
	}
	return prefix + ": " + strings.Join(detail.Recovery, "; ") + ". Do not repeat the same invalid invocation."
}

func AppendToolRecoveryMessage(req *providers.Request, hints []string) {
	if req == nil || len(hints) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(hints))
	unique := make([]string, 0, len(hints))
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		if _, ok := seen[hint]; ok {
			continue
		}
		seen[hint] = struct{}{}
		unique = append(unique, hint)
	}
	if len(unique) == 0 {
		return
	}
	var builder strings.Builder
	builder.WriteString("Tool recovery guidance:\n")
	for _, hint := range unique {
		builder.WriteString("- ")
		builder.WriteString(hint)
		builder.WriteString("\n")
	}
	req.Messages = append(req.Messages, providers.Message{
		Role:    providers.RoleUser,
		Content: strings.TrimSpace(builder.String()),
	})
}
