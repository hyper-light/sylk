package knowledge

import (
	"testing"
	"time"
)

// =============================================================================
// EntityLinker Tests
// =============================================================================

func TestNewEntityLinker(t *testing.T) {
	st := NewSymbolTable()
	linker := NewEntityLinker(st)

	if linker == nil {
		t.Fatal("NewEntityLinker returned nil")
	}

	if linker.symbolTable != st {
		t.Error("Symbol table not set correctly")
	}

	if linker.entityIndex == nil {
		t.Error("Entity index not initialized")
	}

	if linker.entityByID == nil {
		t.Error("Entity by ID map not initialized")
	}
}

func TestNewEntityLinkerWithConfig(t *testing.T) {
	st := NewSymbolTable()
	config := EntityLinkerConfig{
		MinFuzzyConfidence: 0.7,
		CaseSensitive:      false,
		CrossFileEnabled:   false,
		MaxFuzzyDistance:   2,
	}

	linker := NewEntityLinkerWithConfig(st, config)

	if linker.config.MinFuzzyConfidence != 0.7 {
		t.Errorf("Expected MinFuzzyConfidence 0.7, got %f", linker.config.MinFuzzyConfidence)
	}

	if linker.config.CaseSensitive {
		t.Error("Expected CaseSensitive false")
	}

	if linker.config.CrossFileEnabled {
		t.Error("Expected CrossFileEnabled false")
	}

	if linker.config.MaxFuzzyDistance != 2 {
		t.Errorf("Expected MaxFuzzyDistance 2, got %d", linker.config.MaxFuzzyDistance)
	}
}

func TestEntityLinker_IndexEntities(t *testing.T) {
	linker := NewEntityLinker(nil)

	entities := []ExtractedEntity{
		{
			Name:      "TestFunction",
			Kind:      EntityKindFunction,
			FilePath:  "/test/file.go",
			StartLine: 10,
			EndLine:   20,
			Signature: "func TestFunction()",
		},
		{
			Name:      "TestStruct",
			Kind:      EntityKindStruct,
			FilePath:  "/test/file.go",
			StartLine: 25,
			EndLine:   35,
			Signature: "type TestStruct struct",
		},
		{
			Name:      "AnotherFunc",
			Kind:      EntityKindFunction,
			FilePath:  "/test/other.go",
			StartLine: 5,
			EndLine:   15,
			Signature: "func AnotherFunc()",
		},
	}

	linker.IndexEntities(entities)

	// Check entities are indexed by name
	funcs := linker.GetEntitiesByName("TestFunction")
	if len(funcs) != 1 {
		t.Errorf("Expected 1 entity named TestFunction, got %d", len(funcs))
	}

	structs := linker.GetEntitiesByName("TestStruct")
	if len(structs) != 1 {
		t.Errorf("Expected 1 entity named TestStruct, got %d", len(structs))
	}
}

func TestEntityLinker_ExactNameResolution(t *testing.T) {
	linker := NewEntityLinker(nil)

	entities := []ExtractedEntity{
		{
			Name:      "ProcessData",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/processor.go",
			StartLine: 10,
			EndLine:   30,
			Signature: "func ProcessData(data []byte) error",
		},
		{
			Name:      "DataProcessor",
			Kind:      EntityKindStruct,
			FilePath:  "/pkg/processor.go",
			StartLine: 35,
			EndLine:   45,
			Signature: "type DataProcessor struct",
		},
	}

	linker.IndexEntities(entities)

	context := ExtractedEntity{
		Name:     "CallerFunc",
		Kind:     EntityKindFunction,
		FilePath: "/pkg/processor.go",
	}

	// Test exact name resolution
	resolved := linker.ResolveReference("ProcessData", context)
	if resolved == nil {
		t.Fatal("Failed to resolve exact reference 'ProcessData'")
	}

	if resolved.Name != "ProcessData" {
		t.Errorf("Expected resolved name 'ProcessData', got '%s'", resolved.Name)
	}

	if resolved.Kind != EntityKindFunction {
		t.Errorf("Expected kind Function, got %s", resolved.Kind.String())
	}
}

func TestEntityLinker_CrossFileResolution(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CrossFileEnabled = true
	linker := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{
			Name:      "SharedHelper",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/helpers.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	context := ExtractedEntity{
		Name:     "CallerFunc",
		Kind:     EntityKindFunction,
		FilePath: "/pkg/main.go", // Different file
	}

	resolved := linker.ResolveReference("SharedHelper", context)
	if resolved == nil {
		t.Fatal("Failed to resolve cross-file reference")
	}

	if resolved.FilePath != "/pkg/helpers.go" {
		t.Errorf("Expected file '/pkg/helpers.go', got '%s'", resolved.FilePath)
	}
}

func TestEntityLinker_CrossFileDisabled(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CrossFileEnabled = false
	linker := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{
			Name:      "SharedHelper",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/helpers.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	context := ExtractedEntity{
		Name:     "CallerFunc",
		Kind:     EntityKindFunction,
		FilePath: "/pkg/main.go", // Different file
	}

	resolved := linker.ResolveReference("SharedHelper", context)
	if resolved != nil {
		t.Error("Expected nil when cross-file resolution is disabled")
	}
}

func TestEntityLinker_SameFilePriority(t *testing.T) {
	linker := NewEntityLinker(nil)

	// Two entities with same name in different files
	entities := []ExtractedEntity{
		{
			Name:      "Helper",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/file1.go",
			StartLine: 10,
			EndLine:   20,
		},
		{
			Name:      "Helper",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/file2.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	// Context in file1 should prefer file1's Helper
	context := ExtractedEntity{
		Name:     "Caller",
		Kind:     EntityKindFunction,
		FilePath: "/pkg/file1.go",
	}

	resolved := linker.ResolveReference("Helper", context)
	if resolved == nil {
		t.Fatal("Failed to resolve reference")
	}

	if resolved.FilePath != "/pkg/file1.go" {
		t.Errorf("Expected same-file resolution, got file '%s'", resolved.FilePath)
	}
}

func TestEntityLinker_CaseInsensitiveResolution(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	linker := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{
			Name:      "ProcessData",
			Kind:      EntityKindFunction,
			FilePath:  "/test/file.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	context := ExtractedEntity{
		FilePath: "/test/file.go",
	}

	// Should resolve regardless of case
	resolved := linker.ResolveReference("processdata", context)
	if resolved == nil {
		t.Fatal("Failed to resolve case-insensitive reference")
	}

	if resolved.Name != "ProcessData" {
		t.Errorf("Expected 'ProcessData', got '%s'", resolved.Name)
	}
}

func TestEntityLinker_FuzzyMatching(t *testing.T) {
	config := DefaultEntityLinkerConfig()
	config.MinFuzzyConfidence = 0.5
	config.MaxFuzzyDistance = 3
	linker := NewEntityLinkerWithConfig(nil, config)

	entities := []ExtractedEntity{
		{
			Name:      "ProcessData",
			Kind:      EntityKindFunction,
			FilePath:  "/test/file.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	context := ExtractedEntity{
		FilePath: "/test/file.go",
	}

	// Test prefix matching
	resolved := linker.ResolveReference("Process", context)
	if resolved == nil {
		t.Fatal("Failed to resolve prefix match")
	}

	// Test fuzzy matching with small edit distance
	resolved = linker.ResolveReference("ProcesDat", context)
	if resolved == nil {
		t.Fatal("Failed to resolve fuzzy match with edit distance")
	}
}

func TestEntityLinker_FuzzyMatchScore(t *testing.T) {
	linker := NewEntityLinker(nil)

	tests := []struct {
		ref      string
		target   string
		minScore float64
		maxScore float64
	}{
		{"ProcessData", "ProcessData", 1.0, 1.0},
		{"processdata", "ProcessData", 0.7, 0.95}, // Case difference (edit distance in case-sensitive mode)
		{"Process", "ProcessData", 0.7, 0.95},     // Prefix match
		{"Data", "ProcessData", 0.5, 0.85},        // Suffix match
		{"essData", "ProcessData", 0.4, 0.85},     // Substring match
		{"xyz", "ProcessData", 0.0, 0.3},          // No match
	}

	for _, tt := range tests {
		score := linker.fuzzyMatchScore(tt.ref, tt.target)
		if score < tt.minScore || score > tt.maxScore {
			t.Errorf("fuzzyMatchScore(%q, %q) = %f, expected between %f and %f",
				tt.ref, tt.target, score, tt.minScore, tt.maxScore)
		}
	}
}

func TestEntityLinker_TokenizeName(t *testing.T) {
	linker := NewEntityLinker(nil)

	tests := []struct {
		name     string
		expected []string
	}{
		{"processData", []string{"process", "data"}},
		{"ProcessData", []string{"process", "data"}},
		{"process_data", []string{"process", "data"}},
		{"process-data", []string{"process", "data"}},
		{"PROCESS_DATA", []string{"p", "r", "o", "c", "e", "s", "s", "d", "a", "t", "a"}}, // All caps splits on each
		{"processdata", []string{"processdata"}},
		{"PDFParser", []string{"p", "d", "f", "parser"}},
	}

	for _, tt := range tests {
		tokens := linker.tokenizeName(tt.name)
		if len(tokens) != len(tt.expected) {
			t.Errorf("tokenizeName(%q) = %v, expected %v", tt.name, tokens, tt.expected)
			continue
		}
		for i, token := range tokens {
			if token != tt.expected[i] {
				t.Errorf("tokenizeName(%q)[%d] = %q, expected %q", tt.name, i, token, tt.expected[i])
			}
		}
	}
}

func TestEntityLinker_LevenshteinDistance(t *testing.T) {
	linker := NewEntityLinker(nil)

	tests := []struct {
		a        string
		b        string
		expected int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "adc", 1},
		{"abc", "abcd", 1},
		{"kitten", "sitting", 3},
	}

	for _, tt := range tests {
		distance := linker.levenshteinDistance(tt.a, tt.b)
		if distance != tt.expected {
			t.Errorf("levenshteinDistance(%q, %q) = %d, expected %d",
				tt.a, tt.b, distance, tt.expected)
		}
	}
}

func TestEntityLinker_QualifiedNameResolution(t *testing.T) {
	st := NewSymbolTable()

	// Create a scope for the package
	st.EnterScope("pkg")
	st.Define("Helper", &ExtractedEntity{
		Name:      "Helper",
		Kind:      EntityKindFunction,
		FilePath:  "/pkg/helpers.go",
		StartLine: 10,
		EndLine:   20,
	})
	st.ExitScope()

	linker := NewEntityLinker(st)

	entities := []ExtractedEntity{
		{
			Name:      "Helper",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/helpers.go",
			StartLine: 10,
			EndLine:   20,
		},
	}
	linker.IndexEntities(entities)

	context := ExtractedEntity{
		FilePath: "/main/main.go",
	}

	// Test qualified name resolution
	resolved := linker.ResolveReference("pkg.Helper", context)
	if resolved == nil {
		t.Fatal("Failed to resolve qualified name reference")
	}

	if resolved.Name != "Helper" {
		t.Errorf("Expected 'Helper', got '%s'", resolved.Name)
	}
}

func TestEntityLinker_Clear(t *testing.T) {
	linker := NewEntityLinker(nil)

	entities := []ExtractedEntity{
		{
			Name:      "TestFunc",
			Kind:      EntityKindFunction,
			FilePath:  "/test/file.go",
			StartLine: 10,
			EndLine:   20,
		},
	}

	linker.IndexEntities(entities)

	// Verify entity exists
	if linker.GetEntitiesByName("TestFunc") == nil {
		t.Fatal("Entity should exist before clear")
	}

	linker.Clear()

	// Verify entity no longer exists
	if linker.GetEntitiesByName("TestFunc") != nil {
		t.Error("Entity should not exist after clear")
	}
}

func TestLinkType_String(t *testing.T) {
	tests := []struct {
		lt       LinkType
		expected string
	}{
		{LinkDefinite, "definite"},
		{LinkProbable, "probable"},
		{LinkPossible, "possible"},
		{LinkType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.lt.String(); got != tt.expected {
			t.Errorf("LinkType(%d).String() = %q, expected %q", tt.lt, got, tt.expected)
		}
	}
}

func TestParseLinkType(t *testing.T) {
	tests := []struct {
		input    string
		expected LinkType
		ok       bool
	}{
		{"definite", LinkDefinite, true},
		{"probable", LinkProbable, true},
		{"possible", LinkPossible, true},
		{"invalid", LinkDefinite, false},
		{"", LinkDefinite, false},
	}

	for _, tt := range tests {
		got, ok := ParseLinkType(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseLinkType(%q) ok = %v, expected %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseLinkType(%q) = %v, expected %v", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// RelationValidator Tests
// =============================================================================

func TestNewRelationValidator(t *testing.T) {
	validator := NewRelationValidator()

	if validator == nil {
		t.Fatal("NewRelationValidator returned nil")
	}

	if validator.entityIndex == nil {
		t.Error("Entity index not initialized")
	}

	if validator.validRelationMatrix == nil {
		t.Error("Valid relation matrix not initialized")
	}
}

func TestNewRelationValidatorWithConfig(t *testing.T) {
	config := RelationValidatorConfig{
		MinConfidence:           0.5,
		RequireBothEndpoints:    true,
		StrictTypeChecking:      false,
		AllowExternalReferences: false,
	}

	validator := NewRelationValidatorWithConfig(config)

	if validator.config.MinConfidence != 0.5 {
		t.Errorf("Expected MinConfidence 0.5, got %f", validator.config.MinConfidence)
	}

	if !validator.config.RequireBothEndpoints {
		t.Error("Expected RequireBothEndpoints true")
	}

	if validator.config.StrictTypeChecking {
		t.Error("Expected StrictTypeChecking false")
	}
}

func TestRelationValidator_IndexEntities(t *testing.T) {
	validator := NewRelationValidator()

	entities := []ExtractedEntity{
		{
			Name:      "TestFunction",
			Kind:      EntityKindFunction,
			FilePath:  "/test/file.go",
			StartLine: 10,
			EndLine:   20,
			Signature: "func TestFunction()",
		},
		{
			Name:      "TestStruct",
			Kind:      EntityKindStruct,
			FilePath:  "/test/file.go",
			StartLine: 25,
			EndLine:   35,
			Signature: "type TestStruct struct",
		},
	}

	validator.IndexEntities(entities)

	// Verify entities are indexed (internal check via validation)
	relation := ExtractedRelation{
		SourceEntity: &entities[0],
		TargetEntity: &entities[1],
		RelationType: RelUses,
		Confidence:   1.0,
	}

	result := validator.ValidateRelation(relation, entities)
	if !result.Valid {
		t.Error("Expected valid relation with indexed entities")
	}
}

func TestRelationValidator_ValidateRelation_Valid(t *testing.T) {
	validator := NewRelationValidator()

	sourceEntity := &ExtractedEntity{
		Name:      "ProcessData",
		Kind:      EntityKindFunction,
		FilePath:  "/pkg/processor.go",
		StartLine: 10,
		EndLine:   30,
	}

	targetEntity := &ExtractedEntity{
		Name:      "helperFunc",
		Kind:      EntityKindFunction,
		FilePath:  "/pkg/helpers.go",
		StartLine: 5,
		EndLine:   15,
	}

	entities := []ExtractedEntity{*sourceEntity, *targetEntity}
	validator.IndexEntities(entities)

	relation := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: RelCalls,
		Evidence: []EvidenceSpan{
			{FilePath: "/pkg/processor.go", StartLine: 15, EndLine: 15, Snippet: "helperFunc()"},
		},
		Confidence:  0.95,
		ExtractedAt: time.Now(),
	}

	result := validator.ValidateRelation(relation, entities)

	if !result.Valid {
		t.Errorf("Expected valid relation, got invalid with issues: %v", result.Issues)
	}

	if result.Confidence < 0.9 {
		t.Errorf("Expected confidence >= 0.9, got %f", result.Confidence)
	}
}

func TestRelationValidator_ValidateRelation_NilSource(t *testing.T) {
	validator := NewRelationValidator()

	targetEntity := &ExtractedEntity{
		Name:      "TargetFunc",
		Kind:      EntityKindFunction,
		FilePath:  "/test/file.go",
		StartLine: 10,
		EndLine:   20,
	}

	relation := ExtractedRelation{
		SourceEntity: nil, // Nil source
		TargetEntity: targetEntity,
		RelationType: RelCalls,
		Confidence:   0.9,
	}

	result := validator.ValidateRelation(relation, nil)

	if result.Valid {
		t.Error("Expected invalid relation with nil source")
	}

	hasIssue := false
	for _, issue := range result.Issues {
		if issue == "source entity is nil" {
			hasIssue = true
			break
		}
	}
	if !hasIssue {
		t.Error("Expected 'source entity is nil' issue")
	}
}

func TestRelationValidator_ValidateRelation_NilTarget(t *testing.T) {
	validator := NewRelationValidator()

	sourceEntity := &ExtractedEntity{
		Name:      "SourceFunc",
		Kind:      EntityKindFunction,
		FilePath:  "/test/file.go",
		StartLine: 10,
		EndLine:   20,
	}

	relation := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: nil, // Nil target
		RelationType: RelCalls,
		Confidence:   0.9,
	}

	result := validator.ValidateRelation(relation, nil)

	if result.Valid {
		t.Error("Expected invalid relation with nil target")
	}

	hasIssue := false
	for _, issue := range result.Issues {
		if issue == "target entity is nil" {
			hasIssue = true
			break
		}
	}
	if !hasIssue {
		t.Error("Expected 'target entity is nil' issue")
	}
}

func TestRelationValidator_ValidateRelation_InvalidType(t *testing.T) {
	config := DefaultRelationValidatorConfig()
	config.StrictTypeChecking = true
	validator := NewRelationValidatorWithConfig(config)

	sourceEntity := &ExtractedEntity{
		Name:      "MyStruct",
		Kind:      EntityKindStruct,
		FilePath:  "/test/file.go",
		StartLine: 10,
		EndLine:   20,
	}

	targetEntity := &ExtractedEntity{
		Name:      "MyVar",
		Kind:      EntityKindVariable,
		FilePath:  "/test/file.go",
		StartLine: 25,
		EndLine:   25,
	}

	entities := []ExtractedEntity{*sourceEntity, *targetEntity}
	validator.IndexEntities(entities)

	// Calls relation is not valid between struct and variable
	relation := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: RelCalls, // Invalid for struct->variable
		Confidence:   0.9,
		Evidence: []EvidenceSpan{
			{FilePath: "/test/file.go", StartLine: 15},
		},
	}

	result := validator.ValidateRelation(relation, entities)

	if result.Valid {
		t.Error("Expected invalid relation with wrong relation type")
	}

	hasTypeIssue := false
	for _, issue := range result.Issues {
		if len(issue) > 0 && (issue[0:8] == "relation") {
			hasTypeIssue = true
			break
		}
	}
	if !hasTypeIssue {
		t.Error("Expected relation type validation issue")
	}
}

func TestRelationValidator_ValidateRelation_LowConfidence(t *testing.T) {
	config := DefaultRelationValidatorConfig()
	config.MinConfidence = 0.5
	validator := NewRelationValidatorWithConfig(config)

	sourceEntity := &ExtractedEntity{
		Name: "Func1",
		Kind: EntityKindFunction,
	}

	targetEntity := &ExtractedEntity{
		Name: "Func2",
		Kind: EntityKindFunction,
	}

	relation := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: RelCalls,
		Confidence:   0.2, // Below threshold
		Evidence: []EvidenceSpan{
			{FilePath: "/test/file.go", StartLine: 10},
		},
	}

	result := validator.ValidateRelation(relation, nil)

	if result.Valid {
		t.Error("Expected invalid relation with low confidence")
	}

	hasConfidenceIssue := false
	for _, issue := range result.Issues {
		if len(issue) > 8 && issue[0:8] == "relation" {
			hasConfidenceIssue = true
			break
		}
	}
	if !hasConfidenceIssue {
		t.Error("Expected confidence threshold issue")
	}
}

func TestRelationValidator_ValidateRelation_SelfReference(t *testing.T) {
	validator := NewRelationValidator()

	entity := &ExtractedEntity{
		Name:      "MyFunc",
		Kind:      EntityKindFunction,
		FilePath:  "/test/file.go",
		StartLine: 10,
		EndLine:   20,
	}

	relation := ExtractedRelation{
		SourceEntity: entity,
		TargetEntity: entity, // Self-reference
		RelationType: RelCalls,
		Confidence:   0.9,
		Evidence: []EvidenceSpan{
			{FilePath: "/test/file.go", StartLine: 15},
		},
	}

	result := validator.ValidateRelation(relation, []ExtractedEntity{*entity})

	// Self-reference adds an issue but doesn't necessarily invalidate
	hasSelfRefIssue := false
	for _, issue := range result.Issues {
		if issue == "relation is a self-reference" {
			hasSelfRefIssue = true
			break
		}
	}

	if !hasSelfRefIssue {
		t.Error("Expected self-reference issue")
	}
}

func TestRelationValidator_ValidateRelation_NoEvidence(t *testing.T) {
	validator := NewRelationValidator()

	sourceEntity := &ExtractedEntity{
		Name: "Func1",
		Kind: EntityKindFunction,
	}

	targetEntity := &ExtractedEntity{
		Name: "Func2",
		Kind: EntityKindFunction,
	}

	relation := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: RelCalls,
		Confidence:   0.9,
		Evidence:     []EvidenceSpan{}, // No evidence
	}

	result := validator.ValidateRelation(relation, nil)

	hasEvidenceIssue := false
	for _, issue := range result.Issues {
		if issue == "relation has no evidence" {
			hasEvidenceIssue = true
			break
		}
	}

	if !hasEvidenceIssue {
		t.Error("Expected no evidence issue")
	}
}

func TestRelationValidator_ValidateAll(t *testing.T) {
	validator := NewRelationValidator()

	entities := []ExtractedEntity{
		{Name: "Func1", Kind: EntityKindFunction, FilePath: "/test/file.go"},
		{Name: "Func2", Kind: EntityKindFunction, FilePath: "/test/file.go"},
		{Name: "Func3", Kind: EntityKindFunction, FilePath: "/test/file.go"},
	}

	relations := []ExtractedRelation{
		{
			SourceEntity: &entities[0],
			TargetEntity: &entities[1],
			RelationType: RelCalls,
			Confidence:   0.9,
			Evidence:     []EvidenceSpan{{FilePath: "/test/file.go", StartLine: 10}},
		},
		{
			SourceEntity: &entities[1],
			TargetEntity: &entities[2],
			RelationType: RelCalls,
			Confidence:   0.8,
			Evidence:     []EvidenceSpan{{FilePath: "/test/file.go", StartLine: 20}},
		},
		{
			SourceEntity: nil, // Invalid
			TargetEntity: &entities[2],
			RelationType: RelCalls,
			Confidence:   0.9,
		},
	}

	results := validator.ValidateAll(relations, entities)

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// First two should be valid
	if !results[0].Valid {
		t.Error("Expected first relation to be valid")
	}
	if !results[1].Valid {
		t.Error("Expected second relation to be valid")
	}

	// Third should be invalid
	if results[2].Valid {
		t.Error("Expected third relation to be invalid")
	}
}

func TestRelationValidator_FilterValid(t *testing.T) {
	validator := NewRelationValidator()

	entities := []ExtractedEntity{
		{Name: "Func1", Kind: EntityKindFunction},
		{Name: "Func2", Kind: EntityKindFunction},
	}

	relations := []ExtractedRelation{
		{
			SourceEntity: &entities[0],
			TargetEntity: &entities[1],
			RelationType: RelCalls,
			Confidence:   0.9,
			Evidence:     []EvidenceSpan{{FilePath: "/test/file.go", StartLine: 10}},
		},
		{
			SourceEntity: nil, // Invalid
			TargetEntity: &entities[1],
			RelationType: RelCalls,
			Confidence:   0.9,
		},
	}

	filtered := validator.FilterValid(relations, entities)

	if len(filtered) != 1 {
		t.Errorf("Expected 1 valid relation, got %d", len(filtered))
	}
}

func TestRelationValidator_FilterByConfidence(t *testing.T) {
	validator := NewRelationValidator()

	relations := []ExtractedRelation{
		{Confidence: 0.9},
		{Confidence: 0.7},
		{Confidence: 0.5},
		{Confidence: 0.3},
	}

	// Filter with minimum confidence 0.6
	filtered := validator.FilterByConfidence(relations, 0.6)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 relations with confidence >= 0.6, got %d", len(filtered))
	}
}

func TestRelationValidator_GetValidRelationTypes(t *testing.T) {
	validator := NewRelationValidator()

	// Function to Function should allow calls
	validTypes := validator.GetValidRelationTypes(EntityKindFunction, EntityKindFunction)

	hasCalls := false
	for _, rt := range validTypes {
		if rt == RelCalls {
			hasCalls = true
			break
		}
	}

	if !hasCalls {
		t.Error("Expected RelCalls to be valid for Function->Function")
	}

	// Struct to Interface should allow implements
	validTypes = validator.GetValidRelationTypes(EntityKindStruct, EntityKindInterface)

	hasImplements := false
	for _, rt := range validTypes {
		if rt == RelImplements {
			hasImplements = true
			break
		}
	}

	if !hasImplements {
		t.Error("Expected RelImplements to be valid for Struct->Interface")
	}
}

func TestRelationValidator_Clear(t *testing.T) {
	validator := NewRelationValidator()

	entities := []ExtractedEntity{
		{Name: "Func1", Kind: EntityKindFunction, Signature: "func Func1()"},
	}

	validator.IndexEntities(entities)
	validator.Clear()

	// After clear, entity should not be found
	if len(validator.entityIndex) != 0 {
		t.Error("Entity index should be empty after clear")
	}

	if len(validator.entityBySignature) != 0 {
		t.Error("Entity by signature should be empty after clear")
	}
}

func TestRelationValidator_SetConfig(t *testing.T) {
	validator := NewRelationValidator()

	newConfig := RelationValidatorConfig{
		MinConfidence:           0.8,
		RequireBothEndpoints:    true,
		StrictTypeChecking:      false,
		AllowExternalReferences: false,
	}

	validator.SetConfig(newConfig)
	config := validator.GetConfig()

	if config.MinConfidence != 0.8 {
		t.Errorf("Expected MinConfidence 0.8, got %f", config.MinConfidence)
	}

	if !config.RequireBothEndpoints {
		t.Error("Expected RequireBothEndpoints true")
	}
}

func TestRelationValidator_Summarize(t *testing.T) {
	validator := NewRelationValidator()

	results := []ValidationResult{
		{Valid: true, Confidence: 0.9, Issues: nil},
		{Valid: true, Confidence: 0.8, Issues: []string{"minor issue"}},
		{Valid: false, Confidence: 0.3, Issues: []string{"source entity is nil"}},
		{Valid: false, Confidence: 0.2, Issues: []string{"source entity is nil", "low confidence"}},
	}

	summary := validator.Summarize(results)

	if summary.TotalRelations != 4 {
		t.Errorf("Expected TotalRelations 4, got %d", summary.TotalRelations)
	}

	if summary.ValidRelations != 2 {
		t.Errorf("Expected ValidRelations 2, got %d", summary.ValidRelations)
	}

	if summary.InvalidRelations != 2 {
		t.Errorf("Expected InvalidRelations 2, got %d", summary.InvalidRelations)
	}

	expectedAvg := (0.9 + 0.8 + 0.3 + 0.2) / 4.0
	if summary.AverageConfidence != expectedAvg {
		t.Errorf("Expected AverageConfidence %f, got %f", expectedAvg, summary.AverageConfidence)
	}

	if summary.CommonIssues["source entity is nil"] != 2 {
		t.Errorf("Expected 'source entity is nil' count 2, got %d", summary.CommonIssues["source entity is nil"])
	}
}

func TestRelationValidator_RequireBothEndpoints(t *testing.T) {
	config := DefaultRelationValidatorConfig()
	config.RequireBothEndpoints = true
	validator := NewRelationValidatorWithConfig(config)

	// Only index one entity
	entities := []ExtractedEntity{
		{Name: "Func1", Kind: EntityKindFunction, FilePath: "/test/file.go"},
	}
	validator.IndexEntities(entities)

	relation := ExtractedRelation{
		SourceEntity: &entities[0],
		TargetEntity: &ExtractedEntity{
			Name: "ExternalFunc", // Not indexed
			Kind: EntityKindFunction,
		},
		RelationType: RelCalls,
		Confidence:   0.9,
		Evidence:     []EvidenceSpan{{FilePath: "/test/file.go", StartLine: 10}},
	}

	result := validator.ValidateRelation(relation, entities)

	if result.Valid {
		t.Error("Expected invalid when RequireBothEndpoints is true and target not found")
	}
}

func TestRelationValidator_AllowExternalReferences(t *testing.T) {
	config := DefaultRelationValidatorConfig()
	config.RequireBothEndpoints = false
	config.AllowExternalReferences = true
	validator := NewRelationValidatorWithConfig(config)

	entities := []ExtractedEntity{
		{Name: "Func1", Kind: EntityKindFunction, FilePath: "/test/file.go"},
	}
	validator.IndexEntities(entities)

	relation := ExtractedRelation{
		SourceEntity: &entities[0],
		TargetEntity: &ExtractedEntity{
			Name: "ExternalFunc", // Not indexed - external
			Kind: EntityKindFunction,
		},
		RelationType: RelCalls,
		Confidence:   0.9,
		Evidence:     []EvidenceSpan{{FilePath: "/test/file.go", StartLine: 10}},
	}

	result := validator.ValidateRelation(relation, entities)

	if !result.Valid {
		t.Errorf("Expected valid when AllowExternalReferences is true, got issues: %v", result.Issues)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestEntityLinkerAndValidatorIntegration(t *testing.T) {
	// Create entities
	entities := []ExtractedEntity{
		{
			Name:      "ProcessData",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/processor.go",
			StartLine: 10,
			EndLine:   30,
			Signature: "func ProcessData(data []byte) error",
		},
		{
			Name:      "validateInput",
			Kind:      EntityKindFunction,
			FilePath:  "/pkg/processor.go",
			StartLine: 35,
			EndLine:   50,
			Signature: "func validateInput(data []byte) bool",
		},
		{
			Name:      "DataProcessor",
			Kind:      EntityKindStruct,
			FilePath:  "/pkg/processor.go",
			StartLine: 55,
			EndLine:   65,
			Signature: "type DataProcessor struct",
		},
		{
			Name:      "Processor",
			Kind:      EntityKindInterface,
			FilePath:  "/pkg/interfaces.go",
			StartLine: 5,
			EndLine:   10,
			Signature: "type Processor interface",
		},
	}

	// Setup linker
	linker := NewEntityLinker(nil)
	linker.IndexEntities(entities)

	// Resolve references
	context := entities[0]
	resolved := linker.ResolveReference("validateInput", context)
	if resolved == nil {
		t.Fatal("Failed to resolve 'validateInput' reference")
	}

	// Create relations based on resolved references
	relations := []ExtractedRelation{
		{
			SourceEntity: &entities[0],
			TargetEntity: resolved,
			RelationType: RelCalls,
			Confidence:   0.95,
			Evidence: []EvidenceSpan{
				{FilePath: "/pkg/processor.go", StartLine: 20, EndLine: 20, Snippet: "validateInput(data)"},
			},
			ExtractedAt: time.Now(),
		},
		{
			SourceEntity: &entities[2], // DataProcessor struct
			TargetEntity: &entities[3], // Processor interface
			RelationType: RelImplements,
			Confidence:   0.9,
			Evidence: []EvidenceSpan{
				{FilePath: "/pkg/processor.go", StartLine: 55, EndLine: 55},
			},
			ExtractedAt: time.Now(),
		},
	}

	// Setup validator
	validator := NewRelationValidator()
	validator.IndexEntities(entities)

	// Validate all relations
	results := validator.ValidateAll(relations, entities)

	for i, result := range results {
		if !result.Valid {
			t.Errorf("Relation %d failed validation: %v", i, result.Issues)
		}
	}

	// Get summary
	summary := validator.Summarize(results)
	if summary.ValidRelations != 2 {
		t.Errorf("Expected 2 valid relations, got %d", summary.ValidRelations)
	}
}

func TestCrossFileEntityLinking(t *testing.T) {
	entities := []ExtractedEntity{
		{
			Name:     "SharedConfig",
			Kind:     EntityKindStruct,
			FilePath: "/pkg/config/config.go",
		},
		{
			Name:     "LoadConfig",
			Kind:     EntityKindFunction,
			FilePath: "/pkg/config/loader.go",
		},
		{
			Name:     "UseConfig",
			Kind:     EntityKindFunction,
			FilePath: "/pkg/main/main.go",
		},
	}

	linker := NewEntityLinker(nil)
	linker.IndexEntities(entities)

	// Resolve from main.go to config.go
	context := entities[2] // UseConfig in main.go
	resolved := linker.ResolveReference("SharedConfig", context)

	if resolved == nil {
		t.Fatal("Failed to resolve cross-file reference")
	}

	if resolved.FilePath != "/pkg/config/config.go" {
		t.Errorf("Expected file '/pkg/config/config.go', got '%s'", resolved.FilePath)
	}

	// Create and validate the relation
	relation := ExtractedRelation{
		SourceEntity: &entities[2],
		TargetEntity: resolved,
		RelationType: RelUses,
		Confidence:   0.85,
		Evidence: []EvidenceSpan{
			{FilePath: "/pkg/main/main.go", StartLine: 10},
		},
	}

	validator := NewRelationValidator()
	validator.IndexEntities(entities)

	result := validator.ValidateRelation(relation, entities)
	if !result.Valid {
		t.Errorf("Cross-file relation failed validation: %v", result.Issues)
	}
}

func TestEdgeCases(t *testing.T) {
	t.Run("EmptyEntities", func(t *testing.T) {
		linker := NewEntityLinker(nil)
		linker.IndexEntities([]ExtractedEntity{})

		resolved := linker.ResolveReference("NonExistent", ExtractedEntity{})
		if resolved != nil {
			t.Error("Expected nil for non-existent reference with empty index")
		}
	})

	t.Run("EmptyReference", func(t *testing.T) {
		linker := NewEntityLinker(nil)
		linker.IndexEntities([]ExtractedEntity{
			{Name: "Test", Kind: EntityKindFunction},
		})

		resolved := linker.ResolveReference("", ExtractedEntity{})
		if resolved != nil {
			t.Error("Expected nil for empty reference string")
		}
	})

	t.Run("EmptyRelations", func(t *testing.T) {
		validator := NewRelationValidator()
		results := validator.ValidateAll([]ExtractedRelation{}, []ExtractedEntity{})

		if len(results) != 0 {
			t.Error("Expected empty results for empty relations")
		}
	})

	t.Run("DuplicateEntityNames", func(t *testing.T) {
		linker := NewEntityLinker(nil)
		entities := []ExtractedEntity{
			{Name: "Helper", Kind: EntityKindFunction, FilePath: "/a.go"},
			{Name: "Helper", Kind: EntityKindFunction, FilePath: "/b.go"},
			{Name: "Helper", Kind: EntityKindStruct, FilePath: "/c.go"},
		}
		linker.IndexEntities(entities)

		found := linker.GetEntitiesByName("Helper")
		if len(found) != 3 {
			t.Errorf("Expected 3 entities named Helper, got %d", len(found))
		}
	})

	t.Run("ZeroConfidenceSummary", func(t *testing.T) {
		validator := NewRelationValidator()
		summary := validator.Summarize([]ValidationResult{})

		if summary.TotalRelations != 0 {
			t.Error("Expected 0 total relations for empty results")
		}
		if summary.AverageConfidence != 0 {
			t.Error("Expected 0 average confidence for empty results")
		}
	})
}
