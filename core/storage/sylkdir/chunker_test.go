package sylkdir

import (
	"strings"
	"testing"
)

func TestChunkerConfigDerivation(t *testing.T) {
	cfg := NewChunkerConfig(512, ContentClassSourceCode)

	if cfg.EstBytesPerToken != 4.0 {
		t.Errorf("bpt: got %f, want 4.0", cfg.EstBytesPerToken)
	}
	if cfg.MaxChunkBytes != 2048 {
		t.Errorf("max: got %d, want 2048", cfg.MaxChunkBytes)
	}
	if cfg.MinChunkBytes != 512 {
		t.Errorf("min: got %d, want 512", cfg.MinChunkBytes)
	}
	if cfg.OverlapBytes != 256 {
		t.Errorf("overlap: got %d, want 256", cfg.OverlapBytes)
	}
}

func TestChunkerConfigMarkdown(t *testing.T) {
	cfg := NewChunkerConfig(512, ContentClassMarkdown)

	if cfg.EstBytesPerToken != 5.0 {
		t.Errorf("bpt: got %f, want 5.0", cfg.EstBytesPerToken)
	}
	if cfg.MaxChunkBytes != 2560 {
		t.Errorf("max: got %d, want 2560", cfg.MaxChunkBytes)
	}
}

func TestChunkerConfigFloors(t *testing.T) {
	cfg := NewChunkerConfig(1, ContentClassSourceCode)

	if cfg.MaxChunkBytes < 64 {
		t.Errorf("max below floor: %d", cfg.MaxChunkBytes)
	}
	if cfg.MinChunkBytes < 16 {
		t.Errorf("min below floor: %d", cfg.MinChunkBytes)
	}
}

func TestBuildLineIndex(t *testing.T) {
	content := []byte("line1\nline2\nline3\n")
	li := buildLineIndex(content)

	if li.lineCount() != 3 {
		t.Fatalf("lineCount: got %d, want 3", li.lineCount())
	}
	if li.byteAt(1) != 0 {
		t.Errorf("line 1 start: got %d, want 0", li.byteAt(1))
	}
	if li.byteAt(2) != 6 {
		t.Errorf("line 2 start: got %d, want 6", li.byteAt(2))
	}
	if li.byteAt(3) != 12 {
		t.Errorf("line 3 start: got %d, want 12", li.byteAt(3))
	}
}

func TestLineAt(t *testing.T) {
	content := []byte("ab\ncd\nef\n")
	li := buildLineIndex(content)

	cases := []struct {
		offset uint32
		want   uint32
	}{
		{0, 1}, {1, 1}, {2, 1},
		{3, 2}, {4, 2}, {5, 2},
		{6, 3}, {7, 3}, {8, 3},
	}

	for _, tc := range cases {
		got := li.lineAt(tc.offset)
		if got != tc.want {
			t.Errorf("lineAt(%d): got %d, want %d", tc.offset, got, tc.want)
		}
	}
}

func TestLineAtEndExclusive(t *testing.T) {
	content := []byte("ab\ncd\nef\n")
	li := buildLineIndex(content)

	if got := li.lineAtEnd(3); got != 1 {
		t.Errorf("lineAtEnd(3): got %d, want 1", got)
	}
	if got := li.lineAtEnd(6); got != 2 {
		t.Errorf("lineAtEnd(6): got %d, want 2", got)
	}
	if got := li.lineAtEnd(9); got != 3 {
		t.Errorf("lineAtEnd(9): got %d, want 3", got)
	}
}

func TestSplitContentSingleChunk(t *testing.T) {
	content := []byte("small content")
	li := buildLineIndex(content)
	cfg := ChunkerConfig{MaxChunkBytes: 1000, MinChunkBytes: 100, OverlapBytes: 50}

	bounds := splitContent(li, uint32(len(content)), nil, cfg, BoundarySentence)

	if len(bounds) != 1 {
		t.Fatalf("got %d chunks, want 1", len(bounds))
	}
	if bounds[0].ByteStart != 0 {
		t.Errorf("start: got %d, want 0", bounds[0].ByteStart)
	}
	if bounds[0].ByteEnd != uint32(len(content)) {
		t.Errorf("end: got %d, want %d", bounds[0].ByteEnd, len(content))
	}
}

func TestSplitContentMultipleChunks(t *testing.T) {
	// 3 blocks of 200 bytes separated by blank lines = ~610 bytes total.
	block := strings.Repeat("x", 200)
	content := []byte(block + "\n\n" + block + "\n\n" + block + "\n")

	li := buildLineIndex(content)
	candidates := findBlankLinePositions(content)
	cfg := ChunkerConfig{MaxChunkBytes: 300, MinChunkBytes: 50, OverlapBytes: 0}

	bounds := splitContent(li, uint32(len(content)), candidates, cfg, BoundarySentence)

	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}
	verifyBoundsCoverage(t, bounds, uint32(len(content)))
}

func TestBestCandidateInRange(t *testing.T) {
	candidates := []uint32{100, 200, 300, 400, 500}

	val, ok := bestCandidateInRange(candidates, 150, 350)
	if !ok {
		t.Fatal("expected to find candidate")
	}
	if val != 300 {
		t.Errorf("got %d, want 300", val)
	}
}

func TestBestCandidateInRangeNone(t *testing.T) {
	candidates := []uint32{100, 200, 300}

	_, ok := bestCandidateInRange(candidates, 350, 500)
	if ok {
		t.Error("expected no candidate found")
	}
}

func TestMergeCandidates(t *testing.T) {
	a := []uint32{10, 30, 50}
	b := []uint32{20, 30, 40}

	merged := mergeCandidates(a, b)

	want := []uint32{10, 20, 30, 40, 50}
	if len(merged) != len(want) {
		t.Fatalf("len: got %d, want %d", len(merged), len(want))
	}
	for i, v := range merged {
		if v != want[i] {
			t.Errorf("[%d]: got %d, want %d", i, v, want[i])
		}
	}
}

func TestFindBlankLinePositions(t *testing.T) {
	content := []byte("hello\n\nworld\n\n\n\nend\n")
	positions := findBlankLinePositions(content)

	if len(positions) != 2 {
		t.Fatalf("got %d positions, want 2", len(positions))
	}
	if positions[0] != 7 {
		t.Errorf("[0]: got %d, want 7", positions[0])
	}
	if positions[1] != 16 {
		t.Errorf("[1]: got %d, want 16", positions[1])
	}
}

func TestChunkerRegistryGet(t *testing.T) {
	reg := NewChunkerRegistry(512)

	if reg.Get(ContentClassSourceCode).Class() != ContentClassSourceCode {
		t.Error("source code chunker class mismatch")
	}
	if reg.Get(ContentClassMarkdown).Class() != ContentClassMarkdown {
		t.Error("markdown chunker class mismatch")
	}
	if reg.Get(ContentClassLLMPrompt).Class() != ContentClassLLMPrompt {
		t.Error("llm prompt chunker class mismatch")
	}
	if reg.Get(ContentClassGitCommit).Class() != ContentClassGitCommit {
		t.Error("git commit chunker class mismatch")
	}
}

func TestChunkerRegistryFallback(t *testing.T) {
	reg := NewChunkerRegistry(512)
	got := reg.Get(ContentClass(255))
	if got.Class() != ContentClassNote {
		t.Errorf("fallback: got %v, want Note", got.Class())
	}
}

func TestSplitContentWithOverlap(t *testing.T) {
	// Build content with clear line boundaries.
	var lines []string
	for i := range 20 {
		lines = append(lines, strings.Repeat("x", 40)+string(rune('A'+i)))
	}
	content := []byte(strings.Join(lines, "\n") + "\n")

	li := buildLineIndex(content)
	candidates := findBlankLinePositions(content)
	cfg := ChunkerConfig{MaxChunkBytes: 300, MinChunkBytes: 50, OverlapBytes: 80}

	bounds := splitContent(li, uint32(len(content)), candidates, cfg, BoundarySentence)

	if len(bounds) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(bounds))
	}

	// Verify overlap: chunk[i+1].ByteStart < chunk[i].ByteEnd for middle chunks.
	for i := 0; i < len(bounds)-1; i++ {
		if bounds[i+1].ByteStart >= bounds[i].ByteEnd {
			t.Errorf("chunks %d-%d: no overlap (next start %d >= prev end %d)",
				i, i+1, bounds[i+1].ByteStart, bounds[i].ByteEnd)
		}
	}
}

// verifyBoundsCoverage checks that chunks cover the full content range.
func verifyBoundsCoverage(t *testing.T, bounds []ChunkBoundary, contentLen uint32) {
	t.Helper()
	if bounds[0].ByteStart != 0 {
		t.Errorf("first chunk doesn't start at 0: %d", bounds[0].ByteStart)
	}
	if bounds[len(bounds)-1].ByteEnd != contentLen {
		t.Errorf("last chunk doesn't end at %d: %d", contentLen, bounds[len(bounds)-1].ByteEnd)
	}
}
