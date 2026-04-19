package bridge

import (
	"encoding/json"
	"sync"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/agentidentity"
	"github.com/adalundhe/sylk/ui/msg"
)

const tokenUsageBridgeName = "bridge.token_usage"

// TokenUsageBridge subscribes to EventTypeLLMResponse activity events on the
// ChannelBus and forwards token deltas to the Bubble Tea program as
// TokenUsageMsg. Unlike the ActivityBridge (which filters to VisibilityUser),
// this bridge accepts all visibility levels so that background agent LLM calls
// are counted in the status bar totals.
type TokenUsageBridge struct {
	id       string
	bus      guide.EventBus
	sub      guide.Subscription
	stopOnce sync.Once
}

// NewTokenUsageBridge creates a bridge that converts LLM response events into
// token-usage messages for the TUI status bar.
func NewTokenUsageBridge(id string, bus guide.EventBus) *TokenUsageBridge {
	return &TokenUsageBridge{id: id, bus: bus}
}

func (b *TokenUsageBridge) Start(program TeaProgram) error {
	handler := guide.NewFilteredActivityHandler(
		b.forwardHandler(program),
		guide.ByEventType(events.EventTypeLLMResponse),
	)

	sub, err := b.bus.SubscribeAsync(guide.TopicActivity, handler)
	if err != nil {
		return err
	}
	b.sub = sub
	return nil
}

func (b *TokenUsageBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.sub != nil {
			_ = b.sub.Unsubscribe()
		}
	})
}

func (b *TokenUsageBridge) Name() string        { return tokenUsageBridgeName }
func (b *TokenUsageBridge) DroppedCount() int64 { return 0 }

func (b *TokenUsageBridge) forwardHandler(program TeaProgram) guide.MessageHandler {
	return func(m *guide.Message) error {
		evt, ok := m.GetActivityEvent()
		if !ok || evt.Outcome != events.OutcomeSuccess {
			return nil
		}

		um := msg.TokenUsageMsg{
			CorrelationID: evt.CorrelationID,
			AgentID: agentidentity.VisibleAgentID(
				evt.AgentID,
				activityMetadataString(evt.Data, "canonical_agent_id"),
				activityMetadataString(evt.Data, "agent_type"),
				activityMetadataString(evt.Data, "pipeline_id"),
				activityMetadataString(evt.Data, "task_id"),
			),
		}
		if v, ok := evt.Data["runtime_agent_id"].(string); ok {
			um.RuntimeAgentID = v
		} else {
			um.RuntimeAgentID = agentidentity.RuntimeAgentID(evt.AgentID, "")
		}
		if v, ok := evt.Data["agent_type"].(string); ok {
			um.AgentType = v
		}
		if v, ok := evt.Data["pipeline_id"].(string); ok {
			um.PipelineID = v
		}
		if v, ok := evt.Data["task_id"].(string); ok {
			um.TaskID = v
		}
		if v, ok := evt.Data["model"].(string); ok {
			um.Model = v
		}
		if v, ok := intFromAny(evt.Data["input_tokens"]); ok {
			um.InputTokens = v
		}
		if v, ok := intFromAny(evt.Data["output_tokens"]); ok {
			um.OutputTokens = v
		}
		if v, ok := intFromAny(evt.Data["cache_read_tokens"]); ok {
			um.CacheReadTokens = v
		}
		if v, ok := intFromAny(evt.Data["cache_write_tokens"]); ok {
			um.CacheWriteTokens = v
		}
		if v, ok := intFromAny(evt.Data["reasoning_tokens"]); ok {
			um.ReasoningTokens = v
		}

		// Typed identity + task overlay. Populated by the
		// provider-gateway MultiHook when the call carried typed
		// *AgentIdentity / *TaskRef on ctx. Empty when the call
		// originated from an unmigrated path.
		if id := evt.Identity; id != nil {
			um.AgentUID = string(id.UID())
			um.Generation = uint64(id.Generation())
			if owner := id.Owner(); owner != nil {
				um.OwnerAgentUID = string(owner.UID)
			}
		}
		if task := evt.Task; task != nil {
			um.TaskUID = string(task.UID())
			um.TaskCorrelation = string(task.Correlation())
		}

		program.Send(um)
		return nil
	}
}

func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}
