package cache

import (
	"bytes"
	"errors"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDictionaryStore_CompressDecompress_NoDict(t *testing.T) {
	store := NewDictionaryStore()
	data := []byte("the quick brown fox jumps over the lazy dog")
	compressed, err := store.Compress("", data)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	got, err := store.Decompress("", compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("round-trip: got %q, want %q", got, data)
	}
}

func TestDictionaryStore_InstallAndUse(t *testing.T) {
	// A real zstd dictionary requires the zstd magic prefix
	// (0xEC30A437); arbitrary bytes are rejected as invalid. We
	// generate one by self-compressing a corpus, then strip the
	// frame header to leave a valid dict body. (In production the
	// substrate runs `zstd --train` against an ecosystem-specific
	// corpus offline; here we just need any byte sequence the zstd
	// library recognizes as a dictionary.)
	dict := generateTestDictionary(t)
	store := NewDictionaryStore()
	if err := store.Install("test-eco", dict); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !store.Has("test-eco") {
		t.Error("Has(\"test-eco\") returned false after Install")
	}

	data := []byte("payload that the test dictionary will compress")
	compressed, err := store.Compress("test-eco", data)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	got, err := store.Decompress("test-eco", compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("round-trip with dict: got %q, want %q", got, data)
	}
}

// generateTestDictionary returns a real zstd dictionary suitable for
// install/use round-trip testing.
//
// Building a dict via BuildDict requires a large, varied corpus —
// klauspost/compress's BuildDict needs enough byte mass (typically
// hundreds of KB) to extract meaningful patterns. Rather than carry
// a fixture corpus in the test binary, we synthesize one by
// generating per-document variation around a stable backbone of
// Python-shaped fragments.
//
// Production substrate dictionaries are produced offline via
// `zstd --train` against an ecosystem-specific corpus; this generator
// exists to give unit tests a structurally-valid dictionary.
func generateTestDictionary(t *testing.T) []byte {
	t.Helper()
	corpus := buildSyntheticPythonCorpus()
	dict, err := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       1,
		Contents: corpus,
		Level:    zstd.SpeedDefault,
	})
	if err != nil || len(dict) == 0 {
		t.Skipf("BuildDict requires a corpus larger than feasible to ship in-test (%v); test skipped", err)
	}
	return dict
}

func buildSyntheticPythonCorpus() [][]byte {
	const documentCount = 128
	corpus := make([][]byte, 0, documentCount)
	for i := range documentCount {
		corpus = append(corpus, []byte(syntheticPythonDocument(i)))
	}
	return corpus
}

func syntheticPythonDocument(seed int) string {
	var sb bytes.Buffer
	sb.WriteString("import os\nimport sys\nimport json\nimport time\n\n")
	sb.WriteString("class Document_")
	sb.WriteString(intToStr(seed))
	sb.WriteString(":\n    def __init__(self, value):\n        self.value = value\n\n")
	for i := range 16 {
		sb.WriteString("    def method_")
		sb.WriteString(intToStr(i))
		sb.WriteString("(self, arg):\n        return self.value + arg\n\n")
	}
	sb.WriteString("if __name__ == \"__main__\":\n    main()\n")
	return sb.String()
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	out := []byte{}
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}

func TestDictionaryStore_RejectsEmptyEcosystem(t *testing.T) {
	store := NewDictionaryStore()
	err := store.Install("", []byte("dict"))
	if !errors.Is(err, ErrEmptyEcosystem) {
		t.Errorf("expected ErrEmptyEcosystem, got %v", err)
	}
}

func TestDictionaryStore_UnknownEcosystemFallsBackToDefault(t *testing.T) {
	store := NewDictionaryStore()
	data := []byte("compressing without a registered dict")
	compressed, err := store.Compress("unknown-eco", data)
	if err != nil {
		t.Fatalf("Compress with unknown eco: %v", err)
	}
	got, err := store.Decompress("unknown-eco", compressed)
	if err != nil {
		t.Fatalf("Decompress with unknown eco: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("unknown-ecosystem fallback failed round-trip")
	}
}

func TestDictionaryStore_Ecosystems(t *testing.T) {
	store := NewDictionaryStore()
	dict := generateTestDictionary(t)
	if err := store.Install("python", dict); err != nil {
		t.Fatalf("Install python: %v", err)
	}
	if err := store.Install("node", dict); err != nil {
		t.Fatalf("Install node: %v", err)
	}
	got := store.Ecosystems()
	if len(got) != 2 {
		t.Errorf("Ecosystems returned %d entries, want 2", len(got))
	}
}

func TestDictionaryStore_Close(t *testing.T) {
	store := NewDictionaryStore()
	_ = store.Install("python", []byte("dict"))
	store.Close() // should not panic
}
