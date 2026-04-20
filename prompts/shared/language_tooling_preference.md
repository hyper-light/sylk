## LANGUAGE, FRAMEWORK, AND TOOLING SELECTION

When you choose a language, framework, library, file placement, build invocation, test runner, dependency, or naming convention, three signals inform you. Apply them per axis — none of them is exclusionary, each one carries weight on the dimension where it speaks most clearly.

### Signals, in priority order

1. **The task specification (strongest on what to build).**
   The planner's structured task spec is authoritative for the deliverable's identity: language choice, framework choice, library choice, public surface, behavioral acceptance criteria. Read `task_intent`, `worker_packets`, `acceptance_criteria`, `test_requirements`, `workspace.{read_set,write_set,test_surface}`, and `affected_files`. When the task says "Go service" or "Python migration", honor it even when the surrounding codebase is built in a different stack.

2. **Existing work in the Activity Fabric (strongest on in-flight conventions).**
   Before inventing a parallel choice, read peer activity for the same surface (`query_peer_activity`, `find_related_activity`, ambient context envelopes, peer artifacts, decisions). If a peer agent has already chosen a framework, output path, naming pattern, or library for adjacent work, match what is already in flight rather than introducing a divergent variant.

3. **The existing codebase (strongest on placement and integration).**
   The project's primary language, build system, directory layout, package conventions, release flow, and lint/format rules tell you where new work belongs and how it has to integrate. Use these to situate your work — even when the task introduces a new language to a previously single-language codebase.

### Resolving ambiguity

- **No existing work in the Fabric for this surface** → treat the codebase's existing language and tooling as the second-strongest signal.
- **No existing codebase (greenfield)** → favor the task spec and any in-flight peer work even more strongly; do not invent conventions ahead of the task.
- **The task itself is unclear on language or tooling preferences** → consult whichever knowledge agent fits the gap (`librarian` for codebase patterns, `archivalist` for historical context and prior decisions, `academic` for theoretical guidance and tradeoff analysis); you may consult one, two, or all three depending on what the gap actually is. If a clarification skill is available to your agent (e.g., `ask_user_clarification`, `request_user_clarification`, `ask_user_question`), feel free to use it to get explicit direction from the user. When neither path is available directly, surface the ambiguity in your report or via `challenge_agent` so it routes to an agent that can resolve it.

### Non-exclusion principle

These signals operate on different axes. The task spec wins on **what** is being built; the Fabric wins on **how peers have already chosen to build it**; the codebase wins on **where it fits and how it integrates**. Adding a Python component to a Go project means honoring the task's "Python" decision (axis 1), matching any in-flight Python conventions peers have already established (axis 2), and still situating the Python module within the Go project's directory layout, build orchestration, and release flow (axis 3). Do not let one signal override another on an axis it does not speak to.

### Apply this when writing

A `.py` file must contain Python; a `.go` file must contain Go. Match file extension, package/module declarations, import syntax, and idiomatic structure to the language the task and the file path agree on. If your inferred language disagrees with the task spec or the existing files at the target path, stop and reconcile before writing — re-read the planner's structured fields, check Fabric for prior decisions on this surface, and consult or clarify rather than commit a mismatched write.
