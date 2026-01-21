// Package storage provides mmap-based storage types for the Vamana index.
package storage

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Header sizes - all headers are 16 bytes for page alignment.
const (
	HeaderSize = 16
)

var (
	ErrInvalidHeaderSize  = errors.New("invalid header size: expected 16 bytes")
	ErrInvalidMagic       = errors.New("invalid magic bytes")
	ErrUnsupportedVersion = errors.New("unsupported format version")
	ErrChecksumMismatch   = errors.New("checksum mismatch: data corrupted")
	ErrIDTooLong          = errors.New("external ID exceeds 65535 bytes")
)

// VectorHeader describes the binary layout of vector storage metadata.
//
// Binary layout (16 bytes, little-endian):
//
//	Offset  Size  Field
//	0       4     dim     (uint32) - vector dimensionality
//	4       8     count   (uint64) - number of vectors stored
//	12      4     flags   (uint32) - reserved flags for future use
type VectorHeader struct {
	Dim   uint32 // Vector dimensionality
	Count uint64 // Number of vectors stored
	Flags uint32 // Reserved flags
}

// MarshalBinary encodes the VectorHeader to a 16-byte binary representation.
func (h *VectorHeader) MarshalBinary() ([]byte, error) {
	buf := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], h.Dim)
	binary.LittleEndian.PutUint64(buf[4:12], h.Count)
	binary.LittleEndian.PutUint32(buf[12:16], h.Flags)
	return buf, nil
}

// UnmarshalBinary decodes a 16-byte binary representation into the VectorHeader.
func (h *VectorHeader) UnmarshalBinary(data []byte) error {
	if len(data) != HeaderSize {
		return ErrInvalidHeaderSize
	}
	h.Dim = binary.LittleEndian.Uint32(data[0:4])
	h.Count = binary.LittleEndian.Uint64(data[4:12])
	h.Flags = binary.LittleEndian.Uint32(data[12:16])
	return nil
}

// GraphHeader describes the binary layout of graph storage metadata.
//
// Binary layout (16 bytes, little-endian):
//
//	Offset  Size  Field
//	0       4     R       (uint32) - maximum out-degree per node
//	4       8     count   (uint64) - number of nodes in the graph
//	12      4     flags   (uint32) - reserved flags for future use
type GraphHeader struct {
	R     uint32 // Maximum out-degree (edges per node)
	Count uint64 // Number of nodes in the graph
	Flags uint32 // Reserved flags
}

// MarshalBinary encodes the GraphHeader to a 16-byte binary representation.
func (h *GraphHeader) MarshalBinary() ([]byte, error) {
	buf := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], h.R)
	binary.LittleEndian.PutUint64(buf[4:12], h.Count)
	binary.LittleEndian.PutUint32(buf[12:16], h.Flags)
	return buf, nil
}

// UnmarshalBinary decodes a 16-byte binary representation into the GraphHeader.
func (h *GraphHeader) UnmarshalBinary(data []byte) error {
	if len(data) != HeaderSize {
		return ErrInvalidHeaderSize
	}
	h.R = binary.LittleEndian.Uint32(data[0:4])
	h.Count = binary.LittleEndian.Uint64(data[4:12])
	h.Flags = binary.LittleEndian.Uint32(data[12:16])
	return nil
}

// LabelHeader describes the binary layout of label storage metadata.
//
// Binary layout (16 bytes, little-endian):
//
//	Offset  Size  Field
//	0       1     domainBits (uint8)  - bits for domain encoding
//	1       2     typeBits   (uint16) - bits for type encoding
//	3       1     _padding   (uint8)  - alignment padding
//	4       8     count      (uint64) - number of labels stored
//	12      4     flags      (uint32) - reserved flags for future use
type LabelHeader struct {
	DomainBits uint8  // Bits for domain encoding
	TypeBits   uint16 // Bits for type encoding
	Count      uint64 // Number of labels stored
	Flags      uint32 // Reserved flags
}

// MarshalBinary encodes the LabelHeader to a 16-byte binary representation.
func (h *LabelHeader) MarshalBinary() ([]byte, error) {
	buf := make([]byte, HeaderSize)
	buf[0] = h.DomainBits
	binary.LittleEndian.PutUint16(buf[1:3], h.TypeBits)
	// buf[3] is padding, left as zero
	binary.LittleEndian.PutUint64(buf[4:12], h.Count)
	binary.LittleEndian.PutUint32(buf[12:16], h.Flags)
	return buf, nil
}

// UnmarshalBinary decodes a 16-byte binary representation into the LabelHeader.
func (h *LabelHeader) UnmarshalBinary(data []byte) error {
	if len(data) != HeaderSize {
		return ErrInvalidHeaderSize
	}
	h.DomainBits = data[0]
	h.TypeBits = binary.LittleEndian.Uint16(data[1:3])
	// data[3] is padding, ignored
	h.Count = binary.LittleEndian.Uint64(data[4:12])
	h.Flags = binary.LittleEndian.Uint32(data[12:16])
	return nil
}

// SnapshotMetadataSize is the fixed size of SnapshotMetadata in bytes.
const SnapshotMetadataSize = 32

// SnapshotMetadata describes a point-in-time snapshot of the index state.
//
// Binary layout (32 bytes, little-endian):
//
//	Offset  Size  Field
//	0       4     version       (uint32) - snapshot format version
//	4       8     timestamp     (uint64) - unix timestamp in nanoseconds
//	12      8     vectorCount   (uint64) - number of vectors at snapshot time
//	20      4     graphChecksum (uint32) - CRC32 checksum of graph data
//	24      8     _reserved     (uint64) - reserved for future use
type SnapshotMetadata struct {
	Version       uint32 // Snapshot format version
	Timestamp     uint64 // Unix timestamp in nanoseconds
	VectorCount   uint64 // Number of vectors at snapshot time
	GraphChecksum uint32 // CRC32 checksum of graph data
}

// MarshalBinary encodes the SnapshotMetadata to a 32-byte binary representation.
func (m *SnapshotMetadata) MarshalBinary() ([]byte, error) {
	buf := make([]byte, SnapshotMetadataSize)
	binary.LittleEndian.PutUint32(buf[0:4], m.Version)
	binary.LittleEndian.PutUint64(buf[4:12], m.Timestamp)
	binary.LittleEndian.PutUint64(buf[12:20], m.VectorCount)
	binary.LittleEndian.PutUint32(buf[20:24], m.GraphChecksum)
	// buf[24:32] is reserved, left as zero
	return buf, nil
}

// UnmarshalBinary decodes a 32-byte binary representation into the SnapshotMetadata.
func (m *SnapshotMetadata) UnmarshalBinary(data []byte) error {
	if len(data) != SnapshotMetadataSize {
		return errors.New("invalid snapshot metadata size: expected 32 bytes")
	}
	m.Version = binary.LittleEndian.Uint32(data[0:4])
	m.Timestamp = binary.LittleEndian.Uint64(data[4:12])
	m.VectorCount = binary.LittleEndian.Uint64(data[12:20])
	m.GraphChecksum = binary.LittleEndian.Uint32(data[20:24])
	// data[24:32] is reserved, ignored
	return nil
}

// ComputeChecksum calculates a CRC32 checksum for integrity verification.
// Uses the IEEE polynomial for compatibility.
func ComputeChecksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
