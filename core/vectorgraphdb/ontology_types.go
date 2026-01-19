package vectorgraphdb

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Ontology Types (KG.2.3)
// =============================================================================

// ConstraintType represents the type of constraint applied to an ontology class.
type ConstraintType int

const (
	// ConstraintAllowedEdges specifies which edge types are allowed for this class.
	ConstraintAllowedEdges ConstraintType = 0

	// ConstraintRequiredProperties specifies which properties must be present.
	ConstraintRequiredProperties ConstraintType = 1

	// ConstraintCardinality specifies cardinality constraints for relationships.
	ConstraintCardinality ConstraintType = 2
)

// String returns the string representation of the ConstraintType.
func (ct ConstraintType) String() string {
	switch ct {
	case ConstraintAllowedEdges:
		return "allowed_edges"
	case ConstraintRequiredProperties:
		return "required_properties"
	case ConstraintCardinality:
		return "cardinality"
	default:
		return fmt.Sprintf("constraint_type(%d)", ct)
	}
}

// ParseConstraintType parses a string into a ConstraintType.
func ParseConstraintType(s string) (ConstraintType, error) {
	switch s {
	case "allowed_edges":
		return ConstraintAllowedEdges, nil
	case "required_properties":
		return ConstraintRequiredProperties, nil
	case "cardinality":
		return ConstraintCardinality, nil
	default:
		return ConstraintType(0), fmt.Errorf("unknown constraint type: %s", s)
	}
}

// IsValid returns true if the constraint type is a recognized value.
func (ct ConstraintType) IsValid() bool {
	return ct >= ConstraintAllowedEdges && ct <= ConstraintCardinality
}

// ValidConstraintTypes returns all valid ConstraintType values.
func ValidConstraintTypes() []ConstraintType {
	return []ConstraintType{
		ConstraintAllowedEdges,
		ConstraintRequiredProperties,
		ConstraintCardinality,
	}
}

// MarshalJSON implements json.Marshaler for ConstraintType.
func (ct ConstraintType) MarshalJSON() ([]byte, error) {
	return json.Marshal(ct.String())
}

// UnmarshalJSON implements json.Unmarshaler for ConstraintType.
func (ct *ConstraintType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		parsed, err := ParseConstraintType(asString)
		if err != nil {
			return err
		}
		*ct = parsed
		return nil
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*ct = ConstraintType(asInt)
		return nil
	}

	return fmt.Errorf("invalid constraint type")
}

// =============================================================================
// OntologyClass
// =============================================================================

// OntologyClass represents a class in the knowledge graph ontology.
// Classes form a hierarchical structure where child classes inherit
// constraints from their parents.
type OntologyClass struct {
	// ID is the unique identifier for this class.
	ID string `json:"id"`

	// Name is the human-readable name of this class.
	Name string `json:"name"`

	// ParentID is the ID of the parent class, or nil if this is a root class.
	ParentID *string `json:"parent_id,omitempty"`

	// Description provides additional context about this class.
	Description string `json:"description,omitempty"`

	// Children holds child classes for in-memory tree navigation.
	// This field is not persisted to the database.
	Children []*OntologyClass `json:"-"`

	// CreatedAt is when this class was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when this class was last updated.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewOntologyClass creates a new OntologyClass with the specified values.
func NewOntologyClass(id, name string, parentID *string) *OntologyClass {
	now := time.Now()
	return &OntologyClass{
		ID:        id,
		Name:      name,
		ParentID:  parentID,
		Children:  nil,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// IsChildOf checks if this class is a child (direct or indirect) of the
// specified class ID by traversing up the hierarchy.
// Note: This method requires the parent chain to be populated in memory.
// For database-level hierarchy checks, use a recursive query.
func (c *OntologyClass) IsChildOf(classID string) bool {
	if c.ParentID == nil {
		return false
	}
	if *c.ParentID == classID {
		return true
	}
	// For in-memory hierarchy traversal, we would need the parent object.
	// This implementation only checks direct parent. Full hierarchy check
	// requires either parent reference or database query.
	return false
}

// AddChild adds a child class to this class's children list.
func (c *OntologyClass) AddChild(child *OntologyClass) {
	if child == nil {
		return
	}
	// Set the child's parent ID to this class's ID
	parentID := c.ID
	child.ParentID = &parentID
	c.Children = append(c.Children, child)
	c.UpdatedAt = time.Now()
}

// FindChild finds a child class by ID (direct children only).
// Returns nil if no child with the given ID is found.
func (c *OntologyClass) FindChild(id string) *OntologyClass {
	for _, child := range c.Children {
		if child.ID == id {
			return child
		}
	}
	return nil
}

// FindChildRecursive finds a child class by ID recursively through all descendants.
// Returns nil if no matching class is found.
func (c *OntologyClass) FindChildRecursive(id string) *OntologyClass {
	for _, child := range c.Children {
		if child.ID == id {
			return child
		}
		if found := child.FindChildRecursive(id); found != nil {
			return found
		}
	}
	return nil
}

// GetAllowedEdges parses and returns the allowed edge types from an
// OntologyConstraint's Value field when the constraint type is ConstraintAllowedEdges.
// Note: This is a convenience method that expects a constraint to be associated.
// For actual constraint management, use the OntologyConstraint type.
func (c *OntologyClass) GetAllowedEdges() []string {
	// This is a placeholder implementation. The actual allowed edges would
	// typically come from associated OntologyConstraint records.
	// This method is kept for interface compatibility.
	return nil
}

// IsRoot returns true if this class has no parent (is a root class).
func (c *OntologyClass) IsRoot() bool {
	return c.ParentID == nil
}

// ChildCount returns the number of direct children.
func (c *OntologyClass) ChildCount() int {
	return len(c.Children)
}

// AllDescendants returns all descendant classes (children, grandchildren, etc.).
func (c *OntologyClass) AllDescendants() []*OntologyClass {
	var descendants []*OntologyClass
	for _, child := range c.Children {
		descendants = append(descendants, child)
		descendants = append(descendants, child.AllDescendants()...)
	}
	return descendants
}

// =============================================================================
// OntologyConstraint
// =============================================================================

// OntologyConstraint represents a constraint applied to an ontology class.
// Constraints define rules about what properties, edges, or cardinalities
// are valid for instances of the class.
type OntologyConstraint struct {
	// ID is the unique identifier for this constraint.
	ID string `json:"id"`

	// ClassID is the ID of the ontology class this constraint applies to.
	ClassID string `json:"class_id"`

	// Type is the type of constraint.
	Type ConstraintType `json:"type"`

	// Value is the JSON-encoded constraint value.
	// For ConstraintAllowedEdges: ["edge_type1", "edge_type2", ...]
	// For ConstraintRequiredProperties: ["prop1", "prop2", ...]
	// For ConstraintCardinality: {"min": 0, "max": 10}
	Value string `json:"value"`
}

// NewOntologyConstraint creates a new OntologyConstraint.
func NewOntologyConstraint(id, classID string, constraintType ConstraintType, value string) *OntologyConstraint {
	return &OntologyConstraint{
		ID:      id,
		ClassID: classID,
		Type:    constraintType,
		Value:   value,
	}
}

// GetAllowedEdges parses the Value field as a list of allowed edge types.
// Returns nil if the constraint is not of type ConstraintAllowedEdges or
// if the value cannot be parsed.
func (oc *OntologyConstraint) GetAllowedEdges() []string {
	if oc.Type != ConstraintAllowedEdges {
		return nil
	}
	var edges []string
	if err := json.Unmarshal([]byte(oc.Value), &edges); err != nil {
		return nil
	}
	return edges
}

// GetRequiredProperties parses the Value field as a list of required properties.
// Returns nil if the constraint is not of type ConstraintRequiredProperties or
// if the value cannot be parsed.
func (oc *OntologyConstraint) GetRequiredProperties() []string {
	if oc.Type != ConstraintRequiredProperties {
		return nil
	}
	var props []string
	if err := json.Unmarshal([]byte(oc.Value), &props); err != nil {
		return nil
	}
	return props
}

// CardinalityConstraint represents min/max cardinality values.
type CardinalityConstraint struct {
	Min int `json:"min"`
	Max int `json:"max"` // -1 indicates unlimited
}

// GetCardinality parses the Value field as a cardinality constraint.
// Returns nil if the constraint is not of type ConstraintCardinality or
// if the value cannot be parsed.
func (oc *OntologyConstraint) GetCardinality() *CardinalityConstraint {
	if oc.Type != ConstraintCardinality {
		return nil
	}
	var cardinality CardinalityConstraint
	if err := json.Unmarshal([]byte(oc.Value), &cardinality); err != nil {
		return nil
	}
	return &cardinality
}

// SetAllowedEdges sets the Value field to a JSON array of allowed edge types.
func (oc *OntologyConstraint) SetAllowedEdges(edges []string) error {
	oc.Type = ConstraintAllowedEdges
	data, err := json.Marshal(edges)
	if err != nil {
		return fmt.Errorf("failed to encode allowed edges: %w", err)
	}
	oc.Value = string(data)
	return nil
}

// SetRequiredProperties sets the Value field to a JSON array of required properties.
func (oc *OntologyConstraint) SetRequiredProperties(props []string) error {
	oc.Type = ConstraintRequiredProperties
	data, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("failed to encode required properties: %w", err)
	}
	oc.Value = string(data)
	return nil
}

// SetCardinality sets the Value field to a JSON object with min/max cardinality.
func (oc *OntologyConstraint) SetCardinality(min, max int) error {
	oc.Type = ConstraintCardinality
	cardinality := CardinalityConstraint{Min: min, Max: max}
	data, err := json.Marshal(cardinality)
	if err != nil {
		return fmt.Errorf("failed to encode cardinality: %w", err)
	}
	oc.Value = string(data)
	return nil
}

// IsValid returns true if the constraint has valid structure.
func (oc *OntologyConstraint) IsValid() bool {
	if oc.ID == "" || oc.ClassID == "" || oc.Value == "" {
		return false
	}
	if !oc.Type.IsValid() {
		return false
	}
	// Validate that Value is valid JSON
	var js json.RawMessage
	return json.Unmarshal([]byte(oc.Value), &js) == nil
}
