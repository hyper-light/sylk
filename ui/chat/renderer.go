package chat

import (
	"fmt"
	"strings"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// sourceIcon returns the icon glyph for a given ChatSource.
func sourceIcon(source ChatSource) string {
	icons := [...]string{
		SourceUser:   theme.IconUser,
		SourceAgent:  theme.IconAgent,
		SourceSystem: theme.IconSystem,
		SourceTool:   theme.IconTool,
		SourceError:  theme.IconError,
	}
	if int(source) < len(icons) {
		return icons[source]
	}
	return theme.IconSystem
}

// sourceLabel returns a display label for a ChatSource.
func sourceLabel(source ChatSource) string {
	labels := [...]string{
		SourceUser:   "you",
		SourceAgent:  "agent",
		SourceSystem: "system",
		SourceTool:   "tool",
		SourceError:  "error",
	}
	if int(source) < len(labels) {
		return labels[source]
	}
	return "system"
}

// badgeWidth is the fixed column width reserved for the agent badge
// (icon + space + label + space + timestamp).
const badgeTimestampFormat = "15:04:05"

// headerLines is the number of lines the entry header occupies.
// Derived from: badge + timestamp = 1 line.
const headerLines = 1

// RenderEntry renders a ChatEntry into a slice of display lines that
// fit within the given width. Lines are word-wrapped. The theme controls
// colors and styling. Code fence blocks (``` ... ```) are rendered with
// a subtle background. Returns the rendered lines and any code block regions
// (with line indices relative to the returned slice).
func RenderEntry(entry *ChatEntry, width int, th *theme.Theme) ([]string, []CodeRegion) {
	if width <= 0 {
		return nil, nil
	}

	badge := renderBadge(entry, th)
	timestamp := entry.Timestamp.Format(badgeTimestampFormat)
	header := badge + " " + lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(timestamp)

	bodyStyle := messageStyle(entry.Source, th)
	contentLines, codeRegions := renderContent(entry.Content, width, bodyStyle, th)

	// Pre-allocate: 1 header + content lines + 1 trailing spacer.
	lines := make([]string, 0, 2+len(contentLines))
	lines = append(lines, header)
	lines = append(lines, contentLines...)
	lines = append(lines, "")

	// Offset code region indices to account for the header line.
	for i := range codeRegions {
		codeRegions[i].Start += headerLines
		codeRegions[i].End += headerLines
	}
	return lines, codeRegions
}

// renderBadge produces the styled icon + label string for the entry header.
func renderBadge(entry *ChatEntry, th *theme.Theme) string {
	icon := sourceIcon(entry.Source)
	label := badgeLabel(entry)
	style := badgeStyle(entry, th)
	return style.Render(icon + " " + label)
}

// badgeLabel returns the human-readable name for the badge.
// For agent entries it uses the AgentType; otherwise the source label.
func badgeLabel(entry *ChatEntry) string {
	if entry.Source == SourceAgent && entry.AgentType != "" {
		return entry.AgentType
	}
	return sourceLabel(entry.Source)
}

// badgeStyle selects the lipgloss style for the badge based on source.
func badgeStyle(entry *ChatEntry, th *theme.Theme) lipgloss.Style {
	if entry.Source == SourceAgent {
		return th.AgentBadge(entry.AgentType)
	}
	return messageStyle(entry.Source, th)
}

// messageStyle returns the theme style for a given source.
func messageStyle(source ChatSource, th *theme.Theme) lipgloss.Style {
	styles := [...]lipgloss.Style{
		SourceUser:   th.UserMessage,
		SourceAgent:  th.AgentMessage,
		SourceSystem: th.SystemMessage,
		SourceTool:   th.ToolMessage,
		SourceError:  th.ErrorMessage,
	}
	if int(source) < len(styles) {
		return styles[source]
	}
	return th.SystemMessage
}

// langAliases maps common short fence tags to the canonical language names
// used by the syntax highlighter's keyword tables.
var langAliases = map[string]string{
	"js":     "javascript",
	"ts":     "typescript",
	"py":     "python",
	"rb":     "ruby",
	"c++":    "cpp",
	"rs":     "rust",
	"golang": "go",
}

// normalizeLang converts a fence language tag to the canonical form.
func normalizeLang(tag string) string {
	lang := strings.ToLower(strings.TrimSpace(tag))
	if alias, ok := langAliases[lang]; ok {
		return alias
	}
	return lang
}

// renderContent splits raw content into styled, word-wrapped lines.
// Code fences (``` blocks) are syntax-highlighted with line numbers; prose is word-wrapped.
// Returns the rendered lines and code block regions (indices relative to the returned slice).
func renderContent(raw string, width int, style lipgloss.Style, th *theme.Theme) ([]string, []CodeRegion) {
	if width <= 0 {
		return nil, nil
	}

	var result []string
	var regions []CodeRegion
	var codeBuffer []string
	var codeLang string
	inCode := false

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "```") {
			if !inCode {
				codeLang = normalizeLang(strings.TrimPrefix(line, "```"))
				inCode = true
			} else {
				codeStart := len(result)
				result = append(result, renderCodeBlock(codeBuffer, codeLang, width, th)...)
				regions = append(regions, CodeRegion{
					Start:   codeStart,
					End:     len(result),
					Content: strings.Join(codeBuffer, "\n"),
				})
				codeBuffer = nil
				codeLang = ""
				inCode = false
			}
			continue
		}
		if inCode {
			codeBuffer = append(codeBuffer, line)
		} else {
			result = append(result, wrapLine(line, width, style)...)
		}
	}

	// Flush unclosed fence as highlighted code.
	if inCode && len(codeBuffer) > 0 {
		codeStart := len(result)
		result = append(result, renderCodeBlock(codeBuffer, codeLang, width, th)...)
		regions = append(regions, CodeRegion{
			Start:   codeStart,
			End:     len(result),
			Content: strings.Join(codeBuffer, "\n"),
		})
	}

	return result, regions
}

// gutterSep is the separator between line numbers and code content.
const gutterSep = " │ "

// gutterSepWidth is the visible column width of gutterSep.
// Derived from: space(1) + vertical bar(1) + space(1) = 3.
const gutterSepWidth = 3

// renderCodeBlock syntax-highlights buffered code lines with line numbers.
func renderCodeBlock(lines []string, lang string, width int, th *theme.Theme) []string {
	hl := codepkg.NewHighlighter(th)
	content := strings.Join(lines, "\n")
	allRegions := hl.HighlightContent(content, lang)

	digits := digitCount(len(lines))
	gutterWidth := digits + gutterSepWidth

	numStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	sepStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		var regions []codepkg.HighlightRegion
		if i < len(allRegions) {
			regions = allRegions[i]
		}

		num := numStyle.Render(fmt.Sprintf("%*d", digits, i+1))
		sep := sepStyle.Render(gutterSep)
		highlighted := hl.HighlightLine(line, i, regions)

		// Truncate code if it exceeds available width after the gutter.
		codeWidth := max(width-gutterWidth, 1)
		if lipgloss.Width(highlighted) > codeWidth {
			highlighted = truncateCode(highlighted, codeWidth)
		}

		result = append(result, num+sep+highlighted)
	}
	return result
}

// truncateCode truncates a styled string to fit within maxWidth visible columns.
func truncateCode(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	for i := range runes {
		if lipgloss.Width(string(runes[:i])) > maxWidth {
			if i == 0 {
				return ""
			}
			return string(runes[:i-1])
		}
	}
	return s
}

// digitCount returns the number of decimal digits in n.
// Derived from: iterative division avoids float/log dependency.
func digitCount(n int) int {
	if n <= 0 {
		return 1
	}
	count := 0
	for n > 0 {
		count++
		n /= 10
	}
	return count
}

// wrapLine performs word wrapping on a single line, returning one or more
// styled output lines, each no wider than width.
func wrapLine(line string, width int, style lipgloss.Style) []string {
	if width <= 0 {
		return nil
	}

	// Fast path: line fits.
	if lipgloss.Width(line) <= width {
		return []string{style.Render(line)}
	}

	return wrapLong(line, width, style)
}

// wrapLong splits a line that exceeds width using word boundaries.
func wrapLong(line string, width int, style lipgloss.Style) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{style.Render("")}
	}

	var lines []string
	var current strings.Builder

	for _, word := range words {
		needed := lipgloss.Width(word)
		currentWidth := lipgloss.Width(current.String())

		if currentWidth > 0 && currentWidth+1+needed > width {
			lines = append(lines, style.Render(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		// If a single word exceeds width, force-break it.
		if needed > width {
			lines = append(lines, forceBreak(word, width, style, &current)...)
			continue
		}
		current.WriteString(word)
	}

	if current.Len() > 0 {
		lines = append(lines, style.Render(current.String()))
	}
	return lines
}

// forceBreak splits a word that is wider than the available width into
// multiple lines using character-level breaks, flushing any partial
// current buffer first.
func forceBreak(word string, width int, style lipgloss.Style, current *strings.Builder) []string {
	// Flush anything already accumulated.
	var lines []string
	if current.Len() > 0 {
		lines = append(lines, style.Render(current.String()))
		current.Reset()
	}

	runes := []rune(word)
	for len(runes) > 0 {
		take := min(len(runes), width)
		lines = append(lines, style.Render(string(runes[:take])))
		runes = runes[take:]
	}
	return lines
}
