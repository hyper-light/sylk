## DIAGNOSIS METHODOLOGY

When a test fails, follow this investigation protocol. The product code is ALWAYS the first suspect.

### Investigation Protocol

1. **READ** the failing test output and stack trace
2. **READ** the product code under test
3. **TRACE** the execution path from test input to failure point
4. **CHECK** for common defect patterns:
   - Off-by-one errors in loops and slice operations
   - Nil pointer dereferences on optional values
   - Unchecked error returns (especially from I/O, parsing, type assertions)
   - Race conditions on shared state (maps, slices, struct fields)
   - Deadlocks from inconsistent lock ordering
   - Resource leaks (unclosed files, channels, connections, goroutines)
   - Boundary violations (integer overflow, empty slice, zero-length string)
   - Security flaws (unsanitized input, missing auth checks)
5. **FORM** a hypothesis about the root cause with specific file and line
6. **VERIFY** by checking additional code paths that interact with the defect
7. **Only if ALL product checks pass** — then consider test defect
8. **PRODUCE** a DiagnosisReport with the full investigation trail

### Confidence Levels

| Level | Confidence | Criteria |
|-------|-----------|----------|
| High | 0.9 - 1.0 | Root cause identified at specific line, defect pattern confirmed |
| Medium | 0.7 - 0.89 | Root cause area identified, specific line uncertain |
| Low | 0.5 - 0.69 | Multiple possible causes, more investigation needed |
| Speculative | < 0.5 | Insufficient evidence, flag for human review |

### Common False Negatives

Watch for tests that SHOULD fail but pass:
- Tests that only check error == nil (no value validation)
- Tests with no assertions beyond "didn't panic"
- Tests that test the mock, not the product code
- Tests with hardcoded expected values that happen to match a bug
