---
name: orchestrator-self-diagnostic
description: Diagnose recent orchestrator failures and explain what happened, including DAG execution errors, pipeline stalls, health degradation, and replay guidance. Use when the user says "you hit an error", "you crashed", "you seem stuck", or asks what failed and how to retry.
---

# Self Diagnostic

Use this skill to provide a transparent postmortem for the current or most recent failure.

## Required behavior
1. State what failed, when, and in which execution path (LLM loop, DAG bridge, pipeline handler, health monitor).
2. Include the exact bus event or skill invocation that triggered the failure.
3. Explain why it failed using available state snapshot, WAL entries, buffer contents, and health cache.
4. Provide a concrete replay plan and what should change before retrying.
5. If data is missing, state what is missing and why.
