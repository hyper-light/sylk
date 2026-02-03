package modal

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PickerItem represents a selectable entry in the picker.
type PickerItem struct {
	ID          string
	Label       string
	Description string
	Icon        string
}

// PickerResult holds the outcome of a picker selection.
type PickerResult struct {
	Selected *PickerItem // nil if cancelled.
}

// pickerHeaderLines is the number of lines consumed by the picker header.
// Derived from: title(1) + filter input(1) + separator(1) = 3.
const pickerHeaderLines = 3

// Picker is a filterable selection list modal.
type Picker struct {
	title    string
	items    []PickerItem
	filtered []int // Indices into items matching the current filter.
	selected int   // Index into filtered.
	filter   string
	onSelect func(PickerItem)
	th       *theme.Theme
}

// Verify interface compliance at compile time.
var _ ModalContent = (*Picker)(nil)

// NewPicker creates a picker modal.
func NewPicker(title string, items []PickerItem, onSelect func(PickerItem), th *theme.Theme) *Picker {
	p := &Picker{
		title:    title,
		items:    items,
		onSelect: onSelect,
		th:       th,
	}
	p.applyFilter()
	return p
}

// pickerKeyActions maps key strings to handler methods using table-driven dispatch.
type pickerKeyAction func(p *Picker) (done bool, result any, cmd tea.Cmd)

func pickerKeyTable() map[string]pickerKeyAction {
	return map[string]pickerKeyAction{
		"up":        func(p *Picker) (bool, any, tea.Cmd) { p.moveSelection(-1); return false, nil, nil },
		"k":         func(p *Picker) (bool, any, tea.Cmd) { p.moveSelection(-1); return false, nil, nil },
		"down":      func(p *Picker) (bool, any, tea.Cmd) { p.moveSelection(1); return false, nil, nil },
		"j":         func(p *Picker) (bool, any, tea.Cmd) { p.moveSelection(1); return false, nil, nil },
		"enter":     func(p *Picker) (bool, any, tea.Cmd) { return p.selectItem() },
		"esc":       func(p *Picker) (bool, any, tea.Cmd) { return p.cancel() },
		"backspace": func(p *Picker) (bool, any, tea.Cmd) { p.deleteFilterChar(); return false, nil, nil },
	}
}

// Update processes key input.
func (p *Picker) Update(key tea.KeyMsg) (done bool, result any, cmd tea.Cmd) {
	actions := pickerKeyTable()
	if action, ok := actions[key.String()]; ok {
		return action(p)
	}

	// Typing characters updates the filter.
	if isFilterableRune(key) {
		p.appendFilterChar(key.String())
		return false, nil, nil
	}

	return false, nil, nil
}

// View renders the picker.
func (p *Picker) View(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(p.th.Palette.Primary)
	filterStyle := lipgloss.NewStyle().Foreground(p.th.Palette.Foreground)

	title := titleStyle.Render(p.title)
	filterLine := filterStyle.Render(theme.IconSearch + " " + p.filter + "_")
	separator := lipgloss.NewStyle().
		Foreground(p.th.Palette.Border).
		Render(strings.Repeat("\u2500", max(width, 0)))

	availableLines := height - pickerHeaderLines
	lines := p.renderItems(width, availableLines)

	return fmt.Sprintf("%s\n%s\n%s\n%s", title, filterLine, separator, lines)
}

// ---------------------------------------------------------------------------
// Navigation & actions
// ---------------------------------------------------------------------------

// moveSelection moves the cursor by delta.
func (p *Picker) moveSelection(delta int) {
	count := len(p.filtered)
	if count == 0 {
		return
	}
	p.selected = clampIdx(p.selected+delta, count)
}

// selectItem selects the currently highlighted item.
func (p *Picker) selectItem() (bool, any, tea.Cmd) {
	if len(p.filtered) == 0 {
		return false, nil, nil
	}

	idx := p.filtered[p.selected]
	item := p.items[idx]
	if p.onSelect != nil {
		p.onSelect(item)
	}
	return true, PickerResult{Selected: &item}, nil
}

// cancel closes the picker without selecting.
func (p *Picker) cancel() (bool, any, tea.Cmd) {
	return true, PickerResult{Selected: nil}, nil
}

// ---------------------------------------------------------------------------
// Filter
// ---------------------------------------------------------------------------

// appendFilterChar adds a character to the filter and re-applies.
func (p *Picker) appendFilterChar(ch string) {
	p.filter += ch
	p.applyFilter()
}

// deleteFilterChar removes the last character from the filter.
func (p *Picker) deleteFilterChar() {
	if len(p.filter) == 0 {
		return
	}
	p.filter = p.filter[:len(p.filter)-1]
	p.applyFilter()
}

// applyFilter rebuilds the filtered index list based on the current filter text.
func (p *Picker) applyFilter() {
	p.filtered = p.filtered[:0]
	lower := strings.ToLower(p.filter)

	for i, item := range p.items {
		if matchesFilter(item, lower) {
			p.filtered = append(p.filtered, i)
		}
	}
	p.selected = clampIdx(p.selected, max(len(p.filtered), 1))
}

// matchesFilter returns true if the item matches the lowercase filter string.
func matchesFilter(item PickerItem, lower string) bool {
	if lower == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Label), lower) ||
		strings.Contains(strings.ToLower(item.Description), lower)
}

// isFilterableRune returns true if the key is a single printable character.
func isFilterableRune(key tea.KeyMsg) bool {
	r := []rune(key.String())
	if len(r) != 1 {
		return false
	}
	return r[0] >= ' ' && r[0] <= '~'
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderItems renders the visible list of filtered items.
func (p *Picker) renderItems(width, maxLines int) string {
	if len(p.filtered) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(p.th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No matching items")
	}

	start, end := visibleItemWindow(p.selected, len(p.filtered), maxLines)
	lines := make([]string, 0, end-start)

	for i := start; i < end; i++ {
		item := p.items[p.filtered[i]]
		selected := i == p.selected
		lines = append(lines, p.renderPickerItem(item, selected, width))
	}

	return strings.Join(lines, "\n")
}

// renderPickerItem renders a single picker item line.
func (p *Picker) renderPickerItem(item PickerItem, selected bool, width int) string {
	indicator := " "
	if selected {
		indicator = theme.IconExpand
	}

	icon := item.Icon
	if icon == "" {
		icon = " "
	}

	labelStyle := p.itemLabelStyle(selected)
	descStyle := lipgloss.NewStyle().Foreground(p.th.Palette.Muted)

	label := labelStyle.Render(item.Label)
	desc := ""
	if item.Description != "" {
		desc = " " + descStyle.Render(item.Description)
	}

	entry := fmt.Sprintf("  %s %s %s%s", indicator, icon, label, desc)

	if selected {
		highlightStyle := lipgloss.NewStyle().
			Background(p.th.Palette.Subtle).
			Width(width)
		return highlightStyle.Render(entry)
	}
	return entry
}

// itemLabelStyle returns the style for a picker item label.
func (p *Picker) itemLabelStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Foreground(p.th.Palette.Primary).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(p.th.Palette.Foreground)
}

// visibleItemWindow calculates start and end indices for scrolling.
func visibleItemWindow(selected, total, height int) (start, end int) {
	if total <= height {
		return 0, total
	}

	half := height / 2
	start = selected - half
	start = max(start, 0)
	end = start + height
	if end > total {
		end = total
		start = max(end-height, 0)
	}
	return start, end
}

// clampIdx constrains an index to [0, count-1].
func clampIdx(idx, count int) int {
	return max(0, min(idx, count-1))
}
