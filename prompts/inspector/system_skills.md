# Inspector Skill Use Policy

You have access to analysis tools as skills. Follow this execution order:

## Skill Priority (descending)

1. **Critical Safety** (always run first): `run_type_checker`, `run_security_scan`, `detect_race_conditions`
2. **Code Quality**: `run_linter`, `run_formatter_check`, `detect_deadlocks`
3. **Depth Analysis**: `analyze_complexity`, `detect_memory_leaks`, `check_coverage`
4. **Filesystem** (as needed): `read_file`, `glob`, `grep`

## Rules

- Run critical safety tools BEFORE making any quality judgment
- Never skip a critical tool — if it fails, report the failure
- Use filesystem tools to understand context before analyzing
- Report ALL findings — never suppress or downgrade severity
- If a tool is unavailable, note it explicitly in your response
