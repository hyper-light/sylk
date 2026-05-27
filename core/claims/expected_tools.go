package claims

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ExpectedToolRegistry is the optional validation surface used by
// callers that can verify tool names against the active skill registry.
type ExpectedToolRegistry interface {
	HasTool(name string) bool
}

// ValidateExpectedToolCalls checks structural correctness. When a
// registry is provided, tool names must also exist in the registry.
func ValidateExpectedToolCalls(calls []ExpectedToolCall, registry ExpectedToolRegistry) error {
	seen := make(map[string]struct{}, len(calls))
	for idx := range calls {
		call := calls[idx]
		id := strings.TrimSpace(call.ID)
		if id != "" {
			if _, ok := seen[id]; ok {
				return fmt.Errorf("expected tool call %q appears more than once", id)
			}
			seen[id] = struct{}{}
		}
		if err := ValidateExpectedToolCall(call, registry); err != nil {
			return fmt.Errorf("expected tool call[%d]: %w", idx, err)
		}
	}
	return nil
}

// ValidateExpectedToolCall checks one expected tool call. It rejects
// provider-only call identifiers because expected tools are durable Sylk
// skill specs, not provider tool-call echoes.
func ValidateExpectedToolCall(call ExpectedToolCall, registry ExpectedToolRegistry) error {
	tool := strings.TrimSpace(call.Tool)
	if tool == "" {
		return fmt.Errorf("tool is required")
	}
	if strings.Contains(tool, ".") {
		return fmt.Errorf("tool %q must be a Sylk skill name, not a provider-qualified tool id", tool)
	}
	if registry != nil && !registry.HasTool(tool) {
		return fmt.Errorf("unknown tool %q", tool)
	}
	if _, ok := call.Arguments["tool_call_id"]; ok {
		return fmt.Errorf("provider tool_call_id is not allowed")
	}
	if _, ok := call.Arguments["provider_tool_call_id"]; ok {
		return fmt.Errorf("provider_tool_call_id is not allowed")
	}
	if len(call.Arguments) > 0 {
		if _, err := json.Marshal(call.Arguments); err != nil {
			return fmt.Errorf("arguments must be JSON serializable: %w", err)
		}
	}
	if call.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds must be non-negative")
	}
	return nil
}

const (
	ExpectedToolMetadataID          = "expected_tool_call_id"
	ExpectedToolMetadataTool        = "expected_tool"
	ExpectedToolMetadataPurpose     = "expected_tool_purpose"
	ExpectedToolMetadataFailureKind = "expected_tool_failure_kind"
	ExpectedToolMetadataRequired    = "expected_tool_required"
)

// ExpectedToolFailureArtifact returns an error-family artifact for a
// failed or refused expected tool. Callers attach it to the testament or
// validation result that explains the failed work.
func ExpectedToolFailureArtifact(call ExpectedToolCall, agentID, failureKind, message string) *Artifact {
	failureKind = strings.TrimSpace(failureKind)
	if failureKind == "" {
		failureKind = ArtifactKindInvalidExpectedToolCall
	}
	if !isErrorArtifactKind(failureKind) {
		failureKind = ArtifactKindError
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "expected tool call failed"
	}
	metadata := map[string]any{
		ExpectedToolMetadataID:          strings.TrimSpace(call.ID),
		ExpectedToolMetadataTool:        strings.TrimSpace(call.Tool),
		ExpectedToolMetadataFailureKind: failureKind,
		ExpectedToolMetadataRequired:    call.Required,
	}
	if purpose := strings.TrimSpace(call.Purpose); purpose != "" {
		metadata[ExpectedToolMetadataPurpose] = purpose
	}
	return &Artifact{
		AgentID:   strings.TrimSpace(agentID),
		Kind:      failureKind,
		Reference: message,
		Metadata:  metadata,
	}
}

func stampExpectedToolCalls(calls []ExpectedToolCall) []ExpectedToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ExpectedToolCall, 0, len(calls))
	for _, call := range calls {
		call.ID = strings.TrimSpace(call.ID)
		if call.ID == "" {
			call.ID = "etc_" + uuid.NewString()
		}
		call.Tool = strings.TrimSpace(call.Tool)
		call.Purpose = strings.TrimSpace(call.Purpose)
		call.ProducesArtifacts = normalizeStringList(call.ProducesArtifacts)
		out = append(out, call)
	}
	return out
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func expectedToolCallContext(calls []ExpectedToolCall) []map[string]any {
	if len(calls) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		entry := map[string]any{
			"id":       call.ID,
			"tool":     call.Tool,
			"required": call.Required,
		}
		if len(call.Arguments) > 0 {
			entry["arguments"] = sortedAnyMap(call.Arguments)
		}
		if strings.TrimSpace(call.Purpose) != "" {
			entry["purpose"] = call.Purpose
		}
		if len(call.ProducesArtifacts) > 0 {
			entry["produces_artifacts"] = append([]string(nil), call.ProducesArtifacts...)
		}
		if call.TimeoutSeconds > 0 {
			entry["timeout_seconds"] = call.TimeoutSeconds
		}
		if call.Policy.AllowAgentSubstitution || call.Policy.RequiresUserApproval {
			entry["policy"] = map[string]any{
				"allow_agent_substitution": call.Policy.AllowAgentSubstitution,
				"requires_user_approval":   call.Policy.RequiresUserApproval,
			}
		}
		out = append(out, entry)
	}
	return out
}

func sortedAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(in))
	for _, key := range keys {
		out[key] = in[key]
	}
	return out
}
