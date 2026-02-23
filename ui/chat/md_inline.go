package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// styledRun represents a single renderable inline fragment with pre-computed
// ANSI styling, visible width, and wrapping hints.
type styledRun struct {
	text     string // Raw visible text.
	rendered string // Pre-rendered ANSI string via lipgloss.
	width    int    // Visible column width.
	atomic   bool   // Never split across lines (code spans, links).
	trailing bool   // Breakable whitespace (dropped at line start).
}

// collectInlineRuns walks an inline AST node's children and produces a flat
// slice of styledRuns for word wrapping. The style parameter is the inherited
// style for plain text at this nesting level.
func collectInlineRuns(node ast.Node, source []byte, style lipgloss.Style, styles *chatMdStyles) []styledRun {
	var runs []styledRun
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		runs = append(runs, inlineNodeRuns(child, source, style, styles)...)
	}
	return runs
}

// inlineNodeRuns converts a single inline AST node into styledRuns.
func inlineNodeRuns(node ast.Node, source []byte, inherited lipgloss.Style, styles *chatMdStyles) []styledRun {
	switch n := node.(type) {
	case *ast.Text:
		return textRuns(n, source, inherited)
	case *ast.String:
		return splitTextIntoRuns(string(n.Value), inherited)
	case *ast.Emphasis:
		return emphasisRuns(n, source, inherited, styles)
	case *ast.CodeSpan:
		return codeSpanRuns(n, source, styles)
	case *ast.Link:
		return linkRuns(n, source, styles)
	case *ast.Image:
		return imageRuns(n, source, styles)
	case *ast.AutoLink:
		return autoLinkRuns(n, source, styles)
	case *east.Strikethrough:
		return strikethroughRuns(n, source, styles)
	case *east.TaskCheckBox:
		return taskCheckBoxRuns(n, styles)
	case *ast.RawHTML:
		return rawHTMLRuns(n, source, inherited)
	default:
		// Unknown inline — recurse children with inherited style.
		return collectInlineRuns(node, source, inherited, styles)
	}
}

// textRuns handles ast.Text nodes, splitting into word + space runs.
func textRuns(n *ast.Text, source []byte, style lipgloss.Style) []styledRun {
	seg := n.Segment
	raw := string(source[seg.Start:seg.Stop])
	runs := splitTextIntoRuns(raw, style)
	// SoftLineBreak → treat as a space.
	if n.SoftLineBreak() {
		runs = append(runs, styledRun{
			text:     " ",
			rendered: " ",
			width:    1,
			trailing: true,
		})
	}
	// HardLineBreak → forced newline marker.
	if n.HardLineBreak() {
		runs = append(runs, styledRun{
			text:     "\n",
			rendered: "\n",
			width:    0,
		})
	}
	return runs
}

// splitTextIntoRuns breaks text into word runs and trailing-space runs.
func splitTextIntoRuns(text string, style lipgloss.Style) []styledRun {
	if text == "" {
		return nil
	}
	var runs []styledRun
	i := 0
	for i < len(text) {
		// Consume whitespace as trailing runs.
		if text[i] == ' ' || text[i] == '\t' {
			j := i + 1
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			sp := text[i:j]
			runs = append(runs, styledRun{
				text:     sp,
				rendered: sp,
				width:    len(sp),
				trailing: true,
			})
			i = j
			continue
		}
		// Consume word characters.
		j := i + 1
		for j < len(text) && text[j] != ' ' && text[j] != '\t' {
			j++
		}
		word := text[i:j]
		runs = append(runs, styledRun{
			text:     word,
			rendered: style.Render(word),
			width:    lipgloss.Width(word),
		})
		i = j
	}
	return runs
}

// emphasisRuns handles *italic* and **bold** (and ***bold italic***).
func emphasisRuns(n *ast.Emphasis, source []byte, inherited lipgloss.Style, styles *chatMdStyles) []styledRun {
	var emStyle lipgloss.Style
	switch n.Level {
	case 1:
		emStyle = styles.italic
	case 2:
		emStyle = styles.bold
	default:
		emStyle = styles.boldItalic
	}
	// If parent was already italic/bold, combine.
	if inherited.GetBold() && n.Level == 1 {
		emStyle = styles.boldItalic
	}
	if inherited.GetItalic() && n.Level == 2 {
		emStyle = styles.boldItalic
	}
	return collectInlineRuns(n, source, emStyle, styles)
}

// codeSpanRuns renders inline code as a single atomic run with padding.
func codeSpanRuns(n *ast.CodeSpan, source []byte, styles *chatMdStyles) []styledRun {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			seg := t.Segment
			b.Write(source[seg.Start:seg.Stop])
		}
	}
	raw := b.String()
	padded := " " + raw + " "
	rendered := styles.code.Render(padded)
	return []styledRun{{
		text:     padded,
		rendered: rendered,
		width:    lipgloss.Width(rendered),
		atomic:   true,
	}}
}

// linkRuns renders [text](url) as atomic display-text-only.
func linkRuns(n *ast.Link, source []byte, styles *chatMdStyles) []styledRun {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		extractPlainText(child, source, &b)
	}
	text := b.String()
	if text == "" {
		text = string(n.Destination)
	}
	rendered := styles.link.Render(text)
	return []styledRun{{
		text:     text,
		rendered: rendered,
		width:    lipgloss.Width(rendered),
		atomic:   true,
	}}
}

// imageRuns renders ![alt](url) as "[image: alt]".
func imageRuns(n *ast.Image, source []byte, styles *chatMdStyles) []styledRun {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		extractPlainText(child, source, &b)
	}
	alt := b.String()
	if alt == "" {
		alt = "image"
	}
	display := "[image: " + alt + "]"
	rendered := styles.link.Render(display)
	return []styledRun{{
		text:     display,
		rendered: rendered,
		width:    lipgloss.Width(rendered),
		atomic:   true,
	}}
}

// autoLinkRuns renders auto-detected URLs/emails.
func autoLinkRuns(n *ast.AutoLink, source []byte, styles *chatMdStyles) []styledRun {
	label := string(n.Label(source))
	rendered := styles.link.Render(label)
	return []styledRun{{
		text:     label,
		rendered: rendered,
		width:    lipgloss.Width(rendered),
		atomic:   true,
	}}
}

// strikethroughRuns handles ~~strikethrough~~.
func strikethroughRuns(n *east.Strikethrough, source []byte, styles *chatMdStyles) []styledRun {
	return collectInlineRuns(n, source, styles.strike, styles)
}

// taskCheckBoxRuns renders [x] or [ ].
func taskCheckBoxRuns(n *east.TaskCheckBox, styles *chatMdStyles) []styledRun {
	glyph := "[ ] "
	if n.IsChecked {
		glyph = "[x] "
	}
	rendered := styles.bullet.Render(glyph)
	return []styledRun{{
		text:     glyph,
		rendered: rendered,
		width:    lipgloss.Width(rendered),
		atomic:   true,
	}}
}

// rawHTMLRuns renders inline HTML as plain text.
func rawHTMLRuns(n *ast.RawHTML, source []byte, style lipgloss.Style) []styledRun {
	var b strings.Builder
	for i := range n.Segments.Len() {
		seg := n.Segments.At(i)
		b.Write(source[seg.Start:seg.Stop])
	}
	return splitTextIntoRuns(b.String(), style)
}

// extractPlainText recursively extracts visible text from an inline AST node.
func extractPlainText(node ast.Node, source []byte, b *strings.Builder) {
	if t, ok := node.(*ast.Text); ok {
		seg := t.Segment
		b.Write(source[seg.Start:seg.Stop])
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		extractPlainText(child, source, b)
	}
}
