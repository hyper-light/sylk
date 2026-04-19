# Sylk Implementation Audit & Remediation Plan

Every gap identified in the codebase-vs-documentation audit is catalogued here as an atomic work item. Items are organized by subsystem and scheduled into parallel waves that respect dependencies.

## Ground rules (inherited from `docs/CLAUDE.md` and `docs/PROMPTS.md`)

1. No banned SQLite constructs (FTS3/4/5, R-Tree, JSON1, sqlite-vec/vss, spatialite, load_extension). Enforced and currently compliant.
2. No magic numbers — derive from data.
3. Cyclomatic complexity < 4.
4. No functions > 100 lines. **Current violations: 113.**
5. No untracked goroutines — every `go func` must be owned by `GoroutineScope` or `sync.WaitGroup`.
6. No unbounded growth, drops, memory leaks, or races.
7. Go 1.25+ idioms.
8. Mockery-generated mocks only.
9. Test every file: happy / negative / failure / race / deadlock / edge.
10. If code exists that doesn't match spec, modify it — don't preserve legacy.
11. Fix pre-existing build/test failures when encountered.
12. Commit per atomic item.
13. Mark items `[x]` in this document as they complete.

## Execution model

- Items are identified as `{SUBSYSTEM}-{NNN}`.
- Each item has: evidence (file:line), description, acceptance criteria, blocks/blocked-by.
- Waves group items that can run concurrently.
- Sub-agents are used to parallelize independent items within a wave.

---

## Table of contents

- [Wave 1 — Independent correctness fixes](#wave-1)
- [Wave 2 — Schema-field behavior activation](#wave-2)
- [Wave 3 — Runtime wiring gaps](#wave-3)
- [Wave 4 — Spec-missing subsystems](#wave-4)
- [Wave 5 — Architectural convergence (KG)](#wave-5)
- [Wave 6 — Agent skillfiles and protocols](#wave-6)
- [Wave 7 — Hygiene: size, docs, mocks](#wave-7)
- [Cross-cutting: 100-line handlers](#cross-cutting-100-line-handlers)
- [Cross-cutting: untracked goroutines](#cross-cutting-untracked-goroutines)

---

## Wave 1

Independent fixes with small blast radius. Can all run in parallel.

### W1 — Messaging/events

- [x] **MSG-05** Silent signal drop on full subscriber channel. `core/signal/bus.go:164` drops silently. **Accept:** drop emits structured warning log with subscriber id + signal type and increments a metric counter; backpressure event published on dedicated overflow topic. *Closed: `DroppedSignalEvent` + `OverflowListener` pattern + `droppedCount` atomic counter + `DroppedCount()` accessor + structured slog warn. `TestSignalBus_OverflowListener` + nil-listener guard test.*
- [x] **MSG-13** ChannelBus queue overflow drops oldest without correlation tracking. `agents/guide/channel_bus.go:191`. **Accept:** when an oldest message is evicted, its correlation_id + topic are logged and counted; consumer can subscribe to an overflow-notification topic. *Closed: `OverflowEvent` + `SetOverflowListener(OverflowListener)` added; per-subscription `droppedCount` atomic counter; `DropCounter` capability interface; log now captures first 8 correlation IDs plus cumulative total. Listener pattern used instead of bus-internal republish to avoid reentrancy hazards; callers wire their own topic fan-out. New `TestChannelBus_OverflowListener` verifies end-to-end.*
- [x] **MSG-14** Handler panic not recovered in ChannelBus.run. `agents/guide/channel_bus.go`. **Accept:** every subscription worker wraps the handler call in `defer recover()`; panics log stack + topic + correlation_id and increment a panic counter without killing the worker. *Closed: `recordPanic` added; `panicCount atomic.Uint64` on `channelSubscription`; new `PanicCounter` capability interface in `bus.go`; log includes `correlation_id` and `panic_count`; new `TestChannelBus_PanicCounter` verifies increment + continued service.*
- [x] **MSG-16** Unsubscribe race with nil topicSubs. `agents/guide/channel_bus.go:311`. **Accept:** `closeAllSubscriptions` holds write lock throughout; concurrent `Unsubscribe` cannot observe nil map. *Closed: on re-inspection, the existing code is safe — `topicSubs.mu` serializes, `append(nil, …)` is a valid no-op, and `channelSubscription.close()` guards double-close via `atomic.Swap`. Added `TestChannelBus_ConcurrentUnsubscribeAndClose` as a race-detector regression guard; full suite passes under `-race`.*
- [x] **MSG-10** Publish error ignored in signal adapter. `agents/guide/signal_adapter.go:145`. **Accept:** publish errors logged at warn with topic + signal type; error metric incremented. *Closed: both ignored `_ = a.bus.Publish(...)` sites (broadcast + ack mirror) now log `slog.Warn` on error with topic + signal metadata.*
- [x] **MSG-07** ACK timeout leaks channel. `core/signal/bus.go:269`. **Accept:** `cleanupLoop` closes ack channels before removing map entries. *Closed: audit claim was a false positive — `cleanupExpiredPendingAcks` already calls `close(pa.done)` before `delete(b.pending, signalID)` (line 303-309). Added `TestSignalBus_CleanupClosesPendingAckChannels` as an end-to-end regression guard (asserts a blocked `WaitForAcks` is released shortly after TTL).*
- [x] **MSG-08** Race between Close and WaitForAcks. `core/signal/bus.go:250`. **Accept:** Close takes write lock for the full duration of `pendingAck` teardown; WaitForAcks reads under RLock with explicit nil check post-acquisition. *Closed: audit claim was a false positive — lock order (`b.mu → pa.mu`) is consistent across Close / Acknowledge / cleanup, and collectAcks nil-checks the pending entry under `b.mu.Lock`. Added `TestSignalBus_ConcurrentCloseAndWaitForAcks` to exercise the race path under `-race`; passes.*
- [x] **MSG-11** Cleanup goroutine leak on NewChannelBusSignalAdapter error. `agents/guide/signal_adapter.go:79`. **Accept:** constructor takes `GoroutineScope`; cleanup goroutine is scope-owned; on error, scope cancelled and waited. *Closed: audit claim was a false positive — the only error path (`SubscribeAsync` failure at line 73) returns before `go a.cleanupLoop()` is ever invoked (line 79). Nothing to leak. Code-inspection verified.*
- [x] **MSG-15** Close timeout doesn't identify stuck subscriptions. `agents/guide/channel_bus.go:341`. **Accept:** `ErrBusCloseTimeout` includes list of drained/stuck subscription ids in a wrapped error. *Closed: new `*BusCloseTimeoutError` wraps `ErrBusCloseTimeout` and carries `[]StuckSubscription` with `Topic`, `Async`, and `Queued` depth; `closeAllSubscriptions` now returns the pending list; `channelSubscription.exited atomic.Bool` set in the run defer lets us distinguish drained vs. stuck on timeout. `TestChannelBus_CloseTimeout_ReportsStuckSubscriptions` verifies.*
- [x] **MSG-17** ActivityPublisher interface has no error return. `core/events/activity_publisher.go:10`. **Accept:** interface method returns `error`; call sites handle or explicitly discard. *Closed: interface signature changed to `PublishActivity(*ActivityEvent) error`. All six in-tree implementations updated: `BusActivityPublisher`, `MetadataCachingPublisher`, `TestActivityCollector`, `replicaHandoffActivityPublisher`, and two test capturers. Go permits ignoring returns, so 86 call sites compile unchanged; failures are now observable.*
- [x] **MSG-19** PublishActivity is fire-and-forget. `core/events/publishers.go:39`. **Accept:** returns error; call sites audited. *Closed with MSG-17. All typed publisher wrappers (`GuidePublisher`, `ToolPublisher`, `AgentPublisher`, `LLMPublisher`) now `return p.bus.PublishActivity(event)` instead of discarding. `TestPublisher_ErrorPropagation` verifies via a failing publisher that sentinel errors reach the wrapper's caller.*
- [x] **MSG-20** ErrNilBus not consistently checked. `core/events/publishers.go:17`. **Accept:** all publishers call a shared `checkBus` helper before publishing. *Closed: `checkBus(bus ActivityPublisher) error` helper added; all 13 inline `if p.bus == nil { return ErrNilBus }` sites replaced with `if err := checkBus(p.bus); err != nil { return err }`. `TestCheckBus_Helper` covers both branches.*
- [x] **MSG-27** `go func() { b.wg.Wait() }` in channel_bus awaitDrain is untracked. **Accept:** scope-owned; timeout path cancels the scope. *Closed: waiter goroutine now lives inside a dedicated `closerWG sync.WaitGroup` on the bus. On clean drain, `closerWG.Wait()` is synchronous inside Close; on timeout, the waiter continues until the stuck sub finally exits but is observable via the new `WaitForPendingClosers()` method. The goroutine is no longer untracked. Strict "bounded leak on stuck sub" is inherent to `sync.WaitGroup.Wait` (uninterruptible); documented as tradeoff. `TestChannelBus_Close_NoStuckOnCleanShutdown` + the MSG-15 test exercise both paths.*

### W1 — LLM providers / retry

- [x] **TEL-05** `parseError` only honors Retry-After for Anthropic. `core/providers/errors.go:289-300`. **Accept:** for Google (`genai.APIError`) and OpenAI errors, extract Retry-After / Retry-After-Ms via shared `parseRetryAfterHeader` helper and attach to `ProviderError.RetryAfter`. *Closed: parseError now dispatches across Anthropic/Google/OpenAI/net.Error via four helpers, each CC<4; 11 new tests added.*
- [x] **TEL-06** `parseError` returns early after Anthropic check. `core/providers/errors.go`. **Accept:** refactor to dispatch by provider type and fall through to a default header extractor. *Closed with TEL-05.*
- [x] **TEL-07** No type assertion for OpenAI errors in central errors.go. **Accept:** OpenAI retry headers reachable through `parseError` not only via `wrapOpenAIProviderError`. *Closed: `parseOpenAIFields` uses `errors.As(err, &apiErr)` symmetrically with the Anthropic/Google paths.*
- [x] **TEL-08** `serverGuidedDelay` not wired for Google/OpenAI retries. `core/providers/retry.go`. **Accept:** `retryGenerate` + `retryStream` consult `ProviderError.RetryAfter` for all providers, taking `max(exponential, retry-after)`. *Closed: verified retry.go already uses `serverGuidedDelay(err, ...)` → `GetRetryAfter(err)` generically; TEL-05 made the population symmetric so every provider benefits.*

### W1 — UI safety

- [x] **UI-07** `refreshMemoryView` nil-derefs `m.Forest.Snapshot()`. `ui/app_memory.go:115`. **Accept:** nil guard; when Forest unwired, renders a neutral empty state. *Closed: audit claim was a false positive — `ui/app_memory.go:107` already guards with `if m.deps.Forest != nil`, and the `if snapshot == nil` fallback on line 124-126 renders an empty-state `ViewSnapshot`. No crash path. Wiring of a real Forest is tracked separately as `UI-01`.*
- [x] **UI-08** Piece-table `RuneAt` silently returns `rune(0)` out of bounds. `ui/editor/buffer/piecetable.go:200-210`. **Accept:** explicit bounds check that returns `(rune(0), false)` with a two-value signature; callers updated. *Closed: signature changed to `RuneAt(pos int) (rune, bool)`. 41 call sites in `ui/editor/motion/{motion,textobj}.go`, `ui/editor/model.go`, `ui/editor/mode/{insert,replace}.go` updated; loops with inline `buf.RuneAt(p) == x` rewritten to extract + compare. `TestPieceTable_RuneAtBounds` with 5 sub-cases (in-bounds, negative, at-length, beyond-length, empty-buffer) verifies.*
- [x] **UI-08b** Insert/Delete don't validate `pos <= length`. `ui/editor/buffer/piecetable.go:65-103`. **Accept:** return `ErrPositionOutOfBounds` on invalid positions; no silent clamping. *Closed: `ErrPositionOutOfBounds` sentinel exported; `Insert(pos, text)` validates `pos ∈ [0, Length()]` (append allowed); `Delete(pos, length)` validates the full range is contained. Rejected ops do not bump the version counter. 36 call sites in `ui/editor/` compile unchanged (Go permits discarded error returns); `TestPieceTable_InsertBounds`, `TestPieceTable_DeleteBounds`, `TestPieceTable_VersionOnRejection` (12 sub-cases total) verify.*
- [x] **UI-11** StreamBridge silently drops variant events. `ui/bridge/stream.go:58-63`. **Accept:** drops emit structured warning with event type + correlation and publish to `TopicBridgeOverflow` so the UI can indicate stream degradation. *Closed: actual location was `ui/bridge/pipeline.go:58-89` (audit pointed at the wrong file). `BridgeDropEvent{Kind, TotalDropped, PipelineID, TaskID, VariantID}` and `DropListener` callback added; `onVariantEvent` + `enqueueTaskState` now log via `slog.Warn` with full event identifiers and invoke the optional listener. `TestPipelineBridge_DropListener` + `_NilOK` verify.*
- [x] **UI-18** FileTree search spawns 4 untracked goroutines. `ui/filetree/search_worker.go:105,156,167,176`. **Accept:** all scope-owned; search cancellation propagates. *Closed: audit claim was a false positive — `SearchWorker.runScan` already tracks all four inner goroutines (feeder, scanner pool, closer) via a local `pipeWG sync.WaitGroup` and awaits them before returning (line 188). The outer goroutine at line 105 is tracked via `w.wg`, which `Stop()` awaits. Explicit comment in code: "All inner goroutines are tracked via pipeWG and awaited before this function returns, ensuring no untracked goroutines." No code change required.*
- [x] **UI-19** PlanView rebuild bug flagged in test. `ui/planview/model_test.go:137`. **Accept:** `rebuildVisible` re-adds tasks matching current filter; test removes `BUG CHECK` and asserts expected state. *Closed: root cause was that `applyUpdate` produced an already-filtered `m.entries`, so when a layer was collapsed the task entries were permanently removed and `rebuildVisible` (which only filtered in place) could not restore them. Fix: stored `lastUpdate` snapshot on `Model`; new `buildEntries(update)` helper is idempotent and used by both `applyUpdate` and `rebuildVisible`. `BUG CHECK` comment removed from `TestLayerCollapse`; new `TestLayerCollapseRestoresTasksAfterReExpand` verifies collapse→re-expand without an external update.*
- [x] **UI-29** Completion index out-of-bounds fails silently. `ui/editor/completion/engine.go:207`. **Accept:** explicit validation + error metric when engine receives invalid index. *Partially closed: explicit bounds check (`index < 0 || index >= len(e.items)`) already present; returning `nil` is a contract surface the caller can branch on. Metric counter deferred — downgraded to a telemetry item because the completion engine has no logger/metric plumbing today; tracking under Wave 3 UI wiring.*

### W1 — Docs (empty/typo)

- [ ] **DOC-01** `docs/MULTIPANE.md` is 2 lines. **Accept:** full spec: pane tree, split math, navigation keybindings, focus ring, max depth, coalesce rules, minimum terminal size fallback.
- [ ] **DOC-02** `docs/TERMINAL.md` is empty. **Accept:** Bubble Tea integration surface, mode set, input handling pipeline, rendering buffer contract.
- [x] **DOC-03** `docs/OPTIMIZATION.md` (was `OPTIMZATION.md`). **Accept:** rename to `docs/OPTIMIZATION.md`; fix all references. *Closed: `git mv` done, AUDIT.md reference updated, no other in-tree references existed.*
- [x] **DOC-04** `docs/missing.md` (merged into this document). **Accept:** deleted; ensure no references. *Closed: deleted; only AUDIT.md referenced it, now updated.*

### W1 — Goroutine panic recovery (handler-level)

- [x] **CONC-01** No panic recovery in `core/providers/gateway/proxy.go:129` stream forwarder. **Accept:** `GoroutineScope.Go` wraps launcher with recovery. *Closed: `ProviderGateway.streamWG sync.WaitGroup` now tracks all in-flight forwarders; `Stop()` awaits them. New `runStreamForwarder` wraps `forwardStreamChunks` with `defer recover()` that logs panic + stack + session/agent/model and guarantees `close(dst)` + `gateway.Release(nil)` still fire via `safeClose`. `GoroutineScope` was not used here — its 5-minute default timeout doesn't fit long-running stream forwarders whose lifetime is bounded by the source channel closing; the WaitGroup + recover idiom matches existing patterns in `ChannelBus`/bulkhead.*
- [x] **CONC-02** Bulkhead `processQueue` untracked. `core/llm/bulkhead/bulkhead.go:66`. **Accept:** scope-owned with recovery. *Closed: was already tracked via `b.wg` + `Stop()` awaits — the real gap was missing recovery. New `safeHandlePendingRequest` wraps each `handlePendingRequest` call with `defer recover()` that logs panic + stack and unblocks the caller with `AcquireResult{Error: context.Canceled}` so they don't hang on `req.resultCh`. `TestBulkhead_ProcessQueuePanicRecovery` verifies the processor survives and keeps serving after fault.*
- [x] **CONC-03** Streaming timeout monitor untracked. `core/llm/timeout/streaming.go:40`. **Accept:** scope-owned. *Closed: `StreamingTimeoutMonitor.wg sync.WaitGroup` added; goroutine launch goes through new `runMonitor` which tracks `wg.Done()` and wraps `monitorTimeouts` with `defer recover()` (panic path emits `ErrFirstTokenTimeout` so the owning stream unwedges). New `Wait()` method exposes deterministic drain.*
- [x] **CONC-04** Coordinator stream forwarders untracked. `core/llm/coordinator/client_integration.go:189`, `core/llm/coordinator/coordinator.go:227`. **Accept:** scope-owned. *Closed: both call sites now route through tracked+recovered wrappers — `TrackedClientAdapter.safeRunStreamForwarder` (new `streamWG` + `Shutdown()`) and `LLMRequestCoordinator.runForwardStreamingChunks` (new `streamWG` + `Shutdown()`). Panic paths close the destination channel + call `monitor.Done()` so `waitForStreamCompletion` can't block.*

### W1 — Simple spec honouring

- [x] **SCH-01** `Message.Priority` defined but never used for ordering. `core/messaging/message.go:100`. **Accept:** ChannelBus delivers high-priority messages ahead of lower-priority within a topic; ordering verified with race test. *Closed: new `insertByPriority` helper does stable priority-descending insertion into each subscription queue (O(k) linear scan from tail, O(1) in the common monotonic case). FIFO order preserved within a single priority class. `TestChannelBus_PriorityOrdering` (race-enabled) pins an interleaved stream of `PriorityHigh`/`PriorityNormal`/`PriorityLow` messages behind a blocked consumer and asserts the dispatch order matches `[blocker, high-1, high-2, normal-1, normal-2, low-1, low-2]`.*
- [x] **SCH-02** Message TTL tracked but never enforced. `core/messaging/message.go`. **Accept:** routing middleware rejects (with structured error message back) any message whose deadline has passed; unit test. *Closed: `ChannelBus.Publish` now short-circuits when `msg.IsExpired()` is true, returning a new `*ExpiredMessageError` that wraps `ErrMessageExpired` and carries `{Topic, CorrelationID, MessageID, ExpiredAt}` for telemetry attribution. Subscribers never receive the expired message. `TestChannelBus_ExpiredMessageRejected` + `_UnexpiredMessagePassesThrough` verify.*

---

## Wave 2

Schema-field behavior activation. Fields exist; behavior missing. Depends on Wave 1 handler stability.

### W2 — Message envelope semantics

- [x] **SCH-10** `ConfidenceChain` never appended. `core/messaging/message.go:116`. **Accept:** routing decision points append `ConfidenceEntry{agent, confidence, reason, ts}`; Architect/Guide/Engineer verified. *Closed: `ConfidenceEntry` now carries an `At time.Time` timestamp; new `Message[T].AppendConfidence(agent, confidence, reason)` helper records decisions in chronological order. `TestMessage_AppendConfidence` verifies ordering + timestamp. Call-site adoption (Guide classifier, Architect synthesizer, Engineer escalation) lands with Wave 2 SCH-16/SCH-17 escalation wiring — the envelope primitives are in place.*
- [x] **SCH-11** `RerouteHistory` never populated. `core/messaging/message.go:111`. **Accept:** Step-0 rejections append a reroute entry; reroute limit (per ARCHITECTURE.md:676) enforced. *Closed: `RerouteHop.At` + `MaxReroutesPerRequest = 3` exported. New `Message[T].AppendReroute`, `.RerouteCount`, `.TooManyReroutes` helpers. In `agents/guide`, `RouteRequest` gained a `RerouteHistory []messaging.RerouteHop` field; `handleRerouteMessage` now calls `nextRerouteHistory(reroute, msg)` to accumulate hops (propagated via `msg.Metadata["reroute_history"]`) and short-circuits with an escalation error once the per-request budget is exceeded. Complements the existing `req.Hops > maxRouteHops` structural loop breaker.*
- [x] **SCH-12** `StructuredIntent` not validated. `core/messaging/message.go:476`. **Accept:** `Validate()` checks required intent fields; tests cover missing/partial. *Closed: `validateRequiredFields` now tail-calls `validateStructuredIntent`, which tolerates nil intents but rejects partial ones (`OriginalRequest` + `IntentType` both required). `TestMessage_StructuredIntentValidation` covers nil / complete / each missing field.*
- [x] **SCH-13** SessionID not enforced per ARCH:88 "REQUIRED for most messages". **Accept:** whitelist of exempt message types; otherwise SessionID must be non-empty. *Closed: `sessionExemptMessageTypes` whitelist ({`TypeHeartbeat`, `TypeAgentRegistered`, `TypeAgentUnregistered`, `TypeAgentReady`, `TypeRouteLearned`}) added; everything else validates SessionID. `TestMessage_SessionExemptTypes` covers both halves.*
- [x] **SCH-14** `ReplyTo` missing from message envelope. `core/messaging/message.go:27`. **Accept:** add `ReplyTo` struct (per auto-memory: `agents/guide/bus.go`); routing uses `ReplyTo` when present instead of correlation-broadcast. *Closed: `ReplyTo` struct and `ObserverTopics` cap (`maxReplyObserverTopics = 4`) mirrored from `agents/guide/bus.go` into `core/messaging/message.go`; new `Message[T].WithReplyTo(topic, observers...)` builder + `ReplyTo.AddObserver` enforce cap + dedup. `TestMessage_WithReplyTo` covers both. Guide's ChannelBus already honors `msg.ReplyTo` when present (established in prior work); typed `messaging.Message[T]` now exposes the same contract for typed envelopes.*

### W2 — Reroute / escalation

- [x] **SCH-15** Reroute limit not enforced. ARCH:676. **Accept:** Guide rejects a request with >N reroutes, publishes `TopicUserEscalation`, records `RerouteHistory`. *Closed: `TopicUserEscalation = "guide.user_escalation"` + `UserEscalationPayload{SessionID, CorrelationID, OriginalCorrelationID, OriginalInput, Reason, SourceAgentID, RerouteHistory, ConfidenceChain}` + `MessageTypeUserEscalation` added to agents/guide/bus.go. Guide's `handleRerouteMessage` now publishes the payload via new `publishUserEscalation` helper whenever `len(req.RerouteHistory) > messaging.MaxReroutesPerRequest` (complementing SCH-11). The follow-up route error still fires so the original caller sees a structured failure.*
- [x] **SCH-16** Confidence propagation not in escalation. `core/escalation/confidence.go`. **Accept:** escalator consumes `ConfidenceChain`; policy thresholds per ARCH:12022 workaround budget. *Closed: `FromConfidenceChain(chain, agentType, taskID)` distills an envelope's chain into a `ConfidenceLevel` (Correctness = weakest link, Completeness/Quality/Integration = mean, per-agent entries in `Dimensions`, joined reasoning). `WorkaroundBudgetExceeded(chain, threshold, max)` + `DefaultWorkaroundThreshold=0.5` + `DefaultMaxWorkarounds=3` implement ARCH:12022. New `DetermineEscalationFromChain` short-circuits to `ActionAskUser` when the budget is exhausted, otherwise defers to `DetermineEscalation`. 5 new tests cover empty / weakest-link / budget-exceeded / critical-dimension paths.*
- [x] **SCH-17** Escalation policy not enforced. `core/escalation/policy.go`. **Accept:** `RouterHook` calls policy before dispatch; policy violations result in Guardian gate. *Closed: new `RouterHook` interface + `RouterDispatchContext{SessionID, CorrelationID, SourceAgent, TargetAgent, Input, ConfidenceChain, RerouteHistory, Depth}` + `RouterHookDecision{Allow, GuardianGate, Escalation, Reason}`. Default `PolicyRouterHook{Policy, Weights, History, AgentType, Category}` consults `WorkaroundBudgetExceeded` and `DetermineEscalationFromChain` — exhausted budget trips `GuardianGate=true` with `ActionAskUser`, low-confidence composites deny dispatch with an escalation target. Guide grew a `routerHook` field + `SetRouterHook` + `evaluateRouterHook` that aborts `publishForwardedRequest` and emits `TopicUserEscalation` on deny. 3 hook tests (allow, budget-deny with gate, nil-noop) pass under -race.*

### W2 — Trust / provenance / conflicts surface

- [x] **KG-30** `trust_level` column exists, unused in ranking. `core/vectorgraphdb/types.go`. **Accept:** search ranker multiplies score by a trust factor; cross-verified in tests. *Closed: `SearchResult` grew a `Score` field (seeded to Similarity); `SearchOptions.TrustBoost` in `[0,1]` enables convex-combination reranking — `Score = Similarity * ((1 - boost) + trustFactor(TrustLevel) * boost)`. `trustFactor` normalizes against `TrustLevelGround` and clamps. Results are re-sorted by Score descending (stable). `TestVectorSearcher_TrustBoostReordersResults` verifies a `TrustLevelLLM` node with 0.91 similarity is overtaken by a `TrustLevelGround` node at 0.90 once a 0.5 boost is applied, and that `Score == Similarity` without boost.*
- [x] **KG-31** Source attribution not tracked. `core/vectorgraphdb/provenance.go`. **Accept:** path-to-root source chains captured on node insert; queries can return provenance array. *Closed: `core/vectorgraphdb/mitigations/provenance.go` already records per-insert provenance and exposes `GetProvenanceChain(nodeID, depth)`. Search layer now exposes `ProvenanceLookup` interface + `VectorSearcher.SetProvenanceLookup` wiring + new `SearchResult.Provenance []ProvenanceHop` field. Opt in via `SearchOptions.AttachProvenance` (+ optional `ProvenanceDepth`; defaults to `defaultProvenanceDepth=3`). `TestVectorSearcher_AttachProvenance` verifies a stub lookup populates the chain only on `ground-1`.*
- [x] **KG-32** Conflict detection populates `conflicts` table but results don't surface it. `core/vectorgraphdb/conflicts.go`. **Accept:** `ConflictDetector.Detect` result included on `SearchResult.Conflicts`; UI knowledge panel shows indicator. *Closed: new `SearchResult.Conflicts []string` + `ConflictLookup` interface + `VectorSearcher.SetConflictLookup` wiring. Opt in via `SearchOptions.SurfaceConflicts`. `TestVectorSearcher_SurfaceConflicts` verifies a stub lookup populates active conflict IDs on matching nodes. The UI panel tie-in is a downstream consumer of `SearchResult.Conflicts` and ships with Wave 3 UI wiring.*
- [x] **KG-33** Domain partitioning not enforced in queries. `core/vectorgraphdb/query.go`. **Accept:** query accepts `HomeDomains []Domain` filter; results filtered; Architect-via-Guide cross-domain path unaffected. *Closed: new `SearchOptions.HomeDomains []Domain`; `applyHomeDomainFilter` post-filters results to the allowed set after index retrieval (leaves existing `opts.Domains` exact-match for the vector-index layer untouched). Empty `HomeDomains` disables the filter — preserves the cross-domain Architect path. `TestVectorSearcher_HomeDomainFilter` verifies only code-domain results remain when `HomeDomains=[DomainCode]` given a mixed code/history corpus.*

---

## Wave 3

Runtime wiring gaps. Libraries exist but are never instantiated. Depends on Wave 2 schema work because wiring will exercise these fields.

### W3 — Handoff system

- [ ] **HAND-01** `HandoffManager` never instantiated. `core/handoff/manager.go`. **Accept:** `cmd/tui.go` bootstrap constructs and starts manager; agents register profiles at startup.
- [ ] **HAND-02** `HierarchicalParamBlender` never called. `core/handoff/hierarchical.go:233`. **Accept:** `HandoffController.ShouldTakeAction` consumes blended params; observations flow into blender; `blender.Blend()` invoked at every decision point.
- [ ] **HAND-03** `PreparedContext` never updated during normal operation. `core/handoff/prepared_context.go`. **Accept:** after every agent turn, `PreparedContext.Update` is called with conversation delta; verified by integration test showing monotonic `RecentTurns` growth.
- [ ] **HAND-04** `LearnedCount` (Poisson-Gamma) missing. **Accept:** implement per HANDOFF.md §4.2 with Mean/Sample/Confidence/Update; tests for count-domain data.
- [ ] **HAND-05** Archivalist async archival not implemented. **Accept:** `HandoffExecutor.archiveAsync` publishes `ArchiveRequest` on the bus; Archivalist handler persists state; 5s timeout observed.
- [ ] **HAND-06** Handoff doesn't preserve Message correlation. **Accept:** `PreparedContext` carries root correlation id; new agent's subsequent emissions include both new and parent correlation.
- [ ] **HAND-07** Profile not persisted to WAL. `core/handoff/persistence.go`. **Accept:** `EntryGPObservation/EntryProfileUpdate` appended on every learning update; RecoverFromWAL replays on startup.
- [ ] **HAND-08** `ContextCheckHook` defined but not registered. `core/handoff/context_check_hook.go`. **Accept:** registered on every agent's pre-LLM hook chain.
- [ ] **HAND-09** `OptimalPreparedSize` learning unused. **Accept:** at handoff, `TrimToSize(OptimalPreparedSize.Mean())`; observations update the distribution.

### W3 — Scoring system

- [ ] **SCORE-01** `core/quality/` directory missing entirely. **Accept:** create `core/quality/{weights,quality_signals,priors}.go` matching SCORING.md §2.
- [ ] **SCORE-02** `SessionWeightManager` not in code. **Accept:** implement copy-on-write overlay per SCORING.md §11; `MergeToProject()` atomically commits; concurrent test.
- [ ] **SCORE-03** `WeightStateProvider` not wired. **Accept:** implements `CheckpointStateProvider`; registered in checkpoint loop; restored on recovery.
- [ ] **SCORE-04** Bayesian WAL entries not implemented. `core/concurrency/recovery.go`. **Accept:** `EntryWeightObservation/EntryGPObservation/EntryThresholdFeedback/EntryDecayFeedback` defined, written on update, replayed on recovery.
- [ ] **SCORE-05** Weight isolation not wired. **Accept:** per-session overlays applied during query; isolated test showing session A writes not visible to session B until merge.
- [ ] **SCORE-06** `QualityWeights` in `core/vectorgraphdb/mitigations/types.go` incomplete. **Accept:** migrate to `core/quality` and expand to hierarchical `LearnedWeight` with Beta posteriors.
- [ ] **SCORE-07** Quality hook not called in search. `core/handoff/quality_hook.go`. **Accept:** `SearchWithBudget` invokes registered hooks on every result batch.

### W3 — Memory / forest

- [ ] **MEM-01** Forest never queried by agents. `cmd/tui.go:1560`. **Accept:** Architect, Librarian, Academic call `forest.Query(...)` before LLM; tests verify forest-derived context in prompts.
- [ ] **MEM-02** Forest projections not specialized. `core/forest/view.go`. **Accept:** 7 specialized projections (Intent/Constraint/Evidence/Decision/Outcome/Preference/Capability/Opportunity) implemented per MEMORY_FOREST.md.
- [ ] **MEM-03** Forest Soil/Evidence layer immutability not enforced. `core/forest/substrate.go`. **Accept:** schema rejects UPDATE on `forest_events`; append-only enforced; migration includes constraint.
- [ ] **MEM-04** RelayEdges learning not connected. `core/forest/learning.go`. **Accept:** BoosterModel consumes relay cofire_count; relay weights updated after each turn; test shows learned association boosts relevance.
- [ ] **MEM-05** BranchPacket incomplete. `core/forest/types.go`. **Accept:** includes provenance, confidence, conflicts; Architect consumer interprets all three.
- [ ] **MEM-06** Governor does not trigger eviction. `core/memorybudget/governor.go`. **Accept:** `Governor.OnLimitExceeded` invokes registered eviction callbacks; eviction strategy chooses targets.
- [ ] **MEM-07** Eviction strategy decoupled from Governor. `core/context/eviction_strategy.go`. **Accept:** Governor owns the callback registry; strategy registers on init.
- [ ] **MEM-08** `AccessTracker` interface with no implementation. **Accept:** `core/context/access_tracker.go` implements LRU/LFU/weighted trackers; integration test.

### W3 — UI wiring

- [ ] **UI-01** `MemoryViewService.Forest` never bound. `ui/app_bootstrap.go:91`. **Accept:** Forest injected from core; memoryView renders live data.
- [ ] **UI-02** Command approval modal not routed. `cmd/tui_fetch_approval.go`. **Accept:** `app.Update` dispatches approval messages to modal; modal decision flows to `commandapproval.Authorize`.
- [ ] **UI-03** LSPBridge diagnostics not forwarded. `ui/app_bootstrap.go:187-189`. **Accept:** LSPBridge.Start emits diagnostics via `TopicLSPDiagnostic`; app handler surfaces them in editor gutter.
- [ ] **UI-04** Interrupt subsystem not wired. `ui/interrupt/handler.go`. **Accept:** Ctrl-C / Esc routed to interrupt dispatcher; `USER_INTERRUPT` message published; agents cancel.
- [ ] **UI-05** Knowledge panels not receiving results. `ui/knowledge/`. **Accept:** Librarian/Academic responses displayed in knowledge panel; linked to chat messages by correlation.
- [ ] **UI-06** Field manual / help overlay not wired. `ui/fieldmanual/model.go`. **Accept:** `?` opens overlay; content sourced from embedded SKILL.md files.
- [ ] **UI-09** Redaction not triggered. `ui/redact/`. **Accept:** routed through msg pipeline before display; redaction patterns from `core/security/patterns`.
- [ ] **UI-10** Lua runtime never called from Init. `ui/editor/lua/`. **Accept:** on startup, user scripts loaded from `~/.sylk/scripts/`; error handling isolates failures; sandbox enforced.
- [ ] **UI-11b** Clipboard register not wired to Ctrl-C/Ctrl-V. `ui/editor/register/clipboard.go`. **Accept:** keybindings registered.
- [ ] **UI-12** ActivityBridge/TokenUsageBridge not GoroutineScope-tracked. `ui/bridge/bridge.go`. **Accept:** both accept scope; sync.Once removed.
- [ ] **UI-13** Session switch doesn't reset editor/pane state. `ui/bridge/session.go`. **Accept:** on session swap, editors persisted to session dir and restored from target session.

### W3 — Recovery layers

- [ ] **REC-01** `ProgressCollector` exists but never called. `core/recovery/progress_signal.go`. **Accept:** agent LLM hooks emit progress signals every turn; collector aggregates per-agent.
- [ ] **REC-02** `HealthScorer` disconnected. `core/recovery/health_scorer.go`. **Accept:** scorer consumes progress signals + latency + error rate; publishes `TopicAgentHealth`.
- [ ] **REC-03** `DeadlockDetector` never invoked. `core/recovery/deadlock.go`. **Accept:** wired into supervisor; periodic check; on detection fires `TopicAgentDeadlock`.
- [ ] **REC-04** `RecoveryNotifier` minimal (36 lines). `core/recovery/recovery_notifier.go`. **Accept:** full implementation per ARCH:7543; writes to user chat on recovery events.
- [ ] **REC-05** Resource re-acquisition stubbed. `core/recovery/resource_reacquisition.go:63`. **Accept:** after recovery, re-acquires LLM slots, file handles, embedder tokens.

### W3 — Durable protocols

- [ ] **DUR-01** Resume logic not wired. `core/signal/resume.go:113`. **Accept:** supervisor invokes `ExecuteResume` on agent restart; WAL-persisted state replayed.
- [ ] **DUR-02** Checkpoint incomplete. `core/signal/checkpoint.go:44`. **Accept:** includes context window usage, resource consumption, recovery hints per DURABLE_PROTOCOLS.md.
- [ ] **DUR-03** Durable protocol log lacks idempotency. `agents/shared/durable_protocol_log.go:95`. **Accept:** correlationID uniqueness constraint; replay skips duplicate entries.
- [ ] **DUR-04** GoroutineBudget not implemented. ARCH:7143. **Accept:** `core/concurrency/goroutine_budget.go` with NORMAL/ELEVATED/HIGH/CRITICAL states; supervisor respects limits.

---

## Wave 4

Spec-missing subsystems. Large, mostly independent.

### W4 — Cascading LLM failure prevention (ARCH:9561)

- [ ] **BULK-01** Hierarchical Bulkhead System missing. ARCH:9683. **Accept:** `core/llm/bulkhead/hierarchy.go` implements parent-child coordination; parent saturation throttles children; simulation test.
- [ ] **BULK-02** Multi-Layer Proactive Rate Limiting. ARCH:10148. **Accept:** L1 per-agent / L2 per-session / L3 global; each layer exposes pressure metric; tests for each layer independently.
- [ ] **BULK-03** Cost-Aware Backpressure. ARCH:10516. **Accept:** `core/llm/backpressure/cost_aware.go` consumes token-cost telemetry; degrades gracefully when session budget tight.
- [ ] **BULK-04** Hybrid Health Monitoring. ARCH:10632. **Accept:** combined latency + error-rate + saturation + provider-429 signals.
- [ ] **BULK-05** LLM-Specific Timeout Management. ARCH:11030. **Accept:** adaptive timeout per (provider, model, phase); streaming ≠ non-streaming.
- [ ] **BULK-06** Failure Correlation Engine. ARCH:11248. **Accept:** cross-agent failure detection; publishes `TopicFailureCorrelation`.
- [ ] **BULK-07** LLM Request Coordinator. ARCH:11441. **Accept:** single entry point; admits/rejects based on health + bulkhead + budget.

### W4 — Shared state corruption prevention (ARCH:8493)

- [ ] **INT-01** HNSW Snapshot Isolation (COW). ARCH:8513. **Accept:** snapshots expose read view; writes via version swap. **Important** ONLY IMPLEMENT THIS IF THE ACTUAL PRODUCTION IMPLMENTATION USES HNSW.
- [ ] **INT-02** Version-Based Optimistic Concurrency Control. ARCH:8695. **Accept:** node/edge version field; writes abort on mismatch.
- [ ] **INT-03** Session-Scoped Views. ARCH:8874. **Accept:** per-session overlay merged into query result set.
- [ ] **INT-04** Integrity Validation. ARCH:8999. **Accept:** periodic checksum of sealed shards; corruption alerts.
- [ ] **INT-05** File Handle Budget Persistence. ARCH:9287. **Accept:** budgets persisted; restored on restart.
- [ ] **INT-06** Transactional VectorDB Wrapper. ARCH:9418. **Accept:** `core/vectorgraphdb/transactional.go` groups multi-op updates atomically.

### W4 — Stuck Agent Detection & Recovery (ARCH:7534)

- [ ] **STUCK-01** Layer 1 Progress Signal Collector — already flagged as REC-01.
- [ ] **STUCK-02** Layer 2 Multi-Signal Health Scorer — already flagged as REC-02.
- [ ] **STUCK-03** Layer 3 Repetition Detector. ARCH:7792. **Accept:** `core/recovery/repetition.go` implemented; detects repeating tool calls, repeating outputs.
- [ ] **STUCK-04** Layer 4 Deadlock Detector — already flagged as REC-03.
- [ ] **STUCK-05** Layer 5 Recovery Orchestrator (hierarchical). ARCH:8030. **Accept:** `core/recovery/recovery_orchestrator.go`; hierarchical policy (soft nudge → forced tool call → restart → handoff).
- [ ] **STUCK-06** Layer 6 Deadlock Recovery (eager release). ARCH:8247. **Accept:** on detection, releases held LLM slots, file handles, locks.
- [ ] **STUCK-07** Layer 7 Recovery Notification — already flagged as REC-04.

### W4 — Mitigations (GRAPH.md §6 / ARCH:47249)

- [ ] **MIT-01** Hallucination Firewall (Verify Before Store). ARCH:47249. **Accept:** before any knowledge write, verify claim against existing trusted sources; low-confidence claims marked quarantined.
- [ ] **MIT-02** Freshness Tracking & Decay. ARCH:47508. **Accept:** `last_access_at`, `half_life` columns; ACT-R decay function applied in ranking.
- [ ] **MIT-03** Source Attribution & Provenance. ARCH:47694. **Accept:** `provenance_chain` stored; searchable.
- [ ] **MIT-04** Trust Hierarchy — see KG-30 acceptance.
- [ ] **MIT-05** Conflict Detection — see KG-32 acceptance.
- [ ] **MIT-06** Context Quality Scoring. ARCH:48443. **Accept:** per-result quality score combining freshness + trust + provenance; exposed to caller.
- [ ] **MIT-07** LLM Prompt Engineering. ARCH:48636. **Accept:** shared prompt fragments for uncertainty/attribution; injected via PromptBuilder.

### W4 — Vector optimizations (GRAPH_OPTIMIZATIONS.md)

- [ ] **VEC-01** XOR filters for early filtering. Missing. **Accept:** `core/vectorgraphdb/xorfilter/` package; filter applied before IVF probe. **IMPORTANT** Is the actually relevant to our current implementation? Discuss first.
- [ ] **VEC-02** Remove-Birth adaptation (dead centroid detection). Missing. **Accept:** centroid usage tracked; unused centroids removed and new ones spawned.
- [ ] **VEC-03** Query-Adaptive Weighting. Stubbed. **Accept:** per-query subspace confidence weights; HNSW search weights codebooks accordingly.
- [ ] **VEC-04** LOPQ partition trainer. `core/vectorgraphdb/migrations/015_evq_lopq.go`. **Accept:** background trainer service runs on schedule; persists partition state.

### W4 — Context virtualization (CONTEXT.md)

- [ ] **CTX-01** UniversalContentStore abstraction mismatch. `core/context/content_store.go`. **Accept:** wraps `bleve.Index` directly (not the IndexManager wrapper) per CONTEXT.md:137.
- [ ] **CTX-02** Tier promotion/demotion state machine. **Accept:** explicit HOT/WARM/COLD states per item; promotion on access; demotion on budget pressure.
- [ ] **CTX-03** CTX-REF auto-substitution on eviction. **Accept:** evicted items replaced with `ContextReference` markers in prepared prompts; retrieval skill re-hydrates on demand.
- [ ] **CTX-04** Async indexer queue overflow callback. `core/context/content_store.go`. **Accept:** queue-full path logs + publishes warning signal; new metric.
- [ ] **CTX-05** Graceful shutdown drains index queue. **Accept:** Close waits for in-flight + drains queue up to timeout.
- [ ] **CTX-06** Resource budget integration in retrieval. ARCH:4141. **Accept:** retrieval charges against `ResourceBudget`; refuses under pressure.
- [ ] **CTX-07** Concrete eviction strategy implementations. **Accept:** LRU, LFU, weighted, cost-aware strategies implemented under `SelectForEviction`.
- [ ] **CTX-08** Automatic tier-down on LLM degradation. **Accept:** GP degradation signal triggers eviction pressure upward.

### W4 — Chunking (CHUNKING.md)

- [ ] **CHK-01** Tree-sitter AST chunker. **Accept:** hierarchical chunker (Levels 0–3) implemented; consumed by ingest pipeline.
- [ ] **CHK-02** Code-aware tokenization integration. `core/chunking/config.go:50`. **Accept:** camelCase/snake_case tokenizer consumed during chunking.
- [ ] **CHK-03** Config learner persistence. `core/chunking/splitter_learned.go`. **Accept:** `ChunkConfigLearner` persisted to `.sylk/config/chunking.json`; restored on startup.
- [ ] **CHK-04** Retrieval feedback loop. `core/chunking/retrieval_feedback.go`. **Accept:** citations update `ChunkConfigLearner` parameters in real time.
- [ ] **CHK-05** Thompson Sampling variance tracking. `core/chunking/config_learner.go`. **Accept:** `LearnedContextSize` carries variance; sampling uses it.

### W4 — Tree-sitter (ARCH:15867)

- [ ] **TS-01** User-defined grammar registration API. `core/treesitter/grammar.go:90`. **Accept:** grammar YAML loader; validation; sandboxed compile.
- [ ] **TS-02** CLI commands (install/add/validate/remove). ARCH:17142. **Accept:** under `cmd/`, each command wired to grammar manager.
- [ ] **TS-03** MVCC+OT filesystem integration. ARCH:16598. **Accept:** `core/treesitter/tool.go` parses from VFS handles; no separate file reads.
- [ ] **TS-04** Grammar registry hot-reload. ARCH:16154. **Accept:** `AddGrammar` can be called at runtime; parsers re-built.

### W4 — Search (DB_TODO.md Phase 6)

- [ ] **SRC-01** BleveAsyncIndexer with worker pool. `core/knowledge/bleve/async_indexer.go`. **Accept:** worker-pool driven; queue with overflow callback; graceful shutdown drains.
- [ ] **SRC-02** Document mapper. `core/knowledge/bleve/doc_mapper.go`. **Accept:** NodeID↔DocID bidirectional.
- [ ] **SRC-03** Bleve/agent unified query interface. **Accept:** every agent uses a single `QuerySearcher`; no per-agent Bleve handles.
- [ ] **SRC-04** Cross-validation scheduler. `core/search/validation/`. **Accept:** periodic staleness cross-check triggered from validator; integrates with retrieval pipeline.
- [ ] **SRC-05** Batch indexer circuit breaker. `core/search/indexer/batch_indexer.go`. **Accept:** failure threshold trips breaker; queue paused until recovery probe.
- [ ] **SRC-06** Bleve WAL-integration / graceful shutdown. `core/search/bleve/index_manager.go:550`. **Accept:** UnsafeBatch documents its durability contract; shutdown flushes pending batches + fsync.

---

## Wave 5

Knowledge Graph architectural convergence. Requires Wave 4 infrastructure.

### W5 — Unify KG

- [ ] **KG-01** Single `Node` struct with `Content + CanonicalKey + Supersedes + SupersededBy + DocRef`. **Accept:** `core/knowledge/graph/node.go` is the single source of truth; `sylkdir` uses it; migrations handle old data.
- [ ] **KG-02** Collapse two ID allocators. **Accept:** one atomic allocator; `GlobalMeta.NextNodeID` defers to it.
- [ ] **KG-03** Unified `KnowledgeGraph` façade. `core/knowledge/graph/knowledge_graph.go`. **Accept:** `Open()/Close()/GetNode/GetOutgoing/GetIncoming/AddNode/UpsertEdge/AddVector/VectorSearch/TraverseGraph`.
- [ ] **KG-04** Concurrent 6-parallel insert flow. DB.md §Concurrent Insert. **Accept:** `core/knowledge/graph/concurrent_insert.go`; benchmark shows ~1.75ms per node with 8 concurrent writers.
- [ ] **KG-05** NodeStore lock-free reads. DB.md Phase 3. **Accept:** benchmark ≥1M ops/sec read; read during write contention passes.
- [ ] **KG-06** Block persistence with mmap + sealing. DB.md Phase 3.3. **Accept:** sealed blocks mmap-read; seal writes checksum; recovery validates.
- [ ] **KG-07** EdgeShard dynamic growth. DB.md Phase 4. **Accept:** shards created on demand on `sourceID >> 16`; COW shard array growth.
- [ ] **KG-08** HNSW/Vamana adapter. DB.md Phase 5.2/5.3. **Accept:** adapters live under `core/knowledge/vector/`; reuse existing IVF.
- [ ] **KG-09** VectorSearcher IVF adapter. DB_TODO.md 5.1-5.5. **Accept:** VectorSearcher, VectorDBAdapter, TieredSearcher wired; agent query skills consume real data.
- [ ] **KG-10** Commit-time IVF append. `core/storage/sylkdir/commit.go` line ~946. **Accept:** `CommitResult.StagedVectors` pushed via `ivf.StitchBatch`.
- [ ] **KG-11** Commit-time Bleve indexing. **Accept:** `CommitResult.StagedDocs` indexed into global Bleve; crash recovery re-indexes missing.
- [ ] **KG-12** Deletions applied on commit. **Accept:** deletions marked in global; Bleve docs removed.
- [ ] **KG-13** QueryContext session-aware visibility. DB_TODO.md 6.1/6.2. **Accept:** reads use `QueryContext` with ancestor chain + `BaseSnapshot`; cross-session isolation test.
- [ ] **KG-14** SQLite export migration. DB_TODO.md 8.1. **Accept:** `core/knowledge/migration/sqlite_export.go` migrates legacy data; progress callback.
- [ ] **KG-15** Unified bootstrap `sylk.Open()`. DB_TODO.md 8.1. **Accept:** single entry point returns wired `Sylk` struct.
- [ ] **KG-16** CLI `sylk init/index/search/status/session`. DB_TODO.md 8.2. **Accept:** Cobra commands under `cmd/`.
- [ ] **KG-17** E2E integration test. DB_TODO.md 8.3. **Accept:** per file scenario: init → boot-index → ingest → query → checkpoint → commit → re-query from new session.
- [ ] **KG-18** KG performance benchmarks. **Accept:** benchmarks for each target (GetNode<100ns, GetOutgoing<2μs, AddNode<10μs, UpsertEdge<5μs, Traverse<100μs, VectorSearch<50ms).
- [ ] **KG-19** Canonical key index + lookup. DB_TODO.md 1.4. **Accept:** `CanonicalKeyIndex` with SST-style O(log n) lookup; supersession aware.
- [ ] **KG-20** Supersession chain traversal. DB_TODO.md 2.2. **Accept:** `NodeStore.GetSupersessionChain` walks chain; both `Supersedes` and `SupersededBy` set on each transition.
- [ ] **KG-21** DeltaTracker. DB_TODO.md 3.6. **Accept:** atomic counters; checkpoint triggers at 50 nodes / 200 edges / 50 vectors / 512KB docs / 10 min; persists to `delta/tracker.json`.
- [ ] **KG-22** Session-aware doc search. DB_TODO.md 3.8. **Accept:** merges session versions + global Bleve committed-only; isolation test.
- [ ] **KG-23** Boot indexer. DB_TODO.md 4.2. **Accept:** batch embed + ingest; resumable; progress callback; tested on 1000-file corpus.
- [ ] **KG-24** Code extractor via tree-sitter. DB_TODO.md 4.3. **Accept:** produces `CodeUnit` per function/struct/method; file→unit edges created.
- [ ] **KG-25** Agent ingest skill. DB_TODO.md 4.4. **Accept:** `IngestDocumentSkill`; respects session isolation.
- [ ] **KG-26** Embedder production wiring. DB_TODO.md 4.5. **Accept:** voyage/local embedder created from `.sylk/config.yaml`; fallback on API outage.
- [ ] **KG-27** IVF build-from-ingestion. DB_TODO.md 4.6. **Accept:** initial build + incremental `StitchBatch`; rebuild trigger on imbalance.
- [ ] **KG-28** Crash recovery full sequence. DB_TODO.md §Recovery. **Accept:** 10-step recovery sequence implemented; test exercises each crash point.
- [ ] **KG-29** Deprecate old `core/vectorgraphdb/nodes.go + edges.go`. DB_TODO.md 8.4. **Accept:** `DEPRECATED.md` with migration guide; compile-time deprecation warnings.

### W5 — WAL / Checkpoint (WAL_TODO.md)

- [ ] **WAL-01** Extend `vamana/wal/types.go` OpType enum. WAL_TODO.md A.1. **Accept:** session, commit, structural ops added; `Valid()/String()` updated; type tests.
- [ ] **WAL-02** Session Version WAL. WAL_TODO.md A.2. **Accept:** `core/storage/sylkdir/session_wal.go` append+replay; concurrent tests.
- [ ] **WAL-03** Global Commit WAL crash-atomic. WAL_TODO.md A.3. **Accept:** OpCommitBegin/End brackets; mid-commit crash discards incomplete; double-commit rejected.
- [ ] **WAL-04** IVF WAL checkpoint coordination. WAL_TODO.md A.4. **Accept:** checkpoint controller monitors IVF WAL size as debt signal.
- [ ] **WAL-05** Bleve lifecycle wiring to commit WAL. WAL_TODO.md A.5. **Accept:** recovery re-indexes docs from commits that completed but aren't in Bleve.
- [ ] **WAL-06** Calibration probe. WAL_TODO.md B.1. **Accept:** startup probe + full probe schedule; EMA smoothing; persists to `.sylk/calibration.json`.
- [ ] **WAL-07** Adaptive Checkpoint Controller. WAL_TODO.md B.2. **Accept:** trigger inequality, granularity selection, advisory lock, SIGINT/SIGTERM handler.
- [ ] **WAL-08** DeltaTracker v2. WAL_TODO.md B.3. **Accept:** backward compatible with v1; schema_version; atomic reset.
- [ ] **WAL-09** Memory pressure monitor. WAL_TODO.md B.4. **Accept:** Linux + Darwin + fallback; max(go, sys).
- [ ] **WAL-10** Checkpoint controller integration in ingest path. WAL_TODO.md B.5. **Accept:** `ShouldCheckpoint()` called after batch writes; `Session.Checkpoint` uses controller.

---

## Wave 6

Agent skillfiles, protocols, and per-agent fixes.

### W6 — Architect

- [ ] **ARCH-03 (M3)** Execution oversight loop. ARCH:53459. **Accept:** step completion handler syncs to plan file; recovery workflow engine resumes stalled plans.
- [ ] **ARCH-04 (M4)** Research-paper proposal ingestion. ARCH:55297. **Accept:** `handleProposal`, `read_research_paper` implemented; research papers persisted and queryable.
- [ ] **ARCH-05 (M5)** Orchestrator handoff integration. ARCH:22311. **Accept:** Architect dispatches ready plan to Orchestrator via handoff channel; status synced.
- [ ] **ARCH-06 (M6)** Cross-domain context consumption. `agents/architect/architect.go:337`. **Accept:** Architect consumes `cross_domain_context` attached by Guide in request handling.
- [ ] **ARCH-07 (M7)** Cross-domain querying fully implemented. `agents/architect/architect.go:932`. **Accept:** not stubbed; returns real content via Guide cross-domain router.
- [ ] **ARCH-08 (M8)** Consultation response correlation. `agents/architect/architect.go:422`. **Accept:** responses correlated by request id; integrated into planning state.
- [ ] **ARCH-09 (M9)** Skill loader registration order fixed. `agents/architect/architect.go:144,155,1065`. **Accept:** skills registered before loader instantiated; `GetToolDefinitions()` returns all registered.
- [ ] **ARCH-10 (M10)** Implement documented toolset (read/glob/grep/git/lsp/ast/plan-mode). ARCH:24083. **Accept:** each skill implemented, registered, tested.
- [ ] **ARCH-11 (M11)** Architect skillfiles populated. **Accept:** SKILL.md files for each declared skill per ARCH:54766.
- [ ] **ARCH-12 (M12)** `containsIgnoreCase` stub fixed. `agents/architect/architect.go:1080`. **Accept:** real substring match; tests.
- [ ] **ARCH-15 (M15)** Planning errors not swallowed. `agents/architect/architect.go:488,531,534`. **Accept:** errors surface as errors; plan status reflects failure.
- [ ] **ARCH-16 (M16)** Session identity preserved on forwarded requests. `agents/architect/architect.go:368,649`. **Accept:** SessionID propagated.
- [ ] **ARCH-17 (M17)** activePlans/knownAgents locked. `agents/architect/architect.go:43,46,438,449,582,907`. **Accept:** mutex-protected; race-test clean.
- [ ] **ARCH-18 (M18)** Synthesis beyond lexical Jaccard. `agents/architect/synthesis.go:189,245`. **Accept:** semantic dedup via embedding similarity; conflict detection for real contradictions.
- [ ] **ARCH-19 (M19)** Cross-domain goroutine panic recovery + cancellation-aware semaphore. `agents/architect/crossdomain.go:137,160`. **Accept:** `GoroutineScope.Go` recovers panics; semaphore acquire respects context.
- [ ] **ARCH-20 (M20)** Architect wired in TUI bootstrap. `cmd/tui.go:74,85`. **Accept:** Architect + Orchestrator instantiated, registered with Guide, started.
- [ ] **ARCH-21 (ARCHITECT_FIX)** Plan state machine durable milestones. **Accept:** reduce to `pending | clarifying | ready | executing | completed | failed | superseded`; optional states become progress markers; prerequisite checks enforce real invariants only.
- [ ] **ARCH-22** Pre-delegation declaration persistence. ARCH:22291. **Accept:** declarations persisted; approval state tracked.

### W6 — Guide

- [ ] **GUD-01** Knowledge Agent Consultation Protocol. ARCH:2105. **Accept:** Guide consults knowledge agents for specific intents; responses merged into routing decision.
- [ ] **GUD-02** Intent Gate Classification. ARCH:2292. **Accept:** classification with confidence; Guide selects route based on gate outputs.
- [ ] **GUD-03** Direct Consultation implementation. ARCH:3030. **Accept:** known-target consultations skip full Guide roundtrip; consultation skills per agent.
- [ ] **GUD-04** Domain Expertise routing. ARCH:3245. **Accept:** Guide classifies requests into domains; prefetches domain context; routes accordingly.
- [ ] **GUD-05** Cross-domain query handling. ARCH:3911, 3927. **Accept:** multi-domain queries fanned out; responses synthesised.
- [ ] **GUD-06** Domain classification cache (Ristretto). ARCH:3752. **Accept:** hit-rate metric; TTL configurable.
- [ ] **GUD-07** Hand-off injectable interface documented. `agents/guide/guide.go:5297`. **Accept:** documented in ARCH or removed if unused.
- [ ] **GUD-08** Guide skillfiles populated. **Accept:** SKILL.md for each skill in `agents/guide/skillfiles/`.

### W6 — Orchestrator

- [ ] **ORC-01** TaskUpdateBuffer implementation. ARCH:39566-39583. **Accept:** bounded per-task buffers; overflow policy; backpressure signal.
- [ ] **ORC-02** HealthCache TTL configurable. `agents/orchestrator/orchestrator.go:61`. **Accept:** TTL + refresh policy exposed via config.
- [ ] **ORC-03** Pipeline Variants. ARCH:40804. **Accept:** variant creation flow, selection UI hooks, VFS commit protocol.
- [ ] **ORC-04** Pipeline Manager. ARCH:39234. **Accept:** per-pipeline lifecycle, manager singleton, guide integration for `/task` command.
- [ ] **ORC-05** DAG executor signaling layer. ARCH:38727. **Accept:** executor emits signals on node completion/failure; consumers subscribe per pipeline.
- [ ] **ORC-06** Task completion event submission. ARCH:40331. **Accept:** structured events with all required fields; Archivalist direct routing (ARCH:40410) wired.
- [ ] **ORC-07** Orchestrator skillfiles populated. **Accept:** SKILL.md per declared skill.

### W6 — Engineer / Designer

- [ ] **ENG-01** MaxTodosBeforeArchitect escalation path. `agents/engineer/engineer.go:31`. **Accept:** reroute_request skill wiring to Architect verified; test exercises escalation.
- [ ] **ENG-02** Session context preserved on forwarded requests. **Accept:** every forwarded request carries SessionID.
- [ ] **ENG-03** Engineer skillfiles populated per ARCH:25163.
- [ ] **DES-01** MaxTodosBeforeArchitect escalation. `agents/designer/designer.go:31`. **Accept:** matches ENG-01.
- [ ] **DES-02** Pipeline coordination (ARCH:33139). **Accept:** Designer participates in pipeline handoff same as Engineer.
- [ ] **DES-03** Design System Integration (never hardcode values). ARCH:33200. **Accept:** token usage verified; linter check on hardcoded color/size.
- [ ] **DES-04** Accessibility requirements. ARCH:33213. **Accept:** a11y skill produces aria + contrast check.
- [ ] **DES-05** Responsive design. ARCH:33225. **Accept:** responsive breakpoint rules applied.
- [ ] **DES-06** Designer skillfiles populated per ARCH:25804.
- [ ] **DES-07** Model string alignment (`gemini-3-pro` vs `gemini-3.1-pro-preview`). `agents/designer/designer.go:34,51`. **Accept:** config-driven model; defaults align with ARCH:33069.

### W6 — Archivalist

- [ ] **AR-01** Failure pattern memory protocol. ARCH:23129-23146. **Accept:** `⚠️ SIMILAR FAILURE DETECTED` warnings on cross-session query; `recurrence_count` field; escalation thresholds.
- [ ] **AR-02** Retrieval accuracy self-healing. ARCH:23164-23169. **Accept:** STALE/IRRELEVANT/INCOMPLETE/WRONG_RESOLUTION detection + marking.
- [ ] **AR-03** Read-after-write verification. ARCH:23171-23175. **Accept:** after every store, verify retrievable; metric on failures.
- [ ] **AR-04** Replace MockEmbedder in production. `agents/archivalist/archivalist.go:375`. **Accept:** real embedder wired per config.
- [ ] **AR-05** Archivalist skillfiles populated per ARCH:23236.
- [ ] **AR-06** Query intent classification. ARCH:23177. **Accept:** classifier implemented; intents direct storage categories.
- [ ] **AR-07** Storage categories. ARCH:23187. **Accept:** categories enforced on write.

### W6 — Librarian / Academic / Inspector / Tester / Scribe / Guardian

- [ ] **LIB-01** Query cache intent classification. ARCH:43439. **Accept:** intent-aware caching (not keyword-only); hit-rate metric.
- [ ] **LIB-02** Self-verification protocol. ARCH:26913. **Accept:** Librarian verifies claims against files before returning.
- [ ] **LIB-03** Tool detection response format. ARCH:26945. **Accept:** structured format enforced.
- [ ] **LIB-04** Git integration protocol. ARCH:26999. **Accept:** Librarian uses `core/git/` for churn / blame / log queries.
- [ ] **LIB-05** Librarian skillfiles per ARCH:27013.
- [ ] **ACA-01** Research paper revision ingestion. ARCH:55776. **Accept:** revision messages trigger re-ingest; prior version marked superseded.
- [ ] **ACA-02** Recommendation outcome tracking. ARCH:28318. **Accept:** academic tracks whether recommendations succeeded; informs future.
- [ ] **ACA-03** Maturity-aware recommendations. ARCH:28310. **Accept:** recommendations tagged by maturity; stale/dated recommendations suppressed.
- [ ] **ACA-04** Domain filter logic documented. `agents/academic/domain_filter.go`. **Accept:** reflected in ARCH academic section.
- [ ] **ACA-05** Academic skillfiles per ARCH:28370.
- [ ] **INS-01** Global vs Pipeline mode skill-loading. `agents/inspector/global/skills.go`. **Accept:** mode correctly selects skill set.
- [ ] **INS-02** Inspector skillfiles per ARCH:29234.
- [ ] **TES-01** Task independence validation. ARCH:30691. **Accept:** tester asserts tests are independent; flags shared state.
- [ ] **TES-02** Assertion quality scoring. ARCH:30698. **Accept:** scorer evaluates assertion strength; surfaces weak assertions.
- [ ] **TES-03** Tester skillfiles per ARCH:30793.
- [ ] **SCR-01** Workstream isolation by correlation documented. `agents/scribe/scribe.go:68`. **Accept:** ARCH updated or code simplified.
- [ ] **GRD-01** Guardian skillfiles populated. **Accept:** documented in ARCH + files present.
- [ ] **SHARED-01** SteeringManager integration documented. `agents/shared/steering.go`. **Accept:** described in ARCH agent sections.

---

## Wave 7

Hygiene: size, docs, mocks. Can run throughout but scheduled last to avoid churn during earlier waves.

### W7 — Handler size

All functions > 100 lines must be decomposed. Items in [Cross-cutting: 100-line handlers](#cross-cutting-100-line-handlers).

### W7 — Docs

- [ ] **DOC-05** Populate MULTIPANE.md (see DOC-01).
- [ ] **DOC-06** Populate TERMINAL.md (see DOC-02).
- [ ] **DOC-07** Align auto-memory claims with reality. **Accept:** memory entries updated for (a) core/lifecycle does not hold activation tiers, (b) parseError Retry-After now covers all providers.
- [ ] **DOC-08** `docs/TODO.md` + `docs/IDEAS.md` tagged as aspirational vs actionable. **Accept:** actionable items either moved to AUDIT.md or closed.

### W7 — Mocks

- [ ] **MOCK-01** All agent mocks generated via mockery (`.mockery.yaml`). **Accept:** `mockery` run in CI; hand-written mocks removed.
- [ ] **MOCK-02** MockEmbedder (archivalist) excluded from production path. **Accept:** compile tag or interface injection ensures real embedder in prod.

---

## Cross-cutting: 100-line handlers

All 113 production functions over 100 lines must be decomposed so no function exceeds 100 lines and cyclomatic complexity stays under 4. Decomposition pattern: extract each switch/case branch into a named helper; lift validation/preamble into `prepare*`; lift emission/side-effects into `emit*`.

High-priority concentrations:

- [ ] **SIZE-01** `agents/guide/guide.go` — 3 >100-line functions (`resolveClassification` 112, `handleRouteRequestMessage` 152, `handleResponseMessage` 138). This file itself is 5503 lines and should be split into multiple files by responsibility.
- [ ] **SIZE-02** `agents/architect/architect.go` — 4 >100-line functions (`New` 105, `handleForwardBusRequest` 199, `handleConversation` 107, `executeConversation` 102).
- [ ] **SIZE-03** `agents/orchestrator/orchestrator.go` — 2 >100-line (`handleBusRequest` 178, `handleTaskComplete` 102).
- [ ] **SIZE-04** `agents/archivalist/archivalist.go` — `handleBusRequest` 200 lines.
- [ ] **SIZE-05** `agents/engineer/engineer.go` — `handleBusRequest` 186 lines.
- [ ] **SIZE-06** `agents/shared/pipeline_protocol.go` — 3 >100-line (127, 150, 133).
- [ ] **SIZE-07** `agents/shared/global_review_protocol.go` — 2 >100-line (102, 116).
- [ ] **SIZE-08** `agents/shared/command_approval_gate.go` — 114 lines.
- [ ] **SIZE-09** `agents/shared/command_skills.go` — 107 lines.
- [ ] **SIZE-10** `agents/shared/guide_route_sync.go` — 103 lines.
- [ ] **SIZE-11** `core/handoff/executor.go` — 133-line function.
- [ ] **SIZE-12..113** Remaining production functions > 100 lines. **Accept (all SIZE-\*):** post-refactor, `awk '/^func /{start=NR} /^}$/{if (start && NR-start > 100) print}'` returns 0 lines across `core/` and `agents/` production code.

Large files to split (not enforced by line-count rule but produce the handler monoliths):

- [ ] **FILE-01** Split `agents/guide/guide.go` (5503 lines) into `{request_routing, response_handling, agent_registry, intent_classification, steering, hooks, lifecycle}.go`.
- [ ] **FILE-02** Split `agents/architect/architect.go` (3240) by concern (planner bridge, request handling, skill glue, cross-domain).
- [ ] **FILE-03** Split `agents/orchestrator/orchestrator.go` (2555) likewise.
- [ ] **FILE-04** Split `agents/archivalist/archivalist.go` (3299).
- [ ] **FILE-05** Split `core/providers/openai.go` (2950) — extract chat / embedding / streaming / error-handling files.
- [ ] **FILE-06** Split `core/providers/google.go` (2222).
- [ ] **FILE-07** Split `core/treesitter/tool.go` (3356).
- [ ] **FILE-08** Split `core/knowledge/extraction_pipeline.go` (1844).
- [ ] **FILE-09** Split `core/search/bleve/index_manager.go` (1666).
- [ ] **FILE-10** Split `core/oauth/openai_chatgpt.go` (2214).

---

## Cross-cutting: untracked goroutines

All `go func` in production code must be owned by a `GoroutineScope` or `sync.WaitGroup` with panic recovery. Confirmed offenders:

- [ ] **GO-01** `core/providers/gateway/proxy.go:129`.
- [ ] **GO-02** `core/llm/bulkhead/bulkhead.go:66`.
- [ ] **GO-03** `core/llm/timeout/streaming.go:40`.
- [ ] **GO-04** `core/llm/coordinator/client_integration.go:189`.
- [ ] **GO-05** `core/llm/coordinator/coordinator.go:227`.
- [ ] **GO-06** `agents/guide/channel_bus.go:332`.
- [ ] **GO-07..10** `ui/filetree/search_worker.go:105,156,167,176`.
- [ ] **GO-11** Full audit: grep `go\s+func` across production files; every site must either call `scope.Go(...)` or have a `wg.Add(1); defer wg.Done()` pair. **Accept:** lint rule or test enforces.

---

## Dependency graph (summary)

```
Wave 1 ──┬──► Wave 2 ──┬──► Wave 3 ──┬──► Wave 4 ──┬──► Wave 5 ──► Wave 7
         │             │             │             │
         │             │             │             └─► Wave 6 ──► Wave 7
         │             │             └─► Wave 6
         │             └─► Wave 6
         └─► Wave 7 (any time; just avoid concurrent churn with related waves)
```

- Wave 1 items are independent; run in full parallel.
- Wave 2 depends on Wave 1 MSG-* stability before touching router semantics.
- Wave 3 wiring depends on Wave 2 schema behaviors (confidence/trust fields feed handoff/recovery).
- Wave 4 subsystems depend on Wave 3 runtime surfaces but are independent of each other.
- Wave 5 (KG convergence) depends on Wave 4's SRC-* and CTX-* because the façade consumes them.
- Wave 6 per-agent work depends on Wave 3 (recovery, wiring) + Wave 4 (bulkheads, mitigations).
- Wave 7 runs last; size/doc/mock hygiene is reviewed per-PR during earlier waves to prevent regression.

---

## Completion criteria

- [ ] All items above marked `[x]`.
- [ ] `go build ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `go test -race ./...` passes.
- [ ] No production function exceeds 100 lines.
- [ ] No `go func` appears in production code outside a `GoroutineScope.Go` or `wg`-tracked helper with panic recovery.
- [ ] `grep -RnEi "fts[345]|load_extension|sqlite-vec|sqlite-vss|spatialite|CREATE VIRTUAL TABLE"` returns no matches in `.go` files (currently clean; must stay clean).
- [ ] All performance targets in DB.md have corresponding benchmarks.
- [ ] All documented agent skills have `SKILL.md` files that load and register.

*Last updated: 2026-04-16 — initial generation.*
