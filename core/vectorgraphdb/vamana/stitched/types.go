// Package stitched implements filtered Vamana graph structures using the
// Stitched Vamana algorithm for label-filtered approximate nearest neighbor search.
//
// The stitched approach partitions the graph into per-label subgraphs while
// maintaining cross-subgraph connectivity through "stitching" edges, enabling
// efficient filtered queries without full graph traversal.
package stitched

// LabelSet represents a packed label combination of Domain and NodeType.
// Layout: 3 bytes total (24 bits)
//   - Byte 0: Domain (8 bits, max 255)
//   - Bytes 1-2: NodeType (16 bits, big-endian, max 65535)
//
// This compact representation enables efficient storage and hashing for
// subgraph partitioning while supporting the full range of domain/nodetype
// combinations used in the vector graph.
type LabelSet struct {
	Domain   uint8
	NodeType uint16
}

// Pack encodes the LabelSet into a 3-byte array.
// The encoding is deterministic and suitable for use as a map key or hash input.
func (ls LabelSet) Pack() [3]byte {
	return [3]byte{
		ls.Domain,
		byte(ls.NodeType >> 8),   // high byte
		byte(ls.NodeType & 0xFF), // low byte
	}
}

// Unpack decodes a 3-byte array into a LabelSet.
func Unpack(data [3]byte) LabelSet {
	return LabelSet{
		Domain:   data[0],
		NodeType: uint16(data[1])<<8 | uint16(data[2]),
	}
}

// SubgraphID uniquely identifies a subgraph partition.
// Maximum of 65535 distinct label combinations are supported.
type SubgraphID uint16

// ToSubgraphID derives a deterministic SubgraphID from the LabelSet.
// The mapping uses a bijective encoding that preserves uniqueness:
// SubgraphID = (Domain << 8) XOR NodeType
//
// This ensures different LabelSets produce different SubgraphIDs while
// distributing values across the uint16 range for balanced partitioning.
func (ls LabelSet) ToSubgraphID() SubgraphID {
	// XOR spreads the domain bits across the ID space, reducing clustering
	// when NodeType values are sequential within the same domain.
	return SubgraphID(uint16(ls.Domain)<<8 ^ ls.NodeType)
}

// StitchedConfig holds configuration parameters for stitched Vamana graph construction.
type StitchedConfig struct {
	// StitchDegree is the maximum number of cross-subgraph edges per node.
	// Higher values improve recall for filtered queries spanning multiple
	// labels but increase memory usage and construction time.
	StitchDegree int

	// FilteredSearchL is the search list size (beam width) used during
	// filtered queries. Larger values improve recall at the cost of
	// increased query latency.
	FilteredSearchL int
}

// DefaultStitchedConfig returns a StitchedConfig with sensible defaults
// balancing recall and performance for typical workloads.
func DefaultStitchedConfig() StitchedConfig {
	return StitchedConfig{
		StitchDegree:    32,
		FilteredSearchL: 50,
	}
}

// SubgraphMetadata contains metadata for a single label-partitioned subgraph.
type SubgraphMetadata struct {
	// ID uniquely identifies this subgraph partition.
	ID SubgraphID

	// Labels is the LabelSet that defines membership in this subgraph.
	Labels LabelSet

	// NodeCount is the number of nodes belonging to this subgraph.
	NodeCount uint32

	// Medoid is the node ID of the subgraph's centroid (most central node).
	// Search within this subgraph starts from the medoid for optimal traversal.
	// A value of 0 with NodeCount > 0 indicates the medoid has not been computed.
	Medoid uint32
}
