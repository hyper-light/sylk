package sylkdir

import (
	"math"
	"testing"
)

func TestChunkSpanRoundTrip(t *testing.T) {
	span := ChunkSpan{
		DocRef:    42,
		ChunkSeq:  5,
		ByteStart: 1024,
		ByteEnd:   3072,
		LineStart: 10,
		LineEnd:   50,
		Flags:     PackFlags(OverlapSuffix, ContentClassSourceCode, BoundaryStructural),
	}

	data := span.MarshalBinary()
	if len(data) != ChunkSpanSize {
		t.Fatalf("marshal size: got %d, want %d", len(data), ChunkSpanSize)
	}

	var decoded ChunkSpan
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != span {
		t.Fatalf("round-trip mismatch:\n  got:  %+v\n  want: %+v", decoded, span)
	}
}

func TestChunkSpanZeroValues(t *testing.T) {
	var span ChunkSpan
	data := span.MarshalBinary()

	var decoded ChunkSpan
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal zero: %v", err)
	}

	if decoded != span {
		t.Fatalf("zero round-trip mismatch")
	}
}

func TestChunkSpanMaxValues(t *testing.T) {
	span := ChunkSpan{
		DocRef:    math.MaxUint32,
		ChunkSeq:  math.MaxUint16,
		ByteStart: math.MaxUint32,
		ByteEnd:   math.MaxUint32,
		LineStart: math.MaxUint32,
		LineEnd:   math.MaxUint32,
		Flags:     math.MaxUint16,
	}

	data := span.MarshalBinary()
	var decoded ChunkSpan
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal max: %v", err)
	}

	if decoded != span {
		t.Fatalf("max round-trip mismatch")
	}
}

func TestChunkSpanUnmarshalTooShort(t *testing.T) {
	var span ChunkSpan
	if err := span.UnmarshalBinary(make([]byte, ChunkSpanSize-1)); err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestChunkSpanFlagsPacking(t *testing.T) {
	tests := []struct {
		overlap OverlapKind
		cc      ContentClass
		bk      BoundaryKind
	}{
		{OverlapNone, ContentClassSourceCode, BoundarySentence},
		{OverlapPrefix, ContentClassMarkdown, BoundaryHeading},
		{OverlapSuffix, ContentClassWebContent, BoundaryCodeBlock},
		{OverlapBoth, ContentClassAcademic, BoundaryStructural},
		{OverlapNone, ContentClassLLMResponse, BoundarySentence},
		{OverlapBoth, ContentClassConfig, BoundaryStructural},
	}

	for _, tt := range tests {
		flags := PackFlags(tt.overlap, tt.cc, tt.bk)
		span := ChunkSpan{Flags: flags}

		if got := span.UnpackOverlap(); got != tt.overlap {
			t.Errorf("overlap: got %d, want %d (flags=0x%04x)", got, tt.overlap, flags)
		}
		if got := span.UnpackContentClass(); got != tt.cc {
			t.Errorf("content class: got %d, want %d (flags=0x%04x)", got, tt.cc, flags)
		}
		if got := span.UnpackBoundaryKind(); got != tt.bk {
			t.Errorf("boundary kind: got %d, want %d (flags=0x%04x)", got, tt.bk, flags)
		}
	}
}

func TestChunkSpanByteLen(t *testing.T) {
	span := ChunkSpan{ByteStart: 100, ByteEnd: 350}
	if got := span.ByteLen(); got != 250 {
		t.Fatalf("ByteLen: got %d, want 250", got)
	}
}

func TestChunkRefRoundTrip(t *testing.T) {
	ref := ChunkRef{
		NodeID: 999,
		Span: ChunkSpan{
			DocRef:    42,
			ChunkSeq:  3,
			ByteStart: 500,
			ByteEnd:   1500,
			LineStart: 20,
			LineEnd:   60,
			Flags:     PackFlags(OverlapPrefix, ContentClassMarkdown, BoundaryHeading),
		},
	}

	data := ref.MarshalBinary()
	if len(data) != ChunkRefRecordSize {
		t.Fatalf("chunk ref size: got %d, want %d", len(data), ChunkRefRecordSize)
	}

	var decoded ChunkRef
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal chunk ref: %v", err)
	}

	if decoded.NodeID != ref.NodeID {
		t.Errorf("NodeID: got %d, want %d", decoded.NodeID, ref.NodeID)
	}
	if decoded.Span != ref.Span {
		t.Errorf("Span mismatch:\n  got:  %+v\n  want: %+v", decoded.Span, ref.Span)
	}
}

func TestChunkRefUnmarshalTooShort(t *testing.T) {
	var ref ChunkRef
	if err := ref.UnmarshalBinary(make([]byte, ChunkRefRecordSize-1)); err == nil {
		t.Fatal("expected error for short data")
	}
}
