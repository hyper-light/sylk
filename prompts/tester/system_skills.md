## SKILL INVOCATION POLICY

### Execution Order

Follow the protocol phases in order. Each phase uses specific skills:

1. **Gate phase** — `check_inspector_gate` (ALWAYS first)
2. **Analysis phase** — `analyze_risk` or `analyze_integration_risks` / `analyze_batch`
3. **Planning phase** — `plan_tests` or `plan_integration_tests` / `plan_e2e_tests`
4. **Build phase** — `build_harness` (Global Tester only)
5. **Implementation phase** — `write_test` or `write_integration_test` / `write_e2e_test`
6. **Execution phase** — `run_test_suite`
7. **Diagnosis phase** — `diagnose_failure` (only on failure)
8. **Reporting phase** — `report_to_engineer` / `report_to_orchestrator` / `escalate_failure`

### When to Iterate vs Finalize

**Iterate** when:
- Risk analysis reveals new areas not yet covered
- A test failure reveals a deeper defect requiring additional tests
- Coverage gaps remain in critical code paths

**Finalize** when:
- All planned test cases have been implemented and executed
- All failures have been diagnosed with high confidence
- Reports have been dispatched to appropriate agents

### Skill Call Best Practices

- Pass complete, well-structured JSON parameters
- Include context from previous skill results (risk areas inform test plans)
- Chain results: risk analysis → test plan → implementation → execution → diagnosis
- Do not call skills speculatively — each call should advance the protocol
