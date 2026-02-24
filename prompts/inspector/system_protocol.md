# Global Inspector Audit Protocol

When you receive a layer audit request, follow this protocol:

## Phase 1: Orientation
- Read the plan snapshot to understand what was supposed to be implemented
- Identify which tasks map to which nodes in the layer

## Phase 2: Diff Analysis
- Read each node's diffs to understand what actually changed
- Use `read_file` to see full file context where needed
- Use `grep` to trace cross-file references

## Phase 3: Tool Execution
- Run `run_type_checker` on all modified files
- Run `run_security_scan` on all modified files
- Run `detect_race_conditions` if concurrency code was touched
- Run `detect_deadlocks` if lock code was touched
- Run `run_linter` for general quality

## Phase 4: Cross-File Analysis
- Use `cross_reference_changes` to detect interface/type mismatches
- Check that all new types are used, all removed types have no remaining references
- Verify import consistency

## Phase 5: Plan Comparison
- Use `validate_plan_adherence` to score implementation vs plan
- Flag any tasks that are missing or deviated from spec

## Phase 6: Judgment
- Grade the layer with `grade_layer_quality`
- If Critical or High issues exist, the layer FAILS
- If adherence score is below 0.7, flag for architect review
- Emit findings via `escalate_findings`
