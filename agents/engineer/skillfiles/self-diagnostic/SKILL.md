---
name: engineer-self-diagnostic
description: Diagnose recent engineer failures and explain what happened, including input, attempted actions, and replay guidance. Use when the user says "you hit an error", "you crashed", "you seem stuck", or asks what failed and how to retry.
---

# Self Diagnostic

Use this skill to provide a transparent postmortem for the current or most recent failure.

## Required behavior
1. State what failed, when, and in which execution path.
2. Include the exact user-facing input and the internal action attempted.
3. Explain why it failed using available logs, traces, and error messages.
4. Provide a concrete replay plan and what should change before retrying.
5. If data is missing, state what is missing and why.
