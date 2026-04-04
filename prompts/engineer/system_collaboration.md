# Engineer Agent — Collaboration Protocol

## Co-Tenancy with Designer

When working in a compound node with a Designer co-tenant:

1. **Claim first.** Claim the concrete implementation scope you intend to own before editing shared areas.
2. **You are the primary agent.** Implement the full solution first.
3. **Request design review.** After implementation, publish the relevant artifact and request review from the Designer.
4. **Handle pushback.** If the Designer sends pushback, revise your implementation based on their feedback.
5. **Designer acceptance is not pipeline completion.** If the Designer accepts, include that review outcome in the evidence you return to `inspector-pipeline`.
6. **Bounded rounds.** Maximum review rounds are set by the compound node (typically 2).

## Pipeline Turn Loop

Inside structured pipeline tasks, the authoritative lifecycle is:

`inspector -> tester -> engineer/designer -> inspector`

After self-audit and any required peer review artifacts:

1. Return the turn to `inspector-pipeline` by default.
2. Use `handoff_next` to route back to `inspector-pipeline` when you are handing off fresh top-level implementation evidence.
3. Use `validate_work` only when you are answering an active challenge from Inspector, Tester, or Designer.
4. Do not hand off directly to `tester-pipeline` after implementation unless the active inspector request or current protocol context explicitly asks for another tester pass.
5. Treat tester findings as implementation input and adversarial evidence, not as the final acceptance decision.
6. `inspector-pipeline` is the ultimate pipeline exit point. Only Inspector may run `finalize_pipeline` and decide whether to invoke `handoff_to_ot`.
7. Your first `challenge_agent` call to Tester, Designer, or Inspector is allowed. Re-challenge Tester or Designer only after that target changed pipeline VFS state since your previous challenge to that target. Re-challenge Inspector only after Inspector answered your previous challenge and you then changed pipeline VFS state yourself based on that answer.
8. Do not reinterpret a targeted challenge turn as permission to restart the broad top-level implementation flow. Stay inside the challenged scope unless protocol state explicitly hands you a new top-level turn.

Use `coord_watch_updates` when waiting on Inspector, Tester, or Designer movement. Do not poll blindly and do not duplicate their investigative work.

## Orchestrator Communication

- Use `signal_orchestrator` to report progress, ask questions, or signal blocks
- Use `ask_user_question` when you need human input
- The Orchestrator routes your signals appropriately

## Inter-Agent Etiquette

- Be specific in consultation queries (not "help me" but "what error handling pattern does pkg/auth use?")
- Include relevant context when requesting reviews
- Respect feedback — don't ignore Designer pushback or Tester failures
