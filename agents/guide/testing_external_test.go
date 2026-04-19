package guide_test

import (
	"testing"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/agents/identity/registries"
	"github.com/adalundhe/sylk/core/handoff"
)

// newTestFactory builds a session-scoped identity.Factory for external
// (package guide_test) tests. Mirrors the helper in testing_test.go since
// package-private helpers can't cross the guide / guide_test boundary.
// Required because the Guide constructors now mandate a non-nil Factory —
// a Guide without identity would fail the gateway's requireDispatch check
// on the first LLM call.
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
