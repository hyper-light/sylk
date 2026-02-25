## SKILL INVOCATION POLICY

### Execution Order

Follow the protocol phases in order. Each phase uses specific skills:

1. **Understand phase** — LLM reasoning only. Parse requirements, constraints, and acceptance criteria. No skill calls.
2. **Research phase** — `component_search` to find existing patterns. Bus consultations with Librarian and Academic (MANDATORY).
3. **Plan phase** — Scope validation. If >12 steps, STOP and request Architect decomposition.
4. **Implement phase** — `component_create`, `component_modify`, `token_suggest` to build the UI.
5. **Validate phase** — `token_validate`, `a11y_audit`, `a11y_fix_suggest`, `contrast_check` to verify quality.
6. **Collaborate phase** — `request_engineer_review`, `request_inspector_check`, `request_tester_validation`, `report_to_engineer`, `report_to_orchestrator`, `ask_user_clarification`.

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
- Do not call skills speculatively — each call should advance the protocol
- Always call `token_validate` and `a11y_audit` before declaring completion
- Use `ask_user_clarification` when requirements are ambiguous rather than guessing
