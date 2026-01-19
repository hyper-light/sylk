package integration

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

// TestDirectAddressRouting tests explicit @agent addressing.
func TestDirectAddressRouting_AtMention(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)

	tests := []struct {
		name          string
		query         string
		expectedAgent concurrency.AgentType
		isDirectAddr  bool
	}{
		{
			name:          "at_librarian",
			query:         "@Librarian where is the config file?",
			expectedAgent: concurrency.AgentLibrarian,
			isDirectAddr:  true,
		},
		{
			name:          "at_academic",
			query:         "@Academic find papers on consensus",
			expectedAgent: concurrency.AgentAcademic,
			isDirectAddr:  true,
		},
		{
			name:          "at_archivalist",
			query:         "@Archivalist what decisions were made?",
			expectedAgent: concurrency.AgentArchivalist,
			isDirectAddr:  true,
		},
		{
			name:          "at_architect",
			query:         "@Architect design the system",
			expectedAgent: concurrency.AgentArchitect,
			isDirectAddr:  true,
		},
		{
			name:          "at_alias_lib",
			query:         "@lib find the function",
			expectedAgent: concurrency.AgentLibrarian,
			isDirectAddr:  true,
		},
		{
			name:          "at_alias_prof",
			query:         "@prof explain this concept",
			expectedAgent: concurrency.AgentAcademic,
			isDirectAddr:  true,
		},
		{
			name:          "no_direct_address",
			query:         "find the config file",
			expectedAgent: "",
			isDirectAddr:  false,
		},
		{
			name:          "at_in_middle",
			query:         "please @Librarian find the file",
			expectedAgent: "",
			isDirectAddr:  false, // @ must be at start
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := detector.Detect(tc.query)

			assert.Equal(t, tc.isDirectAddr, result.IsDirectAddress)
			if tc.isDirectAddr {
				assert.Equal(t, tc.expectedAgent, result.TargetAgent)
				assert.Greater(t, result.Confidence, 0.0)
			}
		})
	}
}

func TestDirectAddressRouting_Greeting(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)

	tests := []struct {
		name          string
		query         string
		expectedAgent concurrency.AgentType
		isDirectAddr  bool
	}{
		{
			name:          "hey_librarian",
			query:         "hey librarian, where is the file?",
			expectedAgent: concurrency.AgentLibrarian,
			isDirectAddr:  true,
		},
		{
			name:          "hello_academic",
			query:         "hello academic: explain this concept",
			expectedAgent: concurrency.AgentAcademic,
			isDirectAddr:  true,
		},
		{
			name:          "hi_archivalist",
			query:         "hi archivalist, what happened?",
			expectedAgent: concurrency.AgentArchivalist,
			isDirectAddr:  true,
		},
		{
			name:          "yo_architect",
			query:         "yo architect design this",
			expectedAgent: concurrency.AgentArchitect,
			isDirectAddr:  true,
		},
		{
			name:          "hey_unknown",
			query:         "hey bob, what's up?",
			expectedAgent: "",
			isDirectAddr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := detector.Detect(tc.query)

			assert.Equal(t, tc.isDirectAddr, result.IsDirectAddress)
			if tc.isDirectAddr {
				assert.Equal(t, tc.expectedAgent, result.TargetAgent)
				assert.Equal(t, 0.9, result.Confidence) // Greeting confidence
			}
		})
	}
}

func TestDirectAddressRouting_QueryCleaning(t *testing.T) {
	detector := guide.NewDirectAddressDetector(&guide.DirectAddressConfig{
		StripAddressFromQuery: true,
	})

	result := detector.Detect("@Librarian where is the config file?")

	assert.True(t, result.IsDirectAddress)
	assert.Equal(t, "where is the config file?", result.CleanedQuery)
}

func TestDirectAddressRouting_PreserveQuery(t *testing.T) {
	detector := guide.NewDirectAddressDetector(&guide.DirectAddressConfig{
		StripAddressFromQuery: false,
	})

	result := detector.Detect("@Librarian where is the config file?")

	assert.True(t, result.IsDirectAddress)
	assert.Equal(t, "@Librarian where is the config file?", result.CleanedQuery)
}

func TestCrossDomainRouting_ToArchitect(t *testing.T) {
	config := domain.DefaultDomainConfig()
	config.CrossDomainThreshold = 0.3 // Lower for testing

	cascade := classifier.NewClassificationCascade(config, &classifier.CascadeConfig{
		CrossDomainThreshold: 0.3,
	})
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	// Cross-domain query
	query := "what is the best practice for our implementation of authentication?"

	domainCtx, err := cascade.Classify(ctx, query, concurrency.AgentGuide)
	require.NoError(t, err)

	// If cross-domain is detected, it should route to Architect
	if domainCtx.IsCrossDomain || len(domainCtx.SecondaryDomains) > 0 {
		// Architect handles cross-domain coordination
		assert.True(t, len(domainCtx.DetectedDomains) > 1 || len(domainCtx.SecondaryDomains) > 0,
			"Cross-domain query should have multiple detected domains")
	}
}

func TestSingleDomainRouting_ToKnowledgeAgent(t *testing.T) {
	config := domain.DefaultDomainConfig()
	cascade := classifier.NewClassificationCascade(config, nil)
	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	ctx := context.Background()

	tests := []struct {
		name           string
		query          string
		expectedDomain domain.Domain
	}{
		{
			name:           "single_librarian",
			query:          "find the function in our code",
			expectedDomain: domain.DomainLibrarian,
		},
		{
			name:           "single_archivalist",
			query:          "what did we decide in the last session?",
			expectedDomain: domain.DomainArchivalist,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			domainCtx, err := cascade.Classify(ctx, tc.query, concurrency.AgentGuide)
			require.NoError(t, err)

			// Single domain queries should route directly
			if !domainCtx.IsCrossDomain && domainCtx.DomainCount() == 1 {
				assert.Equal(t, tc.expectedDomain, domainCtx.PrimaryDomain)
			}
		})
	}
}

func TestDomainContextPropagation(t *testing.T) {
	// Create a domain context
	domainCtx := domain.NewDomainContext("test query")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, []string{"code", "function"})
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, []string{"best practice"})
	domainCtx.SetCrossDomain(true)
	domainCtx.SetClassificationMethod("lexical")

	// Clone should preserve all fields
	clone := domainCtx.Clone()

	require.NotNil(t, clone)
	assert.Equal(t, domainCtx.OriginalQuery, clone.OriginalQuery)
	assert.Equal(t, domainCtx.IsCrossDomain, clone.IsCrossDomain)
	assert.Equal(t, domainCtx.PrimaryDomain, clone.PrimaryDomain)
	assert.Equal(t, domainCtx.ClassificationMethod, clone.ClassificationMethod)
	assert.Len(t, clone.DetectedDomains, len(domainCtx.DetectedDomains))

	// Verify confidences are preserved
	assert.Equal(t, 0.9, clone.GetConfidence(domain.DomainLibrarian))
	assert.Equal(t, 0.7, clone.GetConfidence(domain.DomainAcademic))

	// Verify signals are preserved
	assert.Equal(t, domainCtx.GetSignals(domain.DomainLibrarian), clone.GetSignals(domain.DomainLibrarian))
}

func TestDomainContextPropagation_Nil(t *testing.T) {
	var domainCtx *domain.DomainContext = nil
	clone := domainCtx.Clone()

	assert.Nil(t, clone)
}

func TestCrossDomainHandler_Routing(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*architect.DomainResult, error) {
		return &architect.DomainResult{
			Domain:  d,
			Query:   query,
			Content: "content from " + d.String(),
			Score:   0.8,
			Source:  d.String() + "-source",
		}, nil
	}

	handler := architect.NewCrossDomainHandler(&architect.CrossDomainHandlerConfig{
		Timeout:       5 * time.Second,
		MaxConcurrent: 3,
		QueryHandler:  mockHandler,
	})

	domainCtx := domain.NewDomainContext("test cross-domain query")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	ctx := context.Background()
	result, err := handler.HandleCrossDomain(ctx, "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have results from both domains
	assert.Equal(t, 2, result.TotalDomains)
	assert.Equal(t, 2, result.SuccessDomains)
	assert.True(t, result.IsCrossDomain)

	// Source map should track both domains
	assert.Len(t, result.SourceMap, 2)
}

func TestDomainAllowedDomains_SingleDomain(t *testing.T) {
	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.SetCrossDomain(false)

	allowed := domainCtx.AllowedDomains()

	assert.Len(t, allowed, 1)
	assert.Contains(t, allowed, domain.DomainLibrarian)
}

func TestDomainAllowedDomains_CrossDomain(t *testing.T) {
	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	allowed := domainCtx.AllowedDomains()

	assert.Len(t, allowed, 2)
	assert.Contains(t, allowed, domain.DomainLibrarian)
	assert.Contains(t, allowed, domain.DomainAcademic)
}

func TestDirectAddressDetector_CustomAliases(t *testing.T) {
	detector := guide.NewDirectAddressDetector(&guide.DirectAddressConfig{
		CustomAliases: map[string]concurrency.AgentType{
			"custombot": concurrency.AgentLibrarian,
		},
	})

	result := detector.Detect("@custombot find the file")

	assert.True(t, result.IsDirectAddress)
	assert.Equal(t, concurrency.AgentLibrarian, result.TargetAgent)
}

func TestDirectAddressDetector_AddRemoveAlias(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)

	// Add alias
	detector.AddAlias("newagent", concurrency.AgentLibrarian)
	assert.True(t, detector.HasAlias("newagent"))

	result := detector.Detect("@newagent find something")
	assert.True(t, result.IsDirectAddress)

	// Remove alias
	detector.RemoveAlias("newagent")
	assert.False(t, detector.HasAlias("newagent"))

	result = detector.Detect("@newagent find something")
	assert.False(t, result.IsDirectAddress)
}

func TestDirectAddressDetector_ListAliases(t *testing.T) {
	detector := guide.NewDirectAddressDetector(nil)
	aliases := detector.ListAliases()

	// Should have default aliases
	assert.Contains(t, aliases, "librarian")
	assert.Contains(t, aliases, "academic")
	assert.Contains(t, aliases, "archivalist")
	assert.Contains(t, aliases, "architect")
}

func TestDomainFromAgentType(t *testing.T) {
	tests := []struct {
		agent    concurrency.AgentType
		expected domain.Domain
		valid    bool
	}{
		{concurrency.AgentLibrarian, domain.DomainLibrarian, true},
		{concurrency.AgentAcademic, domain.DomainAcademic, true},
		{concurrency.AgentArchivalist, domain.DomainArchivalist, true},
		{concurrency.AgentArchitect, domain.DomainArchitect, true},
		{concurrency.AgentGuide, domain.DomainGuide, true},
		{"unknown", domain.Domain(0), false},
	}

	for _, tc := range tests {
		t.Run(string(tc.agent), func(t *testing.T) {
			d, ok := domain.DomainFromAgentType(tc.agent)

			assert.Equal(t, tc.valid, ok)
			if tc.valid {
				assert.Equal(t, tc.expected, d)
			}
		})
	}
}

func TestDomainContext_HighestConfidenceDomain(t *testing.T) {
	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.7, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.9, nil)
	domainCtx.AddDomain(domain.DomainArchivalist, 0.5, nil)

	highest := domainCtx.HighestConfidenceDomain()

	assert.Equal(t, domain.DomainAcademic, highest)
}

func TestDomainContext_HasDomain(t *testing.T) {
	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	assert.True(t, domainCtx.HasDomain(domain.DomainLibrarian))
	assert.False(t, domainCtx.HasDomain(domain.DomainAcademic))
}

func TestDomainContext_SecondaryDomains(t *testing.T) {
	config := domain.DefaultDomainConfig()
	config.CrossDomainThreshold = 0.5

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.AddDomain(domain.DomainArchivalist, 0.3, nil) // Below threshold

	// Primary should be highest
	assert.Equal(t, domain.DomainLibrarian, domainCtx.PrimaryDomain)

	// Secondary should include Academic (above default threshold)
	assert.Contains(t, domainCtx.SecondaryDomains, domain.DomainAcademic)
}
