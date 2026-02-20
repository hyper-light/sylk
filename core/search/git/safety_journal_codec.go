package git

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// Codec errors.
var (
	ErrJournalCodecTruncated = errors.New("safety journal: data truncated")
	ErrJournalCodecOverflow  = errors.New("safety journal: string exceeds uint16 limit")
	ErrJournalInvalidPhase   = errors.New("safety journal: invalid phase")
)

// Wire format per entry:
// [Seq:8][Op:2][Phase:1][Timestamp:8][HeadHash:20][BranchRefLen:2][BranchRef]
// [ParamsLen:2][Params][SnapRefLen:2][SnapRef][ErrLen:2][Err]
//
// Fixed header = Seq(8) + Op(2) + Phase(1) + Timestamp(8) + HeadHash(20) = 39 bytes
// Plus 4 × uint16 length prefixes = 8 bytes → minimum 47 bytes.
const journalEntryFixedSize = 47

// EncodeJournalEntry serializes a JournalEntry to bytes.
func EncodeJournalEntry(e *JournalEntry) ([]byte, error) {
	if err := validateJournalStrings(e); err != nil {
		return nil, err
	}

	total := journalEntryFixedSize +
		len(e.BranchRef) + len(e.Params) + len(e.SnapRef) + len(e.Err)
	buf := make([]byte, total)

	binary.LittleEndian.PutUint64(buf[0:8], e.Seq)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(e.Op))
	buf[10] = byte(e.Phase)
	binary.LittleEndian.PutUint64(buf[11:19], uint64(e.Timestamp.UnixNano()))
	copy(buf[19:39], e.HeadHash[:])

	offset := 39
	offset = journalWriteStr(buf, offset, e.BranchRef)
	offset = journalWriteStr(buf, offset, e.Params)
	offset = journalWriteStr(buf, offset, e.SnapRef)
	journalWriteStr(buf, offset, e.Err)

	return buf, nil
}

// DecodeJournalEntry deserializes a JournalEntry from bytes.
func DecodeJournalEntry(data []byte) (*JournalEntry, error) {
	if len(data) < journalEntryFixedSize {
		return nil, ErrJournalCodecTruncated
	}

	e := &JournalEntry{
		Seq:       binary.LittleEndian.Uint64(data[0:8]),
		Op:        GitOp(binary.LittleEndian.Uint16(data[8:10])),
		Phase:     JournalPhase(data[10]),
		Timestamp: time.Unix(0, int64(binary.LittleEndian.Uint64(data[11:19]))),
	}

	if e.Phase < JournalBegin || e.Phase > JournalAbort {
		return nil, ErrJournalInvalidPhase
	}

	copy(e.HeadHash[:], data[19:39])

	var err error
	offset := 39
	e.BranchRef, offset, err = journalReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.Params, offset, err = journalReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.SnapRef, offset, err = journalReadStr(data, offset)
	if err != nil {
		return nil, err
	}
	e.Err, _, err = journalReadStr(data, offset)
	if err != nil {
		return nil, err
	}

	return e, nil
}

// validateJournalStrings checks that all strings fit in uint16 length fields.
func validateJournalStrings(e *JournalEntry) error {
	const maxLen = int(^uint16(0))
	if len(e.BranchRef) > maxLen || len(e.Params) > maxLen ||
		len(e.SnapRef) > maxLen || len(e.Err) > maxLen {
		return ErrJournalCodecOverflow
	}
	return nil
}

// journalWriteStr writes a uint16 length-prefixed string into buf at offset.
func journalWriteStr(buf []byte, offset int, s string) int {
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	copy(buf[offset+2:], s)
	return offset + 2 + len(s)
}

// journalReadStr reads a uint16 length-prefixed string from data at offset.
func journalReadStr(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", 0, ErrJournalCodecTruncated
	}
	length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	if offset+2+length > len(data) {
		return "", 0, ErrJournalCodecTruncated
	}
	return string(data[offset+2 : offset+2+length]), offset + 2 + length, nil
}

// journalEntryToWALData converts a JournalEntry for storage in the WAL.
// The Seq field is stored in the WAL entry's SequenceID, not in the payload.
func journalEntryToWALData(e *JournalEntry) ([]byte, error) {
	return EncodeJournalEntry(e)
}

// walDataToJournalEntry converts WAL payload back to a JournalEntry.
// The caller must set Seq from the WAL entry's SequenceID.
func walDataToJournalEntry(data []byte) (*JournalEntry, error) {
	return DecodeJournalEntry(data)
}

// hashFromBytes creates a plumbing.Hash from a 20-byte slice.
func hashFromBytes(b []byte) plumbing.Hash {
	var h plumbing.Hash
	copy(h[:], b)
	return h
}
