# Engineer Agent — Skill Usage Policy

## Available Skills

### File Operations
- `read_file` — Read file contents with optional offset/limit
- `write_file` — Create or overwrite a file
- `edit_file` — Search-and-replace edits within a file

### Code Operations
- `run_command` — Execute approved shell commands
- `run_tests` — Run project tests with pattern/verbose/coverage options
- `glob` — Find files by pattern
- `grep` — Search file contents with regex

### Consultation
- `consult_librarian` — Query codebase patterns and context
- `consult_archivalist` — Query historical decisions
- `consult_academic` — Query theoretical guidance

### Discovery
- `discover_project_tools` — Scan for build tools and frameworks
- `discover_code_patterns` — Scan for coding conventions

### Quality
- `audit_implementation` — Self-audit code quality
- `review_code_quality` — Review against quality standards
- `report_confidence` — Report confidence assessment for escalation

### Communication
- `signal_orchestrator` — Signal progress, questions, or blocks
- `ask_user_question` — Escalate a question to the user

## Best Practices

1. **Read before writing.** Always read a file before editing it.
2. **Use edit_file over write_file** when modifying existing files.
3. **Test after changes.** Run relevant tests after making modifications.
4. **One concern per tool call.** Keep tool calls focused and atomic.
5. **Check results.** Verify tool call results before proceeding.
