package providers

import "testing"

func TestRequestUsesPromptCacheHonorsRequestOverride(t *testing.T) {
	provider := &AnthropicProvider{
		config: AnthropicConfig{
			EnableCaching: true,
		},
	}

	if !provider.requestUsesPromptCache(nil) {
		t.Fatal("expected provider default caching to be enabled")
	}

	if provider.requestUsesPromptCache(&Request{UsePromptCache: boolPtr(false)}) {
		t.Fatal("expected request override to disable prompt cache")
	}

	provider.config.EnableCaching = false
	if !provider.requestUsesPromptCache(&Request{UsePromptCache: boolPtr(true)}) {
		t.Fatal("expected request override to enable prompt cache")
	}
}
