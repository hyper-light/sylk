// Package accounting provides a single, typed aggregator for LLM
// token usage across every provider, every agent, every model
// generation, and every task.
//
// The accountant is the sole consumer of
// providers.LLMProviderEventHook in production — all token deltas
// pass through it. Views reduce the canonical map over the four
// axes of the AccountingKey (container uid × generation × model ×
// task) and project per-kind / per-model / per-pod / per-pipeline /
// per-namespace totals.
//
// Persistence uses a JSONL WAL per session so a restart can replay
// totals. Downstream subscribers (UI, handoff GP, cost reporting)
// read from Subscribe().
//
// See docs/FIX_ID_AND_TOKENS.md Section 6 "Accountant" for the
// contract.
package accounting

import (
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
)

// UsageSample mirrors the fields the accountant actually needs from
// provider.Usage. The gateway translates provider.Usage into this
// type at the call boundary so the accounting package stays
// independent of provider internals.
type UsageSample struct {
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
}

// TotalTokens returns input + output (excluding cache re-use, which
// is accounted separately for cost analysis).
func (u UsageSample) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// Event is what the accountant ingests on every hook call. The
// gateway constructs an Event and calls Accountant.Record(event).
type Event struct {
	// Identity must be non-nil. CategorySystem identities are
	// recorded but marked non-billable.
	Identity *identity.AgentIdentity
	// Task must be non-nil. For system identities, use
	// identity.SystemTaskUID via Factory.SystemTask.
	Task *identity.TaskRef
	// Usage is zero-value for error events.
	Usage UsageSample
	// Elapsed is the observed request latency.
	Elapsed time.Duration
	// Phase classifies the event: request initiated, response
	// observed, or error.
	Phase Phase
	// RecordedAt defaults to time.Now() when zero.
	RecordedAt time.Time
}

// Phase enumerates the hook-call points the accountant records.
type Phase uint8

const (
	PhaseUnspecified Phase = iota
	PhaseRequest
	PhaseResponse
	PhaseError
)

var phaseNames = map[Phase]string{
	PhaseUnspecified: "unspecified",
	PhaseRequest:     "request",
	PhaseResponse:    "response",
	PhaseError:       "error",
}

// String returns the canonical phase name.
func (p Phase) String() string {
	if name, ok := phaseNames[p]; ok {
		return name
	}
	return "unknown"
}

// UsageDelta is the typed record that fans out on the subscriber
// channel after Accountant.Record returns. Subscribers read deltas
// in publish order. Backpressure: the subscriber channel is
// bounded; slow subscribers drop deltas, which are counted for
// observability but not retained.
type UsageDelta struct {
	Key       AccountingKey
	Identity  *identity.AgentIdentity
	Task      *identity.TaskRef
	Usage     UsageSample
	Elapsed   time.Duration
	Phase     Phase
	Billable  bool
	Timestamp time.Time
}

// AccountingKey is the canonical primary key for aggregation. Re-
// exported from identity.AccountingKey so downstream callers don't
// have to import both packages.
type AccountingKey = identity.AccountingKey

// ContainerKey is re-exported for the same reason.
type ContainerKey = identity.ContainerKey

// AggregatedUsage is one bucket's running totals. All fields are
// safe to read under the accountant's RLock; mutation is confined
// to the write path.
type AggregatedUsage struct {
	Input            int64
	Output           int64
	Reasoning        int64
	CacheRead        int64
	CacheWrite       int64
	RequestCount     int64
	ErrorCount       int64
	TotalElapsedNano int64 // cumulative, for average-latency reducers
	FirstAt          time.Time
	LastAt           time.Time
	// Billable mirrors the Category at the identity's Mint time.
	// CategorySystem entries are retained but callers that want
	// billable-only totals filter on this flag.
	Billable bool
}

// Add folds one UsageSample into the aggregate. Mutates receiver.
func (a *AggregatedUsage) Add(u UsageSample, elapsed time.Duration, phase Phase, at time.Time) {
	switch phase {
	case PhaseResponse:
		a.Input += int64(u.InputTokens)
		a.Output += int64(u.OutputTokens)
		a.Reasoning += int64(u.ReasoningTokens)
		a.CacheRead += int64(u.CacheReadTokens)
		a.CacheWrite += int64(u.CacheWriteTokens)
		a.RequestCount++
		a.TotalElapsedNano += int64(elapsed)
	case PhaseError:
		a.ErrorCount++
	case PhaseRequest:
		// Request-initiated events update LastAt but don't add
		// tokens (usage is zero at this phase). They let us
		// observe requests-in-flight later if we expand the data
		// model.
	}
	if a.FirstAt.IsZero() || at.Before(a.FirstAt) {
		a.FirstAt = at
	}
	if at.After(a.LastAt) {
		a.LastAt = at
	}
}

// Merge combines another AggregatedUsage into this one. Used by
// view reducers that roll up many buckets into a single total.
func (a *AggregatedUsage) Merge(other AggregatedUsage) {
	a.Input += other.Input
	a.Output += other.Output
	a.Reasoning += other.Reasoning
	a.CacheRead += other.CacheRead
	a.CacheWrite += other.CacheWrite
	a.RequestCount += other.RequestCount
	a.ErrorCount += other.ErrorCount
	a.TotalElapsedNano += other.TotalElapsedNano
	if a.FirstAt.IsZero() || (!other.FirstAt.IsZero() && other.FirstAt.Before(a.FirstAt)) {
		a.FirstAt = other.FirstAt
	}
	if other.LastAt.After(a.LastAt) {
		a.LastAt = other.LastAt
	}
	// Billable flag: a roll-up is billable iff any contributor is.
	if other.Billable {
		a.Billable = true
	}
}

// AverageLatency returns the mean elapsed time per RequestCount.
// Returns 0 when no requests have been recorded.
func (a *AggregatedUsage) AverageLatency() time.Duration {
	if a.RequestCount <= 0 {
		return 0
	}
	return time.Duration(a.TotalElapsedNano / a.RequestCount)
}

// TotalTokens returns Input + Output (excludes cache re-use).
func (a *AggregatedUsage) TotalTokens() int64 {
	return a.Input + a.Output
}

// AccountingSnapshot is a point-in-time view: one AggregatedUsage
// paired with its key. Returned by Accountant.All().
type AccountingSnapshot struct {
	Key   AccountingKey
	Usage AggregatedUsage
}
