package shared

import (
	"fmt"
	"strings"
	"unicode"
)

// ReportShapeGraceTurns reserves a small number of extra completion turns
// when the terminal response failed the report shape contract. One turn lets
// the model re-emit in the correct format; the second covers a single
// follow-up attempt. After the budget is exhausted, the loop accepts
// whatever the model produced rather than holding the user's turn hostage.
const ReportShapeGraceTurns = 2

// ExtendReportShapeGrace returns the bounded grace budget when the terminal
// response failed the report shape contract. Mirrors ExtendRequiredProtocolGrace.
func ExtendReportShapeGrace(current int) int {
	if current >= ReportShapeGraceTurns {
		return current
	}
	return ReportShapeGraceTurns
}

// AgentReportShapeProfile defines the structural contract an agent's terminal
// assistant text must satisfy. Different agents register different profiles —
// the tester's profile differs from the inspector's, etc. The profile is
// rule-based, not heuristic: every check is deterministic and produces a
// precise violation message the model can act on when re-prompted.
//
// This is the per-agent counterpart to ValidateGlobalReviewCompletion:
// a contract gate enforced at the tool-loop terminal exit point. The
// model's text is never rewritten -- only re-prompted when it fails to
// match the contract.
type AgentReportShapeProfile struct {
	// Name identifies the profile in error messages (e.g. "tester turn report").
	Name string
	// MinHeading is the minimum heading depth the report must start with.
	// 2 = "## ", 3 = "### ". The first non-blank line of the response must
	// start with this many '#' characters followed by a space.
	MinHeading int
	// MaxHeading is the maximum heading depth allowed at the start (so
	// MinHeading=2, MaxHeading=3 accepts both "## " and "### "). Set equal
	// to MinHeading to require an exact level.
	MaxHeading int
	// MaxProseParagraphWords is the largest unstructured paragraph permitted
	// anywhere in the response. A "paragraph" is a block of consecutive
	// non-blank lines whose first line does not begin with a markdown
	// structural marker (heading, list bullet, table pipe, blockquote,
	// code-fence, indent). Set to 0 to disable the check.
	MaxProseParagraphWords int
	// RequiredSections lists header text fragments that must each appear as
	// a heading at any depth in the response. Match is case-insensitive and
	// substring-based against the heading text (after stripping leading '#'
	// and whitespace). Empty slice disables the check.
	RequiredSections []string
}

// TesterTurnReportShapeProfile is the contract for the pipeline tester's
// terminal assistant text. Mirrors the visual density of the architect's
// plan output (architect/streaming.go formatPlanForChat) without forcing
// typed-data rendering — the LLM produces the markdown directly, the
// validator enforces structural minimums.
var TesterTurnReportShapeProfile = AgentReportShapeProfile{
	Name:                   "tester turn report",
	MinHeading:             2,
	MaxHeading:             3,
	MaxProseParagraphWords: 80,
	RequiredSections: []string{
		"summary",
		"findings",
		"next",
	},
}

// ValidateAgentReportShape enforces the structural contract on a terminal
// assistant text. Returns an error describing the specific violation when
// the text is non-empty and fails the profile; nil otherwise.
//
// The contract is checked in order: heading prefix → required sections →
// paragraph length. The first violation surfaces as the error message so
// the re-prompt is precisely actionable.
//
// An empty text is a no-op: terminal turns that ended via tool calls
// (handoff_next, validate_work, etc.) frequently carry no assistant prose
// because the tool result IS the response. Only non-empty text is policed.
func ValidateAgentReportShape(text string, profile AgentReportShapeProfile) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	if profile.MinHeading > 0 {
		if err := validateReportHeadingPrefix(trimmed, profile); err != nil {
			return err
		}
	}

	if len(profile.RequiredSections) > 0 {
		if err := validateReportRequiredSections(trimmed, profile); err != nil {
			return err
		}
	}

	if profile.MaxProseParagraphWords > 0 {
		if err := validateReportProseParagraphs(trimmed, profile); err != nil {
			return err
		}
	}

	return nil
}

func validateReportHeadingPrefix(text string, profile AgentReportShapeProfile) error {
	maxDepth := profile.MaxHeading
	if maxDepth < profile.MinHeading {
		maxDepth = profile.MinHeading
	}
	for _, raw := range strings.SplitN(text, "\n", 2) {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		depth := markdownHeadingDepth(line)
		if depth >= profile.MinHeading && depth <= maxDepth {
			return nil
		}
		break
	}
	if profile.MinHeading == profile.MaxHeading {
		return fmt.Errorf(
			"the %s must start with a `%s ` heading; the first non-blank line was not a heading at the required depth",
			profile.Name,
			strings.Repeat("#", profile.MinHeading),
		)
	}
	return fmt.Errorf(
		"the %s must start with a heading at depth %d-%d (`%s ` or `%s `); the first non-blank line was not a heading at the required depth",
		profile.Name,
		profile.MinHeading,
		maxDepth,
		strings.Repeat("#", profile.MinHeading),
		strings.Repeat("#", maxDepth),
	)
}

func validateReportRequiredSections(text string, profile AgentReportShapeProfile) error {
	headings := collectMarkdownHeadingTexts(text)
	missing := make([]string, 0, len(profile.RequiredSections))
	for _, want := range profile.RequiredSections {
		needle := strings.ToLower(strings.TrimSpace(want))
		if needle == "" {
			continue
		}
		if !headingMatches(headings, needle) {
			missing = append(missing, want)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"the %s is missing required section heading(s): %s",
		profile.Name,
		strings.Join(missing, ", "),
	)
}

func validateReportProseParagraphs(text string, profile AgentReportShapeProfile) error {
	max := profile.MaxProseParagraphWords
	if max <= 0 {
		return nil
	}
	inFence := false
	var paragraph strings.Builder
	flush := func() error {
		body := strings.TrimSpace(paragraph.String())
		paragraph.Reset()
		if body == "" {
			return nil
		}
		words := countWords(body)
		if words > max {
			snippet := body
			if len(snippet) > 80 {
				snippet = strings.TrimSpace(snippet[:80]) + "…"
			}
			return fmt.Errorf(
				"the %s contains an unstructured paragraph of %d words (limit %d): %q. Convert long prose into bullet/numbered lists, sub-headings, or tables so the report stays scannable.",
				profile.Name,
				words,
				max,
				snippet,
			)
		}
		return nil
	}
	for _, raw := range strings.Split(text, "\n") {
		line := raw
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			if err := flush(); err != nil {
				return err
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if trim == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if isMarkdownStructuralLine(line, trim) {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if paragraph.Len() > 0 {
			paragraph.WriteByte(' ')
		}
		paragraph.WriteString(trim)
	}
	return flush()
}

// markdownHeadingDepth returns the heading depth (1-6) for an ATX heading
// line ("# ", "## ", etc.) or 0 for any other line. Trims leading whitespace.
func markdownHeadingDepth(line string) int {
	line = strings.TrimLeft(line, " \t")
	depth := 0
	for depth < len(line) && line[depth] == '#' && depth < 6 {
		depth++
	}
	if depth == 0 {
		return 0
	}
	if depth >= len(line) || line[depth] != ' ' {
		return 0
	}
	return depth
}

// collectMarkdownHeadingTexts returns the header text (after '#'+space) for
// every ATX heading in the text, lowercased and trimmed for matching.
func collectMarkdownHeadingTexts(text string) []string {
	out := make([]string, 0, 8)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimLeft(raw, " \t")
		depth := markdownHeadingDepth(line)
		if depth == 0 {
			continue
		}
		body := strings.TrimSpace(line[depth+1:])
		body = strings.Trim(body, "*_`")
		out = append(out, strings.ToLower(body))
	}
	return out
}

// headingMatches reports whether any heading text contains the lowercased
// needle as a substring. Substring (not exact) match keeps the contract
// flexible — "summary" matches "### Summary" and "### Verification Summary".
func headingMatches(headings []string, needle string) bool {
	for _, h := range headings {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// isMarkdownStructuralLine reports whether the line carries a markdown
// structural marker (heading, list bullet, table pipe, blockquote, indent
// code, or horizontal rule). Used by the prose-paragraph check to decide
// whether the line breaks a paragraph or extends one.
func isMarkdownStructuralLine(raw, trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if markdownHeadingDepth(trimmed) > 0 {
		return true
	}
	switch trimmed[0] {
	case '-', '*', '+', '|', '>':
		// Bullets, table rows, blockquotes.
		if len(trimmed) == 1 {
			return true
		}
		// Bullets / blockquotes require space after marker; table rows
		// don't but the leading '|' is enough.
		next := trimmed[1]
		if trimmed[0] == '|' || next == ' ' || next == '\t' {
			return true
		}
		return false
	}
	if isOrderedListLine(trimmed) {
		return true
	}
	// Indent-style code (4 spaces or a tab) is structural.
	if strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") {
		return true
	}
	return false
}

func isOrderedListLine(trimmed string) bool {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed) {
		return false
	}
	if trimmed[i] != '.' && trimmed[i] != ')' {
		return false
	}
	if i+1 >= len(trimmed) {
		return false
	}
	return trimmed[i+1] == ' ' || trimmed[i+1] == '\t'
}

func countWords(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if inWord {
				count++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		count++
	}
	return count
}
