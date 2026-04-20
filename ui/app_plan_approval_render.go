// Plan acceptance dialog render.
//
// Matches the guardian command-approval dialog's layout pattern:
//
//   1. A short fixed prompt line ("Approve, modify, or reject this
//      plan:") — never contains variable content, so never truncates.
//   2. A markdown-rendered, height-budgeted, scrollable body showing
//      the plan name (and, when space permits, the plan summary).
//      Word-wrapping is handled by the same RenderMarkdown path
//      command approval uses for its code block.
//   3. The three Approve / Modify / Reject option buttons.
//
// Previously the dialog concatenated the plan name directly into the
// prompt header and relied on hard truncation to fit a single line.
// That failed for two cases the guardian dialog gets right:
//
//   - Multi-line PlanName values (the architect's derivePlanName
//     pulls the user's original query, which routinely contains
//     newlines). lipgloss.Width measures the widest line, not total
//     length, so the truncation check would pass for a multi-line
//     string whose widest line fit; the border then rendered the
//     additional lines, the layout's line count underestimated the
//     visual height, and subsequent rows (the ellipsis tail, a blank
//     spacer, sometimes the start of the first option) clipped at
//     the bottom of the panel — surfacing as the "Re..." fragment
//     the user reported.
//   - Long single-line PlanName values that wrap at render time.
//     Same underlying bug: one layout line vs. multiple visual rows.
//
// Delegating body rendering to RenderMarkdown (with the same height
// budgeting and scroll state the guardian dialog uses) eliminates
// the mismatch — len(layout.lines) now equals the visible row count.
package ui

import (
	"strings"

	"github.com/adalundhe/sylk/core/planapproval"
	markdownpkg "github.com/adalundhe/sylk/ui/markdown"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
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

// planApprovalMaxVisibleBodyLines mirrors commandApprovalMaxVisibleCodeLines:
// caps the scrollable plan-body at a size that keeps the dialog from
// dominating the chat panel. Agents summarize the full plan elsewhere;
// the dialog is purely a verdict surface.
const planApprovalMaxVisibleBodyLines = 8

// planApprovalLayout composes: prompt + (height-budgeted, scrollable)
// body + option buttons. Returns commandApprovalViewLayout so mouse
// hit testing and scroll handling share the existing optionAt path.
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
	layout.lines = append(layout.lines,
		promptStyle.Render(planApprovalPromptLine()),
		"",
	)

	optionLines, optionHitboxes, optionLineCount := m.renderPlanApprovalOptions(width)
	bodyLines := renderPlanApprovalBody(m.planApproval.proposal, width, th)
	maxBodyLines := max(m.height-statusBarHeight-mainMinContentHeight-inputBorderSize, 1)
	availableForBody := max(maxBodyLines-len(layout.lines)-optionLineCount, 0)
	bodySpacerLines := 0
	if availableForBody > 1 && len(bodyLines) > 0 {
		bodySpacerLines = 1
	}
	bodyBudget := min(max(availableForBody-bodySpacerLines, 0), planApprovalMaxVisibleBodyLines)
	visibleBodyLines, appliedScroll := visibleCommandApprovalCodeLines(bodyLines, bodyBudget, m.planApproval.bodyScroll)
	layout.codeTotalLines = len(bodyLines)
	layout.codeVisibleLines = len(visibleBodyLines)
	layout.codeScroll = appliedScroll
	if len(visibleBodyLines) > 0 {
		layout.codeStartY = len(layout.lines)
		layout.lines = append(layout.lines, visibleBodyLines...)
		layout.codeEndY = len(layout.lines)
		if bodySpacerLines > 0 {
			layout.lines = append(layout.lines, "")
		}
	}
	for idx := range planApprovalOptions {
		baseY := len(layout.lines)
		layout.lines = append(layout.lines, optionLines[idx]...)
		for _, hitbox := range optionHitboxes[idx] {
			hitbox.y += baseY
			layout.hitboxes = append(layout.hitboxes, hitbox)
		}
	}
	return layout
}

func (m *AppModel) renderPlanApprovalOptions(width int) ([][]string, [][]commandApprovalHitbox, int) {
	optionLines := make([][]string, len(planApprovalOptions))
	optionHitboxes := make([][]commandApprovalHitbox, len(planApprovalOptions))
	optionLineCount := 0
	for idx, option := range planApprovalOptions {
		renderedLines, hitboxes := m.renderPlanApprovalOption(idx, option, width)
		optionLines[idx] = renderedLines
		optionHitboxes[idx] = hitboxes
		optionLineCount += len(renderedLines)
	}
	return optionLines, optionHitboxes, optionLineCount
}

// planApprovalPromptLine is the fixed prompt header. No variable
// content — matches commandApprovalPromptLine's "<agent> wants
// approval for:" invariant.
func planApprovalPromptLine() string {
	return "Approve, modify, or reject this plan:"
}

// renderPlanApprovalBody produces the scrollable body lines shown
// between the prompt and the option buttons. Mirrors
// renderCommandApprovalCodeBlock: uses the shared markdown renderer
// so word wrapping is consistent with the rest of the chat UI, and
// returns a proper []string whose length equals the visual row count.
//
// Body content (in priority order, joined by blank lines when the
// budget permits):
//
//   1. The plan name (collapsed to single-line whitespace so any
//      embedded newlines from a multi-line user query don't survive
//      into the rendered form). Emitted as a bold heading.
//   2. The plan summary, when non-empty.
//
// Empty body (no name, no summary) returns nil so the dialog degrades
// gracefully to prompt + buttons.
func renderPlanApprovalBody(proposal *planapproval.Proposal, width int, th *theme.Theme) []string {
	markdown := buildPlanApprovalMarkdown(proposal)
	if markdown == "" {
		return nil
	}
	rendered := markdownpkg.RenderMarkdown(markdown, width, th)
	return trimApprovalMarkdownBorders(rendered)
}

func buildPlanApprovalMarkdown(proposal *planapproval.Proposal) string {
	name := collapseWhitespace(proposal.PlanName)
	summary := collapseWhitespace(proposal.PlanSummary)
	parts := []string{}
	if name != "" {
		parts = append(parts, "**"+name+"**")
	}
	if summary != "" && summary != name {
		parts = append(parts, summary)
	}
	return strings.Join(parts, "\n\n")
}

// collapseWhitespace replaces any run of whitespace (including
// embedded newlines from multi-line queries) with a single space and
// trims. Prevents newlines in the plan name from being interpreted as
// markdown paragraph breaks the body block can't height-budget for.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// trimApprovalMarkdownBorders strips leading/trailing empty lines the
// markdown renderer sometimes emits around block content. Matches
// the guardian dialog's renderCommandApprovalCodeBlock behavior so
// the visual height matches the logical line count.
func trimApprovalMarkdownBorders(lines []string) []string {
	for len(lines) >= 2 && commandApprovalMarkdownLineEmpty(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) >= 2 && commandApprovalMarkdownLineEmpty(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
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

// scrollPlanApprovalBody adjusts the plan body scroll by delta.
// Mirrors scrollCommandApprovalCode so mouse-wheel and keyboard
// scroll events on the plan approval dialog work identically to the
// command approval dialog.
func (m *AppModel) scrollPlanApprovalBody(layout commandApprovalViewLayout, delta int) bool {
	if m.planApproval == nil || delta == 0 || layout.codeVisibleLines <= 0 || layout.codeTotalLines <= layout.codeVisibleLines {
		return false
	}
	maxScroll := layout.codeTotalLines - layout.codeVisibleLines
	next := clampInt(layout.codeScroll+delta, 0, maxScroll)
	if next == m.planApproval.bodyScroll {
		return false
	}
	m.planApproval.bodyScroll = next
	return true
}
