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

	cfg.RouterConfig.Model = "gemini-3.1-pro-preview"
	cfg.RouterConfig.ClassificationTimeout = 60 * 1000000000 // 60s timeout for thinking model
	
	g, _ := guide.NewWithGeminiClient(client, cfg)

	prompts := []string{
		"Hey, how do I use the DSL syntax to talk to the archivalist?",
		"I'm getting a nil pointer panic in the auth middleware. Have we seen this before?",
		"@engineer please fix the bug in main.go line 42",
		"Can you read up on the latest OAuth 2.1 specifications and let me know if our implementation is compliant?",
	}

	for i, prompt := range prompts {
		fmt.Printf("=== Test %d ===\nPrompt: %q\n", i+1, prompt)
		req := &guide.RouteRequest{
			Input:         prompt,
			SourceAgentID: "user",
		}
		fwd, err := g.Route(ctx, req)
		if err != nil {
			fmt.Printf("Routing failed: %v\n\n", err)
			continue
		}
		out, _ := json.MarshalIndent(fwd, "", "  ")
		fmt.Printf("Decision:\n%s\n\n", string(out))
	}
}
