package toolruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

func TestBuildManifestFromRegistry_FailsWhenVisibleToolIsUnregistered(t *testing.T) {
	registry := skills.NewRegistry()
	if err := registry.Register(skills.NewSkill("registered_tool").
		Description("registered tool").
		Domain("system").
		Handler(func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		}).
		Build()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	manifest := BuildManifestFromRegistry(ManifestBuildConfig{
		AgentID:          "test-agent",
		CapabilityScope:  "test.scope",
		Registry:         registry,
		VisibleByDefault: []string{"registered_tool", "missing_visible_tool"},
	})

	_, err := New(Config{
		Registry: registry,
		Manifest: manifest,
		State:    NewState(),
	})
	if err == nil {
		t.Fatal("expected runtime.New to fail for missing visible tool registration")
	}
	if !strings.Contains(err.Error(), "missing_visible_tool") {
		t.Fatalf("runtime.New() error = %v, want missing tool name", err)
	}
}

func TestBuildManifestFromRegistry_FailsWhenMutatingToolIsUnregistered(t *testing.T) {
	registry := skills.NewRegistry()
	manifest := BuildManifestFromRegistry(ManifestBuildConfig{
		AgentID:         "test-agent",
		CapabilityScope: "test.scope",
		Registry:        registry,
		Mutating:        []string{"missing_mutating_tool"},
	})

	_, err := New(Config{
		Registry: registry,
		Manifest: manifest,
		State:    NewState(),
	})
	if err == nil {
		t.Fatal("expected runtime.New to fail for missing mutating tool registration")
	}
	if !strings.Contains(err.Error(), "missing_mutating_tool") {
		t.Fatalf("runtime.New() error = %v, want missing tool name", err)
	}
}
