// Plan acceptance dialog render — buttons only.
//
// The dialog is intentionally minimal: a one-line prompt header plus
// the three Approve / Modify / Reject buttons. The plan body, freshness
// summary, drift signals, and orchestrator state hint live in the
// architect's chat narrative (where the user can scroll them); the
// dialog is purely a verdict-collection surface so it never crowds out
// the chat panel or breaks the input border.
package ui

import (
	"strings"

	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/charmbracelet/lipgloss"
)

func (m *AppModel) planApprovalHeight() int {
	bodyLines := len(m.planApprovalLayout(max(m.width-2, 1)).lines)
	if bodyLines < 1 {
		bodyLines = 1
	}
	maxH := m.height - statusBarHeight - mainMinContentHeight
	if maxH < inputBorderSize {
		maxH = inputBorderSize
	}
	return min(bodyLines+inputBorderSize, max(maxH, inputBorderSize))
}

func (m *AppModel) renderPlanApprovalView() string {
	th := m.config.Theme()
	contentWidth := max(m.width-2, 1)
	body := strings.Join(m.planApprovalLayout(contentWidth).lines, "\n")
	if m.focus.Current() == component.FocusInput && th.ActiveBorderRender != nil {
		return enforceLineCountToHeight(th.ActiveBorderRender(body, contentWidth, max(m.planApprovalHeight()-inputBorderSize, 1), m.planApprovalHeight()), m.planApprovalHeight())
	}
	style := th.InputBorder
	if m.focus.Current() == component.FocusInput {
		style = th.InputFocused
	}
	return enforceLineCountToHeight(style.Width(contentWidth).Render(body), m.planApprovalHeight())
}

// planApprovalLayout composes the buttons-only layout: a one-line
// prompt header followed by the three option rows. No scrollable
// body, no drift signals — the chat panel carries that context.
// Returns commandApprovalViewLayout (reused) so mouse hit testing
// shares the existing optionAt path.
func (m *AppModel) planApprovalLayout(width int) commandApprovalViewLayout {
	if width <= 0 {
		width = 1
	}
	if m.planApproval == nil || m.planApproval.proposal == nil {
		return commandApprovalViewLayout{lines: []string{""}}
	}
	th := m.config.Theme()
	promptStyle := lipgloss.NewStyle().Foreground(th.Palette.Secondary).Bold(true)

	layout := commandApprovalViewLayout{
		lines:      []string{},
		codeStartY: -1,
		codeEndY:   -1,
	}
	layout.lines = append(layout.lines, promptStyle.Render(planApprovalHeaderText(m.planApproval.proposal, width)), "")

	for idx, option := range planApprovalOptions {
		baseY := len(layout.lines)
		renderedLines, hitboxes := m.renderPlanApprovalOption(idx, option, width)
		layout.lines = append(layout.lines, renderedLines...)
		for _, hitbox := range hitboxes {
			hitbox.y += baseY
			layout.hitboxes = append(layout.hitboxes, hitbox)
		}
	}
	return layout
}

// planApprovalHeaderText is the single prompt line at the top of the
// dialog. Truncated to fit the available width with a trailing
// ellipsis when the plan name pushes total length past one row —
// without this guard the long plan-query name wraps to multiple
// terminal rows, the layout's bodyLines underestimates the rendered
// height, and the option buttons spill below the input border.
func planApprovalHeaderText(proposal *planapproval.Proposal, width int) string {
	const fallback = "Approve, modify, or reject this plan"
	if proposal == nil {
		return truncateHeaderToWidth(fallback, width)
	}
	name := strings.TrimSpace(proposal.PlanName)
	if name == "" {
		return truncateHeaderToWidth(fallback, width)
	}
	return truncateHeaderToWidth("Approve, modify, or reject plan: "+name, width)
}

// truncateHeaderToWidth caps a header string to the visible terminal
// column budget so the dialog header always renders on a single row.
// Width comes from the layout's content width (panel inner width);
// when the full text fits it's returned untouched, otherwise the
// trailing ellipsis is appended after a trim that leaves room for it.
// Operates on runes (display cells) rather than bytes so multi-byte
// characters in the header don't overshoot the column budget.
func truncateHeaderToWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	const ellipsis = "…"
	if width <= 1 {
		return ellipsis
	}
	runes := []rune(text)
	// Drop trailing runes one at a time until the (text + ellipsis)
	// width fits the budget. Linear in over-length but bounded by the
	// header itself (worst case ~120 chars per derivePlanName cap).
	for len(runes) > 0 {
		candidate := string(runes) + ellipsis
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return ellipsis
}

func (m *AppModel) renderPlanApprovalOption(index int, option commandApprovalOption, width int) ([]string, []commandApprovalHitbox) {
	th := m.config.Theme()
	selected := m.planApproval != nil && m.planApproval.selected == index
	activated := m.planApproval != nil && m.planApproval.activated == index
	renderedLines := renderCommandApprovalOptionLines(option, width, th, selected, activated)
	hitboxes := make([]commandApprovalHitbox, 0, len(renderedLines))
	for i, line := range renderedLines {
		visibleW := lipgloss.Width(line)
		hitboxes = append(hitboxes, commandApprovalHitbox{
			option: index,
			y:      i,
			x0:     0,
			x1:     visibleW,
		})
	}
	return renderedLines, hitboxes
}
