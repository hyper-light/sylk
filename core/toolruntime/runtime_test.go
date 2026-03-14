package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

func TestNew_ValidatesManifestAndActivatesDefaults(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("visible_tool", "Visible tool"))

	rt, err := New(Config{
		Registry: registry,
		Manifest: NewManifest("architect", "architect.default",
			NewToolPolicy("visible_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
			NewToolPolicy(SearchToolName, EffectReadOnly, DomainDiscovery, ExecutionModeLocal, WithVisibleByDefault(), WithSearchable(false)),
		),
	})
	if err != nil {
		t.Fatalf("unexpected runtime construction error: %v", err)
	}

	tools := rt.BuildToolDefinitions()
	if len(tools) != 2 {
		t.Fatalf("expected 2 visible tools, got %d", len(tools))
	}
	if tools[0].Name != SearchToolName && tools[1].Name != SearchToolName {
		t.Fatalf("expected search tool to be visible by default")
	}
}

func TestNew_RejectsManifestMissingRegisteredTool(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("visible_tool", "Visible tool"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("missing_tool", "Missing tool"))

	_, err := New(Config{
		Registry: registry,
		Manifest: NewManifest("architect", "architect.default",
			NewToolPolicy("visible_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
			NewToolPolicy(SearchToolName, EffectReadOnly, DomainDiscovery, ExecutionModeLocal, WithVisibleByDefault(), WithSearchable(false)),
		),
	})
	if err == nil || !strings.Contains(err.Error(), "missing_tool") {
		t.Fatalf("expected missing registered tool error, got %v", err)
	}
}

func TestBuildToolDefinitions_UsesActiveSetNotRegistryLoading(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("visible_tool", "Visible tool"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("loaded_but_inactive", "Loaded but inactive"))
	registry.Load("visible_tool")
	registry.Load("loaded_but_inactive")

	rt := mustNewRuntime(t, registry, NewManifest("architect", "architect.default",
		NewToolPolicy("visible_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
		NewToolPolicy("loaded_but_inactive", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
		NewToolPolicy(SearchToolName, EffectReadOnly, DomainDiscovery, ExecutionModeLocal, WithVisibleByDefault(), WithSearchable(false)),
	))

	tools := rt.BuildToolDefinitions()
	if containsTool(tools, "loaded_but_inactive") {
		t.Fatal("loaded but inactive tool should not be visible")
	}
}

func TestCompileToolDescription_IncludesWorkflowMetadata(t *testing.T) {
	skill := skills.NewSkill("write_test").
		Description("Write a concrete test.").
		Usage("Use after planning.").
		Requirement("Prepare the output path first.").
		Satisfies("Creates a test artifact.").
		Avoid("Do not use for placeholders.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	desc := compileToolDescription(skill, NewToolPolicy("write_test", EffectMutating, DomainFilesystem, ExecutionModeLocal))
	for _, want := range []string{
		"Use when: Use after planning.",
		"Requirements: Prepare the output path first.",
		"Satisfies: Creates a test artifact.",
		"Avoid: Do not use for placeholders.",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("compiled description missing %q: %s", want, desc)
		}
	}
}

func TestRequestView_TransientActivationDoesNotMutateSharedActiveSet(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("visible_tool", "Visible tool"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("reroute_request", "Request scoped tool"))

	rt := mustNewRuntime(t, registry, manifestWithSearch("academic", "academic.default",
		NewToolPolicy("visible_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
		NewToolPolicy("reroute_request", EffectMutating, DomainControl, ExecutionModeLocalWorker),
	))

	baseTools := rt.BuildToolDefinitions()
	if containsTool(baseTools, "reroute_request") {
		t.Fatal("request-scoped tool should not be visible in shared tool definitions")
	}

	view, err := rt.RequestView("reroute_request")
	if err != nil {
		t.Fatalf("request view: %v", err)
	}
	viewTools := view.BuildToolDefinitions()
	if !containsTool(viewTools, "reroute_request") {
		t.Fatal("request-scoped tool should be visible inside the request view")
	}

	_, err = view.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "call-reroute", Name: "reroute_request", Arguments: `{}`},
		AgentID:         "academic",
		CorrelationID:   "corr-reroute",
		CapabilityScope: "academic.default",
	})
	if err != nil {
		t.Fatalf("request view should admit transient tool execution, got %v", err)
	}

	if _, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "call-reroute-shared", Name: "reroute_request", Arguments: `{}`},
		AgentID:         "academic",
		CorrelationID:   "corr-reroute-shared",
		CapabilityScope: "academic.default",
	}); err == nil || !strings.Contains(err.Error(), "active tool set") {
		t.Fatalf("shared runtime should still reject transient tool outside request view, got %v", err)
	}

	finalTools := rt.BuildToolDefinitions()
	if containsTool(finalTools, "reroute_request") {
		t.Fatal("request-scoped activation leaked into shared tool definitions")
	}
}

func TestScopedView_RestrictsToRequestedTools(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("visible_tool", "Visible tool"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("scoped_tool", "Scoped tool"))

	rt := mustNewRuntime(t, registry, manifestWithSearch("tester", "tester.pipeline",
		NewToolPolicy("visible_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
		NewToolPolicy("scoped_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))

	view, err := rt.ScopedView("scoped_tool")
	if err != nil {
		t.Fatalf("scoped view: %v", err)
	}
	tools := view.BuildToolDefinitions()
	if containsTool(tools, "visible_tool") {
		t.Fatal("scoped view should not inherit shared default-visible tools")
	}
	if !containsTool(tools, "scoped_tool") {
		t.Fatal("scoped view should include the requested scoped tool")
	}
}

func TestRequestView_SyncActiveFromLoadedDoesNotMutateSharedActiveSet(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("loaded_tool", "Loaded tool"))
	registry.Load("loaded_tool")

	rt := mustNewRuntime(t, registry, manifestWithSearch("academic", "academic.default",
		NewToolPolicy("loaded_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))

	if containsTool(rt.BuildToolDefinitions(), "loaded_tool") {
		t.Fatal("loaded tool should not be visible in shared definitions before activation")
	}

	view, err := rt.RequestView()
	if err != nil {
		t.Fatalf("request view: %v", err)
	}
	activated := view.SyncActiveFromLoaded()
	if len(activated) != 1 || activated[0] != "loaded_tool" {
		t.Fatalf("loaded-tool activation = %v, want [loaded_tool]", activated)
	}
	if !containsTool(view.BuildToolDefinitions(), "loaded_tool") {
		t.Fatal("loaded tool should be visible inside request view after sync")
	}
	if containsTool(rt.BuildToolDefinitions(), "loaded_tool") {
		t.Fatal("request-view sync should not leak loaded tool into shared definitions")
	}
}

func TestRequestView_SearchActivationRemainsTransient(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("grep").
		Description("Search file contents by pattern.").
		StringParam("pattern", "Pattern", true).
		Domain("filesystem").
		Keywords("search", "pattern", "files").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var payload map[string]any
			_ = json.Unmarshal(input, &payload)
			return map[string]any{"pattern": payload["pattern"]}, nil
		}).
		Build())
	rt := mustNewRuntime(t, registry, manifestWithSearch("academic", "academic.default",
		NewToolPolicy("grep", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))

	view, err := rt.RequestView()
	if err != nil {
		t.Fatalf("request view: %v", err)
	}
	searchResult, err := view.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "search-1", Name: SearchToolName, Arguments: `{"query":"search files by pattern","activate":true}`},
		AgentID:         "academic",
		CorrelationID:   "corr-search",
		CapabilityScope: "academic.default",
	})
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}
	if !searchResult.ToolDefsDirty {
		t.Fatal("expected transient search activation to dirty tool definitions")
	}
	if !containsTool(view.BuildToolDefinitions(), "grep") {
		t.Fatal("request-view search activation should expose grep inside the request view")
	}
	if containsTool(rt.BuildToolDefinitions(), "grep") {
		t.Fatal("request-view search activation should not leak into shared definitions")
	}
	if _, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "grep-shared", Name: "grep", Arguments: `{"pattern":"TODO"}`},
		AgentID:         "academic",
		CorrelationID:   "corr-shared",
		CapabilityScope: "academic.default",
	}); err == nil || !strings.Contains(err.Error(), "active tool set") {
		t.Fatalf("shared runtime should still reject transiently activated grep, got %v", err)
	}
}

func TestExecute_RejectsInvocationIdentityMismatch(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("echo", "Echo tool"))
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("echo", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
	))

	_, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "call-1", Name: "echo", Arguments: `{}`},
		AgentID:         "guardian",
		CorrelationID:   "corr-1",
		CapabilityScope: "architect.default",
	})
	if err == nil || !strings.Contains(err.Error(), "agent mismatch") {
		t.Fatalf("expected agent mismatch error, got %v", err)
	}
}

func TestExecute_SearchActivatesWithoutLoadingAndExecutionLoadsLater(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("grep").
		Description("Search file contents by pattern.").
		StringParam("pattern", "Pattern", true).
		Domain("filesystem").
		Keywords("search", "pattern", "files").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var payload map[string]any
			_ = json.Unmarshal(input, &payload)
			return map[string]any{"pattern": payload["pattern"]}, nil
		}).
		Build())
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("grep", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))

	searchResult, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "search-1", Name: SearchToolName, Arguments: `{"query":"search files by pattern","activate":true}`},
		AgentID:         "architect",
		CorrelationID:   "corr-search",
		CapabilityScope: "architect.default",
	})
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}
	if !searchResult.ToolDefsDirty {
		t.Fatal("expected search activation to dirty tool definitions")
	}
	if skill := registry.Get("grep"); skill == nil || skill.Loaded {
		t.Fatal("search activation should not load the skill implementation")
	}

	execResult, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "grep-1", Name: "grep", Arguments: `{"pattern":"TODO"}`},
		AgentID:         "architect",
		CorrelationID:   "corr-search",
		CapabilityScope: "architect.default",
	})
	if err != nil {
		t.Fatalf("unexpected grep execution error: %v", err)
	}
	if execResult.ToolDefsDirty {
		t.Fatal("executing an already-active tool should not dirty tool definitions")
	}
	if skill := registry.Get("grep"); skill == nil || !skill.Loaded {
		t.Fatal("expected grep to load on execution after activation")
	}
	assertJSONContains(t, execResult.Output, "pattern", "TODO")
}

func TestExecute_PanickingLocalToolReturnsError(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newPanickingRuntimeTestSkill("panic_local"))
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("panic_local", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
	))

	_, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "panic-local-1", Name: "panic_local", Arguments: `{}`},
		AgentID:         "architect",
		CorrelationID:   "corr-panic-local",
		CapabilityScope: "architect.default",
	})
	if err == nil || !strings.Contains(err.Error(), `tool runtime panic in "panic_local"`) {
		t.Fatalf("expected named panic error, got %v", err)
	}
}

func TestExecute_PanickingWorkerToolReturnsError(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newPanickingRuntimeTestSkill("panic_worker"))
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("panic_worker", EffectMutating, DomainFilesystem, ExecutionModeLocalWorker, WithVisibleByDefault()),
	))

	_, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "panic-worker-1", Name: "panic_worker", Arguments: `{}`},
		AgentID:         "architect",
		CorrelationID:   "corr-panic-worker",
		CapabilityScope: "architect.default",
	})
	if err == nil || !strings.Contains(err.Error(), `tool runtime panic in "panic_worker"`) {
		t.Fatalf("expected named panic error, got %v", err)
	}
}

func TestExecute_DisallowsInactiveToolEvenIfManifestAllowsIt(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("hidden_tool", "Hidden tool"))
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("hidden_tool", EffectReadOnly, DomainFilesystem, ExecutionModeLocal),
	))

	_, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "call-hidden", Name: "hidden_tool", Arguments: `{}`},
		AgentID:         "architect",
		CorrelationID:   "corr-hidden",
		CapabilityScope: "architect.default",
	})
	if err == nil || !strings.Contains(err.Error(), "active tool set") {
		t.Fatalf("expected inactive-tool-set rejection, got %v", err)
	}
}

func TestValidateBatch_AllowsCrossDomainAndSameDomainRepeats(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, newRuntimeTestSkill("read_file", "Read file"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("consult", "Consult"))
	mustRegisterSkill(t, registry, newRuntimeTestSkill("grep", "Search"))

	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("read_file", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
		NewToolPolicy("grep", EffectReadOnly, DomainFilesystem, ExecutionModeLocal, WithVisibleByDefault()),
		NewToolPolicy("consult", EffectReadOnly, DomainKnowledge, ExecutionModeLocal, WithVisibleByDefault()),
	))

	err := rt.ValidateBatch([]Invocation{
		{
			ToolCall:        providers.ToolCall{ID: "1", Name: "read_file", Arguments: `{"path":"a"}`},
			AgentID:         "architect",
			CorrelationID:   "corr-batch",
			CapabilityScope: "architect.default",
		},
		{
			ToolCall:        providers.ToolCall{ID: "2", Name: "consult", Arguments: `{"q":"x"}`},
			AgentID:         "architect",
			CorrelationID:   "corr-batch",
			CapabilityScope: "architect.default",
		},
	})
	if err != nil {
		t.Fatalf("expected cross-domain batch to pass, got %v", err)
	}

	err = rt.ValidateBatch([]Invocation{
		{
			ToolCall:        providers.ToolCall{ID: "1", Name: "grep", Arguments: `{"pattern":"a"}`},
			AgentID:         "architect",
			CorrelationID:   "corr-batch",
			CapabilityScope: "architect.default",
		},
		{
			ToolCall:        providers.ToolCall{ID: "2", Name: "grep", Arguments: `{"pattern":"b"}`},
			AgentID:         "architect",
			CorrelationID:   "corr-batch",
			CapabilityScope: "architect.default",
		},
	})
	if err != nil {
		t.Fatalf("expected same-domain repeated batch to pass, got %v", err)
	}
}

func TestExecute_ApprovalSensitiveToolRequiresGuardianGrant(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("ask_user_question").
		Description("Ask the user a question.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"asked": true}, nil
		}).
		Build())

	controlPlane := &stubGuardianControlPlane{}
	rt := mustNewRuntimeWithConfig(t, Config{
		Registry: registry,
		Manifest: manifestWithSearch("architect", "architect.default",
			NewToolPolicy("ask_user_question", EffectMutating, DomainControl, ExecutionModeGuardian, WithApprovalSensitive(), WithVisibleByDefault()),
		),
		State:                NewState(),
		GuardianControlPlane: controlPlane,
	})

	result, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "ask-1", Name: "ask_user_question", Arguments: `{}`},
		AgentID:         "architect",
		CorrelationID:   "corr-ask",
		CapabilityScope: "architect.default",
	})
	if err != nil {
		t.Fatalf("unexpected approval-sensitive execution error: %v", err)
	}
	if controlPlane.lastRequest == nil {
		t.Fatal("expected guardian control plane to receive a grant request")
	}
	if controlPlane.lastRequest.ToolName != "ask_user_question" {
		t.Fatalf("expected guardian request for ask_user_question, got %+v", controlPlane.lastRequest)
	}
	assertJSONContains(t, result.Output, "asked", "true")
}

func TestExecute_PreservesSentinelErrors(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("reroute_request").
		Description("Request a reroute.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, skills.ErrRerouteRequested
		}).
		Build())
	rt := mustNewRuntime(t, registry, manifestWithSearch("architect", "architect.default",
		NewToolPolicy("reroute_request", EffectMutating, DomainControl, ExecutionModeLocalWorker, WithVisibleByDefault()),
	))

	_, err := rt.Execute(context.Background(), Invocation{
		ToolCall:        providers.ToolCall{ID: "reroute-1", Name: "reroute_request", Arguments: `{}`},
		AgentID:         "architect",
		CorrelationID:   "corr-reroute",
		CapabilityScope: "architect.default",
	})
	if !errors.Is(err, skills.ErrRerouteRequested) {
		t.Fatalf("expected reroute sentinel to survive wrapping, got %v", err)
	}
}

type stubGuardianControlPlane struct {
	lastRequest *GuardianControlRequest
}

func (s *stubGuardianControlPlane) RequestGrant(ctx context.Context, req GuardianControlRequest) (*GuardianControlGrant, error) {
	s.lastRequest = &req
	return &GuardianControlGrant{
		GrantID:           "grant-1",
		AgentID:           req.AgentID,
		CorrelationID:     req.CorrelationID,
		CapabilityScope:   req.CapabilityScope,
		ToolName:          req.ToolName,
		ArgumentsHash:     req.ArgumentsHash,
		PolicyFingerprint: req.PolicyFingerprint,
		Approved:          true,
		ExpiresAt:         time.Now().Add(time.Minute),
	}, nil
}

func manifestWithSearch(agentID, scope string, policies ...ToolPolicy) *PolicyManifest {
	policies = append(policies, NewToolPolicy(SearchToolName, EffectReadOnly, DomainDiscovery, ExecutionModeLocal, WithVisibleByDefault(), WithSearchable(false)))
	return NewManifest(agentID, scope, policies...)
}

func mustNewRuntime(t *testing.T, registry *skills.Registry, manifest *PolicyManifest) *Runtime {
	t.Helper()
	return mustNewRuntimeWithConfig(t, Config{
		Registry: registry,
		Manifest: manifest,
		State:    NewState(),
	})
}

func mustNewRuntimeWithConfig(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("construct runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func containsTool(tools []providers.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func newRuntimeTestSkill(name, description string) *skills.Skill {
	return skills.NewSkill(name).
		Description(description).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"name": name}, nil
		}).
		Build()
}

func newPanickingRuntimeTestSkill(name string) *skills.Skill {
	return skills.NewSkill(name).
		Description("Panics for runtime recovery tests.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			panic("boom")
		}).
		Build()
}

func mustRegisterSkill(t *testing.T, registry *skills.Registry, skill *skills.Skill) {
	t.Helper()
	if err := registry.Register(skill); err != nil {
		t.Fatalf("register skill %q: %v", skill.Name, err)
	}
}

func assertJSONContains(t *testing.T, raw, key, expected string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	value := payload[key]
	switch typed := value.(type) {
	case string:
		if typed != expected {
			t.Fatalf("expected %q=%q, got %#v", key, expected, value)
		}
	case bool:
		if typed != (expected == "true") {
			t.Fatalf("expected %q=%q, got %#v", key, expected, value)
		}
	default:
		t.Fatalf("unexpected value type for %q: %#v", key, value)
	}
}
