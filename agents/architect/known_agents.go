package architect

import "github.com/adalundhe/sylk/agents/guide"

// SeedKnownAgents populates the architect's known-agent registry from a
// snapshot of already-registered Guide agents. This is required for
// late-starting architects that may miss earlier registry announcements.
func (a *Architect) SeedKnownAgents(agents []*guide.AgentAnnouncement) {
	if a == nil {
		return
	}
	a.knownAgentsMu.Lock()
	defer a.knownAgentsMu.Unlock()
	for _, ann := range agents {
		if ann == nil || ann.AgentID == "" {
			continue
		}
		if _, exists := a.knownAgents[ann.AgentID]; exists {
			continue
		}
		a.knownAgents[ann.AgentID] = ann
	}
}
