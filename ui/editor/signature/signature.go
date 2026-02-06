// Package signature provides a floating tooltip for LSP signature help,
// showing the active function signature with the current parameter highlighted.
package signature

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/ui/theme"
)

// Responsive popup sizing constants.
// Derived from: signatures are typically wider than hover content (long param lists).
const (
	popupWidthFraction = 3   // numerator: use 3/4 of editor width
	popupWidthDivisor  = 4   // denominator
	popupMaxCap        = 120 // signatures can be long
	popupMinWidth      = 30  // hard floor
)

// SignatureHelp manages the state and rendering of the signature popup.
type SignatureHelp struct {
	result *lsp.SignatureHelp
	line   int  // anchor line (0-indexed)
	col    int  // anchor column (0-indexed)
	active bool
}

// New creates an inactive SignatureHelp instance.
func New() *SignatureHelp {
	return &SignatureHelp{}
}

// Show activates the popup with the given signature help result.
func (s *SignatureHelp) Show(result *lsp.SignatureHelp, line, col int) {
	s.result = result
	s.line = line
	s.col = col
	s.active = true
}

// Dismiss hides the popup and releases the result.
func (s *SignatureHelp) Dismiss() {
	s.result = nil
	s.active = false
}

// Active reports whether the popup is currently displayed.
func (s *SignatureHelp) Active() bool { return s.active }

// AnchorLine returns the editor line this popup is anchored to.
func (s *SignatureHelp) AnchorLine() int { return s.line }

// View renders the signature popup as a bordered box with the active
// parameter highlighted. Returns "" if inactive or no signatures.
func (s *SignatureHelp) View(width int, th *theme.Theme) string {
	if !s.active || s.result == nil || len(s.result.Signatures) == 0 {
		return ""
	}

	popupWidth := responsiveWidth(width)
	innerWidth := popupWidth - 4 // 2 border chars + 2 padding chars

	// Select active signature, clamped to valid range.
	sigIdx := min(s.result.ActiveSignature, len(s.result.Signatures)-1)
	sig := s.result.Signatures[sigIdx]

	content := renderSignatureLabel(sig, s.result.ActiveParameter, innerWidth, th)

	bgColor := th.Palette.PopupBg
	borderStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Background(bgColor)
	bgStyle := lipgloss.NewStyle().Background(bgColor)

	var b strings.Builder
	topFill := strings.Repeat("─", innerWidth+2)
	b.WriteString(borderStyle.Render("┌" + topFill + "┐"))
	b.WriteRune('\n')

	pad := bgStyle.Render(" ")
	padded := padToWidth(content, innerWidth, bgStyle)
	b.WriteString(borderStyle.Render("│") + pad + padded + pad + borderStyle.Render("│"))
	b.WriteRune('\n')

	botFill := strings.Repeat("─", innerWidth+2)
	b.WriteString(borderStyle.Render("└" + botFill + "┘"))

	return b.String()
}

// renderSignatureLabel styles the signature label with the active parameter
// rendered in bold Primary color.
func renderSignatureLabel(sig lsp.SignatureInformation, activeParam, maxWidth int, th *theme.Theme) string {
	label := sig.Label
	normalStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Foreground).
		Background(th.Palette.PopupBg)
	activeStyle := lipgloss.NewStyle().
		Foreground(th.Palette.Primary).
		Background(th.Palette.PopupBg).
		Bold(true)

	if activeParam < 0 || activeParam >= len(sig.Parameters) {
		return normalStyle.Render(truncate(label, maxWidth))
	}

	param := sig.Parameters[activeParam]
	start, end := paramLabelOffsets(label, param)
	if start < 0 || end <= start || end > len(label) {
		return normalStyle.Render(truncate(label, maxWidth))
	}

	before := label[:start]
	active := label[start:end]
	after := label[end:]
	return normalStyle.Render(before) + activeStyle.Render(active) + normalStyle.Render(after)
}

// paramLabelOffsets returns byte offsets [start, end) into the signature
// label for the given parameter. Returns (-1, -1) if the parameter cannot
// be located.
func paramLabelOffsets(sigLabel string, param lsp.ParameterInformation) (int, int) {
	if param.LabelOffsets != [2]int{} {
		return param.LabelOffsets[0], param.LabelOffsets[1]
	}
	if param.LabelString == "" {
		return -1, -1
	}
	idx := strings.Index(sigLabel, param.LabelString)
	if idx < 0 {
		return -1, -1
	}
	return idx, idx + len(param.LabelString)
}

// responsiveWidth computes the popup width from the editor width.
func responsiveWidth(editorWidth int) int {
	w := editorWidth * popupWidthFraction / popupWidthDivisor
	return min(max(w, popupMinWidth), popupMaxCap)
}

// padToWidth pads styled content to fill innerWidth with background.
func padToWidth(content string, innerWidth int, bgStyle lipgloss.Style) string {
	vis := lipgloss.Width(content)
	if vis >= innerWidth {
		return content
	}
	return content + bgStyle.Render(strings.Repeat(" ", innerWidth-vis))
}

// truncate trims a string to at most maxWidth visible characters.
func truncate(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return string(runes[:maxWidth])
	}
	return string(runes[:maxWidth-3]) + "..."
}
