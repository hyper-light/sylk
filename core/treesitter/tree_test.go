package treesitter

import (
	"testing"
)

func getTestTree(t *testing.T) *Tree {
	t.Helper()
	skipIfNoGrammar(t, "go")

	p := NewParser()
	t.Cleanup(p.Close)

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName: %v", err)
	}

	tree, err := p.ParseString(`package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	t.Cleanup(tree.Close)

	return tree
}

func TestTreeRootNode(t *testing.T) {
	tree := getTestTree(t)

	root := tree.RootNode()
	if root == nil {
		t.Fatal("RootNode returned nil")
	}

	if root.Type() != "source_file" {
		t.Errorf("root type = %q, want source_file", root.Type())
	}
}

func TestTreeSource(t *testing.T) {
	tree := getTestTree(t)

	source := tree.Source()
	if source == nil {
		t.Fatal("Source returned nil")
	}

	if len(source) == 0 {
		t.Error("Source is empty")
	}
}

func TestNodeType(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.Type() == "" {
		t.Error("Type should not be empty")
	}
}

func TestNodePositions(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.StartByte() != 0 {
		t.Errorf("StartByte = %d, want 0", root.StartByte())
	}

	if root.EndByte() == 0 {
		t.Error("EndByte should not be 0")
	}

	start := root.StartPosition()
	if start.Row != 0 || start.Column != 0 {
		t.Errorf("StartPosition = %+v, want (0,0)", start)
	}

	end := root.EndPosition()
	if end.Row == 0 && end.Column == 0 {
		t.Error("EndPosition should not be (0,0)")
	}
}

func TestNodeChildCount(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.ChildCount() == 0 {
		t.Error("ChildCount should not be 0 for source_file")
	}

	if root.NamedChildCount() == 0 {
		t.Error("NamedChildCount should not be 0 for source_file")
	}
}

func TestNodeChild(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	child := root.Child(0)
	if child == nil {
		t.Fatal("Child(0) returned nil")
	}

	if child.Type() == "" {
		t.Error("Child type should not be empty")
	}

	outOfBounds := root.Child(999)
	if outOfBounds != nil {
		t.Error("Child(999) should return nil for out of bounds")
	}
}

func TestNodeNamedChild(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	namedChild := root.NamedChild(0)
	if namedChild == nil {
		t.Fatal("NamedChild(0) returned nil")
	}

	if !namedChild.IsNamed() {
		t.Error("NamedChild should return named node")
	}
}

func TestNodeParent(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()
	child := root.Child(0)

	if root.Parent() != nil {
		t.Error("Root should have nil parent")
	}

	if child != nil {
		parent := child.Parent()
		if parent == nil {
			t.Error("Child's parent should not be nil")
		}
		if parent.Type() != root.Type() {
			t.Error("Child's parent should be root")
		}
	}
}

func TestNodeSiblings(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.ChildCount() < 2 {
		t.Skip("Need at least 2 children to test siblings")
	}

	first := root.Child(0)
	second := root.Child(1)

	if next := first.NextSibling(); next == nil {
		t.Error("NextSibling should not be nil")
	}

	if prev := second.PrevSibling(); prev == nil {
		t.Error("PrevSibling should not be nil")
	}
}

func TestNodeNamedSiblings(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.NamedChildCount() < 2 {
		t.Skip("Need at least 2 named children to test named siblings")
	}

	first := root.NamedChild(0)
	if next := first.NextNamedSibling(); next == nil {
		t.Error("NextNamedSibling should not be nil")
	}

	second := root.NamedChild(1)
	if prev := second.PrevNamedSibling(); prev == nil {
		t.Error("PrevNamedSibling should not be nil")
	}
}

func TestNodeIsNull(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.IsNull() {
		t.Error("Root should not be null")
	}
}

func TestNodeIsNamed(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if !root.IsNamed() {
		t.Error("source_file should be named")
	}
}

func TestNodeHasError(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	if root.HasError() {
		t.Error("Valid Go code should not have parse errors")
	}
}

func TestNodeContent(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	content := root.Content()
	if content == "" {
		t.Error("Content should not be empty")
	}

	if len(content) != int(root.EndByte()-root.StartByte()) {
		t.Error("Content length should match byte range")
	}
}

func TestNodeString(t *testing.T) {
	tree := getTestTree(t)
	root := tree.RootNode()

	sexp := root.String()
	if sexp == "" {
		t.Error("String (S-expression) should not be empty")
	}

	if sexp[0] != '(' {
		t.Errorf("S-expression should start with '(', got %q", sexp[:10])
	}
}

func TestNodeContentBoundsCheck(t *testing.T) {
	skipIfNoGrammar(t, "go")

	p := NewParser()
	defer p.Close()

	if err := p.SetLanguageByName("go"); err != nil {
		t.Fatalf("SetLanguageByName: %v", err)
	}

	tree, err := p.ParseString(`a`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	content := root.Content()

	if len(content) > len("a")+10 {
		t.Errorf("Content should be bounded, got len=%d", len(content))
	}
}

func TestPoint(t *testing.T) {
	p := Point{Row: 10, Column: 5}

	if p.Row != 10 {
		t.Errorf("Row = %d, want 10", p.Row)
	}
	if p.Column != 5 {
		t.Errorf("Column = %d, want 5", p.Column)
	}
}
