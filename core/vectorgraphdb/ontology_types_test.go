package vectorgraphdb

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// ConstraintType Tests
// =============================================================================

func TestConstraintType_String(t *testing.T) {
	tests := []struct {
		constraintType ConstraintType
		expected       string
	}{
		{ConstraintAllowedEdges, "allowed_edges"},
		{ConstraintRequiredProperties, "required_properties"},
		{ConstraintCardinality, "cardinality"},
		{ConstraintType(99), "constraint_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.constraintType.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.constraintType.String())
			}
		})
	}
}

func TestParseConstraintType(t *testing.T) {
	tests := []struct {
		input    string
		expected ConstraintType
		hasError bool
	}{
		{"allowed_edges", ConstraintAllowedEdges, false},
		{"required_properties", ConstraintRequiredProperties, false},
		{"cardinality", ConstraintCardinality, false},
		{"invalid", ConstraintType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseConstraintType(tt.input)
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

func TestConstraintType_IsValid(t *testing.T) {
	tests := []struct {
		constraintType ConstraintType
		expected       bool
	}{
		{ConstraintAllowedEdges, true},
		{ConstraintRequiredProperties, true},
		{ConstraintCardinality, true},
		{ConstraintType(-1), false},
		{ConstraintType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.constraintType.String(), func(t *testing.T) {
			if tt.constraintType.IsValid() != tt.expected {
				t.Errorf("IsValid() = %v, expected %v", tt.constraintType.IsValid(), tt.expected)
			}
		})
	}
}

func TestValidConstraintTypes(t *testing.T) {
	types := ValidConstraintTypes()
	if len(types) != 3 {
		t.Errorf("expected 3 constraint types, got %d", len(types))
	}

	for _, ct := range types {
		if !ct.IsValid() {
			t.Errorf("expected constraint type %v to be valid", ct)
		}
	}
}

func TestConstraintType_JSON(t *testing.T) {
	ct := ConstraintAllowedEdges

	data, err := json.Marshal(ct)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	expected := `"allowed_edges"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	var unmarshaled ConstraintType
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled != ct {
		t.Errorf("expected %v, got %v", ct, unmarshaled)
	}

	// Test integer unmarshaling
	var intUnmarshaled ConstraintType
	if err := json.Unmarshal([]byte("1"), &intUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal int: %v", err)
	}
	if intUnmarshaled != ConstraintRequiredProperties {
		t.Errorf("expected ConstraintRequiredProperties, got %v", intUnmarshaled)
	}

	// Test invalid string
	var invalidUnmarshaled ConstraintType
	err = json.Unmarshal([]byte(`"invalid"`), &invalidUnmarshaled)
	if err == nil {
		t.Error("expected error for invalid string")
	}
}

// =============================================================================
// OntologyClass Tests
// =============================================================================

func TestNewOntologyClass(t *testing.T) {
	class := NewOntologyClass("class-1", "TestClass", nil)

	if class.ID != "class-1" {
		t.Errorf("expected ID 'class-1', got %s", class.ID)
	}
	if class.Name != "TestClass" {
		t.Errorf("expected Name 'TestClass', got %s", class.Name)
	}
	if class.ParentID != nil {
		t.Error("expected ParentID to be nil for root class")
	}
	if class.Children != nil {
		t.Error("expected Children to be nil initially")
	}
	if class.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if class.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestNewOntologyClass_WithParent(t *testing.T) {
	parentID := "parent-1"
	class := NewOntologyClass("class-1", "ChildClass", &parentID)

	if class.ParentID == nil {
		t.Fatal("expected ParentID to be set")
	}
	if *class.ParentID != parentID {
		t.Errorf("expected ParentID %s, got %s", parentID, *class.ParentID)
	}
}

func TestOntologyClass_IsChildOf(t *testing.T) {
	parentID := "parent-1"
	class := NewOntologyClass("class-1", "ChildClass", &parentID)

	if !class.IsChildOf("parent-1") {
		t.Error("expected IsChildOf('parent-1') to return true")
	}
	if class.IsChildOf("other-parent") {
		t.Error("expected IsChildOf('other-parent') to return false")
	}

	// Root class is not a child of anything
	rootClass := NewOntologyClass("root", "RootClass", nil)
	if rootClass.IsChildOf("any") {
		t.Error("expected root class IsChildOf to return false")
	}
}

func TestOntologyClass_AddChild(t *testing.T) {
	parent := NewOntologyClass("parent-1", "ParentClass", nil)
	child := NewOntologyClass("child-1", "ChildClass", nil)

	originalUpdatedAt := parent.UpdatedAt
	time.Sleep(time.Millisecond)

	parent.AddChild(child)

	if len(parent.Children) != 1 {
		t.Errorf("expected 1 child, got %d", len(parent.Children))
	}
	if parent.Children[0] != child {
		t.Error("expected child to be added to parent")
	}
	if child.ParentID == nil || *child.ParentID != "parent-1" {
		t.Error("expected child's ParentID to be set to parent's ID")
	}
	if !parent.UpdatedAt.After(originalUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}

	// Adding nil should not panic
	parent.AddChild(nil)
	if len(parent.Children) != 1 {
		t.Error("expected nil child to be ignored")
	}
}

func TestOntologyClass_FindChild(t *testing.T) {
	parent := NewOntologyClass("parent-1", "ParentClass", nil)
	child1 := NewOntologyClass("child-1", "ChildClass1", nil)
	child2 := NewOntologyClass("child-2", "ChildClass2", nil)

	parent.AddChild(child1)
	parent.AddChild(child2)

	found := parent.FindChild("child-1")
	if found == nil {
		t.Fatal("expected to find child-1")
	}
	if found.ID != "child-1" {
		t.Errorf("expected child-1, got %s", found.ID)
	}

	notFound := parent.FindChild("nonexistent")
	if notFound != nil {
		t.Error("expected nil for nonexistent child")
	}
}

func TestOntologyClass_FindChildRecursive(t *testing.T) {
	root := NewOntologyClass("root", "Root", nil)
	child := NewOntologyClass("child", "Child", nil)
	grandchild := NewOntologyClass("grandchild", "Grandchild", nil)

	root.AddChild(child)
	child.AddChild(grandchild)

	// Find direct child
	found := root.FindChildRecursive("child")
	if found == nil || found.ID != "child" {
		t.Error("expected to find direct child")
	}

	// Find grandchild
	found = root.FindChildRecursive("grandchild")
	if found == nil || found.ID != "grandchild" {
		t.Error("expected to find grandchild recursively")
	}

	// Not found
	found = root.FindChildRecursive("nonexistent")
	if found != nil {
		t.Error("expected nil for nonexistent descendant")
	}
}

func TestOntologyClass_IsRoot(t *testing.T) {
	root := NewOntologyClass("root", "Root", nil)
	if !root.IsRoot() {
		t.Error("expected root class to be root")
	}

	parentID := "parent"
	child := NewOntologyClass("child", "Child", &parentID)
	if child.IsRoot() {
		t.Error("expected child class to not be root")
	}
}

func TestOntologyClass_ChildCount(t *testing.T) {
	parent := NewOntologyClass("parent", "Parent", nil)

	if parent.ChildCount() != 0 {
		t.Errorf("expected 0 children, got %d", parent.ChildCount())
	}

	parent.AddChild(NewOntologyClass("c1", "C1", nil))
	parent.AddChild(NewOntologyClass("c2", "C2", nil))

	if parent.ChildCount() != 2 {
		t.Errorf("expected 2 children, got %d", parent.ChildCount())
	}
}

func TestOntologyClass_AllDescendants(t *testing.T) {
	root := NewOntologyClass("root", "Root", nil)
	child1 := NewOntologyClass("child1", "Child1", nil)
	child2 := NewOntologyClass("child2", "Child2", nil)
	grandchild1 := NewOntologyClass("grandchild1", "Grandchild1", nil)
	grandchild2 := NewOntologyClass("grandchild2", "Grandchild2", nil)

	root.AddChild(child1)
	root.AddChild(child2)
	child1.AddChild(grandchild1)
	child2.AddChild(grandchild2)

	descendants := root.AllDescendants()
	if len(descendants) != 4 {
		t.Errorf("expected 4 descendants, got %d", len(descendants))
	}

	// Verify all descendants are present
	ids := make(map[string]bool)
	for _, d := range descendants {
		ids[d.ID] = true
	}
	for _, expected := range []string{"child1", "child2", "grandchild1", "grandchild2"} {
		if !ids[expected] {
			t.Errorf("expected %s in descendants", expected)
		}
	}
}

func TestOntologyClass_GetAllowedEdges(t *testing.T) {
	class := NewOntologyClass("class-1", "TestClass", nil)

	// Without any constraints, should return nil
	edges := class.GetAllowedEdges()
	if edges != nil {
		t.Error("expected nil for class without constraints")
	}
}

func TestOntologyClass_JSON(t *testing.T) {
	parentID := "parent-1"
	class := &OntologyClass{
		ID:          "class-1",
		Name:        "TestClass",
		ParentID:    &parentID,
		Description: "A test class",
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(class)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled OntologyClass
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.ID != class.ID {
		t.Errorf("expected ID %s, got %s", class.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != class.Name {
		t.Errorf("expected Name %s, got %s", class.Name, unmarshaled.Name)
	}
	if unmarshaled.ParentID == nil || *unmarshaled.ParentID != *class.ParentID {
		t.Errorf("expected ParentID %s, got %v", *class.ParentID, unmarshaled.ParentID)
	}
	if unmarshaled.Description != class.Description {
		t.Errorf("expected Description %s, got %s", class.Description, unmarshaled.Description)
	}
}

// =============================================================================
// OntologyConstraint Tests
// =============================================================================

func TestNewOntologyConstraint(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintAllowedEdges, `["calls", "imports"]`)

	if constraint.ID != "const-1" {
		t.Errorf("expected ID 'const-1', got %s", constraint.ID)
	}
	if constraint.ClassID != "class-1" {
		t.Errorf("expected ClassID 'class-1', got %s", constraint.ClassID)
	}
	if constraint.Type != ConstraintAllowedEdges {
		t.Errorf("expected Type ConstraintAllowedEdges, got %v", constraint.Type)
	}
	if constraint.Value != `["calls", "imports"]` {
		t.Errorf("expected Value '[\"calls\", \"imports\"]', got %s", constraint.Value)
	}
}

func TestOntologyConstraint_GetAllowedEdges(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintAllowedEdges, `["calls", "imports", "implements"]`)

	edges := constraint.GetAllowedEdges()
	if edges == nil {
		t.Fatal("expected edges to be returned")
	}
	if len(edges) != 3 {
		t.Errorf("expected 3 edges, got %d", len(edges))
	}
	expected := []string{"calls", "imports", "implements"}
	for i, e := range expected {
		if edges[i] != e {
			t.Errorf("expected edge %s at index %d, got %s", e, i, edges[i])
		}
	}

	// Wrong type should return nil
	wrongType := NewOntologyConstraint("const-2", "class-1", ConstraintCardinality, `{"min": 0, "max": 10}`)
	if wrongType.GetAllowedEdges() != nil {
		t.Error("expected nil for wrong constraint type")
	}

	// Invalid JSON should return nil
	invalidJSON := NewOntologyConstraint("const-3", "class-1", ConstraintAllowedEdges, `invalid`)
	if invalidJSON.GetAllowedEdges() != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestOntologyConstraint_GetRequiredProperties(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintRequiredProperties, `["name", "type", "description"]`)

	props := constraint.GetRequiredProperties()
	if props == nil {
		t.Fatal("expected properties to be returned")
	}
	if len(props) != 3 {
		t.Errorf("expected 3 properties, got %d", len(props))
	}

	// Wrong type should return nil
	wrongType := NewOntologyConstraint("const-2", "class-1", ConstraintAllowedEdges, `["calls"]`)
	if wrongType.GetRequiredProperties() != nil {
		t.Error("expected nil for wrong constraint type")
	}
}

func TestOntologyConstraint_GetCardinality(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintCardinality, `{"min": 1, "max": 10}`)

	cardinality := constraint.GetCardinality()
	if cardinality == nil {
		t.Fatal("expected cardinality to be returned")
	}
	if cardinality.Min != 1 {
		t.Errorf("expected Min 1, got %d", cardinality.Min)
	}
	if cardinality.Max != 10 {
		t.Errorf("expected Max 10, got %d", cardinality.Max)
	}

	// Test unlimited max (-1)
	unlimited := NewOntologyConstraint("const-2", "class-1", ConstraintCardinality, `{"min": 0, "max": -1}`)
	card := unlimited.GetCardinality()
	if card == nil || card.Max != -1 {
		t.Error("expected unlimited max (-1)")
	}

	// Wrong type should return nil
	wrongType := NewOntologyConstraint("const-3", "class-1", ConstraintAllowedEdges, `["calls"]`)
	if wrongType.GetCardinality() != nil {
		t.Error("expected nil for wrong constraint type")
	}
}

func TestOntologyConstraint_SetAllowedEdges(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintCardinality, `{}`)

	err := constraint.SetAllowedEdges([]string{"calls", "imports"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if constraint.Type != ConstraintAllowedEdges {
		t.Errorf("expected Type to be ConstraintAllowedEdges, got %v", constraint.Type)
	}

	edges := constraint.GetAllowedEdges()
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestOntologyConstraint_SetRequiredProperties(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintCardinality, `{}`)

	err := constraint.SetRequiredProperties([]string{"name", "type"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if constraint.Type != ConstraintRequiredProperties {
		t.Errorf("expected Type to be ConstraintRequiredProperties, got %v", constraint.Type)
	}

	props := constraint.GetRequiredProperties()
	if len(props) != 2 {
		t.Errorf("expected 2 properties, got %d", len(props))
	}
}

func TestOntologyConstraint_SetCardinality(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintAllowedEdges, `[]`)

	err := constraint.SetCardinality(1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if constraint.Type != ConstraintCardinality {
		t.Errorf("expected Type to be ConstraintCardinality, got %v", constraint.Type)
	}

	cardinality := constraint.GetCardinality()
	if cardinality.Min != 1 || cardinality.Max != 5 {
		t.Errorf("expected cardinality {1, 5}, got {%d, %d}", cardinality.Min, cardinality.Max)
	}
}

func TestOntologyConstraint_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		const_   *OntologyConstraint
		expected bool
	}{
		{
			name:     "valid allowed edges",
			const_:   NewOntologyConstraint("c1", "class-1", ConstraintAllowedEdges, `["calls"]`),
			expected: true,
		},
		{
			name:     "valid required properties",
			const_:   NewOntologyConstraint("c2", "class-1", ConstraintRequiredProperties, `["name"]`),
			expected: true,
		},
		{
			name:     "valid cardinality",
			const_:   NewOntologyConstraint("c3", "class-1", ConstraintCardinality, `{"min": 0, "max": 10}`),
			expected: true,
		},
		{
			name:     "empty ID",
			const_:   NewOntologyConstraint("", "class-1", ConstraintAllowedEdges, `["calls"]`),
			expected: false,
		},
		{
			name:     "empty ClassID",
			const_:   NewOntologyConstraint("c1", "", ConstraintAllowedEdges, `["calls"]`),
			expected: false,
		},
		{
			name:     "empty Value",
			const_:   NewOntologyConstraint("c1", "class-1", ConstraintAllowedEdges, ""),
			expected: false,
		},
		{
			name:     "invalid JSON",
			const_:   NewOntologyConstraint("c1", "class-1", ConstraintAllowedEdges, "invalid"),
			expected: false,
		},
		{
			name:     "invalid constraint type",
			const_:   NewOntologyConstraint("c1", "class-1", ConstraintType(99), `["calls"]`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.const_.IsValid() != tt.expected {
				t.Errorf("IsValid() = %v, expected %v", tt.const_.IsValid(), tt.expected)
			}
		})
	}
}

func TestOntologyConstraint_JSON(t *testing.T) {
	constraint := NewOntologyConstraint("const-1", "class-1", ConstraintAllowedEdges, `["calls", "imports"]`)

	data, err := json.Marshal(constraint)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var unmarshaled OntologyConstraint
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if unmarshaled.ID != constraint.ID {
		t.Errorf("expected ID %s, got %s", constraint.ID, unmarshaled.ID)
	}
	if unmarshaled.ClassID != constraint.ClassID {
		t.Errorf("expected ClassID %s, got %s", constraint.ClassID, unmarshaled.ClassID)
	}
	if unmarshaled.Type != constraint.Type {
		t.Errorf("expected Type %v, got %v", constraint.Type, unmarshaled.Type)
	}
	if unmarshaled.Value != constraint.Value {
		t.Errorf("expected Value %s, got %s", constraint.Value, unmarshaled.Value)
	}
}

// =============================================================================
// OntologyClass Full Workflow Tests
// =============================================================================

func TestOntologyClass_FullWorkflow(t *testing.T) {
	// Create a class hierarchy: Entity -> Person -> Developer
	entityClass := NewOntologyClass("entity", "Entity", nil)
	entityClass.Description = "Base entity class"

	personClass := NewOntologyClass("person", "Person", nil)
	personClass.Description = "A person entity"

	developerClass := NewOntologyClass("developer", "Developer", nil)
	developerClass.Description = "A developer (software engineer)"

	// Build hierarchy
	entityClass.AddChild(personClass)
	personClass.AddChild(developerClass)

	// Verify hierarchy
	if !entityClass.IsRoot() {
		t.Error("expected Entity to be root")
	}
	if personClass.IsRoot() {
		t.Error("expected Person to not be root")
	}
	if developerClass.IsRoot() {
		t.Error("expected Developer to not be root")
	}

	// Verify parent relationships
	if !personClass.IsChildOf("entity") {
		t.Error("expected Person to be child of Entity")
	}
	if !developerClass.IsChildOf("person") {
		t.Error("expected Developer to be child of Person")
	}

	// Find descendants
	found := entityClass.FindChildRecursive("developer")
	if found == nil {
		t.Error("expected to find Developer in Entity's descendants")
	}

	// Count descendants
	descendants := entityClass.AllDescendants()
	if len(descendants) != 2 { // Person and Developer
		t.Errorf("expected 2 descendants, got %d", len(descendants))
	}
}
