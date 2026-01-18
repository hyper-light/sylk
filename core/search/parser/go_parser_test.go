package parser

import (
	"strings"
	"testing"
)

// =============================================================================
// GoParser Tests
// =============================================================================

func TestGoParser_Extensions(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	extensions := parser.Extensions()

	if len(extensions) != 1 {
		t.Errorf("expected 1 extension, got %d", len(extensions))
	}
	if extensions[0] != ".go" {
		t.Errorf("expected .go, got %s", extensions[0])
	}
}

func TestGoParser_EmptyContent(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	result, err := parser.Parse([]byte{}, "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if len(result.Symbols) != 0 {
		t.Error("empty content should have no symbols")
	}
}

func TestGoParser_ParseFunctions(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

// Hello prints a greeting
func Hello(name string) string {
	return "Hello, " + name
}

func privateFunc() {
}

func (r *Receiver) Method() error {
	return nil
}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	funcSymbols := filterSymbolsByKind(result.Symbols, KindFunction)
	if len(funcSymbols) != 3 {
		t.Errorf("expected 3 functions, got %d", len(funcSymbols))
	}

	// Check exported detection
	hello := findSymbolByName(result.Symbols, "Hello")
	if hello == nil {
		t.Fatal("Hello function not found")
	}
	if !hello.Exported {
		t.Error("Hello should be exported")
	}

	private := findSymbolByName(result.Symbols, "privateFunc")
	if private == nil {
		t.Fatal("privateFunc not found")
	}
	if private.Exported {
		t.Error("privateFunc should not be exported")
	}
}

func TestGoParser_ParseTypes(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

type User struct {
	Name string
	Age  int
}

type privateType struct {
	data []byte
}

type Handler func(w http.ResponseWriter, r *http.Request)
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	typeSymbols := filterSymbolsByKind(result.Symbols, KindType)
	if len(typeSymbols) != 3 {
		t.Errorf("expected 3 types, got %d", len(typeSymbols))
	}

	user := findSymbolByName(result.Symbols, "User")
	if user == nil {
		t.Fatal("User type not found")
	}
	if !user.Exported {
		t.Error("User should be exported")
	}
}

func TestGoParser_ParseInterfaces(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

type Reader interface {
	Read(p []byte) (n int, err error)
}

type writer interface {
	Write(p []byte) (n int, err error)
}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	ifaceSymbols := filterSymbolsByKind(result.Symbols, KindInterface)
	if len(ifaceSymbols) != 2 {
		t.Errorf("expected 2 interfaces, got %d", len(ifaceSymbols))
	}

	reader := findSymbolByName(result.Symbols, "Reader")
	if reader == nil {
		t.Fatal("Reader interface not found")
	}
	if reader.Kind != KindInterface {
		t.Errorf("Reader should be interface, got %s", reader.Kind)
	}
}

func TestGoParser_ParseConstants(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

const MaxSize = 1024

const (
	StatusOK = 200
	statusError = 500
)
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	constSymbols := filterSymbolsByKind(result.Symbols, KindConst)
	if len(constSymbols) != 3 {
		t.Errorf("expected 3 constants, got %d", len(constSymbols))
	}

	maxSize := findSymbolByName(result.Symbols, "MaxSize")
	if maxSize == nil {
		t.Fatal("MaxSize constant not found")
	}
	if !maxSize.Exported {
		t.Error("MaxSize should be exported")
	}
}

func TestGoParser_ParseVariables(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

var GlobalVar = "hello"

var (
	Debug = false
	version = "1.0.0"
)
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	varSymbols := filterSymbolsByKind(result.Symbols, KindVariable)
	if len(varSymbols) != 3 {
		t.Errorf("expected 3 variables, got %d", len(varSymbols))
	}
}

func TestGoParser_ParseImports(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

import (
	"fmt"
	"net/http"
	"github.com/example/pkg"
)

func main() {}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result.Imports) != 3 {
		t.Errorf("expected 3 imports, got %d", len(result.Imports))
	}

	expected := []string{"fmt", "net/http", "github.com/example/pkg"}
	for _, exp := range expected {
		found := false
		for _, imp := range result.Imports {
			if imp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("import %q not found", exp)
		}
	}
}

func TestGoParser_ParseComments(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

// This is a package comment

/*
Multi-line comment
with multiple lines
*/

// Function comment
func Hello() {}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should not be empty")
	}
	if !strings.Contains(result.Comments, "package comment") {
		t.Error("comments should contain 'package comment'")
	}
	if !strings.Contains(result.Comments, "Multi-line comment") {
		t.Error("comments should contain 'Multi-line comment'")
	}
}

func TestGoParser_ParseMetadata(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package mypackage

func Test() {}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Metadata["package"] != "mypackage" {
		t.Errorf("expected package 'mypackage', got %q", result.Metadata["package"])
	}
}

func TestGoParser_FunctionSignature(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

func Add(a, b int) int {
	return a + b
}

func (s *Server) Start(ctx context.Context) error {
	return nil
}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	add := findSymbolByName(result.Symbols, "Add")
	if add == nil {
		t.Fatal("Add function not found")
	}
	if !strings.Contains(add.Signature, "Add(a, b int)") {
		t.Errorf("signature should contain 'Add(a, b int)', got %q", add.Signature)
	}

	start := findSymbolByName(result.Symbols, "Start")
	if start == nil {
		t.Fatal("Start method not found")
	}
	if !strings.Contains(start.Signature, "Server") {
		t.Errorf("signature should contain receiver, got %q", start.Signature)
	}
}

func TestGoParser_MalformedContent(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

func incomplete(
type broken struct

func Valid() {}
`
	result, err := parser.Parse([]byte(content), "test.go")

	// Should not error, but return partial result
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil for malformed content")
	}
}

func TestGoParser_LineNumbers(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

func First() {}

func Second() {}

func Third() {}
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	first := findSymbolByName(result.Symbols, "First")
	if first == nil || first.Line != 3 {
		t.Errorf("First should be at line 3, got %d", first.Line)
	}

	second := findSymbolByName(result.Symbols, "Second")
	if second == nil || second.Line != 5 {
		t.Errorf("Second should be at line 5, got %d", second.Line)
	}
}

func TestGoParser_BlankIdentifier(t *testing.T) {
	t.Parallel()

	parser := NewGoParser()
	content := `package main

var _ = "ignored"

const _ = 0
`
	result, err := parser.Parse([]byte(content), "test.go")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Blank identifiers should be skipped
	for _, sym := range result.Symbols {
		if sym.Name == "_" {
			t.Error("blank identifier should not be included in symbols")
		}
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func filterSymbolsByKind(symbols []Symbol, kind string) []Symbol {
	var result []Symbol
	for _, s := range symbols {
		if s.Kind == kind {
			result = append(result, s)
		}
	}
	return result
}

func findSymbolByName(symbols []Symbol, name string) *Symbol {
	for i, s := range symbols {
		if s.Name == name {
			return &symbols[i]
		}
	}
	return nil
}
