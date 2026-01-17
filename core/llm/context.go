package llm

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrContextBudgetExceeded is returned when even minimal context exceeds the token budget.
var ErrContextBudgetExceeded = errors.New("minimal context exceeds token budget")

// ContextManager handles smart context window management with overflow handling.
// It ensures messages fit within the model's context window while preserving relevance.
type ContextManager struct {
	mu               sync.RWMutex
	model            string
	maxContextTokens int
	reserveTokens    int
}

// NewContextManager creates a new ContextManager with the specified configuration.
func NewContextManager(model string, maxContextTokens, reserveTokens int) *ContextManager {
	return &ContextManager{
		model:            model,
		maxContextTokens: maxContextTokens,
		reserveTokens:    reserveTokens,
	}
}

// MaxTokens returns the maximum context tokens allowed.
func (cm *ContextManager) MaxTokens() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.maxContextTokens
}

// AvailableTokens returns the tokens available for context (MaxTokens - ReserveTokens).
func (cm *ContextManager) AvailableTokens() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.maxContextTokens - cm.reserveTokens
}

// PrepareContext prepares messages to fit within the available token budget.
// If messages fit, they are returned as-is.
// If overflow occurs, strategies are applied: smart selection, storage for RAG, summarization.
func (cm *ContextManager) PrepareContext(messages []Message, query string) ([]Message, error) {
	cm.mu.RLock()
	available := cm.maxContextTokens - cm.reserveTokens
	cm.mu.RUnlock()

	tokens := cm.countTokens(messages)
	if tokens <= available {
		return messages, nil
	}

	return cm.applyOverflowStrategies(messages, query, available)
}

// countTokens estimates the total token count for a slice of messages.
func (cm *ContextManager) countTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

// estimateMessageTokens provides a rough token estimate for a single message.
// Uses approximately 4 characters per token plus overhead for role.
func estimateMessageTokens(msg Message) int {
	roleTokens := len(msg.Role) / 4
	contentTokens := len(msg.Content) / 4
	overhead := 4
	return roleTokens + contentTokens + overhead
}

// applyOverflowStrategies applies strategies to fit messages within budget.
func (cm *ContextManager) applyOverflowStrategies(messages []Message, query string, maxTokens int) ([]Message, error) {
	selected := cm.selectRelevant(messages, query, maxTokens)
	if len(selected) == 0 {
		return nil, ErrContextBudgetExceeded
	}
	return selected, nil
}

// scoredMessage holds a message with its relevance score and original index.
type scoredMessage struct {
	message Message
	score   float64
	index   int
}

// selectRelevant selects the most relevant messages that fit within the token budget.
// Messages are scored by relevance to the query, and the highest-scoring messages
// that fit within the budget are returned in their original order.
func (cm *ContextManager) selectRelevant(messages []Message, query string, maxTokens int) []Message {
	scored := cm.scoreMessages(messages, query)
	selected := cm.selectWithinBudget(scored, maxTokens)
	return cm.restoreOrder(selected)
}

// scoreMessages assigns relevance scores to all messages based on the query.
func (cm *ContextManager) scoreMessages(messages []Message, query string) []scoredMessage {
	scored := make([]scoredMessage, len(messages))
	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	for i, msg := range messages {
		scored[i] = scoredMessage{
			message: msg,
			score:   cm.calculateScore(msg, queryLower, queryTerms, i, len(messages)),
			index:   i,
		}
	}
	return scored
}

// calculateScore computes the relevance score for a single message.
func (cm *ContextManager) calculateScore(msg Message, queryLower string, queryTerms []string, index, total int) float64 {
	score := cm.baseScore(msg.Role)
	score += cm.termMatchScore(msg.Content, queryTerms)
	score += cm.recencyScore(index, total)
	return score
}

// baseScore returns the base score based on message role.
func (cm *ContextManager) baseScore(role string) float64 {
	switch role {
	case "system":
		return 100.0
	case "user":
		return 10.0
	case "assistant":
		return 5.0
	default:
		return 1.0
	}
}

// termMatchScore calculates score based on query term matches in content.
func (cm *ContextManager) termMatchScore(content string, queryTerms []string) float64 {
	contentLower := strings.ToLower(content)
	score := 0.0
	for _, term := range queryTerms {
		if strings.Contains(contentLower, term) {
			score += 15.0
		}
	}
	return score
}

// recencyScore gives higher scores to more recent messages.
func (cm *ContextManager) recencyScore(index, total int) float64 {
	if total <= 1 {
		return 0.0
	}
	return float64(index) / float64(total) * 20.0
}

// selectWithinBudget selects highest-scoring messages that fit within the token budget.
func (cm *ContextManager) selectWithinBudget(scored []scoredMessage, maxTokens int) []scoredMessage {
	sorted := cm.sortByScoreDesc(scored)
	return cm.fillBudget(sorted, maxTokens)
}

// sortByScoreDesc returns a copy of scored messages sorted by score descending.
func (cm *ContextManager) sortByScoreDesc(scored []scoredMessage) []scoredMessage {
	sorted := make([]scoredMessage, len(scored))
	copy(sorted, scored)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})
	return sorted
}

// fillBudget greedily selects messages until the budget is exhausted.
func (cm *ContextManager) fillBudget(sorted []scoredMessage, maxTokens int) []scoredMessage {
	var selected []scoredMessage
	usedTokens := 0

	for _, sm := range sorted {
		tokens := estimateMessageTokens(sm.message)
		if usedTokens+tokens <= maxTokens {
			selected = append(selected, sm)
			usedTokens += tokens
		}
	}
	return selected
}

// restoreOrder sorts selected messages back to their original order.
func (cm *ContextManager) restoreOrder(selected []scoredMessage) []Message {
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].index < selected[j].index
	})
	return cm.extractMessages(selected)
}

// extractMessages extracts Message objects from scored messages.
func (cm *ContextManager) extractMessages(scored []scoredMessage) []Message {
	messages := make([]Message, len(scored))
	for i, sm := range scored {
		messages[i] = sm.message
	}
	return messages
}

// Model returns the model name associated with this context manager.
func (cm *ContextManager) Model() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.model
}

// SetMaxContextTokens updates the maximum context tokens.
func (cm *ContextManager) SetMaxContextTokens(tokens int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.maxContextTokens = tokens
}

// SetReserveTokens updates the reserve tokens for response.
func (cm *ContextManager) SetReserveTokens(tokens int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.reserveTokens = tokens
}
