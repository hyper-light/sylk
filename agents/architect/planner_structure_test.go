package architect

import (
	"strings"
	"testing"
)

// --- JSON structural analysis ---

func TestAnalyzeJSONStructure_Balanced(t *testing.T) {
	sc := analyzeJSONStructure(`{"key":"value","list":[1,2]}`)
	if sc.unclosedBraces != 0 {
		t.Fatalf("unclosedBraces = %d, want 0", sc.unclosedBraces)
	}
	if sc.unclosedBrackets != 0 {
		t.Fatalf("unclosedBrackets = %d, want 0", sc.unclosedBrackets)
	}
	if sc.inString {
		t.Fatal("inString should be false")
	}
	if !sc.hasStructure {
		t.Fatal("hasStructure should be true")
	}
}

func TestAnalyzeJSONStructure_Unclosed(t *testing.T) {
	sc := analyzeJSONStructure(`{"goals":["ship"],"tasks":[{"id":"t1"`)
	if sc.unclosedBraces != 2 {
		t.Fatalf("unclosedBraces = %d, want 2", sc.unclosedBraces)
	}
	if sc.unclosedBrackets != 1 {
		t.Fatalf("unclosedBrackets = %d, want 1", sc.unclosedBrackets)
	}
}

func TestAnalyzeJSONStructure_InString(t *testing.T) {
	sc := analyzeJSONStructure(`{"key":"unterminated value`)
	if !sc.inString {
		t.Fatal("inString should be true for unterminated string")
	}
}

func TestAnalyzeJSONStructure_EscapedQuotes(t *testing.T) {
	sc := analyzeJSONStructure(`{"key":"value with \"escaped\" quotes"}`)
	if sc.inString {
		t.Fatal("inString should be false (escaped quotes handled)")
	}
	if sc.unclosedBraces != 0 {
		t.Fatalf("unclosedBraces = %d, want 0", sc.unclosedBraces)
	}
}

func TestAnalyzeJSONStructure_NoStructure(t *testing.T) {
	sc := analyzeJSONStructure("plain text without braces")
	if sc.hasStructure {
		t.Fatal("hasStructure should be false for text without JSON delimiters")
	}
}

func TestAnalyzeJSONStructure_BracesInStrings(t *testing.T) {
	// Braces inside strings should not affect the count.
	sc := analyzeJSONStructure(`{"msg":"use {x} and [y]"}`)
	if sc.unclosedBraces != 0 {
		t.Fatalf("unclosedBraces = %d, want 0 (braces in string ignored)", sc.unclosedBraces)
	}
	if sc.unclosedBrackets != 0 {
		t.Fatalf("unclosedBrackets = %d, want 0 (brackets in string ignored)", sc.unclosedBrackets)
	}
}

// --- Markdown structural analysis ---

func TestAnalyzeMarkdownStructure_CodeBlock(t *testing.T) {
	sc := analyzeMarkdownStructure("Some text\n```go\nfunc main() {")
	if !sc.inCodeBlock {
		t.Fatal("inCodeBlock should be true for open fence")
	}
}

func TestAnalyzeMarkdownStructure_ClosedCodeBlock(t *testing.T) {
	sc := analyzeMarkdownStructure("```go\nfunc main() {}\n```\nMore text")
	if sc.inCodeBlock {
		t.Fatal("inCodeBlock should be false (fence closed)")
	}
}

func TestAnalyzeMarkdownStructure_List(t *testing.T) {
	sc := analyzeMarkdownStructure("## Items\n- first\n- second")
	if !sc.inList {
		t.Fatal("inList should be true")
	}
	if sc.lastHeadingLevel != 2 {
		t.Fatalf("lastHeadingLevel = %d, want 2", sc.lastHeadingLevel)
	}
}

func TestAnalyzeMarkdownStructure_Heading(t *testing.T) {
	sc := analyzeMarkdownStructure("# Title\n\n## Subtitle\n\nContent")
	if sc.lastHeadingLevel != 2 {
		t.Fatalf("lastHeadingLevel = %d, want 2", sc.lastHeadingLevel)
	}
}

func TestMarkdownHeadingLevel(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"# Title", 1},
		{"## Subtitle", 2},
		{"### Deep", 3},
		{"Not a heading", 0},
		{"#NoSpace", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := markdownHeadingLevel(tt.line)
		if got != tt.want {
			t.Errorf("markdownHeadingLevel(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

// --- Structural completeness ---

func TestIsStructurallyComplete_JSON_Balanced(t *testing.T) {
	sc := analyzeJSONStructure(`{"complete":true}`)
	if !isStructurallyComplete(sc) {
		t.Fatal("balanced JSON should be structurally complete")
	}
}

func TestIsStructurallyComplete_JSON_Unclosed(t *testing.T) {
	sc := analyzeJSONStructure(`{"incomplete":`)
	if isStructurallyComplete(sc) {
		t.Fatal("unclosed JSON should NOT be structurally complete")
	}
}

func TestIsStructurallyComplete_JSON_NoStructure(t *testing.T) {
	sc := analyzeJSONStructure("plain text")
	if isStructurallyComplete(sc) {
		t.Fatal("text without JSON delimiters should NOT be structurally complete")
	}
}

func TestIsStructurallyComplete_Markdown_OpenFence(t *testing.T) {
	sc := analyzeMarkdownStructure("```python\nprint('hi')")
	if isStructurallyComplete(sc) {
		t.Fatal("open code fence should NOT be structurally complete")
	}
}

func TestIsStructurallyComplete_Markdown_ParagraphBreak(t *testing.T) {
	sc := analyzeMarkdownStructure("Complete paragraph.\n\n")
	if !isStructurallyComplete(sc) {
		t.Fatal("text ending with paragraph break should be structurally complete")
	}
}

func TestIsStructurallyComplete_Markdown_MidSentence(t *testing.T) {
	sc := analyzeMarkdownStructure("This sentence is not")
	if isStructurallyComplete(sc) {
		t.Fatal("text ending mid-sentence should NOT be structurally complete")
	}
}

// --- Continuation prompt construction ---

func TestBuildContinuationPrompt_JSON(t *testing.T) {
	sc := structuralContext{
		mode:             continuationModeJSON,
		unclosedBraces:   3,
		unclosedBrackets: 1,
		inString:         false,
	}
	prompt := buildContinuationPrompt(continuationModeJSON, sc)
	if !strings.Contains(prompt, "3 unclosed braces") {
		t.Fatalf("prompt missing brace hint: %q", prompt)
	}
	if !strings.Contains(prompt, "1 unclosed brackets") {
		t.Fatalf("prompt missing bracket hint: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not repeat") {
		t.Fatalf("prompt missing repetition warning: %q", prompt)
	}
}

func TestBuildContinuationPrompt_JSON_InString(t *testing.T) {
	sc := structuralContext{
		mode:     continuationModeJSON,
		inString: true,
	}
	prompt := buildContinuationPrompt(continuationModeJSON, sc)
	if !strings.Contains(prompt, "string literal") {
		t.Fatalf("prompt missing string state hint: %q", prompt)
	}
}

func TestBuildContinuationPrompt_Text(t *testing.T) {
	sc := structuralContext{
		mode:        continuationModeText,
		inCodeBlock: true,
		inList:      false,
	}
	prompt := buildContinuationPrompt(continuationModeText, sc)
	if !strings.Contains(prompt, "code block") {
		t.Fatalf("prompt missing code block hint: %q", prompt)
	}
	if strings.Contains(prompt, "list") {
		t.Fatalf("prompt should not mention list when not in list: %q", prompt)
	}
}

func TestBuildContinuationPrompt_Text_List(t *testing.T) {
	sc := structuralContext{
		mode:   continuationModeText,
		inList: true,
	}
	prompt := buildContinuationPrompt(continuationModeText, sc)
	if !strings.Contains(prompt, "list") {
		t.Fatalf("prompt missing list hint: %q", prompt)
	}
}
