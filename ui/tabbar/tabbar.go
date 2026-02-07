package tabbar

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/adalundhe/sylk/ui/filetree"
	"github.com/adalundhe/sylk/ui/theme"
)

// Tab represents a single open file tab.
type Tab struct {
	Path     string
	Modified bool
}

// Config holds rendering parameters for the tab bar.
type Config struct {
	Tabs      []Tab
	Active    int // Index of the active tab in Tabs.
	Width     int // Available character width for the entire bar.
	NerdFonts bool
	Theme     *theme.Theme

	// FlashLeft/FlashRight highlight the overflow arrow in Primary color
	// when true (used for a brief click-feedback flash).
	FlashLeft  bool
	FlashRight bool

	// HoverClose is the index of the tab whose close icon is being
	// hovered, or -1 when no close icon is hovered.
	HoverClose int
}

// Height is the number of lines the tab bar occupies (tabs + divider).
const Height = 2

// separatorWidth is the rendered width of the tab separator (" │ ").
// Derived from: 1 space + 1 pipe + 1 space.
const separatorWidth = 3

// modifiedGlyph replaces the close icon on tabs with unsaved changes.
const modifiedGlyph = "●"

// closeWidth is the rendered width of the close region (" ✕ ").
// Derived from: 1 space + 1 glyph + 1 space, so the hover highlight
// can be centered around the glyph.
const closeWidth = 3

// closeHitWidth is the clickable zone for the close icon.
// Derived from: matches closeWidth so the entire visual region is clickable.
const closeHitWidth = 3

// padWidth is the leading space before icon in each tab.
// Derived from: 1 space.
const padWidth = 1

// iconGap is the space between icon glyph and filename.
// Derived from: 1 space.
const iconGap = 1

// minNameChars is the minimum visible display columns of a truncated filename.
// Derived from: at least 1 char visible.
const minNameChars = 1

// overflowIndicatorWidth is the width of a scroll indicator ("◀ " or " ▶").
// Derived from: 1 glyph + 1 space.
const overflowIndicatorWidth = 2

// trailPadWidth is the trailing " │" after the last tab, matching the
// border character that follows every other tab via the separator.
// Derived from: 1 space + 1 pipe.
const trailPadWidth = 2

// clampActive returns cfg.Active clamped to [0, len(cfg.Tabs)-1].
func clampActive(cfg Config) int {
	if cfg.Active < 0 {
		return 0
	}
	if cfg.Active >= len(cfg.Tabs) {
		return len(cfg.Tabs) - 1
	}
	return cfg.Active
}

// View renders the tab bar as two lines: the tab row and a divider.
// The divider has a gap under the active tab. Returns "" when empty.
func View(cfg Config) string {
	if len(cfg.Tabs) == 0 || cfg.Width < 1 {
		return ""
	}
	cfg.Active = clampActive(cfg)

	widths := resolveWidths(cfg)
	tabLine := renderTabs(cfg, widths)
	divider := renderDivider(cfg, widths)
	return tabLine + "\n" + divider
}

// iconDisplayWidth returns the terminal cell width of a tab's icon glyph.
func iconDisplayWidth(cfg Config, t Tab) int {
	icon, _ := filetree.FileIcon(t.Path, false, cfg.NerdFonts, cfg.Theme)
	return runewidth.StringWidth(icon)
}

// naturalWidth returns the display width of a tab at its full name.
func naturalWidth(cfg Config, t Tab) int {
	name := filepath.Base(t.Path)
	return padWidth + iconDisplayWidth(cfg, t) + iconGap + runewidth.StringWidth(name) + closeWidth
}

// naturalWidths returns the natural width of each tab.
func naturalWidths(cfg Config) []int {
	widths := make([]int, len(cfg.Tabs))
	for i, t := range cfg.Tabs {
		widths[i] = naturalWidth(cfg, t)
	}
	return widths
}

// sumWidths returns the total width of n tabs including separators and
// the trailing pad after the last tab.
func sumWidths(widths []int, n int) int {
	total := 0
	for i := range n {
		total += widths[i]
	}
	if n > 1 {
		total += separatorWidth * (n - 1)
	}
	if n > 0 {
		total += trailPadWidth
	}
	return total
}

// resolveWidths computes the per-tab widths to use for rendering.
func resolveWidths(cfg Config) []int {
	naturals := naturalWidths(cfg)
	totalNatural := sumWidths(naturals, len(cfg.Tabs))

	if totalNatural <= cfg.Width {
		return naturals
	}

	budgets := truncatedWidths(cfg, naturals)
	if sumWidths(budgets, len(cfg.Tabs)) <= cfg.Width {
		return budgets
	}

	// Overflow: fall back to natural widths (renderOverflow handles clipping).
	return naturals
}

// truncatedWidths computes per-tab widths that fit within cfg.Width.
func truncatedWidths(cfg Config, naturals []int) []int {
	n := len(cfg.Tabs)
	seps := separatorWidth * max(n-1, 0)
	available := cfg.Width - seps - trailPadWidth
	budget := max(available/n, minTabWidth(cfg))

	widths := make([]int, n)
	for i := range n {
		widths[i] = min(naturals[i], budget)
	}
	return widths
}

// minTabWidth returns the smallest renderable tab width.
func minTabWidth(cfg Config) int {
	// Use a representative icon width (worst case 2 for wide glyphs).
	maxIcon := 1
	for _, t := range cfg.Tabs {
		if w := iconDisplayWidth(cfg, t); w > maxIcon {
			maxIcon = w
		}
	}
	// pad + icon + gap + 1 name char + ellipsis + close
	return padWidth + maxIcon + iconGap + minNameChars + 1 + closeWidth
}

// renderTabs renders all tabs in a single line with the given per-tab widths.
func renderTabs(cfg Config, widths []int) string {
	total := sumWidths(widths, len(cfg.Tabs))
	if total > cfg.Width {
		return renderOverflow(cfg)
	}

	sep := separatorStyle(cfg.Theme)
	var b strings.Builder
	for i, t := range cfg.Tabs {
		if i > 0 {
			b.WriteString(sep.Render(" │ "))
		}
		b.WriteString(renderTab(cfg, t, i == cfg.Active, i == cfg.HoverClose, widths[i]))
	}
	b.WriteString(separatorStyle(cfg.Theme).Render(" │"))
	return b.String()
}

// renderTab renders a single tab within the given width budget.
func renderTab(cfg Config, t Tab, active, hoverClose bool, budget int) string {
	icon, iconColor := filetree.FileIcon(t.Path, false, cfg.NerdFonts, cfg.Theme)
	name := filepath.Base(t.Path)

	// Determine available display columns for the name.
	iconW := runewidth.StringWidth(icon)
	overhead := padWidth + iconW + iconGap + closeWidth
	nameSpace := budget - overhead
	if nameSpace < 0 {
		nameSpace = minNameChars
	}
	name = truncateName(name, nameSpace)

	// Build styles.
	nameStyle, iconSt, closeGapSt, closeGlyphSt := tabStyles(cfg.Theme, iconColor, active, hoverClose, t.Modified)

	closeGlyph := "✕"
	if t.Modified {
		closeGlyph = modifiedGlyph
	}

	var b strings.Builder
	b.WriteByte(' ')
	b.WriteString(iconSt.Render(icon))
	b.WriteByte(' ')
	b.WriteString(nameStyle.Render(name))
	b.WriteString(closeGapSt.Render(" "))
	b.WriteString(closeGlyphSt.Render(closeGlyph))
	b.WriteString(closeGapSt.Render(" "))
	return b.String()
}

// tabStyles returns lipgloss styles for the tab components.
// closeGapStyle is for the spaces flanking the close glyph; closeGlyphStyle is
// for the glyph itself so the hover highlight covers the full close region.
// When modified is true the close glyph shows the warning color (unsaved dot).
func tabStyles(th *theme.Theme, iconColor lipgloss.Color, active, hoverClose, modified bool) (nameStyle, iconStyle, closeGapStyle, closeGlyphStyle lipgloss.Style) {
	closeColor := th.Palette.Muted
	if modified {
		closeColor = th.Palette.Warning
	}
	if active {
		nameStyle = lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
		iconStyle = lipgloss.NewStyle().Foreground(iconColor).Bold(true)
	} else {
		nameStyle = lipgloss.NewStyle().Foreground(th.Palette.Muted)
		iconStyle = lipgloss.NewStyle().Foreground(th.Palette.Muted)
	}
	closeGapStyle = lipgloss.NewStyle().Foreground(th.Palette.Muted)
	closeGlyphStyle = lipgloss.NewStyle().Foreground(closeColor)
	if hoverClose {
		closeGlyphStyle = lipgloss.NewStyle().Foreground(th.Palette.Error)
	}
	return
}

// separatorStyle returns the style for tab separators.
func separatorStyle(th *theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(th.Palette.Border)
}

// truncateName truncates a filename to fit within maxWidth display columns,
// appending "…" when truncated. Operates on runes and their display widths
// so multi-byte and wide characters are handled correctly.
func truncateName(name string, maxWidth int) string {
	if runewidth.StringWidth(name) <= maxWidth {
		return name
	}
	if maxWidth <= 1 {
		return "…"
	}

	// Reserve 1 column for the ellipsis.
	budget := maxWidth - 1
	var b strings.Builder
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		rw := runewidth.RuneWidth(r)
		if rw > budget {
			break
		}
		budget -= rw
		b.WriteRune(r)
		i += size
	}
	b.WriteRune('…')
	return b.String()
}

// renderDivider renders the bottom border line with a gap under the active tab.
func renderDivider(cfg Config, widths []int) string {
	total := sumWidths(widths, len(cfg.Tabs))
	if total > cfg.Width {
		return renderOverflowDivider(cfg)
	}

	lineStyle := lipgloss.NewStyle().Foreground(cfg.Theme.Palette.Border)

	// Compute the start and end column of the active tab.
	// The separator " │ " has its flanking spaces visually belonging to the
	// adjacent tabs, so extend the gap 1 char into each neighboring separator.
	activeStart := 0
	for i := range cfg.Active {
		activeStart += widths[i]
		activeStart += separatorWidth
	}
	activeEnd := activeStart + widths[cfg.Active]
	if cfg.Active > 0 {
		activeStart-- // absorb trailing space of left separator
	}
	if cfg.Active < len(cfg.Tabs)-1 {
		activeEnd++ // absorb leading space of right separator
	} else {
		activeEnd += trailPadWidth // absorb trailing pad
	}

	// Batch contiguous segments to minimize ANSI escape overhead.
	var b strings.Builder
	col := 0
	if activeStart > 0 {
		b.WriteString(lineStyle.Render(strings.Repeat("─", activeStart)))
		col = activeStart
	}
	if activeEnd > col {
		b.WriteString(strings.Repeat(" ", activeEnd-col))
		col = activeEnd
	}
	if cfg.Width > col {
		b.WriteString(lineStyle.Render(strings.Repeat("─", cfg.Width-col)))
	}
	return b.String()
}

// renderOverflowDivider renders a divider with a gap under the active tab
// in overflow mode, matching the layout produced by renderOverflow.
func renderOverflowDivider(cfg Config) string {
	lineStyle := lipgloss.NewStyle().Foreground(cfg.Theme.Palette.Border)
	lo, hi := visibleRange(cfg)

	// Walk the same layout as renderOverflow to find the active tab bounds.
	pos := 0
	if lo > 0 {
		pos += overflowIndicatorWidth
	}

	activeStart, activeEnd := 0, 0
	for i := lo; i <= hi; i++ {
		if i > lo {
			pos += separatorWidth
		}
		w := naturalWidth(cfg, cfg.Tabs[i])
		if i == cfg.Active {
			activeStart = pos
			activeEnd = pos + w
			// Extend gap into adjacent separators or trailing pad.
			if i > lo {
				activeStart--
			}
			if i < hi {
				activeEnd++
			} else if hi == len(cfg.Tabs)-1 {
				activeEnd += trailPadWidth // absorb trailing pad
			}
		}
		pos += w
	}

	// Batch contiguous segments to minimize ANSI escape overhead.
	var b strings.Builder
	col := 0
	if activeStart > 0 {
		b.WriteString(lineStyle.Render(strings.Repeat("─", activeStart)))
		col = activeStart
	}
	if activeEnd > col {
		b.WriteString(strings.Repeat(" ", activeEnd-col))
		col = activeEnd
	}
	if cfg.Width > col {
		b.WriteString(lineStyle.Render(strings.Repeat("─", cfg.Width-col)))
	}
	return b.String()
}

// visibleRange computes the inclusive [lo, hi] index range of tabs visible
// in overflow mode, centered on cfg.Active.
func visibleRange(cfg Config) (lo, hi int) {
	n := len(cfg.Tabs)
	lo, hi = cfg.Active, cfg.Active
	used := naturalWidth(cfg, cfg.Tabs[cfg.Active])

	for lo > 0 || hi < n-1 {
		expanded := false
		if hi < n-1 {
			w := naturalWidth(cfg, cfg.Tabs[hi+1]) + separatorWidth
			need := w
			if lo > 0 {
				need += overflowIndicatorWidth
			}
			if hi+1 < n-1 {
				need += overflowIndicatorWidth
			} else {
				need += trailPadWidth // last tab visible → trailing pad
			}
			if used+need <= cfg.Width {
				hi++
				used += w
				expanded = true
			}
		}
		if lo > 0 {
			w := naturalWidth(cfg, cfg.Tabs[lo-1]) + separatorWidth
			need := w
			if lo-1 > 0 {
				need += overflowIndicatorWidth
			}
			if hi < n-1 {
				need += overflowIndicatorWidth
			} else {
				need += trailPadWidth // last tab visible → trailing pad
			}
			if used+need <= cfg.Width {
				lo--
				used += w
				expanded = true
			}
		}
		if !expanded {
			break
		}
	}
	return lo, hi
}

// renderOverflow renders a scrollable window of tabs centered on the active tab,
// with ◀/▶ indicators when tabs are clipped.
func renderOverflow(cfg Config) string {
	th := cfg.Theme
	n := len(cfg.Tabs)
	lo, hi := visibleRange(cfg)

	sep := separatorStyle(th)
	leftStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	if cfg.FlashLeft {
		leftStyle = lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	}
	rightStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	if cfg.FlashRight {
		rightStyle = lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	}

	var b strings.Builder
	if lo > 0 {
		b.WriteString(leftStyle.Render("◀ "))
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			b.WriteString(sep.Render(" │ "))
		}
		w := naturalWidth(cfg, cfg.Tabs[i])
		b.WriteString(renderTab(cfg, cfg.Tabs[i], i == cfg.Active, i == cfg.HoverClose, w))
	}
	if hi < n-1 {
		b.WriteString(rightStyle.Render(" ▶"))
	} else {
		b.WriteString(separatorStyle(th).Render(" │"))
	}
	return b.String()
}

// HitResult describes what was clicked in the tab bar.
type HitResult struct {
	TabIndex   int  // -1 if nothing was hit.
	IsClose    bool // True if the close icon was clicked.
	IsLeftNav  bool // True if the ◀ overflow arrow was clicked.
	IsRightNav bool // True if the ▶ overflow arrow was clicked.
}

// HitTest returns the tab and region at the given x coordinate.
func HitTest(cfg Config, x int) HitResult {
	if len(cfg.Tabs) == 0 || cfg.Width < 1 {
		return HitResult{TabIndex: -1}
	}
	cfg.Active = clampActive(cfg)

	widths := resolveWidths(cfg)
	total := sumWidths(widths, len(cfg.Tabs))
	if total > cfg.Width {
		return hitTestOverflow(cfg, x)
	}

	pos := 0
	for i, w := range widths {
		closeStart := pos + w - min(closeHitWidth, w)
		// Extend the close zone 1 char past the tab edge so the right
		// side of the glyph is still clickable.
		closeEnd := pos + w + 1
		if x >= pos && x < pos+w {
			return HitResult{
				TabIndex: i,
				IsClose:  x >= closeStart,
			}
		}
		if x >= pos+w && x < closeEnd {
			return HitResult{TabIndex: i, IsClose: true}
		}
		pos += w
		if i < len(widths)-1 {
			pos += separatorWidth
		}
	}
	return HitResult{TabIndex: -1}
}

// hitTestOverflow performs hit testing in overflow mode, walking the same
// layout as renderOverflow: optional ◀, visible tabs, optional ▶.
func hitTestOverflow(cfg Config, x int) HitResult {
	n := len(cfg.Tabs)
	lo, hi := visibleRange(cfg)

	pos := 0

	// Left arrow region.
	if lo > 0 {
		if x < pos+overflowIndicatorWidth {
			return HitResult{TabIndex: -1, IsLeftNav: true}
		}
		pos += overflowIndicatorWidth
	}

	// Visible tabs.
	for i := lo; i <= hi; i++ {
		if i > lo {
			// First char of separator extends the previous tab's close zone.
			if x == pos {
				return HitResult{TabIndex: i - 1, IsClose: true}
			}
			if x > pos && x < pos+separatorWidth {
				return HitResult{TabIndex: -1}
			}
			pos += separatorWidth
		}
		w := naturalWidth(cfg, cfg.Tabs[i])
		closeStart := pos + w - min(closeHitWidth, w)
		if x >= pos && x < pos+w {
			return HitResult{
				TabIndex: i,
				IsClose:  x >= closeStart,
			}
		}
		pos += w
	}

	// Trailing pad extends last visible tab's close zone (1 char).
	if hi >= lo && x == pos {
		return HitResult{TabIndex: hi, IsClose: true}
	}

	// Right arrow region.
	if hi < n-1 {
		if x >= pos && x < pos+overflowIndicatorWidth {
			return HitResult{TabIndex: -1, IsRightNav: true}
		}
	}

	return HitResult{TabIndex: -1}
}
