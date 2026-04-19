package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// LLMMetrics - Metrics tracking for LLM requests/responses
// =============================================================================

// LLMMetrics contains telemetry data for LLM interactions. Emitted by
// the gateway-level hook alongside the typed identity + task.
type LLMMetrics struct {
	Model        string
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
	RequestTime  time.Time
	ResponseTime time.Time
}

// TokensPerSecond calculates the output token generation rate.
// Returns 0 if duration is zero or negative.
func (m *LLMMetrics) TokensPerSecond() float64 {
	if m.Duration <= 0 {
		return 0
	}
	seconds := m.Duration.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(m.OutputTokens) / seconds
}

// TotalTokens returns the sum of input and output tokens.
func (m *LLMMetrics) TotalTokens() int {
	return m.InputTokens + m.OutputTokens
}

// =============================================================================
// LLMProviderEventHook — typed hook contract
// =============================================================================

// LLMProviderEventHook receives request / response / error events from
// the provider gateway. Every call carries the typed identity + task
// values extracted from the request context — no string fallbacks, no
// metadata-map lookups. Identity and Task are guaranteed non-nil; the
// gateway aborts the dispatch before ever calling the hook if either
// is missing.
//
// Implementations MUST NOT retain references to Identity or Task
// across goroutines without deep-copying — both are intended to flow
// through the single request-scoped ctx.
//
// Composition: bootstrap typically wires a MultiHook containing the
// accountant hook and the activity-publisher hook. Gateway-level
// admission still holds exactly one hook per gateway.
type LLMProviderEventHook interface {
	// OnRequest fires immediately after admission control releases a
	// request but before the provider call begins.
	OnRequest(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID)

	// OnResponse fires when the provider returns a successful response
	// (non-stream) or when the terminal chunk of a stream arrives with
	// populated Usage. Usage is always non-nil for this call.
	OnResponse(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, usage *Usage, elapsed time.Duration)

	// OnError fires when the provider call fails. elapsed is the wall
	// time from admission to error; err is the observed failure.
	OnError(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, err error, elapsed time.Duration)
}

// =============================================================================
// LLMEventPublisher — emits ActivityEvents enriched with typed identity + task
// =============================================================================

// LLMEventPublisher converts provider-level request / response / error
// signals into ActivityEvent publications. It attaches the typed
// identity and task values to the event and also projects them into
// the legacy string fields (AgentID, Data.task_id, …) so existing UI
// consumers keep working.
type LLMEventPublisher struct {
	bus events.ActivityPublisher
}

// NewLLMEventPublisher creates a new LLMEventPublisher with the given
// activity publisher. Returns nil if bus is nil.
func NewLLMEventPublisher(bus events.ActivityPublisher) *LLMEventPublisher {
	if bus == nil {
		return nil
	}
	return &LLMEventPublisher{bus: bus}
}

// PublishLLMRequest publishes an EventTypeLLMRequest event.
func (p *LLMEventPublisher) PublishLLMRequest(id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}
	event := events.NewActivityEvent(
		events.EventTypeLLMRequest,
		string(id.Namespace()),
		fmt.Sprintf("LLM request to %s", model),
	)
	attachIdentity(event, id, task)
	event.Data = baseIdentityData(id, task, model)
	event.Data["timestamp"] = time.Now().UnixMilli()
	event.Category = "llm"
	p.bus.PublishActivity(event)
	return nil
}

// PublishLLMResponse publishes an EventTypeLLMResponse event.
func (p *LLMEventPublisher) PublishLLMResponse(id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, usage *Usage, duration time.Duration) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}
	inputTokens, outputTokens := 0, 0
	if usage != nil {
		inputTokens = usage.InputTokens
		outputTokens = usage.OutputTokens
	}
	var tokensPerSec float64
	if duration > 0 {
		tokensPerSec = float64(outputTokens) / duration.Seconds()
	}
	event := events.NewActivityEvent(
		events.EventTypeLLMResponse,
		string(id.Namespace()),
		fmt.Sprintf("LLM response from %s: %d input, %d output tokens in %v", model, inputTokens, outputTokens, duration),
	)
	attachIdentity(event, id, task)
	event.Outcome = events.OutcomeSuccess
	data := baseIdentityData(id, task, model)
	data["input_tokens"] = inputTokens
	data["output_tokens"] = outputTokens
	data["duration_ms"] = duration.Milliseconds()
	data["tokens_per_sec"] = tokensPerSec
	if usage != nil {
		if usage.CacheReadTokens > 0 {
			data["cache_read_tokens"] = usage.CacheReadTokens
		}
		if usage.CacheWriteTokens > 0 {
			data["cache_write_tokens"] = usage.CacheWriteTokens
		}
		if usage.ReasoningTokens > 0 {
			data["reasoning_tokens"] = usage.ReasoningTokens
		}
	}
	event.Data = data
	event.Category = "llm"
	p.bus.PublishActivity(event)
	return nil
}

// PublishLLMError publishes an EventTypeAgentError event.
func (p *LLMEventPublisher) PublishLLMError(id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, err error, duration time.Duration) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("event publisher not initialized")
	}
	if err == nil {
		return fmt.Errorf("error cannot be nil")
	}
	message := FriendlyErrorMessage(err)
	if message == "" {
		message = err.Error()
	}
	event := events.NewActivityEvent(
		events.EventTypeAgentError,
		string(id.Namespace()),
		fmt.Sprintf("LLM error from %s: %s", model, message),
	)
	attachIdentity(event, id, task)
	event.Outcome = events.OutcomeFailure
	data := baseIdentityData(id, task, model)
	data["error"] = err.Error()
	data["error_type"] = "llm_api_error"
	data["duration_ms"] = duration.Milliseconds()
	data["timestamp"] = time.Now().UnixMilli()
	event.Data = data
	event.Category = "llm"
	p.bus.PublishActivity(event)
	return nil
}

// attachIdentity stamps the typed identity + task onto the event plus
// the flat string fields persisted form reads.
//
// AgentID is the typed identity's K8s-shaped Panel() value
// ("namespace/pod/name"). Downstream UI consumers key on
// event.Identity.UID() for per-replica / per-generation tracking and
// derive display from Identity.Panel() or Identity.Kind().String()
// per spec item 51. The metadata-caching publisher and any legacy
// consumers that fall back to matching AgentID strings should
// recognize this panel form.
func attachIdentity(event *events.ActivityEvent, id *identity.AgentIdentity, task *identity.TaskRef) {
	event.Identity = id
	event.Task = task
	event.SessionID = string(id.Namespace())
	event.AgentID = id.Panel()
	if task != nil {
		event.CorrelationID = string(task.Correlation())
	}
}

// baseIdentityData returns the common set of data-map fields derived
// from the identity + task pair.
//
// `agent_type` is populated with Kind().String() so the legacy
// core/events MetadataCachingPublisher can enrich events that pass
// through it (its `known` map is keyed on the agent type string like
// "guide" / "architect"). `agent_kind` is the typed spelling kept for
// new consumers that want to avoid string parsing — both keys carry
// the same value.
//
// `agent_name` is NOT emitted. `id.Name()` is the K8s-shaped
// container name (lowercase kind + ordinal, e.g. "architect" or
// "librarian-r0"); setting it in Data would override the
// MetadataCachingPublisher's display-name resolution from its
// `known` map (which carries human-readable names like "Architect").
// Downstream consumers that want the Name read it via
// event.Identity.Name() directly.
// `agent_container_name` is emitted for log correlation only.
func baseIdentityData(id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID) map[string]any {
	kind := id.Kind().String()
	data := map[string]any{
		"model":                string(model),
		"agent_uid":            string(id.UID()),
		"agent_kind":           kind,
		"agent_type":           kind,
		"agent_container_name": string(id.Name()),
		"agent_pod":            string(id.Pod().ID),
		"agent_category":       id.Category().String(),
		"generation":           uint64(id.Generation()),
	}
	if owner := id.Owner(); owner != nil {
		data["owner_uid"] = string(owner.UID)
		data["owner_name"] = string(owner.Name)
	}
	if task != nil {
		data["task_uid"] = string(task.UID())
		if task.DisplayID() != "" {
			data["task_display"] = task.DisplayID()
		}
		if p := task.Pipeline(); p != nil {
			data["pipeline_id"] = string(p.ID)
			if p.Stage != "" {
				data["pipeline_stage"] = string(p.Stage)
			}
			if p.Parent != "" {
				data["parent_task"] = string(p.Parent)
			}
		}
	}
	return data
}

// =============================================================================
// LLMEventPublisherHook — adapter implementing LLMProviderEventHook
// =============================================================================

// LLMEventPublisherHook adapts LLMEventPublisher to the
// LLMProviderEventHook contract.
type LLMEventPublisherHook struct {
	publisher *LLMEventPublisher
}

// NewLLMEventPublisherHook creates a new hook adapter. Returns nil if
// publisher is nil.
func NewLLMEventPublisherHook(publisher *LLMEventPublisher) *LLMEventPublisherHook {
	if publisher == nil {
		return nil
	}
	return &LLMEventPublisherHook{publisher: publisher}
}

// OnRequest implements LLMProviderEventHook.
func (h *LLMEventPublisherHook) OnRequest(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMRequest(id, task, model)
}

// OnResponse implements LLMProviderEventHook.
func (h *LLMEventPublisherHook) OnResponse(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, usage *Usage, elapsed time.Duration) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMResponse(id, task, model, usage, elapsed)
}

// OnError implements LLMProviderEventHook.
func (h *LLMEventPublisherHook) OnError(_ context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, err error, elapsed time.Duration) {
	if h == nil || h.publisher == nil {
		return
	}
	_ = h.publisher.PublishLLMError(id, task, model, err, elapsed)
}

// =============================================================================
// MultiHook — fans out hook calls to several sub-hooks
// =============================================================================

// MultiHook composes several LLMProviderEventHook implementations
// behind a single handle. The gateway holds exactly one hook; wiring
// the accountant + activity publisher at the same time is done via a
// MultiHook.
type MultiHook struct {
	hooks []LLMProviderEventHook
}

// NewMultiHook returns a hook that forwards every call to each
// supplied sub-hook in order. Nil sub-hooks are filtered out; passing
// zero non-nil hooks returns a MultiHook whose methods are no-ops.
func NewMultiHook(hooks ...LLMProviderEventHook) *MultiHook {
	out := make([]LLMProviderEventHook, 0, len(hooks))
	for _, h := range hooks {
		if h == nil {
			continue
		}
		out = append(out, h)
	}
	return &MultiHook{hooks: out}
}

// OnRequest implements LLMProviderEventHook.
func (m *MultiHook) OnRequest(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID) {
	if m == nil {
		return
	}
	for _, h := range m.hooks {
		h.OnRequest(ctx, id, task, model)
	}
}

// OnResponse implements LLMProviderEventHook.
func (m *MultiHook) OnResponse(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, usage *Usage, elapsed time.Duration) {
	if m == nil {
		return
	}
	for _, h := range m.hooks {
		h.OnResponse(ctx, id, task, model, usage, elapsed)
	}
}

// OnError implements LLMProviderEventHook.
func (m *MultiHook) OnError(ctx context.Context, id *identity.AgentIdentity, task *identity.TaskRef, model identity.ModelID, err error, elapsed time.Duration) {
	if m == nil {
		return
	}
	for _, h := range m.hooks {
		h.OnError(ctx, id, task, model, err, elapsed)
	}
}

// =============================================================================
// NoOpLLMEventHook — no-operation hook for tests / disabled emission
// =============================================================================

// NoOpLLMEventHook is a no-operation hook. Every call is a no-op.
type NoOpLLMEventHook struct{}

func (*NoOpLLMEventHook) OnRequest(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID) {
}

func (*NoOpLLMEventHook) OnResponse(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID, *Usage, time.Duration) {
}

func (*NoOpLLMEventHook) OnError(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID, error, time.Duration) {
}

// Compile-time interface compliance checks.
var (
	_ LLMProviderEventHook = (*LLMEventPublisherHook)(nil)
	_ LLMProviderEventHook = (*NoOpLLMEventHook)(nil)
	_ LLMProviderEventHook = (*MultiHook)(nil)
)
