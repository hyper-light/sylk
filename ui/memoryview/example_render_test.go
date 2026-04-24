package memoryview

import (
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/x/ansi"
)

// TestExampleRender prints a representative rendering of the memory
// forest so the output can be eyeballed. Uses a richer sample than
// the unit-test fixture — more leaves, deeper chains, cross-family
// temporal correlation — so pollen trails and shimmer actually spawn.
func TestExampleRender(t *testing.T) {
	snapshot := exampleForestSnapshot()

	m := New(theme.DefaultDark())
	m.SetCanvasSize(140, 36)
	m.SetSnapshot(snapshot)

	// Advance animation a few ticks so particles have drifted into
	// view — the first paint would otherwise show only the static
	// forest + freshly-seeded ground dust.
	for i := 0; i < 20; i++ {
		m.Tick()
	}

	// Render once without hover.
	canvas := ansi.Strip(m.ViewCanvas())
	fmt.Println("\n── memory forest (no hover) ──")
	fmt.Println(canvas)

	// Render again with a hover on a specific leaf so the tooltip
	// is visible in the output.
	if pos, ok := m.positions["decision-4"]; ok {
		m.SetHoverCell(pos.x, pos.y)
		hoverCanvas := ansi.Strip(m.ViewCanvas())
		fmt.Println("\n── memory forest (hovering 'decision-4') ──")
		fmt.Println(hoverCanvas)
	}
}

func exampleForestSnapshot() *forest.ViewSnapshot {
	now := time.Now()
	t0 := now.Add(-90 * time.Second)
	t1 := now.Add(-75 * time.Second)
	t2 := now.Add(-60 * time.Second)
	t3 := now.Add(-45 * time.Second)
	t4 := now.Add(-30 * time.Second)
	t5 := now.Add(-15 * time.Second)

	return &forest.ViewSnapshot{
		SessionID:        "session-demo",
		SelectedBranchID: "decision-3",
		Trees: []forest.ViewTree{
			{
				Family:    forest.TreeFamilyIntent,
				Label:     "Intent Tree",
				UpdatedAt: t5,
				Roots:     []string{"intent-1"},
				Nodes: []forest.ViewNode{
					{ID: "intent-1", RootID: "intent-1", Title: "Ship memory-forest redesign", Summary: "Replace pole-tree layout with trunk+canopy and ambient animation.", CreatedAt: t0, UpdatedAt: t0, Salience: 0.9, Confidence: 0.95},
					{ID: "intent-2", RootID: "intent-1", ParentID: "intent-1", Title: "Trunk + canopy layout", Summary: "Longest-path trunk with radial crown of leaves.", CreatedAt: t1, UpdatedAt: t1, Salience: 0.88, Confidence: 0.9, Leaf: false},
					{ID: "intent-3", RootID: "intent-1", ParentID: "intent-2", Title: "Crown leaf spacing", Summary: "Fan leaves across 78% of a semicircle with aspect compensation.", CreatedAt: t2, UpdatedAt: t2, Salience: 0.8, Confidence: 0.8, Leaf: true},
					{ID: "intent-4", RootID: "intent-1", ParentID: "intent-2", Title: "Depth shading", Summary: "5-tier glyph ramp + parallax.", CreatedAt: t2, UpdatedAt: t2, Salience: 0.78, Confidence: 0.78, Leaf: true},
					{ID: "intent-5", RootID: "intent-1", ParentID: "intent-1", Title: "Hover tooltips", Summary: "Mouse cell-motion to node hit-test + auto-flipped tooltip.", CreatedAt: t3, UpdatedAt: t3, Salience: 0.85, Confidence: 0.82, Leaf: true},
					{ID: "intent-6", RootID: "intent-1", ParentID: "intent-1", Title: "Ambient layer", Summary: "Ground dust, drifting pollen, dashed cross-tree trails.", CreatedAt: t4, UpdatedAt: t4, Salience: 0.9, Confidence: 0.85, Leaf: true},
				},
			},
			{
				Family:    forest.TreeFamilyDecision,
				Label:     "Decision Tree",
				UpdatedAt: t5,
				Roots:     []string{"decision-1"},
				Nodes: []forest.ViewNode{
					{ID: "decision-1", RootID: "decision-1", Title: "Cache static grid", Summary: "Separate static forest render from per-tick dynamic overlay.", CreatedAt: t0, UpdatedAt: t0, Salience: 0.9, Confidence: 0.95},
					{ID: "decision-2", RootID: "decision-1", ParentID: "decision-1", Title: "Deterministic RNG", Summary: "Seed the particle pool with sessionSeed so frames are reproducible.", CreatedAt: t1, UpdatedAt: t1, Salience: 0.82, Confidence: 0.9},
					{ID: "decision-3", RootID: "decision-1", ParentID: "decision-2", Title: "Setforced vs setIfBlank", Summary: "Overlays pick a write policy per-layer: tooltip=forced, ambient=blank-only.", CreatedAt: t2, UpdatedAt: t2, Salience: 0.88, Confidence: 0.9, Leaf: true},
					{ID: "decision-4", RootID: "decision-1", ParentID: "decision-2", Title: "Generation-counter ticks", Summary: "Exit memory mode bumps the counter so in-flight ticks drop themselves.", CreatedAt: t3, UpdatedAt: t3, Salience: 0.86, Confidence: 0.9, Leaf: true},
					{ID: "decision-5", RootID: "decision-1", ParentID: "decision-1", Title: "Lane-relative angle", Summary: "Singleton crown angle picks the side with more lane room.", CreatedAt: t4, UpdatedAt: t4, Salience: 0.84, Confidence: 0.88, Leaf: true},
				},
			},
			{
				Family:    forest.TreeFamilyOutcome,
				Label:     "Outcome Tree",
				UpdatedAt: t5,
				Roots:     []string{"outcome-1"},
				Nodes: []forest.ViewNode{
					{ID: "outcome-1", RootID: "outcome-1", Title: "Tests green", Summary: "Layout + hover + animation tests all passing.", CreatedAt: t3, UpdatedAt: t3, Salience: 0.95, Confidence: 0.95},
					{ID: "outcome-2", RootID: "outcome-1", ParentID: "outcome-1", Title: "No compile errors", Summary: "go build ./... clean.", CreatedAt: t4, UpdatedAt: t4, Salience: 0.9, Confidence: 0.95, Leaf: true},
					{ID: "outcome-3", RootID: "outcome-1", ParentID: "outcome-1", Title: "go vet quiet", Summary: "No lint hits.", CreatedAt: t5, UpdatedAt: t5, Salience: 0.88, Confidence: 0.92, Leaf: true},
				},
			},
		},
	}
}
