package daemon

import (
	"math"

	"github.com/adalundhe/sylk/core/container"
)

// daemonAgents lists the agent types that run as daemon sets —
// always hot, never evicted, auto-restarted on failure.
// Guide is the bus router (must be first). Orchestrator coordinates
// pipeline execution.
var daemonAgents = []string{"guide", "orchestrator"}

// daemonPriority assigns scheduling priority to daemon agents.
// Guide gets the highest priority because all routing depends on it.
// Orchestrator gets the next-highest. Both are above any workload agent.
var daemonPriority = map[string]int{
	"guide":        math.MaxInt,
	"orchestrator": math.MaxInt - 1,
}

// resourcePressureToleration exempts daemon containers from eviction
// under resource pressure. Without this, the system would lose its
// core routing and orchestration capabilities.
var resourcePressureToleration = Toleration{
	Key:      "resource-pressure",
	Operator: "Exists",
	Effect:   "NoExecute",
}

// AgentDaemonSetSpecs generates DaemonSetSpec entries for always-hot agents.
// Each daemon set ensures exactly one container instance per session,
// with RestartAlways policy and resource-pressure toleration.
func AgentDaemonSetSpecs(specReg *container.AgentSpecRegistry) []DaemonSetSpec {
	specs := make([]DaemonSetSpec, 0, len(daemonAgents))

	for _, agentType := range daemonAgents {
		cSpec, err := specReg.SpecForAgent(agentType)
		if err != nil {
			continue // skip agents not in the registry
		}

		// Daemon containers always restart on exit.
		cSpec.RestartPolicy = container.RestartAlways

		specs = append(specs, DaemonSetSpec{
			Name:          agentType,
			ContainerSpec: cSpec,
			Tolerations:   []Toleration{resourcePressureToleration},
			Priority:      daemonPriority[agentType],
		})
	}

	return specs
}
