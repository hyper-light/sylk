package versioning

import (
	"context"
	"sort"
	"strings"
)

const maxReadFileDirectoryEntries = 50

// ReadFileToolResult reads a file for tool output. When the requested path is
// a directory, it returns a structured directory preview instead of an error so
// agents can recover without burning a failed tool turn.
func ReadFileToolResult(ctx context.Context, fa FileAccess, path string, offset, limit int) (map[string]any, error) {
	if fa == nil {
		return nil, ErrPermissionDenied
	}

	if info, err := fa.Stat(ctx, path); err == nil && info != nil && info.IsDir() {
		return readFileDirectoryResult(ctx, fa, path)
	}

	content, err := fa.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	return readFileContentResult(path, string(content), offset, limit), nil
}

func readFileContentResult(path, content string, offset, limit int) map[string]any {
	lines := strings.Split(content, "\n")
	start := readFileClampOffset(offset, len(lines))
	maxLines := readFileClampLimit(limit)
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{
		"path":        path,
		"kind":        "file",
		"content":     strings.Join(lines[start:end], "\n"),
		"offset":      start,
		"limit":       maxLines,
		"total_lines": len(lines),
		"truncated":   end < len(lines),
	}
}

func readFileClampOffset(offset, total int) int {
	if offset < 0 {
		return 0
	}
	if offset > total {
		return total
	}
	return offset
}

func readFileClampLimit(limit int) int {
	if limit <= 0 {
		return 1000
	}
	return limit
}

func readFileDirectoryResult(ctx context.Context, fa FileAccess, path string) (map[string]any, error) {
	entries, err := fa.ListDir(ctx, path)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		name := entry.Name()
		if info, infoErr := entry.Info(); infoErr == nil && info != nil && info.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)

	truncated := false
	if len(names) > maxReadFileDirectoryEntries {
		names = append([]string(nil), names[:maxReadFileDirectoryEntries]...)
		truncated = true
	}

	return map[string]any{
		"path":              path,
		"kind":              "directory",
		"message":           "path is a directory; inspect these entries or use glob to narrow to a file",
		"entries":           names,
		"entry_count":       len(entries),
		"truncated_entries": truncated,
	}, nil
}
