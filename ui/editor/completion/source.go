package completion

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// ---------------------------------------------------------------------------
// Source interface and context
// ---------------------------------------------------------------------------

// Source produces completion candidates from a specific origin.
type Source interface {
	// ID returns a unique identifier for this source.
	ID() string
	// Gather returns completion items relevant to the given context.
	Gather(ctx CompletionContext) []CompletionItem
}

// CompletionContext carries the editor state needed by sources to generate
// candidates.
type CompletionContext struct {
	Content     []rune         // full buffer content as runes
	CursorPos   int            // absolute rune offset of cursor
	CurrentLine int            // zero-indexed current line number
	Prefix      string         // word prefix being completed
	Mode        CompletionMode // which sub-mode triggered completion
}

// ---------------------------------------------------------------------------
// Source registry
// ---------------------------------------------------------------------------

// maxSources is the upper bound on registered sources to prevent unbounded
// growth. Derived from the practical number of distinct completion
// providers an editor configuration would use.
const maxSources = 16

// modeSourceTable maps each completion mode to the set of source IDs that
// should be queried. ModeGeneric queries all sources.
var modeSourceTable = map[CompletionMode][]string{
	ModeFile: {"filepath"},
	ModeLine: {"line"},
}

// SourceRegistry manages a bounded set of completion sources with
// thread-safe access.
type SourceRegistry struct {
	mu      sync.RWMutex
	sources []Source
}

// NewSourceRegistry creates an empty registry.
func NewSourceRegistry() *SourceRegistry {
	return &SourceRegistry{
		sources: make([]Source, 0, maxSources),
	}
}

// Register adds a source to the registry. If the registry is at capacity,
// the registration is silently dropped. If a source with the same ID
// already exists it is replaced.
func (r *SourceRegistry) Register(source Source) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Replace existing source with same ID.
	for i, s := range r.sources {
		if s.ID() == source.ID() {
			r.sources[i] = source
			return
		}
	}
	if len(r.sources) >= maxSources {
		return
	}
	r.sources = append(r.sources, source)
}

// Unregister removes the source with the given ID.
func (r *SourceRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = slices.DeleteFunc(r.sources, func(s Source) bool {
		return s.ID() == id
	})
}

// Gather collects candidates from all sources relevant to the given mode,
// merges them, deduplicates by word, and sorts by descending score.
func (r *SourceRegistry) Gather(ctx CompletionContext, mode CompletionMode) []CompletionItem {
	r.mu.RLock()
	sources := make([]Source, len(r.sources))
	copy(sources, r.sources)
	r.mu.RUnlock()

	filtered := filterSources(sources, mode)

	var all []CompletionItem
	for _, src := range filtered {
		items := src.Gather(ctx)
		all = append(all, items...)
	}

	all = deduplicateItems(all)
	slices.SortFunc(all, func(a, b CompletionItem) int {
		return b.Score - a.Score // descending
	})

	if len(all) > maxCompletionItems {
		all = all[:maxCompletionItems]
	}
	return all
}

// filterSources returns the subset of sources applicable to the mode.
// ModeGeneric queries every registered source.
func filterSources(sources []Source, mode CompletionMode) []Source {
	allowed, restricted := modeSourceTable[mode]
	if !restricted {
		return sources
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowSet[id] = struct{}{}
	}
	var out []Source
	for _, s := range sources {
		if _, ok := allowSet[s.ID()]; ok {
			out = append(out, s)
		}
	}
	return out
}

// deduplicateItems removes duplicate words, keeping the highest-scored
// instance of each.
func deduplicateItems(items []CompletionItem) []CompletionItem {
	seen := make(map[string]int, len(items))
	result := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		if idx, exists := seen[item.Word]; exists {
			if item.Score > result[idx].Score {
				result[idx] = item
			}
			continue
		}
		seen[item.Word] = len(result)
		result = append(result, item)
	}
	return result
}

// ---------------------------------------------------------------------------
// Built-in source: buffer words
// ---------------------------------------------------------------------------

// maxBufferWords caps the number of unique words extracted from the buffer.
const maxBufferWords = 1000

// BufferWordSource scans the buffer for unique words and returns those
// matching the prefix, scored by frequency and proximity to the cursor.
type BufferWordSource struct{}

// ID returns the source identifier.
func (BufferWordSource) ID() string { return "buffer" }

// Gather tokenizes buffer content into words, filters by prefix, and
// scores by frequency weighted by recency (proximity to cursor).
func (BufferWordSource) Gather(ctx CompletionContext) []CompletionItem {
	if ctx.Prefix == "" {
		return nil
	}

	words := tokenizeWords(ctx.Content)
	prefixLower := strings.ToLower(ctx.Prefix)

	type wordStat struct {
		word      string
		frequency int
		bestDist  int
	}

	stats := make(map[string]*wordStat, len(words))
	for _, w := range words {
		lower := strings.ToLower(w.text)
		if !strings.HasPrefix(lower, prefixLower) {
			continue
		}
		if w.text == ctx.Prefix {
			continue
		}
		s, exists := stats[w.text]
		if !exists {
			if len(stats) >= maxBufferWords {
				continue
			}
			s = &wordStat{word: w.text, bestDist: abs(w.pos - ctx.CursorPos)}
			stats[w.text] = s
		}
		s.frequency++
		dist := abs(w.pos - ctx.CursorPos)
		if dist < s.bestDist {
			s.bestDist = dist
		}
	}

	contentLen := max(len(ctx.Content), 1)
	items := make([]CompletionItem, 0, len(stats))
	for _, s := range stats {
		// Score: frequency bonus + proximity bonus (closer = higher).
		// Proximity is inverted distance normalized to [0, 100].
		proximityScore := 100 - min((s.bestDist*100)/contentLen, 100)
		score := s.frequency*10 + proximityScore
		items = append(items, CompletionItem{
			Word:  s.word,
			Kind:  KindBuffer,
			Menu:  "[buf]",
			Score: score,
		})
	}
	return items
}

// wordToken is a word found during tokenization with its buffer position.
type wordToken struct {
	text string
	pos  int
}

// tokenizeWords extracts word tokens from content using unicode word
// boundaries. A word is a run of letters, digits, or underscores.
func tokenizeWords(content []rune) []wordToken {
	var tokens []wordToken
	total := len(content)
	i := 0
	for i < total {
		// Skip non-word runes.
		if !isIdentRune(content[i]) {
			i++
			continue
		}
		start := i
		for i < total && isIdentRune(content[i]) {
			i++
		}
		tokens = append(tokens, wordToken{
			text: string(content[start:i]),
			pos:  start,
		})
	}
	return tokens
}

// isIdentRune returns true for runes that constitute an identifier:
// letters, digits, and underscore.
func isIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// ---------------------------------------------------------------------------
// Built-in source: file paths
// ---------------------------------------------------------------------------

// maxFileResults caps directory listing results.
const maxFileResults = 200

// FilePathSource completes file paths from the working directory.
type FilePathSource struct {
	// WorkDir is the base directory for relative path resolution.
	// When empty, os.Getwd is used.
	WorkDir string
}

// ID returns the source identifier.
func (FilePathSource) ID() string { return "filepath" }

// Gather lists directory entries matching the prefix path.
func (s FilePathSource) Gather(ctx CompletionContext) []CompletionItem {
	if ctx.Prefix == "" {
		return nil
	}

	// Only activate when the prefix looks like a path.
	if !looksLikePath(ctx.Prefix) {
		return nil
	}

	dir, base := splitPathPrefix(ctx.Prefix, s.resolveWorkDir())
	entries := readDirBounded(dir, maxFileResults)

	baseLower := strings.ToLower(base)
	items := make([]CompletionItem, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), baseLower) {
			continue
		}
		word := filepath.Join(dir, name)
		if entry.IsDir() {
			word += "/"
		}
		items = append(items, CompletionItem{
			Word:  word,
			Kind:  KindFile,
			Menu:  "[file]",
			Score: 50,
		})
	}
	return items
}

// resolveWorkDir returns the effective working directory for path
// completion.
func (s FilePathSource) resolveWorkDir() string {
	if s.WorkDir != "" {
		return s.WorkDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// looksLikePath returns true when the prefix contains path separators or
// starts with a dot, suggesting the user is completing a file path.
func looksLikePath(prefix string) bool {
	return strings.ContainsRune(prefix, '/') || strings.HasPrefix(prefix, ".")
}

// splitPathPrefix splits a typed prefix into a directory to list and a
// filename prefix to match against.
func splitPathPrefix(prefix, workDir string) (string, string) {
	// Absolute paths are used directly.
	if filepath.IsAbs(prefix) {
		return filepath.Dir(prefix), filepath.Base(prefix)
	}
	// Relative paths are resolved against workDir.
	full := filepath.Join(workDir, prefix)
	return filepath.Dir(full), filepath.Base(full)
}

// readDirBounded reads at most limit entries from a directory, returning
// an empty slice on any error.
func readDirBounded(dir string, limit int) []os.DirEntry {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()

	entries, err := f.ReadDir(limit)
	if err != nil {
		return entries // partial read is acceptable
	}
	return entries
}

// ---------------------------------------------------------------------------
// Built-in source: line completion
// ---------------------------------------------------------------------------

// maxLineResults caps the number of line completion candidates.
const maxLineResults = 100

// LineSource completes entire lines from the buffer that match the current
// line's prefix.
type LineSource struct{}

// ID returns the source identifier.
func (LineSource) ID() string { return "line" }

// Gather finds buffer lines whose trimmed content starts with the prefix
// and returns them as completion items.
func (LineSource) Gather(ctx CompletionContext) []CompletionItem {
	if ctx.Prefix == "" {
		return nil
	}

	lines := splitLines(ctx.Content)
	prefixLower := strings.ToLower(ctx.Prefix)
	seen := make(map[string]struct{}, maxLineResults)

	items := make([]CompletionItem, 0, min(len(lines), maxLineResults))
	for i, line := range lines {
		if i == ctx.CurrentLine {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(trimmed), prefixLower) {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}

		// Score by proximity to cursor line.
		dist := abs(i - ctx.CurrentLine)
		score := 100 - min(dist, 100)
		items = append(items, CompletionItem{
			Word:  trimmed,
			Kind:  KindLine,
			Menu:  "[line]",
			Score: score,
		})
		if len(items) >= maxLineResults {
			break
		}
	}
	return items
}

// splitLines splits rune content into string lines on newline boundaries.
func splitLines(content []rune) []string {
	var lines []string
	start := 0
	for i, r := range content {
		if r == '\n' {
			lines = append(lines, string(content[start:i]))
			start = i + 1
		}
	}
	// Trailing content after last newline.
	if start <= len(content) {
		lines = append(lines, string(content[start:]))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

// abs returns the absolute value of an integer.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
