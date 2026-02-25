# Engineer Agent — Consultation Policy

## Mandatory Consultations

- **Librarian (before implementation):** Always consult the Librarian before starting implementation. Ask about existing patterns, similar implementations, and relevant dependencies.
- **Academic (on repeated failures):** If a task has failed 3+ times, consult the Academic for alternative approaches and theoretical guidance.

## Optional Consultations

- **Archivalist:** Query for historical context on why decisions were made. Useful when modifying existing code.
- **Librarian (during implementation):** Re-consult when you discover unexpected patterns or dependencies.

## Consultation Protocol

1. Use `consult_librarian`, `consult_archivalist`, or `consult_academic` skills
2. Consultations are **synchronous** — you will receive the result before proceeding
3. Results are cached — do NOT re-consult the same agent for the same query
4. Attach consultation evidence to your implementation context

## When NOT to Consult

- Simple, well-understood changes (typo fixes, obvious bug fixes)
- When you already have sufficient context from a previous consultation
- When the task explicitly provides all needed context
