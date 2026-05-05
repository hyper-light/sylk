package compiler

import (
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// SchemaFromJSON converts an MCP-supplied JSON Schema fragment into Sylk's
// internal *skills.InputSchema representation.
//
// Goals:
//   - preserve nested object/array structure exactly,
//   - lossless required-field propagation,
//   - never panic on malformed input — return an error so the caller can
//     surface it as a compile-time diagnostic and quarantine the tool,
//   - preserve enum values verbatim so the LLM still sees the constraint
//     when calling the tool.
func SchemaFromJSON(raw json.RawMessage) (*skills.InputSchema, error) {
	if len(raw) == 0 {
		return &skills.InputSchema{Type: "object", Properties: map[string]*skills.Property{}}, nil
	}
	var node schemaNode
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("mcp: parse input schema: %w", err)
	}
	if node.Type == "" {
		node.Type = "object"
	}
	properties := convertProperties(node.Properties)
	return &skills.InputSchema{
		Type:       node.Type,
		Properties: properties,
		Required:   append([]string(nil), node.Required...),
	}, nil
}

// schemaNode captures the subset of JSON Schema MCP tool schemas use in
// practice. Anything beyond is preserved by the converter but not enforced
// by Sylk's runtime (it lives in the wire-side validation only).
type schemaNode struct {
	Type        string                `json:"type,omitempty"`
	Description string                `json:"description,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Items       *schemaNode           `json:"items,omitempty"`
	Properties  map[string]schemaNode `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Default     any                   `json:"default,omitempty"`
}

func convertProperties(in map[string]schemaNode) map[string]*skills.Property {
	if len(in) == 0 {
		return map[string]*skills.Property{}
	}
	out := make(map[string]*skills.Property, len(in))
	for name, node := range in {
		out[name] = convertNode(node)
	}
	return out
}

func convertNode(node schemaNode) *skills.Property {
	prop := &skills.Property{
		Type:        node.Type,
		Description: node.Description,
		Default:     node.Default,
	}
	if len(node.Enum) > 0 {
		prop.Enum = append([]string(nil), node.Enum...)
	}
	if node.Items != nil {
		prop.Items = convertNode(*node.Items)
	}
	if len(node.Properties) > 0 {
		prop.Properties = convertProperties(node.Properties)
	}
	if len(node.Required) > 0 {
		prop.Required = append([]string(nil), node.Required...)
	}
	return prop
}
