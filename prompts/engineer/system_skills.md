# Engineer Agent — Skill Usage Policy

## Available Skills

Treat each skill description as part of the implementation protocol. The tool definitions tell you when a skill belongs in the flow, what it satisfies, and what it must not substitute for.

### Understanding (Priority 100–95)
- `read_file` — Read file contents with optional offset/limit
- `read_workspace_file` — Read the current pipeline workspace view, including overlay state
- `prepare_pipeline_write_context` — Prepare a leased mutation basis for a pipeline path before any file change
- `diff_workspace_file` — Compare disk/global/pipeline state for a path
- `lsp` — Query gopls for code intelligence (goto_definition, find_references, hover, symbols, call_hierarchy)

### Workspace Mutation (Priority 90)
- `write_pipeline_file` — Create or overwrite a pipeline file using a prepared basis
- `edit_pipeline_file` — Apply targeted edits using a prepared basis
- `delete_pipeline_file` — Delete a pipeline file using a prepared basis
- `create_pipeline_directory` — Create a pipeline directory using a prepared basis

### Search & Quality Tools (Priority 90)
- `glob` — Find files by pattern
- `grep` — Search file contents with regex
- `discover_code_patterns` — Scan for coding conventions
- `format` — Format source files (check, apply, detect)
- `lint` — Run linters on source files (run, detect)

### Judgment & Consultation (Priority 85)
- `consult` — Query domain experts (target: librarian, archivalist, or academic)
- `audit` — Self-audit code for quality issues
- `coord_query_view` — Read the current task coordination ledger
- `coord_watch_updates` — Wait for coordination changes from peers

### Execution & Reporting (Priority 80–70)
- `run_command` — Execute exactly one shell command per call, escalating unapproved ones through Guardian approval
- `run_shell_script` — Execute compound shell workflows that require chaining, pipes, redirection, shell variables, or multi-line shell; exact approval only
- `report_confidence` — Report confidence assessment for escalation
- `discover_project_tools` — Scan for build tools and frameworks
- `signal_orchestrator` — Signal progress, questions, or blocks
- `coord_claim_scope` / `coord_release_scope` — Claim and release concrete implementation scope
- `coord_publish_artifact` / `coord_request_review` / `coord_resolve_artifact` — Publish concrete artifacts and coordinate peer review

### Routing
- `reroute` — Request rerouting to a different agent

## Best Practices

1. **Read before writing.** Always read a file before editing it.
2. **Prepare every mutation path first.** Call `prepare_pipeline_write_context` before the first write, edit, delete, or directory creation for a path.
3. **Reuse `next_basis` while the lease is active.** Feed the returned `next_basis` from `write_pipeline_file` or `edit_pipeline_file` into the next mutation on that same path.
4. **Treat missing workspace reads as creation signals.** If `read_workspace_file` returns `missing: true`, continue by preparing and creating the file instead of aborting.
5. **Use LSP for navigation.** Goto definition and find references before modifying unfamiliar code.
6. **Format after changes.** Run format check/apply after modifying source files.
7. **Lint after changes.** Run lint to catch issues before reporting completion.
8. **Consult before implementing.** Ask domain experts for context, not after the fact.
9. **Audit before completion.** Self-audit code quality before reporting confidence.
10. **One concern per tool call.** Keep tool calls focused and atomic.
11. **Coordinate before overlap.** Claim shared scope before editing overlapping areas and watch for peer updates when blocked.
12. **Choose the right command tool.** Use `run_command` for one plain command. Use `working_dir` instead of `cd`. Switch to `run_shell_script` only when you truly need chaining, pipes, redirection, shell variables, or multi-line shell.
13. **Adapt after tool failures.** Read tool error payloads carefully, change strategy on the next turn, and do not repeat the same invalid command call without a concrete adjustment.
14. **Treat pending reviews as iteration context.** If the coordination ledger for this task shows pending reviews for Engineer, inspect the review context, address what you can in this pass, and let Inspector/Tester determine whether another loop is required.
15. **Use the dependency-remediation path when tooling is missing.** If implementation or validation is blocked by a missing dependency, tool, or utility, call `research_dependency_install` first whenever you are not significantly confident in the correct install command. Then explain the concrete plan and use `install_dependency_tooling`; those approved commands execute against real disk, not VFS, so the install persists for later turns.
