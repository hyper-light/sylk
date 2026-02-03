package modal

import (
	"fmt"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// buttonCount is the number of buttons in the confirm dialog.
// Derived from: [Yes] and [No] = 2 buttons.
const buttonCount = 2

// ConfirmResult holds the outcome of a confirm dialog.
type ConfirmResult struct {
	Confirmed bool
}

// ConfirmDialog is a modal that asks the user to confirm or cancel an action.
type ConfirmDialog struct {
	title      string
	message    string
	onConfirm  func()
	onCancel   func()
	selectedNo bool // false = Yes selected, true = No selected.
	th         *theme.Theme
}

// Verify interface compliance at compile time.
var _ ModalContent = (*ConfirmDialog)(nil)

// NewConfirmDialog creates a confirmation dialog.
func NewConfirmDialog(title, message string, onConfirm, onCancel func(), th *theme.Theme) *ConfirmDialog {
	return &ConfirmDialog{
		title:     title,
		message:   message,
		onConfirm: onConfirm,
		onCancel:  onCancel,
		th:        th,
	}
}

// confirmKeyActions maps key strings to handler methods using table-driven dispatch.
type confirmKeyAction func(d *ConfirmDialog) (done bool, result any, cmd tea.Cmd)

func confirmKeyTable() map[string]confirmKeyAction {
	return map[string]confirmKeyAction{
		"tab":   func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.toggleButton(); return false, nil, nil },
		"left":  func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.toggleButton(); return false, nil, nil },
		"right": func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.toggleButton(); return false, nil, nil },
		"h":     func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.toggleButton(); return false, nil, nil },
		"l":     func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.toggleButton(); return false, nil, nil },
		"enter": func(d *ConfirmDialog) (bool, any, tea.Cmd) { return d.confirm() },
		"y":     func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.selectedNo = false; return d.confirm() },
		"n":     func(d *ConfirmDialog) (bool, any, tea.Cmd) { d.selectedNo = true; return d.confirm() },
		"esc":   func(d *ConfirmDialog) (bool, any, tea.Cmd) { return d.cancel() },
	}
}

// Update processes key input.
func (d *ConfirmDialog) Update(key tea.KeyMsg) (done bool, result any, cmd tea.Cmd) {
	actions := confirmKeyTable()
	if action, ok := actions[key.String()]; ok {
		return action(d)
	}
	return false, nil, nil
}

// View renders the confirm dialog.
func (d *ConfirmDialog) View(width, _ int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(d.th.Palette.Primary)
	msgStyle := lipgloss.NewStyle().Foreground(d.th.Palette.Foreground).Width(width)

	title := titleStyle.Render(d.title)
	message := msgStyle.Render(d.message)
	buttons := d.renderButtons()

	return fmt.Sprintf("%s\n\n%s\n\n%s", title, message, buttons)
}

// toggleButton switches between Yes and No.
func (d *ConfirmDialog) toggleButton() {
	d.selectedNo = !d.selectedNo
}

// confirm executes the appropriate callback and returns done.
func (d *ConfirmDialog) confirm() (bool, any, tea.Cmd) {
	confirmed := !d.selectedNo
	if confirmed && d.onConfirm != nil {
		d.onConfirm()
	}
	if !confirmed && d.onCancel != nil {
		d.onCancel()
	}
	return true, ConfirmResult{Confirmed: confirmed}, nil
}

// cancel invokes the cancel callback and returns done.
func (d *ConfirmDialog) cancel() (bool, any, tea.Cmd) {
	if d.onCancel != nil {
		d.onCancel()
	}
	return true, ConfirmResult{Confirmed: false}, nil
}

// renderButtons draws the [Yes] [No] button row.
func (d *ConfirmDialog) renderButtons() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(d.th.Palette.Background).
		Background(d.th.Palette.Primary).
		Padding(0, 1)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(d.th.Palette.Muted).
		Padding(0, 1)

	yesStyle, noStyle := d.buttonStyles(activeStyle, inactiveStyle)

	return fmt.Sprintf("  %s  %s",
		yesStyle.Render("Yes"),
		noStyle.Render("No"))
}

// buttonStyles returns the styles for Yes and No buttons based on selection.
func (d *ConfirmDialog) buttonStyles(active, inactive lipgloss.Style) (yes, no lipgloss.Style) {
	if d.selectedNo {
		return inactive, active
	}
	return active, inactive
}
