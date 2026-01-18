package search

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// DocumentType Tests
// =============================================================================

func TestDocumentType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		docType  DocumentType
		expected bool
	}{
		{"source_code is valid", DocTypeSourceCode, true},
		{"markdown is valid", DocTypeMarkdown, true},
		{"config is valid", DocTypeConfig, true},
		{"llm_prompt is valid", DocTypeLLMPrompt, true},
		{"llm_response is valid", DocTypeLLMResponse, true},
		{"web_fetch is valid", DocTypeWebFetch, true},
		{"note is valid", DocTypeNote, true},
		{"git_commit is valid", DocTypeGitCommit, true},
		{"empty string is invalid", DocumentType(""), false},
		{"unknown type is invalid", DocumentType("unknown"), false},
		{"SOURCE_CODE uppercase is invalid", DocumentType("SOURCE_CODE"), false},
		{"random string is invalid", DocumentType("random_type"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.docType.IsValid()
			if got != tt.expected {
				t.Errorf("DocumentType(%q).IsValid() = %v, want %v", tt.docType, got, tt.expected)
			}
		})
	}
}

func TestDocumentType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		docType  DocumentType
		expected string
	}{
		{"source_code", DocTypeSourceCode, "source_code"},
		{"markdown", DocTypeMarkdown, "markdown"},
		{"config", DocTypeConfig, "config"},
		{"llm_prompt", DocTypeLLMPrompt, "llm_prompt"},
		{"llm_response", DocTypeLLMResponse, "llm_response"},
		{"web_fetch", DocTypeWebFetch, "web_fetch"},
		{"note", DocTypeNote, "note"},
		{"git_commit", DocTypeGitCommit, "git_commit"},
		{"empty string", DocumentType(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.docType.String()
			if got != tt.expected {
				t.Errorf("DocumentType.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDocumentType_AllTypesValid(t *testing.T) {
	t.Parallel()

	// Verify that all 8 required types are present and valid
	requiredTypes := []DocumentType{
		DocTypeSourceCode,
		DocTypeMarkdown,
		DocTypeConfig,
		DocTypeLLMPrompt,
		DocTypeLLMResponse,
		DocTypeWebFetch,
		DocTypeNote,
		DocTypeGitCommit,
	}

	if len(requiredTypes) != 8 {
		t.Errorf("Expected 8 document types, got %d", len(requiredTypes))
	}

	for _, dt := range requiredTypes {
		if !dt.IsValid() {
			t.Errorf("DocumentType %q should be valid", dt)
		}
	}
}

// =============================================================================
// SymbolKind Tests
// =============================================================================

func TestSymbolKind_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     SymbolKind
		expected bool
	}{
		{"function is valid", SymbolKindFunction, true},
		{"type is valid", SymbolKindType, true},
		{"variable is valid", SymbolKindVariable, true},
		{"const is valid", SymbolKindConst, true},
		{"interface is valid", SymbolKindInterface, true},
		{"empty string is invalid", SymbolKind(""), false},
		{"unknown kind is invalid", SymbolKind("unknown"), false},
		{"FUNCTION uppercase is invalid", SymbolKind("FUNCTION"), false},
		{"class is invalid", SymbolKind("class"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.kind.IsValid()
			if got != tt.expected {
				t.Errorf("SymbolKind(%q).IsValid() = %v, want %v", tt.kind, got, tt.expected)
			}
		})
	}
}

func TestSymbolKind_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     SymbolKind
		expected string
	}{
		{"function", SymbolKindFunction, "function"},
		{"type", SymbolKindType, "type"},
		{"variable", SymbolKindVariable, "variable"},
		{"const", SymbolKindConst, "const"},
		{"interface", SymbolKindInterface, "interface"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.kind.String()
			if got != tt.expected {
				t.Errorf("SymbolKind.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// SymbolInfo Tests
// =============================================================================

func TestSymbolInfo_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		symbol      *SymbolInfo
		expectedErr error
	}{
		{
			name: "valid function symbol",
			symbol: &SymbolInfo{
				Name:      "MyFunction",
				Kind:      SymbolKindFunction,
				Signature: "func MyFunction(x int) string",
				Line:      10,
				Exported:  true,
			},
			expectedErr: nil,
		},
		{
			name: "valid unexported variable",
			symbol: &SymbolInfo{
				Name:     "myVar",
				Kind:     SymbolKindVariable,
				Line:     5,
				Exported: false,
			},
			expectedErr: nil,
		},
		{
			name: "empty name",
			symbol: &SymbolInfo{
				Name: "",
				Kind: SymbolKindFunction,
				Line: 1,
			},
			expectedErr: ErrSymbolNameEmpty,
		},
		{
			name: "invalid kind",
			symbol: &SymbolInfo{
				Name: "Test",
				Kind: SymbolKind("invalid"),
				Line: 1,
			},
			expectedErr: ErrSymbolKindInvalid,
		},
		{
			name: "empty kind",
			symbol: &SymbolInfo{
				Name: "Test",
				Kind: SymbolKind(""),
				Line: 1,
			},
			expectedErr: ErrSymbolKindInvalid,
		},
		{
			name: "zero line number",
			symbol: &SymbolInfo{
				Name: "Test",
				Kind: SymbolKindFunction,
				Line: 0,
			},
			expectedErr: ErrSymbolLineInvalid,
		},
		{
			name: "negative line number",
			symbol: &SymbolInfo{
				Name: "Test",
				Kind: SymbolKindFunction,
				Line: -5,
			},
			expectedErr: ErrSymbolLineInvalid,
		},
		{
			name: "unicode symbol name",
			symbol: &SymbolInfo{
				Name: "handleUserInput_",
				Kind: SymbolKindFunction,
				Line: 1,
			},
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.symbol.Validate()
			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("SymbolInfo.Validate() error = %v, want %v", err, tt.expectedErr)
			}
		})
	}
}

// =============================================================================
// Document Tests
// =============================================================================

func TestDocument_Validate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	validChecksum := GenerateChecksum([]byte("test content"))

	tests := []struct {
		name        string
		doc         *Document
		expectedErr error
	}{
		{
			name: "valid source code document",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Language:   "go",
				Content:    "package main",
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: nil,
		},
		{
			name: "valid markdown document",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("readme")),
				Path:       "/path/to/README.md",
				Type:       DocTypeMarkdown,
				Content:    "# Title",
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: nil,
		},
		{
			name: "valid document with symbols",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("with symbols")),
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Language:   "go",
				Content:    "package main\nfunc main() {}",
				Symbols:    []string{"main"},
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: nil,
		},
		{
			name: "valid document with imports",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("with imports")),
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Language:   "go",
				Content:    "package main\nimport \"fmt\"",
				Imports:    []string{"fmt"},
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: nil,
		},
		{
			name: "empty ID",
			doc: &Document{
				ID:         "",
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: ErrDocumentIDEmpty,
		},
		{
			name: "empty path",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "",
				Type:       DocTypeSourceCode,
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: ErrDocumentPathEmpty,
		},
		{
			name: "invalid type",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "/path/to/file.txt",
				Type:       DocumentType("invalid"),
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: ErrDocumentTypeInvalid,
		},
		{
			name: "empty type",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "/path/to/file.txt",
				Type:       DocumentType(""),
				Checksum:   validChecksum,
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: ErrDocumentTypeInvalid,
		},
		{
			name: "empty checksum",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Checksum:   "",
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: ErrDocumentChecksumEmpty,
		},
		{
			name: "document with git commit",
			doc: &Document{
				ID:         GenerateDocumentID([]byte("test")),
				Path:       "/path/to/file.go",
				Type:       DocTypeSourceCode,
				Checksum:   validChecksum,
				GitCommit:  "abc123def456",
				ModifiedAt: now,
				IndexedAt:  now,
			},
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.doc.Validate()
			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("Document.Validate() error = %v, want %v", err, tt.expectedErr)
			}
		})
	}
}

func TestDocument_IsCodeDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		docType  DocumentType
		expected bool
	}{
		{"source_code is code", DocTypeSourceCode, true},
		{"markdown is not code", DocTypeMarkdown, false},
		{"config is not code", DocTypeConfig, false},
		{"llm_prompt is not code", DocTypeLLMPrompt, false},
		{"llm_response is not code", DocTypeLLMResponse, false},
		{"web_fetch is not code", DocTypeWebFetch, false},
		{"note is not code", DocTypeNote, false},
		{"git_commit is not code", DocTypeGitCommit, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := &Document{Type: tt.docType}
			got := doc.IsCodeDocument()
			if got != tt.expected {
				t.Errorf("Document.IsCodeDocument() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDocument_HasSymbols(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		symbols  []string
		expected bool
	}{
		{"nil symbols", nil, false},
		{"empty symbols", []string{}, false},
		{"single symbol", []string{"main"}, true},
		{"multiple symbols", []string{"main", "handler", "utils"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := &Document{Symbols: tt.symbols}
			got := doc.HasSymbols()
			if got != tt.expected {
				t.Errorf("Document.HasSymbols() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDocument_IsLLMDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		docType  DocumentType
		expected bool
	}{
		{"llm_prompt is LLM document", DocTypeLLMPrompt, true},
		{"llm_response is LLM document", DocTypeLLMResponse, true},
		{"source_code is not LLM document", DocTypeSourceCode, false},
		{"markdown is not LLM document", DocTypeMarkdown, false},
		{"config is not LLM document", DocTypeConfig, false},
		{"web_fetch is not LLM document", DocTypeWebFetch, false},
		{"note is not LLM document", DocTypeNote, false},
		{"git_commit is not LLM document", DocTypeGitCommit, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := &Document{Type: tt.docType}
			got := doc.IsLLMDocument()
			if got != tt.expected {
				t.Errorf("Document.IsLLMDocument() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// GenerateDocumentID Tests
// =============================================================================

func TestGenerateDocumentID(t *testing.T) {
	t.Parallel()

	t.Run("basic content", func(t *testing.T) {
		t.Parallel()
		content := []byte("hello world")
		id := GenerateDocumentID(content)

		// Verify format: should be 64 hex characters (256 bits / 4 bits per hex)
		if len(id) != 64 {
			t.Errorf("GenerateDocumentID length = %d, want 64", len(id))
		}

		// Verify it's valid hex
		_, err := hex.DecodeString(id)
		if err != nil {
			t.Errorf("GenerateDocumentID returned invalid hex: %v", err)
		}

		// Verify it matches expected SHA-256
		expected := sha256.Sum256(content)
		expectedHex := hex.EncodeToString(expected[:])
		if id != expectedHex {
			t.Errorf("GenerateDocumentID = %q, want %q", id, expectedHex)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		t.Parallel()
		id := GenerateDocumentID([]byte{})

		// SHA-256 of empty string is well-known
		expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if id != expected {
			t.Errorf("GenerateDocumentID([]) = %q, want %q", id, expected)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		id1 := GenerateDocumentID(content)
		id2 := GenerateDocumentID(content)

		if id1 != id2 {
			t.Errorf("GenerateDocumentID is not deterministic: %q != %q", id1, id2)
		}
	})

	t.Run("different content different IDs", func(t *testing.T) {
		t.Parallel()
		id1 := GenerateDocumentID([]byte("content A"))
		id2 := GenerateDocumentID([]byte("content B"))

		if id1 == id2 {
			t.Errorf("Different content should produce different IDs")
		}
	})

	t.Run("unicode content", func(t *testing.T) {
		t.Parallel()
		content := []byte("Hello, World!")
		id := GenerateDocumentID(content)

		if len(id) != 64 {
			t.Errorf("GenerateDocumentID length = %d, want 64", len(id))
		}
	})

	t.Run("very long content", func(t *testing.T) {
		t.Parallel()
		// 1MB of content
		content := make([]byte, 1024*1024)
		for i := range content {
			content[i] = byte(i % 256)
		}
		id := GenerateDocumentID(content)

		if len(id) != 64 {
			t.Errorf("GenerateDocumentID length = %d, want 64", len(id))
		}
	})

	t.Run("special characters", func(t *testing.T) {
		t.Parallel()
		content := []byte("line1\nline2\ttab\r\nwindows\x00null")
		id := GenerateDocumentID(content)

		if len(id) != 64 {
			t.Errorf("GenerateDocumentID length = %d, want 64", len(id))
		}
	})
}

func TestGenerateChecksum(t *testing.T) {
	t.Parallel()

	t.Run("same as GenerateDocumentID", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		id := GenerateDocumentID(content)
		checksum := GenerateChecksum(content)

		if id != checksum {
			t.Errorf("GenerateChecksum should equal GenerateDocumentID: %q != %q", checksum, id)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		t.Parallel()
		checksum := GenerateChecksum([]byte{})
		expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if checksum != expected {
			t.Errorf("GenerateChecksum([]) = %q, want %q", checksum, expected)
		}
	})
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestDocument_UnicodeContent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	unicodeContent := "func main() { fmt.Println(\"Hello, World!\") }"
	doc := &Document{
		ID:         GenerateDocumentID([]byte(unicodeContent)),
		Path:       "/path/to/unicode.go",
		Type:       DocTypeSourceCode,
		Language:   "go",
		Content:    unicodeContent,
		Checksum:   GenerateChecksum([]byte(unicodeContent)),
		ModifiedAt: now,
		IndexedAt:  now,
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Document with unicode content should be valid: %v", err)
	}
}

func TestDocument_VeryLongPath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	longPath := "/a" + strings.Repeat("/very_long_directory_name", 50) + "/file.go"
	doc := &Document{
		ID:         GenerateDocumentID([]byte("content")),
		Path:       longPath,
		Type:       DocTypeSourceCode,
		Content:    "package main",
		Checksum:   GenerateChecksum([]byte("content")),
		ModifiedAt: now,
		IndexedAt:  now,
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Document with long path should be valid: %v", err)
	}
}

func TestDocument_SpecialCharactersInPath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	paths := []string{
		"/path/with spaces/file.go",
		"/path/with-dashes/file.go",
		"/path/with_underscores/file.go",
		"/path/with.dots/file.go",
		"/path/with[brackets]/file.go",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			doc := &Document{
				ID:         GenerateDocumentID([]byte("content")),
				Path:       path,
				Type:       DocTypeSourceCode,
				Content:    "package main",
				Checksum:   GenerateChecksum([]byte("content")),
				ModifiedAt: now,
				IndexedAt:  now,
			}

			if err := doc.Validate(); err != nil {
				t.Errorf("Document with path %q should be valid: %v", path, err)
			}
		})
	}
}

func TestDocument_EmptyOptionalFields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	doc := &Document{
		ID:         GenerateDocumentID([]byte("content")),
		Path:       "/path/to/file.go",
		Type:       DocTypeSourceCode,
		Language:   "", // optional, can be empty
		Content:    "", // content can be empty
		Symbols:    nil,
		Comments:   "",
		Imports:    nil,
		Checksum:   GenerateChecksum([]byte("content")),
		ModifiedAt: now,
		IndexedAt:  now,
		GitCommit:  "", // optional, can be empty
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Document with empty optional fields should be valid: %v", err)
	}
}

func TestDocument_AllDocumentTypes(t *testing.T) {
	t.Parallel()

	now := time.Now()
	allTypes := []DocumentType{
		DocTypeSourceCode,
		DocTypeMarkdown,
		DocTypeConfig,
		DocTypeLLMPrompt,
		DocTypeLLMResponse,
		DocTypeWebFetch,
		DocTypeNote,
		DocTypeGitCommit,
	}

	for _, docType := range allTypes {
		t.Run(docType.String(), func(t *testing.T) {
			t.Parallel()
			doc := &Document{
				ID:         GenerateDocumentID([]byte("content")),
				Path:       "/path/to/file",
				Type:       docType,
				Content:    "test content",
				Checksum:   GenerateChecksum([]byte("content")),
				ModifiedAt: now,
				IndexedAt:  now,
			}

			if err := doc.Validate(); err != nil {
				t.Errorf("Document with type %q should be valid: %v", docType, err)
			}
		})
	}
}

func TestSymbolInfo_AllKinds(t *testing.T) {
	t.Parallel()

	allKinds := []SymbolKind{
		SymbolKindFunction,
		SymbolKindType,
		SymbolKindVariable,
		SymbolKindConst,
		SymbolKindInterface,
	}

	for _, kind := range allKinds {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			symbol := &SymbolInfo{
				Name:     "TestSymbol",
				Kind:     kind,
				Line:     1,
				Exported: true,
			}

			if err := symbol.Validate(); err != nil {
				t.Errorf("SymbolInfo with kind %q should be valid: %v", kind, err)
			}
		})
	}
}

// =============================================================================
// Error Sentinel Tests
// =============================================================================

func TestErrorSentinels(t *testing.T) {
	t.Parallel()

	// Verify that error sentinels are distinct and non-nil
	errs := []error{
		ErrDocumentIDEmpty,
		ErrDocumentPathEmpty,
		ErrDocumentTypeInvalid,
		ErrDocumentChecksumEmpty,
		ErrSymbolNameEmpty,
		ErrSymbolKindInvalid,
		ErrSymbolLineInvalid,
	}

	for i, err := range errs {
		if err == nil {
			t.Errorf("Error sentinel at index %d is nil", i)
			continue
		}

		// Check that error message is non-empty
		if err.Error() == "" {
			t.Errorf("Error sentinel at index %d has empty message", i)
		}

		// Check uniqueness
		for j, other := range errs {
			if i != j && err == other {
				t.Errorf("Error sentinels at indices %d and %d are the same", i, j)
			}
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenerateDocumentID(b *testing.B) {
	content := []byte("This is some content for benchmarking the document ID generation")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateDocumentID(content)
	}
}

func BenchmarkGenerateDocumentID_Large(b *testing.B) {
	content := make([]byte, 1024*1024) // 1MB
	for i := range content {
		content[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateDocumentID(content)
	}
}

func BenchmarkDocument_Validate(b *testing.B) {
	now := time.Now()
	doc := &Document{
		ID:         GenerateDocumentID([]byte("test")),
		Path:       "/path/to/file.go",
		Type:       DocTypeSourceCode,
		Language:   "go",
		Content:    "package main",
		Checksum:   GenerateChecksum([]byte("test")),
		ModifiedAt: now,
		IndexedAt:  now,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.Validate()
	}
}

func BenchmarkSymbolInfo_Validate(b *testing.B) {
	symbol := &SymbolInfo{
		Name:      "TestFunction",
		Kind:      SymbolKindFunction,
		Signature: "func TestFunction(x int) string",
		Line:      10,
		Exported:  true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		symbol.Validate()
	}
}
