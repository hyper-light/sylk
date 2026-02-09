package knowledge

import (
	"fmt"
	"math"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// ContentType enum
// ---------------------------------------------------------------------------

// ContentType identifies the rendering strategy for a document.
type ContentType int

const (
	ContentPlain    ContentType = iota // Plain text
	ContentMarkdown                    // Markdown with word wrap
	ContentCode                        // Code with line numbers
	ContentConfig                      // YAML/JSON/TOML with keyword highlighting
)

// contentTypeNames maps ContentType values to human-readable labels.
var contentTypeNames = map[ContentType]string{
	ContentPlain:    "plain",
	ContentMarkdown: "markdown",
	ContentCode:     "code",
	ContentConfig:   "config",
}

// String returns the human-readable name of a ContentType.
func (ct ContentType) String() string {
	if name, ok := contentTypeNames[ct]; ok {
		return name
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Extension to ContentType detection (table-driven)
// ---------------------------------------------------------------------------

// extContentTypeTable maps file extensions to content types.
var extContentTypeTable = map[string]ContentType{
	// Markdown
	".md":       ContentMarkdown,
	".markdown": ContentMarkdown,
	// Code
	".go":   ContentCode,
	".py":   ContentCode,
	".js":   ContentCode,
	".ts":   ContentCode,
	".tsx":  ContentCode,
	".jsx":  ContentCode,
	".rs":   ContentCode,
	".rb":   ContentCode,
	".java": ContentCode,
	".c":    ContentCode,
	".cpp":  ContentCode,
	".h":    ContentCode,
	".hpp":  ContentCode,
	".sh":   ContentCode,
	".bash": ContentCode,
	".sql":  ContentCode,
	".lua":  ContentCode,
	// Config
	".yaml": ContentConfig,
	".yml":  ContentConfig,
	".json": ContentConfig,
	".toml": ContentConfig,
	".ini":  ContentConfig,
	".env":  ContentConfig,
	// Plain
	".txt": ContentPlain,
	".log": ContentPlain,
}

// DetectContentType determines the ContentType from a file path by examining
// its extension. Falls back to ContentPlain for unrecognized extensions.
func DetectContentType(filePath string) ContentType {
	for ext, ct := range extContentTypeTable {
		if strings.HasSuffix(filePath, ext) {
			return ct
		}
	}
	return ContentPlain
}

// ---------------------------------------------------------------------------
// Config keyword highlighting (table-driven)
// ---------------------------------------------------------------------------

// configKeywords lists keywords to highlight in config files.
// Derived from common YAML/JSON/TOML structural keywords.
var configKeywords = []string{
	"true", "false", "null", "nil", "yes", "no",
	"on", "off", "enabled", "disabled",
}

// ---------------------------------------------------------------------------
// DocumentViewer
// ---------------------------------------------------------------------------

// gutterPadding is the number of spaces between line number and content.
const gutterPadding = 1

// DocumentViewer displays document content with virtual scroll and
// content-type-aware rendering.
type DocumentViewer struct {
	content      string
	lines        []string
	filePath     string
	contentType  ContentType
	scrollOffset int
	cursorLine   int
	width        int
	height       int
	theme        *theme.Theme
}

// NewDocumentViewer creates a DocumentViewer with the given theme.
func NewDocumentViewer(th *theme.Theme) *DocumentViewer {
	return &DocumentViewer{
		theme: th,
	}
}

// SetContent loads content into the viewer, auto-detecting content type
// from the file path.
func (dv *DocumentViewer) SetContent(content, filePath string) {
	dv.content = content
	dv.filePath = filePath
	dv.contentType = DetectContentType(filePath)
	dv.lines = strings.Split(content, "\n")
	dv.scrollOffset = 0
	dv.cursorLine = 0
}

// SetContentWithType loads content with an explicit content type.
func (dv *DocumentViewer) SetContentWithType(content, filePath string, ct ContentType) {
	dv.content = content
	dv.filePath = filePath
	dv.contentType = ct
	dv.lines = strings.Split(content, "\n")
	dv.scrollOffset = 0
	dv.cursorLine = 0
}

// SetSize updates the viewer's available dimensions.
func (dv *DocumentViewer) SetSize(width, height int) {
	dv.width = max(width, 1)
	dv.height = max(height, 1)
	dv.clampScroll()
}

// ScrollDown moves the cursor down by one line.
func (dv *DocumentViewer) ScrollDown() {
	if dv.cursorLine < len(dv.lines)-1 {
		dv.cursorLine++
		dv.scrollIntoView()
	}
}

// ScrollUp moves the cursor up by one line.
func (dv *DocumentViewer) ScrollUp() {
	if dv.cursorLine > 0 {
		dv.cursorLine--
		dv.scrollIntoView()
	}
}

// PageDown scrolls down by one page.
func (dv *DocumentViewer) PageDown() {
	dv.cursorLine = min(dv.cursorLine+dv.height, len(dv.lines)-1)
	dv.scrollIntoView()
}

// PageUp scrolls up by one page.
func (dv *DocumentViewer) PageUp() {
	dv.cursorLine = max(dv.cursorLine-dv.height, 0)
	dv.scrollIntoView()
}

// ToTop scrolls to the first line.
func (dv *DocumentViewer) ToTop() {
	dv.cursorLine = 0
	dv.scrollOffset = 0
}

// ToBottom scrolls to the last line.
func (dv *DocumentViewer) ToBottom() {
	dv.cursorLine = max(len(dv.lines)-1, 0)
	dv.scrollOffset = max(len(dv.lines)-dv.height, 0)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// View renders the visible portion of the document.
func (dv *DocumentViewer) View() string {
	if len(dv.lines) == 0 {
		return ""
	}

	// Dispatch to content-type-specific renderer.
	renderer, ok := contentRenderers[dv.contentType]
	if !ok {
		renderer = renderPlainLine
	}

	lineCount := len(dv.lines)
	visEnd := min(dv.scrollOffset+dv.height, lineCount)
	visible := dv.lines[dv.scrollOffset:visEnd]

	gw := dv.gutterWidth()
	contentW := dv.contentWidth(gw)

	var b strings.Builder
	b.Grow((gw + contentW + 2) * len(visible))

	for i, line := range visible {
		if i > 0 {
			b.WriteByte('\n')
		}
		absLine := dv.scrollOffset + i
		b.WriteString(dv.renderLine(line, absLine, gw, contentW, renderer))
	}

	return b.String()
}

// contentRenderer is a function type for rendering a single line of content.
type contentRenderer func(line string, lineIdx int, contentWidth int, th *theme.Theme) string

// contentRenderers is the table-driven dispatch for content type rendering.
var contentRenderers = map[ContentType]contentRenderer{
	ContentPlain:    renderPlainLine,
	ContentMarkdown: renderMarkdownLine,
	ContentCode:     renderCodeLine,
	ContentConfig:   renderConfigLine,
}

func (dv *DocumentViewer) renderLine(line string, absLine, gutterW, contentW int, renderer contentRenderer) string {
	var b strings.Builder

	// Gutter (line number) for code; empty indent for other types.
	if dv.contentType == ContentCode {
		gutterStyle := lipgloss.NewStyle().Foreground(dv.theme.Palette.Muted)
		numStr := fmt.Sprintf("%*d", gutterW-gutterPadding, absLine+1)
		b.WriteString(gutterStyle.Render(numStr))
		b.WriteString(strings.Repeat(" ", gutterPadding))
	}

	rendered := renderer(line, absLine, contentW, dv.theme)
	b.WriteString(rendered)
	return b.String()
}

// ---------------------------------------------------------------------------
// Content type renderers
// ---------------------------------------------------------------------------

// renderPlainLine renders a line as plain text.
func renderPlainLine(line string, _ int, contentWidth int, th *theme.Theme) string {
	style := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	return style.Render(wrapLine(line, contentWidth))
}

// renderMarkdownLine renders a line with basic markdown awareness:
// headings are bolded and colored, code blocks are dimmed.
func renderMarkdownLine(line string, _ int, contentWidth int, th *theme.Theme) string {
	trimmed := strings.TrimSpace(line)

	// Heading detection: lines starting with #.
	if strings.HasPrefix(trimmed, "#") {
		headingStyle := lipgloss.NewStyle().
			Foreground(th.Palette.Primary).
			Bold(true)
		return headingStyle.Render(wrapLine(line, contentWidth))
	}

	// Code fence detection.
	if strings.HasPrefix(trimmed, "```") {
		fenceStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
		return fenceStyle.Render(line)
	}

	// Emphasis: lines starting with > are block quotes.
	if strings.HasPrefix(trimmed, ">") {
		quoteStyle := lipgloss.NewStyle().
			Foreground(th.Palette.Muted).
			Italic(true)
		return quoteStyle.Render(wrapLine(line, contentWidth))
	}

	defaultStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	return defaultStyle.Render(wrapLine(line, contentWidth))
}

// renderCodeLine renders a line with default code styling.
func renderCodeLine(line string, _ int, _ int, th *theme.Theme) string {
	style := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	return style.Render(line)
}

// renderConfigLine renders a config line, highlighting known keywords.
func renderConfigLine(line string, _ int, contentWidth int, th *theme.Theme) string {
	defaultStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	keywordStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)
	commentStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)

	trimmed := strings.TrimSpace(line)

	// Comment lines (# or //).
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return commentStyle.Render(wrapLine(line, contentWidth))
	}

	// Highlight keywords within the line.
	result := line
	for _, kw := range configKeywords {
		if strings.Contains(strings.ToLower(result), kw) {
			result = highlightKeyword(result, kw, defaultStyle, keywordStyle)
		}
	}

	return defaultStyle.Render(wrapLine(result, contentWidth))
}

// highlightKeyword replaces occurrences of keyword in text with styled versions.
// Uses case-insensitive matching.
func highlightKeyword(text, keyword string, baseStyle, kwStyle lipgloss.Style) string {
	lower := strings.ToLower(text)
	lowerKW := strings.ToLower(keyword)
	idx := strings.Index(lower, lowerKW)
	if idx < 0 {
		return baseStyle.Render(text)
	}
	before := text[:idx]
	match := text[idx : idx+len(keyword)]
	after := text[idx+len(keyword):]
	return baseStyle.Render(before) + kwStyle.Render(match) + baseStyle.Render(after)
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// gutterWidth returns the column width needed for line numbers, derived from
// the total number of lines. Returns 0 for non-code content.
func (dv *DocumentViewer) gutterWidth() int {
	if dv.contentType != ContentCode {
		return 0
	}
	lineCount := len(dv.lines)
	if lineCount == 0 {
		return 1 + gutterPadding
	}
	digits := int(math.Log10(float64(lineCount))) + 1
	return digits + gutterPadding
}

// contentWidth returns the width available for content after the gutter.
func (dv *DocumentViewer) contentWidth(gutterW int) int {
	w := dv.width - gutterW
	if w < 1 {
		return 1
	}
	return w
}

// ---------------------------------------------------------------------------
// Scroll management
// ---------------------------------------------------------------------------

func (dv *DocumentViewer) scrollIntoView() {
	if dv.cursorLine < dv.scrollOffset {
		dv.scrollOffset = dv.cursorLine
	}
	if dv.cursorLine >= dv.scrollOffset+dv.height {
		dv.scrollOffset = dv.cursorLine - dv.height + 1
	}
	dv.clampScroll()
}

func (dv *DocumentViewer) clampScroll() {
	maxOffset := max(len(dv.lines)-dv.height, 0)
	dv.scrollOffset = min(dv.scrollOffset, maxOffset)
	dv.scrollOffset = max(dv.scrollOffset, 0)
}

// ---------------------------------------------------------------------------
// Text wrapping
// ---------------------------------------------------------------------------

// wrapLine soft-wraps a line to fit within maxWidth. For simplicity, this
// performs rune-count truncation (full word wrap would require layout context).
func wrapLine(line string, maxWidth int) string {
	if maxWidth <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= maxWidth {
		return line
	}
	return string(runes[:maxWidth])
}
