package versioning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanWorkspaceImageSkipsControlRoots(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "app", "main.py"), []byte("print('ok')"))
	mustWriteFile(t, filepath.Join(root, ".git", "objects", "blob"), make([]byte, 256))
	mustWriteFile(t, filepath.Join(root, ".sylk", "orchestrator.db"), make([]byte, 512))

	manifest, err := scanWorkspaceImage(root, 1<<20)
	if err != nil {
		t.Fatalf("scan workspace image: %v", err)
	}
	if manifest.totalBytes != int64(len("print('ok')")) {
		t.Fatalf("expected only workspace file bytes, got %d", manifest.totalBytes)
	}
	if len(manifest.entries) != 2 {
		t.Fatalf("expected app dir and main.py entries, got %d", len(manifest.entries))
	}
}

func TestScanWorkspaceImageHonorsGitIgnore(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, ".gitignore"), []byte("*.log\nbuild/*.bin\n"))
	mustWriteFile(t, filepath.Join(root, "app.py"), []byte("print('ok')"))
	mustWriteFile(t, filepath.Join(root, "debug.log"), []byte("ignore me"))
	mustWriteFile(t, filepath.Join(root, "build", "artifact.bin"), []byte("ignore dir"))
	mustWriteFile(t, filepath.Join(root, "build", "keep.txt"), []byte("keep me"))

	manifest, err := scanWorkspaceImage(root, 1<<20)
	if err != nil {
		t.Fatalf("scan workspace image: %v", err)
	}

	paths := make(map[string]struct{}, len(manifest.entries))
	for _, entry := range manifest.entries {
		paths[entry.relPath] = struct{}{}
	}

	assertHasPath(t, paths, ".gitignore")
	assertHasPath(t, paths, "app.py")
	assertHasPath(t, paths, "build")
	assertHasPath(t, paths, "build/keep.txt")
	assertMissingPath(t, paths, "debug.log")
	assertMissingPath(t, paths, "build/artifact.bin")
}

func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertHasPath(t *testing.T, paths map[string]struct{}, want string) {
	t.Helper()
	if _, ok := paths[want]; !ok {
		t.Fatalf("expected path %q in manifest", want)
	}
}

func assertMissingPath(t *testing.T, paths map[string]struct{}, unwanted string) {
	t.Helper()
	if _, ok := paths[unwanted]; ok {
		t.Fatalf("did not expect path %q in manifest", unwanted)
	}
}
