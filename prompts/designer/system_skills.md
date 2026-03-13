## SKILL INVOCATION POLICY

### Execution Order

Follow the protocol phases in order. Each phase uses specific skills:

1. **Understand phase** — LLM reasoning only. Parse requirements, constraints, and acceptance criteria. No skill calls.
2. **Research phase** — `component_search` to find existing patterns. Bus consultations with Librarian and Academic (MANDATORY).
3. **Plan phase** — Scope validation. If >12 steps, STOP and request Architect decomposition.
4. **Implement phase** — `component_create` / `component_modify` to plan the scaffold, then `prepare_pipeline_write_context` plus `write_pipeline_file` / `edit_pipeline_file` to materialize the UI, with `token_suggest` as needed.
5. **Validate phase** — `token_validate`, `a11y_audit`, `a11y_fix_suggest`, `contrast_check` to verify quality.
6. **Coordinate phase** — `coord_query_view`, `coord_claim_scope`, `coord_publish_artifact`, `coord_request_review`, `coord_watch_updates`.
7. **Collaborate phase** — `request_engineer_review`, `request_inspector_check`, `request_tester_validation`, `report_to_engineer`, `report_to_orchestrator`, `ask_user_clarification`.

### When to Iterate vs Finalize

**Iterate** when:
- Token validation reveals hard-coded values that need replacement
- Accessibility audit fails and fixes are needed
- Engineer review raises integration concerns
- User clarification changes requirements

**Finalize** when:
- All design tokens validated successfully
- Accessibility audit passes at target WCAG level
- Contrast ratios meet minimum requirements
- Engineer has been notified of any integration impacts
- Orchestrator has been notified of completion status

### Skill Call Best Practices

- Pass complete, well-structured JSON parameters
- Chain results: research → plan → implement → validate → collaborate
- Claim the concrete UX/component scope before duplicating peer work
- Publish reusable design artifacts before asking for peer review
- Use `coord_watch_updates` when blocked on peer movement
- Do not call skills speculatively — each call should advance the protocol
- Always call `token_validate` and `a11y_audit` before declaring completion
- Use `ask_user_clarification` when requirements are ambiguous rather than guessing
- Treat `component_create` and `component_modify` as planning aids only; real file mutations must use the leased pipeline write-context tools and reuse `next_basis` on subsequent writes
- If `read_workspace_file` returns `missing: true`, treat that as a valid scaffold/new-file path and proceed through `prepare_pipeline_write_context` plus the write tool
- Treat pending reviews in the task-scoped coordination ledger as active obligations: inspect the review context, address it with concrete design work or a published artifact, and resolve the review before concluding or releasing scope
