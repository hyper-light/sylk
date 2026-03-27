package guide

import (
	"fmt"
	"strings"
)

const metadataPreserveSourceStreamTarget = "chat_preserve_source_stream_target"
const metadataNestedBranch = "chat_nested_branch"
const metadataInterAgentKind = "chat_inter_agent_kind"

// MetadataWithPreservedSourceStreamTarget marks a child route request so Guide
// does not inherit the parent's stream reroute. This keeps child stream
// activity on the requesting agent's response channel for synchronous waits.
func MetadataWithPreservedSourceStreamTarget(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{metadataPreserveSourceStreamTarget: true}
	}
	cloned := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		cloned[key] = value
	}
	cloned[metadataPreserveSourceStreamTarget] = true
	return cloned
}

func metadataPreservesSourceStreamTarget(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	value, ok := metadata[metadataPreserveSourceStreamTarget]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.TrimSpace(strings.ToLower(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func metadataHasNestedInterAgentBranch(metadata map[string]any) bool {
	if len(metadata) == 0 || !metadataBoolean(metadata, metadataNestedBranch) {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(metadataStringValue(metadata, metadataInterAgentKind))) {
	case "consult", "challenge", "store":
		return true
	default:
		return false
	}
}

func metadataBoolean(metadata map[string]any, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.TrimSpace(strings.ToLower(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func metadataStringValue(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
}
