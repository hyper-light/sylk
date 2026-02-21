package login

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// loginStep tracks the current step of the login state machine.
type loginStep int

const (
	stepIdle     loginStep = iota // Panel not active.
	stepProvider                  // Select a provider.
	stepMethod                    // Select auth method (OAuth or API key).
	stepAPIKey                    // Enter an API key.
	stepOAuth                     // Waiting for OAuth completion.
)

// btnFocus tracks which element has keyboard focus in button-enabled steps.
type btnFocus int

const (
	focusInput btnFocus = iota // Text input (API key step only).
	focusShow                  // Show/reveal button (API key step only).
	focusPaste                 // Paste button (API key step only).
	focusOk                    // Ok button.
	focusAbort                 // Abort button.
)

// LoginAction describes the outcome when the panel reports done.
type LoginAction int

const (
	ActionNone       LoginAction = iota
	ActionAPIKey                 // API key was entered.
	ActionOAuthStart             // OAuth flow should be launched.
	ActionCancelled              // User pressed ESC.
)

// LoginResult is the value returned when the panel finishes a step.
type LoginResult struct {
	Action   LoginAction
	Provider string
	Method   string
	APIKey   string
}

// item is a selectable entry in a list step.
type item struct {
	id    string
	label string
	desc  string
}

// panelBorderSize matches the app-wide constant: 1 char per side x 2 sides.
const panelBorderSize = 2

// Icon labels for the input-row action buttons.
const (
	showIcon  = "[show]"
	pasteIcon = "[paste]"
)

// Icon widths include a leading space separator.
// Derived from: 1 (space) + len(icon label).
const (
	showIconWidth  = 7  // " [show]"
	pasteIconWidth = 8  // " [paste]"
	iconAreaWidth  = 15 // showIconWidth + pasteIconWidth
)

// revealDuration is how long the key stays visible after pressing [show].
// Derived from: 3 seconds is long enough to verify, short enough to limit exposure.
const revealDuration = 3 * time.Second

// Button cell padding width (1 space each side of "icon label").
// Derived from: " ✓ Ok " = 6, " ✕ Abort " = 9.
const (
	okCellWidth    = 6
	abortCellWidth = 9
)

// Panel replaces the main panel area during the login flow.
type Panel struct {
	step          loginStep
	provider      string // Set after stepProvider.
	method        string // Set after stepMethod.
	items         []item // Current step's selectable items.
	selected      int
	input         []rune // API key text buffer.
	cursor        int    // Cursor position within input (0 = before first rune).
	oauth         string // OAuth status message.
	errMsg        string // Error from last attempt (shown inline, cleared on next input).
	validationErr string // Format validation error (shown as gutter error below input).
	btnFocus      btnFocus
	showRevealed  bool      // True while the key is temporarily visible.
	showRevealAt  time.Time // When the reveal started; auto-expires after revealDuration.
	showHover     bool      // Mouse is hovering over the show icon.
	pasteHover    bool      // Mouse is hovering over the paste icon.
	okHover       bool      // Mouse is hovering over the Ok button.
	abortHover    bool      // Mouse is hovering over the Abort button.
	keyResolver   func(string) string      // Returns pre-validated env/dotenv key or "".
	keyValidator  func(string, string) bool // Validates key format (provider, key) → ok.
	clipboard     func() (string, error)   // Reads system clipboard.
	btnRowY       int                      // Y offset of button content row (set during render).
	inputRowY     int                      // Y offset of input row (set during render).
	th            *theme.Theme
	width         int
	height        int
}

// New creates an idle login panel.
//
// resolver: called on transition to the API key step to pre-populate the input.
// validator: called before submit to check key format (provider, key) → ok.
// clipboard: reads the system clipboard for the paste button.
func New(
	th *theme.Theme,
	resolver func(string) string,
	validator func(string, string) bool,
	clipboard func() (string, error),
) *Panel {
	return &Panel{
		step:         stepIdle,
		th:           th,
		keyResolver:  resolver,
		keyValidator: validator,
		clipboard:    clipboard,
		btnRowY:      -1,
		inputRowY:    -1,
	}
}

// SetClipboard wires the clipboard read function after construction.
func (p *Panel) SetClipboard(fn func() (string, error)) {
	p.clipboard = fn
}

// Active reports whether the panel is currently displayed.
func (p *Panel) Active() bool { return p.step != stepIdle }

// Activate resets the panel to stepProvider and populates the provider list.
func (p *Panel) Activate() {
	p.step = stepProvider
	p.provider = ""
	p.method = ""
	p.selected = 0
	p.clearInput()
	p.oauth = ""
	p.errMsg = ""
	p.validationErr = ""
	p.btnFocus = focusInput
	p.showRevealed = false
	p.showHover = false
	p.pasteHover = false
	p.okHover = false
	p.abortHover = false
	p.btnRowY = -1
	p.inputRowY = -1
	p.items = []item{
		{id: "google", label: "Google (Gemini)", desc: "OAuth or API Key"},
		{id: "openai", label: "OpenAI", desc: "OAuth or API Key"},
		{id: "anthropic", label: "Anthropic", desc: "API Key"},
	}
}

// SetSize updates the panel's available dimensions for rendering.
func (p *Panel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetOAuthStatus updates the text shown during the OAuth waiting step.
func (p *Panel) SetOAuthStatus(text string) {
	p.oauth = text
}

// SetError displays an inline error message.
func (p *Panel) SetError(msg string) {
	p.errMsg = msg
}

// ---------------------------------------------------------------------------
// Mouse handling
// ---------------------------------------------------------------------------

// HandleClick processes a left-click at coordinates local to the panel's
// content area (0,0 = top-left corner inside the border).
func (p *Panel) HandleClick(x, y int) (done bool, result LoginResult, cmd tea.Cmd) {
	switch p.step {
	case stepProvider, stepMethod:
		idx := y - 2
		if idx < 0 || idx >= len(p.items) {
			return false, LoginResult{}, nil
		}
		p.selected = idx
		return p.selectCurrent()

	case stepAPIKey:
		if p.inputRowY >= 0 && y == p.inputRowY {
			hit := p.hitTestInputIcons(x)
			switch hit {
			case focusShow:
				p.btnFocus = focusShow
				p.doShowReveal()
				return false, LoginResult{}, nil
			case focusPaste:
				p.btnFocus = focusPaste
				return p.doPaste()
			default:
				p.btnFocus = focusInput
				return false, LoginResult{}, nil
			}
		}
		return p.handleButtonClick(x, y)

	case stepOAuth:
		return p.handleButtonClick(x, y)

	default:
		return false, LoginResult{}, nil
	}
}

// HandleMouseMotion processes mouse motion for hover effects.
func (p *Panel) HandleMouseMotion(x, y int) {
	p.showHover = false
	p.pasteHover = false
	p.okHover = false
	p.abortHover = false

	// Input row icon hover (API key step only).
	if p.step == stepAPIKey && p.inputRowY >= 0 && y == p.inputRowY {
		hit := p.hitTestInputIcons(x)
		p.showHover = hit == focusShow
		p.pasteHover = hit == focusPaste
		return
	}

	// Button row hover (API key and OAuth steps).
	if p.hasButtons() && p.btnRowY >= 0 && y == p.btnRowY {
		switch {
		case x < okCellWidth:
			p.okHover = true
		case x > okCellWidth && x < okCellWidth+1+abortCellWidth:
			p.abortHover = true
		}
	}
}

// hitTestInputIcons returns which icon the x coordinate hits on the input row.
func (p *Panel) hitTestInputIcons(x int) btnFocus {
	fieldW := p.inputFieldWidth()
	textW := max(fieldW-iconAreaWidth, 10)
	showStart := 1 + textW
	pasteStart := showStart + showIconWidth
	switch {
	case x >= showStart && x < pasteStart:
		return focusShow
	case x >= pasteStart && x < pasteStart+pasteIconWidth:
		return focusPaste
	default:
		return focusInput
	}
}

// handleButtonClick checks if a click landed on the Ok or Abort button row.
func (p *Panel) handleButtonClick(x, y int) (bool, LoginResult, tea.Cmd) {
	if p.btnRowY < 0 || y != p.btnRowY {
		if p.step == stepAPIKey {
			p.btnFocus = focusInput
		}
		return false, LoginResult{}, nil
	}
	// Content row layout: " ✓ Ok │ ✕ Abort │"
	// Ok cell: x 0..5 (" ✓ Ok "), separator at 6 ("│"),
	// Abort cell: x 7..15 (" ✕ Abort "), separator at 16 ("│").
	switch {
	case x < okCellWidth:
		p.btnFocus = focusOk
		return p.activateButton()
	case x > okCellWidth && x < okCellWidth+1+abortCellWidth:
		p.btnFocus = focusAbort
		return p.activateButton()
	default:
		return false, LoginResult{}, nil
	}
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// Update processes a key event.
func (p *Panel) Update(key tea.KeyMsg) (done bool, result LoginResult, cmd tea.Cmd) {
	switch p.step {
	case stepProvider, stepMethod:
		return p.updateList(key)
	case stepAPIKey:
		return p.updateInput(key)
	case stepOAuth:
		return p.updateOAuth(key)
	default:
		return false, LoginResult{}, nil
	}
}

func (p *Panel) updateList(key tea.KeyMsg) (bool, LoginResult, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if p.selected > 0 {
			p.selected--
		}
	case "down", "j":
		if p.selected < len(p.items)-1 {
			p.selected++
		}
	case "enter":
		return p.selectCurrent()
	case "esc":
		return true, LoginResult{Action: ActionCancelled}, nil
	}
	return false, LoginResult{}, nil
}

func (p *Panel) selectCurrent() (bool, LoginResult, tea.Cmd) {
	if p.selected >= len(p.items) {
		return false, LoginResult{}, nil
	}
	sel := p.items[p.selected]

	switch p.step {
	case stepProvider:
		p.provider = sel.id
		if sel.id == "anthropic" {
			p.method = "apikey"
			p.transitionToAPIKey()
			return false, LoginResult{}, nil
		}
		p.transitionToMethod()
		return false, LoginResult{}, nil

	case stepMethod:
		p.method = sel.id
		switch sel.id {
		case "apikey":
			p.transitionToAPIKey()
			return false, LoginResult{}, nil
		case "oauth":
			p.step = stepOAuth
			p.oauth = "Starting OAuth — a browser window will open..."
			p.btnFocus = focusAbort
			return true, LoginResult{
				Action:   ActionOAuthStart,
				Provider: p.provider,
				Method:   "oauth",
			}, nil
		}
	}
	return false, LoginResult{}, nil
}

func (p *Panel) transitionToMethod() {
	p.step = stepMethod
	p.selected = 0
	p.items = []item{
		{id: "oauth", label: "OAuth", desc: "Browser-based sign-in"},
		{id: "apikey", label: "API Key", desc: "Paste your key"},
	}
}

func (p *Panel) transitionToAPIKey() {
	p.step = stepAPIKey
	p.selected = 0
	p.items = nil
	p.clearInput()
	p.btnFocus = focusInput
	p.validationErr = ""
	p.showRevealed = false
	p.showHover = false
	p.pasteHover = false
	p.okHover = false
	p.abortHover = false

	if p.keyResolver != nil {
		if key := p.keyResolver(p.provider); key != "" {
			p.input = []rune(key)
			p.cursor = len(p.input)
		}
	}
}

func (p *Panel) updateInput(key tea.KeyMsg) (bool, LoginResult, tea.Cmd) {
	p.errMsg = ""
	p.validationErr = ""
	ks := key.String()

	// Universal keys.
	switch ks {
	case "esc":
		return true, LoginResult{Action: ActionCancelled}, nil
	case "tab":
		p.advanceFocusAPIKey()
		return false, LoginResult{}, nil
	case "shift+tab":
		p.retreatFocusAPIKey()
		return false, LoginResult{}, nil
	}

	switch p.btnFocus {
	case focusShow:
		if ks == "enter" || ks == " " {
			p.doShowReveal()
			return false, LoginResult{}, nil
		}
	case focusPaste:
		if ks == "enter" || ks == " " {
			return p.doPaste()
		}
	case focusOk:
		if ks == "enter" || ks == " " {
			return p.submitAPIKey()
		}
	case focusAbort:
		if ks == "enter" || ks == " " {
			return true, LoginResult{Action: ActionCancelled}, nil
		}
	default:
		return p.handleInputKey(key, ks)
	}
	return false, LoginResult{}, nil
}

func (p *Panel) handleInputKey(key tea.KeyMsg, ks string) (bool, LoginResult, tea.Cmd) {
	switch ks {
	case "enter":
		return p.submitAPIKey()
	case "left":
		if p.cursor > 0 {
			p.cursor--
		}
	case "right":
		if p.cursor < len(p.input) {
			p.cursor++
		}
	case "home":
		p.cursor = 0
	case "end":
		p.cursor = len(p.input)
	case "backspace":
		if p.cursor > 0 {
			copy(p.input[p.cursor-1:], p.input[p.cursor:])
			p.input[len(p.input)-1] = 0 // wipe tail
			p.input = p.input[:len(p.input)-1]
			p.cursor--
		}
	case "delete":
		if p.cursor < len(p.input) {
			copy(p.input[p.cursor:], p.input[p.cursor+1:])
			p.input[len(p.input)-1] = 0
			p.input = p.input[:len(p.input)-1]
		}
	default:
		if len(key.Runes) == 1 {
			p.input = append(p.input, 0)
			copy(p.input[p.cursor+1:], p.input[p.cursor:])
			p.input[p.cursor] = key.Runes[0]
			p.cursor++
		}
	}
	return false, LoginResult{}, nil
}

// doShowReveal toggles the temporary key reveal.
func (p *Panel) doShowReveal() {
	if p.showRevealed {
		p.showRevealed = false
		return
	}
	p.showRevealed = true
	p.showRevealAt = time.Now()
}

// isRevealed reports whether the key should currently be shown in plaintext.
func (p *Panel) isRevealed() bool {
	if !p.showRevealed {
		return false
	}
	if time.Since(p.showRevealAt) >= revealDuration {
		p.showRevealed = false
		return false
	}
	return true
}

func (p *Panel) doPaste() (bool, LoginResult, tea.Cmd) {
	if p.clipboard == nil {
		return false, LoginResult{}, nil
	}
	text, err := p.clipboard()
	if err != nil || text == "" {
		return false, LoginResult{}, nil
	}
	p.clearInput()
	p.input = []rune(strings.TrimSpace(text))
	p.cursor = len(p.input)
	p.btnFocus = focusInput
	return false, LoginResult{}, nil
}

func (p *Panel) submitAPIKey() (bool, LoginResult, tea.Cmd) {
	apiKey := strings.TrimSpace(string(p.input))
	if apiKey == "" {
		return false, LoginResult{}, nil
	}
	if p.keyValidator != nil && !p.keyValidator(p.provider, apiKey) {
		p.validationErr = formatValidationError(p.provider)
		return false, LoginResult{}, nil
	}
	p.clearInput()
	return true, LoginResult{
		Action:   ActionAPIKey,
		Provider: p.provider,
		Method:   "apikey",
		APIKey:   apiKey,
	}, nil
}

func formatValidationError(provider string) string {
	switch provider {
	case "anthropic":
		return "Invalid key: expected prefix \"sk-ant-\" with minimum 24 characters"
	case "openai":
		return "Invalid key: expected prefix \"sk-\" with minimum 24 characters"
	case "google":
		return "Invalid key: expected prefix \"AIza\" with minimum 24 characters"
	default:
		return "Invalid key: minimum 24 characters required"
	}
}

// advanceFocusAPIKey cycles: input → show → paste → ok → abort → input.
func (p *Panel) advanceFocusAPIKey() {
	switch p.btnFocus {
	case focusInput:
		p.btnFocus = focusShow
	case focusShow:
		p.btnFocus = focusPaste
	case focusPaste:
		p.btnFocus = focusOk
	case focusOk:
		p.btnFocus = focusAbort
	case focusAbort:
		p.btnFocus = focusInput
	}
}

func (p *Panel) retreatFocusAPIKey() {
	switch p.btnFocus {
	case focusInput:
		p.btnFocus = focusAbort
	case focusShow:
		p.btnFocus = focusInput
	case focusPaste:
		p.btnFocus = focusShow
	case focusOk:
		p.btnFocus = focusPaste
	case focusAbort:
		p.btnFocus = focusOk
	}
}

func (p *Panel) updateOAuth(key tea.KeyMsg) (bool, LoginResult, tea.Cmd) {
	ks := key.String()
	switch ks {
	case "esc":
		return true, LoginResult{Action: ActionCancelled}, nil
	case "tab":
		p.advanceFocusOAuth()
		return false, LoginResult{}, nil
	case "shift+tab":
		p.retreatFocusOAuth()
		return false, LoginResult{}, nil
	}
	switch p.btnFocus {
	case focusOk:
		if ks == "enter" || ks == " " {
			return false, LoginResult{}, nil
		}
	case focusAbort:
		if ks == "enter" || ks == " " {
			return true, LoginResult{Action: ActionCancelled}, nil
		}
	}
	return false, LoginResult{}, nil
}

func (p *Panel) advanceFocusOAuth() {
	if p.btnFocus == focusOk {
		p.btnFocus = focusAbort
	} else {
		p.btnFocus = focusOk
	}
}

func (p *Panel) retreatFocusOAuth() { p.advanceFocusOAuth() }

func (p *Panel) activateButton() (bool, LoginResult, tea.Cmd) {
	switch p.btnFocus {
	case focusOk:
		if p.step == stepAPIKey {
			return p.submitAPIKey()
		}
		return false, LoginResult{}, nil
	case focusAbort:
		return true, LoginResult{Action: ActionCancelled}, nil
	default:
		return false, LoginResult{}, nil
	}
}

// ---------------------------------------------------------------------------
// State helpers
// ---------------------------------------------------------------------------

func (p *Panel) Deactivate() {
	p.step = stepIdle
	p.provider = ""
	p.method = ""
	p.items = nil
	p.clearInput()
	p.oauth = ""
	p.selected = 0
	p.btnFocus = focusInput
	p.showRevealed = false
	p.showHover = false
	p.pasteHover = false
	p.okHover = false
	p.abortHover = false
	p.validationErr = ""
	p.btnRowY = -1
	p.inputRowY = -1
}

func (p *Panel) clearInput() {
	wipeRunes(p.input)
	p.input = nil
	p.cursor = 0
}

func wipeRunes(input []rune) {
	for i := range input {
		input[i] = 0
	}
}

func (p *Panel) inputFieldWidth() int {
	w := max(p.width, 4)
	innerW := max(w-panelBorderSize, 1)
	return min(innerW-1, 60)
}

func (p *Panel) hasButtons() bool {
	return p.step == stepAPIKey || p.step == stepOAuth
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (p *Panel) View(cursorVisible bool) string {
	if !p.Active() {
		return ""
	}

	w := max(p.width, 4)
	h := max(p.height, 4)
	innerW := max(w-panelBorderSize, 1)
	innerH := max(h-panelBorderSize, 1)

	header := p.sectionHeader(innerW)
	body := p.bodyLines(innerW, cursorVisible)
	hint := p.hintLine(innerW)

	// Bottom section: toolbar-style buttons (2 rows) + hint, or just hint.
	var bottomLines []string
	if p.hasButtons() {
		topBorder, contentRow := p.renderToolbarButtons()
		hintStr := hint
		// Place hint right-aligned on the content row.
		contentW := lipgloss.Width(contentRow)
		hintW := lipgloss.Width(hintStr)
		gap := max(innerW-contentW-hintW, 1)
		combinedRow := contentRow + strings.Repeat(" ", gap) + hintStr
		bottomLines = []string{topBorder, combinedRow}
	} else {
		bottomLines = []string{hint}
	}

	fixedLines := len(body) + 2 + len(bottomLines)
	bottomPad := max(innerH-fixedLines, 0)

	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")
	lines = append(lines, body...)
	for range bottomPad {
		lines = append(lines, "")
	}

	// btnRowY = the content row (second of the two bottom lines).
	if p.hasButtons() {
		p.btnRowY = len(lines) + len(bottomLines) - 1
	} else {
		p.btnRowY = -1
	}

	lines = append(lines, bottomLines...)
	content := strings.Join(lines, "\n")

	return p.th.ActiveBorder.
		Width(innerW).
		Height(innerH).
		MaxHeight(h).
		Render(content)
}

func (p *Panel) renderToolbarButtons() (topBorder, contentRow string) {
	bSt := lipgloss.NewStyle().Foreground(p.th.Palette.Border)
	pal := p.th.Palette

	okInner := p.buildBtnInner("✓", "Ok", pal.Success, p.btnFocus == focusOk, p.okHover)
	abortInner := p.buildBtnInner("✕", "Abort", pal.Error, p.btnFocus == focusAbort, p.abortHover)

	// Top border: ─(okW)─┬─(abortW)─╮
	var top strings.Builder
	top.WriteString(bSt.Render(strings.Repeat("─", okCellWidth)))
	top.WriteString(bSt.Render("┬"))
	top.WriteString(bSt.Render(strings.Repeat("─", abortCellWidth)))
	top.WriteString(bSt.Render("╮"))

	// Content row: okInner│abortInner│
	var bot strings.Builder
	bot.WriteString(okInner)
	bot.WriteString(bSt.Render("│"))
	bot.WriteString(abortInner)
	bot.WriteString(bSt.Render("│"))

	return top.String(), bot.String()
}

// buildBtnInner renders " icon label " matching the codebase toolbar pattern:
// default=Foreground, hovered=accent, selected=accent+bold.
func (p *Panel) buildBtnInner(icon, label string, accent lipgloss.Color, selected, hovered bool) string {
	pal := p.th.Palette
	fg := pal.Foreground
	bold := false
	if hovered {
		fg = accent
	}
	if selected {
		fg = accent
		bold = true
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Bold(bold).
		Render(" " + icon + " " + label + " ")
}

func (p *Panel) sectionHeader(width int) string {
	pal := p.th.Palette
	labelStyle := lipgloss.NewStyle().Foreground(pal.Secondary).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(pal.Border)

	text := labelStyle.Render(" " + p.titleText() + " ")
	textW := lipgloss.Width(text)
	lineW := max(width-textW, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineW))
}

func (p *Panel) titleText() string {
	switch p.step {
	case stepProvider:
		return "Login — Select Provider"
	case stepMethod:
		return "Login — " + providerLabel(p.provider) + " — Select Method"
	case stepAPIKey:
		return "Login — " + providerLabel(p.provider) + " — Enter API Key"
	case stepOAuth:
		return "Login — " + providerLabel(p.provider) + " — OAuth"
	default:
		return "Login"
	}
}

func (p *Panel) hintLine(width int) string {
	var text string
	switch p.step {
	case stepProvider, stepMethod:
		text = "j/k: navigate  enter: select  esc: cancel"
	case stepAPIKey, stepOAuth:
		text = "tab: cycle  enter: activate  esc: cancel"
	}
	style := lipgloss.NewStyle().Foreground(p.th.Palette.Muted).Italic(true)
	if !p.hasButtons() {
		return lipgloss.NewStyle().Width(width).Align(lipgloss.Right).
			Render(style.Render(text))
	}
	return style.Render(text)
}

func (p *Panel) bodyLines(innerW int, cursorVisible bool) []string {
	switch p.step {
	case stepProvider, stepMethod:
		return p.renderListBody(innerW)
	case stepAPIKey:
		return p.renderAPIKeyBody(innerW, cursorVisible)
	case stepOAuth:
		return p.renderOAuthBody(innerW)
	default:
		return nil
	}
}

func (p *Panel) renderListBody(innerW int) []string {
	pal := p.th.Palette
	selStyle := lipgloss.NewStyle().Foreground(pal.Primary).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(pal.Foreground)
	descStyle := lipgloss.NewStyle().Foreground(pal.Muted)
	pad := " "

	var lines []string
	for i, it := range p.items {
		prefix := "  "
		style := normalStyle
		if i == p.selected {
			prefix = "> "
			style = selStyle
		}
		line := pad + prefix + style.Render(it.label) + "  " + descStyle.Render(it.desc)
		lines = append(lines, truncate(line, innerW))
	}
	return lines
}

func (p *Panel) renderAPIKeyBody(innerW int, cursorVisible bool) []string {
	pal := p.th.Palette
	labelStyle := lipgloss.NewStyle().Foreground(pal.Primary).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(pal.Muted)
	placeholderStyle := lipgloss.NewStyle().Foreground(pal.Muted).Italic(true)
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	pad := " "

	desc := pad + mutedStyle.Render("Paste your "+providerLabel(p.provider)+" API key below.")
	label := pad + labelStyle.Render("API Key:")

	showCursor := cursorVisible && p.btnFocus == focusInput
	revealed := p.isRevealed()

	fieldW := min(innerW-1, 60)
	textW := max(fieldW-iconAreaWidth, 10)

	// Build visible input with cursor at the correct position.
	var inputContent string
	if len(p.input) == 0 {
		if showCursor {
			inputContent = cursorStyle.Render("s") + placeholderStyle.Render("k-...")
		} else {
			inputContent = placeholderStyle.Render("sk-...")
		}
	} else {
		var display string
		if revealed {
			display = string(p.input)
		} else {
			display = maskAPIKey(p.input)
		}
		// Render with cursor at p.cursor position.
		inputContent = renderWithCursor(display, p.cursor, len(p.input), textW-1, showCursor, cursorStyle)
	}

	inputBox := lipgloss.NewStyle().
		Width(textW).
		MaxWidth(textW).
		Render(inputContent)

	showStr := p.renderShowIcon()
	pasteStr := p.renderPasteIcon()
	compositeLine := pad + inputBox + showStr + pasteStr
	underline := mutedStyle.Render(strings.Repeat("─", max(fieldW, 10)))

	var lines []string
	lines = append(lines, desc)
	lines = append(lines, "")
	lines = append(lines, label)

	p.inputRowY = 2 + len(lines)
	lines = append(lines, compositeLine)
	lines = append(lines, pad+underline)

	if p.validationErr != "" {
		errStyle := lipgloss.NewStyle().Foreground(pal.Error)
		lines = append(lines, pad+errStyle.Render(truncate(p.validationErr, innerW-1)))
	}
	if p.errMsg != "" {
		errStyle := lipgloss.NewStyle().Foreground(pal.Error)
		lines = append(lines, pad+errStyle.Render(truncate(p.errMsg, innerW-1)))
	}

	return lines
}

// renderWithCursor inserts a reverse-video block cursor at the given position
// within a display string. The display string may be shorter than the input
// length (masked keys compress characters).
func renderWithCursor(display string, cursorPos, inputLen, maxW int, showCursor bool, cursorStyle lipgloss.Style) string {
	runes := []rune(display)
	n := len(runes)

	// Map cursor position proportionally when display is shorter than input
	// (masked text compresses internal chars to dots).
	displayPos := cursorPos
	if inputLen > 0 && n != inputLen {
		displayPos = cursorPos * n / inputLen
	}
	displayPos = min(displayPos, n)

	// Clamp to maxW.
	if n > maxW {
		runes = runes[:maxW]
		n = maxW
		displayPos = min(displayPos, n)
	}

	if !showCursor {
		return string(runes) + " "
	}

	if displayPos >= n {
		return string(runes) + cursorStyle.Render(" ")
	}
	return string(runes[:displayPos]) + cursorStyle.Render(string(runes[displayPos])) + string(runes[displayPos+1:])
}

func (p *Panel) renderShowIcon() string {
	pal := p.th.Palette
	revealed := p.isRevealed()
	switch {
	case revealed:
		return lipgloss.NewStyle().Bold(true).Foreground(pal.Primary).
			Render(" " + showIcon)
	case p.btnFocus == focusShow:
		return lipgloss.NewStyle().Foreground(pal.Secondary).
			Render(" " + showIcon)
	case p.showHover:
		return lipgloss.NewStyle().Foreground(pal.Secondary).
			Render(" " + showIcon)
	default:
		return lipgloss.NewStyle().Foreground(pal.Muted).
			Render(" " + showIcon)
	}
}

func (p *Panel) renderPasteIcon() string {
	pal := p.th.Palette
	switch {
	case p.btnFocus == focusPaste:
		return lipgloss.NewStyle().Bold(true).Foreground(pal.Primary).
			Render(" " + pasteIcon)
	case p.pasteHover:
		return lipgloss.NewStyle().Foreground(pal.Secondary).
			Render(" " + pasteIcon)
	default:
		return lipgloss.NewStyle().Foreground(pal.Muted).
			Render(" " + pasteIcon)
	}
}

func (p *Panel) renderOAuthBody(innerW int) []string {
	pal := p.th.Palette
	mutedStyle := lipgloss.NewStyle().Foreground(pal.Muted)
	infoStyle := lipgloss.NewStyle().Foreground(pal.Info)
	labelStyle := lipgloss.NewStyle().Foreground(pal.Primary).Bold(true)
	pad := " "

	provLabel := providerLabel(p.provider)
	desc := pad + mutedStyle.Render("Complete sign-in in your browser to authenticate with "+provLabel+".")

	status := p.oauth
	if status == "" {
		status = "Waiting for browser..."
	}
	statusLine := pad + labelStyle.Render("Status: ") + infoStyle.Render(status)

	var lines []string
	lines = append(lines, desc)
	lines = append(lines, "")
	lines = append(lines, statusLine)

	if p.errMsg != "" {
		errStyle := lipgloss.NewStyle().Foreground(pal.Error)
		lines = append(lines, "")
		lines = append(lines, pad+errStyle.Render(truncate(p.errMsg, innerW-1)))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

func maskAPIKey(input []rune) string {
	n := len(input)
	if n == 0 {
		return ""
	}
	if n <= 4 {
		return strings.Repeat("·", n)
	}
	if n <= 8 {
		return strings.Repeat("·", n-2) + string(input[n-2:])
	}
	return string(input[:2]) + strings.Repeat("·", n-4) + string(input[n-2:])
}

func providerLabel(provider string) string {
	switch provider {
	case "google":
		return "Google (Gemini)"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	default:
		return provider
	}
}

func truncate(s string, maxLen int) string {
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxLen-1 {
		runes = runes[:maxLen-1]
	}
	return string(runes) + "…"
}
