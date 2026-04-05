# Audit Instructions

## Reading Diffs

Diffs are provided as unified diff format. For each file:
- Lines starting with `+` are additions
- Lines starting with `-` are deletions
- Context lines have no prefix

## Cross-File Coherence Checks

1. **Interface Mismatch**: If a method signature changes in an interface, verify ALL implementors are updated
2. **Import Cycle**: If package A imports B and B imports A (directly or transitively), flag immediately
3. **Type Inconsistency**: If a type is defined in one file and used in another, verify the definition matches usage
4. **Shared State Race**: If a field is accessed from multiple goroutines, verify proper synchronization

## Whole-Plan Adversarial Checks

1. **Plan Completeness**: Verify the merged result satisfies the portion of the architect plan that should exist at this review stage. At checkpoints, future unmerged tasks are pending; at the final review, the whole plan must be present.
2. **Style Fit**: Validate naming, layering, file layout, and local implementation patterns against the rest of the repository.
3. **No Slop**: Penalize needless abstraction, verbosity, duplicated logic, decorative complexity, and generic AI-shaped code that does not fit the codebase.
4. **Alternative Comparison**: Ask whether a cleaner, more robust, or more performant implementation was available only when a concrete weakness or architectural decision point makes that comparison likely to change the verdict.
5. **Historical Preservation**: Verify the change does not repeat prior failure modes or violate previously expressed user preferences.
6. **User-Intent Protection**: If the intended behavior or tradeoffs remain unclear, do not guess. Force clarification.
7. **Pending-Work Compatibility**: Verify the current change does not block, contradict, or mis-shape pending planned work that depends on this surface.

## Plan Adherence Scoring

Score from 0.0 to 1.0:
- 1.0: Every task is implemented exactly as specified
- 0.8+: Minor deviations that don't affect correctness
- 0.5-0.8: Significant deviations or missing tasks
- Below 0.5: Major divergence from plan — escalate to architect

## Quality Grade Dimensions

- **Correctness** (30%): Does the code do what it should?
- **Robustness** (20%): Does it handle errors, edge cases, nil?
- **Performance** (15%): No unbounded growth, no unnecessary allocations?
- **Security** (20%): No injection, no leaked credentials, no unsafe operations?
- **Adherence** (15%): Does it match the plan?

## Consultation Triggers

- Consult only after direct diff, file, workspace, or tool evidence leaves a specific unanswered question.
- Use the Librarian when code style, structure, naming, or local patterns matter and nearby repository evidence does not already answer the question.
- Use the Academic when one concrete stronger implementation or plan alternative may exist and that comparison could change the verdict.
- Use the Archivalist when prior failures, prior preferences, or earlier remediation may change the verdict.
- Ask the user directly when intent is still materially ambiguous after consultation.
