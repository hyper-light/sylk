package knowledge

import (
	"encoding/json"
	"fmt"
)

// =============================================================================
// Entity Kind Types
// =============================================================================

// EntityKind represents the type of code entity extracted from source files.
// Supports Go, TypeScript, and Python entities across different language constructs.
type EntityKind int

const (
	EntityKindFunction  EntityKind = 0
	EntityKindType      EntityKind = 1
	EntityKindVariable  EntityKind = 2
	EntityKindImport    EntityKind = 3
	EntityKindConstant  EntityKind = 4
	EntityKindInterface EntityKind = 5
	EntityKindMethod    EntityKind = 6
	EntityKindStruct    EntityKind = 7
	EntityKindPackage   EntityKind = 8
	EntityKindFile      EntityKind = 9
)

// ValidEntityKinds returns all valid EntityKind values.
func ValidEntityKinds() []EntityKind {
	return []EntityKind{
		EntityKindFunction,
		EntityKindType,
		EntityKindVariable,
		EntityKindImport,
		EntityKindConstant,
		EntityKindInterface,
		EntityKindMethod,
		EntityKindStruct,
		EntityKindPackage,
		EntityKindFile,
	}
}

// IsValid returns true if the entity kind is a recognized value.
func (ek EntityKind) IsValid() bool {
	for _, valid := range ValidEntityKinds() {
		if ek == valid {
			return true
		}
	}
	return false
}

func (ek EntityKind) String() string {
	switch ek {
	case EntityKindFunction:
		return "function"
	case EntityKindType:
		return "type"
	case EntityKindVariable:
		return "variable"
	case EntityKindImport:
		return "import"
	case EntityKindConstant:
		return "constant"
	case EntityKindInterface:
		return "interface"
	case EntityKindMethod:
		return "method"
	case EntityKindStruct:
		return "struct"
	case EntityKindPackage:
		return "package"
	case EntityKindFile:
		return "file"
	default:
		return fmt.Sprintf("entity_kind(%d)", ek)
	}
}

func ParseEntityKind(value string) (EntityKind, bool) {
	switch value {
	case "function":
		return EntityKindFunction, true
	case "type":
		return EntityKindType, true
	case "variable":
		return EntityKindVariable, true
	case "import":
		return EntityKindImport, true
	case "constant":
		return EntityKindConstant, true
	case "interface":
		return EntityKindInterface, true
	case "method":
		return EntityKindMethod, true
	case "struct":
		return EntityKindStruct, true
	case "package":
		return EntityKindPackage, true
	case "file":
		return EntityKindFile, true
	default:
		return EntityKind(0), false
	}
}

func (ek EntityKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(ek.String())
}

func (ek *EntityKind) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEntityKind(asString); ok {
			*ek = parsed
			return nil
		}
		return fmt.Errorf("invalid entity kind: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*ek = EntityKind(asInt)
		return nil
	}

	return fmt.Errorf("invalid entity kind")
}

// =============================================================================
// Entity Scope Types
// =============================================================================

// EntityScope represents the scope level of an entity within the code.
type EntityScope int

const (
	ScopeGlobal   EntityScope = 0
	ScopeModule   EntityScope = 1
	ScopeFunction EntityScope = 2
	ScopeBlock    EntityScope = 3
)

// ValidEntityScopes returns all valid EntityScope values.
func ValidEntityScopes() []EntityScope {
	return []EntityScope{
		ScopeGlobal,
		ScopeModule,
		ScopeFunction,
		ScopeBlock,
	}
}

// IsValid returns true if the entity scope is a recognized value.
func (es EntityScope) IsValid() bool {
	for _, valid := range ValidEntityScopes() {
		if es == valid {
			return true
		}
	}
	return false
}

func (es EntityScope) String() string {
	switch es {
	case ScopeGlobal:
		return "global"
	case ScopeModule:
		return "module"
	case ScopeFunction:
		return "function"
	case ScopeBlock:
		return "block"
	default:
		return fmt.Sprintf("scope(%d)", es)
	}
}

func ParseEntityScope(value string) (EntityScope, bool) {
	switch value {
	case "global":
		return ScopeGlobal, true
	case "module":
		return ScopeModule, true
	case "function":
		return ScopeFunction, true
	case "block":
		return ScopeBlock, true
	default:
		return EntityScope(0), false
	}
}

func (es EntityScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(es.String())
}

func (es *EntityScope) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEntityScope(asString); ok {
			*es = parsed
			return nil
		}
		return fmt.Errorf("invalid entity scope: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*es = EntityScope(asInt)
		return nil
	}

	return fmt.Errorf("invalid entity scope")
}

// =============================================================================
// Entity Visibility Types
// =============================================================================

// EntityVisibility represents the visibility level of an entity.
type EntityVisibility int

const (
	VisibilityPublic   EntityVisibility = 0
	VisibilityPrivate  EntityVisibility = 1
	VisibilityInternal EntityVisibility = 2
)

// ValidEntityVisibilities returns all valid EntityVisibility values.
func ValidEntityVisibilities() []EntityVisibility {
	return []EntityVisibility{
		VisibilityPublic,
		VisibilityPrivate,
		VisibilityInternal,
	}
}

// IsValid returns true if the entity visibility is a recognized value.
func (ev EntityVisibility) IsValid() bool {
	for _, valid := range ValidEntityVisibilities() {
		if ev == valid {
			return true
		}
	}
	return false
}

func (ev EntityVisibility) String() string {
	switch ev {
	case VisibilityPublic:
		return "public"
	case VisibilityPrivate:
		return "private"
	case VisibilityInternal:
		return "internal"
	default:
		return fmt.Sprintf("visibility(%d)", ev)
	}
}

func ParseEntityVisibility(value string) (EntityVisibility, bool) {
	switch value {
	case "public":
		return VisibilityPublic, true
	case "private":
		return VisibilityPrivate, true
	case "internal":
		return VisibilityInternal, true
	default:
		return EntityVisibility(0), false
	}
}

func (ev EntityVisibility) MarshalJSON() ([]byte, error) {
	return json.Marshal(ev.String())
}

func (ev *EntityVisibility) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEntityVisibility(asString); ok {
			*ev = parsed
			return nil
		}
		return fmt.Errorf("invalid entity visibility: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*ev = EntityVisibility(asInt)
		return nil
	}

	return fmt.Errorf("invalid entity visibility")
}

// =============================================================================
// Reference Kind Types
// =============================================================================

// ReferenceKind represents the type of reference to an entity.
type ReferenceKind int

const (
	RefCall  ReferenceKind = 0
	RefRead  ReferenceKind = 1
	RefWrite ReferenceKind = 2
	RefType  ReferenceKind = 3
)

// ValidReferenceKinds returns all valid ReferenceKind values.
func ValidReferenceKinds() []ReferenceKind {
	return []ReferenceKind{
		RefCall,
		RefRead,
		RefWrite,
		RefType,
	}
}

// IsValid returns true if the reference kind is a recognized value.
func (rk ReferenceKind) IsValid() bool {
	for _, valid := range ValidReferenceKinds() {
		if rk == valid {
			return true
		}
	}
	return false
}

func (rk ReferenceKind) String() string {
	switch rk {
	case RefCall:
		return "call"
	case RefRead:
		return "read"
	case RefWrite:
		return "write"
	case RefType:
		return "type"
	default:
		return fmt.Sprintf("reference_kind(%d)", rk)
	}
}

func ParseReferenceKind(value string) (ReferenceKind, bool) {
	switch value {
	case "call":
		return RefCall, true
	case "read":
		return RefRead, true
	case "write":
		return RefWrite, true
	case "type":
		return RefType, true
	default:
		return ReferenceKind(0), false
	}
}

func (rk ReferenceKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(rk.String())
}

func (rk *ReferenceKind) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseReferenceKind(asString); ok {
			*rk = parsed
			return nil
		}
		return fmt.Errorf("invalid reference kind: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*rk = ReferenceKind(asInt)
		return nil
	}

	return fmt.Errorf("invalid reference kind")
}

// =============================================================================
// Core Entity Structures
// =============================================================================

// EntityReference represents a usage of an entity at a specific location.
type EntityReference struct {
	FilePath string        `json:"file_path"`
	Line     int           `json:"line"`
	Kind     ReferenceKind `json:"kind"`
}

// ExtractedEntity represents a code entity extracted from source files.
// Supports Go, TypeScript, and Python entities with full position tracking
// and reference analysis.
type ExtractedEntity struct {
	Name      string           `json:"name"`
	Kind      EntityKind       `json:"kind"`
	FilePath  string           `json:"file_path"`
	StartLine int              `json:"start_line"`
	EndLine   int              `json:"end_line"`
	Signature string           `json:"signature,omitempty"`
	Scope     EntityScope      `json:"scope"`
	Visibility EntityVisibility `json:"visibility"`
	References []EntityReference `json:"references,omitempty"`
}
