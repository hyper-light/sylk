package bridge

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/llm/accounting"
	"github.com/adalundhe/sylk/ui/msg"
)

const accountantBridgeName = "bridge.accountant"

// AccountantBridge subscribes directly to accounting.Accountant.Subscribe()
// and forwards each UsageDelta as a typed TokenDeltaMsg. It also emits
// periodic AccountingSnapshotMsg so the status bar can render running
// totals without maintaining its own running counter.
//
// This is the canonical token-telemetry path (FIX_ID_AND_TOKENS.md
// item 50, 52). UI token telemetry is accountant-owned; no activity-bus
// token bridge is started by the app.
type AccountantBridge struct {
	id        string
	acc       *accounting.Accountant
	cancelSub func()
	ch        <-chan accounting.UsageDelta

	done       chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	dropped    atomic.Int64
	snapshotMS time.Duration
}

// NewAccountantBridge builds the bridge bound to the given Accountant.
// Pass a nil Accountant to get a no-op bridge (useful in tests + pre-
// phase4 bootstrap states where the Accountant is not yet wired).
func NewAccountantBridge(id string, acc *accounting.Accountant) *AccountantBridge {
	return &AccountantBridge{
		id:         id,
		acc:        acc,
		done:       make(chan struct{}),
		snapshotMS: 1 * time.Second,
	}
}

// Start subscribes to the accountant and spawns the forwarder +
// snapshotter goroutines. Returns nil and operates as a no-op when
// the Accountant is nil.
func (b *AccountantBridge) Start(program TeaProgram) error {
	if b == nil || b.acc == nil {
		return nil
	}
	ch, cancel := b.acc.Subscribe()
	b.ch = ch
	b.cancelSub = cancel

	b.wg.Add(1)
	go b.runForwarder(program)

	b.wg.Add(1)
	go b.runSnapshotter(program)

	return nil
}

// Stop cancels the subscription, waits for the forwarder + snapshotter
// to exit, and releases resources. Idempotent.
func (b *AccountantBridge) Stop() {
	if b == nil {
		return
	}
	b.stopOnce.Do(func() {
		close(b.done)
		if b.cancelSub != nil {
			b.cancelSub()
		}
		b.wg.Wait()
	})
}

// Name implements Bridge.Name.
func (b *AccountantBridge) Name() string { return accountantBridgeName }

// DroppedCount reports the number of deltas dropped because the
// Bubble Tea program send returned a closed-channel error or
// equivalent.
func (b *AccountantBridge) DroppedCount() int64 { return b.dropped.Load() }

// runForwarder consumes UsageDelta off the Accountant subscription
// channel and publishes a TokenDeltaMsg for each. Exits when the
// subscription channel closes (cancel triggered) or Stop is called.
func (b *AccountantBridge) runForwarder(program TeaProgram) {
	defer b.wg.Done()
	for {
		select {
		case <-b.done:
			return
		case delta, ok := <-b.ch:
			if !ok {
				return
			}
			b.publishDelta(program, delta)
		}
	}
}

// runSnapshotter periodically publishes AccountingSnapshotMsg so the
// status bar always has a recent total. The ticker fires at
// b.snapshotMS cadence; deltas fire immediately on each LLM call.
func (b *AccountantBridge) runSnapshotter(program TeaProgram) {
	defer b.wg.Done()
	t := time.NewTicker(b.snapshotMS)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			b.publishSnapshot(program)
		}
	}
}

// publishDelta constructs a TokenDeltaMsg from a UsageDelta and sends
// it to the Bubble Tea program.
func (b *AccountantBridge) publishDelta(program TeaProgram, d accounting.UsageDelta) {
	m := msg.TokenDeltaMsg{
		Timestamp:        d.Timestamp,
		Identity:         d.Identity,
		Task:             d.Task,
		Model:            string(d.Key.Container.Model),
		Phase:            d.Phase.String(),
		Billable:         d.Billable,
		InputTokens:      d.Usage.InputTokens,
		OutputTokens:     d.Usage.OutputTokens,
		ReasoningTokens:  d.Usage.ReasoningTokens,
		CacheReadTokens:  d.Usage.CacheReadTokens,
		CacheWriteTokens: d.Usage.CacheWriteTokens,
		ElapsedMS:        d.Elapsed.Milliseconds(),
	}
	program.Send(m)
}

// publishSnapshot reads the accountant's namespace-wide billable total
// and sends an AccountingSnapshotMsg.
func (b *AccountantBridge) publishSnapshot(program TeaProgram) {
	if b.acc == nil {
		return
	}
	total := b.acc.Billable()
	program.Send(msg.AccountingSnapshotMsg{
		Timestamp:      time.Now(),
		TotalInput:     total.Input,
		TotalOutput:    total.Output,
		TotalReasoning: total.Reasoning,
		TotalRequests:  total.RequestCount,
		TotalErrors:    total.ErrorCount,
	})
}

// BillableForNamespace is a convenience accessor used by consumers
// that need the per-namespace billable totals without subscribing to
// the bridge. Returns a zero-value AggregatedUsage when the bridge
// is nil or unbound.
func (b *AccountantBridge) BillableForNamespace(ns identity.Namespace) accounting.AggregatedUsage {
	if b == nil || b.acc == nil {
		return accounting.AggregatedUsage{}
	}
	return b.acc.ByNamespace(ns)
}
