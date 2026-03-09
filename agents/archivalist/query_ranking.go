package archivalist

import (
	"fmt"
	"sort"
	"strings"
)

func sortEntriesForQuery(results []*Entry, q ArchiveQuery) {
	if strings.TrimSpace(q.SearchText) == "" {
		sortEntriesByCreatedDesc(results)
		return
	}

	sort.Slice(results, func(i, j int) bool {
		left := entrySearchScore(results[i], q.SearchText)
		right := entrySearchScore(results[j], q.SearchText)
		if left == right {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return left > right
	})
}

func sortEntriesByCreatedDesc(results []*Entry) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
}

func entryMatchesSearchText(entry *Entry, searchText string) bool {
	searchText = strings.TrimSpace(searchText)
	if searchText == "" {
		return true
	}
	return entrySearchScore(entry, searchText) > 0
}

func entrySearchScore(entry *Entry, searchText string) float64 {
	if entry == nil {
		return 0
	}

	searchText = strings.TrimSpace(strings.ToLower(searchText))
	if searchText == "" {
		return 0
	}

	title := strings.ToLower(strings.TrimSpace(entry.Title))
	content := strings.ToLower(strings.TrimSpace(entry.Content))
	metadata := strings.ToLower(strings.Join(entryMetadataSearchValues(entry.Metadata), " "))
	terms := uniqueSearchTerms(strings.Fields(searchText))

	score := 0.0
	if title != "" && strings.Contains(title, searchText) {
		score += 2.0
	}
	if content != "" && strings.Contains(content, searchText) {
		score += 1.5
	}
	if metadata != "" && strings.Contains(metadata, searchText) {
		score += 1.0
	}

	for _, term := range terms {
		if title != "" && strings.Contains(title, term) {
			score += 0.45
		}
		if content != "" && strings.Contains(content, term) {
			score += 0.25
		}
		if metadata != "" && strings.Contains(metadata, term) {
			score += 0.15
		}
	}

	return score
}

func entryMetadataSearchValues(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}

	values := make([]string, 0, len(metadata))
	for _, raw := range metadata {
		values = append(values, flattenMetadataSearchValue(raw)...)
	}
	return values
}

func flattenMetadataSearchValue(value any) []string {
	switch typed := value.(type) {
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return filterSearchValues(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, flattenMetadataSearchValue(item)...)
		}
		return values
	case fmt.Stringer:
		return flattenMetadataSearchValue(typed.String())
	case bool:
		if typed {
			return []string{"true"}
		}
		return []string{"false"}
	case int, int8, int16, int32, int64, float32, float64:
		return []string{fmt.Sprint(typed)}
	default:
		return nil
	}
}

func filterSearchValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func uniqueSearchTerms(terms []string) []string {
	if len(terms) == 0 {
		return nil
	}

	result := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		result = append(result, term)
	}
	return result
}
