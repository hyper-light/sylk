package pipeline

import (
	"fmt"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	agentShared "github.com/adalundhe/sylk/agents/shared"
)

func TestHandleRegistryAnnouncement_ConcurrentRegistrations(t *testing.T) {
	t.Parallel()

	pi := &PipelineInspector{
		id:          "inspector-test",
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		steering:    agentShared.NewSteeringManager(),
	}

	const registrations = 256
	var wg sync.WaitGroup
	wg.Add(registrations)

	for i := 0; i < registrations; i++ {
		i := i
		go func() {
			defer wg.Done()
			msg := &guide.Message{
				Type: guide.MessageTypeAgentRegistered,
				Payload: &guide.AgentAnnouncement{
					AgentID:   fmt.Sprintf("agent-%03d", i),
					AgentType: "tester-pipeline",
				},
			}
			if err := pi.handleRegistryAnnouncement(msg); err != nil {
				t.Errorf("handleRegistryAnnouncement() error = %v", err)
			}
		}()
	}

	wg.Wait()

	pi.knownAgentsMu.RLock()
	defer pi.knownAgentsMu.RUnlock()
	if got := len(pi.knownAgents); got != registrations {
		t.Fatalf("expected %d known agents, got %d", registrations, got)
	}
}
