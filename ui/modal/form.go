package modal

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FormField defines a single input field in a form.
type FormField struct {
	Key         string
	Label       string
	Default     string
	Placeholder string
}

// FormResult holds the outcome of a form submission.
type FormResult struct {
	Values    map[string]string // nil if cancelled.
	Submitted bool
}

// formHeaderLines is the number of lines consumed by the form header.
// Derived from: title(1) + separator(1) = 2.
const formHeaderLines = 2

// formFieldLines is the number of lines consumed per field.
// Derived from: label(1) + input(1) = 2.
const formFieldLines = 2

// Form is a multi-field input modal.
type Form struct {
	title    string
	fields   []FormField
	values   []string // Current value for each field, parallel to fields.
	active   int      // Index of the focused field.
	onSubmit func(map[string]string)
	th       *theme.Theme
}

// Verify interface compliance at compile time.
var _ ModalContent = (*Form)(nil)

// NewForm creates a form modal with the given fields.
func NewForm(title string, fields []FormField, onSubmit func(map[string]string), th *theme.Theme) *Form {
	values := make([]string, len(fields))
	for i, f := range fields {
		values[i] = f.Default
	}
	return &Form{
		title:    title,
		fields:   fields,
		values:   values,
		onSubmit: onSubmit,
		th:       th,
	}
}

// formKeyActions maps key strings to handler methods using table-driven dispatch.
type formKeyAction func(f *Form) (done bool, result any, cmd tea.Cmd)

func formKeyTable() map[string]formKeyAction {
	return map[string]formKeyAction{
		"tab":       func(f *Form) (bool, any, tea.Cmd) { f.nextField(); return false, nil, nil },
		"shift+tab": func(f *Form) (bool, any, tea.Cmd) { f.prevField(); return false, nil, nil },
		"down":      func(f *Form) (bool, any, tea.Cmd) { f.nextField(); return false, nil, nil },
		"up":        func(f *Form) (bool, any, tea.Cmd) { f.prevField(); return false, nil, nil },
		"enter":     func(f *Form) (bool, any, tea.Cmd) { return f.handleEnter() },
		"esc":       func(f *Form) (bool, any, tea.Cmd) { return f.cancel() },
		"backspace": func(f *Form) (bool, any, tea.Cmd) { f.deleteChar(); return false, nil, nil },
	}
}

// Update processes key input.
func (f *Form) Update(key tea.KeyMsg) (done bool, result any, cmd tea.Cmd) {
	actions := formKeyTable()
	if action, ok := actions[key.String()]; ok {
		return action(f)
	}

	// Typing characters appends to the active field.
	if isTypableRune(key) {
		f.appendChar(key.String())
		return false, nil, nil
	}

	return false, nil, nil
}

// View renders the form.
func (f *Form) View(width, _ int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(f.th.Palette.Primary)
	separator := lipgloss.NewStyle().
		Foreground(f.th.Palette.Border).
		Render(strings.Repeat("\u2500", max(width, 0)))

	title := titleStyle.Render(f.title)
	fieldViews := f.renderFields(width)

	hintStyle := lipgloss.NewStyle().Foreground(f.th.Palette.Muted).Italic(true)
	hint := hintStyle.Render("Tab: next field  Enter: submit  Esc: cancel")

	return fmt.Sprintf("%s\n%s\n%s\n\n%s", title, separator, fieldViews, hint)
}

// ---------------------------------------------------------------------------
// Field navigation
// ---------------------------------------------------------------------------

// nextField moves focus to the next field.
func (f *Form) nextField() {
	if len(f.fields) == 0 {
		return
	}
	f.active = (f.active + 1) % len(f.fields)
}

// prevField moves focus to the previous field.
func (f *Form) prevField() {
	if len(f.fields) == 0 {
		return
	}
	f.active = (f.active - 1 + len(f.fields)) % len(f.fields)
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

// appendChar adds a character to the active field's value.
func (f *Form) appendChar(ch string) {
	if f.active < 0 || f.active >= len(f.values) {
		return
	}
	f.values[f.active] += ch
}

// deleteChar removes the last character from the active field's value.
func (f *Form) deleteChar() {
	if f.active < 0 || f.active >= len(f.values) {
		return
	}
	v := f.values[f.active]
	if len(v) == 0 {
		return
	}
	f.values[f.active] = v[:len(v)-1]
}

// handleEnter submits the form if on the last field, otherwise moves to the next.
func (f *Form) handleEnter() (bool, any, tea.Cmd) {
	isLastField := f.active >= len(f.fields)-1
	if isLastField {
		return f.submit()
	}
	f.nextField()
	return false, nil, nil
}

// submit collects all field values and invokes the callback.
func (f *Form) submit() (bool, any, tea.Cmd) {
	result := f.collectValues()
	if f.onSubmit != nil {
		f.onSubmit(result)
	}
	return true, FormResult{Values: result, Submitted: true}, nil
}

// cancel closes the form without submitting.
func (f *Form) cancel() (bool, any, tea.Cmd) {
	return true, FormResult{Values: nil, Submitted: false}, nil
}

// collectValues builds the key-value map from current field values.
func (f *Form) collectValues() map[string]string {
	result := make(map[string]string, len(f.fields))
	for i, field := range f.fields {
		result[field.Key] = f.values[i]
	}
	return result
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderFields renders all form fields.
func (f *Form) renderFields(width int) string {
	lines := make([]string, 0, len(f.fields)*formFieldLines)
	for i, field := range f.fields {
		isActive := i == f.active
		lines = append(lines, f.renderField(field, f.values[i], isActive, width))
	}
	return strings.Join(lines, "\n")
}

// renderField renders a single form field with label and input.
func (f *Form) renderField(field FormField, value string, active bool, width int) string {
	labelStyle := f.fieldLabelStyle(active)
	label := labelStyle.Render(field.Label + ":")

	inputContent := f.formatInputContent(value, field.Placeholder, active)
	inputStyle := f.fieldInputStyle(active, width)
	input := inputStyle.Render(inputContent)

	return fmt.Sprintf("%s\n%s", label, input)
}

// fieldLabelStyle returns the style for a field label.
func (f *Form) fieldLabelStyle(active bool) lipgloss.Style {
	if active {
		return lipgloss.NewStyle().Foreground(f.th.Palette.Primary).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(f.th.Palette.Foreground)
}

// fieldInputStyle returns the style for a field input box.
func (f *Form) fieldInputStyle(active bool, width int) lipgloss.Style {
	borderColor := f.th.Palette.Border
	if active {
		borderColor = f.th.Palette.Primary
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(max(width-2, 0)). // Account for border width.
		Padding(0, 1)
}

// formatInputContent formats the display text for a field input.
func (f *Form) formatInputContent(value, placeholder string, active bool) string {
	if value != "" {
		if active {
			return value + "_"
		}
		return value
	}

	if active {
		return "_"
	}

	if placeholder != "" {
		return placeholder
	}
	return ""
}

// isTypableRune returns true if the key represents a single printable character.
func isTypableRune(key tea.KeyMsg) bool {
	r := []rune(key.String())
	if len(r) != 1 {
		return false
	}
	return r[0] >= ' ' && r[0] <= '~'
}
