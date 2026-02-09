package graph

import (
	"encoding/binary"
	"hash/maphash"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

type Node struct {
	ID          uint32
	Domain      vectorgraphdb.Domain
	Type        vectorgraphdb.NodeType
	Name        string
	Path        string
	Package     string
	Signature   string
	Content     []byte
	ContentHash uint64
	CreatedAt   uint64
	SessionID   string
	CreatedBy   uint16
}

var contentHashSeed = maphash.MakeSeed()

func (n *Node) ComputeContentHash() uint64 {
	var h maphash.Hash
	h.SetSeed(contentHashSeed)
	h.Write(n.Content)
	return h.Sum64()
}

func (n *Node) UpdateContentHash() {
	n.ContentHash = n.ComputeContentHash()
}

const nodeFixedSize = 4 + 4 + 4 + 8 + 8 + 2 // ID + Domain + Type + ContentHash + CreatedAt + CreatedBy = 30 bytes

// MarshalBinary format:
// [ID:4][Domain:4][Type:4][ContentHash:8][CreatedAt:8][CreatedBy:2]
// [NameLen:2][Name:N][PathLen:2][Path:N][PackageLen:2][Package:N]
// [SignatureLen:2][Signature:N][SessionIDLen:2][SessionID:N][ContentLen:4][Content:N]
func (n *Node) MarshalBinary() []byte {
	nameBytes := []byte(n.Name)
	pathBytes := []byte(n.Path)
	packageBytes := []byte(n.Package)
	signatureBytes := []byte(n.Signature)
	sessionBytes := []byte(n.SessionID)

	totalSize := nodeFixedSize +
		2 + len(nameBytes) +
		2 + len(pathBytes) +
		2 + len(packageBytes) +
		2 + len(signatureBytes) +
		2 + len(sessionBytes) +
		4 + len(n.Content)

	buf := make([]byte, totalSize)
	offset := 0

	binary.LittleEndian.PutUint32(buf[offset:], n.ID)
	offset += 4
	binary.LittleEndian.PutUint32(buf[offset:], uint32(n.Domain))
	offset += 4
	binary.LittleEndian.PutUint32(buf[offset:], uint32(n.Type))
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], n.ContentHash)
	offset += 8
	binary.LittleEndian.PutUint64(buf[offset:], n.CreatedAt)
	offset += 8
	binary.LittleEndian.PutUint16(buf[offset:], n.CreatedBy)
	offset += 2

	offset = writeString(buf, offset, nameBytes)
	offset = writeString(buf, offset, pathBytes)
	offset = writeString(buf, offset, packageBytes)
	offset = writeString(buf, offset, signatureBytes)
	offset = writeString(buf, offset, sessionBytes)

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(n.Content)))
	offset += 4
	copy(buf[offset:], n.Content)

	return buf
}

func (n *Node) UnmarshalBinary(data []byte) error {
	if len(data) < nodeFixedSize {
		return errInvalidNodeData
	}

	offset := 0
	n.ID = binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	n.Domain = vectorgraphdb.Domain(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	n.Type = vectorgraphdb.NodeType(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	n.ContentHash = binary.LittleEndian.Uint64(data[offset:])
	offset += 8
	n.CreatedAt = binary.LittleEndian.Uint64(data[offset:])
	offset += 8
	n.CreatedBy = binary.LittleEndian.Uint16(data[offset:])
	offset += 2

	var err error
	n.Name, offset, err = readString(data, offset)
	if err != nil {
		return err
	}
	n.Path, offset, err = readString(data, offset)
	if err != nil {
		return err
	}
	n.Package, offset, err = readString(data, offset)
	if err != nil {
		return err
	}
	n.Signature, offset, err = readString(data, offset)
	if err != nil {
		return err
	}
	n.SessionID, offset, err = readString(data, offset)
	if err != nil {
		return err
	}

	if offset+4 > len(data) {
		return errInvalidNodeData
	}
	contentLen := binary.LittleEndian.Uint32(data[offset:])
	offset += 4

	if offset+int(contentLen) > len(data) {
		return errInvalidNodeData
	}
	n.Content = make([]byte, contentLen)
	copy(n.Content, data[offset:offset+int(contentLen)])

	return nil
}

func writeString(buf []byte, offset int, s []byte) int {
	binary.LittleEndian.PutUint16(buf[offset:], uint16(len(s)))
	offset += 2
	copy(buf[offset:], s)
	return offset + len(s)
}

func readString(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", offset, errInvalidNodeData
	}
	strLen := binary.LittleEndian.Uint16(data[offset:])
	offset += 2
	if offset+int(strLen) > len(data) {
		return "", offset, errInvalidNodeData
	}
	s := string(data[offset : offset+int(strLen)])
	return s, offset + int(strLen), nil
}
