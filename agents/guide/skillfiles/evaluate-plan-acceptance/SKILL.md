---
name: evaluate_plan_acceptance
description: Evaluate whether a user approved or rejected a plan proposed by the architect.
---


# Evaluate Plan Acceptance

Evaluate whether a user approved or rejected a plan proposed by the architect and any modifications requested if so.

## When to use
- Architect sends a plan and user response that needs to be evaluated as accept/modify/reject
- Input is an architect plan and user response that requires sentiment evaluation before running or modifying
- Architect needs help understanding whether a user approved a plan


## Parameters
- `plan` (required): The plan to be accepted/rejected/modified
- `user_response` (required): The user response indicating acceptance, rejection, or partial acceptance with change notes
- `plan_id` (required): The ID of the plan
- `plan_name` (required): The name of the plan


## Example
```json
{
  "plan": "1. Create a file called SKILL.md. 2. Add frontmatter at the top",
  "plan_id": "1dfud9-39fes0-fhsoihf8-8hf94",
  "plan_name": "Create a Skill",
  "user_response": "Make it so!"
}

```

The Guide will classify the user's response as accept (pure positive intent, NO requests to change ANY of the items in the plan), modify (has requirests to modify certain items OR indicates acceptance with suggestions OR indicates acceptance on the condition of changes), or reject (negative intent, not just change requests but any statement that would indicate that the plan should not proceed). Note that rejections may ALSO contain modifications.


## Output
A structure like the following:
```json
{
  "plan": "1. Create a file called SKILL.md. 2. Add frontmatter at the top",
  "plan_id": "1dfud9-39fes0-fhsoihf8-8hf94",
  "plan_name": "Create a Skill",
  "user_response": "Make it so! But don't forget the Output section!",
  "result": "modify",
  "modifications": [
    "The user wants to ensure the Skill document has an output section"
  ]
}

```

Result is the pure accept/modify/reject classification. Modifications is a list of "things to adjust" for the architect, such that the architect can make the necessary modifications.