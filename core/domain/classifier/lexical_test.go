package classifier

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

func TestNewLexicalClassifier(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	if lc == nil {
		t.Fatal("NewLexicalClassifier returned nil")
	}
	if lc.config != config {
		t.Error("Config not set correctly")
	}
	if len(lc.patterns) == 0 {
		t.Error("Patterns should be compiled")
	}
}

func TestNewLexicalClassifier_NilConfig(t *testing.T) {
	lc := NewLexicalClassifier(nil)

	if lc == nil {
		t.Fatal("Should not return nil")
	}
	if len(lc.patterns) != 0 {
		t.Error("Patterns should be empty with nil config")
	}
}

func TestLexicalClassifier_Name(t *testing.T) {
	lc := NewLexicalClassifier(nil)

	if lc.Name() != "lexical" {
		t.Errorf("Name() = %s, want lexical", lc.Name())
	}
}

func TestLexicalClassifier_Priority(t *testing.T) {
	lc := NewLexicalClassifier(nil)

	if lc.Priority() != 10 {
		t.Errorf("Priority() = %d, want 10", lc.Priority())
	}
}

func TestLexicalClassifier_Classify_LibrarianKeywords(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "find the function in our code"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.IsEmpty() {
		t.Fatal("Result should not be empty")
	}

	conf := result.GetConfidence(domain.DomainLibrarian)
	if conf == 0 {
		t.Error("Should detect Librarian domain")
	}

	if result.Method != "lexical" {
		t.Errorf("Method = %s, want lexical", result.Method)
	}
}

func TestLexicalClassifier_Classify_AcademicKeywords(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "what is the best practice according to the documentation"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	conf := result.GetConfidence(domain.DomainAcademic)
	if conf == 0 {
		t.Error("Should detect Academic domain")
	}
}

func TestLexicalClassifier_Classify_ArchivalistKeywords(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "what did we decide in the last session"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	conf := result.GetConfidence(domain.DomainArchivalist)
	if conf == 0 {
		t.Error("Should detect Archivalist domain")
	}
}

func TestLexicalClassifier_Classify_TesterKeywords(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "write a unit test with good coverage"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	conf := result.GetConfidence(domain.DomainTester)
	if conf == 0 {
		t.Error("Should detect Tester domain")
	}
}

func TestLexicalClassifier_Classify_EmptyQuery(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()

	result, err := lc.Classify(ctx, "", concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if !result.IsEmpty() {
		t.Error("Empty query should produce empty result")
	}
}

func TestLexicalClassifier_Classify_NoMatch(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "xyzzy nonsense gibberish"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if !result.IsEmpty() {
		t.Error("No matching keywords should produce empty result")
	}
}

func TestLexicalClassifier_Classify_MultipleDomains(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "review the test in our code"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() < 2 {
		t.Error("Should detect multiple domains")
	}

	if result.ShouldContinue == false {
		t.Error("Multiple domains should not terminate early")
	}
}

func TestLexicalClassifier_Classify_HighConfidenceTerminates(t *testing.T) {
	config := &domain.DomainConfig{
		LexicalKeywords: map[domain.Domain][]string{
			domain.DomainLibrarian: {"code", "function", "file", "class", "method"},
		},
	}
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "code function file class method"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() != 1 {
		t.Error("Should have exactly one domain")
	}

	_, conf := result.HighestConfidence()
	if conf < highConfidenceExit {
		t.Errorf("Confidence %f should be >= %f", conf, highConfidenceExit)
	}

	if result.ShouldContinue {
		t.Error("High confidence single domain should terminate")
	}
}

func TestLexicalClassifier_Classify_CaseInsensitive(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "FIND THE FUNCTION IN OUR CODE"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	conf := result.GetConfidence(domain.DomainLibrarian)
	if conf == 0 {
		t.Error("Should detect domain case-insensitively")
	}
}

func TestLexicalClassifier_Classify_ContextCanceled(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lc.Classify(ctx, "test query", concurrency.AgentGuide)
	if err == nil {
		t.Error("Should return error for canceled context")
	}
}

func TestLexicalClassifier_Classify_HasSignals(t *testing.T) {
	config := domain.DefaultDomainConfig()
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "find the function in our code"

	result, err := lc.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	signals := result.GetSignals(domain.DomainLibrarian)
	if len(signals) == 0 {
		t.Error("Should include matched keyword signals")
	}
}

func TestLexicalClassifier_UpdateConfig(t *testing.T) {
	config1 := &domain.DomainConfig{
		LexicalKeywords: map[domain.Domain][]string{
			domain.DomainLibrarian: {"old_keyword"},
		},
	}
	lc := NewLexicalClassifier(config1)

	config2 := &domain.DomainConfig{
		LexicalKeywords: map[domain.Domain][]string{
			domain.DomainLibrarian: {"new_keyword"},
		},
	}
	lc.UpdateConfig(config2)

	ctx := context.Background()

	result1, _ := lc.Classify(ctx, "old_keyword", concurrency.AgentGuide)
	if result1.GetConfidence(domain.DomainLibrarian) != 0 {
		t.Error("Old keyword should not match after config update")
	}

	result2, _ := lc.Classify(ctx, "new_keyword", concurrency.AgentGuide)
	if result2.GetConfidence(domain.DomainLibrarian) == 0 {
		t.Error("New keyword should match after config update")
	}
}

func TestLexicalClassifier_Classify_WordBoundary(t *testing.T) {
	config := &domain.DomainConfig{
		LexicalKeywords: map[domain.Domain][]string{
			domain.DomainTester: {"test"},
		},
	}
	lc := NewLexicalClassifier(config)

	ctx := context.Background()

	result1, _ := lc.Classify(ctx, "run test now", concurrency.AgentGuide)
	if result1.GetConfidence(domain.DomainTester) == 0 {
		t.Error("Should match 'test' as whole word")
	}

	result2, _ := lc.Classify(ctx, "testing something", concurrency.AgentGuide)
	if result2.GetConfidence(domain.DomainTester) != 0 {
		t.Error("Should not match 'testing' when looking for 'test'")
	}
}

func TestLexicalClassifier_Classify_ConfidenceNormalization(t *testing.T) {
	config := &domain.DomainConfig{
		LexicalKeywords: map[domain.Domain][]string{
			domain.DomainLibrarian: {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
		},
	}
	lc := NewLexicalClassifier(config)

	ctx := context.Background()
	query := "a b c d e f g h i j"

	result, _ := lc.Classify(ctx, query, concurrency.AgentGuide)
	_, conf := result.HighestConfidence()

	if conf > 1.0 {
		t.Errorf("Confidence %f should not exceed 1.0", conf)
	}
}

func TestCompileKeywordPatterns(t *testing.T) {
	keywords := []string{"test", "hello world", "func()"}

	patterns := compileKeywordPatterns(keywords)

	if len(patterns) != 3 {
		t.Errorf("Should compile %d patterns, got %d", 3, len(patterns))
	}
}

func TestCountMatches(t *testing.T) {
	patterns := compileKeywordPatterns([]string{"test", "code", "function"})
	query := "test the code and function"

	count := countMatches(query, patterns)

	if count != 3 {
		t.Errorf("countMatches = %d, want 3", count)
	}
}

func TestCalculateConfidence(t *testing.T) {
	tests := []struct {
		matches int
		total   int
		wantMin float64
		wantMax float64
	}{
		{0, 10, 0, 0},
		{1, 10, 0.1, 0.2},
		{5, 10, 0.5, 0.8},
		{10, 10, 1.0, 1.0},
		{0, 0, 0, 0},
	}

	for _, tt := range tests {
		conf := calculateConfidence(tt.matches, tt.total)
		if conf < tt.wantMin || conf > tt.wantMax {
			t.Errorf("calculateConfidence(%d, %d) = %f, want between %f and %f",
				tt.matches, tt.total, conf, tt.wantMin, tt.wantMax)
		}
	}
}

func TestNormalizeScore(t *testing.T) {
	tests := []struct {
		base    float64
		matches int
		wantMax float64
	}{
		{0.5, 2, 0.6},
		{0.9, 5, 1.0},
		{1.0, 10, 1.0},
	}

	for _, tt := range tests {
		score := normalizeScore(tt.base, tt.matches)
		if score > tt.wantMax {
			t.Errorf("normalizeScore(%f, %d) = %f, want <= %f",
				tt.base, tt.matches, score, tt.wantMax)
		}
	}
}
