package versioning

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPipelineVFS_ReadWrite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	initialContent := []byte("initial content")
	if err := os.WriteFile(testFile, initialContent, 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	content, err := vfs.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(content) != string(initialContent) {
		t.Errorf("Read content mismatch: got %q, want %q", content, initialContent)
	}

	newContent := []byte("new content")
	if err := vfs.Write(ctx, testFile, newContent); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	content, err = vfs.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Read after write failed: %v", err)
	}
	if string(content) != string(newContent) {
		t.Errorf("Read after write mismatch: got %q, want %q", content, newContent)
	}

	diskContent, _ := os.ReadFile(testFile)
	if string(diskContent) != string(initialContent) {
		t.Errorf("Disk should not be modified: got %q, want %q", diskContent, initialContent)
	}
}

func TestPipelineVFS_CreateNewFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	newFile := filepath.Join(tmpDir, "new.txt")

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()
	content := []byte("new file content")

	if err := vfs.Write(ctx, newFile, content); err != nil {
		t.Fatalf("Write new file failed: %v", err)
	}

	readContent, err := vfs.Read(ctx, newFile)
	if err != nil {
		t.Fatalf("Read new file failed: %v", err)
	}
	if string(readContent) != string(content) {
		t.Errorf("Content mismatch: got %q, want %q", readContent, content)
	}

	mods := vfs.GetModifications()
	if len(mods) != 1 {
		t.Fatalf("Expected 1 modification, got %d", len(mods))
	}
	if mods[0].Operation != FileOpCreate {
		t.Errorf("Expected FileOpCreate, got %v", mods[0].Operation)
	}
}

func TestPipelineVFS_Delete(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "delete_me.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	if err := vfs.Delete(ctx, testFile); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := vfs.Read(ctx, testFile)
	if err != ErrFileNotFound {
		t.Errorf("Expected ErrFileNotFound, got %v", err)
	}

	exists, err := vfs.Exists(ctx, testFile)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if exists {
		t.Error("File should not exist after delete")
	}

	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("File should still exist on disk")
	}
}

func TestPipelineVFS_Exists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existingFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	tests := []struct {
		name   string
		path   string
		exists bool
	}{
		{"existing file", existingFile, true},
		{"non-existing file", filepath.Join(tmpDir, "nonexistent.txt"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := vfs.Exists(ctx, tt.path)
			if err != nil {
				t.Fatalf("Exists failed: %v", err)
			}
			if exists != tt.exists {
				t.Errorf("Exists(%q) = %v, want %v", tt.path, exists, tt.exists)
			}
		})
	}
}

func TestPipelineVFS_List(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	listed, err := vfs.List(ctx, tmpDir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(listed) != len(files) {
		t.Errorf("List returned %d files, want %d", len(listed), len(files))
	}

	for _, f := range files {
		if !containsString(listed, f) {
			t.Errorf("List missing file: %s", f)
		}
	}
}

func TestPipelineVFS_ListIncludesStaged(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	newFile := filepath.Join(tmpDir, "new.txt")
	if err := vfs.Write(ctx, newFile, []byte("new content")); err != nil {
		t.Fatal(err)
	}

	listed, err := vfs.List(ctx, tmpDir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if !containsString(listed, "existing.txt") {
		t.Error("List missing existing.txt")
	}
	if !containsString(listed, "new.txt") {
		t.Error("List missing new.txt (staged)")
	}
}

func TestPipelineVFS_ListExcludesDeleted(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	files := []string{"keep.txt", "delete.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	if err := vfs.Delete(ctx, filepath.Join(tmpDir, "delete.txt")); err != nil {
		t.Fatal(err)
	}

	listed, err := vfs.List(ctx, tmpDir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if !containsString(listed, "keep.txt") {
		t.Error("List missing keep.txt")
	}
	if containsString(listed, "delete.txt") {
		t.Error("List should not include deleted delete.txt")
	}
}

func TestPipelineVFS_ReadOnly(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
		ReadOnly:   true,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	if _, err := vfs.Read(ctx, testFile); err != nil {
		t.Fatalf("Read should succeed in read-only mode: %v", err)
	}

	if err := vfs.Write(ctx, testFile, []byte("new")); err != ErrPermissionDenied {
		t.Errorf("Write should fail with ErrPermissionDenied, got %v", err)
	}

	if err := vfs.Delete(ctx, testFile); err != ErrPermissionDenied {
		t.Errorf("Delete should fail with ErrPermissionDenied, got %v", err)
	}
}

func TestPipelineVFS_PathBoundary(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outsideFile := filepath.Join(os.TempDir(), "outside.txt")

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	if _, err := vfs.Read(ctx, outsideFile); err != ErrPathOutsideBounds {
		t.Errorf("Read outside bounds should fail with ErrPathOutsideBounds, got %v", err)
	}

	if err := vfs.Write(ctx, outsideFile, []byte("content")); err != ErrPathOutsideBounds {
		t.Errorf("Write outside bounds should fail with ErrPathOutsideBounds, got %v", err)
	}
}

func TestPipelineVFS_Transaction(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	tx, err := vfs.BeginTransaction(ctx)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	if err := tx.Write(ctx, testFile, []byte("modified")); err != nil {
		t.Fatalf("Transaction write failed: %v", err)
	}

	content, err := tx.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Transaction read failed: %v", err)
	}
	if string(content) != "modified" {
		t.Errorf("Transaction read mismatch: got %q, want %q", content, "modified")
	}

	mainContent, err := vfs.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Main read failed: %v", err)
	}
	if string(mainContent) != "original" {
		t.Errorf("Main should see original: got %q", mainContent)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	committedContent, err := vfs.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Read after commit failed: %v", err)
	}
	if string(committedContent) != "modified" {
		t.Errorf("After commit should see modified: got %q", committedContent)
	}
}

func TestPipelineVFS_TransactionRollback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	tx, err := vfs.BeginTransaction(ctx)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	if err := tx.Write(ctx, testFile, []byte("modified")); err != nil {
		t.Fatalf("Transaction write failed: %v", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	content, err := vfs.Read(ctx, testFile)
	if err != nil {
		t.Fatalf("Read after rollback failed: %v", err)
	}
	if string(content) != "original" {
		t.Errorf("After rollback should see original: got %q", content)
	}
}

func TestPipelineVFS_DoubleTransaction(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	tx1, err := vfs.BeginTransaction(ctx)
	if err != nil {
		t.Fatalf("First transaction failed: %v", err)
	}

	if _, err := vfs.BeginTransaction(ctx); err != ErrVFSInTransaction {
		t.Errorf("Second transaction should fail with ErrVFSInTransaction, got %v", err)
	}

	if err := tx1.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineVFS_Close(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)

	if err := vfs.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	ctx := context.Background()

	if _, err := vfs.Read(ctx, "test.txt"); err != ErrVFSClosed {
		t.Errorf("Read after close should return ErrVFSClosed, got %v", err)
	}

	if err := vfs.Write(ctx, "test.txt", []byte("content")); err != ErrVFSClosed {
		t.Errorf("Write after close should return ErrVFSClosed, got %v", err)
	}
}

func TestPipelineVFS_GetModifications(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	newFile := filepath.Join(tmpDir, "new.txt")
	if err := vfs.Write(ctx, newFile, []byte("new")); err != nil {
		t.Fatal(err)
	}

	if err := vfs.Write(ctx, existingFile, []byte("modified")); err != nil {
		t.Fatal(err)
	}

	toDelete := filepath.Join(tmpDir, "delete.txt")
	if err := os.WriteFile(toDelete, []byte("delete me"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := vfs.Delete(ctx, toDelete); err != nil {
		t.Fatal(err)
	}

	mods := vfs.GetModifications()
	if len(mods) != 3 {
		t.Fatalf("Expected 3 modifications, got %d", len(mods))
	}

	opCounts := map[FileOp]int{}
	for _, mod := range mods {
		opCounts[mod.Operation]++
	}

	if opCounts[FileOpCreate] != 1 {
		t.Errorf("Expected 1 create, got %d", opCounts[FileOpCreate])
	}
	if opCounts[FileOpModify] != 1 {
		t.Errorf("Expected 1 modify, got %d", opCounts[FileOpModify])
	}
	if opCounts[FileOpDelete] != 1 {
		t.Errorf("Expected 1 delete, got %d", opCounts[FileOpDelete])
	}
}

func TestPipelineVFS_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "concurrent.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if j%2 == 0 {
					vfs.Read(ctx, testFile)
				} else {
					vfs.Write(ctx, testFile, []byte("updated"))
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestPipelineVFS_RelativePath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "test-pipeline",
		SessionID:  "test-session",
		WorkingDir: tmpDir,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	content, err := vfs.Read(ctx, "test.txt")
	if err != nil {
		t.Fatalf("Read relative path failed: %v", err)
	}
	if string(content) != "content" {
		t.Errorf("Content mismatch: got %q", content)
	}
}

type mockPermissionChecker struct {
	allowRead   bool
	allowWrite  bool
	allowDelete bool
}

func (m *mockPermissionChecker) CheckFileRead(ctx context.Context, agentID, agentRole, path string) error {
	if !m.allowRead {
		return ErrPermissionDenied
	}
	return nil
}

func (m *mockPermissionChecker) CheckFileWrite(ctx context.Context, agentID, agentRole, path string) error {
	if !m.allowWrite {
		return ErrPermissionDenied
	}
	return nil
}

func (m *mockPermissionChecker) CheckFileDelete(ctx context.Context, agentID, agentRole, path string) error {
	if !m.allowDelete {
		return ErrPermissionDenied
	}
	return nil
}

func TestPipelineVFS_PermissionChecker(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	checker := &mockPermissionChecker{
		allowRead:   true,
		allowWrite:  false,
		allowDelete: false,
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID:  "test-pipeline",
		SessionID:   "test-session",
		WorkingDir:  tmpDir,
		PermChecker: checker,
	}, nil, nil)
	defer vfs.Close()

	ctx := context.Background()

	if _, err := vfs.Read(ctx, testFile); err != nil {
		t.Errorf("Read should be allowed: %v", err)
	}

	if err := vfs.Write(ctx, testFile, []byte("new")); err != ErrPermissionDenied {
		t.Errorf("Write should be denied, got %v", err)
	}

	if err := vfs.Delete(ctx, testFile); err != ErrPermissionDenied {
		t.Errorf("Delete should be denied, got %v", err)
	}
}
