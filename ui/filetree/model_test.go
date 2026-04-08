package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestReadDirIncludesDotPrefixedEntriesExceptVCSDirs(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".github"))
	mustMkdirAll(t, filepath.Join(root, "visible-dir"))
	mustMkdirAll(t, filepath.Join(root, ".git"))
	mustWriteFile(t, filepath.Join(root, ".env"), "KEY=value\n")
	mustWriteFile(t, filepath.Join(root, "visible.txt"), "visible\n")

	var m Model
	entries := m.readDir(root, 0)
	names := entryNames(entries)

	want := []string{".github", "visible-dir", ".env", "visible.txt"}
	assertSameStrings(t, names, want)
}

func TestCollectSearchableFilesIncludesDotPrefixedEntriesExceptVCSDirs(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".github", "workflows"))
	mustMkdirAll(t, filepath.Join(root, ".git"))
	mustWriteFile(t, filepath.Join(root, ".env"), "KEY=value\n")
	mustWriteFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), "name: ci\n")
	mustWriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWriteFile(t, filepath.Join(root, "visible.txt"), "visible\n")

	got := collectSearchableFiles(root)
	got = relPaths(root, got)

	want := []string{
		".env",
		filepath.ToSlash(filepath.Join(".github", "workflows", "ci.yml")),
		"visible.txt",
	}
	assertSameStrings(t, got, want)
}

func TestViewTreeModeShowsCreationToolbar(t *testing.T) {
	root := t.TempDir()
	m := New(theme.New(theme.DarkPalette))
	m.SetSize(40, 10)
	m.SetRoot(root)
	m.SetFocused(true)

	view := m.View(false)
	if !strings.Contains(view, "New file") {
		t.Fatalf("tree view missing new file action: %q", view)
	}
	if !strings.Contains(view, "New dir") {
		t.Fatalf("tree view missing new dir action: %q", view)
	}
	if got, want := m.bodyHeight(), 5; got != want {
		t.Fatalf("bodyHeight() = %d, want %d", got, want)
	}
}

func TestClickTreeToolbarStartsNewEntry(t *testing.T) {
	root := t.TempDir()
	m := New(theme.New(theme.DarkPalette))
	m.SetSize(40, 10)
	m.SetRoot(root)

	layout := m.treeToolbarLayout(m.width)
	footerY := treeChromeHeight + m.bodyHeight() + footerToolbarOffset

	if cmd := m.ClickAt(layout.fileCol, footerY); cmd != nil {
		t.Fatalf("ClickAt(new file) command = %v, want nil", cmd)
	}
	if !m.newEntryActive {
		t.Fatal("newEntryActive = false, want true after clicking new file action")
	}
	if m.newEntryIsDir {
		t.Fatal("newEntryIsDir = true, want false after clicking new file action")
	}
	if m.newEntryDir != root {
		t.Fatalf("newEntryDir = %q, want %q", m.newEntryDir, root)
	}

	m.exitNewEntry()

	if cmd := m.ClickAt(layout.dirCol, footerY); cmd != nil {
		t.Fatalf("ClickAt(new dir) command = %v, want nil", cmd)
	}
	if !m.newEntryActive {
		t.Fatal("newEntryActive = false, want true after clicking new dir action")
	}
	if !m.newEntryIsDir {
		t.Fatal("newEntryIsDir = false, want true after clicking new dir action")
	}
	if m.newEntryDir != root {
		t.Fatalf("newEntryDir = %q, want %q", m.newEntryDir, root)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func relPaths(root string, paths []string) []string {
	rel := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		rel = append(rel, filepath.ToSlash(trimmed))
	}
	return rel
}

func assertSameStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
