package treesitter

import (
	"testing"
)

func TestNewParser(t *testing.T) {
	p := NewParser()
	if p == nil {
		t.Fatal("NewParser returned nil")
	}
	defer p.Close()

	if p.inner == nil {
		t.Error("parser inner should not be nil")
	}
}

func TestParserLanguage(t *testing.T) {
	p := NewParser()
	defer p.Close()

	if p.Language() != nil {
		t.Error("Language should be nil before setting")
	}
}

func TestParserSetLanguageByNameInvalid(t *testing.T) {
	p := NewParser()
	defer p.Close()

	err := p.SetLanguageByName("nonexistent_grammar_xyz")
	if err == nil {
		t.Error("SetLanguageByName should error for nonexistent grammar")
	}
}

func TestParserSetLanguageByNamePathTraversal(t *testing.T) {
	p := NewParser()
	defer p.Close()

	err := p.SetLanguageByName("../../../etc/passwd")
	if err == nil {
		t.Error("SetLanguageByName should reject path traversal")
	}
}

func TestParserReset(t *testing.T) {
	p := NewParser()
	defer p.Close()

	p.Reset()
}

func TestParserParseWithoutLanguage(t *testing.T) {
	p := NewParser()
	defer p.Close()

	_, err := p.ParseString("hello world")
	if err == nil {
		t.Error("Parse should error without language set")
	}
}

func TestParserConcurrentAccess(t *testing.T) {
	p := NewParser()
	defer p.Close()

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			p.Reset()
			_ = p.Language()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func skipIfNoGrammar(t *testing.T, name string) {
	t.Helper()
	if !IsGrammarAvailable(name) {
		t.Skipf("grammar %q not available, skipping", name)
	}
}

func TestParserWithGrammar(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName(go): %v", err)
	}

	if p.Language() == nil {
		t.Error("Language should be set after SetLanguageByName")
	}

	tree, err := p.ParseString(`package main

func main() {
	println("hello")
}
`)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		t.Fatal("RootNode returned nil")
	}

	if root.Type() != "source_file" {
		t.Errorf("root type = %q, want source_file", root.Type())
	}
}

func TestParserParseBytes(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName(go): %v", err)
	}

	content := []byte(`package main`)
	tree, err := p.Parse(content, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	if tree.Source() == nil {
		t.Error("Source should not be nil")
	}
	if string(tree.Source()) != "package main" {
		t.Errorf("Source = %q", tree.Source())
	}
}

func TestParserIncrementalParse(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName(go): %v", err)
	}

	tree1, err := p.ParseString(`package main`)
	if err != nil {
		t.Fatalf("Parse 1: %v", err)
	}
	defer tree1.Close()

	tree2, err := p.Parse([]byte(`package main

import "fmt"`), tree1)
	if err != nil {
		t.Fatalf("Parse 2: %v", err)
	}
	defer tree2.Close()
}

func TestParserTreeCopy(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName(go): %v", err)
	}

	tree, err := p.ParseString(`package main`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	copy := tree.Copy()
	if copy == nil {
		t.Fatal("Copy returned nil")
	}
	defer copy.Close()

	if copy.RootNode().Type() != tree.RootNode().Type() {
		t.Error("Copy should have same root type")
	}
}
