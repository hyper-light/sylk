package knowledge

import (
	"strings"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Sub-view state machine
// ---------------------------------------------------------------------------

// ViewMode represents which sub-view is currently active.
type ViewMode int

const (
	QueryMode    ViewMode = iota // Query input and weight configuration.
	ResultsMode                  // Browsing search results.
	DocumentMode                 // Viewing a selected document.
)

// viewModeNames maps ViewMode values to human-readable labels.
var viewModeNames = map[ViewMode]string{
	QueryMode:    "Query",
	ResultsMode:  "Results",
	DocumentMode: "Document",
}

// String returns the human-readable name of the ViewMode.
func (vm ViewMode) String() string {
	if name, ok := viewModeNames[vm]; ok {
		return name
	}
	return "Unknown"
}

// ---------------------------------------------------------------------------
// Layout constants (derived)
// ---------------------------------------------------------------------------

// queryPanelHeight is the number of rows reserved for the query builder
// sub-view (input + preset + 3 sliders + padding).
// Derived from: query line(1) + preset(1) + 3 sliders(3) + blank(1) = 6.
const queryPanelHeight = 6

// headerHeight is the vertical space for the panel header.
// Derived from: title(1) + separator(1) = 2.
const headerHeight = 2

// statusLineHeight is the vertical space for the bottom status bar.
// Derived from: status(1) = 1.
const statusLineHeight = 1

// maxResults bounds the number of results stored to prevent unbounded growth.
// Derived from: a practical upper bound for interactive browsing.
const maxResults = 200

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Model is the knowledge query panel. It composes three sub-views: query
// builder, results list, and document viewer. It implements component.Focusable
// and component.Resizable.
type Model struct {
	// Sub-view state.
	mode ViewMode

	// Sub-components.
	queryBuilder *QueryBuilder
	docViewer    *DocumentViewer

	// Results state.
	results        []ResultEntry
	selectedResult int
	resultScroll   int

	// Query input state (runes for the query text).
	queryInput []rune
	queryCursor int

	// Dimensions.
	width   int
	height  int
	focused bool
	theme   *theme.Theme
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a knowledge panel Model with the given theme.
func New(th *theme.Theme) *Model {
	return &Model{
		mode:         QueryMode,
		queryBuilder: NewQueryBuilder(th),
		docViewer:    NewDocumentViewer(th),
		results:      make([]ResultEntry, 0, maxResults),
		theme:        th,
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetResults replaces the current results, capped to maxResults.
func (m *Model) SetResults(entries []ResultEntry) {
	count := min(len(entries), maxResults)
	m.results = make([]ResultEntry, count)
	copy(m.results, entries[:count])
	m.selectedResult = 0
	m.resultScroll = 0
}

// ResultCount returns the number of active results. Exposed for
// the UI-05 dispatch stitch so callers can assert the panel was
// populated without reaching into the private slice.
func (m *Model) ResultCount() int {
	return len(m.results)
}

// SetDocumentContent loads content into the document viewer and switches
// to DocumentMode.
func (m *Model) SetDocumentContent(content, filePath string) {
	m.docViewer.SetContent(content, filePath)
	m.mode = DocumentMode
}

// QueryText returns the current query text.
func (m *Model) QueryText() string {
	return string(m.queryInput)
}

// Mode returns the current view mode.
func (m *Model) Mode() ViewMode {
	return m.mode
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes incoming messages.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	key, ok := incoming.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.handleKey(key)
}

// View renders the knowledge panel with its current sub-view.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	var b strings.Builder

	// Header.
	b.WriteString(m.renderHeader())
	b.WriteByte('\n')

	// Body: dispatch to active sub-view.
	bodyHeight := m.bodyHeight()
	bodyRenderer, ok := viewRenderers[m.mode]
	if !ok {
		bodyRenderer = renderQueryView
	}
	b.WriteString(bodyRenderer(m, bodyHeight))

	// Status bar.
	b.WriteByte('\n')
	b.WriteString(m.renderStatusBar())

	// Wrap in border.
	innerW := max(m.width-2, 0)
	if m.focused && m.theme.ActiveBorderRender != nil {
		return m.theme.ActiveBorderRender(b.String(), innerW, m.height, 0)
	}
	borderStyle := m.theme.InactiveBorder
	if m.focused {
		borderStyle = m.theme.ActiveBorder
	}
	return borderStyle.
		Width(innerW). // -2 for border chars
		Height(m.height).
		Render(b.String())
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusKnowledge }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	m.docViewer.SetSize(m.contentWidth(), m.documentViewerHeight())
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven, per mode)
// ---------------------------------------------------------------------------

type keyAction func(m *Model) tea.Cmd

type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

// globalKeys apply in all modes.
var globalKeys = []keyEntry{
	{matchKey("esc"), actionEscapeBack},
}

// queryModeKeys apply in QueryMode.
var queryModeKeys = []keyEntry{
	{matchKey("enter"), actionSubmitQuery},
	{matchKey("tab"), actionNextPreset},
	{matchKey("shift+tab"), actionPrevPreset},
	{matchKey("backspace"), actionQueryBackspace},
	{matchKey("ctrl+u"), actionClearQueryText},
}

// resultsModeKeys apply in ResultsMode.
var resultsModeKeys = []keyEntry{
	{matchKey("j"), actionResultDown},
	{matchKey("down"), actionResultDown},
	{matchKey("k"), actionResultUp},
	{matchKey("up"), actionResultUp},
	{matchKey("enter"), actionOpenDocument},
	{matchKey("g"), actionResultsTop},
	{matchKey("G"), actionResultsBottom},
	{matchKey("pgdown"), actionResultsPageDown},
	{matchKey("pgup"), actionResultsPageUp},
	{matchKey("/"), actionBackToQuery},
}

// documentModeKeys apply in DocumentMode.
var documentModeKeys = []keyEntry{
	{matchKey("j"), actionDocScrollDown},
	{matchKey("down"), actionDocScrollDown},
	{matchKey("k"), actionDocScrollUp},
	{matchKey("up"), actionDocScrollUp},
	{matchKey("g"), actionDocTop},
	{matchKey("G"), actionDocBottom},
	{matchKey("pgdown"), actionDocPageDown},
	{matchKey("pgup"), actionDocPageUp},
}

// modeKeyTable maps modes to their key tables.
var modeKeyTable = map[ViewMode][]keyEntry{
	QueryMode:    queryModeKeys,
	ResultsMode:  resultsModeKeys,
	DocumentMode: documentModeKeys,
}

func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	// Global keys first.
	if cmd, handled := dispatchKeyTable(m, globalKeys, k); handled {
		return m, cmd
	}

	// Mode-specific keys.
	if table, ok := modeKeyTable[m.mode]; ok {
		if cmd, handled := dispatchKeyTable(m, table, k); handled {
			return m, cmd
		}
	}

	// Fall through: insert rune in query mode.
	if m.mode == QueryMode && k.Type == tea.KeyRunes {
		m.queryInput = append(m.queryInput, []rune(k.String())...)
		m.queryCursor = len(m.queryInput)
		m.queryBuilder.SetQueryText(string(m.queryInput))
	}

	return m, nil
}

func dispatchKeyTable(m *Model, table []keyEntry, k tea.KeyMsg) (tea.Cmd, bool) {
	for _, e := range table {
		if e.match(k) {
			return e.action(m), true
		}
	}
	return nil, false
}

func matchKey(desc string) func(tea.KeyMsg) bool {
	return func(k tea.KeyMsg) bool { return k.String() == desc }
}

// ---------------------------------------------------------------------------
// Key actions: global
// ---------------------------------------------------------------------------

func actionEscapeBack(m *Model) tea.Cmd {
	switch m.mode {
	case DocumentMode:
		m.mode = ResultsMode
	case ResultsMode:
		m.mode = QueryMode
	}
	return nil
}

// ---------------------------------------------------------------------------
// Key actions: QueryMode
// ---------------------------------------------------------------------------

func actionSubmitQuery(m *Model) tea.Cmd {
	if len(m.queryInput) == 0 {
		return nil
	}
	// Transition to results mode. The actual search is driven by the
	// parent application wiring; here we signal readiness.
	m.mode = ResultsMode
	return nil
}

func actionNextPreset(m *Model) tea.Cmd {
	m.queryBuilder.NextPreset()
	return nil
}

func actionPrevPreset(m *Model) tea.Cmd {
	m.queryBuilder.PrevPreset()
	return nil
}

func actionQueryBackspace(m *Model) tea.Cmd {
	if len(m.queryInput) == 0 {
		return nil
	}
	m.queryInput = m.queryInput[:len(m.queryInput)-1]
	m.queryCursor = len(m.queryInput)
	m.queryBuilder.SetQueryText(string(m.queryInput))
	return nil
}

func actionClearQueryText(m *Model) tea.Cmd {
	m.queryInput = nil
	m.queryCursor = 0
	m.queryBuilder.SetQueryText("")
	return nil
}

// ---------------------------------------------------------------------------
// Key actions: ResultsMode
// ---------------------------------------------------------------------------

func actionResultDown(m *Model) tea.Cmd {
	if m.selectedResult < len(m.results)-1 {
		m.selectedResult++
		m.scrollResultIntoView()
	}
	return nil
}

func actionResultUp(m *Model) tea.Cmd {
	if m.selectedResult > 0 {
		m.selectedResult--
		m.scrollResultIntoView()
	}
	return nil
}

func actionOpenDocument(m *Model) tea.Cmd {
	if m.selectedResult < 0 || m.selectedResult >= len(m.results) {
		return nil
	}
	// Transition to document mode. The parent wiring should load the
	// document content based on the selected result.
	m.mode = DocumentMode
	return nil
}

func actionResultsTop(m *Model) tea.Cmd {
	m.selectedResult = 0
	m.resultScroll = 0
	return nil
}

func actionResultsBottom(m *Model) tea.Cmd {
	m.selectedResult = max(len(m.results)-1, 0)
	m.scrollResultIntoView()
	return nil
}

func actionResultsPageDown(m *Model) tea.Cmd {
	pageSize := m.resultsViewHeight()
	m.selectedResult = min(m.selectedResult+pageSize, max(len(m.results)-1, 0))
	m.scrollResultIntoView()
	return nil
}

func actionResultsPageUp(m *Model) tea.Cmd {
	pageSize := m.resultsViewHeight()
	m.selectedResult = max(m.selectedResult-pageSize, 0)
	m.scrollResultIntoView()
	return nil
}

func actionBackToQuery(m *Model) tea.Cmd {
	m.mode = QueryMode
	return nil
}

// ---------------------------------------------------------------------------
// Key actions: DocumentMode
// ---------------------------------------------------------------------------

func actionDocScrollDown(m *Model) tea.Cmd {
	m.docViewer.ScrollDown()
	return nil
}

func actionDocScrollUp(m *Model) tea.Cmd {
	m.docViewer.ScrollUp()
	return nil
}

func actionDocTop(m *Model) tea.Cmd {
	m.docViewer.ToTop()
	return nil
}

func actionDocBottom(m *Model) tea.Cmd {
	m.docViewer.ToBottom()
	return nil
}

func actionDocPageDown(m *Model) tea.Cmd {
	m.docViewer.PageDown()
	return nil
}

func actionDocPageUp(m *Model) tea.Cmd {
	m.docViewer.PageUp()
	return nil
}

// ---------------------------------------------------------------------------
// Scroll management
// ---------------------------------------------------------------------------

func (m *Model) scrollResultIntoView() {
	vh := m.resultsViewHeight()
	if m.selectedResult < m.resultScroll {
		m.resultScroll = m.selectedResult
	}
	if m.selectedResult >= m.resultScroll+vh {
		m.resultScroll = m.selectedResult - vh + 1
	}
}

func (m *Model) resultsViewHeight() int {
	h := m.bodyHeight()
	if h < 1 {
		return 1
	}
	return h
}

// ---------------------------------------------------------------------------
// Rendering: header and status bar
// ---------------------------------------------------------------------------

func (m *Model) renderHeader() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Primary).
		Bold(true)
	modeStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Info)
	sepStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Border)

	title := titleStyle.Render(theme.IconSearch + " Knowledge")
	modeLabel := modeStyle.Render(" [" + m.mode.String() + "]")
	separator := sepStyle.Render(strings.Repeat("─", max(m.contentWidth(), 0)))

	return title + modeLabel + "\n" + separator
}

func (m *Model) renderStatusBar() string {
	barStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Muted)

	statusParts := statusBarByMode[m.mode]
	return barStyle.Render(statusParts)
}

// statusBarByMode provides context-sensitive help text for each mode.
var statusBarByMode = map[ViewMode]string{
	QueryMode:    "Enter: search | Tab/Shift+Tab: preset | Esc: back",
	ResultsMode:  "j/k: navigate | Enter: view | /: query | Esc: back",
	DocumentMode: "j/k: scroll | g/G: top/bottom | Esc: results",
}

// ---------------------------------------------------------------------------
// Rendering: sub-view dispatch (table-driven)
// ---------------------------------------------------------------------------

type viewRenderer func(m *Model, bodyHeight int) string

var viewRenderers = map[ViewMode]viewRenderer{
	QueryMode:    renderQueryView,
	ResultsMode:  renderResultsView,
	DocumentMode: renderDocumentView,
}

func renderQueryView(m *Model, _ int) string {
	return m.queryBuilder.View(m.contentWidth())
}

func renderResultsView(m *Model, bodyHeight int) string {
	return RenderResults(
		m.results,
		m.theme,
		m.contentWidth(),
		bodyHeight,
		m.resultScroll,
		m.selectedResult,
	)
}

func renderDocumentView(m *Model, bodyHeight int) string {
	m.docViewer.SetSize(m.contentWidth(), bodyHeight)
	return m.docViewer.View()
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// contentWidth returns the usable width inside the border.
// Border consumes 2 characters total (1 per side).
const borderWidth = 2

func (m *Model) contentWidth() int {
	w := m.width - borderWidth
	if w < 1 {
		return 1
	}
	return w
}

// bodyHeight returns the height available for the sub-view body.
func (m *Model) bodyHeight() int {
	h := m.height - headerHeight - statusLineHeight
	if h < 1 {
		return 1
	}
	return h
}

// documentViewerHeight returns the height available for the document viewer.
func (m *Model) documentViewerHeight() int {
	return m.bodyHeight()
}
