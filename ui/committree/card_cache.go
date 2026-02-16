package committree

// cardCacheKey captures all inputs affecting a branch card's rendered output.
// Two keys are equal if and only if the corresponding cards would render
// identically.
type cardCacheKey struct {
	// Branch identity and data.
	name       string
	shortHash  string
	subject    string
	commitCount  int
	capped       bool
	behindCount  int
	behindCapped bool
	isHead     bool
	relTime    string // rendered relative time (changes ~1/min)

	// Visual state.
	selected bool
	expanded bool

	// Layout parameters.
	innerWidth     int
	trunkInnerTop  int
	trunkInnerBot  int
	hasTrunkTop    bool
	hasTrunkBot    bool

	// Working tree state.
	dirty     bool
	conflicts bool

	// Diff selection overlay state.
	diffActive bool
	diffIdx    int

	// Expansion state (only meaningful when expanded=true).
	selectedActionID int
	hasStagedFiles   bool
	commitInput      bool
	commitPhase      commitPhase
	commitMsg        string
	commitCursor     int
	commitSpinner    int
	cursorVisible    bool
	deleteConfirm    bool
	deleteConfirmYes bool
	defaultBranch    string
}

// cardCacheEntry pairs a key with its rendered output.
type cardCacheEntry struct {
	valid bool
	key   cardCacheKey
	lines []string
}

// buildCardCacheKey constructs a cache key from all rendering inputs.
func buildCardCacheKey(b BranchNode, selected bool, innerWidth, trunkInnerTop, trunkInnerBot int,
	hasTrunkTop, hasTrunkBot, expanded bool, exp *branchExpansion, wt workingTreeState, diff diffOverlay) cardCacheKey {

	k := cardCacheKey{
		name:          b.Name,
		shortHash:     b.ShortHash,
		subject:       b.Subject,
		commitCount:   b.CommitCount,
		capped:        b.CommitCountCapped,
		behindCount:   b.BehindCount,
		behindCapped:  b.BehindCountCapped,
		isHead:        b.IsHead,
		relTime:       relativeTime(b.AuthorTime),
		selected:      selected,
		expanded:      expanded,
		innerWidth:    innerWidth,
		trunkInnerTop: trunkInnerTop,
		trunkInnerBot: trunkInnerBot,
		hasTrunkTop:   hasTrunkTop,
		hasTrunkBot:   hasTrunkBot,
		dirty:         wt.dirty,
		conflicts:     wt.conflicts,
		diffActive:    diff.active,
		diffIdx:       diff.idx,
	}

	if expanded && exp != nil {
		k.selectedActionID = exp.selectedActionID
		k.hasStagedFiles = exp.hasStagedFiles
		k.commitInput = exp.commitInput
		k.commitPhase = exp.commitPhase
		k.commitMsg = exp.commitMsg
		k.commitCursor = exp.commitCursor
		k.commitSpinner = exp.commitSpinner
		k.cursorVisible = exp.cursorVisible
		k.deleteConfirm = exp.deleteConfirm
		k.deleteConfirmYes = exp.deleteConfirmYes
		k.defaultBranch = exp.defaultBranch
	}

	return k
}

// invalidateCardCache clears all cached branch card entries.
func (m *Model) invalidateCardCache() {
	m.cardCache = m.cardCache[:0]
}

// =========================================================================
// Commit node cache
// =========================================================================

// nodeCacheKey captures all inputs affecting a commit node's rendered output.
type nodeCacheKey struct {
	shortHash string
	subject   string
	author    string
	branch    string
	isMerge   bool
	additions int
	deletions int
	relTime   string
	selected  bool
	width     int
	isFirst   bool
	isLast    bool

	// Diff selection overlay state.
	diffActive bool
	diffIdx    int

	// DAG graph gutter fields (zero-valued in flat mode).
	commitLane  int
	maxLane     int
	laneGlyphs [maxGraphLanes]LaneGlyph // fixed-size for comparable struct
}

// nodeCacheEntry pairs a key with its rendered output.
type nodeCacheEntry struct {
	valid bool
	key   nodeCacheKey
	lines []string
}

// invalidateNodeCache clears all cached commit node entries.
func (m *Model) invalidateNodeCache() {
	m.nodeCache = m.nodeCache[:0]
}

// getCachedNode returns cached node lines if the key matches, otherwise
// renders the node, stores it in the cache, and returns fresh lines.
// graph is nil in flat/centered mode; non-nil in DAG mode.
func (m *Model) getCachedNode(idx int, n TreeNode, selected, isFirst, isLast bool, graph *GraphRow, maxLane int, diff diffOverlay) []string {
	key := nodeCacheKey{
		shortHash:  n.ShortHash,
		subject:    n.Subject,
		author:     n.Author,
		branch:     n.Branch,
		isMerge:    n.IsMerge,
		additions:  n.Additions,
		deletions:  n.Deletions,
		relTime:    relativeTime(n.AuthorTime),
		selected:   selected,
		width:      m.width,
		isFirst:    isFirst,
		isLast:     isLast,
		diffActive: diff.active,
		diffIdx:    diff.idx,
	}

	if graph != nil {
		key.commitLane = graph.CommitLane
		key.maxLane = maxLane
		for i := 0; i < len(graph.Lanes) && i < maxGraphLanes; i++ {
			key.laneGlyphs[i] = graph.Lanes[i]
		}
	}

	// Grow cache slice if needed.
	for len(m.nodeCache) <= idx {
		m.nodeCache = append(m.nodeCache, nodeCacheEntry{})
	}

	entry := &m.nodeCache[idx]
	if entry.valid && entry.key == key {
		return entry.lines
	}

	lines := renderNode(n, selected, m.width, m.theme, isFirst, isLast, graph, maxLane, diff)
	entry.valid = true
	entry.key = key
	entry.lines = lines
	return lines
}

// getCachedCard returns cached card lines if the key matches, otherwise
// renders the card, stores it in the cache, and returns fresh lines.
func (m *Model) getCachedCard(flatIdx int, b BranchNode, selected bool,
	innerWidth, trunkInnerTop, trunkInnerBot int, hasTrunkTop, hasTrunkBot, expanded bool,
	exp *branchExpansion, wt workingTreeState, diff diffOverlay) []string {

	key := buildCardCacheKey(b, selected, innerWidth, trunkInnerTop, trunkInnerBot,
		hasTrunkTop, hasTrunkBot, expanded, exp, wt, diff)

	// Grow cache slice if needed.
	for len(m.cardCache) <= flatIdx {
		m.cardCache = append(m.cardCache, cardCacheEntry{})
	}

	entry := &m.cardCache[flatIdx]
	if entry.valid && entry.key == key {
		return entry.lines
	}

	lines := buildBranchCard(b, selected, innerWidth, m.theme.Palette,
		trunkInnerTop, trunkInnerBot, hasTrunkTop, hasTrunkBot,
		expanded, exp, wt, diff)

	entry.valid = true
	entry.key = key
	entry.lines = lines
	return lines
}

// =========================================================================
// Commit card cache (DAG trunk/offshoot cards)
// =========================================================================

// commitCardCacheKey captures all inputs affecting a commit card's rendered
// output in the DAG trunk-and-offshoots view. Two keys are equal if and only
// if the corresponding cards would render identically.
type commitCardCacheKey struct {
	shortHash string
	subject   string
	author    string
	branch    string
	isMerge   bool
	additions int
	deletions int
	relTime   string

	selected      bool
	innerWidth    int
	trunkInnerTop int
	trunkInnerBot int
	hasTrunkTop   bool
	hasTrunkBot   bool

	// Diff selection overlay state.
	diffActive bool
	diffIdx    int
}

// commitCardCacheEntry pairs a key with its rendered output.
type commitCardCacheEntry struct {
	valid bool
	key   commitCardCacheKey
	lines []string
}

// invalidateCommitCardCache clears all cached commit card entries.
func (m *Model) invalidateCommitCardCache() {
	m.commitCardCache = m.commitCardCache[:0]
}

// getCachedCommitCard returns cached card lines if the key matches, otherwise
// renders the card, stores it in the cache, and returns fresh lines.
func (m *Model) getCachedCommitCard(cacheIdx int, n TreeNode, selected bool,
	innerWidth, trunkInnerTop, trunkInnerBot int, hasTrunkTop, hasTrunkBot bool,
	diff diffOverlay) []string {

	key := commitCardCacheKey{
		shortHash:     n.ShortHash,
		subject:       n.Subject,
		author:        n.Author,
		branch:        n.Branch,
		isMerge:       n.IsMerge,
		additions:     n.Additions,
		deletions:     n.Deletions,
		relTime:       relativeTime(n.AuthorTime),
		selected:      selected,
		innerWidth:    innerWidth,
		trunkInnerTop: trunkInnerTop,
		trunkInnerBot: trunkInnerBot,
		hasTrunkTop:   hasTrunkTop,
		hasTrunkBot:   hasTrunkBot,
		diffActive:    diff.active,
		diffIdx:       diff.idx,
	}

	for len(m.commitCardCache) <= cacheIdx {
		m.commitCardCache = append(m.commitCardCache, commitCardCacheEntry{})
	}

	entry := &m.commitCardCache[cacheIdx]
	if entry.valid && entry.key == key {
		return entry.lines
	}

	lines := renderCommitCard(n, selected, innerWidth, m.theme.Palette,
		trunkInnerTop, trunkInnerBot, hasTrunkTop, hasTrunkBot, diff)

	entry.valid = true
	entry.key = key
	entry.lines = lines
	return lines
}
