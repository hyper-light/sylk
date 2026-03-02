package agentlog

import (
	"encoding/binary"
	"errors"
	"time"
)

// File header constants.
const (
	// WALMagic identifies an agent WAL file ("AWLS" = 0x41574C53).
	WALMagic uint32 = 0x41574C53

	// WALVersion is the current format version.
	WALVersion uint16 = 1

	// WALHeaderSize is the fixed file header size in bytes.
	// [Magic:4][Version:2][Flags:2]
	WALHeaderSize = 8

	// EntryHeaderSize is the fixed entry header size in bytes.
	// [Seq:8][EventType:2][Timestamp:8][DataLen:4]
	EntryHeaderSize = 22

	// CRCSize is the trailing CRC32 checksum size.
	CRCSize = 4
)

// Codec errors.
var (
	ErrBadMagic       = errors.New("agentlog: invalid WAL magic")
	ErrBadVersion     = errors.New("agentlog: unsupported WAL version")
	ErrEntryTruncated = errors.New("agentlog: entry truncated")
	ErrBadCRC         = errors.New("agentlog: CRC mismatch")
)

// Entry is a single WAL record.
type Entry struct {
	Seq       uint64
	EventType EventType
	Timestamp time.Time
	Data      []byte // JSON payload
}

// MarshalFileHeader writes the 8-byte file header into buf.
func MarshalFileHeader(buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], WALMagic)
	binary.LittleEndian.PutUint16(buf[4:6], WALVersion)
	binary.LittleEndian.PutUint16(buf[6:8], 0) // flags reserved
}

// ValidateFileHeader checks the file header in buf.
func ValidateFileHeader(buf []byte) error {
	if len(buf) < WALHeaderSize {
		return ErrEntryTruncated
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != WALMagic {
		return ErrBadMagic
	}
	if binary.LittleEndian.Uint16(buf[4:6]) != WALVersion {
		return ErrBadVersion
	}
	return nil
}

// MarshalEntry serializes an Entry into binary form:
// [Seq:8][EventType:2][Timestamp:8][DataLen:4][Data:variable][CRC32:4]
func MarshalEntry(e *Entry) []byte {
	dataLen := len(e.Data)
	total := EntryHeaderSize + dataLen + CRCSize
	buf := make([]byte, total)

	binary.LittleEndian.PutUint64(buf[0:8], e.Seq)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(e.EventType))
	binary.LittleEndian.PutUint64(buf[10:18], uint64(e.Timestamp.UnixNano()))
	binary.LittleEndian.PutUint32(buf[18:22], uint32(dataLen))
	copy(buf[22:22+dataLen], e.Data)

	crc := crc32IEEE(buf[:22+dataLen])
	binary.LittleEndian.PutUint32(buf[22+dataLen:], crc)

	return buf
}

// UnmarshalEntry deserializes an Entry from raw bytes.
// Returns the entry and the total number of bytes consumed.
func UnmarshalEntry(raw []byte) (*Entry, int, error) {
	if len(raw) < EntryHeaderSize+CRCSize {
		return nil, 0, ErrEntryTruncated
	}

	dataLen := int(binary.LittleEndian.Uint32(raw[18:22]))
	total := EntryHeaderSize + dataLen + CRCSize
	if len(raw) < total {
		return nil, 0, ErrEntryTruncated
	}

	storedCRC := binary.LittleEndian.Uint32(raw[EntryHeaderSize+dataLen:])
	computedCRC := crc32IEEE(raw[:EntryHeaderSize+dataLen])
	if storedCRC != computedCRC {
		return nil, 0, ErrBadCRC
	}

	data := make([]byte, dataLen)
	copy(data, raw[22:22+dataLen])

	e := &Entry{
		Seq:       binary.LittleEndian.Uint64(raw[0:8]),
		EventType: EventType(binary.LittleEndian.Uint16(raw[8:10])),
		Timestamp: time.Unix(0, int64(binary.LittleEndian.Uint64(raw[10:18]))),
		Data:      data,
	}

	return e, total, nil
}

// EntrySize returns the total on-disk size for a payload of the given length.
func EntrySize(dataLen int) int {
	return EntryHeaderSize + dataLen + CRCSize
}

// crc32IEEE computes CRC-32 with the IEEE polynomial.
// Inline implementation — no heap allocation, same as orchestrator/safety_journal.
func crc32IEEE(data []byte) uint32 {
	var crc uint32 = 0xFFFFFFFF
	for _, b := range data {
		crc ^= uint32(b)
		for range 8 {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}
