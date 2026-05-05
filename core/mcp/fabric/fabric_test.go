package fabric

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/transport"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// stubServer is a minimal in-memory MCP server peer used by the tests.
// It supports initialize, tools/list, tools/call. Unknown methods return
// MethodNotFound — same as a spec-compliant server. All goroutines run on
// a tracked scope so the leak detector passes.
type stubServer struct {
	clientTx transport.Transport
	tx       transport.Transport
	scope    *concurrency.GoroutineScope
	tools    []mcp.ToolDescriptor
	callImpl func(name string, args json.RawMessage) *mcp.CallToolResult

	mu          sync.Mutex
	calledTools []string
}

func newStubServer(t *testing.T, tools []mcp.ToolDescriptor, call func(string, json.RawMessage) *mcp.CallToolResult) *stubServer {
	t.Helper()
	clientTx, serverTx := transport.NewInProcessPair()
	scope := concurrency.NewGoroutineScope(context.Background(), "stub-server", nil)
	s := &stubServer{clientTx: clientTx, tx: serverTx, scope: scope, tools: tools, callImpl: call}
	t.Cleanup(func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	})
	if err := serverTx.Start(context.Background()); err != nil {
		t.Fatalf("server transport start: %v", err)
	}
	if err := scope.Go("stub-server.dispatcher", 0, s.run); err != nil {
		t.Fatalf("schedule dispatcher: %v", err)
	}
	return s
}

func (s *stubServer) run(ctx context.Context) error {
	for {
		msg, err := s.tx.Receive(ctx)
		if err != nil {
			return nil
		}
		if msg.Method == "" || mcp.IsNotification(msg.Method) {
			continue
		}
		if msg.ID == nil {
			continue
		}
		resp := s.dispatch(*msg.ID, msg)
		_ = s.tx.Send(ctx, resp)
	}
}

func (s *stubServer) dispatch(id mcp.RequestID, msg *mcp.Message) *mcp.Message {
	switch msg.Method {
	case mcp.MethodInitialize:
		resp, _ := mcp.NewResponse(id, mcp.InitializeResult{
			ProtocolVersion: mcp.ProtocolVersionLatest,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.Implementation{Name: "stub", Version: "1.0"},
		})
		return resp
	case mcp.MethodToolsList:
		resp, _ := mcp.NewResponse(id, mcp.ListToolsResult{Tools: s.tools})
		return resp
	case mcp.MethodToolsCall:
		var p mcp.CallToolParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			resp, _ := mcp.NewErrorResponse(id, mcp.ErrCodeInvalidParams, err.Error(), nil)
			return resp
		}
		s.mu.Lock()
		s.calledTools = append(s.calledTools, p.Name)
		s.mu.Unlock()
		result := s.callImpl(p.Name, p.Arguments)
		resp, _ := mcp.NewResponse(id, result)
		return resp
	}
	resp, _ := mcp.NewErrorResponse(id, mcp.ErrCodeMethodNotFound, "method not found: "+msg.Method, nil)
	return resp
}

// stubGuardian is a stub GuardianGate that always approves.
type stubGuardian struct {
	approved int
	denied   int
	approve  bool
}

func (g *stubGuardian) RequestGrant(_ context.Context, req toolruntime.GuardianControlRequest) (*toolruntime.GuardianControlGrant, error) {
	if !g.approve {
		g.denied++
		return &toolruntime.GuardianControlGrant{
			AgentID:           req.AgentID,
			CorrelationID:     req.CorrelationID,
			CapabilityScope:   req.CapabilityScope,
			ToolName:          req.ToolName,
			Approved:          false,
			Reason:            "test gate denied",
			ExpiresAt:         time.Now().Add(time.Minute),
			ArgumentsHash:     req.ArgumentsHash,
			PolicyFingerprint: req.PolicyFingerprint,
		}, nil
	}
	g.approved++
	return &toolruntime.GuardianControlGrant{
		AgentID:           req.AgentID,
		CorrelationID:     req.CorrelationID,
		CapabilityScope:   req.CapabilityScope,
		ToolName:          req.ToolName,
		Approved:          true,
		ExpiresAt:         time.Now().Add(time.Minute),
		ArgumentsHash:     req.ArgumentsHash,
		PolicyFingerprint: req.PolicyFingerprint,
	}, nil
}

func TestFabric_EndToEndReadOnlyToolCall(t *testing.T) {
	readOnly := true
	tools := []mcp.ToolDescriptor{
		{
			Name:        "search",
			Description: "Search for matching documents",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
			Annotations: &mcp.ToolHints{ReadOnlyHint: &readOnly},
		},
	}
	stub := newStubServer(t, tools, func(name string, args json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: "ok:" + string(args)}}}
	})
	registry := skills.NewRegistry()
	mgr := catalog.NewManager()
	mgr.LoadSession(&catalog.File{Servers: []catalog.ServerSpec{
		// in-process transport is not built by the catalog, so we skip the
		// catalog wiring and inject the connection directly via a probe.
	}})
	guardian := &stubGuardian{approve: true}
	scope := concurrency.NewGoroutineScope(context.Background(), "fabric-test", nil)
	defer scope.Shutdown(time.Second, 2*time.Second)
	fabric, err := NewFabric(FabricConfig{
		SessionID:  "sess-1",
		SessionDir: t.TempDir(),
		Registry:   registry,
		Catalog:    mgr,
		Guardian:   guardian,
		Scope:      scope,
		ReloadInterval: time.Hour, // disable noisy reload
	})
	if err != nil {
		t.Fatalf("new fabric: %v", err)
	}
	defer fabric.Close()
	if err := fabric.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Inject the stub connection. The real wire-up happens via the catalog
	// + transport factory; this test bypasses both because in-process
	// transports cannot be expressed in YAML.
	if err := fabric.AttachInProcessSession(context.Background(), "stub", stub.clientTx); err != nil {
		t.Fatalf("attach in-process: %v", err)
	}
	skill := registry.Get("mcp.stub.search")
	if skill == nil {
		t.Fatal("compiled skill not registered")
	}
	if skill.Domain != "discovery" {
		t.Fatalf("expected discovery domain, got %s", skill.Domain)
	}
	out, err := skill.Handler(context.Background(), json.RawMessage(`{"q":"sylk"}`))
	if err != nil {
		t.Fatalf("invoke skill: %v", err)
	}
	result, ok := out.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", out)
	}
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Fatalf("empty result: %+v", result)
	}
	// Read-only tools must NOT have called the guardian.
	if guardian.approved != 0 || guardian.denied != 0 {
		t.Fatalf("read-only tool should not invoke guardian, approved=%d denied=%d", guardian.approved, guardian.denied)
	}
	// Pending should be empty after a successful call.
	if pending := fabric.Pending(); len(pending) != 0 {
		t.Fatalf("expected zero pending, got %d", len(pending))
	}
}

func TestFabric_MutatingToolRequiresGuardian(t *testing.T) {
	dest := false
	readOnly := false
	tools := []mcp.ToolDescriptor{
		{
			Name:        "delete_repo",
			Description: "Delete a repository.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}}}`),
			Annotations: &mcp.ToolHints{ReadOnlyHint: &readOnly, DestructiveHint: &dest},
		},
	}
	stub := newStubServer(t, tools, func(_ string, _ json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: "boom"}}}
	})
	registry := skills.NewRegistry()
	guardian := &stubGuardian{approve: false}
	scope := concurrency.NewGoroutineScope(context.Background(), "fabric-test-mutate", nil)
	defer scope.Shutdown(time.Second, 2*time.Second)
	fabric, err := NewFabric(FabricConfig{
		SessionID:      "sess-2",
		SessionDir:     t.TempDir(),
		Registry:       registry,
		Catalog:        catalog.NewManager(),
		Guardian:       guardian,
		Scope:          scope,
		ReloadInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("new fabric: %v", err)
	}
	defer fabric.Close()
	if err := fabric.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := fabric.AttachInProcessSession(context.Background(), "danger", stub.clientTx); err != nil {
		t.Fatalf("attach: %v", err)
	}
	skill := registry.Get("mcp.danger.delete_repo")
	if skill == nil {
		t.Fatal("compiled skill not registered")
	}
	_, err = skill.Handler(context.Background(), json.RawMessage(`{"repo":"foo"}`))
	if err == nil {
		t.Fatalf("expected guardian denial, got nil error")
	}
	if guardian.denied != 1 {
		t.Fatalf("expected guardian denial, got %d", guardian.denied)
	}
}
