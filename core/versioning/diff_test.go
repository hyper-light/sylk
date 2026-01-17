package versioning

import (
	"testing"
)

func TestMyersDiffer_EmptyInputs(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)

	t.Run("both empty", func(t *testing.T) {
		diff := differ.DiffBytes([]byte{}, []byte{})
		if len(diff.Hunks) != 0 {
			t.Errorf("Expected 0 hunks for empty inputs, got %d", len(diff.Hunks))
		}
	})

	t.Run("base empty", func(t *testing.T) {
		diff := differ.DiffBytes([]byte{}, []byte("line1\nline2"))
		if diff.Stats.Additions != 2 {
			t.Errorf("Expected 2 additions, got %d", diff.Stats.Additions)
		}
	})

	t.Run("target empty", func(t *testing.T) {
		diff := differ.DiffBytes([]byte("line1\nline2"), []byte{})
		if diff.Stats.Deletions != 2 {
			t.Errorf("Expected 2 deletions, got %d", diff.Stats.Deletions)
		}
	})
}

func TestMyersDiffer_IdenticalInputs(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	content := []byte("line1\nline2\nline3\n")

	diff := differ.DiffBytes(content, content)

	if diff.Stats.Changes != 0 {
		t.Errorf("Expected 0 changes for identical inputs, got %d", diff.Stats.Changes)
	}
}

func TestMyersDiffer_SingleAddition(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	baseLines := []string{"line1", "line3"}
	targetLines := []string{"line1", "line2", "line3"}

	diff := differ.DiffLines(baseLines, targetLines)

	if diff.Stats.Additions != 1 {
		t.Errorf("Expected 1 addition, got %d", diff.Stats.Additions)
	}
	if diff.Stats.Deletions != 0 {
		t.Errorf("Expected 0 deletions, got %d", diff.Stats.Deletions)
	}
}

func TestMyersDiffer_SingleDeletion(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	baseLines := []string{"line1", "line2", "line3"}
	targetLines := []string{"line1", "line3"}

	diff := differ.DiffLines(baseLines, targetLines)

	if diff.Stats.Deletions != 1 {
		t.Errorf("Expected 1 deletion, got %d", diff.Stats.Deletions)
	}
	if diff.Stats.Additions != 0 {
		t.Errorf("Expected 0 additions, got %d", diff.Stats.Additions)
	}
}

func TestMyersDiffer_Modification(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	base := []byte("line1\nold_line\nline3\n")
	target := []byte("line1\nnew_line\nline3\n")

	diff := differ.DiffBytes(base, target)

	if diff.Stats.Additions != 1 || diff.Stats.Deletions != 1 {
		t.Errorf("Expected 1 add + 1 delete for modification, got %d adds, %d deletes",
			diff.Stats.Additions, diff.Stats.Deletions)
	}
}

func TestMyersDiffer_MultipleHunks(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(0)
	base := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")
	target := []byte("a\nB\nc\nd\ne\nf\nG\nh\ni\nj\n")

	diff := differ.DiffBytes(base, target)

	if len(diff.Hunks) < 1 {
		t.Errorf("Expected at least 1 hunk, got %d", len(diff.Hunks))
	}

	if diff.Stats.Additions != 2 || diff.Stats.Deletions != 2 {
		t.Errorf("Expected 2 adds + 2 deletes, got %d adds, %d deletes",
			diff.Stats.Additions, diff.Stats.Deletions)
	}
}

func TestMyersDiffer_HunkLineNumbers(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(1)
	baseLines := []string{"line1", "line2", "line3"}
	targetLines := []string{"line1", "newline", "line3"}

	diff := differ.DiffLines(baseLines, targetLines)

	if len(diff.Hunks) == 0 {
		t.Fatal("Expected at least 1 hunk")
	}

	foundDelete := false
	foundAdd := false
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.Type == DiffLineDelete && line.Content == "line2" {
				foundDelete = true
			}
			if line.Type == DiffLineAdd && line.Content == "newline" {
				foundAdd = true
			}
		}
	}

	if !foundDelete {
		t.Error("Expected to find delete of 'line2'")
	}
	if !foundAdd {
		t.Error("Expected to find add of 'newline'")
	}
}

func TestMyersDiffer_ToOperations(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	base := []byte("line1\nold\nline3\n")
	target := []byte("line1\nnew\nline3\n")

	diff := differ.DiffBytes(base, target)

	baseVersion := VersionID{}
	ops := differ.ToOperations(diff, "/test/file.txt", baseVersion)

	if len(ops) == 0 {
		t.Fatal("Expected operations from diff")
	}

	hasInsert := false
	hasDelete := false
	for _, op := range ops {
		if op.Type == OpInsert {
			hasInsert = true
		}
		if op.Type == OpDelete {
			hasDelete = true
		}
		if op.FilePath != "/test/file.txt" {
			t.Errorf("Expected file path /test/file.txt, got %s", op.FilePath)
		}
	}

	if !hasInsert || !hasDelete {
		t.Errorf("Expected both insert and delete ops, got insert=%v, delete=%v", hasInsert, hasDelete)
	}
}

func TestMyersDiffer_FromOperations(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)

	ops := []Operation{
		{
			Type:    OpInsert,
			Target:  NewLineTarget(2, 2),
			Content: []byte("new line\n"),
		},
		{
			Type:       OpDelete,
			Target:     NewLineTarget(5, 5),
			OldContent: []byte("old line\n"),
		},
	}

	diff := differ.FromOperations(ops)

	if diff.Stats.Additions != 1 {
		t.Errorf("Expected 1 addition, got %d", diff.Stats.Additions)
	}
	if diff.Stats.Deletions != 1 {
		t.Errorf("Expected 1 deletion, got %d", diff.Stats.Deletions)
	}
}

func TestMyersDiffer_RoundTrip(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	base := []byte("line1\nline2\nline3\n")
	target := []byte("line1\nmodified\nline3\nnewline\n")

	diff := differ.DiffBytes(base, target)

	ops := differ.ToOperations(diff, "/test.txt", VersionID{})

	reconstructed := differ.FromOperations(ops)

	if diff.Stats.Additions != reconstructed.Stats.Additions {
		t.Errorf("Additions mismatch: orig=%d, reconstructed=%d",
			diff.Stats.Additions, reconstructed.Stats.Additions)
	}
	if diff.Stats.Deletions != reconstructed.Stats.Deletions {
		t.Errorf("Deletions mismatch: orig=%d, reconstructed=%d",
			diff.Stats.Deletions, reconstructed.Stats.Deletions)
	}
}

func TestMyersDiffer_ContextLines(t *testing.T) {
	t.Parallel()

	base := []byte("a\nb\nc\nd\ne\nf\n")
	target := []byte("a\nb\nC\nd\ne\nf\n")

	t.Run("no context", func(t *testing.T) {
		differ := NewMyersDiffer(0)
		diff := differ.DiffBytes(base, target)

		if len(diff.Hunks) == 0 {
			t.Fatal("Expected hunks")
		}

		hunk := diff.Hunks[0]
		contextCount := 0
		for _, line := range hunk.Lines {
			if line.Type == DiffLineContext {
				contextCount++
			}
		}
		if contextCount != 0 {
			t.Errorf("Expected 0 context lines, got %d", contextCount)
		}
	})

	t.Run("with context", func(t *testing.T) {
		differ := NewMyersDiffer(2)
		diff := differ.DiffBytes(base, target)

		if len(diff.Hunks) == 0 {
			t.Fatal("Expected hunks")
		}

		hunk := diff.Hunks[0]
		contextCount := 0
		for _, line := range hunk.Lines {
			if line.Type == DiffLineContext {
				contextCount++
			}
		}
		if contextCount == 0 {
			t.Error("Expected some context lines with context=2")
		}
	})
}

func TestASTDiffer_FallbackToMyers(t *testing.T) {
	t.Parallel()

	differ := NewASTDiffer()
	base := []byte("line1\nline2\n")
	target := []byte("line1\nmodified\n")

	diff := differ.DiffBytes(base, target)

	if diff.Stats.Additions != 1 || diff.Stats.Deletions != 1 {
		t.Errorf("Expected 1 add + 1 delete, got %d adds, %d deletes",
			diff.Stats.Additions, diff.Stats.Deletions)
	}
}

func TestASTChangeType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		changeType ASTChangeType
		expected   string
	}{
		{ASTChangeAdded, "added"},
		{ASTChangeRemoved, "removed"},
		{ASTChangeModified, "modified"},
		{ASTChangeMoved, "moved"},
		{ASTChangeType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.changeType.String(); got != tt.expected {
				t.Errorf("ASTChangeType.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMyersDiffer_LargeFile(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)

	var baseLines, targetLines []string
	for i := 0; i < 1000; i++ {
		baseLines = append(baseLines, "line"+string(rune('0'+i%10)))
	}
	targetLines = append([]string{}, baseLines...)
	targetLines[500] = "modified_line"
	targetLines = append(targetLines[:750], append([]string{"new_line"}, targetLines[750:]...)...)

	diff := differ.DiffLines(baseLines, targetLines)

	if diff.Stats.Changes == 0 {
		t.Error("Expected some changes in large file diff")
	}
}

func TestDiffLineType_Values(t *testing.T) {
	t.Parallel()

	if DiffLineContext != 0 {
		t.Errorf("DiffLineContext should be 0, got %d", DiffLineContext)
	}
	if DiffLineAdd != 1 {
		t.Errorf("DiffLineAdd should be 1, got %d", DiffLineAdd)
	}
	if DiffLineDelete != 2 {
		t.Errorf("DiffLineDelete should be 2, got %d", DiffLineDelete)
	}
}

func TestFileDiff_EmptyFile(t *testing.T) {
	t.Parallel()

	differ := NewMyersDiffer(3)
	base := []byte("")
	target := []byte("new content\n")

	diff := differ.DiffBytes(base, target)

	if diff.Stats.Additions == 0 {
		t.Error("Expected additions when adding to empty file")
	}
}
