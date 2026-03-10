package credentials

import "strings"

var providerAuthMethodAliases = map[string]map[string]string{
	"google": {
		"apikey":          "api_key",
		"api_key":         "api_key",
		"oauth":           "oauth",
		"service_account": "service_account",
	},
	"anthropic": {
		"apikey":  "api_key",
		"api_key": "api_key",
		"oauth":   "oauth",
	},
	"openai": {
		"apikey":  "api_key",
		"api_key": "api_key",
		"oauth":   "chatgpt",
		"chatgpt": "chatgpt",
	},
}

var providerDefaultAuthMethod = map[string]string{
	"google":    "oauth",
	"anthropic": "api_key",
	"openai":    "api_key",
}

var providerAuthFallbackOrder = map[string][]string{
	"google":    {"oauth", "service_account", "api_key"},
	"anthropic": {"api_key", "oauth"},
	"openai":    {"api_key", "chatgpt"},
}

// CanonicalAuthMethod normalizes user-facing or legacy auth method labels
// into provider-native auth modes.
func CanonicalAuthMethod(provider, method string) string {
	provider = normalizeAuthProvider(provider)
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		return ""
	}
	if aliases, ok := providerAuthMethodAliases[provider]; ok {
		if canonical, ok := aliases[method]; ok {
			return canonical
		}
	}
	return method
}

// DefaultAuthMethod returns the provider's default auth mode when no stored
// preference exists.
func DefaultAuthMethod(provider string) string {
	provider = normalizeAuthProvider(provider)
	return providerDefaultAuthMethod[provider]
}

func authFallbackOrder(provider string) []string {
	provider = normalizeAuthProvider(provider)
	return providerAuthFallbackOrder[provider]
}
