package context

import (
	"testing"
	"time"
)

func TestNewReferenceGenerator(t *testing.T) {
	gen := NewReferenceGenerator(nil)
	if gen == nil {
		t.Fatal("expected non-nil generator")
	}
	if gen.contentStore != nil {
		t.Error("expected nil content store")
	}
}

func TestReferenceGenerator_GenerateReference_MultipleEntries(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:          "entry1",
			ContentType: ContentTypeUserPrompt,
			TokenCount:  100,
			TurnNumber:  1,
			Timestamp:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
			Keywords:    []string{"auth", "login"},
			Entities:    []string{"UserService", "AuthController"},
		},
		{
			ID:          "entry2",
			ContentType: ContentTypeAssistantReply,
			TokenCount:  200,
			TurnNumber:  2,
			Timestamp:   time.Date(2025, 1, 15, 10, 5, 0, 0, time.UTC),
			Keywords:    []string{"auth", "jwt"},
			Entities:    []string{"JWTValidator", "AuthController"},
		},
		{
			ID:          "entry3",
			ContentType: ContentTypeUserPrompt,
			TokenCount:  50,
			TurnNumber:  3,
			Timestamp:   time.Date(2025, 1, 15, 10, 10, 0, 0, time.UTC),
			Keywords:    []string{"security", "auth"},
			Entities:    []string{"SecurityMiddleware"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify content IDs
	if len(ref.ContentIDs) != 3 {
		t.Errorf("expected 3 content IDs, got %d", len(ref.ContentIDs))
	}
	if ref.ContentIDs[0] != "entry1" {
		t.Errorf("expected first content ID to be entry1, got %s", ref.ContentIDs[0])
	}

	// Verify tokens saved
	if ref.TokensSaved != 350 {
		t.Errorf("expected 350 tokens saved, got %d", ref.TokensSaved)
	}

	// Verify turn range
	if ref.TurnRange[0] != 1 || ref.TurnRange[1] != 3 {
		t.Errorf("expected turn range [1, 3], got %v", ref.TurnRange)
	}

	// Verify topics (auth should be top due to frequency)
	if len(ref.Topics) == 0 {
		t.Error("expected at least one topic")
	}
	if ref.Topics[0] != "auth" {
		t.Errorf("expected first topic to be 'auth', got %s", ref.Topics[0])
	}

	// Verify entities
	if len(ref.Entities) == 0 {
		t.Error("expected at least one entity")
	}

	// Verify type (should be conversation as user_prompt is dominant)
	if ref.Type != RefTypeConversation {
		t.Errorf("expected RefTypeConversation, got %s", ref.Type)
	}

	// Verify summary
	if ref.Summary == "" {
		t.Error("expected non-empty summary")
	}

	// Verify ID is set
	if ref.ID == "" {
		t.Error("expected non-empty ID")
	}

	// Verify timestamp is earliest
	expected := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	if !ref.Timestamp.Equal(expected) {
		t.Errorf("expected timestamp %v, got %v", expected, ref.Timestamp)
	}
}

func TestReferenceGenerator_GenerateReference_EmptyEntries(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	ref, err := gen.GenerateReference([]*ContentEntry{})
	if err != ErrNoEntries {
		t.Errorf("expected ErrNoEntries, got %v", err)
	}
	if ref != nil {
		t.Error("expected nil reference")
	}
}

func TestReferenceGenerator_GenerateReference_NilEntries(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	ref, err := gen.GenerateReference(nil)
	if err != ErrNoEntries {
		t.Errorf("expected ErrNoEntries, got %v", err)
	}
	if ref != nil {
		t.Error("expected nil reference")
	}
}

func TestReferenceGenerator_GenerateReference_SingleEntry(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:          "single",
			ContentType: ContentTypeCodeFile,
			TokenCount:  500,
			TurnNumber:  5,
			Timestamp:   time.Now(),
			Keywords:    []string{"function", "handler"},
			Entities:    []string{"HTTPHandler"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ref.ContentIDs) != 1 {
		t.Errorf("expected 1 content ID, got %d", len(ref.ContentIDs))
	}
	if ref.TokensSaved != 500 {
		t.Errorf("expected 500 tokens saved, got %d", ref.TokensSaved)
	}
	if ref.TurnRange[0] != 5 || ref.TurnRange[1] != 5 {
		t.Errorf("expected turn range [5, 5], got %v", ref.TurnRange)
	}
	if ref.Type != RefTypeCodeAnalysis {
		t.Errorf("expected RefTypeCodeAnalysis, got %s", ref.Type)
	}
}

func TestReferenceGenerator_GenerateReference_MixedContentTypes(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	tests := []struct {
		name         string
		entries      []*ContentEntry
		expectedType ReferenceType
	}{
		{
			name: "research dominant",
			entries: []*ContentEntry{
				{ID: "1", ContentType: ContentTypeResearchPaper, TurnNumber: 1, Timestamp: time.Now()},
				{ID: "2", ContentType: ContentTypeResearchPaper, TurnNumber: 2, Timestamp: time.Now()},
				{ID: "3", ContentType: ContentTypeUserPrompt, TurnNumber: 3, Timestamp: time.Now()},
			},
			expectedType: RefTypeResearch,
		},
		{
			name: "tool results dominant",
			entries: []*ContentEntry{
				{ID: "1", ContentType: ContentTypeToolCall, TurnNumber: 1, Timestamp: time.Now()},
				{ID: "2", ContentType: ContentTypeToolResult, TurnNumber: 2, Timestamp: time.Now()},
				{ID: "3", ContentType: ContentTypeToolResult, TurnNumber: 3, Timestamp: time.Now()},
			},
			expectedType: RefTypeToolResults,
		},
		{
			name: "plan discussion dominant",
			entries: []*ContentEntry{
				{ID: "1", ContentType: ContentTypePlanWorkflow, TurnNumber: 1, Timestamp: time.Now()},
				{ID: "2", ContentType: ContentTypePlanWorkflow, TurnNumber: 2, Timestamp: time.Now()},
			},
			expectedType: RefTypePlanDiscussion,
		},
		{
			name: "web fetch maps to research",
			entries: []*ContentEntry{
				{ID: "1", ContentType: ContentTypeWebFetch, TurnNumber: 1, Timestamp: time.Now()},
				{ID: "2", ContentType: ContentTypeWebFetch, TurnNumber: 2, Timestamp: time.Now()},
			},
			expectedType: RefTypeResearch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := gen.GenerateReference(tt.entries)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.Type != tt.expectedType {
				t.Errorf("expected %s, got %s", tt.expectedType, ref.Type)
			}
		})
	}
}

func TestReferenceGenerator_TopicExtraction(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Keywords:   []string{"auth", "login", "security"},
		},
		{
			ID:         "2",
			TurnNumber: 2,
			Timestamp:  time.Now(),
			Keywords:   []string{"AUTH", "Login", "database"}, // Different case
		},
		{
			ID:         "3",
			TurnNumber: 3,
			Timestamp:  time.Now(),
			Keywords:   []string{"auth", "api"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "auth" appears 3 times (case-insensitive), should be first
	if len(ref.Topics) == 0 {
		t.Fatal("expected at least one topic")
	}
	if ref.Topics[0] != "auth" {
		t.Errorf("expected 'auth' as top topic, got %s", ref.Topics[0])
	}

	// "login" appears 2 times, should be high
	found := false
	for _, topic := range ref.Topics {
		if topic == "login" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'login' in topics")
	}
}

func TestReferenceGenerator_EntityExtraction(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Entities:   []string{"UserService", "AuthController"},
		},
		{
			ID:         "2",
			TurnNumber: 2,
			Timestamp:  time.Now(),
			Entities:   []string{"UserService", "DatabaseHandler"},
		},
		{
			ID:         "3",
			TurnNumber: 3,
			Timestamp:  time.Now(),
			Entities:   []string{"UserService", "AuthController"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "UserService" appears 3 times, should be first
	if len(ref.Entities) == 0 {
		t.Fatal("expected at least one entity")
	}
	if ref.Entities[0] != "UserService" {
		t.Errorf("expected 'UserService' as top entity, got %s", ref.Entities[0])
	}
}

func TestReferenceGenerator_QueryHints(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Keywords:   []string{"auth", "security"},
			Entities:   []string{"UserService", "AuthController"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have hints for topics and entities
	if len(ref.QueryHints) == 0 {
		t.Fatal("expected at least one query hint")
	}

	foundTopicHint := false
	foundEntityHint := false
	for _, hint := range ref.QueryHints {
		if hint == "discussion about auth" || hint == "discussion about security" {
			foundTopicHint = true
		}
		if hint == "mentions of UserService" || hint == "mentions of AuthController" {
			foundEntityHint = true
		}
	}

	if !foundTopicHint {
		t.Error("expected topic-based hint")
	}
	if !foundEntityHint {
		t.Error("expected entity-based hint")
	}
}

func TestReferenceGenerator_Summary_WithTopics(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Keywords:   []string{"authentication", "jwt", "middleware"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if ref.Summary[:10] != "Discussion" {
		t.Errorf("expected summary to start with 'Discussion', got %s", ref.Summary)
	}
}

func TestReferenceGenerator_Summary_NoTopics(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1, Timestamp: time.Now()},
		{ID: "2", TurnNumber: 2, Timestamp: time.Now()},
		{ID: "3", TurnNumber: 3, Timestamp: time.Now()},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.Summary != "3 turns of conversation" {
		t.Errorf("expected '3 turns of conversation', got %s", ref.Summary)
	}
}

func TestReferenceGenerator_TurnRangeBounds(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	// Entries with non-sequential turn numbers
	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 5, Timestamp: time.Now()},
		{ID: "2", TurnNumber: 2, Timestamp: time.Now()},
		{ID: "3", TurnNumber: 10, Timestamp: time.Now()},
		{ID: "4", TurnNumber: 1, Timestamp: time.Now()},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.TurnRange[0] != 1 {
		t.Errorf("expected min turn 1, got %d", ref.TurnRange[0])
	}
	if ref.TurnRange[1] != 10 {
		t.Errorf("expected max turn 10, got %d", ref.TurnRange[1])
	}
}

func TestReferenceGenerator_TimestampSelection(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	earliest := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	middle := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	latest := time.Date(2025, 1, 20, 0, 0, 0, 0, time.UTC)

	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1, Timestamp: middle},
		{ID: "2", TurnNumber: 2, Timestamp: latest},
		{ID: "3", TurnNumber: 3, Timestamp: earliest},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ref.Timestamp.Equal(earliest) {
		t.Errorf("expected earliest timestamp %v, got %v", earliest, ref.Timestamp)
	}
}

func TestReferenceGenerator_UniqueID(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1, Timestamp: time.Now()},
	}

	ref1, _ := gen.GenerateReference(entries)
	ref2, _ := gen.GenerateReference(entries)

	if ref1.ID == ref2.ID {
		t.Error("expected unique IDs for different references")
	}
}

func TestReferenceGenerator_TopicsLimit(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Keywords:   []string{"a", "b", "c", "d", "e", "f", "g", "h"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be limited to 5 topics max
	if len(ref.Topics) > 5 {
		t.Errorf("expected at most 5 topics, got %d", len(ref.Topics))
	}
}

func TestReferenceGenerator_EntitiesLimit(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{
			ID:         "1",
			TurnNumber: 1,
			Timestamp:  time.Now(),
			Entities:   []string{"A", "B", "C", "D", "E", "F", "G", "H"},
		},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be limited to 5 entities max
	if len(ref.Entities) > 5 {
		t.Errorf("expected at most 5 entities, got %d", len(ref.Entities))
	}
}

func TestReferenceGenerator_EmptyKeywordsAndEntities(t *testing.T) {
	gen := NewReferenceGenerator(nil)

	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1, Timestamp: time.Now()},
		{ID: "2", TurnNumber: 2, Timestamp: time.Now()},
	}

	ref, err := gen.GenerateReference(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.Topics == nil {
		t.Error("expected non-nil topics slice")
	}
	if ref.Entities == nil {
		t.Error("expected non-nil entities slice")
	}
	if ref.QueryHints == nil {
		t.Error("expected non-nil query hints slice")
	}
}

func TestMinInt(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-1, 1, -1},
	}

	for _, tt := range tests {
		result := minInt(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("minInt(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestUpdateTurnBounds(t *testing.T) {
	tests := []struct {
		minTurn, maxTurn, turn   int
		expectedMin, expectedMax int
	}{
		{5, 10, 3, 3, 10},  // New min
		{5, 10, 15, 5, 15}, // New max
		{5, 10, 7, 5, 10},  // Within bounds
		{5, 10, 5, 5, 10},  // Equal to min
		{5, 10, 10, 5, 10}, // Equal to max
	}

	for _, tt := range tests {
		newMin, newMax := updateTurnBounds(tt.minTurn, tt.maxTurn, tt.turn)
		if newMin != tt.expectedMin || newMax != tt.expectedMax {
			t.Errorf("updateTurnBounds(%d, %d, %d) = (%d, %d), expected (%d, %d)",
				tt.minTurn, tt.maxTurn, tt.turn, newMin, newMax, tt.expectedMin, tt.expectedMax)
		}
	}
}
