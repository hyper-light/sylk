package academic

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/core/providers"
)

func normalizeSearchQuery(query string) (string, map[string]struct{}) {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r):
			return ' '
		default:
			return ' '
		}
	}, query)
	fields := strings.Fields(normalized)
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		tokens[field] = struct{}{}
	}
	if len(tokens) == 0 {
		return "", nil
	}
	ordered := make([]string, 0, len(tokens))
	for token := range tokens {
		ordered = append(ordered, token)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, " "), tokens
}

func extractStringField(raw string, field string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	value, _ := payload[field].(string)
	return strings.TrimSpace(value)
}

func consultationTarget(call providers.ToolCall, name, result string) string {
	if raw := extractStringField(call.Arguments, "target"); raw != "" {
		return strings.ToLower(raw)
	}
	if raw := extractStringField(result, "target"); raw != "" {
		return strings.ToLower(raw)
	}
	if name == "clone_via_librarian" {
		return "librarian"
	}
	if strings.HasPrefix(name, "consult_") {
		return strings.ToLower(strings.TrimPrefix(name, "consult_"))
	}
	return ""
}

func responseLooksLikeResearchStatus(content string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(content))
	if trimmed == "" {
		return true
	}
	trimmed = strings.NewReplacer(
		"`", "",
		"’", "'",
		"“", "\"",
		"”", "\"",
	).Replace(trimmed)
	phrases := []string{
		"still searching",
		"still researching",
		"searching again",
		"looking for more",
		"need to search",
		"let me search",
		"gathering more sources",
		"researching further",
		"still fetching",
		"fetching now",
		"let me fetch",
		"i'm fetching",
		"i am fetching",
		"i'll fetch",
		"i will fetch",
		"about to fetch",
		"executing web_fetch",
		"executing fetch_document",
		"executing crawl_links",
		"executing a web fetch",
		"running web_fetch",
		"running fetch_document",
		"running crawl_links",
		"opening the page",
		"opening this page",
		"let me open the page",
		"consulting librarian",
		"consulting the librarian",
		"consulting archivalist",
		"consulting the archivalist",
		"i'll consult",
		"i will consult",
		"cloning the repository",
		"cloning the repo",
	}
	for _, phrase := range phrases {
		if strings.Contains(trimmed, phrase) {
			return true
		}
	}
	return false
}
