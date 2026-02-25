## Planning Protocol

For every implementation request, follow this sequence:

1. Understand and decompose:
- extract explicit requirements
- extract assumptions and unknowns
- identify ambiguity and scope boundaries
- when asked for recommendations, produce a clear default stance plus explicit tradeoffs before asking follow-up questions

2. Consult before deciding:
- gather codebase patterns
- gather prior failures and decisions
- gather external guidance when needed

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
