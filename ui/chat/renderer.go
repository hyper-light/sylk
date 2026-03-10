package chat

import (
	"fmt"
	"strings"
	"unicode/utf8"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// thinkingSummaryGlyph is the bullet used for the collapsed thinking line.
const thinkingSummaryGlyph = "◉"

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
// The optional cache stores pre-rendered code blocks keyed by (lang, content, width).
func RenderEntry(entry *ChatEntry, width int, th *theme.Theme, cache *codeBlockCache) ([]string, []CodeRegion) {
	if width <= 0 {
		return nil, nil
	}

	badge := renderBadge(entry, th)
	timestamp := entry.Timestamp.Format(badgeTimestampFormat)
	header := badge + " " + lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(timestamp)

	bodyStyle := messageStyle(entry.Source, th)

	// Phase 1: Thinking (streaming, no content yet).
	// Capped at: header(1) + spinner(1) + status(≤thinkingStatusMaxLines) + spacer(1).
	if entry.Streaming && entry.Content == "" && entry.ThinkingText != "" {
		color := th.Palette.Info
		if entry.ThinkingColor != "" {
			color = lipgloss.Color(entry.ThinkingColor)
		}
		animatedStyle := lipgloss.NewStyle().Foreground(color).Italic(true)
		lines := make([]string, 0, 2+thinkingStatusMaxLines+1)
		lines = append(lines, header)
		lines = append(lines, animatedStyle.Render(truncateToWidth(normalizeThinkingLine(entry.ThinkingText), width)))
		if status := strings.TrimSpace(entry.ThinkingStatus); status != "" {
			mdLines, _ := renderMarkdownContent(status, width, animatedStyle, th, nil)
			lines = append(lines, capLines(mdLines, thinkingStatusMaxLines, width, animatedStyle)...)
		}
		lines = append(lines, "")
		return lines, nil
	}

	// Phase 1b: Steering pending (holographic shimmer until acknowledged).
	// Content is present but renders with animated italic + gradient color.
	if entry.SteeringPending && entry.Content != "" {
		color := th.Palette.Warning
		if entry.ThinkingColor != "" {
			color = lipgloss.Color(entry.ThinkingColor)
		}
		animatedStyle := lipgloss.NewStyle().Foreground(color).Italic(true)
		wrapped := wrapLine(entry.Content, width, animatedStyle)
		lines := make([]string, 0, 1+len(wrapped)+1)
		lines = append(lines, header)
		lines = append(lines, wrapped...)
		lines = append(lines, "")
		return lines, nil
	}

	// Phase 2: Collapsed summary (content arrived after thinking).
	var summaryLines []string
	if entry.ThinkingElapsed > 0 {
		summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		summaryText := fmt.Sprintf("%s thought for %.1fs", thinkingSummaryGlyph, entry.ThinkingElapsed.Seconds())
		summaryLines = wrapLine(summaryText, width, summaryStyle)
	}

	// Phase 2b: Inline tool call visualization.
	toolCallLines, toolCallRegions := renderToolCalls(entry.ToolCalls, width, th)

	contentLines, codeRegions := renderContent(entry.Content, width, bodyStyle, th, cache)

	// Pre-allocate: 1 header + summary + tool calls + content lines + 1 trailing spacer.
	lines := make([]string, 0, 2+len(summaryLines)+len(toolCallLines)+len(contentLines))
	lines = append(lines, header)
	lines = append(lines, summaryLines...)
	lines = append(lines, toolCallLines...)
	lines = append(lines, contentLines...)
	lines = append(lines, "")

	// Offset code region indices to account for the header + summary + tool call lines.
	headerOffset := headerLines + len(summaryLines) + len(toolCallLines)
	for i := range codeRegions {
		codeRegions[i].Start += headerOffset
		codeRegions[i].End += headerOffset
	}

	// Offset tool call region indices to account for header + summary lines.
	tcOffset := headerLines + len(summaryLines)
	for i := range toolCallRegions {
		toolCallRegions[i].Start += tcOffset
		toolCallRegions[i].End += tcOffset
	}
	entry.ToolCallRegions = toolCallRegions

	return lines, codeRegions
}

// thinkingStatusMaxLines is the maximum number of wrapped lines the
// thinking status text may occupy. Enough for a sentence or two without
// filling the viewport.
// Derived from: 2 lines ≈ 1-2 sentences at typical terminal widths.
const thinkingStatusMaxLines = 2

// capLines returns at most maxLines from the given slice. If truncated,
// an ellipsis indicator is appended to the last retained line.
func capLines(lines []string, maxLines, _ int, style lipgloss.Style) []string {
	if len(lines) <= maxLines {
		return lines
	}
	capped := make([]string, maxLines)
	copy(capped, lines[:maxLines])
	capped[maxLines-1] += style.Render("…")
	return capped
}

// truncateToWidth truncates plain text to fit within width visible columns,
// appending "…" if truncated. For unstyled text only (no ANSI sequences).
func truncateToWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	// Reserve 1 column for the ellipsis.
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func normalizeThinkingLine(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "Thinking..."
	}
	return text
}

// renderBadge produces the styled icon + label string for the entry header.
func renderBadge(entry *ChatEntry, th *theme.Theme) string {
	icon := sourceIcon(entry.Source)
	label := badgeLabel(entry)
	style := badgeStyle(entry, th)
	return style.Render(icon + " " + label)
}

// badgeLabel returns the human-readable name for the badge.
// For pipeline agents, it renders a task-scoped label like
// "Auth Checkout: Inspector" while keeping the semantic agent type available
// separately for styling.
func badgeLabel(entry *ChatEntry) string {
	if entry.Source == SourceAgent {
		if label, ok := pipelineBadgeLabel(entry); ok {
			return label
		}
		if entry.AgentType != "" {
			return entry.AgentType
		}
	}
	return sourceLabel(entry.Source)
}

// badgeStyle selects the lipgloss style for the badge based on source.
func badgeStyle(entry *ChatEntry, th *theme.Theme) lipgloss.Style {
	if entry.Source == SourceAgent {
		return th.AgentBadge(badgeAgentType(entry))
	}
	return messageStyle(entry.Source, th)
}

func badgeAgentType(entry *ChatEntry) string {
	raw := strings.TrimSpace(entry.AgentType)
	if raw == "" {
		raw = strings.TrimSpace(entry.AgentID)
	}
	if _, agentPart, ok := splitPipelineBadgeIdentity(raw); ok {
		raw = agentPart
	}
	return strings.TrimSpace(raw)
}

func pipelineBadgeLabel(entry *ChatEntry) (string, bool) {
	taskLabel := humanizePipelineTaskLabel(firstNonEmpty(
		strings.TrimSpace(entry.TaskName),
		strings.TrimSpace(entry.TaskSlug),
		strings.TrimSpace(entry.TaskID),
	))
	if taskLabel != "" {
		agentLabel := humanizePipelineAgentLabel(badgeAgentType(entry))
		if agentLabel == "" {
			return taskLabel, true
		}
		return taskLabel + ": " + agentLabel, true
	}

	raw := strings.TrimSpace(entry.AgentID)
	if raw == "" {
		raw = strings.TrimSpace(entry.AgentType)
	}
	if raw == "" {
		return "", false
	}

	taskPart, agentPart, ok := splitPipelineBadgeIdentity(raw)
	if !ok {
		taskPart = ""
		agentPart = raw
	}

	agentLabel := humanizePipelineAgentLabel(agentPart)
	if taskPart == "" {
		if agentLabel != "" && agentLabel != agentPart {
			return agentLabel, true
		}
		return "", false
	}

	parsedTaskLabel := humanizePipelineTaskLabel(taskPart)
	if parsedTaskLabel == "" {
		return "", false
	}
	if agentLabel == "" {
		agentLabel = humanizePipelineAgentLabel(badgeAgentType(entry))
	}
	if agentLabel == "" {
		return parsedTaskLabel, true
	}
	return parsedTaskLabel + ": " + agentLabel, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func splitPipelineBadgeIdentity(raw string) (taskPart, agentPart string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx < len(raw)-1 {
		return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+1:]), true
	}
	if idx := strings.LastIndex(raw, "__"); idx >= 0 && idx+2 < len(raw) {
		return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+2:]), true
	}
	return "", raw, false
}

func humanizePipelineTaskLabel(task string) string {
	task = strings.TrimSpace(task)
	task = strings.TrimPrefix(task, "task_")
	task = strings.ReplaceAll(task, "-", " ")
	task = strings.ReplaceAll(task, "_", " ")
	return titleWords(task)
}

func humanizePipelineAgentLabel(agent string) string {
	agent = strings.TrimSpace(agent)
	switch agent {
	case "inspector-pipeline":
		return "Inspector"
	case "tester-pipeline":
		return "Tester"
	case "engineer":
		return "Engineer"
	case "designer":
		return "Designer"
	}
	if agent == "" {
		return ""
	}
	return titleWords(strings.ReplaceAll(agent, "-", " "))
}

func titleWords(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	for i, part := range parts {
		parts[i] = titleWord(part)
	}
	return strings.Join(parts, " ")
}

func titleWord(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return ""
	}
	return strings.ToUpper(string(r)) + strings.ToLower(value[size:])
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

// renderContent parses raw markdown and renders it to styled terminal lines
// with syntax-highlighted code blocks. Returns the rendered lines and code
// block regions (indices relative to the returned slice).
func renderContent(raw string, width int, style lipgloss.Style, th *theme.Theme, cache *codeBlockCache) ([]string, []CodeRegion) {
	if width <= 0 {
		return nil, nil
	}
	return renderMarkdownContent(raw, width, style, th, cache)
}

// gutterSep is the separator between line numbers and code content.
const gutterSep = " │ "

// gutterSepWidth is the visible column width of gutterSep.
// Derived from: space(1) + vertical bar(1) + space(1) = 3.
const gutterSepWidth = 3

// renderCodeBlock syntax-highlights buffered code lines with line numbers.
// When cache is non-nil, the result is looked up / stored by (lang, content, width).
func renderCodeBlock(lines []string, lang string, width int, th *theme.Theme, cache *codeBlockCache) []string {
	content := strings.Join(lines, "\n")

	if cache != nil {
		key := codeBlockKey{lang: lang, content: content, width: width}
		if cached := cache.Get(key); cached != nil {
			return cached
		}
		result := renderCodeBlockUncached(lines, lang, content, width, th)
		cache.Put(key, result)
		return result
	}
	return renderCodeBlockUncached(lines, lang, content, width, th)
}

// renderCodeBlockUncached performs syntax highlighting and line-number formatting
// without cache interaction. content must equal strings.Join(lines, "\n").
func renderCodeBlockUncached(lines []string, lang, content string, width int, th *theme.Theme) []string {
	hl := codepkg.NewHighlighter(th)
	defer hl.Close()
	allRegions := hl.HighlightContent(content, lang)

	digits := digitCount(len(lines))
	gutterWidth := digits + gutterSepWidth

	numStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	sepStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)

	codeWidth := max(width-gutterWidth, 1)
	contGutter := strings.Repeat(" ", gutterWidth)
	result := make([]string, 0, len(lines))
	for i, line := range lines {
		var regions []codepkg.HighlightRegion
		if i < len(allRegions) {
			regions = allRegions[i]
		}

		num := numStyle.Render(fmt.Sprintf("%*d", digits, i+1))
		sep := sepStyle.Render(gutterSep)
		highlighted := hl.HighlightLine(line, i, regions)

		visWidth := lipgloss.Width(highlighted)
		if visWidth <= codeWidth {
			if visWidth < codeWidth {
				highlighted += strings.Repeat(" ", codeWidth-visWidth)
			}
			result = append(result, num+sep+highlighted)
		} else {
			// Wrap long lines: split highlighted text at codeWidth
			// boundaries, preserving ANSI styles across segments.
			wrapped := wrapStyledCode(highlighted, codeWidth)
			for j, seg := range wrapped {
				if j == 0 {
					result = append(result, num+sep+seg)
				} else {
					result = append(result, contGutter+seg)
				}
			}
		}
	}
	return result
}

// wrapStyledCode splits a styled string into segments of maxWidth visible
// columns, preserving ANSI escape sequences across segment boundaries.
// Each segment is reset-terminated; subsequent segments replay the active
// style state from the prior segment's end.
func wrapStyledCode(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{""}
	}

	var segments []string
	var buf strings.Builder
	var activeStyle strings.Builder // accumulated ANSI sequences
	vis := 0
	i := 0

	for i < len(s) {
		if s[i] == '\x1b' {
			// Consume the entire ANSI escape sequence.
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			seq := s[i:j]
			buf.WriteString(seq)
			// Track style: reset clears, other SGR sequences accumulate.
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				activeStyle.Reset()
			} else {
				activeStyle.WriteString(seq)
			}
			i = j
			continue
		}

		// Visible character — if adding it exceeds maxWidth, break.
		if vis >= maxWidth {
			buf.WriteString("\x1b[0m")
			seg := buf.String()
			padWidth := maxWidth - vis
			if padWidth > 0 {
				seg += strings.Repeat(" ", padWidth)
			}
			segments = append(segments, seg)
			buf.Reset()
			// Replay active style into the new segment.
			buf.WriteString(activeStyle.String())
			vis = 0
		}

		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		vis++
		i += size
	}

	// Flush remaining content.
	if buf.Len() > 0 || vis > 0 {
		buf.WriteString("\x1b[0m")
		seg := buf.String()
		if vis < maxWidth {
			// Pad to full width so the code background fills the line.
			seg = buf.String()[:buf.Len()-len("\x1b[0m")] + strings.Repeat(" ", maxWidth-vis) + "\x1b[0m"
		}
		segments = append(segments, seg)
	}

	if len(segments) == 0 {
		return []string{strings.Repeat(" ", maxWidth)}
	}
	return segments
}

// truncateStyledCode truncates a styled string to fit within maxWidth visible
// columns, preserving ANSI escape sequences and appending a reset.
func truncateStyledCode(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var buf strings.Builder
	vis := 0
	i := 0
	for i < len(s) && vis < maxWidth {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		vis++
		i += size
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// isCSITerminator reports whether b is a CSI sequence final byte (0x40–0x7E).
func isCSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7E
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
