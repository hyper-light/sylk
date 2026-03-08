package librarian

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// SearchLedger tracks search evidence accumulated across tool loop turns.
// It records every search invocation and its results, detects saturation
// (repeated or overlapping searches), and produces a compact evidence
// summary that is injected into the LLM context so it knows what it has
// found and when to stop searching.
//
// The ledger is scoped to a single request — create one per tool loop.
type SearchLedger struct {
	mu      sync.Mutex
	entries []searchEntry
	// argsIndex deduplicates by tool name + args hash.
	argsIndex map[string]int // key: "tool:argsHash" → index in entries
	// fileIndex tracks unique files seen across all searches.
	fileIndex map[string]struct{}
	// symbolIndex tracks unique symbols found.
	symbolIndex map[string]struct{}
}

// searchEntry records a single tool invocation and its outcome.
type searchEntry struct {
	Turn       int    // Tool loop turn when this search was executed
	ToolName   string // e.g. "grep", "find_symbol", "glob"
	ArgsHash   string // SHA-256 prefix of serialized arguments
	ArgsLabel  string // Human-readable summary of key args
	MatchCount int    // Number of results returned
	FileCount  int    // Unique files in this result set
	Symbols    []string
	Files      []string
	Saturated  bool // True if this search returned only already-seen results
}

// NewSearchLedger creates a ledger for tracking search progress.
func NewSearchLedger() *SearchLedger {
	return &SearchLedger{
		argsIndex:   make(map[string]int, 16),
		fileIndex:   make(map[string]struct{}, 64),
		symbolIndex: make(map[string]struct{}, 32),
	}
}

// searchToolNames is the set of tool names that are search operations.
// Non-search tools (read_file, git) are not tracked for saturation.
var searchToolNames = map[string]struct{}{
	"grep":            {},
	"glob":            {},
	"find_symbol":     {},
	"search_codebase": {},
	"find_pattern":    {},
	"locate_symbol":   {},
	"knowledge_search": {},
	"ast_grep_search": {},
}

// IsSearchTool returns true if the tool name is a tracked search operation.
func IsSearchTool(name string) bool {
	_, ok := searchToolNames[name]
	return ok
}

// Record records a tool call and its result. Extracts match counts, file
// paths, and symbols from the result JSON. Detects saturation by checking
// whether all files in the result are already in the ledger.
func (sl *SearchLedger) Record(turn int, toolName, args, result string) {
	if !IsSearchTool(toolName) {
		return
	}

	sl.mu.Lock()
	defer sl.mu.Unlock()

	argsHash := hashArgs(args)
	key := toolName + ":" + argsHash

	// Extract result metadata.
	matchCount, files, symbols := extractSearchMetadata(result)

	// Check for exact duplicate (same tool + same args).
	if _, exists := sl.argsIndex[key]; exists {
		// Already recorded — mark as saturated and skip.
		return
	}

	// Check saturation: are all returned files already known?
	saturated := len(files) > 0
	for _, f := range files {
		if _, seen := sl.fileIndex[f]; !seen {
			saturated = false
			break
		}
	}

	entry := searchEntry{
		Turn:       turn,
		ToolName:   toolName,
		ArgsHash:   argsHash,
		ArgsLabel:  extractArgsLabel(toolName, args),
		MatchCount: matchCount,
		FileCount:  len(files),
		Symbols:    symbols,
		Files:      files,
		Saturated:  saturated,
	}

	idx := len(sl.entries)
	sl.entries = append(sl.entries, entry)
	sl.argsIndex[key] = idx

	// Update global indices.
	for _, f := range files {
		sl.fileIndex[f] = struct{}{}
	}
	for _, s := range symbols {
		sl.symbolIndex[s] = struct{}{}
	}
}

// SearchCount returns the total number of search operations recorded.
func (sl *SearchLedger) SearchCount() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return len(sl.entries)
}

// UniqueFileCount returns the total unique files discovered.
func (sl *SearchLedger) UniqueFileCount() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return len(sl.fileIndex)
}

// SaturatedCount returns how many searches returned only already-seen files.
func (sl *SearchLedger) SaturatedCount() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	count := 0
	for _, e := range sl.entries {
		if e.Saturated {
			count++
		}
	}
	return count
}

// TotalMatches returns the sum of match counts across all searches.
func (sl *SearchLedger) TotalMatches() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	total := 0
	for _, e := range sl.entries {
		total += e.MatchCount
	}
	return total
}

// IsSaturated returns true when the last N searches all returned
// only already-seen results, indicating no new evidence is being found.
// The threshold is 2 consecutive saturated searches.
func (sl *SearchLedger) IsSaturated() bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.isSaturatedLocked()
}

func (sl *SearchLedger) isSaturatedLocked() bool {
	n := len(sl.entries)
	if n < 2 {
		return false
	}
	return sl.entries[n-1].Saturated && sl.entries[n-2].Saturated
}

// EvidenceSummary produces a compact text summary of all search evidence
// gathered so far. This is injected into the LLM context so it knows
// what it has found and can make informed decisions about whether to
// continue searching or synthesize an answer.
func (sl *SearchLedger) EvidenceSummary() string {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if len(sl.entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[SEARCH EVIDENCE LEDGER]\n")
	fmt.Fprintf(&b, "Searches: %d | Unique files: %d | Total matches: %d",
		len(sl.entries), len(sl.fileIndex), sl.totalMatchesLocked())

	saturated := 0
	for _, e := range sl.entries {
		if e.Saturated {
			saturated++
		}
	}
	if saturated > 0 {
		fmt.Fprintf(&b, " | Saturated: %d", saturated)
	}
	b.WriteByte('\n')

	// List searches compactly.
	for i, e := range sl.entries {
		status := "✓"
		if e.Saturated {
			status = "≡" // indicates overlap with prior results
		}
		fmt.Fprintf(&b, "  %d. %s %s → %d matches in %d files %s\n",
			i+1, status, e.ToolName, e.MatchCount, e.FileCount, e.ArgsLabel)
	}

	// Saturation advisory.
	if sl.isSaturatedLocked() {
		b.WriteString("\n⚠ SATURATED: Recent searches found no new files. You likely have enough evidence to synthesize your answer.\n")
	}

	b.WriteString("[/SEARCH EVIDENCE LEDGER]")
	return b.String()
}

func (sl *SearchLedger) totalMatchesLocked() int {
	total := 0
	for _, e := range sl.entries {
		total += e.MatchCount
	}
	return total
}

// hashArgs produces a short SHA-256 prefix of the arguments string.
func hashArgs(args string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(args)))
	return hex.EncodeToString(h[:8])
}

// extractArgsLabel pulls a human-readable label from tool arguments.
// For grep: the pattern. For find_symbol: the name. For glob: the pattern.
func extractArgsLabel(toolName, args string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return ""
	}

	labelKeys := map[string][]string{
		"grep":            {"pattern", "include"},
		"find_symbol":     {"name", "include"},
		"glob":            {"pattern"},
		"search_codebase": {"query"},
		"find_pattern":    {"pattern_type"},
		"locate_symbol":   {"symbol"},
		"knowledge_search": {"query"},
		"ast_grep_search": {"pattern", "lang"},
	}

	keys, ok := labelKeys[toolName]
	if !ok {
		return ""
	}

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if v, exists := parsed[k]; exists {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			parts = append(parts, fmt.Sprintf("%s=%q", k, s))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// extractSearchMetadata parses a tool result JSON to extract match count,
// file paths, and symbol names. Best-effort — returns zeros on parse failure.
func extractSearchMetadata(result string) (matchCount int, files []string, symbols []string) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return 0, nil, nil
	}

	// Extract count.
	if c, ok := parsed["count"]; ok {
		matchCount = coerceInt(c)
	}

	// Extract files from matches array.
	fileSet := make(map[string]struct{}, 16)
	symbolSet := make(map[string]struct{}, 8)

	extractFromArray(parsed, "matches", fileSet, symbolSet)
	extractFromArray(parsed, "symbols", fileSet, symbolSet)
	extractFromArray(parsed, "results", fileSet, symbolSet)

	// Convert sets to slices.
	files = make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	symbols = make([]string, 0, len(symbolSet))
	for s := range symbolSet {
		symbols = append(symbols, s)
	}

	if matchCount == 0 && len(files) > 0 {
		matchCount = len(files)
	}

	return matchCount, files, symbols
}

// extractFromArray pulls file and symbol data from a named array field.
func extractFromArray(parsed map[string]any, key string, fileSet, symbolSet map[string]struct{}) {
	arr, ok := parsed[key]
	if !ok {
		return
	}
	items, ok := arr.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if f, ok := m["file"].(string); ok && f != "" {
			fileSet[f] = struct{}{}
		}
		if f, ok := m["path"].(string); ok && f != "" {
			fileSet[f] = struct{}{}
		}
		if s, ok := m["name"].(string); ok && s != "" {
			symbolSet[s] = struct{}{}
		}
		if s, ok := m["symbol_context"].(string); ok && s != "" {
			symbolSet[s] = struct{}{}
		}
	}
}

// coerceInt converts a JSON number (float64) to int.
func coerceInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
