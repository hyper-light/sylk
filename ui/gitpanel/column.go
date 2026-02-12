package gitpanel

import (
	"sort"
	"time"

	"github.com/adalundhe/sylk/core/search/git"
)

// ColumnID is a stable string identifier for config persistence.
type ColumnID string

// WidthStrategy defines how a column computes its width.
type WidthStrategy int

const (
	// WidthFixed uses a constant character width.
	WidthFixed WidthStrategy = iota
	// WidthMaxContent uses the maximum content width across visible entries.
	WidthMaxContent
	// WidthFlex absorbs remaining horizontal space (exactly one per tab).
	WidthFlex
)

// SortDirection identifies ascending or descending sort.
type SortDirection int

const (
	SortNone SortDirection = iota
	SortAsc
	SortDesc
)

// ColumnDef is the declarative definition of a single column.
type ColumnDef struct {
	ID       ColumnID
	Label    string
	Strategy WidthStrategy
	Fixed    int  // only used when Strategy == WidthFixed
	Hideable       bool // false = always visible
	DefaultHidden  bool // true = not visible until user enables
	DropdownHidden bool // true = excluded from gear dropdown

	// Sort comparators. nil = column is not sortable.
	SortAsc  func(a, b listEntry) bool
	SortDesc func(a, b listEntry) bool

	// Measure returns the display width of this column's content for the
	// given entry. Only used when Strategy == WidthMaxContent.
	Measure func(e listEntry) int
}

// sortIndicatorWidth is the character width of a sort indicator suffix
// (" ↕", " ▲", or " ▼"). The space + arrow is 2 visible columns.
const sortIndicatorWidth = 2

// Absolute minimum column widths during progressive truncation.
const (
	// labelMinChars is the minimum label characters before truncation stops.
	labelMinChars = 1
	// sortToggleMinWidth is the minimum width reserved for the sort toggle
	// indicator. Sort toggles must always be visible for sortable columns.
	sortToggleMinWidth = 4
)

// HeaderMinWidth returns the minimum width needed to display the column
// header label without truncation, including the sort indicator for
// sortable columns.
func (d *ColumnDef) HeaderMinWidth() int {
	w := len(d.Label)
	if d.SortAsc != nil {
		w += sortIndicatorWidth
	}
	return max(w, 1)
}

// colMinWidth returns the absolute minimum width for a column: one label
// character plus the sort toggle width for sortable columns.
func colMinWidth(def *ColumnDef) int {
	if def.SortAsc != nil {
		return labelMinChars + sortToggleMinWidth
	}
	return labelMinChars
}

// ColumnState holds the runtime state for a column within a tab.
type ColumnState struct {
	Def       *ColumnDef
	Visible   bool
	Width     int // computed per render cycle
	UserWidth int // user-set width override via drag resize; 0 = auto
}

// colSepWidth is the width of the separator between columns (" │ ").
const colSepWidth = 3

// hasSepBefore reports whether a column separator should be rendered before
// visible column index i. The separator is suppressed when the preceding
// column has an empty header label (decorative icon/marker columns).
func hasSepBefore(vis []ColumnState, i int) bool {
	return i > 0 && vis[i-1].Def.Label != ""
}

// separatorCount returns the number of inter-column separators that will
// actually be rendered, accounting for suppressed separators after
// empty-label columns.
func separatorCount(vis []ColumnState) int {
	n := 0
	for i := 1; i < len(vis); i++ {
		if vis[i-1].Def.Label != "" {
			n++
		}
	}
	return n
}

// dropdownIndices returns the indices into Columns that should appear in the
// gear dropdown. Columns with empty labels (decorative) or DropdownHidden
// are excluded.
func (cl *ColumnLayout) dropdownIndices() []int {
	indices := make([]int, 0, len(cl.Columns))
	for i, c := range cl.Columns {
		if c.Def.Label != "" && !c.Def.DropdownHidden {
			indices = append(indices, i)
		}
	}
	return indices
}

// gearIconWidth is the width reserved for the gear icon on the header row
// (" │ " divider (3) + "⚙ " icon (2) = 5).
const gearIconWidth = 5

// SortKey represents a single column in a multi-column sort specification.
type SortKey struct {
	Col ColumnID
	Dir SortDirection
}

// ColumnLayout manages the ordered, visibility-toggled column set for one tab.
type ColumnLayout struct {
	Columns  []ColumnState
	SortKeys []SortKey // ordered multi-column sort (primary first)
}

// NewColumnLayout creates a ColumnLayout from a slice of definitions.
// Columns with DefaultHidden start invisible; all others start visible.
func NewColumnLayout(defs []*ColumnDef) ColumnLayout {
	cols := make([]ColumnState, len(defs))
	for i, d := range defs {
		cols[i] = ColumnState{Def: d, Visible: !d.DefaultHidden}
	}
	return ColumnLayout{Columns: cols}
}

// VisibleColumns returns only the visible columns in display order.
func (cl *ColumnLayout) VisibleColumns() []ColumnState {
	var out []ColumnState
	for _, c := range cl.Columns {
		if c.Visible {
			out = append(out, c)
		}
	}
	return out
}

// visToFullIndices returns a mapping from visible column index to the
// corresponding index in the full Columns slice.
func (cl *ColumnLayout) visToFullIndices() []int {
	indices := make([]int, 0, len(cl.Columns))
	for i, c := range cl.Columns {
		if c.Visible {
			indices = append(indices, i)
		}
	}
	return indices
}

// visibleCount returns the number of visible columns.
func (cl *ColumnLayout) visibleCount() int {
	n := 0
	for _, c := range cl.Columns {
		if c.Visible {
			n++
		}
	}
	return n
}

// elasticCol tracks a non-fixed column during width distribution.
type elasticCol struct {
	idx     int
	natural int
	minW    int
	isFlex  bool
}

// ComputeWidths calculates column widths for the current entry set and total
// available width. Fixed columns get their declared width. MaxContent and Flex
// columns measure their natural content width; when there is enough room the
// Flex column absorbs the surplus, otherwise all elastic columns shrink using
// a water-filling algorithm so that columns with longer data truncate before
// columns with shorter data. User-resized columns are honoured up to the
// point where other columns would be pushed below their absolute minimum;
// beyond that the user-resized column is capped.
func (cl *ColumnLayout) ComputeWidths(entries []listEntry, totalWidth int) {
	vis := cl.visibleCount()
	if vis == 0 {
		return
	}

	// Account for inter-column separators and gear icon.
	seps := separatorCount(cl.VisibleColumns())
	budget := totalWidth - seps*colSepWidth - gearIconWidth

	// Phase 1: Assign fixed-width columns (strategy-declared).
	trueFixedTotal := 0
	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible {
			c.Width = 0
			continue
		}
		if c.Def.Strategy == WidthFixed && c.UserWidth == 0 {
			c.Width = c.Def.Fixed
			trueFixedTotal += c.Width
		}
	}

	// Phase 2: Cap user-resized columns so other columns keep at least
	// their absolute minimum width. This prevents resize from pushing
	// any column outside the panel.
	otherMinSum := trueFixedTotal
	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible || c.UserWidth > 0 {
			continue
		}
		if c.Def.Strategy == WidthFixed {
			continue // already counted in trueFixedTotal
		}
		otherMinSum += colMinWidth(c.Def)
	}

	maxUserBudget := max(budget-otherMinSum, 0)
	userTotal := 0
	for i := range cl.Columns {
		c := &cl.Columns[i]
		if c.Visible && c.UserWidth > 0 {
			userTotal += c.UserWidth
		}
	}

	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible || c.UserWidth == 0 {
			continue
		}
		minW := colMinWidth(c.Def)
		if userTotal <= maxUserBudget {
			c.Width = max(c.UserWidth, minW)
		} else if maxUserBudget <= 0 {
			c.Width = minW
		} else {
			// Scale proportionally among user-resized columns.
			scaled := c.UserWidth * maxUserBudget / userTotal
			c.Width = max(scaled, minW)
		}
	}

	// Recompute how much space fixed + user-resized columns consume.
	fixedTotal := 0
	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible {
			continue
		}
		if c.UserWidth > 0 || c.Def.Strategy == WidthFixed {
			fixedTotal += c.Width
		}
	}

	elastic := budget - fixedTotal
	if elastic <= 0 {
		// Give every elastic column its absolute minimum.
		for i := range cl.Columns {
			c := &cl.Columns[i]
			if c.Visible && c.Def.Strategy != WidthFixed && c.UserWidth == 0 {
				c.Width = colMinWidth(c.Def)
			}
		}
		return
	}

	// Phase 3: Measure natural widths for elastic columns.
	var elCols []elasticCol
	naturalSum := 0
	flexPos := -1

	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible || c.Def.Strategy == WidthFixed || c.UserWidth > 0 {
			continue
		}

		headerW := c.Def.HeaderMinWidth()
		minW := colMinWidth(c.Def)
		w := max(headerW, minW)
		if c.Def.Measure != nil {
			for _, e := range entries {
				if ew := c.Def.Measure(e); ew > w {
					w = ew
				}
			}
		}

		isFlex := c.Def.Strategy == WidthFlex
		if isFlex {
			flexPos = len(elCols)
			// Cap Flex natural width so it doesn't dominate the budget.
			halfBudget := elastic / 2
			if halfBudget > 0 && w > halfBudget {
				w = halfBudget
			}
		}
		// Use colMinWidth as the absolute floor — headers truncate
		// progressively down to 1 label char + sort toggle. This must
		// match the floor used in the Phase 2 capping step so that the
		// total never exceeds the budget.
		elCols = append(elCols, elasticCol{
			idx: i, natural: w, minW: minW, isFlex: isFlex,
		})
		naturalSum += w
	}

	if len(elCols) == 0 {
		return
	}

	// Phase 4: Distribute elastic space.
	if naturalSum <= elastic {
		// Plenty of room — MaxContent gets natural, Flex absorbs surplus.
		remainder := elastic
		for _, ec := range elCols {
			if ec.isFlex {
				continue
			}
			cl.Columns[ec.idx].Width = ec.natural
			remainder -= ec.natural
		}
		if flexPos >= 0 {
			cl.Columns[elCols[flexPos].idx].Width = max(remainder, 0)
		}
	} else {
		// Tight — shrink longest columns first (water-filling).
		shrinkLongestFirst(cl, elCols, elastic)
	}

	// Final clamp: absorb any rounding discrepancy into the widest
	// column so the gear icon never jitters by even one pixel.
	cl.clampToTotal(budget)
}

// clampToTotal adjusts the widest visible column so that the sum of all
// visible column widths equals target exactly.
func (cl *ColumnLayout) clampToTotal(target int) {
	total := 0
	widestIdx := -1
	widestW := 0
	for i := range cl.Columns {
		c := &cl.Columns[i]
		if !c.Visible {
			continue
		}
		total += c.Width
		if c.Width > widestW {
			widestW = c.Width
			widestIdx = i
		}
	}
	if diff := target - total; diff != 0 && widestIdx >= 0 {
		cl.Columns[widestIdx].Width = max(cl.Columns[widestIdx].Width+diff, 1)
	}
}

// shrinkLongestFirst assigns elastic space using a water-filling algorithm.
// Columns are processed smallest-natural-first. Each column that fits within
// its fair share keeps its natural width; once a column exceeds the fair
// share, all remaining (wider) columns split the remaining budget equally.
// This ensures Subject (longest) truncates before Author or Time (shorter).
func shrinkLongestFirst(cl *ColumnLayout, cols []elasticCol, budget int) {
	// Sort by natural width ascending — shortest first.
	sort.Slice(cols, func(i, j int) bool { return cols[i].natural < cols[j].natural })

	remaining := budget
	n := len(cols)

	for i, ec := range cols {
		fairShare := remaining / (n - i)
		if ec.natural <= fairShare {
			cl.Columns[ec.idx].Width = max(ec.natural, ec.minW)
			remaining -= cl.Columns[ec.idx].Width
		} else {
			// All remaining columns (including this one) split evenly.
			share := remaining / (n - i)
			for j := i; j < n; j++ {
				w := max(share, cols[j].minW)
				cl.Columns[cols[j].idx].Width = w
				remaining -= w
			}
			// Distribute rounding remainder one pixel each to the widest.
			leftover := budget - sumWidths(cl, cols)
			for j := n - 1; j >= i && leftover > 0; j-- {
				cl.Columns[cols[j].idx].Width++
				leftover--
			}
			return
		}
	}
}

// sumWidths totals the current assigned widths for the given elastic columns.
func sumWidths(cl *ColumnLayout, cols []elasticCol) int {
	total := 0
	for _, ec := range cols {
		total += cl.Columns[ec.idx].Width
	}
	return total
}

// Reorder moves the column at fromIdx to toIdx within the Columns slice.
// Bounds are clamped. No-op if from == to.
func (cl *ColumnLayout) Reorder(fromIdx, toIdx int) {
	n := len(cl.Columns)
	fromIdx = clamp(fromIdx, 0, n-1)
	toIdx = clamp(toIdx, 0, n-1)
	if fromIdx == toIdx {
		return
	}

	col := cl.Columns[fromIdx]
	// Remove from old position.
	cl.Columns = append(cl.Columns[:fromIdx], cl.Columns[fromIdx+1:]...)
	// Insert at new position.
	cl.Columns = append(cl.Columns[:toIdx], append([]ColumnState{col}, cl.Columns[toIdx:]...)...)
}

// ToggleVisibility flips the visible flag for the column with the given ID.
// Non-hideable columns are never toggled off.
func (cl *ColumnLayout) ToggleVisibility(id ColumnID) {
	for i := range cl.Columns {
		if cl.Columns[i].Def.ID != id {
			continue
		}
		if !cl.Columns[i].Def.Hideable {
			return
		}
		cl.Columns[i].Visible = !cl.Columns[i].Visible
		return
	}
}

// lineStatIDs are column IDs that require line-level diff stats (Patch).
var lineStatIDs = map[ColumnID]bool{"changed": true, "added": true, "removed": true}

// fileStatIDs are column IDs that require file-level diff stats (DiffTree).
var fileStatIDs = map[ColumnID]bool{"files": true, "files_added": true, "files_modified": true, "files_deleted": true}

// isStatColumn reports whether a column ID requires diff stat data.
func isStatColumn(id ColumnID) bool { return lineStatIDs[id] || fileStatIDs[id] }

// StatLevel returns the highest DiffStatLevel required by the currently
// visible columns. Returns DiffStatNone when no stat columns are visible.
func (cl *ColumnLayout) StatLevel() git.DiffStatLevel {
	level := git.DiffStatNone
	for _, c := range cl.Columns {
		if !c.Visible {
			continue
		}
		if lineStatIDs[c.Def.ID] {
			return git.DiffStatLines
		}
		if fileStatIDs[c.Def.ID] {
			level = git.DiffStatFiles
		}
	}
	return level
}

// CycleSort toggles sorting on the given column within the multi-column sort.
// If the column is not in SortKeys, it is appended as Asc. If already present,
// it cycles Asc→Desc→removed. No-op if the column has no sort comparators.
func (cl *ColumnLayout) CycleSort(id ColumnID) {
	def := cl.findDef(id)
	if def == nil || def.SortAsc == nil {
		return
	}

	for i, sk := range cl.SortKeys {
		if sk.Col != id {
			continue
		}
		switch sk.Dir {
		case SortAsc:
			cl.SortKeys[i].Dir = SortDesc
		case SortDesc:
			cl.SortKeys = append(cl.SortKeys[:i], cl.SortKeys[i+1:]...)
		default:
			cl.SortKeys[i].Dir = SortAsc
		}
		return
	}

	cl.SortKeys = append(cl.SortKeys, SortKey{Col: id, Dir: SortAsc})
}

// ActiveSortFunc returns a chained comparator over all active sort keys,
// or nil if no sort keys are set.
func (cl *ColumnLayout) ActiveSortFunc() func(a, b listEntry) bool {
	if len(cl.SortKeys) == 0 {
		return nil
	}

	// Build a slice of concrete sort functions for each key.
	type sortFn struct {
		less func(a, b listEntry) bool
	}
	fns := make([]sortFn, 0, len(cl.SortKeys))
	for _, sk := range cl.SortKeys {
		def := cl.findDef(sk.Col)
		if def == nil || def.SortAsc == nil {
			continue
		}
		fn := def.SortAsc
		if sk.Dir == SortDesc && def.SortDesc != nil {
			fn = def.SortDesc
		}
		fns = append(fns, sortFn{less: fn})
	}
	if len(fns) == 0 {
		return nil
	}

	// Single-key fast path.
	if len(fns) == 1 {
		return fns[0].less
	}

	// Multi-key: chain comparators. If primary is equal, fall through to secondary, etc.
	return func(a, b listEntry) bool {
		for _, sf := range fns {
			aLess := sf.less(a, b)
			bLess := sf.less(b, a)
			if aLess {
				return true
			}
			if bLess {
				return false
			}
			// Equal on this key — fall through to next.
		}
		return false
	}
}

// findDef returns the ColumnDef for the given ID, or nil if not found.
func (cl *ColumnLayout) findDef(id ColumnID) *ColumnDef {
	for _, c := range cl.Columns {
		if c.Def.ID == id {
			return c.Def
		}
	}
	return nil
}

// ApplySortToFiltered sorts the filtered indices using the active column sort.
func (cl *ColumnLayout) ApplySortToFiltered(entries []listEntry, filtered []int) {
	sortFn := cl.ActiveSortFunc()
	if sortFn == nil {
		return
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return sortFn(entries[filtered[i]], entries[filtered[j]])
	})
}

// -------------------------------------------------------------------------
// Config round-trip
// -------------------------------------------------------------------------

// ToConfig serializes the current column layout to a config struct.
func (cl *ColumnLayout) ToConfig() TabColumnConfig {
	cfg := TabColumnConfig{}
	for _, c := range cl.Columns {
		cfg.Columns = append(cfg.Columns, ColumnEntry{
			ID:      string(c.Def.ID),
			Visible: c.Visible,
			Width:   c.UserWidth,
		})
	}
	for _, sk := range cl.SortKeys {
		dir := "asc"
		if sk.Dir == SortDesc {
			dir = "desc"
		}
		cfg.Sort = append(cfg.Sort, SortEntry{Col: string(sk.Col), Dir: dir})
	}
	return cfg
}

// ApplyConfig restores column order, visibility, and sort from config.
// Unknown column IDs in the config are ignored. Columns not mentioned
// in the config retain their default position and visibility.
func (cl *ColumnLayout) ApplyConfig(cfg TabColumnConfig) {
	// Build index of existing columns by ID.
	byID := make(map[ColumnID]ColumnState, len(cl.Columns))
	for _, c := range cl.Columns {
		byID[c.Def.ID] = c
	}

	// Rebuild in config order, then append any not mentioned.
	seen := make(map[ColumnID]bool, len(cfg.Columns))
	ordered := make([]ColumnState, 0, len(cl.Columns))

	for _, ce := range cfg.Columns {
		id := ColumnID(ce.ID)
		c, ok := byID[id]
		if !ok {
			continue // unknown ID, skip
		}
		if c.Def.Hideable {
			c.Visible = ce.Visible
		}
		c.UserWidth = ce.Width
		ordered = append(ordered, c)
		seen[id] = true
	}

	// Append columns not in config (preserving their default order).
	for _, c := range cl.Columns {
		if !seen[c.Def.ID] {
			ordered = append(ordered, c)
		}
	}

	cl.Columns = ordered

	// Restore sort keys.
	cl.SortKeys = nil
	if len(cfg.Sort) > 0 {
		for _, se := range cfg.Sort {
			id := ColumnID(se.Col)
			def := cl.findDef(id)
			if def == nil || def.SortAsc == nil {
				continue
			}
			dir := SortAsc
			if se.Dir == "desc" {
				dir = SortDesc
			}
			cl.SortKeys = append(cl.SortKeys, SortKey{Col: id, Dir: dir})
		}
	} else if cfg.SortCol != "" {
		// Backward compatibility: single-column sort from old config.
		id := ColumnID(cfg.SortCol)
		def := cl.findDef(id)
		if def != nil && def.SortAsc != nil {
			dir := SortAsc
			if cfg.SortDir == "desc" {
				dir = SortDesc
			}
			cl.SortKeys = []SortKey{{Col: id, Dir: dir}}
		}
	}
}

// -------------------------------------------------------------------------
// Sort helpers for column definitions
// -------------------------------------------------------------------------

// sortAlphaAsc returns a sort function that compares by a string accessor, ascending.
func sortAlphaAsc(accessor func(listEntry) string) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a) < accessor(b) }
}

// sortAlphaDesc returns a sort function that compares by a string accessor, descending.
func sortAlphaDesc(accessor func(listEntry) string) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a) > accessor(b) }
}

// sortTimeAsc returns a sort function that compares by a time accessor, oldest first.
func sortTimeAsc(accessor func(listEntry) time.Time) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a).Before(accessor(b)) }
}

// sortTimeDesc returns a sort function that compares by a time accessor, newest first.
func sortTimeDesc(accessor func(listEntry) time.Time) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a).After(accessor(b)) }
}

// intWidth returns the display width of a non-negative integer in decimal.
func intWidth(v int) int {
	if v == 0 {
		return 1
	}
	n := 0
	for v > 0 {
		v /= 10
		n++
	}
	return n
}

// sortIntAsc returns a sort function that compares by an int accessor, ascending.
func sortIntAsc(accessor func(listEntry) int) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a) < accessor(b) }
}

// sortIntDesc returns a sort function that compares by an int accessor, descending.
func sortIntDesc(accessor func(listEntry) int) func(a, b listEntry) bool {
	return func(a, b listEntry) bool { return accessor(a) > accessor(b) }
}

// -------------------------------------------------------------------------
// Per-tab column definitions
// -------------------------------------------------------------------------

// commitColumnDefs defines the columns for the Commits tab.
func commitColumnDefs() []*ColumnDef {
	return []*ColumnDef{
		{
			ID: "hash", Label: " Hash", Strategy: WidthFixed, Fixed: 8,
			Hideable: true,
			SortAsc:  sortAlphaAsc(commitHash),
			SortDesc: sortAlphaDesc(commitHash),
		},
		{
			ID: "subject", Label: "Subject", Strategy: WidthFlex,
			Hideable: true,
			SortAsc:  sortAlphaAsc(func(e listEntry) string { return e.SortKey() }),
			SortDesc: sortAlphaDesc(func(e listEntry) string { return e.SortKey() }),
			Measure:  func(e listEntry) int { return len(e.SortKey()) },
		},
		{
			ID: "author", Label: "Author", Strategy: WidthMaxContent,
			Hideable: true,
			SortAsc:  sortAlphaAsc(commitAuthor),
			SortDesc: sortAlphaDesc(commitAuthor),
			Measure:  func(e listEntry) int { return len(commitAuthor(e)) },
		},
		{
			ID: "time", Label: "Time", Strategy: WidthMaxContent,
			Hideable: true,
			SortAsc:  sortTimeAsc(func(e listEntry) time.Time { return e.SortTime() }),
			SortDesc: sortTimeDesc(func(e listEntry) time.Time { return e.SortTime() }),
			Measure:  func(e listEntry) int { return len(relativeTime(e.SortTime())) },
		},
		{
			ID: "changed", Label: "Changed", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitChanged),
			SortDesc: sortIntDesc(commitChanged),
			Measure:  func(e listEntry) int { return intWidth(commitChanged(e)) },
		},
		{
			ID: "added", Label: "Added", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitAdded),
			SortDesc: sortIntDesc(commitAdded),
			Measure:  func(e listEntry) int { return intWidth(commitAdded(e)) },
		},
		{
			ID: "removed", Label: "Removed", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitRemoved),
			SortDesc: sortIntDesc(commitRemoved),
			Measure:  func(e listEntry) int { return intWidth(commitRemoved(e)) },
		},
		{
			ID: "files", Label: "Files", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitFiles),
			SortDesc: sortIntDesc(commitFiles),
			Measure:  func(e listEntry) int { return intWidth(commitFiles(e)) },
		},
		{
			ID: "files_added", Label: "Files +", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitFilesAdded),
			SortDesc: sortIntDesc(commitFilesAdded),
			Measure:  func(e listEntry) int { return intWidth(commitFilesAdded(e)) },
		},
		{
			ID: "files_modified", Label: "Files ~", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitFilesModified),
			SortDesc: sortIntDesc(commitFilesModified),
			Measure:  func(e listEntry) int { return intWidth(commitFilesModified(e)) },
		},
		{
			ID: "files_deleted", Label: "Files -", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(commitFilesDeleted),
			SortDesc: sortIntDesc(commitFilesDeleted),
			Measure:  func(e listEntry) int { return intWidth(commitFilesDeleted(e)) },
		},
	}
}

// commitHash extracts the short hash from a commitEntry, or empty string.
func commitHash(e listEntry) string {
	if ce, ok := e.(commitEntry); ok {
		return ce.shortHash
	}
	return ""
}

// commitAuthor extracts the author string from a commitEntry, or empty string.
func commitAuthor(e listEntry) string {
	if ce, ok := e.(commitEntry); ok {
		return ce.author
	}
	return ""
}

func commitChanged(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.additions + ce.deletions
	}
	return 0
}

func commitAdded(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.additions
	}
	return 0
}

func commitRemoved(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.deletions
	}
	return 0
}

func commitFiles(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.filesAdded + ce.filesModified + ce.filesDeleted
	}
	return 0
}

func commitFilesAdded(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.filesAdded
	}
	return 0
}

func commitFilesModified(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.filesModified
	}
	return 0
}

func commitFilesDeleted(e listEntry) int {
	if ce, ok := e.(commitEntry); ok {
		return ce.filesDeleted
	}
	return 0
}

// branchColumnDefs defines the columns for the Branches tab.
func branchColumnDefs() []*ColumnDef {
	return []*ColumnDef{
		{
			ID: "status", Label: "Status", Strategy: WidthMaxContent,
			Hideable: false,
			SortAsc:  sortAlphaAsc(branchStatusKey),
			SortDesc: sortAlphaDesc(branchStatusKey),
			Measure:  branchBadgeWidth,
		},
		{
			ID: "name", Label: " Name", Strategy: WidthMaxContent,
			Hideable: false,
			SortAsc:  sortAlphaAsc(func(e listEntry) string { return e.SortKey() }),
			SortDesc: sortAlphaDesc(func(e listEntry) string { return e.SortKey() }),
			Measure:  func(e listEntry) int { return 1 + len(e.SortKey()) },
		},
		{
			ID: "hash", Label: "Hash", Strategy: WidthFixed, Fixed: 7,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortAlphaAsc(branchHash),
			SortDesc: sortAlphaDesc(branchHash),
		},
		{
			ID: "subject", Label: "Subject", Strategy: WidthFlex,
			Hideable: true,
			SortAsc:  sortAlphaAsc(branchSubject),
			SortDesc: sortAlphaDesc(branchSubject),
			Measure:  func(e listEntry) int { return len(branchSubject(e)) },
		},
		{
			ID: "time", Label: "Time", Strategy: WidthMaxContent,
			Hideable: true,
			SortAsc:  sortTimeAsc(func(e listEntry) time.Time { return e.SortTime() }),
			SortDesc: sortTimeDesc(func(e listEntry) time.Time { return e.SortTime() }),
			Measure:  func(e listEntry) int { return len(relativeTime(e.SortTime())) },
		},
		{
			ID: "changed", Label: "Changed", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchChanged),
			SortDesc: sortIntDesc(branchChanged),
			Measure:  func(e listEntry) int { return intWidth(branchChanged(e)) },
		},
		{
			ID: "added", Label: "Added", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchAdded),
			SortDesc: sortIntDesc(branchAdded),
			Measure:  func(e listEntry) int { return intWidth(branchAdded(e)) },
		},
		{
			ID: "removed", Label: "Removed", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchRemoved),
			SortDesc: sortIntDesc(branchRemoved),
			Measure:  func(e listEntry) int { return intWidth(branchRemoved(e)) },
		},
		{
			ID: "files", Label: "Files", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchFiles),
			SortDesc: sortIntDesc(branchFiles),
			Measure:  func(e listEntry) int { return intWidth(branchFiles(e)) },
		},
		{
			ID: "files_added", Label: "Files +", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchFilesAdded),
			SortDesc: sortIntDesc(branchFilesAdded),
			Measure:  func(e listEntry) int { return intWidth(branchFilesAdded(e)) },
		},
		{
			ID: "files_modified", Label: "Files ~", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchFilesModified),
			SortDesc: sortIntDesc(branchFilesModified),
			Measure:  func(e listEntry) int { return intWidth(branchFilesModified(e)) },
		},
		{
			ID: "files_deleted", Label: "Files -", Strategy: WidthMaxContent,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortIntAsc(branchFilesDeleted),
			SortDesc: sortIntDesc(branchFilesDeleted),
			Measure:  func(e listEntry) int { return intWidth(branchFilesDeleted(e)) },
		},
	}
}

// branchHash extracts the short hash from a branchEntry, or empty string.
func branchHash(e listEntry) string {
	if be, ok := e.(branchEntry); ok {
		return be.shortHash
	}
	return ""
}

// branchStatusKey returns a sort key for the branch status column:
// conflicts → dirty → HEAD (clean) → other branches.
func branchStatusKey(e listEntry) string {
	be, ok := e.(branchEntry)
	if !ok {
		return "d"
	}
	if be.hasConflicts {
		return "a"
	}
	if be.uncommitted {
		return "b"
	}
	if be.isHead {
		return "c"
	}
	return "d"
}

// branchBadgeWidth returns the badge column width for a branch entry.
func branchBadgeWidth(e listEntry) int {
	be, ok := e.(branchEntry)
	if !ok {
		return 1
	}
	if be.hasConflicts {
		return len("[conflicts]") + 1
	}
	if be.uncommitted {
		return len("[dirty]") + 1
	}
	return 1
}

// branchSubject extracts the subject from a branchEntry, or empty string.
func branchSubject(e listEntry) string {
	if be, ok := e.(branchEntry); ok {
		return be.subject
	}
	return ""
}

func branchChanged(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.additions + be.deletions
	}
	return 0
}

func branchAdded(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.additions
	}
	return 0
}

func branchRemoved(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.deletions
	}
	return 0
}

func branchFiles(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.filesAdded + be.filesModified + be.filesDeleted
	}
	return 0
}

func branchFilesAdded(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.filesAdded
	}
	return 0
}

func branchFilesModified(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.filesModified
	}
	return 0
}

func branchFilesDeleted(e listEntry) int {
	if be, ok := e.(branchEntry); ok {
		return be.filesDeleted
	}
	return 0
}

// tagColumnDefs defines the columns for the Tags tab.
func tagColumnDefs() []*ColumnDef {
	return []*ColumnDef{
		{
			ID: "marker", Label: "", Strategy: WidthFixed, Fixed: 2,
			Hideable: false,
		},
		{
			ID: "name", Label: "Name", Strategy: WidthMaxContent,
			Hideable: false,
			SortAsc:  sortAlphaAsc(func(e listEntry) string { return e.SortKey() }),
			SortDesc: sortAlphaDesc(func(e listEntry) string { return e.SortKey() }),
			Measure:  func(e listEntry) int { return len(e.SortKey()) },
		},
		{
			ID: "hash", Label: "Hash", Strategy: WidthFixed, Fixed: 7,
			Hideable: true, DefaultHidden: true,
			SortAsc:  sortAlphaAsc(tagHash),
			SortDesc: sortAlphaDesc(tagHash),
		},
		{
			ID: "subject", Label: "Subject", Strategy: WidthFlex,
			Hideable: true,
			SortAsc:  sortAlphaAsc(tagSubject),
			SortDesc: sortAlphaDesc(tagSubject),
			Measure:  func(e listEntry) int { return len(tagSubject(e)) },
		},
		{
			ID: "time", Label: "Time", Strategy: WidthMaxContent,
			Hideable: true,
			SortAsc:  sortTimeAsc(func(e listEntry) time.Time { return e.SortTime() }),
			SortDesc: sortTimeDesc(func(e listEntry) time.Time { return e.SortTime() }),
			Measure:  func(e listEntry) int { return len(relativeTime(e.SortTime())) },
		},
	}
}

// tagHash extracts the short hash from a tagEntry, or empty string.
func tagHash(e listEntry) string {
	if te, ok := e.(tagEntry); ok {
		return te.shortHash
	}
	return ""
}

// tagSubject extracts the subject from a tagEntry, or empty string.
func tagSubject(e listEntry) string {
	if te, ok := e.(tagEntry); ok {
		return te.subject
	}
	return ""
}

// uncommittedColumnDefs defines the columns for the Uncommitted tab.
func uncommittedColumnDefs() []*ColumnDef {
	return []*ColumnDef{
		{
			ID: "icon", Label: " Staged", Strategy: WidthFixed, Fixed: 9,
			Hideable: false, DropdownHidden: true,
			SortAsc:  sortIntAsc(uncommittedStagingKey),
			SortDesc: sortIntDesc(uncommittedStagingKey),
		},
		{
			ID: "status", Label: "Status", Strategy: WidthMaxContent,
			Hideable: false,
			SortAsc:  sortAlphaAsc(uncommittedStatus),
			SortDesc: sortAlphaDesc(uncommittedStatus),
			Measure:  func(e listEntry) int { return len(uncommittedStatus(e)) + 2 },
		},
		{
			ID: "path", Label: "Path", Strategy: WidthFlex,
			Hideable: false,
			SortAsc:  sortAlphaAsc(func(e listEntry) string { return e.SortKey() }),
			SortDesc: sortAlphaDesc(func(e listEntry) string { return e.SortKey() }),
			Measure:  func(e listEntry) int { return len(e.SortKey()) },
		},
	}
}

// uncommittedStatus extracts the status from an uncommittedEntry, or empty string.
func uncommittedStatus(e listEntry) string {
	if ue, ok := e.(uncommittedEntry); ok {
		return ue.status
	}
	return ""
}

// uncommittedStagingKey returns an int sort key for the staging state:
// Staged(1) < Default(0) < Excluded(2) when ascending, grouping staged first.
func uncommittedStagingKey(e listEntry) int {
	if ue, ok := e.(uncommittedEntry); ok {
		return int(ue.staging)
	}
	return 0
}
