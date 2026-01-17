package parsers

import (
	"testing"
)

func TestGitParser_ParseStatus(t *testing.T) {
	p := NewGitParser()

	output := ` M modified.go
A  staged.go
?? untracked.go
 D deleted.go`

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GitResult)
	if r.Status == nil {
		t.Fatal("expected status result")
	}

	if len(r.Status.Staged) != 1 || r.Status.Staged[0] != "staged.go" {
		t.Errorf("expected staged.go, got %v", r.Status.Staged)
	}
	if len(r.Status.Unstaged) != 2 {
		t.Errorf("expected 2 unstaged files, got %d", len(r.Status.Unstaged))
	}
	if len(r.Status.Untracked) != 1 || r.Status.Untracked[0] != "untracked.go" {
		t.Errorf("expected untracked.go, got %v", r.Status.Untracked)
	}
}

func TestGitParser_ParseDiff(t *testing.T) {
	p := NewGitParser()

	output := "diff --git a/file.go b/file.go\n" +
		"index abc123..def456 100644\n" +
		"--- a/file.go\n" +
		"+++ b/file.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" unchanged\n" +
		"+added line\n" +
		"-removed line\n" +
		" unchanged"

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GitResult)
	if r.Diff == nil {
		t.Fatal("expected diff result")
	}

	if len(r.Diff.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(r.Diff.Files))
	}
	if r.Diff.Files[0].Path != "file.go" {
		t.Errorf("expected file.go, got %q", r.Diff.Files[0].Path)
	}
	if r.Diff.Additions != 1 {
		t.Errorf("expected 1 addition, got %d", r.Diff.Additions)
	}
	if r.Diff.Deletions != 1 {
		t.Errorf("expected 1 deletion, got %d", r.Diff.Deletions)
	}
}

func TestGitParser_ParseLog(t *testing.T) {
	p := NewGitParser()

	output := "commit abc123def456\n" +
		"Author: John Doe <john@example.com>\n" +
		"\n" +
		"    First commit message\n" +
		"\n" +
		"commit 789xyz000111\n" +
		"Author: Jane Smith <jane@example.com>\n" +
		"\n" +
		"    Second commit message"

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GitResult)
	if len(r.Log) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(r.Log))
	}

	if r.Log[0].Hash != "abc123def456" {
		t.Errorf("expected hash abc123def456, got %q", r.Log[0].Hash)
	}
	if r.Log[0].Message != "First commit message" {
		t.Errorf("expected 'First commit message', got %q", r.Log[0].Message)
	}
}

func TestGitParser_ParseEmpty(t *testing.T) {
	p := NewGitParser()

	result, err := p.Parse(nil, nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GitResult)
	if r.Status != nil || r.Diff != nil || r.Log != nil {
		t.Error("expected nil results for empty input")
	}
}

func TestGitParser_ParseDiffMultipleFiles(t *testing.T) {
	p := NewGitParser()

	output := `diff --git a/a.go b/a.go
+line
diff --git a/b.go b/b.go
-line
+line`

	result, _ := p.Parse([]byte(output), nil)
	r := result.(*GitResult)

	if len(r.Diff.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(r.Diff.Files))
	}
	if r.Diff.Additions != 2 {
		t.Errorf("expected 2 additions, got %d", r.Diff.Additions)
	}
	if r.Diff.Deletions != 1 {
		t.Errorf("expected 1 deletion, got %d", r.Diff.Deletions)
	}
}

func TestGitParser_StatusShortFormat(t *testing.T) {
	p := NewGitParser()

	output := `MM both.go
AM added_modified.go`

	result, _ := p.Parse([]byte(output), nil)
	r := result.(*GitResult)

	if len(r.Status.Staged) != 2 {
		t.Errorf("expected 2 staged, got %d", len(r.Status.Staged))
	}
}

func TestGitParser_LogAuthorParsing(t *testing.T) {
	p := NewGitParser()

	output := "commit abc123\n" +
		"Author: Test User <test@test.com>\n" +
		"\n" +
		"    message"

	result, _ := p.Parse([]byte(output), nil)
	r := result.(*GitResult)

	if len(r.Log) == 0 {
		t.Fatal("expected at least 1 commit")
	}
	if r.Log[0].Author != "Test User <test@test.com>" {
		t.Errorf("expected author, got %q", r.Log[0].Author)
	}
}
