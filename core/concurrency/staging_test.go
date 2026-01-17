package concurrency

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagingManagerBasic(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)

	mgr := NewStagingManager("session1", workDir, stagingRoot)

	staging, err := mgr.CreateStaging("pipeline1")
	if err != nil {
		t.Fatalf("CreateStaging failed: %v", err)
	}

	if staging.PipelineID() != "pipeline1" {
		t.Errorf("PipelineID: got %s, want pipeline1", staging.PipelineID())
	}

	got, ok := mgr.GetStaging("pipeline1")
	if !ok || got != staging {
		t.Error("GetStaging should return created staging")
	}

	_, ok = mgr.GetStaging("nonexistent")
	if ok {
		t.Error("GetStaging should return false for nonexistent")
	}
}

func TestStagingReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "existing.txt"), []byte("original content"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	content, err := staging.ReadFile("existing.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(content) != "original content" {
		t.Errorf("Content: got %s, want 'original content'", content)
	}

	err = staging.WriteFile("existing.txt", []byte("modified content"))
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	content, _ = staging.ReadFile("existing.txt")
	if string(content) != "modified content" {
		t.Errorf("Modified content: got %s, want 'modified content'", content)
	}

	workContent, _ := os.ReadFile(filepath.Join(workDir, "existing.txt"))
	if string(workContent) != "original content" {
		t.Error("Work dir should be unchanged until merge")
	}
}

func TestStagingNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	err := staging.WriteFile("new.txt", []byte("new content"))
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	content, err := staging.ReadFile("new.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(content) != "new content" {
		t.Errorf("Content: got %s, want 'new content'", content)
	}
}

func TestStagingDeleteFile(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "todelete.txt"), []byte("content"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	_, _ = staging.ReadFile("todelete.txt")

	err := staging.DeleteFile("todelete.txt")
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	_, err = staging.ReadFile("todelete.txt")
	if !os.IsNotExist(err) {
		t.Error("File should appear deleted")
	}
}

func TestStagingMerge(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "file1.txt"), []byte("original1"), 0644)
	os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("original2"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	_, _ = staging.ReadFile("file1.txt")
	_ = staging.WriteFile("file1.txt", []byte("modified1"))
	_ = staging.WriteFile("newfile.txt", []byte("new content"))

	result, err := staging.Merge()
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Errorf("Conflicts: got %d, want 0", len(result.Conflicts))
	}

	if len(result.Applied) < 2 {
		t.Errorf("Applied: got %d, want at least 2", len(result.Applied))
	}

	content1, _ := os.ReadFile(filepath.Join(workDir, "file1.txt"))
	if string(content1) != "modified1" {
		t.Errorf("file1: got %s, want 'modified1'", content1)
	}

	newContent, _ := os.ReadFile(filepath.Join(workDir, "newfile.txt"))
	if string(newContent) != "new content" {
		t.Errorf("newfile: got %s, want 'new content'", newContent)
	}

	content2, _ := os.ReadFile(filepath.Join(workDir, "file2.txt"))
	if string(content2) != "original2" {
		t.Errorf("file2 should be unchanged: got %s", content2)
	}
}

func TestStagingConflict(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "conflict.txt"), []byte("original"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	_, _ = staging.ReadFile("conflict.txt")
	_ = staging.WriteFile("conflict.txt", []byte("pipeline modified"))

	os.WriteFile(filepath.Join(workDir, "conflict.txt"), []byte("user modified"), 0644)

	conflicts, err := staging.CheckConflicts()
	if err != nil {
		t.Fatalf("CheckConflicts failed: %v", err)
	}

	if len(conflicts) != 1 {
		t.Fatalf("Conflicts: got %d, want 1", len(conflicts))
	}

	if conflicts[0].Path != "conflict.txt" {
		t.Errorf("Conflict path: got %s", conflicts[0].Path)
	}
	if string(conflicts[0].Ours) != "pipeline modified" {
		t.Errorf("Ours: got %s", conflicts[0].Ours)
	}
	if string(conflicts[0].Theirs) != "user modified" {
		t.Errorf("Theirs: got %s", conflicts[0].Theirs)
	}

	result, err := staging.Merge()
	if err == nil {
		t.Error("Merge should fail with conflicts")
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("Merge conflicts: got %d, want 1", len(result.Conflicts))
	}
}

func TestStagingListModified(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "file1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("content2"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	_, _ = staging.ReadFile("file1.txt")
	_ = staging.WriteFile("file1.txt", []byte("modified"))
	_ = staging.WriteFile("newfile.txt", []byte("new"))
	_ = staging.DeleteFile("file2.txt")

	modified := staging.ListModified()
	if len(modified) < 2 {
		t.Errorf("Modified count: got %d, want at least 2", len(modified))
	}
}

func TestStagingAbort(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	staging, _ := mgr.CreateStaging("pipeline1")

	stagingDir := staging.StagingDir()

	_ = staging.WriteFile("test.txt", []byte("content"))

	if _, err := os.Stat(stagingDir); os.IsNotExist(err) {
		t.Error("Staging dir should exist before abort")
	}

	err := staging.Abort()
	if err != nil {
		t.Fatalf("Abort failed: %v", err)
	}

	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Error("Staging dir should be removed after abort")
	}
}

func TestStagingManagerCloseAll(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)

	mgr := NewStagingManager("session1", workDir, stagingRoot)
	_, _ = mgr.CreateStaging("pipeline1")
	_, _ = mgr.CreateStaging("pipeline2")

	err := mgr.CloseAll()
	if err != nil {
		t.Fatalf("CloseAll failed: %v", err)
	}

	_, ok := mgr.GetStaging("pipeline1")
	if ok {
		t.Error("pipeline1 should be closed")
	}

	_, ok = mgr.GetStaging("pipeline2")
	if ok {
		t.Error("pipeline2 should be closed")
	}
}

func TestStagingConcurrentPipelines(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	stagingRoot := filepath.Join(tmpDir, "staging")

	os.MkdirAll(workDir, 0755)
	os.WriteFile(filepath.Join(workDir, "shared.txt"), []byte("original"), 0644)

	mgr := NewStagingManager("session1", workDir, stagingRoot)

	staging1, _ := mgr.CreateStaging("pipeline1")
	staging2, _ := mgr.CreateStaging("pipeline2")

	_, _ = staging1.ReadFile("shared.txt")
	_, _ = staging2.ReadFile("shared.txt")

	_ = staging1.WriteFile("shared.txt", []byte("modified by 1"))
	_ = staging2.WriteFile("shared.txt", []byte("modified by 2"))

	_, err := staging1.Merge()
	if err != nil {
		t.Fatalf("Merge 1 failed: %v", err)
	}

	_, err = staging2.Merge()
	if err == nil {
		t.Error("Merge 2 should fail with conflict")
	}
}
