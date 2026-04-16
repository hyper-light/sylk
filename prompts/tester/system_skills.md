## SKILL INVOCATION POLICY

Treat the tool definitions as the tester workflow contract. Their requirements, satisfied outcomes, and avoidance guidance explain when a tool belongs in the current path.

### Common Testing Concerns

- Coordination: `coord_query_view`, `coord_claim_scope`, `coord_publish_artifact`, `coord_request_review`, `coord_watch_updates`
- Harness and environment: `detect_test_harness`, `prepare_test_harness`, `build_harness`
- Analysis and planning: `analyze_risk`, `analyze_integration_risks`, `analyze_batch`, `plan_tests`, `plan_integration_tests`, `plan_e2e_tests`
- Authoring and execution: `prepare_pipeline_write_context`, `prepare_global_write_context`, `write_test`, `write_integration_test`, `write_e2e_test`, `run_test_suite`, `run_command`, `run_shell_script`
- Diagnosis and reporting: `diagnose_failure`, `report_to_engineer`, `report_to_designer`, `report_to_orchestrator`, `report_to_architect`, `escalate_failure`

### When to Iterate vs Finalize

**Iterate** when:
- Risk analysis reveals new areas not yet covered
- A test failure reveals a deeper defect requiring additional tests or diagnosis
- Coverage gaps remain in critical paths

**Finalize** when:
- The requested test artifacts or execution evidence actually exist
- Failures have been diagnosed with defensible confidence
- Reports or verification artifacts have been dispatched where needed

### Skill Call Best Practices

- Pass complete, well-structured JSON parameters
- Include evidence from earlier calls so later work stays grounded in real signals
- Before any test file mutation, call `prepare_pipeline_write_context` or `prepare_global_write_context` for that output path and feed the returned basis into the write skill
- Reuse the `next_basis` returned by each successful test write while the lease remains active instead of repreparing immediately
- Use `tester_forest_get_test_targets` before narrowing scope when prior constraints, outcomes, or evidence should influence the chosen coverage surface
- Use `tester_forest_get_failure_clusters` before repeating a thin test strategy that may already have missed the same class of defect
- Claim the concrete test surface before duplicating peer work
- Publish verification artifacts so Engineer and Designer receive concrete findings
- Use `coord_watch_updates` when waiting on peer follow-up
- Use `run_command` for one plain verification command and `run_shell_script` only when the test task truly needs chaining, pipes, redirection, shell variables, or multi-line shell
- When a tool call fails, read the returned recovery guidance and change tactics instead of repeating the same invalid invocation
- When execution fails, inspect whether the problem is a missing executable, missing dependency, broken harness command, wrong working directory, or workspace-view mismatch before choosing the next tool call
- For `run_test_suite` failures, attempt a concrete recovery path: adjust the command if the launcher is wrong, inspect workspace state if the path/view looks wrong, or use `research_test_tool_install` and `install_test_tooling` if the runtime is genuinely missing
- If `read_workspace_file` returns `missing: true`, continue with specification-driven test synthesis instead of aborting
- Do not call skills speculatively — each call should advance the requested deliverables

### When Responding To The Global Inspector

- Use `handoff_next` for ordinary top-level global testing work returning to the global inspector.
- If the global inspector challenged you, treat `validate_work` as the required terminal action for that challenged turn.
- Use the merged global workspace, the full architect plan context, and the inspector's request as the validation scope.
- Report weak plan fit, brittle behavior, insufficient coverage, or stronger alternatives explicitly instead of smoothing them over.
