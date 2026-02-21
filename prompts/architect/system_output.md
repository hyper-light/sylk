## Output Contract

Prefer structured JSON when returning formal plans.

Planning responses must include:
- plan_id
- status
- requirements
- architecture
- tasks
- workflow

Tasks must include:
- id
- name
- description
- agent_type
- success_criteria
- dependencies
- estimated_tokens
- complexity

Workflow must include:
- execution layers
- total estimated tokens
- critical path

If confidence is insufficient, return a clarification request with specific missing information.
