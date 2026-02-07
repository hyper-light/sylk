package fieldmanual

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Layout constants (derived, not magic)
// ---------------------------------------------------------------------------

// overlayBorderSize is the horizontal/vertical space consumed by the border:
// 1 char per side = 2.
const overlayBorderSize = 2

// overlayPaddingSize is the horizontal/vertical space consumed by inner
// padding: Padding(1) = 1 char per side = 2.
const overlayPaddingSize = 2

// overlayFrameSize is the total non-content space per axis:
// border + padding = 4.
const overlayFrameSize = overlayBorderSize + overlayPaddingSize

// marginLines is the minimum vertical margin between the overlay edge and
// the terminal boundary. Derived from: requirement specifies 1-space margin
// top and bottom.
const marginLines = 1

// sectionGap is the number of blank lines between sections.
// Derived from: visual separation between logical groups.
const sectionGap = 1

// headerLines is the vertical space consumed by the fixed title line.
// Derived from: title(1).
const headerLines = 1

// searchAreaLines is the vertical space consumed by the search area between
// title and body: title-divider(1) + search-bar(1) + body-divider(1) = 3.
const searchAreaLines = 3

// bottomToolbarLines is the vertical space consumed by the bottom toolbar
// when search is active: divider(1) + badge row(1) + summary row(1) = 3.
const bottomToolbarLines = 3

// contentIndent is the left padding applied to all content lines within the
// overlay (section headers, search bar, dividers, toolbar).
// Derived from: 2 spaces visual indent matching section headers.
const contentIndent = 2

// contentIndentStr is the string form of contentIndent.
var contentIndentStr = strings.Repeat(" ", contentIndent)

// keyColumnWidth is derived from the maximum visible key width across all
// bindings, ensuring consistent column alignment.
var keyColumnWidth = deriveKeyColumnWidth()

func deriveKeyColumnWidth() int {
	maxW := 0
	for _, sec := range allSections() {
		for _, b := range sec.Bindings {
			if w := lipgloss.Width(b.Key); w > maxW {
				maxW = w
			}
		}
	}
	return maxW + 1
}

// ---------------------------------------------------------------------------
// Search types
// ---------------------------------------------------------------------------

// searchFocusZone identifies which input zone has focus during search.
type searchFocusZone int

const (
	focusSearchQuery   searchFocusZone = iota // Query text input.
	focusSearchToggles                        // Toggle badges [Aa] [ab] [.*].
)

// toggleIdx identifies a specific toggle badge within the search toolbar.
type toggleIdx int

const (
	toggleCaseSensitive toggleIdx = iota // [Aa] case sensitivity.
	toggleWholeWord                      // [ab] whole word.
	toggleUseRegex                       // [.*] regex mode.
)

// searchToggleCount is the number of toggle badges.
// Derived from: the three toggleIdx values above.
const searchToggleCount = 3

// searchBadgeLabels maps each toggle index to its display string.
// Derived from: the three toggle indicators.
var searchBadgeLabels = [searchToggleCount]string{"Aa", "ab", ".*"}

// matchCfg holds the compiled match configuration derived from toggle state.
type matchCfg struct {
	caseSensitive bool
	wholeWord     bool
	useRegex      bool
	compiled      *regexp.Regexp
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// Model is the Field Manual help overlay. It implements component.Component,
// component.Focusable, and component.Resizable.
type Model struct {
	scrollOffset int
	totalLines   int
	lines        []string

	width   int
	height  int
	focused bool
	visible bool
	theme   *theme.Theme

	// Search state.
	searching    bool
	searchQuery  []rune
	searchFocus  searchFocusZone
	toggleActive [searchToggleCount]bool
	toggleCursor toggleIdx
	matchCount   int
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
	_ component.Component = (*Model)(nil)
)

// New creates a Field Manual overlay Model with the given theme.
func New(th *theme.Theme) *Model {
	m := &Model{theme: th}
	return m
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Show opens the Field Manual overlay and resets scroll to the top.
func (m *Model) Show() {
	m.visible = true
	m.scrollOffset = 0
	m.resetSearch()
	m.rebuildContent()
}

// Hide closes the Field Manual overlay.
func (m *Model) Hide() {
	m.visible = false
	m.resetSearch()
}

// Visible reports whether the overlay is currently shown.
func (m *Model) Visible() bool {
	return m.visible
}

// ScrollUp moves the viewport up by one line (for mouse wheel).
func (m *Model) ScrollUp() {
	if m.scrollOffset > 0 {
		m.scrollOffset--
	}
}

// ScrollDown moves the viewport down by one line (for mouse wheel).
func (m *Model) ScrollDown() {
	if m.scrollOffset < m.maxScroll() {
		m.scrollOffset++
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes incoming messages. When visible, the overlay captures all
// key input.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	if !m.visible {
		return m, nil
	}
	key, ok := incoming.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.handleKey(key)
}

// View renders the Field Manual as a full-screen overlay with margin.
func (m *Model) View() string {
	if !m.visible {
		return ""
	}
	return m.renderOverlay()
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusFieldManual }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the available dimensions and rebuilds pre-rendered content.
func (m *Model) SetSize(width, height int) {
	m.width = max(width, 0)
	m.height = max(height, 0)
	if m.visible {
		m.rebuildContent()
	}
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven)
// ---------------------------------------------------------------------------

type keyAction func(m *Model) tea.Cmd

type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

var manualKeys = []keyEntry{
	{matchKey("esc"), actionClose},
	{matchKey("q"), actionClose},
	{matchKey("alt+h"), actionClose},
	{matchKey("down"), actionScrollDown},
	{matchKey("j"), actionScrollDown},
	{matchKey("up"), actionScrollUp},
	{matchKey("k"), actionScrollUp},
	{matchKey("pgdown"), actionPageDown},
	{matchKey("pgup"), actionPageUp},
	{matchKey("home"), actionScrollTop},
	{matchKey("g"), actionScrollTop},
	{matchKey("end"), actionScrollBottom},
	{matchKey("G"), actionScrollBottom},
}

func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	if m.searching {
		return m.handleSearchKey(k)
	}
	ks := k.String()
	if ks == "/" || ks == "alt+f" {
		m.enterSearch()
		return m, nil
	}
	for _, e := range manualKeys {
		if e.match(k) {
			return m, e.action(m)
		}
	}
	return m, nil
}

func matchKey(desc string) func(tea.KeyMsg) bool {
	return func(k tea.KeyMsg) bool { return k.String() == desc }
}

// ---------------------------------------------------------------------------
// Key actions
// ---------------------------------------------------------------------------

func actionClose(m *Model) tea.Cmd {
	m.Hide()
	return nil
}

func actionScrollDown(m *Model) tea.Cmd {
	if m.scrollOffset < m.maxScroll() {
		m.scrollOffset++
	}
	return nil
}

func actionScrollUp(m *Model) tea.Cmd {
	if m.scrollOffset > 0 {
		m.scrollOffset--
	}
	return nil
}

func actionPageDown(m *Model) tea.Cmd {
	m.scrollOffset = min(m.scrollOffset+m.viewportHeight(), m.maxScroll())
	return nil
}

func actionPageUp(m *Model) tea.Cmd {
	m.scrollOffset = max(m.scrollOffset-m.viewportHeight(), 0)
	return nil
}

func actionScrollTop(m *Model) tea.Cmd {
	m.scrollOffset = 0
	return nil
}

func actionScrollBottom(m *Model) tea.Cmd {
	m.scrollOffset = m.maxScroll()
	return nil
}

// ---------------------------------------------------------------------------
// Scroll helpers
// ---------------------------------------------------------------------------

func (m *Model) viewportHeight() int {
	fixed := headerLines + searchAreaLines
	if m.searching {
		fixed += bottomToolbarLines
	}
	return max(m.height-marginLines*2-overlayFrameSize-fixed, 1)
}

func (m *Model) maxScroll() int {
	return max(m.totalLines-m.viewportHeight(), 0)
}

func (m *Model) canScrollUp() bool {
	return m.scrollOffset > 0
}

func (m *Model) canScrollDown() bool {
	return m.scrollOffset < m.maxScroll()
}

// ---------------------------------------------------------------------------
// Content building
// ---------------------------------------------------------------------------

func (m *Model) rebuildContent() {
	contentW := m.contentWidth()
	if contentW <= 0 {
		m.lines, m.totalLines, m.matchCount = nil, 0, 0
		return
	}
	sections := m.activeSections()
	m.lines, m.totalLines = m.buildLines(sections, contentW)
}

// activeSections returns the sections to display, filtered by the current
// search query when search is active.
func (m *Model) activeSections() []Section {
	sections := allSections()
	if m.searching && len(m.searchQuery) > 0 {
		sections, m.matchCount = m.filterSections(sections)
		return sections
	}
	m.matchCount = 0
	return sections
}

// buildLines renders sections into pre-formatted display lines.
func (m *Model) buildLines(sections []Section, contentW int) ([]string, int) {
	lines := m.assembleSectionLines(sections, contentW)
	return lines, len(lines)
}

// assembleSectionLines iterates sections, inserting gaps and rendering each.
func (m *Model) assembleSectionLines(sections []Section, contentW int) []string {
	var lines []string
	for i, sec := range sections {
		if i > 0 {
			lines = appendGap(lines)
		}
		lines = append(lines, m.renderSectionLines(sec, contentW)...)
	}
	return lines
}

// renderSectionLines renders a single section: header + separator + bindings.
func (m *Model) renderSectionLines(sec Section, contentW int) []string {
	lines := []string{
		m.renderSectionHeader(sec.Title),
		m.renderSeparator(contentW),
	}
	for _, b := range sec.Bindings {
		lines = append(lines, m.renderBinding(b, contentW)...)
	}
	return lines
}

// appendGap appends sectionGap blank lines to lines.
func appendGap(lines []string) []string {
	for range sectionGap {
		lines = append(lines, "")
	}
	return lines
}

func (m *Model) contentWidth() int {
	return max(m.width-overlayFrameSize-marginLines*2, 1)
}

// ---------------------------------------------------------------------------
// Line renderers
// ---------------------------------------------------------------------------

func (m *Model) renderSectionHeader(title string) string {
	style := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Primary).
		Bold(true)
	return contentIndentStr + style.Render(title)
}

// separatorInset is subtracted from contentWidth to produce the separator
// width. Derived from: contentIndent(2) left + 2 right visual margin = 4.
const separatorInset = 4

func (m *Model) renderSeparator(width int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Subtle)
	sepW := max(width-separatorInset, 1)
	return contentIndentStr + style.Render(strings.Repeat("─", sepW))
}

// bindingIndent is the left margin before each key column.
// Derived from: 2 spaces visual indent.
const bindingIndent = 2

// bindingGap is the space between the key column and the description.
// Derived from: 2 spaces visual separation.
const bindingGap = 2

// descColumn returns the column at which descriptions begin.
// Derived from: bindingIndent + keyColumnWidth + bindingGap.
func descColumn() int {
	return bindingIndent + keyColumnWidth + bindingGap
}

// renderBinding renders a single binding with the key on the first line and
// the description (and optional prerequisites) word-wrapped to align at the
// description column.
func (m *Model) renderBinding(b Binding, contentW int) []string {
	descCol := descColumn()
	descAvail := max(contentW-descCol, 1)

	lines := m.renderKeyAndDesc(b.Key, b.Description, descAvail, descCol)
	lines = append(lines, m.renderPrereqLines(b.Prerequisites, descAvail, descCol)...)
	return lines
}

// renderKeyAndDesc renders the key + first description chunk on line 1, with
// continuation description lines indented to descCol.
func (m *Model) renderKeyAndDesc(key, desc string, descAvail, descCol int) []string {
	keyStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Warning).
		Bold(true)
	descStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Foreground)

	keyW := lipgloss.Width(key)
	keyPad := strings.Repeat(" ", max(keyColumnWidth-keyW, 0))
	prefix := strings.Repeat(" ", bindingIndent)
	gap := strings.Repeat(" ", bindingGap)
	contPrefix := strings.Repeat(" ", descCol)

	wrapped := wrapText(desc, descAvail)
	first := firstOr(wrapped, "")
	lines := []string{prefix + keyStyle.Render(key+keyPad) + gap + descStyle.Render(first)}
	for _, dl := range tailOf(wrapped) {
		lines = append(lines, contPrefix+descStyle.Render(dl))
	}
	return lines
}

// renderPrereqLines renders word-wrapped prerequisite text aligned at descCol.
func (m *Model) renderPrereqLines(prereq string, descAvail, descCol int) []string {
	if prereq == "" {
		return nil
	}
	prereqStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Muted).
		Italic(true)
	contPrefix := strings.Repeat(" ", descCol)

	var lines []string
	for _, pl := range wrapText("["+prereq+"]", descAvail) {
		lines = append(lines, contPrefix+prereqStyle.Render(pl))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Overlay rendering
// ---------------------------------------------------------------------------

func (m *Model) renderOverlay() string {
	contentW := m.contentWidth()
	vpH := m.viewportHeight()

	header := m.renderTitleLine(contentW)
	searchLine := m.activeSearchLine(contentW)
	divider := m.renderDashDivider(contentW)
	body := m.renderBodyBlock(vpH, contentW)
	content := m.composeContent(header, divider, searchLine, body, contentW)

	return m.renderBox(content, contentW)
}

// activeSearchLine returns the search hint or active search input line.
func (m *Model) activeSearchLine(contentW int) string {
	if m.searching {
		return m.renderSearchInput(contentW)
	}
	return m.renderSearchHint(contentW)
}

// renderBodyBlock slices visible lines from pre-rendered content and pads
// to fill the viewport height.
func (m *Model) renderBodyBlock(vpH, contentW int) string {
	end := min(m.scrollOffset+vpH, m.totalLines)
	start := min(m.scrollOffset, end)
	visible := m.lines[start:end]
	body := strings.Join(visible, "\n")

	remaining := vpH - (end - start)
	if remaining > 0 {
		emptyLine := strings.Repeat(" ", contentW)
		body += strings.Repeat("\n"+emptyLine, remaining)
	}
	return body
}

// composeContent assembles the full overlay content string from its parts.
// Layout: header + divider + search-line + divider + body [+ toolbar].
func (m *Model) composeContent(header, divider, searchLine, body string, contentW int) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(divider)
	b.WriteByte('\n')
	b.WriteString(searchLine)
	b.WriteByte('\n')
	b.WriteString(divider)
	b.WriteByte('\n')
	b.WriteString(body)

	if m.searching {
		b.WriteByte('\n')
		b.WriteString(m.renderDashDivider(contentW))
		b.WriteByte('\n')
		b.WriteString(m.renderSearchToolbar(contentW))
		b.WriteByte('\n')
		b.WriteString(m.renderMatchSummary(contentW))
	}
	return b.String()
}

// renderBox wraps content in a centered, bordered overlay box.
func (m *Model) renderBox(content string, contentW int) string {
	outerH := m.height - marginLines*2
	widthParam := max(contentW+overlayPaddingSize, 1)
	heightParam := max(outerH-overlayBorderSize-overlayPaddingSize, 1)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Palette.BorderActive).
		Padding(1).
		Width(widthParam).
		Height(heightParam)

	box := boxStyle.Render(content)

	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	hPad := max((m.width-boxWidth)/2, 0)
	vPad := max((m.height-boxHeight)/2, 0)

	padded := indentLines(box, hPad)
	return strings.Repeat("\n", vPad) + padded
}

// rightMargin is the space between the rightmost content and the overlay edge.
// Derived from: separatorInset(4) - contentIndent(2) = 2, matching dividers.
const rightMargin = separatorInset - contentIndent

func (m *Model) renderTitleLine(contentW int) string {
	titleStyle := lipgloss.NewStyle().
		Foreground(m.theme.Palette.Secondary).
		Bold(true)
	title := contentIndentStr + titleStyle.Render("Field Manual")

	indicator := m.renderScrollIndicator()
	if indicator == "" {
		return title
	}

	titleW := lipgloss.Width(title)
	indicatorW := lipgloss.Width(indicator)
	gap := max(contentW-rightMargin-titleW-indicatorW, 1)
	return title + strings.Repeat(" ", gap) + indicator
}

func (m *Model) renderScrollIndicator() string {
	ms := m.maxScroll()
	if ms <= 0 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	arrows := ""
	if m.canScrollUp() {
		arrows += theme.IconArrowUp
	}
	if m.canScrollDown() {
		arrows += theme.IconArrowDown
	}

	pct := (m.scrollOffset * 100) / ms
	return style.Render(fmt.Sprintf("%s [%d%%]", arrows, pct))
}

// ---------------------------------------------------------------------------
// Search mode
// ---------------------------------------------------------------------------

func (m *Model) enterSearch() {
	m.searching = true
	m.searchQuery = nil
	m.searchFocus = focusSearchQuery
	m.toggleActive = [searchToggleCount]bool{}
	m.toggleCursor = toggleCaseSensitive
	m.matchCount = 0
	m.scrollOffset = 0
	m.rebuildContent()
}

func (m *Model) exitSearch() {
	m.resetSearch()
	m.scrollOffset = 0
	m.rebuildContent()
}

func (m *Model) resetSearch() {
	m.searching = false
	m.searchQuery = nil
	m.searchFocus = focusSearchQuery
	m.toggleActive = [searchToggleCount]bool{}
	m.toggleCursor = toggleCaseSensitive
	m.matchCount = 0
}

// ---------------------------------------------------------------------------
// Search key dispatch (table-driven)
// ---------------------------------------------------------------------------

func (m *Model) handleSearchKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	switch m.searchFocus {
	case focusSearchQuery:
		return m.handleQueryKey(k)
	default:
		return m.handleToggleKey(k)
	}
}

var queryKeys = []keyEntry{
	{matchKey("esc"), func(m *Model) tea.Cmd { return m.queryEsc() }},
	{matchKey("alt+f"), func(m *Model) tea.Cmd { m.exitSearch(); return nil }},
	{matchKey("backspace"), func(m *Model) tea.Cmd { return m.queryBackspace() }},
	{matchKey("tab"), func(m *Model) tea.Cmd {
		m.searchFocus = focusSearchToggles
		m.toggleCursor = toggleCaseSensitive
		return nil
	}},
	{matchKey("shift+tab"), func(m *Model) tea.Cmd {
		m.searchFocus = focusSearchToggles
		m.toggleCursor = toggleIdx(searchToggleCount - 1)
		return nil
	}},
	{matchKey("up"), actionScrollUp},
	{matchKey("down"), actionScrollDown},
	{matchKey("pgup"), actionPageUp},
	{matchKey("pgdown"), actionPageDown},
	{matchKey("home"), actionScrollTop},
	{matchKey("end"), actionScrollBottom},
}

func (m *Model) handleQueryKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	for _, e := range queryKeys {
		if e.match(k) {
			return m, e.action(m)
		}
	}
	return m, m.queryType(k)
}

func (m *Model) queryEsc() tea.Cmd {
	if len(m.searchQuery) > 0 {
		m.searchQuery = nil
		m.scrollOffset = 0
		m.rebuildContent()
		return nil
	}
	m.exitSearch()
	return nil
}

func (m *Model) queryBackspace() tea.Cmd {
	if len(m.searchQuery) > 0 {
		m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
		m.scrollOffset = 0
		m.rebuildContent()
		return nil
	}
	m.exitSearch()
	return nil
}

func (m *Model) queryType(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyRunes:
		m.searchQuery = append(m.searchQuery, k.Runes...)
	case tea.KeySpace:
		m.searchQuery = append(m.searchQuery, ' ')
	default:
		return nil
	}
	m.scrollOffset = 0
	m.rebuildContent()
	return nil
}

var toggleKeys = []keyEntry{
	{matchKey("esc"), func(m *Model) tea.Cmd { m.searchFocus = focusSearchQuery; return nil }},
	{matchKey("tab"), func(m *Model) tea.Cmd { return m.advanceToggle() }},
	{matchKey("shift+tab"), func(m *Model) tea.Cmd { return m.retreatToggle() }},
	{matchKey("left"), func(m *Model) tea.Cmd { m.moveToggleCursor(-1); return nil }},
	{matchKey("h"), func(m *Model) tea.Cmd { m.moveToggleCursor(-1); return nil }},
	{matchKey("right"), func(m *Model) tea.Cmd { m.moveToggleCursor(1); return nil }},
	{matchKey("l"), func(m *Model) tea.Cmd { m.moveToggleCursor(1); return nil }},
	{matchKey("enter"), func(m *Model) tea.Cmd { return m.flipToggle() }},
	{matchKey(" "), func(m *Model) tea.Cmd { return m.flipToggle() }},
}

func (m *Model) handleToggleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	for _, e := range toggleKeys {
		if e.match(k) {
			return m, e.action(m)
		}
	}
	return m, nil
}

func (m *Model) moveToggleCursor(delta int) {
	next := int(m.toggleCursor) + delta
	if next >= 0 && next < searchToggleCount {
		m.toggleCursor = toggleIdx(next)
	}
}

// advanceToggle moves to the next toggle; wraps to query when past the last.
func (m *Model) advanceToggle() tea.Cmd {
	if m.toggleCursor < toggleIdx(searchToggleCount-1) {
		m.toggleCursor++
		return nil
	}
	m.searchFocus = focusSearchQuery
	return nil
}

// retreatToggle moves to the previous toggle; wraps to query when before first.
func (m *Model) retreatToggle() tea.Cmd {
	if m.toggleCursor > 0 {
		m.toggleCursor--
		return nil
	}
	m.searchFocus = focusSearchQuery
	return nil
}

func (m *Model) flipToggle() tea.Cmd {
	m.toggleActive[m.toggleCursor] = !m.toggleActive[m.toggleCursor]
	m.scrollOffset = 0
	m.rebuildContent()
	return nil
}

// ---------------------------------------------------------------------------
// Section filtering
// ---------------------------------------------------------------------------

func (m *Model) filterSections(sections []Section) ([]Section, int) {
	query := string(m.searchQuery)
	cfg := m.buildMatchCfg(query)

	var filtered []Section
	total := 0
	for _, sec := range sections {
		matched := filterBindings(sec.Bindings, query, cfg)
		if len(matched) == 0 {
			continue
		}
		total += len(matched)
		filtered = append(filtered, Section{Title: sec.Title, Bindings: matched})
	}
	return filtered, total
}

func filterBindings(bindings []Binding, query string, cfg matchCfg) []Binding {
	var out []Binding
	for _, b := range bindings {
		if bindingMatches(b, query, cfg) {
			out = append(out, b)
		}
	}
	return out
}

func bindingMatches(b Binding, query string, cfg matchCfg) bool {
	for _, text := range bindingTexts(b) {
		if textMatches(text, query, cfg) {
			return true
		}
	}
	return false
}

func bindingTexts(b Binding) []string {
	texts := []string{b.Key, b.Description}
	if b.Prerequisites != "" {
		texts = append(texts, b.Prerequisites)
	}
	return texts
}

func (m *Model) buildMatchCfg(query string) matchCfg {
	cfg := matchCfg{
		caseSensitive: m.toggleActive[toggleCaseSensitive] || hasUpperCase(query),
		wholeWord:     m.toggleActive[toggleWholeWord],
		useRegex:      m.toggleActive[toggleUseRegex],
	}
	if cfg.useRegex {
		cfg.compiled = compileSearchRegex(query, cfg.caseSensitive)
	}
	return cfg
}

func compileSearchRegex(query string, caseSensitive bool) *regexp.Regexp {
	pattern := query
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

func textMatches(text, query string, cfg matchCfg) bool {
	if cfg.useRegex {
		return cfg.compiled != nil && cfg.compiled.MatchString(text)
	}
	return plainTextMatches(text, query, cfg)
}

func plainTextMatches(text, query string, cfg matchCfg) bool {
	if !cfg.caseSensitive {
		text = strings.ToLower(text)
		query = strings.ToLower(query)
	}
	if cfg.wholeWord {
		return containsWholeWord(text, query)
	}
	return strings.Contains(text, query)
}

func containsWholeWord(text, word string) bool {
	pattern := `\b` + regexp.QuoteMeta(word) + `\b`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return strings.Contains(text, word)
	}
	return re.MatchString(text)
}

func hasUpperCase(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Search UI renderers
// ---------------------------------------------------------------------------

func (m *Model) renderSearchHint(contentW int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	text := contentIndentStr + style.Render("/ search")
	return padLine(text, contentW)
}

func (m *Model) renderSearchInput(contentW int) string {
	prefix := contentIndentStr + lipgloss.NewStyle().
		Foreground(m.theme.Palette.Muted).
		Render("/ ")
	prefixW := lipgloss.Width(prefix)

	queryStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	// Reserve 1 column for cursor so layout is stable.
	availableForQuery := max(contentW-prefixW-1, 0)

	queryStr := string(m.searchQuery)
	queryRunes := m.searchQuery
	if len(queryRunes) > availableForQuery {
		queryStr = string(queryRunes[len(queryRunes)-availableForQuery:])
	}

	cursor := m.searchCursorChar()
	line := prefix + queryStyle.Render(queryStr) + cursor
	return padLine(line, contentW)
}

func (m *Model) searchCursorChar() string {
	if m.searchFocus == focusSearchQuery {
		return lipgloss.NewStyle().Reverse(true).Render(" ")
	}
	return " "
}

func (m *Model) renderDashDivider(contentW int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Border)
	sepW := max(contentW-separatorInset, 1)
	return contentIndentStr + style.Render(strings.Repeat("-", sepW))
}

func (m *Model) renderSearchToolbar(contentW int) string {
	var tb strings.Builder
	tb.WriteString(contentIndentStr)
	for i := range searchToggleCount {
		if i > 0 {
			tb.WriteByte(' ')
		}
		tb.WriteString(m.renderBadge(toggleIdx(i)))
	}
	return padLine(tb.String(), contentW)
}

func (m *Model) renderBadge(idx toggleIdx) string {
	return m.badgeStyle(idx).Render("[" + searchBadgeLabels[idx] + "]")
}

func (m *Model) badgeStyle(idx toggleIdx) lipgloss.Style {
	fg := m.badgeForeground(idx)
	style := lipgloss.NewStyle().Foreground(fg)
	if m.isBadgeFocused(idx) {
		style = style.Background(m.theme.Palette.Selection)
	}
	return style
}

func (m *Model) badgeForeground(idx toggleIdx) lipgloss.Color {
	if m.toggleActive[idx] {
		return m.theme.Palette.Primary
	}
	return m.theme.Palette.Muted
}

func (m *Model) isBadgeFocused(idx toggleIdx) bool {
	return m.searchFocus == focusSearchToggles && m.toggleCursor == idx
}

func (m *Model) renderMatchSummary(contentW int) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	var text string
	switch {
	case len(m.searchQuery) == 0:
		text = ""
	case m.matchCount == 0:
		text = contentIndentStr + style.Render("No results")
	default:
		text = contentIndentStr + style.Render(fmt.Sprintf("%d matches", m.matchCount))
	}
	return padLine(text, contentW)
}

// ---------------------------------------------------------------------------
// Text wrapping
// ---------------------------------------------------------------------------

// wrapText word-wraps text to fit within maxWidth columns, breaking at spaces.
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	return wrapWords(words, maxWidth)
}

// wrapWords joins words into lines that fit within maxWidth.
func wrapWords(words []string, maxWidth int) []string {
	var lines []string
	line := words[0]
	lineW := len([]rune(line))
	for _, w := range words[1:] {
		wW := len([]rune(w))
		if lineW+1+wW <= maxWidth {
			line += " " + w
			lineW += 1 + wW
		} else {
			lines = append(lines, line)
			line = w
			lineW = wW
		}
	}
	return append(lines, line)
}

// firstOr returns the first element of s, or fallback if s is empty.
func firstOr(s []string, fallback string) string {
	if len(s) > 0 {
		return s[0]
	}
	return fallback
}

// tailOf returns all elements after the first, or nil if len <= 1.
func tailOf(s []string) []string {
	if len(s) <= 1 {
		return nil
	}
	return s[1:]
}

// padLine right-pads a rendered line to fill the given content width.
func padLine(line string, contentW int) string {
	padCount := max(contentW-lipgloss.Width(line), 0)
	if padCount > 0 {
		return line + strings.Repeat(" ", padCount)
	}
	return line
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// indentLines prepends each line with n spaces.
func indentLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	prefix := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.Grow(len(s) + n*len(lines))
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(prefix)
		b.WriteString(line)
	}
	return b.String()
}
