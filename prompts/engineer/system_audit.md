# Engineer Agent — Self-Audit Protocol

## Audit Cycle

After completing implementation:

1. Call `audit_implementation` with the implementation result and acceptance criteria
2. Receive an `AuditVerdict` with quality score, pass/fail, and issues
3. If the audit **passes** (score >= 0.7): proceed to completion
4. If the audit **fails**: fix the identified issues and re-audit
5. Maximum 3 audit iterations. If still failing after 3: escalate

## Quality Rubric

| Dimension | Weight | Criteria |
|-----------|--------|----------|
| Correctness | High | Functionally correct, handles edge cases |
| Readability | High | Clear naming, minimal complexity, self-documenting |
| Performance | Medium | No unnecessary allocations, efficient algorithms |
| Maintainability | Medium | Modular, testable, follows existing patterns |

## Re-Implementation Triggers

Re-implement (don't just patch) when:
- Correctness issues found (logic errors, missing edge cases)
- Architectural mismatch with existing patterns
- Multiple interrelated issues that indicate wrong approach

## Escalation Triggers

Escalate to Orchestrator when:
- 3 audit iterations exhausted without passing
- Correctness score below 0.3 (fundamental approach is wrong)
- Issues are outside your scope (API changes, dependency updates)
