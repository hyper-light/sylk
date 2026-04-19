# Hot-Swappable Dependencies: The Lookup Discipline

## Rule

Any agent component (sub-component, helper, internal type) that depends on a
hot-swappable external dependency — most commonly the LLM provider, but
also any value that can change after the agent is constructed — **MUST
access that dependency via a lookup function, not a stored value**.

```go
// WRONG
type Synthesizer struct {
    provider archivalistProvider // snapshot — goes stale on SetProvider
}

// RIGHT
type Synthesizer struct {
    providerLookup func() archivalistProvider // resolved per-call
}
```

## Why the snapshot pattern is structurally wrong

Agents are constructed before their dependencies are guaranteed to be
ready. The LLM provider is typically wired *after* `agent.New()` returns,
once authentication completes. A sub-component constructed during init
that captures `cfg.Provider` as a value field gets nil at construction,
and never sees the live provider again unless the owning agent explicitly
rebuilds the sub-component every time `SetProvider` / `SwapModel` runs.

The "remember to rebuild" pattern works as long as every hot-swap call
site remembers every sub-component. It is a footgun: every new
sub-component added later has to be remembered in *every* swap path.
Forgetting one produces the
[`synthesis: no LLM provider configured`](../archivalist/synthesis.go) class
of bug — the sub-component advertises a capability but cannot deliver,
and the failure surfaces from the deepest layer with a confusing message.

The lookup pattern eliminates the bug class structurally: there is no
snapshot to go stale, so there is nothing to remember.

## Sentinel error

When a lookup returns nil (the dependency hasn't been wired yet — startup
race, post-init / pre-auth window), wrap the failure in
[`shared.ErrAgentNotReady`](agent_errors.go) so callers can distinguish
transient startup state from permanent misconfiguration:

```go
provider := s.providerLookup()
if provider == nil {
    return nil, fmt.Errorf("synthesis: %w: LLM provider not yet wired",
        shared.ErrAgentNotReady)
}
```

## Strict construction

A sub-component constructor that requires a lookup MUST panic if the
lookup is nil. Half-constructed components that pass nil-checks but
cannot deliver are worse than no component at all: consumers see the
component as live and call into it, then get a confusing failure from
deep inside. Refusing construction makes the bug visible at startup
rather than at first tool call.

```go
func NewSynthesizer(cfg SynthesizerConfig) *Synthesizer {
    if cfg.ProviderLookup == nil {
        panic("ProviderLookup is required (see synthesis.go for rationale)")
    }
    // ...
}
```

If the agent legitimately has no synthesizer (e.g., RAG disabled), DO
NOT construct the synthesizer at all — leave the field nil and have
consumers check for nil at the synthesizer level.

## Owning agent pattern

The owning agent provides a method that returns the live field under the
agent's existing read lock, and passes that method as the lookup:

```go
type Archivalist struct {
    runMu    sync.RWMutex
    provider archivalistProvider
}

func (a *Archivalist) lookupProvider() archivalistProvider {
    a.runMu.RLock()
    defer a.runMu.RUnlock()
    return a.provider
}

// At init:
a.synthesizer = NewSynthesizer(SynthesizerConfig{
    ProviderLookup: a.lookupProvider,
    // ...
})
```

`SetProvider` and `SwapModel` only update the agent's own field — sub-
components observe the change automatically on the next call.

## When the snapshot+rebuild pattern is acceptable

A snapshot+rebuild is acceptable ONLY when the rebuild does meaningful
work beyond refreshing the snapshot — e.g., resetting per-component
internal state that should not survive the swap.

The Guide is an example: `SwapModel` rebuilds the LLM classifier and
self-responder not only to refresh their provider but also to reset
caches and conversation stickiness that would corrupt routing decisions
made under the previous provider. In that case the rebuild is the
*primary* mechanism (state reset) and provider refresh is a side effect.

Even so, the sub-component should still gracefully handle a nil
provider for the brief startup window before the first rebuild — return
`ErrAgentNotReady` rather than panicking or returning a misleading
permanent-error message.

## Audit checklist

When adding or reviewing an agent sub-component, ask:

1. Does it hold a hot-swappable dependency as a value field?
2. If yes, is there a lookup function alternative? Use it.
3. If snapshot+rebuild is genuinely needed (state-reset semantics), is
   the rebuild wired into EVERY hot-swap path on the owning agent?
4. If the dependency can be nil at construction, does the constructor
   panic, OR does the use-site return `ErrAgentNotReady` cleanly?

## Current audit state (2026-04-19)

- **archivalist.Synthesizer**: lookup. ✅
- **archivalist.Client**: lookup. ✅
- **guide.LLMClassifier**: snapshot + rebuild (state reset semantics).
  Acceptable per "When snapshot+rebuild is acceptable" above. Keep.
- **guide.GuideResponder**: snapshot + rebuild (same rationale). Keep.
- **handoff.DirectBriefSource**: snapshot, but only constructed in
  tests today. Defer until production usage emerges.
