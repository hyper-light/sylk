package extractor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"

	"github.com/klauspost/compress/zstd"
)

func TestTarExtractor_Plain_RoundTrip(t *testing.T) {
	files := map[string]string{
		"sample-1.0/bin/tool":     "binary content",
		"sample-1.0/share/doc.md": "doc content",
	}
	archive := buildTarArchive(t, files)

	te := NewTarExtractor(recipe.ExtractFormatTar)
	got := collectEntries(t, te, archive, recipe.ExtractSpec{
		Format:       recipe.ExtractFormatTar,
		StripPrefix:  "sample-1.0/",
		PreserveMode: true,
	})
	assertExtractedFiles(t, got, map[string]string{
		"bin/tool":     "binary content",
		"share/doc.md": "doc content",
	})
}

func TestTarExtractor_Gzip_RoundTrip(t *testing.T) {
	files := map[string]string{"a/b/c.txt": "hi"}
	archive := buildTarGzArchive(t, files)

	te := NewTarExtractor(recipe.ExtractFormatTarGz)
	got := collectEntries(t, te, archive, recipe.ExtractSpec{
		Format:      recipe.ExtractFormatTarGz,
		StripPrefix: "a/",
	})
	assertExtractedFiles(t, got, map[string]string{"b/c.txt": "hi"})
}

func TestTarExtractor_Zstd_RoundTrip(t *testing.T) {
	files := map[string]string{"x/y.bin": "zstd-content"}
	archive := buildTarZstdArchive(t, files)

	te := NewTarExtractor(recipe.ExtractFormatTarZst)
	got := collectEntries(t, te, archive, recipe.ExtractSpec{
		Format:      recipe.ExtractFormatTarZst,
		StripPrefix: "x/",
	})
	assertExtractedFiles(t, got, map[string]string{"y.bin": "zstd-content"})
}

func TestTarExtractor_Xz_NotImplemented(t *testing.T) {
	te := NewTarExtractor(recipe.ExtractFormatTarXz)
	err := te.Extract(bytes.NewReader([]byte("anything")), recipe.ExtractSpec{
		Format: recipe.ExtractFormatTarXz,
	}, func(Entry) error { return nil })
	if !errors.Is(err, ErrXzNotImplemented) {
		t.Fatalf("expected ErrXzNotImplemented, got %v", err)
	}
}

func TestTarExtractor_PathEscape(t *testing.T) {
	archive := buildTarArchive(t, map[string]string{
		"../../../etc/passwd": "evil",
	})

	te := NewTarExtractor(recipe.ExtractFormatTar)
	err := te.Extract(bytes.NewReader(archive), recipe.ExtractSpec{Format: recipe.ExtractFormatTar},
		func(Entry) error { return nil })
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}

func TestTarExtractor_PreservesMode(t *testing.T) {
	archive := buildTarArchiveWithModes(t, map[string]int64{
		"executable": 0o755,
		"readonly":   0o400,
	})

	te := NewTarExtractor(recipe.ExtractFormatTar)
	got := collectEntries(t, te, archive, recipe.ExtractSpec{
		Format:       recipe.ExtractFormatTar,
		PreserveMode: true,
	})
	if got["executable"].Mode != 0o755 {
		t.Errorf("executable mode = %#o, want 0o755", got["executable"].Mode)
	}
	if got["readonly"].Mode != 0o400 {
		t.Errorf("readonly mode = %#o, want 0o400", got["readonly"].Mode)
	}
}

func TestTarExtractor_FilterIncludeExclude(t *testing.T) {
	archive := buildTarArchive(t, map[string]string{
		"src/main.py":   "main",
		"src/util.py":   "util",
		"tests/main.py": "test",
	})

	te := NewTarExtractor(recipe.ExtractFormatTar)
	got := collectEntries(t, te, archive, recipe.ExtractSpec{
		Format: recipe.ExtractFormatTar,
		Filter: &recipe.Filter{
			Include: []string{"src/*"},
			Exclude: []string{"src/util.py"},
		},
	})
	wantPaths := map[string]bool{"src/main.py": true}
	for path := range got {
		if !wantPaths[path] {
			t.Errorf("filter let through unexpected path: %q", path)
		}
	}
	if _, ok := got["src/main.py"]; !ok {
		t.Error("filter dropped expected path src/main.py")
	}
}

func TestTarExtractor_Symlink(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "lib/libz.so.1",
		Linkname: "libz.so.1.3.1",
		Typeflag: tar.TypeSymlink,
		Mode:     0o777,
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	te := NewTarExtractor(recipe.ExtractFormatTar)
	var captured Entry
	err := te.Extract(bytes.NewReader(buf.Bytes()), recipe.ExtractSpec{Format: recipe.ExtractFormatTar},
		func(e Entry) error { captured = e; return nil })
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !captured.Symlink {
		t.Error("expected Entry.Symlink == true")
	}
	if string(captured.Content) != "libz.so.1.3.1" {
		t.Errorf("symlink content = %q, want target string", captured.Content)
	}
}

func TestTarExtractor_CallbackError_HaltsExtraction(t *testing.T) {
	archive := buildTarArchive(t, map[string]string{
		"a": "1",
		"b": "2",
	})

	te := NewTarExtractor(recipe.ExtractFormatTar)
	stop := errors.New("stop")
	err := te.Extract(bytes.NewReader(archive), recipe.ExtractSpec{Format: recipe.ExtractFormatTar},
		func(Entry) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("expected callback error to propagate, got %v", err)
	}
}

func TestTarExtractor_Corrupt(t *testing.T) {
	te := NewTarExtractor(recipe.ExtractFormatTarGz)
	err := te.Extract(bytes.NewReader([]byte("not gzip bytes at all")), recipe.ExtractSpec{
		Format: recipe.ExtractFormatTarGz,
	}, func(Entry) error { return nil })
	if !errors.Is(err, ErrCorruptArchive) {
		t.Fatalf("expected ErrCorruptArchive, got %v", err)
	}
}

// Test helpers — build in-memory tar/tar.gz/tar.zst archives.

func buildTarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		writeTarFile(t, tw, name, content, 0o644)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	return buf.Bytes()
}

func buildTarArchiveWithModes(t *testing.T, modes map[string]int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, mode := range modes {
		writeTarFile(t, tw, name, "x", mode)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	return buf.Bytes()
}

func writeTarFile(t *testing.T, tw *tar.Writer, name, content string, mode int64) {
	t.Helper()
	hdr := &tar.Header{
		Name:     name,
		Size:     int64(len(content)),
		Mode:     mode,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("WriteHeader %s: %v", name, err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("Write %s: %v", name, err)
	}
}

func buildTarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	tarBytes := buildTarArchive(t, files)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := io.Copy(gw, bytes.NewReader(tarBytes)); err != nil {
		t.Fatalf("gzip copy: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func buildTarZstdArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	tarBytes := buildTarArchive(t, files)
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := io.Copy(enc, bytes.NewReader(tarBytes)); err != nil {
		t.Fatalf("zstd copy: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func collectEntries(t *testing.T, e Extractor, archive []byte, spec recipe.ExtractSpec) map[string]Entry {
	t.Helper()
	out := make(map[string]Entry)
	err := e.Extract(bytes.NewReader(archive), spec, func(entry Entry) error {
		out[entry.Path] = entry
		return nil
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

func assertExtractedFiles(t *testing.T, got map[string]Entry, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d (got=%v)", len(got), len(want), keysOf(got))
	}
	for path, content := range want {
		entry, ok := got[path]
		if !ok {
			t.Errorf("missing path %q", path)
			continue
		}
		if string(entry.Content) != content {
			t.Errorf("path %q: content = %q, want %q", path, entry.Content, content)
		}
	}
}

func keysOf(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
