package shared

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
)

func TestContextBudgetObserver_TracksRuntimeAgentID(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	quota := container.NewResourceQuota(container.ResourceQuotaConfig{
		ContextWindowRequest:   800,
		ContextWindowLimit:     4000,
		ContextLeaseMin:        20 * time.Millisecond,
		ContextReleaseHalfLife: 10 * time.Millisecond,
		ContextShortWindow:     20 * time.Millisecond,
		ContextLongWindow:      80 * time.Millisecond,
		ContextAdmissionTick:   5 * time.Millisecond,
	})
	observer := NewContextBudgetObserver("test.context_budget", bus, quota)
	if err := observer.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer observer.Stop()

	evt := events.NewActivityEvent(events.EventTypeLLMResponse, "sess-1", "ok")
	evt.AgentID = "task_1:engineer"
	evt.Outcome = events.OutcomeSuccess
	evt.Data["runtime_agent_id"] = "worker-1"
	evt.Data["agent_type"] = "engineer"
	evt.Data["input_tokens"] = 1200

	if err := bus.Publish(guide.TopicActivity, guide.NewActivityMessage(evt.AgentID, evt)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := quota.Usage().ContextWindowUsed; got >= 1200 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("expected observer to record runtime agent usage, got %d", quota.Usage().ContextWindowUsed)
}
