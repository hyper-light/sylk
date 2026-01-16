package archivalist

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Tool Constants Tests
// =============================================================================

func TestToolConstants_Names(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"GetBriefing", ToolGetBriefing, "archivalist_get_briefing"},
		{"QueryPatterns", ToolQueryPatterns, "archivalist_query_patterns"},
		{"QueryFailures", ToolQueryFailures, "archivalist_query_failures"},
		{"QueryContext", ToolQueryContext, "archivalist_query_context"},
		{"QueryFileState", ToolQueryFileState, "archivalist_query_file_state"},
		{"RecordPattern", ToolRecordPattern, "archivalist_record_pattern"},
		{"RecordFailure", ToolRecordFailure, "archivalist_record_failure"},
		{"UpdateFileState", ToolUpdateFileState, "archivalist_update_file_state"},
		{"DeclareIntent", ToolDeclareIntent, "archivalist_declare_intent"},
		{"CompleteIntent", ToolCompleteIntent, "archivalist_complete_intent"},
		{"GetConflicts", ToolGetConflicts, "archivalist_get_conflicts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant, "Tool constant should match")
		})
	}
}

func TestToolConstants_UniqueNames(t *testing.T) {
	// Collect all tool names
	names := []string{
		ToolGetBriefing,
		ToolQueryPatterns,
		ToolQueryFailures,
		ToolQueryContext,
		ToolQueryFileState,
		ToolRecordPattern,
		ToolRecordFailure,
		ToolUpdateFileState,
		ToolDeclareIntent,
		ToolCompleteIntent,
		ToolGetConflicts,
	}

	// Check for uniqueness
	seen := make(map[string]bool)
	for _, name := range names {
		assert.False(t, seen[name], "Tool name %s should be unique", name)
		seen[name] = true
	}
}

func TestToolConstants_Prefix(t *testing.T) {
	// All tools should have the archivalist prefix
	names := []string{
		ToolGetBriefing,
		ToolQueryPatterns,
		ToolQueryFailures,
		ToolQueryContext,
		ToolQueryFileState,
		ToolRecordPattern,
		ToolRecordFailure,
		ToolUpdateFileState,
		ToolDeclareIntent,
		ToolCompleteIntent,
		ToolGetConflicts,
	}

	for _, name := range names {
		assert.Contains(t, name, "archivalist_", "Tool name should have archivalist prefix")
	}
}

// =============================================================================
// ToolDefinition Tests
// =============================================================================

func TestToolDefinition_Structure(t *testing.T) {
	def := ToolDefinition{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}

	assert.Equal(t, "test_tool", def.Name)
	assert.Equal(t, "A test tool", def.Description)
	assert.NotNil(t, def.InputSchema)
}

func TestToolDefinition_JSONSerialization(t *testing.T) {
	def := ToolDefinition{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]interface{}{
					"type":        "string",
					"description": "An input",
				},
			},
		},
	}

	// Serialize
	data, err := json.Marshal(def)
	require.NoError(t, err, "Marshal")

	// Deserialize
	var restored ToolDefinition
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err, "Unmarshal")

	assert.Equal(t, def.Name, restored.Name)
	assert.Equal(t, def.Description, restored.Description)
	assert.NotNil(t, restored.InputSchema)
}

// =============================================================================
// AllToolDefinitions Tests
// =============================================================================

func TestAllToolDefinitions_Count(t *testing.T) {
	defs := AllToolDefinitions()

	// Should have all 11 tools
	assert.Len(t, defs, 11, "Should have 11 tool definitions")
}

func TestAllToolDefinitions_AllHaveNames(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		assert.NotEmpty(t, def.Name, "Every tool should have a name")
	}
}

func TestAllToolDefinitions_AllHaveDescriptions(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		assert.NotEmpty(t, def.Description, "Tool %s should have a description", def.Name)
	}
}

func TestAllToolDefinitions_AllHaveInputSchemas(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		assert.NotNil(t, def.InputSchema, "Tool %s should have an input schema", def.Name)
		// Schema should have type "object"
		schemaType, ok := def.InputSchema["type"]
		assert.True(t, ok, "Tool %s schema should have 'type' field", def.Name)
		assert.Equal(t, "object", schemaType, "Tool %s schema type should be 'object'", def.Name)
	}
}

func TestAllToolDefinitions_UniqueNames(t *testing.T) {
	defs := AllToolDefinitions()

	seen := make(map[string]bool)
	for _, def := range defs {
		assert.False(t, seen[def.Name], "Tool name %s should be unique", def.Name)
		seen[def.Name] = true
	}
}

func TestAllToolDefinitions_ExpectedTools(t *testing.T) {
	defs := AllToolDefinitions()

	expectedTools := map[string]bool{
		ToolGetBriefing:     false,
		ToolQueryPatterns:   false,
		ToolQueryFailures:   false,
		ToolQueryContext:    false,
		ToolQueryFileState:  false,
		ToolRecordPattern:   false,
		ToolRecordFailure:   false,
		ToolUpdateFileState: false,
		ToolDeclareIntent:   false,
		ToolCompleteIntent:  false,
		ToolGetConflicts:    false,
	}

	for _, def := range defs {
		if _, ok := expectedTools[def.Name]; ok {
			expectedTools[def.Name] = true
		}
	}

	for tool, found := range expectedTools {
		assert.True(t, found, "Expected tool %s should be in definitions", tool)
	}
}

// =============================================================================
// Individual Tool Definition Tests
// =============================================================================

func TestGetBriefingDefinition(t *testing.T) {
	def := getBriefingDefinition()

	assert.Equal(t, ToolGetBriefing, def.Name)
	assert.Contains(t, def.Description, "briefing")

	// Check schema has tier property with enum
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	tier, ok := props["tier"].(map[string]interface{})
	require.True(t, ok, "Should have tier property")

	assert.Equal(t, "string", tier["type"])
	enum, ok := tier["enum"].([]string)
	require.True(t, ok, "Tier should have enum")
	assert.Contains(t, enum, "micro")
	assert.Contains(t, enum, "standard")
	assert.Contains(t, enum, "full")
}

func TestQueryPatternsDefinition(t *testing.T) {
	def := queryPatternsDefinition()

	assert.Equal(t, ToolQueryPatterns, def.Name)
	assert.Contains(t, def.Description, "pattern")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "category")

	// Check properties
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	assert.Contains(t, props, "category")
	assert.Contains(t, props, "scope")
	assert.Contains(t, props, "limit")
}

func TestQueryFailuresDefinition(t *testing.T) {
	def := queryFailuresDefinition()

	assert.Equal(t, ToolQueryFailures, def.Name)
	assert.Contains(t, def.Description, "failure")

	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	assert.Contains(t, props, "error_type")
	assert.Contains(t, props, "file_pattern")
	assert.Contains(t, props, "limit")
}

func TestQueryContextDefinition(t *testing.T) {
	def := queryContextDefinition()

	assert.Equal(t, ToolQueryContext, def.Name)
	assert.Contains(t, def.Description, "query")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "query")

	// Check scope enum
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	scope, ok := props["scope"].(map[string]interface{})
	require.True(t, ok, "Should have scope property")

	enum, ok := scope["enum"].([]string)
	require.True(t, ok, "Scope should have enum")
	assert.Contains(t, enum, "session")
	assert.Contains(t, enum, "global")
	assert.Contains(t, enum, "all")
}

func TestQueryFileStateDefinition(t *testing.T) {
	def := queryFileStateDefinition()

	assert.Equal(t, ToolQueryFileState, def.Name)
	assert.Contains(t, def.Description, "file")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "path")

	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	assert.Contains(t, props, "path")
	assert.Contains(t, props, "include_history")
}

func TestRecordPatternDefinition(t *testing.T) {
	def := recordPatternDefinition()

	assert.Equal(t, ToolRecordPattern, def.Name)
	assert.Contains(t, def.Description, "pattern")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "pattern")
	assert.Contains(t, required, "category")

	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	assert.Contains(t, props, "pattern")
	assert.Contains(t, props, "category")
	assert.Contains(t, props, "scope")
	assert.Contains(t, props, "supersedes")
	assert.Contains(t, props, "reason")
}

func TestRecordFailureDefinition(t *testing.T) {
	def := recordFailureDefinition()

	assert.Equal(t, ToolRecordFailure, def.Name)
	assert.Contains(t, def.Description, "failure")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "error")
	assert.Contains(t, required, "approach")
	assert.Contains(t, required, "resolution")
	assert.Contains(t, required, "outcome")

	// Check outcome enum
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	outcome, ok := props["outcome"].(map[string]interface{})
	require.True(t, ok, "Should have outcome property")

	enum, ok := outcome["enum"].([]string)
	require.True(t, ok, "Outcome should have enum")
	assert.Contains(t, enum, "success")
	assert.Contains(t, enum, "partial")
	assert.Contains(t, enum, "failed")
}

func TestUpdateFileStateDefinition(t *testing.T) {
	def := updateFileStateDefinition()

	assert.Equal(t, ToolUpdateFileState, def.Name)
	assert.Contains(t, def.Description, "file")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "path")
	assert.Contains(t, required, "action")

	// Check action enum
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	action, ok := props["action"].(map[string]interface{})
	require.True(t, ok, "Should have action property")

	enum, ok := action["enum"].([]string)
	require.True(t, ok, "Action should have enum")
	assert.Contains(t, enum, "read")
	assert.Contains(t, enum, "modified")
	assert.Contains(t, enum, "created")
	assert.Contains(t, enum, "deleted")
}

func TestDeclareIntentDefinition(t *testing.T) {
	def := declareIntentDefinition()

	assert.Equal(t, ToolDeclareIntent, def.Name)
	assert.Contains(t, def.Description, "intent")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "type")
	assert.Contains(t, required, "description")
	assert.Contains(t, required, "affected_paths")

	// Check type enum
	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	typeField, ok := props["type"].(map[string]interface{})
	require.True(t, ok, "Should have type property")

	typeEnum, ok := typeField["enum"].([]string)
	require.True(t, ok, "Type should have enum")
	assert.Contains(t, typeEnum, "refactor")
	assert.Contains(t, typeEnum, "rename")
	assert.Contains(t, typeEnum, "api_change")
	assert.Contains(t, typeEnum, "breaking_change")

	// Check priority enum
	priority, ok := props["priority"].(map[string]interface{})
	require.True(t, ok, "Should have priority property")

	priorityEnum, ok := priority["enum"].([]string)
	require.True(t, ok, "Priority should have enum")
	assert.Contains(t, priorityEnum, "low")
	assert.Contains(t, priorityEnum, "medium")
	assert.Contains(t, priorityEnum, "high")
	assert.Contains(t, priorityEnum, "critical")
}

func TestCompleteIntentDefinition(t *testing.T) {
	def := completeIntentDefinition()

	assert.Equal(t, ToolCompleteIntent, def.Name)
	assert.Contains(t, def.Description, "intent")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "intent_id")
	assert.Contains(t, required, "success")

	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	// Check success is boolean
	success, ok := props["success"].(map[string]interface{})
	require.True(t, ok, "Should have success property")
	assert.Equal(t, "boolean", success["type"])
}

func TestGetConflictsDefinition(t *testing.T) {
	def := getConflictsDefinition()

	assert.Equal(t, ToolGetConflicts, def.Name)
	assert.Contains(t, def.Description, "conflict")

	// Check required fields
	required, ok := def.InputSchema["required"].([]string)
	require.True(t, ok, "Should have required fields")
	assert.Contains(t, required, "paths")

	props, ok := def.InputSchema["properties"].(map[string]interface{})
	require.True(t, ok, "Should have properties")

	// Check paths is array
	paths, ok := props["paths"].(map[string]interface{})
	require.True(t, ok, "Should have paths property")
	assert.Equal(t, "array", paths["type"])

	// Check check_intents is boolean
	checkIntents, ok := props["check_intents"].(map[string]interface{})
	require.True(t, ok, "Should have check_intents property")
	assert.Equal(t, "boolean", checkIntents["type"])
}

// =============================================================================
// Input Struct Tests
// =============================================================================

func TestGetBriefingInput_JSONTags(t *testing.T) {
	input := GetBriefingInput{Tier: "standard"}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "standard", restored["tier"])
}

func TestQueryPatternsInput_JSONTags(t *testing.T) {
	input := QueryPatternsInput{
		Category: "error.handling",
		Scope:    []string{"/src/", "/pkg/"},
		Limit:    10,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "error.handling", restored["category"])
	assert.NotNil(t, restored["scope"])
	assert.Equal(t, float64(10), restored["limit"])
}

func TestQueryPatternsInput_OptionalFields(t *testing.T) {
	input := QueryPatternsInput{Category: "test"}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	// Optional fields with omitempty should not be present when zero
	_, hasScope := restored["scope"]
	_, hasLimit := restored["limit"]

	// Both scope and limit have omitempty, so zero values are omitted
	assert.False(t, hasScope, "Empty scope should be omitted")
	assert.False(t, hasLimit, "Zero limit should be omitted (has omitempty)")
}

func TestQueryFailuresInput_JSONTags(t *testing.T) {
	input := QueryFailuresInput{
		ErrorType:   "compilation",
		FilePattern: "*.go",
		Limit:       5,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "compilation", restored["error_type"])
	assert.Equal(t, "*.go", restored["file_pattern"])
	assert.Equal(t, float64(5), restored["limit"])
}

func TestQueryContextInput_JSONTags(t *testing.T) {
	input := QueryContextInput{
		Query: "How do I handle errors?",
		Scope: "session",
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "How do I handle errors?", restored["query"])
	assert.Equal(t, "session", restored["scope"])
}

func TestQueryFileStateInput_JSONTags(t *testing.T) {
	input := QueryFileStateInput{
		Path:           "/src/main.go",
		IncludeHistory: true,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "/src/main.go", restored["path"])
	assert.Equal(t, true, restored["include_history"])
}

func TestRecordPatternInput_JSONTags(t *testing.T) {
	input := RecordPatternInput{
		Pattern:    "Use wrapped errors",
		Category:   "error.handling",
		Scope:      []string{"/src/"},
		Supersedes: []string{"pattern-123"},
		Reason:     "Better error context",
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "Use wrapped errors", restored["pattern"])
	assert.Equal(t, "error.handling", restored["category"])
	assert.NotNil(t, restored["scope"])
	assert.NotNil(t, restored["supersedes"])
	assert.Equal(t, "Better error context", restored["reason"])
}

func TestRecordFailureInput_JSONTags(t *testing.T) {
	input := RecordFailureInput{
		Error:      "nil pointer dereference",
		Context:    "Processing user input",
		Approach:   "Direct access without nil check",
		Resolution: "Add nil guard before access",
		Outcome:    "success",
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "nil pointer dereference", restored["error"])
	assert.Equal(t, "Processing user input", restored["context"])
	assert.Equal(t, "Direct access without nil check", restored["approach"])
	assert.Equal(t, "Add nil guard before access", restored["resolution"])
	assert.Equal(t, "success", restored["outcome"])
}

func TestUpdateFileStateInput_JSONTags(t *testing.T) {
	input := UpdateFileStateInput{
		Path:         "/src/handler.go",
		Action:       "modified",
		Summary:      "Added error handling",
		LinesChanged: "45-89",
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "/src/handler.go", restored["path"])
	assert.Equal(t, "modified", restored["action"])
	assert.Equal(t, "Added error handling", restored["summary"])
	assert.Equal(t, "45-89", restored["lines_changed"])
}

func TestDeclareIntentInput_JSONTags(t *testing.T) {
	input := DeclareIntentInput{
		Type:          "refactor",
		Description:   "Rename function for clarity",
		AffectedPaths: []string{"/src/handler.go", "/src/service.go"},
		AffectedAPIs:  []string{"ProcessRequest"},
		Priority:      "medium",
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "refactor", restored["type"])
	assert.Equal(t, "Rename function for clarity", restored["description"])
	assert.NotNil(t, restored["affected_paths"])
	assert.NotNil(t, restored["affected_apis"])
	assert.Equal(t, "medium", restored["priority"])
}

func TestCompleteIntentInput_JSONTags(t *testing.T) {
	input := CompleteIntentInput{
		IntentID:     "intent-123",
		Success:      true,
		FilesChanged: []string{"/src/handler.go"},
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "intent-123", restored["intent_id"])
	assert.Equal(t, true, restored["success"])
	assert.NotNil(t, restored["files_changed"])
}

func TestGetConflictsInput_JSONTags(t *testing.T) {
	input := GetConflictsInput{
		Paths:        []string{"/src/handler.go", "/src/service.go"},
		CheckIntents: true,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var restored map[string]interface{}
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.NotNil(t, restored["paths"])
	assert.Equal(t, true, restored["check_intents"])
}

// =============================================================================
// Schema Validation Tests
// =============================================================================

func TestToolDefinitions_ValidJSONSchema(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			// Should be able to marshal to JSON
			data, err := json.Marshal(def)
			require.NoError(t, err, "Should marshal to JSON")

			// Should be able to unmarshal back
			var restored ToolDefinition
			err = json.Unmarshal(data, &restored)
			require.NoError(t, err, "Should unmarshal from JSON")

			assert.Equal(t, def.Name, restored.Name)
		})
	}
}

func TestToolDefinitions_AllHavePropertiesField(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			props, ok := def.InputSchema["properties"]
			assert.True(t, ok, "Schema should have 'properties' field")
			assert.NotNil(t, props, "Properties should not be nil")
		})
	}
}

func TestToolDefinitions_PropertyTypesValid(t *testing.T) {
	validTypes := map[string]bool{
		"string":  true,
		"boolean": true,
		"integer": true,
		"number":  true,
		"array":   true,
		"object":  true,
	}

	defs := AllToolDefinitions()

	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			props, ok := def.InputSchema["properties"].(map[string]interface{})
			if !ok {
				return
			}

			for propName, propDef := range props {
				propMap, ok := propDef.(map[string]interface{})
				if !ok {
					continue
				}

				propType, hasType := propMap["type"].(string)
				if hasType {
					assert.True(t, validTypes[propType],
						"Property %s in %s has invalid type %s", propName, def.Name, propType)
				}
			}
		})
	}
}

// =============================================================================
// Edge Cases and Completeness Tests
// =============================================================================

func TestAllToolDefinitions_Idempotent(t *testing.T) {
	// Calling AllToolDefinitions multiple times should return equivalent results
	defs1 := AllToolDefinitions()
	defs2 := AllToolDefinitions()

	assert.Len(t, defs1, len(defs2), "Should return same number of definitions")

	for i := range defs1 {
		assert.Equal(t, defs1[i].Name, defs2[i].Name, "Names should match")
		assert.Equal(t, defs1[i].Description, defs2[i].Description, "Descriptions should match")
	}
}

func TestToolDefinitions_DescriptionsNotEmpty(t *testing.T) {
	defs := AllToolDefinitions()

	for _, def := range defs {
		assert.True(t, len(def.Description) > 10,
			"Tool %s should have a meaningful description (>10 chars)", def.Name)
	}
}

func TestInputStructs_ZeroValues(t *testing.T) {
	// Test that zero values can be serialized without issues
	inputs := []interface{}{
		GetBriefingInput{},
		QueryPatternsInput{},
		QueryFailuresInput{},
		QueryContextInput{},
		QueryFileStateInput{},
		RecordPatternInput{},
		RecordFailureInput{},
		UpdateFileStateInput{},
		DeclareIntentInput{},
		CompleteIntentInput{},
		GetConflictsInput{},
	}

	for _, input := range inputs {
		data, err := json.Marshal(input)
		assert.NoError(t, err, "Zero value should be serializable")
		assert.NotEmpty(t, data, "Serialized data should not be empty")
	}
}
