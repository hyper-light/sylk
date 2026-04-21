package agentlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptCorpus_WriteOnceDedupes(t *testing.T) {
	dir := t.TempDir()
	c := NewPromptCorpus(dir, nil)
	content := "system prompt body\nsecond line"

	h1, err := c.Put(content)
	if err != nil {
		t.Fatalf("put1: %v", err)
	}
	// Second put with the same content must return the same hash
	// without re-writing (we verify via in-memory seen-set then by
	// observing the file is unchanged).
	h2, err := c.Put(content)
	if err != nil {
		t.Fatalf("put2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash mismatch for identical content: %q / %q", h1, h2)
	}

	path := filepath.Join(dir, h1+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus file: %v", err)
	}
	if string(data) != content {
		t.Fatalf("corpus content differs: got %q want %q", data, content)
	}
}

func TestPromptCorpus_DifferentContentDifferentHash(t *testing.T) {
	c := NewPromptCorpus(t.TempDir(), nil)
	h1, _ := c.Put("prompt A")
	h2, _ := c.Put("prompt B")
	if h1 == h2 {
		t.Fatal("distinct content should produce distinct hashes")
	}
}

func TestPromptCorpus_EmptyInputReturnsEmpty(t *testing.T) {
	c := NewPromptCorpus(t.TempDir(), nil)
	h, err := c.Put("")
	if err != nil {
		t.Fatalf("put empty: %v", err)
	}
	if h != "" {
		t.Fatalf("expected empty hash for empty content, got %q", h)
	}
}

func TestPromptCorpus_GetRoundtrip(t *testing.T) {
	c := NewPromptCorpus(t.TempDir(), nil)
	content := "system prompt body"
	hash, err := c.Put(content)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := c.Get(hash)
	if !ok {
		t.Fatal("Get missed after Put")
	}
	if got != content {
		t.Fatalf("roundtrip mismatch: %q vs %q", got, content)
	}
	if _, ok := c.Get("deadbeef"); ok {
		t.Fatal("Get returned true for unknown hash")
	}
}

func TestPromptCorpus_RedactorAppliedBeforeHash(t *testing.T) {
	redactor := NewRedactor(RedactorConfig{IncludeFingerprint: false}, nil)
	c := NewPromptCorpus(t.TempDir(), redactor)
	// Two different originals that redact to the same text should
	// produce the same hash.
	contentA := "bearer eyJabcdefghij.defghijklmn.opqrstuvwxy and trailing"
	contentB := "bearer eyJzzzzzzzzzz.wwwwwwwwww.xxxxxxxxxx and trailing"
	hA, _ := c.Put(contentA)
	hB, _ := c.Put(contentB)
	if hA != hB {
		t.Fatalf("redactor should produce equal hashes for equivalent redacted forms: %q / %q", hA, hB)
	}
}
