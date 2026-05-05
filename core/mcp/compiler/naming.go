package compiler

import "strings"

// CanonicalToolName returns the internal, stable Sylk name for a remote MCP tool.
// Format: mcp.<server_id>.<tool_name>. Always lowercased; dots in remote names
// are preserved (the server id segment cannot contain dots).
func CanonicalToolName(serverID, remoteName string) string {
	return "mcp." + sanitizeServerID(serverID) + "." + strings.TrimSpace(remoteName)
}

// CanonicalResourceURI returns the internal stable URI for a remote MCP resource.
func CanonicalResourceURI(serverID, uri string) string {
	return "mcp://" + sanitizeServerID(serverID) + "/" + strings.TrimPrefix(uri, "/")
}

// CanonicalPromptName returns the internal stable name for a remote MCP prompt.
func CanonicalPromptName(serverID, promptName string) string {
	return "mcp.prompt." + sanitizeServerID(serverID) + "." + strings.TrimSpace(promptName)
}

// AnthropicAlias returns the flattened name some providers require when
// exporting an MCP tool: mcp_<server_id>_<tool_name>. Not used for routing —
// only as a transport adapter when the wire format demands it.
func AnthropicAlias(serverID, remoteName string) string {
	return "mcp_" + sanitizeServerID(serverID) + "_" + sanitizeForFlatAlias(remoteName)
}

// sanitizeServerID lowercases and strips characters that would break the
// canonical name format. Whitespace collapses to a single dash so a config
// typo is reflected back in the canonical form rather than silently dropped.
func sanitizeServerID(serverID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(serverID))
	if trimmed == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			b.WriteByte('-')
			continue
		}
		if r == '.' || r == '/' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeForFlatAlias removes any characters that would break the flat-alias
// regex enforced by some provider tool registries.
func sanitizeForFlatAlias(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
