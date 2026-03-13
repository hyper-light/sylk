package orchestrator

import "github.com/adalundhe/sylk/agents/guide"

// SeedKnownAgents populates the orchestrator's known-agent registry from a
// snapshot of already-registered Guide agents. This prevents late-starting
// orchestrators from waiting on future registry events just to address peers.
func (o *Orchestrator) SeedKnownAgents(agents []*guide.AgentAnnouncement) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, ann := range agents {
		if ann == nil || ann.AgentID == "" {
			continue
		}
		if _, exists := o.knownAgents[ann.AgentID]; exists {
			continue
		}
		o.knownAgents[ann.AgentID] = ann
	}
}
