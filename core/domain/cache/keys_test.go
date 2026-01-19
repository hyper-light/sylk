package cache

import (
	"strings"
	"testing"
)

func TestNewKeyGenerator(t *testing.T) {
	kg := NewKeyGenerator("test-namespace")

	if kg == nil {
		t.Fatal("NewKeyGenerator returned nil")
	}
	if kg.namespace != "test-namespace" {
		t.Errorf("namespace = %s, want test-namespace", kg.namespace)
	}
}

func TestKeyGenerator_Generate(t *testing.T) {
	kg := NewKeyGenerator("")

	key := kg.Generate("test query")

	if !strings.HasPrefix(key, keyPrefix) {
		t.Errorf("Key should start with prefix %s, got %s", keyPrefix, key)
	}
	if len(key) == 0 {
		t.Error("Key should not be empty")
	}
}

func TestKeyGenerator_Generate_SameQuery(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.Generate("test query")
	key2 := kg.Generate("test query")

	if key1 != key2 {
		t.Errorf("Same query should produce same key: %s != %s", key1, key2)
	}
}

func TestKeyGenerator_Generate_DifferentQueries(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.Generate("query one")
	key2 := kg.Generate("query two")

	if key1 == key2 {
		t.Error("Different queries should produce different keys")
	}
}

func TestKeyGenerator_Generate_CaseNormalization(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.Generate("Test Query")
	key2 := kg.Generate("test query")
	key3 := kg.Generate("TEST QUERY")

	if key1 != key2 || key2 != key3 {
		t.Errorf("Case should be normalized: %s, %s, %s", key1, key2, key3)
	}
}

func TestKeyGenerator_Generate_WhitespaceNormalization(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.Generate("test  query")
	key2 := kg.Generate("test query")
	key3 := kg.Generate("  test  query  ")

	if key1 != key2 || key2 != key3 {
		t.Error("Whitespace should be normalized")
	}
}

func TestKeyGenerator_Generate_WithNamespace(t *testing.T) {
	kg := NewKeyGenerator("ns1")

	key := kg.Generate("test")

	if !strings.Contains(key, "ns1") {
		t.Errorf("Key should contain namespace: %s", key)
	}
}

func TestKeyGenerator_GenerateWithContext(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.GenerateWithContext("query", "session1")
	key2 := kg.GenerateWithContext("query", "session2")

	if key1 == key2 {
		t.Error("Different sessions should produce different keys")
	}
}

func TestKeyGenerator_GenerateWithContext_SameSession(t *testing.T) {
	kg := NewKeyGenerator("")

	key1 := kg.GenerateWithContext("query", "session1")
	key2 := kg.GenerateWithContext("query", "session1")

	if key1 != key2 {
		t.Error("Same query and session should produce same key")
	}
}

func TestKeyGenerator_SetNamespace(t *testing.T) {
	kg := NewKeyGenerator("old")
	kg.SetNamespace("new")

	if kg.GetNamespace() != "new" {
		t.Errorf("GetNamespace() = %s, want new", kg.GetNamespace())
	}
}

func TestKeyGenerator_GetNamespace(t *testing.T) {
	kg := NewKeyGenerator("my-namespace")

	if kg.GetNamespace() != "my-namespace" {
		t.Errorf("GetNamespace() = %s, want my-namespace", kg.GetNamespace())
	}
}

func TestDefaultKeyGenerator(t *testing.T) {
	kg := DefaultKeyGenerator()

	if kg == nil {
		t.Fatal("DefaultKeyGenerator returned nil")
	}
	if kg.namespace != "" {
		t.Errorf("Default namespace should be empty, got %s", kg.namespace)
	}
}

func TestParseKey(t *testing.T) {
	kg := NewKeyGenerator("namespace")
	key := kg.Generate("test")

	prefix, ns, hash, ok := ParseKey(key)

	if !ok {
		t.Fatal("ParseKey should succeed for valid key")
	}
	if prefix != "domain" {
		t.Errorf("prefix = %s, want domain", prefix)
	}
	if ns != "namespace" {
		t.Errorf("namespace = %s, want namespace", ns)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
}

func TestParseKey_NoNamespace(t *testing.T) {
	kg := NewKeyGenerator("")
	key := kg.Generate("test")

	prefix, ns, hash, ok := ParseKey(key)

	if !ok {
		t.Fatal("ParseKey should succeed")
	}
	if prefix != "domain" {
		t.Errorf("prefix = %s, want domain", prefix)
	}
	if ns != "" {
		t.Errorf("namespace should be empty, got %s", ns)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
}

func TestParseKey_Invalid(t *testing.T) {
	tests := []string{
		"",
		"invalid",
		"other:key",
	}

	for _, key := range tests {
		_, _, _, ok := ParseKey(key)
		if ok {
			t.Errorf("ParseKey should fail for invalid key: %s", key)
		}
	}
}

func TestNormalizeForKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello world"},
		{"  trimmed  ", "trimmed"},
		{"multiple   spaces", "multiple spaces"},
		{"UPPERCASE", "uppercase"},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalizeForKey(tt.input)
		if got != tt.want {
			t.Errorf("normalizeForKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeForKey_LongQuery(t *testing.T) {
	longQuery := strings.Repeat("a", maxKeyLength+100)

	normalized := normalizeForKey(longQuery)

	if len(normalized) > maxKeyLength {
		t.Errorf("Normalized length %d exceeds max %d", len(normalized), maxKeyLength)
	}
}

func TestHashQuery(t *testing.T) {
	hash1 := hashQuery("test")
	hash2 := hashQuery("test")
	hash3 := hashQuery("different")

	if hash1 != hash2 {
		t.Error("Same input should produce same hash")
	}
	if hash1 == hash3 {
		t.Error("Different inputs should produce different hashes")
	}
	if len(hash1) != truncateLength {
		t.Errorf("Hash length = %d, want %d", len(hash1), truncateLength)
	}
}
