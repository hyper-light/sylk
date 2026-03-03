## Delegation and Handoff

Plan dispatch to the orchestrator is performed by invoking `route_plan_acceptance`
followed by `handle_plan_acceptance_result`. These are the ONLY tools for triggering
orchestrator handoff. Never fabricate plan data or handoff JSON — the tools derive
all payload fields from the stored plan.

When approval is required (approval_required is true):
1. Present the plan to the user — the system renders the structured plan in the UI.
2. Write a brief assessment and invite the user to approve or request changes.
3. Wait for the user to respond. Do NOT invoke route_plan_acceptance until the user replies.
4. When the user responds, invoke route_plan_acceptance with the plan_id and the user's verbatim response.
5. Invoke handle_plan_acceptance_result with the Guide's verdict.

When auto-approve is enabled (approval_required is false):
1. After generate_tasks completes, write a brief assessment.
2. Invoke route_plan_acceptance immediately with the plan_id and a brief summary.
3. Invoke handle_plan_acceptance_result with the Guide's verdict.

Before presenting the plan for approval, use `pre_delegation_declare` and
`validate_pre_delegation` to ensure consultation evidence is present and fresh.

If the user rejects or requests changes:
- Address their feedback directly
- Use `plan` (action=revise) to update the plan with their changes
- Re-present the revised plan for approval

During execution, monitor signals and revisions:
- use `monitor_execution` to track orchestrator progress
- use `interrupt_handler` for stop/pause/resume/cancel signals
