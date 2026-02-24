package shared

import (
	"strconv"
	"strings"
)

// ParseGoToolLine parses a standard Go tool diagnostic line in the format:
// file.go:line:col: message
// Returns the parsed components and whether parsing succeeded.
func ParseGoToolLine(line string) (file string, lineNum, col int, msg string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", 0, 0, "", false
	}

	// Split on ": " to get location and message
	parts := strings.SplitN(line, ": ", 2)
	if len(parts) < 2 {
		return "", 0, 0, "", false
	}

	msg = strings.TrimSpace(parts[1])
	locParts := strings.Split(parts[0], ":")
	if len(locParts) < 2 {
		return "", 0, 0, "", false
	}

	file = locParts[0]
	lineNum, err := strconv.Atoi(locParts[1])
	if err != nil {
		return "", 0, 0, "", false
	}

	if len(locParts) >= 3 {
		col, _ = strconv.Atoi(locParts[2])
	}

	return file, lineNum, col, msg, true
}

// FilterByPaths filters validation issues to only those whose file paths
// are in the provided set.
func FilterByPaths(issues []ValidationIssue, paths map[string]bool) []ValidationIssue {
	if len(paths) == 0 {
		return issues
	}
	filtered := make([]ValidationIssue, 0, len(issues))
	for _, issue := range issues {
		if paths[issue.File] {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

// DeduplicateIssues removes duplicate issues based on (file, line, message).
func DeduplicateIssues(issues []ValidationIssue) []ValidationIssue {
	type key struct {
		file    string
		line    int
		message string
	}
	seen := make(map[key]bool, len(issues))
	deduped := make([]ValidationIssue, 0, len(issues))
	for _, issue := range issues {
		k := key{file: issue.File, line: issue.Line, message: issue.Message}
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, issue)
	}
	return deduped
}
