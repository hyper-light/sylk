// Package completion implements insert-mode completion for the editor,
// providing Ctrl+N/Ctrl+P generic cycling and Ctrl+X sub-mode completion
// (file paths, line completion).
package completion

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// ---------------------------------------------------------------------------
// Completion kinds
// ---------------------------------------------------------------------------

// CompletionKind classifies the origin of a completion item.
type CompletionKind int

const (
	KindText   CompletionKind = iota // plain text / fallback
	KindFile                         // file path
	KindLine                         // whole-line completion
	KindBuffer                       // buffer word
	KindUser                         // user-registered source
)

// kindNameTable maps each CompletionKind to a short display label.
var kindNameTable = map[CompletionKind]string{
	KindText:   "Text",
	KindFile:   "File",
	KindLine:   "Line",
	KindBuffer: "Buf",
	KindUser:   "User",
}

// KindName returns the display name for a CompletionKind.
func (k CompletionKind) KindName() string {
	if name, ok := kindNameTable[k]; ok {
		return name
	}
	return "?"
}

// ---------------------------------------------------------------------------
// Completion modes
// ---------------------------------------------------------------------------

// CompletionMode identifies which sub-mode triggered completion.
type CompletionMode int

const (
	ModeGeneric CompletionMode = iota // Ctrl+N / Ctrl+P
	ModeFile                          // Ctrl+X Ctrl+F
	ModeLine                          // Ctrl+X Ctrl+L
)

// ---------------------------------------------------------------------------
// Completion item
// ---------------------------------------------------------------------------

// CompletionItem represents a single candidate in the completion popup.
type CompletionItem struct {
	Word  string         // the text to insert
	Kind  CompletionKind // origin category
	Menu  string         // short source label shown in popup
	Info  string         // extended description (tooltip)
	Score int            // ranking score (higher = better)
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

// maxCompletionItems caps the total number of candidates to prevent
// unbounded growth. Derived from the practical limit where scanning a
// popup menu ceases to be useful.
const maxCompletionItems = 200

// maxVisibleItems is the number of rows visible in the popup before
// virtual scrolling engages. Derived from typical terminal completion
// popup UX conventions.
const maxVisibleItems = 10

// Engine manages the lifecycle and rendering of the completion popup.
type Engine struct {
	mu       sync.RWMutex
	sources  *SourceRegistry
	active   bool
	items    []CompletionItem
	selected int
	prefix   string
	startCol int
	mode     CompletionMode
}

// NewEngine creates an Engine backed by the given source registry.
func NewEngine(sources *SourceRegistry) *Engine {
	return &Engine{sources: sources}
}

// Start activates completion in the given mode, gathering candidates from
// the source registry for the content around the cursor.
func (e *Engine) Start(mode CompletionMode, content []rune, cursorPos int, currentLine int) {
	prefix := extractPrefix(content, cursorPos)
	ctx := CompletionContext{
		Content:     content,
		CursorPos:   cursorPos,
		CurrentLine: currentLine,
		Prefix:      prefix,
		Mode:        mode,
	}

	items := e.sources.Gather(ctx, mode)
	if len(items) > maxCompletionItems {
		items = items[:maxCompletionItems]
	}

	e.mu.Lock()
	e.active = len(items) > 0
	e.items = items
	e.selected = 0
	e.prefix = prefix
	e.startCol = cursorPos - utf8.RuneCountInString(prefix)
	e.mode = mode
	e.mu.Unlock()
}

// Next selects the next completion item, wrapping around to the first.
func (e *Engine) Next() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.items) == 0 {
		return
	}
	e.selected = (e.selected + 1) % len(e.items)
}

// Prev selects the previous completion item, wrapping around to the last.
func (e *Engine) Prev() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.items) == 0 {
		return
	}
	e.selected = (e.selected - 1 + len(e.items)) % len(e.items)
}

// Accept returns the currently selected item and deactivates the popup.
// Returns nil when no completion is active.
func (e *Engine) Accept() *CompletionItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.active || len(e.items) == 0 {
		return nil
	}
	item := e.items[e.selected]
	e.deactivate()
	return &item
}

// Dismiss closes the completion popup without accepting any item.
func (e *Engine) Dismiss() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deactivate()
}

// Active reports whether the completion popup is currently shown.
func (e *Engine) Active() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.active
}

// Items returns the current completion candidates.
func (e *Engine) Items() []CompletionItem {
	e.mu.RLock()
	defer e.mu.RUnlock()
	dst := make([]CompletionItem, len(e.items))
	copy(dst, e.items)
	return dst
}

// Selected returns the index of the currently highlighted candidate.
func (e *Engine) Selected() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.selected
}

// StartCol returns the column at which the completed prefix begins.
func (e *Engine) StartCol() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.startCol
}

// Prefix returns the prefix used to filter candidates.
func (e *Engine) Prefix() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.prefix
}

// deactivate resets state. Caller must hold e.mu.
func (e *Engine) deactivate() {
	e.active = false
	e.items = nil
	e.selected = 0
	e.prefix = ""
	e.startCol = 0
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// popupColWidth is the minimum column width for the word column inside the
// popup. It expands to fit the longest candidate.
const popupColWidth = 20

// Render produces the styled completion popup string. The caller is
// responsible for overlaying this at the correct screen position.
func (e *Engine) Render(width int, th *theme.Theme) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.active || len(e.items) == 0 {
		return ""
	}

	visibleStart, visibleEnd := e.visibleWindow()
	visible := e.items[visibleStart:visibleEnd]

	wordCol := e.longestWord(visible)

	borderStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Background(th.Palette.Background)
	normalStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Foreground).
		Background(th.Palette.Background)
	selectedStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Background).
		Background(th.Palette.Primary).
		Bold(true)
	kindStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Muted).
		Background(th.Palette.Background)
	kindSelectedStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Background).
		Background(th.Palette.Primary)

	maxWidth := min(wordCol+kindBadgeWidth+menuPadding, max(width, popupColWidth))

	var b strings.Builder
	topBorder := borderStyle.Render(strings.Repeat("\u2500", maxWidth))
	b.WriteString(topBorder)
	b.WriteRune('\n')

	for i, item := range visible {
		isSelected := (visibleStart + i) == e.selected
		row := e.renderRow(item, wordCol, maxWidth, isSelected, normalStyle, selectedStyle, kindStyle, kindSelectedStyle)
		b.WriteString(row)
		if i < len(visible)-1 {
			b.WriteRune('\n')
		}
	}

	b.WriteRune('\n')
	bottomBorder := borderStyle.Render(strings.Repeat("\u2500", maxWidth))
	b.WriteString(bottomBorder)

	// Scroll indicator when items exceed visible window.
	if len(e.items) > maxVisibleItems {
		indicator := fmt.Sprintf(" %d/%d ", e.selected+1, len(e.items))
		b.WriteRune('\n')
		b.WriteString(borderStyle.Render(indicator))
	}

	return b.String()
}

// kindBadgeWidth is the number of characters reserved for the kind badge
// column: "[Buf] " = bracket + 4 chars + bracket + space.
const kindBadgeWidth = 7

// menuPadding is the spacing between the kind badge and menu text.
const menuPadding = 2

// visibleWindow computes the start and end indices for the virtual scroll
// window around the selected item.
func (e *Engine) visibleWindow() (int, int) {
	total := len(e.items)
	visible := min(maxVisibleItems, total)
	start := 0

	// Keep the selected item within the visible window.
	if e.selected >= start+visible {
		start = e.selected - visible + 1
	}
	if e.selected < start {
		start = e.selected
	}

	end := min(start+visible, total)
	return start, end
}

// longestWord returns the display width of the longest word among items,
// clamped to popupColWidth at minimum.
func (e *Engine) longestWord(items []CompletionItem) int {
	longest := popupColWidth
	for _, item := range items {
		if w := utf8.RuneCountInString(item.Word); w > longest {
			longest = w
		}
	}
	return longest
}

// renderRow renders a single popup row.
func (e *Engine) renderRow(
	item CompletionItem,
	wordCol int,
	maxWidth int,
	isSelected bool,
	normal, selected, kind, kindSel lipgloss.Style,
) string {
	rowStyle := normal
	badgeStyle := kind
	if isSelected {
		rowStyle = selected
		badgeStyle = kindSel
	}

	badge := badgeStyle.Render(fmt.Sprintf("[%s]", item.Kind.KindName()))
	word := padRight(item.Word, wordCol)
	text := rowStyle.Render(word)

	row := fmt.Sprintf(" %s %s", badge, text)

	// Truncate to maxWidth if necessary.
	if runeWidth := utf8.RuneCountInString(stripAnsi(row)); runeWidth > maxWidth {
		runes := []rune(row)
		row = string(runes[:maxWidth])
	}
	return row
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractPrefix walks backward from cursorPos through content to find the
// word prefix being typed. A word boundary is any non-letter, non-digit,
// non-underscore, non-dot, non-slash rune.
func extractPrefix(content []rune, cursorPos int) string {
	end := min(cursorPos, len(content))
	start := end
	for start > 0 && isWordRune(content[start-1]) {
		start--
	}
	return string(content[start:end])
}

// isWordRune returns true for runes that can be part of a completion prefix.
func isWordRune(r rune) bool {
	// Letters, digits, underscore, dot, slash, hyphen for file paths.
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '.' || r == '/' || r == '-'
}

// padRight pads s with spaces to width w.
func padRight(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// stripAnsi removes ANSI escape sequences for width measurement.
// This is a minimal implementation covering the CSI sequences produced
// by lipgloss.
func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
