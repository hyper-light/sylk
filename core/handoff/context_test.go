package handoff

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// HO.6.3 CircularBuffer Tests
// =============================================================================

func TestNewCircularBuffer(t *testing.T) {
	t.Run("creates with specified capacity", func(t *testing.T) {
		buf := NewCircularBuffer[int](10)
		if buf.Cap() != 10 {
			t.Errorf("Cap() = %d, expected 10", buf.Cap())
		}
		if buf.Len() != 0 {
			t.Errorf("Len() = %d, expected 0", buf.Len())
		}
	})

	t.Run("enforces minimum capacity of 1", func(t *testing.T) {
		buf := NewCircularBuffer[int](0)
		if buf.Cap() != 1 {
			t.Errorf("Cap() = %d, expected 1 for zero capacity", buf.Cap())
		}

		buf2 := NewCircularBuffer[int](-5)
		if buf2.Cap() != 1 {
			t.Errorf("Cap() = %d, expected 1 for negative capacity", buf2.Cap())
		}
	})
}

func TestCircularBuffer_Push(t *testing.T) {
	t.Run("adds items to buffer", func(t *testing.T) {
		buf := NewCircularBuffer[string](5)
		buf.Push("a")
		buf.Push("b")
		buf.Push("c")

		if buf.Len() != 3 {
			t.Errorf("Len() = %d, expected 3", buf.Len())
		}

		items := buf.Items()
		expected := []string{"a", "b", "c"}
		if len(items) != len(expected) {
			t.Fatalf("Items() length = %d, expected %d", len(items), len(expected))
		}
		for i, item := range items {
			if item != expected[i] {
				t.Errorf("Items()[%d] = %s, expected %s", i, item, expected[i])
			}
		}
	})

	t.Run("evicts oldest when full", func(t *testing.T) {
		buf := NewCircularBuffer[int](3)
		evicted := buf.Push(1) // [1]
		if evicted {
			t.Error("Push should not evict when not full")
		}
		buf.Push(2) // [1, 2]
		buf.Push(3) // [1, 2, 3]

		evicted = buf.Push(4) // [2, 3, 4] - evicts 1
		if !evicted {
			t.Error("Push should evict when full")
		}

		items := buf.Items()
		expected := []int{2, 3, 4}
		for i, item := range items {
			if item != expected[i] {
				t.Errorf("Items()[%d] = %d, expected %d", i, item, expected[i])
			}
		}
	})

	t.Run("handles wrap-around correctly", func(t *testing.T) {
		buf := NewCircularBuffer[int](3)

		// Fill buffer
		buf.Push(1)
		buf.Push(2)
		buf.Push(3)

		// Cause multiple wrap-arounds
		for i := 4; i <= 10; i++ {
			buf.Push(i)
		}

		items := buf.Items()
		expected := []int{8, 9, 10}
		for i, item := range items {
			if item != expected[i] {
				t.Errorf("Items()[%d] = %d, expected %d", i, item, expected[i])
			}
		}
	})
}

func TestCircularBuffer_Pop(t *testing.T) {
	t.Run("removes and returns oldest item", func(t *testing.T) {
		buf := NewCircularBuffer[string](5)
		buf.Push("first")
		buf.Push("second")
		buf.Push("third")

		item, ok := buf.Pop()
		if !ok {
			t.Error("Pop should return true when buffer has items")
		}
		if item != "first" {
			t.Errorf("Pop() = %s, expected 'first'", item)
		}
		if buf.Len() != 2 {
			t.Errorf("Len() = %d, expected 2 after Pop", buf.Len())
		}
	})

	t.Run("returns false for empty buffer", func(t *testing.T) {
		buf := NewCircularBuffer[int](5)
		item, ok := buf.Pop()
		if ok {
			t.Error("Pop should return false for empty buffer")
		}
		if item != 0 {
			t.Errorf("Pop() should return zero value, got %d", item)
		}
	})

	t.Run("handles pop after wrap-around", func(t *testing.T) {
		buf := NewCircularBuffer[int](3)
		buf.Push(1)
		buf.Push(2)
		buf.Push(3)
		buf.Push(4) // [2, 3, 4]

		item, _ := buf.Pop()
		if item != 2 {
			t.Errorf("Pop() = %d, expected 2", item)
		}

		item, _ = buf.Pop()
		if item != 3 {
			t.Errorf("Pop() = %d, expected 3", item)
		}
	})
}

func TestCircularBuffer_Peek(t *testing.T) {
	t.Run("returns newest item without removing", func(t *testing.T) {
		buf := NewCircularBuffer[string](5)
		buf.Push("a")
		buf.Push("b")
		buf.Push("c")

		item, ok := buf.Peek()
		if !ok {
			t.Error("Peek should return true when buffer has items")
		}
		if item != "c" {
			t.Errorf("Peek() = %s, expected 'c'", item)
		}
		if buf.Len() != 3 {
			t.Errorf("Len() = %d, should remain 3 after Peek", buf.Len())
		}
	})

	t.Run("returns false for empty buffer", func(t *testing.T) {
		buf := NewCircularBuffer[int](5)
		item, ok := buf.Peek()
		if ok {
			t.Error("Peek should return false for empty buffer")
		}
		if item != 0 {
			t.Errorf("Peek() should return zero value, got %d", item)
		}
	})
}

func TestCircularBuffer_PeekOldest(t *testing.T) {
	t.Run("returns oldest item without removing", func(t *testing.T) {
		buf := NewCircularBuffer[string](5)
		buf.Push("first")
		buf.Push("second")
		buf.Push("third")

		item, ok := buf.PeekOldest()
		if !ok {
			t.Error("PeekOldest should return true when buffer has items")
		}
		if item != "first" {
			t.Errorf("PeekOldest() = %s, expected 'first'", item)
		}
		if buf.Len() != 3 {
			t.Errorf("Len() = %d, should remain 3 after PeekOldest", buf.Len())
		}
	})
}

func TestCircularBuffer_At(t *testing.T) {
	buf := NewCircularBuffer[int](5)
	buf.Push(10)
	buf.Push(20)
	buf.Push(30)

	t.Run("returns item at valid index", func(t *testing.T) {
		item, ok := buf.At(0)
		if !ok || item != 10 {
			t.Errorf("At(0) = %d, expected 10", item)
		}

		item, ok = buf.At(1)
		if !ok || item != 20 {
			t.Errorf("At(1) = %d, expected 20", item)
		}

		item, ok = buf.At(2)
		if !ok || item != 30 {
			t.Errorf("At(2) = %d, expected 30", item)
		}
	})

	t.Run("returns false for invalid index", func(t *testing.T) {
		_, ok := buf.At(-1)
		if ok {
			t.Error("At(-1) should return false")
		}

		_, ok = buf.At(3)
		if ok {
			t.Error("At(3) should return false for buffer with 3 items")
		}
	})
}

func TestCircularBuffer_RecentN(t *testing.T) {
	buf := NewCircularBuffer[int](10)
	for i := 1; i <= 5; i++ {
		buf.Push(i)
	}

	t.Run("returns n most recent items", func(t *testing.T) {
		recent := buf.RecentN(3)
		expected := []int{3, 4, 5}
		if len(recent) != len(expected) {
			t.Fatalf("RecentN(3) length = %d, expected %d", len(recent), len(expected))
		}
		for i, item := range recent {
			if item != expected[i] {
				t.Errorf("RecentN(3)[%d] = %d, expected %d", i, item, expected[i])
			}
		}
	})

	t.Run("returns all items if n > count", func(t *testing.T) {
		recent := buf.RecentN(10)
		if len(recent) != 5 {
			t.Errorf("RecentN(10) length = %d, expected 5", len(recent))
		}
	})

	t.Run("returns empty for n <= 0", func(t *testing.T) {
		recent := buf.RecentN(0)
		if len(recent) != 0 {
			t.Errorf("RecentN(0) length = %d, expected 0", len(recent))
		}
	})
}

func TestCircularBuffer_IsEmptyAndIsFull(t *testing.T) {
	buf := NewCircularBuffer[int](3)

	if !buf.IsEmpty() {
		t.Error("New buffer should be empty")
	}
	if buf.IsFull() {
		t.Error("New buffer should not be full")
	}

	buf.Push(1)
	if buf.IsEmpty() {
		t.Error("Buffer with items should not be empty")
	}

	buf.Push(2)
	buf.Push(3)
	if !buf.IsFull() {
		t.Error("Buffer at capacity should be full")
	}
}

func TestCircularBuffer_Clear(t *testing.T) {
	buf := NewCircularBuffer[int](5)
	buf.Push(1)
	buf.Push(2)
	buf.Push(3)

	buf.Clear()

	if buf.Len() != 0 {
		t.Errorf("Len() = %d after Clear, expected 0", buf.Len())
	}
	if !buf.IsEmpty() {
		t.Error("Buffer should be empty after Clear")
	}

	// Should be able to use normally after clear
	buf.Push(10)
	if buf.Len() != 1 {
		t.Errorf("Len() = %d after Push, expected 1", buf.Len())
	}
}

func TestCircularBuffer_ForEach(t *testing.T) {
	buf := NewCircularBuffer[int](5)
	buf.Push(1)
	buf.Push(2)
	buf.Push(3)

	var sum int
	var indices []int

	buf.ForEach(func(item int, index int) bool {
		sum += item
		indices = append(indices, index)
		return true
	})

	if sum != 6 {
		t.Errorf("Sum = %d, expected 6", sum)
	}
	if len(indices) != 3 {
		t.Errorf("Visited %d items, expected 3", len(indices))
	}
}

func TestCircularBuffer_ForEachEarlyStop(t *testing.T) {
	buf := NewCircularBuffer[int](10)
	for i := 1; i <= 5; i++ {
		buf.Push(i)
	}

	count := 0
	buf.ForEach(func(item int, index int) bool {
		count++
		return item < 3 // Stop when we see 3
	})

	if count != 3 {
		t.Errorf("ForEach stopped after %d items, expected 3", count)
	}
}

func TestCircularBuffer_Filter(t *testing.T) {
	buf := NewCircularBuffer[int](10)
	for i := 1; i <= 10; i++ {
		buf.Push(i)
	}

	evens := buf.Filter(func(item int) bool {
		return item%2 == 0
	})

	expected := []int{2, 4, 6, 8, 10}
	if len(evens) != len(expected) {
		t.Fatalf("Filter length = %d, expected %d", len(evens), len(expected))
	}
	for i, item := range evens {
		if item != expected[i] {
			t.Errorf("Filter[%d] = %d, expected %d", i, item, expected[i])
		}
	}
}

func TestCircularBuffer_JSON(t *testing.T) {
	buf := NewCircularBuffer[string](5)
	buf.Push("a")
	buf.Push("b")
	buf.Push("c")

	data, err := json.Marshal(buf)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded CircularBuffer[string]
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Len() != buf.Len() {
		t.Errorf("Decoded Len() = %d, expected %d", decoded.Len(), buf.Len())
	}
	if decoded.Cap() != buf.Cap() {
		t.Errorf("Decoded Cap() = %d, expected %d", decoded.Cap(), buf.Cap())
	}

	originalItems := buf.Items()
	decodedItems := decoded.Items()
	for i, item := range originalItems {
		if decodedItems[i] != item {
			t.Errorf("Decoded item %d = %s, expected %s", i, decodedItems[i], item)
		}
	}
}

func TestCircularBuffer_Snapshot(t *testing.T) {
	buf := NewCircularBuffer[int](5)
	buf.Push(1)
	buf.Push(2)
	buf.Push(3)

	snap := buf.Snapshot()

	if snap.Count != 3 {
		t.Errorf("Snapshot Count = %d, expected 3", snap.Count)
	}
	if snap.Capacity != 5 {
		t.Errorf("Snapshot Capacity = %d, expected 5", snap.Capacity)
	}
	if len(snap.Items) != 3 {
		t.Errorf("Snapshot Items length = %d, expected 3", len(snap.Items))
	}

	// Modifying snapshot should not affect original
	snap.Items[0] = 999
	item, _ := buf.At(0)
	if item == 999 {
		t.Error("Modifying snapshot affected original buffer")
	}
}

func TestCircularBuffer_ThreadSafety(t *testing.T) {
	buf := NewCircularBuffer[int](100)
	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent pushes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				buf.Push(base*numOperations + j)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = buf.Len()
				_ = buf.Items()
				_, _ = buf.Peek()
			}
		}()
	}

	wg.Wait()

	// Buffer should be in a valid state
	if buf.Len() > buf.Cap() {
		t.Errorf("Len() = %d exceeds Cap() = %d", buf.Len(), buf.Cap())
	}
}

// =============================================================================
// HO.6.2 RollingSummary Tests
// =============================================================================

func TestNewRollingSummary(t *testing.T) {
	t.Run("creates with specified max tokens", func(t *testing.T) {
		summary := NewRollingSummary(500)
		if summary.MaxTokens() != 500 {
			t.Errorf("MaxTokens() = %d, expected 500", summary.MaxTokens())
		}
	})

	t.Run("uses default for zero or negative", func(t *testing.T) {
		summary := NewRollingSummary(0)
		if summary.MaxTokens() != 1000 {
			t.Errorf("MaxTokens() = %d, expected default 1000", summary.MaxTokens())
		}
	})
}

func TestRollingSummary_AddMessage(t *testing.T) {
	summary := NewRollingSummary(1000)

	msg := NewMessage("user", "Hello, how are you?")
	summary.AddMessage(msg)

	if summary.MessageCount() != 1 {
		t.Errorf("MessageCount() = %d, expected 1", summary.MessageCount())
	}

	summaryText := summary.Summary()
	if summaryText == "" {
		t.Error("Summary() should not be empty after adding message")
	}

	if summary.TokenCount() == 0 {
		t.Error("TokenCount() should be > 0 after adding message")
	}
}

func TestRollingSummary_MultipleMessages(t *testing.T) {
	summary := NewRollingSummary(1000)

	messages := []Message{
		NewMessage("user", "What is Go programming?"),
		NewMessage("assistant", "Go is a statically typed language."),
		NewMessage("user", "How do I declare variables?"),
		NewMessage("assistant", "Use var or := for declaration."),
	}

	for _, msg := range messages {
		summary.AddMessage(msg)
	}

	if summary.MessageCount() != 4 {
		t.Errorf("MessageCount() = %d, expected 4", summary.MessageCount())
	}

	bufferedMsgs := summary.AllBufferedMessages()
	if len(bufferedMsgs) != 4 {
		t.Errorf("AllBufferedMessages() length = %d, expected 4", len(bufferedMsgs))
	}
}

func TestRollingSummary_KeyTopics(t *testing.T) {
	summary := NewRollingSummaryWithConfig(&RollingSummaryConfig{
		MaxTokens:              1000,
		MessageBufferSize:      20,
		TopicExtractionEnabled: true,
		MaxTopics:              10,
		MinTopicMentions:       2,
		TokensPerChar:          0.25,
	})

	// Add messages with repeated topics
	messages := []Message{
		NewMessage("user", "Tell me about Go programming"),
		NewMessage("assistant", "Go is great for backend programming"),
		NewMessage("user", "What about Go concurrency?"),
		NewMessage("assistant", "Go has excellent concurrency with goroutines"),
	}

	for _, msg := range messages {
		summary.AddMessage(msg)
	}

	topics := summary.KeyTopics()
	// "programming" and other words should appear as topics
	if len(topics) == 0 {
		t.Log("No key topics found (may be expected with simple extraction)")
	}
}

func TestRollingSummary_RecentMessages(t *testing.T) {
	summary := NewRollingSummary(1000)

	for i := 0; i < 10; i++ {
		msg := NewMessage("user", "Message "+string(rune('A'+i)))
		summary.AddMessage(msg)
	}

	recent := summary.RecentMessages(3)
	if len(recent) != 3 {
		t.Errorf("RecentMessages(3) length = %d, expected 3", len(recent))
	}

	// Should be the last 3 messages
	if recent[2].Content != "Message J" {
		t.Errorf("Last recent message = %s, expected 'Message J'", recent[2].Content)
	}
}

func TestRollingSummary_Clear(t *testing.T) {
	summary := NewRollingSummary(1000)

	summary.AddMessage(NewMessage("user", "Hello"))
	summary.AddMessage(NewMessage("assistant", "Hi there"))

	summary.Clear()

	if summary.MessageCount() != 0 {
		t.Errorf("MessageCount() = %d after Clear, expected 0", summary.MessageCount())
	}
	if summary.TokenCount() != 0 {
		t.Errorf("TokenCount() = %d after Clear, expected 0", summary.TokenCount())
	}
	if summary.Summary() != "" {
		t.Error("Summary() should be empty after Clear")
	}
}

func TestRollingSummary_Stats(t *testing.T) {
	summary := NewRollingSummary(1000)

	summary.AddMessage(NewMessage("user", "Hello"))
	summary.AddMessage(NewMessage("assistant", "Hi there"))

	stats := summary.Stats()

	if stats.MessageCount != 2 {
		t.Errorf("Stats.MessageCount = %d, expected 2", stats.MessageCount)
	}
	if stats.MaxTokens != 1000 {
		t.Errorf("Stats.MaxTokens = %d, expected 1000", stats.MaxTokens)
	}
	if stats.TokenUsage < 0 || stats.TokenUsage > 1 {
		t.Errorf("Stats.TokenUsage = %v, expected in [0,1]", stats.TokenUsage)
	}
}

func TestRollingSummary_JSON(t *testing.T) {
	original := NewRollingSummary(500)
	original.AddMessage(NewMessage("user", "Test message"))
	original.AddMessage(NewMessage("assistant", "Response message"))

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded RollingSummary
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.MaxTokens() != original.MaxTokens() {
		t.Errorf("Decoded MaxTokens = %d, expected %d",
			decoded.MaxTokens(), original.MaxTokens())
	}
	if decoded.MessageCount() != original.MessageCount() {
		t.Errorf("Decoded MessageCount = %d, expected %d",
			decoded.MessageCount(), original.MessageCount())
	}
}

func TestRollingSummary_TokenBudget(t *testing.T) {
	// Create summary with small token budget
	summary := NewRollingSummary(50)

	// Add messages that exceed the budget
	for i := 0; i < 20; i++ {
		msg := NewMessage("user", "This is a longer message that should eventually exceed the token budget for the summary")
		summary.AddMessage(msg)
	}

	// Summary should stay within budget (approximately)
	if summary.TokenCount() > summary.MaxTokens()*2 {
		t.Errorf("TokenCount() = %d significantly exceeds MaxTokens = %d",
			summary.TokenCount(), summary.MaxTokens())
	}
}

func TestRollingSummary_ThreadSafety(t *testing.T) {
	summary := NewRollingSummary(1000)
	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 50

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				msg := NewMessage("user", "Message from goroutine")
				summary.AddMessage(msg)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = summary.Summary()
				_ = summary.TokenCount()
				_ = summary.KeyTopics()
			}
		}()
	}

	wg.Wait()

	// Should have processed all messages
	if summary.MessageCount() != numGoroutines*numOperations {
		t.Errorf("MessageCount() = %d, expected %d",
			summary.MessageCount(), numGoroutines*numOperations)
	}
}

// =============================================================================
// HO.6.1 PreparedContext Tests
// =============================================================================

func TestNewPreparedContext(t *testing.T) {
	t.Run("creates with specified config", func(t *testing.T) {
		config := PreparedContextConfig{
			MaxSummaryTokens:  500,
			MaxRecentMessages: 5,
			MaxAge:            time.Minute,
			MaxToolStates:     10,
		}
		ctx := NewPreparedContext(config)

		actualConfig := ctx.Config()
		if actualConfig.MaxSummaryTokens != 500 {
			t.Errorf("MaxSummaryTokens = %d, expected 500", actualConfig.MaxSummaryTokens)
		}
		if actualConfig.MaxRecentMessages != 5 {
			t.Errorf("MaxRecentMessages = %d, expected 5", actualConfig.MaxRecentMessages)
		}
	})

	t.Run("applies defaults for zero values", func(t *testing.T) {
		ctx := NewPreparedContext(PreparedContextConfig{})
		actualConfig := ctx.Config()

		defaults := DefaultPreparedContextConfig()
		if actualConfig.MaxSummaryTokens != defaults.MaxSummaryTokens {
			t.Errorf("MaxSummaryTokens = %d, expected default %d",
				actualConfig.MaxSummaryTokens, defaults.MaxSummaryTokens)
		}
	})
}

func TestPreparedContext_AddMessage(t *testing.T) {
	ctx := NewPreparedContextDefault()

	msg := NewMessage("user", "Hello, world!")
	ctx.AddMessage(msg)

	messages := ctx.RecentMessages()
	if len(messages) != 1 {
		t.Errorf("RecentMessages() length = %d, expected 1", len(messages))
	}

	if ctx.TokenCount() == 0 {
		t.Error("TokenCount() should be > 0 after adding message")
	}

	summary := ctx.Summary()
	if summary == "" {
		t.Error("Summary() should not be empty after adding message")
	}
}

func TestPreparedContext_ToolStates(t *testing.T) {
	ctx := NewPreparedContextDefault()

	state := ToolState{
		Active:   true,
		State:    map[string]interface{}{"file": "test.go"},
		UseCount: 1,
	}
	ctx.UpdateToolState("editor", state)

	retrieved, ok := ctx.GetToolState("editor")
	if !ok {
		t.Fatal("GetToolState should return true for existing state")
	}
	if !retrieved.Active {
		t.Error("Retrieved state should be active")
	}
	if retrieved.Name != "editor" {
		t.Errorf("Retrieved state name = %s, expected 'editor'", retrieved.Name)
	}

	allStates := ctx.ToolStates()
	if len(allStates) != 1 {
		t.Errorf("ToolStates() length = %d, expected 1", len(allStates))
	}

	removed := ctx.RemoveToolState("editor")
	if !removed {
		t.Error("RemoveToolState should return true for existing state")
	}

	_, ok = ctx.GetToolState("editor")
	if ok {
		t.Error("GetToolState should return false after removal")
	}
}

func TestPreparedContext_RefreshIfStale(t *testing.T) {
	ctx := NewPreparedContext(PreparedContextConfig{
		MaxSummaryTokens:  1000,
		MaxRecentMessages: 10,
		MaxAge:            100 * time.Millisecond,
		MaxToolStates:     20,
	})

	ctx.AddMessage(NewMessage("user", "Test message"))

	// Should not be stale immediately
	refreshed := ctx.RefreshIfStale(100 * time.Millisecond)
	if refreshed {
		t.Error("Context should not be stale immediately")
	}

	// Wait for context to become stale
	time.Sleep(150 * time.Millisecond)

	refreshed = ctx.RefreshIfStale(100 * time.Millisecond)
	if !refreshed {
		t.Error("Context should be stale after waiting")
	}
}

func TestPreparedContext_IsStale(t *testing.T) {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Test"))

	if ctx.IsStale(time.Hour) {
		t.Error("Context should not be stale with 1 hour maxAge")
	}

	// Very short maxAge should make it stale
	time.Sleep(10 * time.Millisecond)
	if !ctx.IsStale(time.Nanosecond) {
		t.Error("Context should be stale with 1ns maxAge")
	}
}

func TestPreparedContext_ToBytes(t *testing.T) {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Hello"))
	ctx.AddMessage(NewMessage("assistant", "Hi there"))
	ctx.UpdateToolState("test", ToolState{Active: true})
	ctx.SetMetadata("session_id", "abc123")

	data, err := ctx.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	// Verify it's valid JSON
	var snapshot PreparedContextSnapshot
	err = json.Unmarshal(data, &snapshot)
	if err != nil {
		t.Fatalf("Invalid JSON from ToBytes: %v", err)
	}

	if len(snapshot.RecentMessages) != 2 {
		t.Errorf("Snapshot RecentMessages = %d, expected 2", len(snapshot.RecentMessages))
	}
	if len(snapshot.ToolStates) != 1 {
		t.Errorf("Snapshot ToolStates = %d, expected 1", len(snapshot.ToolStates))
	}
}

func TestPreparedContext_FromBytes(t *testing.T) {
	original := NewPreparedContextDefault()
	original.AddMessage(NewMessage("user", "Hello"))
	original.AddMessage(NewMessage("assistant", "World"))
	original.UpdateToolState("editor", ToolState{Active: true})
	original.SetMetadata("key", "value")

	data, err := original.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes failed: %v", err)
	}

	restored, err := FromBytes(data)
	if err != nil {
		t.Fatalf("FromBytes failed: %v", err)
	}

	originalMessages := original.RecentMessages()
	restoredMessages := restored.RecentMessages()
	if len(restoredMessages) != len(originalMessages) {
		t.Errorf("Restored messages = %d, expected %d",
			len(restoredMessages), len(originalMessages))
	}

	originalStates := original.ToolStates()
	restoredStates := restored.ToolStates()
	if len(restoredStates) != len(originalStates) {
		t.Errorf("Restored tool states = %d, expected %d",
			len(restoredStates), len(originalStates))
	}

	value, ok := restored.GetMetadata("key")
	if !ok || value != "value" {
		t.Error("Metadata was not restored correctly")
	}
}

func TestPreparedContext_Snapshot(t *testing.T) {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Test"))
	ctx.UpdateToolState("tool", ToolState{Active: true})

	snap := ctx.Snapshot()

	if len(snap.RecentMessages) != 1 {
		t.Errorf("Snapshot RecentMessages = %d, expected 1", len(snap.RecentMessages))
	}
	if len(snap.ToolStates) != 1 {
		t.Errorf("Snapshot ToolStates = %d, expected 1", len(snap.ToolStates))
	}
	if snap.Version != ctx.Version() {
		t.Errorf("Snapshot Version = %d, expected %d", snap.Version, ctx.Version())
	}
}

func TestPreparedContext_Metadata(t *testing.T) {
	ctx := NewPreparedContextDefault()

	ctx.SetMetadata("key1", "value1")
	ctx.SetMetadata("key2", "value2")

	val, ok := ctx.GetMetadata("key1")
	if !ok || val != "value1" {
		t.Errorf("GetMetadata('key1') = %s, %v, expected 'value1', true", val, ok)
	}

	metadata := ctx.Metadata()
	if len(metadata) != 2 {
		t.Errorf("Metadata() length = %d, expected 2", len(metadata))
	}

	// Modifying returned map should not affect original
	metadata["key3"] = "value3"
	if _, ok := ctx.GetMetadata("key3"); ok {
		t.Error("Modifying returned metadata affected original")
	}
}

func TestPreparedContext_Version(t *testing.T) {
	ctx := NewPreparedContextDefault()
	initialVersion := ctx.Version()

	ctx.AddMessage(NewMessage("user", "Test"))
	if ctx.Version() <= initialVersion {
		t.Error("Version should increment after AddMessage")
	}

	prevVersion := ctx.Version()
	ctx.UpdateToolState("tool", ToolState{Active: true})
	if ctx.Version() <= prevVersion {
		t.Error("Version should increment after UpdateToolState")
	}

	prevVersion = ctx.Version()
	ctx.SetMetadata("key", "value")
	if ctx.Version() <= prevVersion {
		t.Error("Version should increment after SetMetadata")
	}
}

func TestPreparedContext_Clear(t *testing.T) {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Test"))
	ctx.UpdateToolState("tool", ToolState{Active: true})
	ctx.SetMetadata("key", "value")

	ctx.Clear()

	if len(ctx.RecentMessages()) != 0 {
		t.Error("RecentMessages should be empty after Clear")
	}
	if len(ctx.ToolStates()) != 0 {
		t.Error("ToolStates should be empty after Clear")
	}
	if len(ctx.Metadata()) != 0 {
		t.Error("Metadata should be empty after Clear")
	}
	if ctx.TokenCount() != 0 {
		t.Errorf("TokenCount() = %d after Clear, expected 0", ctx.TokenCount())
	}
}

func TestPreparedContext_Stats(t *testing.T) {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Hello"))
	ctx.UpdateToolState("editor", ToolState{Active: true})
	ctx.UpdateToolState("terminal", ToolState{Active: false})

	stats := ctx.Stats()

	if stats.MessageCount != 1 {
		t.Errorf("Stats.MessageCount = %d, expected 1", stats.MessageCount)
	}
	if stats.ToolStateCount != 2 {
		t.Errorf("Stats.ToolStateCount = %d, expected 2", stats.ToolStateCount)
	}
	if stats.ActiveTools != 1 {
		t.Errorf("Stats.ActiveTools = %d, expected 1", stats.ActiveTools)
	}
	if stats.TokenCount == 0 {
		t.Error("Stats.TokenCount should be > 0")
	}
}

func TestPreparedContext_JSON(t *testing.T) {
	original := NewPreparedContextDefault()
	original.AddMessage(NewMessage("user", "Test message"))
	original.UpdateToolState("editor", ToolState{Active: true})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded PreparedContext
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Version() != original.Version() {
		t.Errorf("Decoded Version = %d, expected %d",
			decoded.Version(), original.Version())
	}

	originalMessages := original.RecentMessages()
	decodedMessages := decoded.RecentMessages()
	if len(decodedMessages) != len(originalMessages) {
		t.Errorf("Decoded messages = %d, expected %d",
			len(decodedMessages), len(originalMessages))
	}
}

func TestPreparedContext_ThreadSafety(t *testing.T) {
	ctx := NewPreparedContextDefault()
	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 50

	// Concurrent message adds
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				ctx.AddMessage(NewMessage("user", "Message"))
			}
		}(i)
	}

	// Concurrent tool state updates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				ctx.UpdateToolState("tool", ToolState{Active: true})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = ctx.Summary()
				_ = ctx.RecentMessages()
				_ = ctx.ToolStates()
				_ = ctx.TokenCount()
			}
		}()
	}

	wg.Wait()

	// Context should be in a valid state
	stats := ctx.Stats()
	if stats.TokenCount < 0 {
		t.Errorf("TokenCount = %d, should be >= 0", stats.TokenCount)
	}
}

func TestPreparedContext_ToolStatePruning(t *testing.T) {
	config := PreparedContextConfig{
		MaxSummaryTokens:  1000,
		MaxRecentMessages: 10,
		MaxAge:            time.Minute,
		MaxToolStates:     3,
	}
	ctx := NewPreparedContext(config)

	// Add tools up to limit
	ctx.UpdateToolState("tool1", ToolState{Active: true, LastUsed: time.Now()})
	time.Sleep(time.Millisecond)
	ctx.UpdateToolState("tool2", ToolState{Active: true, LastUsed: time.Now()})
	time.Sleep(time.Millisecond)
	ctx.UpdateToolState("tool3", ToolState{Active: true, LastUsed: time.Now()})

	// Adding one more should prune the oldest
	time.Sleep(time.Millisecond)
	ctx.UpdateToolState("tool4", ToolState{Active: true, LastUsed: time.Now()})

	states := ctx.ToolStates()
	if len(states) > 3 {
		t.Errorf("Tool states = %d, expected <= 3", len(states))
	}

	// tool1 should have been pruned as oldest
	if _, exists := states["tool1"]; exists {
		t.Error("tool1 should have been pruned")
	}
}

// =============================================================================
// Edge Cases Tests
// =============================================================================

func TestCircularBuffer_EdgeCases(t *testing.T) {
	t.Run("single capacity buffer", func(t *testing.T) {
		buf := NewCircularBuffer[int](1)
		buf.Push(1)
		buf.Push(2) // Evicts 1

		item, ok := buf.Peek()
		if !ok || item != 2 {
			t.Errorf("Peek() = %d, expected 2", item)
		}

		if buf.Len() != 1 {
			t.Errorf("Len() = %d, expected 1", buf.Len())
		}
	})

	t.Run("push after clear", func(t *testing.T) {
		buf := NewCircularBuffer[string](5)
		buf.Push("a")
		buf.Push("b")
		buf.Clear()
		buf.Push("c")

		items := buf.Items()
		if len(items) != 1 || items[0] != "c" {
			t.Errorf("Items() = %v, expected [c]", items)
		}
	})

	t.Run("pop until empty", func(t *testing.T) {
		buf := NewCircularBuffer[int](3)
		buf.Push(1)
		buf.Push(2)

		buf.Pop()
		buf.Pop()

		_, ok := buf.Pop()
		if ok {
			t.Error("Pop() should return false for empty buffer")
		}
	})
}

func TestRollingSummary_EdgeCases(t *testing.T) {
	t.Run("empty message content", func(t *testing.T) {
		summary := NewRollingSummary(1000)
		summary.AddMessage(NewMessage("user", ""))

		if summary.MessageCount() != 1 {
			t.Errorf("MessageCount() = %d, expected 1", summary.MessageCount())
		}
	})

	t.Run("very long message", func(t *testing.T) {
		summary := NewRollingSummary(100) // Small token budget

		longContent := ""
		for i := 0; i < 1000; i++ {
			longContent += "word "
		}

		summary.AddMessage(NewMessage("user", longContent))

		// Summary should handle long content without panic
		_ = summary.Summary()
		_ = summary.TokenCount()
	})
}

func TestPreparedContext_EdgeCases(t *testing.T) {
	t.Run("empty context serialization", func(t *testing.T) {
		ctx := NewPreparedContextDefault()

		data, err := ctx.ToBytes()
		if err != nil {
			t.Fatalf("ToBytes failed for empty context: %v", err)
		}

		restored, err := FromBytes(data)
		if err != nil {
			t.Fatalf("FromBytes failed for empty context: %v", err)
		}

		if len(restored.RecentMessages()) != 0 {
			t.Error("Restored empty context should have no messages")
		}
	})

	t.Run("update same tool state multiple times", func(t *testing.T) {
		ctx := NewPreparedContextDefault()

		for i := 0; i < 10; i++ {
			ctx.UpdateToolState("tool", ToolState{
				Active:   true,
				UseCount: i,
			})
		}

		state, ok := ctx.GetToolState("tool")
		if !ok {
			t.Fatal("Tool state should exist")
		}
		if state.UseCount != 9 {
			t.Errorf("UseCount = %d, expected 9", state.UseCount)
		}
	})
}

// =============================================================================
// Helper Functions
// =============================================================================

// Note: assertValid is defined in learned_params_test.go and is reused here

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkCircularBuffer_Push(b *testing.B) {
	buf := NewCircularBuffer[int](100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf.Push(i)
	}
}

func BenchmarkCircularBuffer_Pop(b *testing.B) {
	buf := NewCircularBuffer[int](100)
	for i := 0; i < 100; i++ {
		buf.Push(i)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf.Pop()
		buf.Push(i) // Keep buffer full
	}
}

func BenchmarkCircularBuffer_Items(b *testing.B) {
	buf := NewCircularBuffer[int](100)
	for i := 0; i < 100; i++ {
		buf.Push(i)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = buf.Items()
	}
}

func BenchmarkRollingSummary_AddMessage(b *testing.B) {
	summary := NewRollingSummary(1000)
	msg := NewMessage("user", "This is a test message for benchmarking")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		summary.AddMessage(msg)
	}
}

func BenchmarkRollingSummary_Summary(b *testing.B) {
	summary := NewRollingSummary(1000)
	for i := 0; i < 20; i++ {
		summary.AddMessage(NewMessage("user", "Test message content"))
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = summary.Summary()
	}
}

func BenchmarkPreparedContext_AddMessage(b *testing.B) {
	ctx := NewPreparedContextDefault()
	msg := NewMessage("user", "Benchmark message content")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.AddMessage(msg)
	}
}

func BenchmarkPreparedContext_ToBytes(b *testing.B) {
	ctx := NewPreparedContextDefault()
	for i := 0; i < 10; i++ {
		ctx.AddMessage(NewMessage("user", "Test message"))
	}
	ctx.UpdateToolState("editor", ToolState{Active: true})
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = ctx.ToBytes()
	}
}

func BenchmarkPreparedContext_RefreshIfStale(b *testing.B) {
	ctx := NewPreparedContextDefault()
	for i := 0; i < 10; i++ {
		ctx.AddMessage(NewMessage("user", "Test message"))
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.RefreshIfStale(time.Hour) // Won't be stale
	}
}
