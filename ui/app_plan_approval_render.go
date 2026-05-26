// Plan acceptance dialog render.
//
// The dialog is only the decision control. The reviewable plan itself is a
// claims-board plan_markdown artifact rendered in the chat panel through
// ClaimPresentationMsg. Proposal.PlanText remains on the proposal as a
// migration/integrity fallback, but this renderer deliberately does not put it
// in the input panel.
package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

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
	maxPlanH := planApprovalMaxInnerLines + inputBorderSize
	return min(bodyLines+inputBorderSize, min(max(maxH, inputBorderSize), maxPlanH))
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

// planApprovalMaxInnerLines caps the entire plan approval dialog content,
// excluding the border. The dialog is intentionally compact: prompt, status,
// and the three decision options.
const planApprovalMaxInnerLines = 5

func (m *AppModel) planApprovalInnerLineBudget() int {
	maxBodyLines := m.height - statusBarHeight - mainMinContentHeight - inputBorderSize
	if maxBodyLines < 1 {
		maxBodyLines = 1
	}
	return min(maxBodyLines, planApprovalMaxInnerLines)
}

// planApprovalLayout composes: prompt + status + option buttons. Returns
// commandApprovalViewLayout so mouse hit testing shares the existing optionAt
// path.
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

	innerBudget := m.planApprovalInnerLineBudget()
	optionLines, optionHitboxes, _ := m.renderPlanApprovalOptions(width)
	if innerBudget > 0 {
		layout.lines = append(layout.lines, promptStyle.Render(planApprovalPromptLine()))
	}
	if len(layout.lines) < innerBudget {
		layout.lines = append(layout.lines, planApprovalStatusLine(m.planApproval.proposal.Metadata))
	}
	for idx := range planApprovalOptions {
		baseY := len(layout.lines)
		for _, line := range optionLines[idx] {
			if len(layout.lines) >= innerBudget {
				break
			}
			layout.lines = append(layout.lines, line)
		}
		for _, hitbox := range optionHitboxes[idx] {
			if baseY+hitbox.y >= len(layout.lines) {
				continue
			}
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

func planApprovalStatusLine(metadata map[string]any) string {
	status := planApprovalMetadataString(metadata, "plan_status", "status")
	if status == "" {
		status = "ready"
	}
	return "Status: " + status
}

func planApprovalMetadataBool(metadata map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if metadata == nil {
			return false, false
		}
		switch value := metadata[key].(type) {
		case bool:
			return value, true
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1", "yes":
				return true, true
			case "false", "0", "no":
				return false, true
			}
		}
	}
	return false, false
}

func planApprovalMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if metadata == nil {
			return ""
		}
		if value, ok := metadata[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func planApprovalMarkdownHash(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return "sha256:" + hex.EncodeToString(sum[:])
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

// scrollPlanApprovalBody is retained for shared mouse handling. Plan approval
// no longer has a scrollable body; the plan body lives in the chat artifact.
func (m *AppModel) scrollPlanApprovalBody(layout commandApprovalViewLayout, delta int) bool {
	return false
}
