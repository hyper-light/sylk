package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
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
//
// Typed protocol errors (ProtocolError, skills.ContractViolation) project
// their structured fields into the payload so the LLM reads
// {rule_id, scope, missing_artifact, recovery_action, human_message}
// instead of parsing English prose. The prose from err.Error() still
// lands in "error" as a fallback for consumers that don't inspect
// typed fields yet.
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
	if pe, ok := AsProtocolError(err); ok && pe != nil {
		payload["protocol_error"] = pe
		payload["error_kind"] = "protocol_violation"
		if pe.RecoveryAction != "" {
			payload["recovery_action"] = pe.RecoveryAction
		}
	}
	var cv *skills.ContractViolation
	if errors.As(err, &cv) && cv != nil {
		payload["contract_violation"] = cv
		payload["error_kind"] = "contract_violation"
		if cv.RecoveryAction != "" {
			payload["recovery_action"] = cv.RecoveryAction
		}
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

func WrapApprovalDenied(toolName string, err error) error {
	if err == nil || !errors.Is(err, commandapproval.ErrApprovalDenied) || errors.Is(err, skills.ErrDelegatedRequested) {
		return err
	}
	return ApprovalDeniedDelegatedError(toolName, approvalDeniedReason(err))
}

func ApprovalDeniedDelegatedError(toolName, reason string) error {
	message := approvalDeniedUserMessage(toolName)
	payload := map[string]any{
		"status":       "approval_denied",
		"tool_name":    strings.TrimSpace(toolName),
		"user_message": message,
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		payload["reason"] = trimmed
	}
	return skills.NewDelegatedError(payload, message)
}

func DelegatedToolMessage(output string, err error) string {
	if message := ToolOutputUserMessage(output); message != "" {
		return message
	}
	return strings.TrimSpace(skills.DelegatedMessage(err))
}

func ToolOutputUserMessage(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(output), &payload); err == nil {
		if summary := SummarizeInterAgentPayload(payload); summary != "" {
			return summary
		}
	}
	return output
}

func approvalDeniedUserMessage(toolName string) string {
	target := strings.TrimSpace(toolName)
	if target == "" {
		return "The user denied approval for this operation. What would you like me to do next or instead?"
	}
	return fmt.Sprintf("The user denied approval for %s. What would you like me to do next or instead?", target)
}

func approvalDeniedReason(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.TrimSpace(err.Error())
	lowered := strings.ToLower(reason)
	const marker = "command approval denied:"
	if idx := strings.Index(lowered, marker); idx >= 0 {
		return strings.TrimSpace(reason[idx+len(marker):])
	}
	return reason
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
	case errors.Is(err, ErrRouteTestExecutionToTester):
		return toolErrorDetailPayload{
			Kind: "route_test_execution_to_tester",
			Recovery: []string{
				"Do not retry the same inspector test-execution call",
				"Route the execution-backed test work to Tester with challenge_agent or handoff_next",
				"Have Tester return the requested evidence with validate_work for a challenge or handoff_next for a normal top-level testing turn",
			},
		}
	case errors.Is(err, ErrRouteTestToolingToTester):
		return toolErrorDetailPayload{
			Kind: "route_test_tooling_to_tester",
			Recovery: []string{
				"Do not retry the same inspector dependency-install call",
				"Route the blocked test-tooling work to Tester with challenge_agent or handoff_next",
				"Have Tester use research_test_tool_install first and install_test_tooling only after it has a concrete plan",
			},
		}
	case strings.Contains(message, "only accepts one plain command"),
		strings.Contains(message, "shell control operators are not allowed"):
		return toolErrorDetailPayload{
			Kind: "single_command_only",
			Recovery: []string{
				"Use working_dir instead of cd when you only need a different directory",
				"For compound scripts (chaining, pipes, redirection, shell variables, multi-line) pass the full script to bash; the approval policy adapts automatically",
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
	case executableNotFound(err):
		return toolErrorDetailPayload{
			Kind: "missing_executable",
			Recovery: []string{
				"Inspect the detected harness, generated command, and working directory before retrying",
				"If the executable is a language or test tool, look for an available runtime or module-based invocation instead of repeating the same launcher",
				"If the toolchain is actually missing, use the relevant research/install tooling path before retrying suite execution",
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
	}
	return toolErrorDetailPayload{}
}

func executableNotFound(err error) bool {
	var execErr *purevfs.ExecutableNotFoundError
	return errors.As(err, &execErr)
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

// SkippedToolResultPayload returns a synthetic tool_result payload for tool
// calls that were not executed because an earlier tool in the same assistant
// batch already ended or redirected the turn.
func SkippedToolResultPayload(reason string) string {
	payload := map[string]any{
		"error":      "tool call skipped",
		"error_kind": "tool_call_skipped",
		"skipped":    true,
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		payload["reason"] = trimmed
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return `{"error":"tool call skipped","error_kind":"tool_call_skipped","skipped":true}`
	}
	return string(payloadJSON)
}

// PostCorrectiveClaimFromContext posts a corrective claim from the
// tool loop error path. Looks up the board from the session registry
// via the accumulator's session ID. Nil-safe.
func PostCorrectiveClaimFromContext(ctx context.Context, agentID, title, description string, scope []claims.ClaimScopeEntry) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	sessionID := acc.SessionID()
	if sessionID == "" {
		return
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(sessionID)
	if board == nil {
		return
	}
	_ = claims.PostCorrectiveClaim(board, agentID, title, description, scope)
}

// AppendSkippedToolResults appends synthetic error tool results for any tool
// calls left unexecuted in the current assistant batch. This preserves the
// provider contract for APIs that require a tool_result for every prior
// tool_use block before the next model turn.
func AppendSkippedToolResults(req *providers.Request, calls []providers.ToolCall, reason string) {
	if req == nil || len(calls) == 0 {
		return
	}
	payload := SkippedToolResultPayload(reason)
	for _, call := range calls {
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    payload,
			IsError:    true,
		})
	}
}
