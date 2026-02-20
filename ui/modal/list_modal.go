package modal

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxVisibleItems caps the number of items visible at once in the scrollable list.
// Derived from: typical modal height (~20 lines) minus title/footer/actions (~6 lines).
const maxVisibleItems = 14

// ListModalItem represents a single row in the list modal.
type ListModalItem struct {
	Label  string
	Detail string
	Badge  string         // optional right-aligned badge (e.g. "12.3 MB", "INTEGRATED")
	Color  lipgloss.Color // badge color
}

// ListModalResult holds the outcome of a list modal interaction.
type ListModalResult struct {
	Action int // index of the selected action button
}

// ListModal is a scrollable list with action buttons at the bottom.
// Implements ModalContent for use with the modal stack.
type ListModal struct {
	title    string
	items    []ListModalItem
	footer   string   // summary text below list
	actions  []string // button labels, e.g. ["Continue", "Cancel"]
	selected int      // which action button is selected
	scroll   int      // scroll offset into items
	th       *theme.Theme
}

// Verify interface compliance at compile time.
var _ ModalContent = (*ListModal)(nil)

// NewListModal creates a scrollable list modal with the given title, items,
// footer summary, and action buttons.
func NewListModal(title string, items []ListModalItem, footer string, actions []string, th *theme.Theme) *ListModal {
	return &ListModal{
		title:   title,
		items:   items,
		footer:  footer,
		actions: actions,
		th:      th,
	}
}

// listKeyActions maps key strings to handler methods.
type listKeyAction func(m *ListModal) (done bool, result any, cmd tea.Cmd)

func listKeyTable() map[string]listKeyAction {
	return map[string]listKeyAction{
		"up":    func(m *ListModal) (bool, any, tea.Cmd) { m.scrollUp(); return false, nil, nil },
		"k":     func(m *ListModal) (bool, any, tea.Cmd) { m.scrollUp(); return false, nil, nil },
		"down":  func(m *ListModal) (bool, any, tea.Cmd) { m.scrollDown(); return false, nil, nil },
		"j":     func(m *ListModal) (bool, any, tea.Cmd) { m.scrollDown(); return false, nil, nil },
		"tab":   func(m *ListModal) (bool, any, tea.Cmd) { m.nextAction(); return false, nil, nil },
		"left":  func(m *ListModal) (bool, any, tea.Cmd) { m.prevAction(); return false, nil, nil },
		"right": func(m *ListModal) (bool, any, tea.Cmd) { m.nextAction(); return false, nil, nil },
		"h":     func(m *ListModal) (bool, any, tea.Cmd) { m.prevAction(); return false, nil, nil },
		"l":     func(m *ListModal) (bool, any, tea.Cmd) { m.nextAction(); return false, nil, nil },
		"enter": func(m *ListModal) (bool, any, tea.Cmd) { return m.selectAction() },
		"esc":   func(m *ListModal) (bool, any, tea.Cmd) { return m.cancel() },
	}
}

// Update processes key input for scrolling and action selection.
func (m *ListModal) Update(key tea.KeyMsg) (done bool, result any, cmd tea.Cmd) {
	actions := listKeyTable()
	if action, ok := actions[key.String()]; ok {
		return action(m)
	}
	return false, nil, nil
}

// View renders the list modal with title, scrollable items, footer, and buttons.
func (m *ListModal) View(width, _ int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(m.th.Palette.Warning)
	itemStyle := lipgloss.NewStyle().Foreground(m.th.Palette.Foreground)
	detailStyle := lipgloss.NewStyle().Foreground(m.th.Palette.Muted)
	footerStyle := lipgloss.NewStyle().Foreground(m.th.Palette.Muted).Italic(true)

	var b strings.Builder

	// Title.
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n\n")

	// Scrollable items.
	visible := min(maxVisibleItems, len(m.items))
	end := min(m.scroll+visible, len(m.items))

	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		line := itemStyle.Render(item.Label)
		if item.Detail != "" {
			line += " " + detailStyle.Render(item.Detail)
		}
		if item.Badge != "" {
			badgeStyle := lipgloss.NewStyle().Bold(true)
			if item.Color != "" {
				badgeStyle = badgeStyle.Foreground(item.Color)
			}
			line += "  " + badgeStyle.Render(item.Badge)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Scroll indicator.
	if len(m.items) > maxVisibleItems {
		shown := fmt.Sprintf("(%d-%d of %d)", m.scroll+1, end, len(m.items))
		b.WriteString(detailStyle.Render(shown))
		b.WriteString("\n")
	}

	// Footer.
	if m.footer != "" {
		b.WriteString("\n")
		b.WriteString(footerStyle.Render(m.footer))
		b.WriteString("\n")
	}

	// Action buttons.
	b.WriteString("\n")
	b.WriteString(m.renderActions())

	return b.String()
}

// scrollUp moves the scroll position up by one.
func (m *ListModal) scrollUp() {
	if m.scroll > 0 {
		m.scroll--
	}
}

// scrollDown moves the scroll position down by one.
func (m *ListModal) scrollDown() {
	maxScroll := max(len(m.items)-maxVisibleItems, 0)
	if m.scroll < maxScroll {
		m.scroll++
	}
}

// nextAction cycles to the next action button.
func (m *ListModal) nextAction() {
	if len(m.actions) > 0 {
		m.selected = (m.selected + 1) % len(m.actions)
	}
}

// prevAction cycles to the previous action button.
func (m *ListModal) prevAction() {
	if len(m.actions) > 0 {
		m.selected = (m.selected - 1 + len(m.actions)) % len(m.actions)
	}
}

// selectAction confirms the currently selected action.
func (m *ListModal) selectAction() (bool, any, tea.Cmd) {
	return true, ListModalResult{Action: m.selected}, nil
}

// cancel dismisses the modal with the last action (assumed Cancel).
func (m *ListModal) cancel() (bool, any, tea.Cmd) {
	cancelIdx := len(m.actions) - 1
	if cancelIdx < 0 {
		cancelIdx = 0
	}
	return true, ListModalResult{Action: cancelIdx}, nil
}

// renderActions draws the action button row.
func (m *ListModal) renderActions() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.th.Palette.Background).
		Background(m.th.Palette.Primary).
		Padding(0, 1)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(m.th.Palette.Muted).
		Padding(0, 1)

	var parts []string
	for i, label := range m.actions {
		if i == m.selected {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, inactiveStyle.Render(label))
		}
	}

	return "  " + strings.Join(parts, "  ")
}
