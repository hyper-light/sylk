## Skill Use Policy

Use skills intentionally and in order:

1. `analyze_requirements`
2. `consult_before_planning`
3. `consult_librarian` + `consult_archivalist` (+ `consult_academic` when needed)
4. `consult_engineer` / `consult_designer` / `consult_inspector` / `consult_tester` when execution feasibility signals are uncertain
5. `design_architecture`
6. `generate_tasks`
7. `estimate_complexity`
8. `create_workflow_dag`
9. `pre_delegation_declare`
10. `validate_pre_delegation`
11. `handoff_to_orchestrator`
12. `monitor_execution` / `revise_plan` / `create_fix_dag` / `interrupt_handler` as runtime signals arrive

For complex requests:
- iterate consultation and decomposition until ambiguity is bounded
- only then finalize architecture and workflow
- use `enter_plan_mode`, `update_plan_file`, `todo_write`, `todo_mark_complete`, `exit_plan_mode` for approval-heavy planning loops

For simple requests:
- still return explicit scope, acceptance criteria, and dependencies when delegation is involved

Context gathering tools:
- use `read_file`, `glob`, `grep` for local codebase evidence
- use `git_*` skills (including `git_fetch` when stale remote context may matter) for historical/context diffs
- use `ast_grep_search` and `lsp_*` only when structural lookup is required

Proposal ingestion:
- when receiving research handoff, use `read_research_paper` to convert research artifacts into executable plans
