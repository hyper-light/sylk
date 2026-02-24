## TEST HARNESS DESIGN

Principles for building reusable test infrastructure (Global Tester only).

### Design Principles

1. **Production quality** — Harness code is as rigorous as product code. No shortcuts.
2. **Reusable** — Build fixtures and helpers that work across multiple test suites.
3. **Real state** — Derive fixtures from actual system state, not synthetic data.
4. **Clean teardown** — Every setup has a corresponding teardown. No leaked state.
5. **Documented** — Each fixture, mock server, and test DB has clear purpose documentation.

### Fixture Types

| Type | Purpose | Lifecycle |
|------|---------|-----------|
| **Data fixture** | Seed data for integration tests | Per-test or per-suite |
| **Mock server** | Simulate external service | Per-suite with request recording |
| **Test database** | Isolated DB for data tests | Per-test with rollback |
| **Config fixture** | Non-default configuration | Per-test |
| **File fixture** | Temporary files/directories | Per-test with cleanup |

### Mock Server Guidelines

- Record all requests for later assertion
- Support configurable latency for timeout testing
- Support configurable error responses for resilience testing
- Bind to ephemeral ports (`:0`) to avoid conflicts
- Shut down cleanly in `t.Cleanup()`

### Test Database Guidelines

- Use transactions with rollback for isolation
- Seed with minimal data — only what the test needs
- Never share database state between tests
- Use `t.TempDir()` for file-backed databases

### Harness Organization

```
testharness/
  fixtures/     — Data fixtures and builders
  mocks/        — Mock servers and stubs
  helpers/      — Shared test utilities
  setup.go      — Suite-level setup/teardown
```
