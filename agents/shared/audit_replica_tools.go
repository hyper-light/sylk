package shared

import "github.com/adalundhe/sylk/core/providers"

// Audit-scoped tool names. These are the ONLY tools an audit replica
// runtime exposes — static, known at compile time, no dynamic skill
// loading. Keeps the LLM's tool space focused and eliminates shared
// mutable state (shared skill registry, shared tool-runtime) from
// the audit path.
const (
	// AuditToolWorkspaceRead reads a path from the replica's VFS
	// (local overlay → parent chain → disk baseline).
	AuditToolWorkspaceRead = "workspace_read"
	// AuditToolWorkspaceWrite writes content at a path in the
	// replica's VFS. Write is per-replica; does not touch other
	// replicas' overlays or disk.
	AuditToolWorkspaceWrite = "workspace_write"
	// AuditToolWorkspaceDelete shadows a path at the replica's
	// layer. Chain walks below this replica see the shadow.
	AuditToolWorkspaceDelete = "workspace_delete"
	// AuditToolMergesAfter returns merge descriptors with
	// MergedVersion > since — mid-audit awareness per §3.9.
	AuditToolMergesAfter = "merges_after"
	// AuditToolConsultPeer is retained only so defensive dispatch can
	// return a clear error for stale transcripts. It is no longer
	// exposed in audit-replica tool schemas because synchronous peer
	// routing bypasses the claims/deltas contract.
	AuditToolConsultPeer = "consult_peer"
	// AuditToolEmitDecision is the terminal tool. Calling it fires
	// the ctx-scoped finalizer and ends the audit. No other tool
	// ends the loop — the LLM MUST call this.
	AuditToolEmitDecision = "emit_audit_decision"
)

// buildAuditReplicaTools returns the static tool set exposed to the
// audit replica's LLM. Per-agent-type variations (inspector vs
// tester) use the same set; the system prompt differentiates the
// lens each role applies.
//
// includeConsult is ignored. It remains in the signature so older
// construction sites compile while Phase 7 removes the legacy sync
// peer route: audit replicas never expose consult_peer in their tool
// schema.
func buildAuditReplicaTools(agentType string, includeConsult bool) []providers.Tool {
	_ = agentType // room for per-role customization if needed later.
	_ = includeConsult
	tools := []providers.Tool{
		{
			Name:        AuditToolWorkspaceRead,
			Description: "Read the content of a file in the audit's workspace. Resolves through the replica's overlay, parent chain, then disk baseline. Returns an error when the path does not exist at any layer.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Workspace-relative path to read.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        AuditToolWorkspaceWrite,
			Description: "Write content to a path in the audit's workspace overlay. Writes land in THIS replica's copy-on-write overlay; they do not touch disk or other replicas' overlays. Used for linter/formatter output (inspector) or new test files / harness adjustments (tester). The writes become an audit-addendum merge on acceptance.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Workspace-relative path to write.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full file contents to write.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        AuditToolWorkspaceDelete,
			Description: "Shadow a path at the replica's layer. The path becomes invisible to downstream chain walks and to disk reads through this replica. Rarely needed — prefer workspace_write when the intent is 'replace' rather than 'remove'.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Workspace-relative path to shadow.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        AuditToolMergesAfter,
			Description: "List merge descriptors whose MergedVersion is strictly greater than the supplied version. Used to see concurrent / later merges that landed after this audit's context was cut, so the audit can decide whether to rebase or flag a dependency.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"since_major": map[string]any{
						"type":        "integer",
						"description": "Major component of the 'since' version. Default 0.",
					},
					"since_minor": map[string]any{
						"type":        "integer",
						"description": "Minor component of the 'since' version. Default 0.",
					},
				},
			},
		},
		{
			Name:        AuditToolEmitDecision,
			Description: "Terminate the audit and emit the final decision. Call exactly once. The coordinator receives the decision and transitions the commit queue accordingly. After this call the audit is done — further tool calls are wasted.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision": map[string]any{
						"type":        "string",
						"enum":        []string{"accepted", "rejected"},
						"description": "Terminal verdict for this audit.",
					},
					"summary": map[string]any{
						"type":        "string",
						"description": "Human-readable prose summary. Required. Used by observers (TUI, architect) and stored on the lifecycle log.",
					},
					"concerns": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Structured concern list — blocking reasons on rejected, soft warnings on accepted. Architect remediation reads these on rejection to compose the fix DAG.",
					},
				},
				"required": []string{"decision", "summary"},
			},
		},
	}
	return tools
}
