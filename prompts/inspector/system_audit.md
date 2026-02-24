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
