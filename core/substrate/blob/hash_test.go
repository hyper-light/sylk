package blob

import (
	"strings"
	"testing"
)

func TestSum_Deterministic(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	h1 := Sum(data)
	h2 := Sum(data)
	if h1 != h2 {
		t.Fatalf("Sum is non-deterministic: %s vs %s", h1, h2)
	}
}

func TestSum_DifferentInputs_DifferentHashes(t *testing.T) {
	h1 := Sum([]byte("foo"))
	h2 := Sum([]byte("bar"))
	if h1 == h2 {
		t.Fatalf("distinct inputs collided: foo and bar both → %s", h1)
	}
}

func TestSum_EmptyInput(t *testing.T) {
	h := Sum(nil)
	// BLAKE3 of empty input is non-zero — verify by checking it's not the zero Hash.
	if h.IsZero() {
		t.Fatalf("Sum(nil) returned the zero Hash; expected a real BLAKE3 digest of the empty input")
	}
}

func TestHashString_HexEncoded(t *testing.T) {
	h := Sum([]byte("hello"))
	s := h.String()
	if len(s) != HashSize*2 {
		t.Fatalf("expected hex string of length %d, got %d (%q)", HashSize*2, len(s), s)
	}
	if strings.ToLower(s) != s {
		t.Fatalf("expected lowercase hex, got %q", s)
	}
}

func TestParseHash_RoundTrip(t *testing.T) {
	original := Sum([]byte("round-trip"))
	parsed, err := ParseHash(original.String())
	if err != nil {
		t.Fatalf("ParseHash on a freshly-encoded hash failed: %v", err)
	}
	if parsed != original {
		t.Fatalf("parsed hash differs from original")
	}
}

func TestParseHash_RejectsWrongLength(t *testing.T) {
	_, err := ParseHash("abcd")
	if err == nil {
		t.Fatal("expected error for short hex input")
	}
}

func TestParseHash_RejectsNonHex(t *testing.T) {
	_, err := ParseHash(strings.Repeat("z", HashSize*2))
	if err == nil {
		t.Fatal("expected error for non-hex input")
	}
}

func TestZeroHash_IsZero(t *testing.T) {
	var h Hash
	if !h.IsZero() {
		t.Fatal("zero-valued Hash should report IsZero == true")
	}
}

func TestSum_ResultIsNotZero(t *testing.T) {
	// The zero Hash is reserved as a sentinel. Verify no real BLAKE3
	// digest accidentally matches it for at least the empty input.
	if Sum(nil).IsZero() {
		t.Fatal("BLAKE3 of empty input must not be the zero Hash sentinel")
	}
}
