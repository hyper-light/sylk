package shared

import "github.com/adalundhe/sylk/core/providers"

// EstimateContextSize approximates total context tokens from accumulated messages.
// Uses ~4 characters per token plus overhead per message, matching core/llm/context.go.
func EstimateContextSize(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)/4 + 4
	}
	return total
}
