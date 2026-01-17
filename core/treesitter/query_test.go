package treesitter

import (
	"testing"
)

func getTestLanguage(t *testing.T) interface{} {
	t.Helper()
	skipIfNoGrammar(t, "go")

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}
	return lang
}

func TestNewQueryCursor(t *testing.T) {
	cursor := NewQueryCursor()
	if cursor == nil {
		t.Fatal("NewQueryCursor returned nil")
	}
	defer cursor.Close()

	if cursor.inner == nil {
		t.Error("cursor inner should not be nil")
	}
}

func TestNewQueryInvalidPattern(t *testing.T) {
	skipIfNoGrammar(t, "go")

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	_, err = NewQuery(lang, "((((invalid")
	if err == nil {
		t.Error("NewQuery should error on invalid pattern")
	}
}

func TestNewQueryValidPattern(t *testing.T) {
	skipIfNoGrammar(t, "go")

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	query, err := NewQuery(lang, "(identifier) @name")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer query.Close()

	if query.PatternCount() != 1 {
		t.Errorf("PatternCount = %d, want 1", query.PatternCount())
	}

	names := query.CaptureNames()
	if len(names) != 1 || names[0] != "name" {
		t.Errorf("CaptureNames = %v, want [name]", names)
	}
}

func TestQueryMatches(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName: %v", err)
	}

	tree, err := p.ParseString(`package main

func foo() {}
func bar() {}
`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	query, err := NewQuery(lang, "(function_declaration name: (identifier) @fn)")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer query.Close()

	cursor := NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), tree.Source())

	if len(matches) < 2 {
		t.Errorf("expected at least 2 matches, got %d", len(matches))
	}

	fnNames := make(map[string]bool)
	for _, m := range matches {
		for _, cap := range m.Captures {
			if cap.Name == "fn" {
				fnNames[cap.Node.Content()] = true
			}
		}
	}

	if !fnNames["foo"] || !fnNames["bar"] {
		t.Errorf("expected foo and bar, got %v", fnNames)
	}
}

func TestQueryCaptures(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName: %v", err)
	}

	tree, err := p.ParseString(`package main

var x = 1
var y = 2
`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	query, err := NewQuery(lang, "(identifier) @id")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer query.Close()

	cursor := NewQueryCursor()
	defer cursor.Close()

	captures := cursor.Captures(query, tree.RootNode(), tree.Source())

	if len(captures) < 2 {
		t.Errorf("expected at least 2 captures, got %d", len(captures))
	}

	for _, cap := range captures {
		if cap.Name != "id" {
			t.Errorf("unexpected capture name: %s", cap.Name)
		}
		if cap.Node == nil {
			t.Error("capture Node should not be nil")
		}
	}
}

func TestQueryMatchStruct(t *testing.T) {
	m := QueryMatch{
		PatternIndex: 0,
		Captures:     []QueryCapture{},
	}

	if m.PatternIndex != 0 {
		t.Error("PatternIndex should be 0")
	}
	if m.Captures == nil {
		t.Error("Captures should not be nil")
	}
}

func TestQueryCaptureStruct(t *testing.T) {
	c := QueryCapture{
		Name: "test",
		Node: nil,
	}

	if c.Name != "test" {
		t.Errorf("Name = %q, want test", c.Name)
	}
}

func TestQueryMultiplePatterns(t *testing.T) {
	skipIfNoGrammar(t, "go")

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	query, err := NewQuery(lang, `
(function_declaration name: (identifier) @fn)
(type_declaration (type_spec name: (type_identifier) @type))
`)
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer query.Close()

	if query.PatternCount() != 2 {
		t.Errorf("PatternCount = %d, want 2", query.PatternCount())
	}

	names := query.CaptureNames()
	if len(names) != 2 {
		t.Errorf("CaptureNames length = %d, want 2", len(names))
	}
}

func TestQueryEmptySource(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName: %v", err)
	}

	tree, err := p.ParseString("")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	defer tree.Close()

	lang, err := LoadGrammar("go")
	if err != nil {
		t.Fatalf("LoadGrammar: %v", err)
	}

	query, err := NewQuery(lang, "(identifier) @id")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer query.Close()

	cursor := NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), tree.Source())
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty source, got %d", len(matches))
	}
}
