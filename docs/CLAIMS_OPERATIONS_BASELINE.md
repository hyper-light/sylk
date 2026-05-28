# Claims Operations Baseline Inventory

This note is the Phase 0 traceability inventory for
`docs/CLAIMS_OPERATIONS.md`. The executable copy lives in
`core/claims/operations_inventory.go`; tests assert it covers every
known canonical delta action and validation type.

| Requirement | Status | Package | Boundary |
|---|---|---|---|
| No untracked goroutines | partially implemented | `core/claims` | `ScopeProvider` |
| No unbounded queues | partially implemented | `core/claims` | `ParticipantRegistration` |
| No silent drops | partially implemented | `core/claims` | `ServiceDispatcher` |
| Cancellation propagation | planned | `core/claims` | cancellation graph traversal |
| Replay reconstructs state | implemented | `core/claims` | `OpenDurableBoard` |
| Bootstrap idempotency | implemented | `core/boot` | `OperationsSequencer` |
| Shutdown drains | planned | `core/claims` | shutdown drain ordering |
| Performance bounds declared | partially implemented | `core/claims` | participant metadata |
| Boot phases 0-7 | implemented | `core/boot` | `InitializePhase0`, `CommitPhase1`-`CommitPhase7` |
| Service handler dispatch | implemented | `core/claims` | `ServiceDispatcher` |
| Programmatic validator dispatch | implemented | `core/claims` | `ProgrammaticValidatorDispatcher` |
| Recovery audits | planned | `core/claims` | recovery audit worker |
| Telemetry exporters | planned | `core/claims` | telemetry exporter |
| UI observer intake | partially implemented | `ui/bridge` | `ClaimsBridge.startClaimsIntake` |
| Legacy delta compatibility | partially implemented | `core/claims` | `deltas.go` compatibility projections |

Baseline commands:

```sh
GOCACHE=/tmp/sylk-gocache go test ./core/claims ./core/boot ./agents/shared ./ui/bridge ./cmd
GOCACHE=/tmp/sylk-gocache go test -race ./core/claims ./core/boot ./agents/shared
mockery --config .mockery.yaml
```
