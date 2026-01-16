package skills

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestNewSkill_Builder(t *testing.T) {
	skill := NewSkill("test_skill").
		Description("A test skill").
		Domain("testing").
		Keywords("test", "verify").
		Priority(10).
		StringParam("input", "The input string", true).
		IntParam("count", "Number of times", false).
		BoolParam("verbose", "Enable verbose output", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return "success", nil
		}).
		Build()

	if skill.Name != "test_skill" {
		t.Errorf("expected name 'test_skill', got %s", skill.Name)
	}
	if skill.Description != "A test skill" {
		t.Errorf("expected description 'A test skill', got %s", skill.Description)
	}
	if skill.Domain != "testing" {
		t.Errorf("expected domain 'testing', got %s", skill.Domain)
	}
	if len(skill.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(skill.Keywords))
	}
	if skill.Priority != 10 {
		t.Errorf("expected priority 10, got %d", skill.Priority)
	}
	if len(skill.InputSchema.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(skill.InputSchema.Properties))
	}
	if len(skill.InputSchema.Required) != 1 {
		t.Errorf("expected 1 required param, got %d", len(skill.InputSchema.Required))
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

	for i, name := range []string{"s1", "s2", "s3"} {
		domain := "domain_x"
		if i == 2 {
			domain = "domain_y"
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

	registry.Load("s1")
	registry.Load("s2")

	// Invoke s1 multiple times
	for i := 0; i < 10; i++ {
		registry.Invoke(context.Background(), "s1", nil)
	}
	for i := 0; i < 5; i++ {
		registry.Invoke(context.Background(), "s2", nil)
	}

	stats := registry.Stats()

	if stats.Total != 3 {
		t.Errorf("expected total 3, got %d", stats.Total)
	}
	if stats.Loaded != 2 {
		t.Errorf("expected loaded 2, got %d", stats.Loaded)
	}
	if stats.ByDomain["domain_x"] != 2 {
		t.Errorf("expected 2 in domain_x, got %d", stats.ByDomain["domain_x"])
	}
	if stats.ByDomain["domain_y"] != 1 {
		t.Errorf("expected 1 in domain_y, got %d", stats.ByDomain["domain_y"])
	}
	if len(stats.TopInvoked) != 2 {
		t.Errorf("expected 2 top invoked, got %d", len(stats.TopInvoked))
	}
	if stats.TopInvoked[0].Name != "s1" || stats.TopInvoked[0].InvokeCount != 10 {
		t.Errorf("expected s1 with 10 invokes first, got %+v", stats.TopInvoked[0])
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
