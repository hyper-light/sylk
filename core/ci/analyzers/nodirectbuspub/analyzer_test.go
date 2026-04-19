package nodirectbuspub

import (
	"testing"
)

// TestIsAllowedPackage_CoversBoundaryPackages asserts the allowlist
// includes the bus packages that legitimately host the Publish path.
func TestIsAllowedPackage_CoversBoundaryPackages(t *testing.T) {
	for _, p := range []string{
		"github.com/adalundhe/sylk/agents/guide",
		"github.com/adalundhe/sylk/core/events",
	} {
		if !isAllowedPackage(p) {
			t.Errorf("%s should be allowlisted", p)
		}
	}
}

// TestIsAllowedPackage_BlocksAgentPackages asserts agent packages are
// NOT allowlisted — they MUST go through the canonical Publish.
func TestIsAllowedPackage_BlocksAgentPackages(t *testing.T) {
	for _, p := range []string{
		"github.com/adalundhe/sylk/agents/orchestrator",
		"github.com/adalundhe/sylk/agents/architect",
		"github.com/adalundhe/sylk/agents/tester/pipeline",
	} {
		if isAllowedPackage(p) {
			t.Errorf("agent %s must NOT be allowlisted", p)
		}
	}
}
