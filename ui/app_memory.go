package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/forest"
	chatpkg "github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	memoryRefreshInterval    = 750 * time.Millisecond
	memoryIngestTimeout      = 650 * time.Millisecond
	memoryWriteRetryBase     = 15 * time.Millisecond
	memoryWriteRetryAttempts = 5

	// memoryCanvasTickInterval is the ambient animation cadence for
	// the memory view. ~60ms matches memoryview.animationTickInterval.
	// Chosen so particle drift and shimmer read as smooth without
	// burning CPU — outside memory mode, no tick is scheduled.
	memoryCanvasTickInterval = 60 * time.Millisecond
)

// MemoryViewService exposes the structural forest snapshot consumed by the TUI.
type MemoryViewService interface {
	Snapshot(ctx context.Context, input forest.ViewSnapshotInput) (*forest.ViewSnapshot, error)
}

type memoryContentRecorder interface {
	RecordContent(ctx context.Context, entry *ctxpkg.ContentEntry) error
}

func (m *AppModel) toggleMemoryMode() tea.Cmd {
	if m.viewMode == ViewMemory {
		m.exitMemoryMode()
		return nil
	}
	return m.enterMemoryMode()
}

// startMemoryCanvasTick bumps the generation counter and fires the
// first tick cmd. Subsequent ticks self-chain via continueMemoryCanvasTick
// until exitMemoryMode bumps the counter and orphans the live chain.
func (m *AppModel) startMemoryCanvasTick() tea.Cmd {
	if m.memoryView == nil || !m.memoryView.AnimationEnabled() {
		return nil
	}
	m.memoryTickGen++
	m.memoryTickOn = true
	gen := m.memoryTickGen
	return tea.Tick(memoryCanvasTickInterval, func(t time.Time) tea.Msg {
		return msg.MemoryCanvasTickMsg{Time: t, Gen: gen}
	})
}

// continueMemoryCanvasTick schedules the next tick if memory mode is
// still the active view. Returns nil when the mode has been exited or
// the generation has been superseded — stops the chain naturally.
func (m *AppModel) continueMemoryCanvasTick(gen uint64) tea.Cmd {
	if gen != m.memoryTickGen {
		return nil
	}
	if m.viewMode != ViewMemory || m.memoryView == nil || !m.memoryView.AnimationEnabled() {
		m.memoryTickOn = false
		return nil
	}
	return tea.Tick(memoryCanvasTickInterval, func(t time.Time) tea.Msg {
		return msg.MemoryCanvasTickMsg{Time: t, Gen: gen}
	})
}

// stopMemoryCanvasTick invalidates any pending ticks. Called from
// exitMemoryMode so the animation chain goes silent immediately when
// the user switches away from memory mode.
func (m *AppModel) stopMemoryCanvasTick() {
	m.memoryTickGen++
	m.memoryTickOn = false
}

// handleMemoryCanvasTick advances the memory view's animation state
// by one frame and schedules the next tick. Stale ticks (whose gen
// doesn't match) are dropped silently so old chains can't interfere
// with a newly-started one.
func (m *AppModel) handleMemoryCanvasTick(typed msg.MemoryCanvasTickMsg) tea.Cmd {
	if typed.Gen != m.memoryTickGen {
		return nil
	}
	if m.memoryView == nil || m.viewMode != ViewMemory {
		m.memoryTickOn = false
		return nil
	}
	m.memoryView.Tick()
	m.markSlotDirty(m.chatSlot())
	m.viewDirty = true
	return m.continueMemoryCanvasTick(typed.Gen)
}

func (m *AppModel) enterMemoryMode() tea.Cmd {
	switch m.viewMode {
	case ViewGit:
		m.savedGitFocus = m.focus.Current()
		m.leftRing.index = m.savedGitLeftIdx
		m.rightRing.index = m.savedGitRightIdx
		m.viewMode = ViewChat
		m.input.SetPlaceholder("Type a message...")
		m.syncViewState()
		if m.savedChatFocus != 0 {
			m.focus.SetFocus(m.savedChatFocus)
			m.syncFocusState()
		}
	case ViewEdit:
		m.exitEditMode()
	}

	m.savedChatFocus = m.focus.Current()
	m.savedMemoryLeftIdx = m.leftRing.index
	m.savedMemoryRightIdx = m.rightRing.index
	m.viewMode = ViewMemory
	m.statusBar.SetMode(ViewMemory.String())
	m.input.SetPlaceholder("Type a message...")
	m.syncViewState()
	m.focus.SetFocus(component.FocusChat)
	m.syncFocusState()
	m.refreshMemoryView(true)
	m.statusBar.SetFlash("Memory view")
	return m.startMemoryCanvasTick()
}

func (m *AppModel) exitMemoryMode() {
	m.stopMemoryCanvasTick()
	m.leftRing.index = m.savedMemoryLeftIdx
	m.rightRing.index = m.savedMemoryRightIdx
	m.viewMode = ViewChat
	m.statusBar.SetMode(ViewChat.String())
	m.syncViewState()
	target := m.savedChatFocus
	if target == 0 || target == component.FocusAgentPanel || target == component.FocusGitPanel || target == component.FocusCommitTree {
		target = component.FocusChat
	}
	m.focus.SetFocus(target)
	m.syncFocusState()
	m.statusBar.SetFlash("Chat mode")
}

func (m *AppModel) refreshMemoryView(force bool) bool {
	if m.memoryView == nil {
		return false
	}
	if !force && time.Since(m.memoryLoadedAt) < memoryRefreshInterval {
		return false
	}

	sessionID := m.activeMemorySessionID()
	if err := m.syncChatHistoryToForest(sessionID); err != nil {
		m.memoryView.SetError("MEMORY FOREST ERROR", "forest ingest failed", err)
		m.memoryLoadedAt = time.Now()
		if m.viewMode == ViewMemory {
			m.statusBar.SetFlash("Memory forest ingest failed")
		}
		return true
	}

	var snapshot *forest.ViewSnapshot
	if m.deps.Forest != nil {
		ctx, cancel := context.WithTimeout(m.ctx, 200*time.Millisecond)
		defer cancel()
		loaded, err := m.deps.Forest.Snapshot(ctx, forest.ViewSnapshotInput{
			SessionID: sessionID,
			Horizon:   forest.CanopyHorizonSession,
		})
		if err != nil {
			m.memoryView.SetError("MEMORY FOREST ERROR", "snapshot query failed", err)
			m.memoryLoadedAt = time.Now()
			if m.viewMode == ViewMemory {
				m.statusBar.SetFlash("Memory forest snapshot failed")
			}
			return true
		}
		snapshot = loaded
	}
	if snapshot == nil {
		snapshot = &forest.ViewSnapshot{}
	}

	m.memoryView.SetSnapshot(snapshot)
	m.memoryLoadedAt = time.Now()
	return true
}

func (m *AppModel) syncChatHistoryToForest(sessionID string) error {
	if m.chat == nil || m.deps.Forest == nil {
		return nil
	}
	recorder, ok := m.deps.Forest.(memoryContentRecorder)
	if !ok {
		return nil
	}
	if m.memoryIndexedIDs == nil {
		m.memoryIndexedIDs = make(map[string]struct{})
	}

	entries := m.chat.SnapshotEntries()
	if len(entries) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(m.ctx, memoryIngestTimeout)
	defer cancel()

	parentByFamily := make(map[forest.TreeFamily]string, 8)
	turnNumber := 0
	for _, entry := range entries {
		if contentEntry, family, ok := memoryContentEntryFromChat(entry, sessionID, nextMemoryTurn(&turnNumber)); ok {
			if err := m.recordMemoryContentEntry(ctx, recorder, contentEntry, family, parentByFamily); err != nil {
				return err
			}
		}
		for _, projection := range memoryConsultEntriesFromChat(entry, sessionID, &turnNumber) {
			if err := m.recordMemoryContentEntry(ctx, recorder, projection.entry, projection.family, parentByFamily); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *AppModel) recordMemoryContentEntry(
	ctx context.Context,
	recorder memoryContentRecorder,
	contentEntry *ctxpkg.ContentEntry,
	family forest.TreeFamily,
	parentByFamily map[forest.TreeFamily]string,
) error {
	if contentEntry == nil || strings.TrimSpace(contentEntry.ID) == "" {
		return nil
	}
	if _, seen := m.memoryIndexedIDs[contentEntry.ID]; seen {
		return nil
	}
	if family != "" && strings.TrimSpace(contentEntry.ParentID) == "" {
		contentEntry.ParentID = parentByFamily[family]
	}
	if err := m.recordMemoryContentEntryWithRetry(ctx, recorder, contentEntry); err != nil {
		return err
	}
	if family != "" {
		parentByFamily[family] = contentEntry.BranchID
	}
	m.memoryIndexedIDs[contentEntry.ID] = struct{}{}
	return ctx.Err()
}

func (m *AppModel) recordMemoryContentEntryWithRetry(
	ctx context.Context,
	recorder memoryContentRecorder,
	contentEntry *ctxpkg.ContentEntry,
) error {
	if recorder == nil || contentEntry == nil {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= memoryWriteRetryAttempts; attempt++ {
		err := recorder.RecordContent(ctx, contentEntry)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil || !isMemoryForestBusyError(err) || attempt == memoryWriteRetryAttempts {
			return err
		}

		backoff := memoryWriteRetryBase * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return lastErr
}

func isMemoryForestBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked")
}

func (m *AppModel) activeMemorySessionID() string {
	if m.deps.SessionManager == nil {
		return ""
	}
	active, ok := m.deps.SessionManager.GetActive()
	if !ok || active == nil {
		return ""
	}
	return strings.TrimSpace(active.ID())
}

func (m *AppModel) memoryPanelView() string {
	if m.viewMode == ViewMemory && m.memoryView != nil {
		return m.memoryView.ViewCanvas()
	}
	return m.chat.View()
}

func (m *AppModel) renderMemorySidebar(th *theme.Theme) string {
	leftW, leftH := m.layout.GetPanelSize(component.FocusSessionPanel)
	innerW := max(leftW-panelBorderSize, 1)
	innerH := max(leftH-panelBorderSize, 1)

	if m.memoryView == nil {
		return padLeftPanelBody(memorySidebarFailureText(), innerH)
	}
	if title, detail, errText, ok := m.memoryView.ErrorState(); ok {
		return padLeftPanelBody(memorySidebarErrorText(title, detail, errText), innerH)
	}
	sections := m.memoryView.Sections()
	if len(sections) == 0 {
		return padLeftPanelBody(memorySidebarFailureText(), innerH)
	}

	selected := m.memoryView.SelectedBranchID()
	selectedFamily := m.memoryView.SelectedFamily()
	rowStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	selectedStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning).Bold(true)

	lines := make([]string, 0, len(sections)*6)
	for idx, section := range sections {
		lines = append(lines, sectionHeader(section.Label, innerW, selectedFamily == section.Family, th))
		for _, entry := range section.Entries {
			prefix := "  "
			style := rowStyle
			if entry.BranchID == selected {
				prefix = "› "
				style = selectedStyle
			}
			title := truncateMemoryLabel(entry.Title, max(innerW-lipgloss.Width(prefix), 0))
			lines = append(lines, style.Render(prefix+title))
		}
		if idx < len(sections)-1 {
			lines = append(lines, "")
		}
	}
	if m.memoryView != nil {
		scroll := m.memoryViewIndexScroll()
		switch {
		case scroll > 0 && scroll < len(lines):
			lines = lines[scroll:]
		case scroll >= len(lines):
			lines = nil
		}
	}
	content := strings.Join(lines, "\n")
	return padLeftPanelBody(content, innerH)
}

func (m *AppModel) memoryViewIndexScroll() int {
	if m.memoryView == nil {
		return 0
	}
	return m.memoryView.IndexScroll()
}

func truncateMemoryLabel(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	ellipsis := "…"
	for i := len(runes); i >= 0; i-- {
		candidate := string(runes[:i]) + ellipsis
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	if lipgloss.Width(ellipsis) <= width {
		return ellipsis
	}
	return string([]rune(s)[:1])
}

func memoryContentEntryFromChat(entry chatpkg.ChatEntry, sessionID string, turnNumber int) (*ctxpkg.ContentEntry, forest.TreeFamily, bool) {
	content := strings.TrimSpace(entry.Content)
	if content == "" || entry.Streaming {
		return nil, "", false
	}

	family, ok := memoryTreeFamilyFromChat(entry)
	if !ok {
		return nil, "", false
	}

	contentType, facet := memoryContentTypeForSource(entry.Source, family)
	agentID := strings.TrimSpace(entry.AgentID)
	if entry.Source == chatpkg.SourceUser && agentID == "" {
		agentID = "user"
	}
	agentType := strings.TrimSpace(entry.AgentType)
	if entry.Source == chatpkg.SourceUser && agentType == "" {
		agentType = "user"
	}

	branchID := fmt.Sprintf("chat-branch:%s", entry.ID)
	return &ctxpkg.ContentEntry{
		ID:          fmt.Sprintf("chat:%s", entry.ID),
		SessionID:   firstNonEmpty(strings.TrimSpace(entry.SessionID), strings.TrimSpace(sessionID)),
		AgentID:     agentID,
		AgentType:   agentType,
		ContentType: contentType,
		Content:     content,
		Timestamp:   entry.Timestamp,
		TurnNumber:  turnNumber,
		BranchID:    branchID,
		Facet:       facet,
		Scope:       string(forest.ScopeEpisodic),
		Confidence:  memoryEntryConfidence(entry),
		Salience:    memoryEntrySalience(entry),
		Metadata: map[string]string{
			"memory.source": "chat",
		},
	}, family, true
}

func memoryTreeFamilyFromChat(entry chatpkg.ChatEntry) (forest.TreeFamily, bool) {
	switch entry.Source {
	case chatpkg.SourceUser:
		return forest.TreeFamilyIntent, true
	case chatpkg.SourceError:
		return forest.TreeFamilyConflict, true
	case chatpkg.SourceSystem:
		return "", false
	}

	return memoryTreeFamilyForAgentType(entry.AgentType, false)
}

func memoryTreeFamilyForAgentType(agentType string, failed bool) (forest.TreeFamily, bool) {
	if failed {
		return forest.TreeFamilyConflict, true
	}
	normalized := strings.ToLower(strings.TrimSpace(agentType))
	switch {
	case strings.HasPrefix(normalized, "scribe-"):
		return forest.TreeFamilyEvidence, true
	case normalized == "guardian":
		return forest.TreeFamilyConstraint, true
	case normalized == "designer":
		return forest.TreeFamilyIntent, true
	case normalized == "engineer":
		return forest.TreeFamilyCapability, true
	case normalized == "architect", normalized == "orchestrator", normalized == "guide":
		return forest.TreeFamilyDecision, true
	case normalized == "archivalist", strings.Contains(normalized, "tester"), strings.Contains(normalized, "inspector"):
		return forest.TreeFamilyOutcome, true
	case normalized == "academic", normalized == "librarian":
		return forest.TreeFamilyEvidence, true
	case normalized == "":
		return forest.TreeFamilyEvidence, true
	default:
		return forest.TreeFamilyEvidence, true
	}
}

func memoryContentTypeForSource(source chatpkg.ChatSource, family forest.TreeFamily) (ctxpkg.ContentType, string) {
	if source == chatpkg.SourceUser {
		return ctxpkg.ContentTypeUserPrompt, ""
	}
	if source == chatpkg.SourceError {
		return ctxpkg.ContentTypeHypothesis, string(family)
	}
	switch family {
	case forest.TreeFamilyIntent:
		return ctxpkg.ContentTypeIntentSnapshot, ""
	case forest.TreeFamilyConstraint:
		return ctxpkg.ContentTypeConstraint, ""
	case forest.TreeFamilyDecision:
		return ctxpkg.ContentTypeDecision, ""
	case forest.TreeFamilyOutcome:
		return ctxpkg.ContentTypeOutcome, ""
	case forest.TreeFamilyPreference:
		return ctxpkg.ContentTypePreference, ""
	case forest.TreeFamilyCapability:
		return ctxpkg.ContentTypeBranchSummary, string(family)
	case forest.TreeFamilyOpportunity, forest.TreeFamilyConflict:
		return ctxpkg.ContentTypeHypothesis, string(family)
	default:
		return ctxpkg.ContentTypeEvidence, ""
	}
}

type memoryProjection struct {
	entry  *ctxpkg.ContentEntry
	family forest.TreeFamily
}

func memoryConsultEntriesFromChat(entry chatpkg.ChatEntry, sessionID string, turnNumber *int) []memoryProjection {
	if len(entry.ToolCalls) == 0 {
		return nil
	}
	projections := make([]memoryProjection, 0, 4)
	for callIdx, call := range entry.ToolCalls {
		projections = append(projections, memoryConsultEntriesFromToolCalls(entry, sessionID, turnNumber, call.ToolCallKey, []int{callIdx}, call)...)
	}
	return projections
}

func memoryConsultEntriesFromToolCalls(
	entry chatpkg.ChatEntry,
	sessionID string,
	turnNumber *int,
	parentToolCallKey string,
	path []int,
	call chatpkg.ToolCallRecord,
) []memoryProjection {
	if call.InterAgent == nil || len(call.InterAgent.Children) == 0 {
		return nil
	}
	projections := make([]memoryProjection, 0, len(call.InterAgent.Children))
	for childIdx, child := range call.InterAgent.Children {
		childPath := appendPath(path, childIdx)
		if projection, ok := memoryContentEntryFromInterAgentChild(entry, child, sessionID, nextMemoryTurn(turnNumber), parentToolCallKey, childPath); ok {
			projections = append(projections, projection)
		}
		for nestedIdx, nestedCall := range child.ToolCalls {
			nestedPath := appendPath(childPath, nestedIdx)
			projections = append(projections, memoryConsultEntriesFromToolCalls(entry, sessionID, turnNumber, nestedCall.ToolCallKey, nestedPath, nestedCall)...)
		}
	}
	return projections
}

func memoryContentEntryFromInterAgentChild(
	entry chatpkg.ChatEntry,
	child chatpkg.InterAgentChildActivity,
	sessionID string,
	turnNumber int,
	parentToolCallKey string,
	path []int,
) (memoryProjection, bool) {
	content := strings.TrimSpace(memoryInterAgentChildContent(child))
	if content == "" {
		return memoryProjection{}, false
	}
	family, ok := memoryTreeFamilyForAgentType(child.AgentType, child.Failed)
	if !ok {
		return memoryProjection{}, false
	}
	contentType, facet := memoryContentTypeForSource(chatpkg.SourceAgent, family)
	stableID := strings.TrimSpace(child.CorrelationID)
	if stableID == "" {
		stableID = fmt.Sprintf("%s:%s", strings.TrimSpace(entry.ID), formatMemoryPath(path))
	}
	agentID := strings.TrimSpace(child.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(child.AgentType)
	}
	timestamp := entry.Timestamp
	if !child.ThinkingStartedAt.IsZero() {
		timestamp = child.ThinkingStartedAt
	}
	entryID := fmt.Sprintf("consult:%s", stableID)
	branchID := fmt.Sprintf("consult-branch:%s", stableID)
	metadata := map[string]string{
		"memory.source":         "inter_agent",
		"memory.parent_entry":   strings.TrimSpace(entry.ID),
		"memory.parent_call":    strings.TrimSpace(parentToolCallKey),
		"memory.correlation_id": strings.TrimSpace(child.CorrelationID),
	}
	provenance := []string{fmt.Sprintf("chat:%s", entry.ID)}
	if correlationID := strings.TrimSpace(child.CorrelationID); correlationID != "" {
		provenance = append(provenance, "correlation:"+correlationID)
	}
	return memoryProjection{
		entry: &ctxpkg.ContentEntry{
			ID:             entryID,
			SessionID:      firstNonEmpty(strings.TrimSpace(entry.SessionID), strings.TrimSpace(sessionID)),
			AgentID:        agentID,
			AgentType:      strings.TrimSpace(child.AgentType),
			ContentType:    contentType,
			Content:        content,
			Timestamp:      timestamp,
			TurnNumber:     turnNumber,
			BranchID:       branchID,
			Facet:          facet,
			Scope:          string(forest.ScopeEpisodic),
			Confidence:     memoryInterAgentConfidence(child),
			Salience:       memoryInterAgentSalience(child),
			Metadata:       metadata,
			ProvenanceRefs: provenance,
		},
		family: family,
	}, true
}

func memoryInterAgentChildContent(child chatpkg.InterAgentChildActivity) string {
	if summary := strings.TrimSpace(child.ResultSummary); summary != "" {
		return summary
	}
	for idx := len(child.ToolCalls) - 1; idx >= 0; idx-- {
		call := child.ToolCalls[idx]
		if output := strings.TrimSpace(call.Output); output != "" {
			return output
		}
		if errMsg := strings.TrimSpace(call.ErrorMsg); errMsg != "" {
			return errMsg
		}
		if call.InterAgent != nil {
			if summary := strings.TrimSpace(call.InterAgent.Summary); summary != "" {
				return summary
			}
		}
	}
	if status := strings.TrimSpace(child.ThinkingStatus); status != "" {
		return status
	}
	return strings.TrimSpace(child.ThinkingText)
}

func memoryInterAgentConfidence(child chatpkg.InterAgentChildActivity) float64 {
	if child.Failed {
		return 0.65
	}
	if child.Completed {
		return 0.82
	}
	return 0.72
}

func memoryInterAgentSalience(child chatpkg.InterAgentChildActivity) float64 {
	if child.Failed {
		return 0.84
	}
	if child.Completed {
		return 0.8
	}
	return 0.7
}

func nextMemoryTurn(turnNumber *int) int {
	if turnNumber == nil {
		return 0
	}
	*turnNumber = *turnNumber + 1
	return *turnNumber
}

func appendPath(base []int, next int) []int {
	out := append([]int(nil), base...)
	return append(out, next)
}

func formatMemoryPath(path []int) string {
	if len(path) == 0 {
		return "0"
	}
	parts := make([]string, 0, len(path))
	for _, value := range path {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ".")
}

func memoryEntryConfidence(entry chatpkg.ChatEntry) float64 {
	switch entry.Source {
	case chatpkg.SourceUser:
		return 0.95
	case chatpkg.SourceError:
		return 0.7
	default:
		return 0.8
	}
}

func memoryEntrySalience(entry chatpkg.ChatEntry) float64 {
	if entry.Importance > 0 {
		return entry.Importance
	}
	switch entry.Source {
	case chatpkg.SourceUser:
		return 0.9
	case chatpkg.SourceError:
		return 0.85
	default:
		return 0.75
	}
}

func memorySidebarFailureText() string {
	return strings.Join([]string{
		" Memory Forest ",
		"",
		" no indexed branches",
		" for this session",
	}, "\n")
}

func memorySidebarErrorText(title, detail, errText string) string {
	lines := []string{
		" " + strings.TrimSpace(title) + " ",
		"",
		" " + strings.TrimSpace(detail),
	}
	if msg := strings.TrimSpace(errText); msg != "" {
		lines = append(lines, "", " "+msg)
	}
	return strings.Join(lines, "\n")
}
