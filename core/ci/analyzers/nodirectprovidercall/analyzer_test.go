package nodirectprovidercall

import "testing"

func TestIsAllowedPackage_CoversBoundary(t *testing.T) {
	for _, p := range []string{
		"github.com/adalundhe/sylk/core/providers",
		"github.com/adalundhe/sylk/core/oauth",
	} {
		if !isAllowedPackage(p) {
			t.Errorf("%s should be allowlisted", p)
		}
	}
}

func TestIsAllowedPackage_BlocksAgents(t *testing.T) {
	for _, p := range []string{
		"github.com/adalundhe/sylk/agents/architect",
		"github.com/adalundhe/sylk/agents/orchestrator",
		"github.com/adalundhe/sylk/agents/tester/pipeline",
	} {
		if isAllowedPackage(p) {
			t.Errorf("agent %s must NOT be allowlisted", p)
		}
	}
}

func TestBannedImports_CoversKnownProviders(t *testing.T) {
	for _, want := range []string{
		"github.com/anthropics/anthropic-sdk-go",
		"google.golang.org/genai",
		"github.com/openai/openai-go",
	} {
		if _, ok := bannedImports[want]; !ok {
			t.Errorf("provider SDK %q should be banned outside the allowlist", want)
		}
	}
}
