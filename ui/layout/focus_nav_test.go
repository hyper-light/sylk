package layout

import (
	"fmt"
	"testing"

	"github.com/adalundhe/sylk/ui/component"
)

// Synthetic FocusIDs used only in tests — the navigation algorithm never
// references specific IDs, so any distinct values work.
const (
	idA component.FocusID = iota + 100
	idB
	idC
	idD
	idE
	idF
	idG
	idH
	idI
	idJ
)

// pg wraps a single FocusID into a trivial PanelGroup (1×1 sub-grid).
func pg(id component.FocusID) PanelGroup {
	return PanelGroup{SubPanels: [][]component.FocusID{{id}}}
}

// pgSubs creates a PanelGroup from the given sub-grid rows.
func pgSubs(rows ...[]component.FocusID) PanelGroup {
	return PanelGroup{SubPanels: rows}
}

// mustFind calls FindInGrid and fails the test if not found.
func mustFind(t *testing.T, grid [][]PanelGroup, id component.FocusID) GridPos {
	t.Helper()
	pos, ok := FindInGrid(grid, id)
	if !ok {
		t.Fatalf("FindInGrid: %d not found in grid", id)
	}
	return pos
}

// navFrom navigates from the given ID in the given direction and returns
// the target. Fails on FindInGrid miss.
func navFrom(t *testing.T, grid [][]PanelGroup, id component.FocusID, dir Direction) component.FocusID {
	t.Helper()
	pos := mustFind(t, grid, id)
	target, _ := Navigate(grid, pos, dir)
	return target
}

// ---------------------------------------------------------------------------
// FindInGrid
// ---------------------------------------------------------------------------

func TestFindInGrid_Present(t *testing.T) {
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
		{pg(idD)},
	}
	tests := []struct {
		id   component.FocusID
		want GridPos
	}{
		{idA, GridPos{0, 0, 0, 0}},
		{idB, GridPos{0, 0, 1, 0}},
		{idC, GridPos{0, 1, 0, 0}},
		{idD, GridPos{1, 0, 0, 0}},
	}
	for _, tt := range tests {
		got, ok := FindInGrid(grid, tt.id)
		if !ok {
			t.Errorf("FindInGrid(%d): not found, want %v", tt.id, tt.want)
			continue
		}
		if got != tt.want {
			t.Errorf("FindInGrid(%d) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestFindInGrid_Absent(t *testing.T) {
	grid := [][]PanelGroup{{pg(idA)}}
	_, ok := FindInGrid(grid, idB)
	if ok {
		t.Error("FindInGrid: expected not found for absent ID")
	}
}

// ---------------------------------------------------------------------------
// Navigate — single cell
// ---------------------------------------------------------------------------

func TestNavigate_SingleCell(t *testing.T) {
	grid := [][]PanelGroup{{pg(idA)}}
	pos := mustFind(t, grid, idA)
	for _, dir := range []Direction{DirRight, DirLeft, DirDown, DirUp} {
		target, ok := Navigate(grid, pos, dir)
		if ok {
			t.Errorf("dir=%d: expected ok=false for single-cell grid, got target=%d", dir, target)
		}
		if target != idA {
			t.Errorf("dir=%d: expected idA, got %d", dir, target)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — single row, multiple panels (no sub-panels)
// ---------------------------------------------------------------------------

func TestNavigate_SingleRow_RightLeft(t *testing.T) {
	// [A] [B] [C]
	grid := [][]PanelGroup{{pg(idA), pg(idB), pg(idC)}}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Right: A→B→C→A (wrap)
		{idA, DirRight, idB},
		{idB, DirRight, idC},
		{idC, DirRight, idA},
		// Left: C→B→A→C (wrap)
		{idC, DirLeft, idB},
		{idB, DirLeft, idA},
		{idA, DirLeft, idC},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

func TestNavigate_SingleRow_UpDown(t *testing.T) {
	// Single row: up/down wraps to first panel of the same row.
	// From the first panel this is a no-op (ok=false).
	// From other panels it navigates to the first panel (ok=true).
	grid := [][]PanelGroup{{pg(idA), pg(idB)}}

	// A is already the first panel — up/down are no-ops.
	posA := mustFind(t, grid, idA)
	for _, dir := range []Direction{DirUp, DirDown} {
		_, ok := Navigate(grid, posA, dir)
		if ok {
			t.Errorf("from=A dir=%d: expected ok=false (no-op)", dir)
		}
	}

	// B wraps to A (the leftmost panel of the same row).
	posB := mustFind(t, grid, idB)
	for _, dir := range []Direction{DirUp, DirDown} {
		target, ok := Navigate(grid, posB, dir)
		if !ok || target != idA {
			t.Errorf("from=B dir=%d: got (%d, %v), want (%d, true)", dir, target, ok, idA)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — multiple rows, single column
// ---------------------------------------------------------------------------

func TestNavigate_MultiRow_SingleCol(t *testing.T) {
	// [A]
	// [B]
	// [C]
	grid := [][]PanelGroup{
		{pg(idA)},
		{pg(idB)},
		{pg(idC)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Down: A→B→C→A (wrap)
		{idA, DirDown, idB},
		{idB, DirDown, idC},
		{idC, DirDown, idA},
		// Up: A→C→B→A (wrap)
		{idA, DirUp, idC},
		{idC, DirUp, idB},
		{idB, DirUp, idA},
		// Right wraps across rows: A→B→C→A
		{idA, DirRight, idB},
		{idB, DirRight, idC},
		{idC, DirRight, idA},
		// Left wraps across rows: A→C→B→A
		{idA, DirLeft, idC},
		{idC, DirLeft, idB},
		{idB, DirLeft, idA},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — sub-panel traversal
// ---------------------------------------------------------------------------

func TestNavigate_SubPanels_Right(t *testing.T) {
	// Panel group with 2 sub-rows: [A],[B] — then panel [C].
	// Right: A→B (sub-row wrap) → C (exit) → A (main wrap)
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
	}

	tests := []struct {
		from, want component.FocusID
	}{
		{idA, idB},
		{idB, idC},
		{idC, idA},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, DirRight)
		if got != tt.want {
			t.Errorf("Right from %d: got %d, want %d", tt.from, got, tt.want)
		}
	}
}

func TestNavigate_SubPanels_Left(t *testing.T) {
	// Same grid: [A],[B] then [C].
	// Left: A→C (wrap to prev panel bottom-right) → B (left in sub) → A
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
	}

	tests := []struct {
		from, want component.FocusID
	}{
		{idC, idB},
		{idB, idA},
		{idA, idC},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, DirLeft)
		if got != tt.want {
			t.Errorf("Left from %d: got %d, want %d", tt.from, got, tt.want)
		}
	}
}

func TestNavigate_SubPanels_DownUp(t *testing.T) {
	// Left panel: [A],[B]. Right panel: [C]. Bottom row: [D].
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
		{pg(idD)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Down within sub-panels
		{idA, DirDown, idB},
		// Down exits sub-panels to next main row
		{idB, DirDown, idD},
		// Down from panel without sub-rows
		{idC, DirDown, idD},
		// Down wraps bottom→top
		{idD, DirDown, idA},
		// Up within sub-panels
		{idB, DirUp, idA},
		// Up exits sub-panels to previous main row (wraps top→bottom)
		{idA, DirUp, idD},
		// Up from panel without sub-rows (wraps)
		{idC, DirUp, idD},
		// Up from bottom row
		{idD, DirUp, idA},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — horizontal sub-panels (e.g., split editor: Preview | Editor)
// ---------------------------------------------------------------------------

func TestNavigate_HorizontalSubPanels(t *testing.T) {
	// Row 0: [LeftPanel([A],[B])], [Chat(C)], [CodePanel([D,E])]
	// Row 1: [Input(F)]
	grid := [][]PanelGroup{
		{
			pgSubs([]component.FocusID{idA}, []component.FocusID{idB}),
			pg(idC),
			pgSubs([]component.FocusID{idD, idE}),
		},
		{pg(idF)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Right through horizontal sub-panels
		{idD, DirRight, idE},
		// Right exits horizontal sub-panel
		{idE, DirRight, idF},
		// Left enters horizontal sub-panel at bottom-right
		{idF, DirLeft, idE},
		// Left within horizontal sub-panel
		{idE, DirLeft, idD},
		// Left exits horizontal sub-panel to previous panel
		{idD, DirLeft, idC},
		// Down from horizontal sub-panel (single sub-row, exits)
		{idD, DirDown, idF},
		{idE, DirDown, idF},
		// Up from horizontal sub-panel (single sub-row, exits)
		{idD, DirUp, idF},
		{idE, DirUp, idF},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — ragged sub-grids
// ---------------------------------------------------------------------------

func TestNavigate_RaggedSubGrid(t *testing.T) {
	// PanelGroup with ragged sub-rows: [A,B], [C]. Second panel: [D].
	// Second main row: [E].
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA, idB}, []component.FocusID{idC}), pg(idD)},
		{pg(idE)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Right: A→B→C→D→E→A (full cycle)
		{idA, DirRight, idB},
		{idB, DirRight, idC},
		{idC, DirRight, idD},
		{idD, DirRight, idE},
		{idE, DirRight, idA},
		// Left reverse: A→E→D→C→B→A
		{idA, DirLeft, idE},
		{idE, DirLeft, idD},
		{idD, DirLeft, idC},
		{idC, DirLeft, idB},
		{idB, DirLeft, idA},
		// Down: A→C (next sub-row), B→C, C→E (exit), D→E, E→A (wrap)
		{idA, DirDown, idC},
		{idB, DirDown, idC},
		{idC, DirDown, idE},
		{idD, DirDown, idE},
		{idE, DirDown, idA},
		// Up: C→A (prev sub-row), A→E (exit wrap), D→E (wrap), E→A
		{idC, DirUp, idA},
		{idA, DirUp, idE},
		{idD, DirUp, idE},
		{idE, DirUp, idA},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — full realistic grid (FourColumn-like)
// ---------------------------------------------------------------------------

func TestNavigate_FourColumnLayout(t *testing.T) {
	// Mimics FourColumn with Sessions+Agents left column and split code:
	//   Row 0: [Left([A],[B]), FileTree(C), Chat(D), Code([E,F])]
	//   Row 1: [Input(G)]
	grid := [][]PanelGroup{
		{
			pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), // Left: Sessions, Agents
			pg(idC), // FileTree
			pg(idD), // Chat
			pgSubs([]component.FocusID{idE, idF}), // Code: Preview, Editor
		},
		{pg(idG)}, // Input
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
		desc string
	}{
		// Right full cycle: A→B→C→D→E→F→G→A
		{idA, DirRight, idB, "Sessions→Agents"},
		{idB, DirRight, idC, "Agents→FileTree"},
		{idC, DirRight, idD, "FileTree→Chat"},
		{idD, DirRight, idE, "Chat→Preview"},
		{idE, DirRight, idF, "Preview→Editor"},
		{idF, DirRight, idG, "Editor→Input"},
		{idG, DirRight, idA, "Input→Sessions (wrap)"},

		// Left full cycle: A→G→F→E→D→C→B→A
		{idA, DirLeft, idG, "Sessions→Input (wrap)"},
		{idG, DirLeft, idF, "Input→Editor"},
		{idF, DirLeft, idE, "Editor→Preview"},
		{idE, DirLeft, idD, "Preview→Chat"},
		{idD, DirLeft, idC, "Chat→FileTree"},
		{idC, DirLeft, idB, "FileTree→Agents"},
		{idB, DirLeft, idA, "Agents→Sessions"},

		// Down: within sub-panels then exit
		{idA, DirDown, idB, "Sessions↓Agents"},
		{idB, DirDown, idG, "Agents↓Input"},
		{idC, DirDown, idG, "FileTree↓Input"},
		{idD, DirDown, idG, "Chat↓Input"},
		{idE, DirDown, idG, "Preview↓Input"},
		{idF, DirDown, idG, "Editor↓Input"},
		{idG, DirDown, idA, "Input↓Sessions (wrap)"},

		// Up: within sub-panels then exit
		{idB, DirUp, idA, "Agents↑Sessions"},
		{idA, DirUp, idG, "Sessions↑Input (wrap)"},
		{idC, DirUp, idG, "FileTree↑Input (wrap)"},
		{idD, DirUp, idG, "Chat↑Input (wrap)"},
		{idE, DirUp, idG, "Preview↑Input (wrap)"},
		{idF, DirUp, idG, "Editor↑Input (wrap)"},
		{idG, DirUp, idA, "Input↑Sessions"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := navFrom(t, grid, tt.from, tt.dir)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Navigate — ragged code panel (preview + horizontal editor split)
// ---------------------------------------------------------------------------

func TestNavigate_RaggedCodePanel(t *testing.T) {
	// Mimics FourColumn where the code panel has a preview (E) alongside a
	// horizontal editor split (F over G). The pane tree produces:
	//   V(preview, H(editor1, editor2)) → [[E, F], [G]]  (ragged)
	//
	// Row 0: [Left([A],[B]), FileTree(C), Chat(D), Code([E,F],[G])]
	// Row 1: [Input(H)]
	grid := [][]PanelGroup{
		{
			pgSubs([]component.FocusID{idA}, []component.FocusID{idB}),
			pg(idC),
			pg(idD),
			pgSubs([]component.FocusID{idE, idF}, []component.FocusID{idG}),
		},
		{pg(idH)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
		desc string
	}{
		// Right: full cycle visits all panes including ragged sub-row
		{idD, DirRight, idE, "Chat→Preview"},
		{idE, DirRight, idF, "Preview→Editor1"},
		{idF, DirRight, idG, "Editor1→Editor2 (sub-row wrap)"},
		{idG, DirRight, idH, "Editor2→Input (exit code panel)"},
		{idH, DirRight, idA, "Input→Sessions (wrap)"},

		// Left: reverse cycle
		{idH, DirLeft, idG, "Input→Editor2"},
		{idG, DirLeft, idF, "Editor2→Editor1 (sub-row wrap)"},
		{idF, DirLeft, idE, "Editor1→Preview"},
		{idE, DirLeft, idD, "Preview→Chat (exit code panel)"},

		// Down within code panel
		{idE, DirDown, idG, "Preview↓Editor2"},
		{idF, DirDown, idG, "Editor1↓Editor2"},
		{idG, DirDown, idH, "Editor2↓Input (exit)"},

		// Up within code panel
		{idG, DirUp, idE, "Editor2↑Preview"},
		{idF, DirUp, idH, "Editor1↑Input (exit wrap)"},
		{idE, DirUp, idH, "Preview↑Input (exit wrap)"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := navFrom(t, grid, tt.from, tt.dir)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Navigate — SingleColumn with sub-rows only
// ---------------------------------------------------------------------------

func TestNavigate_SingleColumn_SubRowsOnly(t *testing.T) {
	// SingleColumn showing Sessions: [Left([A],[B])], [Input(C)]
	grid := [][]PanelGroup{
		{pgSubs([]component.FocusID{idA}, []component.FocusID{idB})},
		{pg(idC)},
	}

	tests := []struct {
		from component.FocusID
		dir  Direction
		want component.FocusID
	}{
		// Right: A→B→C→A
		{idA, DirRight, idB},
		{idB, DirRight, idC},
		{idC, DirRight, idA},
		// Left: A→C→B→A
		{idA, DirLeft, idC},
		{idC, DirLeft, idB},
		{idB, DirLeft, idA},
		// Down: A→B→C→A
		{idA, DirDown, idB},
		{idB, DirDown, idC},
		{idC, DirDown, idA},
		// Up: A→C→B→A (note: A up → wrap to C)
		{idA, DirUp, idC},
		{idB, DirUp, idA},
		{idC, DirUp, idA},
	}
	for _, tt := range tests {
		got := navFrom(t, grid, tt.from, tt.dir)
		if got != tt.want {
			t.Errorf("from=%d dir=%d: got %d, want %d", tt.from, tt.dir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate — empty grid
// ---------------------------------------------------------------------------

func TestNavigate_EmptyGrid(t *testing.T) {
	var grid [][]PanelGroup
	_, ok := Navigate(grid, GridPos{}, DirRight)
	if ok {
		t.Error("expected ok=false for empty grid")
	}
}

// ---------------------------------------------------------------------------
// wrapIdx
// ---------------------------------------------------------------------------

func TestWrapIdx(t *testing.T) {
	tests := []struct {
		i, n, want int
	}{
		{0, 3, 0},
		{1, 3, 1},
		{3, 3, 0},
		{-1, 3, 2},
		{-3, 3, 0},
		{4, 3, 1},
	}
	for _, tt := range tests {
		got := wrapIdx(tt.i, tt.n)
		if got != tt.want {
			t.Errorf("wrapIdx(%d, %d) = %d, want %d", tt.i, tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Completeness — verify Navigate always returns a valid grid member
// ---------------------------------------------------------------------------

func TestNavigate_AllDirections_AlwaysValid(t *testing.T) {
	grids := []struct {
		name string
		grid [][]PanelGroup
	}{
		{"1x1", [][]PanelGroup{{pg(idA)}}},
		{"1x3", [][]PanelGroup{{pg(idA), pg(idB), pg(idC)}}},
		{"3x1", [][]PanelGroup{{pg(idA)}, {pg(idB)}, {pg(idC)}}},
		{"2x2", [][]PanelGroup{{pg(idA), pg(idB)}, {pg(idC), pg(idD)}}},
		{"subpanels", [][]PanelGroup{
			{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
			{pg(idD)},
		}},
		{"ragged", [][]PanelGroup{
			{pgSubs([]component.FocusID{idA, idB}, []component.FocusID{idC}), pg(idD)},
			{pg(idE), pg(idF)},
		}},
	}

	dirs := []Direction{DirRight, DirLeft, DirDown, DirUp}

	for _, tc := range grids {
		t.Run(tc.name, func(t *testing.T) {
			// Collect all IDs in the grid.
			ids := make(map[component.FocusID]bool)
			for _, row := range tc.grid {
				for _, pg := range row {
					for _, sr := range pg.SubPanels {
						for _, id := range sr {
							ids[id] = true
						}
					}
				}
			}

			for id := range ids {
				pos := mustFind(t, tc.grid, id)
				for _, dir := range dirs {
					target, _ := Navigate(tc.grid, pos, dir)
					if !ids[target] {
						t.Errorf("from=%d dir=%d: target %d not in grid", id, dir, target)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Right cycle completeness — right from every cell eventually visits all cells
// ---------------------------------------------------------------------------

func TestNavigate_RightCycleVisitsAll(t *testing.T) {
	grid := [][]PanelGroup{
		{
			pgSubs([]component.FocusID{idA}, []component.FocusID{idB}),
			pg(idC),
			pgSubs([]component.FocusID{idD, idE}),
		},
		{pg(idF)},
	}

	// Count total cells.
	total := 0
	for _, row := range grid {
		for _, pg := range row {
			for _, sr := range pg.SubPanels {
				total += len(sr)
			}
		}
	}

	start := idA
	visited := map[component.FocusID]bool{start: true}
	current := start
	for range total {
		pos := mustFind(t, grid, current)
		target, _ := Navigate(grid, pos, DirRight)
		current = target
		visited[current] = true
	}

	if len(visited) != total {
		t.Errorf("right cycle visited %d of %d cells: %v", len(visited), total, visited)
	}

	// Must return to start.
	if current != start {
		t.Errorf("right cycle did not return to start: ended at %d", current)
	}
}

// ---------------------------------------------------------------------------
// Left cycle completeness — mirrors right
// ---------------------------------------------------------------------------

func TestNavigate_LeftCycleVisitsAll(t *testing.T) {
	grid := [][]PanelGroup{
		{
			pgSubs([]component.FocusID{idA}, []component.FocusID{idB}),
			pg(idC),
			pgSubs([]component.FocusID{idD, idE}),
		},
		{pg(idF)},
	}

	total := 0
	for _, row := range grid {
		for _, pg := range row {
			for _, sr := range pg.SubPanels {
				total += len(sr)
			}
		}
	}

	start := idA
	visited := map[component.FocusID]bool{start: true}
	current := start
	for range total {
		pos := mustFind(t, grid, current)
		target, _ := Navigate(grid, pos, DirLeft)
		current = target
		visited[current] = true
	}

	if len(visited) != total {
		t.Errorf("left cycle visited %d of %d cells: %v", len(visited), total, visited)
	}

	if current != start {
		t.Errorf("left cycle did not return to start: ended at %d", current)
	}
}

// ---------------------------------------------------------------------------
// Symmetry: left(right(x)) == x and right(left(x)) == x
// ---------------------------------------------------------------------------

func TestNavigate_LeftRightSymmetry(t *testing.T) {
	grids := [][][]PanelGroup{
		{{pg(idA), pg(idB), pg(idC)}},
		{
			{pgSubs([]component.FocusID{idA}, []component.FocusID{idB}), pg(idC)},
			{pg(idD)},
		},
		{
			{pgSubs([]component.FocusID{idA, idB}, []component.FocusID{idC}), pg(idD)},
			{pg(idE), pg(idF)},
		},
	}

	for gi, grid := range grids {
		t.Run(fmt.Sprintf("grid%d", gi), func(t *testing.T) {
			for _, row := range grid {
				for _, grp := range row {
					for _, sr := range grp.SubPanels {
						for _, id := range sr {
							pos := mustFind(t, grid, id)

							// right then left should return to start
							rTarget, _ := Navigate(grid, pos, DirRight)
							rPos := mustFind(t, grid, rTarget)
							back, _ := Navigate(grid, rPos, DirLeft)
							if back != id {
								t.Errorf("left(right(%d)) = %d, want %d", id, back, id)
							}

							// left then right should return to start
							lTarget, _ := Navigate(grid, pos, DirLeft)
							lPos := mustFind(t, grid, lTarget)
							back, _ = Navigate(grid, lPos, DirRight)
							if back != id {
								t.Errorf("right(left(%d)) = %d, want %d", id, back, id)
							}
						}
					}
				}
			}
		})
	}
}
