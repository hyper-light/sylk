package shared

import (
	"strings"
	"sync"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
)

// ContextBudgetObserver keeps the session token-budget ledger aligned with the
// authoritative LLM activity stream. It centralizes usage updates so agents do
// not each need bespoke quota hooks.
type ContextBudgetObserver struct {
	id       string
	bus      guide.EventBus
	quota    *container.ResourceQuota
	sub      guide.Subscription
	stopOnce sync.Once
}

func NewContextBudgetObserver(id string, bus guide.EventBus, quota *container.ResourceQuota) *ContextBudgetObserver {
	if bus == nil || quota == nil {
		return nil
	}
	return &ContextBudgetObserver{id: strings.TrimSpace(id), bus: bus, quota: quota}
}

func (o *ContextBudgetObserver) Start() error {
	if o == nil || o.bus == nil || o.quota == nil {
		return nil
	}
	handler := guide.NewFilteredActivityHandler(o.forwardHandler(), guide.ByEventType(
		events.EventTypeLLMRequest,
		events.EventTypeLLMResponse,
	))
	sub, err := o.bus.SubscribeAsync(guide.TopicActivity, handler)
	if err != nil {
		return err
	}
	o.sub = sub
	return nil
}

func (o *ContextBudgetObserver) Stop() {
	if o == nil {
		return
	}
	o.stopOnce.Do(func() {
		if o.sub != nil {
			_ = o.sub.Unsubscribe()
		}
	})
}

func (o *ContextBudgetObserver) forwardHandler() guide.MessageHandler {
	return func(m *guide.Message) error {
		evt, ok := m.GetActivityEvent()
		if !ok || evt == nil {
			return nil
		}

		agentID := eventRuntimeAgentID(evt)
		agentType := eventAgentType(evt)
		if agentID == "" {
			return nil
		}

		switch evt.EventType {
		case events.EventTypeLLMRequest:
			if inputTokens, ok := int64FromAny(evt.Data["input_tokens"]); ok && inputTokens > 0 {
				o.quota.ObserveContextUsage(container.ContextUsageObservation{
					AgentID:     agentID,
					AgentType:   agentType,
					InputTokens: inputTokens,
					ObservedAt:  evt.Timestamp,
				})
				return nil
			}
			o.quota.TouchContextClaim(agentID, agentType)
		case events.EventTypeLLMResponse:
			if evt.Outcome != events.OutcomeSuccess {
				return nil
			}
			inputTokens, ok := int64FromAny(evt.Data["input_tokens"])
			if !ok || inputTokens <= 0 {
				return nil
			}
			o.quota.ObserveContextUsage(container.ContextUsageObservation{
				AgentID:     agentID,
				AgentType:   agentType,
				InputTokens: inputTokens,
				ObservedAt:  evt.Timestamp,
			})
		}
		return nil
	}
}

func eventRuntimeAgentID(evt *events.ActivityEvent) string {
	if evt == nil {
		return ""
	}
	if evt.Data != nil {
		if runtimeID, ok := evt.Data["runtime_agent_id"].(string); ok && strings.TrimSpace(runtimeID) != "" {
			return strings.TrimSpace(runtimeID)
		}
	}
	return strings.TrimSpace(evt.AgentID)
}

func eventAgentType(evt *events.ActivityEvent) string {
	if evt == nil || evt.Data == nil {
		return ""
	}
	if agentType, ok := evt.Data["agent_type"].(string); ok {
		return strings.TrimSpace(agentType)
	}
	return ""
}

func int64FromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
