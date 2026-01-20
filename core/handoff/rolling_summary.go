package handoff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// =============================================================================
// HO.6.2 RollingSummary - Incrementally Updated Conversation Summary
// =============================================================================
//
// RollingSummary maintains a compact, fixed-size summary of a conversation
// that is incrementally updated as new messages arrive. It uses a sliding
// window approach to maintain the most recent and relevant information
// within a token budget.
//
// The summary extracts key topics and maintains a condensed representation
// that can be used for context handoffs without requiring full conversation
// history.
//
// Features:
//   - Fixed maximum size (configurable, default 1000 tokens)
//   - Incremental updates with each new message
//   - Key topic extraction and tracking
//   - Sliding window over messages
//   - Thread-safe operations
//
// Example usage:
//
//	summary := NewRollingSummary(1000)
//	summary.AddMessage(Message{Role: "user", Content: "How do I write Go code?"})
//	summary.AddMessage(Message{Role: "assistant", Content: "Here's how..."})
//	text := summary.Summary()
//	topics := summary.KeyTopics()

// Message represents a conversation message for the rolling summary.
type Message struct {
	// Role identifies who sent the message (e.g., "user", "assistant", "system").
	Role string `json:"role"`

	// Content is the text content of the message.
	Content string `json:"content"`

	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp"`

	// TokenCount is the approximate token count for this message.
	// If 0, it will be estimated from content length.
	TokenCount int `json:"token_count,omitempty"`
}

// NewMessage creates a new Message with the current timestamp.
func NewMessage(role, content string) Message {
	return Message{
		Role:       role,
		Content:    content,
		Timestamp:  time.Now(),
		TokenCount: estimateTokens(content),
	}
}

// RollingSummary maintains an incrementally updated conversation summary
// with a fixed maximum token size.
type RollingSummary struct {
	mu sync.RWMutex

	// maxTokens is the maximum number of tokens for the summary.
	maxTokens int

	// currentTokens tracks the current token count.
	currentTokens int

	// messageBuffer stores recent messages using a circular buffer.
	messageBuffer *CircularBuffer[Message]

	// keyTopics tracks important topics mentioned in the conversation.
	keyTopics map[string]int // topic -> mention count

	// summaryText is the current condensed summary text.
	summaryText string

	// lastUpdated is when the summary was last updated.
	lastUpdated time.Time

	// messageCount tracks total messages processed.
	messageCount int

	// config holds configuration options.
	config *RollingSummaryConfig
}

// RollingSummaryConfig controls the behavior of the rolling summary.
type RollingSummaryConfig struct {
	// MaxTokens is the maximum token budget for the summary.
	MaxTokens int `json:"max_tokens"`

	// MessageBufferSize is how many recent messages to keep.
	MessageBufferSize int `json:"message_buffer_size"`

	// TopicExtractionEnabled enables automatic topic extraction.
	TopicExtractionEnabled bool `json:"topic_extraction_enabled"`

	// MaxTopics limits the number of tracked topics.
	MaxTopics int `json:"max_topics"`

	// MinTopicMentions is the minimum mentions to consider a topic key.
	MinTopicMentions int `json:"min_topic_mentions"`

	// TokensPerChar is the approximate tokens per character ratio.
	// Default is 0.25 (4 characters per token).
	TokensPerChar float64 `json:"tokens_per_char"`
}

// DefaultRollingSummaryConfig returns sensible defaults.
func DefaultRollingSummaryConfig() *RollingSummaryConfig {
	return &RollingSummaryConfig{
		MaxTokens:              1000,
		MessageBufferSize:      20,
		TopicExtractionEnabled: true,
		MaxTopics:              10,
		MinTopicMentions:       2,
		TokensPerChar:          0.25,
	}
}

// NewRollingSummary creates a new RollingSummary with the specified maximum tokens.
// If maxTokens is <= 0, uses the default of 1000 tokens.
func NewRollingSummary(maxTokens int) *RollingSummary {
	config := DefaultRollingSummaryConfig()
	if maxTokens > 0 {
		config.MaxTokens = maxTokens
	}
	return NewRollingSummaryWithConfig(config)
}

// NewRollingSummaryWithConfig creates a new RollingSummary with custom configuration.
func NewRollingSummaryWithConfig(config *RollingSummaryConfig) *RollingSummary {
	if config == nil {
		config = DefaultRollingSummaryConfig()
	}

	return &RollingSummary{
		maxTokens:     config.MaxTokens,
		currentTokens: 0,
		messageBuffer: NewCircularBuffer[Message](config.MessageBufferSize),
		keyTopics:     make(map[string]int),
		summaryText:   "",
		lastUpdated:   time.Now(),
		messageCount:  0,
		config:        config,
	}
}

// AddMessage incorporates a new message into the rolling summary.
// The summary is updated incrementally, maintaining the token budget.
func (rs *RollingSummary) AddMessage(msg Message) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.addMessageLocked(msg)
}

// addMessageLocked is the internal implementation of AddMessage.
// Caller must hold rs.mu write lock.
func (rs *RollingSummary) addMessageLocked(msg Message) {
	// Estimate tokens if not provided
	if msg.TokenCount == 0 {
		msg.TokenCount = estimateTokens(msg.Content)
	}

	// Set timestamp if not provided
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	// Add to message buffer (use locked variant)
	rs.messageBuffer.pushLocked(msg)
	rs.messageCount++

	// Extract topics if enabled
	if rs.config.TopicExtractionEnabled {
		rs.extractTopics(msg.Content)
	}

	// Rebuild summary
	rs.rebuildSummary()

	rs.lastUpdated = time.Now()
}

// Summary returns the current condensed summary text.
func (rs *RollingSummary) Summary() string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.summaryLocked()
}

// summaryLocked is the internal implementation of Summary.
// Caller must hold rs.mu (read or write lock).
func (rs *RollingSummary) summaryLocked() string {
	return rs.summaryText
}

// KeyTopics returns the list of key topics extracted from the conversation.
// Topics are sorted by mention count (most mentioned first).
func (rs *RollingSummary) KeyTopics() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.keyTopicsLocked()
}

// keyTopicsLocked is the internal implementation of KeyTopics.
// Caller must hold rs.mu (read or write lock).
// W4L.10: Pre-allocate slice with known capacity for better performance.
func (rs *RollingSummary) keyTopicsLocked() []string {
	// Pre-allocate slice with estimated capacity based on keyTopics map size
	topics := make([]string, 0, len(rs.keyTopics))

	// Collect topics that meet minimum mentions threshold
	for topic, count := range rs.keyTopics {
		if count >= rs.config.MinTopicMentions {
			topics = append(topics, topic)
		}
	}

	// Sort by mention count using standard library sort (stable, efficient)
	sort.Slice(topics, func(i, j int) bool {
		return rs.keyTopics[topics[i]] > rs.keyTopics[topics[j]]
	})

	// Limit to MaxTopics
	if len(topics) > rs.config.MaxTopics {
		topics = topics[:rs.config.MaxTopics]
	}

	return topics
}

// TokenCount returns the current token count of the summary.
func (rs *RollingSummary) TokenCount() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.tokenCountLocked()
}

// tokenCountLocked is the internal implementation of TokenCount.
// Caller must hold rs.mu (read or write lock).
func (rs *RollingSummary) tokenCountLocked() int {
	return rs.currentTokens
}

// MaxTokens returns the maximum token budget.
func (rs *RollingSummary) MaxTokens() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.maxTokens
}

// MessageCount returns the total number of messages processed.
func (rs *RollingSummary) MessageCount() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.messageCountLocked()
}

// messageCountLocked is the internal implementation of MessageCount.
// Caller must hold rs.mu (read or write lock).
func (rs *RollingSummary) messageCountLocked() int {
	return rs.messageCount
}

// RecentMessages returns the most recent messages from the buffer.
func (rs *RollingSummary) RecentMessages(n int) []Message {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.messageBuffer.RecentN(n)
}

// AllBufferedMessages returns all messages currently in the buffer.
func (rs *RollingSummary) AllBufferedMessages() []Message {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.messageBuffer.Items()
}

// LastUpdated returns when the summary was last updated.
func (rs *RollingSummary) LastUpdated() time.Time {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.lastUpdated
}

// Clear resets the rolling summary to its initial state.
func (rs *RollingSummary) Clear() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.clearLocked()
}

// clearLocked is the internal implementation of Clear.
// Caller must hold rs.mu write lock.
func (rs *RollingSummary) clearLocked() {
	rs.messageBuffer.clearLocked()
	rs.keyTopics = make(map[string]int)
	rs.summaryText = ""
	rs.currentTokens = 0
	rs.messageCount = 0
	rs.lastUpdated = time.Now()
}

// =============================================================================
// Internal Methods
// =============================================================================

// rebuildSummary constructs the summary from buffered messages within the token budget.
// Caller must hold rs.mu write lock.
func (rs *RollingSummary) rebuildSummary() {
	var builder strings.Builder
	messages := rs.messageBuffer.itemsLocked()
	tokenBudget := rs.maxTokens

	// Reserve some tokens for topics section if enabled
	topicsReserve := 0
	if rs.config.TopicExtractionEnabled && len(rs.keyTopics) > 0 {
		topicsReserve = 50 // Reserve ~50 tokens for topics
	}
	messageBudget := tokenBudget - topicsReserve

	// Build summary from messages, starting from most recent
	// but output in chronological order
	summaryParts := make([]string, 0, len(messages))
	usedTokens := 0

	// Process from oldest to newest, tracking what fits
	for _, msg := range messages {
		part := rs.formatMessage(msg)
		partTokens := estimateTokens(part)

		if usedTokens+partTokens <= messageBudget {
			summaryParts = append(summaryParts, part)
			usedTokens += partTokens
		} else if len(summaryParts) == 0 {
			// Always include at least the most recent message, truncated if needed
			truncated := rs.truncateToTokens(part, messageBudget)
			summaryParts = append(summaryParts, truncated)
			usedTokens += estimateTokens(truncated)
		}
	}

	// Build the final summary
	for i, part := range summaryParts {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(part)
	}

	// Add key topics section if enabled and we have topics
	if rs.config.TopicExtractionEnabled {
		topics := rs.getTopTopicsUnlocked(5)
		if len(topics) > 0 {
			builder.WriteString("\n\nKey Topics: ")
			builder.WriteString(strings.Join(topics, ", "))
		}
	}

	rs.summaryText = builder.String()
	rs.currentTokens = estimateTokens(rs.summaryText)
}

// formatMessage formats a single message for the summary.
func (rs *RollingSummary) formatMessage(msg Message) string {
	// Truncate long messages
	content := msg.Content
	maxContentLen := 500 // Limit individual message length
	if len(content) > maxContentLen {
		content = content[:maxContentLen] + "..."
	}

	return msg.Role + ": " + content
}

// truncateToTokens truncates text to approximately the given token count.
func (rs *RollingSummary) truncateToTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}

	// Approximate: 4 chars per token
	maxChars := int(float64(maxTokens) / rs.config.TokensPerChar)
	if len(text) <= maxChars {
		return text
	}

	return text[:maxChars] + "..."
}

// extractTopics extracts potential topics from the text content.
func (rs *RollingSummary) extractTopics(content string) {
	// Simple topic extraction: look for capitalized words and common patterns
	words := tokenizeForTopics(content)

	for _, word := range words {
		if isLikelyTopic(word) {
			rs.keyTopics[strings.ToLower(word)]++
		}
	}

	// Prune topics if we have too many
	rs.pruneTopics()
}

// pruneTopics removes low-value topics to prevent unbounded growth.
func (rs *RollingSummary) pruneTopics() {
	if len(rs.keyTopics) <= rs.config.MaxTopics*2 {
		return
	}

	// Find the minimum count to keep
	counts := make([]int, 0, len(rs.keyTopics))
	for _, count := range rs.keyTopics {
		counts = append(counts, count)
	}

	// Sort counts descending
	for i := 0; i < len(counts); i++ {
		for j := i + 1; j < len(counts); j++ {
			if counts[j] > counts[i] {
				counts[i], counts[j] = counts[j], counts[i]
			}
		}
	}

	// Keep only top topics
	threshold := 1
	if len(counts) > rs.config.MaxTopics {
		threshold = counts[rs.config.MaxTopics-1]
	}

	// Remove topics below threshold
	for topic, count := range rs.keyTopics {
		if count < threshold {
			delete(rs.keyTopics, topic)
		}
	}
}

// getTopTopicsUnlocked returns top N topics (caller must hold lock).
// W4L.10: Pre-allocate slice and use efficient sorting.
func (rs *RollingSummary) getTopTopicsUnlocked(n int) []string {
	// Pre-allocate slice with estimated capacity
	topics := make([]string, 0, len(rs.keyTopics))
	for topic, count := range rs.keyTopics {
		if count >= rs.config.MinTopicMentions {
			topics = append(topics, topic)
		}
	}

	// Sort by count using standard library sort
	sort.Slice(topics, func(i, j int) bool {
		return rs.keyTopics[topics[i]] > rs.keyTopics[topics[j]]
	})

	if n > len(topics) {
		n = len(topics)
	}
	return topics[:n]
}

// =============================================================================
// Topic Extraction Helpers
// =============================================================================

// tokenizeForTopics splits text into potential topic words.
func tokenizeForTopics(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() >= 3 {
				words = append(words, current.String())
			}
			current.Reset()
		}
	}

	if current.Len() >= 3 {
		words = append(words, current.String())
	}

	return words
}

// isLikelyTopic determines if a word is likely a meaningful topic.
func isLikelyTopic(word string) bool {
	if len(word) < 3 || len(word) > 30 {
		return false
	}

	// Skip common stop words
	lower := strings.ToLower(word)
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true, "but": true,
		"not": true, "you": true, "all": true, "can": true, "had": true,
		"her": true, "was": true, "one": true, "our": true, "out": true,
		"has": true, "have": true, "been": true, "this": true, "that": true,
		"with": true, "they": true, "from": true, "what": true, "when": true,
		"will": true, "would": true, "there": true, "their": true, "which": true,
		"about": true, "could": true, "other": true, "these": true, "than": true,
		"then": true, "them": true, "some": true, "into": true, "just": true,
		"also": true, "only": true, "your": true, "more": true, "very": true,
	}

	return !stopWords[lower]
}

// estimateTokens provides a rough token count estimate.
// Uses the approximation that 1 token is about 4 characters.
func estimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	// Rough estimate: ~4 characters per token
	tokens := len(text) / 4
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// =============================================================================
// JSON Serialization
// =============================================================================

// rollingSummaryJSON is the JSON representation of a RollingSummary.
type rollingSummaryJSON struct {
	MaxTokens     int               `json:"max_tokens"`
	CurrentTokens int               `json:"current_tokens"`
	Messages      []Message         `json:"messages"`
	KeyTopics     map[string]int    `json:"key_topics"`
	SummaryText   string            `json:"summary_text"`
	LastUpdated   string            `json:"last_updated"`
	MessageCount  int               `json:"message_count"`
	Config        *RollingSummaryConfig `json:"config"`
}

// MarshalJSON implements json.Marshaler.
func (rs *RollingSummary) MarshalJSON() ([]byte, error) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	return json.Marshal(rollingSummaryJSON{
		MaxTokens:     rs.maxTokens,
		CurrentTokens: rs.currentTokens,
		Messages:      rs.messageBuffer.itemsLocked(),
		KeyTopics:     rs.keyTopics,
		SummaryText:   rs.summaryText,
		LastUpdated:   rs.lastUpdated.Format(time.RFC3339Nano),
		MessageCount:  rs.messageCount,
		Config:        rs.config,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
// W4L.10: Error wrapping with %w for proper error chains.
func (rs *RollingSummary) UnmarshalJSON(data []byte) error {
	var temp rollingSummaryJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal rolling summary JSON: %w", err)
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.maxTokens = temp.MaxTokens
	rs.currentTokens = temp.CurrentTokens
	rs.summaryText = temp.SummaryText
	rs.messageCount = temp.MessageCount

	if temp.Config != nil {
		rs.config = temp.Config
	} else {
		rs.config = DefaultRollingSummaryConfig()
	}

	// Restore message buffer
	rs.messageBuffer = NewCircularBuffer[Message](rs.config.MessageBufferSize)
	for _, msg := range temp.Messages {
		rs.messageBuffer.pushLocked(msg)
	}

	// Restore key topics
	rs.keyTopics = temp.KeyTopics
	if rs.keyTopics == nil {
		rs.keyTopics = make(map[string]int)
	}

	// Parse timestamp
	if temp.LastUpdated != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastUpdated); err == nil {
			rs.lastUpdated = t
		}
	}

	return nil
}

// =============================================================================
// Summary Stats
// =============================================================================

// SummaryStats contains statistics about the rolling summary.
type SummaryStats struct {
	MaxTokens       int       `json:"max_tokens"`
	CurrentTokens   int       `json:"current_tokens"`
	TokenUsage      float64   `json:"token_usage"`
	MessageCount    int       `json:"message_count"`
	BufferedCount   int       `json:"buffered_count"`
	TopicCount      int       `json:"topic_count"`
	LastUpdated     time.Time `json:"last_updated"`
	SummaryLength   int       `json:"summary_length"`
}

// Stats returns statistics about the current summary state.
func (rs *RollingSummary) Stats() SummaryStats {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	usage := 0.0
	if rs.maxTokens > 0 {
		usage = float64(rs.currentTokens) / float64(rs.maxTokens)
	}

	return SummaryStats{
		MaxTokens:     rs.maxTokens,
		CurrentTokens: rs.currentTokens,
		TokenUsage:    usage,
		MessageCount:  rs.messageCount,
		BufferedCount: rs.messageBuffer.lenLocked(),
		TopicCount:    len(rs.keyTopics),
		LastUpdated:   rs.lastUpdated,
		SummaryLength: len(rs.summaryText),
	}
}
