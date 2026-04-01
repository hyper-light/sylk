package ui

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/bridge"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Focus
// ---------------------------------------------------------------------------

func (m *AppModel) syncFocusState() {
	current := m.focus.Current()
	m.syncFocusedEditorPane(current)
	m.syncCoreFocusState(current)
	m.syncPaneEditorFocusState(current)
	m.syncPreviewPanelFocus(current)
	m.syncGitPanelFocus(current)
	m.syncDiffViewFocus(current)
	m.syncMergeDiffViewFocus(current)
	m.syncConflictViewFocus(current)
	m.syncPlanViewFocus(current)
	m.syncEditorWarpLines()
	m.syncPreviewModeDisplay()
}

func (m *AppModel) syncFocusedEditorPane(current component.FocusID) {
	if !pane.IsPaneFocus(current) || m.diffViewActive || m.mergeDiffViewActive {
		return
	}
	pid := pane.PaneIDFromFocus(current)
	if _, ok := m.paneEditors[pid]; ok {
		m.focusedPane = pid
	}
}

func (m *AppModel) syncCoreFocusState(current component.FocusID) {
	m.chat.SetFocused(current == component.FocusChat)
	m.input.SetFocused(current == component.FocusInput)
	m.sessionPanel.SetFocused(current == component.FocusSessionPanel)
	m.agentPanel.SetFocused(current == component.FocusAgentPanel)
	m.codePanel.SetFocused(current == component.FocusCodeViewer && m.viewMode != ViewEdit && !m.hasPreview())
	m.fileTree.SetFocused(current == component.FocusFileTree)
}

func (m *AppModel) syncPaneEditorFocusState(current component.FocusID) {
	for id, ps := range m.paneEditors {
		focused := current == pane.PaneFocusID(id) && !m.diffViewActive && !m.mergeDiffViewActive
		ps.editor.SetFocused(focused)
		if !focused {
			ps.editor.DismissAllOverlays()
		}
	}
}

func (m *AppModel) syncPreviewPanelFocus(current component.FocusID) {
	m.previewPanel.SetFocused(m.isPreviewFocused())
	m.mdPreviewPanel.SetFocused(m.mdPreviewPane != 0 && current == pane.PaneFocusID(m.mdPreviewPane))
}

func (m *AppModel) syncGitPanelFocus(current component.FocusID) {
	if m.gitPanel == nil {
		return
	}
	m.gitPanel.SetFocused(current == component.FocusGitPanel)
	m.commitTree.SetFocused(current == component.FocusCommitTree)
}

func (m *AppModel) syncDiffViewFocus(current component.FocusID) {
	if !m.diffViewActive || m.diffView == nil {
		return
	}
	if pane.IsPaneFocus(current) {
		m.diffView.SetFocusedPane(pane.PaneIDFromFocus(current))
	}
	m.diffView.SetFocused(pane.IsPaneFocus(current) || current == component.FocusDiffView)
	m.diffView.SetFileListFocused(current == component.FocusDiffFileList)
}

func (m *AppModel) syncMergeDiffViewFocus(current component.FocusID) {
	if !m.mergeDiffViewActive || m.mergeDiffView == nil {
		return
	}
	if pane.IsPaneFocus(current) {
		m.mergeDiffView.SetFocusedPane(pane.PaneIDFromFocus(current))
	}
	m.mergeDiffView.SetFocused(pane.IsPaneFocus(current) || current == component.FocusMergeDiffView)
	m.mergeDiffView.SetFileListFocused(current == component.FocusMergeDiffFileList)
}

func (m *AppModel) syncConflictViewFocus(current component.FocusID) {
	if !m.conflictViewActive || m.conflictView == nil {
		return
	}
	m.conflictView.SetFocused(current == component.FocusConflictView)
	m.conflictView.SetFileListFocused(current == component.FocusConflictFileList)
}

func (m *AppModel) syncPlanViewFocus(current component.FocusID) {
	if m.planView == nil {
		return
	}
	m.planView.SetFocused(current == component.FocusPlanView)
}

// syncPreviewModeDisplay sets the editor status line to PREVIEW mode when
// the preview panel is focused, and restores the editor's actual mode otherwise.
func (m *AppModel) syncPreviewModeDisplay() {
	browsing := m.hasPreview() && m.focus.Current() == component.FocusFileTree
	if m.isPreviewFocused() || browsing {
		m.focusedEditor().SetStatusMode(mode.ModePreview)
		return
	}
	if m.isEditorFocused() {
		m.focusedEditor().RestoreStatusMode()
	}
}

// spatialFocusTarget resolves an alt+shift+arrow key to the panel it should
// navigate to. Delegates to the generic layout.Navigate algorithm over a
// hierarchical panel grid built from the current layout mode and state.
func (m *AppModel) spatialFocusTarget(key string) (component.FocusID, bool) {
	dir, ok := keyToDirection(key)
	if !ok {
		return 0, false
	}
	grid := m.buildPanelGrid()
	pos, ok := layout.FindInGrid(grid, m.focus.Current())
	if !ok {
		return 0, false
	}
	return layout.Navigate(grid, pos, dir)
}

// keyToDirection maps an alt+shift+arrow key string to a layout.Direction.
func keyToDirection(key string) (layout.Direction, bool) {
	switch key {
	case "alt+shift+right":
		return layout.DirRight, true
	case "alt+shift+left":
		return layout.DirLeft, true
	case "alt+shift+down":
		return layout.DirDown, true
	case "alt+shift+up":
		return layout.DirUp, true
	}
	return 0, false
}

// buildPanelGrid returns the visible panels as a hierarchical grid of
// layout.PanelGroup entries. Only panels actually rendered on screen are
// included. Sub-panels (e.g. Sessions+Agents, Preview+Editor) are encoded
// within their parent PanelGroup's sub-grid.
func (m *AppModel) buildPanelGrid() [][]layout.PanelGroup {
	return [][]layout.PanelGroup{
		m.buildTopPanelGrid(),
		{m.panelGroup(component.FocusInput)},
	}
}

func (m *AppModel) buildTopPanelGrid() []layout.PanelGroup {
	switch m.layout.Mode() {
	case layout.FourColumn:
		return m.buildFourColumnPanelGrid()
	case layout.ThreeColumn:
		return m.buildThreeColumnPanelGrid()
	case layout.TwoColumn:
		return m.buildTwoColumnPanelGrid()
	default:
		return m.buildSingleColumnPanelGrid()
	}
}

func (m *AppModel) buildFourColumnPanelGrid() []layout.PanelGroup {
	return []layout.PanelGroup{
		m.leftSubPanelGroup(),
		m.panelGroup(component.FocusFileTree),
		m.panelGroup(component.FocusChat),
		m.codePanelGroup(),
	}
}

func (m *AppModel) buildThreeColumnPanelGrid() []layout.PanelGroup {
	return []layout.PanelGroup{
		m.leftColumnPanelGroup(m.leftRing.current()),
		m.panelGroup(component.FocusChat),
		m.codePanelGroup(),
	}
}

func (m *AppModel) buildTwoColumnPanelGrid() []layout.PanelGroup {
	return []layout.PanelGroup{
		m.leftColumnPanelGroup(m.leftRing.current()),
		m.rightColumnPanelGroup(m.rightRing.current()),
	}
}

func (m *AppModel) buildSingleColumnPanelGrid() []layout.PanelGroup {
	return []layout.PanelGroup{m.singleColumnPanelGroup(m.leftRing.current())}
}

func (m *AppModel) panelGroup(id component.FocusID) layout.PanelGroup {
	return layout.PanelGroup{SubPanels: [][]component.FocusID{{m.spatialPanelID(id)}}}
}

func (m *AppModel) leftSubPanelGroup() layout.PanelGroup {
	return layout.PanelGroup{SubPanels: [][]component.FocusID{
		{component.FocusSessionPanel},
		{component.FocusAgentPanel},
	}}
}

func (m *AppModel) leftColumnPanelGroup(id component.FocusID) layout.PanelGroup {
	if id == component.FocusSessionPanel {
		return m.leftSubPanelGroup()
	}
	return m.panelGroup(id)
}

func (m *AppModel) rightColumnPanelGroup(id component.FocusID) layout.PanelGroup {
	if m.isCodeColumnFocusID(id) {
		return m.codePanelGroup()
	}
	return m.panelGroup(id)
}

func (m *AppModel) singleColumnPanelGroup(id component.FocusID) layout.PanelGroup {
	if id == component.FocusSessionPanel {
		return m.leftSubPanelGroup()
	}
	if m.isCodeColumnFocusID(id) {
		return m.codePanelGroup()
	}
	return m.panelGroup(id)
}

func (m *AppModel) spatialPanelID(id component.FocusID) component.FocusID {
	if m.viewMode != ViewGit {
		return id
	}
	switch id {
	case component.FocusFileTree:
		return m.gitSpatialSidebarID()
	case component.FocusCodeViewer:
		return component.FocusCommitTree
	default:
		return id
	}
}

func (m *AppModel) gitSpatialSidebarID() component.FocusID {
	if m.mergeDiffViewActive {
		return component.FocusMergeDiffFileList
	}
	if m.diffViewActive {
		return component.FocusDiffFileList
	}
	return component.FocusGitPanel
}

func (m *AppModel) isCodeColumnFocusID(id component.FocusID) bool {
	switch id {
	case component.FocusCodeViewer, component.FocusDiffView, component.FocusMergeDiffView:
		return true
	default:
		return false
	}
}

// codePanelGroup returns the PanelGroup for the code column, with sub-panels
// derived from the pane tree for spatial navigation.
func (m *AppModel) codePanelGroup() layout.PanelGroup {
	if m.viewMode == ViewEdit && m.paneTree != nil {
		return layout.PanelGroup{SubPanels: m.paneTree.ToSubGrid()}
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil {
		if dt := m.mergeDiffView.PaneTree(); dt != nil {
			return layout.PanelGroup{SubPanels: dt.ToSubGrid()}
		}
	}
	if m.diffViewActive && m.diffView != nil {
		if dt := m.diffView.PaneTree(); dt != nil {
			return layout.PanelGroup{SubPanels: dt.ToSubGrid()}
		}
	}
	id := component.FocusCodeViewer
	if m.viewMode == ViewGit {
		id = component.FocusCommitTree
	}
	return layout.PanelGroup{SubPanels: [][]component.FocusID{
		{id},
	}}
}

// ---------------------------------------------------------------------------
// Bridges
// ---------------------------------------------------------------------------

func (m *AppModel) startBridges() tea.Cmd {
	return func() tea.Msg {
		return bridgeReadyMsg{}
	}
}

// bridgeReadyMsg is an internal message signaling that bridges should be started.
type bridgeReadyMsg struct{}

// StartBridges connects all event bridges to the running tea.Program.
// This must be called after tea.NewProgram is created.
func (m *AppModel) StartBridges(program bridge.TeaProgram) error {
	bridges := []bridge.Bridge{
		m.activityBridge,
		m.tokenUsageBridge,
		m.sessionBridge,
		m.streamBridge,
		m.guideBridge,
		m.lspBridge,
	}
	for _, b := range bridges {
		if err := b.Start(program); err != nil {
			return err
		}
	}
	if m.gitBridge != nil {
		if err := m.gitBridge.Start(program); err != nil {
			return err
		}
	}
	if m.pipelineBridge != nil {
		if err := m.pipelineBridge.Start(program); err != nil {
			return err
		}
	}
	m.startIndexProgressObserver(program)
	return nil
}

// pipelinePhaseMap maps boot pipeline phase strings to UI IndexPhase constants.
// Pipeline "done" is deliberately absent — it only means the synchronous
// pipeline finished, not that background indexing is complete. The real
// Done signal comes from bgWaiter.Ready().
var pipelinePhaseMap = map[string]status.IndexPhase{
	"setup":    status.PhaseLoad,
	"allocate": status.PhaseLoad,
	"ingest":   status.PhaseEmbed,
	"commit":   status.PhaseCommit,
}

// startIndexProgressObserver wires progress from both the boot pipeline and
// the background indexer into the status bar. Pipeline phases fire via
// KnowledgeStore.NotifyProgress (set in cmd/tui.go); background indexer
// batches fire via BackgroundIndexWaiter.OnProgress.
func (m *AppModel) startIndexProgressObserver(program bridge.TeaProgram) {
	ks := m.deps.KnowledgeStore
	if ks == nil {
		return
	}
	scope := m.deps.Scope
	if scope == nil {
		return
	}

	// Register the pipeline progress observer immediately so we catch
	// phases that fire before the background indexer exists. Pipeline
	// phases fire after completion, so we send current=1, total=1 to
	// snap the stage's bar segment to full.
	ks.SetProgressObserver(func(phase string, current, total int64) {
		uiPhase, ok := pipelinePhaseMap[phase]
		if !ok {
			return // Skip unknown phases including "done"; real Done comes from bgWaiter.Ready().
		}
		program.Send(msg.IndexProgressMsg{
			Phase:   int(uiPhase),
			Current: 1,
			Total:   1,
		})
	})

	// Goroutine waits for partial readiness, then hooks background indexer.
	_ = scope.Go("index-progress-observer", 0, func(bgCtx context.Context) error {
		if err := ks.WaitForPartial(bgCtx); err != nil {
			return nil
		}
		bgWaiter := ks.BackgroundWaiter()
		if bgWaiter == nil {
			return nil
		}

		bgWaiter.OnProgress(func(indexed, total int64) {
			program.Send(msg.IndexProgressMsg{
				Phase:   int(status.PhaseIndex),
				Current: indexed,
				Total:   total,
			})
		})

		select {
		case <-bgWaiter.Ready():
			program.Send(msg.IndexProgressMsg{Phase: int(status.PhaseDone), Done: true})
		case <-bgCtx.Done():
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Guide integration
// ---------------------------------------------------------------------------

func (m *AppModel) publishRouteRequest(submit msg.SubmitPromptMsg) tea.Cmd {
	sessionID := m.resolveRouteSessionID(submit.SessionID)
	submit.SessionID = sessionID
	targetAgent := strings.TrimSpace(submit.TargetAgent)
	routeTarget := m.resolveConcreteTargetAgent(targetAgent)
	promptEstimate := estimateGuideTokens(submit.Text) + guideRouteOverheadTokens
	m.bumpAgentContextUsage(guideAgentID, promptEstimate)
	m.statusBar.SetTokenPhase(status.PhaseInput)
	// Only attribute routing activity to the Guide when it will actually
	// perform LLM classification. Explicit targets bypass the classifier —
	// publishing Guide activity here falsely sets the Guide to
	// StatusThinking in the agent panel.
	if routeTarget == "" {
		m.publishGuideActivity(
			events.EventTypeLLMRequest,
			events.OutcomePending,
			"Classifying and routing request",
		)
	}

	req := &guide.RouteRequest{
		CorrelationID:  uuid.New().String(),
		Input:          submit.Text,
		SourceAgentID:  sourceAgentTUI,
		TargetAgentID:  routeTarget,
		ExplicitTarget: routeTarget != "",
		SessionID:      submit.SessionID,
		Timestamp:      time.Now(),
	}
	m.registerStream(msg.StreamStartMsg{
		SessionID:     submit.SessionID,
		CorrelationID: req.CorrelationID,
		AgentID:       thinkingAgentType(targetAgent),
		AgentType:     thinkingAgentType(targetAgent),
		AgentName:     thinkingAgentType(targetAgent),
	})

	if !m.guideRequestAvailable() {
		return func() tea.Msg {
			return msg.StreamErrorMsg{
				SessionID:     submit.SessionID,
				CorrelationID: req.CorrelationID,
				Err:           errors.New("guide is not running; start with --mock or connect a guide backend"),
			}
		}
	}

	busMsg := guide.NewRequestMessage("", req)

	return func() tea.Msg {
		err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg)
		if err != nil {
			return msg.StreamErrorMsg{
				SessionID:     submit.SessionID,
				CorrelationID: req.CorrelationID,
				Err:           err,
			}
		}
		return nil
	}
}

func (m *AppModel) bumpGuideContextUsage(addedTokens int) float64 {
	retained := int(float64(m.guideContextTokens) * guideContextRetention)
	tokens := retained + max(addedTokens, 0)
	tokens = min(tokens, guideMaxContextTokens)
	m.guideContextTokens = tokens
	m.guideContextUsage = float64(tokens) / float64(guideMaxContextTokens)
	m.agentContextTokens[guideAgentID] = tokens
	if m.agentPanel != nil {
		m.agentPanel.SyncContextUsage(guideAgentID, m.guideContextUsage)
	}
	return m.guideContextUsage
}

// setAgentContextUsage directly sets the context usage from real input tokens.
// Input tokens represent the full conversation context sent to the agent on each
// call, so they directly measure context window occupancy — no decay needed.
func (m *AppModel) setAgentContextUsage(agentID string, inputTokens int) float64 {
	panelAgentID := normalizeAgentID(agentID)
	if panelAgentID == "" {
		return 0
	}
	return m.setAgentReplicaContextUsage(panelAgentID, panelAgentID, "", inputTokens)
}

func (m *AppModel) bumpAgentContextUsage(agentID string, addedTokens int) float64 {
	panelAgentID := normalizeAgentID(agentID)
	if panelAgentID == "" {
		return 0
	}
	if panelAgentID == guideAgentID {
		return m.bumpGuideContextUsage(addedTokens)
	}
	runtimeAgentID := panelAgentID
	key := runtimeContextKey(panelAgentID, runtimeAgentID)
	state, ok := m.agentRuntimeContexts[key]
	if !ok {
		state = runtimeContextState{
			PanelAgentID:   panelAgentID,
			RuntimeAgentID: runtimeAgentID,
		}
	}
	limit := m.agentContextTokenLimitForModel(panelAgentID, state.ModelID)
	retained := int(float64(state.Tokens) * guideContextRetention)
	tokens := min(retained+max(addedTokens, 0), limit)
	return m.setAgentReplicaContextUsage(panelAgentID, runtimeAgentID, state.ModelID, tokens)
}

func (m *AppModel) agentContextTokenLimit(agentID string) int {
	return m.agentContextTokenLimitForModel(agentID, "")
}

func (m *AppModel) agentContextTokenLimitForModel(agentID, modelID string) int {
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		return defaultAgentMaxContextTokens
	}
	if normalized == guideAgentID {
		return guideMaxContextTokens
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		if observed := strings.TrimSpace(m.agentContextModels[normalized]); observed != "" {
			modelID = observed
		}
	}
	if m.agentPanel != nil && modelID == "" {
		modelID = strings.TrimSpace(m.agentPanel.ModelIDOf(normalized))
	}
	if modelID == "" {
		return defaultAgentMaxContextTokens
	}
	limit := agentContextCounter.MaxContextTokens(modelID)
	if limit <= 0 {
		return defaultAgentMaxContextTokens
	}
	return limit
}

func (m *AppModel) agentContextCategory(agentID string) string {
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		return ""
	}
	if _, _, ok := parseCanonicalPipelinePanelAgentID(normalized); ok {
		return "pipeline"
	}
	if m.agentPanel != nil {
		if agentType := strings.TrimSpace(m.agentPanel.AgentTypeOf(normalized)); agentType != "" {
			if isPipelineWorkerType(agentType) {
				if _, _, ok := parseCanonicalPipelinePanelAgentID(normalized); ok {
					return "pipeline"
				}
				return "standalone"
			}
			switch agentType {
			case "academic", "librarian", "archivalist":
				return "knowledge"
			default:
				return "standalone"
			}
		}
	}
	switch normalized {
	case "academic", "librarian", "archivalist":
		return "knowledge"
	default:
		return "standalone"
	}
}

func (m *AppModel) setAgentReplicaContextUsage(panelAgentID, runtimeAgentID, modelID string, inputTokens int) float64 {
	panelAgentID = normalizeAgentID(panelAgentID)
	if panelAgentID == "" {
		return 0
	}
	if panelAgentID == guideAgentID {
		limit := guideMaxContextTokens
		tokens := min(max(inputTokens, 0), limit)
		m.guideContextTokens = tokens
		m.guideContextUsage = float64(tokens) / float64(limit)
		m.agentContextTokens[guideAgentID] = tokens
		if m.agentPanel != nil {
			m.agentPanel.SyncContextUsage(guideAgentID, m.guideContextUsage)
		}
		return m.guideContextUsage
	}

	runtimeAgentID = normalizeRuntimeAgentID(panelAgentID, runtimeAgentID)
	if m.agentRuntimeContexts == nil {
		m.agentRuntimeContexts = make(map[string]runtimeContextState)
	}
	if m.agentContextModels == nil {
		m.agentContextModels = make(map[string]string)
	}
	if strings.TrimSpace(modelID) != "" {
		m.agentContextModels[panelAgentID] = strings.TrimSpace(modelID)
	}
	if m.agentContextCategory(panelAgentID) != "knowledge" {
		for key, state := range m.agentRuntimeContexts {
			if state.PanelAgentID == panelAgentID && state.RuntimeAgentID != runtimeAgentID {
				delete(m.agentRuntimeContexts, key)
			}
		}
	}

	limit := m.agentContextTokenLimitForModel(panelAgentID, modelID)
	tokens := min(max(inputTokens, 0), limit)
	key := runtimeContextKey(panelAgentID, runtimeAgentID)
	m.agentRuntimeContexts[key] = runtimeContextState{
		PanelAgentID:   panelAgentID,
		RuntimeAgentID: runtimeAgentID,
		ModelID:        strings.TrimSpace(modelID),
		Tokens:         tokens,
		Ephemeral:      isEphemeralReplicaRuntime(runtimeAgentID),
		UpdatedAt:      time.Now(),
	}
	return m.recomputeDisplayedAgentContextUsage(panelAgentID)
}

func (m *AppModel) clearAgentReplicaContextUsage(panelAgentID, runtimeAgentID string) float64 {
	panelAgentID = normalizeAgentID(panelAgentID)
	if panelAgentID == "" {
		return 0
	}
	if panelAgentID == guideAgentID {
		m.guideContextTokens = 0
		m.guideContextUsage = 0
		m.agentContextTokens[guideAgentID] = 0
		if m.agentPanel != nil {
			m.agentPanel.SyncContextUsage(guideAgentID, 0)
		}
		return 0
	}
	if m.agentRuntimeContexts == nil {
		return m.recomputeDisplayedAgentContextUsage(panelAgentID)
	}
	if strings.TrimSpace(runtimeAgentID) == "" {
		for key, state := range m.agentRuntimeContexts {
			if state.PanelAgentID == panelAgentID {
				delete(m.agentRuntimeContexts, key)
			}
		}
		return m.recomputeDisplayedAgentContextUsage(panelAgentID)
	}
	key := runtimeContextKey(panelAgentID, runtimeAgentID)
	if _, ok := m.agentRuntimeContexts[key]; !ok && m.agentContextCategory(panelAgentID) != "knowledge" {
		for existingKey, state := range m.agentRuntimeContexts {
			if state.PanelAgentID == panelAgentID {
				delete(m.agentRuntimeContexts, existingKey)
			}
		}
		return m.recomputeDisplayedAgentContextUsage(panelAgentID)
	}
	delete(m.agentRuntimeContexts, key)
	return m.recomputeDisplayedAgentContextUsage(panelAgentID)
}

func (m *AppModel) clearEphemeralReplicaContextUsage(panelAgentID string) float64 {
	panelAgentID = normalizeAgentID(panelAgentID)
	if panelAgentID == "" || m.agentRuntimeContexts == nil {
		return 0
	}
	for key, state := range m.agentRuntimeContexts {
		if state.PanelAgentID == panelAgentID && state.Ephemeral {
			delete(m.agentRuntimeContexts, key)
		}
	}
	return m.recomputeDisplayedAgentContextUsage(panelAgentID)
}

func (m *AppModel) noteAgentReplicaCount(panelAgentID string, active int) float64 {
	panelAgentID = normalizeAgentID(panelAgentID)
	if panelAgentID == "" {
		return 0
	}
	if m.agentReplicaCounts == nil {
		m.agentReplicaCounts = make(map[string]int)
	}
	if active < 0 {
		active = 0
	}
	m.agentReplicaCounts[panelAgentID] = active
	if active == 0 && m.agentContextCategory(panelAgentID) == "knowledge" {
		return m.clearEphemeralReplicaContextUsage(panelAgentID)
	}
	m.pruneReplicaContextUsage(panelAgentID, active)
	return m.recomputeDisplayedAgentContextUsage(panelAgentID)
}

func (m *AppModel) pruneReplicaContextUsage(panelAgentID string, desired int) {
	if desired < 0 || m.agentRuntimeContexts == nil {
		return
	}
	type candidate struct {
		key    string
		active bool
		state  runtimeContextState
	}
	var candidates []candidate
	for key, state := range m.agentRuntimeContexts {
		if state.PanelAgentID != panelAgentID || !state.Ephemeral {
			continue
		}
		candidates = append(candidates, candidate{
			key:    key,
			active: m.runtimeAgentHasActiveStream(panelAgentID, state.RuntimeAgentID),
			state:  state,
		})
	}
	if len(candidates) <= desired {
		return
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		switch {
		case a.active && !b.active:
			return 1
		case !a.active && b.active:
			return -1
		case a.state.UpdatedAt.Before(b.state.UpdatedAt):
			return -1
		case b.state.UpdatedAt.Before(a.state.UpdatedAt):
			return 1
		default:
			return strings.Compare(a.key, b.key)
		}
	})
	for len(candidates) > desired {
		delete(m.agentRuntimeContexts, candidates[0].key)
		candidates = candidates[1:]
	}
}

func (m *AppModel) runtimeAgentHasActiveStream(panelAgentID, runtimeAgentID string) bool {
	panelAgentID = normalizeAgentID(panelAgentID)
	runtimeAgentID = normalizeRuntimeAgentID(panelAgentID, runtimeAgentID)
	for _, entry := range m.activeStreams {
		if entry == nil {
			continue
		}
		if normalizeAgentID(entry.AgentID) == panelAgentID &&
			normalizeRuntimeAgentID(panelAgentID, entry.RuntimeAgentID) == runtimeAgentID {
			return true
		}
	}
	for _, entry := range m.nestedStreams {
		if entry == nil {
			continue
		}
		if normalizeAgentID(entry.AgentID) == panelAgentID &&
			normalizeRuntimeAgentID(panelAgentID, entry.RuntimeAgentID) == runtimeAgentID {
			return true
		}
	}
	return false
}

func (m *AppModel) recomputeDisplayedAgentContextUsage(panelAgentID string) float64 {
	panelAgentID = normalizeAgentID(panelAgentID)
	if panelAgentID == "" {
		return 0
	}
	if panelAgentID == guideAgentID {
		if m.agentPanel != nil {
			m.agentPanel.SyncContextUsage(guideAgentID, m.guideContextUsage)
		}
		return m.guideContextUsage
	}

	totalTokens := 0
	totalLimit := 0
	explicitReplicaCount := 0
	for _, state := range m.agentRuntimeContexts {
		if state.PanelAgentID != panelAgentID {
			continue
		}
		totalTokens += state.Tokens
		totalLimit += m.agentContextTokenLimitForModel(panelAgentID, state.ModelID)
		if state.Ephemeral {
			explicitReplicaCount++
		}
	}

	if m.agentContextCategory(panelAgentID) == "knowledge" {
		if desired := m.agentReplicaCounts[panelAgentID]; desired > explicitReplicaCount {
			totalLimit += (desired - explicitReplicaCount) * m.agentContextTokenLimit(panelAgentID)
		}
	}
	if totalLimit <= 0 {
		totalLimit = m.agentContextTokenLimit(panelAgentID)
	}
	totalTokens = min(totalTokens, totalLimit)
	m.agentContextTokens[panelAgentID] = totalTokens
	ratio := float64(totalTokens) / float64(totalLimit)
	if m.agentPanel != nil {
		m.agentPanel.SyncContextUsage(panelAgentID, ratio)
	}
	return ratio
}

func estimateGuideTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	chars := len([]rune(trimmed))
	return max((chars+3)/4, 1)
}

func (m *AppModel) publishGuideActivity(
	eventType events.EventType,
	outcome events.EventOutcome,
	content string,
) {
	if m.deps.ActivityPub == nil {
		return
	}
	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   guideAgentID,
		Content:   content,
		Outcome:   outcome,
		Data: map[string]any{
			"agent_type": guideAgentType,
			"agent_name": guideAgentName,
		},
	}
	m.deps.ActivityPub.PublishActivity(event)
}

func (m *AppModel) guideRequestAvailable() bool {
	if m.deps.GuideBus == nil {
		return false
	}
	if m.deps.Guide != nil {
		return true
	}
	if channelBus, ok := m.deps.GuideBus.(*guide.ChannelBus); ok {
		return channelBus.TopicSubscriberCount(guide.TopicGuideRequests) > 0
	}
	return true
}

// ---------------------------------------------------------------------------
// Tick — demand-driven tick chain
// ---------------------------------------------------------------------------

// needsFastTick reports whether any 60fps animation is active.
func (m *AppModel) needsFastTick() bool {
	return !m.scroll.settled() ||
		!m.bounceSettled()
}

// needsActiveDecorTick reports whether high-frequency decor effects are active.
func (m *AppModel) needsActiveDecorTick() bool {
	return m.activeDecorTickMask(time.Now()) != 0
}

func (m *AppModel) activeDecorTickMask(now time.Time) uint16 {
	return m.chromeDecorTickMask() |
		m.tabArrowDecorTickMask(now) |
		m.commitTreeDecorTickMask() |
		m.gitDecorTickMask() |
		m.diffDecorTickMask() |
		m.mergeDiffDecorTickMask() |
		m.conflictDecorTickMask() |
		m.planDecorTickMask() |
		m.queueDecorTickMask() |
		m.agentDecorTickMask() |
		m.focusGradientDecorTickMask()
}

func (m *AppModel) chromeDecorTickMask() uint16 {
	return boolMask(m.chatActiveAnimation()) | boolMask(m.statusBarAnimating())
}

func (m *AppModel) chatActiveAnimation() bool {
	return m.chat != nil && m.chat.HasActiveAnimation()
}

func (m *AppModel) statusBarAnimating() bool {
	return m.statusBar != nil && m.statusBar.IsAnimating()
}

func (m *AppModel) tabArrowDecorTickMask(now time.Time) uint16 {
	return boolMask(now.Before(m.tabArrowFlashLeftUntil)) |
		boolMask(now.Before(m.tabArrowFlashRightUntil))
}

func (m *AppModel) commitTreeDecorTickMask() uint16 {
	return boolMask(m.commitTree != nil && m.commitTree.NeedsDecorTick())
}

func (m *AppModel) gitDecorTickMask() uint16 {
	return boolMask(m.viewMode == ViewGit && m.gitPanel != nil && m.gitPanel.NeedsDecorTick())
}

func (m *AppModel) diffDecorTickMask() uint16 {
	return boolMask(m.diffViewActive && m.diffView != nil && m.diffView.NeedsDecorTick())
}

func (m *AppModel) mergeDiffDecorTickMask() uint16 {
	return boolMask(m.mergeDiffViewActive && m.mergeDiffView != nil && m.mergeDiffView.NeedsDecorTick())
}

func (m *AppModel) conflictDecorTickMask() uint16 {
	return boolMask(m.conflictViewActive && m.conflictView != nil && m.conflictView.NeedsDecorTick())
}

func (m *AppModel) planDecorTickMask() uint16 {
	return boolMask(m.planView != nil && m.planView.NeedsDecorTick())
}

func (m *AppModel) queueDecorTickMask() uint16 {
	return boolMask(!m.promptQueue.IsEmpty() && !m.promptQueue.IsPaused())
}

func (m *AppModel) agentDecorTickMask() uint16 {
	return boolMask(m.agentPanel != nil && m.agentPanel.NeedsHighFrequencyDecorTick())
}

func (m *AppModel) focusGradientDecorTickMask() uint16 {
	return boolMask(m.currentFocusGradient() != nil && m.hasActiveAgent())
}

func boolMask(ok bool) uint16 {
	if ok {
		return 1
	}
	return 0
}

// needsIdleDecorTick reports whether any resting decor effects are active.
func (m *AppModel) needsIdleDecorTick() bool {
	return m.needsActiveDecorTick() ||
		(m.agentPanel != nil && m.agentPanel.NeedsDecorTick()) ||
		m.currentFocusGradient() != nil
}

// needsDecorTick reports whether any decor effect is active at all.
func (m *AppModel) needsDecorTick() bool {
	return m.decorDemand() != decorCadenceOff
}

func (m *AppModel) decorDemand() decorCadence {
	switch {
	case m.needsActiveDecorTick():
		return decorCadenceActive
	case m.needsIdleDecorTick():
		return decorCadenceIdle
	default:
		return decorCadenceOff
	}
}

func decorIntervalFor(c decorCadence) time.Duration {
	switch c {
	case decorCadenceActive:
		return decorTickActiveInterval
	case decorCadenceIdle:
		return decorTickIdleInterval
	default:
		return decorTickIdleInterval
	}
}

func (m *AppModel) nextIdleDecorInterval(now time.Time) time.Duration {
	delay := decorTickIdleInterval
	if !m.hasActiveAgent() {
		if focusDelay := m.nextIdleFocusBorderDelay(now); focusDelay > 0 {
			delay = minDuration(delay, focusDelay)
		}
		if agentDelay := m.nextIdleAgentDecorDelay(now); agentDelay > 0 {
			delay = minDuration(delay, agentDelay)
		}
	}
	return delay
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func (m *AppModel) hasActiveAgent() bool {
	return m.agentPanel != nil && m.agentPanel.HasActiveAgent()
}

func (m *AppModel) currentFocusGradient() *theme.Gradient {
	if m.hasActiveAgent() {
		return m.activeFocusGradient
	}
	return m.idleFocusGradient
}

func (m *AppModel) focusBorderFrameChanged(now time.Time) bool {
	if m.currentFocusGradient() == nil {
		return false
	}
	if m.hasActiveAgent() {
		return true
	}
	bucket := int64(now.Sub(m.focusRingStart) / idleFocusBorderPhaseStep)
	if bucket == m.lastFocusBorderBucket {
		return false
	}
	m.lastFocusBorderBucket = bucket
	return true
}

func (m *AppModel) nextIdleFocusBorderDelay(now time.Time) time.Duration {
	if m.currentFocusGradient() == nil || m.hasActiveAgent() {
		return 0
	}
	elapsed := now.Sub(m.focusRingStart)
	bucket := elapsed / idleFocusBorderPhaseStep
	next := m.focusRingStart.Add((bucket + 1) * idleFocusBorderPhaseStep)
	delay := next.Sub(now)
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func (m *AppModel) nextIdleAgentDecorDelay(now time.Time) time.Duration {
	if m.agentPanel == nil || m.hasActiveAgent() {
		return 0
	}
	return m.agentPanel.NextIdleDecorDelay(now)
}

// needsSlowTick reports whether any non-blink, non-LSP debounce needs
// slow-rate ticking. Cursor blink is handled by BlinkMsg; LSP flush by
// LSPFlushMsg.
func (m *AppModel) needsSlowTick() bool {
	return m.swipe.accum != 0 // Swipe decay pending.
}

// ensureTick starts or upgrades the tick chain. Returns a tea.Cmd only when
// a new chain must be scheduled; nil if the current chain already covers it.
func (m *AppModel) ensureTick(fast bool) tea.Cmd {
	if fast && m.tickRate != tickFast {
		m.tickGen++
		m.tickRate = tickFast
		return m.tickCmdWith(tickFastInterval)
	}
	if m.tickRate == tickIdle {
		m.tickGen++
		if fast {
			m.tickRate = tickFast
			return m.tickCmdWith(tickFastInterval)
		}
		m.tickRate = tickSlow
		return m.tickCmdWith(tickSlowInterval)
	}
	return nil
}

// tickCmdWith schedules a one-shot tick at the given interval, tagged with
// the current generation to detect stale chains.
func (m *AppModel) tickCmdWith(d time.Duration) tea.Cmd {
	gen := m.tickGen
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return msg.TickMsg{Time: t, Gen: gen}
	})
}

// continueTickChain returns the next tick command at the appropriate
// interval, or nil to let the chain stop when nothing needs ticking.
func (m *AppModel) continueTickChain() tea.Cmd {
	if m.needsFastTick() {
		m.tickRate = tickFast
		return m.tickCmdWith(tickFastInterval)
	}
	if m.needsSlowTick() {
		m.tickRate = tickSlow
		return m.tickCmdWith(tickSlowInterval)
	}
	m.tickRate = tickIdle
	return nil
}

// ensureTickAfterDispatch starts or upgrades the tick chain if the
// dispatch changed state that requires ticking.
func (m *AppModel) ensureTickAfterDispatch() tea.Cmd {
	if m.needsFastTick() {
		return m.ensureTick(true)
	}
	if m.needsSlowTick() {
		return m.ensureTick(false)
	}
	return nil
}

// ensureDecorTick starts the decor tick chain if needed.
func (m *AppModel) ensureDecorTick() tea.Cmd {
	desired := m.decorDemand()
	if desired == decorCadenceOff {
		m.decorOn = false
		m.decorCadence = decorCadenceOff
		return nil
	}
	if m.decorOn && m.decorCadence == desired {
		return nil
	}
	m.decorOn = true
	m.decorCadence = desired
	m.decorGen++
	gen := m.decorGen
	interval := decorIntervalFor(desired)
	if desired == decorCadenceIdle {
		interval = m.nextIdleDecorInterval(time.Now())
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return msg.DecorTickMsg{Time: t, Gen: gen}
	})
}

// continueDecorTickChain schedules the next decor tick if effects remain.
func (m *AppModel) continueDecorTickChain() tea.Cmd {
	desired := m.decorDemand()
	if desired == decorCadenceOff {
		m.decorOn = false
		m.decorCadence = decorCadenceOff
		return nil
	}
	if desired != m.decorCadence {
		m.decorCadence = desired
		m.decorGen++
	}
	gen := m.decorGen
	interval := decorIntervalFor(m.decorCadence)
	if m.decorCadence == decorCadenceIdle {
		interval = m.nextIdleDecorInterval(time.Now())
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return msg.DecorTickMsg{Time: t, Gen: gen}
	})
}

// ensureDecorTickAfterDispatch starts decor ticking when needed.
func (m *AppModel) ensureDecorTickAfterDispatch() tea.Cmd {
	if m.needsDecorTick() {
		return m.ensureDecorTick()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Blink — one-shot cursor blink timer
// ---------------------------------------------------------------------------

// needsBlink reports whether any component has a cursor that needs blinking.
func (m *AppModel) needsBlink() bool {
	if m.viewMode == ViewEdit {
		return true
	}
	if m.focus.Current() == component.FocusInput {
		return true
	}
	if m.hasPreview() && m.isPreviewFocused() {
		return true
	}
	if m.viewMode == ViewGit && m.gitPanel.NeedsBlink() {
		return true
	}
	if m.viewMode == ViewGit && m.commitTree != nil && m.commitTree.NeedsBlink() {
		return true
	}
	return m.fileTree.NeedsBlink()
}

// blinkPhase computes whether the cursor should be visible based on
// the wall clock. Phase 0 (visible) starts at blinkEpoch; each
// blinkHalfPeriod the phase alternates. Using the clock instead of
// toggle state prevents phase inversion from delayed messages.
func (m *AppModel) blinkPhase() bool {
	elapsed := time.Since(m.blinkEpoch)
	phase := int(elapsed/blinkHalfPeriod) % 2
	return phase == 0 // 0 = visible, 1 = invisible
}

// nextBlinkDeadline returns the absolute time of the next phase boundary.
func (m *AppModel) nextBlinkDeadline() time.Time {
	elapsed := time.Since(m.blinkEpoch)
	periods := elapsed/blinkHalfPeriod + 1
	return m.blinkEpoch.Add(periods * blinkHalfPeriod)
}

// blinkCmd schedules a timer targeting the next phase boundary.
// The goroutine sleeps until the absolute deadline, compensating for
// View() latency and message queue delay.
func (m *AppModel) blinkCmd() tea.Cmd {
	gen := m.blinkGen
	deadline := m.nextBlinkDeadline()
	return func() tea.Msg {
		if d := time.Until(deadline); d > 0 {
			time.Sleep(d)
		}
		return msg.BlinkMsg{Gen: gen, Deadline: deadline}
	}
}

// handleBlink schedules the next blink timer. Phase sync happens in
// View() via centralized blink logic, which sets viewDirty only when
// the phase actually changed — avoiding wasted renders on early/jittered timers.
func (m *AppModel) handleBlink(blink msg.BlinkMsg) tea.Cmd {
	if blink.Gen != m.blinkGen {
		return nil
	}
	if !m.needsBlink() {
		return nil
	}
	if m.animationsSuspended(time.Now()) {
		return m.blinkCmd()
	}
	if m.commitTree != nil {
		m.commitTree.AdvanceSpinner()
	}
	return m.blinkCmd()
}

func (m *AppModel) beginResizeQuiesce(now time.Time) {
	m.resizeFreezeUntil = now.Add(resizeAnimationQuiesce)
}

func (m *AppModel) animationsSuspended(now time.Time) bool {
	return !m.resizeFreezeUntil.IsZero() && now.Before(m.resizeFreezeUntil)
}

// ensureBlinkAfterDispatch starts a blink chain if any component needs
// cursor blinking. The generation counter ensures at most one chain runs.
func (m *AppModel) ensureBlinkAfterDispatch() tea.Cmd {
	if m.needsBlink() {
		return m.blinkCmd()
	}
	return nil
}

// ---------------------------------------------------------------------------
// LSP flush — one-shot debounced didChange
// ---------------------------------------------------------------------------

// ensureLSPFlush schedules a one-shot LSP flush timer if the editor has
// pending changes that haven't been scheduled yet (by editGeneration).
func (m *AppModel) ensureLSPFlush() tea.Cmd {
	if m.viewMode != ViewEdit || !m.focusedEditor().LSPDirty() {
		return nil
	}
	gen := m.focusedEditor().EditGeneration()
	if gen == m.lspFlushGen {
		return nil // Already scheduled for this generation.
	}
	m.lspFlushGen = gen
	return tea.Tick(lspDebounceInterval, func(_ time.Time) tea.Msg {
		return msg.LSPFlushMsg{Gen: gen}
	})
}

// handleLSPFlush fires the debounced LSP didChange notification if the
// editor is still dirty at the same generation as when the timer was scheduled.
func (m *AppModel) handleLSPFlush(flush msg.LSPFlushMsg) tea.Cmd {
	if m.viewMode != ViewEdit || !m.focusedEditor().LSPDirty() {
		return nil
	}
	if m.focusedEditor().EditGeneration() != flush.Gen {
		return nil // Stale — more edits happened; a newer flush is pending.
	}
	m.focusedEditor().ClearLSPDirty()
	return m.lspDidChangeCmd(m.focusedEditor().FilePath(), m.focusedEditor().Content())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// appendCmd appends a non-nil command to the slice.
// clampInt constrains v to [lo, hi].
func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

func appendCmd(cmds []tea.Cmd, cmd tea.Cmd) []tea.Cmd {
	if cmd != nil {
		return append(cmds, cmd)
	}
	return cmds
}

// programAdapter wraps a *tea.Program to satisfy bridge.TeaProgram.
// This adapter exists because bridge.TeaProgram.Send uses `any` to avoid
// importing bubbletea in the bridge package, while tea.Program.Send
// uses the named type tea.Msg.
type programAdapter struct {
	program *tea.Program
}

func (a *programAdapter) Send(m any) {
	a.program.Send(m)
}

// Run creates and runs the Bubble Tea program. This is the main entry point.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	// Resolve project root: explicit → git root → CWD.
	root := cfg.ProjectRoot
	if root == "" {
		cwd, _ := os.Getwd()
		if gitRoot, err := boot.FindGitRoot(cwd); err == nil {
			root = gitRoot
		} else {
			root = cwd
		}
		cfg.ProjectRoot = root
	}

	app := New(ctx, cfg, deps)
	app.fileTree.SetRoot(root)

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
		tea.WithContext(ctx),
	)

	adapter := &programAdapter{program: p}

	// Start bridges with the program reference via adapter.
	if err := app.StartBridges(adapter); err != nil {
		return err
	}

	// Register live agents so the agent panel displays them. Must run after
	// StartBridges so the activity bridge is subscribed to the bus.
	seedLiveAgents(deps)

	// In mock mode, seed additional demo data. Requests still route through
	// real Guide/Architect agents via the event bus.
	if cfg.MockMode {
		seedMockData(deps)
	}

	_, err := p.Run()

	// Restore default signal handling so a second Ctrl+C during shutdown
	// immediately terminates the process instead of being silently consumed.
	if deps.SignalStop != nil {
		deps.SignalStop()
	}

	if err != nil {
		return err
	}

	return app.Shutdown()
}
