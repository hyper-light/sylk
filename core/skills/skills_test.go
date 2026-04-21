package skills

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNewSkill_Builder(t *testing.T) {
	skill := NewSkill("test_skill").
		Description("A test skill").
		Domain("testing").
		Keywords("test", "verify").
		Requirement("Run after the target file is identified.").
		Satisfies("Produces validation evidence.").
		Avoid("Do not use for speculative checks.").
		Priority(10).
		StringParam("input", "The input string", true).
		IntParam("count", "Number of times", false).
		BoolParam("verbose", "Enable verbose output", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return "success", nil
		}).
		Build()

	assertSkillName(t, skill, "test_skill")
	assertSkillDescription(t, skill, "A test skill")
	assertSkillDomain(t, skill, "testing")
	assertSkillKeywordsLen(t, skill, 2)
	assertSkillPriority(t, skill, 10)
	assertInputSchemaPropertiesLen(t, skill, 3)
	assertInputSchemaRequiredLen(t, skill, 1)
	if len(skill.Requirements) != 1 || len(skill.Satisfies) != 1 || len(skill.Avoids) != 1 {
		t.Fatalf("expected workflow metadata to be captured, got requirements=%v satisfies=%v avoids=%v", skill.Requirements, skill.Satisfies, skill.Avoids)
	}
}

func assertSkillName(t *testing.T, skill *Skill, expected string) {
	if skill.Name != expected {
		t.Errorf("expected name '%s', got %s", expected, skill.Name)
	}
}

func assertSkillDescription(t *testing.T, skill *Skill, expected string) {
	if skill.Description != expected {
		t.Errorf("expected description '%s', got %s", expected, skill.Description)
	}
}

func assertSkillDomain(t *testing.T, skill *Skill, expected string) {
	if skill.Domain != expected {
		t.Errorf("expected domain '%s', got %s", expected, skill.Domain)
	}
}

func assertSkillKeywordsLen(t *testing.T, skill *Skill, expected int) {
	if len(skill.Keywords) != expected {
		t.Errorf("expected %d keywords, got %d", expected, len(skill.Keywords))
	}
}

func assertSkillPriority(t *testing.T, skill *Skill, expected int) {
	if skill.Priority != expected {
		t.Errorf("expected priority %d, got %d", expected, skill.Priority)
	}
}

func assertInputSchemaPropertiesLen(t *testing.T, skill *Skill, expected int) {
	if len(skill.InputSchema.Properties) != expected {
		t.Errorf("expected %d properties, got %d", expected, len(skill.InputSchema.Properties))
	}
}

func assertInputSchemaRequiredLen(t *testing.T, skill *Skill, expected int) {
	if len(skill.InputSchema.Required) != expected {
		t.Errorf("expected %d required param, got %d", expected, len(skill.InputSchema.Required))
	}
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("my_skill").
		Description("Test").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	err := registry.Register(skill)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Register without name should fail
	err = registry.Register(&Skill{Handler: func(ctx context.Context, input json.RawMessage) (any, error) { return nil, nil }})
	if err == nil {
		t.Error("expected error for skill without name")
	}

	// Register without handler should fail
	err = registry.Register(&Skill{Name: "no_handler"})
	if err == nil {
		t.Error("expected error for skill without handler")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("finder").
		Description("Finds things").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	registry.Register(skill)

	found := registry.Get("finder")
	if found == nil {
		t.Fatal("expected to find skill")
	}
	if found.Name != "finder" {
		t.Errorf("expected name 'finder', got %s", found.Name)
	}

	missing := registry.Get("nonexistent")
	if missing != nil {
		t.Error("expected nil for missing skill")
	}
}

func TestRegistry_GetByDomain(t *testing.T) {
	registry := NewRegistry()

	for i, name := range []string{"skill1", "skill2", "skill3"} {
		domain := "domain_a"
		if i == 2 {
			domain = "domain_b"
		}
		skill := NewSkill(name).
			Description("Test").
			Domain(domain).
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	domainA := registry.GetByDomain("domain_a")
	if len(domainA) != 2 {
		t.Errorf("expected 2 skills in domain_a, got %d", len(domainA))
	}

	domainB := registry.GetByDomain("domain_b")
	if len(domainB) != 1 {
		t.Errorf("expected 1 skill in domain_b, got %d", len(domainB))
	}
}

func TestRegistry_GetByKeyword(t *testing.T) {
	registry := NewRegistry()

	skill1 := NewSkill("search").
		Description("Search").
		Keywords("find", "search", "query").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	skill2 := NewSkill("lookup").
		Description("Lookup").
		Keywords("find", "lookup").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	registry.Register(skill1)
	registry.Register(skill2)

	byFind := registry.GetByKeyword("find")
	if len(byFind) != 2 {
		t.Errorf("expected 2 skills for 'find', got %d", len(byFind))
	}

	bySearch := registry.GetByKeyword("search")
	if len(bySearch) != 1 {
		t.Errorf("expected 1 skill for 'search', got %d", len(bySearch))
	}
}

func TestRegistry_Invoke_Success(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("echo").
		Description("Echoes input").
		StringParam("message", "Message to echo", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Message string `json:"message"`
			}
			json.Unmarshal(input, &params)
			return map[string]string{"echoed": params.Message}, nil
		}).
		Build()

	registry.Register(skill)

	input := json.RawMessage(`{"message": "hello"}`)
	result := registry.Invoke(context.Background(), "echo", input)

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.SkillName != "echo" {
		t.Errorf("expected skill name 'echo', got %s", result.SkillName)
	}

	data, ok := result.Data.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", result.Data)
	}
	if data["echoed"] != "hello" {
		t.Errorf("expected 'hello', got %s", data["echoed"])
	}
}

func TestRegistry_Invoke_Error(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("failing").
		Description("Always fails").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, errors.New("intentional failure")
		}).
		Build()

	registry.Register(skill)

	result := registry.Invoke(context.Background(), "failing", nil)

	if result.Success {
		t.Error("expected failure")
	}
	if result.Error != "intentional failure" {
		t.Errorf("expected 'intentional failure', got %s", result.Error)
	}
}

func TestRegistry_Invoke_NotFound(t *testing.T) {
	registry := NewRegistry()

	result := registry.Invoke(context.Background(), "nonexistent", nil)

	if result.Success {
		t.Error("expected failure for nonexistent skill")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

func TestRegistry_Invoke_TracksCount(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("counter").
		Description("Test").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	registry.Register(skill)

	for i := 0; i < 5; i++ {
		registry.Invoke(context.Background(), "counter", nil)
	}

	found := registry.Get("counter")
	if found.InvokeCount != 5 {
		t.Errorf("expected invoke count 5, got %d", found.InvokeCount)
	}
}

func TestRegistry_LoadUnload(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("loadable").
		Description("Test").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	registry.Register(skill)

	if skill.Loaded {
		t.Error("skill should not be loaded initially")
	}

	ok := registry.Load("loadable")
	if !ok {
		t.Error("expected Load to return true")
	}
	if !skill.Loaded {
		t.Error("skill should be loaded")
	}

	ok = registry.Unload("loadable")
	if !ok {
		t.Error("expected Unload to return true")
	}
	if skill.Loaded {
		t.Error("skill should not be loaded")
	}

	// Load nonexistent
	ok = registry.Load("nonexistent")
	if ok {
		t.Error("expected Load to return false for nonexistent skill")
	}
}

func TestRegistry_LoadDomain(t *testing.T) {
	registry := NewRegistry()

	for _, name := range []string{"a", "b", "c"} {
		skill := NewSkill(name).
			Description("Test").
			Domain("test_domain").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	count := registry.LoadDomain("test_domain")
	if count != 3 {
		t.Errorf("expected 3 skills loaded, got %d", count)
	}

	loaded := registry.GetLoaded()
	if len(loaded) != 3 {
		t.Errorf("expected 3 loaded skills, got %d", len(loaded))
	}

	// Loading again should return 0 (already loaded)
	count = registry.LoadDomain("test_domain")
	if count != 0 {
		t.Errorf("expected 0 newly loaded, got %d", count)
	}
}

func TestRegistry_UnloadDomain(t *testing.T) {
	registry := NewRegistry()

	for _, name := range []string{"a", "b"} {
		skill := NewSkill(name).
			Description("Test").
			Domain("unload_test").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	registry.LoadAll()

	count := registry.UnloadDomain("unload_test")
	if count != 2 {
		t.Errorf("expected 2 unloaded, got %d", count)
	}

	loaded := registry.GetLoaded()
	if len(loaded) != 0 {
		t.Errorf("expected 0 loaded skills, got %d", len(loaded))
	}
}

func TestRegistry_LoadAllUnloadAll(t *testing.T) {
	registry := NewRegistry()

	for _, name := range []string{"x", "y", "z"} {
		skill := NewSkill(name).
			Description("Test").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	registry.LoadAll()
	if len(registry.GetLoaded()) != 3 {
		t.Error("expected all 3 skills loaded")
	}

	registry.UnloadAll()
	if len(registry.GetLoaded()) != 0 {
		t.Error("expected no skills loaded")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry()

	skill := NewSkill("removable").
		Description("Test").
		Domain("test").
		Keywords("remove").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	registry.Register(skill)

	if registry.Get("removable") == nil {
		t.Fatal("skill should exist")
	}

	registry.Unregister("removable")

	if registry.Get("removable") != nil {
		t.Error("skill should be removed")
	}

	// Domain and keyword indexes should be cleaned up
	if len(registry.GetByDomain("test")) != 0 {
		t.Error("domain index not cleaned up")
	}
	if len(registry.GetByKeyword("remove")) != 0 {
		t.Error("keyword index not cleaned up")
	}
}

func TestSkill_ToToolDefinition(t *testing.T) {
	skill := NewSkill("api_tool").
		Description("An API tool").
		StringParam("query", "Search query", true).
		IntParam("limit", "Max results", false).
		Build()

	def := skill.ToToolDefinition()

	if def["name"] != "api_tool" {
		t.Errorf("expected name 'api_tool', got %v", def["name"])
	}
	if def["description"] != "An API tool" {
		t.Errorf("expected description 'An API tool', got %v", def["description"])
	}

	schema, ok := def["input_schema"].(*InputSchema)
	if !ok {
		t.Fatalf("expected *InputSchema, got %T", def["input_schema"])
	}
	if schema.Type != "object" {
		t.Errorf("expected type 'object', got %s", schema.Type)
	}
}

func TestRegistry_GetToolDefinitions(t *testing.T) {
	registry := NewRegistry()

	for _, name := range []string{"tool1", "tool2", "tool3"} {
		skill := NewSkill(name).
			Description("Test " + name).
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	// Only tool1 and tool2 are loaded
	registry.Load("tool1")
	registry.Load("tool2")

	defs := registry.GetToolDefinitions()
	if len(defs) != 2 {
		t.Errorf("expected 2 tool definitions, got %d", len(defs))
	}
}

func TestRegistry_Stats(t *testing.T) {
	registry := NewRegistry()

	registerStatsSkills(registry)
	loadStatsSkills(registry)
	invokeSkillTimes(registry, "s1", 10)
	invokeSkillTimes(registry, "s2", 5)

	stats := registry.Stats()
	assertRegistryStats(t, stats)
}

func registerStatsSkills(registry *Registry) {
	registerSkillWithDomain(registry, "s1", "domain_x")
	registerSkillWithDomain(registry, "s2", "domain_x")
	registerSkillWithDomain(registry, "s3", "domain_y")
}

func registerSkillWithDomain(registry *Registry, name, domain string) {
	skill := NewSkill(name).
		Description("Test").
		Domain(domain).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()
	registry.Register(skill)
}

func loadStatsSkills(registry *Registry) {
	registry.Load("s1")
	registry.Load("s2")
}

func invokeSkillTimes(registry *Registry, name string, times int) {
	for i := 0; i < times; i++ {
		registry.Invoke(context.Background(), name, nil)
	}
}

func assertRegistryStats(t *testing.T, stats Stats) {
	assertRegistryCounts(t, stats)
	assertRegistryTopInvoked(t, stats)
}

func assertRegistryCounts(t *testing.T, stats Stats) {
	assertStatsCount(t, "total", stats.Total, 3)
	assertStatsCount(t, "loaded", stats.Loaded, 2)
	assertStatsCount(t, "domain_x", stats.ByDomain["domain_x"], 2)
	assertStatsCount(t, "domain_y", stats.ByDomain["domain_y"], 1)
	assertStatsCount(t, "top invoked", len(stats.TopInvoked), 2)
}

func assertRegistryTopInvoked(t *testing.T, stats Stats) {
	first := stats.TopInvoked[0]
	if first.Name != "s1" || first.InvokeCount != 10 {
		t.Errorf("expected s1 with 10 invokes first, got %+v", first)
	}
}

func assertStatsCount(t *testing.T, label string, actual int, expected int) {
	if actual != expected {
		t.Errorf("expected %s %d, got %d", label, expected, actual)
	}
}

func TestBuilder_ArrayParam(t *testing.T) {
	skill := NewSkill("array_test").
		Description("Test").
		ArrayParam("items", "List of items", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	prop := skill.InputSchema.Properties["items"]
	if prop.Type != "array" {
		t.Errorf("expected type 'array', got %s", prop.Type)
	}
	if prop.Items == nil || prop.Items.Type != "string" {
		t.Error("expected items type 'string'")
	}
}

func TestBuilder_ObjectParam(t *testing.T) {
	skill := NewSkill("object_test").
		Description("Test").
		ObjectParam("config", "Configuration object", map[string]*Property{
			"enabled": {Type: "boolean", Description: "Enable feature"},
			"name":    {Type: "string", Description: "Feature name"},
		}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	prop := skill.InputSchema.Properties["config"]
	if prop.Type != "object" {
		t.Errorf("expected type 'object', got %s", prop.Type)
	}
	if len(prop.Properties) != 2 {
		t.Errorf("expected 2 nested properties, got %d", len(prop.Properties))
	}
}

func TestBuilder_EnumParam(t *testing.T) {
	skill := NewSkill("enum_test").
		Description("Test").
		EnumParam("level", "Log level", []string{"debug", "info", "warn", "error"}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	prop := skill.InputSchema.Properties["level"]
	if prop.Type != "string" {
		t.Errorf("expected type 'string', got %s", prop.Type)
	}
	if len(prop.Enum) != 4 {
		t.Errorf("expected 4 enum values, got %d", len(prop.Enum))
	}
}

// TestBuilder_InheritSchemaFrom_CopiesParamsWithActionScope verifies that a
// façade that inherits from two delegates ends up with the union of their
// params, each tagged with the action(s) that use it. The delegates'
// structural fields (type, nested properties, enum) must survive the copy.
func TestBuilder_InheritSchemaFrom_CopiesParamsWithActionScope(t *testing.T) {
	finalize := NewSkill("finalize_pipeline").
		ArrayObjectParam(
			"targets",
			"Per-recipient verification specs.",
			map[string]*Property{
				"target":  {Type: "string", Description: "Recipient agent."},
				"summary": {Type: "string", Description: "What the recipient should know."},
			},
			[]string{"summary"},
			true,
		).
		Build()

	challenge := NewSkill("challenge_agent").
		StringParam("reason", "Why the challenge is being issued.", false).
		EnumArrayParam("target_agents", "Challenge targets.", "string", []string{"engineer", "designer"}, false).
		Build()

	facade := NewSkill("pipeline_protocol").
		EnumParam("action", "Protocol action", []string{"finalize", "challenge"}, true).
		InheritSchemaFrom(finalize, "finalize").
		InheritSchemaFrom(challenge, "challenge").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	targets := facade.InputSchema.Properties["targets"]
	if targets == nil {
		t.Fatal("expected façade to inherit `targets` from finalize delegate")
	}
	if targets.Type != "array" {
		t.Errorf("expected `targets` type array, got %s", targets.Type)
	}
	if targets.Items == nil || targets.Items.Type != "object" {
		t.Fatal("expected `targets` items to be objects")
	}
	if _, ok := targets.Items.Properties["summary"]; !ok {
		t.Error("expected nested `summary` property to survive inheritance")
	}
	if !strings.HasPrefix(targets.Description, "[action=finalize] ") {
		t.Errorf("expected targets description to start with action scope tag; got %q", targets.Description)
	}

	reason := facade.InputSchema.Properties["reason"]
	if reason == nil {
		t.Fatal("expected façade to inherit `reason` from challenge delegate")
	}
	if !strings.HasPrefix(reason.Description, "[action=challenge] ") {
		t.Errorf("expected reason description to start with action scope tag; got %q", reason.Description)
	}

	targetAgents := facade.InputSchema.Properties["target_agents"]
	if targetAgents == nil || len(targetAgents.Items.Enum) != 2 {
		t.Fatalf("expected target_agents enum of 2 values, got %+v", targetAgents)
	}
}

// TestBuilder_InheritSchemaFrom_MergesSharedParamScopes verifies that when a
// param name exists on two delegates (e.g. `summary` shared by validate and
// process_validation), the scope tag merges into a pipe-separated list.
func TestBuilder_InheritSchemaFrom_MergesSharedParamScopes(t *testing.T) {
	validate := NewSkill("validate_work").
		StringParam("summary", "Validation summary.", false).
		Build()
	process := NewSkill("process_validation").
		StringParam("summary", "Response summary.", false).
		Build()

	facade := NewSkill("pipeline_protocol").
		EnumParam("action", "Protocol action", []string{"validate", "process_validation"}, true).
		InheritSchemaFrom(validate, "validate").
		InheritSchemaFrom(process, "process_validation").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	summary := facade.InputSchema.Properties["summary"]
	if summary == nil {
		t.Fatal("expected façade to inherit `summary`")
	}
	wantPrefix := "[action=validate|process_validation] "
	if !strings.HasPrefix(summary.Description, wantPrefix) {
		t.Errorf("expected merged action scope prefix %q, got %q", wantPrefix, summary.Description)
	}
	// Re-inheriting the same action should not duplicate it.
	facade2 := NewSkill("pipeline_protocol").
		InheritSchemaFrom(validate, "validate").
		InheritSchemaFrom(validate, "validate").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()
	sum2 := facade2.InputSchema.Properties["summary"]
	if !strings.HasPrefix(sum2.Description, "[action=validate] ") {
		t.Errorf("expected de-duplicated single action tag, got %q", sum2.Description)
	}
}

// TestSkill_ValidateActionRequirements verifies the façade-level
// pre-validator: missing required fields for the chosen action return an
// error whose message names the action, lists the missing fields, and
// echoes each field's Property shape so the LLM can recover on retry.
func TestSkill_ValidateActionRequirements(t *testing.T) {
	finalize := NewSkill("finalize_pipeline").
		ArrayObjectParam(
			"targets",
			"Per-recipient verification specs.",
			map[string]*Property{
				"target":  {Type: "string"},
				"summary": {Type: "string"},
			},
			[]string{"summary"},
			true,
		).
		Build()

	facade := NewSkill("pipeline_protocol").
		EnumParam("action", "Protocol action", []string{"finalize", "handoff"}, true).
		InheritSchemaFrom(finalize, "finalize").
		ActionRequires("finalize", "targets").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return nil, nil
		}).
		Build()

	// Missing targets: should error with shape description.
	err := facade.ValidateActionRequirements("finalize", json.RawMessage(`{"action":"finalize"}`))
	if err == nil {
		t.Fatal("expected ValidateActionRequirements to reject missing targets")
	}
	msg := err.Error()
	for _, want := range []string{
		"tool \"pipeline_protocol\"",
		"action=finalize",
		"missing required field(s): targets",
		"array of object",
		"summary",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing marker %q; got: %s", want, msg)
		}
	}

	// Empty array: treated as missing (isJSONEmpty check).
	err = facade.ValidateActionRequirements("finalize", json.RawMessage(`{"action":"finalize","targets":[]}`))
	if err == nil {
		t.Error("expected empty targets array to be rejected")
	}

	// Populated targets: passes.
	err = facade.ValidateActionRequirements("finalize",
		json.RawMessage(`{"action":"finalize","targets":[{"target":"engineer","summary":"x"}]}`))
	if err != nil {
		t.Errorf("expected populated targets to pass; got: %v", err)
	}

	// Action with no requirements declared: passes trivially.
	err = facade.ValidateActionRequirements("handoff", json.RawMessage(`{"action":"handoff"}`))
	if err != nil {
		t.Errorf("expected handoff to pass when no requirements declared; got: %v", err)
	}
}
