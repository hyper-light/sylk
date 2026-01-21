// Package wal provides Write-Ahead Log types for the Vamana index.
//
// Wire Format:
//
// WALHeader (8 bytes total):
//
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	|       Magic (4 bytes)             |  Version (2B)   |   Flags (2B)    |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//
// WALEntry (25 bytes header + variable data + 4 byte checksum):
//
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	|                        SequenceID (8 bytes)                           |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	| OpType |                    Timestamp (8 bytes)                       |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	|  ...   |          DataLen (4 bytes)        |       Data (variable)    |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	|                              ...Data...                               |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//	|                        CRC32 Checksum (4 bytes)                       |
//	+--------+--------+--------+--------+--------+--------+--------+--------+
//
// All multi-byte integers are encoded in little-endian format.
// CRC32 is computed over the entire entry excluding the checksum field itself.
package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// WAL constants.
const (
	// WALMagic identifies a valid WAL file ("VAMW" as little-endian uint32).
	WALMagic uint32 = 0x56414D57

	// WALVersion is the current WAL format version.
	WALVersion uint16 = 1

	// WALHeaderSize is the fixed size of the WAL file header in bytes.
	WALHeaderSize = 8

	// WALEntryHeaderSize is the fixed size of a WAL entry header in bytes
	// (SequenceID:8 + OpType:1 + Timestamp:8 + DataLen:4 = 21).
	WALEntryHeaderSize = 21

	// WALChecksumSize is the size of the CRC32 checksum in bytes.
	WALChecksumSize = 4
)

// OpType represents the type of operation recorded in a WAL entry.
type OpType uint8

const (
	// OpInsert records a vector insertion operation.
	OpInsert OpType = iota

	// OpDelete records a vector deletion operation.
	OpDelete

	// OpCheckpoint records a checkpoint marker for recovery.
	OpCheckpoint

	// OpCompact records a compaction event.
	OpCompact
)

// String returns the string representation of an OpType.
func (op OpType) String() string {
	switch op {
	case OpInsert:
		return "Insert"
	case OpDelete:
		return "Delete"
	case OpCheckpoint:
		return "Checkpoint"
	case OpCompact:
		return "Compact"
	default:
		return "Unknown"
	}
}

// Valid returns true if the OpType is a known operation type.
func (op OpType) Valid() bool {
	return op <= OpCompact
}

// Errors returned by WAL operations.
var (
	ErrInvalidMagic    = errors.New("wal: invalid magic number")
	ErrInvalidVersion  = errors.New("wal: unsupported version")
	ErrInvalidChecksum = errors.New("wal: checksum mismatch")
	ErrInvalidOpType   = errors.New("wal: invalid operation type")
	ErrBufferTooSmall  = errors.New("wal: buffer too small")
	ErrDataTooLarge    = errors.New("wal: data exceeds maximum size")
)

// WALHeader represents the file header of a WAL file.
//
// Wire format (8 bytes):
//   - Magic:   4 bytes (little-endian uint32, value 0x56414D57 "VAMW")
//   - Version: 2 bytes (little-endian uint16)
//   - Flags:   2 bytes (little-endian uint16, reserved for future use)
type WALHeader struct {
	Magic   uint32
	Version uint16
	Flags   uint16
}

// NewWALHeader creates a new WALHeader with default values.
func NewWALHeader() WALHeader {
	return WALHeader{
		Magic:   WALMagic,
		Version: WALVersion,
		Flags:   0,
	}
}

// MarshalBinary encodes the WALHeader into binary format.
// Returns exactly WALHeaderSize (8) bytes.
func (h WALHeader) MarshalBinary() ([]byte, error) {
	buf := make([]byte, WALHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], h.Magic)
	binary.LittleEndian.PutUint16(buf[4:6], h.Version)
	binary.LittleEndian.PutUint16(buf[6:8], h.Flags)
	return buf, nil
}

// UnmarshalBinary decodes a WALHeader from binary format.
// Validates the magic number and version.
func (h *WALHeader) UnmarshalBinary(data []byte) error {
	if len(data) < WALHeaderSize {
		return ErrBufferTooSmall
	}

	h.Magic = binary.LittleEndian.Uint32(data[0:4])
	h.Version = binary.LittleEndian.Uint16(data[4:6])
	h.Flags = binary.LittleEndian.Uint16(data[6:8])

	if h.Magic != WALMagic {
		return ErrInvalidMagic
	}

	if h.Version == 0 || h.Version > WALVersion {
		return ErrInvalidVersion
	}

	return nil
}

// WALEntry represents a single entry in the WAL.
//
// Wire format (21 bytes header + variable data + 4 bytes checksum):
//   - SequenceID: 8 bytes (little-endian uint64, monotonically increasing)
//   - OpType:     1 byte  (operation type)
//   - Timestamp:  8 bytes (little-endian int64, Unix nanoseconds)
//   - DataLen:    4 bytes (little-endian uint32, length of Data)
//   - Data:       variable length payload
//   - Checksum:   4 bytes (CRC32 IEEE of all preceding bytes)
type WALEntry struct {
	SequenceID uint64
	OpType     OpType
	Timestamp  int64
	Data       []byte
	Checksum   uint32
}

// Size returns the total serialized size of the entry in bytes.
func (e WALEntry) Size() int {
	return WALEntryHeaderSize + len(e.Data) + WALChecksumSize
}

// ComputeChecksum calculates the CRC32 checksum for the entry.
// The checksum covers SequenceID, OpType, Timestamp, DataLen, and Data.
func (e WALEntry) ComputeChecksum() uint32 {
	// Allocate buffer for header + data (excluding checksum)
	buf := make([]byte, WALEntryHeaderSize+len(e.Data))

	binary.LittleEndian.PutUint64(buf[0:8], e.SequenceID)
	buf[8] = byte(e.OpType)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(e.Timestamp))
	binary.LittleEndian.PutUint32(buf[17:21], uint32(len(e.Data)))
	copy(buf[21:], e.Data)

	return crc32.ChecksumIEEE(buf)
}

// ValidateChecksum verifies the entry's checksum matches the computed value.
func (e WALEntry) ValidateChecksum() bool {
	return e.Checksum == e.ComputeChecksum()
}

// MarshalBinary encodes the WALEntry into binary format.
// The checksum is computed and included in the output.
func (e WALEntry) MarshalBinary() ([]byte, error) {
	if !e.OpType.Valid() {
		return nil, ErrInvalidOpType
	}

	dataLen := len(e.Data)
	if dataLen > int(^uint32(0)) {
		return nil, ErrDataTooLarge
	}

	totalSize := WALEntryHeaderSize + dataLen + WALChecksumSize
	buf := make([]byte, totalSize)

	// Encode header fields
	binary.LittleEndian.PutUint64(buf[0:8], e.SequenceID)
	buf[8] = byte(e.OpType)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(e.Timestamp))
	binary.LittleEndian.PutUint32(buf[17:21], uint32(dataLen))

	// Copy data
	copy(buf[21:21+dataLen], e.Data)

	// Compute and append checksum over header + data
	checksum := crc32.ChecksumIEEE(buf[:21+dataLen])
	binary.LittleEndian.PutUint32(buf[21+dataLen:], checksum)

	return buf, nil
}

// UnmarshalBinary decodes a WALEntry from binary format.
// Validates the operation type and checksum.
func (e *WALEntry) UnmarshalBinary(data []byte) error {
	if len(data) < WALEntryHeaderSize+WALChecksumSize {
		return ErrBufferTooSmall
	}

	// Decode header fields
	e.SequenceID = binary.LittleEndian.Uint64(data[0:8])
	e.OpType = OpType(data[8])
	e.Timestamp = int64(binary.LittleEndian.Uint64(data[9:17]))
	dataLen := binary.LittleEndian.Uint32(data[17:21])

	if !e.OpType.Valid() {
		return ErrInvalidOpType
	}

	// Validate buffer has enough data
	totalSize := WALEntryHeaderSize + int(dataLen) + WALChecksumSize
	if len(data) < totalSize {
		return ErrBufferTooSmall
	}

	// Copy data (defensive copy to avoid aliasing)
	e.Data = make([]byte, dataLen)
	copy(e.Data, data[21:21+dataLen])

	// Extract and validate checksum
	e.Checksum = binary.LittleEndian.Uint32(data[21+dataLen : totalSize])

	expectedChecksum := crc32.ChecksumIEEE(data[:21+dataLen])
	if e.Checksum != expectedChecksum {
		return ErrInvalidChecksum
	}

	return nil
}
