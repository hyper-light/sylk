package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/layout"
	markdownpkg "github.com/adalundhe/sylk/ui/markdown"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/tabbar"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// statusBarHeight is the fixed height of the status bar (1 line).
const statusBarHeight = 1

// inputBorderSize is the vertical space consumed by the input border.
// Derived from: 1 char top + 1 char bottom = 2.
const inputBorderSize = 2

// inputMinContentLines is the minimum visible content lines in the input.
const inputMinContentLines = 1

// mainMinContentHeight is the minimum usable height for the main area.
// Derived from: border (2) + minimum 3 content rows for context.
const mainMinContentHeight = panelBorderSize + 3

// commandApprovalMaxVisibleCodeLines keeps approval previews compact enough to
// preserve the prompt header and all actions while still showing some code
// before the user scrolls.
const commandApprovalMaxVisibleCodeLines = 8

// panelBorderSize is the space consumed by a rounded border on each axis.
// Derived from: 1 char per side × 2 sides = 2.
const panelBorderSize = 2

// leftPanelOverhead is the vertical space consumed by section chrome.
// Derived from: 2 headers (1 line each) + 1 divider (1 line + 1 top padding) = 4.
const leftPanelOverhead = 4

// minAgentSectionHeight is the minimum content height reserved for the Agents
// subsection above the selector row. This keeps Knowledge and Pipelines usable
// even when the Sessions list is short or the terminal is tight.
const minAgentSectionHeight = 6

func computeLeftPanelSections(leftW, leftH, selectorLines, preferredSessionHeight int) leftPanelSections {
	innerLeftW := max(leftW-panelBorderSize, 1)
	innerLeftH := max(leftH-panelBorderSize, 1)
	contentH := max(innerLeftH-leftPanelOverhead, 2)
	minAgentTotalH := max(minAgentSectionHeight+selectorLines, 1)
	maxSessionH := max(contentH-minAgentTotalH, 1)
	sessionH := min(max(preferredSessionHeight, 1), maxSessionH)
	agentTotalH := contentH - sessionH
	agentContentH := max(agentTotalH-selectorLines, 1)

	// Screen-space coordinates inside the bordered left panel content area.
	contentX := 1
	contentY := 1
	sessionTop := contentY + 1
	agentsHeaderY := contentY + 1 + sessionH + 2
	agentsTop := agentsHeaderY + 1
	selectorY := contentY + innerLeftH - selectorLines
	if selectorY < agentsTop {
		selectorY = agentsTop
	}

	return leftPanelSections{
		sessionRect:   pane.Rect{X: contentX, Y: sessionTop, W: innerLeftW, H: sessionH},
		agentsRect:    pane.Rect{X: contentX, Y: agentsTop, W: innerLeftW, H: agentContentH},
		agentsHeaderY: agentsHeaderY,
		selectorY:     selectorY,
	}
}

// inputMaxVisualLines computes the dynamic maximum content lines for the input.
// Derived from: total height - status bar - main area minimum - input border.
func (m *AppModel) inputMaxVisualLines() int {
	available := m.height - statusBarHeight - mainMinContentHeight - inputBorderSize
	proportional := max(m.height/10, inputMinContentLines)
	return max(min(available, proportional), inputMinContentLines)
}

// inputHeight returns the current rendered height of the input area
// based on its visual line count, clamped to [min, dynamic max] + border.
// The result is further constrained to ensure the main area retains
// at least 1 row — the input grows upward, never beyond available space.
func (m *AppModel) inputHeight() int {
	if m.commandApproval != nil {
		return m.commandApprovalHeight()
	}
	if m.layerDecision != nil {
		return m.layerDecisionHeight()
	}
	visualLines := m.input.VisualLineCount()
	maxLines := m.inputMaxVisualLines()
	lines := clampInt(visualLines, inputMinContentLines, maxLines)
	h := lines + inputBorderSize
	maxH := m.height - statusBarHeight - 1
	if maxH < inputMinContentLines+inputBorderSize {
		return inputMinContentLines + inputBorderSize
	}
	return min(h, maxH)
}

func (m *AppModel) commandApprovalHeight() int {
	bodyLines := len(m.commandApprovalLayout(max(m.width-2, 1)).lines)
	if bodyLines < 1 {
		bodyLines = 1
	}
	maxH := m.height - statusBarHeight - mainMinContentHeight
	if maxH < inputBorderSize {
		maxH = inputBorderSize
	}
	return min(bodyLines+inputBorderSize, max(maxH, inputBorderSize))
}

func (m *AppModel) layerDecisionHeight() int {
	bodyLines := len(m.layerDecisionLayout(max(m.width-2, 1)).lines)
	if bodyLines < 1 {
		bodyLines = 1
	}
	maxH := m.height - statusBarHeight - mainMinContentHeight
	if maxH < inputBorderSize {
		maxH = inputBorderSize
	}
	return min(bodyLines+inputBorderSize, max(maxH, inputBorderSize))
}

func (m *AppModel) renderCommandApprovalView() string {
	th := m.config.Theme()
	contentWidth := max(m.width-2, 1)
	body := strings.Join(m.commandApprovalLayout(contentWidth).lines, "\n")
	if m.focus.Current() == component.FocusInput && th.ActiveBorderRender != nil {
		return enforceLineCountToHeight(th.ActiveBorderRender(body, contentWidth, max(m.commandApprovalHeight()-inputBorderSize, 1), m.commandApprovalHeight()), m.commandApprovalHeight())
	}
	style := th.InputBorder
	if m.focus.Current() == component.FocusInput {
		style = th.InputFocused
	}
	return enforceLineCountToHeight(style.Width(contentWidth).Render(body), m.commandApprovalHeight())
}

func (m *AppModel) renderLayerDecisionView() string {
	th := m.config.Theme()
	contentWidth := max(m.width-2, 1)
	body := strings.Join(m.layerDecisionLayout(contentWidth).lines, "\n")
	if m.focus.Current() == component.FocusInput && th.ActiveBorderRender != nil {
		return enforceLineCountToHeight(th.ActiveBorderRender(body, contentWidth, max(m.layerDecisionHeight()-inputBorderSize, 1), m.layerDecisionHeight()), m.layerDecisionHeight())
	}
	style := th.InputBorder
	if m.focus.Current() == component.FocusInput {
		style = th.InputFocused
	}
	return enforceLineCountToHeight(style.Width(contentWidth).Render(body), m.layerDecisionHeight())
}

func (m *AppModel) commandApprovalLayout(width int) commandApprovalViewLayout {
	if width <= 0 {
		width = 1
	}
	if m.commandApproval == nil || m.commandApproval.proposal == nil {
		return commandApprovalViewLayout{lines: []string{""}}
	}
	proposal := m.commandApproval.proposal
	th := m.config.Theme()
	promptStyle := lipgloss.NewStyle().Foreground(th.Palette.Secondary).Bold(true)
	layout := commandApprovalViewLayout{
		lines:      []string{},
		codeStartY: -1,
		codeEndY:   -1,
	}
	options := commandApprovalOptionsForProposal(proposal)
	layout.lines = append(layout.lines,
		promptStyle.Render(commandApprovalPromptLine(proposal)),
		"",
	)
	optionLines := make([][]string, len(options))
	optionHitboxes := make([][]commandApprovalHitbox, len(options))
	optionLineCount := 0
	for idx, option := range options {
		renderedLines, hitboxes := m.renderCommandApprovalOption(idx, option, width)
		optionLines[idx] = renderedLines
		optionHitboxes[idx] = hitboxes
		optionLineCount += len(renderedLines)
	}
	codeLines := renderCommandApprovalCodeBlock(proposal, width, th)
	maxBodyLines := max(m.height-statusBarHeight-mainMinContentHeight-inputBorderSize, 1)
	availableForCode := max(maxBodyLines-len(layout.lines)-optionLineCount, 0)
	codeSpacerLines := 0
	if availableForCode > 1 && len(codeLines) > 0 {
		codeSpacerLines = 1
	}
	codeBudget := min(max(availableForCode-codeSpacerLines, 0), commandApprovalMaxVisibleCodeLines)
	visibleCodeLines, appliedScroll := visibleCommandApprovalCodeLines(codeLines, codeBudget, m.commandApproval.codeScroll)
	layout.codeTotalLines = len(codeLines)
	layout.codeVisibleLines = len(visibleCodeLines)
	layout.codeScroll = appliedScroll
	if len(visibleCodeLines) > 0 {
		layout.codeStartY = len(layout.lines)
		layout.lines = append(layout.lines, visibleCodeLines...)
		layout.codeEndY = len(layout.lines)
		if codeSpacerLines > 0 {
			layout.lines = append(layout.lines, "")
		}
	}
	for idx := range options {
		baseY := len(layout.lines)
		layout.lines = append(layout.lines, optionLines[idx]...)
		for _, hitbox := range optionHitboxes[idx] {
			hitbox.y += baseY
			layout.hitboxes = append(layout.hitboxes, hitbox)
		}
	}
	return layout
}

func visibleCommandApprovalCodeLines(lines []string, budget, scroll int) ([]string, int) {
	if budget <= 0 || len(lines) == 0 {
		return nil, 0
	}
	if len(lines) <= budget {
		return append([]string(nil), lines...), 0
	}
	maxScroll := len(lines) - budget
	appliedScroll := clampInt(scroll, 0, maxScroll)
	return append([]string(nil), lines[appliedScroll:appliedScroll+budget]...), appliedScroll
}

func (l commandApprovalViewLayout) optionAt(contentX, contentY int) (int, bool) {
	for _, hitbox := range l.hitboxes {
		if contentY != hitbox.y {
			continue
		}
		if contentX < hitbox.x0 || contentX >= hitbox.x1 {
			continue
		}
		return hitbox.option, true
	}
	return 0, false
}

func (l commandApprovalViewLayout) commandApprovalCodeContains(contentX, contentY int) bool {
	return contentX >= 0 && contentY >= l.codeStartY && contentY < l.codeEndY
}

func (m *AppModel) scrollCommandApprovalCode(layout commandApprovalViewLayout, delta int) bool {
	if m.commandApproval == nil || delta == 0 || layout.codeVisibleLines <= 0 || layout.codeTotalLines <= layout.codeVisibleLines {
		return false
	}
	maxScroll := layout.codeTotalLines - layout.codeVisibleLines
	next := clampInt(layout.codeScroll+delta, 0, maxScroll)
	if next == m.commandApproval.codeScroll {
		return false
	}
	m.commandApproval.codeScroll = next
	return true
}

func (m *AppModel) layerDecisionLayout(width int) commandApprovalViewLayout {
	if width <= 0 {
		width = 1
	}
	if m.layerDecision == nil || m.layerDecision.request == nil {
		return commandApprovalViewLayout{lines: []string{""}}
	}
	req := m.layerDecision.request
	th := m.config.Theme()
	titleLines := markdownpkg.RenderMarkdown(
		fmt.Sprintf("**DAG %s layer %d has blocking failures.**", req.DAGID, req.LayerIdx),
		width,
		th,
	)
	if len(titleLines) == 0 {
		titleLines = []string{""}
	}
	layout := commandApprovalViewLayout{lines: append([]string(nil), titleLines...)}
	optionLines := make([][]string, len(layerDecisionOptions))
	optionHitboxes := make([][]commandApprovalHitbox, len(layerDecisionOptions))
	optionLineCount := 0
	for idx, option := range layerDecisionOptions {
		renderedLines, hitboxes := m.renderLayerDecisionOption(idx, option, width)
		optionLines[idx] = renderedLines
		optionHitboxes[idx] = hitboxes
		optionLineCount += len(renderedLines)
	}
	maxBodyLines := max(m.height-statusBarHeight-mainMinContentHeight-inputBorderSize, 1)
	failureBudget := maxBodyLines - len(layout.lines) - optionLineCount
	if failureBudget > 0 {
		layout.lines = append(layout.lines, renderLayerDecisionFailures(req.FailedNodes, width, failureBudget, th)...)
	}
	for idx := range layerDecisionOptions {
		baseY := len(layout.lines)
		layout.lines = append(layout.lines, optionLines[idx]...)
		for _, hitbox := range optionHitboxes[idx] {
			hitbox.y += baseY
			layout.hitboxes = append(layout.hitboxes, hitbox)
		}
	}
	return layout
}

func (m *AppModel) renderCommandApprovalOption(index int, option commandApprovalOption, width int) ([]string, []commandApprovalHitbox) {
	th := m.config.Theme()
	selected := m.commandApproval != nil && m.commandApproval.selected == index
	activated := m.commandApproval != nil && m.commandApproval.activated == index
	renderedLines := renderCommandApprovalOptionLines(option, width, th, selected, activated)
	hitboxes := make([]commandApprovalHitbox, 0, len(renderedLines))
	for i, line := range renderedLines {
		stripped := ansi.Strip(line)
		visibleW := lipgloss.Width(stripped)
		hitboxes = append(hitboxes, commandApprovalHitbox{
			option: index,
			y:      i,
			x0:     0,
			x1:     visibleW,
		})
	}
	return renderedLines, hitboxes
}

type styledApprovalToken struct {
	text  string
	style lipgloss.Style
}

func renderCommandApprovalOptionLines(
	option commandApprovalOption,
	width int,
	th *theme.Theme,
	selected bool,
	activated bool,
) []string {
	if width <= 0 {
		width = 40
	}

	palette := th.Palette
	bulletStyle := lipgloss.NewStyle().Foreground(palette.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(palette.Foreground)
	hintStyle := lipgloss.NewStyle().Foreground(palette.Subtext)
	if selected {
		bulletStyle = bulletStyle.Foreground(palette.Secondary)
		labelStyle = labelStyle.Foreground(palette.Secondary)
		hintStyle = hintStyle.Foreground(palette.Primary)
	}
	if activated {
		bulletStyle = bulletStyle.Bold(true)
		labelStyle = labelStyle.Bold(true)
		hintStyle = hintStyle.Bold(true)
	}

	tokens := []styledApprovalToken{{text: "•", style: bulletStyle}}
	for _, token := range strings.Fields(strings.TrimSpace(option.label)) {
		tokens = append(tokens, styledApprovalToken{text: token, style: labelStyle})
	}
	if hint := strings.TrimSpace(option.hint); hint != "" {
		for _, token := range strings.Fields("(" + hint + ")") {
			tokens = append(tokens, styledApprovalToken{text: token, style: hintStyle})
		}
	}
	return wrapStyledApprovalTokens(tokens, width)
}

func wrapStyledApprovalTokens(tokens []styledApprovalToken, width int) []string {
	if len(tokens) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 1)
	current := make([]styledApprovalToken, 0, len(tokens))
	currentWidth := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		parts := make([]string, 0, len(current))
		for _, token := range current {
			parts = append(parts, token.style.Render(token.text))
		}
		lines = append(lines, strings.Join(parts, " "))
		current = current[:0]
		currentWidth = 0
	}
	for _, token := range tokens {
		tokenWidth := lipgloss.Width(token.text)
		if len(current) == 0 {
			current = append(current, token)
			currentWidth = tokenWidth
			continue
		}
		nextWidth := currentWidth + 1 + tokenWidth
		if nextWidth > width {
			flush()
			current = append(current, token)
			currentWidth = tokenWidth
			continue
		}
		current = append(current, token)
		currentWidth = nextWidth
	}
	flush()
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m *AppModel) renderLayerDecisionOption(index int, option commandApprovalOption, width int) ([]string, []commandApprovalHitbox) {
	th := m.config.Theme()
	selected := m.layerDecision != nil && m.layerDecision.selected == index
	activated := m.layerDecision != nil && m.layerDecision.activated == index
	renderedLines := renderLayerDecisionOptionLines(option, width, th, selected, activated)
	hitboxes := make([]commandApprovalHitbox, 0, len(renderedLines))
	for i, line := range renderedLines {
		stripped := ansi.Strip(line)
		visibleW := lipgloss.Width(stripped)
		hitboxes = append(hitboxes, commandApprovalHitbox{
			option: index,
			y:      i,
			x0:     0,
			x1:     visibleW,
		})
	}
	return renderedLines, hitboxes
}

func renderLayerDecisionOptionLines(
	option commandApprovalOption,
	width int,
	th *theme.Theme,
	selected bool,
	activated bool,
) []string {
	if width <= 0 {
		width = 40
	}

	palette := th.Palette
	bulletStyle := lipgloss.NewStyle().Foreground(palette.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(palette.Foreground)
	hintStyle := lipgloss.NewStyle().Foreground(palette.Subtext)
	if selected {
		bulletStyle = bulletStyle.Foreground(palette.Secondary)
		labelStyle = labelStyle.Foreground(palette.Secondary)
		hintStyle = hintStyle.Foreground(palette.Primary)
	}
	if activated {
		bulletStyle = bulletStyle.Bold(true)
		labelStyle = labelStyle.Bold(true)
		hintStyle = hintStyle.Bold(true)
	}

	tokens := []styledApprovalToken{{text: "•", style: bulletStyle}}
	for _, token := range strings.Fields(strings.TrimSpace(option.label)) {
		tokens = append(tokens, styledApprovalToken{text: token, style: labelStyle})
	}
	for _, token := range strings.Fields(strings.TrimSpace(option.hint)) {
		tokens = append(tokens, styledApprovalToken{text: token, style: hintStyle})
	}
	return wrapStyledApprovalTokens(tokens, width)
}

func renderCommandApprovalCodeBlock(proposal *commandapproval.Proposal, width int, th *theme.Theme) []string {
	command := ""
	if proposal != nil {
		command = strings.TrimSpace(proposal.Command)
	}
	fenceLang := "sh"
	if proposal != nil && proposal.IsFetchApproval() {
		fenceLang = "text"
	}
	rendered := markdownpkg.RenderMarkdown("```"+fenceLang+"\n"+command+"\n```", width, th)
	if len(rendered) >= 2 && commandApprovalMarkdownLineEmpty(rendered[0]) {
		rendered = rendered[1:]
	}
	if len(rendered) >= 2 && commandApprovalMarkdownLineEmpty(rendered[len(rendered)-1]) {
		rendered = rendered[:len(rendered)-1]
	}
	return rendered
}

func commandApprovalMarkdownLineEmpty(line string) bool {
	return strings.TrimSpace(ansi.Strip(line)) == ""
}

func commandApprovalOptionMarkdown(option commandApprovalOption) string {
	hint := strings.TrimSpace(option.hint)
	if hint == "" {
		return "- " + option.label
	}
	return "- " + option.label + " (" + hint + ")"
}

func commandApprovalOptionsForProposal(proposal *commandapproval.Proposal) []commandApprovalOption {
	if proposal != nil && proposal.IsFetchApproval() {
		return fetchApprovalOptions
	}
	return commandApprovalOptions
}

func commandApprovalCancelOptionIndex(proposal *commandapproval.Proposal) int {
	options := commandApprovalOptionsForProposal(proposal)
	if len(options) == 0 {
		return 0
	}
	switch {
	case len(options) > 2:
		return 2
	default:
		return len(options) - 1
	}
}

func commandApprovalPromptLine(proposal *commandapproval.Proposal) string {
	requester := commandApprovalRequesterName(proposal)
	if proposal != nil && proposal.IsFetchApproval() {
		return requester + " wants approval to fetch:"
	}
	return requester + " wants approval for:"
}

func commandApprovalDecisionApproved(proposal *commandapproval.Proposal, decision string) bool {
	decision = strings.TrimSpace(strings.ToLower(decision))
	return strings.HasPrefix(decision, "allow_")
}

func commandApprovalDecisionReason(proposal *commandapproval.Proposal, decision string, approved bool) string {
	if approved {
		return "approved by user"
	}
	return "denied by user"
}

func commandApprovalRequesterName(proposal *commandapproval.Proposal) string {
	if proposal == nil {
		return "Agent"
	}
	value := strings.TrimSpace(proposal.AgentType)
	if value == "" {
		value = strings.TrimSpace(proposal.AgentID)
	}
	if value == "" {
		return "Agent"
	}
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "_", " ")
	words := strings.Fields(value)
	for i, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) == 0 {
			continue
		}
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		words[i] = string(runes)
	}
	if len(words) == 0 {
		return "Agent"
	}
	return strings.Join(words, " ")
}

func renderLayerDecisionFailures(failures []msg.LayerFailedNode, width, maxLines int, th *theme.Theme) []string {
	if maxLines <= 0 || len(failures) == 0 {
		return nil
	}
	lines := make([]string, 0, min(maxLines, len(failures)))
	for i, failure := range failures {
		rendered := markdownpkg.RenderMarkdown("- "+layerDecisionFailureSummary(failure), width, th)
		if len(lines)+len(rendered) > maxLines {
			omitted := len(failures) - i
			return appendLayerDecisionOverflow(lines, omitted, width, maxLines, th)
		}
		lines = append(lines, rendered...)
	}
	return lines
}

func appendLayerDecisionOverflow(lines []string, omitted, width, maxLines int, th *theme.Theme) []string {
	if omitted <= 0 || maxLines <= 0 {
		return lines
	}
	notice := markdownpkg.RenderMarkdown(fmt.Sprintf("_%d more blocking failure(s) omitted._", omitted), width, th)
	if len(notice) > maxLines {
		return notice[:maxLines]
	}
	if len(lines)+len(notice) > maxLines {
		lines = lines[:max(0, maxLines-len(notice))]
	}
	return append(lines, notice...)
}

func layerDecisionFailureSummary(failure msg.LayerFailedNode) string {
	name := strings.TrimSpace(failure.NodeName)
	if name == "" {
		name = strings.TrimSpace(failure.NodeID)
	}
	if name == "" {
		name = "unknown node"
	}
	if agentType := strings.TrimSpace(failure.AgentType); agentType != "" {
		name += " [" + agentType + "]"
	}
	errText := strings.TrimSpace(failure.Error)
	if errText == "" {
		errText = "failed with no error details"
	}
	return name + ": " + errText
}

func enforceLineCountToHeight(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) == n {
		return s
	}
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	var b strings.Builder
	b.WriteString(s)
	for i := 0; i < n-len(lines); i++ {
		b.WriteByte('\n')
	}
	return b.String()
}

// correctOverflow reduces queueH and inputH so the total frame height
// (mainH + queueH + inputH + statusBarHeight) does not exceed termH.
// Steals from queue first (can reach 0), then shrinks input (minimum
// inputBorderSize). Returns corrected queueH and inputH.
func correctOverflow(mainH, queueH, inputH, termH int) (int, int) {
	overflow := mainH + queueH + inputH + statusBarHeight - termH
	if overflow <= 0 {
		return queueH, inputH
	}
	steal := min(overflow, queueH)
	queueH -= steal
	overflow -= steal
	if overflow > 0 {
		inputH = max(inputH-overflow, inputBorderSize)
	}
	return queueH, inputH
}

func (m *AppModel) recalcLayout() {
	// Reserve space for queue strip, input (dynamic), and status bar.
	queueH := m.promptQueue.ViewHeight()
	inputH := m.inputHeight()
	mainHeight := m.height - queueH - inputH - statusBarHeight
	mainHeight = max(mainHeight, 1)

	// Budget correction: when mainHeight is clamped to 1 the total may
	// exceed m.height. Reduce queue/input to compensate.
	queueH, inputH = correctOverflow(mainHeight, queueH, inputH, m.height)

	m.layout.SetSize(m.width, mainHeight)

	// Sync tab order and focus to the current layout mode so collapsed
	// panels are excluded from keyboard navigation.
	m.syncViewState()

	// Center panel: chat. Subtract border so content fits inside renderPanel().
	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	newChatViewH := max(chatH-panelBorderSize, 1)

	m.chat.SetSize(max(chatW-panelBorderSize, 1), newChatViewH)

	// Left panel: split between session (top) and agent (bottom).
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerLeftW := max(leftW-panelBorderSize, 1)
	sessionPreferredH := m.sessionPanel.PreferredHeight(max(leftH-panelBorderSize-leftPanelOverhead, 1))
	sections := computeLeftPanelSections(leftW, leftH, m.agentPanel.SelectorLineCount(), sessionPreferredH)
	m.leftPanelSections = sections
	sessionH := sections.sessionRect.H
	agentH := sections.agentsRect.H
	m.sessionPanel.SetSize(innerLeftW, sessionH)
	m.agentPanel.SetSize(innerLeftW, max(agentH, 1))

	// File tree panel.
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	m.fileTree.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))

	// Right panel: code viewer (and knowledge, same dimensions).
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.codePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	m.knowledgePanel.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	m.resizeCodePanelForPreview(rightW, rightH)

	// Git mode panels reuse file tree and code viewer slot dimensions.
	if m.gitPanel != nil {
		m.gitPanel.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
		m.commitTree.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
	}
	if m.diffView != nil {
		m.diffView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.diffView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.mergeDiffView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}
	if m.conflictView != nil {
		m.conflictView.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
		m.conflictView.SetFileListSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))
	}

	// Input: dynamic height based on content.
	m.input.SetSize(m.width, inputH)
	m.statusBar.SetSize(m.width, statusBarHeight)

	// Overlays get full terminal dimensions.
	m.editorOverlay.SetSize(m.width, m.height)
	m.modalOverlay.SetSize(m.width, m.height)
	m.searchOverlay.SetSize(m.width, m.height)
	m.fieldManualOverlay.SetSize(m.width, m.height)
	m.loginPanel.SetSize(m.width, mainHeight)

	// Full invalidation — section + per-slot caches cleared.
	m.comp.SetStructure(m.compositorColumns(), mainHeight, queueH, inputH, statusBarHeight)
	if m.slotBodyCache != nil {
		clear(m.slotBodyCache)
	}
	if m.slotBorderOnlyDirty != nil {
		clear(m.slotBorderOnlyDirty)
	}
	m.prevInputH = inputH
}

// handleInputGrowth performs a targeted layout update when the input panel
// grows (user added lines). Only the chat slot and input are re-rendered;
// side panels keep their cached output truncated to the new main-area
// height with the bottom border preserved. This limits the terminal diff
// to ~4-5 boundary rows instead of a full-screen redraw.
func (m *AppModel) handleInputGrowth(newInputH int) {
	queueH := m.promptQueue.ViewHeight()
	mainHeight := m.height - queueH - newInputH - statusBarHeight
	mainHeight = max(mainHeight, 1)

	// Update layout to get correct chat panel dimensions.
	m.layout.SetSize(m.width, mainHeight)

	chatW, chatH := m.layout.GetPanelSize(component.FocusChat)
	newChatViewH := max(chatH-panelBorderSize, 1)

	m.chat.SetSize(max(chatW-panelBorderSize, 1), newChatViewH)

	// Resize input to new height.
	m.input.SetSize(m.width, newInputH)

	// NOTE: left/right/filetree/session/agent panels are deliberately NOT
	// resized. Their cached compositor output is truncated below, keeping
	// their rendered content byte-identical for all rows except the border.

	// Determine which compositor slot contains the chat panel.
	chatSlot := m.chatCompositorSlot()

	// Targeted compositor update — only chat slot + queue + input dirty.
	m.comp.AdjustVerticalSections(mainHeight, queueH, newInputH, chatSlot)

	// Truncate every main-area side-panel slot EXCEPT the chat slot.
	for _, slot := range []compositor.SlotID{
		compositor.SlotLeft,
		compositor.SlotCenterLeft,
		compositor.SlotCenter,
		compositor.SlotRight,
	} {
		if slot != chatSlot {
			m.comp.TruncateSlot(slot, mainHeight)
		}
	}
	if m.slotBodyCache != nil {
		clear(m.slotBodyCache)
	}
	if m.slotBorderOnlyDirty != nil {
		clear(m.slotBorderOnlyDirty)
	}

	m.prevInputH = newInputH
}

// chatCompositorSlot returns the compositor slot that holds the chat panel
// in the current layout mode.
func (m *AppModel) chatCompositorSlot() compositor.SlotID {
	switch m.layout.Mode() {
	case layout.TwoColumn:
		return compositor.SlotRight
	case layout.SingleColumn:
		return compositor.SlotLeft
	default:
		return compositor.SlotCenter
	}
}

// resizeCodePanelForPreview computes sizes for all panes within the code
// panel slot using the pane tree's ComputeLayout.
func (m *AppModel) resizeCodePanelForPreview(rightW, rightH int) {
	if m.viewMode != ViewEdit {
		return
	}
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)

	// Full preview mode: preview active, single editor pane with no tabs.
	if m.hasPreview() && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0 {
		m.previewPanel.SetSize(innerW, max(innerH-tabbar.Height, 1))
		return
	}

	// Full markdown preview mode: mdPreview active, sole editor has no tabs.
	if m.isFullMdPreview() {
		m.mdPreviewPanel.SetSize(innerW, max(innerH-tabbar.Height, 1))
		return
	}

	// Single editor pane, no splits.
	if m.paneTree.IsLeaf() {
		m.focusedEditor().SetSize(innerW, max(innerH-m.tabBarHeight(), 1))
		return
	}

	// Multi-pane: compute layout and apply sizes.
	contentH := max(innerH-1, 1) // Reserve status line row.
	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	rects := m.paneTree.ComputeLayout(area)

	for id, r := range rects {
		if id == m.previewPane {
			m.previewPanel.SetSize(r.W, r.H-tabbar.Height)
			continue
		}
		if id == m.mdPreviewPane {
			m.mdPreviewPanel.SetSize(r.W, max(r.H-tabbar.Height, 1))
			continue
		}
		ps := m.paneEditors[id]
		tbH := 0
		if len(ps.tabOrder) > 0 {
			tbH = tabbar.Height
		}
		// +1 because editor.viewportHeight() subtracts 1 for the status
		// line, which is rendered separately at the bottom.
		ps.editor.SetSize(r.W, r.H-tbH+1)
	}
}

// syncViewState rebuilds the dual view cycling rings for the current layout
// mode, updates the focus tab order to match visible panels, and pushes the
// ring indicator to the status bar. Called on layout recompute and after cycling.
type viewRingPlan struct {
	left  []component.FocusID
	right []component.FocusID
}

var viewRingPlans = map[layout.LayoutMode]viewRingPlan{
	layout.FourColumn: {
		left: []component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusChat,
			component.FocusCodeViewer,
			component.FocusGitPanel,
			component.FocusCommitTree,
		},
	},
	layout.ThreeColumn: {
		left: []component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusGitPanel,
		},
		right: []component.FocusID{
			component.FocusCodeViewer,
			component.FocusCommitTree,
		},
	},
	layout.TwoColumn: {
		left: []component.FocusID{
			component.FocusSessionPanel,
			component.FocusFileTree,
			component.FocusGitPanel,
		},
		right: []component.FocusID{
			component.FocusChat,
			component.FocusCodeViewer,
			component.FocusCommitTree,
		},
	},
	layout.SingleColumn: {
		left: []component.FocusID{
			component.FocusSessionPanel,
			component.FocusChat,
			component.FocusFileTree,
			component.FocusCodeViewer,
			component.FocusGitPanel,
			component.FocusCommitTree,
		},
	},
}

func (m *AppModel) syncViewState() {
	mode := m.layout.Mode()
	wasEmpty := m.viewRingsEmpty()
	m.resetViewRings(mode)
	m.syncViewModeRingSelection()
	m.applyViewRingOverrides()
	m.showCollapseHintIfNeeded(wasEmpty)
	m.updateViewFocusOrder(mode)
}

func (m *AppModel) viewRingsEmpty() bool {
	return m.leftRing.empty() && m.rightRing.empty()
}

func (m *AppModel) resetViewRings(mode layout.LayoutMode) {
	plan := viewRingPlanFor(mode)
	m.leftRing.reset(cloneFocusIDs(plan.left))
	m.rightRing.reset(cloneFocusIDs(plan.right))
}

func viewRingPlanFor(mode layout.LayoutMode) viewRingPlan {
	plan, ok := viewRingPlans[mode]
	if ok {
		return plan
	}
	return viewRingPlans[layout.SingleColumn]
}

func cloneFocusIDs(ids []component.FocusID) []component.FocusID {
	return append([]component.FocusID(nil), ids...)
}

func (m *AppModel) syncViewModeRingSelection() {
	switch m.viewMode {
	case ViewChat:
		m.syncChatRingSelection()
	case ViewEdit:
		m.syncEditRingSelection()
	case ViewGit:
		m.syncGitRingSelection()
	}
}

func (m *AppModel) syncChatRingSelection() {
	m.positionRing(&m.rightRing, component.FocusChat)
}

func (m *AppModel) syncEditRingSelection() {
	m.positionRing(&m.rightRing, component.FocusCodeViewer)
	if m.positionRing(&m.leftRing, component.FocusCodeViewer) {
		return
	}
	m.positionRing(&m.leftRing, component.FocusFileTree)
}

func (m *AppModel) syncGitRingSelection() {
	m.positionRingOnGit(&m.leftRing, component.FocusGitPanel)
	m.positionRingOnGit(&m.rightRing, component.FocusCommitTree)
}

func (m *AppModel) positionRing(r *viewRing, id component.FocusID) bool {
	if r.empty() {
		return false
	}
	return r.setTo(id)
}

func (m *AppModel) positionRingOnGit(r *viewRing, id component.FocusID) {
	if r.empty() || isGitPanel(r.current()) {
		return
	}
	r.setTo(id)
}

func (m *AppModel) applyViewRingOverrides() {
	m.applyDiffRingOverrides()
	m.applyMergeDiffRingOverrides()
	m.applyConflictRingOverrides()
}

func (m *AppModel) applyDiffRingOverrides() {
	if !m.diffViewActive {
		return
	}
	m.replaceCommitAndGitPanels(component.FocusDiffView, component.FocusDiffFileList)
}

func (m *AppModel) applyMergeDiffRingOverrides() {
	if !m.mergeDiffViewActive {
		return
	}
	m.replaceCommitAndGitPanels(component.FocusMergeDiffView, component.FocusMergeDiffFileList)
}

func (m *AppModel) applyConflictRingOverrides() {
	if !m.conflictViewActive {
		return
	}
	m.replaceCommitAndGitPanels(component.FocusConflictView, component.FocusConflictFileList)
}

func (m *AppModel) replaceCommitAndGitPanels(commitID, fileListID component.FocusID) {
	replaceInRing(&m.leftRing, component.FocusCommitTree, commitID)
	replaceInRing(&m.rightRing, component.FocusCommitTree, commitID)
	replaceInRing(&m.leftRing, component.FocusGitPanel, fileListID)
	replaceInRing(&m.rightRing, component.FocusGitPanel, fileListID)
}

func (m *AppModel) showCollapseHintIfNeeded(wasEmpty bool) {
	if !wasEmpty || m.viewRingsEmpty() || m.collapseHintShown {
		return
	}
	m.statusBar.SetFlash("Panel collapsed — Alt+V ←/→ to cycle views")
	m.collapseHintShown = true
}

func (m *AppModel) updateViewFocusOrder(mode layout.LayoutMode) {
	m.focus.SetTabOrder(m.tabOrderForView(mode))
	m.syncFocusState()
	m.statusBar.SetViewRingHint(m.buildRingHint())
}

// tabOrderForView returns the focus cycling order for the current mode and
// ring state. Ring-active panels replace fixed panels in the order.
func (m *AppModel) tabOrderForView(mode layout.LayoutMode) []component.FocusID {
	switch mode {
	case layout.FourColumn:
		order := []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			component.FocusSessionPanel,
			component.FocusAgentPanel,
			component.FocusFileTree,
		}
		return m.appendCodePanelFocus(order)
	case layout.ThreeColumn:
		left := m.leftRing.current()
		order := []component.FocusID{
			component.FocusInput,
			component.FocusChat,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	case layout.TwoColumn:
		left := m.leftRing.current()
		right := m.rightRing.current()
		order := []component.FocusID{
			component.FocusInput,
			right,
			left,
		}
		if left == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	default:
		// SingleColumn: Input + whatever the left ring shows.
		active := m.leftRing.current()
		order := []component.FocusID{component.FocusInput, active}
		if active == component.FocusSessionPanel {
			order = append(order, component.FocusAgentPanel)
		}
		return m.appendCodePanelFocus(order)
	}
}

// appendCodePanelFocus appends the code panel's focus target to the tab
// order. In edit mode, this is the focused pane's dynamic ID (replacing
// any FocusCodeViewer entry from ring state). In non-edit mode, this is
// FocusCodeViewer. Preview-only mode (no editor tabs) skips the tab order
// since Tab cannot meaningfully target a read-only preview.
func (m *AppModel) appendCodePanelFocus(order []component.FocusID) []component.FocusID {
	switch m.viewMode {
	case ViewGit:
		return m.appendGitCodePanelFocus(order)
	case ViewEdit:
		return m.appendEditCodePanelFocus(order)
	default:
		return appendFocusIfMissing(order, component.FocusCodeViewer)
	}
}

func (m *AppModel) appendGitCodePanelFocus(order []component.FocusID) []component.FocusID {
	for i, id := range order {
		order[i] = m.remapGitCodePanelFocus(id)
	}
	return appendFocusIfMissing(order, m.gitContentFocus())
}

func (m *AppModel) remapGitCodePanelFocus(id component.FocusID) component.FocusID {
	switch id {
	case component.FocusFileTree:
		return m.gitFileListFocus()
	case component.FocusCodeViewer, component.FocusDiffView, component.FocusMergeDiffView, component.FocusConflictView:
		return m.gitContentFocus()
	default:
		return id
	}
}

func (m *AppModel) gitFileListFocus() component.FocusID {
	switch {
	case m.conflictViewActive:
		return component.FocusConflictFileList
	case m.mergeDiffViewActive:
		return component.FocusMergeDiffFileList
	case m.diffViewActive:
		return component.FocusDiffFileList
	default:
		return component.FocusGitPanel
	}
}

func (m *AppModel) gitContentFocus() component.FocusID {
	switch {
	case m.conflictViewActive:
		return component.FocusConflictView
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		return pane.PaneFocusID(m.mergeDiffView.FocusedPane())
	case m.diffViewActive && m.diffView != nil:
		return pane.PaneFocusID(m.diffView.FocusedPane())
	default:
		return component.FocusCommitTree
	}
}

func (m *AppModel) appendEditCodePanelFocus(order []component.FocusID) []component.FocusID {
	if m.hasPreview() && len(m.focusedTabOrder()) == 0 {
		return order
	}
	return m.replaceOrAppendCodeFocus(order, m.editCodePanelFocus())
}

func (m *AppModel) editCodePanelFocus() component.FocusID {
	if m.isFullMdPreview() {
		return pane.PaneFocusID(m.mdPreviewPane)
	}
	return pane.PaneFocusID(m.focusedPane)
}

func (m *AppModel) replaceOrAppendCodeFocus(order []component.FocusID, fid component.FocusID) []component.FocusID {
	for i, id := range order {
		if id == component.FocusCodeViewer {
			order[i] = fid
			return order
		}
	}
	return append(order, fid)
}

func appendFocusIfMissing(order []component.FocusID, fid component.FocusID) []component.FocusID {
	if slices.Contains(order, fid) {
		return order
	}
	return append(order, fid)
}

// panelDisplayNames maps panel IDs to short labels for the status bar ring.
var panelDisplayNames = map[component.FocusID]string{
	component.FocusChat:         "Chat",
	component.FocusCodeViewer:   "Code",
	component.FocusSessionPanel: "Sess",
	component.FocusFileTree:     "Files",
	component.FocusGitPanel:     "Git",
	component.FocusCommitTree:   "Tree",
	component.FocusDiffView:     "Diff",
	component.FocusDiffFileList: "Files",
}

// buildRingHint returns the formatted ring indicator string for the status bar.
// Returns "" when both rings are empty (FourColumn).
// When both rings are active, shows: ◀ Sess ● Tree ○ | Chat ● Code ○ ▶
func (m *AppModel) buildRingHint() string {
	if m.leftRing.empty() && m.rightRing.empty() {
		return ""
	}
	th := m.config.Theme()
	currentStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	renderRing := func(ring *viewRing) []string {
		var items []string
		for i, pid := range ring.panels {
			name := panelDisplayNames[pid]
			var indicator string
			switch {
			case i == ring.index:
				indicator = currentStyle.Render(name + " \u25cf")
			case pid == component.FocusChat && m.chat.IsStreaming():
				indicator = activeStyle.Render(name + " \u2731")
			default:
				indicator = dimStyle.Render(name + " \u25cb")
			}
			items = append(items, indicator)
		}
		return items
	}

	var parts []string
	parts = append(parts, dimStyle.Render("\u25c0"))

	if !m.leftRing.empty() {
		parts = append(parts, renderRing(&m.leftRing)...)
	}

	if !m.leftRing.empty() && !m.rightRing.empty() {
		parts = append(parts, dimStyle.Render("|"))
	}

	if !m.rightRing.empty() {
		parts = append(parts, renderRing(&m.rightRing)...)
	}

	parts = append(parts, dimStyle.Render("\u25b6"))
	return strings.Join(parts, " ")
}

// compositorColumns returns the slot IDs for the current layout mode's columns.
func (m *AppModel) compositorColumns() []compositor.SlotID {
	switch m.layout.Mode() {
	case layout.FourColumn:
		return []compositor.SlotID{
			compositor.SlotLeft, compositor.SlotCenterLeft,
			compositor.SlotCenter, compositor.SlotRight,
		}
	case layout.ThreeColumn:
		return []compositor.SlotID{
			compositor.SlotLeft, compositor.SlotCenter, compositor.SlotRight,
		}
	case layout.TwoColumn:
		return []compositor.SlotID{compositor.SlotLeft, compositor.SlotRight}
	default:
		return []compositor.SlotID{compositor.SlotLeft}
	}
}

// focusBorderGroup maps the current focus to the compositor slot whose
// border would change color on focus transitions.
func (m *AppModel) focusBorderGroup() compositor.SlotID {
	fid := m.focus.Current()
	switch fid {
	case component.FocusSessionPanel, component.FocusAgentPanel:
		return compositor.SlotLeft
	case component.FocusFileTree:
		if m.layout.Mode() == layout.FourColumn {
			return compositor.SlotCenterLeft
		}
		return compositor.SlotLeft
	case component.FocusChat:
		switch m.layout.Mode() {
		case layout.TwoColumn:
			return compositor.SlotRight
		case layout.SingleColumn:
			return compositor.SlotLeft
		default:
			return compositor.SlotCenter
		}
	case component.FocusCodeViewer, component.FocusCommitTree, component.FocusDiffView:
		return compositor.SlotRight
	case component.FocusGitPanel, component.FocusDiffFileList:
		if m.layout.Mode() == layout.FourColumn {
			return compositor.SlotCenterLeft
		}
		return compositor.SlotLeft
	case component.FocusInput:
		return compositor.SlotInput
	default:
		if pane.IsPaneFocus(fid) {
			return compositor.SlotRight
		}
		return compositor.SlotCenter
	}
}

func (m *AppModel) ensureSlotCaches() {
	if m.slotBodyCache == nil {
		m.slotBodyCache = make(map[compositor.SlotID]string)
	}
	if m.slotBorderOnlyDirty == nil {
		m.slotBorderOnlyDirty = make(map[compositor.SlotID]bool)
	}
}

func (m *AppModel) invalidateRenderedSlots() {
	m.comp.InvalidateAll()
	if m.slotBodyCache != nil {
		clear(m.slotBodyCache)
	}
	if m.slotBorderOnlyDirty != nil {
		clear(m.slotBorderOnlyDirty)
	}
}

func (m *AppModel) markSlotDirty(id compositor.SlotID) {
	if id == 0 {
		return
	}
	if m.slotBorderOnlyDirty != nil {
		delete(m.slotBorderOnlyDirty, id)
	}
	m.comp.MarkDirty(id)
}

func (m *AppModel) markSlotBorderDirty(id compositor.SlotID) {
	if id == 0 {
		return
	}
	m.ensureSlotCaches()
	m.slotBorderOnlyDirty[id] = true
	m.comp.MarkDirty(id)
}

func (m *AppModel) consumeBorderOnlySlot(id compositor.SlotID) bool {
	if m.slotBorderOnlyDirty == nil || !m.slotBorderOnlyDirty[id] {
		return false
	}
	delete(m.slotBorderOnlyDirty, id)
	return true
}

func (m *AppModel) cacheSlotBody(id compositor.SlotID, content string) {
	m.ensureSlotCaches()
	m.slotBodyCache[id] = content
}

func (m *AppModel) cachedSlotBody(id compositor.SlotID) (string, bool) {
	if m.slotBodyCache == nil {
		return "", false
	}
	content, ok := m.slotBodyCache[id]
	return content, ok
}

// detectDirtySlots checks component dirty state and state transitions,
// marking the appropriate compositor slots for re-rendering.
func (m *AppModel) detectDirtySlots() {
	if m.invalidateForOverlayTransition() ||
		m.invalidateForEditModeTransition() ||
		m.invalidateForGitModeTransition() ||
		m.handleInputHeightTransition() {
		return
	}
	m.updateFocusBorderDirty()
	m.invalidateForChordChange()
	m.updateRingDirty()
	m.updateHoverDirty()
	m.detectChatDirtySlot()
	m.detectFileTreeDirtySlot()
	m.detectChromeDirtySlots()
	m.detectEditorDirtySlot()
	m.detectGitDirtySlots()
	m.detectDiffViewDirtySlots()
	m.detectConflictViewDirtySlots()
	m.detectMergeDiffViewDirtySlots()
	m.detectPreviewDirtySlot()
	m.detectBlinkDirtySlots()

	// Left panel (session+agent): no viewDirty — always mark if main dirty.
	// The compositor avoids re-rendering unless the slot is already dirty.
}

func (m *AppModel) invalidateForOverlayTransition() bool {
	if m.overlay == m.prevOverlay {
		return false
	}
	m.invalidateRenderedSlots()
	m.prevOverlay = m.overlay
	return true
}

func (m *AppModel) invalidateForEditModeTransition() bool {
	editMode := m.viewMode == ViewEdit
	if editMode == m.prevEditMode {
		return false
	}
	m.invalidateRenderedSlots()
	m.prevEditMode = editMode
	return true
}

func (m *AppModel) invalidateForGitModeTransition() bool {
	if (m.viewMode == ViewGit) == (m.prevGitMode == ViewGit) {
		return false
	}
	m.invalidateRenderedSlots()
	m.prevGitMode = m.viewMode
	return true
}

func (m *AppModel) handleInputHeightTransition() bool {
	newInputH := m.inputHeight()
	if newInputH == m.prevInputH {
		return false
	}
	if m.commandApproval != nil || m.layerDecision != nil {
		m.recalcLayout()
	} else if newInputH > m.prevInputH && m.comp.AllMainSlotsCached() {
		m.handleInputGrowth(newInputH)
	} else {
		m.recalcLayout()
	}
	return true
}

func (m *AppModel) updateFocusBorderDirty() {
	curGrp := m.focusBorderGroup()
	if curGrp != m.prevFocusGrp {
		m.markSlotDirty(m.prevFocusGrp)
		m.markSlotDirty(curGrp)
		m.prevFocusGrp = curGrp
	}
	curGrad := m.currentFocusGradient()
	if curGrad != m.focusGradient {
		m.focusGradient = curGrad
		m.markSlotDirty(curGrp)
	}
}

func (m *AppModel) invalidateForChordChange() {
	if m.chord == m.prevChord {
		return
	}
	m.invalidateRenderedSlots()
	m.prevChord = m.chord
}

func (m *AppModel) updateRingDirty() {
	if m.leftRing.index != m.prevLeftRing {
		m.markSlotDirty(compositor.SlotLeft)
		m.prevLeftRing = m.leftRing.index
	}
	if m.rightRing.index != m.prevRightRing {
		m.markSlotDirty(compositor.SlotRight)
		m.prevRightRing = m.rightRing.index
	}
}

func (m *AppModel) updateHoverDirty() {
	hoverKey := [5]int{m.tabHoverClose, int(m.tabHoverPane), m.previewTabHoverClose, m.mdPreviewTabHoverClose, m.mdTooltipTab}
	if hoverKey == m.prevHoverKey {
		return
	}
	m.markSlotDirty(compositor.SlotRight)
	m.prevHoverKey = hoverKey
}

func (m *AppModel) detectChatDirtySlot() {
	if !m.chat.ViewDirty() {
		return
	}
	m.markSlotDirty(m.chatSlot())
}

func (m *AppModel) chatSlot() compositor.SlotID {
	switch m.layout.Mode() {
	case layout.TwoColumn:
		return compositor.SlotRight
	case layout.SingleColumn:
		return compositor.SlotLeft
	default:
		return compositor.SlotCenter
	}
}

func (m *AppModel) detectFileTreeDirtySlot() {
	if !m.fileTree.ViewDirty() {
		return
	}
	m.markSlotDirty(m.sidebarFileListSlot())
}

func (m *AppModel) sidebarFileListSlot() compositor.SlotID {
	if m.layout.Mode() == layout.FourColumn {
		return compositor.SlotCenterLeft
	}
	return compositor.SlotLeft
}

func (m *AppModel) detectChromeDirtySlots() {
	if m.input.ViewDirty() {
		m.markSlotDirty(compositor.SlotInput)
	}
	if m.statusBar.ViewDirty() {
		m.markSlotDirty(compositor.SlotStatus)
	}
}

func (m *AppModel) detectEditorDirtySlot() {
	if m.viewMode != ViewEdit {
		return
	}
	if ps := m.paneEditors[m.focusedPane]; ps != nil && ps.editor.ViewDirty() {
		m.markSlotDirty(compositor.SlotRight)
	}
}

func (m *AppModel) detectGitDirtySlots() {
	if m.viewMode != ViewGit || m.diffViewActive || m.gitPanel == nil {
		return
	}
	m.syncStagedFiles()
	if m.gitPanel.ViewDirty() {
		m.markSlotDirty(m.sidebarFileListSlot())
	}
	if m.commitTree.ViewDirty() {
		m.markSlotDirty(compositor.SlotRight)
	}
}

func (m *AppModel) detectDiffViewDirtySlots() {
	if !m.diffViewActive || m.diffView == nil {
		return
	}
	if m.diffView.ViewDirty() {
		m.markSlotDirty(compositor.SlotRight)
	}
	if m.diffView.FileListDirty() {
		m.markSlotDirty(m.sidebarFileListSlot())
	}
}

func (m *AppModel) detectConflictViewDirtySlots() {
	if !m.conflictViewActive || m.conflictView == nil {
		return
	}
	if m.conflictView.ViewDirty() {
		m.markSlotDirty(compositor.SlotRight)
	}
	if m.conflictView.FileListDirty() {
		m.markSlotDirty(m.sidebarFileListSlot())
	}
}

func (m *AppModel) detectMergeDiffViewDirtySlots() {
	if !m.mergeDiffViewActive || m.mergeDiffView == nil {
		return
	}
	if m.mergeDiffView.ViewDirty() {
		m.markSlotDirty(compositor.SlotRight)
	}
	if m.mergeDiffView.FileListDirty() {
		m.markSlotDirty(m.sidebarFileListSlot())
	}
}

func (m *AppModel) detectPreviewDirtySlot() {
	if m.hasPreview() && m.isPreviewFocused() {
		m.markSlotDirty(compositor.SlotRight)
	}
}

func (m *AppModel) detectBlinkDirtySlots() {
	if !m.blinkDirty {
		return
	}
	m.blinkDirty = false
	if m.viewMode == ViewEdit {
		m.markSlotDirty(compositor.SlotRight)
	}
	if m.focus.Current() == component.FocusInput {
		m.markSlotDirty(compositor.SlotInput)
	}
	if m.fileTree.NeedsBlink() {
		m.markSlotDirty(m.sidebarFileListSlot())
	}
	if m.hasPreview() && m.isPreviewFocused() {
		m.markSlotDirty(compositor.SlotRight)
	}
}

// renderDirtySlots re-renders only the compositor slots that are dirty.
func (m *AppModel) renderDirtySlots() {
	th := m.config.Theme()
	m.applyFocusRingShimmer(th)

	if m.comp.IsDirty(compositor.SlotLeft) {
		m.comp.SetSlotLines(compositor.SlotLeft,
			compositor.SplitLines(m.renderSlotLeft(th)))
	}
	if m.comp.IsDirty(compositor.SlotCenterLeft) {
		m.comp.SetSlotLines(compositor.SlotCenterLeft,
			compositor.SplitLines(m.renderCacheableSlot(compositor.SlotCenterLeft, th)))
	}
	if m.comp.IsDirty(compositor.SlotCenter) {
		m.comp.SetSlotLines(compositor.SlotCenter,
			compositor.SplitLines(m.renderCacheableSlot(compositor.SlotCenter, th)))
	}
	if m.comp.IsDirty(compositor.SlotRight) {
		m.comp.SetSlotLines(compositor.SlotRight,
			compositor.SplitLines(m.renderCacheableSlot(compositor.SlotRight, th)))
	}
	if m.comp.IsDirty(compositor.SlotQueue) {
		m.comp.SetSlotLines(compositor.SlotQueue,
			compositor.SplitLines(m.renderQueueStrip()))
	}
	if m.comp.IsDirty(compositor.SlotInput) {
		m.comp.SetSlotLines(compositor.SlotInput,
			compositor.SplitLines(m.inputPanelView()))
	}
	if m.comp.IsDirty(compositor.SlotStatus) {
		m.comp.SetSlotLines(compositor.SlotStatus,
			compositor.SplitLines(m.statusBar.View()))
	}
}

func (m *AppModel) inputPanelView() string {
	if m.commandApproval != nil {
		return m.renderCommandApprovalView()
	}
	if m.layerDecision != nil {
		return m.renderLayerDecisionView()
	}
	return m.input.View(m.cursorVisible)
}

func (m *AppModel) renderCacheableSlot(id compositor.SlotID, th *theme.Theme) string {
	meta, ok := m.cacheableSlotBorderMeta(id)
	if !ok {
		return m.renderNonCacheableSlot(id, th)
	}
	if m.consumeBorderOnlySlot(id) {
		if content, ok := m.cachedSlotBody(id); ok {
			return m.renderBordered(content, meta.focused, meta.w, meta.h, th)
		}
	}
	content, ok := m.cacheableSlotContent(id, th)
	if !ok {
		return m.renderNonCacheableSlot(id, th)
	}
	m.cacheSlotBody(id, content)
	return m.renderBordered(content, meta.focused, meta.w, meta.h, th)
}

func (m *AppModel) renderNonCacheableSlot(id compositor.SlotID, th *theme.Theme) string {
	switch id {
	case compositor.SlotCenterLeft:
		return m.renderSlotCenterLeft(th)
	case compositor.SlotCenter:
		return m.renderSlotCenter(th)
	case compositor.SlotRight:
		return m.renderSlotRight(th)
	default:
		return ""
	}
}

func (m *AppModel) cacheableSlotContent(id compositor.SlotID, th *theme.Theme) (string, bool) {
	switch id {
	case compositor.SlotCenterLeft:
		return m.slotCenterLeftContent(th), true
	case compositor.SlotCenter:
		return m.slotCenterContent(th), true
	case compositor.SlotRight:
		return m.slotRightContent(th), true
	default:
		return "", false
	}
}

func (m *AppModel) cacheableSlotBorderMeta(id compositor.SlotID) (slotBorderMeta, bool) {
	switch id {
	case compositor.SlotCenterLeft:
		return m.slotCenterLeftBorderMeta(), true
	case compositor.SlotCenter:
		return m.slotCenterBorderMeta(), true
	case compositor.SlotRight:
		return m.slotRightBorderMeta(), true
	default:
		return slotBorderMeta{}, false
	}
}

func (m *AppModel) slotCenterContent(th *theme.Theme) string {
	return m.overlayChordHint(m.chat.View(), component.FocusChat, th)
}

func (m *AppModel) slotCenterBorderMeta() slotBorderMeta {
	w, h := m.layout.GetPanelSize(component.FocusChat)
	return slotBorderMeta{
		focused: m.focus.IsFocused(component.FocusChat),
		w:       w,
		h:       h,
	}
}

func (m *AppModel) slotCenterLeftContent(th *theme.Theme) string {
	switch {
	case m.conflictViewActive && m.conflictView != nil:
		return m.conflictView.FileListView(m.cursorVisible)
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		return m.mergeDiffView.FileListView(m.cursorVisible)
	case m.diffViewActive && m.diffView != nil:
		return m.diffView.FileListView(m.cursorVisible)
	case m.viewMode == ViewGit:
		return m.gitPanel.View(m.cursorVisible)
	default:
		return m.fileTree.View(m.cursorVisible)
	}
}

func (m *AppModel) slotCenterLeftBorderMeta() slotBorderMeta {
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	focused := m.focus.Current() == component.FocusFileTree
	switch {
	case m.conflictViewActive && m.conflictView != nil:
		focused = m.focus.Current() == component.FocusConflictFileList
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		focused = m.focus.Current() == component.FocusMergeDiffFileList
	case m.diffViewActive && m.diffView != nil:
		focused = m.focus.Current() == component.FocusDiffFileList
	case m.viewMode == ViewGit:
		focused = m.focus.Current() == component.FocusGitPanel
	}
	return slotBorderMeta{focused: focused, w: w, h: h}
}

func (m *AppModel) slotRightContent(th *theme.Theme) string {
	switch {
	case m.conflictViewActive && m.conflictView != nil:
		return m.overlayBlockedChord(m.overlayChordHint(m.conflictView.View(m.cursorVisible), component.FocusCodeViewer, th))
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		return m.overlayBlockedChord(m.overlayChordHint(m.mergeDiffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	case m.diffViewActive:
		return m.overlayBlockedChord(m.overlayChordHint(m.diffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	}
	if m.layout.Mode() == layout.TwoColumn {
		right := m.rightRing.current()
		switch right {
		case component.FocusCodeViewer:
			return m.overlayChordHint(m.codePanelView(), component.FocusCodeViewer, th)
		case component.FocusCommitTree:
			return m.overlayBlockedChord(m.overlayChordHint(m.commitTree.View(m.cursorVisible), component.FocusCodeViewer, th))
		default:
			return m.overlayChordHint(m.panelContent(right), right, th)
		}
	}
	if m.viewMode == ViewGit {
		return m.overlayBlockedChord(m.overlayChordHint(m.commitTree.View(m.cursorVisible), component.FocusCodeViewer, th))
	}
	return m.overlayChordHint(m.codePanelView(), component.FocusCodeViewer, th)
}

func (m *AppModel) slotRightBorderMeta() slotBorderMeta {
	switch {
	case m.conflictViewActive && m.conflictView != nil:
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		return slotBorderMeta{focused: m.focus.Current() == component.FocusConflictView, w: w, h: h}
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		return slotBorderMeta{focused: m.isMergeDiffPaneFocused(), w: w, h: h}
	case m.diffViewActive:
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		return slotBorderMeta{focused: m.isDiffPaneFocused(), w: w, h: h}
	}
	if m.layout.Mode() == layout.TwoColumn {
		right := m.rightRing.current()
		switch right {
		case component.FocusCodeViewer:
			w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
			return slotBorderMeta{focused: m.isCodePanelFocused(), w: w, h: h}
		case component.FocusCommitTree:
			w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
			return slotBorderMeta{focused: m.focus.Current() == component.FocusCommitTree, w: w, h: h}
		default:
			w, h := m.layout.GetPanelSize(right)
			return slotBorderMeta{focused: m.focus.IsFocused(right), w: w, h: h}
		}
	}
	if m.viewMode == ViewGit {
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		return slotBorderMeta{focused: m.focus.Current() == component.FocusCommitTree, w: w, h: h}
	}
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return slotBorderMeta{focused: m.isCodePanelFocused(), w: w, h: h}
}

// applyFocusRingShimmer sets per-character gradient border rendering on the
// theme, replacing the old per-side approach that produced visible color
// jumps at corners. Called once per frame before any slot rendering.
func (m *AppModel) applyFocusRingShimmer(th *theme.Theme) {
	m.focusGradient = m.currentFocusGradient()
	if m.focusGradient == nil {
		return
	}
	elapsed := time.Since(m.focusRingStart)
	g := m.focusGradient

	th.ActiveBorderRender = func(content string, innerW, innerH, maxH int) string {
		return theme.RenderGradientBorder(content, g, elapsed, innerW, innerH, maxH)
	}
	if m.focus != nil && m.focus.Current() == component.FocusInput {
		m.input.SetFocusBorderRender(th.ActiveBorderRender)
	}
}

// renderQueueStrip renders the prompt queue between chat and input.
// Returns empty string when the queue is empty (0 lines).
func (m *AppModel) renderQueueStrip() string {
	if m.promptQueue.IsEmpty() {
		return ""
	}
	var grad *theme.Gradient
	if !m.promptQueue.IsPaused() {
		grad = m.queueGradient
	}
	elapsed := time.Since(m.focusRingStart) // Reuse app-level animation clock.
	pal := m.config.Theme().Palette
	return m.promptQueue.View(m.width, elapsed, grad, pal)
}

// renderSlotLeft renders the bordered content for the left column slot.
// In FourColumn mode this is always the session+agent panel. In collapsed
// modes the left ring determines which panel occupies this slot.
func (m *AppModel) renderSlotLeft(th *theme.Theme) string {
	switch m.layout.Mode() {
	case layout.FourColumn:
		return m.renderLeftPanelBordered(th)
	case layout.SingleColumn:
		return m.renderSingleColumnSlot(th)
	default:
		return m.renderLeftSlot(m.leftRing.current(), th)
	}
}

// renderSlotCenterLeft renders the bordered file tree for the center-left slot.
// In git mode, renders the git explorer panel instead.
func (m *AppModel) renderSlotCenterLeft(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictFileListBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffFileListBordered(th)
	}
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffFileListBordered(th)
	}
	if m.viewMode == ViewGit {
		return m.renderGitPanelBordered(th)
	}
	return m.renderPanel(m.fileTree.View(m.cursorVisible), component.FocusFileTree, th)
}

// renderSlotCenter renders the bordered chat for the center slot.
func (m *AppModel) renderSlotCenter(th *theme.Theme) string {
	return m.renderPanel(
		m.overlayChordHint(m.chat.View(), component.FocusChat, th),
		component.FocusChat, th)
}

// renderSlotRight renders the bordered code panel for the right slot.
// In TwoColumn mode the right ring may show chat, commit tree, or code.
// In git mode (non-TwoColumn), renders the commit tree instead of the code panel.
func (m *AppModel) renderSlotRight(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictViewBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffViewBordered(th)
	}
	if m.diffViewActive {
		return m.renderDiffViewBordered(th)
	}
	if m.layout.Mode() == layout.TwoColumn {
		right := m.rightRing.current()
		switch right {
		case component.FocusCodeViewer:
			return m.renderCodePanelBordered(th)
		case component.FocusCommitTree:
			return m.renderGitCommitTreeBordered(th)
		default:
			content := m.overlayChordHint(m.panelContent(right), right, th)
			return m.renderPanel(content, right, th)
		}
	}
	if m.viewMode == ViewGit {
		return m.renderGitCommitTreeBordered(th)
	}
	return m.renderCodePanelBordered(th)
}

// renderSingleColumnSlot renders the single visible panel in SingleColumn mode.
func (m *AppModel) renderSingleColumnSlot(th *theme.Theme) string {
	active := m.leftRing.current()
	if rendered, ok := m.renderSingleColumnOverlaySlot(active, th); ok {
		return rendered
	}
	switch active {
	case component.FocusSessionPanel:
		content := m.overlayChordHint(m.renderLeftPanel(th), active, th)
		return m.borderLeftPanel(content, th)
	case component.FocusCodeViewer:
		return m.renderCodePanelBordered(th)
	case component.FocusFileTree:
		return m.renderSingleColumnFileTreeSlot(active, th)
	case component.FocusGitPanel:
		return m.renderGitPanelBordered(th)
	case component.FocusCommitTree:
		return m.renderGitCommitTreeBordered(th)
	default:
		return m.renderSingleColumnDefaultSlot(active, th)
	}
}

func (m *AppModel) renderSingleColumnOverlaySlot(active component.FocusID, th *theme.Theme) (string, bool) {
	switch active {
	case component.FocusConflictView:
		return m.renderSingleColumnConflictViewSlot(th), true
	case component.FocusConflictFileList:
		return m.renderSingleColumnConflictFileListSlot(th), true
	case component.FocusMergeDiffView:
		return m.renderSingleColumnMergeDiffViewSlot(th), true
	case component.FocusMergeDiffFileList:
		return m.renderSingleColumnMergeDiffFileListSlot(th), true
	case component.FocusDiffView:
		return m.renderSingleColumnDiffViewSlot(th), true
	case component.FocusDiffFileList:
		return m.renderSingleColumnDiffFileListSlot(th), true
	default:
		return "", false
	}
}

func (m *AppModel) renderSingleColumnFileTreeSlot(active component.FocusID, th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictFileListBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffFileListBordered(th)
	}
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffFileListBordered(th)
	}
	content := m.overlayChordHint(m.fileTree.View(m.cursorVisible), active, th)
	return m.renderPanel(content, active, th)
}

func (m *AppModel) renderSingleColumnConflictViewSlot(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictViewBordered(th)
	}
	return m.renderCodePanelBordered(th)
}

func (m *AppModel) renderSingleColumnConflictFileListSlot(th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictFileListBordered(th)
	}
	return m.renderGitPanelBordered(th)
}

func (m *AppModel) renderSingleColumnMergeDiffViewSlot(th *theme.Theme) string {
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffViewBordered(th)
	}
	return m.renderCodePanelBordered(th)
}

func (m *AppModel) renderSingleColumnMergeDiffFileListSlot(th *theme.Theme) string {
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffFileListBordered(th)
	}
	return m.renderGitPanelBordered(th)
}

func (m *AppModel) renderSingleColumnDiffViewSlot(th *theme.Theme) string {
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffViewBordered(th)
	}
	return m.renderCodePanelBordered(th)
}

func (m *AppModel) renderSingleColumnDiffFileListSlot(th *theme.Theme) string {
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffFileListBordered(th)
	}
	return m.renderGitPanelBordered(th)
}

func (m *AppModel) renderSingleColumnDefaultSlot(active component.FocusID, th *theme.Theme) string {
	if m.conflictViewActive && m.conflictView != nil {
		return m.renderConflictViewBordered(th)
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		return m.renderMergeDiffViewBordered(th)
	}
	if m.diffViewActive && m.diffView != nil {
		return m.renderDiffViewBordered(th)
	}
	content := m.overlayChordHint(m.panelContent(active), active, th)
	return m.renderPanel(content, active, th)
}

// renderLeftSlot renders the left column for the given panel ID.
// Session gets the composite left panel with border; FileTree and GitPanel
// get their respective panel renderers.
func (m *AppModel) renderLeftSlot(id component.FocusID, th *theme.Theme) string {
	if renderer := m.activeLeftSlotRenderer(id); renderer != nil {
		return renderer(th)
	}
	switch id {
	case component.FocusFileTree:
		return m.renderPanel(m.fileTree.View(m.cursorVisible), component.FocusFileTree, th)
	case component.FocusGitPanel:
		return m.renderGitPanelBordered(th)
	default:
		return m.renderLeftPanelBordered(th)
	}
}

type themeRenderer func(*theme.Theme) string

func (m *AppModel) activeLeftSlotRenderer(id component.FocusID) themeRenderer {
	switch id {
	case component.FocusFileTree:
		return m.activeSidebarRenderer()
	case component.FocusConflictFileList:
		return m.conditionalLeftSlotRenderer(m.conflictViewActive && m.conflictView != nil, m.renderConflictFileListBordered)
	case component.FocusMergeDiffFileList:
		return m.conditionalLeftSlotRenderer(m.mergeDiffViewActive && m.mergeDiffView != nil, m.renderMergeDiffFileListBordered)
	case component.FocusDiffFileList:
		return m.conditionalLeftSlotRenderer(m.diffViewActive && m.diffView != nil, m.renderDiffFileListBordered)
	default:
		return nil
	}
}

func (m *AppModel) activeSidebarRenderer() themeRenderer {
	switch {
	case m.conflictViewActive && m.conflictView != nil:
		return m.renderConflictFileListBordered
	case m.mergeDiffViewActive && m.mergeDiffView != nil:
		return m.renderMergeDiffFileListBordered
	case m.diffViewActive && m.diffView != nil:
		return m.renderDiffFileListBordered
	default:
		return nil
	}
}

func (m *AppModel) conditionalLeftSlotRenderer(active bool, renderer themeRenderer) themeRenderer {
	if active {
		return renderer
	}
	return m.renderLeftPanelBordered
}

// panelContent returns the raw view content for a swappable panel.
func (m *AppModel) panelContent(id component.FocusID) string {
	switch id {
	case component.FocusChat:
		return m.chat.View()
	case component.FocusCodeViewer:
		if m.viewMode == ViewGit {
			return m.commitTree.View(m.cursorVisible)
		}
		return m.codePanelView()
	case component.FocusFileTree:
		if m.viewMode == ViewGit {
			return m.gitPanel.View(m.cursorVisible)
		}
		return m.fileTree.View(m.cursorVisible)
	case component.FocusGitPanel:
		return m.gitPanel.View(m.cursorVisible)
	case component.FocusCommitTree:
		return m.commitTree.View(m.cursorVisible)
	case component.FocusDiffFileList:
		if m.diffView != nil {
			return m.diffView.FileListView(m.cursorVisible)
		}
		return ""
	default:
		return ""
	}
}

// chordHintLines is the number of lines consumed by the chord hint overlay
// (label row + divider row).
const chordHintLines = 2

// overlayChordHint prepends the chord hint bar to content when a chord is active.
// The content is trimmed from the bottom so the total line count stays within
// the panel's inner height budget, preventing border clipping from MaxHeight.
func (m *AppModel) overlayChordHint(content string, panelID component.FocusID, th *theme.Theme) string {
	hint := m.chordHint(th)
	if hint == "" {
		return content
	}
	w, _ := m.layout.GetPanelSize(panelID)
	innerW := max(w-panelBorderSize, 1)
	hintWidth := lipgloss.Width(hint)
	pad := max(innerW-hintWidth, 0)
	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", innerW))

	// Replace the first two lines of content with hint + divider
	// so the remaining content stays in place (no layout shift).
	lines := strings.Split(content, "\n")
	hintLine := strings.Repeat(" ", pad) + hint
	if len(lines) >= chordHintLines {
		lines[0] = hintLine
		lines[1] = divider
	} else {
		lines = append([]string{hintLine, divider}, lines...)
	}
	return strings.Join(lines, "\n")
}

// isLeftPanelFocused returns true when either sub-section of the left panel has focus.
func (m *AppModel) isLeftPanelFocused() bool {
	return m.focus.IsFocused(component.FocusSessionPanel) || m.focus.IsFocused(component.FocusAgentPanel)
}

// renderLeftPanelBordered wraps the left panel content in a border that activates
// when either sessions or agents is focused.
func (m *AppModel) renderLeftPanelBordered(th *theme.Theme) string {
	return m.borderLeftPanel(m.renderLeftPanel(th), th)
}

// borderLeftPanel wraps arbitrary content in the left panel's border frame.
func (m *AppModel) borderLeftPanel(content string, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusSessionPanel)
	return m.renderBordered(content, m.isLeftPanelFocused(), w, h, th)
}

// renderLeftPanel stacks sessions and agents with line-extended headers and a divider.
// The model selector is pinned to the absolute bottom row (just above the border).
func (m *AppModel) renderLeftPanel(th *theme.Theme) string {
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerW := max(leftW-panelBorderSize, 1)
	innerH := max(leftH-panelBorderSize, 1)

	sessionsFocused := m.focus.IsFocused(component.FocusSessionPanel)
	agentsFocused := m.focus.IsFocused(component.FocusAgentPanel)
	sections := m.leftPanelSections
	if sections.sessionRect.H <= 0 || sections.agentsRect.H <= 0 {
		sections = computeLeftPanelSections(leftW, leftH, m.agentPanel.SelectorLineCount(), m.sessionPanel.PreferredHeight(max(leftH-panelBorderSize-leftPanelOverhead, 1)))
	}

	sel := m.agentPanel.RenderSelectorLine()
	selLines := m.agentPanel.SelectorLineCount()
	return composeLeftPanelContent(
		innerW,
		innerH,
		sections,
		m.sessionPanel.View(),
		m.agentPanel.View(),
		sessionsFocused,
		agentsFocused,
		sel,
		selLines,
		th,
	)
}

func composeLeftPanelContent(
	innerW, innerH int,
	sections leftPanelSections,
	sessionContent, agentContent string,
	sessionsFocused, agentsFocused bool,
	selector string,
	selectorLines int,
	th *theme.Theme,
) string {
	dividerStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
	divider := lipgloss.NewStyle().PaddingTop(1).Render(
		dividerStyle.Render(strings.Repeat("─", innerW)),
	)

	sessionBody := padLeftPanelBody(sessionContent, max(sections.sessionRect.H, 0))
	agentBody := padLeftPanelBody(agentContent, max(sections.agentsRect.H, 0))
	body := strings.Join([]string{
		sectionHeader("Sessions", innerW, sessionsFocused, th),
		sessionBody,
		divider,
		sectionHeader("Agents", innerW, agentsFocused, th),
		agentBody,
	}, "\n")

	bodyTarget := innerH - selectorLines
	body = padLeftPanelBody(body, bodyTarget)
	return body + "\n" + selector
}

// padLeftPanelBody pads or clips s to exactly targetLines lines.
func padLeftPanelBody(s string, targetLines int) string {
	if targetLines <= 0 {
		return ""
	}
	lines := []string{""}
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	switch {
	case len(lines) > targetLines:
		lines = lines[:targetLines]
	case len(lines) < targetLines:
		lines = append(lines, make([]string, targetLines-len(lines))...)
	}
	return strings.Join(lines, "\n")
}

// sectionHeader renders a label followed by a trailing line.
// When focused, the label uses Primary color to indicate the active sub-section.
func sectionHeader(label string, width int, focused bool, th *theme.Theme) string {
	labelColor := th.Palette.Muted
	lineColor := th.Palette.Border
	if focused {
		labelColor = th.Palette.Secondary
	}
	headerStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(lineColor)

	text := headerStyle.Render(" " + label + " ")
	textWidth := lipgloss.Width(text)
	lineWidth := max(width-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

// isCodePanelFocused returns true when any sub-panel of the code column
// (editor, preview, or read-only code viewer) currently has focus.
func (m *AppModel) isCodePanelFocused() bool {
	c := m.focus.Current()
	return c == component.FocusCodeViewer || pane.IsPaneFocus(c)
}

// isEditorFocused returns true when an editor pane has focus (edit mode).
// Returns false when the diff view is active because diff pane IDs share
// the same namespace and would falsely match editor entries.
func (m *AppModel) isEditorFocused() bool {
	if m.diffViewActive {
		return false
	}
	c := m.focus.Current()
	if !pane.IsPaneFocus(c) {
		return false
	}
	pid := pane.PaneIDFromFocus(c)
	_, ok := m.paneEditors[pid]
	return ok
}

// isPreviewFocused returns true when the preview pane has focus.
func (m *AppModel) isPreviewFocused() bool {
	if m.previewPane == 0 {
		return false
	}
	return m.focus.Current() == pane.PaneFocusID(m.previewPane)
}

// focusCodePanel sets focus to the code panel: in edit mode, the focused
// pane's dynamic ID; otherwise the static FocusCodeViewer.
func (m *AppModel) focusCodePanel() {
	if m.viewMode == ViewEdit {
		m.focus.SetFocus(pane.PaneFocusID(m.focusedPane))
		return
	}
	m.focus.SetFocus(component.FocusCodeViewer)
}

// renderCodePanelBordered wraps the code panel content in a border that
// activates when either the editor or preview sub-panel is focused
// (mirrors renderLeftPanelBordered for the session/agent sub-panels).
func (m *AppModel) renderCodePanelBordered(th *theme.Theme) string {
	content := m.overlayChordHint(m.codePanelView(), component.FocusCodeViewer, th)
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isCodePanelFocused(), w, h, th)
}

// renderGitPanelBordered renders the git explorer panel using the FileTree slot
// dimensions with border highlight based on FocusGitPanel focus state.
func (m *AppModel) renderGitPanelBordered(th *theme.Theme) string {
	content := m.gitPanel.View(m.cursorVisible)
	if m.layout.Mode() == layout.SingleColumn {
		content = m.overlayChordHint(content, component.FocusFileTree, th)
	}
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	focused := m.focus.Current() == component.FocusGitPanel
	return m.renderBordered(content, focused, w, h, th)
}

// renderGitCommitTreeBordered renders the commit tree panel using the CodeViewer
// slot dimensions with border highlight based on FocusCommitTree focus state.
func (m *AppModel) renderGitCommitTreeBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.commitTree.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.focus.Current() == component.FocusCommitTree, w, h, th)
}

// renderPlanViewBordered renders the plan DAG viewer in the right panel slot
// with border highlight based on FocusPlanView focus state.
func (m *AppModel) renderPlanViewBordered(th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.planView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	content := m.planView.View(m.cursorVisible)
	return m.renderBordered(content, m.focus.Current() == component.FocusPlanView, w, h, th)
}

// renderDiffFileListBordered renders the diff file list sidebar using the
// FileTree slot dimensions with border highlight based on FocusDiffFileList.
func (m *AppModel) renderDiffFileListBordered(th *theme.Theme) string {
	content := m.diffView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusDiffFileList, w, h, th)
}

// renderDiffViewBordered renders the diff view using the CodeViewer slot
// dimensions with border highlight based on FocusDiffView focus state.
func (m *AppModel) renderDiffViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.diffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isDiffPaneFocused(), w, h, th)
}

// renderMergeDiffFileListBordered renders the merge diff file list sidebar.
func (m *AppModel) renderMergeDiffFileListBordered(th *theme.Theme) string {
	content := m.mergeDiffView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusMergeDiffFileList, w, h, th)
}

// renderMergeDiffViewBordered renders the merge diff view.
func (m *AppModel) renderMergeDiffViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.mergeDiffView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.isMergeDiffPaneFocused(), w, h, th)
}

// isMergeDiffPaneFocused reports whether the focused component is a merge diff pane.
func (m *AppModel) isMergeDiffPaneFocused() bool {
	return m.mergeDiffViewActive && m.mergeDiffView != nil && pane.IsPaneFocus(m.focus.Current())
}

// renderConflictFileListBordered renders the conflict file list sidebar.
func (m *AppModel) renderConflictFileListBordered(th *theme.Theme) string {
	content := m.conflictView.FileListView(m.cursorVisible)
	w, h := m.layout.GetPanelSize(component.FocusFileTree)
	return m.renderBordered(content, m.focus.Current() == component.FocusConflictFileList, w, h, th)
}

// renderConflictViewBordered renders the conflict resolution content pane.
func (m *AppModel) renderConflictViewBordered(th *theme.Theme) string {
	content := m.overlayBlockedChord(m.overlayChordHint(m.conflictView.View(m.cursorVisible), component.FocusCodeViewer, th))
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	return m.renderBordered(content, m.focus.Current() == component.FocusConflictView, w, h, th)
}

// renderBordered wraps content in a panel border. When focused and a gradient
// border renderer is active, uses per-character gradient coloring; otherwise
// falls back to the standard lipgloss per-side border style.
func (m *AppModel) renderBordered(content string, focused bool, w, h int, th *theme.Theme) string {
	innerW := max(w-panelBorderSize, 1)
	innerH := max(h-panelBorderSize, 1)
	if focused && th.ActiveBorderRender != nil {
		return th.ActiveBorderRender(content, innerW, innerH, h)
	}
	border := th.InactiveBorder
	if focused {
		border = th.ActiveBorder
	}
	return border.
		Width(innerW).
		Height(innerH).
		MaxHeight(h).
		Render(content)
}

func (m *AppModel) renderPanel(content string, id component.FocusID, th *theme.Theme) string {
	w, h := m.layout.GetPanelSize(id)
	return m.renderBordered(content, m.focus.IsFocused(id), w, h, th)
}
