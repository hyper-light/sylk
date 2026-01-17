package filesystem

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type testAuditLogger struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (l *testAuditLogger) Log(entry AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *testAuditLogger) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AuditEntry{}, l.entries...)
}

func newTestManager(t *testing.T, roots ...string) (*FilesystemManager, string) {
	t.Helper()

	tmpDir := t.TempDir()
	if len(roots) == 0 {
		roots = []string{tmpDir}
	}

	config := DefaultFilesystemConfig(roots...)
	fm, err := NewFilesystemManager(config)
	if err != nil {
		t.Fatalf("NewFilesystemManager failed: %v", err)
	}

	return fm, tmpDir
}

func TestFilesystemManager_ValidatePath_Success(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	resolved, err := fm.ValidatePath("pipeline-1", testFile)
	if err != nil {
		t.Fatalf("ValidatePath failed: %v", err)
	}

	if resolved != testFile {
		t.Errorf("expected %s, got %s", testFile, resolved)
	}
}

func TestFilesystemManager_ValidatePath_OutsideBoundary(t *testing.T) {
	fm, _ := newTestManager(t)

	_, err := fm.ValidatePath("pipeline-1", "/etc/passwd")
	if err != ErrOutsideBoundary {
		t.Errorf("expected ErrOutsideBoundary, got %v", err)
	}
}

func TestFilesystemManager_ValidatePath_Traversal(t *testing.T) {
	fm, _ := newTestManager(t)

	traversalPath := "../../../etc/passwd"
	_, err := fm.ValidatePath("pipeline-1", traversalPath)
	if err != ErrPathTraversal {
		t.Errorf("expected ErrPathTraversal, got %v", err)
	}
}

func TestFilesystemManager_ValidatePath_HiddenFile(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	hiddenFile := filepath.Join(tmpDir, ".hidden")
	if err := os.WriteFile(hiddenFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := fm.ValidatePath("pipeline-1", hiddenFile)
	if err != ErrOperationDenied {
		t.Errorf("expected ErrOperationDenied for hidden file, got %v", err)
	}
}

func TestFilesystemManager_Read(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	testFile := filepath.Join(tmpDir, "read_test.txt")
	content := []byte("hello world")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	data, err := fm.Read("pipeline-1", testFile)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("expected %s, got %s", content, data)
	}
}

func TestFilesystemManager_Read_NotFound(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	_, err := fm.Read("pipeline-1", filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFilesystemManager_Write(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	testFile := filepath.Join(tmpDir, "write_test.txt")
	content := []byte("test content")

	err := fm.Write("pipeline-1", testFile, content)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != string(content) {
		t.Errorf("expected %s, got %s", content, data)
	}
}

func TestFilesystemManager_Write_ExceedsMaxSize(t *testing.T) {
	fm, tmpDir := newTestManager(t)
	fm.config.MaxFileSize = 10

	testFile := filepath.Join(tmpDir, "large.txt")
	content := make([]byte, 100)

	err := fm.Write("pipeline-1", testFile, content)
	if err != ErrOperationDenied {
		t.Errorf("expected ErrOperationDenied, got %v", err)
	}
}

func TestFilesystemManager_Delete(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	testFile := filepath.Join(tmpDir, "delete_test.txt")
	if err := os.WriteFile(testFile, []byte("delete me"), 0644); err != nil {
		t.Fatal(err)
	}

	err := fm.Delete("pipeline-1", testFile)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestFilesystemManager_CreateDir(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	newDir := filepath.Join(tmpDir, "subdir", "nested")

	err := fm.CreateDir("pipeline-1", newDir)
	if err != nil {
		t.Fatalf("CreateDir failed: %v", err)
	}

	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatal(err)
	}

	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestFilesystemManager_List(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	_ = os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("1"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("2"), 0644)
	_ = os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	infos, err := fm.List("pipeline-1", tmpDir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(infos) != 3 {
		t.Errorf("expected 3 entries, got %d", len(infos))
	}
}

func TestFilesystemManager_List_HiddenFilesExcluded(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	_ = os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte("1"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte("2"), 0644)

	infos, err := fm.List("pipeline-1", tmpDir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(infos) != 1 {
		t.Errorf("expected 1 entry (hidden excluded), got %d", len(infos))
	}
}

func TestFilesystemManager_Copy(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	srcFile := filepath.Join(tmpDir, "source.txt")
	dstFile := filepath.Join(tmpDir, "destination.txt")
	content := []byte("copy me")

	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	err := fm.Copy("pipeline-1", srcFile, dstFile)
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != string(content) {
		t.Errorf("expected %s, got %s", content, data)
	}
}

func TestFilesystemManager_Exists(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	testFile := filepath.Join(tmpDir, "exists_test.txt")
	if err := os.WriteFile(testFile, []byte("exists"), 0644); err != nil {
		t.Fatal(err)
	}

	exists, err := fm.Exists("pipeline-1", testFile)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("file should exist")
	}

	exists, err = fm.Exists("pipeline-1", filepath.Join(tmpDir, "nonexistent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("file should not exist")
	}
}

func TestFilesystemManager_AddRoot(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	newRoot := t.TempDir()
	testFile := filepath.Join(newRoot, "test.txt")
	if err := os.WriteFile(testFile, []byte("new root"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := fm.ValidatePath("pipeline-1", testFile)
	if err != ErrOutsideBoundary {
		t.Errorf("expected ErrOutsideBoundary before adding root")
	}

	err = fm.AddRoot(newRoot)
	if err != nil {
		t.Fatalf("AddRoot failed: %v", err)
	}

	_, err = fm.ValidatePath("pipeline-1", testFile)
	if err != nil {
		t.Errorf("expected success after adding root, got %v", err)
	}

	roots := fm.Roots()
	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %d", len(roots))
	}

	_ = tmpDir
}

func TestFilesystemManager_AuditLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logger := &testAuditLogger{}

	config := DefaultFilesystemConfig(tmpDir)
	config.AuditLogger = logger

	fm, err := NewFilesystemManager(config)
	if err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "audit_test.txt")
	_ = fm.Write("pipeline-1", testFile, []byte("test"))
	_, _ = fm.Read("pipeline-1", testFile)
	_ = fm.Delete("pipeline-1", testFile)

	entries := logger.Entries()
	if len(entries) != 3 {
		t.Errorf("expected 3 audit entries, got %d", len(entries))
	}

	if entries[0].Operation != OpWrite {
		t.Errorf("expected OpWrite, got %s", entries[0].Operation)
	}

	if entries[1].Operation != OpRead {
		t.Errorf("expected OpRead, got %s", entries[1].Operation)
	}

	if entries[2].Operation != OpDelete {
		t.Errorf("expected OpDelete, got %s", entries[2].Operation)
	}
}

func TestFilesystemManager_AllowHidden(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultFilesystemConfig(tmpDir)
	config.AllowHidden = true

	fm, err := NewFilesystemManager(config)
	if err != nil {
		t.Fatal(err)
	}

	hiddenFile := filepath.Join(tmpDir, ".hidden")
	if err := os.WriteFile(hiddenFile, []byte("hidden"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = fm.ValidatePath("pipeline-1", hiddenFile)
	if err != nil {
		t.Errorf("expected success with AllowHidden=true, got %v", err)
	}
}

func TestFilesystemManager_ConcurrentOperations(t *testing.T) {
	fm, tmpDir := newTestManager(t)

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			filename := filepath.Join(tmpDir, "concurrent_"+string(rune('A'+idx))+".txt")
			_ = fm.Write("pipeline-1", filename, []byte("content"))
		}(i)
	}

	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = fm.List("pipeline-1", tmpDir)
		}(i)
	}

	wg.Wait()
}

func TestFilesystemManager_DefaultConfig(t *testing.T) {
	config := DefaultFilesystemConfig("/tmp")

	if len(config.AllowedRoots) != 1 {
		t.Errorf("expected 1 root, got %d", len(config.AllowedRoots))
	}

	if config.AllowSymlinks {
		t.Error("AllowSymlinks should be false by default")
	}

	if config.AllowHidden {
		t.Error("AllowHidden should be false by default")
	}

	if config.MaxFileSize != 100*1024*1024 {
		t.Errorf("expected 100MB max, got %d", config.MaxFileSize)
	}
}

func TestContainsTraversal(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/home/user/file.txt", false},
		{"/home/user/../etc/passwd", true},
		{"./file.txt", false},
		{"../secret", true},
		{"foo/bar/baz", false},
		{"foo/../bar", true},
	}

	for _, tc := range tests {
		result := containsTraversal(tc.path)
		if result != tc.expected {
			t.Errorf("path %q: expected %v, got %v", tc.path, tc.expected, result)
		}
	}
}

func TestIsWithinRoot(t *testing.T) {
	tests := []struct {
		path     string
		root     string
		expected bool
	}{
		{"/home/user/file.txt", "/home/user", true},
		{"/home/user", "/home/user", true},
		{"/home/userfile", "/home/user", false},
		{"/etc/passwd", "/home/user", false},
	}

	for _, tc := range tests {
		result := isWithinRoot(tc.path, tc.root)
		if result != tc.expected {
			t.Errorf("path %q, root %q: expected %v, got %v", tc.path, tc.root, tc.expected, result)
		}
	}
}
