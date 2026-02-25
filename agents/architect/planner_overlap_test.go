package architect

import (
	"strings"
	"testing"
)

func TestComputeZArray_EmptyString(t *testing.T) {
	z := computeZArray("")
	if len(z) != 0 {
		t.Fatalf("len(z) = %d, want 0", len(z))
	}
}

func TestComputeZArray_SingleChar(t *testing.T) {
	z := computeZArray("a")
	if len(z) != 1 || z[0] != 0 {
		t.Fatalf("z = %v, want [0]", z)
	}
}

func TestComputeZArray_AllSame(t *testing.T) {
	z := computeZArray("aaaa")
	// z[0] unused, z[1]=3, z[2]=2, z[3]=1
	want := []int{0, 3, 2, 1}
	for i, v := range want {
		if z[i] != v {
			t.Fatalf("z[%d] = %d, want %d (z=%v)", i, z[i], v, z)
		}
	}
}

func TestComputeZArray_Pattern(t *testing.T) {
	z := computeZArray("aabxaa")
	// z[0]=0 (unused), z[1]=1, z[2]=0, z[3]=0, z[4]=2, z[5]=1
	want := []int{0, 1, 0, 0, 2, 1}
	for i, v := range want {
		if z[i] != v {
			t.Fatalf("z[%d] = %d, want %d (z=%v)", i, z[i], v, z)
		}
	}
}

func TestLongestSuffixPrefixOverlap_NoOverlap(t *testing.T) {
	got := longestSuffixPrefixOverlap("hello", "world")
	if got != 0 {
		t.Fatalf("overlap = %d, want 0", got)
	}
}

func TestLongestSuffixPrefixOverlap_FullMatch(t *testing.T) {
	// "abcabc" suffix "abc" matches prefix of "abcdef"
	got := longestSuffixPrefixOverlap("abcabc", "abcdef")
	if got != 3 {
		t.Fatalf("overlap = %d, want 3", got)
	}
}

func TestLongestSuffixPrefixOverlap_PartialOverlap(t *testing.T) {
	got := longestSuffixPrefixOverlap("the quick brown", "brown fox")
	if got != 5 {
		t.Fatalf("overlap = %d, want 5 ('brown')", got)
	}
}

func TestLongestSuffixPrefixOverlap_EmptyInputs(t *testing.T) {
	if got := longestSuffixPrefixOverlap("", "abc"); got != 0 {
		t.Fatalf("overlap(%q, %q) = %d, want 0", "", "abc", got)
	}
	if got := longestSuffixPrefixOverlap("abc", ""); got != 0 {
		t.Fatalf("overlap(%q, %q) = %d, want 0", "abc", "", got)
	}
}

func TestLongestSuffixPrefixOverlap_FullRepeat(t *testing.T) {
	// Continuation is a complete repeat of the tail.
	got := longestSuffixPrefixOverlap("xyz", "xyz")
	if got != 3 {
		t.Fatalf("overlap = %d, want 3", got)
	}
}

func TestDetectOverlap_NoOverlap(t *testing.T) {
	result := detectOverlap("hello world", "foo bar", 100)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0", result.overlapLen)
	}
	if result.netNew != "foo bar" {
		t.Fatalf("netNew = %q, want %q", result.netNew, "foo bar")
	}
}

func TestDetectOverlap_PartialOverlap(t *testing.T) {
	result := detectOverlap("the end of section", "section continues here", 100)
	if result.overlapLen != 7 { // "section"
		t.Fatalf("overlapLen = %d, want 7", result.overlapLen)
	}
	if result.netNew != " continues here" {
		t.Fatalf("netNew = %q, want %q", result.netNew, " continues here")
	}
}

func TestDetectOverlap_EmptyAccumulated(t *testing.T) {
	result := detectOverlap("", "continuation", 100)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0", result.overlapLen)
	}
	if result.netNew != "continuation" {
		t.Fatalf("netNew = %q, want %q", result.netNew, "continuation")
	}
}

func TestDetectOverlap_EmptyContinuation(t *testing.T) {
	result := detectOverlap("accumulated", "", 100)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0", result.overlapLen)
	}
	if result.netNew != "" {
		t.Fatalf("netNew = %q, want empty", result.netNew)
	}
}

func TestDetectOverlap_BoundedSearch(t *testing.T) {
	// Overlap exists beyond the search window — should not be found.
	long := strings.Repeat("a", 100) + "OVERLAP"
	cont := "OVERLAP" + strings.Repeat("b", 100)
	// Search limit of 5 bytes — "OVERLAP" (7 chars) is beyond window.
	result := detectOverlap(long, cont, 5)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0 (overlap outside search window)", result.overlapLen)
	}
	// Search limit of 10 bytes — "OVERLAP" is within window.
	result = detectOverlap(long, cont, 10)
	if result.overlapLen != 7 {
		t.Fatalf("overlapLen = %d, want 7", result.overlapLen)
	}
}

func TestDetectOverlap_JSONLike(t *testing.T) {
	acc := `{"goals":["ship"],"scope":"ap`
	cont := `,"scope":"api","priority":"high"}`
	result := detectOverlap(acc, cont, 1000)
	// Overlap: ,"scope":"ap is NOT a prefix of cont.
	// Actually the suffix of acc that matches prefix of cont is:
	// acc ends with: ,"scope":"ap
	// cont starts with: ,"scope":"api",...
	// Longest matching suffix-prefix: ,"scope":"ap (12 chars)
	if result.overlapLen != 12 {
		t.Fatalf("overlapLen = %d, want 12", result.overlapLen)
	}
	if result.netNew != `i","priority":"high"}` {
		t.Fatalf("netNew = %q", result.netNew)
	}
}

func TestDetectModeOverlap_JSONUsesBytes(t *testing.T) {
	result := detectModeOverlap(continuationModeJSON, "abc", "def", 100)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0", result.overlapLen)
	}
}

func TestDetectModeOverlap_TextFallsBackToLines(t *testing.T) {
	// Byte-level: no exact overlap.
	// Line-level: last line of accumulated matches first line of continuation
	// (with trailing space difference).
	acc := "line one\nline two  "
	cont := "line two\nline three"
	result := detectModeOverlap(continuationModeText, acc, cont, 1000)
	// Line-level should match "line two" (with trimmed comparison).
	if result.overlapLen == 0 {
		t.Fatal("expected line-level overlap to find match")
	}
}

func TestDetectLineOverlap_MultipleLines(t *testing.T) {
	acc := "header\nfirst\nsecond\nthird"
	cont := "second\nthird\nfourth\nfifth"
	result := detectLineOverlap(acc, cont, 1000)
	if result.overlapLen == 0 {
		t.Fatal("expected line overlap")
	}
	// "second\nthird" = 6 + 1 + 5 = 12 bytes (including newline between)
	if result.overlapLen != 12 {
		t.Fatalf("overlapLen = %d, want 12", result.overlapLen)
	}
}

func TestDetectLineOverlap_NoMatch(t *testing.T) {
	result := detectLineOverlap("aaa\nbbb", "ccc\nddd", 1000)
	if result.overlapLen != 0 {
		t.Fatalf("overlapLen = %d, want 0", result.overlapLen)
	}
	if result.netNew != "ccc\nddd" {
		t.Fatalf("netNew = %q, want original continuation", result.netNew)
	}
}

func TestSuffixBounded(t *testing.T) {
	if got := suffixBounded("hello", 10); got != "hello" {
		t.Fatalf("got %q, want full string", got)
	}
	if got := suffixBounded("hello world", 5); got != "world" {
		t.Fatalf("got %q, want %q", got, "world")
	}
	if got := suffixBounded("", 5); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestTrimLines(t *testing.T) {
	input := []string{"hello  ", "world\t", "clean", "trailing\r"}
	got := trimLines(input)
	want := []string{"hello", "world", "clean", "trailing"}
	for i, g := range got {
		if g != want[i] {
			t.Fatalf("trimLines[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestHashLines_Deterministic(t *testing.T) {
	lines := []string{"hello", "world"}
	h1 := hashLines(lines)
	h2 := hashLines(lines)
	for i := range h1 {
		if h1[i] != h2[i] {
			t.Fatalf("hashLines not deterministic at index %d", i)
		}
	}
	if h1[0] == h1[1] {
		t.Fatal("different lines should produce different hashes")
	}
}

func TestComputeUint64ZArray_Empty(t *testing.T) {
	z := computeUint64ZArray(nil)
	if len(z) != 0 {
		t.Fatalf("len(z) = %d, want 0", len(z))
	}
}

func TestComputeUint64ZArray_AllSame(t *testing.T) {
	s := []uint64{42, 42, 42, 42}
	z := computeUint64ZArray(s)
	want := []int{0, 3, 2, 1}
	for i, v := range want {
		if z[i] != v {
			t.Fatalf("z[%d] = %d, want %d (z=%v)", i, z[i], v, z)
		}
	}
}

func TestComputeUint64ZArray_Pattern(t *testing.T) {
	s := []uint64{1, 1, 2, 3, 1, 1}
	z := computeUint64ZArray(s)
	// z[0]=0 (unused), z[1]=1, z[2]=0, z[3]=0, z[4]=2, z[5]=1
	want := []int{0, 1, 0, 0, 2, 1}
	for i, v := range want {
		if z[i] != v {
			t.Fatalf("z[%d] = %d, want %d (z=%v)", i, z[i], v, z)
		}
	}
}

func TestLinesEqual(t *testing.T) {
	if !linesEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Fatal("identical slices should be equal")
	}
	if linesEqual([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("different slices should not be equal")
	}
}

func TestJoinedByteLength(t *testing.T) {
	if got := joinedByteLength(nil); got != 0 {
		t.Fatalf("nil = %d, want 0", got)
	}
	if got := joinedByteLength([]string{"ab", "cd"}); got != 5 {
		// "ab" + "\n" + "cd" = 2+1+2 = 5
		t.Fatalf("two lines = %d, want 5", got)
	}
	if got := joinedByteLength([]string{"solo"}); got != 4 {
		t.Fatalf("single line = %d, want 4", got)
	}
}
