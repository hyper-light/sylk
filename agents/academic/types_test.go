package academic

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestDefaultMemoryThreshold verifies the default memory threshold values.
func TestDefaultMemoryThreshold(t *testing.T) {
	t.Run("returns correct default values", func(t *testing.T) {
		threshold := DefaultMemoryThreshold()

		if threshold.CheckpointThreshold != 0.85 {
			t.Errorf("CheckpointThreshold = %v, want 0.85", threshold.CheckpointThreshold)
		}
		if threshold.CompactThreshold != 0.95 {
			t.Errorf("CompactThreshold = %v, want 0.95", threshold.CompactThreshold)
		}
	})

	t.Run("checkpoint threshold is less than compact threshold", func(t *testing.T) {
		threshold := DefaultMemoryThreshold()

		if threshold.CheckpointThreshold >= threshold.CompactThreshold {
			t.Errorf("CheckpointThreshold (%v) should be less than CompactThreshold (%v)",
				threshold.CheckpointThreshold, threshold.CompactThreshold)
		}
	})
}

// TestValidSourceTypes verifies all valid source types are returned.
func TestValidSourceTypes(t *testing.T) {
	tests := []struct {
		name     string
		expected []SourceType
	}{
		{
			name: "returns all five source types",
			expected: []SourceType{
				SourceTypeGitHub,
				SourceTypeArticle,
				SourceTypePaper,
				SourceTypeRFC,
				SourceTypeDocumentation,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidSourceTypes()

			if len(got) != len(tt.expected) {
				t.Errorf("ValidSourceTypes() returned %d types, want %d", len(got), len(tt.expected))
			}

			for i, sourceType := range tt.expected {
				if got[i] != sourceType {
					t.Errorf("ValidSourceTypes()[%d] = %v, want %v", i, got[i], sourceType)
				}
			}
		})
	}

	t.Run("source type string values are correct", func(t *testing.T) {
		expectedStrings := map[SourceType]string{
			SourceTypeGitHub:        "github",
			SourceTypeArticle:       "article",
			SourceTypePaper:         "paper",
			SourceTypeRFC:           "rfc",
			SourceTypeDocumentation: "documentation",
		}

		for sourceType, expectedString := range expectedStrings {
			if string(sourceType) != expectedString {
				t.Errorf("SourceType %v = %q, want %q", sourceType, string(sourceType), expectedString)
			}
		}
	})
}

// TestValidConfidenceLevels verifies all valid confidence levels are returned.
func TestValidConfidenceLevels(t *testing.T) {
	tests := []struct {
		name     string
		expected []ConfidenceLevel
	}{
		{
			name: "returns all three confidence levels",
			expected: []ConfidenceLevel{
				ConfidenceLevelHigh,
				ConfidenceLevelMedium,
				ConfidenceLevelLow,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidConfidenceLevels()

			if len(got) != len(tt.expected) {
				t.Errorf("ValidConfidenceLevels() returned %d levels, want %d", len(got), len(tt.expected))
			}

			for i, level := range tt.expected {
				if got[i] != level {
					t.Errorf("ValidConfidenceLevels()[%d] = %v, want %v", i, got[i], level)
				}
			}
		})
	}

	t.Run("confidence level string values are correct", func(t *testing.T) {
		expectedStrings := map[ConfidenceLevel]string{
			ConfidenceLevelHigh:   "high",
			ConfidenceLevelMedium: "medium",
			ConfidenceLevelLow:    "low",
		}

		for level, expectedString := range expectedStrings {
			if string(level) != expectedString {
				t.Errorf("ConfidenceLevel %v = %q, want %q", level, string(level), expectedString)
			}
		}
	})
}

// TestValidResearchIntents verifies all valid research intents are returned.
func TestValidResearchIntents(t *testing.T) {
	tests := []struct {
		name     string
		expected []ResearchIntent
	}{
		{
			name: "returns all two research intents",
			expected: []ResearchIntent{
				IntentRecall,
				IntentCheck,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidResearchIntents()

			if len(got) != len(tt.expected) {
				t.Errorf("ValidResearchIntents() returned %d intents, want %d", len(got), len(tt.expected))
			}

			for i, intent := range tt.expected {
				if got[i] != intent {
					t.Errorf("ValidResearchIntents()[%d] = %v, want %v", i, got[i], intent)
				}
			}
		})
	}

	t.Run("research intent string values are correct", func(t *testing.T) {
		expectedStrings := map[ResearchIntent]string{
			IntentRecall: "recall",
			IntentCheck:  "check",
		}

		for intent, expectedString := range expectedStrings {
			if string(intent) != expectedString {
				t.Errorf("ResearchIntent %v = %q, want %q", intent, string(intent), expectedString)
			}
		}
	})
}

// TestValidResearchDomains verifies all valid research domains are returned.
func TestValidResearchDomains(t *testing.T) {
	tests := []struct {
		name     string
		expected []ResearchDomain
	}{
		{
			name: "returns all three research domains",
			expected: []ResearchDomain{
				DomainPatterns,
				DomainDecisions,
				DomainLearnings,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidResearchDomains()

			if len(got) != len(tt.expected) {
				t.Errorf("ValidResearchDomains() returned %d domains, want %d", len(got), len(tt.expected))
			}

			for i, domain := range tt.expected {
				if got[i] != domain {
					t.Errorf("ValidResearchDomains()[%d] = %v, want %v", i, got[i], domain)
				}
			}
		})
	}

	t.Run("research domain string values are correct", func(t *testing.T) {
		expectedStrings := map[ResearchDomain]string{
			DomainPatterns:  "patterns",
			DomainDecisions: "decisions",
			DomainLearnings: "learnings",
		}

		for domain, expectedString := range expectedStrings {
			if string(domain) != expectedString {
				t.Errorf("ResearchDomain %v = %q, want %q", domain, string(domain), expectedString)
			}
		}
	})
}

// TestIngestResultMarshalJSON tests the custom JSON marshaling for IngestResult.
func TestIngestResultMarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		result  IngestResult
		checkFn func(t *testing.T, data map[string]any)
	}{
		{
			name: "successful ingestion with all fields",
			result: IngestResult{
				Success:    true,
				SourceID:   "src-123",
				TokenCount: 1500,
				Error:      "",
				Duration:   5 * time.Second,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["success"] != true {
					t.Errorf("success = %v, want true", data["success"])
				}
				if data["source_id"] != "src-123" {
					t.Errorf("source_id = %v, want src-123", data["source_id"])
				}
				if data["token_count"] != float64(1500) {
					t.Errorf("token_count = %v, want 1500", data["token_count"])
				}
				if data["duration"] != "5s" {
					t.Errorf("duration = %v, want 5s", data["duration"])
				}
			},
		},
		{
			name: "failed ingestion with error",
			result: IngestResult{
				Success:    false,
				SourceID:   "",
				TokenCount: 0,
				Error:      "connection timeout",
				Duration:   30 * time.Second,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["success"] != false {
					t.Errorf("success = %v, want false", data["success"])
				}
				if data["error"] != "connection timeout" {
					t.Errorf("error = %v, want connection timeout", data["error"])
				}
				if data["duration"] != "30s" {
					t.Errorf("duration = %v, want 30s", data["duration"])
				}
			},
		},
		{
			name: "zero duration",
			result: IngestResult{
				Success:  true,
				SourceID: "src-456",
				Duration: 0,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["duration"] != "0s" {
					t.Errorf("duration = %v, want 0s", data["duration"])
				}
			},
		},
		{
			name: "millisecond duration",
			result: IngestResult{
				Success:  true,
				SourceID: "src-789",
				Duration: 150 * time.Millisecond,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["duration"] != "150ms" {
					t.Errorf("duration = %v, want 150ms", data["duration"])
				}
			},
		},
		{
			name: "nanosecond duration",
			result: IngestResult{
				Success:  true,
				SourceID: "src-nano",
				Duration: 500 * time.Nanosecond,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["duration"] != "500ns" {
					t.Errorf("duration = %v, want 500ns", data["duration"])
				}
			},
		},
		{
			name: "complex duration",
			result: IngestResult{
				Success:  true,
				SourceID: "src-complex",
				Duration: 2*time.Hour + 30*time.Minute + 45*time.Second,
			},
			checkFn: func(t *testing.T, data map[string]any) {
				if data["duration"] != "2h30m45s" {
					t.Errorf("duration = %v, want 2h30m45s", data["duration"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.result.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}

			var result map[string]any
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			tt.checkFn(t, result)
		})
	}
}

// TestSourceJSONMarshaling tests JSON marshaling/unmarshaling for Source.
func TestSourceJSONMarshaling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name   string
		source Source
	}{
		{
			name: "full source with all fields",
			source: Source{
				ID:          "src-001",
				Type:        SourceTypeGitHub,
				URL:         "https://github.com/example/repo",
				Title:       "Example Repository",
				Description: "An example repository for testing",
				IngestedAt:  now,
				UpdatedAt:   now.Add(time.Hour),
				TokenCount:  5000,
				Quality:     0.95,
				Metadata: map[string]any{
					"stars":    100,
					"language": "Go",
				},
			},
		},
		{
			name: "source with minimal fields",
			source: Source{
				ID:         "src-002",
				Type:       SourceTypeArticle,
				URL:        "https://example.com/article",
				Title:      "Test Article",
				IngestedAt: now,
				UpdatedAt:  now,
				TokenCount: 1000,
				Quality:    0.5,
			},
		},
		{
			name: "source with zero values",
			source: Source{
				ID:         "src-003",
				Type:       SourceTypePaper,
				URL:        "https://arxiv.org/paper",
				Title:      "Research Paper",
				IngestedAt: now,
				UpdatedAt:  now,
				TokenCount: 0,
				Quality:    0.0,
			},
		},
		{
			name: "source with empty metadata",
			source: Source{
				ID:         "src-004",
				Type:       SourceTypeRFC,
				URL:        "https://tools.ietf.org/rfc/123",
				Title:      "RFC 123",
				IngestedAt: now,
				UpdatedAt:  now,
				TokenCount: 2000,
				Quality:    1.0,
				Metadata:   map[string]any{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.source)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			// Unmarshal back
			var got Source
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			// Compare fields
			if got.ID != tt.source.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.source.ID)
			}
			if got.Type != tt.source.Type {
				t.Errorf("Type = %v, want %v", got.Type, tt.source.Type)
			}
			if got.URL != tt.source.URL {
				t.Errorf("URL = %v, want %v", got.URL, tt.source.URL)
			}
			if got.Title != tt.source.Title {
				t.Errorf("Title = %v, want %v", got.Title, tt.source.Title)
			}
			if got.Description != tt.source.Description {
				t.Errorf("Description = %v, want %v", got.Description, tt.source.Description)
			}
			if got.TokenCount != tt.source.TokenCount {
				t.Errorf("TokenCount = %v, want %v", got.TokenCount, tt.source.TokenCount)
			}
			if got.Quality != tt.source.Quality {
				t.Errorf("Quality = %v, want %v", got.Quality, tt.source.Quality)
			}
		})
	}
}

// TestGitHubRepoMetadataJSONMarshaling tests JSON marshaling for GitHubRepoMetadata.
func TestGitHubRepoMetadataJSONMarshaling(t *testing.T) {
	tests := []struct {
		name     string
		metadata GitHubRepoMetadata
	}{
		{
			name: "full metadata",
			metadata: GitHubRepoMetadata{
				Owner:     "example-org",
				Repo:      "example-repo",
				Branch:    "main",
				CommitSHA: "abc123def456",
				Stars:     1500,
				Language:  "Go",
				Topics:    []string{"cli", "tools", "golang"},
			},
		},
		{
			name: "minimal metadata",
			metadata: GitHubRepoMetadata{
				Owner:     "user",
				Repo:      "project",
				Branch:    "master",
				CommitSHA: "xyz789",
				Stars:     0,
				Language:  "",
			},
		},
		{
			name: "empty topics",
			metadata: GitHubRepoMetadata{
				Owner:     "org",
				Repo:      "repo",
				Branch:    "develop",
				CommitSHA: "commit123",
				Stars:     50,
				Language:  "Rust",
				Topics:    []string{},
			},
		},
		{
			name: "nil topics",
			metadata: GitHubRepoMetadata{
				Owner:     "org",
				Repo:      "repo",
				Branch:    "feature",
				CommitSHA: "feat123",
				Stars:     25,
				Language:  "Python",
				Topics:    nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.metadata)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got GitHubRepoMetadata
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.Owner != tt.metadata.Owner {
				t.Errorf("Owner = %v, want %v", got.Owner, tt.metadata.Owner)
			}
			if got.Repo != tt.metadata.Repo {
				t.Errorf("Repo = %v, want %v", got.Repo, tt.metadata.Repo)
			}
			if got.Branch != tt.metadata.Branch {
				t.Errorf("Branch = %v, want %v", got.Branch, tt.metadata.Branch)
			}
			if got.CommitSHA != tt.metadata.CommitSHA {
				t.Errorf("CommitSHA = %v, want %v", got.CommitSHA, tt.metadata.CommitSHA)
			}
			if got.Stars != tt.metadata.Stars {
				t.Errorf("Stars = %v, want %v", got.Stars, tt.metadata.Stars)
			}
			if got.Language != tt.metadata.Language {
				t.Errorf("Language = %v, want %v", got.Language, tt.metadata.Language)
			}
		})
	}
}

// TestArticleMetadataJSONMarshaling tests JSON marshaling for ArticleMetadata.
func TestArticleMetadataJSONMarshaling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name     string
		metadata ArticleMetadata
	}{
		{
			name: "full metadata",
			metadata: ArticleMetadata{
				Author:      "John Doe",
				PublishedAt: &now,
				Site:        "example.com",
				Tags:        []string{"go", "testing", "tutorial"},
			},
		},
		{
			name: "minimal metadata",
			metadata: ArticleMetadata{
				Author: "Jane Smith",
			},
		},
		{
			name: "nil published at",
			metadata: ArticleMetadata{
				Author:      "Anonymous",
				PublishedAt: nil,
				Site:        "blog.example.com",
				Tags:        []string{"programming"},
			},
		},
		{
			name: "empty tags",
			metadata: ArticleMetadata{
				Author:      "Test Author",
				PublishedAt: &now,
				Site:        "news.example.com",
				Tags:        []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.metadata)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got ArticleMetadata
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.Author != tt.metadata.Author {
				t.Errorf("Author = %v, want %v", got.Author, tt.metadata.Author)
			}
			if got.Site != tt.metadata.Site {
				t.Errorf("Site = %v, want %v", got.Site, tt.metadata.Site)
			}
		})
	}
}

// TestFindingJSONMarshaling tests JSON marshaling for Finding.
func TestFindingJSONMarshaling(t *testing.T) {
	tests := []struct {
		name    string
		finding Finding
	}{
		{
			name: "full finding",
			finding: Finding{
				ID:         "find-001",
				Topic:      "Error Handling",
				Summary:    "Best practices for error handling in Go",
				Details:    "Detailed explanation of error handling patterns...",
				Confidence: ConfidenceLevelHigh,
				SourceIDs:  []string{"src-001", "src-002"},
				Citations:  []string{"[1] Source One", "[2] Source Two"},
			},
		},
		{
			name: "minimal finding",
			finding: Finding{
				ID:         "find-002",
				Topic:      "Testing",
				Summary:    "Basic testing patterns",
				Confidence: ConfidenceLevelMedium,
				SourceIDs:  []string{"src-003"},
			},
		},
		{
			name: "finding with low confidence",
			finding: Finding{
				ID:         "find-003",
				Topic:      "Experimental Feature",
				Summary:    "Experimental approach to X",
				Confidence: ConfidenceLevelLow,
				SourceIDs:  []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.finding)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got Finding
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.ID != tt.finding.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.finding.ID)
			}
			if got.Topic != tt.finding.Topic {
				t.Errorf("Topic = %v, want %v", got.Topic, tt.finding.Topic)
			}
			if got.Summary != tt.finding.Summary {
				t.Errorf("Summary = %v, want %v", got.Summary, tt.finding.Summary)
			}
			if got.Details != tt.finding.Details {
				t.Errorf("Details = %v, want %v", got.Details, tt.finding.Details)
			}
			if got.Confidence != tt.finding.Confidence {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tt.finding.Confidence)
			}
		})
	}
}

// TestRecommendationJSONMarshaling tests JSON marshaling for Recommendation.
func TestRecommendationJSONMarshaling(t *testing.T) {
	tests := []struct {
		name           string
		recommendation Recommendation
	}{
		{
			name: "full recommendation",
			recommendation: Recommendation{
				ID:            "rec-001",
				Title:         "Use Structured Logging",
				Description:   "Implement structured logging for better observability",
				Rationale:     "Structured logs are easier to query and analyze",
				Applicability: "All production services",
				Confidence:    ConfidenceLevelHigh,
				Alternatives:  []string{"printf-style logging", "custom logging"},
				SourceIDs:     []string{"src-001", "src-002"},
			},
		},
		{
			name: "minimal recommendation",
			recommendation: Recommendation{
				ID:          "rec-002",
				Title:       "Basic Recommendation",
				Description: "A basic recommendation",
				Rationale:   "Simple rationale",
				Confidence:  ConfidenceLevelLow,
				SourceIDs:   []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.recommendation)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got Recommendation
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.ID != tt.recommendation.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.recommendation.ID)
			}
			if got.Title != tt.recommendation.Title {
				t.Errorf("Title = %v, want %v", got.Title, tt.recommendation.Title)
			}
			if got.Description != tt.recommendation.Description {
				t.Errorf("Description = %v, want %v", got.Description, tt.recommendation.Description)
			}
			if got.Rationale != tt.recommendation.Rationale {
				t.Errorf("Rationale = %v, want %v", got.Rationale, tt.recommendation.Rationale)
			}
			if got.Confidence != tt.recommendation.Confidence {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tt.recommendation.Confidence)
			}
		})
	}
}

// TestResearchQueryJSONMarshaling tests JSON marshaling for ResearchQuery.
func TestResearchQueryJSONMarshaling(t *testing.T) {
	tests := []struct {
		name  string
		query ResearchQuery
	}{
		{
			name: "full query",
			query: ResearchQuery{
				Query:          "What are best practices for error handling?",
				Intent:         IntentRecall,
				Domain:         DomainPatterns,
				MaxSources:     10,
				LanguageFilter: "Go",
				SessionID:      "session-001",
			},
		},
		{
			name: "minimal query",
			query: ResearchQuery{
				Query: "How to implement X?",
			},
		},
		{
			name: "query with check intent",
			query: ResearchQuery{
				Query:      "Is singleton pattern recommended?",
				Intent:     IntentCheck,
				Domain:     DomainDecisions,
				MaxSources: 5,
			},
		},
		{
			name: "query with learnings domain",
			query: ResearchQuery{
				Query:  "What are common mistakes in concurrent programming?",
				Intent: IntentRecall,
				Domain: DomainLearnings,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.query)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got ResearchQuery
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.Query != tt.query.Query {
				t.Errorf("Query = %v, want %v", got.Query, tt.query.Query)
			}
			if got.Intent != tt.query.Intent {
				t.Errorf("Intent = %v, want %v", got.Intent, tt.query.Intent)
			}
			if got.Domain != tt.query.Domain {
				t.Errorf("Domain = %v, want %v", got.Domain, tt.query.Domain)
			}
			if got.MaxSources != tt.query.MaxSources {
				t.Errorf("MaxSources = %v, want %v", got.MaxSources, tt.query.MaxSources)
			}
			if got.LanguageFilter != tt.query.LanguageFilter {
				t.Errorf("LanguageFilter = %v, want %v", got.LanguageFilter, tt.query.LanguageFilter)
			}
			if got.SessionID != tt.query.SessionID {
				t.Errorf("SessionID = %v, want %v", got.SessionID, tt.query.SessionID)
			}
		})
	}
}

// TestResearchResultJSONMarshaling tests JSON marshaling for ResearchResult.
func TestResearchResultJSONMarshaling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name   string
		result ResearchResult
	}{
		{
			name: "full result",
			result: ResearchResult{
				QueryID: "query-001",
				Findings: []Finding{
					{
						ID:         "find-001",
						Topic:      "Testing",
						Summary:    "Test finding",
						Confidence: ConfidenceLevelHigh,
						SourceIDs:  []string{"src-001"},
					},
				},
				Recommendations: []Recommendation{
					{
						ID:          "rec-001",
						Title:       "Test Recommendation",
						Description: "A test recommendation",
						Rationale:   "Test rationale",
						Confidence:  ConfidenceLevelMedium,
						SourceIDs:   []string{"src-001"},
					},
				},
				SourcesConsulted: []string{"src-001", "src-002"},
				Confidence:       ConfidenceLevelHigh,
				CachedAt:         &now,
				GeneratedAt:      now,
			},
		},
		{
			name: "minimal result",
			result: ResearchResult{
				QueryID:          "query-002",
				Findings:         []Finding{},
				SourcesConsulted: []string{},
				Confidence:       ConfidenceLevelLow,
				GeneratedAt:      now,
			},
		},
		{
			name: "result without cache",
			result: ResearchResult{
				QueryID: "query-003",
				Findings: []Finding{
					{
						ID:         "find-002",
						Topic:      "Architecture",
						Summary:    "Architecture finding",
						Confidence: ConfidenceLevelMedium,
						SourceIDs:  []string{"src-003"},
					},
				},
				SourcesConsulted: []string{"src-003"},
				Confidence:       ConfidenceLevelMedium,
				CachedAt:         nil,
				GeneratedAt:      now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got ResearchResult
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.QueryID != tt.result.QueryID {
				t.Errorf("QueryID = %v, want %v", got.QueryID, tt.result.QueryID)
			}
			if got.Confidence != tt.result.Confidence {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tt.result.Confidence)
			}
			if len(got.Findings) != len(tt.result.Findings) {
				t.Errorf("len(Findings) = %v, want %v", len(got.Findings), len(tt.result.Findings))
			}
			if len(got.SourcesConsulted) != len(tt.result.SourcesConsulted) {
				t.Errorf("len(SourcesConsulted) = %v, want %v", len(got.SourcesConsulted), len(tt.result.SourcesConsulted))
			}
		})
	}
}

// TestAcademicResearchPaperJSONMarshaling tests JSON marshaling for AcademicResearchPaper.
func TestAcademicResearchPaperJSONMarshaling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name  string
		paper AcademicResearchPaper
	}{
		{
			name: "full paper",
			paper: AcademicResearchPaper{
				ID:               "paper-001",
				Timestamp:        now,
				SessionID:        "session-001",
				ContextUsage:     0.87,
				Title:            "Research on Error Handling Patterns",
				Abstract:         "This paper explores error handling patterns in Go.",
				TopicsResearched: []string{"error handling", "patterns", "Go"},
				KeyFindings: []Finding{
					{
						ID:         "find-001",
						Topic:      "Error Wrapping",
						Summary:    "Error wrapping is recommended",
						Confidence: ConfidenceLevelHigh,
						SourceIDs:  []string{"src-001"},
					},
				},
				SourcesCited: []Source{
					{
						ID:         "src-001",
						Type:       SourceTypeArticle,
						URL:        "https://example.com/article",
						Title:      "Error Handling in Go",
						IngestedAt: now,
						UpdatedAt:  now,
						TokenCount: 1000,
						Quality:    0.9,
					},
				},
				Recommendations: []string{"Use error wrapping", "Implement custom error types"},
				OpenQuestions:   []string{"How to handle errors in async code?"},
				RelatedTopics:   []string{"concurrency", "testing"},
			},
		},
		{
			name: "minimal paper",
			paper: AcademicResearchPaper{
				ID:               "paper-002",
				Timestamp:        now,
				SessionID:        "session-002",
				ContextUsage:     0.85,
				Title:            "Minimal Paper",
				Abstract:         "A minimal paper",
				TopicsResearched: []string{},
				KeyFindings:      []Finding{},
				SourcesCited:     []Source{},
				Recommendations:  []string{},
				OpenQuestions:    []string{},
				RelatedTopics:    []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.paper)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got AcademicResearchPaper
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.ID != tt.paper.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.paper.ID)
			}
			if got.SessionID != tt.paper.SessionID {
				t.Errorf("SessionID = %v, want %v", got.SessionID, tt.paper.SessionID)
			}
			if got.ContextUsage != tt.paper.ContextUsage {
				t.Errorf("ContextUsage = %v, want %v", got.ContextUsage, tt.paper.ContextUsage)
			}
			if got.Title != tt.paper.Title {
				t.Errorf("Title = %v, want %v", got.Title, tt.paper.Title)
			}
			if got.Abstract != tt.paper.Abstract {
				t.Errorf("Abstract = %v, want %v", got.Abstract, tt.paper.Abstract)
			}
		})
	}
}

// TestIngestRequestJSONMarshaling tests JSON marshaling for IngestRequest.
func TestIngestRequestJSONMarshaling(t *testing.T) {
	tests := []struct {
		name    string
		request IngestRequest
	}{
		{
			name: "full request",
			request: IngestRequest{
				URL:       "https://github.com/example/repo",
				Type:      SourceTypeGitHub,
				Deep:      true,
				SessionID: "session-001",
			},
		},
		{
			name: "minimal request",
			request: IngestRequest{
				URL:  "https://example.com/article",
				Type: SourceTypeArticle,
			},
		},
		{
			name: "request with all source types",
			request: IngestRequest{
				URL:  "https://tools.ietf.org/rfc/7231",
				Type: SourceTypeRFC,
				Deep: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got IngestRequest
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.URL != tt.request.URL {
				t.Errorf("URL = %v, want %v", got.URL, tt.request.URL)
			}
			if got.Type != tt.request.Type {
				t.Errorf("Type = %v, want %v", got.Type, tt.request.Type)
			}
			if got.Deep != tt.request.Deep {
				t.Errorf("Deep = %v, want %v", got.Deep, tt.request.Deep)
			}
			if got.SessionID != tt.request.SessionID {
				t.Errorf("SessionID = %v, want %v", got.SessionID, tt.request.SessionID)
			}
		})
	}
}

// TestApproachComparisonJSONMarshaling tests JSON marshaling for ApproachComparison.
func TestApproachComparisonJSONMarshaling(t *testing.T) {
	tests := []struct {
		name       string
		comparison ApproachComparison
	}{
		{
			name: "full comparison",
			comparison: ApproachComparison{
				Topic: "Error Handling Strategies",
				Approaches: []Approach{
					{
						ID:          "approach-001",
						Name:        "Error Wrapping",
						Description: "Wrap errors with context",
						Pros:        []string{"Preserves stack trace", "Adds context"},
						Cons:        []string{"Can be verbose"},
						UseCases:    []string{"Library code", "API handlers"},
						SourceIDs:   []string{"src-001"},
					},
					{
						ID:          "approach-002",
						Name:        "Sentinel Errors",
						Description: "Use predefined error values",
						Pros:        []string{"Easy to compare"},
						Cons:        []string{"Limited context"},
						UseCases:    []string{"Simple error checking"},
						SourceIDs:   []string{"src-002"},
					},
				},
				Summary:             "Error wrapping is preferred for most cases",
				RecommendedApproach: "approach-001",
				Rationale:           "Better debugging experience",
			},
		},
		{
			name: "comparison without recommendation",
			comparison: ApproachComparison{
				Topic: "Database Choice",
				Approaches: []Approach{
					{
						ID:          "approach-003",
						Name:        "PostgreSQL",
						Description: "Relational database",
						Pros:        []string{"ACID compliant", "Rich features"},
						Cons:        []string{"Scaling complexity"},
					},
				},
				Summary: "Choice depends on use case",
			},
		},
		{
			name: "minimal comparison",
			comparison: ApproachComparison{
				Topic:      "Simple Topic",
				Approaches: []Approach{},
				Summary:    "No approaches analyzed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.comparison)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got ApproachComparison
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.Topic != tt.comparison.Topic {
				t.Errorf("Topic = %v, want %v", got.Topic, tt.comparison.Topic)
			}
			if got.Summary != tt.comparison.Summary {
				t.Errorf("Summary = %v, want %v", got.Summary, tt.comparison.Summary)
			}
			if got.RecommendedApproach != tt.comparison.RecommendedApproach {
				t.Errorf("RecommendedApproach = %v, want %v", got.RecommendedApproach, tt.comparison.RecommendedApproach)
			}
			if got.Rationale != tt.comparison.Rationale {
				t.Errorf("Rationale = %v, want %v", got.Rationale, tt.comparison.Rationale)
			}
			if len(got.Approaches) != len(tt.comparison.Approaches) {
				t.Errorf("len(Approaches) = %v, want %v", len(got.Approaches), len(tt.comparison.Approaches))
			}
		})
	}
}

// TestApproachJSONMarshaling tests JSON marshaling for Approach.
func TestApproachJSONMarshaling(t *testing.T) {
	tests := []struct {
		name     string
		approach Approach
	}{
		{
			name: "full approach",
			approach: Approach{
				ID:          "approach-001",
				Name:        "Test Approach",
				Description: "A test approach",
				Pros:        []string{"pro1", "pro2"},
				Cons:        []string{"con1"},
				UseCases:    []string{"use case 1", "use case 2"},
				SourceIDs:   []string{"src-001", "src-002"},
			},
		},
		{
			name: "minimal approach",
			approach: Approach{
				ID:          "approach-002",
				Name:        "Minimal",
				Description: "Minimal approach",
				Pros:        []string{},
				Cons:        []string{},
			},
		},
		{
			name: "approach with nil slices",
			approach: Approach{
				ID:          "approach-003",
				Name:        "Nil Slices",
				Description: "Approach with nil slices",
				Pros:        nil,
				Cons:        nil,
				UseCases:    nil,
				SourceIDs:   nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.approach)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got Approach
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.ID != tt.approach.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.approach.ID)
			}
			if got.Name != tt.approach.Name {
				t.Errorf("Name = %v, want %v", got.Name, tt.approach.Name)
			}
			if got.Description != tt.approach.Description {
				t.Errorf("Description = %v, want %v", got.Description, tt.approach.Description)
			}
		})
	}
}

// TestMemoryThresholdJSONMarshaling tests JSON marshaling for MemoryThreshold.
func TestMemoryThresholdJSONMarshaling(t *testing.T) {
	tests := []struct {
		name      string
		threshold MemoryThreshold
	}{
		{
			name:      "default values",
			threshold: DefaultMemoryThreshold(),
		},
		{
			name: "custom values",
			threshold: MemoryThreshold{
				CheckpointThreshold: 0.75,
				CompactThreshold:    0.90,
			},
		},
		{
			name: "zero values",
			threshold: MemoryThreshold{
				CheckpointThreshold: 0.0,
				CompactThreshold:    0.0,
			},
		},
		{
			name: "boundary values",
			threshold: MemoryThreshold{
				CheckpointThreshold: 1.0,
				CompactThreshold:    1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.threshold)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got MemoryThreshold
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if got.CheckpointThreshold != tt.threshold.CheckpointThreshold {
				t.Errorf("CheckpointThreshold = %v, want %v", got.CheckpointThreshold, tt.threshold.CheckpointThreshold)
			}
			if got.CompactThreshold != tt.threshold.CompactThreshold {
				t.Errorf("CompactThreshold = %v, want %v", got.CompactThreshold, tt.threshold.CompactThreshold)
			}
		})
	}
}

// TestConstantValues verifies all constant values are correctly defined.
func TestConstantValues(t *testing.T) {
	t.Run("SourceType constants", func(t *testing.T) {
		tests := []struct {
			constant SourceType
			expected string
		}{
			{SourceTypeGitHub, "github"},
			{SourceTypeArticle, "article"},
			{SourceTypePaper, "paper"},
			{SourceTypeRFC, "rfc"},
			{SourceTypeDocumentation, "documentation"},
		}

		for _, tt := range tests {
			if string(tt.constant) != tt.expected {
				t.Errorf("SourceType constant %v = %q, want %q", tt.constant, string(tt.constant), tt.expected)
			}
		}
	})

	t.Run("ConfidenceLevel constants", func(t *testing.T) {
		tests := []struct {
			constant ConfidenceLevel
			expected string
		}{
			{ConfidenceLevelHigh, "high"},
			{ConfidenceLevelMedium, "medium"},
			{ConfidenceLevelLow, "low"},
		}

		for _, tt := range tests {
			if string(tt.constant) != tt.expected {
				t.Errorf("ConfidenceLevel constant %v = %q, want %q", tt.constant, string(tt.constant), tt.expected)
			}
		}
	})

	t.Run("ResearchIntent constants", func(t *testing.T) {
		tests := []struct {
			constant ResearchIntent
			expected string
		}{
			{IntentRecall, "recall"},
			{IntentCheck, "check"},
		}

		for _, tt := range tests {
			if string(tt.constant) != tt.expected {
				t.Errorf("ResearchIntent constant %v = %q, want %q", tt.constant, string(tt.constant), tt.expected)
			}
		}
	})

	t.Run("ResearchDomain constants", func(t *testing.T) {
		tests := []struct {
			constant ResearchDomain
			expected string
		}{
			{DomainPatterns, "patterns"},
			{DomainDecisions, "decisions"},
			{DomainLearnings, "learnings"},
		}

		for _, tt := range tests {
			if string(tt.constant) != tt.expected {
				t.Errorf("ResearchDomain constant %v = %q, want %q", tt.constant, string(tt.constant), tt.expected)
			}
		}
	})
}

// TestValidFunctionReturnTypes verifies that Valid* functions return fresh slices.
func TestValidFunctionReturnTypes(t *testing.T) {
	t.Run("ValidSourceTypes returns new slice each time", func(t *testing.T) {
		slice1 := ValidSourceTypes()
		slice2 := ValidSourceTypes()

		// Modify slice1
		if len(slice1) > 0 {
			slice1[0] = "modified"
		}

		// slice2 should not be affected
		if len(slice2) > 0 && slice2[0] == "modified" {
			t.Error("ValidSourceTypes() returned shared slice reference")
		}
	})

	t.Run("ValidConfidenceLevels returns new slice each time", func(t *testing.T) {
		slice1 := ValidConfidenceLevels()
		slice2 := ValidConfidenceLevels()

		if len(slice1) > 0 {
			slice1[0] = "modified"
		}

		if len(slice2) > 0 && slice2[0] == "modified" {
			t.Error("ValidConfidenceLevels() returned shared slice reference")
		}
	})

	t.Run("ValidResearchIntents returns new slice each time", func(t *testing.T) {
		slice1 := ValidResearchIntents()
		slice2 := ValidResearchIntents()

		if len(slice1) > 0 {
			slice1[0] = "modified"
		}

		if len(slice2) > 0 && slice2[0] == "modified" {
			t.Error("ValidResearchIntents() returned shared slice reference")
		}
	})

	t.Run("ValidResearchDomains returns new slice each time", func(t *testing.T) {
		slice1 := ValidResearchDomains()
		slice2 := ValidResearchDomains()

		if len(slice1) > 0 {
			slice1[0] = "modified"
		}

		if len(slice2) > 0 && slice2[0] == "modified" {
			t.Error("ValidResearchDomains() returned shared slice reference")
		}
	})
}

// TestIngestResultMarshalJSONPreservesAllFields verifies MarshalJSON preserves all IngestResult fields.
func TestIngestResultMarshalJSONPreservesAllFields(t *testing.T) {
	original := IngestResult{
		Success:    true,
		SourceID:   "test-source-id",
		TokenCount: 12345,
		Error:      "no error",
		Duration:   123456789 * time.Nanosecond,
	}

	data, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	// Verify all expected fields are present
	expectedFields := []string{"success", "source_id", "token_count", "error", "duration"}
	for _, field := range expectedFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("MarshalJSON() missing field %q", field)
		}
	}

	// Verify values
	if parsed["success"] != true {
		t.Errorf("success = %v, want true", parsed["success"])
	}
	if parsed["source_id"] != "test-source-id" {
		t.Errorf("source_id = %v, want test-source-id", parsed["source_id"])
	}
	if parsed["token_count"] != float64(12345) {
		t.Errorf("token_count = %v, want 12345", parsed["token_count"])
	}
	if parsed["error"] != "no error" {
		t.Errorf("error = %v, want 'no error'", parsed["error"])
	}
	// Duration should be string representation
	if _, ok := parsed["duration"].(string); !ok {
		t.Errorf("duration should be a string, got %T", parsed["duration"])
	}
}

// TestTypeAliasesAreStrings verifies that custom type aliases are string-based.
func TestTypeAliasesAreStrings(t *testing.T) {
	var sourceType SourceType = "test"
	if reflect.TypeOf(sourceType).Kind() != reflect.String {
		t.Error("SourceType should be string-based")
	}

	var confidenceLevel ConfidenceLevel = "test"
	if reflect.TypeOf(confidenceLevel).Kind() != reflect.String {
		t.Error("ConfidenceLevel should be string-based")
	}

	var researchIntent ResearchIntent = "test"
	if reflect.TypeOf(researchIntent).Kind() != reflect.String {
		t.Error("ResearchIntent should be string-based")
	}

	var researchDomain ResearchDomain = "test"
	if reflect.TypeOf(researchDomain).Kind() != reflect.String {
		t.Error("ResearchDomain should be string-based")
	}
}

// TestJSONTagsOmitEmpty verifies that optional fields with omitempty work correctly.
func TestJSONTagsOmitEmpty(t *testing.T) {
	t.Run("Source omits empty description", func(t *testing.T) {
		source := Source{
			ID:          "src-001",
			Type:        SourceTypeGitHub,
			URL:         "https://example.com",
			Title:       "Test",
			Description: "",
			IngestedAt:  time.Now(),
			UpdatedAt:   time.Now(),
			TokenCount:  100,
			Quality:     0.5,
		}

		data, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if _, ok := parsed["description"]; ok {
			t.Error("description should be omitted when empty")
		}
	})

	t.Run("Source includes non-empty description", func(t *testing.T) {
		source := Source{
			ID:          "src-001",
			Type:        SourceTypeGitHub,
			URL:         "https://example.com",
			Title:       "Test",
			Description: "A description",
			IngestedAt:  time.Now(),
			UpdatedAt:   time.Now(),
			TokenCount:  100,
			Quality:     0.5,
		}

		data, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if _, ok := parsed["description"]; !ok {
			t.Error("description should be included when non-empty")
		}
	})

	t.Run("Source omits nil metadata", func(t *testing.T) {
		source := Source{
			ID:         "src-001",
			Type:       SourceTypeGitHub,
			URL:        "https://example.com",
			Title:      "Test",
			IngestedAt: time.Now(),
			UpdatedAt:  time.Now(),
			TokenCount: 100,
			Quality:    0.5,
			Metadata:   nil,
		}

		data, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if _, ok := parsed["metadata"]; ok {
			t.Error("metadata should be omitted when nil")
		}
	})
}

// TestEmptyStructsJSONMarshaling verifies that empty/zero-value structs marshal correctly.
func TestEmptyStructsJSONMarshaling(t *testing.T) {
	t.Run("empty Finding marshals without error", func(t *testing.T) {
		var finding Finding
		_, err := json.Marshal(finding)
		if err != nil {
			t.Errorf("json.Marshal() error = %v", err)
		}
	})

	t.Run("empty Recommendation marshals without error", func(t *testing.T) {
		var rec Recommendation
		_, err := json.Marshal(rec)
		if err != nil {
			t.Errorf("json.Marshal() error = %v", err)
		}
	})

	t.Run("empty ResearchQuery marshals without error", func(t *testing.T) {
		var query ResearchQuery
		_, err := json.Marshal(query)
		if err != nil {
			t.Errorf("json.Marshal() error = %v", err)
		}
	})

	t.Run("empty IngestResult marshals without error", func(t *testing.T) {
		var result IngestResult
		_, err := result.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON() error = %v", err)
		}
	})

	t.Run("empty MemoryThreshold marshals without error", func(t *testing.T) {
		var threshold MemoryThreshold
		_, err := json.Marshal(threshold)
		if err != nil {
			t.Errorf("json.Marshal() error = %v", err)
		}
	})
}

// TestValidFunctionsCoverage verifies that Valid* functions cover all defined constants.
func TestValidFunctionsCoverage(t *testing.T) {
	t.Run("ValidSourceTypes covers all SourceType constants", func(t *testing.T) {
		validTypes := ValidSourceTypes()
		expected := map[SourceType]bool{
			SourceTypeGitHub:        false,
			SourceTypeArticle:       false,
			SourceTypePaper:         false,
			SourceTypeRFC:           false,
			SourceTypeDocumentation: false,
		}

		for _, st := range validTypes {
			if _, ok := expected[st]; ok {
				expected[st] = true
			} else {
				t.Errorf("ValidSourceTypes() returned unexpected type: %v", st)
			}
		}

		for st, found := range expected {
			if !found {
				t.Errorf("ValidSourceTypes() missing type: %v", st)
			}
		}
	})

	t.Run("ValidConfidenceLevels covers all ConfidenceLevel constants", func(t *testing.T) {
		validLevels := ValidConfidenceLevels()
		expected := map[ConfidenceLevel]bool{
			ConfidenceLevelHigh:   false,
			ConfidenceLevelMedium: false,
			ConfidenceLevelLow:    false,
		}

		for _, cl := range validLevels {
			if _, ok := expected[cl]; ok {
				expected[cl] = true
			} else {
				t.Errorf("ValidConfidenceLevels() returned unexpected level: %v", cl)
			}
		}

		for cl, found := range expected {
			if !found {
				t.Errorf("ValidConfidenceLevels() missing level: %v", cl)
			}
		}
	})

	t.Run("ValidResearchIntents covers all ResearchIntent constants", func(t *testing.T) {
		validIntents := ValidResearchIntents()
		expected := map[ResearchIntent]bool{
			IntentRecall: false,
			IntentCheck:  false,
		}

		for _, ri := range validIntents {
			if _, ok := expected[ri]; ok {
				expected[ri] = true
			} else {
				t.Errorf("ValidResearchIntents() returned unexpected intent: %v", ri)
			}
		}

		for ri, found := range expected {
			if !found {
				t.Errorf("ValidResearchIntents() missing intent: %v", ri)
			}
		}
	})

	t.Run("ValidResearchDomains covers all ResearchDomain constants", func(t *testing.T) {
		validDomains := ValidResearchDomains()
		expected := map[ResearchDomain]bool{
			DomainPatterns:  false,
			DomainDecisions: false,
			DomainLearnings: false,
		}

		for _, rd := range validDomains {
			if _, ok := expected[rd]; ok {
				expected[rd] = true
			} else {
				t.Errorf("ValidResearchDomains() returned unexpected domain: %v", rd)
			}
		}

		for rd, found := range expected {
			if !found {
				t.Errorf("ValidResearchDomains() missing domain: %v", rd)
			}
		}
	})
}
