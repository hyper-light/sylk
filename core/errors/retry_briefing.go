// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AttemptSummary captures the outcome of a single pipeline attempt.
type AttemptSummary struct {
	AttemptNumber  int
	Timestamp      time.Time
	Approach       string
	Error          string
	ErrorTier      ErrorTier
	TokensSpent    int
	PhasesComplete []string
	Duration       time.Duration
}

// RetryBriefing provides context for an agent about to retry a failed operation.
type RetryBriefing struct {
	OriginalRequest   string
	AttemptNumber     int
	PriorAttempts     []*AttemptSummary
	FailureAnalysis   string
	SuggestedApproach string
	AvoidPatterns     []string
	CreatedAt         time.Time
}

// HasPriorAttempts returns true if there are prior attempts recorded.
func (b *RetryBriefing) HasPriorAttempts() bool {
	return len(b.PriorAttempts) > 0
}

// LastFailureReason returns the error from the most recent attempt.
func (b *RetryBriefing) LastFailureReason() string {
	if !b.HasPriorAttempts() {
		return ""
	}
	return b.PriorAttempts[len(b.PriorAttempts)-1].Error
}

// ArchivalistClient defines the interface for querying historical attempt data.
type ArchivalistClient interface {
	QueryFailures(ctx context.Context, pipelineID string) ([]*AttemptSummary, error)
	RecordAttempt(ctx context.Context, pipelineID string, summary *AttemptSummary) error
}

// RetryBriefingConfig configures the retry briefing service.
type RetryBriefingConfig struct {
	MaxPriorAttempts int  `yaml:"max_prior_attempts"` // default: 5
	AnalysisEnabled  bool `yaml:"analysis_enabled"`   // default: true
}

// DefaultRetryBriefingConfig returns the default configuration.
func DefaultRetryBriefingConfig() RetryBriefingConfig {
	return RetryBriefingConfig{
		MaxPriorAttempts: 5,
		AnalysisEnabled:  true,
	}
}

// RetryBriefingService prepares briefings for retry attempts.
type RetryBriefingService struct {
	config      RetryBriefingConfig
	archivalist ArchivalistClient // can be nil for graceful degradation
}

// NewRetryBriefingService creates a new RetryBriefingService.
func NewRetryBriefingService(
	config RetryBriefingConfig,
	archivalist ArchivalistClient,
) *RetryBriefingService {
	return &RetryBriefingService{
		config:      config,
		archivalist: archivalist,
	}
}

// PrepareRetryBriefing creates a briefing for an upcoming retry attempt.
func (s *RetryBriefingService) PrepareRetryBriefing(
	ctx context.Context,
	pipelineID string,
	currentError error,
) (*RetryBriefing, error) {
	attempts := s.fetchPriorAttempts(ctx, pipelineID)
	return s.buildBriefing(attempts, currentError), nil
}

// fetchPriorAttempts retrieves historical attempts, handling nil archivalist.
func (s *RetryBriefingService) fetchPriorAttempts(
	ctx context.Context,
	pipelineID string,
) []*AttemptSummary {
	if s.archivalist == nil {
		return nil
	}
	attempts, err := s.archivalist.QueryFailures(ctx, pipelineID)
	if err != nil {
		return nil
	}
	return s.limitAttempts(attempts)
}

// limitAttempts restricts the number of attempts to the configured maximum.
func (s *RetryBriefingService) limitAttempts(
	attempts []*AttemptSummary,
) []*AttemptSummary {
	if len(attempts) <= s.config.MaxPriorAttempts {
		return attempts
	}
	return attempts[len(attempts)-s.config.MaxPriorAttempts:]
}

// buildBriefing constructs the RetryBriefing from attempts and current error.
func (s *RetryBriefingService) buildBriefing(
	attempts []*AttemptSummary,
	currentError error,
) *RetryBriefing {
	return &RetryBriefing{
		AttemptNumber:     len(attempts) + 1,
		PriorAttempts:     attempts,
		FailureAnalysis:   s.analyzeFailurePatterns(attempts),
		SuggestedApproach: s.suggestApproach(attempts, currentError),
		AvoidPatterns:     s.extractAvoidPatterns(attempts),
		CreatedAt:         time.Now(),
	}
}

// analyzeFailurePatterns identifies common patterns in prior failures.
func (s *RetryBriefingService) analyzeFailurePatterns(
	attempts []*AttemptSummary,
) string {
	if !s.config.AnalysisEnabled || len(attempts) == 0 {
		return ""
	}
	return s.buildAnalysisSummary(attempts)
}

// buildAnalysisSummary creates a summary of failure patterns.
func (s *RetryBriefingService) buildAnalysisSummary(
	attempts []*AttemptSummary,
) string {
	tierCounts := s.countTiers(attempts)
	return s.formatTierAnalysis(tierCounts, len(attempts))
}

// countTiers counts occurrences of each error tier.
func (s *RetryBriefingService) countTiers(
	attempts []*AttemptSummary,
) map[ErrorTier]int {
	counts := make(map[ErrorTier]int)
	for _, a := range attempts {
		counts[a.ErrorTier]++
	}
	return counts
}

// formatTierAnalysis formats tier counts into a readable analysis.
func (s *RetryBriefingService) formatTierAnalysis(
	tierCounts map[ErrorTier]int,
	total int,
) string {
	dominant := s.findDominantTier(tierCounts)
	return fmt.Sprintf("%d prior failures, primarily %s errors", total, dominant)
}

// findDominantTier returns the tier with the most occurrences.
func (s *RetryBriefingService) findDominantTier(
	tierCounts map[ErrorTier]int,
) ErrorTier {
	var dominant ErrorTier
	maxCount := 0
	for tier, count := range tierCounts {
		if count > maxCount {
			dominant = tier
			maxCount = count
		}
	}
	return dominant
}

// suggestApproach recommends an approach based on failure history.
func (s *RetryBriefingService) suggestApproach(
	attempts []*AttemptSummary,
	currentError error,
) string {
	if len(attempts) == 0 {
		return s.suggestForNewAttempt(currentError)
	}
	return s.suggestBasedOnHistory(attempts)
}

// suggestForNewAttempt provides a suggestion when no history exists.
func (s *RetryBriefingService) suggestForNewAttempt(currentError error) string {
	tier := GetTier(currentError)
	return s.suggestionForTier(tier)
}

// suggestionForTier returns a suggestion based on error tier.
func (s *RetryBriefingService) suggestionForTier(tier ErrorTier) string {
	suggestions := map[ErrorTier]string{
		TierTransient:         "retry with exponential backoff",
		TierPermanent:         "investigate root cause before retry",
		TierUserFixable:       "verify configuration and credentials",
		TierExternalRateLimit: "wait for rate limit reset",
		TierExternalDegrading: "try alternative approach or service",
	}
	if suggestion, ok := suggestions[tier]; ok {
		return suggestion
	}
	return "proceed with caution"
}

// suggestBasedOnHistory analyzes history to suggest an approach.
func (s *RetryBriefingService) suggestBasedOnHistory(
	attempts []*AttemptSummary,
) string {
	last := attempts[len(attempts)-1]
	if last.ErrorTier == TierTransient {
		return "previous transient error may have resolved"
	}
	return fmt.Sprintf("avoid approach: %s", last.Approach)
}

// extractAvoidPatterns identifies approaches that have failed.
func (s *RetryBriefingService) extractAvoidPatterns(
	attempts []*AttemptSummary,
) []string {
	if len(attempts) == 0 {
		return nil
	}
	return s.collectFailedApproaches(attempts)
}

// collectFailedApproaches gathers unique failed approaches.
func (s *RetryBriefingService) collectFailedApproaches(
	attempts []*AttemptSummary,
) []string {
	seen := make(map[string]bool)
	var patterns []string
	for _, a := range attempts {
		if a.Approach != "" && !seen[a.Approach] {
			seen[a.Approach] = true
			patterns = append(patterns, a.Approach)
		}
	}
	return patterns
}

// RecordAttempt records an attempt to the archivalist if available.
func (s *RetryBriefingService) RecordAttempt(
	ctx context.Context,
	pipelineID string,
	summary *AttemptSummary,
) error {
	if s.archivalist == nil {
		return nil
	}
	return s.archivalist.RecordAttempt(ctx, pipelineID, summary)
}

// FormatForAgent formats a briefing into a concise agent prompt.
func FormatForAgent(briefing *RetryBriefing) string {
	if briefing == nil {
		return ""
	}
	return buildAgentMessage(briefing)
}

// buildAgentMessage constructs the agent message from briefing.
func buildAgentMessage(briefing *RetryBriefing) string {
	var parts []string
	parts = append(parts, formatAttemptHeader(briefing))
	parts = appendIfNonEmpty(parts, formatPriorFailure(briefing))
	parts = appendIfNonEmpty(parts, formatAvoidance(briefing))
	parts = appendIfNonEmpty(parts, formatSuggestion(briefing))
	return strings.Join(parts, " ")
}

// formatAttemptHeader creates the attempt number header.
func formatAttemptHeader(briefing *RetryBriefing) string {
	return fmt.Sprintf("Attempt %d.", briefing.AttemptNumber)
}

// formatPriorFailure formats the prior failure reason if present.
func formatPriorFailure(briefing *RetryBriefing) string {
	reason := briefing.LastFailureReason()
	if reason == "" {
		return ""
	}
	return fmt.Sprintf("Prior attempt failed due to %s.", reason)
}

// formatAvoidance formats patterns to avoid if present.
func formatAvoidance(briefing *RetryBriefing) string {
	if len(briefing.AvoidPatterns) == 0 {
		return ""
	}
	return fmt.Sprintf("Avoid %s.", strings.Join(briefing.AvoidPatterns, ", "))
}

// formatSuggestion formats the suggested approach if present.
func formatSuggestion(briefing *RetryBriefing) string {
	if briefing.SuggestedApproach == "" {
		return ""
	}
	return fmt.Sprintf("Try %s.", briefing.SuggestedApproach)
}

// appendIfNonEmpty appends a string to the slice if non-empty.
func appendIfNonEmpty(parts []string, s string) []string {
	if s != "" {
		return append(parts, s)
	}
	return parts
}
