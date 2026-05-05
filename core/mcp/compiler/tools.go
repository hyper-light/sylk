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

// ToolExecutor is the abstraction the compiler uses to delegate a Skill's
// handler to the MCP runtime. The runtime package implements it. Keeping
// this as a single-method interface lets the compiler stay free of a
// runtime import (which would otherwise create a cycle).
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, serverID, canonicalName string, arguments json.RawMessage) (any, error)
	ReadResource(ctx context.Context, serverID, uri string) (any, error)
	GetPrompt(ctx context.Context, serverID, promptName string, args map[string]string) (any, error)
}

// CompileToolOptions tunes optional aspects of compilation.
type CompileToolOptions struct {
	// ServerDomains overrides the policy domain for tools that don't
	// declare one in annotations. Falls back to system when absent.
	ServerDomains []string
	// TrustedReadOnly forces the policy effect to read-only when true,
	// regardless of MCP annotations. Used when a server's tools are all
	// read-only by construction (search, knowledge bases, etc).
	TrustedReadOnly bool
	// ServerInstructions optionally augment each compiled skill's
	// usage doc so the LLM sees the server-level guidance.
	ServerInstructions string
	// PriorityBoost lets a project-local catalog raise the priority of
	// every tool from one server (used when the user pinned the server).
	PriorityBoost int
	// Aliases lets the catalog substitute keywords or hints. Empty means
	// the canonical mapping in DeriveDomain is used.
	Aliases map[string]string
}

// CompileTool turns one wire-side ToolDescriptor into an MCPCompiledSkillSpec
// and a runtime-ready *skills.Skill whose handler invokes executor.ExecuteTool.
func CompileTool(
	serverID string,
	desc mcp.ToolDescriptor,
	opts CompileToolOptions,
	executor ToolExecutor,
) (*CompiledTool, error) {
	if executor == nil {
		return nil, fmt.Errorf("mcp compile: executor is required")
	}
	canonicalName := CanonicalToolName(serverID, desc.Name)
	schema, err := SchemaFromJSON(desc.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp compile: %s: %w", canonicalName, err)
	}
	skillSpec := buildToolSkillSpec(canonicalName, desc, schema, opts)
	binding := buildToolBinding(serverID, desc.Name)
	policy := buildToolPolicy(canonicalName, desc, opts)
	skill := buildToolSkill(serverID, canonicalName, skillSpec, executor)
	return &CompiledTool{
		Spec: MCPCompiledSkillSpec{
			Skill:   skillSpec,
			Binding: binding,
			Policy:  policy,
		},
		Skill: skill,
	}, nil
}

func buildToolSkillSpec(
	canonicalName string,
	desc mcp.ToolDescriptor,
	schema *skills.InputSchema,
	opts CompileToolOptions,
) MCPSkillSpec {
	usage := strings.TrimSpace(desc.Description)
	if instr := strings.TrimSpace(opts.ServerInstructions); instr != "" {
		usage = appendServerInstruction(usage, instr)
	}
	priority := basePriorityForTool(desc) + opts.PriorityBoost
	return MCPSkillSpec{
		Name:        canonicalName,
		Description: descriptionForTool(desc),
		InputSchema: schema,
		Priority:    priority,
		Domain:      DeriveDomain(desc, opts),
		Keywords:    DeriveKeywords(desc, opts),
		UsageDoc:    usage,
	}
}

func buildToolBinding(serverID, remoteName string) MCPBindingSpec {
	return MCPBindingSpec{
		ServerID:       serverID,
		CapabilityKind: string(mcp.CapabilityTool),
		RemoteName:     remoteName,
	}
}

func buildToolPolicy(canonicalName string, desc mcp.ToolDescriptor, opts CompileToolOptions) toolruntime.ToolPolicy {
	effect := DeriveEffect(desc, opts)
	execution := DeriveExecution(effect)
	approvalSensitive := effect == toolruntime.EffectMutating
	policyOpts := []toolruntime.PolicyOption{toolruntime.WithSearchable(true)}
	if approvalSensitive {
		policyOpts = append(policyOpts, toolruntime.WithApprovalSensitive())
	}
	policy := toolruntime.NewToolPolicy(
		canonicalName,
		effect,
		toolruntime.EffectDomain(DeriveDomain(desc, opts)),
		execution,
		policyOpts...,
	)
	return policy
}

func buildToolSkill(
	serverID, canonicalName string,
	spec MCPSkillSpec,
	executor ToolExecutor,
) *skills.Skill {
	skill := &skills.Skill{
		Name:            spec.Name,
		Description:     spec.Description,
		InputSchema:     spec.InputSchema,
		Priority:        spec.Priority,
		Domain:          spec.Domain,
		Keywords:        append([]string(nil), spec.Keywords...),
		UsageDoc:        spec.UsageDoc,
		EstimatedTokens: spec.EstimatedTokens,
	}
	skill.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		return executor.ExecuteTool(ctx, serverID, canonicalName, input)
	}
	return skill
}

func descriptionForTool(desc mcp.ToolDescriptor) string {
	body := strings.TrimSpace(desc.Description)
	if body == "" {
		body = "Remote MCP tool."
	}
	return body
}

func appendServerInstruction(base, instr string) string {
	if base == "" {
		return instr
	}
	return base + "\n\nServer guidance:\n" + instr
}

// basePriorityForTool seeds skill priority from descriptor hints. Tools
// declared as read-only and idempotent are given a higher priority because
// they are safer to surface in fast paths; everything else lands at default.
func basePriorityForTool(desc mcp.ToolDescriptor) int {
	const (
		basePriority         = 50
		readOnlyHintBoost    = 10
		idempotentHintBoost  = 5
	)
	priority := basePriority
	if desc.Annotations == nil {
		return priority
	}
	if desc.Annotations.ReadOnlyHint != nil && *desc.Annotations.ReadOnlyHint {
		priority += readOnlyHintBoost
	}
	if desc.Annotations.IdempotentHint != nil && *desc.Annotations.IdempotentHint {
		priority += idempotentHintBoost
	}
	return priority
}
