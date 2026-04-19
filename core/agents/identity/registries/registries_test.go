package registries

import (
	"testing"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/handoff"
)

func TestHandoffModelRegistry_LooksUpDescriptors(t *testing.T) {
	src := handoff.NewDescriptorRegistry()
	r := NewHandoffModelRegistry(src)

	models := r.AllowedModels(identity.AgentTypeGuide)
	if len(models) == 0 {
		t.Fatal("expected at least one allowed model for guide")
	}
	cat, ok := r.Category(identity.AgentTypeGuide)
	if !ok || cat != identity.CategoryStandalone {
		t.Fatalf("category = %s ok=%v, want standalone/true", cat, ok)
	}
	window := r.ContextWindow(identity.AgentTypeGuide, models[0])
	if window == 0 {
		t.Fatalf("expected non-zero window for guide/%s", models[0])
	}
}

func TestHandoffModelRegistry_SystemCategoryHardcoded(t *testing.T) {
	src := handoff.NewDescriptorRegistry()
	r := NewHandoffModelRegistry(src)
	cat, ok := r.Category(identity.AgentTypeSystem)
	if !ok || cat != identity.CategorySystem {
		t.Fatalf("system category = %s ok=%v", cat, ok)
	}
}

func TestHandoffModelRegistry_UnknownKindIsCategoryUnspecified(t *testing.T) {
	src := handoff.NewDescriptorRegistry()
	r := NewHandoffModelRegistry(src)
	cat, ok := r.Category(identity.AgentTypeUnspecified)
	if ok || cat != identity.CategoryUnspecified {
		t.Fatalf("unknown category = %s ok=%v", cat, ok)
	}
}

func TestHandoffModelRegistry_OverrideAppendsModel(t *testing.T) {
	src := handoff.NewDescriptorRegistry()
	r := NewHandoffModelRegistry(src)
	base := r.AllowedModels(identity.AgentTypeArchitect)
	r.RegisterOverride(identity.AgentTypeArchitect, "claude-sonnet-4-6", 200_000)
	after := r.AllowedModels(identity.AgentTypeArchitect)
	if len(after) != len(base)+1 {
		t.Fatalf("override did not append: before=%d after=%d", len(base), len(after))
	}
	w := r.ContextWindow(identity.AgentTypeArchitect, "claude-sonnet-4-6")
	if w != 200_000 {
		t.Fatalf("override window = %d, want 200000", w)
	}
}

func TestStaticPodRegistry_LookupAndPipelineID(t *testing.T) {
	r := NewStaticPodRegistry(map[identity.PodID]identity.PodType{
		"guide":    identity.PodTypeDaemon,
		"pipeline": identity.PodTypePipeline,
	}, "pipeline")
	pt, ok := r.Lookup("guide")
	if !ok || pt != identity.PodTypeDaemon {
		t.Fatalf("guide: pt=%s ok=%v", pt, ok)
	}
	if r.PipelinePodID() != "pipeline" {
		t.Fatalf("pipeline pod id = %s", r.PipelinePodID())
	}
	_, ok = r.Lookup("missing")
	if ok {
		t.Fatal("missing pod should not resolve")
	}
}

func TestStaticPodRegistry_CopiesSourceMap(t *testing.T) {
	src := map[identity.PodID]identity.PodType{
		"guide": identity.PodTypeDaemon,
	}
	r := NewStaticPodRegistry(src, "guide")
	// Mutating the source after construction must not affect the registry.
	src["guide"] = identity.PodTypePipeline
	pt, _ := r.Lookup("guide")
	if pt != identity.PodTypeDaemon {
		t.Fatalf("registry observed source mutation: %s", pt)
	}
}

func TestEndToEndFactoryBootstrap(t *testing.T) {
	src := handoff.NewDescriptorRegistry()
	models := NewHandoffModelRegistry(src)
	pods := NewStaticPodRegistry(map[identity.PodID]identity.PodType{
		"guide": identity.PodTypeDaemon,
	}, "pipeline")

	f, err := identity.NewFactory(identity.FactoryConfig{
		Namespace: "sess-1",
		Models:    models,
		Pods:      pods,
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	id, err := f.Mint(identity.MintOptions{
		Kind: identity.AgentTypeGuide,
		Pod:  identity.PodRef{ID: "guide", Type: identity.PodTypeDaemon},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id.Kind() != identity.AgentTypeGuide {
		t.Fatalf("kind = %s", id.Kind())
	}
	if id.Category() != identity.CategoryStandalone {
		t.Fatalf("category = %s", id.Category())
	}
	if id.Window() == 0 {
		t.Fatal("window should be populated from descriptor")
	}
}
