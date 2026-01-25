package graph

import (
	"encoding/binary"
	"math"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

type Edge struct {
	SourceID  uint32
	TargetID  uint32
	Type      vectorgraphdb.EdgeType
	Weight    float32
	SessionID string
	AgentID   uint16
	CreatedAt uint64
	UpdatedAt uint64
}

// Fixed portion: SourceID(4) + TargetID(4) + Type(1) + Weight(4) + AgentID(2) + CreatedAt(8) + UpdatedAt(8) + SessionIDLen(2) = 33 bytes
const edgeFixedSize = 4 + 4 + 1 + 4 + 2 + 8 + 8 + 2

// MarshalBinary format:
// [SourceID:4][TargetID:4][Type:1][Weight:4][AgentID:2][CreatedAt:8][UpdatedAt:8][SessionIDLen:2][SessionID:N]
func (e *Edge) MarshalBinary() []byte {
	sessionBytes := []byte(e.SessionID)
	buf := make([]byte, edgeFixedSize+len(sessionBytes))

	binary.LittleEndian.PutUint32(buf[0:4], e.SourceID)
	binary.LittleEndian.PutUint32(buf[4:8], e.TargetID)
	buf[8] = uint8(e.Type)
	binary.LittleEndian.PutUint32(buf[9:13], math.Float32bits(e.Weight))
	binary.LittleEndian.PutUint16(buf[13:15], e.AgentID)
	binary.LittleEndian.PutUint64(buf[15:23], e.CreatedAt)
	binary.LittleEndian.PutUint64(buf[23:31], e.UpdatedAt)
	binary.LittleEndian.PutUint16(buf[31:33], uint16(len(sessionBytes)))
	copy(buf[33:], sessionBytes)

	return buf
}

func (e *Edge) UnmarshalBinary(data []byte) error {
	if len(data) < edgeFixedSize {
		return errInvalidEdgeData
	}

	e.SourceID = binary.LittleEndian.Uint32(data[0:4])
	e.TargetID = binary.LittleEndian.Uint32(data[4:8])
	e.Type = vectorgraphdb.EdgeType(data[8])
	e.Weight = math.Float32frombits(binary.LittleEndian.Uint32(data[9:13]))
	e.AgentID = binary.LittleEndian.Uint16(data[13:15])
	e.CreatedAt = binary.LittleEndian.Uint64(data[15:23])
	e.UpdatedAt = binary.LittleEndian.Uint64(data[23:31])

	sessionLen := binary.LittleEndian.Uint16(data[31:33])
	if len(data) < edgeFixedSize+int(sessionLen) {
		return errInvalidEdgeData
	}
	e.SessionID = string(data[33 : 33+sessionLen])

	return nil
}
