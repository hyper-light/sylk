package providers_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	providermocks "github.com/adalundhe/sylk/core/providers/mocks"
	"github.com/stretchr/testify/mock"
)

func TestClaimsGatewayBackendCompleteWithMockeryProvider(t *testing.T) {
	provider := providermocks.NewMockStreamProvider(t)
	provider.EXPECT().Complete(mock.Anything, mock.MatchedBy(func(req *providers.Request) bool {
		return req.Model == "mock-model" && len(req.Messages) == 1 && req.Messages[0].Content == "hello"
	})).Return(&providers.Response{
		Content:    "mock response",
		Model:      "mock-model",
		StopReason: providers.StopReasonEndTurn,
		Usage:      providers.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		ProviderMetadata: map[string]any{
			"request_id": "req-mock",
		},
	}, nil).Once()

	backend := providers.NewClaimsGatewayBackend(providers.ClaimsGatewayBackendConfig{Provider: provider, ResponseSummaryLimit: len("mock response"), Clock: fixedProviderClock})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Call: claims.ExpectedToolCall{Tool: claims.ProviderGatewayToolComplete, Arguments: map[string]any{
			"model":    "mock-model",
			"identity": "guide",
			"prompt":   "hello",
		}},
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolComplete, Model: "mock-model", Identity: "guide"},
	})
	if err != nil {
		t.Fatalf("HandleProviderGatewayCall: %v", err)
	}
	if data.ResponseSummary != "mock response" || data.ProviderRequestID != "req-mock" || data.TotalTokens != 5 || data.Status != "" {
		t.Fatalf("provider data = %+v, want response summary, request id, and usage", data)
	}
}

func TestClaimsGatewayBackendStreamingBoundsPartialsWithMockeryProvider(t *testing.T) {
	chunks := make(chan *providers.StreamChunk, 3)
	chunks <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "alpha", Timestamp: fixedProviderClock()}
	chunks <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "beta", Timestamp: fixedProviderClock()}
	chunks <- &providers.StreamChunk{Type: providers.ChunkTypeEnd, StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{TotalTokens: 2}, Timestamp: fixedProviderClock()}
	close(chunks)

	provider := providermocks.NewMockStreamProvider(t)
	provider.EXPECT().Stream(mock.Anything, mock.MatchedBy(func(req *providers.Request) bool {
		return req.Model == "stream-model"
	})).Return((<-chan *providers.StreamChunk)(chunks), nil).Once()

	backend := providers.NewClaimsGatewayBackend(providers.ClaimsGatewayBackendConfig{Provider: provider, ResponseSummaryLimit: len("alphabet"), PartialSummaryLimit: 1, Clock: fixedProviderClock})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Call: claims.ExpectedToolCall{Tool: claims.ProviderGatewayToolCompleteStreaming, Arguments: map[string]any{
			"model":    "stream-model",
			"identity": "guide",
			"prompt":   "hello",
		}},
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolCompleteStreaming, Model: "stream-model", Identity: "guide"},
	})
	if err != nil {
		t.Fatalf("HandleProviderGatewayCall: %v", err)
	}
	if data.ResponseSummary != "alphabet" || len(data.PartialSummaries) != 1 || data.PartialSummaries[0] != "alpha" || data.TotalTokens != 2 {
		t.Fatalf("stream data = %+v, want bounded partials and summary", data)
	}
}

func TestClaimsGatewayBackendCountTokensWithMockeryProvider(t *testing.T) {
	messages := []providers.Message{{Role: providers.RoleUser, Content: "count me"}}
	provider := providermocks.NewMockStreamProvider(t)
	provider.EXPECT().CountTokens(mock.MatchedBy(func(got []providers.Message) bool {
		return len(got) == 1 && got[0].Content == messages[0].Content
	})).Return(7, nil).Once()

	backend := providers.NewClaimsGatewayBackend(providers.ClaimsGatewayBackendConfig{Provider: provider, Clock: fixedProviderClock})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Call: claims.ExpectedToolCall{Tool: claims.ProviderGatewayToolCountTokens, Arguments: map[string]any{
			"model":    "token-model",
			"identity": "guide",
			"messages": messages,
		}},
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolCountTokens, Model: "token-model", Identity: "guide"},
	})
	if err != nil {
		t.Fatalf("HandleProviderGatewayCall count tokens: %v", err)
	}
	if data.InputTokens != 7 || data.TotalTokens != 7 || data.Error != "" {
		t.Fatalf("count token data = %+v, want usage-only success", data)
	}
}

func TestClaimsGatewayBackendEmbeddingDoesNotFallThroughToComplete(t *testing.T) {
	provider := providermocks.NewMockStreamProvider(t)
	backend := providers.NewClaimsGatewayBackend(providers.ClaimsGatewayBackendConfig{Provider: provider, Clock: fixedProviderClock})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Call: claims.ExpectedToolCall{Tool: claims.ProviderGatewayToolEmbedding, Arguments: map[string]any{
			"model":    "embed-model",
			"identity": "guide",
			"input":    "embed me",
		}},
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolEmbedding, Model: "embed-model", Identity: "guide"},
	})
	if err != nil {
		t.Fatalf("HandleProviderGatewayCall embedding: %v", err)
	}
	if data.Error != "provider embedding backend not configured" || data.ResponseSummary != "" {
		t.Fatalf("embedding data = %+v, want explicit embedding backend failure", data)
	}
}

func fixedProviderClock() time.Time {
	return time.Unix(1, 0).UTC()
}
