package agentlog

import (
	"testing"
	"time"
)

func TestMarshalFileHeaderRoundtrip(t *testing.T) {
	var buf [WALHeaderSize]byte
	MarshalFileHeader(buf[:])

	if err := ValidateFileHeader(buf[:]); err != nil {
		t.Fatalf("ValidateFileHeader failed: %v", err)
	}
}

func TestValidateFileHeaderBadMagic(t *testing.T) {
	var buf [WALHeaderSize]byte
	MarshalFileHeader(buf[:])
	buf[0] = 0xFF // corrupt magic

	if err := ValidateFileHeader(buf[:]); err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestValidateFileHeaderBadVersion(t *testing.T) {
	var buf [WALHeaderSize]byte
	MarshalFileHeader(buf[:])
	buf[4] = 0xFF // corrupt version

	if err := ValidateFileHeader(buf[:]); err != ErrBadVersion {
		t.Fatalf("expected ErrBadVersion, got %v", err)
	}
}

func TestValidateFileHeaderTruncated(t *testing.T) {
	buf := make([]byte, 4) // too short
	if err := ValidateFileHeader(buf); err != ErrEntryTruncated {
		t.Fatalf("expected ErrEntryTruncated, got %v", err)
	}
}

func TestMarshalEntryRoundtrip(t *testing.T) {
	original := &Entry{
		Seq:       42,
		EventType: EventDAGStarted,
		Timestamp: time.Now().Truncate(time.Nanosecond),
		Data:      []byte(`{"dag_id":"dag_001","state":"running"}`),
	}

	buf := MarshalEntry(original)
	decoded, consumed, err := UnmarshalEntry(buf)
	if err != nil {
		t.Fatalf("UnmarshalEntry failed: %v", err)
	}
	if consumed != len(buf) {
		t.Fatalf("consumed %d, want %d", consumed, len(buf))
	}

	if decoded.Seq != original.Seq {
		t.Errorf("Seq: got %d, want %d", decoded.Seq, original.Seq)
	}
	if decoded.EventType != original.EventType {
		t.Errorf("EventType: got %d, want %d", decoded.EventType, original.EventType)
	}
	if decoded.Timestamp.UnixNano() != original.Timestamp.UnixNano() {
		t.Errorf("Timestamp: got %v, want %v", decoded.Timestamp, original.Timestamp)
	}
	if string(decoded.Data) != string(original.Data) {
		t.Errorf("Data: got %q, want %q", decoded.Data, original.Data)
	}
}

func TestMarshalEntryEmptyPayload(t *testing.T) {
	original := &Entry{
		Seq:       1,
		EventType: EventActivated,
		Timestamp: time.Now().Truncate(time.Nanosecond),
		Data:      nil,
	}

	buf := MarshalEntry(original)
	decoded, _, err := UnmarshalEntry(buf)
	if err != nil {
		t.Fatalf("UnmarshalEntry failed: %v", err)
	}
	if len(decoded.Data) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(decoded.Data))
	}
}

func TestUnmarshalEntryCorruptCRC(t *testing.T) {
	entry := &Entry{
		Seq:       1,
		EventType: EventActivated,
		Timestamp: time.Now(),
		Data:      []byte(`{"phase":"init"}`),
	}
	buf := MarshalEntry(entry)

	// Flip the last byte (CRC).
	buf[len(buf)-1] ^= 0xFF

	_, _, err := UnmarshalEntry(buf)
	if err != ErrBadCRC {
		t.Fatalf("expected ErrBadCRC, got %v", err)
	}
}

func TestUnmarshalEntryTruncatedHeader(t *testing.T) {
	buf := make([]byte, EntryHeaderSize-1)
	_, _, err := UnmarshalEntry(buf)
	if err != ErrEntryTruncated {
		t.Fatalf("expected ErrEntryTruncated, got %v", err)
	}
}

func TestUnmarshalEntryTruncatedData(t *testing.T) {
	entry := &Entry{
		Seq:       1,
		EventType: EventActivated,
		Timestamp: time.Now(),
		Data:      []byte(`{"phase":"init"}`),
	}
	buf := MarshalEntry(entry)

	// Remove 2 bytes from the end — cuts into CRC.
	_, _, err := UnmarshalEntry(buf[:len(buf)-2])
	if err != ErrEntryTruncated {
		t.Fatalf("expected ErrEntryTruncated, got %v", err)
	}
}

func TestEntrySize(t *testing.T) {
	data := []byte(`{"key":"value"}`)
	expected := EntryHeaderSize + len(data) + CRCSize
	if got := EntrySize(len(data)); got != expected {
		t.Errorf("EntrySize: got %d, want %d", got, expected)
	}
}

func TestCRC32IEEEConsistency(t *testing.T) {
	data := []byte("hello world")
	crc1 := crc32IEEE(data)
	crc2 := crc32IEEE(data)
	if crc1 != crc2 {
		t.Fatalf("CRC32 not deterministic: %08x != %08x", crc1, crc2)
	}
	if crc1 == 0 {
		t.Fatal("CRC32 should not be zero for non-empty input")
	}
}
