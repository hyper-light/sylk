// Package vamana implements the DiskANN/Vamana algorithm for approximate nearest
// neighbor search. Vamana uses a single-layer graph with Robust Neighborhood Graph
// (RNG) pruning to achieve high recall with bounded degree, making it suitable for
// both in-memory and disk-based deployments.
//
// Key algorithm concepts:
//   - Single-layer graph: Unlike HNSW, Vamana uses one graph layer with all nodes
//   - RNG pruning: Alpha parameter controls neighbor diversity vs proximity tradeoff
//   - Greedy search: Beam search from medoid entry point to find nearest neighbors
//   - R parameter: Maximum out-degree (connections per node)
//   - L parameter: Search list size during construction and query
//
// Reference: "DiskANN: Fast Accurate Billion-point Nearest Neighbor Search on a
// Single Node" (Subramanya et al., NeurIPS 2019)
package vamana

import (
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// InternalID is a compact 32-bit identifier used for memory-efficient graph storage.
// Each node in the Vamana graph is assigned a unique InternalID that maps to its
// external NodeID string. Using uint32 instead of strings reduces memory footprint
// by approximately 8x for typical UUID-style node identifiers.
type InternalID uint32

// InvalidInternalID represents an unassigned or invalid internal identifier.
// Used as a sentinel value for uninitialized nodes or error conditions.
const InvalidInternalID InternalID = 0

// NodeID is the external string identifier that maps to an InternalID.
// This is typically a UUID or content-addressed hash used by the broader system.
type NodeID = string

// VamanaConfig holds the configuration parameters for a Vamana index.
// These parameters control the tradeoff between index quality, construction time,
// and query performance.
type VamanaConfig struct {
	// R is the maximum out-degree (number of neighbors) per node.
	// Higher values improve recall but increase memory and construction time.
	// Typical range: 32-128. Default: 64.
	R int `json:"r"`

	// L is the search list size used during both construction and query.
	// Higher values improve recall but increase latency.
	// Must be >= R for good index quality. Default: 100.
	L int `json:"l"`

	// Alpha is the RNG pruning parameter that controls neighbor diversity.
	// Values > 1.0 favor more diverse (longer-range) connections.
	// Typical range: 1.0-1.5. Default: 1.2.
	Alpha float64 `json:"alpha"`
}

// DefaultConfig returns a VamanaConfig with standard parameter values.
// These defaults are suitable for most workloads and provide a good balance
// between recall and performance:
//   - R=64: Moderate degree for good recall with reasonable memory
//   - L=100: Search list larger than R for quality construction
//   - Alpha=1.2: Slight preference for diverse connections
func DefaultConfig() VamanaConfig {
	return VamanaConfig{
		R:     64,
		L:     100,
		Alpha: 1.2,
	}
}

// Validate checks that the configuration parameters are within acceptable bounds.
// Returns true if the configuration is valid for index construction.
func (c VamanaConfig) Validate() bool {
	return c.R > 0 && c.L >= c.R && c.Alpha >= 1.0
}

// SearchResult represents a single result from a Vamana nearest neighbor search.
// Results are returned sorted by similarity in descending order.
type SearchResult struct {
	// InternalID is the compact graph identifier of the matched node.
	InternalID InternalID `json:"internal_id"`

	// Similarity is the cosine similarity score in range [-1, 1].
	// Higher values indicate more similar vectors.
	Similarity float64 `json:"similarity"`

	// Domain categorizes the knowledge domain of the matched node.
	Domain vectorgraphdb.Domain `json:"domain"`

	// NodeType specifies the type of node within its domain.
	NodeType vectorgraphdb.NodeType `json:"node_type"`
}

// SearchFilter specifies filtering criteria for search operations.
// Filters are applied post-search to exclude non-matching candidates.
// All filter fields are optional; nil or empty values match all nodes.
type SearchFilter struct {
	// Domains restricts results to nodes in the specified domains.
	// Empty slice matches all domains.
	Domains []vectorgraphdb.Domain `json:"domains,omitempty"`

	// NodeTypes restricts results to nodes of the specified types.
	// Empty slice matches all node types.
	NodeTypes []vectorgraphdb.NodeType `json:"node_types,omitempty"`

	// MinSimilarity excludes results below this similarity threshold.
	// Value of 0 disables the threshold. Range: [-1, 1].
	MinSimilarity float64 `json:"min_similarity,omitempty"`
}

// Matches returns true if the given domain, node type, and similarity
// satisfy all filter criteria.
func (f *SearchFilter) Matches(domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType, similarity float64) bool {
	if f == nil {
		return true
	}
	if f.MinSimilarity > 0 && similarity < f.MinSimilarity {
		return false
	}
	if len(f.Domains) > 0 && !containsDomain(f.Domains, domain) {
		return false
	}
	if len(f.NodeTypes) > 0 && !containsNodeType(f.NodeTypes, nodeType) {
		return false
	}
	return true
}

// containsDomain checks if the domain slice contains the target domain.
func containsDomain(domains []vectorgraphdb.Domain, target vectorgraphdb.Domain) bool {
	for _, d := range domains {
		if d == target {
			return true
		}
	}
	return false
}

// containsNodeType checks if the node type slice contains the target type.
func containsNodeType(types []vectorgraphdb.NodeType, target vectorgraphdb.NodeType) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}
