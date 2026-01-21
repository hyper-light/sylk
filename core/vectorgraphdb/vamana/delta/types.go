// Package delta provides types for the Vamana delta layer, which buffers
// graph modifications before compaction into the main index.
package delta

import (
	"encoding/binary"
	"fmt"
	"io"
)

// DeltaOperation represents the type of modification to apply to the graph.
type DeltaOperation uint8

const (
	// Insert adds a new node to the graph.
	Insert DeltaOperation = iota
	// Delete removes an existing node from the graph.
	Delete
	// Update modifies an existing node's vector or neighbors.
	Update
)

// String returns the string representation of a DeltaOperation.
func (op DeltaOperation) String() string {
	switch op {
	case Insert:
		return "Insert"
	case Delete:
		return "Delete"
	case Update:
		return "Update"
	default:
		return fmt.Sprintf("Unknown(%d)", op)
	}
}

// DeltaEntry represents a single modification in the delta layer.
// It captures all information needed to apply or rollback a graph change.
type DeltaEntry struct {
	// InternalID is the graph-internal identifier for this node.
	InternalID uint32
	// Vector is the embedding vector for this node.
	Vector []float32
	// Neighbors contains the internal IDs of connected nodes.
	Neighbors []uint32
	// Domain identifies the logical partition this node belongs to.
	Domain uint8
	// NodeType classifies the node for type-aware graph operations.
	NodeType uint16
}

// Size returns the number of bytes needed to serialize this entry.
// Layout: InternalID(4) + VectorLen(4) + Vector(len*4) + NeighborsLen(4) + Neighbors(len*4) + Domain(1) + NodeType(2)
func (e *DeltaEntry) Size() int {
	return 4 + 4 + len(e.Vector)*4 + 4 + len(e.Neighbors)*4 + 1 + 2
}

// WriteTo serializes the DeltaEntry to a binary WAL format.
// Returns the number of bytes written and any error encountered.
func (e *DeltaEntry) WriteTo(w io.Writer) (int64, error) {
	var written int64

	// Write InternalID
	if err := binary.Write(w, binary.LittleEndian, e.InternalID); err != nil {
		return written, fmt.Errorf("write internal id: %w", err)
	}
	written += 4

	// Write Vector length and data
	vectorLen := uint32(len(e.Vector))
	if err := binary.Write(w, binary.LittleEndian, vectorLen); err != nil {
		return written, fmt.Errorf("write vector length: %w", err)
	}
	written += 4

	for _, v := range e.Vector {
		if err := binary.Write(w, binary.LittleEndian, v); err != nil {
			return written, fmt.Errorf("write vector element: %w", err)
		}
		written += 4
	}

	// Write Neighbors length and data
	neighborsLen := uint32(len(e.Neighbors))
	if err := binary.Write(w, binary.LittleEndian, neighborsLen); err != nil {
		return written, fmt.Errorf("write neighbors length: %w", err)
	}
	written += 4

	for _, n := range e.Neighbors {
		if err := binary.Write(w, binary.LittleEndian, n); err != nil {
			return written, fmt.Errorf("write neighbor: %w", err)
		}
		written += 4
	}

	// Write Domain
	if err := binary.Write(w, binary.LittleEndian, e.Domain); err != nil {
		return written, fmt.Errorf("write domain: %w", err)
	}
	written += 1

	// Write NodeType
	if err := binary.Write(w, binary.LittleEndian, e.NodeType); err != nil {
		return written, fmt.Errorf("write node type: %w", err)
	}
	written += 2

	return written, nil
}

// ReadFrom deserializes a DeltaEntry from binary WAL format.
// Returns the number of bytes read and any error encountered.
func (e *DeltaEntry) ReadFrom(r io.Reader) (int64, error) {
	var read int64

	// Read InternalID
	if err := binary.Read(r, binary.LittleEndian, &e.InternalID); err != nil {
		return read, fmt.Errorf("read internal id: %w", err)
	}
	read += 4

	// Read Vector
	var vectorLen uint32
	if err := binary.Read(r, binary.LittleEndian, &vectorLen); err != nil {
		return read, fmt.Errorf("read vector length: %w", err)
	}
	read += 4

	e.Vector = make([]float32, vectorLen)
	for i := range e.Vector {
		if err := binary.Read(r, binary.LittleEndian, &e.Vector[i]); err != nil {
			return read, fmt.Errorf("read vector element %d: %w", i, err)
		}
		read += 4
	}

	// Read Neighbors
	var neighborsLen uint32
	if err := binary.Read(r, binary.LittleEndian, &neighborsLen); err != nil {
		return read, fmt.Errorf("read neighbors length: %w", err)
	}
	read += 4

	e.Neighbors = make([]uint32, neighborsLen)
	for i := range e.Neighbors {
		if err := binary.Read(r, binary.LittleEndian, &e.Neighbors[i]); err != nil {
			return read, fmt.Errorf("read neighbor %d: %w", i, err)
		}
		read += 4
	}

	// Read Domain
	if err := binary.Read(r, binary.LittleEndian, &e.Domain); err != nil {
		return read, fmt.Errorf("read domain: %w", err)
	}
	read += 1

	// Read NodeType
	if err := binary.Read(r, binary.LittleEndian, &e.NodeType); err != nil {
		return read, fmt.Errorf("read node type: %w", err)
	}
	read += 2

	return read, nil
}

// DeltaConfig controls the behavior of the delta layer.
type DeltaConfig struct {
	// MaxSize is the maximum number of entries before forced compaction.
	MaxSize int
	// CompactionThreshold triggers compaction when (dirty entries / MaxSize) exceeds this ratio.
	CompactionThreshold float64
	// WALPath is the filesystem path for the write-ahead log.
	WALPath string
}

// DefaultDeltaConfig returns a DeltaConfig with sensible defaults.
func DefaultDeltaConfig() DeltaConfig {
	return DeltaConfig{
		MaxSize:             10000,
		CompactionThreshold: 0.8,
		WALPath:             "",
	}
}
