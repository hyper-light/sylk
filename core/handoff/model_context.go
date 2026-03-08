package handoff

import "strings"

func ContextWindowForModel(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case model == "":
		return 200_000
	case strings.HasPrefix(model, "gemini"):
		return 1_000_000
	case strings.Contains(model, "1m"):
		return 1_000_000
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o"):
		return 272_000
	case strings.HasPrefix(model, "claude"), strings.HasPrefix(model, "haiku"),
		strings.HasPrefix(model, "sonnet"), strings.HasPrefix(model, "opus"),
		strings.Contains(model, "200k"):
		return 200_000
	default:
		return 200_000
	}
}
