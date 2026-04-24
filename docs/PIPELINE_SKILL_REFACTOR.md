# Pipeline Agent Skill Refactor — Route Everything Through Fabric

Status: LANDED — Phases 0–6, §10.K Tier 1 (CR-2, CR-4, GT-2, GT-4/GI-5, GT-A, GT-B, GI-A, GI-B, GI-C, GI-D), the peer-dedupe subset of §10.A–K follow-ups (10.B validate_ui_compliance, 10.G per-role forest_consult, 10.K GI-1 audit, 10.K CR-1 global_review), and §10.H source-level dedupe (engineer glob/grep collapsed onto versioning Func variants; architect/engineer/librarian LSP collapsed onto shared.NewLSPSkill).

See Section 9.1 for implementation notes, Section 10 for follow-up collapses, and §10.K.7 below for the Tier 1 landing summary.
Owner: pipeline-tier
Audience: engineer, reviewer

## 1. Summary

The pipeline agents (architect, engineer, designer, tester-pipeline,
inspector-pipeline) currently ship ~110 LLM-visible skill names across
~253 per-agent registrations. A large fraction of that surface
predates the Activity Fabric and duplicates it: coordination skills
(`coord_*`) read the same state the Fabric amplifier already projects,
decision-manifest skills (`query_decisions` / `declare_decision`) route
through a parallel bus RPC when `AutoPublishDecision` already emits the
same typed activity, and designer-specific "report" / "request" skills
re-implement `consult_peer` with per-target names.

The consequence shows up in agent behavior: LLMs reach for the older
domain-shaped skill (`coord_query_view`, `query_decisions`) because
it's in their catalog next to the newer Fabric one, and the ambient
awareness model stays unused. The fix is to collapse the duplicate
surfaces onto Fabric primitives and have every peer-affecting state
change emit a typed activity.

### Target outcome

| | Before | After | Δ |
|---|---|---|---|
| Unique skill names | ~110 | ~85 | **−25 (~23%)** |
| Total per-agent registrations | 253 | 209 | **−44 (~17%)** |
| architect | 46 | 36 | −10 |
| engineer | 49 | 41 | −8 |
| designer | 47 | 36 | −11 |
| tester-pipeline | 53 | 46 | −7 |
| inspector-pipeline | 58 | 50 | −8 |

Plus behavioral changes: 13 handlers gain Fabric emissions with no
catalog change, and ~25 duplicate source registrations of
`read_file`/`glob`/`grep`/`lsp`/`run_command`/`research_dependency_install`
collapse to single shared builders.

### Non-goals

- Replacing the sovereign stores (coordination service, decision
  manifest, validation store). They stay authoritative; the Fabric
  amplifier keeps projecting their state.
- Changing the pipeline protocol state machine.
- Touching orchestrator-internal skills (`execute_dag`, `modify_dag`,
  etc.) or knowledge-agent skills. This refactor is scoped to the
  five pipeline agents.

## 2. Principles

1. **One primitive per concern.** If a read is already on the Fabric,
   remove the bus-RPC alternative. If a state change is emitted by the
   amplifier, the skill handler must call `activity.Append` directly
   and drop the RPC round-trip.
2. **Every peer-affecting action emits.** If a skill mutates state
   another agent should see, its handler must call one of the
   `AutoPublish*` helpers (or `activity.Append` for non-decision
   kinds) before returning. No silent success.
3. **No per-target skill names.** `request_engineer_review` /
   `request_inspector_check` / `request_tester_validation` all become
   `consult_peer(target_agent_type=…)`. The Fabric surface is
   polymorphic; per-target wrappers just fragment the catalog.
4. **Merge by verb, not by noun.** `coord_claim_scope` /
   `coord_release_scope` become `manage_claim(action=…)`;
   `route_plan_acceptance` / `handle_plan_acceptance_result` /
   `present_plan_approval_dialog` become
   `plan_acceptance(action=…)`. Fewer names, same capabilities.
5. **Preserve escape hatches.** `request_override`,
   `ask_user_clarification`, `self_diagnostic`, `reroute_request`,
   `interrupt_handler`, `discard_queued_artifacts`, `discard_pipeline`
   stay — no Fabric substitute exists and they are genuine
   control-plane primitives.

## 3. Inventory (ground truth)

### 3.1 Shared bundles

| Bundle | File | Installed on |
|---|---|---|
| `fabric.AwarenessSkills` | `core/fabric/awareness_skills.go:49` | all pipeline agents |
| `fabric.RecallSkills` | `core/fabric/recall_skills.go:50` | all pipeline agents |
| `fabric.InspectorAuditSkills` | `core/fabric/inspector_audit_skills.go:52` | inspector-pipeline only |
| `shared.CrossPipelineSkills` | `agents/shared/cross_pipeline_skills.go:39` | all pipeline agents |
| `shared.CoordinationSkills` | `agents/shared/pipeline_coordination.go:230` | engineer, designer, tester-pipeline, inspector-pipeline |
| `shared.DecisionManifestSkills` | `agents/shared/decision_manifest_skills.go:30` | tester-pipeline only today |
| `shared.PipelineProtocolSkills` | `agents/shared/pipeline_protocol.go:923` | engineer, designer, tester-pipeline, inspector-pipeline |
| `shared.NewGlobalReviewProtocolSkills` | `agents/shared/global_review_protocol.go:641` | architect only |
| `shared.RegisterMemoryForestSkills` | `agents/shared/memory_forest.go:76` | all pipeline agents |

### 3.2 Pipeline-agent skills by category

| Category | Skills |
|---|---|
| **Workspace I/O** (A) | `read_workspace_file`, `workspace_glob`, `workspace_grep`, `inspect_workspace_state`, `summarize_workspace_state`, `diff_workspace_file`, `list_pipeline_changes`, `prepare_pipeline_write_context`, `write_pipeline_file`, `edit_pipeline_file`, `delete_pipeline_file`, `create_pipeline_directory` |
| **Disk I/O duplicates** (B) | `read_file` (4 decls), `glob` (4), `grep` (4), `lsp` (2), `run_command` (2), `run_shell_script` (2), `ast_grep_search`, `git` |
| **Pipeline coordination** (C) | `coord_query_view`, `coord_watch_updates`, `coord_claim_scope`, `coord_release_scope`, `coord_publish_artifact`, `coord_request_review`, `coord_resolve_artifact` |
| **Decision manifest** (D) | `query_decisions`, `declare_decision` |
| **Fabric awareness** (E) | `query_peer_activity`, `causal_trace`, `find_related_activity`, `inspect_open_conflicts`, `recall_my_history`, `challenge_peer`, `consult_peer`; inspector: `inspect_open_activity` |
| **Pipeline protocol** (F) | `challenge_agent`, `handoff_next`, `validate_work`, `process_validation`, `discard_queued_artifacts`, `query_pipeline_state`; inspector: `finalize_pipeline`, `handoff_to_ot`, `discard_pipeline`; tester: `finalize_pipeline` variant |
| **Global-review protocol** (G, architect) | `handoff_next`, `validate_work`, `process_validation`, `finalize_global_review`, `accept_checkpoint`, `discard_checkpoint`, `commit_to_disk`, `query_global_review_state` |
| **Memory forest generic** (H) | `forest_resolve_intent`, `forest_recall`, `recall_recent`, `forest_predict_next_branches`, `forest_record_outcome` |
| **Memory forest per-role** (H) | `architect_forest_get_plan_precedents`, `architect_forest_compare_plan_branches`, `engineer_forest_select_implementation_branch`, `engineer_forest_get_failure_precedents`, `designer_forest_get_preference_prior`, `designer_forest_discover_adjacent_value`, `inspector_forest_get_regression_precedents`, `inspector_forest_get_validation_targets`, `tester_forest_get_test_targets`, `tester_forest_get_failure_clusters` |
| **Architect** (I) | `plan`, `plan_workflow`, `start_planning`, `plan_mode`, `interrupt_handler`, `pre_delegation_declare`, `validate_pre_delegation`, `monitor_execution`, `ask_user_question`, `route_requirements_research`, `read_research_paper`, `route_plan_acceptance`, `handle_plan_acceptance_result`, `present_plan_approval_dialog`, `consult` |
| **Engineer** (J) | `format`, `lint`, `audit`, `report_confidence`, `signal_orchestrator`, `discover_project_tools`, `discover_code_patterns`, `consult`, `research_dependency_install`, `install_dependency_tooling`, `ask_user_clarification` |
| **Designer** (K) | `component_search`, `component_create`, `component_modify`, `token_validate`, `token_suggest`, `a11y_audit`, `a11y_fix_suggest`, `contrast_check`, `request_engineer_review`, `request_inspector_check`, `request_tester_validation`, `report_to_engineer`, `report_to_orchestrator`, `ask_user_clarification`, `research_dependency_install`, `install_dependency_tooling` |
| **Tester-pipeline** (L) | `detect_test_harness`, `prepare_test_harness`, `analyze_risk`, `plan_tests`, `write_test`, `run_test_suite`, `diagnose_failure`, `research_test_tool_install`, `install_test_tooling`, `ask_user_clarification` |
| **Inspector analysis** (M) | `run_linter`, `run_type_checker`, `run_formatter_check`, `run_security_scan`, `check_coverage`, `analyze_complexity`, `detect_race_conditions`, `detect_deadlocks`, `detect_memory_leaks` |
| **Inspector design** (M) | `validate_token_usage`, `validate_accessibility`, `validate_component_api`, `validate_design_consistency` |
| **Inspector-pipeline control** (M) | `define_criteria`, `validate_criteria`, `grade_task_quality`, `request_correction`, `request_override`, `get_validation_status`, `research_dependency_install`, `install_dependency_tooling` |
| **Diagnostics** (N) | `self_diagnostic`, `reroute_request` |

## 4. Work breakdown — phased

Each phase is independently mergeable. Phase order is chosen so later
phases build on earlier ones without destabilizing agent behavior.

### Phase 0 — Shared helpers (prerequisite)

**Goal:** land the Fabric emission helpers the later phases depend on.

#### 0.1 Add `AutoPublishArtifact` / `AutoPublishReviewRequested` / `AutoPublishReviewCompleted`

- File: `agents/shared/auto_publish.go`
- Add helpers that wrap `activity.Append` for `ActionArtifactPublished`,
  `ActionReviewRequested`, `ActionReviewCompleted`. Signatures mirror
  `AutoPublishDecision` but carry artifact kind / review correlation.
- Add `AutoPublishValidationStarted` / `Accepted` / `Rejected` wrappers.
- Add `AutoPublishAdvisory` for engineer `audit` output.

#### 0.2 Add `FabricClaimHelpers`

- File: new `core/fabric/claim_skills.go`
- Expose `EmitClaimAcquired`, `EmitClaimReleased` as public helpers.
  (The orchestrator's `coordination_amplifier.go` already has private
  `emitClaimAcquired`; extract and share.)
- Keep the amplifier path working during transition.

#### 0.3 Dedupe shared skill builders

- Consolidate `read_file` / `glob` / `grep` / `lsp` /
  `run_command` / `run_shell_script` / `ast_grep_search` / `git` into
  `agents/shared/file_skills.go` + `agents/shared/shell_skills.go` +
  `agents/shared/git_skills.go`. Delete architect-local and
  engineer-local variants and the inspector-shared `read_file` /
  `glob` / `grep` in `agents/inspector/shared/skills_analysis.go:240,275,342`.
- Consolidate `research_dependency_install` + `install_dependency_tooling`
  in `agents/shared/dependency_install.go`; delete per-agent copies
  under `agents/engineer/dependency_install.go`,
  `agents/designer/dependency_install.go`,
  `agents/inspector/pipeline/tool_install.go`,
  `agents/tester/pipeline/tool_install.go` (the tester's
  `research_test_tool_install` folds into the same skill with
  `category="test"`).
- Consolidate `analyze_risk` / `plan_tests` / `run_test_suite` —
  duplicated in `agents/tester/shared/skills.go:99,141,190` and
  `agents/tester/pipeline/testing_skills.go:149,183,312`. Keep the
  pipeline variant (it's the canonical one) and have
  `agents/tester/global` import the same builder.

**Exit criteria:**
- New helpers have unit tests covering success + nil-source graceful path.
- One source of truth per deduped skill name.
- No behavior change yet; catalog size unchanged.

### Phase 1 — Remove dead weight

**Goal:** delete the 8 skills that have no Fabric-native replacement
because they duplicate functionality the ambient envelope already
delivers.

| Skill | File / line | Action |
|---|---|---|
| `coord_query_view` | `agents/shared/pipeline_coordination.go:242` | Delete function. Delete from `CoordinationSkills` slice. |
| `coord_watch_updates` | `agents/shared/pipeline_coordination.go:325` | Delete function. Delete from `CoordinationSkills` slice. Ambient envelope in `core/fabric/ambient_envelope.go:51` replaces it. |
| `query_decisions` | `agents/shared/decision_manifest_skills.go:37` | Delete function. Delete from `DecisionManifestSkills` slice. |
| `get_validation_status` | `agents/inspector/pipeline/skills.go:561` | Delete. Caller reads state via `query_peer_activity(kinds=["validation_started","validation_accepted","validation_rejected"])` plus `query_pipeline_state`. |
| ~~`query_global_review_state`~~ | `agents/shared/global_review_projection.go:75` | **Kept.** Projects protocol-lifecycle state (audit lock, required terminal action) that the fabric does not model, symmetric with `query_pipeline_state`. |
| `signal_orchestrator` | `agents/engineer/skills.go:294` | Delete. Orchestrator consumes Fabric via amplifier subscribers. For legitimate blocked-state signaling, emit `ActionRemediationOpened` from the calling handler. |
| `request_engineer_review` / `request_inspector_check` / `request_tester_validation` | `agents/designer/feedback.go:16,73,126` | Delete. Designer uses `consult_peer(target_agent_type=…)`. |
| `report_to_engineer` / `report_to_orchestrator` | `agents/designer/feedback.go:237,284` | Delete. Designer uses `handoff_next` for phase transfer. |
| `request_correction` | `agents/inspector/pipeline/skills.go:476` | Delete. Inspector uses `challenge_peer(target_activity_id=<failing artifact/decision>, evidence=…)`. |
| `challenge_agent` | `agents/shared/pipeline_protocol.go:961` | Delete. `challenge_peer` becomes the single challenge primitive; same-pipeline targeting handled inside `challenge_peer` via target activity's pipeline ID. Requires Phase 2 first. |
| `consult` | `agents/engineer/skills.go:195`, `agents/architect/skills_planning.go:45` | Delete. Both callers use `consult_peer(target_agent_type=…)`. Architect's `consult` has additional research-depth enum — promote that parameter onto `consult_peer`. |
| `ask_user_question` | `agents/architect/skills_planning.go:1107` | Delete. Architect uses `ask_user_clarification` (from `shared.BuildAskUserClarificationSkill`). |

**Cross-cutting edits:**
- Prompt updates — every agent's prompt mentions the removed skills.
  Audit `agents/*/prompt.go` + `agents/*/skillfiles/*/SKILL.md` and
  replace with Fabric equivalents.
- Tool policy manifests — remove removed skill names from
  `agents/*/tool_policy.go` and `agents/*/skills_api.go`.
- Tests — delete tests that assert removed skills are registered;
  update any integration test using removed skill names.

**Exit criteria:**
- Go build clean; no references to removed skill names outside docs
  and `git log`.
- Each pipeline agent's `VisibleByDefault` list no longer carries a
  removed name.
- Per-agent catalog drop: engineer −4, designer −7, tester-pipeline
  −2, inspector-pipeline −3, architect −4.

### Phase 2 — Merges

**Goal:** collapse 12 skills into 4 via verb-based consolidation.

#### 2.1 `manage_claim` (replaces `coord_claim_scope` + `coord_release_scope`)

- New file: `agents/shared/fabric_claim_skill.go`
- Skill name: `manage_claim`
- Params: `action ∈ {acquire, release}` + union of original params.
- Handler body: emit `ActionClaimAcquired` / `ActionClaimReleased`
  directly via `fabric.EmitClaim*`; no orchestrator bus RPC.
- Claim ID generation lives in the skill now (previously the
  coordination service minted it). Use `activity.NewActivityID()` as
  `claim_id` so the fabric activity ID and the claim ID are the same
  key.
- Delete `coord_claim_scope` (`pipeline_coordination.go:276`) and
  `coord_release_scope` (`pipeline_coordination.go:363`).

#### 2.2 `publish_work_event` (replaces `coord_publish_artifact` + `coord_request_review` + `coord_resolve_artifact`)

- New file: `agents/shared/fabric_work_event_skill.go`
- Skill name: `publish_work_event`
- Params: `kind ∈ {artifact, review_request, review_completion}` + the
  original fields, gated by kind.
- Handler emits `ActionArtifactPublished` / `ActionReviewRequested` /
  `ActionReviewCompleted` via the Phase 0 helpers.
- Delete `coord_publish_artifact` (`pipeline_coordination.go:400`),
  `coord_request_review` (`:461`), `coord_resolve_artifact` (`:498`).

#### 2.3 `plan_acceptance` (replaces `route_plan_acceptance` + `handle_plan_acceptance_result` + `present_plan_approval_dialog`)

- File: `agents/architect/skills_plan_approval.go` (replace existing).
- Skill name: `plan_acceptance`
- Params: `action ∈ {present, route, handle_result}` + original fields.
- Delete the three source skills and their tool-policy entries.

#### 2.4 `plan` absorbs `pre_delegation_declare` + `validate_pre_delegation`

- File: `agents/architect/skills.go:220` (existing `plan` skill).
- Add `action ∈ {declare_delegation, validate_delegation}` alongside
  the existing `analyze|design|generate_tasks|estimate|revise`.
- Move handler bodies from
  `agents/architect/skills_planning.go:238,491` into
  `planSkillHandlers` map.
- Delete the two standalone skills.

#### 2.5 `plan_mode` folds into `start_planning`

- Two toggles of the same lifecycle flag. Keep `start_planning`; add
  an `off` action.

#### 2.6 `academic_research` (replaces `route_requirements_research` + `read_research_paper`)

- File: `agents/architect/skills_planning.go` (refactor in place).
- Skill name: `academic_research`
- Params: `action ∈ {request, read}`.

#### 2.7 `monitor_execution` → alias removal

- File: `agents/architect/skills_planning.go:629`
- Delete the handler. Prompt guidance steers the architect to
  `query_peer_activity(scope=<pipeline-id-prefix>, kinds=[…])`.

**Exit criteria:**
- 9 fewer unique skill names (13 merged down to 4 new + folds).
- All callers/tests/prompts updated.

### Phase 3 — Fabric-native rewrites

**Goal:** keep skill names, swap handler bodies so the Fabric is the
single writer. Sovereign stores stay authoritative via the amplifier
subscribing to the fabric (or the handler double-writes through the
existing service client; final call per skill).

#### 3.1 `declare_decision` → `AutoPublishDecision`

- File: `agents/shared/decision_manifest_skills.go:72`
- Handler body becomes `AutoPublishDecision(ctx, AutoPublishInput{…})`.
  Drop the manifest-client bus path in `DecisionManifestClient.Declare`.
- The manifest store subscribes to `ActionDecisionDeclared` via a
  fabric subscriber (add
  `core/manifest/fabric_subscriber.go`) so its DB remains populated.
- Verify `agents/orchestrator/decision_manifest_*` still works: the
  amplifier in that direction becomes redundant and is deleted.

#### 3.2 `manage_claim` / `publish_work_event` (already done in Phase 2)

Phase 2 lands these as fabric-native from the start, so there's
nothing additional here except deleting the orchestrator's
`coordination_amplifier.go` emit helpers for claim/artifact/review
(the skill is now the source, amplifier becomes a store-side
subscriber that projects fabric events back into `coordination_claims`
/ `coordination_artifacts` / `coordination_reviews`).

#### 3.3 `challenge_peer` absorbs `challenge_agent`

- File: `agents/shared/cross_pipeline_skills.go:46`
- When `target_activity_id` belongs to the caller's own pipeline,
  additionally notify the protocol state machine (same behavior
  `challenge_agent` had).
- Protocol state subscribes to `ActionChallengeEmitted` via fabric
  and gates `challenge_agent`'s prior side effects.

#### 3.4 `consult_peer` absorbs `consult`

- File: `agents/shared/cross_pipeline_skills.go:121`
- Add optional `sync` + `depth` params. When `sync=true`, block the
  tool-loop on the `ActionConsultResponse` event for `deadline_seconds`.
- Migrate knowledge-agent consults (librarian/archivalist/academic)
  to be subscribers to `ActionConsultEmitted` filtered on their
  `target_agent_type`.

**Exit criteria:**
- `coord_*` round-trips to orchestrator removed; fabric is the only
  writer.
- Sovereign stores populate via fabric subscribers.
- Integration tests: claim acquire from engineer visible in inspector
  ambient context within one tool turn.

### Phase 4 — Handler emissions (no catalog change)

**Goal:** 13 handlers that mutate state peers care about must emit a
typed activity.

| Skill | File | Emission |
|---|---|---|
| `define_criteria` | `agents/inspector/pipeline/skills.go:188` | `AutoPublishDecision(Domain="success_criteria", Confidence=Committed)` once criteria persisted; treat as a charter-style decision so tester/engineer see the contract in ambient context. |
| `validate_criteria` | `agents/inspector/pipeline/skills.go:350` | Emit `ActionValidationStarted` at entry, `ActionValidationAccepted/Rejected` at exit with `Resolves` pointing at the started activity. Kinds already exist in `core/activity/action_kind.go:150-158`. |
| `grade_task_quality` | `agents/inspector/pipeline/skills.go:415` | `AutoPublishDecision(Domain="quality_grade", Value=<overall>, Confidence=Committed)` |
| `analyze_risk` | `agents/tester/pipeline/testing_skills.go:149` | `AutoPublishArtifact(Kind="risk_map", …)` via Phase 0 helper. |
| `plan_tests` | `agents/tester/pipeline/testing_skills.go:183` | `AutoPublishArtifact(Kind="test_plan", …)`. |
| `run_test_suite` | `agents/tester/pipeline/testing_skills.go:312` | Emit `ActionValidationStarted` at entry; `ActionValidationAccepted` on green, `ActionValidationRejected` on red; promote prior Tentative `test_framework` to Committed on first green. |
| `audit` | `agents/engineer/skills.go:260` | `AutoPublishAdvisory(Domain="self_audit", Value=<verdict>, Evidence=<findings>)`. |
| `report_confidence` | `agents/engineer/skills.go:333` | `AutoPublishDecision(Domain="engineer_confidence", Value=<category>, Confidence=Committed, Coordinates={"composite":"…"})`. |
| `discover_project_tools` | `agents/engineer/discovery.go:26` | `AutoPublishHint` per detected tool with domain-appropriate name (e.g., `Domain="build_backend"`). |
| `discover_code_patterns` | `agents/engineer/discovery.go:54` | `AutoPublishHint(Domain="code_convention")`. |
| `component_search` | `agents/designer/skills.go:172` | `AutoPublishHint(Domain="component_library", Value=<match.name>)` per meaningful match. |
| `component_create` | `agents/designer/skills.go:379` | `AutoPublishCommitted(Domain="component_choice", Value=<name>, Scope=<path>)`. |
| `plan` (action=generate_tasks) | `agents/architect/skills.go:398` | Emit `ActionPlanProposed` per task; when plan reaches Ready, emit `ActionPlanRatified` and a `charter_ratified` activity per high-level decision the plan implies. Kinds exist in `action_kind.go:177-186`. |

**Exit criteria:**
- Manual test: inspector defines criteria → tester's next tool result
  ambient envelope contains the criteria decision.
- Tester `analyze_risk` → engineer's next turn ambient shows the
  `risk_map` artifact.
- Engineer `discover_code_patterns` → designer sees conventions in
  ambient.
- No regressions on existing auto-publish sites (`format`, `lint`,
  `detect_test_harness`, `write_test`).

### Phase 5 — Dedupe (no catalog change)

**Goal:** collapse 25+ duplicate source registrations of shared skills.

| Skill | Current duplicate sites | Target |
|---|---|---|
| `read_file` | `agents/architect/skills_tools.go:29`, `agents/engineer/skills.go:428`, `agents/tester/pipeline/pipeline.go:235`, `agents/inspector/shared/skills_analysis.go:240` | Single builder in `agents/shared/file_skills.go` (or keep using `versioning.NewReadFileSkill` everywhere). |
| `glob` | `agents/architect/skills_tools.go:115`, `agents/engineer/skills.go:534`, `agents/inspector/shared/skills_analysis.go:275` | Same file. |
| `grep` | `agents/architect/skills_tools.go:214`, `agents/engineer/skills.go:587`, `agents/inspector/shared/skills_analysis.go:342` | Same. |
| `lsp` | `agents/architect/skills_tools.go:521`, `agents/engineer/skills.go:653` | `agents/shared/lsp_skill.go`. |
| `ast_grep_search` | `agents/architect/skills_tools.go:466` | Move to shared; engineer could use it too. |
| `git` | `agents/architect/skills_tools.go:327` | Move to shared. |
| `run_command` / `run_shell_script` | already partly centralized in `agents/shared/command_skills.go`; delete per-agent wrappers. |
| `research_dependency_install` | `agents/engineer/dependency_install.go:28`, `agents/designer/dependency_install.go:39`, `agents/inspector/pipeline/tool_install.go:40`, `agents/tester/pipeline/tool_install.go:30` (as `research_test_tool_install`) | One shared builder; tester variant parametrized. |
| `install_dependency_tooling` / `install_test_tooling` | same four sites | One shared. |
| `analyze_risk` / `plan_tests` / `run_test_suite` | `agents/tester/shared/skills.go:99,141,190` AND `agents/tester/pipeline/testing_skills.go:149,183,312` | Keep pipeline variant; global imports the builder. |
| `handoff_next` / `validate_work` / `process_validation` | `agents/shared/pipeline_protocol.go:1092,1599,1781` AND `agents/shared/global_review_protocol.go:767,1239,1358` | Single parameterized bundle; the two protocols share the skill shape and differ in routing config. |

**Exit criteria:**
- No pipeline agent directly declares one of the shared skills; each
  comes from exactly one builder.
- `rg 'skills\.NewSkill\("read_file"\)'` returns one hit.

### Phase 6 — Emission audit (safety net)

**Goal:** confirm no regression on existing fabric emissions and
catch skills that should emit but don't.

- Write `core/fabric/emission_audit_test.go`: for each skill in the
  canonical pipeline-agent catalog, assert that invoking the handler
  with a minimal valid input produces a non-empty set of fabric
  activities when the skill's declared intent is "mutating" or
  "observes external state."
- Exceptions list: diagnostic skills, interrupt handlers, user-facing
  clarifications. Maintain the list inline in the test.

**Exit criteria:**
- Test passes with the final catalog.
- Adding a mutating skill without an emission fails the test.

## 5. Per-skill migration specs (for implementers)

### 5.1 `coord_query_view` → `query_peer_activity`

**Caller migration:**
```go
// Before
view, err := coordClient.QueryView(ctx, coordination.QueryViewInput{TaskID: taskID})

// After — LLM tool call
query_peer_activity({
  "scope": taskID,
  "kinds": ["claim_acquired","artifact_published","review_requested","review_completed"],
  "since_minutes": 30
})
```

**Delete:** `agents/shared/pipeline_coordination.go:242-274`,
`CoordinationClient.QueryView` usages outside of orchestrator-internal
code, `coord_query_view` from every agent's `VisibleByDefault` list.

**Keep:** `CoordinationClient.QueryView` on the orchestrator side —
it's still used by the amplifier for post-commit state reads.

### 5.2 `declare_decision` — handler rewrite

```go
// Before: bus RPC through DecisionManifestClient.Declare
// After:
Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
    var p declareDecisionParams
    if err := json.Unmarshal(input, &p); err != nil { … }

    shared.AutoPublishDecision(ctx, shared.AutoPublishInput{
        SessionID:        sessionID(),
        AuthorAgentID:    agentID(),
        AuthorAgentType:  agentType(),
        AuthorPipelineID: pipelineID(),
        TriggerSkill:     "declare_decision",
        Domain:           p.Domain,
        Value:            p.Value,
        Scope:            p.Scope.Path(),
        Confidence:       p.Confidence.String(),
        Coordinates:      p.Scope.Coordinates(),
        Evidence:         p.Evidence,
    })
    return map[string]any{"accepted": true, "domain": p.Domain, "value": p.Value}, nil
})
```

Add `core/manifest/fabric_subscriber.go` that tails
`ActionDecisionDeclared` and writes the canonical manifest row. The
orchestrator-side `decision_manifest_amplifier.go` becomes obsolete
(it was emitting the fabric event from the manifest write; now the
direction flips).

### 5.3 `challenge_peer` absorbs `challenge_agent`

- Add internal routing: if `target_activity_id` resolves to an
  activity whose `Actor.PipelineID == cfg.PipelineID()`, additionally
  invoke the pipeline-protocol state transition
  `agents/shared/pipeline_protocol.go:issuePipelineTurnSelection` so
  the same-pipeline challenge still gates the turn.
- Alternatively: protocol state subscribes to `ActionChallengeEmitted`
  and detects same-pipeline challenges itself, removing the coupling.

### 5.4 `consult_peer` absorbs `consult` (sync mode)

- Add `sync bool` param, default false.
- Add `depth string` param (enum: `minimal|quick|standard|deep|comprehensive`).
- When `sync=true`:
  - Emit `ActionConsultEmitted` as today.
  - Block on the subscriber channel for `ActionConsultResponse` with
    `Resolves == consultID` or context deadline.
  - Return the payload in the skill result.
- Knowledge agents (librarian, archivalist, academic) gain a fabric
  subscriber that watches `ActionConsultEmitted` with
  `target_agent_type == their own` and publishes
  `ActionConsultResponse`.

### 5.5 Designer collapse

The 5 designer-specific peer-communication skills (`feedback.go`)
all become prompt guidance:

```
Instead of request_engineer_review(…) call
  consult_peer({target_agent_type: "engineer", query: …, scope: …})

Instead of report_to_engineer(…) call
  handoff_next({target_agent: "engineer", reason: …, request: …})
```

Delete `agents/designer/feedback.go` entirely, along with
`agents/designer/coordination_bus.go`'s pending-response machinery
if it's only used by the deleted skills.

### 5.6 Inspector `request_correction` → `challenge_peer`

`request_correction` today sends a JSON payload to a target agent via
the bus. Replace with a `challenge_peer` emission where
`target_activity_id` is the offending `artifact_published` or
`validation_rejected` fabric activity ID (which inspector sees in its
ambient context), and `evidence` carries the correction list as
Markdown.

### 5.7 `manage_claim` spec

```go
skills.NewSkill("manage_claim").
    EnumParam("action", "acquire|release", []string{"acquire","release"}, true).
    StringParam("scope_kind", …, true).  // when action=acquire
    StringParam("scope_key", …, true).   // when action=acquire
    StringParam("mode", "exclusive|shared|review", false).
    IntParam("lease_seconds", …, false).
    StringParam("claim_id", "Required for action=release", false).
    StringParam("resolution", "Why releasing", false).
    Handler(func(ctx, input) {
        switch action {
        case "acquire":
            id := activity.NewActivityID()
            fabric.EmitClaimAcquired(ctx, ClaimEvent{ID: id, …})
            return map[string]any{"claim_id": id, "lease_expires_at": …}, nil
        case "release":
            fabric.EmitClaimReleased(ctx, …)
            return map[string]any{"claim_id": claimID, "released": true}, nil
        }
    })
```

## 6. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| LLMs hold old skill names in system prompt cache and fail on removed names | high | Prompt updates land in the same PR as skill removal; bump prompt version; tool-use errors fall back to `unknown_skill` which the runtime translates into a hint message. |
| Sovereign store drift while direction flips from store→fabric to fabric→store | medium | Keep both directions (amplifier + subscriber) writing for one release cycle, guarded by a feature flag. Verify row counts match between the two paths before removing the amplifier. |
| Sync `consult_peer` blocks the tool loop if the responder isn't up | medium | Same deadline machinery as today's `consult`; fall back to async mode if deadline reached; surface the partial result. |
| Protocol `challenge_agent` relied on inline bus ACK for sequencing | low | Subscribe protocol state to `ActionChallengeEmitted`; acceptance test covers the same-pipeline challenge happy path. |
| Ambient envelope misses the just-emitted activity due to rate limiting (`core/fabric/ambient_envelope.go:100`) | medium | Rate limit is 5s; most tool loops have gaps >5s between turns. For deterministic tests, call `fabric.ResetAmbientRateLimitForTesting`. Consider bypass on the next turn after emission. |
| Memory forest role-specialized skills may have hidden callers | low | grep `forestRoleSkillSpecs` and usage in tool manifests; don't touch forest skills in this refactor (scope discipline). |

## 7. Verification checklist

- [ ] `go build ./...` clean.
- [ ] `go test ./agents/... ./core/fabric/... ./core/activity/...` green.
- [ ] Integration test: engineer claims scope → inspector ambient
      envelope shows claim within 2 turns.
- [ ] Integration test: tester `analyze_risk` → engineer ambient
      shows `risk_map` artifact.
- [ ] Integration test: inspector `define_criteria` → tester system
      prompt or ambient shows success criteria on next turn.
- [ ] Integration test: inspector `validate_criteria` reject →
      engineer's ambient shows `validation_rejected`.
- [ ] Prompt audit: no agent prompt references a removed skill name.
- [ ] Catalog counts per agent match Section 1 targets (run a
      one-liner that dumps `VisibleByDefault()` for each agent).
- [ ] `rg 'skills\.NewSkill\(' agents core | sort -u | wc -l` matches
      expected ~85.
- [ ] Manual smoke: run each pipeline agent with a trivial task and
      confirm ambient context populates within one turn.

## 8. Rollout sequencing

1. **PR #1 — Phase 0 helpers** (`auto_publish.go` additions, shared
   file/shell/git/dep skills, no behavior change).
2. **PR #2 — Phase 1 deletions** (removed skill names + prompt updates).
3. **PR #3 — Phase 2 merges** (`manage_claim`, `publish_work_event`,
   `plan_acceptance`, `plan` action expansions).
4. **PR #4 — Phase 3 rewrites** (`declare_decision` fabric-native,
   `challenge_peer` absorbs `challenge_agent`, `consult_peer` absorbs
   `consult`). Behind a feature flag; sovereign stores dual-written.
5. **PR #5 — Phase 4 emissions** (13 handler additions).
6. **PR #6 — Phase 5 dedupe** (code-only; no catalog change).
7. **PR #7 — Phase 6 emission audit test** + flag flip + amplifier
   deletion.

Each PR is independently reversible. PRs #4 and #7 are the only
windows with on-the-wire state; all others are source-level only.

## 9. What's explicitly NOT changing

- `core/activity` kinds (the taxonomy is sufficient).
- Pipeline protocol state machine shape
  (`agents/shared/pipeline_protocol.go` state definitions).
- Authority profiles (`core/authority`).
- Orchestrator-internal skills (`agents/orchestrator/skills*.go`).
- Knowledge-agent skill surfaces (they become consult_peer
  subscribers; their skill catalogs don't change beyond that).
- Memory Forest skills (generic + role-specialized). A separate
  refactor can audit those; out of scope here.
- `ambient_envelope` rate limit and rendering logic.

## 9.1 Implementation notes (what actually landed)

The refactor shipped slightly differently from the original spec
because two assumptions in the plan turned out to be wrong when
compared against the real code.

### Matches spec

- **Phase 0**: `AutoPublishArtifactPublished`, `AutoPublishReviewRequested`,
  `AutoPublishReviewCompleted`, `AutoPublishValidationStarted/Accepted/Rejected`,
  `AutoPublishAdvisory`, `AutoPublishClaimAcquired/Released` all landed
  in `agents/shared/auto_publish.go`.
- **Phase 1**: the 15 dead-weight removals landed. `coord_query_view`,
  `coord_watch_updates`, `query_decisions`, the 5 designer feedback
  skills, `request_correction`, `get_validation_status`,
  `signal_orchestrator`, `consult`, `ask_user_question` are all gone.
- **Phase 2**: `manage_claim`, `publish_work_event`, `plan_acceptance`,
  `delegation`, `academic_research` all landed as verb-typed primitives.
- **Phase 4**: 13 handlers (define_criteria, validate_criteria,
  grade_task_quality, analyze_risk, plan_tests, run_test_suite, audit,
  report_confidence, discover_project_tools, discover_code_patterns,
  component_search, component_create, plan) now emit typed fabric
  activities.
- **Phase 6**: `TestAutoPublishHelpers_EmitExpectedKinds` +
  `TestAutoPublishHelpers_NoOpOnMissingRequiredFields` in
  `agents/shared/auto_publish_pipeline_test.go` pin the helper contract.

### Diverged from spec — and why

- **§2.4 `plan` absorbs `pre_delegation_declare` + `validate_pre_delegation`**
  → implemented as a separate `delegation(action=declare|validate)`
  skill instead. Folding into the already-large `plan` skill would
  have bloated its input struct with 6+ delegation-only fields. The
  new `delegation` skill achieves the same 2→1 merge without polluting
  `plan`.
- **§2.5 `start_planning` + `plan_mode` merger**
  → **NOT merged.** On closer inspection these are distinct
  lifecycles: `start_planning` creates a structured DesignPlan
  (analyze→design→generate_tasks DAG), while `plan_mode` manages a
  lightweight markdown-plan-with-todos flow. They address different
  user workflows and merging them by name alone would hide real
  behavior.
- **§1 `query_global_review_state`**
  → **KEPT.** The skill projects protocol-lifecycle state (audit lock,
  required terminal action) that the fabric does not model. Symmetric
  with `query_pipeline_state` — both are legitimate internal
  control-plane primitives, not fabric duplicates.
- **§3.3 `challenge_peer` absorbs `challenge_agent`**
  → **NOT absorbed.** They are different semantic primitives:
  `challenge_agent` drives pipeline-protocol turn re-dispatch via
  `issuePipelineTurnSelection` (targets by role, respects VFS evidence
  rules, produces `ArtifactPendingChallenge`). `challenge_peer`
  records a cross-pipeline fabric dispute. One is protocol-local turn
  management; the other is cross-pipeline coordination. Keeping both
  honors the "one primitive per concern" principle.
- **§3.4 `consult_peer` sync mode absorbs `consult`**
  → Partial. `consult` was removed in Phase 1 (agents already use
  `consult_peer`). Sync-blocking mode is a future enhancement — the
  knowledge-agent fabric subscriber infrastructure needs to land first
  (they currently respond via bus, not via `ActionConsultResponse`
  emission). Out of scope for surface reduction; noted in §10.
- **§5 source dedupe**
  → Partial. Engineer's `read_file` collapsed onto
  `versioning.NewReadFileSkillFunc`. Remaining `read_file`/`glob`/`grep`
  duplicates across architect, inspector-shared, and tester-shared
  have real handler divergences (disk walks vs FileAccess vs
  fabric-backed paths) that don't merge cleanly without behavior
  changes. Logged in §10 for follow-up.

### Supporting work that landed

- **Manifest fabric subscriber** (`agents/orchestrator/decision_manifest_fabric_subscriber.go`):
  listens for `ActionDecisionDeclared` activities emitted by the
  rewritten `declare_decision` skill and upserts canonical manifest
  rows. Wired in `orchestrator.go` via `SubscribeToDefault`. Guards
  against double-persistence by skipping events tagged with
  `SourceTable = "decision_manifest"` (those are amplifier echoes).
- **`SteeringManager.EventLogger/SessionDir` nil-safety** — the
  orchestrator test harness constructs orchestrators without a bound
  steering manager; the getters now return zero values on a nil
  receiver so downstream code doesn't panic.
- **Centroid store test tolerance** — `TestCentroidStore_LargeK` used
  an absolute `1e-6` tolerance that failed at magnitude 40 because
  float32 precision is ~1.2e-7. Now scales with magnitude.

## 10. Follow-up collapses — further surface reduction

After Phase 1/2/4/6 landed, the per-agent registrations dropped from
~253 to ~209. The remaining ~209 registrations still have collapse
opportunities that don't fit the "Fabric duplicate" rubric but do
reduce the LLM catalog further. Each group below is independently
mergeable.

### 10.A `run_analyzer` — inspector analysis trio (9→1)

Currently 9 inspector skills wrap the same `toolRunner` with different
tool names:

`run_linter`, `run_type_checker`, `run_formatter_check`,
`run_security_scan`, `check_coverage`, `analyze_complexity`,
`detect_race_conditions`, `detect_deadlocks`, `detect_memory_leaks`

Proposal: single `run_analyzer(kind ∈ {lint, typecheck, format_check, security, coverage, complexity, race, deadlock, memory_leak})`.
**Saves 8 skills on inspector-pipeline.**

### 10.B `validate_ui_compliance` — design-system compliance checks (4→1)

`validate_token_usage`, `validate_accessibility`, `validate_component_api`,
`validate_design_consistency` → `validate_ui_compliance(aspect ∈ {tokens, a11y, component_api, consistency})`.
**Saves 3 skills on inspector-pipeline.**

**Important scoping note:** these four skills scan **engineer-authored
Go code** (lipgloss calls, BubbleTea component signatures, hex
literals in UI source) for design-system compliance. They are
currently registered only on inspector-pipeline, where they feed
`validate_criteria` / `grade_task_quality` against design-oriented
quality gates. The name `validate_design` would be misleading — it
implies validating the designer's output, when the actual work being
inspected is engineer-produced. Picking `validate_ui_compliance`
(or equivalent) keeps the naming honest.

**Adjacent capability expansion (optional follow-up):** since these
checks are static scans of engineer code, the engineer itself could
register the same skill for self-audit before handing off to
inspector — same pattern as `lint` / `format` / `audit` today. That
is a capability addition, not a collapse, so it doesn't change the
per-agent catalog numbers for inspector; it adds 1 skill to engineer.
Consider only if the tests show inspector is regularly sending
pipelines back to engineer specifically for token/a11y/component-API
violations that the engineer could have self-caught.

### 10.C `ui_check` — designer UI toolbelt (5→1)

`token_validate`, `token_suggest`, `a11y_audit`, `a11y_fix_suggest`,
`contrast_check` → `ui_check(aspect ∈ {tokens, a11y, contrast}, mode ∈ {audit, suggest})`.
**Saves 4 skills on designer.**

### 10.D `code_quality` — engineer code-quality trio (3→1)

`lint`, `format`, `audit` all produce verdict+issues on a code target.
Collapse into `code_quality(action ∈ {format, lint, audit}, …)`.
**Saves 2 skills on engineer.**

### 10.E Workspace verbs (12→3) — biggest catalog win

Current workspace surface per pipeline agent (engineer, designer,
tester-pipeline, inspector-pipeline):

`read_workspace_file`, `workspace_glob`, `workspace_grep`,
`inspect_workspace_state`, `summarize_workspace_state`,
`diff_workspace_file`, `list_pipeline_changes`,
`prepare_pipeline_write_context`, `write_pipeline_file`,
`edit_pipeline_file`, `delete_pipeline_file`, `create_pipeline_directory`

Collapse into three verbs:

- `workspace_read(op ∈ {read, glob, grep, inspect, summarize, diff, list_changes})` — 7 → 1
- `workspace_write(op ∈ {write, edit, delete, mkdir}, basis={…})` — 4 → 1
- `prepare_pipeline_write_context` — stays (distinct lease semantics)

**Saves 9 skills per agent × 4 agents = 36 registrations.**

### 10.F Fabric lens collapse (4→1) — careful

`query_peer_activity`, `causal_trace`, `find_related_activity`,
`inspect_open_conflicts` could collapse into
`inspect_fabric(lens ∈ {peer_activity, causal, related, conflicts})`.

**Saves 3 skills × 5 pipeline agents = 15 registrations.**

**Tradeoff:** the four distinct names currently cue the LLM on *what*
to ask for. A single lens-dispatched skill requires the LLM to learn
lens semantics from the description. Consider only if the description
is carefully shaped — otherwise risks the same "LLM reaches for older
skills" regression we just fixed.

### 10.G `forest_consult` — role-specialized forest skills (10→1)

10 role-specialized forest skills on pipeline agents
(`architect_forest_get_plan_precedents`,
`engineer_forest_select_implementation_branch`, etc.) all call
`deps.Forest.Predict/Recall/Resolve` with different family filters.
Collapse into `forest_consult(mode ∈ {plan_precedents, implementation_branch, …}, query=…)`
or derive the family set from the calling agent's identity.

**Saves 9 skills on each pipeline agent × 5 = 45 registrations** (if
all role-specialized variants are truly convenience filters with no
behavioral divergence — verify first).

### 10.H Source-level dedupes still on the table

- `read_file`, `glob`, `grep` in architect, inspector-shared,
  tester-shared: 8 source sites, same catalog shape but diverging
  handlers (disk walks vs FileAccess vs workspace-view). A clean
  swap to `versioning.New*SkillFunc` with an agent-provided
  `FileAccessProvider` is possible but needs per-agent behavior
  audit.
- `lsp` in architect + engineer: two copies, same handler shape.
- `ast_grep_search`, `git` in architect: could move to `agents/shared/`
  so engineer can use them.
- `run_command`, `run_shell_script` already centralized in
  `agents/shared/command_skills.go`; per-agent wrappers are thin.

### 10.I Total further reduction if §10.A–G land

| Group | Saves | Applies to |
|---|---|---|
| A — run_analyzer | −8 | inspector-pipeline |
| B — validate_ui_compliance | −3 | inspector-pipeline (validates engineer UI code) |
| C — ui_check | −4 | designer |
| D — code_quality | −2 | engineer |
| E — workspace verbs | −9 × 4 agents | engineer, designer, tester-pipeline, inspector-pipeline |
| F — fabric lens | −3 × 5 agents | all pipeline agents |
| G — forest_consult | −9 × 5 agents | all pipeline agents |

Net: architect 36→~26, engineer 41→~21, designer 36→~17,
tester-pipeline 46→~25, inspector-pipeline 50→~26. **Total
registrations ~209 → ~115, another ~45%.**

### 10.J Recommended rollout order

1. **E — workspace verbs** first. Biggest win, touches every pipeline
   agent equally, semantic boundary is clear (read/write/prep-lease).
2. **A + B — inspector analyzer + design-validation**. Same collapse
   pattern, net −11 on inspector alone.
3. **G — forest_consult**, after confirming per-role filters are just
   convenience (no behavioral divergence).
4. **D — code_quality**. Small but clean.
5. **C — ui_check**. Small, slightly harder because `_audit` vs
   `_suggest` represent different mental models.
6. **F — fabric lens** last. Current 4-skill surface is what's finally
   getting LLMs to reach the fabric; collapsing prematurely risks
   regressing that win. Only ship after A–E land and ambient usage
   metrics confirm LLMs are actually reaching for the fabric skills.
7. **H — source-level dedupes** in parallel — no catalog change, just
   cleanup.

### 10.K Aggressive mode — global tier + cross-tier enum-dispatched collapse

§10.A–J proposes narrow, semantically-clean merges. This section
documents a more aggressive alternative that treats any skill sharing
a mental model with siblings as mergeable via an action/aspect enum.
The upside is a much smaller catalog; the downside is denser
descriptions and more responsibility on the LLM to pick the right
enum value.

#### 10.K.1 Global Inspector — `audit` + consult absorption (−19)

- **GI-1 `audit`** (5→1): fold `validate_plan_adherence`,
  `cross_reference_changes`, `grade_layer_quality`,
  `determine_audit_depth`, `load_plan_context` into
  `audit(aspect ∈ {plan_adherence, cross_references, layer_quality, depth, context_load}, scope, …)`.
  Handlers stay; only the outer name collapses. **−4.**
- **GI-2 consult trio → `consult_peer`** (4→0): same pattern as the
  designer feedback skills removed in Phase 1.
  `consult_librarian_style`, `consult_academic_approach`,
  `consult_archivalist_context`, `request_architect_research` all
  route through `consult_peer(target_agent_type=…)`. **−4.**
- **GI-3 escalate/clarify** (3→0): `request_user_clarification` →
  shared `ask_user_clarification`; `escalate_findings` →
  `publish_work_event(kind=artifact, artifact_kind="audit_finding")`.
  **−2.**
- **GI-4 `run_analyzer`** (9→1): §10.A applied to global inspector
  plus the `read_file`/`glob`/`grep` duplicates inside
  `inspector/shared`. **−8.**
- **GI-5 `dependency`** (2→1): `research_dependency_install` +
  `install_dependency_tooling` → `dependency(action ∈ {research, install})`.
  **−1.**

Net: ~62 → ~43 (−31%).

#### 10.K.2 Global Tester — three verb skills (−13)

- **GT-1 `design_tests`** (7→1): every pre-execution phase collapses
  into one flow skill:
  `design_tests(phase ∈ {analyze_batch, analyze_integration_risks, analyze_risk, plan, harness}, level ∈ {unit, integration, e2e}, …)`.
  Folds in `analyze_batch`, `analyze_integration_risks`,
  `analyze_risk`, `plan_tests`, `plan_integration_tests`,
  `plan_e2e_tests`, `build_harness`. **−6.**
- **GT-2 `write_test(level=…)`** (3→1): `write_test` /
  `write_integration_test` / `write_e2e_test` already share
  `newGlobalTestWriteSkill` builder — the three names just re-expose
  the enum. **−2.**
- **GT-3 `test_outcome`** (5→1): post-write actions collapse into
  `test_outcome(action ∈ {run, diagnose, report, escalate}, targets? ∈ {orchestrator, architect, both}, …)`.
  Folds in `run_test_suite`, `diagnose_failure`,
  `report_to_orchestrator`, `report_to_architect`, `escalate_failure`.
  **−4.**
- **GT-4 `dependency`** (2→1): mirrors GI-5 with `category="test"`.
  **−1.**

Net: ~60 → ~47 (−22%).

#### 10.K.3 Cross-tier: `global_review`, workspace verbs, protocol actions

- **CR-1 `global_review(action=…)`** (5→1 × 3 agents, −12 regs): fold
  `finalize_global_review`, `accept_checkpoint`,
  `discard_checkpoint`, `commit_to_disk`, `query_global_review_state`
  into one. Applies to architect + global-inspector + global-tester.
- **CR-2 workspace verbs (cross-tier)** (12→3 × ~6 agents, −54 regs):
  the biggest win. `workspace_read(op ∈ {read, glob, grep, inspect, summarize, diff, list_changes}, scope ∈ {pipeline, global})` +
  `workspace_write(op ∈ {write, edit, delete, mkdir}, scope, basis)` +
  `prepare_write_context(scope)`. Replaces 12 per-agent wrappers.
- **CR-3 `review_turn(action=…, scope=…)`** (3→1 × 7 agents, −14 regs):
  `handoff_next` + `validate_work` + `process_validation` are
  duplicated across pipeline-protocol and global-review-protocol.
  Merge into one primitive with `scope ∈ {pipeline, global}`.
- **CR-4 architect `plan` absorbs `start_planning` + `plan_workflow`**
  (−2): `plan(action ∈ {start, analyze, design, generate_tasks, estimate, revise, workflow})`.
  `start_planning` returns the `plan_id`; `plan_workflow` has its own
  `type ∈ {standard, fix}` already — folds naturally.

#### 10.K.4 Aggregate target (all aggressive collapses landed)

| Agent | Before refactor | After §10.K |
|---|---|---|
| architect | 46 | ~24 (−48%) |
| engineer | 49 | ~29 (−41%) |
| designer | 47 | ~24 (−49%) |
| tester-pipeline | 53 | ~32 (−40%) |
| inspector-pipeline | 58 | ~28 (−52%) |
| global-inspector | ~62 | ~30 (−52%) |
| global-tester | ~60 | ~36 (−40%) |
| **Totals** | **~375** | **~203 (−46%)** |

#### 10.K.5 Tradeoffs of aggressive mode

Worth knowing before signing on:

1. **Enum-dispatched tools produce longer descriptions.** Each
   merged skill's system-prompt entry has to explain every
   action/aspect with its per-action required params.
2. **LLM learns action vocabularies instead of skill names.** Skill
   names cue *what to do* implicitly; action enums require the LLM
   to have internalized the description.
3. **Action-specific required params become optional-at-top.**
   `audit(aspect=plan_adherence)` needs `plan_snapshot`;
   `audit(aspect=layer_quality)` doesn't. Handler-side validation
   must return clear "`plan_snapshot` required when
   `aspect=plan_adherence`" errors — not cryptic tool-call failures.
4. **Some merges hide semantically distinct work.**
   `design_tests(phase=analyze_batch)` and
   `design_tests(phase=harness)` look like siblings but touch
   totally different subsystems. LLM that treats them uniformly may
   under-specify inputs.
5. **Existing prompts reference the old skill names.** Every merge
   requires prompt surgery across multiple markdown files.
6. **Prompt-cache invalidation.** Every skill rename invalidates the
   system prompt cache for every agent using that skill.

#### 10.K.6 Tiered rollout

**Tier 1 — land now (low risk, high win, ~−70 regs)**:
- CR-2 workspace verbs (biggest single win, touches every pipeline+global agent)
- GT-2 `write_test(level=…)` (builder already exists; pure rename)
- CR-4 architect plan folds (`start_planning`, `plan_workflow` → plan actions)
- GT-4 + GI-5 `dependency` unification across all dependency-installing agents

**Tier 2 — land after Tier 1 settles (~−35 regs)**:
- CR-1 `global_review` (architect + 2 global agents)
- GT-3 `test_outcome`
- GI-1 `audit`
- CR-3 `review_turn` (cross-protocol)

**Tier 3 — only if Tier 2 doesn't regress LLM behavior (~−15 regs)**:
- GT-1 `design_tests` (risk of action-enum confusion between
  `analyze_batch`/`analyze_risk`)
- GI-4 `run_analyzer` (risk of enum confusion among 9 analyzer kinds)

Gate Tier 3 behind ambient usage metrics: once we have the fabric
emitting `ActionToolCallCompleted` for every skill, count whether
the LLM picks the right `aspect`/`phase` vs falling back to a
neighbor. If the wrong-enum rate is under ~2%, ship Tier 3.

### 10.K.7 Tier 1 landing summary (what actually shipped)

Three of the four Tier 1 items landed in the same PR as §10.K was
authored. CR-2 is deferred as its own focused PR because it requires
unifying 12 skill builders with different parameter signatures across
6 agents — the diff would balloon if landed here, and its risk
profile is materially different from the other three.

**CR-4 architect `plan` folds (LANDED)**
- `start_planning` → `plan(action=start)` (`agents/architect/skills.go:handlePlanSkillStart`)
- `plan_workflow` → `plan(action=workflow, workflow_type=…)` (`agents/architect/skills.go:handlePlanSkillWorkflow`)
- Old skill registrations removed from `registerCoreSkills`; tool
  policy updated; tests migrated (`TestArchitect_SkillsLoaded`,
  `TestArchitect_StartPlanning_ReusesRequestScopedPlan`,
  `TestArchitect_PlanWorkflowSkillDispatch`, continuation test).

**GT-2 global tester `write_test(level=…)` (LANDED)**
- `write_test`, `write_integration_test`, `write_e2e_test` → one
  `write_test(level ∈ {unit, integration, e2e})`
  (`agents/tester/global/write_skills.go`).
- `newGlobalTestWriteSkill` helper deleted; `writeTestSkill`
  now carries the level enum directly. `normalizeGlobalTestLevel`
  handles missing/empty values as "unit" for backward compatibility.
- Old registrations dropped from `global.go`; tool policy updated;
  test suite reshaped to iterate levels instead of skill names.

**GT-4 + GI-5 `dependency(action=…)` (LANDED)**
- Added `agents/shared/dependency_management_skill.go` with
  `NewDependencyManagementSkill(cfg)` that exposes one skill
  `dependency(action ∈ {research, install})` with an optional
  `category="test"` switch for tester variants.
- Migrated seven agents: engineer, designer, inspector-pipeline,
  inspector-global, tester-pipeline, tester-global. Each replaced
  its two skill registrations (`research_*`, `install_*`) with one
  `dependencySkill(agent)` that wraps the agent's existing helper
  functions.
- Inspector-side test-tooling rejection gate
  (`InspectorRejectTestDependency*`) moved into the `ResearchHandler`
  / `InstallHandler` closures — same enforcement, earlier in the
  chain.
- Tool policies updated across all six agents; error messages in
  `inspector/shared/execution_policy.go` reworded to reference
  `dependency(action=research|install)`. Tests migrated across six
  test files.

**CR-2 workspace verbs (LANDED)**
- `core/versioning/workspace_verbs.go` adds three new verb-dispatched
  builders that delegate to the existing per-op skills at invocation
  time (no handler rewrites):
  - `NewWorkspaceReadSkill(cfg)` → `workspace_read(op ∈ {read, glob, grep, inspect, summarize, diff, list_changes}, scope ∈ {pipeline, global}, …)`.
    Absorbs 7 skills.
  - `NewWorkspaceWriteSkill(cfg)` → `workspace_write(op ∈ {write, edit, delete, mkdir}, scope, basis, …)`.
    Absorbs 4 skills.
  - `NewPrepareWriteContextSkill(getViews, defaultPipelineID, differ)` →
    `prepare_write_context(scope ∈ {pipeline, global}, path, pipeline_id?)`.
    Absorbs 2 skills.
- `WorkspaceReadSkillConfig.ReadSkillOverride` lets the tester install
  the missing-file-tolerant `NewTesterReadWorkspaceFileSkill` as the
  `read` op handler (red-phase semantics for test synthesis).
- Each of the six agents (engineer, designer, tester-pipeline,
  tester-global, inspector-pipeline, inspector-global) now registers
  only the three unified skills. Inspector-pipeline stays read-only
  — it gets `workspace_read` but not `workspace_write` or
  `prepare_write_context`.
- Internal deterministic callers (`writeGlobalTestFile`,
  `refreshGlobalWriteBasis`, `writeDeterministicTestWithTools`,
  `prepareWorkspaceWriteContexts`, `invokePipelineWriteSkill`) were
  rewritten to route through the new unified skills too, so there
  is no Loaded-vs-Visible split to maintain.
- Tests across inspector-pipeline, tester-pipeline, tester-global
  migrated — ~40 assertions swapped from old skill names to new
  op-based calls.
- **Actual reduction: −9 per agent × 6 agents = −54 registrations**,
  matching the pre-landing estimate exactly.

**GT-A + GT-B global tester (LANDED, follow-up)**
- GT-A `escalate_failure(targets=[orchestrator|architect|both], …)`
  absorbs `report_to_orchestrator` + `report_to_architect` +
  `escalate_failure`. One skill dispatches on the target list.
  (`agents/tester/global/skills.go:escalateFailureSkill`)
- GT-B `plan_tests(level=…)` absorbs `plan_integration_tests` +
  `plan_e2e_tests` + existing `plan_tests`. Dispatches on
  `level ∈ {unit, integration, e2e}`.
  (`agents/tester/global/skills.go:planTestsSkill`)
- Tool policy and tests updated.

**GI-A consult_peer absorbs global-inspector consults (LANDED, follow-up)**
- `consult_librarian_style`, `consult_academic_approach`,
  `consult_archivalist_context`, `request_architect_research`
  all removed — the shared fabric primitive
  `consult_peer(target_agent_type=…)` (already registered via
  `CrossPipelineSkills`) is the single consultation entry point.
- Inspector-specific consult builders and prompt helpers deleted
  from `agents/inspector/global/consultation_skills.go`.
- Tool policy (visible + mutating) updated to drop the four names;
  `consult_peer` arrives via `AppendFabricInspectorSkillNames`.

**GI-B request_user_clarification + escalate_findings redirects (LANDED, follow-up)**
- `request_user_clarification` replaced by the shared
  `ask_user_clarification` skill (`agents/shared/clarification_skill.go`).
- `escalate_findings` replaced by `publish_work_event(kind=artifact,
  artifact_kind="audit_finding")` from
  `agents/shared/fabric_work_event_skill.go`. The orchestrator's
  `ValidationVerdictPayload` control plane stays available for
  programmatic callers but is no longer the LLM-facing path.
- Old skill builders deleted; tool policy, conversation allowlist,
  and the inspector-specific clarification test updated.

**GI-C run_analyzer cross-inspector fold (LANDED, follow-up)**
- `run_linter`, `run_type_checker`, `run_formatter_check`,
  `run_security_scan`, `check_coverage`, `analyze_complexity`,
  `detect_race_conditions`, `detect_deadlocks`, `detect_memory_leaks`
  all collapse into
  `run_analyzer(kind ∈ {lint, typecheck, format_check, security, coverage, complexity, race, deadlock, memory_leak})`
  (`agents/inspector/shared/skills_analysis.go:RunAnalyzerSkill`).
- Applied cross-inspector: both pipeline-inspector and
  global-inspector register one `RunAnalyzerSkill` instead of the
  7-kind subset each used to declare individually.
- `contracts.go:runValidationTool` routes legacy tool names through
  `run_analyzer(kind=…)` via `legacyAnalyzerKindForTool`, so the
  deterministic validation plan + quality-gate map stay unchanged.
- `core/toolruntime/authority_filter.go` executionToolNames list
  collapsed to the single `run_analyzer` entry.

**GI-D challenge_global_agent(target=…) (LANDED, follow-up)**
- `challenge_global_tester`, `challenge_architect`,
  `challenge_orchestrator` absorbed into one skill
  `challenge_global_agent(target ∈ {global-tester, architect, orchestrator})`
  (`agents/shared/global_review_protocol.go:globalReviewChallengeGlobalAgentSkill`).
- `interAgentChallengeTargets` reads the singular `target` arg so
  the UI inter-agent event derivation still works.
- Inspector tool loop, audit prompt, protocol projection, and the
  6 affected tests updated.

#### Tier 1 aggregate landed (complete)

| Agent | Pre–Tier 1 | Post–Tier 1 | Δ |
|---|---|---|---|
| architect | 36 | ~33 | −3 (CR-4 plan folds; CR-2 N/A — architect has no workspace skills) |
| engineer | 41 | ~31 | −10 (dependency −1, CR-2 workspace verbs −9) |
| designer | 36 | ~26 | −10 (dependency −1, CR-2 workspace verbs −9) |
| tester-pipeline | 46 | ~36 | −10 (dependency −1, CR-2 workspace verbs −9) |
| inspector-pipeline | 50 | ~36 | −14 (dependency −1, CR-2 read-only subset −7, GI-C run_analyzer −6) |
| global-inspector | ~62 | ~40 | −22 (dependency −1, CR-2 workspace verbs −9, GI-A −4, GI-B −2, GI-C −6, GI-D −2 via shared proto wrapper; note: `challenge_global_agent` is 1 skill replacing 3) |
| global-tester | ~60 | ~46 | −14 (GT-2 write_test trio −2, dependency −1, CR-2 workspace verbs −9, GT-A escalate_failure −2) |
| **Total registrations** | **~331** | **~248** | **−83 (−25%)** |

Tiers 2 and 3 bring the total to ~183 (−45% cumulative from this
doc's starting ~331, or ~−51% cumulative from the pre-Phase-1
baseline of ~375).


