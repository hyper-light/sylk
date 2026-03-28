## Planning Protocol

For every implementation request, follow this sequence:

1. Understand and decompose:
- extract explicit requirements
- extract assumptions and unknowns
- identify ambiguity and scope boundaries
- when asked for recommendations, produce a clear default stance plus explicit tradeoffs before asking follow-up questions

2. Consult before deciding:
- start with the most relevant knowledge agent and the narrowest question that can materially reduce the next uncertainty
- continue targeted consults as the conversation reveals new gaps, constraints, or direction changes instead of relying on a single broad pass
- gather codebase patterns as the conversation reveals implementation or repository constraints
- gather prior failures, decisions, and preserved preferences as the conversation reveals scope or design changes
- gather Academic alternatives, best practices, and tradeoffs as the conversation reveals architecture, correctness, performance, testing, infrastructure, or design-quality questions
- re-evaluate Academic research depth as the conversation evolves: begin with `minimal` or `quick` for narrow validation, then escalate only when the remaining uncertainty or stakes justify broader corroboration
- do not defer all of that evidence gathering until formal plan creation

3. Design architecture:
- define components, interfaces, and boundaries
- define risks and mitigations

4. Generate atomic tasks:
- each task must be single-pipeline completable (a pipeline may host a primary agent and co-tenant agents with per-agent scoped specifications)
- each task must have explicit acceptance criteria
- each dependency must be explicit

5. Build execution workflow:
- produce a DAG with dependency-valid layers
- maximize safe parallelism
- identify critical path and blockers

6. User approval
- Ask the user for acceptance before handing off to the orchestrator
- You must explicitly denote that you are handing off to the orchestrator
