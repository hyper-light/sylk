package knowledge

import (
	"encoding/json"
	"testing"
)

// =============================================================================
// EntityKind Tests
// =============================================================================

func TestEntityKind_String(t *testing.T) {
	tests := []struct {
		kind     EntityKind
		expected string
	}{
		{EntityKindFunction, "function"},
		{EntityKindType, "type"},
		{EntityKindVariable, "variable"},
		{EntityKindImport, "import"},
		{EntityKindConstant, "constant"},
		{EntityKindInterface, "interface"},
		{EntityKindMethod, "method"},
		{EntityKindStruct, "struct"},
		{EntityKindPackage, "package"},
		{EntityKindFile, "file"},
		{EntityKind(999), "entity_kind(999)"},
	}

	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.expected {
			t.Errorf("EntityKind(%d).String() = %q, want %q", tt.kind, got, tt.expected)
		}
	}
}

func TestParseEntityKind(t *testing.T) {
	tests := []struct {
		input    string
		expected EntityKind
		ok       bool
	}{
		{"function", EntityKindFunction, true},
		{"type", EntityKindType, true},
		{"variable", EntityKindVariable, true},
		{"import", EntityKindImport, true},
		{"constant", EntityKindConstant, true},
		{"interface", EntityKindInterface, true},
		{"method", EntityKindMethod, true},
		{"struct", EntityKindStruct, true},
		{"package", EntityKindPackage, true},
		{"file", EntityKindFile, true},
		{"invalid", EntityKind(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseEntityKind(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseEntityKind(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseEntityKind(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestEntityKind_IsValid(t *testing.T) {
	tests := []struct {
		kind  EntityKind
		valid bool
	}{
		{EntityKindFunction, true},
		{EntityKindFile, true},
		{EntityKind(999), false},
		{EntityKind(-1), false},
	}

	for _, tt := range tests {
		if got := tt.kind.IsValid(); got != tt.valid {
			t.Errorf("EntityKind(%d).IsValid() = %v, want %v", tt.kind, got, tt.valid)
		}
	}
}

func TestEntityKind_JSON(t *testing.T) {
	tests := []struct {
		kind     EntityKind
		expected string
	}{
		{EntityKindFunction, `"function"`},
		{EntityKindMethod, `"method"`},
		{EntityKindStruct, `"struct"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.kind)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.kind, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.kind, data, tt.expected)
		}

		var kind EntityKind
		if err := json.Unmarshal(data, &kind); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if kind != tt.kind {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, kind, tt.kind)
		}
	}
}

// =============================================================================
// EntityScope Tests
// =============================================================================

func TestEntityScope_String(t *testing.T) {
	tests := []struct {
		scope    EntityScope
		expected string
	}{
		{ScopeGlobal, "global"},
		{ScopeModule, "module"},
		{ScopeFunction, "function"},
		{ScopeBlock, "block"},
		{EntityScope(999), "scope(999)"},
	}

	for _, tt := range tests {
		if got := tt.scope.String(); got != tt.expected {
			t.Errorf("EntityScope(%d).String() = %q, want %q", tt.scope, got, tt.expected)
		}
	}
}

func TestParseEntityScope(t *testing.T) {
	tests := []struct {
		input    string
		expected EntityScope
		ok       bool
	}{
		{"global", ScopeGlobal, true},
		{"module", ScopeModule, true},
		{"function", ScopeFunction, true},
		{"block", ScopeBlock, true},
		{"invalid", EntityScope(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseEntityScope(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseEntityScope(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseEntityScope(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestEntityScope_JSON(t *testing.T) {
	tests := []struct {
		scope    EntityScope
		expected string
	}{
		{ScopeGlobal, `"global"`},
		{ScopeModule, `"module"`},
		{ScopeFunction, `"function"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.scope)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.scope, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.scope, data, tt.expected)
		}

		var scope EntityScope
		if err := json.Unmarshal(data, &scope); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if scope != tt.scope {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, scope, tt.scope)
		}
	}
}

// =============================================================================
// EntityVisibility Tests
// =============================================================================

func TestEntityVisibility_String(t *testing.T) {
	tests := []struct {
		visibility EntityVisibility
		expected   string
	}{
		{VisibilityPublic, "public"},
		{VisibilityPrivate, "private"},
		{VisibilityInternal, "internal"},
		{EntityVisibility(999), "visibility(999)"},
	}

	for _, tt := range tests {
		if got := tt.visibility.String(); got != tt.expected {
			t.Errorf("EntityVisibility(%d).String() = %q, want %q", tt.visibility, got, tt.expected)
		}
	}
}

func TestParseEntityVisibility(t *testing.T) {
	tests := []struct {
		input    string
		expected EntityVisibility
		ok       bool
	}{
		{"public", VisibilityPublic, true},
		{"private", VisibilityPrivate, true},
		{"internal", VisibilityInternal, true},
		{"invalid", EntityVisibility(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseEntityVisibility(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseEntityVisibility(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseEntityVisibility(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestEntityVisibility_JSON(t *testing.T) {
	tests := []struct {
		visibility EntityVisibility
		expected   string
	}{
		{VisibilityPublic, `"public"`},
		{VisibilityPrivate, `"private"`},
		{VisibilityInternal, `"internal"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.visibility)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.visibility, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.visibility, data, tt.expected)
		}

		var visibility EntityVisibility
		if err := json.Unmarshal(data, &visibility); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if visibility != tt.visibility {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, visibility, tt.visibility)
		}
	}
}

// =============================================================================
// ReferenceKind Tests
// =============================================================================

func TestReferenceKind_String(t *testing.T) {
	tests := []struct {
		kind     ReferenceKind
		expected string
	}{
		{RefCall, "call"},
		{RefRead, "read"},
		{RefWrite, "write"},
		{RefType, "type"},
		{ReferenceKind(999), "reference_kind(999)"},
	}

	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.expected {
			t.Errorf("ReferenceKind(%d).String() = %q, want %q", tt.kind, got, tt.expected)
		}
	}
}

func TestParseReferenceKind(t *testing.T) {
	tests := []struct {
		input    string
		expected ReferenceKind
		ok       bool
	}{
		{"call", RefCall, true},
		{"read", RefRead, true},
		{"write", RefWrite, true},
		{"type", RefType, true},
		{"invalid", ReferenceKind(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseReferenceKind(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseReferenceKind(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseReferenceKind(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestReferenceKind_JSON(t *testing.T) {
	tests := []struct {
		kind     ReferenceKind
		expected string
	}{
		{RefCall, `"call"`},
		{RefRead, `"read"`},
		{RefWrite, `"write"`},
		{RefType, `"type"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.kind)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.kind, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.kind, data, tt.expected)
		}

		var kind ReferenceKind
		if err := json.Unmarshal(data, &kind); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if kind != tt.kind {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, kind, tt.kind)
		}
	}
}

// =============================================================================
// EntityReference Tests
// =============================================================================

func TestEntityReference_JSON(t *testing.T) {
	ref := EntityReference{
		FilePath: "/path/to/file.go",
		Line:     42,
		Kind:     RefCall,
	}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("json.Marshal(EntityReference) error = %v", err)
	}

	var decoded EntityReference
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(EntityReference) error = %v", err)
	}

	if decoded.FilePath != ref.FilePath {
		t.Errorf("FilePath = %q, want %q", decoded.FilePath, ref.FilePath)
	}
	if decoded.Line != ref.Line {
		t.Errorf("Line = %d, want %d", decoded.Line, ref.Line)
	}
	if decoded.Kind != ref.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, ref.Kind)
	}
}

// =============================================================================
// ExtractedEntity Tests
// =============================================================================

func TestExtractedEntity_GoFunction(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "ProcessData",
		Kind:       EntityKindFunction,
		FilePath:   "/path/to/file.go",
		StartLine:  10,
		EndLine:    50,
		Signature:  "func ProcessData(input string) error",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/caller.go", Line: 25, Kind: RefCall},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
	if decoded.Kind != entity.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, entity.Kind)
	}
	if decoded.FilePath != entity.FilePath {
		t.Errorf("FilePath = %q, want %q", decoded.FilePath, entity.FilePath)
	}
	if decoded.StartLine != entity.StartLine {
		t.Errorf("StartLine = %d, want %d", decoded.StartLine, entity.StartLine)
	}
	if decoded.EndLine != entity.EndLine {
		t.Errorf("EndLine = %d, want %d", decoded.EndLine, entity.EndLine)
	}
	if decoded.Signature != entity.Signature {
		t.Errorf("Signature = %q, want %q", decoded.Signature, entity.Signature)
	}
	if decoded.Scope != entity.Scope {
		t.Errorf("Scope = %v, want %v", decoded.Scope, entity.Scope)
	}
	if decoded.Visibility != entity.Visibility {
		t.Errorf("Visibility = %v, want %v", decoded.Visibility, entity.Visibility)
	}
	if len(decoded.References) != len(entity.References) {
		t.Errorf("len(References) = %d, want %d", len(decoded.References), len(entity.References))
	}
}

func TestExtractedEntity_TypeScriptInterface(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "UserInterface",
		Kind:       EntityKindInterface,
		FilePath:   "/path/to/types.ts",
		StartLine:  5,
		EndLine:    15,
		Signature:  "interface UserInterface { name: string; age: number; }",
		Scope:      ScopeModule,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/user.ts", Line: 10, Kind: RefType},
			{FilePath: "/path/to/auth.ts", Line: 20, Kind: RefType},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
	if decoded.Kind != entity.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, entity.Kind)
	}
}

func TestExtractedEntity_PythonClass(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "DataProcessor",
		Kind:       EntityKindType,
		FilePath:   "/path/to/processor.py",
		StartLine:  1,
		EndLine:    100,
		Signature:  "class DataProcessor:",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/main.py", Line: 15, Kind: RefType},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
	if decoded.Kind != entity.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, entity.Kind)
	}
}

func TestExtractedEntity_GoStruct(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "User",
		Kind:       EntityKindStruct,
		FilePath:   "/path/to/models.go",
		StartLine:  20,
		EndLine:    30,
		Signature:  "type User struct { Name string; Email string }",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/handler.go", Line: 45, Kind: RefType},
			{FilePath: "/path/to/service.go", Line: 60, Kind: RefType},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
	if decoded.Kind != entity.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, entity.Kind)
	}
	if len(decoded.References) != 2 {
		t.Errorf("len(References) = %d, want 2", len(decoded.References))
	}
}

func TestExtractedEntity_TypeScriptFunction(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "calculateTotal",
		Kind:       EntityKindFunction,
		FilePath:   "/path/to/utils.ts",
		StartLine:  10,
		EndLine:    25,
		Signature:  "function calculateTotal(items: Item[]): number",
		Scope:      ScopeModule,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/cart.ts", Line: 30, Kind: RefCall},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
}

func TestExtractedEntity_PythonMethod(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "process",
		Kind:       EntityKindMethod,
		FilePath:   "/path/to/processor.py",
		StartLine:  50,
		EndLine:    75,
		Signature:  "def process(self, data: Dict[str, Any]) -> Result:",
		Scope:      ScopeFunction,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/main.py", Line: 100, Kind: RefCall},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Name != entity.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entity.Name)
	}
	if decoded.Kind != entity.Kind {
		t.Errorf("Kind = %v, want %v", decoded.Kind, entity.Kind)
	}
}

func TestExtractedEntity_GoVariable(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "defaultTimeout",
		Kind:       EntityKindVariable,
		FilePath:   "/path/to/config.go",
		StartLine:  8,
		EndLine:    8,
		Signature:  "var defaultTimeout = 30 * time.Second",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPrivate,
		References: []EntityReference{
			{FilePath: "/path/to/client.go", Line: 50, Kind: RefRead},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Visibility != VisibilityPrivate {
		t.Errorf("Visibility = %v, want %v", decoded.Visibility, VisibilityPrivate)
	}
}

func TestExtractedEntity_GoConstant(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "MaxRetries",
		Kind:       EntityKindConstant,
		FilePath:   "/path/to/constants.go",
		StartLine:  5,
		EndLine:    5,
		Signature:  "const MaxRetries = 3",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPublic,
		References: []EntityReference{
			{FilePath: "/path/to/retry.go", Line: 20, Kind: RefRead},
			{FilePath: "/path/to/config.go", Line: 15, Kind: RefRead},
		},
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.Kind != EntityKindConstant {
		t.Errorf("Kind = %v, want %v", decoded.Kind, EntityKindConstant)
	}
}

func TestExtractedEntity_WithoutReferences(t *testing.T) {
	entity := ExtractedEntity{
		Name:       "unusedFunction",
		Kind:       EntityKindFunction,
		FilePath:   "/path/to/unused.go",
		StartLine:  10,
		EndLine:    20,
		Signature:  "func unusedFunction() {}",
		Scope:      ScopeGlobal,
		Visibility: VisibilityPrivate,
		References: nil,
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("json.Marshal(ExtractedEntity) error = %v", err)
	}

	var decoded ExtractedEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ExtractedEntity) error = %v", err)
	}

	if decoded.References != nil && len(decoded.References) > 0 {
		t.Errorf("References should be nil or empty, got %v", decoded.References)
	}
}
