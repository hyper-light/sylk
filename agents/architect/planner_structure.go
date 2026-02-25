package architect

import (
	"fmt"
	"strings"
)

// continuationMode distinguishes JSON (structured output) from text (streaming
// markdown) continuation paths. The mode determines overlap strategy, prompt
// construction, and structural completeness detection.
type continuationMode int

const (
	continuationModeText continuationMode = iota
	continuationModeJSON
)

// structuralContext captures the structural state of partially-generated text.
// Used for building mode-specific continuation prompts and detecting early
// structural completeness (e.g., all JSON braces balanced).
type structuralContext struct {
	// JSON
	unclosedBraces   int
	unclosedBrackets int
	inString         bool
	hasStructure     bool // true if at least one {, }, [, or ] was seen

	// Markdown
	inCodeBlock      bool
	inList           bool
	lastHeadingLevel int

	// Common
	trailingFragment string
	mode             continuationMode
}

// analyzeStructure dispatches to mode-specific structural analysis.
func analyzeStructure(mode continuationMode, text string) structuralContext {
	if mode == continuationModeJSON {
		return analyzeJSONStructure(text)
	}
	return analyzeMarkdownStructure(text)
}

// --- JSON structural analysis ---

// jsonBraceDeltas maps JSON structural characters to their effect on the
// brace/bracket depth counters. Index 0 = braces, index 1 = brackets.
var jsonBraceDeltas = map[rune][2]int{
	'{': {1, 0},
	'}': {-1, 0},
	'[': {0, 1},
	']': {0, -1},
}

// analyzeJSONStructure scans text for unbalanced JSON structure (braces,
// brackets) and string state, handling escape sequences.
func analyzeJSONStructure(text string) structuralContext {
	sc := structuralContext{mode: continuationModeJSON}
	var escaped bool
	for _, ch := range text {
		sc, escaped = updateJSONState(sc, ch, escaped)
	}
	sc.trailingFragment = suffixBounded(text, 200)
	return sc
}

// updateJSONState advances the JSON parser state by one character.
func updateJSONState(sc structuralContext, ch rune, escaped bool) (structuralContext, bool) {
	if escaped {
		return sc, false
	}
	if sc.inString {
		return updateJSONStringState(sc, ch)
	}
	return updateJSONNonStringState(sc, ch), false
}

// updateJSONStringState handles characters inside a JSON string literal.
func updateJSONStringState(sc structuralContext, ch rune) (structuralContext, bool) {
	if ch == '\\' {
		return sc, true
	}
	if ch == '"' {
		sc.inString = false
	}
	return sc, false
}

// updateJSONNonStringState handles characters outside JSON string literals,
// tracking brace/bracket depth and string entry.
func updateJSONNonStringState(sc structuralContext, ch rune) structuralContext {
	if ch == '"' {
		sc.inString = true
		return sc
	}
	if delta, ok := jsonBraceDeltas[ch]; ok {
		sc.unclosedBraces += delta[0]
		sc.unclosedBrackets += delta[1]
		sc.hasStructure = true
	}
	return sc
}

// --- Markdown structural analysis ---

// analyzeMarkdownStructure scans text for open code fences, list context,
// and heading level.
func analyzeMarkdownStructure(text string) structuralContext {
	sc := structuralContext{mode: continuationModeText}
	for _, line := range strings.Split(text, "\n") {
		sc = updateMarkdownState(sc, line)
	}
	sc.trailingFragment = suffixBounded(text, 200)
	return sc
}

// updateMarkdownState advances markdown analysis by one line, tracking
// code fence toggles and delegating context analysis for non-fence lines.
func updateMarkdownState(sc structuralContext, line string) structuralContext {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		sc.inCodeBlock = !sc.inCodeBlock
		return sc
	}
	if sc.inCodeBlock {
		return sc
	}
	return updateMarkdownLineContext(sc, trimmed)
}

// updateMarkdownLineContext extracts heading and list context from a single
// non-code-block markdown line.
func updateMarkdownLineContext(sc structuralContext, line string) structuralContext {
	if headingLevel := markdownHeadingLevel(line); headingLevel > 0 {
		sc.lastHeadingLevel = headingLevel
		sc.inList = false
		return sc
	}
	if isMarkdownListItem(line) {
		sc.inList = true
	}
	return sc
}

// markdownHeadingLevel returns the heading level (1-6) if the line is a
// markdown heading, or 0 otherwise.
func markdownHeadingLevel(line string) int {
	trimmed := strings.TrimLeft(line, "#")
	level := len(line) - len(trimmed)
	if level == 0 || !strings.HasPrefix(trimmed, " ") {
		return 0
	}
	return level
}

// isMarkdownListItem returns true if the line starts with a markdown list marker.
func isMarkdownListItem(line string) bool {
	return strings.HasPrefix(line, "- ") ||
		strings.HasPrefix(line, "* ") ||
		strings.HasPrefix(line, "1.")
}

// --- Structural completeness ---

// isStructurallyComplete returns true if the accumulated text forms a
// structurally complete document in its mode. For JSON, all braces and
// brackets must be balanced, at least one was seen, and we're not inside
// a string. For markdown, the text must not have an open code fence and
// must end with a paragraph break (double newline).
func isStructurallyComplete(sc structuralContext) bool {
	if sc.mode == continuationModeJSON {
		return isJSONComplete(sc)
	}
	return isMarkdownComplete(sc)
}

// isJSONComplete returns true when JSON delimiters are balanced and at least
// one structural character was seen. Without hasStructure, plain text would
// falsely appear "complete."
func isJSONComplete(sc structuralContext) bool {
	if !sc.hasStructure || sc.inString {
		return false
	}
	return jsonDelimitersBalanced(sc)
}

// jsonDelimitersBalanced returns true when all braces and brackets are closed.
func jsonDelimitersBalanced(sc structuralContext) bool {
	return sc.unclosedBraces == 0 && sc.unclosedBrackets == 0
}

// isMarkdownComplete returns true when no code fence is open and the text
// ends with a paragraph break. Plain text mid-sentence is never "complete."
func isMarkdownComplete(sc structuralContext) bool {
	if sc.inCodeBlock {
		return false
	}
	return strings.HasSuffix(sc.trailingFragment, "\n\n")
}

// --- Continuation prompt construction ---

// buildContinuationPrompt generates a mode-specific prompt that tells the
// model where the text left off, including structural hints.
func buildContinuationPrompt(mode continuationMode, sc structuralContext) string {
	if mode == continuationModeJSON {
		return buildJSONContinuationPrompt(sc)
	}
	return buildMarkdownContinuationPrompt(sc)
}

// buildJSONContinuationPrompt creates a continuation prompt with JSON
// structural hints (unclosed delimiters, string state).
func buildJSONContinuationPrompt(sc structuralContext) string {
	var b strings.Builder
	b.WriteString("Continue the JSON output exactly where it left off.")
	appendCountHint(&b, sc.unclosedBraces, "unclosed braces")
	appendCountHint(&b, sc.unclosedBrackets, "unclosed brackets")
	appendBoolHint(&b, sc.inString, "Currently inside a string literal.")
	b.WriteString(" Do not repeat any previously generated text.")
	return b.String()
}

// buildMarkdownContinuationPrompt creates a continuation prompt with markdown
// structural hints (open code fences, list context).
func buildMarkdownContinuationPrompt(sc structuralContext) string {
	var b strings.Builder
	b.WriteString("Continue the response exactly where it left off.")
	appendBoolHint(&b, sc.inCodeBlock, "A code block (```) is currently open.")
	appendBoolHint(&b, sc.inList, "Currently inside a list.")
	b.WriteString(" Do not repeat any previously generated text.")
	return b.String()
}

// appendCountHint writes " N <label> remain." to b if count > 0.
func appendCountHint(b *strings.Builder, count int, label string) {
	if count > 0 {
		fmt.Fprintf(b, " %d %s remain.", count, label)
	}
}

// appendBoolHint writes " <hint>" to b if condition is true.
func appendBoolHint(b *strings.Builder, condition bool, hint string) {
	if condition {
		b.WriteString(" ")
		b.WriteString(hint)
	}
}
