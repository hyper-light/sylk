## SKILL INVOCATION POLICY

Treat the tool definitions as the workflow contract. Their requirements, satisfied outcomes, and avoidance guidance explain when each skill belongs in the current design path.

### Common Design Concerns

- Research and context: `component_search`, `consult_peer(target_agent_type=librarian|academic|archivalist|…, query=…)` for knowledge-agent and cross-pipeline consultations
- Planning and mutation shaping: `component_create`, `component_modify`, `token_suggest`
- Real file mutation: `workspace_read(op=prepare_write, scope=pipeline, path=…)` + `workspace_write(op ∈ {write, edit, delete, mkdir}, scope=pipeline, basis=…, …)`. For `op=edit`, each entry supplies exact `old_text` and `new_text`
- Validation: `token_validate`, `a11y_audit`, `a11y_fix_suggest`, `contrast_check`, `bash`
- Coordination and collaboration: `challenge_peer(target_activity_id=…, evidence=…)` for cross-pipeline disputes, `ask_user_clarification` for direct user questions
- Dependency remediation: `dependency(action ∈ {research, install}, …)` when blocked on missing tooling

### When to Iterate vs Finalize

**Iterate** when:
- Token validation reveals hard-coded values that need replacement
- Accessibility audit fails and fixes are needed
- Engineer review raises integration concerns
- User clarification changes requirements

**Finalize** when:
- Design tokens validate successfully
- Accessibility audit passes at the target level
- Contrast ratios meet the required threshold
- Integration impacts have been communicated to the relevant peers
- Required design artifacts or review resolutions are complete

### Skill Call Best Practices

- Pass complete, well-structured JSON parameters
- Peer updates arrive through the fabric `ambient_context` on every tool result; reach for `query_peer_activity(scope=…)` when you need a deeper read
- Do not call skills speculatively — each call should advance the requested deliverables
- Always call `token_validate` and `a11y_audit` before declaring completion
- Use `ask_user_clarification` when requirements are ambiguous rather than guessing
- Prefer a single plain validation command to `bash`. Pass a compound script only when the design task truly needs chaining, pipes, redirection, shell variables, or multi-line shell.
- When a tool fails, read the recovery guidance and change the next call instead of blindly retrying the same invalid invocation
- Treat `component_create` and `component_modify` as planning aids only; real file mutations must use `workspace_read(op=prepare_write, scope=pipeline, …)` + `workspace_write(op=…, …)` and reuse `next_basis` on subsequent writes
- If `workspace_read(op=read, …)` returns `missing: true`, treat that as a valid scaffold/new-file path and proceed through `workspace_read(op=prepare_write, …)` plus `workspace_write(op=write, …)`
- Treat pending reviews surfaced through `query_peer_activity(kinds=["review_requested","review_completed"])` as iteration context: inspect them, address what you can in this pass, and let Inspector/Tester decide whether another loop is required
