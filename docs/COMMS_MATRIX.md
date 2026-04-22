# Peer Communication Matrix

This document is the canonical record of which agent types may address which peers via `consult_peer` and `challenge_peer`. The matrix is enforced at four layers:

1. `core/authority/profile.go` — single source of truth (per-agent `PeerConsultTargets` / `PeerChallengeTargets` / `AllowsCrossPipelineConsult`).
2. Schema enum on the `target_agent_type` parameter of both skills — populated from the caller's profile at skill-registration time.
3. Skill omission — when a role's permitted list is empty, the skill isn't registered for that role at all. The LLM's tool catalog never shows it.
4. Runtime guard in the skill handler — defense-in-depth for cached schemas, manual JSON, or future bus injection.

See `core/authority/peer_authority_test.go` for the matrix's authoritative unit-test form and `agents/shared/cross_pipeline_skills_authority_test.go` for schema/handler coverage.

## Guiding principles

- **Self-target is never permitted.** Any agent addressing its own `agent_type` is rejected regardless of list contents. The accessor `authority.PermittedConsultTargets(t)` filters out `t` itself, so a config mistake can't re-enable self-consult.
- **Knowledge agents are reactive.** `librarian`, `archivalist`, and `academic` respond to consults routed *to* them but never initiate `consult_peer` or `challenge_peer`. Empty permitted lists ⇒ the skills are omitted from their catalogs.
- **Challenge permissions are strictly tighter than consult.** Challenges cast doubt on a peer's commitment and should be harder to initiate. A role's challenge list is always a subset of its consult list (pinned by `TestChallengeTargetsAreSubsetOfConsult`).
- **Scope boundaries are respected.** Global agents (global inspector, global tester, architect, orchestrator) don't reach into per-task pipelines. Pipeline agents don't reach up into global scope through `consult_peer` — that's what `pipeline_protocol` and `global_review` are for.
- **Cross-pipeline consults are gated.** A role can only pass a `target_pipeline_id` that differs from its own when `AllowsCrossPipelineConsult=true`. Global and knowledge roles are not cross-pipeline initiators.

## Matrix

Legend: ✓ permitted, ✗ denied, — (initiator not registered for this action).

### Consult (`consult_peer`)

| Caller \ Target     | academic | architect | archivalist | designer | engineer | global-editor | guardian | guide | inspector | inspector-pipeline | librarian | orchestrator | tester | tester-global | tester-pipeline |
|---------------------|----------|-----------|-------------|----------|----------|---------------|----------|-------|-----------|---------------------|-----------|--------------|--------|---------------|------------------|
| **academic**            | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **archivalist**         | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **architect**           | ✓ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ |
| **designer**            | ✓ | ✗ | ✓ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ |
| **engineer**            | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ |
| **global-editor**       | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **guardian**            | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **guide**               | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **inspector** (global)  | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ | ✗ |
| **inspector-pipeline**  | ✓ | ✗ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✓ |
| **librarian**           | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **orchestrator**        | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ |
| **tester** (singleton)  | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ |
| **tester-global**       | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ |
| **tester-pipeline**     | ✓ | ✗ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ |

### Challenge (`challenge_peer`)

Challenge rights are strictly tighter than consult. Any — on a caller row means `challenge_peer` is not registered for that role.

| Caller \ Target     | academic | architect | archivalist | designer | engineer | global-editor | guardian | guide | inspector | inspector-pipeline | librarian | orchestrator | tester | tester-global | tester-pipeline |
|---------------------|----------|-----------|-------------|----------|----------|---------------|----------|-------|-----------|---------------------|-----------|--------------|--------|---------------|------------------|
| **academic**            | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **archivalist**         | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **architect**           | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **designer**            | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ |
| **engineer**            | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ |
| **global-editor**       | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **guardian**            | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **guide**               | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **inspector** (global)  | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✓ | ✗ |
| **inspector-pipeline**  | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| **librarian**           | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **orchestrator**        | — | — | — | — | — | — | — | — | — | — | — | — | — | — | — |
| **tester** (singleton)  | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ |
| **tester-global**       | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ |
| **tester-pipeline**     | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ |

### Cross-pipeline consult

A caller may set `target_pipeline_id` to a value different from its own pipeline only when its profile has `AllowsCrossPipelineConsult=true`.

| Role                 | AllowsCrossPipelineConsult |
|----------------------|----------------------------|
| designer             | ✓ |
| engineer             | ✓ |
| inspector-pipeline   | ✓ |
| tester-pipeline      | ✓ |
| inspector (global)   | ✗ |
| tester-global        | ✗ |
| architect            | ✗ |
| orchestrator         | ✗ |
| academic             | ✗ (reactive) |
| archivalist          | ✗ (reactive) |
| librarian            | ✗ (reactive) |
| guide                | ✗ |
| guardian             | ✗ |

## Error surface

When an agent attempts a denied hop, the handler returns a role-aware error so the LLM can pick a valid alternative on retry:

- **Reactive role initiating**:  
  `consult_peer: role "archivalist" is reactive — it does not initiate peer consults. Respond to incoming consults via the role's natural channels (knowledge queries, advisory emissions, etc.) instead`
- **Denied target with non-empty permitted list**:  
  `consult_peer: "engineer" is not permitted to consult "orchestrator". Permitted targets for "engineer": academic, archivalist, designer, inspector-pipeline, librarian, tester-pipeline`
- **Self-target**:  
  Same denied-target message, since self-targeting is simply not in the permitted list.
- **Cross-pipeline violation**:  
  `consult_peer: "inspector" is not permitted to cross-pipeline consult (own pipeline="", requested="pipeline-B"); leave target_pipeline_id empty to route to the natural same-scope peer`

## What is NOT covered by this matrix

- `pipeline_protocol(action=handoff/validate/challenge)` — pod-internal protocol. Its targets are constrained by pod membership (inspector-pipeline + engineer/designer/tester-pipeline in the same task), not the global registry. Separate routing invariants apply there; `pipeline_protocol` does not go through `consult_peer`/`challenge_peer`.
- `global_review(action=challenge/finalize/commit)` — global-scope protocol between global inspector, architect, and global tester. Not subject to this matrix.
- Handler-to-handler message flow inside an agent's consult response — once a consult has been legitimately dispatched, the target agent runs its own logic which may invoke its own outbound tools (subject to its own authority profile).
- Advisory emissions (`advisory_emitted` activities) from knowledge agents — these are one-way pushes into the fabric, not peer consults/challenges.

## Deferred: receive-side verification

Layer 4 in the original design — verifying at the target agent's message handler that the caller is actually permitted to have initiated — is currently **deferred**. The outbound defense (schema + runtime + skill omission) makes an unauthorized hop impossible from any LLM-initiated path, which covers the entire observed bug surface. A receive-side check would catch direct bus-message injection, but there is no current attack path for that. If direct injection ever becomes a concern, the hook point is each agent's consult/challenge message handler, where `authority.CanConsult(sourceType, targetType)` is called before processing and a structured error is returned on the bus on failure.

## How to add a new agent type

1. Add an entry to `profiles` in `core/authority/profile.go` with `PeerConsultTargets`, `PeerChallengeTargets`, and `AllowsCrossPipelineConsult` per role intent.
2. Update `matrix` in `core/authority/peer_authority_test.go::TestPeerAuthorityMatrix` with the expected permitted/denied targets.
3. Update the tables above.
4. If the new agent is an initiator (non-empty permitted lists), verify `CrossPipelineSkills` receives the new agent's `AgentType()` via its `CrossPipelineSkillConfig`.

## Observed bugs this matrix prevents

These are the screenshot-captured incidents that motivated the work. Each is now covered by a regression test in `core/authority/peer_authority_test.go::TestCanConsult_ReproducesObservedBugs` (and the challenge analogue).

- Global inspector issuing `consult_peer(target_agent_type="inspector")` — self-consult stalling for the 3-minute deadline.
- Global inspector issuing `consult_peer` / `challenge_peer` to `tester-pipeline` — reaching into per-task scope.
- Archivalist issuing `challenge_peer(target_agent_type="tester-pipeline", ...)` — reactive role initiating a challenge.
- Engineer issuing `consult_peer(target_agent_type="engineer", target_pipeline_id=…)` — cross-pipeline self-consult now blocked by cross-pipeline gate + self-target exclusion.
