package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/transport"
	"github.com/adalundhe/sylk/core/skills"
)

type stubSkillSource struct{ skills []*skills.Skill }

func (s *stubSkillSource) List() []*skills.Skill { return s.skills }
func (s *stubSkillSource) Lookup(name string) *skills.Skill {
	for _, sk := range s.skills {
		if sk.Name == name {
			return sk
		}
	}
	return nil
}

type stubGate struct {
	mu     sync.Mutex
	called string
}

func (s *stubGate) Execute(_ context.Context, name string, args json.RawMessage) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	s.called = name
	s.mu.Unlock()
	if name == "boom" {
		return nil, errors.New("denied")
	}
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: "ok:" + string(args)}}}, nil
}

func TestServer_RoundtripsToolCall(t *testing.T) {
	clientTx, serverTx := transport.NewInProcessPair()
	scope := concurrency.NewGoroutineScope(context.Background(), "server-test", nil)
	defer scope.Shutdown(time.Second, 2*time.Second)

	src := &stubSkillSource{skills: []*skills.Skill{
		skills.NewSkill("hello").
			Description("Say hello.").
			StringParam("name", "Name", true).
			Handler(func(_ context.Context, _ json.RawMessage) (any, error) { return "hi", nil }).
			Build(),
	}}
	gate := &stubGate{}
	srv, err := New(Config{
		ServerInfo: mcp.Implementation{Name: "sylk-test", Version: "0.1"},
		Transport:  serverTx,
		Scope:      scope,
		Skills:     src,
		ToolGate:   gate,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	if err := clientTx.Start(context.Background()); err != nil {
		t.Fatalf("client start: %v", err)
	}
	// Initialize
	req, _ := mcp.NewRequest(mcp.NewIntRequestID(1), mcp.MethodInitialize, mcp.InitializeParams{ProtocolVersion: mcp.ProtocolVersionLatest})
	if err := clientTx.Send(context.Background(), req); err != nil {
		t.Fatalf("send init: %v", err)
	}
	resp, err := clientTx.Receive(context.Background())
	if err != nil {
		t.Fatalf("recv init: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("init err: %v", resp.Error)
	}

	// tools/list
	req2, _ := mcp.NewRequest(mcp.NewIntRequestID(2), mcp.MethodToolsList, struct{}{})
	if err := clientTx.Send(context.Background(), req2); err != nil {
		t.Fatalf("send list: %v", err)
	}
	resp2, err := clientTx.Receive(context.Background())
	if err != nil {
		t.Fatalf("recv list: %v", err)
	}
	var listResult mcp.ListToolsResult
	if err := json.Unmarshal(resp2.Result, &listResult); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResult.Tools) != 1 || listResult.Tools[0].Name != "hello" {
		t.Fatalf("unexpected tools: %+v", listResult.Tools)
	}

	// tools/call
	req3, _ := mcp.NewRequest(mcp.NewIntRequestID(3), mcp.MethodToolsCall, mcp.CallToolParams{
		Name: "hello", Arguments: json.RawMessage(`{"name":"sylk"}`),
	})
	if err := clientTx.Send(context.Background(), req3); err != nil {
		t.Fatalf("send call: %v", err)
	}
	resp3, err := clientTx.Receive(context.Background())
	if err != nil {
		t.Fatalf("recv call: %v", err)
	}
	if resp3.Error != nil {
		t.Fatalf("call err: %v", resp3.Error)
	}
	var callResult mcp.CallToolResult
	if err := json.Unmarshal(resp3.Result, &callResult); err != nil {
		t.Fatalf("decode call: %v", err)
	}
	if len(callResult.Content) == 0 || callResult.Content[0].Text == "" {
		t.Fatalf("empty call result: %+v", callResult)
	}
	if gate.called != "hello" {
		t.Fatalf("gate not called: %q", gate.called)
	}
}
