## SKILL INVOCATION POLICY

Treat the tool definitions as the workflow contract. Their requirements, satisfied outcomes, and avoidance guidance explain when each skill belongs in the current design path.

### Common Design Concerns

- Research and context: `component_search`, consultations, `coord_query_view`, `coord_claim_scope`
- Planning and mutation shaping: `component_create`, `component_modify`, `token_suggest`
- Real file mutation: `prepare_pipeline_write_context`, `write_pipeline_file`, `edit_pipeline_file`
- Validation: `token_validate`, `a11y_audit`, `a11y_fix_suggest`, `contrast_check`
- Coordination and collaboration: `coord_publish_artifact`, `coord_request_review`, `coord_watch_updates`, `request_engineer_review`, `request_inspector_check`, `request_tester_validation`, `report_to_engineer`, `report_to_orchestrator`, `ask_user_clarification`

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
- Claim the concrete UX/component scope before duplicating peer work
- Publish reusable design artifacts before asking for peer review
- Use `coord_watch_updates` when blocked on peer movement
- Do not call skills speculatively — each call should advance the requested deliverables
- Always call `token_validate` and `a11y_audit` before declaring completion
- Use `ask_user_clarification` when requirements are ambiguous rather than guessing
- Treat `component_create` and `component_modify` as planning aids only; real file mutations must use the leased pipeline write-context tools and reuse `next_basis` on subsequent writes
- If `read_workspace_file` returns `missing: true`, treat that as a valid scaffold/new-file path and proceed through `prepare_pipeline_write_context` plus the write tool
- Treat pending reviews in the task-scoped coordination ledger as iteration context: inspect them, address what you can in this pass, and let Inspector/Tester decide whether another loop is required
