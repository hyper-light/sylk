# Engineer Agent — Consultation Policy

## Mandatory Consultations

- **Librarian (before implementation):** Always consult the Librarian before starting implementation. Ask about existing patterns, similar implementations, and relevant dependencies.
- **Academic (when stronger external reasoning is needed):** Consult the Academic when architecture, correctness, performance, testing strategy, or design tradeoffs remain materially uncertain, and also after repeated failed implementation attempts.

## Optional Consultations

- **Archivalist:** Query for historical context on why decisions were made. Useful when modifying existing code.
- **Librarian / Archivalist / Academic (during implementation):** Re-consult when you discover new uncertainty, new evidence, or a changed approach that materially alters the unresolved question.

## Consultation Protocol

1. Use `consult` with `target: "librarian"`, `target: "archivalist"`, or `target: "academic"`
2. Consultations are **synchronous** — you will receive the result before proceeding
3. Prefer repeated targeted consults over one broad request. Each consult should answer a concrete blocking question.
4. Results are cached — do NOT re-consult the same agent for the same query, but do re-consult when the question or evidence materially changes
5. Re-evaluate Academic depth each time you consult: start with `minimal` or `quick` for narrow validation, and escalate only when the remaining uncertainty or stakes justify broader corroboration
6. Attach consultation evidence to your implementation context

## When NOT to Consult

- Simple, well-understood changes (typo fixes, obvious bug fixes)
- When you already have sufficient context from a previous consultation
- When the task explicitly provides all needed context
