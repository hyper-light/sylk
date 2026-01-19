package vectorgraphdb

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Entity Types (KG.2.2)
// =============================================================================

// EntityType represents the classification of an entity in the knowledge graph.
// Entities are resolved identities that may have multiple aliases and can
// span multiple source nodes.
type EntityType int

const (
	// EntityTypeFunction represents a function entity.
	EntityTypeFunction EntityType = 0

	// EntityTypeType represents a type (struct, class, etc.) entity.
	EntityTypeType EntityType = 1

	// EntityTypeVariable represents a variable entity.
	EntityTypeVariable EntityType = 2

	// EntityTypeImport represents an import/dependency entity.
	EntityTypeImport EntityType = 3

	// EntityTypeFile represents a file entity.
	EntityTypeFile EntityType = 4

	// EntityTypeModule represents a module entity.
	EntityTypeModule EntityType = 5

	// EntityTypePackage represents a package entity.
	EntityTypePackage EntityType = 6

	// EntityTypeInterface represents an interface entity.
	EntityTypeInterface EntityType = 7

	// EntityTypeMethod represents a method entity.
	EntityTypeMethod EntityType = 8

	// EntityTypeConstant represents a constant entity.
	EntityTypeConstant EntityType = 9
)

// String returns the string representation of the EntityType.
func (et EntityType) String() string {
	switch et {
	case EntityTypeFunction:
		return "function"
	case EntityTypeType:
		return "type"
	case EntityTypeVariable:
		return "variable"
	case EntityTypeImport:
		return "import"
	case EntityTypeFile:
		return "file"
	case EntityTypeModule:
		return "module"
	case EntityTypePackage:
		return "package"
	case EntityTypeInterface:
		return "interface"
	case EntityTypeMethod:
		return "method"
	case EntityTypeConstant:
		return "constant"
	default:
		return fmt.Sprintf("entity_type(%d)", et)
	}
}

// ParseEntityType parses a string into an EntityType.
func ParseEntityType(s string) (EntityType, error) {
	switch s {
	case "function":
		return EntityTypeFunction, nil
	case "type":
		return EntityTypeType, nil
	case "variable":
		return EntityTypeVariable, nil
	case "import":
		return EntityTypeImport, nil
	case "file":
		return EntityTypeFile, nil
	case "module":
		return EntityTypeModule, nil
	case "package":
		return EntityTypePackage, nil
	case "interface":
		return EntityTypeInterface, nil
	case "method":
		return EntityTypeMethod, nil
	case "constant":
		return EntityTypeConstant, nil
	default:
		return EntityType(0), fmt.Errorf("unknown entity type: %s", s)
	}
}

// IsValid returns true if the entity type is a recognized value.
func (et EntityType) IsValid() bool {
	return et >= EntityTypeFunction && et <= EntityTypeConstant
}

// ValidEntityTypes returns all valid EntityType values.
func ValidEntityTypes() []EntityType {
	return []EntityType{
		EntityTypeFunction,
		EntityTypeType,
		EntityTypeVariable,
		EntityTypeImport,
		EntityTypeFile,
		EntityTypeModule,
		EntityTypePackage,
		EntityTypeInterface,
		EntityTypeMethod,
		EntityTypeConstant,
	}
}

// MarshalJSON implements json.Marshaler for EntityType.
func (et EntityType) MarshalJSON() ([]byte, error) {
	return json.Marshal(et.String())
}

// UnmarshalJSON implements json.Unmarshaler for EntityType.
func (et *EntityType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		parsed, err := ParseEntityType(asString)
		if err != nil {
			return err
		}
		*et = parsed
		return nil
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*et = EntityType(asInt)
		return nil
	}

	return fmt.Errorf("invalid entity type")
}

// =============================================================================
// Alias Types
// =============================================================================

// AliasType represents the type of alias for an entity.
type AliasType int

const (
	// AliasCanonical is the canonical/primary name for an entity.
	AliasCanonical AliasType = 0

	// AliasImport is an alias from an import statement.
	AliasImport AliasType = 1

	// AliasTypeAlias is an alias from a type alias declaration.
	AliasTypeAlias AliasType = 2

	// AliasShorthand is a shorthand/abbreviated alias.
	AliasShorthand AliasType = 3

	// AliasQualified is a fully qualified name alias.
	AliasQualified AliasType = 4
)

// String returns the string representation of the AliasType.
func (at AliasType) String() string {
	switch at {
	case AliasCanonical:
		return "canonical"
	case AliasImport:
		return "import"
	case AliasTypeAlias:
		return "type_alias"
	case AliasShorthand:
		return "shorthand"
	case AliasQualified:
		return "qualified"
	default:
		return fmt.Sprintf("alias_type(%d)", at)
	}
}

// ParseAliasType parses a string into an AliasType.
func ParseAliasType(s string) (AliasType, error) {
	switch s {
	case "canonical":
		return AliasCanonical, nil
	case "import":
		return AliasImport, nil
	case "type_alias":
		return AliasTypeAlias, nil
	case "shorthand":
		return AliasShorthand, nil
	case "qualified":
		return AliasQualified, nil
	default:
		return AliasType(0), fmt.Errorf("unknown alias type: %s", s)
	}
}

// IsValid returns true if the alias type is a recognized value.
func (at AliasType) IsValid() bool {
	return at >= AliasCanonical && at <= AliasQualified
}

// ValidAliasTypes returns all valid AliasType values.
func ValidAliasTypes() []AliasType {
	return []AliasType{
		AliasCanonical,
		AliasImport,
		AliasTypeAlias,
		AliasShorthand,
		AliasQualified,
	}
}

// MarshalJSON implements json.Marshaler for AliasType.
func (at AliasType) MarshalJSON() ([]byte, error) {
	return json.Marshal(at.String())
}

// UnmarshalJSON implements json.Unmarshaler for AliasType.
func (at *AliasType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		parsed, err := ParseAliasType(asString)
		if err != nil {
			return err
		}
		*at = parsed
		return nil
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*at = AliasType(asInt)
		return nil
	}

	return fmt.Errorf("invalid alias type")
}

// =============================================================================
// Entity Alias
// =============================================================================

// EntityAlias represents an alternative name for an entity.
type EntityAlias struct {
	// Alias is the alternative name.
	Alias string `json:"alias"`

	// AliasType indicates the type of this alias.
	AliasType AliasType `json:"alias_type"`

	// Confidence is the confidence score for this alias (0.0 to 1.0).
	Confidence float64 `json:"confidence"`

	// Source indicates where this alias was discovered.
	Source string `json:"source,omitempty"`

	// CreatedAt is when this alias was recorded.
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// NewEntityAlias creates a new EntityAlias with the specified values.
func NewEntityAlias(alias string, aliasType AliasType, confidence float64) EntityAlias {
	return EntityAlias{
		Alias:      alias,
		AliasType:  aliasType,
		Confidence: confidence,
		CreatedAt:  time.Now(),
	}
}

// NewCanonicalAlias creates a canonical alias with full confidence.
func NewCanonicalAlias(name string) EntityAlias {
	return EntityAlias{
		Alias:      name,
		AliasType:  AliasCanonical,
		Confidence: 1.0,
		CreatedAt:  time.Now(),
	}
}

// IsHighConfidence returns true if the alias confidence is >= 0.8.
func (ea EntityAlias) IsHighConfidence() bool {
	return ea.Confidence >= 0.8
}

// =============================================================================
// Entity
// =============================================================================

// Entity represents a resolved identity in the knowledge graph.
// An entity may correspond to multiple source nodes and have multiple aliases.
type Entity struct {
	// ID is the unique identifier for this entity.
	ID string `json:"id"`

	// CanonicalName is the primary name for this entity.
	CanonicalName string `json:"canonical_name"`

	// EntityType is the type of this entity.
	EntityType EntityType `json:"entity_type"`

	// SourceNodeID is the ID of the primary source node for this entity.
	SourceNodeID string `json:"source_node_id"`

	// Aliases is the list of alternative names for this entity.
	Aliases []EntityAlias `json:"aliases,omitempty"`

	// Description is an optional description of the entity.
	Description string `json:"description,omitempty"`

	// Metadata contains additional entity-specific data.
	Metadata map[string]any `json:"metadata,omitempty"`

	// LinkedNodeIDs contains IDs of all nodes linked to this entity.
	LinkedNodeIDs []string `json:"linked_node_ids,omitempty"`

	// Confidence is the overall confidence in this entity resolution.
	Confidence float64 `json:"confidence"`

	// CreatedAt is when this entity was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when this entity was last updated.
	UpdatedAt time.Time `json:"updated_at"`

	// Version is used for optimistic concurrency control.
	Version uint64 `json:"version,omitempty"`
}

// NewEntity creates a new Entity with the specified values.
func NewEntity(id, canonicalName string, entityType EntityType, sourceNodeID string) *Entity {
	now := time.Now()
	return &Entity{
		ID:            id,
		CanonicalName: canonicalName,
		EntityType:    entityType,
		SourceNodeID:  sourceNodeID,
		Aliases: []EntityAlias{
			NewCanonicalAlias(canonicalName),
		},
		Confidence: 1.0,
		CreatedAt:  now,
		UpdatedAt:  now,
		Version:    1,
	}
}

// NewEntityWithAliases creates a new Entity with explicit aliases.
func NewEntityWithAliases(id, canonicalName string, entityType EntityType, sourceNodeID string, aliases []EntityAlias) *Entity {
	entity := NewEntity(id, canonicalName, entityType, sourceNodeID)
	// Replace the default canonical alias with provided aliases
	if len(aliases) > 0 {
		entity.Aliases = aliases
	}
	return entity
}

// AddAlias adds a new alias to the entity if it doesn't already exist.
func (e *Entity) AddAlias(alias EntityAlias) bool {
	for _, existing := range e.Aliases {
		if existing.Alias == alias.Alias && existing.AliasType == alias.AliasType {
			return false
		}
	}
	e.Aliases = append(e.Aliases, alias)
	e.UpdatedAt = time.Now()
	return true
}

// RemoveAlias removes an alias from the entity by name.
// Returns true if an alias was removed.
func (e *Entity) RemoveAlias(aliasName string) bool {
	for i, alias := range e.Aliases {
		if alias.Alias == aliasName {
			e.Aliases = append(e.Aliases[:i], e.Aliases[i+1:]...)
			e.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// HasAlias returns true if the entity has an alias with the given name.
func (e *Entity) HasAlias(aliasName string) bool {
	for _, alias := range e.Aliases {
		if alias.Alias == aliasName {
			return true
		}
	}
	return false
}

// GetAlias returns the alias with the given name, or nil if not found.
func (e *Entity) GetAlias(aliasName string) *EntityAlias {
	for i := range e.Aliases {
		if e.Aliases[i].Alias == aliasName {
			return &e.Aliases[i]
		}
	}
	return nil
}

// GetAliasesByType returns all aliases of the specified type.
func (e *Entity) GetAliasesByType(aliasType AliasType) []EntityAlias {
	var result []EntityAlias
	for _, alias := range e.Aliases {
		if alias.AliasType == aliasType {
			result = append(result, alias)
		}
	}
	return result
}

// GetHighConfidenceAliases returns all aliases with confidence >= 0.8.
func (e *Entity) GetHighConfidenceAliases() []EntityAlias {
	var result []EntityAlias
	for _, alias := range e.Aliases {
		if alias.IsHighConfidence() {
			result = append(result, alias)
		}
	}
	return result
}

// LinkNode adds a node ID to the linked nodes list if not already present.
func (e *Entity) LinkNode(nodeID string) bool {
	for _, id := range e.LinkedNodeIDs {
		if id == nodeID {
			return false
		}
	}
	e.LinkedNodeIDs = append(e.LinkedNodeIDs, nodeID)
	e.UpdatedAt = time.Now()
	return true
}

// UnlinkNode removes a node ID from the linked nodes list.
func (e *Entity) UnlinkNode(nodeID string) bool {
	for i, id := range e.LinkedNodeIDs {
		if id == nodeID {
			e.LinkedNodeIDs = append(e.LinkedNodeIDs[:i], e.LinkedNodeIDs[i+1:]...)
			e.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// AllNames returns all names (canonical + aliases) for this entity.
func (e *Entity) AllNames() []string {
	names := make([]string, 0, len(e.Aliases)+1)
	names = append(names, e.CanonicalName)
	for _, alias := range e.Aliases {
		if alias.Alias != e.CanonicalName {
			names = append(names, alias.Alias)
		}
	}
	return names
}

// Match returns true if the given name matches the canonical name or any alias.
func (e *Entity) Match(name string) bool {
	if e.CanonicalName == name {
		return true
	}
	return e.HasAlias(name)
}

// MatchWithConfidence returns the confidence score for a name match.
// Returns 0.0 if no match is found.
func (e *Entity) MatchWithConfidence(name string) float64 {
	if e.CanonicalName == name {
		return 1.0
	}
	if alias := e.GetAlias(name); alias != nil {
		return alias.Confidence
	}
	return 0.0
}
