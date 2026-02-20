package conflictview

import "strings"

// AutoResolveKind identifies the type of auto-resolvable conflict.
type AutoResolveKind int

const (
	AutoImportMerge    AutoResolveKind = iota
	AutoWhitespaceOnly
	AutoAdjacentAdd
)

// autoResolveKindLabels maps kind to display label.
var autoResolveKindLabels = [...]string{
	AutoImportMerge:    "imports",
	AutoWhitespaceOnly: "whitespace",
	AutoAdjacentAdd:    "adjacent",
}

// AutoResolution records a deterministic resolution suggestion for a hunk.
type AutoResolution struct {
	HunkIdx int
	Kind    AutoResolveKind
	Result  ConflictResolution
}

// autoResolveHunks scans hunks for trivially resolvable conflicts.
// Returns nil if no hunks can be auto-resolved.
func autoResolveHunks(hunks []ConflictHunk, lines []string, path string) []AutoResolution {
	var result []AutoResolution
	for i, h := range hunks {
		oursLines := extractLines(lines, h.StartLine+1, h.DivLine)
		theirsLines := extractLines(lines, h.DivLine+1, h.EndLine)

		if isImportConflict(oursLines, theirsLines, path) {
			result = append(result, AutoResolution{
				HunkIdx: i,
				Kind:    AutoImportMerge,
				Result:  ResBoth,
			})
			continue
		}
		if isWhitespaceOnly(oursLines, theirsLines) {
			result = append(result, AutoResolution{
				HunkIdx: i,
				Kind:    AutoWhitespaceOnly,
				Result:  ResTheirs,
			})
			continue
		}
		if isAdjacentAdd(oursLines, theirsLines) {
			result = append(result, AutoResolution{
				HunkIdx: i,
				Kind:    AutoAdjacentAdd,
				Result:  ResBoth,
			})
		}
	}
	return result
}

// extractLines returns lines[start:end], safe for out-of-range.
func extractLines(lines []string, start, end int) []string {
	start = max(start, 0)
	end = min(end, len(lines))
	if start >= end {
		return nil
	}
	return lines[start:end]
}

// isImportConflict detects if both sides only add import statements.
// Language detection uses the file extension.
func isImportConflict(ours, theirs []string, path string) bool {
	if len(ours) == 0 || len(theirs) == 0 {
		return false
	}
	prefixes := importPrefixes(path)
	if len(prefixes) == 0 {
		return false
	}
	return allMatchPrefixes(ours, prefixes) && allMatchPrefixes(theirs, prefixes)
}

// importPrefixes returns the import statement prefixes for a language
// detected from the file extension.
func importPrefixes(path string) []string {
	ext := strings.ToLower(pathExt(path))
	switch ext {
	case ".go":
		return []string{"import", "\t\"", "\t\"", "\""}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return []string{"import ", "require("}
	case ".py":
		return []string{"import ", "from "}
	case ".java", ".kt", ".kts", ".scala":
		return []string{"import "}
	case ".rs":
		return []string{"use "}
	default:
		return nil
	}
}

// pathExt extracts the extension including the dot.
func pathExt(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}

// allMatchPrefixes returns true if every non-blank line starts with one
// of the given prefixes.
func allMatchPrefixes(lines []string, prefixes []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == ")" || trimmed == "(" {
			continue // Allow blank lines and Go import group parens.
		}
		if !startsWithAny(trimmed, prefixes) {
			return false
		}
	}
	return true
}

// startsWithAny returns true if s starts with any of the prefixes.
func startsWithAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// isWhitespaceOnly detects if the difference between ours and theirs is
// purely whitespace.
func isWhitespaceOnly(ours, theirs []string) bool {
	if len(ours) != len(theirs) {
		return false
	}
	for i := range ours {
		if strings.TrimSpace(ours[i]) != strings.TrimSpace(theirs[i]) {
			return false
		}
	}
	return true
}

// isAdjacentAdd detects if both sides added non-overlapping content
// (no matching lines between them, indicating independent additions).
func isAdjacentAdd(ours, theirs []string) bool {
	if len(ours) == 0 || len(theirs) == 0 {
		return false
	}
	// Both sides must contain only additions (no common lines).
	oursSet := make(map[string]struct{}, len(ours))
	for _, l := range ours {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			oursSet[trimmed] = struct{}{}
		}
	}
	for _, l := range theirs {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if _, overlap := oursSet[trimmed]; overlap {
			return false
		}
	}
	return true
}
