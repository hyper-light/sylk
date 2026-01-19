package context

import (
	"strings"
	"testing"
	"time"
)

func TestReferenceType_IsValid(t *testing.T) {
	validTypes := []ReferenceType{
		RefTypeConversation,
		RefTypeResearch,
		RefTypeCodeAnalysis,
		RefTypeToolResults,
		RefTypePlanDiscussion,
	}

	for _, rt := range validTypes {
		if !rt.IsValid() {
			t.Errorf("expected %s to be valid", rt)
		}
	}

	invalidType := ReferenceType("invalid")
	if invalidType.IsValid() {
		t.Error("expected invalid type to be invalid")
	}
}

func TestReferenceType_String(t *testing.T) {
	tests := []struct {
		rt       ReferenceType
		expected string
	}{
		{RefTypeConversation, "conversation"},
		{RefTypeResearch, "research"},
		{RefTypeCodeAnalysis, "code_analysis"},
		{RefTypeToolResults, "tool_results"},
		{RefTypePlanDiscussion, "plan_discussion"},
	}

	for _, tt := range tests {
		if tt.rt.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.rt.String())
		}
	}
}

func TestValidReferenceTypes(t *testing.T) {
	types := ValidReferenceTypes()
	if len(types) != 5 {
		t.Errorf("expected 5 reference types, got %d", len(types))
	}
}

func TestContextReference_Render(t *testing.T) {
	ref := &ContextReference{
		ID:          "ref123",
		Type:        RefTypeConversation,
		ContentIDs:  []string{"id1", "id2"},
		Summary:     "Test summary",
		TokensSaved: 100,
		TurnRange:   [2]int{1, 5},
		Timestamp:   time.Date(2025, 1, 15, 14, 23, 0, 0, time.UTC),
		Topics:      []string{"auth", "jwt", "middleware"},
	}

	rendered := ref.Render()

	if !strings.Contains(rendered, "CTX-REF:conversation") {
		t.Error("rendered should contain type")
	}
	if !strings.Contains(rendered, "5 turns") {
		t.Error("rendered should contain turn count")
	}
	if !strings.Contains(rendered, "100 tokens") {
		t.Error("rendered should contain tokens")
	}
	if !strings.Contains(rendered, "ref123") {
		t.Error("rendered should contain ID")
	}
	if !strings.Contains(rendered, "auth, jwt, middleware") {
		t.Error("rendered should contain topics")
	}
}

func TestContextReference_RenderCompact(t *testing.T) {
	ref := &ContextReference{
		Type:        RefTypeResearch,
		TokensSaved: 500,
		TurnRange:   [2]int{1, 10},
	}

	rendered := ref.RenderCompact()

	if !strings.Contains(rendered, "REF:research") {
		t.Error("compact render should contain type")
	}
	if !strings.Contains(rendered, "10t") {
		t.Error("compact render should contain turn count")
	}
	if !strings.Contains(rendered, "500tok") {
		t.Error("compact render should contain tokens")
	}
}

func TestContextReference_IsEmpty(t *testing.T) {
	emptyRef := &ContextReference{}
	if !emptyRef.IsEmpty() {
		t.Error("reference with no content IDs should be empty")
	}

	ref := &ContextReference{ContentIDs: []string{"id1"}}
	if ref.IsEmpty() {
		t.Error("reference with content IDs should not be empty")
	}
}

func TestContextReference_ContentIDSet(t *testing.T) {
	ref := &ContextReference{
		ContentIDs: []string{"id1", "id2", "id3"},
	}

	set := ref.ContentIDSet()

	if len(set) != 3 {
		t.Errorf("expected 3 IDs in set, got %d", len(set))
	}

	if !set["id1"] {
		t.Error("set should contain id1")
	}
	if set["id4"] {
		t.Error("set should not contain id4")
	}
}

func TestContextReference_HasTopic(t *testing.T) {
	ref := &ContextReference{
		Topics: []string{"authentication", "JWT tokens", "middleware"},
	}

	if !ref.HasTopic("auth") {
		t.Error("should find 'auth' in 'authentication'")
	}
	if !ref.HasTopic("JWT") {
		t.Error("should find 'JWT' (case insensitive)")
	}
	if ref.HasTopic("database") {
		t.Error("should not find 'database'")
	}
}

func TestContextReference_HasEntity(t *testing.T) {
	ref := &ContextReference{
		Entities: []string{"UserService", "AuthController", "JWTValidator"},
	}

	if !ref.HasEntity("user") {
		t.Error("should find 'user' in 'UserService'")
	}
	if !ref.HasEntity("AUTH") {
		t.Error("should find 'AUTH' (case insensitive)")
	}
	if ref.HasEntity("Database") {
		t.Error("should not find 'Database'")
	}
}

func TestContextReference_MatchesQuery(t *testing.T) {
	ref := &ContextReference{
		Topics:     []string{"authentication", "security"},
		Entities:   []string{"UserService"},
		QueryHints: []string{"login flow", "password reset"},
		Summary:    "Discussion about user authentication",
	}

	// All words match
	score := ref.MatchesQuery("authentication security")
	if score < 0.9 {
		t.Errorf("expected high score for matching query, got %f", score)
	}

	// Partial match
	score = ref.MatchesQuery("authentication database")
	if score < 0.4 || score > 0.6 {
		t.Errorf("expected ~0.5 score for partial match, got %f", score)
	}

	// No match
	score = ref.MatchesQuery("unrelated topic")
	if score > 0.1 {
		t.Errorf("expected low score for non-matching query, got %f", score)
	}

	// Empty query
	score = ref.MatchesQuery("")
	if score != 0 {
		t.Errorf("expected 0 for empty query, got %f", score)
	}
}

func TestContextReference_Clone(t *testing.T) {
	original := &ContextReference{
		ID:          "ref1",
		Type:        RefTypeConversation,
		SessionID:   "session1",
		AgentID:     "agent1",
		ContentIDs:  []string{"id1", "id2"},
		Summary:     "Test summary",
		TokensSaved: 100,
		TurnRange:   [2]int{1, 5},
		Timestamp:   time.Now(),
		Topics:      []string{"topic1", "topic2"},
		Entities:    []string{"entity1"},
		QueryHints:  []string{"hint1"},
	}

	clone := original.Clone()

	// Check values are equal
	if clone.ID != original.ID {
		t.Error("ID mismatch")
	}
	if clone.Type != original.Type {
		t.Error("Type mismatch")
	}
	if clone.Summary != original.Summary {
		t.Error("Summary mismatch")
	}

	// Check slices are independent copies
	clone.ContentIDs[0] = "modified"
	if original.ContentIDs[0] == "modified" {
		t.Error("clone should have independent ContentIDs")
	}

	clone.Topics[0] = "modified"
	if original.Topics[0] == "modified" {
		t.Error("clone should have independent Topics")
	}
}

func TestContextReference_Clone_Nil(t *testing.T) {
	var ref *ContextReference
	clone := ref.Clone()
	if clone != nil {
		t.Error("cloning nil should return nil")
	}
}

func TestContextReference_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     *ContextReference
		wantErr bool
	}{
		{
			name: "valid reference",
			ref: &ContextReference{
				ID:         "ref1",
				Type:       RefTypeConversation,
				ContentIDs: []string{"id1"},
				Summary:    "Test summary",
			},
			wantErr: false,
		},
		{
			name: "empty ID",
			ref: &ContextReference{
				ID:         "",
				Type:       RefTypeConversation,
				ContentIDs: []string{"id1"},
				Summary:    "Test summary",
			},
			wantErr: true,
		},
		{
			name: "invalid type",
			ref: &ContextReference{
				ID:         "ref1",
				Type:       ReferenceType("invalid"),
				ContentIDs: []string{"id1"},
				Summary:    "Test summary",
			},
			wantErr: true,
		},
		{
			name: "empty content IDs",
			ref: &ContextReference{
				ID:         "ref1",
				Type:       RefTypeConversation,
				ContentIDs: []string{},
				Summary:    "Test summary",
			},
			wantErr: true,
		},
		{
			name: "empty summary",
			ref: &ContextReference{
				ID:         "ref1",
				Type:       RefTypeConversation,
				ContentIDs: []string{"id1"},
				Summary:    "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestContextReference_ToContentEntry(t *testing.T) {
	ref := &ContextReference{
		ID:          "ref1",
		Type:        RefTypeConversation,
		SessionID:   "session1",
		AgentID:     "agent1",
		Summary:     "Test summary",
		TokensSaved: 100,
		TurnRange:   [2]int{5, 10},
		Timestamp:   time.Now(),
		Topics:      []string{"topic1", "topic2"},
		Entities:    []string{"entity1"},
	}

	entry := ref.ToContentEntry()

	if entry.ID != ref.ID {
		t.Error("ID mismatch")
	}
	if entry.SessionID != ref.SessionID {
		t.Error("SessionID mismatch")
	}
	if entry.AgentID != ref.AgentID {
		t.Error("AgentID mismatch")
	}
	if entry.ContentType != ContentTypeContextRef {
		t.Errorf("expected ContentTypeContextRef, got %s", entry.ContentType)
	}
	if entry.TurnNumber != ref.TurnRange[0] {
		t.Error("TurnNumber should be start of turn range")
	}
	if len(entry.Keywords) != len(ref.Topics) {
		t.Error("Keywords should match Topics")
	}
	if len(entry.Entities) != len(ref.Entities) {
		t.Error("Entities should match")
	}
}

func TestContextReference_formatTopics(t *testing.T) {
	tests := []struct {
		name     string
		topics   []string
		expected string
	}{
		{
			name:     "no topics",
			topics:   []string{},
			expected: "none",
		},
		{
			name:     "one topic",
			topics:   []string{"auth"},
			expected: "auth",
		},
		{
			name:     "three topics",
			topics:   []string{"auth", "jwt", "middleware"},
			expected: "auth, jwt, middleware",
		},
		{
			name:     "more than three topics",
			topics:   []string{"auth", "jwt", "middleware", "security", "oauth"},
			expected: "auth, jwt, middleware",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := &ContextReference{Topics: tt.topics}
			result := ref.formatTopics()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestContextReference_turnCount(t *testing.T) {
	tests := []struct {
		turnRange [2]int
		expected  int
	}{
		{[2]int{1, 1}, 1},
		{[2]int{1, 5}, 5},
		{[2]int{5, 10}, 6},
		{[2]int{0, 0}, 1},
	}

	for _, tt := range tests {
		ref := &ContextReference{TurnRange: tt.turnRange}
		if ref.turnCount() != tt.expected {
			t.Errorf("turnRange %v: expected %d, got %d", tt.turnRange, tt.expected, ref.turnCount())
		}
	}
}
