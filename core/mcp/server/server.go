// Package server exposes Sylk skills, resources, and prompts as an MCP
// server. The server reuses the same goroutine scope and Guardian gate the
// runtime uses, so external MCP clients cannot bypass any Sylk governance.
//
// The wire is delivered by any transport.Transport: stdio (server mode),
// inprocess (used by Sylk itself), or HTTP (for future remote scenarios).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/transport"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// SkillSource exposes the skills the server should publish. Provided by the
// fabric — typically a wrapper around toolruntime that filters to skills
// that are allowed for export.
type SkillSource interface {
	List() []*skills.Skill
	Lookup(name string) *skills.Skill
}

// ResourceSource exposes Sylk session resources for export. Empty means no
// resources are exposed.
type ResourceSource interface {
	List() []ExportedResource
	Read(uri string) (ExportedResource, error)
}

// PromptSource exposes Sylk-native workflow prompts.
type PromptSource interface {
	List() []ExportedPrompt
	Get(name string, args map[string]string) ([]mcp.PromptMessage, string, error)
}

// ExportedResource is a Sylk-side resource projected into MCP shape.
type ExportedResource struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
	Text        string
	Blob        []byte
}

// ExportedPrompt is a Sylk-side prompt projected into MCP shape.
type ExportedPrompt struct {
	Name        string
	Description string
	Arguments   []mcp.PromptArgumentDef
}

// ToolGate is the runtime hook the server calls when an external client
// invokes a tool. The fabric implements it to route into the same execute
// path the local agents use, including Guardian.
type ToolGate interface {
	Execute(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error)
}

// Config configures a Server.
type Config struct {
	ServerInfo  mcp.Implementation
	Capabilities mcp.ServerCapabilities
	Transport   transport.Transport
	Scope       *concurrency.GoroutineScope
	Skills      SkillSource
	Resources   ResourceSource
	Prompts     PromptSource
	ToolGate    ToolGate
	Instructions string
}

// Server is the dispatch loop for an MCP server.
type Server struct {
	cfg Config

	startMu sync.Mutex
	started bool

	closeMu sync.Mutex
	closed  bool
}

// New constructs a Server.
func New(cfg Config) (*Server, error) {
	if cfg.Transport == nil {
		return nil, errors.New("mcp server: transport is required")
	}
	if cfg.Scope == nil {
		return nil, errors.New("mcp server: scope is required")
	}
	if cfg.Skills == nil {
		return nil, errors.New("mcp server: skill source is required")
	}
	if cfg.ToolGate == nil {
		return nil, errors.New("mcp server: tool gate is required")
	}
	if cfg.Capabilities.Tools == nil {
		cfg.Capabilities.Tools = &mcp.ToolsCapability{ListChanged: true}
	}
	return &Server{cfg: cfg}, nil
}

// Start brings up the transport and dispatches inbound messages on the
// owned scope.
func (s *Server) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.started {
		return errors.New("mcp server: already started")
	}
	if err := s.cfg.Transport.Start(ctx); err != nil {
		return fmt.Errorf("mcp server: transport start: %w", err)
	}
	if err := s.cfg.Scope.Go("mcp.server.dispatcher", 0, s.runDispatcher); err != nil {
		return fmt.Errorf("mcp server: schedule dispatcher: %w", err)
	}
	s.started = true
	return nil
}

// Close tears the server down.
func (s *Server) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.cfg.Transport.Close()
}

func (s *Server) runDispatcher(ctx context.Context) error {
	for {
		msg, err := s.cfg.Transport.Receive(ctx)
		if err != nil {
			return nil
		}
		s.handleMessage(ctx, msg)
	}
}

func (s *Server) handleMessage(ctx context.Context, msg *mcp.Message) {
	if msg.Method == "" {
		return
	}
	if mcp.IsNotification(msg.Method) {
		s.handleNotification(ctx, msg)
		return
	}
	s.handleRequest(ctx, msg)
}

func (s *Server) handleNotification(_ context.Context, _ *mcp.Message) {
	// Notifications from clients (initialized, cancelled). No state to
	// update beyond what the wire carries — they're observational here.
}

func (s *Server) handleRequest(ctx context.Context, msg *mcp.Message) {
	if msg.ID == nil {
		return
	}
	id := *msg.ID
	resp := s.dispatchRequest(ctx, id, msg)
	if resp == nil {
		return
	}
	_ = s.cfg.Transport.Send(ctx, resp)
}

func (s *Server) dispatchRequest(ctx context.Context, id mcp.RequestID, msg *mcp.Message) *mcp.Message {
	handler := s.lookupHandler(msg.Method)
	if handler == nil {
		resp, _ := mcp.NewErrorResponse(id, mcp.ErrCodeMethodNotFound, "method not found: "+msg.Method, nil)
		return resp
	}
	result, err := handler(ctx, msg.Params)
	if err != nil {
		resp, _ := mcp.NewErrorResponse(id, errorCodeFor(err), err.Error(), nil)
		return resp
	}
	resp, _ := mcp.NewResponse(id, result)
	return resp
}

func errorCodeFor(err error) int {
	var rpcErr *mcp.RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return mcp.ErrCodeInternalError
}

type requestHandler func(ctx context.Context, params json.RawMessage) (any, error)

func (s *Server) lookupHandler(method string) requestHandler {
	switch method {
	case mcp.MethodInitialize:
		return s.handleInitialize
	case mcp.MethodToolsList:
		return s.handleToolsList
	case mcp.MethodToolsCall:
		return s.handleToolsCall
	case mcp.MethodResourcesList:
		return s.handleResourcesList
	case mcp.MethodResourcesRead:
		return s.handleResourcesRead
	case mcp.MethodPromptsList:
		return s.handlePromptsList
	case mcp.MethodPromptsGet:
		return s.handlePromptsGet
	case mcp.MethodPing:
		return s.handlePing
	}
	return nil
}

func (s *Server) handleInitialize(_ context.Context, _ json.RawMessage) (any, error) {
	caps := s.cfg.Capabilities
	if caps.Tools == nil {
		caps.Tools = &mcp.ToolsCapability{}
	}
	if s.cfg.Resources != nil && caps.Resources == nil {
		caps.Resources = &mcp.ResourcesCapability{}
	}
	if s.cfg.Prompts != nil && caps.Prompts == nil {
		caps.Prompts = &mcp.PromptsCapability{}
	}
	return mcp.InitializeResult{
		ProtocolVersion: mcp.ProtocolVersionLatest,
		Capabilities:    caps,
		ServerInfo:      s.cfg.ServerInfo,
		Instructions:    s.cfg.Instructions,
	}, nil
}

func (s *Server) handleToolsList(_ context.Context, _ json.RawMessage) (any, error) {
	all := s.cfg.Skills.List()
	out := make([]mcp.ToolDescriptor, 0, len(all))
	for _, skill := range all {
		out = append(out, skillToDescriptor(skill))
	}
	return mcp.ListToolsResult{Tools: out}, nil
}

func skillToDescriptor(skill *skills.Skill) mcp.ToolDescriptor {
	schema := skill.InputSchema
	if schema == nil {
		schema = &skills.InputSchema{Type: "object", Properties: map[string]*skills.Property{}}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		raw = []byte(`{"type":"object","properties":{}}`)
	}
	hints := skillHints(skill)
	return mcp.ToolDescriptor{
		Name:        skill.Name,
		Description: skill.Description,
		InputSchema: raw,
		Annotations: hints,
	}
}

// skillHints derives the MCP tool annotations from Sylk policy metadata.
// Only set when we can speak with confidence — the spec treats annotations
// as untrusted hints, and we follow the same convention.
func skillHints(skill *skills.Skill) *mcp.ToolHints {
	hints := &mcp.ToolHints{Title: skill.Name}
	return hints
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, error) {
	var p mcp.CallToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}
	result, err := s.cfg.ToolGate.Execute(ctx, p.Name, p.Arguments)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeMCPGuardianDenied, Message: err.Error()}
	}
	return result, nil
}

func (s *Server) handleResourcesList(_ context.Context, _ json.RawMessage) (any, error) {
	if s.cfg.Resources == nil {
		return mcp.ListResourcesResult{}, nil
	}
	all := s.cfg.Resources.List()
	out := make([]mcp.ResourceDescriptor, 0, len(all))
	for _, r := range all {
		out = append(out, mcp.ResourceDescriptor{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MIMEType:    r.MIMEType,
		})
	}
	return mcp.ListResourcesResult{Resources: out}, nil
}

func (s *Server) handleResourcesRead(_ context.Context, params json.RawMessage) (any, error) {
	if s.cfg.Resources == nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeMCPCapabilityNotFound, Message: "resources not exposed"}
	}
	var p mcp.ReadResourceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}
	resource, err := s.cfg.Resources.Read(p.URI)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeMCPCapabilityNotFound, Message: err.Error()}
	}
	content := mcp.ResourceContent{URI: resource.URI, MIMEType: resource.MIMEType, Text: resource.Text}
	return mcp.ReadResourceResult{Contents: []mcp.ResourceContent{content}}, nil
}

func (s *Server) handlePromptsList(_ context.Context, _ json.RawMessage) (any, error) {
	if s.cfg.Prompts == nil {
		return mcp.ListPromptsResult{}, nil
	}
	all := s.cfg.Prompts.List()
	out := make([]mcp.PromptDescriptor, 0, len(all))
	for _, p := range all {
		out = append(out, mcp.PromptDescriptor{
			Name:        p.Name,
			Description: p.Description,
			Arguments:   p.Arguments,
		})
	}
	return mcp.ListPromptsResult{Prompts: out}, nil
}

func (s *Server) handlePromptsGet(_ context.Context, params json.RawMessage) (any, error) {
	if s.cfg.Prompts == nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeMCPCapabilityNotFound, Message: "prompts not exposed"}
	}
	var p mcp.GetPromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}
	messages, description, err := s.cfg.Prompts.Get(p.Name, p.Arguments)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeMCPCapabilityNotFound, Message: err.Error()}
	}
	return mcp.GetPromptResult{Description: description, Messages: messages}, nil
}

func (s *Server) handlePing(_ context.Context, _ json.RawMessage) (any, error) {
	return struct{}{}, nil
}

// AsToolPolicy returns the policy class a Sylk-exported skill should publish
// in tool annotations. Kept here because both descriptor materialization and
// any future negotiation logic want the same answer.
func AsToolPolicy(skill *skills.Skill) toolruntime.EffectKind {
	if skill == nil {
		return toolruntime.EffectMutating
	}
	return toolruntime.EffectReadOnly
}
