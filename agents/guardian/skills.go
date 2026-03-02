package guardian

import (
	"fmt"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

func (g *Guardian) registerCoreSkills() {
	g.skills.Register(gitSafetySkill(g))
	g.skills.Register(rollbackSkill(g))
	g.skills.Register(contentScanSkill(g))
	g.skills.Register(agentHealthSkill(g))
	g.skills.Register(reviewGateSkill(g))
	g.skills.Register(readFileSkill(g))
	g.skills.Register(globSkill(g))
	g.skills.Register(grepSkill(g))
	g.skills.Register(askUserQuestionSkill(g))
	g.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "guardian",
		SessionID: func() string { return "" },
		Publish:   g.publishRerouteRequest,
	}))
}

func (g *Guardian) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if g.bus == nil {
		return fmt.Errorf("guardian bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   g.id,
		SuggestedTarget: suggestedTarget,
		ExcludeAgents:   []string{"guardian"},
	}
	return g.bus.Publish(guide.TopicGuideRequests, newRerouteMessage(g.id, reroute))
}
