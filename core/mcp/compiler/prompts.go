package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// CompilePromptOptions tunes prompt compilation. The same shape as tool
// compilation so the catalog layer can pass one pile of options to both.
type CompilePromptOptions struct {
	ServerInstructions string
	PriorityBoost      int
}

// CompilePrompt turns one wire-side prompt descriptor into a Sylk skill that
// invokes prompts/get when called. Prompts are exposed as explicit tools — the
// model invokes them just like a tool — so they fold into ToolRuntime's
// activation/scope model. They are never injected globally into every turn.
func CompilePrompt(
	serverID string,
	desc mcp.PromptDescriptor,
	opts CompilePromptOptions,
	executor ToolExecutor,
) (*CompiledPrompt, error) {
	if executor == nil {
		return nil, fmt.Errorf("mcp compile: executor is required")
	}
	canonicalName := CanonicalPromptName(serverID, desc.Name)
	schema := promptSchemaFromArguments(desc.Arguments)
	skillSpec := buildPromptSkillSpec(canonicalName, desc, schema, opts)
	binding := MCPBindingSpec{
		ServerID:       serverID,
		CapabilityKind: string(mcp.CapabilityPrompt),
		PromptName:     desc.Name,
	}
	policy := buildPromptPolicy(canonicalName, desc)
	skill := buildPromptSkill(serverID, canonicalName, desc.Name, skillSpec, executor)
	return &CompiledPrompt{
		Spec: MCPCompiledSkillSpec{
			Skill:   skillSpec,
			Binding: binding,
			Policy:  policy,
		},
		Skill: skill,
	}, nil
}

func promptSchemaFromArguments(args []mcp.PromptArgumentDef) *skills.InputSchema {
	properties := map[string]*skills.Property{}
	required := []string{}
	for _, arg := range args {
		properties[arg.Name] = &skills.Property{
			Type:        "string",
			Description: arg.Description,
		}
		if arg.Required {
			required = append(required, arg.Name)
		}
	}
	return &skills.InputSchema{Type: "object", Properties: properties, Required: required}
}

func buildPromptSkillSpec(
	canonicalName string,
	desc mcp.PromptDescriptor,
	schema *skills.InputSchema,
	opts CompilePromptOptions,
) MCPSkillSpec {
	usage := strings.TrimSpace(desc.Description)
	if instr := strings.TrimSpace(opts.ServerInstructions); instr != "" {
		usage = appendServerInstruction(usage, instr)
	}
	return MCPSkillSpec{
		Name:        canonicalName,
		Description: descriptionForPrompt(desc),
		InputSchema: schema,
		Priority:    promptBasePriority + opts.PriorityBoost,
		Domain:      string(toolruntime.DomainPlanning),
		Keywords:    promptKeywords(desc),
		UsageDoc:    usage,
	}
}

const promptBasePriority = 60

func descriptionForPrompt(desc mcp.PromptDescriptor) string {
	body := strings.TrimSpace(desc.Description)
	if body == "" {
		body = "Remote MCP prompt template."
	}
	return body
}

func promptKeywords(desc mcp.PromptDescriptor) []string {
	keywords := []string{"prompt", "template"}
	for _, token := range strings.FieldsFunc(desc.Name, isTokenSeparator) {
		keywords = append(keywords, strings.ToLower(token))
	}
	return keywords
}

func buildPromptPolicy(canonicalName string, desc mcp.PromptDescriptor) toolruntime.ToolPolicy {
	return toolruntime.NewToolPolicy(
		canonicalName,
		toolruntime.EffectReadOnly,
		toolruntime.DomainPlanning,
		toolruntime.ExecutionModeLocal,
		toolruntime.WithSearchable(true),
	)
}

func buildPromptSkill(
	serverID, canonicalName, promptName string,
	spec MCPSkillSpec,
	executor ToolExecutor,
) *skills.Skill {
	skill := &skills.Skill{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: spec.InputSchema,
		Priority:    spec.Priority,
		Domain:      spec.Domain,
		Keywords:    append([]string(nil), spec.Keywords...),
		UsageDoc:    spec.UsageDoc,
	}
	skill.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		args, err := decodePromptArgs(input)
		if err != nil {
			return nil, err
		}
		return executor.GetPrompt(ctx, serverID, promptName, args)
	}
	return skill
}

func decodePromptArgs(input json.RawMessage) (map[string]string, error) {
	if len(input) == 0 {
		return map[string]string{}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(input, &raw); err != nil {
		return nil, fmt.Errorf("mcp compile: prompt args: %w", err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = stringifyPromptArg(v)
	}
	return out, nil
}

func stringifyPromptArg(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case nil:
		return ""
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(encoded)
}
