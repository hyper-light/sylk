package domain

import (
	"testing"
	"time"
)

func TestDefaultDomainConfig(t *testing.T) {
	cfg := DefaultDomainConfig()

	if cfg.CrossDomainThreshold != 0.65 {
		t.Errorf("CrossDomainThreshold = %f, want 0.65", cfg.CrossDomainThreshold)
	}
	if cfg.SingleDomainThreshold != 0.75 {
		t.Errorf("SingleDomainThreshold = %f, want 0.75", cfg.SingleDomainThreshold)
	}
	if cfg.CacheMaxSize != 10000 {
		t.Errorf("CacheMaxSize = %d, want 10000", cfg.CacheMaxSize)
	}
	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("CacheTTL = %v, want 5m", cfg.CacheTTL)
	}
	if cfg.EmbeddingThreshold != 0.70 {
		t.Errorf("EmbeddingThreshold = %f, want 0.70", cfg.EmbeddingThreshold)
	}
	if !cfg.LLMFallbackEnabled {
		t.Error("LLMFallbackEnabled should be true by default")
	}
	if len(cfg.DefaultDomains) != 1 {
		t.Errorf("DefaultDomains len = %d, want 1", len(cfg.DefaultDomains))
	}
}

func TestDefaultLexicalKeywords(t *testing.T) {
	keywords := DefaultLexicalKeywords()

	for _, d := range ValidDomains() {
		kw := keywords[d]
		if len(kw) == 0 {
			t.Errorf("Domain %s has no keywords", d)
		}
	}
}

func TestDomainConfig_Clone(t *testing.T) {
	cfg := DefaultDomainConfig()
	clone := cfg.Clone()

	if clone.CrossDomainThreshold != cfg.CrossDomainThreshold {
		t.Error("Clone CrossDomainThreshold mismatch")
	}
	if clone.CacheMaxSize != cfg.CacheMaxSize {
		t.Error("Clone CacheMaxSize mismatch")
	}

	cfg.CrossDomainThreshold = 0.99
	if clone.CrossDomainThreshold == 0.99 {
		t.Error("Clone should not be affected by original modification")
	}

	cfg.LexicalKeywords[DomainLibrarian] = []string{"modified"}
	if len(clone.LexicalKeywords[DomainLibrarian]) == 1 {
		t.Error("Clone keywords should not be affected by original modification")
	}
}

func TestDomainConfig_CloneNil(t *testing.T) {
	var cfg *DomainConfig
	clone := cfg.Clone()
	if clone != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestDomainConfig_SetGetLexicalKeywords(t *testing.T) {
	cfg := &DomainConfig{}

	keywords := []string{"test1", "test2"}
	cfg.SetLexicalKeywords(DomainLibrarian, keywords)

	got := cfg.GetLexicalKeywords(DomainLibrarian)
	if len(got) != 2 {
		t.Errorf("GetLexicalKeywords len = %d, want 2", len(got))
	}
}

func TestDomainConfig_GetLexicalKeywordsNil(t *testing.T) {
	cfg := &DomainConfig{}

	got := cfg.GetLexicalKeywords(DomainLibrarian)
	if got != nil {
		t.Error("GetLexicalKeywords on nil map should return nil")
	}
}

func TestDomainConfig_Validate_Valid(t *testing.T) {
	cfg := DefaultDomainConfig()
	issues := cfg.Validate()

	if len(issues) != 0 {
		t.Errorf("DefaultDomainConfig should be valid, got issues: %v", issues)
	}
}

func TestDomainConfig_Validate_InvalidCrossDomainThreshold(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.CrossDomainThreshold = 0
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CrossDomainThreshold = 0")
	}

	cfg.CrossDomainThreshold = 1.5
	issues = cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CrossDomainThreshold > 1")
	}
}

func TestDomainConfig_Validate_InvalidSingleDomainThreshold(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.SingleDomainThreshold = 0
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for SingleDomainThreshold = 0")
	}

	cfg.SingleDomainThreshold = 1.5
	issues = cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for SingleDomainThreshold > 1")
	}
}

func TestDomainConfig_Validate_ThresholdsOrder(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.CrossDomainThreshold = 0.80
	cfg.SingleDomainThreshold = 0.75

	issues := cfg.Validate()
	found := false
	for _, issue := range issues {
		if issue == "CrossDomainThreshold should be less than SingleDomainThreshold" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Should have issue for threshold order")
	}
}

func TestDomainConfig_Validate_InvalidCacheMaxSize(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.CacheMaxSize = 0
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CacheMaxSize = 0")
	}

	cfg.CacheMaxSize = -1
	issues = cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CacheMaxSize < 0")
	}
}

func TestDomainConfig_Validate_InvalidCacheTTL(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.CacheTTL = 0
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CacheTTL = 0")
	}

	cfg.CacheTTL = -1 * time.Minute
	issues = cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for CacheTTL < 0")
	}
}

func TestDomainConfig_Validate_InvalidEmbeddingThreshold(t *testing.T) {
	cfg := DefaultDomainConfig()
	cfg.EmbeddingThreshold = 0
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for EmbeddingThreshold = 0")
	}

	cfg.EmbeddingThreshold = 1.5
	issues = cfg.Validate()
	if len(issues) == 0 {
		t.Error("Should have issues for EmbeddingThreshold > 1")
	}
}

func TestLibrarianKeywords(t *testing.T) {
	kw := librarianKeywords()
	if len(kw) == 0 {
		t.Error("librarianKeywords should not be empty")
	}
	containsCodeRelated := false
	for _, k := range kw {
		if k == "our code" || k == "function" || k == "file" {
			containsCodeRelated = true
			break
		}
	}
	if !containsCodeRelated {
		t.Error("librarianKeywords should contain code-related terms")
	}
}

func TestAcademicKeywords(t *testing.T) {
	kw := academicKeywords()
	if len(kw) == 0 {
		t.Error("academicKeywords should not be empty")
	}
}

func TestArchivalistKeywords(t *testing.T) {
	kw := archivalistKeywords()
	if len(kw) == 0 {
		t.Error("archivalistKeywords should not be empty")
	}
}

func TestArchitectKeywords(t *testing.T) {
	kw := architectKeywords()
	if len(kw) == 0 {
		t.Error("architectKeywords should not be empty")
	}
}

func TestEngineerKeywords(t *testing.T) {
	kw := engineerKeywords()
	if len(kw) == 0 {
		t.Error("engineerKeywords should not be empty")
	}
}

func TestDesignerKeywords(t *testing.T) {
	kw := designerKeywords()
	if len(kw) == 0 {
		t.Error("designerKeywords should not be empty")
	}
}

func TestInspectorKeywords(t *testing.T) {
	kw := inspectorKeywords()
	if len(kw) == 0 {
		t.Error("inspectorKeywords should not be empty")
	}
}

func TestTesterKeywords(t *testing.T) {
	kw := testerKeywords()
	if len(kw) == 0 {
		t.Error("testerKeywords should not be empty")
	}
}

func TestOrchestratorKeywords(t *testing.T) {
	kw := orchestratorKeywords()
	if len(kw) == 0 {
		t.Error("orchestratorKeywords should not be empty")
	}
}

func TestGuideKeywords(t *testing.T) {
	kw := guideKeywords()
	if len(kw) == 0 {
		t.Error("guideKeywords should not be empty")
	}
}
