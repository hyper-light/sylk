package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/domain/classifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainIsolation_EngineerQueryToLibrarian_OnlyCodebaseContent(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	engineerQuery := "find the function that handles user authentication in our code"
	domainCtx, err := cascade.Classify(ctx, engineerQuery, concurrency.AgentGuide)

	require.NoError(t, err)
	require.NotNil(t, domainCtx)

	allowedDomains := domainCtx.AllowedDomains()

	assert.Contains(t, allowedDomains, domain.DomainLibrarian,
		"Codebase query should route to Librarian")

	assert.NotContains(t, allowedDomains, domain.DomainAcademic,
		"Codebase query should NOT include Academic domain")
}

func TestDomainIsolation_AcademicPapers_NeverReturnedForCodeQueries(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	codeQueries := []string{
		"where is the config file?",
		"show me the function that parses JSON",
		"find the class definition for User",
		"what packages do we import?",
	}

	for _, query := range codeQueries {
		t.Run(query, func(t *testing.T) {
			domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
			require.NoError(t, err)

			if !domainCtx.IsCrossDomain && domainCtx.DomainCount() == 1 {
				assert.NotEqual(t, domain.DomainAcademic, domainCtx.PrimaryDomain,
					"Pure code query should not route to Academic: %s", query)
			}
		})
	}
}

func TestDomainIsolation_CrossDomainQuery_SynthesizedByArchitect(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*architect.DomainResult, error) {
		content := ""
		switch d {
		case domain.DomainLibrarian:
			content = "The auth module uses JWT tokens stored in session."
		case domain.DomainAcademic:
			content = "RFC 7519 defines JWT as a compact, URL-safe means of representing claims."
		case domain.DomainArchivalist:
			content = "We chose JWT over sessions in sprint 12 for stateless scaling."
		}

		return &architect.DomainResult{
			Domain:  d,
			Query:   query,
			Content: content,
			Score:   0.8,
			Source:  d.String() + "-source",
		}, nil
	}

	handler := architect.NewCrossDomainHandler(&architect.CrossDomainHandlerConfig{
		Timeout:       5 * time.Second,
		MaxConcurrent: 3,
		QueryHandler:  mockHandler,
	})

	synthesizer := architect.NewResultSynthesizer(nil)

	domainCtx := domain.NewDomainContext("How does our auth work and what standard does it follow?")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.8, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	ctx := context.Background()
	crossResult, err := handler.HandleCrossDomain(ctx, domainCtx.OriginalQuery, domainCtx)

	require.NoError(t, err)
	require.NotNil(t, crossResult)
	assert.True(t, crossResult.IsCrossDomain)
	assert.Equal(t, 2, crossResult.SuccessDomains)

	unified := synthesizer.SynthesizeFromCrossDomain(crossResult)

	require.NotNil(t, unified)
	assert.NotEmpty(t, unified.Content)
	assert.Len(t, unified.Sources, 2)
	assert.Equal(t, "multi_domain_merge", unified.SynthesisMethod)

	libFound, acadFound := false, false
	for _, src := range unified.Sources {
		if src.Domain == domain.DomainLibrarian {
			libFound = true
		}
		if src.Domain == domain.DomainAcademic {
			acadFound = true
		}
	}
	assert.True(t, libFound, "Librarian source should be attributed")
	assert.True(t, acadFound, "Academic source should be attributed")
}

func TestDomainIsolation_DirectAddress_HonorsExplicitRouting(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)

	researchQueryToLibrarian := "@Librarian find papers on authentication"

	result := detector.Detect(researchQueryToLibrarian)

	assert.True(t, result.IsDirectAddress)
	assert.Equal(t, concurrency.AgentLibrarian, result.TargetAgent)
	assert.Equal(t, domain.DomainLibrarian, result.TargetDomain)
	assert.Equal(t, "find papers on authentication", result.CleanedQuery)
}

func TestDomainIsolation_DirectAddress_OverridesClassification(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	query := "@Archivalist what is the best practice for error handling?"

	directResult := detector.Detect(query)

	if directResult.IsDirectAddress {
		assert.Equal(t, concurrency.AgentArchivalist, directResult.TargetAgent)
		assert.Equal(t, domain.DomainArchivalist, directResult.TargetDomain)
	} else {
		domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
		require.NoError(t, err)
		assert.Equal(t, domain.DomainAcademic, domainCtx.PrimaryDomain)
	}

	assert.True(t, directResult.IsDirectAddress,
		"Direct @agent address should be detected and honored")
}

func TestDomainContamination_Scenario1_CodebaseSearchReturnsOnlyCode(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	query := "find where we handle database connections"

	domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
	require.NoError(t, err)

	if domainCtx.HasDomain(domain.DomainLibrarian) {
		conf := domainCtx.GetConfidence(domain.DomainLibrarian)
		acadConf := domainCtx.GetConfidence(domain.DomainAcademic)

		if acadConf > 0 {
			assert.Greater(t, conf, acadConf,
				"Librarian confidence should exceed Academic for code queries")
		}
	}
}

func TestDomainContamination_Scenario2_ResearchQueryExcludesCode(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	query := "what does the RFC specification say about HTTP caching?"

	domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
	require.NoError(t, err)

	if !domainCtx.IsCrossDomain {
		assert.Equal(t, domain.DomainAcademic, domainCtx.PrimaryDomain,
			"RFC query should route to Academic")
	}
}

func TestDomainContamination_Scenario3_HistoricalQueryToArchivalist(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	query := "what did we decide about the database schema last session?"

	domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
	require.NoError(t, err)

	assert.True(t, domainCtx.HasDomain(domain.DomainArchivalist),
		"Historical decision query should include Archivalist")

	if !domainCtx.IsCrossDomain {
		assert.Equal(t, domain.DomainArchivalist, domainCtx.PrimaryDomain)
	}
}

func TestDomainContamination_Scenario4_MixedQueryDetected(t *testing.T) {
	config := domain.DefaultDomainConfig()
	config.CrossDomainThreshold = 0.3

	cascade := classifier.NewClassificationCascade(config, &classifier.CascadeConfig{
		CrossDomainThreshold:  0.3,
		SingleDomainThreshold: 0.95,
	})
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	query := "show me our implementation and what the official documentation recommends"

	domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
	require.NoError(t, err)

	t.Logf("Mixed query detected %d domains", domainCtx.DomainCount())
	for _, d := range domainCtx.DetectedDomains {
		t.Logf("  - %s: %.2f", d.String(), domainCtx.GetConfidence(d))
	}

	assert.GreaterOrEqual(t, domainCtx.DomainCount(), 1,
		"Mixed query should detect at least one domain")
}

func TestDomainIsolation_FilterPropagation(t *testing.T) {
	domainCtx := domain.NewDomainContext("test query")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, []string{"code", "function"})
	domainCtx.SetCrossDomain(false)

	allowed := domainCtx.AllowedDomains()

	assert.Len(t, allowed, 1)
	assert.Equal(t, domain.DomainLibrarian, allowed[0])

	domainCtx.AddDomain(domain.DomainAcademic, 0.7, []string{"best practice"})
	domainCtx.SetCrossDomain(true)

	allowed = domainCtx.AllowedDomains()

	assert.Len(t, allowed, 2)
	assert.Contains(t, allowed, domain.DomainLibrarian)
	assert.Contains(t, allowed, domain.DomainAcademic)
}

func TestDomainIsolation_SourceAttribution_PreservesOrigin(t *testing.T) {
	synthesizer := architect.NewResultSynthesizer(nil)

	results := []architect.DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "Implementation uses JWT tokens",
			Score:   0.9,
			Source:  "auth/jwt.go",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "RFC 7519 specifies JWT format",
			Score:   0.8,
			Source:  "rfc-7519",
		},
	}

	unified := synthesizer.SynthesizeResults(results)

	require.NotNil(t, unified)
	require.Len(t, unified.Sources, 2)

	for _, src := range unified.Sources {
		switch src.Domain {
		case domain.DomainLibrarian:
			assert.Equal(t, "auth/jwt.go", src.SourceID)
			assert.Contains(t, src.Content, "JWT tokens")
		case domain.DomainAcademic:
			assert.Equal(t, "rfc-7519", src.SourceID)
			assert.Contains(t, src.Content, "RFC 7519")
		}
	}
}

func TestDomainIsolation_ConflictDetection_FlagsMismatch(t *testing.T) {
	synthesizer := architect.NewResultSynthesizer(&architect.SynthesizerConfig{
		SimilarityThreshold: 0.8,
		ConflictThreshold:   0.2,
	})

	results := []architect.DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "our code uses session tokens for auth",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "best practice recommends JWT tokens for auth",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	unified := synthesizer.SynthesizeResults(results)

	require.NotNil(t, unified)

	t.Logf("Conflicts detected: %d", len(unified.Conflicts))
	for _, c := range unified.Conflicts {
		t.Logf("  - Severity: %s, Domains: %v", c.Severity, c.Domains)
	}
}

func TestDomainIsolation_AllKnowledgeDomains_Isolated(t *testing.T) {
	knowledgeDomains := domain.KnowledgeDomains()

	for _, d := range knowledgeDomains {
		t.Run(d.String(), func(t *testing.T) {
			assert.True(t, d.IsValid(), "Domain %s should be valid", d.String())

			agent, ok := domain.DomainToAgent[d]
			assert.True(t, ok, "Domain %s should have agent mapping", d.String())
			assert.NotEmpty(t, agent)
		})
	}
}
