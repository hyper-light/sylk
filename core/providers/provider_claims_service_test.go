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

type nilResponseProviderGatewayAdapter struct{}

func (nilResponseProviderGatewayAdapter) Name() string { return "nil-response-provider" }

func (nilResponseProviderGatewayAdapter) SupportedModels() []ModelInfo { return nil }

func (nilResponseProviderGatewayAdapter) Complete(context.Context, *CompletionRequest) (*CompletionResponse, error) {
	return nil, nil
}

func (nilResponseProviderGatewayAdapter) Stream(context.Context, *CompletionRequest) (<-chan *StreamChunk, error) {
	return nil, nil
}

func (nilResponseProviderGatewayAdapter) CountTokens([]Message) (int, error) {
	return 0, nil
}

func (nilResponseProviderGatewayAdapter) MaxContextTokens(string) int { return 0 }

func (nilResponseProviderGatewayAdapter) HealthCheck(context.Context) error { return nil }

func TestProviderGatewayExecutionBackendReturnsNilResponseAsArtifactError(t *testing.T) {
	backend := newProviderGatewayExecutionBackend(nilResponseProviderGatewayAdapter{}, &Request{
		Model:    "nil-response-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolComplete},
	})
	if err != nil {
		t.Fatalf("HandleProviderGatewayCall returned err = %v, want artifact error only", err)
	}
	if !strings.Contains(data.Error, "provider returned nil response") {
		t.Fatalf("data.Error = %q, want provider returned nil response", data.Error)
	}
	saved, resp := backend.Result()
	if resp != nil {
		t.Fatalf("nil response result = %#v, want nil", resp)
	}
	if saved.Error != data.Error {
		t.Fatalf("saved error = %q, want %q", saved.Error, data.Error)
	}
}
