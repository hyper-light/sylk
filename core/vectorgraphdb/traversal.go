package vectorgraphdb

import (
	"container/list"
	"errors"
	"fmt"
)

var (
	ErrPathNotFound = errors.New("path not found")
	ErrMaxDepth     = errors.New("max depth exceeded")
)

// TraversalOptions configures graph traversal behavior.
type TraversalOptions struct {
	MaxDepth  int
	EdgeTypes []EdgeType
	Direction TraversalDirection
}

// TraversalDirection specifies edge direction for traversal.
type TraversalDirection int

const (
	DirectionOutgoing TraversalDirection = iota
	DirectionIncoming
	DirectionBoth
)

// Path represents a path through the graph.
type Path struct {
	Nodes []*GraphNode
	Edges []*GraphEdge
}

// GraphTraverser provides graph traversal operations.
type GraphTraverser struct {
	db *VectorGraphDB
	ns *NodeStore
	es *EdgeStore
}

// NewGraphTraverser creates a new GraphTraverser.
func NewGraphTraverser(db *VectorGraphDB) *GraphTraverser {
	return &GraphTraverser{
		db: db,
		ns: NewNodeStore(db, nil),
		es: NewEdgeStore(db),
	}
}

// GetNeighbors returns all neighbors of a node.
func (gt *GraphTraverser) GetNeighbors(nodeID string, opts *TraversalOptions) ([]*GraphNode, error) {
	if opts == nil {
		opts = &TraversalOptions{Direction: DirectionBoth}
	}

	edges, err := gt.getRelevantEdges(nodeID, opts)
	if err != nil {
		return nil, err
	}

	return gt.loadNeighborNodes(nodeID, edges)
}

func (gt *GraphTraverser) getRelevantEdges(nodeID string, opts *TraversalOptions) ([]*GraphEdge, error) {
	var edges []*GraphEdge

	outgoing, err := gt.getOutgoingIfNeeded(nodeID, opts)
	if err != nil {
		return nil, err
	}
	edges = append(edges, outgoing...)

	incoming, err := gt.getIncomingIfNeeded(nodeID, opts)
	if err != nil {
		return nil, err
	}
	edges = append(edges, incoming...)

	return edges, nil
}

func (gt *GraphTraverser) getOutgoingIfNeeded(nodeID string, opts *TraversalOptions) ([]*GraphEdge, error) {
	if opts.Direction != DirectionOutgoing && opts.Direction != DirectionBoth {
		return nil, nil
	}
	return gt.es.GetOutgoingEdges(nodeID, opts.EdgeTypes...)
}

func (gt *GraphTraverser) getIncomingIfNeeded(nodeID string, opts *TraversalOptions) ([]*GraphEdge, error) {
	if opts.Direction != DirectionIncoming && opts.Direction != DirectionBoth {
		return nil, nil
	}
	return gt.es.GetIncomingEdges(nodeID, opts.EdgeTypes...)
}

func (gt *GraphTraverser) loadNeighborNodes(nodeID string, edges []*GraphEdge) ([]*GraphNode, error) {
	neighborIDs := gt.extractNeighborIDs(nodeID, edges)
	return gt.loadNodesByIDs(neighborIDs)
}

func (gt *GraphTraverser) extractNeighborIDs(nodeID string, edges []*GraphEdge) []string {
	seen := make(map[string]bool)
	var ids []string

	for _, e := range edges {
		id := gt.getOtherNodeID(nodeID, e)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func (gt *GraphTraverser) getOtherNodeID(nodeID string, e *GraphEdge) string {
	if e.SourceID == nodeID {
		return e.TargetID
	}
	return e.SourceID
}

func (gt *GraphTraverser) loadNodesByIDs(ids []string) ([]*GraphNode, error) {
	nodes := make([]*GraphNode, 0, len(ids))
	for _, id := range ids {
		node, err := gt.ns.GetNode(id)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ShortestPath finds the shortest path between two nodes using BFS.
func (gt *GraphTraverser) ShortestPath(fromID, toID string, opts *TraversalOptions) (*Path, error) {
	if opts == nil {
		opts = &TraversalOptions{MaxDepth: 10, Direction: DirectionBoth}
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}

	return gt.bfsPath(fromID, toID, opts)
}

func (gt *GraphTraverser) bfsPath(fromID, toID string, opts *TraversalOptions) (*Path, error) {
	state := gt.initBFSState(fromID)

	for state.queue.Len() > 0 {
		curr := state.queue.Remove(state.queue.Front()).(bfsNode)

		if result := gt.processBFSNode(curr, fromID, toID, opts, state); result != nil {
			return result, nil
		}
	}

	return nil, ErrPathNotFound
}

type bfsState struct {
	visited map[string]bool
	parent  map[string]parentInfo
	queue   *list.List
}

func (gt *GraphTraverser) initBFSState(fromID string) *bfsState {
	state := &bfsState{
		visited: make(map[string]bool),
		parent:  make(map[string]parentInfo),
		queue:   list.New(),
	}
	state.queue.PushBack(bfsNode{id: fromID, depth: 0})
	state.visited[fromID] = true
	return state
}

func (gt *GraphTraverser) processBFSNode(curr bfsNode, fromID, toID string, opts *TraversalOptions, state *bfsState) *Path {
	if curr.id == toID {
		path, _ := gt.reconstructPath(fromID, toID, state.parent)
		return path
	}

	if curr.depth < opts.MaxDepth {
		gt.expandBFS(curr, opts, state.visited, state.parent, state.queue)
	}
	return nil
}

type bfsNode struct {
	id    string
	depth int
}

type parentInfo struct {
	nodeID string
	edge   *GraphEdge
}

func (gt *GraphTraverser) expandBFS(curr bfsNode, opts *TraversalOptions, visited map[string]bool, parent map[string]parentInfo, queue *list.List) error {
	edges, err := gt.getRelevantEdges(curr.id, opts)
	if err != nil {
		return err
	}

	for _, e := range edges {
		neighborID := gt.getOtherNodeID(curr.id, e)
		if visited[neighborID] {
			continue
		}
		visited[neighborID] = true
		parent[neighborID] = parentInfo{nodeID: curr.id, edge: e}
		queue.PushBack(bfsNode{id: neighborID, depth: curr.depth + 1})
	}
	return nil
}

func (gt *GraphTraverser) reconstructPath(fromID, toID string, parent map[string]parentInfo) (*Path, error) {
	path := &Path{Nodes: make([]*GraphNode, 0), Edges: make([]*GraphEdge, 0)}
	nodeIDs, edges := gt.backtrackPath(fromID, toID, parent)

	nodes, err := gt.loadPathNodes(nodeIDs)
	if err != nil {
		return nil, err
	}

	path.Nodes = nodes
	path.Edges = edges
	return path, nil
}

func (gt *GraphTraverser) backtrackPath(fromID, toID string, parent map[string]parentInfo) ([]string, []*GraphEdge) {
	var nodeIDs []string
	var edges []*GraphEdge

	curr := toID
	for curr != fromID {
		nodeIDs = append([]string{curr}, nodeIDs...)
		p := parent[curr]
		edges = append([]*GraphEdge{p.edge}, edges...)
		curr = p.nodeID
	}
	nodeIDs = append([]string{fromID}, nodeIDs...)

	return nodeIDs, edges
}

func (gt *GraphTraverser) loadPathNodes(ids []string) ([]*GraphNode, error) {
	nodes := make([]*GraphNode, 0, len(ids))
	for _, id := range ids {
		node, err := gt.ns.GetNode(id)
		if err != nil {
			return nil, fmt.Errorf("load node %s: %w", id, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// GetConnectedComponent returns all nodes reachable from startID.
func (gt *GraphTraverser) GetConnectedComponent(startID string, opts *TraversalOptions) ([]*GraphNode, error) {
	if opts == nil {
		opts = &TraversalOptions{MaxDepth: 100, Direction: DirectionBoth}
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 100
	}

	return gt.collectComponent(startID, opts)
}

func (gt *GraphTraverser) collectComponent(startID string, opts *TraversalOptions) ([]*GraphNode, error) {
	visited := make(map[string]bool)
	queue := list.New()

	queue.PushBack(bfsNode{id: startID, depth: 0})
	visited[startID] = true

	var nodeIDs []string
	for queue.Len() > 0 {
		curr := queue.Remove(queue.Front()).(bfsNode)
		nodeIDs = append(nodeIDs, curr.id)

		if curr.depth >= opts.MaxDepth {
			continue
		}

		gt.expandComponent(curr, opts, visited, queue)
	}

	return gt.loadNodesByIDs(nodeIDs)
}

func (gt *GraphTraverser) expandComponent(curr bfsNode, opts *TraversalOptions, visited map[string]bool, queue *list.List) {
	edges, _ := gt.getRelevantEdges(curr.id, opts)
	for _, e := range edges {
		neighborID := gt.getOtherNodeID(curr.id, e)
		if !visited[neighborID] {
			visited[neighborID] = true
			queue.PushBack(bfsNode{id: neighborID, depth: curr.depth + 1})
		}
	}
}

// GetSubgraph returns a subgraph within maxDepth hops from the start node.
func (gt *GraphTraverser) GetSubgraph(startID string, maxDepth int) (*Subgraph, error) {
	if maxDepth <= 0 {
		maxDepth = 2
	}

	return gt.buildSubgraph(startID, maxDepth)
}

// Subgraph represents a subset of the graph.
type Subgraph struct {
	Nodes []*GraphNode
	Edges []*GraphEdge
}

func (gt *GraphTraverser) buildSubgraph(startID string, maxDepth int) (*Subgraph, error) {
	visited := make(map[string]bool)
	edgeSeen := make(map[string]bool)
	queue := list.New()

	queue.PushBack(bfsNode{id: startID, depth: 0})
	visited[startID] = true

	var nodeIDs []string
	var edges []*GraphEdge

	for queue.Len() > 0 {
		curr := queue.Remove(queue.Front()).(bfsNode)
		nodeIDs = append(nodeIDs, curr.id)

		if curr.depth >= maxDepth {
			continue
		}

		newEdges := gt.expandSubgraph(curr, visited, edgeSeen, queue)
		edges = append(edges, newEdges...)
	}

	nodes, err := gt.loadNodesByIDs(nodeIDs)
	if err != nil {
		return nil, err
	}

	return &Subgraph{Nodes: nodes, Edges: edges}, nil
}

func (gt *GraphTraverser) expandSubgraph(curr bfsNode, visited, edgeSeen map[string]bool, queue *list.List) []*GraphEdge {
	var newEdges []*GraphEdge
	opts := &TraversalOptions{Direction: DirectionBoth}
	edges, _ := gt.getRelevantEdges(curr.id, opts)

	for _, e := range edges {
		edgeKey := fmt.Sprintf("%d", e.ID)
		if edgeSeen[edgeKey] {
			continue
		}
		edgeSeen[edgeKey] = true
		newEdges = append(newEdges, e)

		neighborID := gt.getOtherNodeID(curr.id, e)
		if !visited[neighborID] {
			visited[neighborID] = true
			queue.PushBack(bfsNode{id: neighborID, depth: curr.depth + 1})
		}
	}
	return newEdges
}
