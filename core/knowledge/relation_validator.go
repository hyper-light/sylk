package knowledge

import (
	"fmt"
	"strings"
	"sync"
)

// =============================================================================
// Validation Result
// =============================================================================

// ValidationResult represents the result of validating a relation.
type ValidationResult struct {
	// Valid indicates whether the relation passed validation.
	Valid bool `json:"valid"`

	// Confidence is the confidence score for the validation (0.0 to 1.0).
	Confidence float64 `json:"confidence"`

	// Issues contains descriptions of any validation problems found.
	Issues []string `json:"issues,omitempty"`

	// RelationType is the type of relation that was validated.
	RelationType RelationType `json:"relation_type"`

	// SourceEntityName is the name of the source entity.
	SourceEntityName string `json:"source_entity_name"`

	// TargetEntityName is the name of the target entity.
	TargetEntityName string `json:"target_entity_name"`
}

// =============================================================================
// Relation Validator
// =============================================================================

// RelationValidator validates extracted relations against entities.
// It checks that both endpoints exist and that the relation type is valid
// for the given entity types.
type RelationValidator struct {
	mu sync.RWMutex

	// entityIndex maps entity names to their entities.
	entityIndex map[string][]*ExtractedEntity

	// entityBySignature maps entity signatures to their entities for more precise lookup.
	entityBySignature map[string]*ExtractedEntity

	// config holds configuration for the validator.
	config RelationValidatorConfig

	// validRelationMatrix defines which relation types are valid for which entity kind pairs.
	validRelationMatrix map[relPair][]RelationType
}

// relPair represents a pair of entity kinds for relation validation.
type relPair struct {
	source EntityKind
	target EntityKind
}

// RelationValidatorConfig contains configuration options for the RelationValidator.
type RelationValidatorConfig struct {
	// MinConfidence is the minimum confidence threshold for valid relations.
	MinConfidence float64

	// RequireBothEndpoints requires both source and target entities to exist.
	RequireBothEndpoints bool

	// StrictTypeChecking enforces strict relation type validation for entity kinds.
	StrictTypeChecking bool

	// AllowExternalReferences allows relations where one endpoint may be external.
	AllowExternalReferences bool
}

// DefaultRelationValidatorConfig returns the default configuration.
func DefaultRelationValidatorConfig() RelationValidatorConfig {
	return RelationValidatorConfig{
		MinConfidence:           0.3,
		RequireBothEndpoints:    false,
		StrictTypeChecking:      true,
		AllowExternalReferences: true,
	}
}

// NewRelationValidator creates a new RelationValidator with default configuration.
func NewRelationValidator() *RelationValidator {
	return NewRelationValidatorWithConfig(DefaultRelationValidatorConfig())
}

// NewRelationValidatorWithConfig creates a new RelationValidator with custom configuration.
func NewRelationValidatorWithConfig(config RelationValidatorConfig) *RelationValidator {
	rv := &RelationValidator{
		entityIndex:       make(map[string][]*ExtractedEntity),
		entityBySignature: make(map[string]*ExtractedEntity),
		config:            config,
	}
	rv.initValidRelationMatrix()
	return rv
}

// initValidRelationMatrix initializes the matrix of valid relation types for entity kind pairs.
func (rv *RelationValidator) initValidRelationMatrix() {
	rv.validRelationMatrix = make(map[relPair][]RelationType)

	// Function relations
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindFunction}] = []RelationType{
		RelCalls, RelReferences, RelUses,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindMethod}] = []RelationType{
		RelCalls, RelReferences, RelUses,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindType}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindStruct}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindInterface}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindVariable}] = []RelationType{
		RelUses, RelReferences, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindFunction, EntityKindConstant}] = []RelationType{
		RelUses, RelReferences,
	}

	// Method relations
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindFunction}] = []RelationType{
		RelCalls, RelReferences, RelUses,
	}
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindMethod}] = []RelationType{
		RelCalls, RelReferences, RelUses,
	}
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindType}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindStruct}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindInterface}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindMethod, EntityKindVariable}] = []RelationType{
		RelUses, RelReferences, RelDefines,
	}

	// Struct relations
	rv.validRelationMatrix[relPair{EntityKindStruct, EntityKindStruct}] = []RelationType{
		RelExtends, RelContains, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindStruct, EntityKindInterface}] = []RelationType{
		RelImplements, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindStruct, EntityKindType}] = []RelationType{
		RelExtends, RelContains, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindStruct, EntityKindMethod}] = []RelationType{
		RelDefines, RelContains,
	}
	rv.validRelationMatrix[relPair{EntityKindStruct, EntityKindVariable}] = []RelationType{
		RelContains, RelDefines,
	}

	// Interface relations
	rv.validRelationMatrix[relPair{EntityKindInterface, EntityKindInterface}] = []RelationType{
		RelExtends, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindInterface, EntityKindMethod}] = []RelationType{
		RelDefines, RelContains,
	}
	rv.validRelationMatrix[relPair{EntityKindInterface, EntityKindType}] = []RelationType{
		RelExtends, RelUses,
	}

	// Type relations
	rv.validRelationMatrix[relPair{EntityKindType, EntityKindType}] = []RelationType{
		RelExtends, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindType, EntityKindStruct}] = []RelationType{
		RelExtends, RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindType, EntityKindInterface}] = []RelationType{
		RelImplements, RelUses, RelReferences,
	}

	// Package/File relations
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindPackage}] = []RelationType{
		RelImports, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindFile}] = []RelationType{
		RelImports, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindPackage}] = []RelationType{
		RelImports, RelContains,
	}
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindFunction}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindType}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindStruct}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindInterface}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindFunction}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindType}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindStruct}] = []RelationType{
		RelContains, RelDefines,
	}
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindInterface}] = []RelationType{
		RelContains, RelDefines,
	}

	// Import relations
	rv.validRelationMatrix[relPair{EntityKindFile, EntityKindImport}] = []RelationType{
		RelImports, RelContains,
	}
	rv.validRelationMatrix[relPair{EntityKindPackage, EntityKindImport}] = []RelationType{
		RelImports, RelContains,
	}
	rv.validRelationMatrix[relPair{EntityKindImport, EntityKindPackage}] = []RelationType{
		RelReferences,
	}

	// Variable/Constant relations
	rv.validRelationMatrix[relPair{EntityKindVariable, EntityKindType}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindVariable, EntityKindStruct}] = []RelationType{
		RelUses, RelReferences,
	}
	rv.validRelationMatrix[relPair{EntityKindConstant, EntityKindType}] = []RelationType{
		RelUses, RelReferences,
	}
}

// IndexEntities indexes a slice of entities for validation lookup.
func (rv *RelationValidator) IndexEntities(entities []ExtractedEntity) {
	rv.mu.Lock()
	defer rv.mu.Unlock()

	for i := range entities {
		entity := &entities[i]
		name := strings.ToLower(entity.Name)
		rv.entityIndex[name] = append(rv.entityIndex[name], entity)

		if entity.Signature != "" {
			rv.entityBySignature[entity.Signature] = entity
		}
	}
}

// ValidateRelation validates a single relation against the indexed entities.
func (rv *RelationValidator) ValidateRelation(relation ExtractedRelation, entities []ExtractedEntity) ValidationResult {
	rv.mu.RLock()
	defer rv.mu.RUnlock()

	result := ValidationResult{
		Valid:            true,
		Confidence:       relation.Confidence,
		RelationType:     relation.RelationType,
		SourceEntityName: "",
		TargetEntityName: "",
	}

	// Validate source entity exists
	if relation.SourceEntity == nil {
		result.Valid = false
		result.Issues = append(result.Issues, "source entity is nil")
		result.Confidence *= 0.0
	} else {
		result.SourceEntityName = relation.SourceEntity.Name
		if !rv.entityExists(relation.SourceEntity) {
			if rv.config.RequireBothEndpoints {
				result.Valid = false
				result.Issues = append(result.Issues, fmt.Sprintf("source entity '%s' not found in indexed entities", relation.SourceEntity.Name))
				result.Confidence *= 0.3
			} else if !rv.config.AllowExternalReferences {
				result.Issues = append(result.Issues, fmt.Sprintf("source entity '%s' may be external", relation.SourceEntity.Name))
				result.Confidence *= 0.7
			}
		}
	}

	// Validate target entity exists
	if relation.TargetEntity == nil {
		result.Valid = false
		result.Issues = append(result.Issues, "target entity is nil")
		result.Confidence *= 0.0
	} else {
		result.TargetEntityName = relation.TargetEntity.Name
		if !rv.entityExists(relation.TargetEntity) {
			if rv.config.RequireBothEndpoints {
				result.Valid = false
				result.Issues = append(result.Issues, fmt.Sprintf("target entity '%s' not found in indexed entities", relation.TargetEntity.Name))
				result.Confidence *= 0.3
			} else if !rv.config.AllowExternalReferences {
				result.Issues = append(result.Issues, fmt.Sprintf("target entity '%s' may be external", relation.TargetEntity.Name))
				result.Confidence *= 0.7
			}
		}
	}

	// Validate relation type is valid for the entity kinds
	if result.Valid && relation.SourceEntity != nil && relation.TargetEntity != nil {
		if !rv.isValidRelationType(relation.SourceEntity.Kind, relation.TargetEntity.Kind, relation.RelationType) {
			if rv.config.StrictTypeChecking {
				result.Valid = false
				result.Issues = append(result.Issues, fmt.Sprintf(
					"relation type '%s' is not valid between '%s' and '%s' entity kinds",
					relation.RelationType.String(),
					relation.SourceEntity.Kind.String(),
					relation.TargetEntity.Kind.String(),
				))
				result.Confidence *= 0.2
			} else {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"relation type '%s' may not be appropriate between '%s' and '%s'",
					relation.RelationType.String(),
					relation.SourceEntity.Kind.String(),
					relation.TargetEntity.Kind.String(),
				))
				result.Confidence *= 0.6
			}
		}
	}

	// Validate confidence meets minimum threshold
	if relation.Confidence < rv.config.MinConfidence {
		result.Valid = false
		result.Issues = append(result.Issues, fmt.Sprintf(
			"relation confidence %.2f is below minimum threshold %.2f",
			relation.Confidence,
			rv.config.MinConfidence,
		))
	}

	// Validate evidence exists
	if len(relation.Evidence) == 0 {
		result.Issues = append(result.Issues, "relation has no evidence")
		result.Confidence *= 0.8
	}

	// Self-reference check
	if relation.SourceEntity != nil && relation.TargetEntity != nil {
		if relation.SourceEntity.Name == relation.TargetEntity.Name &&
			relation.SourceEntity.FilePath == relation.TargetEntity.FilePath &&
			relation.SourceEntity.StartLine == relation.TargetEntity.StartLine {
			result.Issues = append(result.Issues, "relation is a self-reference")
			result.Confidence *= 0.5
		}
	}

	// Ensure confidence is within bounds
	if result.Confidence < 0 {
		result.Confidence = 0
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}

	return result
}

// ValidateAll validates multiple relations and returns results for each.
func (rv *RelationValidator) ValidateAll(relations []ExtractedRelation, entities []ExtractedEntity) []ValidationResult {
	// Index entities if not already done
	rv.mu.Lock()
	if len(rv.entityIndex) == 0 {
		for i := range entities {
			entity := &entities[i]
			name := strings.ToLower(entity.Name)
			rv.entityIndex[name] = append(rv.entityIndex[name], entity)

			if entity.Signature != "" {
				rv.entityBySignature[entity.Signature] = entity
			}
		}
	}
	rv.mu.Unlock()

	results := make([]ValidationResult, len(relations))
	for i, relation := range relations {
		results[i] = rv.ValidateRelation(relation, entities)
	}

	return results
}

// FilterValid filters relations and returns only those that pass validation.
func (rv *RelationValidator) FilterValid(relations []ExtractedRelation, entities []ExtractedEntity) []ExtractedRelation {
	results := rv.ValidateAll(relations, entities)

	var valid []ExtractedRelation
	for i, result := range results {
		if result.Valid {
			valid = append(valid, relations[i])
		}
	}

	return valid
}

// FilterByConfidence filters relations by minimum confidence score.
func (rv *RelationValidator) FilterByConfidence(relations []ExtractedRelation, minConfidence float64) []ExtractedRelation {
	var filtered []ExtractedRelation
	for _, relation := range relations {
		if relation.Confidence >= minConfidence {
			filtered = append(filtered, relation)
		}
	}
	return filtered
}

// entityExists checks if an entity exists in the index.
func (rv *RelationValidator) entityExists(entity *ExtractedEntity) bool {
	if entity == nil {
		return false
	}

	// Try exact signature match first
	if entity.Signature != "" {
		if _, found := rv.entityBySignature[entity.Signature]; found {
			return true
		}
	}

	// Fall back to name-based lookup
	name := strings.ToLower(entity.Name)
	if entities, found := rv.entityIndex[name]; found {
		// Check for matching file path and kind
		for _, e := range entities {
			if e.Kind == entity.Kind {
				// If file paths match, definitely exists
				if e.FilePath == entity.FilePath {
					return true
				}
				// If file path is empty (external reference), allow name+kind match
				if entity.FilePath == "" {
					return true
				}
			}
		}
		// Name exists but kind/file doesn't match - still exists
		return len(entities) > 0
	}

	return false
}

// isValidRelationType checks if a relation type is valid for the given entity kind pair.
func (rv *RelationValidator) isValidRelationType(sourceKind, targetKind EntityKind, relType RelationType) bool {
	pair := relPair{source: sourceKind, target: targetKind}

	validTypes, found := rv.validRelationMatrix[pair]
	if !found {
		// If not explicitly defined, allow generic relation types
		return relType == RelReferences || relType == RelUses
	}

	for _, validType := range validTypes {
		if validType == relType {
			return true
		}
	}

	return false
}

// GetValidRelationTypes returns the valid relation types for a given entity kind pair.
func (rv *RelationValidator) GetValidRelationTypes(sourceKind, targetKind EntityKind) []RelationType {
	rv.mu.RLock()
	defer rv.mu.RUnlock()

	pair := relPair{source: sourceKind, target: targetKind}
	if validTypes, found := rv.validRelationMatrix[pair]; found {
		// Return a copy to prevent modification
		result := make([]RelationType, len(validTypes))
		copy(result, validTypes)
		return result
	}

	// Default allowed types
	return []RelationType{RelReferences, RelUses}
}

// Clear clears all indexed entities.
func (rv *RelationValidator) Clear() {
	rv.mu.Lock()
	defer rv.mu.Unlock()

	rv.entityIndex = make(map[string][]*ExtractedEntity)
	rv.entityBySignature = make(map[string]*ExtractedEntity)
}

// SetConfig updates the validator configuration.
func (rv *RelationValidator) SetConfig(config RelationValidatorConfig) {
	rv.mu.Lock()
	defer rv.mu.Unlock()

	rv.config = config
}

// GetConfig returns the current validator configuration.
func (rv *RelationValidator) GetConfig() RelationValidatorConfig {
	rv.mu.RLock()
	defer rv.mu.RUnlock()

	return rv.config
}

// ValidationSummary provides a summary of validation results.
type ValidationSummary struct {
	TotalRelations   int     `json:"total_relations"`
	ValidRelations   int     `json:"valid_relations"`
	InvalidRelations int     `json:"invalid_relations"`
	AverageConfidence float64 `json:"average_confidence"`
	CommonIssues     map[string]int `json:"common_issues"`
}

// Summarize creates a summary of validation results.
func (rv *RelationValidator) Summarize(results []ValidationResult) ValidationSummary {
	summary := ValidationSummary{
		TotalRelations: len(results),
		CommonIssues:   make(map[string]int),
	}

	if len(results) == 0 {
		return summary
	}

	var totalConfidence float64
	for _, result := range results {
		totalConfidence += result.Confidence

		if result.Valid {
			summary.ValidRelations++
		} else {
			summary.InvalidRelations++
		}

		for _, issue := range result.Issues {
			summary.CommonIssues[issue]++
		}
	}

	summary.AverageConfidence = totalConfidence / float64(len(results))

	return summary
}
