package diffview

import (
	"testing"

	"github.com/adalundhe/sylk/core/search/git"
)

func TestAlignHunks_ContextOnly(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 3,
		NewStart: 1, NewLines: 3,
		Lines: []string{" line1", " line2", " line3"},
	}}
	result := AlignHunks(hunks)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	for i, al := range result {
		if al.Kind != DiffLineContext {
			t.Errorf("line %d: expected Context, got %d", i, al.Kind)
		}
		if al.OldLineNo != i+1 || al.NewLineNo != i+1 {
			t.Errorf("line %d: wrong line numbers: old=%d new=%d", i, al.OldLineNo, al.NewLineNo)
		}
	}
}

func TestAlignHunks_AddedLines(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 1,
		NewStart: 1, NewLines: 3,
		Lines: []string{" ctx", "+added1", "+added2"},
	}}
	result := AlignHunks(hunks)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	if result[0].Kind != DiffLineContext {
		t.Errorf("line 0: expected Context, got %d", result[0].Kind)
	}
	if result[1].Kind != DiffLineAdded || result[1].NewText != "added1" {
		t.Errorf("line 1: expected Added 'added1', got %d '%s'", result[1].Kind, result[1].NewText)
	}
	if result[2].Kind != DiffLineAdded || result[2].NewText != "added2" {
		t.Errorf("line 2: expected Added 'added2', got %d '%s'", result[2].Kind, result[2].NewText)
	}
}

func TestAlignHunks_DeletedLines(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 3,
		NewStart: 1, NewLines: 1,
		Lines: []string{"-del1", "-del2", " ctx"},
	}}
	result := AlignHunks(hunks)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	if result[0].Kind != DiffLineDeleted || result[0].OldText != "del1" {
		t.Errorf("line 0: expected Deleted 'del1', got %d '%s'", result[0].Kind, result[0].OldText)
	}
	if result[1].Kind != DiffLineDeleted || result[1].OldText != "del2" {
		t.Errorf("line 1: expected Deleted 'del2', got %d '%s'", result[1].Kind, result[1].OldText)
	}
	if result[2].Kind != DiffLineContext {
		t.Errorf("line 2: expected Context, got %d", result[2].Kind)
	}
}

func TestAlignHunks_ModifiedLine(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 1,
		NewStart: 1, NewLines: 1,
		Lines: []string{"-old line", "+new line"},
	}}
	result := AlignHunks(hunks)
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	al := result[0]
	if al.Kind != DiffLineModified {
		t.Fatalf("expected Modified, got %d", al.Kind)
	}
	if al.OldText != "old line" || al.NewText != "new line" {
		t.Errorf("wrong text: old='%s' new='%s'", al.OldText, al.NewText)
	}
}

func TestAlignHunks_CharAnnotations(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 1,
		NewStart: 1, NewLines: 1,
		Lines: []string{"-hello world", "+hello earth"},
	}}
	result := AlignHunks(hunks)
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	al := result[0]
	if al.Kind != DiffLineModified {
		t.Fatalf("expected Modified, got %d", al.Kind)
	}

	// "hello " is common prefix (6 chars), "" is common suffix.
	// Old: "world" (bytes 6-11), New: "earth" (bytes 6-11).
	if len(al.OldAnnotations) != 1 {
		t.Fatalf("expected 1 old annotation, got %d", len(al.OldAnnotations))
	}
	if al.OldAnnotations[0].Start != 6 || al.OldAnnotations[0].End != 11 {
		t.Errorf("old annotation: start=%d end=%d, expected 6-11",
			al.OldAnnotations[0].Start, al.OldAnnotations[0].End)
	}
	if len(al.NewAnnotations) != 1 {
		t.Fatalf("expected 1 new annotation, got %d", len(al.NewAnnotations))
	}
	if al.NewAnnotations[0].Start != 6 || al.NewAnnotations[0].End != 11 {
		t.Errorf("new annotation: start=%d end=%d, expected 6-11",
			al.NewAnnotations[0].Start, al.NewAnnotations[0].End)
	}
}

func TestAlignHunks_CharAnnotationsWithCommonSuffix(t *testing.T) {
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 1,
		NewStart: 1, NewLines: 1,
		Lines: []string{"-func foo() int", "+func bar() int"},
	}}
	result := AlignHunks(hunks)
	al := result[0]

	// Common prefix: "func " (5 bytes)
	// Common suffix: "() int" (6 bytes)
	// Old diff: "foo" (bytes 5-8), New diff: "bar" (bytes 5-8)
	if len(al.OldAnnotations) != 1 {
		t.Fatalf("expected 1 old annotation, got %d", len(al.OldAnnotations))
	}
	if al.OldAnnotations[0].Start != 5 || al.OldAnnotations[0].End != 8 {
		t.Errorf("old annotation: start=%d end=%d, expected 5-8",
			al.OldAnnotations[0].Start, al.OldAnnotations[0].End)
	}
}

func TestAlignHunks_MultipleHunksSeparator(t *testing.T) {
	hunks := []git.DiffHunk{
		{
			OldStart: 1, OldLines: 1,
			NewStart: 1, NewLines: 1,
			Lines: []string{" ctx1"},
		},
		{
			OldStart: 10, OldLines: 1,
			NewStart: 10, NewLines: 1,
			Lines: []string{" ctx2"},
		},
	}
	result := AlignHunks(hunks)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines (ctx + sep + ctx), got %d", len(result))
	}
	if result[1].Kind != DiffLineHunkSep {
		t.Errorf("expected HunkSep between hunks, got %d", result[1].Kind)
	}
}

func TestAlignHunks_ExcessDeletions(t *testing.T) {
	// More deletions than additions: extra deletions are flushed as Deleted.
	hunks := []git.DiffHunk{{
		OldStart: 1, OldLines: 3,
		NewStart: 1, NewLines: 1,
		Lines: []string{"-del1", "-del2", "-del3", "+add1"},
	}}
	result := AlignHunks(hunks)
	// del1 pairs with add1 → Modified, del2 + del3 → Deleted
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	if result[0].Kind != DiffLineModified {
		t.Errorf("line 0: expected Modified, got %d", result[0].Kind)
	}
	if result[1].Kind != DiffLineDeleted {
		t.Errorf("line 1: expected Deleted, got %d", result[1].Kind)
	}
	if result[2].Kind != DiffLineDeleted {
		t.Errorf("line 2: expected Deleted, got %d", result[2].Kind)
	}
}

func TestBuildFileBlocks(t *testing.T) {
	files := []git.FileDiff{{
		Path:      "test.go",
		Status:    "M",
		Additions: 2,
		Deletions: 1,
		Hunks: []git.DiffHunk{{
			OldStart: 1, OldLines: 1,
			NewStart: 1, NewLines: 2,
			Lines: []string{"-old", "+new1", "+new2"},
		}},
	}}
	blocks := BuildFileBlocks(files)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	fb := blocks[0]
	if fb.Path != "test.go" || fb.Status != "M" {
		t.Errorf("wrong metadata: path=%s status=%s", fb.Path, fb.Status)
	}
	if fb.Additions != 2 || fb.Deletions != 1 {
		t.Errorf("wrong stats: add=%d del=%d", fb.Additions, fb.Deletions)
	}
	if len(fb.Lines) != 2 {
		t.Fatalf("expected 2 aligned lines, got %d", len(fb.Lines))
	}
}
