package architect

import (
	"os"
	"strings"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/storage"
)

func resolveArchitectAnthropicAPIKey(configured string) string {
	if key := strings.TrimSpace(configured); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key
	}
	if key := resolveArchitectSecureAPIKey("anthropic"); key != "" {
		return key
	}
	key, err := llm.ResolveAPIKey("anthropic")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveArchitectSecureAPIKey(provider string) string {
	dirs, err := storage.ResolveDirs()
	if err != nil || dirs == nil {
		return ""
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return ""
	}
	key, err := manager.GetAPIKey(provider)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}
