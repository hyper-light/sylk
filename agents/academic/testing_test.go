package academic

import (
	"testing"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/agents/identity/registries"
	"github.com/adalundhe/sylk/core/handoff"
)

// newTestFactory builds a session-scoped identity.Factory bound to a
// synthetic namespace and the full production pod layout so Mint
// succeeds for every agent kind. Tests that construct an Academic
// agent pass the returned factory via Config.Factory so the agent's
// strict New() contract is satisfied.
func newTestFactory(t *testing.T) *identity.Factory {
	t.Helper()
	models := registries.NewHandoffModelRegistry(handoff.NewDescriptorRegistry())
	pods := registries.NewStaticPodRegistry(map[identity.PodID]identity.PodType{
		"guide":        identity.PodTypeDaemon,
		"orchestrator": identity.PodTypeDaemon,
		"guardian":     identity.PodTypeDaemon,
		"architect":    identity.PodTypeSingleton,
		"inspector":    identity.PodTypeSingleton,
		"tester":       identity.PodTypeSingleton,
		"librarian":    identity.PodTypeSingleton,
		"archivalist":  identity.PodTypeSingleton,
		"academic":     identity.PodTypeSingleton,
		"pipeline":     identity.PodTypePipeline,
	}, "pipeline")
	f, err := identity.NewFactory(identity.FactoryConfig{
		Namespace: "test-session",
		Models:    models,
		Pods:      pods,
	})
	if err != nil {
		t.Fatalf("new test factory: %v", err)
	}
	return f
}
