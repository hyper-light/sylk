package memoryview

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/x/ansi"
)

func TestViewCanvasSpacesTreesAndUsesBranchConnectors(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())

	canvas := ansi.Strip(model.ViewCanvas())
	// The trunk+canopy layout uses diagonals for crown spokes and
	// rounded elbows for lateral side-branches. At least one branch
	// glyph category must be present when any tree has non-trivial
	// structure.
	hasDiagonals := strings.ContainsAny(canvas, "╱╲")
	hasElbows := strings.ContainsAny(canvas, "╭╮╰╯")
	if !hasDiagonals && !hasElbows {
		t.Fatalf("canvas missing branch connectors (neither diagonals nor elbows):\n%s", canvas)
	}

	lines := strings.Split(canvas, "\n")
	if len(lines) == 0 {
		t.Fatal("canvas rendered no lines")
	}
	bottom := lines[len(lines)-1]
	positions := runeSubstrPositions(bottom, "<⟡>")
	if len(positions) < 3 {
		t.Fatalf("bottom row missing rooted trees: %q", bottom)
	}
	for i := 1; i < len(positions); i++ {
		if positions[i]-positions[i-1] < 14 {
			t.Fatalf("root anchors too closely grouped on bottom row: %q", bottom)
		}
	}
	if strings.Count(bottom, "<⟡>") != 3 {
		t.Fatalf("expected intact root anchors on bottom row, got: %q", bottom)
	}

	extents := renderedTreeExtents(model)
	if len(extents) != 3 {
		t.Fatalf("expected 3 visible trees, got %d extents", len(extents))
	}
	for i := 1; i < len(extents); i++ {
		if extents[i].minX-extents[i-1].maxX < 8 {
			t.Fatalf("rendered trees too tightly grouped: %+v", extents)
		}
	}
}

func TestViewCanvasRendersEveryNonRootNode(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())

	canvas := ansi.Strip(model.ViewCanvas())
	// Count every glyph in the tiered node ramp plus the selected
	// glyph. Any non-root node must surface as exactly one of these.
	var totalNodeGlyphs int
	for _, glyph := range []string{"⬡", "⬢", "◆", "◇", "◈", "▣"} {
		totalNodeGlyphs += strings.Count(canvas, glyph)
	}
	totalRoots := 0
	for _, tree := range sampleSnapshot().Trees {
		totalRoots += len(tree.Roots)
	}
	totalNodes := 0
	for _, tree := range sampleSnapshot().Trees {
		totalNodes += len(tree.Nodes)
	}
	expectedVisibleNodeGlyphs := totalNodes - totalRoots
	if totalNodeGlyphs != expectedVisibleNodeGlyphs {
		t.Fatalf("expected %d non-root node glyphs, got %d:\n%s", expectedVisibleNodeGlyphs, totalNodeGlyphs, canvas)
	}
}

func TestTickSpawnsAndAgesParticles(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())
	if !model.AnimationEnabled() {
		t.Fatal("animation should be enabled by default")
	}

	// First render seeds the static grid and invokes the first
	// particle spawn. Ground dust fills to target on every advance().
	baseline := model.ViewCanvas()
	initialStripped := ansi.Strip(baseline)

	// Run several ticks to allow particles to drift.
	for i := 0; i < 10; i++ {
		model.Tick()
	}
	after := ansi.Strip(model.ViewCanvas())
	if initialStripped == after {
		t.Fatal("canvas unchanged after 10 animation ticks — particles not drifting")
	}
}

func TestDisableAnimationStopsParticleUpdates(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())

	before := ansi.Strip(model.ViewCanvas())
	for i := 0; i < 10; i++ {
		model.Tick()
	}
	after := ansi.Strip(model.ViewCanvas())
	if before != after {
		t.Fatal("canvas changed after ticks despite DisableAnimation")
	}
}

func TestHoverResolvesNodeAndRendersTooltip(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())

	// Build static grid by rendering once.
	_ = model.ViewCanvas()

	// Pick a known node's cell coordinates and hover exactly on it.
	pos, ok := model.positions["decision-2"]
	if !ok {
		t.Fatal("expected decision-2 to have a rendered position")
	}
	if changed := model.SetHoverCell(pos.x, pos.y); !changed {
		t.Fatal("expected hover to resolve a new node")
	}
	if got := model.HoveredNode(); got != "decision-2" {
		t.Fatalf("HoveredNode = %q, want decision-2", got)
	}

	canvas := ansi.Strip(model.ViewCanvas())
	// Tooltip must surface the node's title.
	if !strings.Contains(canvas, "Pool Reuse") {
		t.Fatalf("tooltip did not render node title 'Pool Reuse':\n%s", canvas)
	}
	// Tooltip border must be drawn — at least one rounded-corner glyph.
	if !strings.ContainsAny(canvas, "╭╮╰╯") {
		t.Fatalf("tooltip border missing from canvas:\n%s", canvas)
	}
}

func TestHoverOutsideAnyNodeClearsTooltip(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())
	_ = model.ViewCanvas()

	// Hover far from any node.
	model.SetHoverCell(0, 0)
	if got := model.HoveredNode(); got != "" {
		t.Fatalf("HoveredNode = %q, want empty when no node is under cursor", got)
	}
	canvas := ansi.Strip(model.ViewCanvas())
	// No node title should appear in the tooltip position.
	for _, title := range []string{"Pool Reuse", "Timeout Cap", "Scope", "Branch"} {
		if strings.Contains(canvas, title) {
			t.Fatalf("tooltip unexpectedly rendered %q when no node is hovered:\n%s", title, canvas)
		}
	}
}

func TestHoverOffCanvasClearsHover(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(96, 24)
	model.SetSnapshot(sampleSnapshot())
	_ = model.ViewCanvas()

	pos := model.positions["decision-2"]
	model.SetHoverCell(pos.x, pos.y)
	if got := model.HoveredNode(); got == "" {
		t.Fatal("expected a node to be hovered before off-canvas clear")
	}
	model.SetHoverCell(-1, -1)
	if got := model.HoveredNode(); got != "" {
		t.Fatalf("HoveredNode = %q, want empty after off-canvas clear", got)
	}
}

func TestViewCanvasSingletonCrownUsesDiagonalBranch(t *testing.T) {
	t.Parallel()

	model := New(theme.DefaultDark())
	model.DisableAnimation()
	model.SetCanvasSize(64, 20)
	model.SetSnapshot(&forest.ViewSnapshot{
		SelectedBranchID: "intent-2",
		Trees: []forest.ViewTree{
			{
				Family:    forest.TreeFamilyIntent,
				Label:     "Intent Tree",
				UpdatedAt: time.Unix(100, 0),
				Roots:     []string{"intent-1"},
				Nodes: []forest.ViewNode{
					{ID: "intent-1", RootID: "intent-1", Title: "Root", CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(10, 0)},
					{ID: "intent-2", RootID: "intent-1", ParentID: "intent-1", Title: "Child", CreatedAt: time.Unix(20, 0), UpdatedAt: time.Unix(20, 0)},
					{ID: "intent-3", RootID: "intent-1", ParentID: "intent-2", Title: "Leaf", CreatedAt: time.Unix(30, 0), UpdatedAt: time.Unix(30, 0)},
				},
			},
		},
	})

	canvas := ansi.Strip(model.ViewCanvas())
	// With trunk+canopy layout, a linear chain still surfaces as a
	// tree: the longest path forms the spine and the terminal leaf
	// is displaced laterally into the crown, yielding a diagonal
	// branch glyph (╱ or ╲). No straight pole should be acceptable.
	if !strings.ContainsAny(canvas, "╱╲") {
		t.Fatalf("singleton chain rendered without a diagonal crown branch:\n%s", canvas)
	}
}

func TestVisibleTreesForCanvasNarrowsOnMediumWidth(t *testing.T) {
	t.Parallel()

	trees := orderedTreesForCanvas(sampleSnapshot().Trees)
	visible := visibleTreesForCanvas(trees, "decision-2", 72)
	if len(visible) != 2 {
		t.Fatalf("expected medium-width canvas to show 2 separated trees, got %d", len(visible))
	}

	visible = visibleTreesForCanvas(trees, "decision-2", 96)
	if len(visible) != 3 {
		t.Fatalf("expected wide canvas to show 3 trees, got %d", len(visible))
	}
}

func TestTreePlacementsStayStablePerSession(t *testing.T) {
	t.Parallel()

	trees := visibleTreesForCanvas(orderedTreesForCanvas(sampleSnapshot().Trees), "decision-2", 96)
	first := distributeTreePlacements(96, "session-a", trees)
	second := distributeTreePlacements(96, "session-a", trees)
	if len(first) != len(second) {
		t.Fatalf("placement length mismatch: %d vs %d", len(first), len(second))
	}
	for idx := range first {
		if first[idx].anchorX != second[idx].anchorX || first[idx].lane != second[idx].lane {
			t.Fatalf("placements not stable for same session: %#v vs %#v", first, second)
		}
	}
}

func TestTreePlacementsVaryAcrossSessions(t *testing.T) {
	t.Parallel()

	trees := visibleTreesForCanvas(orderedTreesForCanvas(sampleSnapshot().Trees), "decision-2", 96)
	first := distributeTreePlacements(96, "session-a", trees)
	second := distributeTreePlacements(96, "session-b", trees)
	if len(first) != len(second) {
		t.Fatalf("placement length mismatch: %d vs %d", len(first), len(second))
	}
	different := false
	for idx := range first {
		if first[idx].anchorX != second[idx].anchorX {
			different = true
			break
		}
	}
	if !different {
		t.Fatalf("expected per-session placement variance, got identical anchors: %#v", first)
	}
}

func sampleSnapshot() *forest.ViewSnapshot {
	return &forest.ViewSnapshot{
		SessionID:        "session-alpha",
		SelectedBranchID: "decision-2",
		Trees: []forest.ViewTree{
			{
				Family:    forest.TreeFamilyIntent,
				Label:     "Intent Tree",
				UpdatedAt: time.Unix(100, 0),
				Roots:     []string{"intent-1"},
				Nodes: []forest.ViewNode{
					{ID: "intent-1", RootID: "intent-1", Title: "Root", CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(10, 0)},
					{ID: "intent-2", RootID: "intent-1", ParentID: "intent-1", Title: "Scope", CreatedAt: time.Unix(20, 0), UpdatedAt: time.Unix(20, 0)},
					{ID: "intent-3", RootID: "intent-1", ParentID: "intent-1", Title: "Branch", CreatedAt: time.Unix(30, 0), UpdatedAt: time.Unix(30, 0)},
				},
			},
			{
				Family:    forest.TreeFamilyDecision,
				Label:     "Decision Tree",
				UpdatedAt: time.Unix(200, 0),
				Roots:     []string{"decision-1"},
				Nodes: []forest.ViewNode{
					{ID: "decision-1", RootID: "decision-1", Title: "Baseline", CreatedAt: time.Unix(40, 0), UpdatedAt: time.Unix(40, 0)},
					{ID: "decision-2", RootID: "decision-1", ParentID: "decision-1", Title: "Pool Reuse", CreatedAt: time.Unix(50, 0), UpdatedAt: time.Unix(50, 0)},
					{ID: "decision-3", RootID: "decision-1", ParentID: "decision-1", Title: "Timeout Cap", CreatedAt: time.Unix(60, 0), UpdatedAt: time.Unix(60, 0)},
				},
			},
			{
				Family:    forest.TreeFamilyOutcome,
				Label:     "Outcome Tree",
				UpdatedAt: time.Unix(300, 0),
				Roots:     []string{"outcome-1"},
				Nodes: []forest.ViewNode{
					{ID: "outcome-1", RootID: "outcome-1", Title: "Pass", CreatedAt: time.Unix(70, 0), UpdatedAt: time.Unix(70, 0)},
					{ID: "outcome-2", RootID: "outcome-1", ParentID: "outcome-1", Title: "Follow-up", CreatedAt: time.Unix(80, 0), UpdatedAt: time.Unix(80, 0)},
				},
			},
		},
	}
}

type extent struct {
	minX int
	maxX int
}

func renderedTreeExtents(model *Model) []extent {
	extentsByTree := make(map[int]extent)
	for branchID, pos := range model.positions {
		treeIdx, ok := model.treeByBranch[branchID]
		if !ok {
			continue
		}
		ext, ok := extentsByTree[treeIdx]
		if !ok {
			extentsByTree[treeIdx] = extent{minX: pos.x, maxX: pos.x}
			continue
		}
		ext.minX = min(ext.minX, pos.x)
		ext.maxX = max(ext.maxX, pos.x)
		extentsByTree[treeIdx] = ext
	}
	extents := make([]extent, 0, len(extentsByTree))
	for _, ext := range extentsByTree {
		extents = append(extents, ext)
	}
	sort.Slice(extents, func(i, j int) bool {
		if extents[i].minX == extents[j].minX {
			return extents[i].maxX < extents[j].maxX
		}
		return extents[i].minX < extents[j].minX
	})
	return extents
}

func runeSubstrPositions(line, needle string) []int {
	if needle == "" {
		return nil
	}
	lineRunes := []rune(line)
	needleRunes := []rune(needle)
	if len(lineRunes) < len(needleRunes) {
		return nil
	}
	positions := make([]int, 0, 4)
	for i := 0; i <= len(lineRunes)-len(needleRunes); i++ {
		match := true
		for j := range needleRunes {
			if lineRunes[i+j] != needleRunes[j] {
				match = false
				break
			}
		}
		if match {
			positions = append(positions, i)
		}
	}
	return positions
}
