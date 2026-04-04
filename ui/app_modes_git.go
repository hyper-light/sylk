package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/ui/committree"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/conflictview"
	"github.com/adalundhe/sylk/ui/diffview"
	"github.com/adalundhe/sylk/ui/gitpanel"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/mergediff"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/tabbar"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chordState tracks which chord prefix is pending.
type chordState int

const (
	chordNone        chordState = iota
	chordSession                // Alt+S pressed, waiting for arrow.
	chordAgent                  // Alt+A pressed, waiting for arrow.
	chordView                   // Alt+V pressed, waiting for arrow.
	chordMultiCursor            // Alt+Shift+D pressed, waiting for up/down/d.
)

// chordArrowDelta maps arrow key strings (including alt-held variants) to
// a cycle direction. The alt+arrow variants handle the common case where the
// user still holds Alt from the prefix key when pressing the arrow.
var chordArrowDelta = map[string]int{
	"left":      -1,
	"right":     1,
	"alt+left":  -1,
	"alt+right": 1,
}

// chordFocusGuard restricts specific chords to a required focused pane.
// Chords not listed here are global (activate regardless of focus).
var chordFocusGuard = map[chordState]component.FocusID{}

// chordDisplay holds the label, key hints, and color for a chord hint overlay.
type chordDisplay struct {
	label string
	keys  string
	color func(*theme.Palette) lipgloss.Color
}

// chordDisplays maps chord states to their display properties.
// Session select uses Primary (blue), Agent select uses Success (green).
var chordDisplays = map[chordState]chordDisplay{
	chordSession:     {"Session select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Primary }},
	chordAgent:       {"Agent select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Success }},
	chordView:        {"View select", "←/→ cycle", func(p *theme.Palette) lipgloss.Color { return p.Accent }},
	chordMultiCursor: {"Multi-cursor", "↓ add  ↑ remove  d next word  esc exit", func(p *theme.Palette) lipgloss.Color { return p.Warning }},
}

// chordHint returns a styled hint string when a chord is active, or "" when idle.
// Blocked chords (edit mode) render as "Exit edit mode to <action>" instead of
// the normal cycling hint, so chordBlocked hints are only rendered by
// codePanelView (not overlayChordHint on other panels).
func (m *AppModel) chordHint(th *theme.Theme) string {
	if m.chordBlocked {
		return ""
	}
	disp, ok := chordDisplays[m.chord]
	if !ok {
		return ""
	}
	labelStyle := lipgloss.NewStyle().Foreground(disp.color(&th.Palette)).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return labelStyle.Render(disp.label) + keyStyle.Render("  "+disp.keys+"  any key to exit ")
}

// blockedChordHint returns a styled hint for a chord blocked by the current mode.
// Returns "" when no blocked chord is active.
func (m *AppModel) blockedChordHint(th *theme.Theme) string {
	if !m.chordBlocked {
		return ""
	}
	disp, ok := chordDisplays[m.chord]
	if !ok {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	labelStyle := lipgloss.NewStyle().Foreground(disp.color(&th.Palette)).Bold(true)
	mode := "edit"
	if m.viewMode == ViewGit {
		mode = "git"
	}
	return mutedStyle.Render("Exit "+mode+" mode to ") + labelStyle.Render(disp.label) + mutedStyle.Render("  (press any key to dismiss) ")
}

// handleChord processes two-key chord shortcuts: Alt+S then Left/Right for sessions,
// Alt+A then Left/Right for agents. The chord stays active while arrows are pressed,
// allowing repeated cycling. Any non-arrow key cancels the chord and falls
// through to normal handling. Returns (cmd, true) if consumed.
// chordKeyMap maps chord trigger keys to their chord state.
var chordKeyMap = map[string]chordState{
	"alt+s": chordSession,
	"alt+a": chordAgent,
	"alt+v": chordView,
}

func (m *AppModel) handleChord(key tea.KeyMsg) (tea.Cmd, bool) {
	ks := key.String()
	if m.dismissBlockedChord() {
		return nil, true
	}
	if cmd, handled := m.handleMultiCursorChordTrigger(ks); handled {
		return cmd, true
	}
	if cmd, handled := m.handleActiveMultiCursorChord(ks); handled {
		return cmd, true
	}
	if cmd, handled := m.handleChordToggle(ks); handled {
		return cmd, true
	}
	if m.chord == chordNone {
		return nil, false
	}
	return m.handleActiveChordKey(ks)
}

func (m *AppModel) dismissBlockedChord() bool {
	if !m.chordBlocked {
		return false
	}
	m.chord = chordNone
	m.chordBlocked = false
	return true
}

func (m *AppModel) handleMultiCursorChordTrigger(ks string) (tea.Cmd, bool) {
	if ks != "alt+D" {
		return nil, false
	}
	if m.viewMode != ViewEdit || !m.isEditorFocused() {
		return nil, false
	}
	if m.chord == chordMultiCursor {
		m.chord = chordNone
	} else {
		m.chord = chordMultiCursor
	}
	return nil, true
}

func (m *AppModel) handleActiveMultiCursorChord(ks string) (tea.Cmd, bool) {
	if m.chord != chordMultiCursor {
		return nil, false
	}
	if m.viewMode != ViewEdit || !m.isEditorFocused() {
		m.chord = chordNone
		return nil, false
	}
	return m.handleMultiCursorChord(ks)
}

func (m *AppModel) handleChordToggle(ks string) (tea.Cmd, bool) {
	target, ok := chordKeyMap[ks]
	if !ok {
		return nil, false
	}
	if cmd, handled := m.handleBlockedChordToggle(target); handled {
		return cmd, true
	}
	if !m.canFocusChord(target) {
		return nil, false
	}
	if m.chordToggleDebounced(ks) {
		return nil, true
	}
	m.toggleChord(target)
	return nil, true
}

func (m *AppModel) handleBlockedChordToggle(target chordState) (tea.Cmd, bool) {
	if m.viewMode == ViewChat || target == chordView {
		return nil, false
	}
	m.chord = target
	m.chordBlocked = true
	return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
		return msg.ChordBlockedExpireMsg{}
	}), true
}

func (m *AppModel) canFocusChord(target chordState) bool {
	required, guarded := chordFocusGuard[target]
	return !guarded || m.focus.Current() == required
}

func (m *AppModel) chordToggleDebounced(ks string) bool {
	now := time.Now()
	if ks == m.lastToggleKey && now.Sub(m.lastToggleAt) < overlayToggleDebounce {
		return true
	}
	m.lastToggleKey = ks
	m.lastToggleAt = now
	return false
}

func (m *AppModel) toggleChord(target chordState) {
	if m.chord == target {
		m.chord = chordNone
		return
	}
	m.chord = target
}

func (m *AppModel) handleActiveChordKey(ks string) (tea.Cmd, bool) {
	if delta, ok := chordArrowDelta[ks]; ok {
		return m.dispatchChordCycle(m.chord, delta), true
	}
	m.chord = chordNone
	return nil, true
}

// multiCursorChordKeys maps key strings accepted during the multi-cursor chord
// to their action. The chord stays active after each action, allowing repeated
// presses. Only "up"/"down" (plain or with alt held) and "d"/"alt+d" are valid.
var multiCursorChordKeys = map[string]string{
	"up":       "remove",
	"down":     "below",
	"alt+up":   "remove",
	"alt+down": "below",
	"d":        "word",
	"alt+d":    "word",
}

// handleMultiCursorChord processes keys while the multi-cursor chord is active.
// Returns (cmd, true) if consumed, or cancels the chord and returns (nil, true)
// so the cancelled key is swallowed (user presses a non-chord key to exit).
func (m *AppModel) handleMultiCursorChord(ks string) (tea.Cmd, bool) {
	// Esc cancels chord and clears all secondary cursors.
	if ks == "esc" {
		m.chord = chordNone
		if ed := m.focusedEditor(); ed != nil {
			ed.ClearSecondaryCursors()
		}
		return nil, true
	}
	action, ok := multiCursorChordKeys[ks]
	if !ok {
		// Any non-chord key cancels the chord (but keeps cursors).
		m.chord = chordNone
		return nil, true
	}
	ed := m.focusedEditor()
	switch action {
	case "remove":
		ed.RemoveBottomCursor()
	case "below":
		ed.AddCursorBelow()
	case "word":
		ed.AddCursorAtNextOccurrence()
	}
	return nil, true
}

// dispatchChordCycle routes a completed chord to the target panel.
func (m *AppModel) dispatchChordCycle(chord chordState, delta int) tea.Cmd {
	switch chord {
	case chordSession:
		if delta < 0 {
			return m.sessionPanel.CyclePrev()
		}
		return m.sessionPanel.CycleNext()
	case chordAgent:
		if delta < 0 {
			m.agentPanel.CyclePrev()
		} else {
			m.agentPanel.CycleNext()
		}
		m.syncManualTargetFromAgentSelection()
	case chordView:
		return m.cycleViewSlot(delta)
	}
	return nil
}

// cycleViewSlot dispatches Alt+V cycling to the appropriate ring(s).
// Two active rings: both cycle in lockstep so the panel pair toggles as a unit.
// One active ring: direction is forward/backward within that ring.
func (m *AppModel) cycleViewSlot(delta int) tea.Cmd {
	// Capture pre-cycle focus so syncGitModeForRing can save it
	// for the outgoing view before the ring changes focus.
	preFocus := m.focus.Current()

	bothActive := !m.leftRing.empty() && !m.rightRing.empty()
	if bothActive {
		oldLeft := m.leftRing.current()
		oldRight := m.rightRing.current()
		m.leftRing.cycle(delta)
		m.rightRing.cycle(delta)
		switch preFocus {
		case oldLeft:
			m.focus.SetFocus(m.leftRing.current())
		case oldRight:
			m.focus.SetFocus(m.rightRing.current())
		}
		cmd := m.syncModeForRing(preFocus)
		m.syncViewState()
		return cmd
	}
	if !m.leftRing.empty() {
		return m.cycleRing(&m.leftRing, delta, preFocus)
	}
	if !m.rightRing.empty() {
		return m.cycleRing(&m.rightRing, delta, preFocus)
	}
	return nil
}

// cycleRing advances a single ring by delta, transferring focus if needed.
func (m *AppModel) cycleRing(ring *viewRing, delta int, preFocus component.FocusID) tea.Cmd {
	oldPanel := ring.current()
	ring.cycle(delta)
	newPanel := ring.current()
	if m.focus.Current() == oldPanel {
		m.focus.SetFocus(newPanel)
	}
	cmd := m.syncModeForRing(preFocus)
	m.syncViewState()
	return cmd
}

// ringTargetMode determines the view mode implied by current ring positions.
func (m *AppModel) ringTargetMode() ViewMode {
	if isGitPanel(m.leftRing.current()) || isGitPanel(m.rightRing.current()) {
		return ViewGit
	}
	active := m.leftRing.current()
	if !m.rightRing.empty() {
		active = m.rightRing.current()
	}
	if active == component.FocusCodeViewer {
		return ViewEdit
	}
	return ViewChat
}

// syncModeForRing detects mode transitions (CHAT / EDIT / GIT) after a
// ring cycle and symmetrically saves outgoing focus / restores incoming
// focus. preFocus is the focus captured before the ring advanced.
func (m *AppModel) syncModeForRing(preFocus component.FocusID) tea.Cmd {
	target := m.ringTargetMode()
	if target == m.viewMode {
		return nil
	}
	old := m.viewMode
	m.viewMode = target
	m.saveOutgoingRingFocus(old, preFocus)
	m.exitRingMode(old)
	cmd := m.enterRingMode(target, old)
	m.statusBar.SetMode(target.String())
	return cmd
}

func (m *AppModel) saveOutgoingRingFocus(old ViewMode, preFocus component.FocusID) {
	switch old {
	case ViewGit:
		m.savedGitFocus = preFocus
	case ViewEdit:
		m.savedEditFocus = preFocus
	default:
		m.savedChatFocus = preFocus
	}
}

func (m *AppModel) exitRingMode(old ViewMode) {
	if old != ViewGit {
		return
	}
	m.viewMode = ViewChat
	m.input.SetPlaceholder("Type a message...")
}

func (m *AppModel) enterRingMode(target, old ViewMode) tea.Cmd {
	switch target {
	case ViewGit:
		return m.enterGitRingMode()
	case ViewEdit:
		return m.enterEditRingMode(old)
	default:
		m.enterChatRingMode(old)
		return nil
	}
}

func (m *AppModel) enterGitRingMode() tea.Cmd {
	m.viewMode = ViewGit
	m.resizeGitPanels()
	m.input.SetPlaceholder("git>")
	m.restoreRingFocus(m.savedGitFocus)
	return m.loadGitRingData()
}

func (m *AppModel) enterEditRingMode(old ViewMode) tea.Cmd {
	m.clearPreGitEditMode(old)
	return m.enterEditMode()
}

func (m *AppModel) enterChatRingMode(old ViewMode) {
	m.clearPreGitEditMode(old)
	m.restoreRingFocus(m.savedChatFocus)
}

func (m *AppModel) clearPreGitEditMode(old ViewMode) {
	if old == ViewGit && m.preGitEditMode {
		m.preGitEditMode = false
	}
}

func (m *AppModel) restoreRingFocus(fid component.FocusID) {
	if fid != 0 {
		m.focus.SetFocus(fid)
	}
}

func (m *AppModel) loadGitRingData() tea.Cmd {
	if m.gitDataLoaded {
		return nil
	}
	m.gitDataLoaded = true
	return tea.Batch(m.gitPanel.LoadData(), m.loadGitBranchesCmd())
}

// ---------------------------------------------------------------------------
// Chat mode (Alt+C)
// ---------------------------------------------------------------------------

// switchToChatMode exits git or edit mode to return to chat mode.
// No-op if already in chat mode.
func (m *AppModel) switchToChatMode() tea.Cmd {
	switch m.viewMode {
	case ViewGit:
		return m.exitGitMode()
	case ViewMemory:
		m.exitMemoryMode()
		return nil
	case ViewEdit:
		m.exitEditMode()
		return nil
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Edit mode (Alt+E)
// ---------------------------------------------------------------------------

// toggleEditMode switches between inline editing and read-only code viewing.
func (m *AppModel) toggleEditMode() tea.Cmd {
	switch m.viewMode {
	case ViewGit:
		m.preGitEditMode = false
		m.exitGitMode()
		return m.enterEditMode()
	case ViewEdit:
		m.exitEditMode()
		return nil
	default:
		return m.enterEditMode()
	}
}

// enterEditMode activates inline vim editing in the code panel slot.
// Returns a Cmd that sends didOpen so the LSP client tracks the document
// for subsequent didChange notifications. Idempotent if already tracked.
func (m *AppModel) enterEditMode() tea.Cmd {
	// Dismiss non-view chords — chordView is allowed in edit mode.
	if m.chord != chordView {
		m.chord = chordNone
		m.chordBlocked = false
	}

	// Save ring state and chat-mode focus.
	m.savedLeftIdx = m.leftRing.index
	m.savedRightIdx = m.rightRing.index
	m.savedChatFocus = m.focus.Current()

	// Ensure the code panel is visible.
	switch m.layout.Mode() {
	case layout.ThreeColumn:
		m.leftRing.setTo(component.FocusFileTree)
	case layout.TwoColumn:
		m.leftRing.setTo(component.FocusFileTree)
		m.rightRing.setTo(component.FocusCodeViewer)
	case layout.SingleColumn:
		m.leftRing.setTo(component.FocusCodeViewer)
	}

	// Restore tab order from a previous edit session, or initialise from
	// the code panel's current file.
	var lspCmd tea.Cmd
	if len(m.focusedTabOrder()) > 0 {
		// Re-entering edit mode — restore the last-active tab.
		idx := 0
		for i, p := range m.focusedTabOrder() {
			if p == m.savedActiveTab {
				idx = i
				break
			}
		}
		fp := m.focusedTabOrder()[idx]
		var content string
		if m.restoreFromCache(fp) {
			content = m.focusedEditor().Content()
		} else {
			data, _ := os.ReadFile(fp)
			content = string(data)
			m.focusedEditor().OpenFile(fp, content, detectEditorLanguage(fp))
		}
		m.fileTree.SetActiveFile(fp)
		m.fileTree.RevealPath(fp)
		lspCmd = m.lspReopenCmd(fp, detectEditorLanguage(fp), content)
	} else if fp := m.codePanel.FilePath(); fp != "" {
		m.setFocusedTabOrder([]string{fp})
		var content string
		if m.restoreFromCache(fp) {
			content = m.focusedEditor().Content()
		} else {
			content = m.codePanel.Content()
			m.focusedEditor().OpenFile(fp, content, m.codePanel.Language())
		}
		m.fileTree.SetActiveFile(fp)
		m.fileTree.RevealPath(fp)
		lspCmd = m.lspReopenCmd(fp, detectEditorLanguage(fp), content)
	} else {
		m.focusedEditor().ClearFile()
	}

	m.viewMode = ViewEdit
	m.viewMode = ViewEdit

	// Size the editor to match the code panel slot. resizeInlineEditor
	// handles all states (split preview, full preview, editor-only).
	m.resizeInlineEditor()
	m.fileTree.SetEditMode(true)
	m.statusBar.SetMode("EDIT")
	m.input.SetPlaceholder(":")

	// Rebuild tab order for edit mode first, then restore focus.
	// savedEditFocus preserves the last focused pane's dynamic ID
	// across chat→edit toggles. Falls back to focused editor pane.
	m.syncViewState()
	target := m.savedEditFocus
	if !pane.IsPaneFocus(target) || !m.paneTree.Contains(pane.PaneIDFromFocus(target)) {
		target = pane.PaneFocusID(m.focusedPane)
	}
	m.focus.SetFocus(target)
	m.syncFocusState()
	m.statusBar.SetFlash("Edit mode")
	return lspCmd
}

// exitEditMode deactivates inline editing and syncs content back to the
// code viewer.
func (m *AppModel) exitEditMode() {
	// NOTE: We do NOT call lspDidCloseAsync here. The close is handled
	// atomically by lspReopenCmd when the user re-enters edit mode, which
	// avoids a race condition between async didClose and async didOpen.

	// Capture content for the code panel before detaching the editor.
	content := m.focusedEditor().Content()
	fp := m.focusedEditor().FilePath()
	lang := m.focusedEditor().Language()

	// Save current editor state so re-entering edit mode restores it.
	m.savedActiveTab = fp
	m.savedEditFocus = m.focus.Current()
	m.detachCurrentEditor()

	m.codePanel.SetContent(content, fp, lang)

	// Restore ring state.
	m.leftRing.index = m.savedLeftIdx
	m.rightRing.index = m.savedRightIdx

	m.viewMode = ViewChat
	m.viewMode = ViewChat
	m.fileTree.SetEditMode(false)
	m.editCmdInput = false
	// Preserve chordView — it's allowed across mode transitions.
	if m.chord != chordView {
		m.chord = chordNone
		m.chordBlocked = false
	}
	// tabOrder is intentionally preserved so re-entering edit mode
	// restores all previously open tabs.
	m.statusBar.SetMode("CHAT")
	m.input.SetPlaceholder("Type a message...")

	// Rebuild tab order for chat mode first, then restore focus.
	// This ensures savedChatFocus is evaluated against the fresh tab order.
	m.syncViewState()
	m.focus.SetFocus(m.savedChatFocus)
	m.syncFocusState()
	m.statusBar.SetFlash("View mode")
}

// ---------------------------------------------------------------------------
// Git mode (Alt+G)
// ---------------------------------------------------------------------------

// gitQuickStatusMsg carries lightweight working-tree + stash state so that
// UI elements (commit button, unstash badge) can update without waiting for
// the full branch enumeration in loadGitBranchesCmd.
type gitQuickStatusMsg struct {
	dirty          bool
	conflicts      bool
	hasIndexStaged bool
	hasStash       bool
}

// gitBranchesLoadedMsg carries branch data loaded asynchronously for the
// commit tree panel's branch view.
type gitBranchesLoadedMsg struct {
	branches       []committree.BranchNode
	defaultBranch  string
	dirty          bool // working tree has uncommitted changes
	conflicts      bool // working tree has merge conflicts
	hasIndexStaged bool // git index has staged changes (via git add)
	hasStash       bool // stash list is non-empty
}

// gitBranchFullyLoadedMsg carries commit nodes with their diff stats,
// loaded atomically so the UI can transition from spinner to data in one step.
type gitBranchFullyLoadedMsg struct {
	branch  string
	nodes   []committree.TreeNode
	stats   map[string][2]int
	hasMore bool
}

// gitMoreCommitsLoadedMsg carries an additional page of commits appended
// during infinite scroll.
type gitMoreCommitsLoadedMsg struct {
	branch  string
	nodes   []committree.TreeNode
	stats   map[string][2]int
	hasMore bool
}

// gitBranchDAGLoadedMsg carries a full DAG of branch-unique commits with
// graph layout data, loaded atomically.
type gitBranchDAGLoadedMsg struct {
	branch       string
	nodes        []committree.TreeNode
	stats        map[string][2]int
	graphRows    []committree.GraphRow
	maxGraphLane int
}

type branchDeletedMsg struct{ name string }
type branchStashesLoadedMsg struct {
	stashes map[string][]git.BranchStashMeta
}
type branchDeleteFailedMsg struct{ reason string }
type branchCreatedMsg struct{ name string }
type branchCreateFailedMsg struct{ reason string }
type commitSucceededMsg struct{ message string }
type commitFailedMsg struct{ reason string }
type stashSucceededMsg struct{ count int }
type stashFailedMsg struct{ reason string }
type unstashSucceededMsg struct{}
type unstashFailedMsg struct{ reason string }
type pullSucceededMsg struct{ name string }
type pullFailedMsg struct{ reason string }
type pushSucceededMsg struct{ name string }
type pushFailedMsg struct{ reason string }
type resetSucceededMsg struct{ mode string }
type resetFailedMsg struct{ reason string }
type revertSucceededMsg struct{ hash string }
type revertFailedMsg struct{ reason string }
type commitCheckoutSucceededMsg struct{ hash string }
type commitCheckoutFailedMsg struct{ reason string }

type sequencerResultMsg struct{ status *git.SequencerStatus }
type sequencerFailedMsg struct{ reason string }
type sequencerAbortedMsg struct{}
type sequencerAbortFailedMsg struct{ reason string }
type conflictResolveWrittenMsg struct{ path string }
type conflictResolveFailedMsg struct{ path, reason string }

// pendingSequencerOp stores deferred sequencer parameters while the user
// reviews a conflict preview or integration detection modal.
type pendingSequencerOp struct {
	op           string // "cherry-pick", "rebase", "merge"
	phase        int    // 1=integration modal, 2=conflict preview modal
	hashes       []string
	target       string
	sourceBranch string // rebase: branch being rebased (may differ from HEAD)
	plan         []git.RebasePlanEntry
	delete       bool // merge: delete source branch after
}

// loadGitBranchesCmd returns a tea.Cmd that loads all local branches
// via go-git and converts them to BranchNode data for the commit tree panel.
func (m *AppModel) loadGitBranchesCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		branches, err := bus.ListBranches()
		if err != nil {
			return gitBranchesLoadedMsg{}
		}
		defaultBranch := bus.DefaultBranch()

		// Working tree status.
		statuses, hasIndexStaged, _ := bus.UncommittedFileStatuses()
		dirty := len(statuses) > 0
		var conflicts bool
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}

		const commitLimit = 1000
		branchNames := make([]string, len(branches))
		for i, b := range branches {
			branchNames[i] = b.Name
		}
		commitCounts := bus.CountBranchOnlyCommitsBatch(branchNames, defaultBranch, commitLimit)
		inferredParents := bus.InferBranchParents(branches, defaultBranch)

		nodes := make([]committree.BranchNode, len(branches))
		for i, b := range branches {
			nodes[i] = committree.BranchNode{
				Name:        b.Name,
				Hash:        b.Hash,
				ShortHash:   b.ShortHash,
				Subject:     b.Subject,
				AuthorTime:  b.AuthorTime,
				CreatedTime: b.CreatedTime,
				IsHead:      b.IsHead,
				Parent:      inferredParents[b.Name],
			}
			if bc, ok := commitCounts[b.Name]; ok {
				nodes[i].CommitCount = bc.Count
				nodes[i].CommitCountCapped = bc.Capped
				nodes[i].BehindCount = bc.Behind
				nodes[i].BehindCountCapped = bc.BehindCapped
			}
		}

		// Detect detached HEAD: no branch has IsHead=true.
		if detached := buildDetachedNode(bus, nodes); detached != nil {
			nodes = append([]committree.BranchNode{*detached}, nodes...)
		}

		return gitBranchesLoadedMsg{
			branches:       nodes,
			defaultBranch:  defaultBranch,
			dirty:          dirty,
			conflicts:      conflicts,
			hasIndexStaged: hasIndexStaged,
			hasStash:       bus.HasStash(),
		}
	}
}

// buildDetachedNode returns a synthetic BranchNode if HEAD is detached
// (no branch has IsHead=true). Returns nil if HEAD is on a branch.
func buildDetachedNode(bus *git.GitBus, nodes []committree.BranchNode) *committree.BranchNode {
	for _, n := range nodes {
		if n.IsHead {
			return nil
		}
	}
	hash, err := bus.GetHead()
	if err != nil {
		return nil
	}
	short := hash[:min(len(hash), 7)]
	node := committree.BranchNode{
		Name:       "(detached) " + short,
		Hash:       hash,
		ShortHash:  short,
		IsHead:     true,
		IsDetached: true,
	}
	if ci, err := bus.GetCommit(hash); err == nil {
		node.Subject = ci.Subject
		node.AuthorTime = ci.AuthorTime
	}
	return &node
}

// quickGitStatusCmd returns a tea.Cmd that checks only working-tree dirty
// state, index staging, and stash presence. Runs in O(index-scan) time
// without the expensive branch enumeration and commit counting.
func (m *AppModel) quickGitStatusCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		statuses, hasIndexStaged, _ := bus.UncommittedFileStatuses()
		dirty := len(statuses) > 0
		var conflicts bool
		for _, s := range statuses {
			if s == "!" {
				conflicts = true
				break
			}
		}
		return gitQuickStatusMsg{
			dirty:          dirty,
			conflicts:      conflicts,
			hasIndexStaged: hasIndexStaged,
			hasStash:       bus.HasStash(),
		}
	}
}

// detectSequencerStateCmd checks for an active rebase/merge/cherry-pick
// by reading .git/ filesystem markers.
func (m *AppModel) detectSequencerStateCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		state := bus.DetectSequencerFileState()
		return msg.SequencerFileStateMsg{State: state}
	}
}

// computeDivergenceBatchCmd asynchronously computes ahead/behind counts
// for all branches relative to their configured upstream tracking refs.
func (m *AppModel) computeDivergenceBatchCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		branches, err := bus.ListBranches()
		if err != nil {
			return msg.DivergenceLoadedMsg{}
		}
		names := make([]string, len(branches))
		for i, b := range branches {
			names[i] = b.Name
		}
		info, err := bus.ComputeDivergenceBatch(names)
		if err != nil {
			return msg.DivergenceLoadedMsg{}
		}
		return msg.DivergenceLoadedMsg{Info: info}
	}
}

// loadBranchStashesCmd asynchronously lists branch stashes for stash badges.
func (m *AppModel) loadBranchStashesCmd() tea.Cmd {
	bus := m.gitBus
	if bus == nil {
		return nil
	}
	return func() tea.Msg {
		stashes, err := bus.ListBranchStashes()
		if err != nil {
			return branchStashesLoadedMsg{}
		}
		return branchStashesLoadedMsg{stashes: stashes}
	}
}

// conflictPreviewMergeCmd previews merge conflicts before executing.
func (m *AppModel) conflictPreviewMergeCmd(source, target string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewMerge(source, target)
		if err != nil || result.Clean {
			return msg.ConflictPreviewMsg{Result: result, Op: "merge"}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "merge"}
	}
}

// conflictPreviewRebaseCmd previews rebase conflicts before executing.
func (m *AppModel) conflictPreviewRebaseCmd(onto, sourceBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewRebase(onto, sourceBranch)
		if err != nil || result == nil {
			return msg.ConflictPreviewMsg{Result: result, Op: "rebase"}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "rebase"}
	}
}

// conflictPreviewCherryPickCmd previews cherry-pick conflicts before executing.
func (m *AppModel) conflictPreviewCherryPickCmd(hashes []string, params any) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		result, err := bus.PreviewCherryPick(hashes)
		if err != nil || result == nil {
			return msg.ConflictPreviewMsg{Result: result, Op: "cherry-pick", Params: params}
		}
		return msg.ConflictPreviewMsg{Result: result, Op: "cherry-pick", Params: params}
	}
}

// detectIntegrationCmd checks for already-integrated cherry-pick commits.
func (m *AppModel) detectIntegrationCmd(hashes []string, targetBranch string, params any) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		results, err := bus.DetectIntegratedCommits(hashes, targetBranch)
		if err != nil {
			return msg.IntegrationDetectedMsg{Params: params}
		}
		return msg.IntegrationDetectedMsg{Results: results, Params: params}
	}
}

// preserveAbortCmd creates a backup ref and stash before sequencer abort.
func (m *AppModel) preserveAbortCmd(opName string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		pres, err := bus.PreserveBeforeAbort(opName)
		return msg.AbortPreservedMsg{Preservation: pres, Err: err}
	}
}

// fetchBaseContentCmd reads ancestor blob content for the three-way diff view.
func (m *AppModel) fetchBaseContentCmd(path, baseHash string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		content, err := bus.ReadBlobContent(baseHash)
		return conflictview.BaseContentResponseMsg{
			Path: path, Content: content, Err: err,
		}
	}
}

// fetchStepPreviewCmd computes the commit diff for the step preview overlay.
func (m *AppModel) fetchStepPreviewCmd(hash string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		return m.buildStepPreview(bus, hash)
	}
}

// buildStepPreview resolves a commit's parent and diffs against it.
func (m *AppModel) buildStepPreview(bus *git.GitBus, hash string) conflictview.StepPreviewResponseMsg {
	ci, err := bus.GetCommit(hash)
	if err != nil || len(ci.ParentHashes) == 0 {
		return conflictview.StepPreviewResponseMsg{}
	}
	diffs, err := bus.GetDiff(ci.ParentHashes[0], ci.Hash)
	if err != nil {
		return conflictview.StepPreviewResponseMsg{}
	}
	conflictPaths := m.conflictPathSet()
	fileDiffs := make([]conflictview.FileDiffSummary, len(diffs))
	for i, fd := range diffs {
		fileDiffs[i] = conflictview.FileDiffSummary{
			Path: fd.Path, Additions: fd.Additions, Deletions: fd.Deletions,
		}
	}
	return conflictview.StepPreviewResponseMsg{
		Preview: &conflictview.StepPreview{
			Hash: ci.ShortHash, Subject: ci.Message,
			FileDiffs: fileDiffs, ConflictPaths: conflictPaths,
		},
	}
}

// conflictPathSet returns the set of paths in the current conflict view.
func (m *AppModel) conflictPathSet() map[string]bool {
	if m.conflictView == nil {
		return nil
	}
	entries := m.conflictView.Entries()
	paths := make(map[string]bool, len(entries))
	for _, e := range entries {
		paths[e.Path] = true
	}
	return paths
}

// sequencerUndoStepCmd issues the SequencerUndoStep command via the git bus.
func (m *AppModel) sequencerUndoStepCmd() tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		status, err := bus.SequencerUndoStep()
		if err != nil {
			return sequencerFailedMsg{reason: err.Error()}
		}
		return sequencerResultMsg{status: status}
	}
}

// preCommitSizeThreshold is the file size (bytes) above which a staged file
// is flagged in the pre-commit pipeline. Derived from: 5 MiB is the standard
// GitHub push warning threshold and a common large-file guardrail.
const preCommitSizeThreshold = 5 * 1024 * 1024

// preCommitCheckCmd runs the large-file and secret scan pipeline in sequence.
// If large files are found, returns LargeFilesDetectedMsg (secrets deferred
// to after user confirmation). Otherwise proceeds to secret scan.
func (m *AppModel) preCommitCheckCmd(paths []string, message string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		large, err := bus.ScanStagedLargeFiles(paths, preCommitSizeThreshold)
		if err == nil && len(large) > 0 {
			return msg.LargeFilesDetectedMsg{Files: large, Paths: paths, Message: message}
		}
		secrets, err := bus.ScanStagedSecrets(paths)
		if err == nil && len(secrets) > 0 {
			return msg.SecretsDetectedMsg{Findings: secrets, Paths: paths, Message: message}
		}
		return msg.PreCommitCleanMsg{Paths: paths, Message: message}
	}
}

// formatFileSize renders a byte count as a human-readable string.
func formatFileSize(size int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(gib))
	case size >= mib:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mib))
	case size >= kib:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(kib))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// loadBranchCommitsAndStatsCmd loads the first page of commits with diff
// stats for a branch. Uses flat first-parent pagination for fast initial
// display. Subsequent pages are loaded on demand via loadMoreCommitsCmd.
func (m *AppModel) loadBranchCommitsAndStatsCmd(branchName, defaultBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		commits, hasMore, err := bus.ListBranchOnlyCommits(branchName, defaultBranch, "", commitPageSize)
		if err != nil {
			return gitBranchFullyLoadedMsg{branch: branchName}
		}
		nodes := commitsToTreeNodes(commits)
		if len(nodes) > 0 {
			nodes[0].Branch = branchName
		}
		stats := loadStatsForNodes(bus, nodes)
		return gitBranchFullyLoadedMsg{
			branch:  branchName,
			nodes:   nodes,
			stats:   stats,
			hasMore: hasMore,
		}
	}
}

// loadBranchDAGCmd loads the full DAG of branch-unique commits with graph
// layout. Falls back to flat first-parent mode if the DAG exceeds the limit.
func (m *AppModel) loadBranchDAGCmd(branchName, defaultBranch string) tea.Cmd {
	bus := m.gitBus
	return func() tea.Msg {
		commits, err := bus.ListBranchDAGCommits(branchName, defaultBranch, git.DagCommitLimit)
		if err != nil || len(commits) == 0 {
			// Fall back to flat first-parent pagination.
			return m.loadBranchCommitsAndStatsFallback(bus, branchName, defaultBranch)
		}
		nodes := commitsToTreeNodes(commits)
		if len(nodes) > 0 {
			nodes[0].Branch = branchName
		}
		graphRows, maxLane := committree.AssignLanes(nodes)
		stats := loadStatsForNodes(bus, nodes)
		return gitBranchDAGLoadedMsg{
			branch:       branchName,
			nodes:        nodes,
			stats:        stats,
			graphRows:    graphRows,
			maxGraphLane: maxLane,
		}
	}
}

// loadBranchCommitsAndStatsFallback is the flat-mode fallback used when DAG
// loading fails or exceeds limits. Called from within the DAG cmd goroutine.
func (m *AppModel) loadBranchCommitsAndStatsFallback(bus *git.GitBus, branchName, defaultBranch string) tea.Msg {
	commits, hasMore, err := bus.ListBranchOnlyCommits(branchName, defaultBranch, "", commitPageSize)
	if err != nil {
		return gitBranchFullyLoadedMsg{branch: branchName}
	}
	nodes := commitsToTreeNodes(commits)
	if len(nodes) > 0 {
		nodes[0].Branch = branchName
	}
	stats := loadStatsForNodes(bus, nodes)
	return gitBranchFullyLoadedMsg{
		branch:  branchName,
		nodes:   nodes,
		stats:   stats,
		hasMore: hasMore,
	}
}

// loadMoreCommitsCmd loads the next page of commits for infinite scroll,
// starting after the last loaded commit hash.
func (m *AppModel) loadMoreCommitsCmd() tea.Cmd {
	if m.commitTree == nil {
		return nil
	}
	branch := m.commitTree.ActiveBranch()
	lastHash := m.commitTree.LastHash()
	defaultBranch := m.commitTree.GetDefaultBranch()
	bus := m.gitBus
	return func() tea.Msg {
		commits, hasMore, err := bus.ListBranchOnlyCommits(branch, defaultBranch, lastHash, commitPageSize)
		if err != nil || len(commits) == 0 {
			return gitMoreCommitsLoadedMsg{branch: branch}
		}
		nodes := commitsToTreeNodes(commits)
		stats := loadStatsForNodes(bus, nodes)
		return gitMoreCommitsLoadedMsg{
			branch:  branch,
			nodes:   nodes,
			stats:   stats,
			hasMore: hasMore,
		}
	}
}

// commitsToTreeNodes converts git TreeCommits to UI TreeNodes.
func commitsToTreeNodes(commits []git.TreeCommit) []committree.TreeNode {
	nodes := make([]committree.TreeNode, len(commits))
	for i, c := range commits {
		nodes[i] = committree.TreeNode{
			Hash:       c.Hash,
			ShortHash:  c.ShortHash,
			Subject:    c.Subject,
			Branch:     c.Branch,
			Author:     c.Author,
			AuthorTime: c.AuthorTime,
			Parents:    c.ParentHashes,
			IsMerge:    c.IsMerge,
		}
	}
	return nodes
}

// loadStatsForNodes fetches diff stats for all node hashes in a single batch
// under one read lock, leveraging the ristretto cache for repeated lookups.
func loadStatsForNodes(bus *git.GitBus, nodes []committree.TreeNode) map[string][2]int {
	hashes := make([]string, len(nodes))
	for i, n := range nodes {
		hashes[i] = n.Hash
	}
	summaries := bus.GetCommitDiffSummaries(hashes)
	stats := make(map[string][2]int, len(summaries))
	for h, ds := range summaries {
		stats[h] = [2]int{ds.Additions, ds.Deletions}
	}
	return stats
}

// ---------------------------------------------------------------------------
// Diff view helpers
// ---------------------------------------------------------------------------

// Compare mode aliases for use in app.go (avoid repeating package prefix).
const (
	CompareModeChain    = diffview.CompareModeChain
	CompareModeAllFirst = diffview.CompareModeAllFirst
	CompareModePairs    = diffview.CompareModePairs
)

// fetchDiffDataCmd launches an async diff fetch for the given hashes and mode.
// Diff pairs are computed concurrently via a WaitGroup.
func (m *AppModel) fetchDiffDataCmd(hashes []string, mode diffview.CompareMode) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(hashes) < 2 {
		return nil
	}
	// Build hash→label map for branch name overrides (nil when unused).
	var hashToLabel map[string]string
	if len(m.diffLabels) == len(hashes) {
		hashToLabel = make(map[string]string, len(hashes))
		for i, h := range hashes {
			hashToLabel[h] = m.diffLabels[i]
		}
	}
	return func() tea.Msg {
		fromTo := buildDiffPairs(hashes, mode)
		pairs := make([]msg.DiffViewPair, len(fromTo))
		var wg sync.WaitGroup
		wg.Add(len(fromTo))
		for i, ft := range fromTo {
			go func(idx int, pair [2]string) {
				defer wg.Done()
				pairs[idx] = fetchOneDiffPair(bus, pair)
			}(i, ft)
		}
		wg.Wait()
		// Override short labels with branch names when available.
		if hashToLabel != nil {
			for i := range pairs {
				if lbl, ok := hashToLabel[pairs[i].FromHash]; ok {
					pairs[i].FromShort = lbl
				}
				if lbl, ok := hashToLabel[pairs[i].ToHash]; ok {
					pairs[i].ToShort = lbl
				}
			}
		}
		return msg.DiffViewDataMsg{Pairs: pairs, Mode: int(mode)}
	}
}

// fetchOneDiffPair fetches diff data for a single (from, to) hash pair.
func fetchOneDiffPair(bus *git.GitBus, ft [2]string) msg.DiffViewPair {
	files, _ := bus.GetDiff(ft[0], ft[1])
	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	fromInfo, _ := bus.GetCommit(ft[0])
	toInfo, _ := bus.GetCommit(ft[1])
	fromShort, toShort := shortHash(ft[0]), shortHash(ft[1])
	if fromInfo != nil {
		fromShort = fromInfo.ShortHash
	}
	if toInfo != nil {
		toShort = toInfo.ShortHash
	}
	return msg.DiffViewPair{
		FromHash:  ft[0],
		ToHash:    ft[1],
		FromShort: fromShort,
		ToShort:   toShort,
		Files:     files,
		TotalAdd:  totalAdd,
		TotalDel:  totalDel,
	}
}

// buildDiffPairs produces (from, to) hash pairs based on compare mode.
func buildDiffPairs(hashes []string, mode diffview.CompareMode) [][2]string {
	if len(hashes) < 2 {
		return nil
	}
	switch mode {
	case CompareModeAllFirst:
		pairs := make([][2]string, len(hashes)-1)
		for i := 1; i < len(hashes); i++ {
			pairs[i-1] = [2]string{hashes[0], hashes[i]}
		}
		return pairs
	case CompareModePairs:
		pairs := make([][2]string, 0, len(hashes)/2)
		for i := 0; i+1 < len(hashes); i += 2 {
			pairs = append(pairs, [2]string{hashes[i], hashes[i+1]})
		}
		return pairs
	default: // Chain
		pairs := make([][2]string, len(hashes)-1)
		for i := 0; i+1 < len(hashes); i++ {
			pairs[i] = [2]string{hashes[i], hashes[i+1]}
		}
		return pairs
	}
}

// shortHash returns the first 7 characters of a hash.
func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// enterDiffView creates or updates the diff view model.
// When an established (non-loading) diff view already exists, pairs and
// mode are reloaded in place to preserve open tabs and the active tab.
func (m *AppModel) enterDiffView(pairs []diffview.DiffPair, mode diffview.CompareMode) {
	// Exit any active overlay first.
	if m.conflictViewActive {
		m.exitConflictView()
	}
	if m.mergeDiffViewActive {
		m.exitMergeDiffView()
	}
	if m.diffViewActive && m.diffView != nil && len(m.diffView.OpenTabs()) > 0 {
		m.diffView.ReloadPairs(pairs, mode)
		m.syncViewState()
		m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
		m.syncFocusState()
		m.viewDirty = true
		return
	}
	if m.diffView != nil {
		m.diffView.Close()
	}
	m.diffView = diffview.New(pairs, mode, m.config.Theme(), m.nerdFontsDetected)
	m.diffViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.diffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.diffView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.diffView.SetFocused(true)
	// syncViewState rebuilds rings, tab order, and calls syncFocusState.
	// Focus must be set AFTER so that SetTabOrder (which includes the diff
	// pane ID via appendCodePanelFocus) preserves the diff pane focus.
	m.syncViewState()
	m.focus.SetFocus(pane.PaneFocusID(m.diffView.FocusedPane()))
	m.syncFocusState()
	m.viewDirty = true
}

// exitDiffView clears the diff view and restores commit tree focus.
func (m *AppModel) exitDiffView() {
	if m.diffView != nil {
		m.diffView.Close()
	}
	m.diffViewActive = false
	m.diffView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// setDiffLoading sets or clears the loading state on the active diff view.
// When no diff view exists yet, activates the diff view in loading mode.
func (m *AppModel) setDiffLoading(loading bool) {
	if loading && m.diffView == nil {
		// Create a placeholder diff view in loading state so the spinner
		// is visible while the first fetch runs.
		m.diffView = diffview.New(nil, diffview.CompareModeChain,
			m.config.Theme(), m.nerdFontsDetected)
		m.diffViewActive = true
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		m.diffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
		m.syncViewState()
	}
	if m.diffView != nil {
		m.diffView.SetLoading(loading)
		m.viewDirty = true
	}
}

// ---------------------------------------------------------------------------
// Merge diff view
// ---------------------------------------------------------------------------

// fetchMergeDiffDataCmd fetches diff data for a merge selection.
func (m *AppModel) fetchMergeDiffDataCmd(hashes, labels []string) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(hashes) < 2 {
		return nil
	}
	hashToLabel := make(map[string]string, len(hashes))
	for i, h := range hashes {
		if i < len(labels) {
			hashToLabel[h] = labels[i]
		}
	}
	return func() tea.Msg {
		fromTo := buildDiffPairs(hashes, CompareModeChain)
		pairs := make([]msg.DiffViewPair, len(fromTo))
		var wg sync.WaitGroup
		wg.Add(len(fromTo))
		for i, ft := range fromTo {
			go func(idx int, pair [2]string) {
				defer wg.Done()
				pairs[idx] = fetchOneDiffPair(bus, pair)
			}(i, ft)
		}
		wg.Wait()
		for i := range pairs {
			if lbl, ok := hashToLabel[pairs[i].FromHash]; ok {
				pairs[i].FromShort = lbl
			}
			if lbl, ok := hashToLabel[pairs[i].ToHash]; ok {
				pairs[i].ToShort = lbl
			}
		}
		return msg.MergeDiffViewDataMsg{Pairs: pairs}
	}
}

// enterMergeDiffView creates the merge diff view model.
func (m *AppModel) enterMergeDiffView(pairs []diffview.DiffPair) {
	// Exit any active diff view first.
	if m.diffViewActive {
		m.exitDiffView()
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.Close()
	}
	m.mergeDiffView = mergediff.New(pairs, m.config.Theme(), m.nerdFontsDetected)
	m.mergeDiffViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.mergeDiffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.mergeDiffView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.mergeDiffView.SetFocused(true)
	m.syncViewState()
	m.focus.SetFocus(pane.PaneFocusID(m.mergeDiffView.FocusedPane()))
	m.syncFocusState()
	m.viewDirty = true
}

// exitMergeDiffView clears the merge diff view and restores commit tree focus.
func (m *AppModel) exitMergeDiffView() {
	if m.mergeDiffView != nil {
		m.mergeDiffView.Close()
	}
	m.mergeDiffViewActive = false
	m.mergeDiffView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// enterConflictView creates and activates the conflict resolution view.
func (m *AppModel) enterConflictView(data conflictview.ConflictData) {
	// Exit any active overlays first.
	if m.diffViewActive {
		m.exitDiffView()
	}
	if m.mergeDiffViewActive {
		m.exitMergeDiffView()
	}
	if m.conflictView != nil {
		m.conflictView.Close()
	}
	m.conflictView = conflictview.New(data, m.config.Theme(), m.nerdFontsDetected)
	m.conflictViewActive = true
	w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.conflictView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
	lw, lh := m.layout.GetPanelSize(component.FocusFileTree)
	m.conflictView.SetFileListSize(max(lw-panelBorderSize, 1), max(lh-panelBorderSize, 1))
	m.conflictView.SetFocused(true)
	m.focus.SetFocus(component.FocusConflictView)
	m.syncViewState()
	m.syncFocusState()
	m.viewDirty = true
}

// exitConflictView clears the conflict view and restores commit tree focus.
func (m *AppModel) exitConflictView() {
	if m.conflictView != nil {
		m.conflictView.Close()
	}
	m.conflictViewActive = false
	m.conflictView = nil
	m.focus.SetFocus(component.FocusCommitTree)
	m.syncViewState()
	m.viewDirty = true
}

// executeMergeBranch performs a git merge using the sequencer so that
// conflicts can be resolved interactively in the conflict view.
// mergeLabels[0] is the source branch, mergeLabels[1] is the target branch.
func (m *AppModel) executeMergeBranch(deleteSource bool) tea.Cmd {
	bus := m.gitBus
	if bus == nil || len(m.mergeLabels) < 2 {
		m.statusBar.SetFlash("Merge requires two branches")
		return nil
	}
	source := m.mergeLabels[0]
	target := m.mergeLabels[1]
	m.mergeDeleteSource = deleteSource
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetMerging("Merging — " + source + " to " + target)
	}

	return func() tea.Msg {
		status, err := bus.MergeSequence(source, target)
		if err != nil {
			return mergeBranchFailedMsg{reason: err.Error()}
		}
		// Conflicts — route through sequencer pipeline for conflict view.
		if status != nil && status.State == git.SeqConflict {
			return sequencerResultMsg{status: status}
		}
		// Clean merge — optionally delete source branch.
		if deleteSource {
			if err := bus.DeleteBranch(source); err != nil {
				return mergeBranchDoneMsg{source: source, target: target, deleteErr: err.Error()}
			}
		}
		return mergeBranchDoneMsg{source: source, target: target, deleted: deleteSource}
	}
}

// finishMergeIfPending handles deferred merge completion after sequencer
// finishes (e.g. after conflict resolution). Deletes source branch if
// requested and shows the appropriate flash message.
func (m *AppModel) finishMergeIfPending() {
	if len(m.mergeLabels) < 2 {
		m.statusBar.SetFlash("Sequencer completed")
		return
	}
	source, target := m.mergeLabels[0], m.mergeLabels[1]
	if m.mergeDeleteSource {
		if err := m.gitBus.DeleteBranch(source); err != nil {
			m.statusBar.SetFlash("Merged " + source + " → " + target + " (delete failed: " + err.Error() + ")")
		} else {
			m.statusBar.SetFlash("Merged " + source + " → " + target + " (deleted " + source + ")")
		}
	} else {
		m.statusBar.SetFlash("Merged " + source + " → " + target)
	}
	m.mergeDeleteSource = false
}

// mergeBranchDoneMsg signals a successful merge.
type mergeBranchDoneMsg struct {
	source    string
	target    string
	deleted   bool
	deleteErr string // Non-empty if merge succeeded but delete failed.
}

// mergeBranchFailedMsg signals a failed merge.
type mergeBranchFailedMsg struct {
	reason string
}

// setMergeDiffLoading sets or clears loading on the merge diff view.
func (m *AppModel) setMergeDiffLoading(loading bool) {
	if loading && m.mergeDiffView == nil {
		if m.diffViewActive {
			m.exitDiffView()
		}
		m.mergeDiffView = mergediff.New(nil, m.config.Theme(), m.nerdFontsDetected)
		m.mergeDiffViewActive = true
		w, h := m.layout.GetPanelSize(component.FocusCodeViewer)
		m.mergeDiffView.SetSize(max(w-panelBorderSize, 1), max(h-panelBorderSize, 1))
		m.syncViewState()
	}
	if m.mergeDiffView != nil {
		m.mergeDiffView.SetLoading(loading)
		m.viewDirty = true
	}
}

// commitPageSize is the number of commits per page.
// Derived from: typical viewport shows ~8 nodes; 3× provides comfortable
// scroll depth per page while keeping load times snappy.
const commitPageSize = 25

// handleGitTabShortcut processes git-panel tab jump shortcuts.
// Returns (cmd, true) if the key was handled.
func (m *AppModel) handleGitTabShortcut(ks string) (tea.Cmd, bool) {
	switch ks {
	case "alt+C":
		m.gitPanel.SetActiveTab(gitpanel.TabCommits)
		m.focusGitPanel()
		return nil, true
	case "alt+B":
		m.gitPanel.SetActiveTab(gitpanel.TabBranches)
		m.focusGitPanel()
		return nil, true
	case "alt+T":
		m.gitPanel.SetActiveTab(gitpanel.TabTags)
		m.focusGitPanel()
		return nil, true
	case "alt+U":
		m.gitPanel.SetActiveTab(gitpanel.TabUncommitted)
		m.focusGitPanel()
		m.pendingUncommittedAll = true
		return nil, true
	}
	return nil, false
}

// focusGitPanel sets focus to the git panel and syncs focus state.
func (m *AppModel) focusGitPanel() {
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
}

// toggleGitMode switches between git mode and the previous mode.
func (m *AppModel) toggleGitMode() tea.Cmd {
	if m.gitPanel == nil {
		m.statusBar.SetFlash("Not a git repository")
		return nil
	}
	if m.viewMode == ViewGit {
		return m.exitGitMode()
	}
	return m.enterGitMode()
}

// enterGitMode activates git mode, displaying the git explorer and commit tree.
func (m *AppModel) enterGitMode() tea.Cmd {
	// If edit mode is active, exit it first and remember for restore.
	m.preGitEditMode = m.viewMode == ViewEdit
	if m.viewMode == ViewEdit {
		m.exitEditMode()
	}

	// Save ring state and current focus.
	m.savedGitLeftIdx = m.leftRing.index
	m.savedGitRightIdx = m.rightRing.index
	m.savedChatFocus = m.focus.Current()

	m.viewMode = ViewGit

	// Size git components to their panel slots.
	m.resizeGitPanels()

	m.statusBar.SetMode("GIT")
	m.input.SetPlaceholder("git>")

	// Rebuild rings — syncViewState positions git panels in the rings
	// and builds the ring hint with correct panel labels.
	m.syncViewState()
	m.focus.SetFocus(component.FocusGitPanel)
	m.syncFocusState()
	m.statusBar.SetFlash("Git mode")

	// Alt+G always force-reloads; mark loaded so cycling skips reloads.
	m.gitDataLoaded = true
	return tea.Batch(m.gitPanel.LoadData(), m.loadGitBranchesCmd())
}

// exitGitMode deactivates git mode and returns to the previous mode.
// Returns a Cmd when edit mode is restored (LSP reopen).
func (m *AppModel) exitGitMode() tea.Cmd {
	m.savedGitFocus = m.focus.Current()

	// Restore ring state.
	m.leftRing.index = m.savedGitLeftIdx
	m.rightRing.index = m.savedGitRightIdx

	m.viewMode = ViewChat
	m.statusBar.SetMode("CHAT")
	m.input.SetPlaceholder("Type a message...")

	m.syncViewState()

	if m.preGitEditMode {
		m.preGitEditMode = false
		return m.enterEditMode()
	}

	m.focus.SetFocus(m.savedChatFocus)
	m.syncFocusState()
	m.statusBar.SetFlash("View mode")
	return nil
}

// resizeGitPanels sizes the git panel and commit tree to their layout slots.
func (m *AppModel) resizeGitPanels() {
	if m.gitPanel == nil {
		return
	}
	treeW, treeH := m.layout.GetPanelSize(component.FocusFileTree)
	m.gitPanel.SetSize(max(treeW-panelBorderSize, 1), max(treeH-panelBorderSize, 1))

	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	m.commitTree.SetSize(max(rightW-panelBorderSize, 1), max(rightH-panelBorderSize, 1))
}

// enterCmdInput activates the input panel for ex command entry,
// pre-filling it with ":" and switching focus.
func (m *AppModel) enterCmdInput() {
	m.editCmdInput = true
	m.input.SetText(":")
	m.input.SetLineStyler(m.cmdLineStyler())
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
}

// exitCmdInput aborts command input and returns focus to the code panel.
func (m *AppModel) exitCmdInput() {
	m.editCmdInput = false
	m.input.SetLineStyler(nil)
	m.input.Clear()
	m.focusCodePanel()
	m.syncFocusState()
}

// detachCurrentEditor extracts the current editor's per-file state and
// stores it in the tiered cache. No-op if no file is loaded.
func (m *AppModel) detachCurrentEditor() {
	ws := m.focusedEditor().Detach()
	if ws.FilePath == "" {
		return
	}
	m.editorCache.Put(ws)
}

// restoreFromCache attempts to restore the editor from the tiered cache.
// Returns true if the entry was found and restored, false otherwise.
func (m *AppModel) restoreFromCache(path string) bool {
	entry, ok := m.editorCache.Take(path)
	if !ok {
		return false
	}
	if entry.IsWarm() {
		m.focusedEditor().AttachWarm(entry.Warm())
		return true
	}
	m.focusedEditor().AttachCold(entry.Cold())
	return true
}

// exCmdInfo describes a known ex command and its argument requirement.
type exCmdInfo struct {
	argHint     string // Non-empty means the command accepts an argument.
	optionalArg bool   // When true, command is valid with or without the arg.
}

// knownExCommands maps command words to their validation info.
var knownExCommands = map[string]exCmdInfo{
	"w": {}, "q": {}, "wq": {}, "x": {}, "q!": {},
	"symbols": {}, "format": {}, "fmt": {},
	"rename":   {argHint: "<newname>"},
	"tabn":     {argHint: "[N]", optionalArg: true},
	"tabp":     {},
	"tabclose": {},
	"tab":      {argHint: "<filename>"},
}

// validateExCommand checks the typed text against known commands.
// Returns: known (command word recognized), valid (ready to execute), hint.
func validateExCommand(text string) (known, valid bool, hint string) {
	cmd := strings.TrimPrefix(strings.TrimSpace(text), ":")
	cmd = strings.TrimSpace(cmd)

	word, args, hasArgs := strings.Cut(cmd, " ")
	info, ok := knownExCommands[word]
	if !ok {
		return false, false, ""
	}
	if info.argHint == "" {
		return true, true, ""
	}
	if info.optionalArg {
		return true, true, info.argHint
	}
	if hasArgs && strings.TrimSpace(args) != "" {
		return true, true, ""
	}
	return true, false, info.argHint
}

// cmdLineStyler returns a line styler that colors command text green when
// valid, red when recognized but incomplete, and returns a hint for
// incomplete commands.
func (m *AppModel) cmdLineStyler() func(string) (string, string) {
	th := m.config.Theme()
	validStyle := lipgloss.NewStyle().Foreground(th.Palette.Success)
	invalidStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
	return func(text string) (string, string) {
		known, valid, hint := validateExCommand(text)
		if !known {
			return text, ""
		}
		if valid {
			return validStyle.Render(text), ""
		}
		return invalidStyle.Render(text), hint
	}
}

// handleExCommand processes a vim ex command entered in the input panel
// during edit mode. Supported: :w (save), :q (quit), :wq (save+quit),
// :q! (force quit).
func (m *AppModel) handleExCommand(text string) tea.Cmd {
	cmd := normalizeExCommand(text)
	if handler, ok := exCommandHandlers[cmd]; ok {
		return handler(m)
	}
	if nextCmd, handled := m.handleTabNextCommand(cmd); handled {
		return nextCmd
	}
	if prefixedCmd, handled := m.handlePrefixedExCommand(cmd); handled {
		return prefixedCmd
	}
	m.statusBar.SetFlash("Unknown command: :" + cmd)
	return nil
}

var exCommandHandlers = map[string]func(*AppModel) tea.Cmd{
	"w":        (*AppModel).executeExWrite,
	"q":        func(m *AppModel) tea.Cmd { return m.closeCurrentTab(false) },
	"wq":       (*AppModel).executeExWriteQuit,
	"x":        (*AppModel).executeExWriteQuit,
	"q!":       func(m *AppModel) tea.Cmd { return m.closeCurrentTab(true) },
	"tabp":     func(m *AppModel) tea.Cmd { return m.prevTab() },
	"tabclose": func(m *AppModel) tea.Cmd { return m.closeCurrentTab(false) },
	"symbols":  (*AppModel).executeExSymbols,
	"format":   (*AppModel).executeExFormat,
	"fmt":      (*AppModel).executeExFormat,
}

func normalizeExCommand(text string) string {
	cmd := strings.TrimSpace(text)
	cmd = strings.TrimPrefix(cmd, ":")
	return strings.TrimSpace(cmd)
}

func (m *AppModel) executeExWrite() tea.Cmd {
	m.saveEditorBuffer()
	return nil
}

func (m *AppModel) executeExWriteQuit() tea.Cmd {
	m.saveEditorBuffer()
	return m.closeCurrentTab(false)
}

func (m *AppModel) executeExSymbols() tea.Cmd {
	if fp := m.focusedEditor().FilePath(); fp != "" {
		return m.lspDocumentSymbolCmd(fp)
	}
	return nil
}

func (m *AppModel) executeExFormat() tea.Cmd {
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		return nil
	}
	var flushContent string
	if m.focusedEditor().LSPDirty() {
		m.focusedEditor().ClearLSPDirty()
		flushContent = m.focusedEditor().Content()
	}
	return m.lspFormatCmd(fp, flushContent, m.focusedEditor().EditGeneration())
}

func (m *AppModel) handleTabNextCommand(cmd string) (tea.Cmd, bool) {
	if cmd != "tabn" && !strings.HasPrefix(cmd, "tabn ") {
		return nil, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(cmd, "tabn"))
	if rest == "" {
		return m.nextTab(), true
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		m.statusBar.SetFlash("Usage: :tabn [N]")
		return nil, true
	}
	return m.switchToTab(n - 1), true
}

func (m *AppModel) handlePrefixedExCommand(cmd string) (tea.Cmd, bool) {
	if name, ok := strings.CutPrefix(cmd, "tab "); ok {
		return m.handleTabCommand(strings.TrimSpace(name)), true
	}
	if newName, ok := strings.CutPrefix(cmd, "rename "); ok {
		return m.handleRenameCommand(strings.TrimSpace(newName)), true
	}
	return nil, false
}

// handleRenameCommand initiates an LSP rename for the symbol under the cursor.
func (m *AppModel) handleRenameCommand(newName string) tea.Cmd {
	if newName == "" {
		m.statusBar.SetFlash("Usage: :rename <newname>")
		return nil
	}
	fp := m.focusedEditor().FilePath()
	if fp == "" {
		return nil
	}
	line := m.focusedEditor().CursorLine()
	col := m.focusedEditor().CursorCol()

	// Flush pending edits so the server sees the latest buffer.
	if m.focusedEditor().LSPDirty() {
		m.focusedEditor().ClearLSPDirty()
		_ = m.lspManager.NotifyDidChange(m.ctx, m.config.ProjectRoot, fp, m.focusedEditor().Content())
	}

	if m.lspManager.PrepareRenameSupported(m.config.ProjectRoot, fp) {
		return m.lspPrepareRenameCmd(fp, line, col, newName)
	}
	return m.lspRenameCmd(fp, line, col, newName)
}

// tabCompleter provides tab-completion candidates from open tabs for the
// :tab ex command. Implements input.CompletionProvider.
type tabCompleter struct {
	tabOrderFn func() []string
}

func (tc *tabCompleter) Name() string { return "tabs" }

func (tc *tabCompleter) Complete(prefix string, limit int) []Candidate {
	tabs := tc.tabOrderFn()
	if len(tabs) == 0 {
		return nil
	}
	lower := strings.ToLower(prefix)
	var out []Candidate
	for _, p := range tabs {
		base := filepath.Base(p)
		if prefix == "" || strings.Contains(strings.ToLower(base), lower) {
			out = append(out, Candidate{
				Text:    base,
				Display: base,
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// slashCommand describes a known chat slash command.
type slashCommand struct {
	name string // Without leading "/".
	desc string
}

// chatSlashCommands is the single source of truth for all chat slash commands.
// Both the validator and the completer derive from this slice.
var chatSlashCommands = []slashCommand{
	{name: "clear", desc: "Clear the chat history"},
	{name: "debug-ui", desc: "Toggle or set plain-text chat capture in ./chat.log"},
	{name: "login", desc: "Open the provider login panel"},
}

// isKnownSlashCommand reports whether cmd (without "/") is a registered command.
func isKnownSlashCommand(cmd string) bool {
	for _, sc := range chatSlashCommands {
		if sc.name == cmd {
			return true
		}
	}
	return false
}

// slashCommandCompleter provides autocomplete candidates for slash commands
// typed in the chat input. Implements input.CompletionProvider.
type slashCommandCompleter struct{}

func (sc *slashCommandCompleter) Name() string { return "commands" }

func (sc *slashCommandCompleter) Complete(prefix string, limit int) []Candidate {
	if !strings.HasPrefix(prefix, "/") {
		return nil
	}
	typed := strings.ToLower(prefix[1:])
	var out []Candidate
	for _, cmd := range chatSlashCommands {
		if strings.HasPrefix(cmd.name, typed) {
			out = append(out, Candidate{
				Text:        "/" + cmd.name,
				Display:     "/" + cmd.name,
				Description: cmd.desc,
				Category:    "command",
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// Candidate is an alias for the input completion candidate type.
type Candidate = inputpkg.Candidate

// handleTabCommand switches to an open tab matching the given filename.
func (m *AppModel) handleTabCommand(name string) tea.Cmd {
	if name == "" {
		m.statusBar.SetFlash("Usage: :tab <filename>")
		return nil
	}
	for i, p := range m.focusedTabOrder() {
		if filepath.Base(p) == name {
			return m.switchToTab(i)
		}
	}
	m.statusBar.SetFlash("No open tab: " + name)
	return nil
}

// saveEditorBuffer writes the inline editor buffer to disk.
func (m *AppModel) saveEditorBuffer() {
	path := m.focusedEditor().FilePath()
	if path == "" {
		m.statusBar.SetFlash("No file path")
		return
	}
	content := m.focusedEditor().Content()
	if err := atomicWriteFile(path, []byte(content), 0o644); err != nil {
		m.statusBar.SetFlash("Write failed: " + err.Error())
		return
	}
	m.focusedEditor().MarkSaved()
	m.lspDidSaveAsync(path, content)
	m.codePanel.SetContent(content, path, m.focusedEditor().Language())
	m.nudgeGitWatcher()
	m.refreshTabsModified()
	m.statusBar.SetFlash("Saved " + path)
}

// atomicWriteFile writes data to a temporary file in the same directory as
// path, then renames it into place. This prevents partial writes from
// corrupting the target file on crash or power loss.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sylk-save-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Ensure cleanup on any error path.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	tmpName = "" // prevent deferred cleanup after successful rename
	return nil
}

// codePanelView returns the rendered view for the code panel slot.
// Handles read-only viewer, single-pane editor, full preview, and
// multi-pane compositing (iterative, not recursive).
func (m *AppModel) codePanelView() string {
	if m.viewMode != ViewEdit {
		return m.codePanel.View()
	}

	// Full preview mode: preview active, single editor pane with no tabs.
	if m.hasPreview() && len(m.paneEditors) == 1 && len(m.focusedTabOrder()) == 0 {
		bar := m.previewTabBarView()
		content := m.previewPanel.View(m.cursorVisible)
		if bar != "" {
			return lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		return content
	}

	// Full markdown preview mode: mdPreview active, sole editor has no tabs.
	if m.isFullMdPreview() {
		rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
		bar := m.mdPreviewTabBar(max(rightW-panelBorderSize, 1))
		content := m.mdPreviewPanel.View()
		if bar != "" {
			return lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		return content
	}

	// Single editor pane, no splits: direct render (no compositing overhead).
	if m.paneTree.IsLeaf() {
		content := m.focusedEditor().View(m.cursorVisible)
		bar := m.tabBarView()
		if bar != "" {
			content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
		}
		content = m.overlayMdTooltipOnContent(content)
		return m.overlayBlockedChord(content)
	}

	// Multi-pane: iterative compositing via pre-computed layout.
	rightW, rightH := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)
	innerH := max(rightH-panelBorderSize, 1)
	contentH := max(innerH-1, 1) // Reserve 1 row for status line.

	area := pane.Rect{X: 0, Y: 0, W: innerW, H: contentH}
	content := m.composePanes(area)

	// Overlay markdown tooltip in multi-pane mode.
	if m.mdTooltipTab >= 0 && m.mdTooltipPane != 0 {
		rects := m.paneTree.ComputeLayout(area)
		if pr, ok := rects[m.mdTooltipPane]; ok {
			rows := m.mdTooltipRows()
			lines := strings.Split(content, "\n")
			overlayMdTooltipRows(lines, rows, pr.X+m.mdTooltipX, pr.Y+tabbar.Height-1)
			content = strings.Join(lines, "\n")
		}
	}

	statusLine := m.focusedEditor().StatusLineView(innerW)
	return m.overlayBlockedChord(lipgloss.JoinVertical(lipgloss.Left, content, statusLine))
}

// composePanes renders all panes in the tree and composites them into a
// single string using iterative row-by-row assembly. Divider characters
// are placed at positions computed from the tree structure.
func (m *AppModel) composePanes(area pane.Rect) string {
	rects := m.paneTree.ComputeLayout(area)
	dividers := m.paneTree.Dividers(area)
	leaves := m.paneTree.Leaves()
	dropRect, hasDropTarget := m.composePaneDropTarget(rects)
	leafLines := m.composePaneLeafLines(leaves, rects)
	rows := m.composePaneRows(area.H, leaves, rects, dividers, leafLines, dropRect, hasDropTarget)
	return strings.Join(rows, "\n")
}

type paneSegment struct {
	x int
	s string
}

func (m *AppModel) composePaneDropTarget(rects map[pane.PaneID]pane.Rect) (pane.Rect, bool) {
	if m.tabDropTarget == 0 {
		return pane.Rect{}, false
	}
	return rects[m.tabDropTarget], true
}

func (m *AppModel) composePaneLeafLines(leaves []pane.PaneID, rects map[pane.PaneID]pane.Rect) map[pane.PaneID][]string {
	leafLines := make(map[pane.PaneID][]string, len(leaves))
	for _, id := range leaves {
		leafLines[id] = strings.Split(m.renderLeafPane(id, rects[id]), "\n")
	}
	return leafLines
}

func (m *AppModel) composePaneRows(
	height int,
	leaves []pane.PaneID,
	rects map[pane.PaneID]pane.Rect,
	dividers []pane.Divider,
	leafLines map[pane.PaneID][]string,
	dropRect pane.Rect,
	hasDropTarget bool,
) []string {
	rows := make([]string, height)
	for y := range height {
		rows[y] = m.composePaneRow(y, leaves, rects, dividers, leafLines, dropRect, hasDropTarget)
	}
	return rows
}

func (m *AppModel) composePaneRow(
	y int,
	leaves []pane.PaneID,
	rects map[pane.PaneID]pane.Rect,
	dividers []pane.Divider,
	leafLines map[pane.PaneID][]string,
	dropRect pane.Rect,
	hasDropTarget bool,
) string {
	segs := m.paneContentSegments(y, leaves, rects, leafLines)
	segs = append(segs, m.paneDividerSegments(y, dividers, dropRect, hasDropTarget)...)
	slices.SortFunc(segs, func(a, b paneSegment) int { return a.x - b.x })
	return joinPaneSegments(segs)
}

func (m *AppModel) paneContentSegments(
	y int,
	leaves []pane.PaneID,
	rects map[pane.PaneID]pane.Rect,
	leafLines map[pane.PaneID][]string,
) []paneSegment {
	var segs []paneSegment
	for _, id := range leaves {
		rect := rects[id]
		if y < rect.Y || y >= rect.Y+rect.H {
			continue
		}
		segs = append(segs, paneSegment{
			x: rect.X,
			s: paneLineAt(leafLines[id], y-rect.Y),
		})
	}
	return segs
}

func paneLineAt(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}

func (m *AppModel) paneDividerSegments(y int, dividers []pane.Divider, dropRect pane.Rect, hasDropTarget bool) []paneSegment {
	th := m.config.Theme()
	divStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
	dropDivStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary)
	var segs []paneSegment
	for _, divider := range dividers {
		segment, ok := renderPaneDividerSegment(y, divider, dropRect, hasDropTarget, divStyle, dropDivStyle)
		if ok {
			segs = append(segs, segment)
		}
	}
	return segs
}

func renderPaneDividerSegment(
	y int,
	divider pane.Divider,
	dropRect pane.Rect,
	hasDropTarget bool,
	divStyle lipgloss.Style,
	dropDivStyle lipgloss.Style,
) (paneSegment, bool) {
	style := divStyle
	if hasDropTarget && isDividerAdjacentToRect(divider, dropRect) {
		style = dropDivStyle
	}
	switch {
	case divider.Dir == pane.SplitVertical && y >= divider.Y && y < divider.Y+divider.Len:
		return paneSegment{x: divider.X, s: style.Render("│")}, true
	case divider.Dir == pane.SplitHorizontal && y == divider.Y:
		return paneSegment{x: divider.X, s: style.Render(strings.Repeat("─", divider.Len))}, true
	default:
		return paneSegment{}, false
	}
}

func joinPaneSegments(segs []paneSegment) string {
	var b strings.Builder
	for _, seg := range segs {
		b.WriteString(seg.s)
	}
	return b.String()
}

// isDividerAdjacentToRect reports whether a divider segment borders the given rect.
func isDividerAdjacentToRect(d pane.Divider, r pane.Rect) bool {
	if d.Dir == pane.SplitVertical {
		adjacent := d.X == r.X-1 || d.X == r.X+r.W
		overlaps := d.Y < r.Y+r.H && d.Y+d.Len > r.Y
		return adjacent && overlaps
	}
	adjacent := d.Y == r.Y-1 || d.Y == r.Y+r.H
	overlaps := d.X < r.X+r.W && d.X+d.Len > r.X
	return adjacent && overlaps
}

// renderLeafPane renders a single pane (editor or preview) as a sized block.
// The returned string has exactly r.H lines, each r.W visual columns wide.
func (m *AppModel) renderLeafPane(id pane.PaneID, r pane.Rect) string {
	sizer := lipgloss.NewStyle().
		Width(r.W).MaxWidth(r.W).
		Height(r.H).MaxHeight(r.H)

	if id == m.tabDropTarget {
		return m.renderDropTargetPane(id, r, sizer)
	}

	if id == m.previewPane {
		bar := m.previewTabBarViewSized(r.W)
		content := m.previewPanel.View(m.cursorVisible)
		return sizer.Render(lipgloss.JoinVertical(lipgloss.Left, bar, content))
	}

	if id == m.mdPreviewPane {
		bar := m.mdPreviewTabBar(r.W)
		content := m.mdPreviewPanel.View()
		return sizer.Render(lipgloss.JoinVertical(lipgloss.Left, bar, content))
	}

	ps := m.paneEditors[id]
	bar := m.paneTabBarView(id, r.W)
	content := ps.editor.ViewContent(m.cursorVisible)
	if bar != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
	}
	return sizer.Render(content)
}

// dropOverlayAlpha is the blend factor for the drop-target background tint.
// Derived from: 15% matches VS Code's drag-and-drop overlay opacity.
const dropOverlayAlpha = 0.15

// renderDropTargetPane renders an editor pane as a drop target: the tab bar
// stays normal, content backgrounds are tinted with a blended overlay color,
// and a centered "Drop tab here" label is composited on top.
func (m *AppModel) renderDropTargetPane(id pane.PaneID, r pane.Rect, sizer lipgloss.Style) string {
	ps := m.paneEditors[id]
	if ps == nil {
		return sizer.Render("")
	}
	bar := m.paneTabBarView(id, r.W)
	content := ps.editor.ViewContent(m.cursorVisible)
	if bar != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, bar, content)
	}
	sized := sizer.Render(content)
	lines := strings.Split(sized, "\n")

	// Compute overlay background by blending base bg with Primary.
	th := m.config.Theme()
	tr, tg, tb := blendColors(th.Palette.Background, th.Palette.Primary, dropOverlayAlpha)
	bgCode := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", tr, tg, tb)

	// Tint content lines below the tab bar.
	barH := tabbar.Height
	if bar == "" {
		barH = 0
	}
	for i := barH; i < len(lines); i++ {
		lines[i] = tintLineBg(lines[i], bgCode)
	}

	// Overlay centered "Drop tab here" indicator.
	label := lipgloss.NewStyle().
		Foreground(th.Palette.Background).
		Background(th.Palette.Primary).
		Bold(true).
		Padding(0, 1).
		Render("Drop tab here")

	centerY := barH + (r.H-barH)/2
	labelW := lipgloss.Width(label)
	centerX := max((r.W-labelW)/2, 0)

	if centerY >= 0 && centerY < len(lines) {
		orig := lines[centerY]
		left := truncateStyledN(orig, centerX)
		right := skipStyledN(orig, centerX+labelW)
		// Re-insert tint background after the reset so the overlay
		// continues past the label to the end of the line.
		const resetSeq = "\x1b[0m"
		if strings.HasPrefix(right, resetSeq) {
			right = resetSeq + bgCode + right[len(resetSeq):]
		}
		lines[centerY] = left + label + right
	}

	return strings.Join(lines, "\n")
}

// tintLineBg replaces all ANSI background colors in a line with bgCode,
// producing a semi-transparent overlay effect where foreground content
// remains visible but backgrounds are uniformly tinted.
func tintLineBg(s string, bgCode string) string {
	var out strings.Builder
	out.Grow(len(s) + len(s)/4)
	out.WriteString(bgCode)

	i := 0
	for i < len(s) {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			out.WriteByte(s[i])
			i++
			continue
		}
		// Find end of CSI sequence.
		j := i + 2
		for j < len(s) && !isCSITerminator(s[j]) {
			j++
		}
		if j >= len(s) {
			out.WriteString(s[i:])
			break
		}
		j++ // include terminator
		out.WriteString(s[i:j])
		// After any SGR sequence (m-terminated), re-insert our bg override.
		if s[j-1] == 'm' {
			out.WriteString(bgCode)
		}
		i = j
	}
	out.WriteString("\x1b[0m")
	return out.String()
}

// blendColors blends two hex colors at the given alpha (0.0–1.0).
func blendColors(base, overlay lipgloss.Color, alpha float64) (uint8, uint8, uint8) {
	br, bg, bb := parseHexColor(string(base))
	or, og, ob := parseHexColor(string(overlay))
	r := uint8(float64(br) + alpha*float64(int(or)-int(br)))
	g := uint8(float64(bg) + alpha*float64(int(og)-int(bg)))
	b := uint8(float64(bb) + alpha*float64(int(ob)-int(bb)))
	return r, g, b
}

// parseHexColor parses a "#RRGGBB" string into RGB components.
func parseHexColor(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return uint8(r), uint8(g), uint8(b)
}

// truncateStyledN truncates a potentially ANSI-styled string to maxW visible
// columns, preserving escape sequences and appending a reset.
func truncateStyledN(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if col >= maxW {
			break
		}
		b.WriteRune(r)
		col++
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// skipStyledN drops the first skip visible columns from a styled string
// and returns the remainder with a reset prefix.
func skipStyledN(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	vis := 0
	i := 0
	for i < len(s) && vis < skip {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		i++
		vis++
	}
	if i >= len(s) {
		return ""
	}
	return "\x1b[0m" + s[i:]
}

// isCSITerminator reports whether b is the final byte of a CSI sequence.
func isCSITerminator(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// paneTabBarConfig builds the tabbar.Config for a specific editor pane.
func (m *AppModel) paneTabBarConfig(id pane.PaneID, width int) tabbar.Config {
	ps := m.paneEditors[id]
	tabs := make([]tabbar.Tab, len(ps.tabOrder))
	currentPath := ps.editor.FilePath()
	activeIdx := 0
	for i, path := range ps.tabOrder {
		modified := false
		if path == currentPath {
			activeIdx = i
			modified = ps.editor.Modified()
		} else {
			modified = m.editorCache.IsModified(path)
		}
		tabs[i] = tabbar.Tab{Path: path, Modified: modified}
	}

	hasSplits := m.paneTree.LeafCount() > 1
	isFocused := id == m.focusedPane && m.isEditorFocused()
	hoverClose := -1
	if id == m.tabHoverPane {
		hoverClose = m.tabHoverClose
	}

	return tabbar.Config{
		Tabs:       tabs,
		Active:     activeIdx,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		FlashLeft:  isFocused && time.Now().Before(m.tabArrowFlashLeftUntil),
		FlashRight: isFocused && time.Now().Before(m.tabArrowFlashRightUntil),
		HoverClose: hoverClose,
		Focused:    hasSplits && isFocused,
		DimActive:  hasSplits && !isFocused,
	}
}

// paneTabBarView renders the tab bar for a specific editor pane.
func (m *AppModel) paneTabBarView(id pane.PaneID, width int) string {
	ps := m.paneEditors[id]
	if len(ps.tabOrder) == 0 {
		return ""
	}
	return tabbar.View(m.paneTabBarConfig(id, width))
}

// previewTabBarViewSized renders the preview tab bar at the given width.
func (m *AppModel) previewTabBarViewSized(width int) string {
	if m.previewPanel.FilePath() == "" {
		return ""
	}
	hasSplits := m.paneTree.LeafCount() > 1
	cfg := tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.previewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Preview: ",
		}},
		Active:     0,
		Width:      width,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      m.config.Theme(),
		HoverClose: m.previewTabHoverClose,
		Focused:    hasSplits && m.isPreviewFocused(),
		DimActive:  hasSplits && !m.isPreviewFocused(),
	}
	return tabbar.View(cfg)
}

// previewTabBarConfig returns the tabbar.Config for the preview tab bar.
func (m *AppModel) previewTabBarConfig() tabbar.Config {
	th := m.config.Theme()
	rightW, _ := m.layout.GetPanelSize(component.FocusCodeViewer)
	innerW := max(rightW-panelBorderSize, 1)

	// In split mode, preview gets half the width.
	barWidth := innerW
	if len(m.focusedTabOrder()) > 0 {
		dividerWidth := 1
		barWidth = (innerW - dividerWidth) / 2
	}

	return tabbar.Config{
		Tabs: []tabbar.Tab{{
			Path:        m.previewPanel.FilePath(),
			Modified:    false,
			LabelPrefix: "Preview: ",
		}},
		Active:     0,
		Width:      barWidth,
		NerdFonts:  m.nerdFontsDetected,
		Theme:      th,
		HoverClose: m.previewTabHoverClose,
		Focused:    len(m.focusedTabOrder()) > 0 && m.isPreviewFocused(),
		DimActive:  len(m.focusedTabOrder()) > 0 && !m.isPreviewFocused(),
	}
}

// previewTabBarView renders the tab bar for the preview panel with a single
// "Preview: filename" tab.
func (m *AppModel) previewTabBarView() string {
	if m.previewPanel.FilePath() == "" {
		return ""
	}
	return tabbar.View(m.previewTabBarConfig())
}
