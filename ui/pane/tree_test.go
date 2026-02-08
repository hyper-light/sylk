package pane

import (
	"testing"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/layout"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// tree builds a single-leaf tree.
func leaf(id PaneID) *Node { return NewLeaf(id) }

// splitV builds a vertical split with two leaves.
func splitV(leftID, rightID PaneID) *Node {
	n := NewLeaf(leftID)
	n.Split(leftID, rightID, SplitVertical, Rect{})
	return n
}

// splitH builds a horizontal split with two leaves.
func splitH(topID, bottomID PaneID) *Node {
	n := NewLeaf(topID)
	n.Split(topID, bottomID, SplitHorizontal, Rect{})
	return n
}

// ---------------------------------------------------------------------------
// IsLeaf / LeafCount
// ---------------------------------------------------------------------------

func TestIsLeaf(t *testing.T) {
	n := leaf(1)
	if !n.IsLeaf() {
		t.Fatal("expected leaf")
	}
	if n.LeafCount() != 1 {
		t.Fatalf("expected 1 leaf, got %d", n.LeafCount())
	}
}

func TestLeafCountAfterSplit(t *testing.T) {
	n := splitV(1, 2)
	if n.IsLeaf() {
		t.Fatal("expected internal node")
	}
	if n.LeafCount() != 2 {
		t.Fatalf("expected 2 leaves, got %d", n.LeafCount())
	}
}

// ---------------------------------------------------------------------------
// Find / Contains
// ---------------------------------------------------------------------------

func TestFind(t *testing.T) {
	n := splitV(1, 2)
	if n.Find(1) == nil {
		t.Fatal("expected to find pane 1")
	}
	if n.Find(2) == nil {
		t.Fatal("expected to find pane 2")
	}
	if n.Find(99) != nil {
		t.Fatal("expected nil for missing pane")
	}
}

func TestContains(t *testing.T) {
	n := splitV(1, 2)
	if !n.Contains(1) {
		t.Fatal("expected Contains(1)")
	}
	if n.Contains(99) {
		t.Fatal("expected !Contains(99)")
	}
}

// ---------------------------------------------------------------------------
// Split
// ---------------------------------------------------------------------------

func TestSplitLeaf(t *testing.T) {
	n := leaf(1)
	ok := n.Split(1, 2, SplitVertical, Rect{})
	if !ok {
		t.Fatal("expected split to succeed")
	}
	if n.IsLeaf() {
		t.Fatal("root should be internal after split")
	}
	if n.Dir != SplitVertical {
		t.Fatal("expected vertical split")
	}
	if n.Left.ID != 1 || n.Right.ID != 2 {
		t.Fatalf("expected left=1 right=2, got left=%d right=%d", n.Left.ID, n.Right.ID)
	}
}

func TestSplitNestedTarget(t *testing.T) {
	// Start: V(1, 2). Split pane 2 horizontally.
	n := splitV(1, 2)
	ok := n.Split(2, 3, SplitHorizontal, Rect{})
	if !ok {
		t.Fatal("expected nested split to succeed")
	}
	if n.LeafCount() != 3 {
		t.Fatalf("expected 3 leaves, got %d", n.LeafCount())
	}
	// Right child should now be H(2, 3).
	r := n.Right
	if r.IsLeaf() || r.Dir != SplitHorizontal {
		t.Fatal("expected right child to be horizontal split")
	}
	if r.Left.ID != 2 || r.Right.ID != 3 {
		t.Fatalf("expected left=2 right=3, got left=%d right=%d", r.Left.ID, r.Right.ID)
	}
}

func TestSplitMissingTarget(t *testing.T) {
	n := leaf(1)
	if n.Split(99, 2, SplitVertical, Rect{}) {
		t.Fatal("expected split of missing target to fail")
	}
}

func TestSplitMinSizeEnforced(t *testing.T) {
	n := leaf(1)
	// Width too small for vertical split.
	area := Rect{W: minPaneW*2 - 1, H: 30}
	if n.Split(1, 2, SplitVertical, area) {
		t.Fatal("expected split to fail: width too small")
	}
	// Height too small for horizontal split.
	area = Rect{W: 80, H: minPaneH*2 - 1}
	if n.Split(1, 2, SplitHorizontal, area) {
		t.Fatal("expected split to fail: height too small")
	}
}

func TestSplitMinSizePasses(t *testing.T) {
	n := leaf(1)
	area := Rect{W: minPaneW*2 + dividerSize, H: minPaneH*2 + dividerSize}
	if !n.Split(1, 2, SplitVertical, area) {
		t.Fatal("expected vertical split to succeed at minimum size")
	}
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestCloseLeafInSplit(t *testing.T) {
	n := splitV(1, 2)
	focusTarget, ok := n.Close(1)
	if !ok {
		t.Fatal("expected close to succeed")
	}
	if focusTarget != 2 {
		t.Fatalf("expected focus target 2, got %d", focusTarget)
	}
	if !n.IsLeaf() || n.ID != 2 {
		t.Fatalf("expected root to collapse to leaf 2, got leaf=%v id=%d", n.IsLeaf(), n.ID)
	}
}

func TestCloseRightLeaf(t *testing.T) {
	n := splitV(1, 2)
	focusTarget, ok := n.Close(2)
	if !ok {
		t.Fatal("expected close to succeed")
	}
	if focusTarget != 1 {
		t.Fatalf("expected focus target 1, got %d", focusTarget)
	}
	if !n.IsLeaf() || n.ID != 1 {
		t.Fatal("expected root to collapse to leaf 1")
	}
}

func TestCloseNestedLeaf(t *testing.T) {
	// V(1, H(2, 3)). Close pane 2 → V(1, 3).
	n := splitV(1, 2)
	n.Split(2, 3, SplitHorizontal, Rect{})
	focusTarget, ok := n.Close(2)
	if !ok {
		t.Fatal("expected close to succeed")
	}
	if focusTarget != 3 {
		t.Fatalf("expected focus target 3, got %d", focusTarget)
	}
	if n.LeafCount() != 2 {
		t.Fatalf("expected 2 leaves, got %d", n.LeafCount())
	}
	if !n.Right.IsLeaf() || n.Right.ID != 3 {
		t.Fatal("expected right child to be leaf 3")
	}
}

func TestCloseRoot(t *testing.T) {
	n := leaf(1)
	_, ok := n.Close(1)
	if ok {
		t.Fatal("expected close of root leaf to fail")
	}
}

func TestCloseMissingTarget(t *testing.T) {
	n := splitV(1, 2)
	_, ok := n.Close(99)
	if ok {
		t.Fatal("expected close of missing target to fail")
	}
}

// ---------------------------------------------------------------------------
// ComputeLayout
// ---------------------------------------------------------------------------

func TestComputeLayoutSingleLeaf(t *testing.T) {
	n := leaf(1)
	area := Rect{X: 0, Y: 0, W: 80, H: 24}
	rects := n.ComputeLayout(area)
	if len(rects) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(rects))
	}
	r := rects[1]
	if r != area {
		t.Fatalf("expected %+v, got %+v", area, r)
	}
}

func TestComputeLayoutVerticalSplit(t *testing.T) {
	n := splitV(1, 2)
	area := Rect{X: 0, Y: 0, W: 81, H: 24} // 81 = 40 + 1 + 40
	rects := n.ComputeLayout(area)
	if len(rects) != 2 {
		t.Fatalf("expected 2 rects, got %d", len(rects))
	}
	left := rects[1]
	right := rects[2]

	if left.X != 0 || left.W != 40 {
		t.Fatalf("left: expected X=0 W=40, got X=%d W=%d", left.X, left.W)
	}
	if right.X != 41 || right.W != 40 {
		t.Fatalf("right: expected X=41 W=40, got X=%d W=%d", right.X, right.W)
	}
	// Both should have full height.
	if left.H != 24 || right.H != 24 {
		t.Fatalf("expected H=24, got left.H=%d right.H=%d", left.H, right.H)
	}
}

func TestComputeLayoutHorizontalSplit(t *testing.T) {
	n := splitH(1, 2)
	area := Rect{X: 0, Y: 0, W: 80, H: 25} // 25 = 12 + 1 + 12
	rects := n.ComputeLayout(area)
	if len(rects) != 2 {
		t.Fatalf("expected 2 rects, got %d", len(rects))
	}
	top := rects[1]
	bottom := rects[2]

	if top.Y != 0 || top.H != 12 {
		t.Fatalf("top: expected Y=0 H=12, got Y=%d H=%d", top.Y, top.H)
	}
	if bottom.Y != 13 || bottom.H != 12 {
		t.Fatalf("bottom: expected Y=13 H=12, got Y=%d H=%d", bottom.Y, bottom.H)
	}
	// Both should have full width.
	if top.W != 80 || bottom.W != 80 {
		t.Fatalf("expected W=80, got top.W=%d bottom.W=%d", top.W, bottom.W)
	}
}

func TestComputeLayoutNestedSplits(t *testing.T) {
	// V(1, H(2, 3)) with area 81x25.
	n := splitV(1, 2)
	n.Split(2, 3, SplitHorizontal, Rect{})
	area := Rect{X: 0, Y: 0, W: 81, H: 25}
	rects := n.ComputeLayout(area)
	if len(rects) != 3 {
		t.Fatalf("expected 3 rects, got %d", len(rects))
	}
	// All three panes should be present.
	for _, id := range []PaneID{1, 2, 3} {
		if _, ok := rects[id]; !ok {
			t.Fatalf("missing rect for pane %d", id)
		}
	}
	// Pane 1: left half, full height.
	if rects[1].X != 0 || rects[1].W != 40 {
		t.Fatalf("pane 1: expected W=40, got %+v", rects[1])
	}
	// Panes 2,3: right half, split vertically.
	if rects[2].X != 41 || rects[3].X != 41 {
		t.Fatalf("panes 2,3: expected X=41, got 2.X=%d 3.X=%d", rects[2].X, rects[3].X)
	}
}

// ---------------------------------------------------------------------------
// Leaves
// ---------------------------------------------------------------------------

func TestLeavesSingleLeaf(t *testing.T) {
	n := leaf(1)
	leaves := n.Leaves()
	if len(leaves) != 1 || leaves[0] != 1 {
		t.Fatalf("expected [1], got %v", leaves)
	}
}

func TestLeavesOrder(t *testing.T) {
	// V(1, H(2, 3)) → should be [1, 2, 3].
	n := splitV(1, 2)
	n.Split(2, 3, SplitHorizontal, Rect{})
	leaves := n.Leaves()
	expected := []PaneID{1, 2, 3}
	if len(leaves) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, leaves)
	}
	for i, id := range expected {
		if leaves[i] != id {
			t.Fatalf("leaves[%d]: expected %d, got %d", i, id, leaves[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Navigate
// ---------------------------------------------------------------------------

func TestNavigateVerticalSplit(t *testing.T) {
	n := splitV(1, 2)

	// Right from 1 → 2.
	target, ok := n.Navigate(1, layout.DirRight)
	if !ok || target != 2 {
		t.Fatalf("expected Navigate(1, Right) = (2, true), got (%d, %v)", target, ok)
	}
	// Left from 2 → 1.
	target, ok = n.Navigate(2, layout.DirLeft)
	if !ok || target != 1 {
		t.Fatalf("expected Navigate(2, Left) = (1, true), got (%d, %v)", target, ok)
	}
	// Left from 1 → no movement.
	target, ok = n.Navigate(1, layout.DirLeft)
	if ok {
		t.Fatalf("expected Navigate(1, Left) = (1, false), got (%d, %v)", target, ok)
	}
	// Right from 2 → no movement.
	target, ok = n.Navigate(2, layout.DirRight)
	if ok {
		t.Fatalf("expected Navigate(2, Right) = (2, false), got (%d, %v)", target, ok)
	}
}

func TestNavigateHorizontalSplit(t *testing.T) {
	n := splitH(1, 2)

	// Down from 1 → 2.
	target, ok := n.Navigate(1, layout.DirDown)
	if !ok || target != 2 {
		t.Fatalf("expected Navigate(1, Down) = (2, true), got (%d, %v)", target, ok)
	}
	// Up from 2 → 1.
	target, ok = n.Navigate(2, layout.DirUp)
	if !ok || target != 1 {
		t.Fatalf("expected Navigate(2, Up) = (1, true), got (%d, %v)", target, ok)
	}
	// Up from 1 → no movement.
	target, ok = n.Navigate(1, layout.DirUp)
	if ok {
		t.Fatalf("expected Navigate(1, Up) = (1, false), got (%d, %v)", target, ok)
	}
}

func TestNavigateNestedTree(t *testing.T) {
	// V(1, H(2, 3)).
	n := splitV(1, 2)
	n.Split(2, 3, SplitHorizontal, Rect{})

	// Right from 1 → leftmost of right subtree = 2.
	target, ok := n.Navigate(1, layout.DirRight)
	if !ok || target != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", target, ok)
	}
	// Left from 2 → rightmost of left subtree = 1.
	target, ok = n.Navigate(2, layout.DirLeft)
	if !ok || target != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", target, ok)
	}
	// Left from 3 → 1 (same left subtree).
	target, ok = n.Navigate(3, layout.DirLeft)
	if !ok || target != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", target, ok)
	}
	// Down from 2 → 3.
	target, ok = n.Navigate(2, layout.DirDown)
	if !ok || target != 3 {
		t.Fatalf("expected (3, true), got (%d, %v)", target, ok)
	}
	// Up from 3 → 2.
	target, ok = n.Navigate(3, layout.DirUp)
	if !ok || target != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", target, ok)
	}
	// Down from 1 → no movement (vertical split, no horizontal ancestor).
	target, ok = n.Navigate(1, layout.DirDown)
	if ok {
		t.Fatalf("expected (1, false), got (%d, %v)", target, ok)
	}
}

func TestNavigateMissingPaneID(t *testing.T) {
	n := splitV(1, 2)
	target, ok := n.Navigate(99, layout.DirRight)
	if ok {
		t.Fatalf("expected no movement for missing pane, got (%d, %v)", target, ok)
	}
}

// ---------------------------------------------------------------------------
// ToSubGrid
// ---------------------------------------------------------------------------

func TestToSubGridSingleLeaf(t *testing.T) {
	n := leaf(1)
	grid := n.ToSubGrid()
	if len(grid) != 1 || len(grid[0]) != 1 {
		t.Fatalf("expected 1x1 grid, got %v", grid)
	}
	if grid[0][0] != PaneFocusID(1) {
		t.Fatalf("expected FocusID for pane 1, got %d", grid[0][0])
	}
}

func TestToSubGridVerticalSplit(t *testing.T) {
	n := splitV(1, 2)
	grid := n.ToSubGrid()
	// Single row with two columns.
	if len(grid) != 1 {
		t.Fatalf("expected 1 row, got %d", len(grid))
	}
	if len(grid[0]) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(grid[0]))
	}
	if grid[0][0] != PaneFocusID(1) || grid[0][1] != PaneFocusID(2) {
		t.Fatalf("expected [P1, P2], got %v", grid[0])
	}
}

func TestToSubGridHorizontalSplit(t *testing.T) {
	n := splitH(1, 2)
	grid := n.ToSubGrid()
	// Two rows, one column each.
	if len(grid) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(grid))
	}
	if grid[0][0] != PaneFocusID(1) || grid[1][0] != PaneFocusID(2) {
		t.Fatalf("expected [[P1], [P2]], got %v", grid)
	}
}

func TestToSubGridNestedMixed(t *testing.T) {
	// V(1, H(2, 3)) → [[P1, P2], [P1, P3]] via mergeColumns.
	n := splitV(1, 2)
	n.Split(2, 3, SplitHorizontal, Rect{})
	grid := n.ToSubGrid()

	if len(grid) != 2 {
		t.Fatalf("expected 2 rows, got %d rows: %v", len(grid), grid)
	}
	// Row 0: [P1, P2].
	if len(grid[0]) != 2 || grid[0][0] != PaneFocusID(1) || grid[0][1] != PaneFocusID(2) {
		t.Fatalf("row 0: expected [P1, P2], got %v", grid[0])
	}
	// Row 1: [P1, P3] (P1 repeated from shorter left grid).
	if len(grid[1]) != 2 || grid[1][0] != PaneFocusID(1) || grid[1][1] != PaneFocusID(3) {
		t.Fatalf("row 1: expected [P1, P3], got %v", grid[1])
	}
}

// ---------------------------------------------------------------------------
// FocusID helpers
// ---------------------------------------------------------------------------

func TestPaneFocusID(t *testing.T) {
	fid := PaneFocusID(5)
	expected := component.FocusPaneBase + 5
	if fid != expected {
		t.Fatalf("expected %d, got %d", expected, fid)
	}
}

func TestIsPaneFocus(t *testing.T) {
	if IsPaneFocus(component.FocusCodeViewer) {
		t.Fatal("FocusCodeViewer should not be a pane focus")
	}
	if !IsPaneFocus(component.FocusPaneBase) {
		t.Fatal("FocusPaneBase should be a pane focus")
	}
	if !IsPaneFocus(component.FocusPaneBase + 42) {
		t.Fatal("FocusPaneBase+42 should be a pane focus")
	}
}

func TestPaneIDFromFocus(t *testing.T) {
	fid := component.FocusPaneBase + 7
	pid := PaneIDFromFocus(fid)
	if pid != 7 {
		t.Fatalf("expected PaneID 7, got %d", pid)
	}
}

// ---------------------------------------------------------------------------
// Ratio
// ---------------------------------------------------------------------------

func TestDefaultRatio(t *testing.T) {
	n := leaf(1)
	n.Split(1, 2, SplitVertical, Rect{})
	if n.Ratio != defaultRatio {
		t.Fatalf("expected ratio %f, got %f", defaultRatio, n.Ratio)
	}
}

// ---------------------------------------------------------------------------
// Four-pane grid (2x2)
// ---------------------------------------------------------------------------

func TestFourPaneGrid(t *testing.T) {
	// H(V(1,2), V(3,4)) — 2x2 grid.
	n := NewLeaf(PaneID(1))
	n.Split(1, 2, SplitHorizontal, Rect{})
	// Now H(1, 2). Split 1 → V(1, 3), split 2 → V(2, 4).
	// Wait, that gives H(V(1,3), V(2,4)). Let me restructure:
	// Actually to get a clean 2x2, we want:
	// V(top, bottom) where top = H(1,2), bottom = H(3,4).
	// But Split always makes original=Left, new=Right, so:
	n = NewLeaf(PaneID(1))
	n.Split(1, 3, SplitHorizontal, Rect{}) // H(1, 3)
	n.Split(1, 2, SplitVertical, Rect{})   // H(V(1,2), 3)
	n.Split(3, 4, SplitVertical, Rect{})   // H(V(1,2), V(3,4))

	if n.LeafCount() != 4 {
		t.Fatalf("expected 4 leaves, got %d", n.LeafCount())
	}

	// Layout.
	area := Rect{X: 0, Y: 0, W: 81, H: 25}
	rects := n.ComputeLayout(area)
	if len(rects) != 4 {
		t.Fatalf("expected 4 rects, got %d", len(rects))
	}

	// Navigation: right from 1 → 2, down from 1 → 3.
	target, ok := n.Navigate(1, layout.DirRight)
	if !ok || target != 2 {
		t.Fatalf("expected Right(1)=2, got (%d, %v)", target, ok)
	}
	target, ok = n.Navigate(1, layout.DirDown)
	if !ok || target != 3 {
		t.Fatalf("expected Down(1)=3, got (%d, %v)", target, ok)
	}
	target, ok = n.Navigate(4, layout.DirUp)
	if !ok || target != 2 {
		t.Fatalf("expected Up(4)=2, got (%d, %v)", target, ok)
	}
	target, ok = n.Navigate(4, layout.DirLeft)
	if !ok || target != 3 {
		t.Fatalf("expected Left(4)=3, got (%d, %v)", target, ok)
	}

	// SubGrid.
	grid := n.ToSubGrid()
	if len(grid) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(grid), grid)
	}
	if len(grid[0]) != 2 || len(grid[1]) != 2 {
		t.Fatalf("expected 2 cols per row, got %v", grid)
	}
}

// ---------------------------------------------------------------------------
// Dividers
// ---------------------------------------------------------------------------

func TestDividersSingleLeaf(t *testing.T) {
	n := leaf(1)
	divs := n.Dividers(Rect{0, 0, 80, 24})
	if len(divs) != 0 {
		t.Fatalf("expected 0 dividers, got %d", len(divs))
	}
}

func TestDividersVerticalSplit(t *testing.T) {
	n := splitV(1, 2)
	area := Rect{0, 0, 81, 24}
	divs := n.Dividers(area)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divider, got %d", len(divs))
	}
	d := divs[0]
	if d.Dir != SplitVertical {
		t.Fatalf("expected vertical divider, got %d", d.Dir)
	}
	// With ratio 0.5, leftW = (81-1)*0.5 = 40. Divider at X=40.
	if d.X != 40 {
		t.Fatalf("expected X=40, got %d", d.X)
	}
	if d.Y != 0 || d.Len != 24 {
		t.Fatalf("expected Y=0 Len=24, got Y=%d Len=%d", d.Y, d.Len)
	}
}

func TestDividersHorizontalSplit(t *testing.T) {
	n := splitH(1, 2)
	area := Rect{0, 0, 80, 25}
	divs := n.Dividers(area)
	if len(divs) != 1 {
		t.Fatalf("expected 1 divider, got %d", len(divs))
	}
	d := divs[0]
	if d.Dir != SplitHorizontal {
		t.Fatalf("expected horizontal divider, got %d", d.Dir)
	}
	// With ratio 0.5, topH = (25-1)*0.5 = 12. Divider at Y=12.
	if d.Y != 12 {
		t.Fatalf("expected Y=12, got %d", d.Y)
	}
	if d.X != 0 || d.Len != 80 {
		t.Fatalf("expected X=0 Len=80, got X=%d Len=%d", d.X, d.Len)
	}
}

func TestDividersFourPane(t *testing.T) {
	// H(V(1,2), V(3,4)) — 3 dividers total.
	n := NewLeaf(PaneID(1))
	n.Split(1, 3, SplitHorizontal, Rect{}) // H(1, 3)
	n.Split(1, 2, SplitVertical, Rect{})   // H(V(1,2), 3)
	n.Split(3, 4, SplitVertical, Rect{})   // H(V(1,2), V(3,4))

	area := Rect{0, 0, 81, 25}
	divs := n.Dividers(area)
	if len(divs) != 3 {
		t.Fatalf("expected 3 dividers, got %d", len(divs))
	}

	// Should have 1 horizontal + 2 vertical dividers.
	var hCount, vCount int
	for _, d := range divs {
		if d.Dir == SplitHorizontal {
			hCount++
		} else {
			vCount++
		}
	}
	if hCount != 1 || vCount != 2 {
		t.Fatalf("expected 1H + 2V dividers, got %dH + %dV", hCount, vCount)
	}
}
