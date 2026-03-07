package bridge

import (
	"sync"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
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

func (b *TokenUsageBridge) Name() string       { return tokenUsageBridgeName }
func (b *TokenUsageBridge) DroppedCount() int64 { return 0 }

func (b *TokenUsageBridge) forwardHandler(program TeaProgram) guide.MessageHandler {
	return func(m *guide.Message) error {
		evt, ok := m.GetActivityEvent()
		if !ok || evt.Outcome != events.OutcomeSuccess {
			return nil
		}

		um := msg.TokenUsageMsg{
			AgentID: evt.AgentID,
		}
		if v, ok := evt.Data["model"].(string); ok {
			um.Model = v
		}
		if v, ok := evt.Data["input_tokens"].(int); ok {
			um.InputTokens = v
		}
		if v, ok := evt.Data["output_tokens"].(int); ok {
			um.OutputTokens = v
		}
		if v, ok := evt.Data["cache_read_tokens"].(int); ok {
			um.CacheReadTokens = v
		}
		if v, ok := evt.Data["cache_write_tokens"].(int); ok {
			um.CacheWriteTokens = v
		}
		if v, ok := evt.Data["reasoning_tokens"].(int); ok {
			um.ReasoningTokens = v
		}

		program.Send(um)
		return nil
	}
}
