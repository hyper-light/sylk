package code

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// HighlightRegion marks a column range within a single line as belonging
// to a syntax category. Columns are byte offsets into the line string.
type HighlightRegion struct {
	StartCol int
	EndCol   int
	Category theme.SyntaxCategory
}

// Highlighter applies syntax highlighting to source code lines using the
// theme's Syntax style map. This implementation uses a pure-Go regex/scanner
// approach and does not require CGo or tree-sitter C bindings.
type Highlighter struct {
	theme   *theme.Theme
	styles  map[theme.SyntaxCategory]lipgloss.Style
	default_ lipgloss.Style
}

// NewHighlighter creates a Highlighter that uses the given theme's syntax
// color map for rendering styled output.
func NewHighlighter(th *theme.Theme) *Highlighter {
	return &Highlighter{
		theme:    th,
		styles:   th.Syntax,
		default_: lipgloss.NewStyle().Foreground(th.Palette.Foreground),
	}
}

// HighlightLine applies highlight regions to a single line string and returns
// the lipgloss-styled result. Regions must not overlap. Characters not covered
// by any region use the default foreground style.
func (h *Highlighter) HighlightLine(line string, _ int, regions []HighlightRegion) string {
	if len(regions) == 0 {
		return h.default_.Render(line)
	}

	lineLen := len(line)
	var b strings.Builder
	b.Grow(lineLen * 2) // pre-allocate for styled output

	cursor := 0
	for _, r := range regions {
		start := clampIndex(r.StartCol, lineLen)
		end := clampIndex(r.EndCol, lineLen)
		if start >= end {
			continue
		}

		// Render unstyled gap before this region.
		if cursor < start {
			b.WriteString(h.default_.Render(line[cursor:start]))
		}

		style := h.styleFor(r.Category)
		b.WriteString(style.Render(line[start:end]))
		cursor = end
	}

	// Render trailing unstyled content.
	if cursor < lineLen {
		b.WriteString(h.default_.Render(line[cursor:]))
	}

	return b.String()
}

// styleFor returns the lipgloss style for a category, falling back to default.
func (h *Highlighter) styleFor(cat theme.SyntaxCategory) lipgloss.Style {
	if s, ok := h.styles[cat]; ok {
		return s
	}
	return h.default_
}

// clampIndex constrains an index to [0, max].
func clampIndex(idx, max int) int {
	if idx < 0 {
		return 0
	}
	if idx > max {
		return max
	}
	return idx
}

// HighlightContent scans source content and produces per-line highlight
// regions using a regex-based scanner. This works without CGo or tree-sitter
// C bindings. The language parameter selects the keyword set.
func (h *Highlighter) HighlightContent(content string, language string) [][]HighlightRegion {
	lines := strings.Split(content, "\n")
	result := make([][]HighlightRegion, len(lines))

	keywords := keywordsForLanguage(language)
	scanner := newLineScanner(keywords)

	inBlockComment := false
	for i, line := range lines {
		var regions []HighlightRegion
		regions, inBlockComment = scanner.scanLine(line, inBlockComment)
		result[i] = regions
	}

	return result
}

// ---------------------------------------------------------------------------
// Line scanner
// ---------------------------------------------------------------------------

// lineScanner performs regex-based syntax detection on a single line.
type lineScanner struct {
	keywordSet  map[string]struct{}
	numberRe    *regexp.Regexp
	constantRe  *regexp.Regexp
}

// numberPattern matches integer and float literals (decimal, hex, octal, binary).
var numberPattern = regexp.MustCompile(`\b(?:0[xX][0-9a-fA-F_]+|0[oO][0-7_]+|0[bB][01_]+|[0-9][0-9_]*(?:\.[0-9_]+)?(?:[eE][+-]?[0-9]+)?)\b`)

// constantPattern matches common constant literals across languages.
var constantPattern = regexp.MustCompile(`\b(?:true|false|nil|null|undefined|iota|none|None|True|False)\b`)

func newLineScanner(keywords []string) *lineScanner {
	set := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		set[kw] = struct{}{}
	}
	return &lineScanner{
		keywordSet: set,
		numberRe:   numberPattern,
		constantRe: constantPattern,
	}
}

// scanLine produces highlight regions for a single line. It tracks block
// comment state across lines via the inBlock parameter.
func (s *lineScanner) scanLine(line string, inBlock bool) ([]HighlightRegion, bool) {
	if inBlock {
		return s.scanBlockCommentContinuation(line)
	}
	return s.scanNormal(line)
}

// scanBlockCommentContinuation handles lines that start inside a /* */ comment.
func (s *lineScanner) scanBlockCommentContinuation(line string) ([]HighlightRegion, bool) {
	endIdx := strings.Index(line, "*/")
	if endIdx < 0 {
		// Entire line is still inside the block comment.
		return []HighlightRegion{{
			StartCol: 0,
			EndCol:   len(line),
			Category: theme.CatComment,
		}}, true
	}
	commentEnd := endIdx + len("*/")
	regions := []HighlightRegion{{
		StartCol: 0,
		EndCol:   commentEnd,
		Category: theme.CatComment,
	}}

	// Scan the remainder of the line normally.
	tail, stillInBlock := s.scanNormal(line[commentEnd:])
	regions = append(regions, shiftRegions(tail, commentEnd)...)
	return regions, stillInBlock
}

// scanNormal scans a line that is not inside a block comment.
func (s *lineScanner) scanNormal(line string) ([]HighlightRegion, bool) {
	regions := make([]HighlightRegion, 0, len(line)/4+1) // bounded initial cap
	occupied := make([]bool, len(line))

	// Phase 1: comments (highest priority - they consume the rest of the line).
	commentRegions, inBlock := s.findComments(line)
	regions = append(regions, commentRegions...)
	markOccupied(occupied, commentRegions)

	// Phase 2: strings.
	stringRegions := s.findStrings(line, occupied)
	regions = append(regions, stringRegions...)
	markOccupied(occupied, stringRegions)

	// Phase 3: numbers.
	numberRegions := s.findNumbers(line, occupied)
	regions = append(regions, numberRegions...)
	markOccupied(occupied, numberRegions)

	// Phase 4: constants.
	constRegions := s.findConstants(line, occupied)
	regions = append(regions, constRegions...)
	markOccupied(occupied, constRegions)

	// Phase 5: keywords (lowest priority among tokens).
	kwRegions := s.findKeywords(line, occupied)
	regions = append(regions, kwRegions...)

	sortRegionsByStart(regions)
	return regions, inBlock
}

// findComments detects line comments (//, #) and the start of block comments (/*).
func (s *lineScanner) findComments(line string) ([]HighlightRegion, bool) {
	lineCommentIdx, blockCommentIdx := findCommentStarts(line)

	// Determine which comes first.
	if lineCommentIdx >= 0 && (blockCommentIdx < 0 || lineCommentIdx < blockCommentIdx) {
		return []HighlightRegion{{
			StartCol: lineCommentIdx,
			EndCol:   len(line),
			Category: theme.CatComment,
		}}, false
	}

	if blockCommentIdx < 0 {
		return nil, false
	}

	// Block comment starts on this line.
	endIdx := strings.Index(line[blockCommentIdx+len("/*"):], "*/")
	if endIdx < 0 {
		// Block comment extends past this line.
		return []HighlightRegion{{
			StartCol: blockCommentIdx,
			EndCol:   len(line),
			Category: theme.CatComment,
		}}, true
	}

	commentEnd := blockCommentIdx + len("/*") + endIdx + len("*/")
	return []HighlightRegion{{
		StartCol: blockCommentIdx,
		EndCol:   commentEnd,
		Category: theme.CatComment,
	}}, false
}

// findCommentStarts returns the byte index of the first // or # comment and
// the first /* block comment opening in the line. Returns -1 if not found.
func findCommentStarts(line string) (lineComment, blockComment int) {
	lineComment = -1
	blockComment = -1

	inString := false
	var stringChar byte

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if inString {
			if ch == '\\' && i+1 < len(line) {
				i++ // skip escaped character
				continue
			}
			if ch == stringChar {
				inString = false
			}
			continue
		}

		if ch == '"' || ch == '\'' || ch == '`' {
			inString = true
			stringChar = ch
			continue
		}

		if ch == '/' && i+1 < len(line) {
			if line[i+1] == '/' && lineComment < 0 {
				lineComment = i
				return lineComment, blockComment
			}
			if line[i+1] == '*' && blockComment < 0 {
				blockComment = i
				return lineComment, blockComment
			}
		}

		if ch == '#' && lineComment < 0 {
			lineComment = i
			return lineComment, blockComment
		}
	}

	return lineComment, blockComment
}

// findStrings detects quoted string literals (double, single, backtick).
func (s *lineScanner) findStrings(line string, occupied []bool) []HighlightRegion {
	regions := make([]HighlightRegion, 0, len(line)/8+1)

	i := 0
	for i < len(line) {
		if occupied[i] {
			i++
			continue
		}

		ch := line[i]
		if ch != '"' && ch != '\'' && ch != '`' {
			i++
			continue
		}

		end := findStringEnd(line, i, ch)
		regions = append(regions, HighlightRegion{
			StartCol: i,
			EndCol:   end,
			Category: theme.CatString,
		})
		i = end
	}

	return regions
}

// findStringEnd finds the closing delimiter for a string starting at pos.
func findStringEnd(line string, pos int, delim byte) int {
	for i := pos + 1; i < len(line); i++ {
		if line[i] == '\\' && delim != '`' {
			i++ // skip escaped character
			continue
		}
		if line[i] == delim {
			return i + 1
		}
	}
	return len(line)
}

// findNumbers finds numeric literals using the compiled regex.
func (s *lineScanner) findNumbers(line string, occupied []bool) []HighlightRegion {
	matches := s.numberRe.FindAllStringIndex(line, -1)
	return matchesToRegions(matches, occupied, theme.CatNumber)
}

// findConstants finds constant literals (true, false, nil, etc).
func (s *lineScanner) findConstants(line string, occupied []bool) []HighlightRegion {
	matches := s.constantRe.FindAllStringIndex(line, -1)
	return matchesToRegions(matches, occupied, theme.CatConstant)
}

// matchesToRegions converts regex match indices to HighlightRegions,
// skipping any that overlap with already-occupied bytes.
func matchesToRegions(matches [][]int, occupied []bool, cat theme.SyntaxCategory) []HighlightRegion {
	regions := make([]HighlightRegion, 0, len(matches))
	for _, m := range matches {
		if !anyOccupied(occupied, m[0], m[1]) {
			regions = append(regions, HighlightRegion{
				StartCol: m[0],
				EndCol:   m[1],
				Category: cat,
			})
		}
	}
	return regions
}

// findKeywords scans for word boundaries matching the keyword set.
func (s *lineScanner) findKeywords(line string, occupied []bool) []HighlightRegion {
	regions := make([]HighlightRegion, 0, len(line)/6+1)

	i := 0
	for i < len(line) {
		if occupied[i] || !isWordStart(line, i) {
			i++
			continue
		}

		end := wordEnd(line, i)
		word := line[i:end]

		if _, ok := s.keywordSet[word]; ok {
			if !anyOccupied(occupied, i, end) {
				regions = append(regions, HighlightRegion{
					StartCol: i,
					EndCol:   end,
					Category: theme.CatKeyword,
				})
			}
		}
		i = end
	}

	return regions
}

// isWordStart returns true if position i is the beginning of a word
// (preceded by a non-letter/non-digit or at position 0).
func isWordStart(line string, i int) bool {
	ch := rune(line[i])
	if !unicode.IsLetter(ch) && ch != '_' {
		return false
	}
	if i == 0 {
		return true
	}
	prev := rune(line[i-1])
	return !unicode.IsLetter(prev) && !unicode.IsDigit(prev) && prev != '_'
}

// wordEnd returns the byte index past the end of the word starting at pos.
func wordEnd(line string, pos int) int {
	i := pos
	for i < len(line) {
		ch := rune(line[i])
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '_' {
			break
		}
		i++
	}
	return i
}

// ---------------------------------------------------------------------------
// Occupied-range helpers
// ---------------------------------------------------------------------------

// markOccupied marks byte positions as claimed by existing regions.
func markOccupied(occupied []bool, regions []HighlightRegion) {
	for _, r := range regions {
		for i := r.StartCol; i < r.EndCol && i < len(occupied); i++ {
			occupied[i] = true
		}
	}
}

// anyOccupied returns true if any byte in [start, end) is already occupied.
func anyOccupied(occupied []bool, start, end int) bool {
	for i := start; i < end && i < len(occupied); i++ {
		if occupied[i] {
			return true
		}
	}
	return false
}

// shiftRegions offsets all region columns by delta.
func shiftRegions(regions []HighlightRegion, delta int) []HighlightRegion {
	shifted := make([]HighlightRegion, len(regions))
	for i, r := range regions {
		shifted[i] = HighlightRegion{
			StartCol: r.StartCol + delta,
			EndCol:   r.EndCol + delta,
			Category: r.Category,
		}
	}
	return shifted
}

// sortRegionsByStart sorts regions by StartCol using insertion sort, which is
// efficient for the small, nearly-sorted slices produced by the scanner.
func sortRegionsByStart(regions []HighlightRegion) {
	for i := 1; i < len(regions); i++ {
		key := regions[i]
		j := i - 1
		for j >= 0 && regions[j].StartCol > key.StartCol {
			regions[j+1] = regions[j]
			j--
		}
		regions[j+1] = key
	}
}

// ---------------------------------------------------------------------------
// Language keyword tables
// ---------------------------------------------------------------------------

// languageKeywords maps language names to their keyword lists.
var languageKeywords = map[string][]string{
	"go": {
		"break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if",
		"import", "interface", "map", "package", "range", "return",
		"select", "struct", "switch", "type", "var",
	},
	"python": {
		"and", "as", "assert", "async", "await", "break", "class",
		"continue", "def", "del", "elif", "else", "except", "finally",
		"for", "from", "global", "if", "import", "in", "is", "lambda",
		"nonlocal", "not", "or", "pass", "raise", "return", "try",
		"while", "with", "yield",
	},
	"javascript": {
		"async", "await", "break", "case", "catch", "class", "const",
		"continue", "debugger", "default", "delete", "do", "else",
		"export", "extends", "finally", "for", "function", "if",
		"import", "in", "instanceof", "let", "new", "of", "return",
		"super", "switch", "this", "throw", "try", "typeof", "var",
		"void", "while", "with", "yield",
	},
	"typescript": {
		"abstract", "as", "async", "await", "break", "case", "catch",
		"class", "const", "continue", "debugger", "declare", "default",
		"delete", "do", "else", "enum", "export", "extends", "finally",
		"for", "from", "function", "if", "implements", "import", "in",
		"instanceof", "interface", "is", "keyof", "let", "module",
		"namespace", "new", "of", "override", "readonly", "return",
		"super", "switch", "this", "throw", "try", "type", "typeof",
		"var", "void", "while", "with", "yield",
	},
	"rust": {
		"as", "async", "await", "break", "const", "continue", "crate",
		"dyn", "else", "enum", "extern", "fn", "for", "if", "impl",
		"in", "let", "loop", "match", "mod", "move", "mut", "pub",
		"ref", "return", "self", "static", "struct", "super", "trait",
		"type", "unsafe", "use", "where", "while",
	},
	"java": {
		"abstract", "assert", "boolean", "break", "byte", "case",
		"catch", "char", "class", "const", "continue", "default", "do",
		"double", "else", "enum", "extends", "final", "finally",
		"float", "for", "goto", "if", "implements", "import",
		"instanceof", "int", "interface", "long", "native", "new",
		"package", "private", "protected", "public", "return", "short",
		"static", "strictfp", "super", "switch", "synchronized", "this",
		"throw", "throws", "transient", "try", "var", "void",
		"volatile", "while",
	},
	"c": {
		"auto", "break", "case", "char", "const", "continue", "default",
		"do", "double", "else", "enum", "extern", "float", "for",
		"goto", "if", "inline", "int", "long", "register", "restrict",
		"return", "short", "signed", "sizeof", "static", "struct",
		"switch", "typedef", "union", "unsigned", "void", "volatile",
		"while",
	},
	"cpp": {
		"alignas", "alignof", "and", "auto", "bool", "break", "case",
		"catch", "char", "class", "const", "constexpr", "continue",
		"decltype", "default", "delete", "do", "double", "dynamic_cast",
		"else", "enum", "explicit", "export", "extern", "float", "for",
		"friend", "goto", "if", "inline", "int", "long", "mutable",
		"namespace", "new", "noexcept", "nullptr", "operator",
		"override", "private", "protected", "public", "register",
		"return", "short", "signed", "sizeof", "static", "static_cast",
		"struct", "switch", "template", "this", "throw", "try",
		"typedef", "typeid", "typename", "union", "unsigned", "using",
		"virtual", "void", "volatile", "while",
	},
	"ruby": {
		"alias", "and", "begin", "break", "case", "class", "def",
		"defined", "do", "else", "elsif", "end", "ensure", "for",
		"if", "in", "module", "next", "nil", "not", "or", "redo",
		"rescue", "retry", "return", "self", "super", "then", "unless",
		"until", "when", "while", "yield",
	},
}

// keywordsForLanguage returns the keyword list for the given language, falling
// back to an empty slice for unrecognized languages.
func keywordsForLanguage(lang string) []string {
	if kw, ok := languageKeywords[lang]; ok {
		return kw
	}
	return nil
}
