package planview

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// spinnerFrames are the braille spinner glyphs used during loading.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerFramePeriod is the duration each spinner frame is displayed.
const spinnerFramePeriod = 80 * time.Millisecond

// Status icon glyphs.
const (
	iconPending   = "○"
	iconRunning   = "◉"
	iconCompleted = "✓"
	iconFailed    = "✕"
	iconBlocked   = "◌"
	iconSkipped   = "─"
	iconLayer     = "▸"
	iconLayerOpen = "▾"
)

// Column layout constants.
const (
	colStatusWidth = 2
	colAgentWidth  = 12
	colTokenWidth  = 14
	colTimeWidth   = 8
	colMinName     = 10
)

// entryKind classifies a row in the flat entry list.
type entryKind int

const (
	entryLayer entryKind = iota
	entryTask
)

// entry is a single renderable row in the plan viewer.
type entry struct {
	Kind          entryKind
	Layer         int
	TaskID        string
	Name          string
	Description   string
	AgentType     string
	Status        string
	Dependencies  []string
	TokensIn      int
	TokensOut     int
	Duration      time.Duration
	StatusMessage string
	Expanded      bool
}

// Model is the plan DAG viewer panel.
type Model struct {
	entries    []entry
	cursor     int
	offset     int
	width      int
	height     int
	focused    bool
	visible    bool
	viewDirty  bool
	planID     string
	planStatus string
	startTime  time.Time
	palette    *theme.Palette

	// Expand/collapse state preserved across updates.
	layerExpanded map[int]bool
	taskExpanded  map[string]bool

	// lastUpdate is the most recent PlanUpdateMsg that produced the entry
	// list. Kept so rebuildVisible (invoked on expand/collapse toggles) can
	// re-derive entries that were pruned during a prior collapse — the
	// previous implementation filtered m.entries in place, which meant
	// re-expanding a layer after collapse left its tasks unrecoverable until
	// the next update arrived.
	lastUpdate    msg.PlanUpdateMsg
	hasLastUpdate bool

	// Animation
	animStart time.Time
}

// New creates a new plan viewer model.
func New(p *theme.Palette) *Model {
	return &Model{
		palette:       p,
		layerExpanded: make(map[int]bool),
		taskExpanded:  make(map[string]bool),
		animStart:     time.Now(),
		cursor:        0,
	}
}

// -- component.Focusable --

func (m *Model) ID() component.FocusID  { return component.FocusPlanView }
func (m *Model) Focused() bool          { return m.focused }
func (m *Model) SetFocused(f bool)      { m.focused = f; m.viewDirty = true }
func (m *Model) Init() tea.Cmd          { return nil }
func (m *Model) SetSize(w, h int)       { m.width = w; m.height = h; m.viewDirty = true }
func (m *Model) Visible() bool          { return m.visible }
func (m *Model) SetVisible(v bool)      { m.visible = v; m.viewDirty = true }
func (m *Model) HasPlan() bool          { return m.planID != "" }
func (m *Model) NeedsDecorTick() bool   { return m.visible && m.hasRunningTask() }
func (m *Model) MarkViewDirty()         { m.viewDirty = true }
func (m *Model) PlanStatus() string     { return m.planStatus }

// ViewDirty returns and clears the dirty flag.
func (m *Model) ViewDirty() bool {
	if m.viewDirty {
		m.viewDirty = false
		return true
	}
	return false
}

// Update processes messages.
func (m *Model) Update(raw tea.Msg) (component.Component, tea.Cmd) {
	switch typed := raw.(type) {
	case msg.PlanUpdateMsg:
		m.applyUpdate(typed)
		return m, nil
	case msg.PlanViewToggleMsg:
		m.visible = !m.visible
		m.viewDirty = true
		return m, nil
	case msg.DecorTickMsg:
		if m.visible && m.hasRunningTask() {
			m.viewDirty = true
		}
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		return m, m.handleKey(typed)
	}
	return m, nil
}

// handleKey processes keyboard input when focused.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "enter", " ":
		m.toggleExpand()
	case "g":
		m.cursor = 0
		m.offset = 0
		m.viewDirty = true
	case "G":
		if len(m.entries) > 0 {
			m.cursor = len(m.entries) - 1
		}
		m.viewDirty = true
	}
	return nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.entries) == 0 {
		return
	}
	m.cursor += delta
	m.cursor = clamp(m.cursor, 0, len(m.entries)-1)
	m.ensureVisible()
	m.viewDirty = true
}

func (m *Model) toggleExpand() {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return
	}
	e := &m.entries[m.cursor]
	e.Expanded = !e.Expanded
	switch e.Kind {
	case entryLayer:
		m.layerExpanded[e.Layer] = e.Expanded
	case entryTask:
		m.taskExpanded[e.TaskID] = e.Expanded
	}
	m.rebuildVisible()
	m.viewDirty = true
}

func (m *Model) ensureVisible() {
	viewH := m.viewHeight()
	if viewH <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+viewH {
		m.offset = m.cursor - viewH + 1
	}
}

func (m *Model) viewHeight() int {
	// Reserve 2 lines for header + divider.
	h := m.height - 2
	if h < 1 {
		return 1
	}
	return h
}

// applyUpdate rebuilds the entry list from a PlanUpdateMsg.
func (m *Model) applyUpdate(update msg.PlanUpdateMsg) {
	m.planID = update.PlanID
	m.planStatus = update.Status
	m.startTime = update.StartTime
	m.visible = true
	m.lastUpdate = update
	m.hasLastUpdate = true

	m.buildEntries(update)

	m.cursor = clamp(m.cursor, 0, max(len(m.entries)-1, 0))
	m.ensureVisible()
	m.viewDirty = true
}

// buildEntries regenerates m.entries from update, honoring the current
// layerExpanded/taskExpanded flags. Idempotent: calling it twice with the
// same update produces the same entries. Callers are responsible for
// clamping m.cursor afterwards.
func (m *Model) buildEntries(update msg.PlanUpdateMsg) {
	taskByID := m.buildTaskIndex(update)
	m.entries = m.entries[:0]
	m.appendLayeredEntries(update, taskByID)
	m.appendOrphanTaskEntries(update)
}

func (m *Model) buildTaskIndex(update msg.PlanUpdateMsg) map[string]msg.PlanTaskSnapshot {
	taskByID := make(map[string]msg.PlanTaskSnapshot, len(update.Tasks))
	for _, t := range update.Tasks {
		taskByID[t.ID] = t
	}
	return taskByID
}

func (m *Model) appendLayeredEntries(update msg.PlanUpdateMsg, taskByID map[string]msg.PlanTaskSnapshot) {
	for layerIdx, ids := range update.ExecutionLayers {
		expanded := m.resolveLayerExpansion(layerIdx)
		m.entries = append(m.entries, entry{
			Kind:     entryLayer,
			Layer:    layerIdx,
			Name:     fmt.Sprintf("Layer %d", layerIdx+1),
			Expanded: expanded,
		})
		if !expanded {
			continue
		}
		for _, id := range ids {
			t, ok := taskByID[id]
			if !ok {
				continue
			}
			m.entries = append(m.entries, m.newTaskEntry(t, layerIdx))
		}
	}
}

func (m *Model) resolveLayerExpansion(layerIdx int) bool {
	expanded, ok := m.layerExpanded[layerIdx]
	if !ok {
		expanded = true // Default layers to expanded.
		m.layerExpanded[layerIdx] = true
	}
	return expanded
}

func (m *Model) appendOrphanTaskEntries(update msg.PlanUpdateMsg) {
	layered := make(map[string]struct{}, len(update.Tasks))
	for _, ids := range update.ExecutionLayers {
		for _, id := range ids {
			layered[id] = struct{}{}
		}
	}
	for _, t := range update.Tasks {
		if _, ok := layered[t.ID]; ok {
			continue
		}
		m.entries = append(m.entries, m.newTaskEntry(t, -1))
	}
}

func (m *Model) newTaskEntry(t msg.PlanTaskSnapshot, layer int) entry {
	return entry{
		Kind:          entryTask,
		Layer:         layer,
		TaskID:        t.ID,
		Name:          t.Name,
		Description:   t.Description,
		AgentType:     t.AgentType,
		Status:        t.Status,
		Dependencies:  t.Dependencies,
		TokensIn:      t.TokensIn,
		TokensOut:     t.TokensOut,
		Duration:      t.Duration,
		StatusMessage: t.StatusMessage,
		Expanded:      m.taskExpanded[t.ID],
	}
}

// rebuildVisible re-derives m.entries from the last observed update, honoring
// the current layerExpanded/taskExpanded flags. Previously this filtered the
// already-pruned m.entries in place; after a collapse the task entries were
// gone and re-expanding the layer could not bring them back until the next
// update arrived (UI-19).
func (m *Model) rebuildVisible() {
	if !m.hasLastUpdate {
		return
	}
	m.buildEntries(m.lastUpdate)
	m.cursor = clamp(m.cursor, 0, max(len(m.entries)-1, 0))
}

func (m *Model) hasRunningTask() bool {
	for _, e := range m.entries {
		if e.Kind == entryTask && (e.Status == "running" || e.Status == "queued") {
			return true
		}
	}
	return false
}

// -- Rendering --

// View renders the plan viewer panel.
func (m *Model) View(_ bool) string {
	if m.width <= 0 || m.height <= 0 || !m.visible {
		return ""
	}

	p := m.palette
	lines := make([]string, 0, m.height)

	// Header line.
	header := m.renderHeader()
	lines = append(lines, header)

	// Divider.
	divider := lipgloss.NewStyle().
		Foreground(p.Border).
		Render(strings.Repeat("─", m.width))
	lines = append(lines, divider)

	// Entry rows.
	viewH := m.viewHeight()
	end := m.offset + viewH
	if end > len(m.entries) {
		end = len(m.entries)
	}
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.renderEntry(i))
		// Expanded detail lines.
		if m.entries[i].Kind == entryTask && m.entries[i].Expanded {
			detail := m.renderDetail(m.entries[i])
			for _, dl := range detail {
				if len(lines) >= m.height {
					break
				}
				lines = append(lines, dl)
			}
		}
	}

	// Pad remaining lines.
	for len(lines) < m.height {
		lines = append(lines, strings.Repeat(" ", m.width))
	}

	return strings.Join(lines[:m.height], "\n")
}

func (m *Model) renderHeader() string {
	p := m.palette
	title := "Plan"
	if m.planID != "" {
		title = fmt.Sprintf("Plan %s", truncate(m.planID, 8))
	}
	statusStyle := m.statusStyle(m.planStatus)
	statusText := statusStyle.Render(m.planStatus)

	left := lipgloss.NewStyle().Bold(true).Foreground(p.Foreground).Render(title)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(statusText)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + statusText
}

func (m *Model) renderEntry(idx int) string {
	e := m.entries[idx]
	p := m.palette
	isCursor := idx == m.cursor && m.focused

	var row string
	switch e.Kind {
	case entryLayer:
		row = m.renderLayerRow(e)
	case entryTask:
		row = m.renderTaskRow(e)
	}

	if isCursor {
		return lipgloss.NewStyle().
			Background(p.Selection).
			Width(m.width).
			Render(row)
	}
	return lipgloss.NewStyle().Width(m.width).Render(row)
}

func (m *Model) renderLayerRow(e entry) string {
	p := m.palette
	icon := iconLayer
	if e.Expanded {
		icon = iconLayerOpen
	}
	style := lipgloss.NewStyle().Foreground(p.Subtext).Bold(true)
	return style.Render(fmt.Sprintf("%s %s", icon, e.Name))
}

func (m *Model) renderTaskRow(e entry) string {
	p := m.palette
	nameW := m.width - colStatusWidth - colAgentWidth - colTokenWidth - colTimeWidth - 4
	if nameW < colMinName {
		nameW = colMinName
	}

	// Status icon.
	statusIcon := m.statusIcon(e.Status)

	// Name column.
	name := "  " + truncate(e.Name, nameW-2)
	nameStyle := lipgloss.NewStyle().Foreground(p.Foreground).Width(nameW)

	// Agent column.
	agent := truncate(e.AgentType, colAgentWidth)
	agentColor := p.AgentColor(e.AgentType)
	agentStyle := lipgloss.NewStyle().Foreground(agentColor).Width(colAgentWidth)

	// Token column.
	tokens := formatTokenPair(e.TokensIn, e.TokensOut)
	tokenStyle := lipgloss.NewStyle().Foreground(p.Muted).Width(colTokenWidth).Align(lipgloss.Right)

	// Duration column.
	dur := formatDuration(e.Duration)
	durStyle := lipgloss.NewStyle().Foreground(p.Muted).Width(colTimeWidth).Align(lipgloss.Right)

	return statusIcon + " " +
		nameStyle.Render(name) + " " +
		agentStyle.Render(agent) + " " +
		tokenStyle.Render(tokens) + " " +
		durStyle.Render(dur)
}

func (m *Model) renderDetail(e entry) []string {
	p := m.palette
	indent := "    "
	style := lipgloss.NewStyle().Foreground(p.Muted)
	var lines []string
	if e.Description != "" {
		lines = append(lines, style.Render(indent+truncate(e.Description, m.width-len(indent))))
	}
	if len(e.Dependencies) > 0 {
		lines = append(lines, style.Render(indent+"deps: "+strings.Join(e.Dependencies, ", ")))
	}
	if e.StatusMessage != "" {
		lines = append(lines, style.Render(indent+truncate(e.StatusMessage, m.width-len(indent))))
	}
	return lines
}

func (m *Model) statusIcon(status string) string {
	p := m.palette
	switch status {
	case "pending":
		return lipgloss.NewStyle().Foreground(p.Muted).Render(iconPending)
	case "queued":
		return lipgloss.NewStyle().Foreground(p.Warning).Render(iconPending)
	case "running":
		elapsed := time.Since(m.animStart)
		frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
		return lipgloss.NewStyle().Foreground(p.Warning).Render(frame)
	case "completed":
		return lipgloss.NewStyle().Foreground(p.Success).Render(iconCompleted)
	case "failed":
		return lipgloss.NewStyle().Foreground(p.Error).Render(iconFailed)
	case "blocked":
		return lipgloss.NewStyle().Foreground(p.Muted).Render(iconBlocked)
	case "skipped":
		return lipgloss.NewStyle().Foreground(p.Muted).Render(iconSkipped)
	default:
		return lipgloss.NewStyle().Foreground(p.Muted).Render(iconPending)
	}
}

func (m *Model) statusStyle(status string) lipgloss.Style {
	p := m.palette
	switch status {
	case "ready":
		return lipgloss.NewStyle().Foreground(p.Success)
	case "executing":
		return lipgloss.NewStyle().Foreground(p.Warning)
	case "completed":
		return lipgloss.NewStyle().Foreground(p.Success).Bold(true)
	case "failed":
		return lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(p.Muted)
	}
}

// -- Helpers --

func formatTokenPair(in, out int) string {
	if in == 0 && out == 0 {
		return "--"
	}
	return fmt.Sprintf("↑%s ↓%s", compactCount(in), compactCount(out))
}

func compactCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "--"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
