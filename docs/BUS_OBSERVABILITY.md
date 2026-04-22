# Bus Observability

The inter-agent message bus (`guide.ChannelBus`) is the substrate for every cross-agent interaction in sylk: route requests, forwarded work, responses, streaming events, registry announcements, signals, and typed coordination messages all flow through it. Prior to this work the bus was opaque — there was no way to observe which agent published what, who consumed it, or where queues overflowed without scraping per-agent logs and guessing at ordering.

Bus Observability (`core/buslog`) fixes that by capturing every bus event into a session-global append-only JSONL log.

## Architecture

```
┌────────────────────────────────────────────────────────────────────┐
│                       guide.ChannelBus                             │
│  Publish  ──┐                                                      │
│  Subscribe  │  ┌─────────────────────────────────────────┐         │
│  Unsubscribe├──▶  BusEventHook (interface, one per bus) │         │
│  Overflow   │  │  - OnPublish                            │         │
│  Expired    │  │  - OnDelivery                           │         │
│  Delivery   │  │  - OnSubscribe / OnUnsubscribe          │         │
│             │  │  - OnOverflow / OnExpired               │         │
│             │  └──────────────┬──────────────────────────┘         │
└─────────────┘                 │                                    │
                                ▼
                  ┌──────────────────────────┐
                  │ buslog.BusEventHook      │ (adapter)
                  │ → logger.Publish(...)    │
                  │ → logger.Delivery(...)   │
                  │ → logger.Subscribed(...) │
                  │ ...                      │
                  └────────────┬─────────────┘
                               │
                               ▼
                ┌─────────────────────────────┐
                │ buslog.BusLogger            │ (async, bounded)
                │ - 8192 record buffer        │
                │ - drain goroutine           │
                │ - agentlog.StreamWriter     │
                │   64 MiB + daily rotation   │
                │ - periodic drop records     │
                │ - subscriber fanout API     │
                └────────────┬────────────────┘
                             │
                             ▼
    .sylk/sessions/{sid}/bus/{YYYY-MM-DD}/bus.{nnn}.jsonl
```

## Record kinds

Seven record kinds model the bus's full surface:

| Kind             | Emitted on                                                   | Notable fields |
|------------------|--------------------------------------------------------------|----------------|
| `bus_publish`    | every `ChannelBus.Publish` call                              | topic, subscriberCount, payloadBytes, publisher, correlation_id |
| `bus_delivery`   | every successful delivery to a subscriber                    | publishTopic, subscriberTopic, subscriberID, queueLatencyMicros, handlerErr |
| `bus_subscribe`  | subscription registration                                    | topic, subscriberID, async, wildcard, matchPattern |
| `bus_unsubscribe`| subscription removal                                         | topic, subscriberID, duration |
| `bus_overflow`   | subscription queue drop                                       | topic, dropped, totalDropped, correlations, queueRemaining |
| `bus_expired`    | Publish rejected because the message's Deadline/TTL passed   | topic, message, expiredAt |
| `bus_drop`       | buslog itself dropped a record under backpressure            | reason, droppedCount |

`bus_delivery` is the join record — together with `bus_publish`, it answers "who actually received what I published, and how long did it sit in the queue?"

## Correctness

- **Per-session monotonic `seq`.** Strictly increasing per session; cross-agent ordering is exact even under concurrent publishes. Verified by `TestBusLogger_SeqMonotonicUnderConcurrency` (8 goroutines × 100 writes).
- **Self-contained records.** Publisher, target, correlation ID, message type, priority, and attempt are captured inline on every record. Analysis tools do not need to join against per-agent logs.
- **Redaction at the boundary.** Records pass through `agentlog.Redactor` before write.

## Robustness

- **Async bounded buffer.** Publishes, deliveries, and subscription lifecycle never block on disk I/O. Buffer overflow emits periodic `bus_drop` records so lost observability is visible, not silent.
- **Panic-safe hooks.** Every hook invocation is wrapped in `defer recover()` inside `ChannelBus.invoke*Hook`. A panicking subscriber cannot take the bus down.
- **Bus stays on fast path.** When no hook is installed (typical for tests), all `invoke*Hook` calls short-circuit via an atomic nil-check — zero allocation, zero indirection.
- **Close drains.** `BusLogger.Close()` drains the buffer and flushes the writer. Hooked into orchestrator `Stop`.

## Wiring

1. **`agents/orchestrator/bus_install.go` — `installBusObservability`** constructs a `BusLogger` at `sd.SessionBusPath(sessionID)`, wraps it in a `buslog.BusEventHook`, and installs it via `ChannelBus.SetEventHook`.
2. **Called from `orchestrator.Start`** right after `o.bus = bus`, before the orchestrator's first `SubscribeAsync`. Every subsequent subscription and publish is captured.
3. **`uninstallBusObservability(bus)`** in `orchestrator.Stop` clears the hook, drains the logger, and closes the writer.

## Guide WAL

Companion fix: the guide's `SessionEventLogger` is now configured at construction with `SessionLoggerConfig{EnableBusStream: true, Redactor: …}` and bound eagerly from `cmd/tui.go` at session creation. The guide also gains a `BindSession` method that emits a synthetic `EventGuideStarted` entry so:

- `.sylk/sessions/{sid}/agents/guide/wal/routing.wal.000001` is always populated
- `.sylk/sessions/{sid}/agents/guide/logs/{date}/events.000.jsonl` is populated from the first bind
- `.sylk/sessions/{sid}/agents/guide/logs/{date}/bus.000.jsonl` captures the guide's per-agent bus activity stream

The guide already emits 8 types of events (`EventRouteClassified`, `EventRouteCacheHit`, `EventRerouted`, `EventAgentRegistered`, `EventForwardDispatched`, `EventResponseCorrelated`, `EventStreamStarted`, `EventStreamCompleted`); the fix was getting the logger configured + bound so those emissions reach disk.

## CLI

```
sylk trace bus summary                   # counts by kind, top topics, publishers, subscribers
sylk trace bus topics                    # per-topic publish + subscriber counts
sylk trace bus agent <agent-id>          # one agent's bus footprint
sylk trace bus follow <correlation-id>   # publish → delivery chain
sylk trace bus overflows                 # every subscription queue overflow
```

`--session <id>` picks a specific session; omitted, the CLI infers the most-recently-modified session that has a bus log.

## Storage impact

Rough order of magnitude: a moderately active session with 5 agents produces 200–2000 bus records per minute. With 64 MiB rotation and daily segmentation, expect 10–100 MB per 8-hour session. Tiny relative to the fabric observability log.
