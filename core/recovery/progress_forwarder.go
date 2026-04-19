package recovery

import (
	"strings"
	"sync"
)

// REC-01: external emission for ProgressCollector.
//
// The ProgressCollector's subscriber pattern is in-process only —
// anything wanting to react to progress signals has to be physically
// near enough to call Subscribe directly. Production wiring (TUI
// bridge, Guardian watchdogs) lives in separate packages and cannot
// pull an import of core/recovery's types into core/events without
// creating a cycle. REC-01 closes that gap with a narrow sink
// interface (ProgressEventSink) and a subscriber adapter
// (ProgressBusForwarder) that converts every collected signal into
// a sink call.
//
// Responsibilities:
//
//   - Abstract the delivery surface behind ProgressEventSink so tests
//     can inject a capturing stub and the production TUI bridge can
//     attach a ChannelBus writer.
//   - Filter optional agent / signal-type allowlists so subscribers
//     can narrow their interest without every subscriber having to
//     re-filter post-delivery.
//   - Stay thread-safe: ProgressCollector's notifySubscribers may
//     invoke OnSignal from multiple goroutines if multiple emitters
//     call Signal concurrently, so the forwarder guards the sink
//     and the filter with a read lock.

// ProgressEventSink is the abstract delivery surface for progress
// signals exiting the recovery package. Mirrors RecoveryEventSink's
// shape (core/recovery/bus_notifier.go) — the same discipline that
// keeps core/recovery free of guide/events imports.
type ProgressEventSink interface {
	PublishProgressSignal(sig ProgressSignal)
}

// ProgressForwarderFilter narrows the signals a forwarder delivers.
// All fields are optional — empty means "no filter on this axis".
//
// AgentIDs and SessionIDs are exact-match sets; SignalTypes matches
// the enum directly. The three dimensions AND together when more than
// one is non-empty: a signal must satisfy every filled filter to pass.
type ProgressForwarderFilter struct {
	AgentIDs    map[string]struct{}
	SessionIDs  map[string]struct{}
	SignalTypes map[ProgressSignalType]struct{}
}

// ProgressBusForwarder implements ProgressSubscriber by handing every
// signal it receives to a sink, after applying an optional filter. A
// nil sink is treated as "disabled" — OnSignal becomes a no-op — so
// callers can install the forwarder first and swap the sink in later
// via SetSink without losing the subscription.
type ProgressBusForwarder struct {
	mu     sync.RWMutex
	sink   ProgressEventSink
	filter ProgressForwarderFilter
}

// NewProgressBusForwarder builds a forwarder with the supplied sink.
// A nil sink is permitted (see struct doc); pass nil to defer sink
// attachment until the bus is available.
func NewProgressBusForwarder(sink ProgressEventSink) *ProgressBusForwarder {
	return &ProgressBusForwarder{sink: sink}
}

// SetSink swaps the sink pointer. Passing nil disables delivery
// without unsubscribing from the collector — useful for teardown
// sequences where the sink closes before the collector does.
func (f *ProgressBusForwarder) SetSink(sink ProgressEventSink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sink = sink
}

// SetFilter installs an allowlist filter. Fields with empty maps are
// treated as unconstrained. Replaces the entire filter — merging
// existing values is the caller's responsibility.
func (f *ProgressBusForwarder) SetFilter(filter ProgressForwarderFilter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filter = filter
}

// OnSignal implements ProgressSubscriber. Delivers sig to the sink
// after the filter allows it.
func (f *ProgressBusForwarder) OnSignal(sig ProgressSignal) {
	f.mu.RLock()
	sink := f.sink
	filter := f.filter
	f.mu.RUnlock()
	if sink == nil {
		return
	}
	if !filter.allow(sig) {
		return
	}
	sink.PublishProgressSignal(sig)
}

// allow evaluates the filter against a signal. An empty filter allows
// everything. Whitespace-trimming on inbound agent/session IDs lets
// callers not worry about upstream formatting inconsistencies.
func (f ProgressForwarderFilter) allow(sig ProgressSignal) bool {
	if len(f.AgentIDs) > 0 {
		if _, ok := f.AgentIDs[strings.TrimSpace(sig.AgentID)]; !ok {
			return false
		}
	}
	if len(f.SessionIDs) > 0 {
		if _, ok := f.SessionIDs[strings.TrimSpace(sig.SessionID)]; !ok {
			return false
		}
	}
	if len(f.SignalTypes) > 0 {
		if _, ok := f.SignalTypes[sig.SignalType]; !ok {
			return false
		}
	}
	return true
}
