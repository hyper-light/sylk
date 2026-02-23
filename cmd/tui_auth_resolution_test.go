package cmd

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestDefaultGuideGoogleConfig(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "AIzaValidLookingGoogleKeyValue1234567890")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_OAUTH_ACCESS_TOKEN", "")
	t.Setenv("GOOGLE_OAUTH_REFRESH_TOKEN", "")

	cfg := defaultGuideGoogleConfig()
	if cfg.Model != "gemini-3.1-pro-preview" {
		t.Fatalf("model = %q, want %q", cfg.Model, "gemini-3.1-pro-preview")
	}
	provider, err := providers.NewGoogleProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewGoogleProvider: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider")
	}
}
