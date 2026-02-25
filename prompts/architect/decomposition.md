Decompose the following architecture into atomic, agent-executable tasks.

Architecture:
%s

Each task must be a hyper-specific, self-contained work item — equivalent to a well-written Jira ticket. An executing agent must be able to complete the task using ONLY the information in the task specification, with no follow-up questions or external context.

For each task, provide:

1. **Identity**: ID, name (imperative verb phrase), agent assignment
1b. **Co-Tenancy Classification**: If the task involves both visual/UX and implementation concerns, identify the primary agent and co-agents. Provide per-agent scoped specifications:
   - Each agent gets its own acceptance criteria (what THEY must deliver)
   - Each agent gets its own implementation guide (HOW they should work)
   - Each agent gets its own affected files (WHICH files they touch)
   - The primary agent acts first; co-agents receive the primary's output as context
   - Do NOT duplicate responsibilities — if the designer handles layout, the engineer should NOT re-implement layout
2. **Description**: Detailed explanation of what to build, why, and how it fits the architecture. Reference specific types, functions, and patterns.
3. **Acceptance Criteria**: Given/When/Then conditions that define "done". Each criterion must be independently verifiable. Include at least 2 "must" priority criteria.
4. **Implementation Guide**: Step-by-step instructions covering the implementation sequence, integration points, error handling approach, and key design decisions.
5. **Guidelines**: Implementation constraints — naming conventions, patterns to follow, patterns to avoid, performance requirements.
6. **Examples**: Code snippets showing the expected pattern or API shape (for non-trivial tasks).
7. **Affected Files**: Every file that must be created, modified, or deleted, with a reason.
8. **Test Requirements**: Specific test cases that must pass for the task to be accepted.
9. **Risk Factors**: Potential blockers, failure modes, or tricky edge cases.
10. **Dependencies**: Explicit task IDs that must complete before this task can start.
11. **Complexity and Token Estimate**: Calibrated estimate for agent budget planning.

Quality bar: An engineer unfamiliar with the codebase should be able to implement the task correctly from the specification alone.
