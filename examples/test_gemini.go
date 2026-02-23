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
	if apiKey == "" {
		fmt.Println("Error: GEMINI_API_KEY environment variable is not set.")
		os.Exit(1)
	}

	ctx := context.Background()
	
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		fmt.Printf("Failed to create Gemini client: %v\n", err)
		os.Exit(1)
	}

	cfg := guide.Config{
		RouterConfig: guide.DefaultRouterConfig(),
		Bus:          &DummyBus{},
		SessionID:    "test-session-123",
	}
	
	// Force the model name for the test. We use gemini-3.1-pro-preview as it's generally available,
	// unless you explicitly want to test the 3.1-pro-preview string.
	cfg.RouterConfig.Model = "gemini-3.1-pro-preview"

	g, err := guide.NewWithGeminiClient(client, cfg)
	if err != nil {
		fmt.Printf("Failed to create Guide: %v\n", err)
		os.Exit(1)
	}

	prompt := "There is a memory leak in the authentication middleware. Please find where it is happening and write a fix for it."
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