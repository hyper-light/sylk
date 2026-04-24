package memoryview

// styleClass enumerates every color/emphasis tier used by the canvas.
// The stylesheet in canvas.go materializes one lipgloss.Style per class,
// so adding a new visual layer means adding a class here plus its
// entry in buildStylesheet.
type styleClass int

const (
	styleBack styleClass = iota
	styleMid
	styleFront
	styleSelected
	styleRoot

	// Ambient layers (painted over the forest after the trees are drawn).
	styleGround  // static ground dust
	styleDust    // floating ambient sparks
	stylePollen  // drifting pollen from high-warmth trees
	styleTrail   // dashed cross-tree affinity trails
	styleShimmer // pulsing highlight on recently-updated nodes

	// Tooltip overlay.
	styleTooltipBorder
	styleTooltipText
	styleTooltipDim
	styleTooltipAccent

	styleClassCount
)

// nodeTier maps a node's parallax + crown position to one of five
// render tiers. Lower tiers are drawn dimmer + with thinner glyphs to
// suggest depth; higher tiers are brighter and "closer" to the viewer.
type nodeTier int

const (
	tierFar nodeTier = iota
	tierBackMid
	tierMid
	tierNear
	tierFront
)

// nodeGlyph returns the rune used for a given render tier. The ramp is
// chosen so adjacent tiers remain visually distinct at small sizes:
// outline → filled → diamond → diamond-dot → hex.
func nodeGlyph(tier nodeTier, selected bool) rune {
	if selected {
		return '◈'
	}
	switch tier {
	case tierFar:
		return '⬡'
	case tierBackMid:
		return '◇'
	case tierMid:
		return '⬢'
	case tierNear:
		return '◆'
	case tierFront:
		return '◈'
	default:
		return '⬢'
	}
}

// tierStyle maps a render tier to the baseline styleClass. The parallax
// plane of the tree (treeIndex / treeCount-1) biases the tier toward
// back or front so far trees read dim even when their own topology
// would otherwise promote them.
func tierStyle(tier nodeTier, selected bool) styleClass {
	if selected {
		return styleSelected
	}
	switch tier {
	case tierFar, tierBackMid:
		return styleBack
	case tierMid:
		return styleMid
	case tierNear, tierFront:
		return styleFront
	}
	return styleMid
}

// Root anchor: the glyph row at the base of every trunk. Runes chosen
// so width(<◆>) = 3 cells, matching legacy layout accounting in tests.
const rootAnchorGlyph = "<⟡>"

// Branch glyphs — diagonal strokes for canopy branches from trunk top
// out to each leaf. Kept separate from box-drawing connectors so the
// overlap rules in canvas.set can treat them as ordinary connectors.
const (
	branchDiagonalRight = '╱'
	branchDiagonalLeft  = '╲'
)

// Ground layer glyphs — scattered along the bottom two rows to suggest
// a soil plane. Mix of up/down triangles + mid-dots for density.
var groundGlyphs = []rune{'▴', '▵', '▾', '▿', '·', '˙', '∵', '∴'}

// Ambient particle glyphs (drifting sparks). Lightest first; heavier
// ones are spawned from high-warmth trees to anchor the eye.
var dustGlyphs = []rune{'·', '˙', '∙'}
var pollenGlyphs = []rune{'·', '•', '●'}

// trailGlyph is the dash used in animated cross-tree trails. Using a
// full dash (not half-dash) gives good contrast against ' '.
const trailGlyph = '┈'

// shimmerGlyphs cycle per node during the "recently updated" pulse.
// Enough frames for the eye to register a change without flicker.
var shimmerGlyphs = []rune{'◇', '◈', '◆', '◈'}
