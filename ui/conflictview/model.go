package conflictview

import (
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adalundhe/sylk/core/treesitter"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/editor/findbar"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// toolbarHeight is the number of lines consumed by the toolbar (border + content).
const toolbarHeight = 2

// findBarHeight is the number of lines consumed by the find bar.
const findBarHeight = 2

// fileHeaderHeight is the number of lines consumed by the file path header.
const fileHeaderHeight = 1

// spinnerFrames are the braille spinner glyphs used during loading.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerFramePeriod is the duration each spinner frame is displayed.
// Derived from: 10 braille frames × 80ms = 800ms per full cycle.
const spinnerFramePeriod = 80 * time.Millisecond

// actionTarget records click zones for an inline conflict action line.
type actionTarget struct {
	screenY     int    // row index within the content viewport
	hunkIdx     int    // which hunk this action line belongs to
	oursRange   [2]int // [start, end) visual column
	theirsRange [2]int
	bothRange   [2]int
}

// Model is the conflict resolution view state. It renders a file list on the
// left and merged content with inline conflict markers on the right.
type Model struct {
	data ConflictData

	// File list state.
	selectedFile         int
	fileListScroll       int
	fileListW            int
	fileListH            int
	fileListFocused      bool
	fileListBounceOffset int

	// Content pane state.
	scrollOffset int
	width        int
	height       int

	// Per-file parsed state (recomputed on file selection change).
	mergedLines       []string
	hunks             []ConflictHunk
	currentHunk       int
	highlightRegions  [][]codepkg.HighlightRegion
	runeOffsetPerLine []int

	// Find bar.
	findBar    *findbar.FindBar
	findActive bool

	// Click targets (recomputed each render).
	actionTargets []actionTarget

	// Original content for undo (path → original MergedContent).
	originalContent map[string]string

	// Regression warnings per file path (accumulated across resolutions).
	regressionWarnings map[string][]RegressionWarning

	// Contextual conflict labels.
	oursLabel   string // e.g. "Dest" or "Local"
	theirsLabel string // e.g. "Source" or "Remote"
	oursCtx     string // e.g. branch name for ours side
	theirsCtx   string // e.g. branch name or short sha for theirs side

	// Toolbar state.
	toolbarFocused bool
	toolbarAction  int
	hoverBtnIdx    int

	// Inline action hover state.
	hoverActionHunk int // hunk index, -1 = no hover
	hoverActionZone int // 0=ours, 1=theirs, 2=both

	// Loading state (shown during sequencer continue).
	loading      bool
	loadingEpoch time.Time

	// UI state.
	theme        *theme.Theme
	viewDirty    bool
	fileListDirty bool
	focused      bool
	nerdFonts    bool
	bounceOffset int

	// Base content for three-way diff (path → ancestor content).
	baseContent map[string]string
	baseVisible bool

	// Step preview overlay.
	stepPreview        *StepPreview
	stepPreviewVisible bool

	// Chord state: two-key shortcuts (alt+M then 3, alt+D then c).
	pendingChord string

	// Syntax highlighting.
	highlighter  *codepkg.Highlighter
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style
	defaultSt    lipgloss.Style
}

// New creates a conflict resolution view from pre-fetched conflict data.
func New(data ConflictData, th *theme.Theme, nerdFonts bool) *Model {
	hl := codepkg.NewHighlighter(th)
	m := &Model{
		data:            data,
		theme:           th,
		nerdFonts:       nerdFonts,
		hoverBtnIdx:     -1,
		hoverActionHunk: -1,
		highlighter:     hl,
		syntaxStyles:    th.Syntax,
		defaultSt:       lipgloss.NewStyle().Foreground(th.Palette.Foreground),
		viewDirty:       true,
		fileListDirty:   true,
	}
	m.deriveConflictLabels()
	m.snapshotOriginalContent()
	m.parseAllEntryHunks()
	m.detectAutoResolutions()
	m.scoreAndSortEntries()
	if len(data.Entries) > 0 {
		m.parseMergedContent()
		m.scrollToCurrentHunk()
	}
	return m
}

// detectAutoResolutions scans all entries for trivially resolvable hunks.
func (m *Model) detectAutoResolutions() {
	for i := range m.data.Entries {
		m.detectEntryAutoResolutions(&m.data.Entries[i])
	}
}

// detectEntryAutoResolutions marks auto-resolvable hunks on a single entry.
func (m *Model) detectEntryAutoResolutions(e *ConflictFileEntry) {
	if len(e.Hunks) == 0 || e.MergedContent == "" {
		return
	}
	lines := strings.Split(e.MergedContent, "\n")
	for _, ar := range autoResolveHunks(e.Hunks, lines, e.Path) {
		e.Hunks[ar.HunkIdx].AutoResolved = true
		e.Hunks[ar.HunkIdx].AutoKind = ar.Kind
	}
}

// scoreAndSortEntries computes complexity scores and sorts entries simple-first.
func (m *Model) scoreAndSortEntries() {
	if len(m.data.Entries) == 0 {
		return
	}
	scores := scoreAllEntries(m.data.Entries)
	for i := range m.data.Entries {
		m.data.Entries[i].Complexity = scores[i]
	}
	sortByComplexity(m.data.Entries, scores)
}

// deriveConflictLabels sets contextual labels based on the operation type
// and the source/dest names from the sequencer.
//
// Merge (local):  Dest/Source    — context = branch names
// Pull merge:     Local/Remote   — context = branch names if different,
//                                   short sha if same base branch
// Cherry-pick:    Curr/Incmg     — context = branch + source sha
// Rebase:         Curr/Incmg     — context = branch + source sha
func (m *Model) deriveConflictLabels() {
	const (
		opCherryPick = 0 // SeqCherryPick
		opRebase     = 1 // SeqRebase
		opMerge      = 2 // SeqMerge
	)

	switch {
	case m.data.Op == opMerge && strings.Contains(m.data.SourceName, "/"):
		// Pull merge — short fixed labels.
		m.oursLabel = "Locl"
		m.theirsLabel = "Remte"
		m.oursCtx = m.data.DestName
		m.theirsCtx = m.data.SourceName

	case m.data.Op == opCherryPick || m.data.Op == opRebase:
		// Rebase/cherry-pick — short fixed labels, subject as context.
		m.oursLabel = "Curr"
		m.theirsLabel = "Incmg"
		m.oursCtx = m.data.DestName
		m.theirsCtx = m.data.Subject

	default:
		// Local merge — short fixed labels.
		m.oursLabel = "Dest"
		m.theirsLabel = "Source"
		m.oursCtx = m.data.DestName
		m.theirsCtx = m.data.SourceName
	}
}

// ---------------------------------------------------------------------------
// Size & focus
// ---------------------------------------------------------------------------

// SetSize sets the available width and height for the content pane.
func (m *Model) SetSize(w, h int) {
	if m.width == w && m.height == h {
		return
	}
	m.width = w
	m.height = h
	m.viewDirty = true
}

// SetFileListSize sets the available dimensions for the file list sidebar.
func (m *Model) SetFileListSize(w, h int) {
	if m.fileListW == w && m.fileListH == h {
		return
	}
	m.fileListW = w
	m.fileListH = h
	m.fileListDirty = true
}

// Focused returns whether the conflict view has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets focus state.
func (m *Model) SetFocused(f bool) {
	m.focused = f
	m.viewDirty = true
}

// ViewDirty returns and clears the dirty flag.
func (m *Model) ViewDirty() bool {
	d := m.viewDirty
	m.viewDirty = false
	return d
}

// FileListDirty returns and clears the file list dirty flag.
func (m *Model) FileListDirty() bool {
	d := m.fileListDirty
	m.fileListDirty = false
	return d
}

// resolutionLabel returns the display label for a conflict resolution,
// using the contextual ours/theirs labels.
func (m *Model) resolutionLabel(r ConflictResolution) string {
	switch r {
	case ResOurs:
		return m.oursLabel
	case ResTheirs:
		return m.theirsLabel
	case ResBoth:
		return "Both"
	default:
		return "---"
	}
}

// Entries returns the conflict entries (for syntax validation in app layer).
func (m *Model) Entries() []ConflictFileEntry { return m.data.Entries }

// SetBaseContent stores fetched ancestor content for a file path.
func (m *Model) SetBaseContent(path, content string) {
	if m.baseContent == nil {
		m.baseContent = make(map[string]string)
	}
	m.baseContent[path] = content
	m.viewDirty = true
}

// SetStepPreview stores the commit diff data for step-preview display.
func (m *Model) SetStepPreview(p *StepPreview) {
	m.stepPreview = p
	m.viewDirty = true
}

// BaseVisible reports whether the three-way base view is toggled on.
func (m *Model) BaseVisible() bool { return m.baseVisible }

// StepPreviewVisible reports whether the step preview overlay is toggled on.
func (m *Model) StepPreviewVisible() bool { return m.stepPreviewVisible }

// Close releases tree-sitter resources.
func (m *Model) Close() {
	if m.highlighter != nil {
		m.highlighter.Close()
		m.highlighter = nil
	}
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// FileListFocused returns whether the file list has focus.
func (m *Model) FileListFocused() bool { return m.fileListFocused }

// SetFileListFocused sets file list focus state.
func (m *Model) SetFileListFocused(f bool) {
	m.fileListFocused = f
	m.fileListDirty = true
}

// AllResolved reports whether all conflict files are resolved.
func (m *Model) AllResolved() bool {
	for i := range m.data.Entries {
		if m.data.Entries[i].Resolution == ResUnresolved {
			return false
		}
	}
	return true
}

// MarkResolvedByPath marks a file as resolved by path (editor-driven).
func (m *Model) MarkResolvedByPath(path string) {
	for i := range m.data.Entries {
		if m.data.Entries[i].Path == path {
			m.data.Entries[i].Resolution = ResOurs
			m.viewDirty = true
			m.fileListDirty = true
			return
		}
	}
}

// SetBounceOffset updates the visual bounce displacement.
func (m *Model) SetBounceOffset(offset int) {
	if m.bounceOffset == offset {
		return
	}
	m.bounceOffset = offset
	m.viewDirty = true
}

// BounceOffset returns the current bounce offset.
func (m *Model) BounceOffset() int { return m.bounceOffset }

// SetFileListBounceOffset updates the file list bounce displacement.
func (m *Model) SetFileListBounceOffset(offset int) {
	if m.fileListBounceOffset == offset {
		return
	}
	m.fileListBounceOffset = offset
	m.fileListDirty = true
}

// ---------------------------------------------------------------------------
// Scroll
// ---------------------------------------------------------------------------

// viewportHeight returns the height available for content.
func (m *Model) viewportHeight() int {
	overhead := toolbarHeight + fileHeaderHeight
	if m.findActive {
		overhead += findBarHeight
	}
	return max(m.height-overhead, 1)
}

// totalContentLines returns the total display line count (content + action lines).
func (m *Model) totalContentLines() int {
	return len(m.mergedLines) + len(m.hunks)
}

// maxScroll returns the maximum scroll offset.
func (m *Model) maxScroll() int {
	return max(m.totalContentLines()-m.viewportHeight(), 0)
}

// clampScroll constrains scrollOffset.
func (m *Model) clampScroll() {
	m.scrollOffset = max(min(m.scrollOffset, m.maxScroll()), 0)
}

// ScrollUp scrolls the content pane up. Returns false at boundary.
func (m *Model) ScrollUp() bool {
	if m.scrollOffset <= 0 {
		return false
	}
	m.scrollOffset = max(m.scrollOffset-1, 0)
	m.viewDirty = true
	return true
}

// ScrollDown scrolls the content pane down. Returns false at boundary.
func (m *Model) ScrollDown() bool {
	mx := m.maxScroll()
	if m.scrollOffset >= mx {
		return false
	}
	m.scrollOffset = min(m.scrollOffset+1, mx)
	m.viewDirty = true
	return true
}

// ScrollFileListUp moves the file cursor up. Returns false at boundary.
func (m *Model) ScrollFileListUp() bool {
	if m.selectedFile <= 0 {
		return false
	}
	m.selectedFile--
	m.onFileSelectionChanged()
	return true
}

// ScrollFileListDown moves the file cursor down. Returns false at boundary.
func (m *Model) ScrollFileListDown() bool {
	if m.selectedFile+1 >= len(m.data.Entries) {
		return false
	}
	m.selectedFile++
	m.onFileSelectionChanged()
	return true
}

// onFileSelectionChanged resets scroll and recomputes per-file state,
// then scrolls to the first conflict hunk if one exists.
func (m *Model) onFileSelectionChanged() {
	m.scrollOffset = 0
	m.currentHunk = 0
	m.regressionWarnings = nil
	m.parseMergedContent()
	m.scrollToCurrentHunk()
	m.viewDirty = true
	m.fileListDirty = true
}

// advanceToNextUnresolved moves selection to the next unresolved file.
// Wraps around. Does nothing if all files are resolved.
func (m *Model) advanceToNextUnresolved() {
	n := len(m.data.Entries)
	for i := 1; i <= n; i++ {
		idx := (m.selectedFile + i) % n
		if m.data.Entries[idx].Resolution == ResUnresolved {
			m.selectedFile = idx
			m.onFileSelectionChanged()
			return
		}
	}
	// All files resolved — auto-focus Continue button.
	m.toolbarFocused = true
	m.toolbarAction = ctbContinue
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Undo
// ---------------------------------------------------------------------------

// snapshotOriginalContent stores the initial MergedContent for each entry
// so that undo can restore it.
func (m *Model) snapshotOriginalContent() {
	m.originalContent = make(map[string]string, len(m.data.Entries))
	for i := range m.data.Entries {
		e := &m.data.Entries[i]
		if e.MergedContent != "" {
			m.originalContent[e.Path] = e.MergedContent
		}
	}
}

// canUndo reports whether the selected entry has been modified from its
// original state (resolution set or content changed).
func (m *Model) canUndo() bool {
	e := m.selectedEntry()
	if e == nil {
		return false
	}
	if e.Resolution != ResUnresolved {
		return true
	}
	orig, ok := m.originalContent[e.Path]
	return ok && e.MergedContent != orig
}

// undoResolution resets the selected file to its original unresolved state.
func (m *Model) undoResolution() tea.Cmd {
	e := m.selectedEntry()
	if e == nil {
		return nil
	}
	if orig, ok := m.originalContent[e.Path]; ok {
		e.MergedContent = orig
	}
	e.Resolution = ResUnresolved
	m.parseMergedContent()
	m.scrollToCurrentHunk()
	m.viewDirty = true
	m.fileListDirty = true
	return nil
}

// ---------------------------------------------------------------------------
// File hunk computation
// ---------------------------------------------------------------------------

// parseAllEntryHunks detects conflict hunks for every entry and stores
// them directly on the entry, so the file list can display hunk locations
// without re-parsing.
func (m *Model) parseAllEntryHunks() {
	for i := range m.data.Entries {
		e := &m.data.Entries[i]
		if e.MergedContent == "" {
			e.Hunks = nil
			continue
		}
		lines := strings.Split(e.MergedContent, "\n")
		e.Hunks = detectHunks(lines)
	}
}

// ---------------------------------------------------------------------------
// Merged content parsing
// ---------------------------------------------------------------------------

// parseMergedContent parses the selected file's merged content into lines,
// detects conflict hunks, computes syntax highlights, and rune offsets.
func (m *Model) parseMergedContent() {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		m.mergedLines = nil
		m.hunks = nil
		m.highlightRegions = nil
		m.runeOffsetPerLine = nil
		return
	}

	entry := &m.data.Entries[m.selectedFile]

	// For delete/modify and binary conflicts, there's no merged content.
	if entry.MergedContent == "" {
		m.mergedLines = nil
		m.hunks = nil
		m.highlightRegions = nil
		m.runeOffsetPerLine = nil
		return
	}

	m.mergedLines = strings.Split(entry.MergedContent, "\n")
	m.hunks = detectHunks(m.mergedLines)

	// Syntax highlighting.
	lang := treesitter.DetectLanguage(entry.Path)
	m.highlightRegions = m.highlighter.HighlightContent(entry.MergedContent, lang)

	// Cumulative rune offsets per line (for find bar match mapping).
	m.runeOffsetPerLine = computeRuneOffsets(m.mergedLines)

	// Recompute find matches if active.
	if m.findActive && m.findBar != nil {
		content := []rune(entry.MergedContent)
		m.findBar.Recompute(content, 0, len(content))
	}

	// Clamp current hunk.
	if m.currentHunk >= len(m.hunks) {
		m.currentHunk = max(len(m.hunks)-1, 0)
	}

	entry.Hunks = m.hunks
}

// detectHunks scans lines for <<<<<<</=======/>>>>>>> markers.
func detectHunks(lines []string) []ConflictHunk {
	var hunks []ConflictHunk
	var pending *ConflictHunk
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			pending = &ConflictHunk{
				StartLine: i,
				OursLabel: strings.TrimSpace(strings.TrimPrefix(line, "<<<<<<<")),
			}
		case strings.HasPrefix(line, "=======") && pending != nil:
			pending.DivLine = i
		case strings.HasPrefix(line, ">>>>>>>") && pending != nil && pending.DivLine > 0:
			pending.EndLine = i
			pending.TheirsLabel = strings.TrimSpace(strings.TrimPrefix(line, ">>>>>>>"))
			pending.Snippet = firstNonBlankLine(lines, pending.StartLine+1, pending.DivLine)
			hunks = append(hunks, *pending)
			pending = nil
		}
	}
	return hunks
}

// firstNonBlankLine returns the first non-blank trimmed line in lines[start:end].
func firstNonBlankLine(lines []string, start, end int) string {
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// computeRuneOffsets computes cumulative rune offsets for each line.
// runeOffsetPerLine[i] is the rune offset of the first character of line i
// within the full content string.
func computeRuneOffsets(lines []string) []int {
	offsets := make([]int, len(lines))
	cumulative := 0
	for i, line := range lines {
		offsets[i] = cumulative
		cumulative += utf8.RuneCountInString(line) + 1 // +1 for newline
	}
	return offsets
}

// ---------------------------------------------------------------------------
// Display-line mapping
// ---------------------------------------------------------------------------

// Each hunk inserts one virtual action line before its StartLine.
// Action line for hunk h is at display position: hunks[h].StartLine + h.
// Total display lines = len(mergedLines) + len(hunks).

// displayToContent converts a display line index to either a content line
// index or identifies it as an action line for a specific hunk.
func (m *Model) displayToContent(d int) (contentLine int, isAction bool, hunkIdx int) {
	actionsBefore := 0
	for i, h := range m.hunks {
		actionPos := h.StartLine + i
		if d == actionPos {
			return 0, true, i
		}
		if actionPos < d {
			actionsBefore = i + 1
		}
	}
	return d - actionsBefore, false, -1
}

// contentToDisplay converts a content line index to its display position.
func (m *Model) contentToDisplay(c int) int {
	offset := 0
	for _, h := range m.hunks {
		if h.StartLine <= c {
			offset++
		} else {
			break
		}
	}
	return c + offset
}

// ---------------------------------------------------------------------------
// Hunk navigation
// ---------------------------------------------------------------------------

// nextHunk advances to the next conflict hunk, wrapping around.
func (m *Model) nextHunk() {
	if len(m.hunks) == 0 {
		return
	}
	m.currentHunk = (m.currentHunk + 1) % len(m.hunks)
	m.scrollToCurrentHunk()
	m.viewDirty = true
	m.fileListDirty = true
}

// prevHunk retreats to the previous conflict hunk, wrapping around.
func (m *Model) prevHunk() {
	if len(m.hunks) == 0 {
		return
	}
	m.currentHunk = (m.currentHunk - 1 + len(m.hunks)) % len(m.hunks)
	m.scrollToCurrentHunk()
	m.viewDirty = true
	m.fileListDirty = true
}

// scrollToCurrentHunk scrolls the viewport to show the current hunk's action
// line at approximately 1/4 viewport height.
func (m *Model) scrollToCurrentHunk() {
	if m.currentHunk >= len(m.hunks) {
		return
	}
	// Action line is one display row before the hunk's StartLine.
	actionDisplay := m.contentToDisplay(m.hunks[m.currentHunk].StartLine) - 1
	vpH := m.viewportHeight()
	target := actionDisplay - vpH/4
	m.scrollOffset = max(min(target, m.maxScroll()), 0)
}

// ---------------------------------------------------------------------------
// Per-hunk resolution
// ---------------------------------------------------------------------------

// resolveCurrentHunk resolves the current hunk by splicing mergedLines.
func (m *Model) resolveCurrentHunk(r ConflictResolution) tea.Cmd {
	if !m.canResolveHunk() {
		return nil
	}
	hunk := m.hunks[m.currentHunk]
	entry := &m.data.Entries[m.selectedFile]

	m.recordRegressionWarnings(entry.Path, hunk, r)
	m.mergedLines = spliceHunk(m.mergedLines, hunk, r)
	entry.MergedContent = strings.Join(m.mergedLines, "\n")
	m.parseMergedContent()
	return m.afterHunkResolution(entry)
}

// canResolveHunk reports whether there is a valid hunk+file to resolve.
func (m *Model) canResolveHunk() bool {
	return m.hasSelectedFile() &&
		len(m.hunks) > 0 &&
		m.currentHunk < len(m.hunks)
}

// recordRegressionWarnings checks a hunk resolution for suspicious patterns
// and appends any warnings to the per-file warning map.
func (m *Model) recordRegressionWarnings(path string, hunk ConflictHunk, r ConflictResolution) {
	if m.regressionWarnings == nil {
		m.regressionWarnings = make(map[string][]RegressionWarning)
	}
	warnings := checkResolution(m.mergedLines, hunk, r)
	if len(warnings) == 0 {
		return
	}
	for i := range warnings {
		warnings[i].HunkIdx = m.currentHunk
	}
	m.regressionWarnings[path] = append(m.regressionWarnings[path], warnings...)
}

// spliceHunk removes conflict markers from lines based on the resolution.
func spliceHunk(lines []string, hunk ConflictHunk, r ConflictResolution) []string {
	switch r {
	case ResOurs:
		lines = spliceRemoveRange(lines, hunk.DivLine, hunk.EndLine+1)
		return spliceRemoveRange(lines, hunk.StartLine, hunk.StartLine+1)
	case ResTheirs:
		lines = spliceRemoveRange(lines, hunk.EndLine, hunk.EndLine+1)
		return spliceRemoveRange(lines, hunk.StartLine, hunk.DivLine+1)
	case ResBoth:
		lines = spliceRemoveRange(lines, hunk.EndLine, hunk.EndLine+1)
		lines = spliceRemoveRange(lines, hunk.DivLine, hunk.DivLine+1)
		return spliceRemoveRange(lines, hunk.StartLine, hunk.StartLine+1)
	default:
		return lines
	}
}

// afterHunkResolution handles post-splice state: marks file resolved if all
// hunks are gone, or clamps the hunk index and scrolls.
func (m *Model) afterHunkResolution(entry *ConflictFileEntry) tea.Cmd {
	if len(m.hunks) == 0 {
		return m.markFileResolved(entry)
	}
	m.currentHunk = min(m.currentHunk, len(m.hunks)-1)
	m.scrollToCurrentHunk()
	m.viewDirty = true
	return nil
}

// markFileResolved marks an entry resolved, advances to the next unresolved
// file, and emits a write command for the resolved content.
func (m *Model) markFileResolved(entry *ConflictFileEntry) tea.Cmd {
	entry.Resolution = ResOurs
	m.fileListDirty = true
	path := entry.Path
	content := entry.MergedContent
	m.advanceToNextUnresolved()
	return func() tea.Msg {
		return ConflictWriteContentMsg{Path: path, Content: content}
	}
}

// spliceRemoveRange removes lines[start:end] from the slice.
func spliceRemoveRange(lines []string, start, end int) []string {
	return append(lines[:start], lines[end:]...)
}

// hasRegressionAtLine reports whether the selected file has any regression
// warnings. After resolution, hunk boundaries shift, so regression warnings
// are shown as a per-file indicator on all gutter lines.
func (m *Model) hasRegressionAtLine(_ int) bool {
	if len(m.regressionWarnings) == 0 {
		return false
	}
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return false
	}
	return len(m.regressionWarnings[m.data.Entries[m.selectedFile].Path]) > 0
}

// hasHunks reports whether the selected entry has inline conflict hunks.
func (m *Model) hasHunks() bool {
	return len(m.hunks) > 0
}

// resolveHunkByIndex resolves a specific hunk by its index.
func (m *Model) resolveHunkByIndex(hunkIdx int, r ConflictResolution) tea.Cmd {
	if hunkIdx < 0 || hunkIdx >= len(m.hunks) {
		return nil
	}
	old := m.currentHunk
	m.currentHunk = hunkIdx
	cmd := m.resolveCurrentHunk(r)
	// If resolution didn't clear all hunks, restore a sensible index.
	if len(m.hunks) > 0 && m.currentHunk >= len(m.hunks) {
		m.currentHunk = min(old, len(m.hunks)-1)
	}
	return cmd
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the right panel: content + optional find bar + nav bar + toolbar.
func (m *Model) View(cursorVisible bool) string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	if m.loading {
		return m.renderLoading()
	}

	vpH := m.viewportHeight()

	var parts []string
	parts = append(parts, m.renderFileHeader())

	content := m.renderContent(vpH)
	if m.bounceOffset != 0 {
		content = applyBounceShift(content, m.bounceOffset, vpH)
	}
	parts = append(parts, content)

	if m.findActive && m.findBar != nil {
		parts = append(parts, m.findBar.View(m.width, m.theme, cursorVisible))
	}

	parts = append(parts, m.renderToolbar())

	return strings.Join(parts, "\n")
}

// renderFileHeader renders a header line matching the code viewer style:
// " filename.ext ──────────────".
func (m *Model) renderFileHeader() string {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return padLine("", m.width)
	}
	name := filepath.Base(m.data.Entries[m.selectedFile].Path)
	nameStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted).Bold(true)
	lineStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)

	text := nameStyle.Render(" " + name + " ")
	textWidth := lipgloss.Width(text)
	lineWidth := max(m.width-textWidth, 0)

	return text + lineStyle.Render(strings.Repeat("─", lineWidth))
}

// gutterWidth returns the number of characters needed for line number display,
// derived from the total number of merged content lines.
func (m *Model) gutterWidth() int {
	lineCount := len(m.mergedLines)
	if lineCount == 0 {
		return 1
	}
	return int(math.Log10(float64(lineCount))) + 1
}

// renderLoading renders a centered loading spinner.
func (m *Model) renderLoading() string {
	lines := make([]string, m.height)
	blank := strings.Repeat(" ", m.width)
	for i := range lines {
		lines[i] = blank
	}
	if m.height > 0 {
		elapsed := time.Since(m.loadingEpoch)
		frame := spinnerFrames[int(elapsed/spinnerFramePeriod)%len(spinnerFrames)]
		spinSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary)
		textSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
		text := spinSt.Render(frame) + " " + textSt.Render("Continuing...")
		textW := lipgloss.Width(text)
		pad := max((m.width-textW)/2, 0)
		lines[m.height/2] = padLine(strings.Repeat(" ", pad)+text, m.width)
	}
	return strings.Join(lines, "\n")
}

// SetLoading sets the loading state.
func (m *Model) SetLoading(loading bool) {
	if m.loading == loading {
		return
	}
	m.loading = loading
	if loading {
		m.loadingEpoch = time.Now()
	}
	m.viewDirty = true
}

// NeedsDecorTick returns true when the loading spinner is active.
func (m *Model) NeedsDecorTick() bool {
	return m.loading
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

// Update handles input messages for the content pane.
func (m *Model) Update(raw tea.Msg) tea.Cmd {
	switch msg := raw.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return nil
}

// UpdateFileList handles keyboard input for the file list, returning a tea.Cmd.
func (m *Model) UpdateFileList(key string) tea.Cmd {
	if fn, ok := fileListKeyActions[key]; ok {
		return fn(m)
	}
	return nil
}

// chordPrefixes is the set of keys that start a two-key chord sequence.
var chordPrefixes = map[string]bool{
	"alt+M": true,
	"alt+D": true,
}

// handleKey processes keyboard input for the content pane.
func (m *Model) handleKey(key tea.KeyMsg) tea.Cmd {
	if m.findActive && m.findBar != nil {
		return m.handleFindBarKey(key)
	}
	if m.toolbarFocused {
		return m.handleToolbarKey(key)
	}
	return m.handleContentInput(key.String())
}

// handleContentInput dispatches chord or normal key input.
func (m *Model) handleContentInput(ks string) tea.Cmd {
	if cmd, handled := m.handleChordInput(ks); handled {
		return cmd
	}
	return m.dispatchContentKey(ks)
}

// handleChordInput processes two-key chord sequences.
// Returns (cmd, true) if the key was consumed by chord logic.
func (m *Model) handleChordInput(ks string) (tea.Cmd, bool) {
	if m.pendingChord != "" {
		return m.finishChord(ks)
	}
	if chordPrefixes[ks] {
		m.pendingChord = ks
		m.viewDirty = true
		return nil, true
	}
	return nil, false
}

// finishChord completes a pending chord with the given second key.
func (m *Model) finishChord(ks string) (tea.Cmd, bool) {
	chord := m.pendingChord
	m.pendingChord = ""
	m.viewDirty = true
	cmd := m.completeChord(chord, ks)
	return cmd, cmd != nil
}

// completeChord handles the second key of a two-key chord.
// Returns a non-nil cmd if the chord was consumed.
func (m *Model) completeChord(prefix, completion string) tea.Cmd {
	switch {
	case prefix == "alt+M" && completion == "3":
		// Toggle three-way base context view.
		m.baseVisible = !m.baseVisible
		m.viewDirty = true
		if m.baseVisible {
			return m.requestBaseContent()
		}
		return nil

	case prefix == "alt+D" && completion == "c":
		// Toggle step preview overlay.
		m.stepPreviewVisible = !m.stepPreviewVisible
		m.viewDirty = true
		if m.stepPreviewVisible && m.stepPreview == nil {
			return m.requestStepPreview()
		}
		return nil
	}
	return nil
}

// requestBaseContent emits a BaseContentRequestMsg for the selected file
// if base hash is available and content isn't already cached.
func (m *Model) requestBaseContent() tea.Cmd {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	e := &m.data.Entries[m.selectedFile]
	if e.BaseHash == "" || e.BaseHash == zeroHash {
		return nil
	}
	if m.baseContent != nil {
		if _, ok := m.baseContent[e.Path]; ok {
			return nil // Already cached.
		}
	}
	path := e.Path
	hash := e.BaseHash
	return func() tea.Msg {
		return BaseContentRequestMsg{Path: path, BaseHash: hash}
	}
}

// requestStepPreview emits a StepPreviewRequestMsg for the current commit.
func (m *Model) requestStepPreview() tea.Cmd {
	if m.data.Hash == "" {
		return nil
	}
	hash := m.data.Hash
	return func() tea.Msg {
		return StepPreviewRequestMsg{Hash: hash}
	}
}

// handleFindBarKey routes keys to the find bar.
func (m *Model) handleFindBarKey(key tea.KeyMsg) tea.Cmd {
	action := m.findBar.HandleKey(key)
	switch action {
	case findbar.ActionClose:
		m.findActive = false
		m.findBar = nil
		m.viewDirty = true
	case findbar.ActionQueryChanged:
		m.recomputeFindMatches()
		m.viewDirty = true
	case findbar.ActionNextMatch:
		m.findBar.AdvanceMatch()
		m.scrollToCurrentMatch()
		m.viewDirty = true
	case findbar.ActionPrevMatch:
		m.findBar.RetreatMatch()
		m.scrollToCurrentMatch()
		m.viewDirty = true
	}
	return nil
}

// recomputeFindMatches recomputes find bar matches against current content.
func (m *Model) recomputeFindMatches() {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return
	}
	entry := &m.data.Entries[m.selectedFile]
	content := []rune(entry.MergedContent)
	m.findBar.Recompute(content, 0, len(content))
}

// scrollToCurrentMatch scrolls the viewport to show the current find match.
func (m *Model) scrollToCurrentMatch() {
	match, ok := m.findBar.CurrentMatch()
	if !ok {
		return
	}
	lineIdx := m.runeOffsetToLine(match.Start)
	displayLine := m.contentToDisplay(lineIdx)
	vpH := m.viewportHeight()
	if displayLine < m.scrollOffset || displayLine >= m.scrollOffset+vpH {
		m.scrollOffset = max(displayLine-vpH/4, 0)
		m.clampScroll()
	}
}

// runeOffsetToLine maps a rune offset to its line number via binary search.
func (m *Model) runeOffsetToLine(runeOff int) int {
	lo, hi := 0, len(m.runeOffsetPerLine)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if m.runeOffsetPerLine[mid] <= runeOff {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action == tea.MouseActionPress {
		return m.handleMousePress(msg)
	}
	if msg.Action == tea.MouseActionMotion {
		m.handleMouseMotion(msg)
	}
	return nil
}

// handleMousePress dispatches press events.
func (m *Model) handleMousePress(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollOffset = max(m.scrollOffset-3, 0)
		m.viewDirty = true
	case tea.MouseButtonWheelDown:
		m.scrollOffset = min(m.scrollOffset+3, m.maxScroll())
		m.viewDirty = true
	case tea.MouseButtonLeft:
		return m.handleLeftClick(msg)
	}
	return nil
}

// handleLeftClick dispatches left mouse clicks to the appropriate UI zone.
func (m *Model) handleLeftClick(msg tea.MouseMsg) tea.Cmd {
	vpH := m.viewportHeight()
	tbRow := m.height - toolbarHeight

	// Toolbar zone.
	if msg.Y >= tbRow {
		idx := m.conflictToolbarHitTest(msg.X)
		if idx >= 0 {
			return m.activateToolbarButton(idx)
		}
		return nil
	}

	// Content area — check for action line clicks.
	contentY := msg.Y - fileHeaderHeight
	if contentY >= 0 && contentY < vpH {
		return m.handleContentClick(contentY, msg.X)
	}

	return nil
}

// handleContentClick checks whether a click hit an inline action line.
func (m *Model) handleContentClick(screenY, x int) tea.Cmd {
	for _, at := range m.actionTargets {
		if at.screenY != screenY {
			continue
		}
		if x >= at.oursRange[0] && x < at.oursRange[1] {
			return m.resolveHunkByIndex(at.hunkIdx, ResOurs)
		}
		if x >= at.theirsRange[0] && x < at.theirsRange[1] {
			return m.resolveHunkByIndex(at.hunkIdx, ResTheirs)
		}
		if x >= at.bothRange[0] && x < at.bothRange[1] {
			return m.resolveHunkByIndex(at.hunkIdx, ResBoth)
		}
		return nil
	}
	return nil
}

// handleMouseMotion handles mouse motion for hover tracking.
func (m *Model) handleMouseMotion(msg tea.MouseMsg) {
	tbRow := m.height - toolbarHeight
	if msg.Y >= tbRow {
		idx := m.conflictToolbarHitTest(msg.X)
		if idx != m.hoverBtnIdx {
			m.hoverBtnIdx = idx
			m.viewDirty = true
		}
		if m.hoverActionHunk >= 0 {
			m.hoverActionHunk = -1
			m.viewDirty = true
		}
		return
	}
	if m.hoverBtnIdx >= 0 {
		m.hoverBtnIdx = -1
		m.viewDirty = true
	}
	// Check action line hover in content area.
	contentY := msg.Y - fileHeaderHeight
	if contentY >= 0 && contentY < m.viewportHeight() {
		hunk, zone := m.hitTestActionLine(contentY, msg.X)
		if hunk != m.hoverActionHunk || zone != m.hoverActionZone {
			m.hoverActionHunk = hunk
			m.hoverActionZone = zone
			m.viewDirty = true
		}
		return
	}
	if m.hoverActionHunk >= 0 {
		m.hoverActionHunk = -1
		m.viewDirty = true
	}
}

// hitTestActionLine checks if screen coordinates hit an inline action button.
// Returns hunk index (-1 if miss) and zone (0=ours, 1=theirs, 2=both).
func (m *Model) hitTestActionLine(screenY, x int) (int, int) {
	for _, at := range m.actionTargets {
		if at.screenY != screenY {
			continue
		}
		if x >= at.oursRange[0] && x < at.oursRange[1] {
			return at.hunkIdx, 0
		}
		if x >= at.theirsRange[0] && x < at.theirsRange[1] {
			return at.hunkIdx, 1
		}
		if x >= at.bothRange[0] && x < at.bothRange[1] {
			return at.hunkIdx, 2
		}
		return -1, 0
	}
	return -1, 0
}

// dispatchContentKey handles keys in the content pane.
func (m *Model) dispatchContentKey(ks string) tea.Cmd {
	if fn, ok := contentKeyActions[ks]; ok {
		return fn(m)
	}
	return nil
}

// contentKeyActions maps keys to content pane actions.
var contentKeyActions = map[string]func(*Model) tea.Cmd{
	"j":    func(m *Model) tea.Cmd { m.scrollOffset = min(m.scrollOffset+1, m.maxScroll()); m.viewDirty = true; return nil },
	"down": func(m *Model) tea.Cmd { m.scrollOffset = min(m.scrollOffset+1, m.maxScroll()); m.viewDirty = true; return nil },
	"k":    func(m *Model) tea.Cmd { m.scrollOffset = max(m.scrollOffset-1, 0); m.viewDirty = true; return nil },
	"up":   func(m *Model) tea.Cmd { m.scrollOffset = max(m.scrollOffset-1, 0); m.viewDirty = true; return nil },
	"g":    func(m *Model) tea.Cmd { m.scrollOffset = 0; m.viewDirty = true; return nil },
	"home": func(m *Model) tea.Cmd { m.scrollOffset = 0; m.viewDirty = true; return nil },
	"G":    func(m *Model) tea.Cmd { m.scrollOffset = m.maxScroll(); m.viewDirty = true; return nil },
	"end":  func(m *Model) tea.Cmd { m.scrollOffset = m.maxScroll(); m.viewDirty = true; return nil },
	"ctrl+d": func(m *Model) tea.Cmd {
		m.scrollOffset = min(m.scrollOffset+m.viewportHeight()/2, m.maxScroll())
		m.viewDirty = true
		return nil
	},
	"ctrl+u": func(m *Model) tea.Cmd {
		m.scrollOffset = max(m.scrollOffset-m.viewportHeight()/2, 0)
		m.viewDirty = true
		return nil
	},
	"o": func(m *Model) tea.Cmd { return m.resolveKey(ResOurs) },
	"t": func(m *Model) tea.Cmd { return m.resolveKey(ResTheirs) },
	"b": func(m *Model) tea.Cmd { return m.resolveKey(ResBoth) },
	"u": func(m *Model) tea.Cmd { return m.undoResolution() },
	"n": func(m *Model) tea.Cmd { m.nextHunk(); return nil },
	"N": func(m *Model) tea.Cmd { m.prevHunk(); return nil },
	"ctrl+f": func(m *Model) tea.Cmd { m.toggleFindBar(); return nil },
	"/":      func(m *Model) tea.Cmd { m.toggleFindBar(); return nil },
	"tab": func(m *Model) tea.Cmd {
		m.toolbarFocused = true
		m.toolbarAction = 0
		m.viewDirty = true
		return nil
	},
	"shift+tab": func(m *Model) tea.Cmd {
		m.toolbarFocused = true
		m.toolbarAction = m.ctbFittingCount() - 1
		m.viewDirty = true
		return nil
	},
}

// resolveKey dispatches resolution based on whether the entry has hunks.
func (m *Model) resolveKey(r ConflictResolution) tea.Cmd {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	e := &m.data.Entries[m.selectedFile]
	if !supportsResolution(e, r) {
		return nil
	}
	// For entries with inline hunks, resolve per-hunk.
	if m.hasHunks() {
		return m.resolveCurrentHunk(r)
	}
	// For delete/modify, binary — whole-file resolution.
	return m.setResolution(r)
}

// toggleFindBar toggles the find bar on/off.
func (m *Model) toggleFindBar() {
	if m.findActive {
		m.findActive = false
		m.findBar = nil
	} else {
		m.findBar = findbar.New(0, 0, false)
		m.findActive = true
	}
	m.clampScroll()
	m.viewDirty = true
}

// setResolution applies a resolution to the currently selected file.
func (m *Model) setResolution(r ConflictResolution) tea.Cmd {
	if m.selectedFile < 0 || m.selectedFile >= len(m.data.Entries) {
		return nil
	}
	e := &m.data.Entries[m.selectedFile]
	if e.Resolution == r {
		return nil
	}
	if !supportsResolution(e, r) {
		return nil
	}
	e.Resolution = r
	m.viewDirty = true
	m.fileListDirty = true
	m.advanceToNextUnresolved()
	return func() tea.Msg {
		return ConflictResolveFileMsg{
			Path:       e.Path,
			Resolution: r,
			OursHash:   e.OursHash,
			TheirsHash: e.TheirsHash,
		}
	}
}

// supportsResolution reports whether a resolution is valid for the entry type.
// Binary and DeleteModify skip "Both".
func supportsResolution(e *ConflictFileEntry, r ConflictResolution) bool {
	if r != ResBoth {
		return true
	}
	return e.Type != TypeDeleteModify && e.Type != TypeBinary
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// applyBounceShift shifts content by offset lines for rubber-band effect.
func applyBounceShift(content string, offset, viewHeight int) string {
	lines := strings.Split(content, "\n")
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	if offset > 0 {
		shift := min(absOffset, len(lines))
		lines = lines[shift:]
	} else {
		pad := make([]string, absOffset, absOffset+len(lines))
		lines = append(pad, lines...)
	}
	for len(lines) < viewHeight {
		lines = append(lines, "")
	}
	if len(lines) > viewHeight {
		lines = lines[:viewHeight]
	}
	return strings.Join(lines, "\n")
}