package search

import (
	"regexp"
	"strings"
)

// SubstituteFlag represents a single flag on the :s command.
type SubstituteFlag int

const (
	FlagGlobal      SubstituteFlag = iota // g - replace all matches on each line
	FlagIgnoreCase                        // i - case-insensitive matching
	FlagConfirm                           // c - prompt for each replacement
)

// flagEntry maps a flag character to its SubstituteFlag value.
type flagEntry struct {
	char rune
	flag SubstituteFlag
}

// flagTable is the table-driven registry of substitute flags.
var flagTable = []flagEntry{
	{char: 'g', flag: FlagGlobal},
	{char: 'i', flag: FlagIgnoreCase},
	{char: 'c', flag: FlagConfirm},
}

// lookupFlag returns the SubstituteFlag for a rune, if it is valid.
func lookupFlag(r rune) (SubstituteFlag, bool) {
	for _, entry := range flagTable {
		if entry.char == r {
			return entry.flag, true
		}
	}
	return 0, false
}

// SubstituteCmd represents a parsed :s/pattern/replacement/flags command.
type SubstituteCmd struct {
	Pattern     string
	Replacement string
	Flags       map[SubstituteFlag]bool
	compiled    *regexp.Regexp
}

// Parse parses a :s command string into a SubstituteCmd. The command may use
// any non-alphanumeric character as a delimiter (typically / or #).
// Expected format: s<delim>pattern<delim>replacement<delim>flags
func Parse(cmd string) (*SubstituteCmd, error) {
	input := strings.TrimPrefix(cmd, "s")
	input = strings.TrimPrefix(input, "%s")
	if len(input) == 0 {
		return nil, &ParseError{Msg: "empty substitute command"}
	}
	delim := rune(input[0])
	parts := splitOnDelimiter([]rune(input[1:]), delim)
	pattern, replacement, flagStr := extractParts(parts)
	flags := parseFlags(flagStr)
	compiled, err := compilePattern(pattern, flags)
	if err != nil {
		return nil, err
	}
	return &SubstituteCmd{
		Pattern:     pattern,
		Replacement: replacement,
		Flags:       flags,
		compiled:    compiled,
	}, nil
}

// ParseError describes a parsing failure for a substitute command.
type ParseError struct {
	Msg string
}

func (e *ParseError) Error() string { return "substitute: " + e.Msg }

// Execute applies the substitution to content runes within the line range
// [lineStart, lineEnd) (0-indexed). Returns the modified content and the
// number of replacements made.
func (sc *SubstituteCmd) Execute(content []rune, lineStart, lineEnd int) ([]rune, int) {
	lines := splitRuneLines(content)
	totalReplacements := 0
	effectiveEnd := min(lineEnd, len(lines))
	for i := lineStart; i < effectiveEnd; i++ {
		replaced, count := sc.substituteLine(string(lines[i]))
		lines[i] = []rune(replaced)
		totalReplacements += count
	}
	return joinRuneLines(lines), totalReplacements
}

// ConfirmPositions returns the positions of all matches within the line range
// that would be subject to the 'c' (confirm) flag. Each position is a
// MatchRange within the full content.
func (sc *SubstituteCmd) ConfirmPositions(content []rune, lineStart, lineEnd int) []MatchRange {
	lines := splitRuneLines(content)
	effectiveEnd := min(lineEnd, len(lines))
	var positions []MatchRange
	offset := 0
	for i := range effectiveEnd {
		lineStr := string(lines[i])
		if i >= lineStart {
			matches := sc.compiled.FindAllStringIndex(lineStr, -1)
			for _, loc := range matches {
				mStart := len([]rune(lineStr[:loc[0]]))
				mEnd := len([]rune(lineStr[:loc[1]]))
				positions = append(positions, MatchRange{
					Start: offset + mStart,
					End:   offset + mEnd,
				})
			}
		}
		offset += len(lines[i]) + 1 // +1 for the newline
	}
	return positions
}

// substituteLine applies the substitution to a single line.
func (sc *SubstituteCmd) substituteLine(line string) (string, int) {
	_, isGlobal := sc.Flags[FlagGlobal]
	if isGlobal {
		count := len(sc.compiled.FindAllString(line, -1))
		result := sc.compiled.ReplaceAllString(line, sc.Replacement)
		return result, count
	}
	// Replace only the first match.
	loc := sc.compiled.FindStringIndex(line)
	if loc == nil {
		return line, 0
	}
	result := line[:loc[0]] + sc.compiled.ReplaceAllString(line[loc[0]:loc[1]], sc.Replacement) + line[loc[1]:]
	return result, 1
}

// ---------------------------------------------------------------------------
// Parsing helpers
// ---------------------------------------------------------------------------

// splitOnDelimiter splits runes on the given delimiter, respecting backslash
// escapes. Returns at most 3 parts: [pattern, replacement, flags].
func splitOnDelimiter(runes []rune, delim rune) []string {
	maxParts := 3
	parts := make([]string, 0, maxParts)
	var current []rune
	escaped := false
	for _, r := range runes {
		if escaped {
			current = append(current, r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			current = append(current, r)
			continue
		}
		if r == delim && len(parts) < maxParts-1 {
			parts = append(parts, string(current))
			current = current[:0]
			continue
		}
		current = append(current, r)
	}
	parts = append(parts, string(current))
	return parts
}

// extractParts safely extracts pattern, replacement, and flags from split parts.
func extractParts(parts []string) (pattern, replacement, flags string) {
	if len(parts) > 0 {
		pattern = parts[0]
	}
	if len(parts) > 1 {
		replacement = parts[1]
	}
	if len(parts) > 2 {
		flags = parts[2]
	}
	return pattern, replacement, flags
}

// parseFlags parses a flag string into a set of SubstituteFlags.
func parseFlags(flagStr string) map[SubstituteFlag]bool {
	flags := make(map[SubstituteFlag]bool)
	for _, r := range flagStr {
		f, ok := lookupFlag(r)
		if ok {
			flags[f] = true
		}
	}
	return flags
}

// compilePattern compiles the regex pattern with optional case-insensitivity.
func compilePattern(pattern string, flags map[SubstituteFlag]bool) (*regexp.Regexp, error) {
	if _, ok := flags[FlagIgnoreCase]; ok {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

// ---------------------------------------------------------------------------
// Rune line helpers
// ---------------------------------------------------------------------------

// splitRuneLines splits rune content into lines by newline characters.
func splitRuneLines(content []rune) [][]rune {
	if len(content) == 0 {
		return nil
	}
	// Count newlines first to pre-allocate.
	nlCount := 0
	for _, r := range content {
		if r == '\n' {
			nlCount++
		}
	}
	lines := make([][]rune, 0, nlCount+1)
	start := 0
	for i, r := range content {
		if r == '\n' {
			lines = append(lines, content[start:i])
			start = i + 1
		}
	}
	// Trailing content after last newline.
	lines = append(lines, content[start:])
	return lines
}

// joinRuneLines reassembles rune lines into a single rune slice with newlines.
func joinRuneLines(lines [][]rune) []rune {
	totalLen := 0
	for _, line := range lines {
		totalLen += len(line) + 1 // +1 for newline
	}
	if totalLen > 0 {
		totalLen-- // no trailing newline
	}
	result := make([]rune, 0, totalLen)
	for i, line := range lines {
		result = append(result, line...)
		if i < len(lines)-1 {
			result = append(result, '\n')
		}
	}
	return result
}
