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

// A dummy event bus for the test so the Guide can initialize
type DummyBus struct{}

func (b *DummyBus) Publish(topic string, msg *guide.Message) error { return nil }
func (b *DummyBus) Subscribe(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *DummyBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
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
	
	// Increase timeout for the API
	cfg.RouterConfig.ClassificationTimeout = 60 * 1000000000 // 60 seconds
	
	cfg.RouterConfig.Model = "gemini-3.1-pro-preview"

	g, _ := guide.NewWithProvider(client, "gemini-3.1-pro-preview", cfg)

	// Avoid the fast-path keyword detection
	prompt := "My authentication system seems to be running out of memory slowly. Could you investigate and deploy a patch?"
	fmt.Printf("Testing Prompt: %q\n\n", prompt)

	req := &guide.RouteRequest{
		Input:         prompt,
		SourceAgentID: "user",
	}

	fwd, err := g.Route(ctx, req)
	if err != nil {
		fmt.Printf("Routing failed: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(fwd, "", "  ")
	fmt.Printf("Routing Decision:\n%s\n", string(out))
}