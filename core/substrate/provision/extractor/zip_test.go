package extractor

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestZipExtractor_RoundTrip(t *testing.T) {
	archive := buildZipArchive(t, map[string]string{
		"sample-1.0/bin/tool":     "binary",
		"sample-1.0/share/doc.md": "docs",
	})

	ze := NewZipExtractor()
	got := collectEntries(t, ze, archive, recipe.ExtractSpec{
		Format:      recipe.ExtractFormatZip,
		StripPrefix: "sample-1.0/",
	})
	assertExtractedFiles(t, got, map[string]string{
		"bin/tool":     "binary",
		"share/doc.md": "docs",
	})
}

func TestZipExtractor_PathEscape(t *testing.T) {
	archive := buildZipArchive(t, map[string]string{
		"../etc/passwd": "evil",
	})

	ze := NewZipExtractor()
	err := ze.Extract(bytes.NewReader(archive), recipe.ExtractSpec{Format: recipe.ExtractFormatZip},
		func(Entry) error { return nil })
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}

func TestZipExtractor_PreservesExecutableBit(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "tool"}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := w.Write([]byte("exec content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ze := NewZipExtractor()
	got := collectEntries(t, ze, buf.Bytes(), recipe.ExtractSpec{
		Format:       recipe.ExtractFormatZip,
		PreserveMode: true,
	})
	entry, ok := got["tool"]
	if !ok {
		t.Fatal("missing entry tool")
	}
	if entry.Mode != 0o755 {
		t.Errorf("mode = %#o, want 0o755", entry.Mode)
	}
}

func TestZipExtractor_SkipsDirectoryEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("emptydir/"); err != nil {
		t.Fatalf("Create dir: %v", err)
	}
	w, err := zw.Create("emptydir/file.txt")
	if err != nil {
		t.Fatalf("Create file: %v", err)
	}
	if _, err := w.Write([]byte("content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ze := NewZipExtractor()
	got := collectEntries(t, ze, buf.Bytes(), recipe.ExtractSpec{
		Format:      recipe.ExtractFormatZip,
		StripPrefix: "emptydir/",
	})
	if _, ok := got["file.txt"]; !ok {
		t.Error("expected file.txt in extracted set")
	}
	if _, ok := got[""]; ok {
		t.Error("did not expect a directory entry to be emitted")
	}
}

func TestZipExtractor_Corrupt(t *testing.T) {
	ze := NewZipExtractor()
	err := ze.Extract(bytes.NewReader([]byte("not a zip file")), recipe.ExtractSpec{Format: recipe.ExtractFormatZip},
		func(Entry) error { return nil })
	if !errors.Is(err, ErrCorruptArchive) {
		t.Fatalf("expected ErrCorruptArchive, got %v", err)
	}
}

func buildZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}
