package parsers

import (
	"sync"
	"testing"
)

func TestNewParserRegistry(t *testing.T) {
	r := NewParserRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestParserRegistry_DefaultParsers(t *testing.T) {
	r := NewParserRegistry()

	tests := []string{"go build", "go test", "go vet", "eslint", "git status", "git diff", "git log"}
	for _, tool := range tests {
		if !r.Has(tool) {
			t.Errorf("expected default parser for %q", tool)
		}
	}
}

func TestParserRegistry_Register(t *testing.T) {
	r := NewParserRegistry()

	r.Register("custom-tool", NewGoParser())

	if !r.Has("custom-tool") {
		t.Error("expected registered parser")
	}
}

func TestParserRegistry_Get_NotFound(t *testing.T) {
	r := NewParserRegistry()

	if r.Get("unknown-tool") != nil {
		t.Error("expected nil for unknown tool")
	}
}

func TestParserRegistry_Concurrent(t *testing.T) {
	r := NewParserRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Get("go build")
			r.Has("eslint")
		}(i)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Register("tool-"+string(rune('a'+n)), NewGoParser())
		}(i)
	}

	wg.Wait()
}
