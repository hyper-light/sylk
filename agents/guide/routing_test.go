package guide

import (
	"fmt"
	"sync"
	"testing"
)

func TestRoutingAggregator_ConcurrentRegisterResolveAndUnregister(t *testing.T) {
	ra := NewRoutingAggregator()

	const (
		workers    = 8
		iterations = 200
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for iter := 0; iter < iterations; iter++ {
				id := fmt.Sprintf("agent-%d-%d", worker, iter)
				name := fmt.Sprintf("task_%d-engineer", iter)
				info := &AgentRoutingInfo{
					ID:      id,
					Type:    "engineer",
					Name:    name,
					Aliases: []string{fmt.Sprintf("alias-%d-%d", worker, iter)},
					ActionShortcuts: []ActionShortcut{
						{Name: fmt.Sprintf("action-%d-%d", worker, iter)},
					},
					Triggers: AgentTriggers{
						StrongTriggers: []string{fmt.Sprintf("trigger-%d-%d", worker, iter)},
					},
				}

				ra.RegisterAgent(info)
				_, _ = ra.ResolveAgent(name)
				_, _ = ra.ResolveAction(info.ActionShortcuts[0].Name)
				_ = ra.GetRoutingInfo(id)
				_ = ra.GetAgentTriggers(id)
				_ = ra.GetAllAliases()
				_ = ra.GetAllActions()
				_ = ra.GetAllStrongTriggers()
				_ = ra.GetAllRoutingInfo()
				ra.UnregisterAgent(id)
			}
		}(worker)
	}

	close(start)
	wg.Wait()

	final := &AgentRoutingInfo{
		ID:   "final-agent",
		Type: "tester-pipeline",
		Name: "task_final-tester-pipeline",
	}
	ra.RegisterAgent(final)

	if got := ra.GetRoutingInfo(final.ID); got == nil || got.Type != final.Type {
		t.Fatalf("GetRoutingInfo(final) = %#v, want type %q", got, final.Type)
	}
	if got, ok := ra.ResolveAgent(final.Name); !ok || got != final.ID {
		t.Fatalf("ResolveAgent(final.Name) = %q, %v, want %q, true", got, ok, final.ID)
	}
}
