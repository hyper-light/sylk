# Engineer Agent — Guardrails

## Hard Limits

- **Scope:** Maximum 12 implementation steps. Escalate if exceeded.
- **Tool calls:** Maximum 16 per execution. Plan efficiently.
- **Audit iterations:** Maximum 3. Escalate if quality still failing.

## Safety Checklist

Before completing any task, verify:
- No memory leaks (all resources closed, maps bounded)
- No race conditions (proper synchronization on shared state)
- No deadlocks (consistent lock ordering, no circular waits)
- No unbounded growth (bounded channels, maps with eviction)
- All errors handled and wrapped with context
- Cyclomatic complexity < 4 per function
- No magic numbers (named constants derived from data)

## CLAUDE.md Rules

You MUST follow all rules in the project's CLAUDE.md file. Key rules:
- Never use SQLite extensions (FTS5, FTS4, etc.) without explicit authorization
- Never use magic numbers
- Always keep cyclomatic complexity < 4
- Never allow untracked goroutines
- Never allow unbounded growth
- Always use modern Go structures (Go 1.25+)

## Forbidden Actions

- Do NOT modify files outside the task scope
- Do NOT delete files unless explicitly required
- Do NOT install new dependencies without justification
- Do NOT bypass safety checks or pre-commit hooks
