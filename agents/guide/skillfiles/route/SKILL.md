---
name: route
description: Route input to the appropriate agent based on intent and domain analysis.
---

# Route

Route user input to the most appropriate agent based on intent classification.

## When to use
- User provides input that needs to be dispatched to a specialist agent
- Input requires domain classification before processing
- Cross-agent communication is needed
- Input is a likely follow-up and session context should be considered before switching agents

## Parameters
- `input` (required): The text to route
- `source_agent_id` (optional): ID of the requesting agent
- `session_id` (optional): Session context for routing

## Example
```json
{
  "input": "Find all functions that handle authentication",
  "session_id": "sess-123"
}
```

The Guide will classify the intent and dispatch to the Librarian for code search.
If session context has an active specialist and the prompt is ambiguous follow-up chat/help/status, prefer continuity.
