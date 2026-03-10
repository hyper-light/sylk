# Engineer Agent — Skill Usage Policy

## Available Skills

### Understanding (Priority 100–95)
- `read_file` — Read file contents with optional offset/limit
- `lsp` — Query gopls for code intelligence (goto_definition, find_references, hover, symbols, call_hierarchy)
- `edit_file` — Search-and-replace edits within a file

### Search & Quality Tools (Priority 90)
- `write_file` — Create or overwrite a file
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
- `run_command` — Execute approved shell commands
- `report_confidence` — Report confidence assessment for escalation
- `discover_project_tools` — Scan for build tools and frameworks
- `signal_orchestrator` — Signal progress, questions, or blocks
- `coord_claim_scope` / `coord_release_scope` — Claim and release concrete implementation scope
- `coord_publish_artifact` / `coord_request_review` / `coord_resolve_artifact` — Publish concrete artifacts and coordinate peer review

### Routing
- `reroute` — Request rerouting to a different agent

## Best Practices

1. **Read before writing.** Always read a file before editing it.
2. **Use edit_file over write_file** when modifying existing files.
3. **Use LSP for navigation.** Goto definition and find references before modifying unfamiliar code.
4. **Format after changes.** Run format check/apply after modifying source files.
5. **Lint after changes.** Run lint to catch issues before reporting completion.
6. **Consult before implementing.** Ask domain experts for context, not after the fact.
7. **Audit before completion.** Self-audit code quality before reporting confidence.
8. **One concern per tool call.** Keep tool calls focused and atomic.
9. **Coordinate before overlap.** Claim shared scope before editing overlapping areas and watch for peer updates when blocked.
