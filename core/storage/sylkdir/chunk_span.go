package sylkdir

import (
	"encoding/binary"
	"fmt"
)

// ChunkSpan encodes a chunk's exact location within a parent document.
//
// Binary format (24 bytes, cache-line aligned):
//
//	[DocRef:4][ChunkSeq:2][ByteStart:4][ByteEnd:4][LineStart:4][LineEnd:4][Flags:2]
//
// Flags layout (16 bits):
//
//	Bits 0-1:  OverlapKind  (none / prefix / suffix / both)
//	Bits 2-5:  ContentClass (source_code, markdown, web, llm, etc.)
//	Bits 6-7:  BoundaryKind (sentence, heading, code_block, structural)
//	Bits 8-15: Reserved (zero)
const ChunkSpanSize = 24

// ChunkSpan represents a chunk's position within its parent document.
// DocRef is the same uint32 key used by DocIDMap for the parent document.
// ChunkSeq is the 0-indexed position within the sequence of chunks for that document.
type ChunkSpan struct {
	DocRef    uint32 // Parent document ID (from DocIDMap)
	ChunkSeq uint16 // 0-indexed sequence number within this document
	ByteStart uint32 // Byte offset of chunk start in parent content
	ByteEnd   uint32 // Byte offset of chunk end (exclusive) in parent content
	LineStart uint32 // 1-indexed line number of chunk start
	LineEnd   uint32 // 1-indexed line number of chunk end (inclusive)
	Flags     uint16 // Packed: OverlapKind(2) + ContentClass(4) + BoundaryKind(2) + reserved(8)
}

// OverlapKind describes how a chunk overlaps with its neighbors.
type OverlapKind uint8

const (
	OverlapNone   OverlapKind = 0
	OverlapPrefix OverlapKind = 1 // Chunk starts with overlapping bytes from previous chunk
	OverlapSuffix OverlapKind = 2 // Chunk ends with overlapping bytes for next chunk
	OverlapBoth   OverlapKind = 3 // Both prefix and suffix overlap
)

// BoundaryKind describes the type of boundary that defines a chunk's edges.
type BoundaryKind uint8

const (
	BoundarySentence   BoundaryKind = 0 // Split at sentence or paragraph boundary
	BoundaryHeading    BoundaryKind = 1 // Split at heading boundary
	BoundaryCodeBlock  BoundaryKind = 2 // Split at code block boundary
	BoundaryStructural BoundaryKind = 3 // Split at structural boundary (function, type, etc.)
)

// MarshalBinary encodes the ChunkSpan to a fixed-size binary record.
func (cs *ChunkSpan) MarshalBinary() []byte {
	buf := make([]byte, ChunkSpanSize)
	binary.LittleEndian.PutUint32(buf[0:4], cs.DocRef)
	binary.LittleEndian.PutUint16(buf[4:6], cs.ChunkSeq)
	binary.LittleEndian.PutUint32(buf[6:10], cs.ByteStart)
	binary.LittleEndian.PutUint32(buf[10:14], cs.ByteEnd)
	binary.LittleEndian.PutUint32(buf[14:18], cs.LineStart)
	binary.LittleEndian.PutUint32(buf[18:22], cs.LineEnd)
	binary.LittleEndian.PutUint16(buf[22:24], cs.Flags)
	return buf
}

// UnmarshalBinary decodes a ChunkSpan from a binary record.
func (cs *ChunkSpan) UnmarshalBinary(data []byte) error {
	if len(data) < ChunkSpanSize {
		return fmt.Errorf("sylkdir: chunk span data too short: %d < %d", len(data), ChunkSpanSize)
	}
	cs.DocRef = binary.LittleEndian.Uint32(data[0:4])
	cs.ChunkSeq = binary.LittleEndian.Uint16(data[4:6])
	cs.ByteStart = binary.LittleEndian.Uint32(data[6:10])
	cs.ByteEnd = binary.LittleEndian.Uint32(data[10:14])
	cs.LineStart = binary.LittleEndian.Uint32(data[14:18])
	cs.LineEnd = binary.LittleEndian.Uint32(data[18:22])
	cs.Flags = binary.LittleEndian.Uint16(data[22:24])
	return nil
}

// PackFlags constructs the Flags field from individual components.
func PackFlags(overlap OverlapKind, cc ContentClass, bk BoundaryKind) uint16 {
	return uint16(overlap&0x3) |
		uint16(cc&0xF)<<2 |
		uint16(bk&0x3)<<6
}

// UnpackOverlap extracts the OverlapKind from Flags.
func (cs *ChunkSpan) UnpackOverlap() OverlapKind {
	return OverlapKind(cs.Flags & 0x3)
}

// UnpackContentClass extracts the ContentClass from Flags.
func (cs *ChunkSpan) UnpackContentClass() ContentClass {
	return ContentClass((cs.Flags >> 2) & 0xF)
}

// UnpackBoundaryKind extracts the BoundaryKind from Flags.
func (cs *ChunkSpan) UnpackBoundaryKind() BoundaryKind {
	return BoundaryKind((cs.Flags >> 6) & 0x3)
}

// ByteLen returns the byte length of the chunk (ByteEnd - ByteStart).
func (cs *ChunkSpan) ByteLen() uint32 {
	return cs.ByteEnd - cs.ByteStart
}

// ChunkRef pairs a node ID with its ChunkSpan. Used for batch operations.
type ChunkRef struct {
	NodeID uint32
	Span   ChunkSpan
}

// ChunkRefRecordSize is the size of a ChunkRef record on disk: NodeID(4) + ChunkSpan(24).
const ChunkRefRecordSize = 4 + ChunkSpanSize

// MarshalBinary encodes a ChunkRef to a fixed-size binary record.
func (cr *ChunkRef) MarshalBinary() []byte {
	buf := make([]byte, ChunkRefRecordSize)
	binary.LittleEndian.PutUint32(buf[0:4], cr.NodeID)
	copy(buf[4:], cr.Span.MarshalBinary())
	return buf
}

// UnmarshalBinary decodes a ChunkRef from a binary record.
func (cr *ChunkRef) UnmarshalBinary(data []byte) error {
	if len(data) < ChunkRefRecordSize {
		return fmt.Errorf("sylkdir: chunk ref data too short: %d < %d", len(data), ChunkRefRecordSize)
	}
	cr.NodeID = binary.LittleEndian.Uint32(data[0:4])
	return cr.Span.UnmarshalBinary(data[4:])
}
