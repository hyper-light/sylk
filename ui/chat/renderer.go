package chat

import (
	"strings"

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

// RenderEntry renders a ChatEntry into a slice of display lines that
// fit within the given width. Lines are word-wrapped. The theme controls
// colors and styling. Code fence blocks (``` ... ```) are rendered with
// a subtle background.
func RenderEntry(entry *ChatEntry, width int, th *theme.Theme) []string {
	if width <= 0 {
		return nil
	}

	badge := renderBadge(entry, th)
	timestamp := entry.Timestamp.Format(badgeTimestampFormat)
	header := badge + " " + lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(timestamp)

	bodyStyle := messageStyle(entry.Source, th)
	contentLines := renderContent(entry.Content, width, bodyStyle, th)

	// Pre-allocate: 1 header + content lines.
	lines := make([]string, 0, 1+len(contentLines))
	lines = append(lines, header)
	lines = append(lines, contentLines...)
	return lines
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

// renderContent splits raw content into styled, word-wrapped lines.
// Code fences (``` blocks) are detected and rendered with a subtle background.
func renderContent(raw string, width int, style lipgloss.Style, th *theme.Theme) []string {
	if width <= 0 {
		return nil
	}

	codeStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Foreground).
		Background(th.Palette.Subtle)

	var result []string
	inCode := false

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			// Render the fence delimiter itself in code style.
			result = append(result, wrapLine(line, width, codeStyle)...)
			continue
		}
		active := style
		if inCode {
			active = codeStyle
		}
		result = append(result, wrapLine(line, width, active)...)
	}
	return result
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
