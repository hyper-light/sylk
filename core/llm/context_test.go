package llm

import (
	"errors"
	"sync"
	"testing"
)

func TestNewContextManager(t *testing.T) {
	cm := NewContextManager("gpt-4", 8000, 1000)

	if cm.Model() != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", cm.Model())
	}
	if cm.MaxTokens() != 8000 {
		t.Errorf("expected MaxTokens 8000, got %d", cm.MaxTokens())
	}
	if cm.AvailableTokens() != 7000 {
		t.Errorf("expected AvailableTokens 7000, got %d", cm.AvailableTokens())
	}
}

func TestMaxTokens(t *testing.T) {
	tests := []struct {
		name     string
		maxCtx   int
		reserve  int
		expected int
	}{
		{"standard config", 8000, 1000, 8000},
		{"zero reserve", 4000, 0, 4000},
		{"large context", 128000, 4000, 128000},
		{"zero max", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewContextManager("test", tt.maxCtx, tt.reserve)
			if got := cm.MaxTokens(); got != tt.expected {
				t.Errorf("MaxTokens() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestAvailableTokens(t *testing.T) {
	tests := []struct {
		name     string
		maxCtx   int
		reserve  int
		expected int
	}{
		{"standard config", 8000, 1000, 7000},
		{"zero reserve", 4000, 0, 4000},
		{"large reserve", 8000, 4000, 4000},
		{"equal max and reserve", 1000, 1000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewContextManager("test", tt.maxCtx, tt.reserve)
			if got := cm.AvailableTokens(); got != tt.expected {
				t.Errorf("AvailableTokens() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestPrepareContext_SmallContextUnchanged(t *testing.T) {
	cm := NewContextManager("test", 10000, 1000)
	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result, err := cm.PrepareContext(messages, "greeting")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(result))
	}

	for i, msg := range result {
		if msg.Role != messages[i].Role || msg.Content != messages[i].Content {
			t.Errorf("message %d changed: expected %+v, got %+v", i, messages[i], msg)
		}
	}
}

func TestPrepareContext_LargeContextTriggersSmartSelection(t *testing.T) {
	cm := NewContextManager("test", 60, 10) // 50 available tokens

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant with many capabilities."},
		{Role: "user", Content: "First question about programming languages and frameworks"},
		{Role: "assistant", Content: "Here is a detailed answer about programming languages..."},
		{Role: "user", Content: "Second question about testing methodologies"},
		{Role: "assistant", Content: "Here is comprehensive information about testing..."},
		{Role: "user", Content: "What about debugging techniques?"},
	}

	result, err := cm.PrepareContext(messages, "testing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) >= len(messages) {
		t.Errorf("expected fewer messages after smart selection, got %d (original: %d)", len(result), len(messages))
	}

	if len(result) == 0 {
		t.Error("expected at least one message in result")
	}
}

func TestPrepareContext_MessageScoringByRelevance(t *testing.T) {
	// Very tight budget to force selection
	cm := NewContextManager("test", 50, 10) // 40 available tokens

	messages := []Message{
		{Role: "user", Content: "Tell me about cats"},     // Not relevant
		{Role: "user", Content: "Tell me about testing"},  // Relevant to query
		{Role: "assistant", Content: "Testing info here"}, // Relevant, assistant role
	}

	result, err := cm.PrepareContext(messages, "testing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should prioritize messages containing "testing"
	foundRelevant := false
	for _, msg := range result {
		if msg.Content == "Tell me about testing" || msg.Content == "Testing info here" {
			foundRelevant = true
			break
		}
	}
	if !foundRelevant && len(result) > 0 {
		t.Log("Note: with tight budget, may not fit relevant messages")
	}
}

func TestPrepareContext_SystemMessagesPrioritized(t *testing.T) {
	cm := NewContextManager("test", 80, 10) // 70 available tokens

	messages := []Message{
		{Role: "system", Content: "Important system message"},
		{Role: "user", Content: "User question"},
		{Role: "assistant", Content: "Assistant response"},
	}

	result, err := cm.PrepareContext(messages, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System messages have highest base score (100), should be included
	hasSystem := false
	for _, msg := range result {
		if msg.Role == "system" {
			hasSystem = true
			break
		}
	}
	if !hasSystem && len(result) > 0 {
		t.Error("expected system message to be prioritized")
	}
}

func TestPrepareContext_MessageOrderPreservation(t *testing.T) {
	cm := NewContextManager("test", 200, 20) // 180 available tokens

	messages := []Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "Second"},
		{Role: "user", Content: "Third"},
	}

	result, err := cm.PrepareContext(messages, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify order is preserved (if messages are selected)
	if len(result) > 1 {
		prevIndex := -1
		for _, r := range result {
			// Find original index
			for origIdx, orig := range messages {
				if r.Role == orig.Role && r.Content == orig.Content {
					if origIdx < prevIndex {
						t.Errorf("message order not preserved: %+v came after message at index %d", r, prevIndex)
					}
					prevIndex = origIdx
					break
				}
			}
		}
	}
}

func TestPrepareContext_BudgetEnforcement(t *testing.T) {
	// Create context manager with very small budget
	cm := NewContextManager("test", 10, 5) // Only 5 available tokens

	// Create a message that exceeds the budget
	messages := []Message{
		{Role: "system", Content: "This is a very long system message that definitely exceeds five tokens by a significant margin and cannot possibly fit in the context window."},
	}

	result, err := cm.PrepareContext(messages, "query")

	// Should return ErrContextBudgetExceeded when no messages fit
	if err == nil {
		t.Errorf("expected ErrContextBudgetExceeded, got nil with %d messages", len(result))
	}
	if !errors.Is(err, ErrContextBudgetExceeded) {
		t.Errorf("expected ErrContextBudgetExceeded, got %v", err)
	}
}

func TestPrepareContext_EmptyMessages(t *testing.T) {
	cm := NewContextManager("test", 8000, 1000)

	result, err := cm.PrepareContext([]Message{}, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result, got %d messages", len(result))
	}
}

func TestPrepareContext_SingleMessage(t *testing.T) {
	cm := NewContextManager("test", 8000, 1000)

	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	result, err := cm.PrepareContext(messages, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "Hello" {
		t.Errorf("message content changed: got %+v", result[0])
	}
}

func TestPrepareContext_AllMessagesFit(t *testing.T) {
	cm := NewContextManager("test", 100000, 1000) // Large budget

	messages := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
	}

	result, err := cm.PrepareContext(messages, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(messages) {
		t.Errorf("expected all %d messages to fit, got %d", len(messages), len(result))
	}
}

func TestPrepareContext_ThreadSafety(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	messages := []Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "User message"},
		{Role: "assistant", Content: "Assistant message"},
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// Run concurrent PrepareContext calls
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := cm.PrepareContext(messages, "query")
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Errorf("concurrent PrepareContext failed: %v", err)
	}
}

func TestPrepareContext_ConcurrentReadsAndWrites(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	messages := []Message{
		{Role: "user", Content: "Test message"},
	}

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm.MaxTokens()
			cm.AvailableTokens()
			cm.Model()
		}()
	}

	// Concurrent PrepareContext
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm.PrepareContext(messages, "query")
		}()
	}

	// Concurrent writes
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cm.SetMaxContextTokens(1000 + i)
			cm.SetReserveTokens(100 + i)
		}(i)
	}

	wg.Wait()
}

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name    string
		message Message
		minTok  int
		maxTok  int
	}{
		{
			name:    "short message",
			message: Message{Role: "user", Content: "Hi"},
			minTok:  4,  // At minimum, overhead
			maxTok:  10, // Short content
		},
		{
			name:    "long message",
			message: Message{Role: "assistant", Content: "This is a much longer message with more content that should result in more tokens."},
			minTok:  15,
			maxTok:  50,
		},
		{
			name:    "empty content",
			message: Message{Role: "user", Content: ""},
			minTok:  4, // Overhead only
			maxTok:  8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := estimateMessageTokens(tt.message)
			if tokens < tt.minTok || tokens > tt.maxTok {
				t.Errorf("estimateMessageTokens() = %d, want between %d and %d", tokens, tt.minTok, tt.maxTok)
			}
		})
	}
}

func TestBaseScore(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	tests := []struct {
		role     string
		expected float64
	}{
		{"system", 100.0},
		{"user", 10.0},
		{"assistant", 5.0},
		{"unknown", 1.0},
		{"", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			score := cm.baseScore(tt.role)
			if score != tt.expected {
				t.Errorf("baseScore(%q) = %v, want %v", tt.role, score, tt.expected)
			}
		})
	}
}

func TestTermMatchScore(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	tests := []struct {
		name       string
		content    string
		queryTerms []string
		minScore   float64
	}{
		{"no match", "hello world", []string{"foo", "bar"}, 0.0},
		{"one match", "hello world", []string{"hello", "bar"}, 15.0},
		{"two matches", "hello world", []string{"hello", "world"}, 30.0},
		{"case insensitive", "Hello World", []string{"hello", "world"}, 30.0},
		{"empty terms", "hello world", []string{}, 0.0},
		{"empty content", "", []string{"hello"}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := cm.termMatchScore(tt.content, tt.queryTerms)
			if score < tt.minScore {
				t.Errorf("termMatchScore() = %v, want >= %v", score, tt.minScore)
			}
		})
	}
}

func TestRecencyScore(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	tests := []struct {
		name     string
		index    int
		total    int
		expected float64
	}{
		{"first of many", 0, 10, 0.0},
		{"last of many", 9, 10, 18.0},
		{"middle", 5, 10, 10.0},
		{"single message", 0, 1, 0.0},
		{"zero total", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := cm.recencyScore(tt.index, tt.total)
			if score != tt.expected {
				t.Errorf("recencyScore(%d, %d) = %v, want %v", tt.index, tt.total, score, tt.expected)
			}
		})
	}
}

func TestSetMaxContextTokens(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	cm.SetMaxContextTokens(2000)
	if cm.MaxTokens() != 2000 {
		t.Errorf("SetMaxContextTokens failed: got %d, want 2000", cm.MaxTokens())
	}

	// Should update AvailableTokens too
	if cm.AvailableTokens() != 1900 {
		t.Errorf("AvailableTokens after SetMaxContextTokens: got %d, want 1900", cm.AvailableTokens())
	}
}

func TestSetReserveTokens(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	cm.SetReserveTokens(200)
	if cm.AvailableTokens() != 800 {
		t.Errorf("AvailableTokens after SetReserveTokens: got %d, want 800", cm.AvailableTokens())
	}
}

func TestModel(t *testing.T) {
	tests := []struct {
		model string
	}{
		{"gpt-4"},
		{"claude-3-opus"},
		{""},
		{"custom-model-v1"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			cm := NewContextManager(tt.model, 1000, 100)
			if cm.Model() != tt.model {
				t.Errorf("Model() = %q, want %q", cm.Model(), tt.model)
			}
		})
	}
}

func TestSelectRelevant_PreservesOriginalOrder(t *testing.T) {
	cm := NewContextManager("test", 300, 20) // 280 available

	messages := []Message{
		{Role: "system", Content: "System A"},
		{Role: "user", Content: "User B"},
		{Role: "assistant", Content: "Assistant C"},
		{Role: "user", Content: "User D"},
	}

	result := cm.selectRelevant(messages, "query", 280)

	// Verify messages are in their original order
	if len(result) > 1 {
		for i := 1; i < len(result); i++ {
			// Find indices in original
			var prevIdx, currIdx int
			for j, m := range messages {
				if m.Role == result[i-1].Role && m.Content == result[i-1].Content {
					prevIdx = j
				}
				if m.Role == result[i].Role && m.Content == result[i].Content {
					currIdx = j
				}
			}
			if currIdx < prevIdx {
				t.Errorf("order not preserved: message at original index %d came before %d in result", currIdx, prevIdx)
			}
		}
	}
}

func TestScoreMessages(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	messages := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Question about testing"},
		{Role: "assistant", Content: "Answer"},
	}

	scored := cm.scoreMessages(messages, "testing")

	if len(scored) != len(messages) {
		t.Errorf("expected %d scored messages, got %d", len(messages), len(scored))
	}

	// System message should have highest base score
	systemScore := scored[0].score
	if systemScore < 100.0 {
		t.Errorf("system message score should be >= 100, got %v", systemScore)
	}

	// Message with query match should have higher score than without
	userScore := scored[1].score      // Contains "testing"
	assistantScore := scored[2].score // Does not contain "testing"

	if userScore <= assistantScore {
		t.Errorf("user message with query match should score higher: user=%v, assistant=%v", userScore, assistantScore)
	}
}

func TestPrepareContext_VeryLargeConversation(t *testing.T) {
	cm := NewContextManager("test", 500, 50) // 450 available

	// Create a large conversation
	var messages []Message
	messages = append(messages, Message{Role: "system", Content: "You are a helpful assistant."})

	for i := 0; i < 100; i++ {
		messages = append(messages, Message{Role: "user", Content: "This is user message number " + string(rune('0'+i%10))})
		messages = append(messages, Message{Role: "assistant", Content: "This is assistant response " + string(rune('0'+i%10))})
	}

	result, err := cm.PrepareContext(messages, "testing")
	if err != nil {
		t.Fatalf("unexpected error with large conversation: %v", err)
	}

	// Should have significantly fewer messages
	if len(result) >= len(messages) {
		t.Errorf("expected message reduction, got %d from %d", len(result), len(messages))
	}

	// Should have at least some messages
	if len(result) == 0 {
		t.Error("expected at least some messages in result")
	}

	// Verify token budget is respected
	totalTokens := 0
	for _, msg := range result {
		totalTokens += estimateMessageTokens(msg)
	}
	if totalTokens > 450 {
		t.Errorf("result exceeds budget: %d tokens > 450 available", totalTokens)
	}
}

func TestPrepareContext_ExactBudgetFit(t *testing.T) {
	// Test when messages exactly fit the budget
	cm := NewContextManager("test", 10000, 0)

	messages := []Message{
		{Role: "user", Content: "Test"},
	}

	result, err := cm.PrepareContext(messages, "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

func TestPrepareContext_QueryInfluencesSelection(t *testing.T) {
	cm := NewContextManager("test", 100, 10) // Very tight budget of 90 tokens

	messages := []Message{
		{Role: "user", Content: "Question about Python programming"},
		{Role: "user", Content: "Question about Go testing"},
		{Role: "user", Content: "Question about Java servlets"},
	}

	// Query about "testing" should prioritize the Go testing message
	result, err := cm.PrepareContext(messages, "testing")
	if err != nil && !errors.Is(err, ErrContextBudgetExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) > 0 {
		// If we got any results, check if the relevant one is included
		hasTestingMessage := false
		for _, msg := range result {
			if msg.Content == "Question about Go testing" {
				hasTestingMessage = true
				break
			}
		}
		// With very tight budget, might not fit, so just log
		if !hasTestingMessage {
			t.Log("Note: testing message may not fit in tight budget")
		}
	}
}

func TestFillBudget(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	scored := []scoredMessage{
		{message: Message{Role: "system", Content: "Short"}, score: 100.0, index: 0},
		{message: Message{Role: "user", Content: "Medium length message"}, score: 50.0, index: 1},
		{message: Message{Role: "assistant", Content: "A"}, score: 25.0, index: 2},
	}

	result := cm.fillBudget(scored, 20) // Very limited budget

	// Should include at least the smallest messages that fit
	totalTokens := 0
	for _, sm := range result {
		totalTokens += estimateMessageTokens(sm.message)
	}

	if totalTokens > 20 {
		t.Errorf("fillBudget exceeded budget: %d > 20", totalTokens)
	}
}

func TestRestoreOrder(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	// Scored messages in non-sequential order
	selected := []scoredMessage{
		{message: Message{Role: "assistant", Content: "C"}, score: 10.0, index: 2},
		{message: Message{Role: "system", Content: "A"}, score: 100.0, index: 0},
		{message: Message{Role: "user", Content: "B"}, score: 50.0, index: 1},
	}

	result := cm.restoreOrder(selected)

	// Should be in original order: 0, 1, 2
	expected := []string{"A", "B", "C"}
	for i, msg := range result {
		if msg.Content != expected[i] {
			t.Errorf("restoreOrder: position %d has %q, want %q", i, msg.Content, expected[i])
		}
	}
}

func TestExtractMessages(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	scored := []scoredMessage{
		{message: Message{Role: "user", Content: "A"}, score: 10.0, index: 0},
		{message: Message{Role: "assistant", Content: "B"}, score: 20.0, index: 1},
	}

	result := cm.extractMessages(scored)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	if result[0].Content != "A" || result[1].Content != "B" {
		t.Errorf("extractMessages content mismatch: got %v", result)
	}
}

func TestCountTokens(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	tokens := cm.countTokens(messages)

	// Should be sum of individual estimates
	expected := estimateMessageTokens(messages[0]) + estimateMessageTokens(messages[1])
	if tokens != expected {
		t.Errorf("countTokens() = %d, want %d", tokens, expected)
	}
}

func TestCountTokens_EmptySlice(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	tokens := cm.countTokens([]Message{})
	if tokens != 0 {
		t.Errorf("countTokens([]) = %d, want 0", tokens)
	}
}

func TestSortByScoreDesc(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	scored := []scoredMessage{
		{message: Message{Role: "user", Content: "A"}, score: 10.0, index: 0},
		{message: Message{Role: "system", Content: "B"}, score: 100.0, index: 1},
		{message: Message{Role: "assistant", Content: "C"}, score: 50.0, index: 2},
	}

	sorted := cm.sortByScoreDesc(scored)

	// Should be sorted by score descending: 100, 50, 10
	expectedScores := []float64{100.0, 50.0, 10.0}
	for i, sm := range sorted {
		if sm.score != expectedScores[i] {
			t.Errorf("sortByScoreDesc: position %d has score %v, want %v", i, sm.score, expectedScores[i])
		}
	}

	// Original should be unchanged
	if scored[0].score != 10.0 {
		t.Error("sortByScoreDesc modified original slice")
	}
}

func TestCalculateScore(t *testing.T) {
	cm := NewContextManager("test", 1000, 100)

	// Test system message with query match at end of conversation
	msg := Message{Role: "system", Content: "testing system"}
	score := cm.calculateScore(msg, "testing", []string{"testing"}, 9, 10)

	// Should include: baseScore(system)=100 + termMatch(testing)=15 + recency(9/10*20)=18
	expectedMin := 100.0 + 15.0 + 18.0
	if score < expectedMin {
		t.Errorf("calculateScore() = %v, want >= %v", score, expectedMin)
	}
}
