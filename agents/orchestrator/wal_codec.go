package orchestrator

import (
	"encoding/binary"
	"errors"
	"time"
)

// Codec errors.
var (
	ErrWALCodecTruncated = errors.New("orchestrator wal: data truncated")
	ErrWALCodecOverflow  = errors.New("orchestrator wal: string exceeds uint16 limit")
	ErrWALInvalidOp      = errors.New("orchestrator wal: invalid op type")
)

// Wire format per entry:
// [Seq:8][Op:2][Timestamp:8][Layer:4]
// [DAGIDLen:2][DAGID][NodeIDLen:2][NodeID]
// [StateLen:2][State][PayloadLen:2][Payload][ErrLen:2][Err]
//
// Fixed header = Seq(8) + Op(2) + Timestamp(8) + Layer(4) = 22 bytes
// Plus 5 × uint16 length prefixes = 10 bytes → minimum 32 bytes.
const walEntryFixedSize = 32

// EncodeWALEntry serializes a WALEntry to bytes.
func EncodeWALEntry(e *WALEntry) ([]byte, error) {
	if err := validateWALStrings(e); err != nil {
		return nil, err
	}

	total := walEntryFixedSize +
		len(e.DAGID) + len(e.NodeID) + len(e.State) + len(e.Payload) + len(e.Error)
	buf := make([]byte, total)

	binary.LittleEndian.PutUint64(buf[0:8], e.Seq)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(e.Op))
	binary.LittleEndian.PutUint64(buf[10:18], uint64(e.Timestamp.UnixNano()))
	binary.LittleEndian.PutUint32(buf[18:22], uint32(e.Layer))

	offset := 22
	offset = walWriteStr(buf, offset, e.DAGID)
	offset = walWriteStr(buf, offset, e.NodeID)
	offset = walWriteStr(buf, offset, e.State)
	offset = walWriteStr(buf, offset, e.Payload)
	walWriteStr(buf, offset, e.Error)

	return buf, nil
}

// DecodeWALEntry deserializes a WALEntry from bytes.
func DecodeWALEntry(data []byte) (*WALEntry, error) {
	if len(data) < walEntryFixedSize {
		return nil, ErrWALCodecTruncated
	}

	e := &WALEntry{
		Seq:       binary.LittleEndian.Uint64(data[0:8]),
		Op:        WALOp(binary.LittleEndian.Uint16(data[8:10])),
		Timestamp: time.Unix(0, int64(binary.LittleEndian.Uint64(data[10:18]))),
		Layer:     int(binary.LittleEndian.Uint32(data[18:22])),
	}

	if e.Op < walOpDAGStart || e.Op > walOpDAGCancel {
		return nil, ErrWALInvalidOp
	}

	var err error
	offset := 22
	e.DAGID, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.NodeID, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.State, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.Payload, offset, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.Error, _, err = walReadStr(data, offset)
	if err != nil {
		return nil, err
	}

	return e, nil
}

// validateWALStrings checks that all strings fit in uint16 length fields.
func validateWALStrings(e *WALEntry) error {
	const maxLen = int(^uint16(0))
	if len(e.DAGID) > maxLen || len(e.NodeID) > maxLen ||
		len(e.State) > maxLen || len(e.Payload) > maxLen || len(e.Error) > maxLen {
		return ErrWALCodecOverflow
	}
	return nil
}

// walWriteStr writes a uint16 length-prefixed string into buf at offset.
func walWriteStr(buf []byte, offset int, s string) int {
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	copy(buf[offset+2:], s)
	return offset + 2 + len(s)
}

// walReadStr reads a uint16 length-prefixed string from data at offset.
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
