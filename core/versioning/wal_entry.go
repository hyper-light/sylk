package versioning

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"time"
)

// WALEntryKind distinguishes delta entries from checkpoint entries.
type WALEntryKind uint8

const (
	// WALEntryKindDelta records a pipeline merge into the global VFS.
	WALEntryKindDelta WALEntryKind = 1
	// WALEntryKindCheckpoint records a disk flush (full snapshot).
	WALEntryKindCheckpoint WALEntryKind = 2
)

// WALDeltaOp describes the operation performed on a single file.
type WALDeltaOp uint8

const (
	WALDeltaOpCreate WALDeltaOp = 0
	WALDeltaOpModify WALDeltaOp = 1
	WALDeltaOpDelete WALDeltaOp = 2
)

// WALDeltaOpFromFileOp converts a FileOp to WALDeltaOp.
func WALDeltaOpFromFileOp(op FileOp) WALDeltaOp {
	switch op {
	case FileOpCreate:
		return WALDeltaOpCreate
	case FileOpModify:
		return WALDeltaOpModify
	case FileOpDelete:
		return WALDeltaOpDelete
	default:
		return WALDeltaOpModify
	}
}

// WALFileDelta records a single file change within a WAL entry.
type WALFileDelta struct {
	Path       string
	Op         WALDeltaOp
	NewContent []byte
	OldContent []byte
}

// VersionedWALEntry is a single entry in the versioned WAL.
type VersionedWALEntry struct {
	Kind       WALEntryKind
	Version    SemanticVersion
	PipelineID string
	Timestamp  time.Time
	Deltas     []WALFileDelta
	CRC32      uint32
}

// Binary header layout:
//
//	[1B kind][4B major][4B minor][8B timestamp nanos][4B CRC32][4B payload len]
const walEntryHeaderSize = 1 + 4 + 4 + 8 + 4 + 4 // 25 bytes

var (
	ErrWALEntryCorrupted   = errors.New("wal entry: corrupted data")
	ErrWALEntryTruncated   = errors.New("wal entry: truncated data")
	ErrWALEntryCRCMismatch = errors.New("wal entry: CRC mismatch")
)

// EncodeWALEntry serializes a VersionedWALEntry to binary.
func EncodeWALEntry(e *VersionedWALEntry) []byte {
	payload := encodeWALPayload(e)
	crc := crc32.ChecksumIEEE(payload)

	buf := make([]byte, walEntryHeaderSize+len(payload))
	buf[0] = byte(e.Kind)
	binary.BigEndian.PutUint32(buf[1:5], e.Version.Major)
	binary.BigEndian.PutUint32(buf[5:9], e.Version.Minor)
	binary.BigEndian.PutUint64(buf[9:17], uint64(e.Timestamp.UnixNano()))
	binary.BigEndian.PutUint32(buf[17:21], crc)
	binary.BigEndian.PutUint32(buf[21:25], uint32(len(payload)))
	copy(buf[25:], payload)
	return buf
}

// DecodeWALEntry deserializes a VersionedWALEntry from binary.
func DecodeWALEntry(data []byte) (*VersionedWALEntry, error) {
	if len(data) < walEntryHeaderSize {
		return nil, ErrWALEntryTruncated
	}

	kind := WALEntryKind(data[0])
	major := binary.BigEndian.Uint32(data[1:5])
	minor := binary.BigEndian.Uint32(data[5:9])
	tsNano := binary.BigEndian.Uint64(data[9:17])
	storedCRC := binary.BigEndian.Uint32(data[17:21])
	payloadLen := binary.BigEndian.Uint32(data[21:25])

	if uint32(len(data)-walEntryHeaderSize) < payloadLen {
		return nil, ErrWALEntryTruncated
	}

	payload := data[walEntryHeaderSize : walEntryHeaderSize+payloadLen]
	if crc32.ChecksumIEEE(payload) != storedCRC {
		return nil, ErrWALEntryCRCMismatch
	}

	pipelineID, deltas, err := decodeWALPayload(payload)
	if err != nil {
		return nil, err
	}

	return &VersionedWALEntry{
		Kind:       kind,
		Version:    SemanticVersion{Major: major, Minor: minor},
		PipelineID: pipelineID,
		Timestamp:  time.Unix(0, int64(tsNano)),
		Deltas:     deltas,
		CRC32:      storedCRC,
	}, nil
}

// encodeWALPayload encodes the payload portion: pipelineID + deltas.
func encodeWALPayload(e *VersionedWALEntry) []byte {
	buf := encodeLenPrefixed(nil, []byte(e.PipelineID))
	buf = appendUint32(buf, uint32(len(e.Deltas)))
	for _, d := range e.Deltas {
		buf = encodeLenPrefixed(buf, []byte(d.Path))
		buf = append(buf, byte(d.Op))
		buf = encodeLenPrefixed(buf, d.NewContent)
		buf = encodeLenPrefixed(buf, d.OldContent)
	}
	return buf
}

// decodeWALPayload decodes pipelineID and deltas from payload bytes.
func decodeWALPayload(data []byte) (string, []WALFileDelta, error) {
	off := 0
	pipelineID, n, err := decodeLenPrefixed(data, off)
	if err != nil {
		return "", nil, ErrWALEntryCorrupted
	}
	off += n

	if off+4 > len(data) {
		return "", nil, ErrWALEntryCorrupted
	}
	deltaCount := binary.BigEndian.Uint32(data[off : off+4])
	off += 4

	deltas := make([]WALFileDelta, deltaCount)
	for i := range deltaCount {
		path, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return "", nil, ErrWALEntryCorrupted
		}
		off += n

		if off >= len(data) {
			return "", nil, ErrWALEntryCorrupted
		}
		op := WALDeltaOp(data[off])
		off++

		newContent, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return "", nil, ErrWALEntryCorrupted
		}
		off += n

		oldContent, n, err := decodeLenPrefixed(data, off)
		if err != nil {
			return "", nil, ErrWALEntryCorrupted
		}
		off += n

		deltas[i] = WALFileDelta{
			Path:       string(path),
			Op:         op,
			NewContent: newContent,
			OldContent: oldContent,
		}
	}
	return string(pipelineID), deltas, nil
}

// encodeLenPrefixed appends [4B length][data] to buf.
func encodeLenPrefixed(buf, data []byte) []byte {
	buf = appendUint32(buf, uint32(len(data)))
	return append(buf, data...)
}

// decodeLenPrefixed reads [4B length][data] starting at offset.
// Returns the data slice and total bytes consumed.
func decodeLenPrefixed(buf []byte, off int) ([]byte, int, error) {
	if off+4 > len(buf) {
		return nil, 0, ErrWALEntryCorrupted
	}
	length := binary.BigEndian.Uint32(buf[off : off+4])
	off += 4
	end := off + int(length)
	if end > len(buf) {
		return nil, 0, ErrWALEntryCorrupted
	}
	result := make([]byte, length)
	copy(result, buf[off:end])
	return result, 4 + int(length), nil
}

func appendUint32(buf []byte, v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return append(buf, b...)
}
