package storage

import "strings"

// slugMaxLen caps the slug length to prevent excessively long filenames.
const slugMaxLen = 50

// Slug converts s into a path-safe snake_case segment.
// Lowercases, replaces non-alphanumeric with _, collapses consecutive _,
// trims edges, and truncates to slugMaxLen. Returns "unnamed" if empty.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "unnamed"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isSlugAlphaNum(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	collapsed := collapseUnderscores(b.String())
	collapsed = strings.Trim(collapsed, "_")
	if len(collapsed) > slugMaxLen {
		collapsed = collapsed[:slugMaxLen]
		collapsed = strings.TrimRight(collapsed, "_")
	}
	if collapsed == "" {
		return "unnamed"
	}
	return collapsed
}

func isSlugAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func collapseUnderscores(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prev := false
	for i := range len(s) {
		if s[i] == '_' {
			if !prev {
				b.WriteByte('_')
				prev = true
			}
		} else {
			b.WriteByte(s[i])
			prev = false
		}
	}
	return b.String()
}
