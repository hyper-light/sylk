# Architect Fix Notes

## Problem

The architect planning runtime is still more rigid than the planner protocol it drives.

On March 14, 2026, the architect hit:

`tool "plan" failed: advancePlan: illegal plan state transition: analyzing -> designing`

This happened because the planner legitimately skipped the optional `consult(pre_planning)` hop and went straight from `plan(action=analyze)` to `plan(action=design)`, while the state machine still required:

`analyzing -> consulting -> designing`

## Log Evidence

From `/home/alundhe/.sylk/logs/architect_debug.log`:

- `2026-03-14 14:22:25 -05:00`: `plan(action=analyze)` for plan `e7ab9b3e-98f6-4bc9-a49d-ddf3ba0adc80`
- `2026-03-14 14:23:08 -05:00`: `plan(action=design)` for the same plan
- `2026-03-14 14:23:23 -05:00`: failure with `illegal plan state transition: analyzing -> designing`

The same log file also shows earlier architect failures from the same class of bug:

- `consulting -> consulting`
- `generating -> analyzing`
- `ready -> generating`
- `orchestrating -> generating`

## Immediate Fix Applied

The architect state machine now allows:

- `analyzing -> designing`

but still rejects the transition when unresolved clarification questions exist.

Files changed:

- `/home/alundhe/Projects/sylk/agents/architect/plan_state_machine.go`
- `/home/alundhe/Projects/sylk/agents/architect/plan_state_machine_test.go`
- `/home/alundhe/Projects/sylk/agents/architect/skills_runtime_test.go`

Verified with:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod-cache go test ./agents/architect
```

## Real Root Cause

This is the same category of problem we hit in the pipeline runtime:

- the runtime encodes a stricter semantic workflow than the agent actually follows
- optional semantic steps are modeled as mandatory state transitions
- the result is runtime deadlock/error even when the agent behavior is reasonable

## Correct Long-Term Direction

Do not treat the architect state machine as a script of semantic steps.

Instead:

- let the architect agent choose whether to `consult`, `design`, `generate_tasks`, ask the user, or stop
- keep runtime enforcement focused on objective prerequisites
- model only durable lifecycle states in the state machine

### Runtime should enforce

- requirements must exist before design
- architecture must exist before task generation
- unresolved clarification blocks forward progress
- ready/executing/completed/failed/superseded remain real lifecycle states

### Runtime should not enforce

- exact semantic sequencing of optional planning steps
- mandatory `consulting` before `designing`
- hidden workflow order assumptions that the planner can reasonably skip

## Suggested Follow-Up

Reduce the architect lifecycle to durable milestones and move semantic flexibility into prerequisite validation.

Likely direction:

- keep: `pending`, `clarifying`, `ready`, `executing`, `completed`, `failed`, `superseded`
- make `consulting`, `analyzing`, `designing`, `generating`, `orchestrating` either:
  - optional progress markers only, or
  - removable if they are no longer needed for persistence/UX

Also update the planning protocol text so `consult(pre_planning)` is described as conditional rather than mandatory.
