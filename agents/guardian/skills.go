package guardian

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
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
	g.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewDiffWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil, nil))
	g.skills.Register(askUserQuestionSkill(g))
	g.skills.Register(agentLogsSkill(g))
	g.skills.Register(systemStatusSkill(g))
	g.skills.Register(vfsStatusSkill(g))
	g.skills.Register(pipelineStatusSkill(g))
	g.skills.Register(gatewayMetricsSkill(g))
	g.skills.Register(knowledgeStatusSkill(g))
	g.skills.Register(concurrencyStatusSkill(g))
	g.skills.Register(usageBreakdownSkill(g))
	g.skills.Register(quarantineStatusSkill(g))
	g.skills.Register(toolExecutionControlSkill(g))
	g.skills.Register(commandExecutionControlSkill(g))
	g.skills.Register(shared.NewSelfDiagnosticSkill(&guardianDiag{g: g}))
	g.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "guardian",
		SessionID: func() string { return "" },
		Publish:   g.publishRerouteRequest,
	}))
}

type guardianDiag struct{ g *Guardian }

func (d *guardianDiag) AgentName() string { return "guardian" }
func (d *guardianDiag) SessionID() string { return "" }
func (d *guardianDiag) LogsDir() string {
	return shared.LogsDirForAgent(d.g.steering.SessionDir(), "guardian")
}
func (d *guardianDiag) EventLogger() *agentlog.SessionEventLogger { return d.g.steering.EventLogger() }
func (d *guardianDiag) RecoveryHints() []string                   { return nil }

func (d *guardianDiag) PeerLogsDirs() map[string]string {
	sessionDir := d.g.steering.SessionDir()
	if sessionDir == "" {
		return nil
	}
	agentsDir := filepath.Join(sessionDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil
	}
	peers := make(map[string]string)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "guardian" {
			continue
		}
		logsDir := filepath.Join(agentsDir, e.Name(), "logs")
		if info, err := os.Stat(logsDir); err == nil && info.IsDir() {
			peers[e.Name()] = logsDir
		}
	}
	return peers
}

func (d *guardianDiag) AgentSpecificDiagnostics() map[string]any {
	return map[string]any{
		"health_monitor_active": d.g.healthMon != nil,
	}
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
