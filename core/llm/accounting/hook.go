package accounting

import (
	"context"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/providers"
)

// Hook is the adapter that plugs the Accountant into the provider
// gateway's LLMProviderEventHook contract. One instance is shared
// across every gateway in the session; each call carries the full
// typed identity + task pair so the Accountant can key on the
// canonical AccountingKey without any string parsing.
//
// Hook is thread-safe. Record dispatches are non-blocking — WAL write
// failures are logged; fan-out to Subscribers is best-effort with
// backpressure counted by the Accountant.
type Hook struct {
	acc    *Accountant
	logger *slog.Logger
}

// NewHook builds a Hook bound to the given Accountant. Nil accountant
// is an error — there's nothing to wire onto otherwise.
func NewHook(acc *Accountant, logger *slog.Logger) *Hook {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hook{acc: acc, logger: logger}
}

// OnRequest records a PhaseRequest event. Token usage is zero at this
// phase; the record marks the request-initiated timestamp so reducers
// like AverageLatency can compute meaningful windows even for
// in-flight calls.
func (h *Hook) OnRequest(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID) {
	if h == nil || h.acc == nil {
		return
	}
	if id == nil || task == nil {
		h.logger.Warn("accounting.Hook.OnRequest: missing identity or task")
		return
	}
	_ = h.acc.Record(Event{
		Identity: id,
		Task:     task,
		Phase:    PhaseRequest,
	})
}

// OnResponse records a PhaseResponse event with the provider's usage
// counters and observed elapsed time.
func (h *Hook) OnResponse(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, usage *providers.Usage, elapsed time.Duration) {
	if h == nil || h.acc == nil {
		return
	}
	if id == nil || task == nil {
		h.logger.Warn("accounting.Hook.OnResponse: missing identity or task")
		return
	}
	sample := UsageSample{}
	if usage != nil {
		sample.InputTokens = usage.InputTokens
		sample.OutputTokens = usage.OutputTokens
		sample.ReasoningTokens = usage.ReasoningTokens
		sample.CacheReadTokens = usage.CacheReadTokens
		sample.CacheWriteTokens = usage.CacheWriteTokens
	}
	if err := h.acc.Record(Event{
		Identity: id,
		Task:     task,
		Usage:    sample,
		Elapsed:  elapsed,
		Phase:    PhaseResponse,
	}); err != nil {
		h.logger.Warn("accounting.Hook.OnResponse: record failed",
			"error", err,
			"uid", string(id.UID()),
			"task", string(task.UID()),
		)
	}
}

// OnError records a PhaseError event. Usage is zero at this phase.
func (h *Hook) OnError(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, err error, elapsed time.Duration) {
	if h == nil || h.acc == nil {
		return
	}
	if id == nil || task == nil {
		h.logger.Warn("accounting.Hook.OnError: missing identity or task",
			"error", err,
		)
		return
	}
	if recErr := h.acc.Record(Event{
		Identity: id,
		Task:     task,
		Elapsed:  elapsed,
		Phase:    PhaseError,
	}); recErr != nil {
		h.logger.Warn("accounting.Hook.OnError: record failed",
			"error", recErr,
			"origin", err,
			"uid", string(id.UID()),
			"task", string(task.UID()),
		)
	}
}

// Compile-time interface compliance check.
var _ providers.LLMProviderEventHook = (*Hook)(nil)
