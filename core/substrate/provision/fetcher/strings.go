package fetcher

import "strings"

// stringReplaceAll wraps strings.ReplaceAll. It exists as a single
// indirection so future enhancements (e.g. richer template syntax) can
// land in one place rather than spreading through the package.
func stringReplaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}
