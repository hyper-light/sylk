package knowledge

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Relation Type Enum
// =============================================================================

// RelationType represents the type of relationship between code entities.
// Captures structural and semantic relationships across the codebase.
type RelationType int

const (
	RelCalls      RelationType = 0
	RelImports    RelationType = 1
	RelExtends    RelationType = 2
	RelImplements RelationType = 3
	RelUses       RelationType = 4
	RelReferences RelationType = 5
	RelDefines    RelationType = 6
	RelContains   RelationType = 7
)

// ValidRelationTypes returns all valid RelationType values.
func ValidRelationTypes() []RelationType {
	return []RelationType{
		RelCalls,
		RelImports,
		RelExtends,
		RelImplements,
		RelUses,
		RelReferences,
		RelDefines,
		RelContains,
	}
}

// IsValid returns true if the relation type is a recognized value.
func (rt RelationType) IsValid() bool {
	for _, valid := range ValidRelationTypes() {
		if rt == valid {
			return true
		}
	}
	return false
}

func (rt RelationType) String() string {
	switch rt {
	case RelCalls:
		return "calls"
	case RelImports:
		return "imports"
	case RelExtends:
		return "extends"
	case RelImplements:
		return "implements"
	case RelUses:
		return "uses"
	case RelReferences:
		return "references"
	case RelDefines:
		return "defines"
	case RelContains:
		return "contains"
	default:
		return fmt.Sprintf("relation_type(%d)", rt)
	}
}

func ParseRelationType(value string) (RelationType, bool) {
	switch value {
	case "calls":
		return RelCalls, true
	case "imports":
		return RelImports, true
	case "extends":
		return RelExtends, true
	case "implements":
		return RelImplements, true
	case "uses":
		return RelUses, true
	case "references":
		return RelReferences, true
	case "defines":
		return RelDefines, true
	case "contains":
		return RelContains, true
	default:
		return RelationType(0), false
	}
}

func (rt RelationType) MarshalJSON() ([]byte, error) {
	return json.Marshal(rt.String())
}

func (rt *RelationType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseRelationType(asString); ok {
			*rt = parsed
			return nil
		}
		return fmt.Errorf("invalid relation type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*rt = RelationType(asInt)
		return nil
	}

	return fmt.Errorf("invalid relation type")
}

// =============================================================================
// Evidence Span
// =============================================================================

// EvidenceSpan represents a specific location in source code that proves
// the existence of a relation between entities.
type EvidenceSpan struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
}

// =============================================================================
// Extracted Relation
// =============================================================================

// ExtractedRelation represents a relationship between two code entities.
// Includes evidence and confidence scoring for each relation.
type ExtractedRelation struct {
	SourceEntity *ExtractedEntity `json:"source_entity"`
	TargetEntity *ExtractedEntity `json:"target_entity"`
	RelationType RelationType     `json:"relation_type"`
	Evidence     []EvidenceSpan   `json:"evidence"`
	Confidence   float64          `json:"confidence"`
	ExtractedAt  time.Time        `json:"extracted_at"`
}
