package vectorgraphdb

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// Inference Types (KG.2.4)
// =============================================================================

// InferenceRuleRecord represents an inference rule stored in the database.
// This is separate from core/knowledge/inference.InferenceRule which is used
// for runtime rule processing. This type provides database-level storage
// with JSON serialization for rule bodies.
type InferenceRuleRecord struct {
	// ID is the unique identifier for this rule.
	ID string `json:"id"`

	// Name is the human-readable name of this rule.
	Name string `json:"name"`

	// HeadSubject is the subject of the rule head (conclusion).
	// Can be a variable (e.g., "?x") or a concrete value.
	HeadSubject string `json:"head_subject"`

	// HeadPredicate is the predicate of the rule head (conclusion).
	HeadPredicate string `json:"head_predicate"`

	// HeadObject is the object of the rule head (conclusion).
	// Can be a variable (e.g., "?y") or a concrete value.
	HeadObject string `json:"head_object"`

	// BodyJSON is the JSON-encoded array of RuleAtom representing the rule body.
	// Each atom in the body is a condition that must be satisfied.
	BodyJSON string `json:"body_json"`

	// Priority determines the order in which rules are evaluated.
	// Higher priority rules are evaluated first.
	Priority int `json:"priority"`

	// Enabled indicates whether this rule is active.
	Enabled bool `json:"enabled"`

	// CreatedAt is when this rule was created.
	CreatedAt time.Time `json:"created_at"`
}

// NewInferenceRuleRecord creates a new InferenceRuleRecord with the specified values.
func NewInferenceRuleRecord(id, name string, headSubject, headPredicate, headObject string, bodyAtoms []RuleAtom, priority int) (*InferenceRuleRecord, error) {
	bodyJSON, err := EncodeBodyJSON(bodyAtoms)
	if err != nil {
		return nil, fmt.Errorf("failed to encode body atoms: %w", err)
	}

	return &InferenceRuleRecord{
		ID:            id,
		Name:          name,
		HeadSubject:   headSubject,
		HeadPredicate: headPredicate,
		HeadObject:    headObject,
		BodyJSON:      bodyJSON,
		Priority:      priority,
		Enabled:       true,
		CreatedAt:     time.Now(),
	}, nil
}

// GetBody parses and returns the rule body atoms.
func (r *InferenceRuleRecord) GetBody() ([]RuleAtom, error) {
	return ParseBodyJSON(r.BodyJSON)
}

// SetBody encodes and sets the rule body atoms.
func (r *InferenceRuleRecord) SetBody(atoms []RuleAtom) error {
	bodyJSON, err := EncodeBodyJSON(atoms)
	if err != nil {
		return err
	}
	r.BodyJSON = bodyJSON
	return nil
}

// GetHead returns the head of the rule as a RuleAtom.
func (r *InferenceRuleRecord) GetHead() RuleAtom {
	return RuleAtom{
		Subject:   r.HeadSubject,
		Predicate: r.HeadPredicate,
		Object:    r.HeadObject,
	}
}

// SetHead sets the head of the rule from a RuleAtom.
func (r *InferenceRuleRecord) SetHead(head RuleAtom) {
	r.HeadSubject = head.Subject
	r.HeadPredicate = head.Predicate
	r.HeadObject = head.Object
}

// IsValid returns true if the rule has valid structure.
func (r *InferenceRuleRecord) IsValid() bool {
	if r.ID == "" || r.Name == "" {
		return false
	}
	if r.HeadSubject == "" || r.HeadPredicate == "" || r.HeadObject == "" {
		return false
	}
	if r.BodyJSON == "" {
		return false
	}
	// Verify body JSON is parseable
	if _, err := ParseBodyJSON(r.BodyJSON); err != nil {
		return false
	}
	return true
}

// =============================================================================
// RuleAtom
// =============================================================================

// RuleAtom represents a single atom (triple pattern) in a rule.
// Atoms can contain variables (prefixed with "?") or concrete values.
type RuleAtom struct {
	// Subject is the subject of the triple pattern.
	Subject string `json:"subject"`

	// Predicate is the predicate (relationship type) of the triple pattern.
	Predicate string `json:"predicate"`

	// Object is the object of the triple pattern.
	Object string `json:"object"`
}

// NewRuleAtom creates a new RuleAtom with the specified values.
func NewRuleAtom(subject, predicate, object string) RuleAtom {
	return RuleAtom{
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}
}

// HasVariable returns true if any part of the atom contains a variable.
func (a RuleAtom) HasVariable() bool {
	return IsVariable(a.Subject) || IsVariable(a.Predicate) || IsVariable(a.Object)
}

// GetVariables returns all variables in this atom.
func (a RuleAtom) GetVariables() []string {
	var vars []string
	if IsVariable(a.Subject) {
		vars = append(vars, a.Subject)
	}
	if IsVariable(a.Predicate) {
		vars = append(vars, a.Predicate)
	}
	if IsVariable(a.Object) {
		vars = append(vars, a.Object)
	}
	return vars
}

// IsGround returns true if the atom contains no variables.
func (a RuleAtom) IsGround() bool {
	return !a.HasVariable()
}

// String returns a string representation of the atom.
func (a RuleAtom) String() string {
	return fmt.Sprintf("(%s, %s, %s)", a.Subject, a.Predicate, a.Object)
}

// =============================================================================
// Helper Functions
// =============================================================================

// IsVariable returns true if the term is a variable (starts with "?").
func IsVariable(term string) bool {
	return len(term) > 0 && term[0] == '?'
}

// GetRuleVariables extracts all unique variables from a rule.
// Returns variables from both the head and body of the rule.
func GetRuleVariables(rule *InferenceRuleRecord) []string {
	if rule == nil {
		return nil
	}

	varSet := make(map[string]struct{})

	// Collect head variables
	if IsVariable(rule.HeadSubject) {
		varSet[rule.HeadSubject] = struct{}{}
	}
	if IsVariable(rule.HeadPredicate) {
		varSet[rule.HeadPredicate] = struct{}{}
	}
	if IsVariable(rule.HeadObject) {
		varSet[rule.HeadObject] = struct{}{}
	}

	// Collect body variables
	body, err := ParseBodyJSON(rule.BodyJSON)
	if err == nil {
		for _, atom := range body {
			for _, v := range atom.GetVariables() {
				varSet[v] = struct{}{}
			}
		}
	}

	// Convert set to slice
	vars := make([]string, 0, len(varSet))
	for v := range varSet {
		vars = append(vars, v)
	}
	return vars
}

// ParseBodyJSON parses a JSON-encoded rule body into a slice of RuleAtom.
func ParseBodyJSON(bodyJSON string) ([]RuleAtom, error) {
	if bodyJSON == "" {
		return nil, fmt.Errorf("empty body JSON")
	}
	var atoms []RuleAtom
	if err := json.Unmarshal([]byte(bodyJSON), &atoms); err != nil {
		return nil, fmt.Errorf("failed to parse body JSON: %w", err)
	}
	return atoms, nil
}

// EncodeBodyJSON encodes a slice of RuleAtom to JSON.
func EncodeBodyJSON(atoms []RuleAtom) (string, error) {
	if atoms == nil {
		atoms = []RuleAtom{}
	}
	data, err := json.Marshal(atoms)
	if err != nil {
		return "", fmt.Errorf("failed to encode body atoms: %w", err)
	}
	return string(data), nil
}

// VariableNameWithoutPrefix returns the variable name without the "?" prefix.
// Returns the original term if it's not a variable.
func VariableNameWithoutPrefix(term string) string {
	if IsVariable(term) {
		return term[1:]
	}
	return term
}

// MakeVariable creates a variable name by adding the "?" prefix.
func MakeVariable(name string) string {
	if strings.HasPrefix(name, "?") {
		return name
	}
	return "?" + name
}

// =============================================================================
// MaterializedEdge
// =============================================================================

// MaterializedEdge represents an edge that was derived through inference.
// These edges are stored separately to distinguish them from asserted edges
// and to track their provenance.
type MaterializedEdge struct {
	// RuleID is the ID of the inference rule that derived this edge.
	RuleID int64 `json:"rule_id"`

	// EdgeID is the ID of the derived edge in the edges table.
	EdgeID int64 `json:"edge_id"`

	// DerivedAt is when this edge was materialized.
	DerivedAt time.Time `json:"derived_at"`
}

// NewMaterializedEdge creates a new MaterializedEdge.
func NewMaterializedEdge(ruleID, edgeID int64) *MaterializedEdge {
	return &MaterializedEdge{
		RuleID:    ruleID,
		EdgeID:    edgeID,
		DerivedAt: time.Now(),
	}
}

// =============================================================================
// Rule Matching Helpers
// =============================================================================

// BindingMap represents variable bindings during rule evaluation.
type BindingMap map[string]string

// NewBindingMap creates a new empty binding map.
func NewBindingMap() BindingMap {
	return make(BindingMap)
}

// Bind sets a variable binding. Returns false if the variable is already
// bound to a different value.
func (b BindingMap) Bind(variable, value string) bool {
	if existing, ok := b[variable]; ok {
		return existing == value
	}
	b[variable] = value
	return true
}

// Get returns the value bound to a variable, or the variable itself if unbound.
func (b BindingMap) Get(term string) string {
	if IsVariable(term) {
		if value, ok := b[term]; ok {
			return value
		}
	}
	return term
}

// Clone creates a copy of the binding map.
func (b BindingMap) Clone() BindingMap {
	clone := make(BindingMap, len(b))
	for k, v := range b {
		clone[k] = v
	}
	return clone
}

// Apply substitutes all variables in an atom with their bound values.
func (b BindingMap) Apply(atom RuleAtom) RuleAtom {
	return RuleAtom{
		Subject:   b.Get(atom.Subject),
		Predicate: b.Get(atom.Predicate),
		Object:    b.Get(atom.Object),
	}
}
