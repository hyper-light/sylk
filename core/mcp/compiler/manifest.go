// Package compiler turns wire-level MCP descriptors into Sylk-native
// skill manifests, ToolPolicy entries, and runtime wrappers.
//
// The pipeline is intentionally one-way per session sync:
//
//	raw MCP descriptor
//	    -> normalized MCPSkillSpec (skill-shape lossless form)
//	    -> attach MCPBindingSpec (server, kind, remote name/uri)
//	    -> attach toolruntime.ToolPolicy (effect, domain, execution mode)
//	    -> register with toolruntime via wrapper handler
//
// MCPCompiledSkillSpec is the canonical interchange format. It mirrors
// core/skills.Skill exactly — never invent a second tool schema language.
package compiler

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// MCPSkillSpec is a Sylk-skill-shaped representation of an MCP capability.
type MCPSkillSpec struct {
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	InputSchema     *skills.InputSchema   `json:"input_schema,omitempty"`
	ProviderTool    *skills.ProviderTool  `json:"provider_tool,omitempty"`
	Priority        int                   `json:"priority,omitempty"`
	Domain          string                `json:"domain,omitempty"`
	Keywords        []string              `json:"keywords,omitempty"`
	EstimatedTokens int                   `json:"estimated_tokens,omitempty"`
	UsageDoc        string                `json:"usage_doc,omitempty"`
	BestPractices   []string              `json:"best_practices,omitempty"`
	Examples        []string              `json:"examples,omitempty"`
	Requirements    []string              `json:"requirements,omitempty"`
	Satisfies       []string              `json:"satisfies,omitempty"`
	Avoids          []string              `json:"avoids,omitempty"`
}

// MCPBindingSpec carries the wire-level identity that ties a compiled skill
// back to an MCP server. The runtime executor reads this to know who to call
// when the skill's handler fires.
type MCPBindingSpec struct {
	ServerID        string `json:"server_id"`
	CapabilityKind  string `json:"capability_kind"` // tool, resource, prompt
	RemoteName      string `json:"remote_name,omitempty"`
	RemoteURI       string `json:"remote_uri,omitempty"`
	PromptName      string `json:"prompt_name,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	InstructionsRef string `json:"instructions_ref,omitempty"`
}

// MCPCompiledSkillSpec is the canonical, persistable form of an MCP-backed
// capability after compilation: skill metadata, binding, and ToolPolicy.
type MCPCompiledSkillSpec struct {
	Skill   MCPSkillSpec           `json:"skill"`
	Binding MCPBindingSpec         `json:"binding"`
	Policy  toolruntime.ToolPolicy `json:"policy"`
}

// CompiledTool pairs a runtime-ready Skill with the binding+policy needed
// to register it. The Skill carries a Handler closure that delegates
// execution to the MCP runtime.
type CompiledTool struct {
	Spec    MCPCompiledSkillSpec
	Skill   *skills.Skill
}

// CompiledResource carries a resource descriptor and its compiled binding.
// Resources do not become Skills directly — they become indexed knowledge
// assets (handled by core/mcp/ingest).
type CompiledResource struct {
	Binding     MCPBindingSpec
	Description string
	MIMEType    string
	Title       string
	URI         string
}

// CompiledPrompt mirrors a tool compilation but flagged as kind=prompt.
// Sylk surfaces prompts as explicitly invocable skills (the skill handler
// invokes prompts/get and returns the rendered messages).
type CompiledPrompt struct {
	Spec  MCPCompiledSkillSpec
	Skill *skills.Skill
}
