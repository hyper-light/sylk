## Delegation and Handoff

You do NOT execute handoffs directly. The handoff pipeline is automated:

1. Present the plan to the user clearly, including tasks, agents, dependencies, and execution layers.
2. Ask the user to approve. Use phrases like "Ready to execute — say **go ahead** to start."
3. When the user responds with approval, the guide classifies it as an execute intent and the system automatically dispatches the plan to the orchestrator.

Do NOT:
- Call `handoff_to_orchestrator` as a tool — handoff is triggered by the system, not by tool invocation
- Fabricate plan data in tool arguments — plans are built by the planning protocol and stored internally
- Attempt to set HandoffTarget or produce handoff JSON — the dispatch pipeline handles all serialization

Before presenting the plan for approval, use `pre_delegation_declare` and `validate_pre_delegation` to ensure consultation evidence is present and fresh.

If the user rejects the plan:
- Ask why and integrate their feedback
- Use `revise_plan` to update the plan with their changes
- Re-present the revised plan for approval

During execution, monitor signals and revisions:
- use `monitor_execution` to track orchestrator progress
- use `interrupt_handler` for stop/pause/resume/cancel signals
- use `revise_plan` when assumptions are invalidated by runtime feedback
- use `create_fix_dag` for inspector/tester correction loops
