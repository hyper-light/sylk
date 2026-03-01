package sylkdir

// NodeMeta is a lightweight node descriptor requiring no document I/O.
type NodeMeta struct {
	NodeID       uint32
	CanonicalKey string
	Domain       uint8
	NodeType     uint8
	Path         string
	Name         string
	DocRef       uint32
	SectionStart uint32
	SectionEnd   uint32
}

// ResolvedNode extends NodeMeta with the extracted document content.
type ResolvedNode struct {
	NodeMeta
	Content     string // Extracted section (or windowed)
	WindowStart uint32 // Actual start line of extracted window
	WindowEnd   uint32 // Actual end line of extracted window
	TotalLines  uint32 // Total lines in parent document
}

// ResolveOpts controls how content is extracted from the parent document.
type ResolveOpts struct {
	WindowLines uint32 // ±N surrounding lines (0 = exact section only)
}

// DocResolver joins KG query results with document content.
// Two resolution levels: metadata-only (Level 1) and full hydration (Level 2).
type DocResolver struct {
	nodeStore *VersionNodeStore
	docStore  *VersionDocStore
	docIDMap  *DocIDMap
	version   SemanticVersion
}

// NewDocResolver creates a resolver bound to the session's HEAD version.
func NewDocResolver(sess *Session) *DocResolver {
	return &DocResolver{
		nodeStore: sess.nodeStore,
		docStore:  sess.docStore,
		docIDMap:  sess.DocIDMap,
		version:   sess.Manifest.Head,
	}
}

// NewDocResolverFromStores creates a resolver from explicit store references.
func NewDocResolverFromStores(nodeStore *VersionNodeStore, docStore *VersionDocStore, docIDMap *DocIDMap, version SemanticVersion) *DocResolver {
	return &DocResolver{
		nodeStore: nodeStore,
		docStore:  docStore,
		docIDMap:  docIDMap,
		version:   version,
	}
}

// ResolveMetadata returns lightweight metadata for the given node IDs.
// No document I/O is performed. Missing nodes are silently skipped.
func (r *DocResolver) ResolveMetadata(nodeIDs []uint32) ([]NodeMeta, error) {
	result := make([]NodeMeta, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node, err := r.nodeStore.ReadFromVersion(r.version, id)
		if err != nil {
			continue // skip missing
		}
		result = append(result, nodeToMeta(node))
	}
	return result, nil
}

// Resolve returns fully hydrated nodes with extracted document content.
// Nodes sharing a DocRef share a single document load. Missing nodes or
// documents are gracefully handled with empty content.
func (r *DocResolver) Resolve(nodeIDs []uint32, opts ResolveOpts) ([]ResolvedNode, error) {
	nodes := r.loadNodes(nodeIDs)
	if len(nodes) == 0 {
		return nil, nil
	}

	groups := groupByDocRef(nodes)
	return r.hydrateGroups(groups, opts)
}

// docRefGroup pairs a DocRef with the nodes that share it.
type docRefGroup struct {
	docRef uint32
	nodes  []*Node
}

// loadNodes reads all requested nodes, skipping missing ones.
func (r *DocResolver) loadNodes(nodeIDs []uint32) []*Node {
	nodes := make([]*Node, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node, err := r.nodeStore.ReadFromVersion(r.version, id)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// groupByDocRef partitions nodes by their DocRef value.
// Nodes with DocRef=0 (no document) are placed in a separate group.
func groupByDocRef(nodes []*Node) []docRefGroup {
	order := make([]uint32, 0, len(nodes))
	groups := make(map[uint32]*docRefGroup, len(nodes))

	for _, n := range nodes {
		g, ok := groups[n.DocRef]
		if !ok {
			g = &docRefGroup{docRef: n.DocRef}
			groups[n.DocRef] = g
			order = append(order, n.DocRef)
		}
		g.nodes = append(g.nodes, n)
	}

	result := make([]docRefGroup, len(order))
	for i, ref := range order {
		result[i] = *groups[ref]
	}
	return result
}

// hydrateGroups loads each unique document once and extracts sections.
func (r *DocResolver) hydrateGroups(groups []docRefGroup, opts ResolveOpts) ([]ResolvedNode, error) {
	var result []ResolvedNode
	for _, g := range groups {
		resolved, err := r.hydrateGroup(g, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}
	return result, nil
}

// hydrateGroup loads one document and extracts sections for all its nodes.
func (r *DocResolver) hydrateGroup(g docRefGroup, opts ResolveOpts) ([]ResolvedNode, error) {
	if g.docRef == 0 {
		return emptyResults(g.nodes), nil
	}

	doc, err := r.docStore.ReadByDocRef(r.version, g.docRef)
	if err != nil {
		return emptyResults(g.nodes), nil //nolint:nilerr // missing doc → empty content
	}

	li := BuildLineIndex(doc.Content)
	result := make([]ResolvedNode, len(g.nodes))
	for i, n := range g.nodes {
		result[i] = extractResolved(n, doc.Content, li, opts)
	}
	return result, nil
}

// extractResolved builds a ResolvedNode by extracting the relevant section.
func extractResolved(n *Node, content string, li LineIndex, opts ResolveOpts) ResolvedNode {
	meta := nodeToMeta(n)
	rn := ResolvedNode{
		NodeMeta:   meta,
		TotalLines: li.LineCount(),
	}

	// SectionStart=0 means whole document.
	if n.SectionStart == 0 {
		rn.Content = content
		rn.WindowStart = 1
		rn.WindowEnd = li.LineCount()
		return rn
	}

	if opts.WindowLines > 0 {
		rn.Content, rn.WindowStart, rn.WindowEnd = li.ExtractWithWindow(content, n.SectionStart, n.SectionEnd, opts.WindowLines)
		return rn
	}

	rn.Content = li.Extract(content, n.SectionStart, n.SectionEnd)
	rn.WindowStart = n.SectionStart
	rn.WindowEnd = n.SectionEnd
	return rn
}

// nodeToMeta converts a Node to lightweight NodeMeta.
func nodeToMeta(n *Node) NodeMeta {
	return NodeMeta{
		NodeID:       n.ID,
		CanonicalKey: n.CanonicalKey,
		Domain:       n.Domain,
		NodeType:     n.NodeType,
		Path:         n.Path,
		Name:         n.Name,
		DocRef:       n.DocRef,
		SectionStart: n.SectionStart,
		SectionEnd:   n.SectionEnd,
	}
}

// emptyResults returns ResolvedNodes with empty content for nodes whose
// documents could not be loaded.
func emptyResults(nodes []*Node) []ResolvedNode {
	result := make([]ResolvedNode, len(nodes))
	for i, n := range nodes {
		result[i] = ResolvedNode{NodeMeta: nodeToMeta(n)}
	}
	return result
}

