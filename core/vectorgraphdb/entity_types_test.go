package vectorgraphdb

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// EntityType Tests
// =============================================================================

func TestEntityType_String(t *testing.T) {
	tests := []struct {
		entityType EntityType
		expected   string
	}{
		{EntityTypeFunction, "function"},
		{EntityTypeType, "type"},
		{EntityTypeVariable, "variable"},
		{EntityTypeImport, "import"},
		{EntityTypeFile, "file"},
		{EntityTypeModule, "module"},
		{EntityTypePackage, "package"},
		{EntityTypeInterface, "interface"},
		{EntityTypeMethod, "method"},
		{EntityTypeConstant, "constant"},
		{EntityType(99), "entity_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.entityType.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.entityType.String())
			}
		})
	}
}

func TestParseEntityType(t *testing.T) {
	tests := []struct {
		input    string
		expected EntityType
		hasError bool
	}{
		{"function", EntityTypeFunction, false},
		{"type", EntityTypeType, false},
		{"variable", EntityTypeVariable, false},
		{"import", EntityTypeImport, false},
		{"file", EntityTypeFile, false},
		{"module", EntityTypeModule, false},
		{"package", EntityTypePackage, false},
		{"interface", EntityTypeInterface, false},
		{"method", EntityTypeMethod, false},
		{"constant", EntityTypeConstant, false},
		{"invalid", EntityType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseEntityType(tt.input)
			if tt.hasError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestEntityType_IsValid(t *testing.T) {
	tests := []struct {
		entityType EntityType
		expected   bool
	}{
		{EntityTypeFunction, true},
		{EntityTypeConstant, true},
		{EntityType(-1), false},
		{EntityType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.entityType.String(), func(t *testing.T) {
			if tt.entityType.IsValid() != tt.expected {
				t.Errorf("IsValid() = %v, expected %v", tt.entityType.IsValid(), tt.expected)
			}
		})
	}
}

func TestValidEntityTypes(t *testing.T) {
	types := ValidEntityTypes()
	if len(types) != 10 {
		t.Errorf("expected 10 entity types, got %d", len(types))
	}

	// Verify all are valid
	for _, et := range types {
		if !et.IsValid() {
			t.Errorf("expected entity type %v to be valid", et)
		}
	}
}

func TestEntityType_JSON(t *testing.T) {
	et := EntityTypeFunction

	data, err := json.Marshal(et)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	expected := `"function"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	var unmarshaled EntityType
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled != et {
		t.Errorf("expected %v, got %v", et, unmarshaled)
	}

	// Test integer unmarshaling
	var intUnmarshaled EntityType
	if err := json.Unmarshal([]byte("1"), &intUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal int: %v", err)
	}
	if intUnmarshaled != EntityTypeType {
		t.Errorf("expected EntityTypeType, got %v", intUnmarshaled)
	}

	// Test invalid string
	var invalidUnmarshaled EntityType
	err = json.Unmarshal([]byte(`"invalid"`), &invalidUnmarshaled)
	if err == nil {
		t.Error("expected error for invalid string")
	}
}

// =============================================================================
// AliasType Tests
// =============================================================================

func TestAliasType_String(t *testing.T) {
	tests := []struct {
		aliasType AliasType
		expected  string
	}{
		{AliasCanonical, "canonical"},
		{AliasImport, "import"},
		{AliasTypeAlias, "type_alias"},
		{AliasShorthand, "shorthand"},
		{AliasQualified, "qualified"},
		{AliasType(99), "alias_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.aliasType.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.aliasType.String())
			}
		})
	}
}

func TestParseAliasType(t *testing.T) {
	tests := []struct {
		input    string
		expected AliasType
		hasError bool
	}{
		{"canonical", AliasCanonical, false},
		{"import", AliasImport, false},
		{"type_alias", AliasTypeAlias, false},
		{"shorthand", AliasShorthand, false},
		{"qualified", AliasQualified, false},
		{"invalid", AliasType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseAliasType(tt.input)
			if tt.hasError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestAliasType_IsValid(t *testing.T) {
	tests := []struct {
		aliasType AliasType
		expected  bool
	}{
		{AliasCanonical, true},
		{AliasQualified, true},
		{AliasType(-1), false},
		{AliasType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.aliasType.String(), func(t *testing.T) {
			if tt.aliasType.IsValid() != tt.expected {
				t.Errorf("IsValid() = %v, expected %v", tt.aliasType.IsValid(), tt.expected)
			}
		})
	}
}

func TestValidAliasTypes(t *testing.T) {
	types := ValidAliasTypes()
	if len(types) != 5 {
		t.Errorf("expected 5 alias types, got %d", len(types))
	}

	// Verify all are valid
	for _, at := range types {
		if !at.IsValid() {
			t.Errorf("expected alias type %v to be valid", at)
		}
	}
}

func TestAliasType_JSON(t *testing.T) {
	at := AliasImport

	data, err := json.Marshal(at)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	expected := `"import"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	var unmarshaled AliasType
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled != at {
		t.Errorf("expected %v, got %v", at, unmarshaled)
	}

	// Test integer unmarshaling
	var intUnmarshaled AliasType
	if err := json.Unmarshal([]byte("2"), &intUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal int: %v", err)
	}
	if intUnmarshaled != AliasTypeAlias {
		t.Errorf("expected AliasTypeAlias, got %v", intUnmarshaled)
	}
}

// =============================================================================
// EntityAlias Tests
// =============================================================================

func TestNewEntityAlias(t *testing.T) {
	alias := NewEntityAlias("myAlias", AliasImport, 0.9)

	if alias.Alias != "myAlias" {
		t.Errorf("expected Alias 'myAlias', got %s", alias.Alias)
	}
	if alias.AliasType != AliasImport {
		t.Errorf("expected AliasType AliasImport, got %v", alias.AliasType)
	}
	if alias.Confidence != 0.9 {
		t.Errorf("expected Confidence 0.9, got %f", alias.Confidence)
	}
	if alias.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestNewCanonicalAlias(t *testing.T) {
	alias := NewCanonicalAlias("myFunction")

	if alias.Alias != "myFunction" {
		t.Errorf("expected Alias 'myFunction', got %s", alias.Alias)
	}
	if alias.AliasType != AliasCanonical {
		t.Errorf("expected AliasType AliasCanonical, got %v", alias.AliasType)
	}
	if alias.Confidence != 1.0 {
		t.Errorf("expected Confidence 1.0, got %f", alias.Confidence)
	}
}

func TestEntityAlias_IsHighConfidence(t *testing.T) {
	tests := []struct {
		confidence float64
		expected   bool
	}{
		{1.0, true},
		{0.9, true},
		{0.8, true},
		{0.79, false},
		{0.5, false},
		{0.0, false},
	}

	for _, tt := range tests {
		alias := NewEntityAlias("test", AliasCanonical, tt.confidence)
		if alias.IsHighConfidence() != tt.expected {
			t.Errorf("IsHighConfidence() for confidence %f = %v, expected %v",
				tt.confidence, alias.IsHighConfidence(), tt.expected)
		}
	}
}

func TestEntityAlias_JSON(t *testing.T) {
	alias := EntityAlias{
		Alias:      "myAlias",
		AliasType:  AliasShorthand,
		Confidence: 0.85,
		Source:     "test_source",
		CreatedAt:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(alias)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled EntityAlias
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.Alias != alias.Alias {
		t.Errorf("expected Alias %s, got %s", alias.Alias, unmarshaled.Alias)
	}
	if unmarshaled.AliasType != alias.AliasType {
		t.Errorf("expected AliasType %v, got %v", alias.AliasType, unmarshaled.AliasType)
	}
	if unmarshaled.Confidence != alias.Confidence {
		t.Errorf("expected Confidence %f, got %f", alias.Confidence, unmarshaled.Confidence)
	}
	if unmarshaled.Source != alias.Source {
		t.Errorf("expected Source %s, got %s", alias.Source, unmarshaled.Source)
	}
}

// =============================================================================
// Entity Tests
// =============================================================================

func TestNewEntity(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")

	if entity.ID != "entity-1" {
		t.Errorf("expected ID 'entity-1', got %s", entity.ID)
	}
	if entity.CanonicalName != "myFunction" {
		t.Errorf("expected CanonicalName 'myFunction', got %s", entity.CanonicalName)
	}
	if entity.EntityType != EntityTypeFunction {
		t.Errorf("expected EntityType EntityTypeFunction, got %v", entity.EntityType)
	}
	if entity.SourceNodeID != "node-1" {
		t.Errorf("expected SourceNodeID 'node-1', got %s", entity.SourceNodeID)
	}
	if len(entity.Aliases) != 1 {
		t.Errorf("expected 1 alias, got %d", len(entity.Aliases))
	}
	if entity.Aliases[0].Alias != "myFunction" {
		t.Errorf("expected canonical alias 'myFunction', got %s", entity.Aliases[0].Alias)
	}
	if entity.Confidence != 1.0 {
		t.Errorf("expected Confidence 1.0, got %f", entity.Confidence)
	}
	if entity.Version != 1 {
		t.Errorf("expected Version 1, got %d", entity.Version)
	}
	if entity.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if entity.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestNewEntityWithAliases(t *testing.T) {
	aliases := []EntityAlias{
		NewCanonicalAlias("myFunction"),
		NewEntityAlias("mf", AliasShorthand, 0.9),
		NewEntityAlias("pkg.myFunction", AliasQualified, 0.95),
	}

	entity := NewEntityWithAliases("entity-1", "myFunction", EntityTypeFunction, "node-1", aliases)

	if len(entity.Aliases) != 3 {
		t.Errorf("expected 3 aliases, got %d", len(entity.Aliases))
	}
}

func TestEntity_AddAlias(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	originalUpdatedAt := entity.UpdatedAt

	// Add a new alias
	time.Sleep(time.Millisecond) // Ensure time difference
	added := entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))

	if !added {
		t.Error("expected alias to be added")
	}
	if len(entity.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(entity.Aliases))
	}
	if !entity.UpdatedAt.After(originalUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}

	// Try to add duplicate alias
	added = entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.9))
	if added {
		t.Error("expected duplicate alias to not be added")
	}
	if len(entity.Aliases) != 2 {
		t.Errorf("expected 2 aliases after duplicate attempt, got %d", len(entity.Aliases))
	}
}

func TestEntity_RemoveAlias(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))
	entity.AddAlias(NewEntityAlias("pkg.myFunction", AliasQualified, 0.95))

	originalUpdatedAt := entity.UpdatedAt
	time.Sleep(time.Millisecond)

	removed := entity.RemoveAlias("mf")
	if !removed {
		t.Error("expected alias to be removed")
	}
	if len(entity.Aliases) != 2 {
		t.Errorf("expected 2 aliases after removal, got %d", len(entity.Aliases))
	}
	if !entity.UpdatedAt.After(originalUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}

	// Try to remove non-existent alias
	removed = entity.RemoveAlias("nonexistent")
	if removed {
		t.Error("expected non-existent alias removal to return false")
	}
}

func TestEntity_HasAlias(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))

	if !entity.HasAlias("myFunction") {
		t.Error("expected entity to have canonical alias")
	}
	if !entity.HasAlias("mf") {
		t.Error("expected entity to have shorthand alias")
	}
	if entity.HasAlias("nonexistent") {
		t.Error("expected entity to not have nonexistent alias")
	}
}

func TestEntity_GetAlias(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))

	alias := entity.GetAlias("mf")
	if alias == nil {
		t.Fatal("expected to get alias")
	}
	if alias.Alias != "mf" {
		t.Errorf("expected alias 'mf', got %s", alias.Alias)
	}

	nonexistent := entity.GetAlias("nonexistent")
	if nonexistent != nil {
		t.Error("expected nil for nonexistent alias")
	}
}

func TestEntity_GetAliasesByType(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))
	entity.AddAlias(NewEntityAlias("m_f", AliasShorthand, 0.7))
	entity.AddAlias(NewEntityAlias("pkg.myFunction", AliasQualified, 0.95))

	shorthandAliases := entity.GetAliasesByType(AliasShorthand)
	if len(shorthandAliases) != 2 {
		t.Errorf("expected 2 shorthand aliases, got %d", len(shorthandAliases))
	}

	qualifiedAliases := entity.GetAliasesByType(AliasQualified)
	if len(qualifiedAliases) != 1 {
		t.Errorf("expected 1 qualified alias, got %d", len(qualifiedAliases))
	}

	importAliases := entity.GetAliasesByType(AliasImport)
	if len(importAliases) != 0 {
		t.Errorf("expected 0 import aliases, got %d", len(importAliases))
	}
}

func TestEntity_GetHighConfidenceAliases(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.9))
	entity.AddAlias(NewEntityAlias("m_f", AliasShorthand, 0.5))
	entity.AddAlias(NewEntityAlias("pkg.myFunction", AliasQualified, 0.8))

	highConfidence := entity.GetHighConfidenceAliases()
	if len(highConfidence) != 3 { // canonical (1.0), mf (0.9), pkg.myFunction (0.8)
		t.Errorf("expected 3 high confidence aliases, got %d", len(highConfidence))
	}
}

func TestEntity_LinkNode(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	originalUpdatedAt := entity.UpdatedAt
	time.Sleep(time.Millisecond)

	linked := entity.LinkNode("node-2")
	if !linked {
		t.Error("expected node to be linked")
	}
	if len(entity.LinkedNodeIDs) != 1 {
		t.Errorf("expected 1 linked node, got %d", len(entity.LinkedNodeIDs))
	}
	if !entity.UpdatedAt.After(originalUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}

	// Try to link duplicate node
	linked = entity.LinkNode("node-2")
	if linked {
		t.Error("expected duplicate node link to return false")
	}
	if len(entity.LinkedNodeIDs) != 1 {
		t.Errorf("expected 1 linked node after duplicate attempt, got %d", len(entity.LinkedNodeIDs))
	}
}

func TestEntity_UnlinkNode(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.LinkNode("node-2")
	entity.LinkNode("node-3")

	originalUpdatedAt := entity.UpdatedAt
	time.Sleep(time.Millisecond)

	unlinked := entity.UnlinkNode("node-2")
	if !unlinked {
		t.Error("expected node to be unlinked")
	}
	if len(entity.LinkedNodeIDs) != 1 {
		t.Errorf("expected 1 linked node after unlinking, got %d", len(entity.LinkedNodeIDs))
	}
	if !entity.UpdatedAt.After(originalUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}

	// Try to unlink non-existent node
	unlinked = entity.UnlinkNode("nonexistent")
	if unlinked {
		t.Error("expected non-existent node unlink to return false")
	}
}

func TestEntity_AllNames(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))
	entity.AddAlias(NewEntityAlias("pkg.myFunction", AliasQualified, 0.95))

	names := entity.AllNames()

	// Should include canonical + 2 aliases (canonical alias is same as canonical name, so deduplicated)
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d", len(names))
	}

	// First should be canonical name
	if names[0] != "myFunction" {
		t.Errorf("expected first name to be canonical 'myFunction', got %s", names[0])
	}
}

func TestEntity_Match(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))

	if !entity.Match("myFunction") {
		t.Error("expected match on canonical name")
	}
	if !entity.Match("mf") {
		t.Error("expected match on alias")
	}
	if entity.Match("nonexistent") {
		t.Error("expected no match on nonexistent name")
	}
}

func TestEntity_MatchWithConfidence(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))

	confidence := entity.MatchWithConfidence("myFunction")
	if confidence != 1.0 {
		t.Errorf("expected confidence 1.0 for canonical, got %f", confidence)
	}

	confidence = entity.MatchWithConfidence("mf")
	if confidence != 0.8 {
		t.Errorf("expected confidence 0.8 for alias, got %f", confidence)
	}

	confidence = entity.MatchWithConfidence("nonexistent")
	if confidence != 0.0 {
		t.Errorf("expected confidence 0.0 for nonexistent, got %f", confidence)
	}
}

func TestEntity_JSON(t *testing.T) {
	entity := NewEntity("entity-1", "myFunction", EntityTypeFunction, "node-1")
	entity.AddAlias(NewEntityAlias("mf", AliasShorthand, 0.8))
	entity.LinkNode("node-2")
	entity.Description = "Test function"
	entity.Metadata = map[string]any{"key": "value"}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled Entity
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.ID != entity.ID {
		t.Errorf("expected ID %s, got %s", entity.ID, unmarshaled.ID)
	}
	if unmarshaled.CanonicalName != entity.CanonicalName {
		t.Errorf("expected CanonicalName %s, got %s", entity.CanonicalName, unmarshaled.CanonicalName)
	}
	if unmarshaled.EntityType != entity.EntityType {
		t.Errorf("expected EntityType %v, got %v", entity.EntityType, unmarshaled.EntityType)
	}
	if unmarshaled.SourceNodeID != entity.SourceNodeID {
		t.Errorf("expected SourceNodeID %s, got %s", entity.SourceNodeID, unmarshaled.SourceNodeID)
	}
	if len(unmarshaled.Aliases) != len(entity.Aliases) {
		t.Errorf("expected %d aliases, got %d", len(entity.Aliases), len(unmarshaled.Aliases))
	}
	if len(unmarshaled.LinkedNodeIDs) != len(entity.LinkedNodeIDs) {
		t.Errorf("expected %d linked nodes, got %d", len(entity.LinkedNodeIDs), len(unmarshaled.LinkedNodeIDs))
	}
	if unmarshaled.Description != entity.Description {
		t.Errorf("expected Description %s, got %s", entity.Description, unmarshaled.Description)
	}
	if unmarshaled.Confidence != entity.Confidence {
		t.Errorf("expected Confidence %f, got %f", entity.Confidence, unmarshaled.Confidence)
	}
	if unmarshaled.Version != entity.Version {
		t.Errorf("expected Version %d, got %d", entity.Version, unmarshaled.Version)
	}
}

// =============================================================================
// Entity Behavior Tests
// =============================================================================

func TestEntity_FullWorkflow(t *testing.T) {
	// Create a new entity
	entity := NewEntity("entity-123", "MyService", EntityTypeType, "node-456")

	// Add various aliases
	entity.AddAlias(NewEntityAlias("Service", AliasShorthand, 0.85))
	entity.AddAlias(NewEntityAlias("svc", AliasShorthand, 0.7))
	entity.AddAlias(NewEntityAlias("pkg/service.MyService", AliasQualified, 0.95))
	entity.AddAlias(NewEntityAlias("IMyService", AliasTypeAlias, 0.9))

	// Verify alias count
	if len(entity.Aliases) != 5 { // canonical + 4 added
		t.Errorf("expected 5 aliases, got %d", len(entity.Aliases))
	}

	// Link some nodes
	entity.LinkNode("node-789")
	entity.LinkNode("node-101")

	if len(entity.LinkedNodeIDs) != 2 {
		t.Errorf("expected 2 linked nodes, got %d", len(entity.LinkedNodeIDs))
	}

	// Test matching
	matchTests := []struct {
		name       string
		confidence float64
	}{
		{"MyService", 1.0},
		{"Service", 0.85},
		{"svc", 0.7},
		{"pkg/service.MyService", 0.95},
		{"IMyService", 0.9},
		{"NotFound", 0.0},
	}

	for _, tt := range matchTests {
		conf := entity.MatchWithConfidence(tt.name)
		if conf != tt.confidence {
			t.Errorf("MatchWithConfidence(%s) = %f, expected %f", tt.name, conf, tt.confidence)
		}
	}

	// Get high confidence aliases
	highConf := entity.GetHighConfidenceAliases()
	expectedHighConf := 4 // MyService (1.0), Service (0.85), pkg/service.MyService (0.95), IMyService (0.9)
	if len(highConf) != expectedHighConf {
		t.Errorf("expected %d high confidence aliases, got %d", expectedHighConf, len(highConf))
	}

	// Get shorthand aliases
	shorthands := entity.GetAliasesByType(AliasShorthand)
	if len(shorthands) != 2 {
		t.Errorf("expected 2 shorthand aliases, got %d", len(shorthands))
	}

	// Remove an alias
	entity.RemoveAlias("svc")
	if entity.HasAlias("svc") {
		t.Error("expected 'svc' alias to be removed")
	}

	// Unlink a node
	entity.UnlinkNode("node-789")
	if len(entity.LinkedNodeIDs) != 1 {
		t.Errorf("expected 1 linked node after unlinking, got %d", len(entity.LinkedNodeIDs))
	}

	// Verify all names includes canonical
	allNames := entity.AllNames()
	foundCanonical := false
	for _, name := range allNames {
		if name == "MyService" {
			foundCanonical = true
			break
		}
	}
	if !foundCanonical {
		t.Error("expected canonical name in AllNames()")
	}
}

func TestEntity_EmptyAliases(t *testing.T) {
	entity := NewEntityWithAliases("entity-1", "myFunc", EntityTypeFunction, "node-1", nil)

	// Should still have the default canonical alias when nil is passed
	if len(entity.Aliases) != 1 {
		t.Errorf("expected 1 default alias, got %d", len(entity.Aliases))
	}

	// Test with empty slice - should also keep default since len(aliases) == 0
	entity2 := NewEntityWithAliases("entity-2", "myFunc", EntityTypeFunction, "node-2", []EntityAlias{})

	// Should have default canonical alias since empty slice is treated same as nil
	if len(entity2.Aliases) != 1 {
		t.Errorf("expected 1 default alias with empty slice, got %d", len(entity2.Aliases))
	}

	// Test with explicit aliases - should replace defaults
	customAliases := []EntityAlias{
		NewEntityAlias("custom", AliasShorthand, 0.9),
	}
	entity3 := NewEntityWithAliases("entity-3", "myFunc", EntityTypeFunction, "node-3", customAliases)
	if len(entity3.Aliases) != 1 {
		t.Errorf("expected 1 custom alias, got %d", len(entity3.Aliases))
	}
	if entity3.Aliases[0].Alias != "custom" {
		t.Errorf("expected custom alias 'custom', got %s", entity3.Aliases[0].Alias)
	}
}
