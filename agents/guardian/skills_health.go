package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// agent_health — Domain: health, Priority: 85
// ---------------------------------------------------------------------------

type agentHealthInput struct {
	Action  string `json:"action"`
	AgentID string `json:"agent_id,omitempty"`
}

func agentHealthSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *agentHealthInput) (any, error)
	dispatch := map[string]handler{
		"status": func(_ context.Context, p *agentHealthInput) (any, error) {
			return agentHealthStatus(g, p)
		},
		"budget_check": func(_ context.Context, _ *agentHealthInput) (any, error) {
			budget := g.healthMon.BudgetSnapshot()
			return map[string]any{"budget": budget}, nil
		},
		"anomaly_report": func(_ context.Context, _ *agentHealthInput) (any, error) {
			anomalies := g.healthMon.DetectAnomalies()
			return map[string]any{
				"anomalies":     anomalies,
				"anomaly_count": len(anomalies),
				"clean":         len(anomalies) == 0,
			}, nil
		},
		"registry": func(_ context.Context, _ *agentHealthInput) (any, error) {
			return agentHealthRegistry(g)
		},
		"activation": func(_ context.Context, _ *agentHealthInput) (any, error) {
			return agentHealthActivation(g)
		},
	}

	return skills.NewSkill("agent_health").
		Description("Agent health monitoring: status checks, budget tracking, anomaly detection, "+
			"registry inspection, and activation tier queries.\n\n"+
			"Actions:\n"+
			"- status: Get health status for one or all agents, merged with registry data (params: agent_id [optional])\n"+
			"- budget_check: Check token and cost budget consumption\n"+
			"- anomaly_report: Detect health anomalies across all agents\n"+
			"- registry: Full agent registry view with readiness and capabilities\n"+
			"- activation: Current activation tier and metrics for each agent type").
		Domain("health").
		Keywords("health", "status", "budget", "tokens", "cost", "anomaly", "timeout",
			"agent", "registered", "registry", "activation", "tier").
		Priority(85).
		TokenEstimate(500).
		EnumParam("action", "Health action", []string{
			"status", "budget_check", "anomaly_report", "registry", "activation",
		}, true).
		StringParam("agent_id", "Agent ID to check (for status, optional — omit for all)", false).
		Usage("Use agent_health to check registered agent status, health metrics, "+
			"budget consumption, and anomaly detection. Start with action=status "+
			"for a holistic view before drilling into specific agents.").
		BestPractice("Use action=registry to verify agent registration before "+
			"assuming an agent is missing.").
		Example(`{"action": "status"}`).
		Example(`{"action": "registry"}`).
		Example(`{"action": "activation"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params agentHealthInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "agent_health", "action": params.Action, "agent_id": params.AgentID})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown agent_health action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

// agentHealthStatus merges knownAgents registry with healthMon snapshots.
func agentHealthStatus(g *Guardian, p *agentHealthInput) (any, error) {
	if p.AgentID != "" {
		return agentHealthStatusSingle(g, p.AgentID)
	}

	g.knownMu.RLock()
	known := make(map[string]*agentStatusEntry, len(g.knownAgents))
	for id, ann := range g.knownAgents {
		known[id] = &agentStatusEntry{
			AgentID:   ann.AgentID,
			AgentType: ann.AgentType,
			AgentName: ann.AgentName,
			Ready:     ann.Ready,
			ReadyAt:   ann.ReadyAt,
		}
	}
	g.knownMu.RUnlock()

	// Overlay health data from healthMon.
	snapshots := g.healthMon.AllSnapshots()
	for _, snap := range snapshots {
		entry, ok := known[snap.AgentID]
		if !ok {
			entry = &agentStatusEntry{AgentID: snap.AgentID}
			known[snap.AgentID] = entry
		}
		entry.Responsive = snap.Responsive
		entry.ErrorCount = snap.ErrorCount
		entry.TokensUsed = snap.TokensUsed
	}

	// Overlay activation tier when available.
	if g.config.ActivationController != nil {
		for _, entry := range known {
			if entry.AgentType != "" {
				tier, err := g.config.ActivationController.TierOf(entry.AgentType)
				if err == nil {
					entry.ActivationTier = tierName(tier)
				}
			}
		}
	}

	agents := make([]agentStatusEntry, 0, len(known))
	for _, entry := range known {
		agents = append(agents, *entry)
	}

	return map[string]any{
		"agents":      agents,
		"agent_count": len(agents),
	}, nil
}

func agentHealthStatusSingle(g *Guardian, agentID string) (any, error) {
	entry := &agentStatusEntry{AgentID: agentID}

	g.knownMu.RLock()
	if ann, ok := g.knownAgents[agentID]; ok {
		entry.AgentType = ann.AgentType
		entry.AgentName = ann.AgentName
		entry.Ready = ann.Ready
		entry.ReadyAt = ann.ReadyAt
	}
	g.knownMu.RUnlock()

	if snap, ok := g.healthMon.AgentSnapshot(agentID); ok {
		entry.Responsive = snap.Responsive
		entry.ErrorCount = snap.ErrorCount
		entry.TokensUsed = snap.TokensUsed
	}

	if g.config.ActivationController != nil && entry.AgentType != "" {
		tier, err := g.config.ActivationController.TierOf(entry.AgentType)
		if err == nil {
			entry.ActivationTier = tierName(tier)
		}
	}

	found := entry.AgentName != "" || entry.Responsive
	return map[string]any{
		"found":  found,
		"agent":  entry,
	}, nil
}

// agentHealthRegistry returns the full registry view from knownAgents.
func agentHealthRegistry(g *Guardian) (any, error) {
	g.knownMu.RLock()
	defer g.knownMu.RUnlock()

	type registryEntry struct {
		AgentID   string   `json:"agent_id"`
		AgentType string   `json:"agent_type"`
		AgentName string   `json:"agent_name"`
		Ready     bool     `json:"ready"`
		ReadyAt   string   `json:"ready_at,omitempty"`
		Intents   []string `json:"intents,omitempty"`
		Domains   []string `json:"domains,omitempty"`
	}

	agents := make([]registryEntry, 0, len(g.knownAgents))
	readyCount := 0
	for _, ann := range g.knownAgents {
		entry := registryEntry{
			AgentID:   ann.AgentID,
			AgentType: ann.AgentType,
			AgentName: ann.AgentName,
			Ready:     ann.Ready,
		}
		if !ann.ReadyAt.IsZero() {
			entry.ReadyAt = ann.ReadyAt.Format(time.RFC3339)
		}
		if ann.Capabilities != nil {
			for _, intent := range ann.Capabilities.Intents {
				entry.Intents = append(entry.Intents, string(intent))
			}
			for _, domain := range ann.Capabilities.Domains {
				entry.Domains = append(entry.Domains, string(domain))
			}
		}
		agents = append(agents, entry)
		if ann.Ready {
			readyCount++
		}
	}

	return map[string]any{
		"agents":      agents,
		"total":       len(agents),
		"total_ready": readyCount,
	}, nil
}

// agentHealthActivation returns activation tier and metrics for each known agent type.
func agentHealthActivation(g *Guardian) (any, error) {
	if g.config.ActivationController == nil {
		return map[string]any{
			"available": false,
			"reason":    "activation controller not wired",
		}, nil
	}

	g.knownMu.RLock()
	agentTypes := make(map[string]bool, len(g.knownAgents))
	for _, ann := range g.knownAgents {
		agentTypes[ann.AgentType] = true
	}
	g.knownMu.RUnlock()

	type tierEntry struct {
		AgentType string `json:"agent_type"`
		Tier      string `json:"tier"`
		TierValue int32  `json:"tier_value"`
	}

	tiers := make([]tierEntry, 0, len(agentTypes))
	for agentType := range agentTypes {
		tier, err := g.config.ActivationController.TierOf(agentType)
		if err != nil {
			continue
		}
		tiers = append(tiers, tierEntry{
			AgentType: agentType,
			Tier:      tierName(tier),
			TierValue: tier,
		})
	}

	result := map[string]any{
		"available":   true,
		"tiers":       tiers,
		"entry_count": g.config.ActivationController.EntryCount(),
	}

	if g.config.ActivationMetrics != nil {
		result["metrics"] = g.config.ActivationMetrics.Snapshot()
	}

	return result, nil
}

// agentStatusEntry is the merged status returned by the status action.
type agentStatusEntry struct {
	AgentID        string    `json:"agent_id"`
	AgentType      string    `json:"agent_type,omitempty"`
	AgentName      string    `json:"agent_name,omitempty"`
	Ready          bool      `json:"ready"`
	ReadyAt        time.Time `json:"ready_at,omitempty"`
	Responsive     bool      `json:"responsive,omitempty"`
	ErrorCount     int       `json:"error_count,omitempty"`
	TokensUsed     int64     `json:"tokens_used,omitempty"`
	ActivationTier string    `json:"activation_tier,omitempty"`
}

// tierName maps an int32 tier value to a human-readable name.
func tierName(tier int32) string {
	switch tier {
	case 0:
		return "cold"
	case 1:
		return "cool"
	case 2:
		return "warm"
	case 3:
		return "hot"
	default:
		return "unknown"
	}
}
