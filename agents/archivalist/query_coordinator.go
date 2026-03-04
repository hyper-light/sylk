package archivalist

import (
	"context"

	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/blevesearch/bleve/v2"
)

// bleveIndexAdapter wraps the archivalist's BleveIndex (raw bleve search)
// to implement the query.BleveIndex interface (context-aware search).
type bleveIndexAdapter struct {
	index BleveIndex
}

func (a *bleveIndexAdapter) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return a.index.Search(req)
}

// embeddingVectorAdapter wraps the archivalist's EmbeddingStore
// to implement the query.VectorIndex interface.
type embeddingVectorAdapter struct {
	store *EmbeddingStore
}

func (a *embeddingVectorAdapter) Search(vector []float32, k int) ([]string, []float32, error) {
	results, err := a.store.SearchSimilar(vector, SearchOptions{Limit: k})
	if err != nil {
		return nil, nil, err
	}

	ids := make([]string, len(results))
	distances := make([]float32, len(results))
	for i, r := range results {
		ids[i] = r.Entry.ID
		distances[i] = 1.0 - float32(r.Score) // convert similarity to distance
	}
	return ids, distances, nil
}

// graphEdgeAdapter wraps a VectorGraphDBHybridAdapter to implement
// the query.EdgeQuerier interface by converting edge types.
type graphEdgeAdapter struct {
	adapter *vectorgraphdb.VectorGraphDBHybridAdapter
}

func (a *graphEdgeAdapter) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]query.Edge, error) {
	edges, err := a.adapter.GetOutgoingEdges(nodeID, edgeTypes)
	if err != nil {
		return nil, err
	}
	return convertEdges(edges), nil
}

func (a *graphEdgeAdapter) GetIncomingEdges(nodeID string, edgeTypes []string) ([]query.Edge, error) {
	edges, err := a.adapter.GetIncomingEdges(nodeID, edgeTypes)
	if err != nil {
		return nil, err
	}
	return convertEdges(edges), nil
}

func (a *graphEdgeAdapter) GetNodesByPattern(pattern *query.NodeMatcher) ([]string, error) {
	if pattern == nil {
		return a.adapter.GetNodesByPattern(nil)
	}
	np := &vectorgraphdb.NodePattern{
		EntityType:  pattern.EntityType,
		NamePattern: pattern.NamePattern,
		Properties:  pattern.Properties,
	}
	return a.adapter.GetNodesByPattern(np)
}

func convertEdges(vgEdges []vectorgraphdb.TraversalEdge) []query.Edge {
	qEdges := make([]query.Edge, len(vgEdges))
	for i, e := range vgEdges {
		qEdges[i] = query.Edge{
			ID:       e.ID,
			SourceID: e.SourceID,
			TargetID: e.TargetID,
			EdgeType: e.EdgeType,
			Weight:   e.Weight,
		}
	}
	return qEdges
}

// QueryCoordinatorConfig configures the HybridQueryCoordinator wiring.
type QueryCoordinatorConfig struct {
	BleveIndex     BleveIndex
	EmbeddingStore *EmbeddingStore
	GraphAdapter   *vectorgraphdb.VectorGraphDBHybridAdapter
}

// buildQueryCoordinator creates a HybridQueryCoordinator from archivalist components.
// All components are optional — missing components are nil and the coordinator
// simply won't query that modality.
func buildQueryCoordinator(cfg QueryCoordinatorConfig) *query.HybridQueryCoordinator {
	var bleveSearcher *query.BleveSearcher
	if cfg.BleveIndex != nil {
		bleveSearcher = query.NewBleveSearcher(&bleveIndexAdapter{index: cfg.BleveIndex})
	}

	var vectorSearcher *query.VectorSearcher
	if cfg.GraphAdapter != nil {
		// Prefer graph adapter's vector search (backed by HNSW via VectorGraphDB)
		vectorSearcher = query.NewVectorSearcher(cfg.GraphAdapter)
	} else if cfg.EmbeddingStore != nil {
		vectorSearcher = query.NewVectorSearcher(&embeddingVectorAdapter{store: cfg.EmbeddingStore})
	}

	var graphTraverser *query.GraphTraverser
	if cfg.GraphAdapter != nil {
		graphTraverser = query.NewGraphTraverser(&graphEdgeAdapter{adapter: cfg.GraphAdapter})
	}

	return query.NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)
}
