package transport

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/mcp"
)

func TestInProcessPair_Roundtrip(t *testing.T) {
	clientTx, serverTx := NewInProcessPair()
	defer clientTx.Close()
	defer serverTx.Close()
	if err := clientTx.Start(context.Background()); err != nil {
		t.Fatalf("client start: %v", err)
	}
	if err := serverTx.Start(context.Background()); err != nil {
		t.Fatalf("server start: %v", err)
	}
	req, err := mcp.NewRequest(mcp.NewIntRequestID(1), mcp.MethodPing, struct{}{})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if err := clientTx.Send(context.Background(), req); err != nil {
		t.Fatalf("send: %v", err)
	}
	got, err := serverTx.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if got.Method != mcp.MethodPing {
		t.Fatalf("expected ping, got %q", got.Method)
	}
}
