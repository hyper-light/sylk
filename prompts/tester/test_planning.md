## TEST PLANNING STRATEGY

### Plan Formulation Process

1. **Understand the specification** — Read the task spec. Know what SHOULD happen.
2. **Identify risk areas** — Where are defects most likely? Focus testing effort there.
3. **Design failure hypotheses** — For each test, state: "This test will fail if [specific defect]."
4. **Choose input strategies** — Select inputs that exercise risk areas and boundaries.
5. **Quality over quantity** — 5 precise tests beat 50 shallow ones.

### Input Strategy Selection

| Strategy | When to Use | Example |
|----------|-------------|---------|
| **Boundary** | Numeric limits, slice capacity | 0, 1, max-1, max, max+1 |
| **Equivalence** | Input partitioning | One valid, one invalid per class |
| **Error path** | Error handling code | nil input, timeout, permission denied |
| **Concurrent** | Shared state | Multiple goroutines racing on same resource |
| **Fuzz** | Complex parsing | Random bytes, malformed JSON, unicode edge cases |
| **State machine** | Stateful code | Invalid state transitions, repeated operations |

### Risk-Based Prioritization

Test the highest-risk areas first:

1. **Critical risk:** Concurrency + shared state, security boundaries, data corruption paths
2. **High risk:** Error handling, resource management, API contracts
3. **Medium risk:** Business logic correctness, input validation, edge cases
4. **Low risk:** Formatting, logging, documentation

### Test Design Principles

- **Isolated:** Each test runs independently. No shared state between tests.
- **Deterministic:** Same inputs always produce same result. No timing dependencies.
- **Fast:** Individual tests complete in milliseconds. Use parallel execution.
- **Named for intent:** `TestTokenRefresh_ExpiredToken_ReturnsNewToken` — not `TestRefresh1`.
- **One assertion per concept:** Test one behavior. If it fails, you know exactly why.
