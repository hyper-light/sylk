package extractor

import (
	"bytes"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestRawExtractor_EmitsSingleEntry(t *testing.T) {
	re := NewRawExtractor()
	var captured Entry
	err := re.Extract(bytes.NewReader([]byte("raw bytes")), recipe.ExtractSpec{Format: recipe.ExtractFormatRaw},
		func(e Entry) error { captured = e; return nil })
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if captured.Path != rawCanonicalPath {
		t.Errorf("path = %q, want %q", captured.Path, rawCanonicalPath)
	}
	if string(captured.Content) != "raw bytes" {
		t.Errorf("content = %q, want %q", captured.Content, "raw bytes")
	}
	if captured.Mode != defaultExecutableMode {
		t.Errorf("mode = %#o, want %#o", captured.Mode, defaultExecutableMode)
	}
}

func TestRegistry_DispatchesByFormat(t *testing.T) {
	r := NewDefaultRegistry()
	archive := buildTarArchive(t, map[string]string{"x": "y"})

	collected := []string{}
	err := r.Extract(archive, recipe.ExtractSpec{Format: recipe.ExtractFormatTar},
		func(e Entry) error { collected = append(collected, e.Path); return nil })
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(collected) != 1 || collected[0] != "x" {
		t.Errorf("collected = %v, want [x]", collected)
	}
}

func TestRegistry_UnknownFormatIsError(t *testing.T) {
	r := NewDefaultRegistry()
	err := r.Extract([]byte("anything"), recipe.ExtractSpec{Format: "nonexistent-format"},
		func(Entry) error { return nil })
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}
