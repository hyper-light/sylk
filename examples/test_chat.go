//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/adalundhe/sylk/agents/guide"
	"google.golang.org/genai"
)

type DummyBus struct{}

func (b *DummyBus) Publish(topic string, msg *guide.Message) error { return nil }
func (b *DummyBus) Subscribe(topic string, handler guide.MessageHandler) (guide.Subscription, error) { return nil, nil }
func (b *DummyBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) { return nil, nil }
func (b *DummyBus) Close() error { return nil }

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	ctx := context.Background()
	client, _ := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})

	cfg := guide.Config{
		RouterConfig: guide.DefaultRouterConfig(),
		Bus:          &DummyBus{},
		SessionID:    "test-session-123",
	}

	g, _ := guide.NewWithProvider(client, "gemini-3.1-pro-preview", cfg)

	prompt := "Hello! Can you help me out?"
	req := &guide.RouteRequest{
		Input:         prompt,
		SourceAgentID: "user",
	}

	fwd, err := g.Route(ctx, req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	out, _ := json.MarshalIndent(fwd, "", "  ")
	fmt.Printf("Decision:\n%s\n", string(out))
}
