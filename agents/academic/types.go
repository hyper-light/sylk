// Package academic implements the Academic agent for external knowledge RAG.
// The Academic agent researches best practices, papers, and external sources.
package academic

import (
	"encoding/json"
	"time"
)

// SourceType represents the type of external source.
type SourceType string

const (
	// SourceTypeGitHub represents a GitHub repository.
	SourceTypeGitHub SourceType = "github"
	// SourceTypeArticle represents a web article.
	SourceTypeArticle SourceType = "article"
	// SourceTypePaper represents a research paper.
	SourceTypePaper SourceType = "paper"
	// SourceTypeRFC represents an RFC document.
	SourceTypeRFC SourceType = "rfc"
	// SourceTypeDocumentation represents official documentation.
	SourceTypeDocumentation SourceType = "documentation"
)

// ConfidenceLevel represents the confidence in a research finding.
type ConfidenceLevel string

const (
	// ConfidenceLevelHigh indicates high confidence (well-established).
	ConfidenceLevelHigh ConfidenceLevel = "high"
	// ConfidenceLevelMedium indicates medium confidence (generally accepted).
	ConfidenceLevelMedium ConfidenceLevel = "medium"
	// ConfidenceLevelLow indicates low confidence (experimental or debated).
	ConfidenceLevelLow ConfidenceLevel = "low"
)

// ResearchIntent classifies the type of research query.
type ResearchIntent string

const (
	// IntentRecall retrieves information from ingested sources.
	IntentRecall ResearchIntent = "recall"
	// IntentCheck verifies a claim against sources.
	IntentCheck ResearchIntent = "check"
)

// ResearchDomain classifies the domain of research.
type ResearchDomain string

const (
	// DomainPatterns covers design patterns and architectural approaches.
	DomainPatterns ResearchDomain = "patterns"
	// DomainDecisions covers technical decision-making.
	DomainDecisions ResearchDomain = "decisions"
	// DomainLearnings covers lessons learned and best practices.
	DomainLearnings ResearchDomain = "learnings"
)

// Source represents an external knowledge source.
type Source struct {
	// ID is the unique identifier for this source.
	ID string `json:"id"`
	// Type indicates the source type.
	Type SourceType `json:"type"`
	// URL is the source location.
	URL string `json:"url"`
	// Title is the human-readable title.
	Title string `json:"title"`
	// Description provides a brief summary.
	Description string `json:"description,omitempty"`
	// IngestedAt is when the source was ingested.
	IngestedAt time.Time `json:"ingested_at"`
	// UpdatedAt is when the source was last updated.
	UpdatedAt time.Time `json:"updated_at"`
	// TokenCount is the estimated token count of the content.
	TokenCount int `json:"token_count"`
	// Quality is a quality score (0.0-1.0).
	Quality float64 `json:"quality"`
	// Metadata contains source-specific metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// GitHubRepoMetadata contains GitHub-specific source metadata.
type GitHubRepoMetadata struct {
	// Owner is the repository owner.
	Owner string `json:"owner"`
	// Repo is the repository name.
	Repo string `json:"repo"`
	// Branch is the ingested branch.
	Branch string `json:"branch"`
	// CommitSHA is the commit at ingestion time.
	CommitSHA string `json:"commit_sha"`
	// Stars is the star count at ingestion.
	Stars int `json:"stars"`
	// Language is the primary language.
	Language string `json:"language"`
	// Topics are repository topics.
	Topics []string `json:"topics,omitempty"`
}

// ArticleMetadata contains article-specific source metadata.
type ArticleMetadata struct {
	// Author is the article author.
	Author string `json:"author,omitempty"`
	// PublishedAt is the publication date.
	PublishedAt *time.Time `json:"published_at,omitempty"`
	// Site is the publishing site.
	Site string `json:"site,omitempty"`
	// Tags are article tags.
	Tags []string `json:"tags,omitempty"`
}

// Finding represents a single research finding.
type Finding struct {
	// ID is the unique identifier.
	ID string `json:"id"`
	// Topic is the finding topic.
	Topic string `json:"topic"`
	// Summary is a brief summary of the finding.
	Summary string `json:"summary"`
	// Details contains the full finding details.
	Details string `json:"details,omitempty"`
	// Confidence indicates the confidence level.
	Confidence ConfidenceLevel `json:"confidence"`
	// SourceIDs references the sources for this finding.
	SourceIDs []string `json:"source_ids"`
	// Citations are formatted citations.
	Citations []string `json:"citations,omitempty"`
}

// Recommendation represents a research-based recommendation.
type Recommendation struct {
	// ID is the unique identifier.
	ID string `json:"id"`
	// Title is the recommendation title.
	Title string `json:"title"`
	// Description explains the recommendation.
	Description string `json:"description"`
	// Rationale explains why this is recommended.
	Rationale string `json:"rationale"`
	// Applicability describes when this applies.
	Applicability string `json:"applicability,omitempty"`
	// Confidence indicates the confidence level.
	Confidence ConfidenceLevel `json:"confidence"`
	// Alternatives lists alternative approaches.
	Alternatives []string `json:"alternatives,omitempty"`
	// SourceIDs references supporting sources.
	SourceIDs []string `json:"source_ids"`
}

// ResearchQuery represents a research request.
type ResearchQuery struct {
	// Query is the research question.
	Query string `json:"query"`
	// Intent is the research intent.
	Intent ResearchIntent `json:"intent,omitempty"`
	// Domain is the research domain.
	Domain ResearchDomain `json:"domain,omitempty"`
	// MaxSources limits the number of sources to consult.
	MaxSources int `json:"max_sources,omitempty"`
	// LanguageFilter limits results to specific languages.
	LanguageFilter string `json:"language_filter,omitempty"`
	// SessionID is the requesting session.
	SessionID string `json:"session_id,omitempty"`
}

// ResearchResult represents the result of a research query.
type ResearchResult struct {
	// QueryID links to the original query.
	QueryID string `json:"query_id"`
	// Findings are the research findings.
	Findings []Finding `json:"findings"`
	// Recommendations are actionable recommendations.
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	// SourcesConsulted lists sources used.
	SourcesConsulted []string `json:"sources_consulted"`
	// Confidence is the overall confidence level.
	Confidence ConfidenceLevel `json:"confidence"`
	// CachedAt indicates if this was cached and when.
	CachedAt *time.Time `json:"cached_at,omitempty"`
	// GeneratedAt is when this result was generated.
	GeneratedAt time.Time `json:"generated_at"`
}

// ResearchConstraint captures a hard requirement that should survive handoff to planning.
type ResearchConstraint struct {
	// Name is the short identifier for the constraint.
	Name string `json:"name"`
	// Description explains the operational impact of the constraint.
	Description string `json:"description"`
}

// ResearchInvariant captures a property the resulting design must preserve.
type ResearchInvariant struct {
	// Name is the short invariant identifier.
	Name string `json:"name"`
	// Description explains what must remain true.
	Description string `json:"description"`
}

// ArchitectureOption captures one candidate architecture from the research.
type ArchitectureOption struct {
	// ID is the unique option identifier.
	ID string `json:"id"`
	// Name is the short option name.
	Name string `json:"name"`
	// Summary explains the option.
	Summary string `json:"summary"`
	// Pros lists key advantages.
	Pros []string `json:"pros,omitempty"`
	// Cons lists key disadvantages.
	Cons []string `json:"cons,omitempty"`
	// Fit explains when the option is appropriate.
	Fit string `json:"fit,omitempty"`
	// Risks lists operational or implementation risks.
	Risks []string `json:"risks,omitempty"`
	// Confidence is the confidence level for this option.
	Confidence ConfidenceLevel `json:"confidence,omitempty"`
	// SourceIDs references supporting sources.
	SourceIDs []string `json:"source_ids,omitempty"`
}

// CodebaseApplicability summarizes how well the research fits the current repository reality.
type CodebaseApplicability struct {
	// Summary is the overall applicability assessment.
	Summary string `json:"summary"`
	// MatchingPatterns captures relevant existing repo patterns.
	MatchingPatterns []string `json:"matching_patterns,omitempty"`
	// Conflicts captures design tensions with the current codebase.
	Conflicts []string `json:"conflicts,omitempty"`
	// RequiredChanges captures likely repo changes needed to adopt the proposal.
	RequiredChanges []string `json:"required_changes,omitempty"`
	// Confidence is a 0.0-1.0 applicability score.
	Confidence float64 `json:"confidence,omitempty"`
}

// ArchitectHandoff captures the distilled planning payload intended for the Architect.
type ArchitectHandoff struct {
	// PlanningSummary is the concise handoff summary for planning.
	PlanningSummary string `json:"planning_summary"`
	// RecommendedOptionID points at the preferred option.
	RecommendedOptionID string `json:"recommended_option_id,omitempty"`
	// RequiredDecisions lists unresolved planning decisions.
	RequiredDecisions []string `json:"required_decisions,omitempty"`
	// SuggestedTasks lists likely initial planning tasks.
	SuggestedTasks []string `json:"suggested_tasks,omitempty"`
	// AcceptanceSignals lists what success should look like.
	AcceptanceSignals []string `json:"acceptance_signals,omitempty"`
}

// AcademicResearchPaper represents a comprehensive research paper generated
// during context compaction at the 85% threshold.
type AcademicResearchPaper struct {
	// ID is the unique identifier.
	ID string `json:"id"`
	// Timestamp is when the paper was generated.
	Timestamp time.Time `json:"timestamp"`
	// SessionID is the session that generated this paper.
	SessionID string `json:"session_id"`
	// ContextUsage is the context window usage at generation time.
	ContextUsage float64 `json:"context_usage"`
	// ResearchSlug is the stable identifier for this research line.
	ResearchSlug string `json:"research_slug,omitempty"`
	// Version is the paper revision number for the slug.
	Version int `json:"version,omitempty"`
	// Title is the paper title.
	Title string `json:"title"`
	// Abstract summarizes the findings.
	Abstract string `json:"abstract"`
	// ProblemFraming captures the user/work objective this paper addresses.
	ProblemFraming string `json:"problem_framing,omitempty"`
	// Constraints captures hard requirements discovered during research.
	Constraints []ResearchConstraint `json:"constraints,omitempty"`
	// Invariants captures properties the implementation should preserve.
	Invariants []ResearchInvariant `json:"invariants,omitempty"`
	// TopicsResearched lists topics covered.
	TopicsResearched []string `json:"topics_researched"`
	// KeyFindings are the main findings.
	KeyFindings []Finding `json:"key_findings"`
	// SourcesCited are references used.
	SourcesCited []Source `json:"sources_cited"`
	// OptionMatrix captures the main candidate architectures.
	OptionMatrix []ArchitectureOption `json:"option_matrix,omitempty"`
	// RejectedOptions lists rejected approaches and why they were dropped.
	RejectedOptions []string `json:"rejected_options,omitempty"`
	// TradeOffs captures important design trade-offs surfaced by the research.
	TradeOffs []string `json:"trade_offs,omitempty"`
	// CodebaseApplicability captures fit against the current repository.
	CodebaseApplicability *CodebaseApplicability `json:"codebase_applicability,omitempty"`
	// Recommendations are actionable recommendations.
	Recommendations []string `json:"recommendations"`
	// MigrationConcerns captures migration-specific risks or requirements.
	MigrationConcerns []string `json:"migration_concerns,omitempty"`
	// OperationalConcerns captures runtime and operations concerns.
	OperationalConcerns []string `json:"operational_concerns,omitempty"`
	// OpenQuestions are unresolved questions.
	OpenQuestions []string `json:"open_questions"`
	// RelatedTopics are topics for future research.
	RelatedTopics []string `json:"related_topics"`
	// ArchitectHandoff captures the distilled planning handoff for the Architect.
	ArchitectHandoff *ArchitectHandoff `json:"architect_handoff,omitempty"`
	// PaperPath is the on-disk artifact path when persisted.
	PaperPath string `json:"paper_path,omitempty"`
}

// IngestRequest represents a request to ingest a new source.
type IngestRequest struct {
	// URL is the source URL.
	URL string `json:"url"`
	// Type is the source type.
	Type SourceType `json:"type"`
	// Deep indicates whether to perform deep analysis.
	Deep bool `json:"deep,omitempty"`
	// SessionID is the requesting session.
	SessionID string `json:"session_id,omitempty"`
}

// IngestResult represents the result of an ingestion.
type IngestResult struct {
	// Success indicates if ingestion succeeded.
	Success bool `json:"success"`
	// SourceID is the ID of the ingested source.
	SourceID string `json:"source_id,omitempty"`
	// TokenCount is the token count of ingested content.
	TokenCount int `json:"token_count,omitempty"`
	// Error is the error if ingestion failed.
	Error string `json:"error,omitempty"`
	// Duration is how long ingestion took.
	Duration time.Duration `json:"duration"`
}

// ApproachComparison represents a comparison of different approaches.
type ApproachComparison struct {
	// Topic is what's being compared.
	Topic string `json:"topic"`
	// Approaches are the approaches being compared.
	Approaches []Approach `json:"approaches"`
	// Summary summarizes the comparison.
	Summary string `json:"summary"`
	// RecommendedApproach is the recommended approach ID.
	RecommendedApproach string `json:"recommended_approach,omitempty"`
	// Rationale explains the recommendation.
	Rationale string `json:"rationale,omitempty"`
}

// Approach represents a single approach in a comparison.
type Approach struct {
	// ID is the unique identifier.
	ID string `json:"id"`
	// Name is the approach name.
	Name string `json:"name"`
	// Description explains the approach.
	Description string `json:"description"`
	// Pros are advantages.
	Pros []string `json:"pros"`
	// Cons are disadvantages.
	Cons []string `json:"cons"`
	// UseCases describe when this approach is best.
	UseCases []string `json:"use_cases,omitempty"`
	// SourceIDs reference supporting sources.
	SourceIDs []string `json:"source_ids,omitempty"`
}

// MemoryThreshold defines context window thresholds for Academic agent.
type MemoryThreshold struct {
	// CheckpointThreshold triggers research paper generation (default: 0.85).
	CheckpointThreshold float64 `json:"checkpoint_threshold"`
	// CompactThreshold triggers full compaction (default: 0.95).
	CompactThreshold float64 `json:"compact_threshold"`
}

// DefaultMemoryThreshold returns the default memory thresholds.
func DefaultMemoryThreshold() MemoryThreshold {
	return MemoryThreshold{
		CheckpointThreshold: 0.85,
		CompactThreshold:    0.95,
	}
}

// MarshalJSON implements custom JSON marshaling for IngestResult.
func (r IngestResult) MarshalJSON() ([]byte, error) {
	type Alias IngestResult
	return json.Marshal(&struct {
		Alias
		Duration string `json:"duration"`
	}{
		Alias:    Alias(r),
		Duration: r.Duration.String(),
	})
}

// ValidSourceTypes returns all valid source types.
func ValidSourceTypes() []SourceType {
	return []SourceType{
		SourceTypeGitHub,
		SourceTypeArticle,
		SourceTypePaper,
		SourceTypeRFC,
		SourceTypeDocumentation,
	}
}

// ValidConfidenceLevels returns all valid confidence levels.
func ValidConfidenceLevels() []ConfidenceLevel {
	return []ConfidenceLevel{
		ConfidenceLevelHigh,
		ConfidenceLevelMedium,
		ConfidenceLevelLow,
	}
}

// ValidResearchIntents returns all valid research intents.
func ValidResearchIntents() []ResearchIntent {
	return []ResearchIntent{
		IntentRecall,
		IntentCheck,
	}
}

// ValidResearchDomains returns all valid research domains.
func ValidResearchDomains() []ResearchDomain {
	return []ResearchDomain{
		DomainPatterns,
		DomainDecisions,
		DomainLearnings,
	}
}
