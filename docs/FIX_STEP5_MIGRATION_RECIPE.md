# Step 5 Migration Recipe: Attaching Typed Identity at Dispatch Sites

Companion to `docs/FIX_ID_AND_TOKENS.md`. Steps 1–4 established the
typed identity surface and made the gateway *require* it. The runtime
will not issue an LLM call until every dispatch site has been migrated
to attach `identity.WithIdentity(ctx, id)` and `identity.WithTask(ctx, task)`
before entering the provider.

## Shape of the migration

Every migration touches three layers per agent:

1. **Construction** — the agent owns its `*identity.AgentIdentity` for its
   lifetime. Mint once at `New()` via `identity.Factory.Mint` (or
   `MintReplica` for forwarded replicas). Store on the agent struct.
2. **Request entry** — at the boundary where a request enters the agent
   (bus handler, tool-loop entry, request-channel subscriber), derive
   a `*identity.TaskRef` via `Factory.NewTask` / `Factory.SubTask` /
   `Factory.SystemTask` using the request's correlation id. Attach
   *both* identity and task to the request-scoped ctx.
3. **Dispatch** — any `provider.{Complete, Stream, StreamWithHandler}`
   call receives the ctx with both values already bound.

## Bootstrap requirements (not yet landed)

`cmd/tui.go` needs:

```go
models := registries.NewHandoffModelRegistry(phase1.descriptors)
pods := registries.NewStaticPodRegistry(podIDTypeMap, "pipeline")

factory, err := identity.NewFactory(identity.FactoryConfig{
    Namespace: identity.Namespace(sessionID),
    Models:    models,
    Pods:      pods,
})

acc, err := accounting.New(accounting.Config{
    Namespace: identity.Namespace(sessionID),
    WAL:       accountingWAL, // .sylk/sessions/{id}/accounting/wal.jsonl
})

accHook := accounting.NewHook(acc, slog.Default())
activityHook := providers.NewLLMEventPublisherHook(
    providers.NewLLMEventPublisher(phase1.activityPub))
multi := providers.NewMultiHook(accHook, activityHook)

phase1.googleGateway.SetEventHook(multi)
phase1.anthropicGateway.SetEventHook(multi)
phase1.openaiGateway.SetEventHook(multi)

phase1.factory = factory
phase1.accountant = acc
```

Each agent's `Config` grows a `Factory *identity.Factory` field. Wire
it through `registerAgentCreators` and `bootstrapLiveGuide`,
`bootstrapArchitect`, etc.

## Per-agent recipe

### Step 5a — every agent's `New()` and late-binding

Because Sylk's daemons (guide, orchestrator, guardian) boot at
phase2 — strictly before phase4 creates the session and the
session-scoped `identity.Factory` — agents accept a nullable
`Factory` in Config and expose a late-binding setter:

```go
// config.go
type Config struct {
    // ... existing fields ...
    // Factory is optional; daemon agents are constructed before the
    // session-scoped Factory exists. phase4 calls SetIdentityFactory
    // after wireIdentityAccounting. On-demand and pipeline agents
    // receive it at construction time (they are created after
    // phase4) and should validate non-nil.
    Factory *identity.Factory
}

// agent.go
type Agent struct {
    // ...
    identity *identity.AgentIdentity
    factory  *identity.Factory
}

func New(cfg Config, ...) (*Agent, error) {
    a := &Agent{factory: cfg.Factory, /* ... */}
    if cfg.Factory != nil {
        id, err := cfg.Factory.Mint(identity.MintOptions{
            Kind: identity.AgentTypeGuardian, // per-agent kind
            Pod:  identity.PodRef{ID: "guardian", Type: identity.PodTypeDaemon},
        })
        if err != nil {
            return nil, fmt.Errorf("mint identity: %w", err)
        }
        a.identity = id
    }
    return a, nil
}

// SetIdentityFactory is called from cmd/tui.go:wireIdentityAccounting
// once the session-scoped Factory exists.
func (a *Agent) SetIdentityFactory(f *identity.Factory) error {
    if f == nil {
        return fmt.Errorf("nil identity factory")
    }
    a.factory = f
    id, err := f.Mint(identity.MintOptions{
        Kind: identity.AgentTypeGuardian,
        Pod:  identity.PodRef{ID: "guardian", Type: identity.PodTypeDaemon},
    })
    if err != nil {
        return fmt.Errorf("mint identity: %w", err)
    }
    a.identity = id
    return nil
}
```

In `cmd/tui.go:wireIdentityAccounting`, add the late-bind call
alongside the Guardian example:

```go
if a := phase1.myAgentRef.Load(); a != nil {
    if err := a.SetIdentityFactory(factory); err != nil {
        return fmt.Errorf("my agent set identity: %w", err)
    }
}
```

For on-demand and pipeline agents (created after phase4 via the
ActivationController), pass `phase1.identityFactory.Load()` directly
into the agent's Config at creator invocation time; no late binding
needed.

### Step 5b — request entry boundary

At the handler where a bus message / forwarded request enters the
agent, build a task and decorate ctx:

```go
func (o *Orchestrator) handleBusRequest(ctx context.Context, msg *guide.Message) {
    task, err := o.factory.NewTask(identity.TaskOptions{
        DisplayID:   msg.DisplayID,
        Correlation: identity.CorrelationID(msg.CorrelationID),
    })
    if err != nil { /* return error to caller */ }
    ctx = identity.WithIdentity(ctx, o.identity)
    ctx = identity.WithTask(ctx, task)
    // proceed with ctx
}
```

For pipeline workers, use `Factory.SubTask` with the parent task's
UID, a `Pipeline.Stage`, and any new labels. The replica forwarders
(academic / librarian / archivalist) use `Factory.MintReplica` to
produce a fresh AgentIdentity whose `Owner` back-references the
canonical agent.

### Step 5c — dispatch sites

Every site listed below receives a `ctx` that already carries
identity + task. No extra code needed beyond passing that ctx through
to the provider. Audit each site to confirm nothing is stripping the
values (e.g. `context.Background()`, `context.WithoutCancel`).

| Agent / File | Line | Call |
|---|---|---|
| `agents/guide/llm_classifier.go` | 90 | `c.provider.Complete(ctx, req)` |
| `agents/guide/guide_self_response.go` | 207 | `r.provider.Complete(ctx, req)` |
| `agents/guide/guide_self_response.go` | 247 | `r.provider.Stream(streamCtx, req)` |
| `agents/guide/skill_plan_acceptance.go` | 137 | `provider.Complete(ctx, req)` |
| `agents/architect/planner_anthropic.go` | 774 | `p.provider.StreamWithHandler(ctx, req, handler)` |
| `agents/architect/planner_anthropic.go` | 781 | `p.provider.StreamWithHandler(streamCtx, …)` |
| `agents/orchestrator/conversation.go` | 186 | `o.provider.Complete(llmCtx, llmReq)` |
| `agents/guardian/tool_loop.go` | 165 | `provider.Stream(ctx, req)` |
| `agents/archivalist/client.go` | 109 | `c.provider.Complete(ctx, req)` |
| `agents/archivalist/synthesis.go` | 166 | `s.provider.Complete(ctx, req)` |
| `agents/archivalist/tool_loop.go` | 153 | `p.Stream(ctx, req)` |
| `agents/engineer/audit.go` | 74 | `p.Complete(ctx, req)` |
| `agents/shared/thinking_watchdog.go` | 245 | `p.Complete(ctx, req)` |
| `agents/shared/thinking_watchdog.go` | 271 | `p.Stream(ctx, req)` |
| `agents/shared/inter_agent_branch.go` | 168 | `h.Complete(ctx, …)` |

For each site: the *caller* that produced `ctx` must have already
bound identity and task. If the site wraps with `context.WithTimeout`,
that is fine — values flow through. If it rebuilds ctx from
`context.Background()`, that is a bug that must be fixed as part of
the migration.

### Step 5d — raw-client bypasses (Step 6 in master plan)

`agents/guide/guide.go:~602` (`NewWithClassifier`) and
`agents/guide/classification.go:~175` (`NewClassifierWithAPIKey`)
construct raw `anthropic.NewClient(opts...)` adapters that bypass the
gateway entirely. These must route through a `GatewayProvider`
instead so the accounting hook fires. If a raw client is truly
needed (e.g. for a probe that should not appear in billable totals),
use `Factory.SystemIdentity(purpose)` + `Factory.SystemTask(corr)`
to produce non-billable accounting entries that still flow through
the gateway.

### Step 5e — replica handoffs (Step 9)

`agents/shared/handoff_replica.go` defines `ReplicaHandoffAgentID`
and `AttachReplicaHandoffBridge` which synthesize
`"{agent}#replica-{corr}"` string IDs. Replace:

```go
replicaID := ReplicaHandoffAgentID(canonicalAgentID, opts.CorrelationID)
```

with:

```go
replicaIdentity, err := factory.MintReplica(identity.MintReplicaOptions{
    Parent: canonicalIdentity,
})
```

Then `replicaIdentity.UID()` is the replica's primary key; the `Owner`
ref on the identity carries the canonical pointer. Delete
`ReplicaHandoffAgentID`, the `#replica-` delimiter, and
`replicaHandoffActivityPublisher` (which rewrote strings back to the
canonical ID for display). The accountant + activity hook now
natively carry the replica's identity and the `Owner.UID` so the UI
can render both without string surgery.

## Verification

After each agent migration:

1. `go build ./...` must succeed.
2. The agent's own tests pass with `go test -race`.
3. `go test ./cmd/...` for `TestRegisterPhase4AcademicCompletesGuideReturnPath`
   and siblings should progress further than they did before
   (they fail once any remaining unmigrated agent is reached).

When every dispatch site is migrated, the `cmd` integration tests
should run end-to-end and the accountant's views should be non-empty
after a turn. `accountant.All()` snapshots should contain one bucket
per `(uid, gen, model, task)` tuple, with per-kind / per-model /
per-pipeline reducers matching the per-key totals.

## What not to do

- Do not call `identity.RebuildForReplay` in normal construction. It
  is strictly for WAL replay and bypasses the factory's ordinal
  registry.
- Do not add a "silent fallback" mode to the gateway that proceeds
  without identity. The error is the contract.
- Do not stamp identity back into `req.Metadata` — the old
  `stampRequestIdentity` helper has been deleted; nothing reads those
  keys anymore.
- Do not keep the `ReplicaHandoffAgentID` string convention as a
  compatibility shim. Replicas are real identities with real UIDs.

## Ordering suggestion

Migrate in this order to reduce breakage surface at each step:

1. `cmd/tui.go` bootstrap (Factory + Accountant + MultiHook wiring).
2. Shared helpers (`agents/shared/thinking_watchdog.go`, `inter_agent_branch.go`,
   `context_brief.go`).
3. Standalone agents that only take incoming requests:
   orchestrator, guardian, guide (classifier + self-response first;
   skill-plan-accept and raw-client bypasses after).
4. Knowledge agents and their replica flows:
   archivalist, librarian, academic (Step 9).
5. Pipeline workers: engineer, designer, inspector-pipeline,
   tester-pipeline (Step 8).
6. Singleton global agents: architect, inspector, tester (Step 7).

At every stage, run the focused tests for the touched agent plus
`./cmd/...` to check end-to-end progress.
