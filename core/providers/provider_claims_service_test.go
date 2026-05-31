package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

type panicProviderGatewayAdapter struct{}

func (panicProviderGatewayAdapter) Name() string { return "panic-provider" }

func (panicProviderGatewayAdapter) SupportedModels() []ModelInfo { return nil }

func (panicProviderGatewayAdapter) Complete(context.Context, *CompletionRequest) (*CompletionResponse, error) {
	panic("provider exploded")
}

func (panicProviderGatewayAdapter) Stream(context.Context, *CompletionRequest) (<-chan *StreamChunk, error) {
	panic("provider exploded")
}

func (panicProviderGatewayAdapter) CountTokens([]Message) (int, error) {
	panic("provider exploded")
}

func (panicProviderGatewayAdapter) MaxContextTokens(string) int { return 0 }

func (panicProviderGatewayAdapter) HealthCheck(context.Context) error { return nil }

func TestProviderGatewayExecutionBackendRecoversProviderPanic(t *testing.T) {
	backend := newProviderGatewayExecutionBackend(panicProviderGatewayAdapter{}, &Request{
		Model:    "panic-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolComplete},
	})
	if err == nil {
		t.Fatal("expected provider panic to return an error")
	}
	if !strings.Contains(err.Error(), "provider gateway backend panic") {
		t.Fatalf("error = %q, want provider gateway backend panic", err.Error())
	}
	if !strings.Contains(data.Error, "provider gateway backend panic") {
		t.Fatalf("data.Error = %q, want provider gateway backend panic", data.Error)
	}
	saved, resp := backend.Result()
	if resp != nil {
		t.Fatalf("panic result response = %#v, want nil", resp)
	}
	if saved.Error != data.Error {
		t.Fatalf("saved error = %q, want %q", saved.Error, data.Error)
	}
}
