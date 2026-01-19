package chunking

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// CK.4.1 LearnedSemanticSplitter Integration
// =============================================================================

// Chunk represents a piece of text content that has been split for processing.
// Each chunk contains the text content, metadata about its position, and
// a unique identifier for tracking in the retrieval feedback system.
type Chunk struct {
	// ID uniquely identifies this chunk for feedback tracking.
	ID string `json:"id"`

	// Content is the actual text content of the chunk.
	Content string `json:"content"`

	// Domain indicates the type of content this chunk contains.
	Domain Domain `json:"domain"`

	// TokenCount is the number of tokens in this chunk.
	TokenCount int `json:"token_count"`

	// StartOffset is the byte offset where this chunk starts in the original content.
	StartOffset int `json:"start_offset"`

	// EndOffset is the byte offset where this chunk ends in the original content.
	EndOffset int `json:"end_offset"`

	// ContextBefore contains tokens included before the main content for context.
	ContextBefore string `json:"context_before,omitempty"`

	// ContextAfter contains tokens included after the main content for context.
	ContextAfter string `json:"context_after,omitempty"`

	// Metadata holds additional key-value metadata about this chunk.
	Metadata map[string]string `json:"metadata,omitempty"`

	// CreatedAt is the timestamp when this chunk was created.
	CreatedAt time.Time `json:"created_at"`
}

// SemanticSplitter is the interface for basic semantic text splitting.
// It defines the contract that any splitter implementation must satisfy.
type SemanticSplitter interface {
	// Split divides content into chunks based on semantic boundaries.
	Split(ctx context.Context, content string, maxTokens int) ([]Chunk, error)

	// CountTokens returns the number of tokens in the given text.
	CountTokens(text string) int
}

// IDGenerator generates unique IDs for chunks.
// This allows for customizable ID generation strategies.
type IDGenerator interface {
	// GenerateID creates a unique ID for a chunk.
	GenerateID(content string, domain Domain, index int) string
}

// DefaultIDGenerator provides a simple default ID generation strategy.
type DefaultIDGenerator struct {
	prefix string
}

// NewDefaultIDGenerator creates a new DefaultIDGenerator with the given prefix.
func NewDefaultIDGenerator(prefix string) *DefaultIDGenerator {
	return &DefaultIDGenerator{prefix: prefix}
}

// GenerateID creates a unique ID using the prefix, domain, index, and timestamp.
func (d *DefaultIDGenerator) GenerateID(content string, domain Domain, index int) string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("%s_%s_%d_%d", d.prefix, domain.String(), index, timestamp)
}

// LearnedSemanticSplitter wraps an existing SemanticSplitter and uses
// learned parameters from ChunkConfigLearner to optimize chunk sizes
// based on domain-specific feedback.
type LearnedSemanticSplitter struct {
	mu sync.RWMutex

	// baseSplitter is the underlying splitter to delegate to.
	baseSplitter SemanticSplitter

	// learner provides domain-specific chunk configurations.
	learner *ChunkConfigLearner

	// idGenerator creates unique IDs for chunks.
	idGenerator IDGenerator

	// explore determines whether to use Thompson Sampling for exploration.
	// When true, chunk sizes are sampled from learned distributions.
	// When false, mean (best estimate) values are used.
	explore bool

	// sessionID identifies the current session for feedback tracking.
	sessionID string
}

// LearnedSplitterConfig holds configuration options for LearnedSemanticSplitter.
type LearnedSplitterConfig struct {
	// BaseSplitter is the underlying splitter (required).
	BaseSplitter SemanticSplitter

	// Learner provides learned chunk configurations (required).
	Learner *ChunkConfigLearner

	// IDGenerator creates unique chunk IDs (optional, defaults to DefaultIDGenerator).
	IDGenerator IDGenerator

	// Explore enables Thompson Sampling exploration (default: true).
	Explore bool

	// SessionID identifies the current session (optional).
	SessionID string
}

// NewLearnedSemanticSplitter creates a new LearnedSemanticSplitter with the given configuration.
func NewLearnedSemanticSplitter(config LearnedSplitterConfig) (*LearnedSemanticSplitter, error) {
	if config.BaseSplitter == nil {
		return nil, fmt.Errorf("base splitter is required")
	}
	if config.Learner == nil {
		return nil, fmt.Errorf("learner is required")
	}

	idGen := config.IDGenerator
	if idGen == nil {
		idGen = NewDefaultIDGenerator("chunk")
	}

	sessionID := config.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}

	return &LearnedSemanticSplitter{
		baseSplitter: config.BaseSplitter,
		learner:      config.Learner,
		idGenerator:  idGen,
		explore:      config.Explore,
		sessionID:    sessionID,
	}, nil
}

// SplitWithLearning splits content using domain-specific learned parameters.
// It retrieves the optimal chunk configuration for the given domain and
// uses those parameters to guide the splitting process.
func (lss *LearnedSemanticSplitter) SplitWithLearning(ctx context.Context, content string, domain Domain) ([]Chunk, error) {
	lss.mu.RLock()
	explore := lss.explore
	sessionID := lss.sessionID
	lss.mu.RUnlock()

	// Get domain-specific configuration from the learner
	config := lss.learner.GetConfig(domain, explore)

	// Get effective target and context sizes
	targetTokens := config.GetEffectiveTargetTokens(explore)
	contextBefore := config.GetEffectiveContextTokensBefore(explore)
	contextAfter := config.GetEffectiveContextTokensAfter(explore)

	// Use the base splitter with learned target size
	baseChunks, err := lss.baseSplitter.Split(ctx, content, targetTokens)
	if err != nil {
		return nil, fmt.Errorf("base splitter failed: %w", err)
	}

	// Post-process chunks to add context and metadata
	result := make([]Chunk, 0, len(baseChunks))
	for i, chunk := range baseChunks {
		// Generate unique ID
		chunkID := lss.idGenerator.GenerateID(chunk.Content, domain, i)

		// Add context before and after if configured
		enhancedChunk := Chunk{
			ID:          chunkID,
			Content:     chunk.Content,
			Domain:      domain,
			TokenCount:  lss.baseSplitter.CountTokens(chunk.Content),
			StartOffset: chunk.StartOffset,
			EndOffset:   chunk.EndOffset,
			CreatedAt:   time.Now(),
			Metadata: map[string]string{
				"session_id":     sessionID,
				"target_tokens":  fmt.Sprintf("%d", targetTokens),
				"context_before": fmt.Sprintf("%d", contextBefore),
				"context_after":  fmt.Sprintf("%d", contextAfter),
				"explore":        fmt.Sprintf("%t", explore),
			},
		}

		// Add surrounding context if needed
		if contextBefore > 0 || contextAfter > 0 {
			lss.addSurroundingContext(&enhancedChunk, content, contextBefore, contextAfter)
		}

		result = append(result, enhancedChunk)
	}

	return result, nil
}

// addSurroundingContext extracts and adds context before and after the chunk.
func (lss *LearnedSemanticSplitter) addSurroundingContext(chunk *Chunk, fullContent string, contextBefore, contextAfter int) {
	if chunk.StartOffset > 0 && contextBefore > 0 {
		// Extract content before the chunk
		startPos := chunk.StartOffset
		contextText := extractContextBefore(fullContent, startPos, contextBefore, lss.baseSplitter)
		chunk.ContextBefore = contextText
	}

	if chunk.EndOffset < len(fullContent) && contextAfter > 0 {
		// Extract content after the chunk
		endPos := chunk.EndOffset
		contextText := extractContextAfter(fullContent, endPos, contextAfter, lss.baseSplitter)
		chunk.ContextAfter = contextText
	}
}

// extractContextBefore extracts up to maxTokens of context before the given position.
func extractContextBefore(content string, position int, maxTokens int, tokenCounter SemanticSplitter) string {
	if position <= 0 || maxTokens <= 0 {
		return ""
	}

	// Start from position and work backwards to find enough context
	startSearch := position
	if startSearch > len(content) {
		startSearch = len(content)
	}

	// Binary search for the right amount of context
	low := 0
	high := startSearch

	for low < high {
		mid := (low + high) / 2
		candidate := content[mid:position]
		tokens := tokenCounter.CountTokens(candidate)

		if tokens <= maxTokens {
			high = mid
		} else {
			low = mid + 1
		}
	}

	return strings.TrimSpace(content[low:position])
}

// extractContextAfter extracts up to maxTokens of context after the given position.
func extractContextAfter(content string, position int, maxTokens int, tokenCounter SemanticSplitter) string {
	if position >= len(content) || maxTokens <= 0 {
		return ""
	}

	// Binary search for the right amount of context
	low := position
	high := len(content)

	for low < high {
		mid := (low + high + 1) / 2
		candidate := content[position:mid]
		tokens := tokenCounter.CountTokens(candidate)

		if tokens <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}

	return strings.TrimSpace(content[position:low])
}

// SetExplore updates whether Thompson Sampling exploration is enabled.
func (lss *LearnedSemanticSplitter) SetExplore(explore bool) {
	lss.mu.Lock()
	defer lss.mu.Unlock()
	lss.explore = explore
}

// SetSessionID updates the session ID for feedback tracking.
func (lss *LearnedSemanticSplitter) SetSessionID(sessionID string) {
	lss.mu.Lock()
	defer lss.mu.Unlock()
	lss.sessionID = sessionID
}

// GetSessionID returns the current session ID.
func (lss *LearnedSemanticSplitter) GetSessionID() string {
	lss.mu.RLock()
	defer lss.mu.RUnlock()
	return lss.sessionID
}

// GetLearner returns the underlying ChunkConfigLearner for external feedback recording.
func (lss *LearnedSemanticSplitter) GetLearner() *ChunkConfigLearner {
	return lss.learner
}

// Split implements SemanticSplitter interface, using DomainGeneral as the default domain.
func (lss *LearnedSemanticSplitter) Split(ctx context.Context, content string, maxTokens int) ([]Chunk, error) {
	return lss.SplitWithLearning(ctx, content, DomainGeneral)
}

// CountTokens delegates to the base splitter.
func (lss *LearnedSemanticSplitter) CountTokens(text string) int {
	return lss.baseSplitter.CountTokens(text)
}

// =============================================================================
// Simple Token-Based Splitter Implementation
// =============================================================================

// SimpleTokenSplitter provides a basic implementation of SemanticSplitter
// that splits content based on approximate token counts using whitespace.
// This is useful for testing and as a fallback when no embedding model is available.
type SimpleTokenSplitter struct {
	// tokensPerWord is the average number of tokens per word (default ~1.3 for English).
	tokensPerWord float64
}

// NewSimpleTokenSplitter creates a new SimpleTokenSplitter.
func NewSimpleTokenSplitter() *SimpleTokenSplitter {
	return &SimpleTokenSplitter{
		tokensPerWord: 1.3,
	}
}

// CountTokens estimates the number of tokens in text based on word count.
func (sts *SimpleTokenSplitter) CountTokens(text string) int {
	words := len(strings.Fields(text))
	return int(float64(words) * sts.tokensPerWord)
}

// Split divides content into chunks of approximately maxTokens size.
func (sts *SimpleTokenSplitter) Split(ctx context.Context, content string, maxTokens int) ([]Chunk, error) {
	if maxTokens <= 0 {
		return nil, fmt.Errorf("maxTokens must be positive, got %d", maxTokens)
	}

	// Estimate max words per chunk
	maxWords := int(float64(maxTokens) / sts.tokensPerWord)
	if maxWords < 1 {
		maxWords = 1
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return []Chunk{}, nil
	}

	var chunks []Chunk
	var currentWords []string
	currentStart := 0

	for i, word := range words {
		currentWords = append(currentWords, word)

		if len(currentWords) >= maxWords || i == len(words)-1 {
			chunkContent := strings.Join(currentWords, " ")
			endOffset := currentStart + len(chunkContent)

			chunk := Chunk{
				Content:     chunkContent,
				TokenCount:  sts.CountTokens(chunkContent),
				StartOffset: currentStart,
				EndOffset:   endOffset,
				CreatedAt:   time.Now(),
			}
			chunks = append(chunks, chunk)

			currentStart = endOffset + 1 // +1 for the space
			currentWords = nil
		}
	}

	return chunks, nil
}
