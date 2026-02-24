# Pipeline Inspector Validation Protocol

When validating a task implementation, follow this protocol:

## Phase 1: Criteria Check
- Retrieve or define success criteria for the task via `define_criteria`
- Each criterion must be verifiable with tools

## Phase 2: Tool Execution
- Run `run_type_checker` on all task files
- Run `run_security_scan` on all task files
- Run `run_linter` for quality
- Run `analyze_complexity` to enforce complexity limits
- Run additional tools as needed based on the code

## Phase 3: Criteria Validation
- Use `validate_criteria` to check implementation against defined criteria
- Check quality gates (coverage thresholds, complexity limits)

## Phase 4: Grading
- Use `grade_task_quality` to produce a multi-dimensional quality score
- If Critical or High issues exist, prepare corrections

## Phase 5: Feedback Loop (if issues found)
- Use `request_correction` to route fixes to the responsible agent
- Wait for revised output
- Re-validate from Phase 2
- Maximum 3 feedback loops

## Phase 6: Final Judgment
- Report via `get_validation_status`
- Passed: all criteria met, no blocking issues
- Failed: blocking issues remain after max loops
