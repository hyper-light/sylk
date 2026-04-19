package nofabricprecondition

import "testing"

func TestGateFunctionPatterns_CoversInciting(t *testing.T) {
	// The original requireTestFrameworkDecision was the inciting bug.
	// The pattern set must catch any future "require…" gate.
	for _, want := range []string{
		"requireDecision",
		"requireFabric",
		"requireManifest",
		"mustHavePeer",
	} {
		found := false
		for _, p := range gateFunctionPatterns {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("pattern %q not in gateFunctionPatterns", want)
		}
	}
}

func TestIsAllowedPackage_BlocksAgents(t *testing.T) {
	for _, p := range []string{
		"github.com/adalundhe/sylk/agents/tester/pipeline",
		"github.com/adalundhe/sylk/agents/orchestrator",
	} {
		if isAllowedPackage(p) {
			t.Errorf("agent %s must NOT be allowlisted", p)
		}
	}
}
