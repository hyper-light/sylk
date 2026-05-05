package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/toolruntime"
)

type fakeExecutor struct {
	calls []string
}

func (f *fakeExecutor) ExecuteTool(_ context.Context, _, name string, _ json.RawMessage) (any, error) {
	f.calls = append(f.calls, name)
	return "ok", nil
}

func (f *fakeExecutor) ReadResource(_ context.Context, _, _ string) (any, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeExecutor) GetPrompt(_ context.Context, _, _ string, _ map[string]string) (any, error) {
	return nil, errors.New("not implemented")
}

func TestCompileTool_ReadOnlyHintMapsToReadOnlyEffect(t *testing.T) {
	readOnly := true
	desc := mcp.ToolDescriptor{
		Name:        "list_repos",
		Description: "List repositories.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Annotations: &mcp.ToolHints{ReadOnlyHint: &readOnly},
	}
	exec := &fakeExecutor{}
	out, err := CompileTool("github", desc, CompileToolOptions{}, exec)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if out.Spec.Policy.Effect != toolruntime.EffectReadOnly {
		t.Fatalf("expected read-only, got %s", out.Spec.Policy.Effect)
	}
	if out.Spec.Policy.Execution != toolruntime.ExecutionModeLocal {
		t.Fatalf("expected local execution, got %s", out.Spec.Policy.Execution)
	}
	if out.Spec.Policy.ApprovalSensitive {
		t.Fatal("read-only tool must not be approval-sensitive")
	}
}

func TestCompileTool_UnknownEffectDefaultsToMutating(t *testing.T) {
	desc := mcp.ToolDescriptor{
		Name:        "create_repo",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
	out, err := CompileTool("github", desc, CompileToolOptions{}, &fakeExecutor{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if out.Spec.Policy.Effect != toolruntime.EffectMutating {
		t.Fatalf("expected mutating default, got %s", out.Spec.Policy.Effect)
	}
	if !out.Spec.Policy.ApprovalSensitive {
		t.Fatal("mutating tool must be approval-sensitive")
	}
	if out.Spec.Policy.Execution != toolruntime.ExecutionModeGuardian {
		t.Fatalf("expected guardian execution, got %s", out.Spec.Policy.Execution)
	}
}

func TestCompileTool_TrustedReadOnlyOverride(t *testing.T) {
	desc := mcp.ToolDescriptor{
		Name:        "search",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
	out, err := CompileTool("docs", desc, CompileToolOptions{TrustedReadOnly: true}, &fakeExecutor{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if out.Spec.Policy.Effect != toolruntime.EffectReadOnly {
		t.Fatalf("trusted_read_only override failed: %s", out.Spec.Policy.Effect)
	}
}

func TestCanonicalToolName(t *testing.T) {
	got := CanonicalToolName("GitHub", "create_pull_request")
	want := "mcp.github.create_pull_request"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCanonicalResourceURI(t *testing.T) {
	got := CanonicalResourceURI("docs", "/api/v1/spec")
	want := "mcp://docs/api/v1/spec"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSchemaFromJSON_PreservesNestedStructure(t *testing.T) {
	raw := json.RawMessage(`{
        "type": "object",
        "properties": {
            "tags": {"type": "array", "items": {"type": "string", "enum": ["a","b"]}},
            "config": {"type":"object","properties":{"max":{"type":"integer"}},"required":["max"]}
        },
        "required": ["tags"]
    }`)
	schema, err := SchemaFromJSON(raw)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "tags" {
		t.Fatalf("required mismatch: %+v", schema.Required)
	}
	tags := schema.Properties["tags"]
	if tags == nil || tags.Items == nil || len(tags.Items.Enum) != 2 {
		t.Fatalf("nested array enum lost: %+v", tags)
	}
	cfg := schema.Properties["config"]
	if cfg == nil || cfg.Properties["max"] == nil || cfg.Properties["max"].Type != "integer" {
		t.Fatalf("nested object lost: %+v", cfg)
	}
	if len(cfg.Required) != 1 || cfg.Required[0] != "max" {
		t.Fatalf("nested required lost: %+v", cfg.Required)
	}
}
