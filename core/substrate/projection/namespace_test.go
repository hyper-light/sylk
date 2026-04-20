package projection

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"syscall"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
)

func TestNew_RejectsNilBlobStore(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrNilBlobStore) {
		t.Fatalf("expected ErrNilBlobStore, got %v", err)
	}
}

func TestAttach_AndStat(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "test", "sample", "1.0", map[string]string{
		"/bin/tool": "binary content",
	})

	if err := ns.Attach(Attachment{MountPath: "/", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	info, err := ns.Stat(context.Background(), "/bin/tool")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name() != "tool" {
		t.Errorf("Name = %q, want %q", info.Name(), "tool")
	}
	if info.Size() != int64(len("binary content")) {
		t.Errorf("Size = %d, want %d", info.Size(), len("binary content"))
	}
}

func TestAttach_MountedAtPrefix(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "python", "pytest", "8.3.2", map[string]string{
		"/__init__.py": "py content",
	})

	if err := ns.Attach(Attachment{MountPath: "/python/site-packages/pytest", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	info, err := ns.Stat(context.Background(), "/python/site-packages/pytest/__init__.py")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if string(mustReadFile(t, ns, "/python/site-packages/pytest/__init__.py")) != "py content" {
		t.Errorf("ReadFile returned wrong content")
	}
	if info.IsDir() {
		t.Error("file should not report IsDir")
	}
}

func TestStat_AbsentReturnsENOENT(t *testing.T) {
	ns, _ := newTestNamespace(t)
	_, err := ns.Stat(context.Background(), "/nope")
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestStat_SynthesizedDirectoriesExist(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "x", "y", "1", map[string]string{
		"/a/b/c/file.txt": "content",
	})

	if err := ns.Attach(Attachment{MountPath: "/", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	for _, dir := range []string{"/", "/a", "/a/b", "/a/b/c"} {
		info, err := ns.Stat(context.Background(), dir)
		if err != nil {
			t.Errorf("Stat(%q): %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q) should report directory", dir)
		}
	}
}

func TestListDir_EnumeratesChildren(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "x", "y", "1", map[string]string{
		"/bin/a":      "1",
		"/bin/b":      "2",
		"/bin/sub/c":  "3",
		"/share/doc":  "4",
	})

	if err := ns.Attach(Attachment{MountPath: "/", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	binEntries := mustListDir(t, ns, "/bin")
	binNames := namesOf(binEntries)
	sort.Strings(binNames)
	wantBin := []string{"a", "b", "sub"}
	if !equalStrings(binNames, wantBin) {
		t.Errorf("ListDir(/bin) names = %v, want %v", binNames, wantBin)
	}
}

func TestListDir_AbsentReturnsENOENT(t *testing.T) {
	ns, _ := newTestNamespace(t)
	_, err := ns.ListDir(context.Background(), "/nowhere")
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestReadFile_RoundTrip(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "x", "y", "1", map[string]string{
		"/data.txt": "the quick brown fox",
	})

	if err := ns.Attach(Attachment{MountPath: "/", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	got := mustReadFile(t, ns, "/data.txt")
	if string(got) != "the quick brown fox" {
		t.Errorf("ReadFile = %q, want %q", got, "the quick brown fox")
	}
}

func TestReadFile_AbsentReturnsENOENT(t *testing.T) {
	ns, _ := newTestNamespace(t)
	_, err := ns.ReadFile(context.Background(), "/missing")
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestWrites_AllReturnEROFS(t *testing.T) {
	ns, _ := newTestNamespace(t)
	cases := []struct {
		name string
		op   func() error
	}{
		{"MkdirAll", func() error { return ns.MkdirAll(context.Background(), "/x") }},
		{"WriteFile", func() error { return ns.WriteFile(context.Background(), "/x", []byte{1}) }},
		{"DeleteFile", func() error { return ns.DeleteFile(context.Background(), "/x") }},
		{"Rename", func() error { return ns.Rename(context.Background(), "/a", "/b") }},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, syscall.EROFS) {
			t.Errorf("%s: got %v, want EROFS", c.name, err)
		}
	}
}

func TestReadOnly_True(t *testing.T) {
	ns, _ := newTestNamespace(t)
	if !ns.ReadOnly() {
		t.Error("substrate namespace should report ReadOnly == true")
	}
}

func TestAttach_DetectsConflicts(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	a := registerManifest(t, blobs, "x", "a", "1", map[string]string{"/conflict": "from-a"})
	b := registerManifest(t, blobs, "x", "b", "1", map[string]string{"/conflict": "from-b"})

	if err := ns.Attach(Attachment{MountPath: "/", Manifest: a}); err != nil {
		t.Fatalf("Attach a: %v", err)
	}
	err := ns.Attach(Attachment{MountPath: "/", Manifest: b})
	if !errors.Is(err, ErrPathConflict) {
		t.Fatalf("expected ErrPathConflict, got %v", err)
	}

	// State should be unchanged after the failed attach.
	got := mustReadFile(t, ns, "/conflict")
	if string(got) != "from-a" {
		t.Errorf("conflict left state mutated; got %q, want %q", got, "from-a")
	}
}

func TestAttach_NilManifest(t *testing.T) {
	ns, _ := newTestNamespace(t)
	err := ns.Attach(Attachment{MountPath: "/", Manifest: nil})
	if !errors.Is(err, ErrNilManifest) {
		t.Fatalf("expected ErrNilManifest, got %v", err)
	}
}

func TestDetach_RemovesPaths(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	m := registerManifest(t, blobs, "x", "y", "1", map[string]string{
		"/file": "content",
	})
	if err := ns.Attach(Attachment{MountPath: "/", Manifest: m}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	if err := ns.Detach(m.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if _, err := ns.Stat(context.Background(), "/file"); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("expected ENOENT after Detach, got %v", err)
	}
}

func TestDetach_AbsentReturnsError(t *testing.T) {
	ns, _ := newTestNamespace(t)
	var someID manifest.ID
	someID[0] = 0xff
	err := ns.Detach(someID)
	if !errors.Is(err, ErrNotAttached) {
		t.Fatalf("expected ErrNotAttached, got %v", err)
	}
}

func TestMultipleAttachmentsAtDifferentMounts(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	pyt := registerManifest(t, blobs, "python", "pytest", "8.3.2", map[string]string{
		"/__init__.py": "pytest content",
	})
	rg := registerManifest(t, blobs, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/rg": "ripgrep binary",
	})

	if err := ns.Attach(Attachment{MountPath: "/python/site-packages/pytest", Manifest: pyt}); err != nil {
		t.Fatalf("Attach pytest: %v", err)
	}
	if err := ns.Attach(Attachment{MountPath: "/bin", Manifest: rg}); err != nil {
		t.Fatalf("Attach ripgrep: %v", err)
	}

	if got := string(mustReadFile(t, ns, "/python/site-packages/pytest/__init__.py")); got != "pytest content" {
		t.Errorf("wrong pytest content: %q", got)
	}
	if got := string(mustReadFile(t, ns, "/bin/rg")); got != "ripgrep binary" {
		t.Errorf("wrong ripgrep content: %q", got)
	}

	root := namesOf(mustListDir(t, ns, "/"))
	sort.Strings(root)
	if !contains(root, "bin") || !contains(root, "python") {
		t.Errorf("ListDir(/) = %v, expected to include bin and python", root)
	}
}

func TestAttachments_ListsCurrent(t *testing.T) {
	ns, blobs := newTestNamespace(t)
	a := registerManifest(t, blobs, "x", "a", "1", map[string]string{"/a": "1"})
	b := registerManifest(t, blobs, "x", "b", "1", map[string]string{"/b": "1"})

	_ = ns.Attach(Attachment{MountPath: "/", Manifest: a})
	_ = ns.Attach(Attachment{MountPath: "/sub", Manifest: b})

	atts := ns.Attachments()
	if len(atts) != 2 {
		t.Fatalf("Attachments = %d, want 2", len(atts))
	}
}

// Helpers.

func newTestNamespace(t *testing.T) (*Namespace, *blob.Store) {
	t.Helper()
	blobs := blob.New()
	ns, err := New(blobs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ns, blobs
}

func registerManifest(t *testing.T, blobs *blob.Store, ecosystem, name, version string, files map[string]string) *manifest.Manifest {
	t.Helper()
	entries := make([]manifest.FileEntry, 0, len(files))
	for path, content := range files {
		hash, err := blobs.Put([]byte(content))
		if err != nil {
			t.Fatalf("blob.Put for %q: %v", path, err)
		}
		entries = append(entries, manifest.FileEntry{
			Path:     path,
			BlobHash: hash,
			Mode:     0o644,
		})
	}
	m := &manifest.Manifest{
		Package: manifest.PackageID{Ecosystem: ecosystem, Name: name, Version: version},
		Files:   entries,
	}
	store := manifest.New(blobs)
	if err := store.Register(m); err != nil {
		t.Fatalf("manifest.Register: %v", err)
	}
	return m
}

func mustReadFile(t *testing.T, ns *Namespace, p string) []byte {
	t.Helper()
	got, err := ns.ReadFile(context.Background(), p)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", p, err)
	}
	return got
}

func mustListDir(t *testing.T, ns *Namespace, p string) []fs.DirEntry {
	t.Helper()
	out, err := ns.ListDir(context.Background(), p)
	if err != nil {
		t.Fatalf("ListDir(%q): %v", p, err)
	}
	return out
}

func namesOf(entries []fs.DirEntry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].Name()
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
