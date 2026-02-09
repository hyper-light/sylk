package sylkdir

import (
	"encoding/binary"
	"errors"
	"math"
)

// WAL codec errors.
var (
	ErrWALCodecTruncated = errors.New("sylkdir: WAL data truncated")
	ErrWALCodecOverflow  = errors.New("sylkdir: WAL data exceeds uint16 string limit")
)

// WALNodeData is the WAL payload for node insert/delete operations.
// Wire format: [NodeID:4][Domain:1][Type:1][KeyLen:2][Key][NameLen:2][Name][PathLen:2][Path]
type WALNodeData struct {
	NodeID   uint32
	Domain   uint8
	NodeType uint8
	Key      string
	Name     string
	Path     string
}

// walNodeFixedSize is the fixed portion of a WALNodeData encoding.
// NodeID(4) + Domain(1) + Type(1) + KeyLen(2) + NameLen(2) + PathLen(2) = 12
const walNodeFixedSize = 12

// EncodeNodeData serializes a WALNodeData to bytes.
func EncodeNodeData(d *WALNodeData) ([]byte, error) {
	totalSize := walNodeFixedSize + len(d.Key) + len(d.Name) + len(d.Path)
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint32(buf[0:4], d.NodeID)
	buf[4] = d.Domain
	buf[5] = d.NodeType

	offset := 6
	offset = walWriteStr(buf, offset, d.Key)
	offset = walWriteStr(buf, offset, d.Name)
	walWriteStr(buf, offset, d.Path)

	return buf, nil
}

// DecodeNodeData deserializes a WALNodeData from bytes.
func DecodeNodeData(data []byte) (*WALNodeData, error) {
	if len(data) < walNodeFixedSize {
		return nil, ErrWALCodecTruncated
	}

	d := &WALNodeData{
		NodeID:   binary.LittleEndian.Uint32(data[0:4]),
		Domain:   data[4],
		NodeType: data[5],
	}

	var err error
	offset := 6
	d.Key, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	d.Name, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	d.Path, _, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// WALEdgeData is the WAL payload for edge insert/update/delete operations.
// Wire format: [SourceID:4][TargetID:4][Type:1][Weight:4][SessionID:4]
type WALEdgeData struct {
	SourceID  uint32
	TargetID  uint32
	EdgeType  uint8
	Weight    float32
	SessionID uint32
}

// walEdgeFixedSize is the fixed size of a WALEdgeData encoding.
const walEdgeFixedSize = 17

// EncodeEdgeData serializes a WALEdgeData to bytes.
func EncodeEdgeData(d *WALEdgeData) []byte {
	buf := make([]byte, walEdgeFixedSize)

	binary.LittleEndian.PutUint32(buf[0:4], d.SourceID)
	binary.LittleEndian.PutUint32(buf[4:8], d.TargetID)
	buf[8] = d.EdgeType
	binary.LittleEndian.PutUint32(buf[9:13], math.Float32bits(d.Weight))
	binary.LittleEndian.PutUint32(buf[13:17], d.SessionID)

	return buf
}

// DecodeEdgeData deserializes a WALEdgeData from bytes.
func DecodeEdgeData(data []byte) (*WALEdgeData, error) {
	if len(data) < walEdgeFixedSize {
		return nil, ErrWALCodecTruncated
	}

	return &WALEdgeData{
		SourceID:  binary.LittleEndian.Uint32(data[0:4]),
		TargetID:  binary.LittleEndian.Uint32(data[4:8]),
		EdgeType:  data[8],
		Weight:    math.Float32frombits(binary.LittleEndian.Uint32(data[9:13])),
		SessionID: binary.LittleEndian.Uint32(data[13:17]),
	}, nil
}

// WALVectorData is the WAL payload for vector insert operations.
// Wire format: [NodeID:4][Dim:4][Vector:Dim*4]
type WALVectorData struct {
	NodeID uint32
	Vector []float32
}

// walVectorHeaderSize is the fixed header of a WALVectorData encoding.
const walVectorHeaderSize = 8

// EncodeVectorData serializes a WALVectorData to bytes.
func EncodeVectorData(d *WALVectorData) []byte {
	buf := make([]byte, walVectorHeaderSize+len(d.Vector)*4)

	binary.LittleEndian.PutUint32(buf[0:4], d.NodeID)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(d.Vector)))

	for i, v := range d.Vector {
		binary.LittleEndian.PutUint32(buf[walVectorHeaderSize+i*4:], math.Float32bits(v))
	}

	return buf
}

// DecodeVectorData deserializes a WALVectorData from bytes.
func DecodeVectorData(data []byte) (*WALVectorData, error) {
	if len(data) < walVectorHeaderSize {
		return nil, ErrWALCodecTruncated
	}

	nodeID := binary.LittleEndian.Uint32(data[0:4])
	dim := binary.LittleEndian.Uint32(data[4:8])

	expectedSize := walVectorHeaderSize + int(dim)*4
	if len(data) < expectedSize {
		return nil, ErrWALCodecTruncated
	}

	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[walVectorHeaderSize+i*4:]))
	}

	return &WALVectorData{NodeID: nodeID, Vector: vec}, nil
}

// WALDocData is the WAL payload for document insert operations.
// Wire format: [IDLen:2][ID][PathLen:2][Path][TypeLen:2][Type][ContentLen:4][Content][LangLen:2][Lang]
type WALDocData struct {
	ID       string
	Path     string
	DocType  string
	Content  string
	Language string
}

// walDocMinSize is the minimum size of a WALDocData encoding (all length prefixes, no data).
// IDLen(2) + PathLen(2) + TypeLen(2) + ContentLen(4) + LangLen(2) = 12
const walDocMinSize = 12

// EncodeDocData serializes a WALDocData to bytes.
func EncodeDocData(d *WALDocData) ([]byte, error) {
	if len(d.Content) > int(^uint32(0)) {
		return nil, ErrWALCodecOverflow
	}

	totalSize := walDocMinSize + len(d.ID) + len(d.Path) + len(d.DocType) + len(d.Content) + len(d.Language)
	buf := make([]byte, totalSize)

	offset := 0
	offset = walWriteStr(buf, offset, d.ID)
	offset = walWriteStr(buf, offset, d.Path)
	offset = walWriteStr(buf, offset, d.DocType)

	// Content uses 4-byte length prefix (can be large).
	binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(d.Content)))
	offset += 4
	copy(buf[offset:], d.Content)
	offset += len(d.Content)

	walWriteStr(buf, offset, d.Language)

	return buf, nil
}

// DecodeDocData deserializes a WALDocData from bytes.
func DecodeDocData(data []byte) (*WALDocData, error) {
	if len(data) < walDocMinSize {
		return nil, ErrWALCodecTruncated
	}

	d := &WALDocData{}
	var err error
	offset := 0

	d.ID, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	d.Path, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	d.DocType, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}

	// Content uses 4-byte length prefix.
	if offset+4 > len(data) {
		return nil, ErrWALCodecTruncated
	}
	contentLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+contentLen > len(data) {
		return nil, ErrWALCodecTruncated
	}
	d.Content = string(data[offset : offset+contentLen])
	offset += contentLen

	d.Language, _, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// WALCommitData is the WAL payload for commit begin/end operations.
// Wire format: [SessionID:4][Major:2][Minor:2][Patch:2]
type WALCommitData struct {
	SessionID uint32
	Major     uint16
	Minor     uint16
	Patch     uint16
}

// walCommitFixedSize is the fixed size of a WALCommitData encoding.
const walCommitFixedSize = 10

// EncodeCommitData serializes a WALCommitData to bytes.
func EncodeCommitData(d *WALCommitData) []byte {
	buf := make([]byte, walCommitFixedSize)

	binary.LittleEndian.PutUint32(buf[0:4], d.SessionID)
	binary.LittleEndian.PutUint16(buf[4:6], d.Major)
	binary.LittleEndian.PutUint16(buf[6:8], d.Minor)
	binary.LittleEndian.PutUint16(buf[8:10], d.Patch)

	return buf
}

// DecodeCommitData deserializes a WALCommitData from bytes.
func DecodeCommitData(data []byte) (*WALCommitData, error) {
	if len(data) < walCommitFixedSize {
		return nil, ErrWALCodecTruncated
	}

	return &WALCommitData{
		SessionID: binary.LittleEndian.Uint32(data[0:4]),
		Major:     binary.LittleEndian.Uint16(data[4:6]),
		Minor:     binary.LittleEndian.Uint16(data[6:8]),
		Patch:     binary.LittleEndian.Uint16(data[8:10]),
	}, nil
}

// walWriteStr writes a uint16 length-prefixed string into buf at offset,
// returning the new offset after the write.
func walWriteStr(buf []byte, offset int, s string) int {
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	copy(buf[offset+2:], s)
	return offset + 2 + len(s)
}

// walReadStr reads a uint16 length-prefixed string from data at offset,
// returning the string, the new offset, and any error.
func walReadStr(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", 0, ErrWALCodecTruncated
	}
	length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	if offset+2+length > len(data) {
		return "", 0, ErrWALCodecTruncated
	}
	return string(data[offset+2 : offset+2+length]), offset + 2 + length, nil
}
