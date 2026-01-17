package concurrency

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"time"
)

type EntryType uint8

const (
	EntryCheckpoint EntryType = iota
	EntryStateChange
	EntryLLMRequest
	EntryLLMResponse
	EntryFileChange
)

var entryTypeNames = map[EntryType]string{
	EntryCheckpoint:  "checkpoint",
	EntryStateChange: "state_change",
	EntryLLMRequest:  "llm_request",
	EntryLLMResponse: "llm_response",
	EntryFileChange:  "file_change",
}

func (e EntryType) String() string {
	if name, ok := entryTypeNames[e]; ok {
		return name
	}
	return "unknown"
}

type WALEntry struct {
	Sequence  uint64
	Timestamp time.Time
	Type      EntryType
	Payload   []byte
}

const (
	sequenceSize    = 8
	timestampSize   = 8
	typeSize        = 1
	payloadLenSize  = 4
	entryHeaderSize = sequenceSize + timestampSize + typeSize + payloadLenSize
	crcSize         = 4
)

var (
	ErrCorruptedEntry = errors.New("corrupted WAL entry")
	ErrInvalidCRC     = errors.New("invalid CRC checksum")
	ErrShortRead      = errors.New("short read from WAL")
)

func (e *WALEntry) Encode() []byte {
	payloadLen := len(e.Payload)
	totalSize := entryHeaderSize + payloadLen + crcSize
	buf := make([]byte, totalSize)

	offset := 0
	binary.BigEndian.PutUint64(buf[offset:offset+sequenceSize], e.Sequence)
	offset += sequenceSize

	binary.BigEndian.PutUint64(buf[offset:offset+timestampSize], uint64(e.Timestamp.UnixNano()))
	offset += timestampSize

	buf[offset] = byte(e.Type)
	offset += typeSize

	binary.BigEndian.PutUint32(buf[offset:offset+payloadLenSize], uint32(payloadLen))
	offset += payloadLenSize

	copy(buf[offset:offset+payloadLen], e.Payload)
	offset += payloadLen

	checksum := crc32.ChecksumIEEE(buf[:offset])
	binary.BigEndian.PutUint32(buf[offset:], checksum)

	return buf
}

func DecodeWALEntry(data []byte) (*WALEntry, error) {
	if len(data) < entryHeaderSize+crcSize {
		return nil, ErrShortRead
	}

	payloadLen := binary.BigEndian.Uint32(data[entryHeaderSize-payloadLenSize : entryHeaderSize])
	totalLen := entryHeaderSize + int(payloadLen) + crcSize

	if len(data) < totalLen {
		return nil, ErrShortRead
	}

	dataWithoutCRC := data[:entryHeaderSize+int(payloadLen)]
	storedCRC := binary.BigEndian.Uint32(data[entryHeaderSize+int(payloadLen):])
	computedCRC := crc32.ChecksumIEEE(dataWithoutCRC)

	if storedCRC != computedCRC {
		return nil, ErrInvalidCRC
	}

	offset := 0
	sequence := binary.BigEndian.Uint64(data[offset : offset+sequenceSize])
	offset += sequenceSize

	timestamp := time.Unix(0, int64(binary.BigEndian.Uint64(data[offset:offset+timestampSize])))
	offset += timestampSize

	entryType := EntryType(data[offset])
	offset += typeSize

	offset += payloadLenSize

	payload := make([]byte, payloadLen)
	copy(payload, data[offset:offset+int(payloadLen)])

	return &WALEntry{
		Sequence:  sequence,
		Timestamp: timestamp,
		Type:      entryType,
		Payload:   payload,
	}, nil
}

func EntrySize(payloadLen int) int {
	return entryHeaderSize + payloadLen + crcSize
}

func ReadEntrySize(header []byte) (int, error) {
	if len(header) < entryHeaderSize {
		return 0, ErrShortRead
	}
	payloadLen := binary.BigEndian.Uint32(header[entryHeaderSize-payloadLenSize : entryHeaderSize])
	return EntrySize(int(payloadLen)), nil
}
