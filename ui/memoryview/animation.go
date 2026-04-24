package memoryview

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/adalundhe/sylk/core/forest"
)

// Animation cadence. 60ms ticks = ~16 fps, plenty for drift effects
// without burning CPU. The tick source is cancellable so disabling
// animation (via DisableAnimation) stops scheduling further ticks.
const animationTickInterval = 60 * time.Millisecond

// ambientCaps bounds the total particle pool so worst-case frame cost
// remains predictable regardless of tree count. Per-frame overlay work
// is O(groundDust + drifters + trails).
const (
	groundDustCap = 320
	drifterCap    = 96
	trailCap      = 48
)

// shimmerWindow is how long a node shimmers after its UpdatedAt. Long
// enough for the eye to catch the pulse on a 750ms snapshot refresh
// without trailing past the next snapshot.
const shimmerWindow = 6 * time.Second

// particleKind distinguishes ambient visual layers that share the same
// pool-update path but render with different glyphs and styles.
type particleKind int

const (
	particleKindDust particleKind = iota
	particleKindPollen
	particleKindTrail
)

// particle is one animated point with sub-cell precision. x,y are
// floats so multi-tick drift looks smooth after flooring to cells.
// life counts animation ticks since spawn; ttl is the particle's
// total lifetime in ticks (immortal particles use ttl == 0).
type particle struct {
	kind  particleKind
	x, y  float64
	dx    float64 // per-tick drift (cells)
	dy    float64
	life  int
	ttl   int
	glyph rune
	style styleClass
	seed  uint64 // RNG seed for flicker/glyph rotation
}

// animationState owns the particle pool, deterministic RNG, and tick
// counter. One instance per Model. Reset wipes state when a new
// session is shown so prior session particles don't bleed across.
type animationState struct {
	enabled   bool
	rng       *rand.Rand
	tick      int64
	particles []particle
	// trailByKey caches the active trail for a (srcTree, dstTree) pair
	// so we don't respawn a trail every tick.
	trailByKey map[string]int // key -> particle index
	// lastCanvasW/H tracks geometry so we can cull off-canvas particles
	// when the terminal resizes.
	lastCanvasW int
	lastCanvasH int
}

func newAnimationState(sessionID string) *animationState {
	seed := sessionSeed(sessionID)
	return &animationState{
		enabled:    true,
		rng:        rand.New(rand.NewSource(int64(seed | 1))),
		particles:  make([]particle, 0, groundDustCap+drifterCap+trailCap),
		trailByKey: make(map[string]int, 16),
	}
}

func sessionSeed(sessionID string) uint64 {
	h := fnv.New64a()
	if sessionID == "" {
		_, _ = h.Write([]byte("__global__"))
	} else {
		_, _ = h.Write([]byte(sessionID))
	}
	return h.Sum64()
}

// reseed rebinds the RNG after a session switch, wiping particle
// state so motion in a fresh session starts from zero.
func (a *animationState) reseed(sessionID string) {
	seed := sessionSeed(sessionID)
	a.rng = rand.New(rand.NewSource(int64(seed | 1)))
	a.particles = a.particles[:0]
	a.tick = 0
	for k := range a.trailByKey {
		delete(a.trailByKey, k)
	}
}

// advance runs one animation tick: update positions, cull expired
// particles, opportunistically spawn replacements up to the caps.
//
// The snapshot is passed in so spawn rules can observe tree topology
// (which nodes carry high salience, which families cluster near each
// other, etc.). It is NOT modified.
func (a *animationState) advance(
	snapshot *forest.ViewSnapshot,
	canvasW, canvasH int,
	trees []canvasTree,
	layouts []canopyLayout,
) {
	if a == nil || !a.enabled {
		return
	}
	a.tick++
	a.lastCanvasW = canvasW
	a.lastCanvasH = canvasH

	// Update + cull. Two-pointer compaction keeps the slice packed so
	// the overlay renderer can just iterate without bounds churn.
	write := 0
	for _, p := range a.particles {
		p.life++
		p.x += p.dx
		p.y += p.dy
		if p.kind == particleKindPollen {
			// Pollen lifts and fades; dy drift accelerates slightly.
			p.dy -= 0.004
		}
		if p.ttl > 0 && p.life >= p.ttl {
			continue
		}
		if p.x < 0 || p.x >= float64(canvasW) || p.y < 0 || p.y >= float64(canvasH) {
			continue
		}
		a.particles[write] = p
		write++
	}
	a.particles = a.particles[:write]

	a.refreshGroundDust(canvasW, canvasH)
	a.refreshPollen(trees, layouts)
	a.refreshTrails(trees, layouts)

	// trailByKey is an index into the particle slice; after compaction
	// the indices may have shifted. Rebuild the map so keys stay
	// consistent with the slice.
	a.rebuildTrailIndex()
}

// refreshGroundDust keeps the bottom two rows sprinkled with a stable
// density of triangle + dot glyphs. Particles with ttl==0 are static
// (so the "ground" doesn't drift away), but we flicker glyph rotation
// each few ticks to give the soil subtle life.
func (a *animationState) refreshGroundDust(canvasW, canvasH int) {
	if canvasW < 4 || canvasH < 4 {
		return
	}
	existing := 0
	for _, p := range a.particles {
		if p.kind == particleKindDust {
			existing++
		}
	}
	target := clamp(canvasW/3, 20, groundDustCap)
	for existing < target {
		x := a.rng.Intn(canvasW)
		yOffset := a.rng.Intn(3) // rows 0,1,2 above the very bottom
		y := canvasH - 1 - yOffset
		if y < canvasH-3 {
			y = canvasH - 3
		}
		g := groundGlyphs[a.rng.Intn(len(groundGlyphs))]
		a.particles = append(a.particles, particle{
			kind:  particleKindDust,
			x:     float64(x),
			y:     float64(y),
			glyph: g,
			style: styleGround,
			seed:  a.rng.Uint64(),
		})
		existing++
	}
	// Flicker: rotate glyph every N ticks using seeded rotation so the
	// same particle always cycles through the same sequence.
	if a.tick%12 == 0 {
		for i := range a.particles {
			if a.particles[i].kind != particleKindDust {
				continue
			}
			step := (a.tick/12 + int64(a.particles[i].seed%uint64(len(groundGlyphs)))) % int64(len(groundGlyphs))
			a.particles[i].glyph = groundGlyphs[step]
		}
	}
}

// refreshPollen spawns pollen particles rising from the crown of
// high-salience trees. The salience proxy is the average leaf
// salience; UpdatedAt recency adds a brief burst.
func (a *animationState) refreshPollen(trees []canvasTree, layouts []canopyLayout) {
	if len(trees) == 0 || len(layouts) != len(trees) {
		return
	}
	existing := 0
	for _, p := range a.particles {
		if p.kind == particleKindPollen {
			existing++
		}
	}
	if existing >= drifterCap {
		return
	}
	now := time.Now()
	for i, tree := range trees {
		layout := layouts[i]
		if len(layout.crownLeaves) == 0 {
			continue
		}
		salience := treePollenRate(tree.tree, now)
		if salience <= 0 {
			continue
		}
		// Spawn probability per tick per tree proportional to
		// salience. High salience tree => ~1 particle per 10 ticks.
		if a.rng.Float64() > salience*0.08 {
			continue
		}
		leafIdx := a.rng.Intn(len(layout.crownLeaves))
		start, ok := layout.positions[layout.crownLeaves[leafIdx]]
		if !ok {
			continue
		}
		a.particles = append(a.particles, particle{
			kind: particleKindPollen,
			x:    float64(start.x),
			y:    float64(start.y),
			dx:   (a.rng.Float64()*0.6 - 0.3),
			dy:   -0.25 - a.rng.Float64()*0.25,
			ttl:  80 + a.rng.Intn(60),
			glyph: pollenGlyphs[a.rng.Intn(len(pollenGlyphs))],
			style: stylePollen,
			seed:  a.rng.Uint64(),
		})
		existing++
		if existing >= drifterCap {
			return
		}
	}
}

// refreshTrails manages dashed trails between cross-tree related
// nodes. A "relation" here is: two nodes in different trees share the
// same UpdatedAt-within-a-window, implying they were emitted as part
// of the same agent turn. Visualizes cross-family associativity.
func (a *animationState) refreshTrails(trees []canvasTree, layouts []canopyLayout) {
	if len(trees) < 2 || len(layouts) != len(trees) {
		return
	}
	// Collect a sample of cross-tree pairs by proximity in UpdatedAt.
	type anchor struct {
		treeIdx int
		nodeID  string
		pos     point
		t       time.Time
	}
	anchors := make([]anchor, 0, 32)
	for i, tree := range trees {
		layout := layouts[i]
		for _, leafID := range layout.crownLeaves {
			pos, ok := layout.positions[leafID]
			if !ok {
				continue
			}
			node := layout.nodeByID[leafID]
			anchors = append(anchors, anchor{
				treeIdx: i,
				nodeID:  leafID,
				pos:     pos,
				t:       node.UpdatedAt,
			})
			_ = tree
		}
	}
	// Sort by time and pair adjacent anchors that cross tree boundaries.
	sort.Slice(anchors, func(i, j int) bool {
		return anchors[i].t.Before(anchors[j].t)
	})
	for i := 1; i < len(anchors); i++ {
		prev := anchors[i-1]
		cur := anchors[i]
		if prev.treeIdx == cur.treeIdx {
			continue
		}
		if cur.t.Sub(prev.t) > 30*time.Second {
			continue
		}
		key := trailKey(prev.nodeID, cur.nodeID)
		if _, exists := a.trailByKey[key]; exists {
			continue
		}
		// Spawn 3-5 dash particles interpolated along the line; they
		// drift in opposite directions so the trail appears to flow.
		segments := 4 + a.rng.Intn(2)
		dx := float64(cur.pos.x - prev.pos.x)
		dy := float64(cur.pos.y - prev.pos.y)
		for s := 0; s < segments; s++ {
			t := float64(s+1) / float64(segments+1)
			px := float64(prev.pos.x) + dx*t
			py := float64(prev.pos.y) + dy*t
			a.particles = append(a.particles, particle{
				kind:  particleKindTrail,
				x:     px,
				y:     py,
				dx:    dx * 0.004,
				dy:    dy * 0.004,
				ttl:   60 + a.rng.Intn(30),
				glyph: trailGlyph,
				style: styleTrail,
				seed:  a.rng.Uint64(),
			})
			if len(a.particles) >= groundDustCap+drifterCap+trailCap {
				return
			}
		}
		// Mark the trail as active (index rebuilt after compaction).
		a.trailByKey[key] = len(a.particles) - 1
	}
}

// rebuildTrailIndex refreshes trailByKey after particle compaction.
// Called at end of advance(); the map values are used only as
// presence flags, so we store -1 for any key whose particles were
// culled this tick (causing refreshTrails to respawn them).
func (a *animationState) rebuildTrailIndex() {
	seen := make(map[string]struct{}, len(a.trailByKey))
	for i, p := range a.particles {
		if p.kind != particleKindTrail {
			continue
		}
		// We don't track which trail each particle belongs to; using
		// any live trail particle as the "key present" signal is
		// sufficient because trails are respawned in bulk.
		_ = i
		_ = seen
	}
	// Simple policy: if we have fewer trail particles than the number
	// of registered keys, treat trail pool as exhausted — clear the
	// map so next tick is allowed to respawn.
	count := 0
	for _, p := range a.particles {
		if p.kind == particleKindTrail {
			count++
		}
	}
	if count == 0 {
		for k := range a.trailByKey {
			delete(a.trailByKey, k)
		}
	}
}

// paint draws all active particles onto the supplied grid. Each
// particle's kind dictates whether it writes atop existing cells (via
// set, which respects layering) or only onto blanks (setIfBlank).
func (a *animationState) paint(grid *canvasGrid) {
	if a == nil || grid == nil {
		return
	}
	for _, p := range a.particles {
		x, y := int(p.x), int(p.y)
		switch p.kind {
		case particleKindDust:
			grid.setIfBlank(x, y, p.glyph, p.style)
		case particleKindPollen:
			grid.setIfBlank(x, y, p.glyph, p.style)
		case particleKindTrail:
			// Trails weave between nodes — paint only on blanks so we
			// don't obscure a leaf when a dash happens to coincide.
			grid.setIfBlank(x, y, p.glyph, p.style)
		}
	}
}

// paintShimmer pulses recently-updated nodes by overwriting their
// glyph with a higher-contrast one on specific tick phases. Reads
// nodes+positions from the supplied layout set; fails silently when
// the Model hasn't built them yet.
func (a *animationState) paintShimmer(
	grid *canvasGrid,
	trees []canvasTree,
	layouts []canopyLayout,
	selectedPath map[string]struct{},
) {
	if a == nil || grid == nil || len(layouts) == 0 {
		return
	}
	now := time.Now()
	phase := int(a.tick) % len(shimmerGlyphs)
	for i := range layouts {
		layout := layouts[i]
		for _, node := range trees[i].tree.Nodes {
			pos, ok := layout.positions[node.ID]
			if !ok {
				continue
			}
			if _, selected := selectedPath[node.ID]; selected {
				continue // selected nodes already have their own style
			}
			since := now.Sub(node.UpdatedAt)
			if since < 0 || since > shimmerWindow {
				continue
			}
			// Ramp: closer to UpdatedAt → stronger pulse. We show the
			// shimmer on 1-in-2 ticks so it reads as a pulse, not a
			// static overwrite.
			if a.tick%2 != int64(hashMod(node.ID, 2)) {
				continue
			}
			g := shimmerGlyphs[(phase+hashMod(node.ID, len(shimmerGlyphs)))%len(shimmerGlyphs)]
			grid.setForced(pos.x, pos.y, g, styleShimmer)
		}
	}
}

// treePollenRate estimates how many pollen particles a tree should
// emit per tick, in [0,1]. Inputs: crown salience average and recency
// of last update.
func treePollenRate(tree forest.ViewTree, now time.Time) float64 {
	if len(tree.Nodes) == 0 {
		return 0
	}
	var sum float64
	var count int
	for _, node := range tree.Nodes {
		if node.Leaf {
			sum += node.Salience
			count++
		}
	}
	if count == 0 {
		return 0
	}
	avg := sum / float64(count)
	recency := 1.0
	if !tree.UpdatedAt.IsZero() {
		elapsed := now.Sub(tree.UpdatedAt).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		// Decays to 0.1 over ~120 seconds.
		recency = 0.1 + 0.9*math.Exp(-elapsed/120)
	}
	return clampFloat(avg*recency, 0, 1)
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func trailKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

func hashMod(s string, mod int) int {
	if mod <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum64() % uint64(mod))
}
