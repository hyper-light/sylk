package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/mcp"
)

// CapabilitySnapshot is an immutable view of one server's tools/resources/
// prompts at a single point in time. The Fingerprint is a stable hash over
// the descriptors so the catalog merge can detect drift without descending
// into every field.
type CapabilitySnapshot struct {
	ServerID    string
	Tools       []mcp.ToolDescriptor
	Resources   []mcp.ResourceDescriptor
	Prompts     []mcp.PromptDescriptor
	Fingerprint string
}

// ListTools paginates through tools/list and returns the full inventory.
func (s *Session) ListTools(ctx context.Context) ([]mcp.ToolDescriptor, error) {
	caps := s.Capabilities()
	if caps.Tools == nil {
		return nil, nil
	}
	var out []mcp.ToolDescriptor
	cursor := ""
	for {
		params := paginatedParams(cursor)
		var result mcp.ListToolsResult
		if err := s.Call(ctx, mcp.MethodToolsList, params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// ListResources paginates through resources/list.
func (s *Session) ListResources(ctx context.Context) ([]mcp.ResourceDescriptor, error) {
	caps := s.Capabilities()
	if caps.Resources == nil {
		return nil, nil
	}
	var out []mcp.ResourceDescriptor
	cursor := ""
	for {
		params := paginatedParams(cursor)
		var result mcp.ListResourcesResult
		if err := s.Call(ctx, mcp.MethodResourcesList, params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Resources...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// ListPrompts paginates through prompts/list.
func (s *Session) ListPrompts(ctx context.Context) ([]mcp.PromptDescriptor, error) {
	caps := s.Capabilities()
	if caps.Prompts == nil {
		return nil, nil
	}
	var out []mcp.PromptDescriptor
	cursor := ""
	for {
		params := paginatedParams(cursor)
		var result mcp.ListPromptsResult
		if err := s.Call(ctx, mcp.MethodPromptsList, params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Prompts...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// SyncAll lists all three surfaces and returns a fingerprinted snapshot.
func (s *Session) SyncAll(ctx context.Context) (*CapabilitySnapshot, error) {
	tools, err := s.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := s.ListResources(ctx)
	if err != nil {
		return nil, err
	}
	prompts, err := s.ListPrompts(ctx)
	if err != nil {
		return nil, err
	}
	snap := &CapabilitySnapshot{
		ServerID:  s.ServerID(),
		Tools:     tools,
		Resources: resources,
		Prompts:   prompts,
	}
	snap.Fingerprint = fingerprintSnapshot(snap)
	return snap, nil
}

// CallTool issues tools/call and returns the structured result.
func (s *Session) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	params := mcp.CallToolParams{Name: name, Arguments: arguments}
	var result mcp.CallToolResult
	if err := s.Call(ctx, mcp.MethodToolsCall, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReadResource issues resources/read.
func (s *Session) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	params := mcp.ReadResourceParams{URI: uri}
	var result mcp.ReadResourceResult
	if err := s.Call(ctx, mcp.MethodResourcesRead, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPrompt issues prompts/get.
func (s *Session) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	params := mcp.GetPromptParams{Name: name, Arguments: args}
	var result mcp.GetPromptResult
	if err := s.Call(ctx, mcp.MethodPromptsGet, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func paginatedParams(cursor string) any {
	if cursor == "" {
		return struct{}{}
	}
	return map[string]string{"cursor": cursor}
}

// fingerprintSnapshot derives a stable hash over the snapshot's structural
// content. Names are sorted before hashing so peer-side ordering changes do
// not produce spurious invalidations.
func fingerprintSnapshot(snap *CapabilitySnapshot) string {
	if snap == nil {
		return ""
	}
	type fingerprintBody struct {
		Server    string                 `json:"server"`
		Tools     []mcp.ToolDescriptor   `json:"tools"`
		Resources []mcp.ResourceDescriptor `json:"resources"`
		Prompts   []mcp.PromptDescriptor  `json:"prompts"`
	}
	body := fingerprintBody{
		Server:    snap.ServerID,
		Tools:     append([]mcp.ToolDescriptor(nil), snap.Tools...),
		Resources: append([]mcp.ResourceDescriptor(nil), snap.Resources...),
		Prompts:   append([]mcp.PromptDescriptor(nil), snap.Prompts...),
	}
	sortToolDescriptors(body.Tools)
	sortResourceDescriptors(body.Resources)
	sortPromptDescriptors(body.Prompts)
	payload, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sortToolDescriptors(s []mcp.ToolDescriptor) {
	sort.Slice(s, func(i, j int) bool { return strings.Compare(s[i].Name, s[j].Name) < 0 })
}

func sortResourceDescriptors(s []mcp.ResourceDescriptor) {
	sort.Slice(s, func(i, j int) bool { return strings.Compare(s[i].URI, s[j].URI) < 0 })
}

func sortPromptDescriptors(s []mcp.PromptDescriptor) {
	sort.Slice(s, func(i, j int) bool { return strings.Compare(s[i].Name, s[j].Name) < 0 })
}
