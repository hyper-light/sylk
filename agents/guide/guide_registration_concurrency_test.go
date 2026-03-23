package guide

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGuide_ConcurrentRegisterAndRouteDSL(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-concurrent-register-route",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	const (
		writers    = 6
		readers    = 6
		iterations = 150
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			<-start
			for iter := 0; iter < iterations; iter++ {
				id := fmt.Sprintf("worker-%d-%d", writer, iter)
				if err := g.Register(&AgentRoutingInfo{
					ID:      id,
					Type:    "engineer",
					Name:    fmt.Sprintf("task_%d-engineer", iter),
					Aliases: []string{fmt.Sprintf("eng_%d_%d", writer, iter)},
					Registration: &AgentRegistration{
						ID:   id,
						Name: fmt.Sprintf("task_%d-engineer", iter),
						Capabilities: AgentCapabilities{
							Intents: []Intent{IntentHelp, IntentCheck},
							Domains: []Domain{DomainCode, DomainTasks},
						},
					},
				}); err != nil {
					t.Errorf("Register(%s): %v", id, err)
					return
				}
			}
		}(writer)
	}

	for reader := 0; reader < readers; reader++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			<-start
			for iter := 0; iter < iterations; iter++ {
				req := &RouteRequest{
					Input:         "@guide help",
					SourceAgentID: fmt.Sprintf("reader-%d", reader),
					SessionID:     "session-concurrent-register-route",
					Timestamp:     time.Now(),
				}
				result, err := g.Route(context.Background(), req)
				if err != nil {
					t.Errorf("Route(%d): %v", iter, err)
					return
				}
				if result == nil {
					t.Errorf("Route(%d): nil result", iter)
					return
				}
			}
		}(reader)
	}

	close(start)
	wg.Wait()
}
