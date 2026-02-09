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

// maxRowShrinkPerUpdate is the maximum number of rows the popup can
// shrink per Start() call. Limits visual bounce while allowing gradual
// resizing. Derived from: at 2 rows per update, a 10-row popup reaches
// its target in at most 5 keystrokes.
const maxRowShrinkPerUpdate = 2

// maxWidthShrinkPerUpdate is the maximum number of columns the popup can
// shrink per Start() call. Derived from: at 4 chars per update, a
// 28-char popup reaches its target in at most 7 keystrokes.
const maxWidthShrinkPerUpdate = 4

// Engine manages the lifecycle and rendering of the completion popup.
type Engine struct {
	mu           sync.RWMutex
	sources      *SourceRegistry
	active       bool
	items        []CompletionItem
	selected     int
	prefix       string
	startCol     int
	mode         CompletionMode
	sessionWidth int // damped popup width — grows instantly, shrinks gradually
	sessionRows  int // damped visible row count — grows instantly, shrinks gradually
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
	// Preserve the previously selected word across re-triggers so the
	// highlight doesn't jump to the top on every keystroke.
	prevWord := ""
	if e.active && e.selected < len(e.items) {
		prevWord = e.items[e.selected].Word
	}

	e.active = len(items) > 0
	e.items = items
	e.selected = findItemIndex(items, prevWord)
	e.prefix = prefix
	e.startCol = cursorPos - utf8.RuneCountInString(prefix)
	e.mode = mode

	// Track popup dimensions with damped shrinking: grow instantly so new
	// items are always visible, shrink at most N units per keystroke to
	// avoid frame-to-frame bounce while still converging to target size.
	targetWidth := e.longestWord(items) + kindBadgeWidth + rowPadding
	if targetWidth > e.sessionWidth {
		e.sessionWidth = targetWidth
	} else if targetWidth < e.sessionWidth {
		e.sessionWidth = max(targetWidth, e.sessionWidth-maxWidthShrinkPerUpdate)
	}
	targetRows := min(len(items), maxVisibleItems)
	if targetRows > e.sessionRows {
		e.sessionRows = targetRows
	} else if targetRows < e.sessionRows {
		e.sessionRows = max(targetRows, e.sessionRows-maxRowShrinkPerUpdate)
	}
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

// AcceptAt selects the item at the given absolute index and accepts it.
// Returns nil if the index is out of range or no completion is active.
func (e *Engine) AcceptAt(index int) *CompletionItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.active || index < 0 || index >= len(e.items) {
		return nil
	}
	item := e.items[index]
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

// VisibleRange returns the start and end indices of items currently shown
// in the popup window, limited to maxItems visible rows.
func (e *Engine) VisibleRange(maxItems int) (int, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.visibleWindowN(maxItems)
}

// MaxVisibleItems returns the maximum item rows the popup will show.
func MaxVisibleItems() int { return maxVisibleItems }

// PopupChromeRows returns the number of non-item rows in the popup
// (borders + footer).
func PopupChromeRows() int { return popupChrome }

// deactivate resets state. Caller must hold e.mu.
func (e *Engine) deactivate() {
	e.active = false
	e.items = nil
	e.selected = 0
	e.prefix = ""
	e.startCol = 0
	e.sessionWidth = 0
	e.sessionRows = 0
}

// findItemIndex returns the index of the item whose Word matches target,
// or 0 if not found. Used to preserve selection across re-triggers.
func findItemIndex(items []CompletionItem, target string) int {
	for i, item := range items {
		if item.Word == target {
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// popupColWidth is the minimum column width for the word column inside the
// popup. It expands to fit the longest candidate.
const popupColWidth = 20

// Render produces the styled completion popup string. The caller is
// responsible for overlaying this at the correct screen position.
// popupChrome is the number of lines consumed by borders and the footer:
// top border (1) + bottom border (1) + hint footer (1).
const popupChrome = 3

// Render produces the styled completion popup string. The maxHeight
// parameter limits the total popup height in terminal rows (including
// borders and indicator). Pass 0 for no constraint.
func (e *Engine) Render(width, maxHeight int, th *theme.Theme) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.active || len(e.items) == 0 {
		return ""
	}

	itemLimit := maxVisibleItems
	if maxHeight > 0 {
		itemLimit = min(itemLimit, max(maxHeight-popupChrome, 1))
	}

	visibleStart, visibleEnd := e.visibleWindowN(itemLimit)
	visible := e.items[visibleStart:visibleEnd]

	// Use sessionWidth (damped) so the popup shrinks gradually rather
	// than snapping to a narrower size on every keystroke.
	wordCol := e.longestWord(e.items)
	computedWidth := wordCol + kindBadgeWidth + rowPadding
	stableWidth := max(computedWidth, e.sessionWidth)

	// Clamp wordCol so rows fit within the available editor width.
	if stableWidth > width {
		wordCol = max(width-kindBadgeWidth-rowPadding, 1)
	}

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

	maxWidth := min(stableWidth, width)

	// Use sessionRows (damped) so the popup height shrinks gradually.
	stableRows := min(max(e.sessionRows, len(visible)), itemLimit)

	var b strings.Builder
	topBorder := borderStyle.Render(strings.Repeat("\u2500", maxWidth))
	b.WriteString(topBorder)
	b.WriteRune('\n')

	for i := range stableRows {
		if i < len(visible) {
			isSelected := (visibleStart + i) == e.selected
			row := e.renderRow(visible[i], wordCol, maxWidth, isSelected, normalStyle, selectedStyle, kindStyle, kindSelectedStyle)
			b.WriteString(row)
		} else {
			// Pad empty row to maintain stable height.
			b.WriteString(normalStyle.Render(strings.Repeat(" ", maxWidth)))
		}
		if i < stableRows-1 {
			b.WriteRune('\n')
		}
	}

	b.WriteRune('\n')
	bottomBorder := borderStyle.Render(strings.Repeat("\u2500", maxWidth))
	b.WriteString(bottomBorder)

	// Footer: key hints and optional scroll indicator, padded to full width.
	b.WriteRune('\n')
	footer := " Tab select"
	if len(e.items) > len(visible) {
		footer = fmt.Sprintf(" %d/%d  Tab select", e.selected+1, len(e.items))
	}
	b.WriteString(borderStyle.Render(padRight(footer, maxWidth)))

	return b.String()
}

// kindBadgeWidth is the visible width of the kind badge column.
// All kind names are padded to 4 characters: "[Xxxx]" = 6 visible chars.
const kindBadgeWidth = 6

// rowPadding accounts for the leading space and the space separating the
// badge from the word column in each popup row.
const rowPadding = 2

// visibleWindowN computes the start and end indices for the virtual scroll
// window around the selected item, showing at most n items.
func (e *Engine) visibleWindowN(n int) (int, int) {
	total := len(e.items)
	visible := min(n, total)
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

// maxKindNameLen is the longest kind name across all completion kinds.
// Used to left-pad shorter names inside the badge brackets for alignment.
const maxKindNameLen = 4

// renderRow renders a single popup row with consistent width.
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

	badge := badgeStyle.Render(fmt.Sprintf("[%-*s]", maxKindNameLen, item.Kind.KindName()))
	word := padRight(item.Word, wordCol)

	// Style every segment (including spaces) so the background is uniform.
	row := rowStyle.Render(" ") + badge + rowStyle.Render(" ") + rowStyle.Render(word)

	// Pad to maxWidth for consistent row widths across the popup.
	visibleWidth := lipgloss.Width(row)
	if visibleWidth < maxWidth {
		row += rowStyle.Render(strings.Repeat(" ", maxWidth-visibleWidth))
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

