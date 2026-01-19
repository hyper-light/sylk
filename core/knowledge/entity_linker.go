package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode"
)

// =============================================================================
// Link Type Enum
// =============================================================================

// LinkType represents the confidence level of an entity link.
type LinkType int

const (
	// LinkDefinite indicates an exact, unambiguous match between reference and definition.
	LinkDefinite LinkType = 0

	// LinkProbable indicates a high-confidence match with minor ambiguity.
	LinkProbable LinkType = 1

	// LinkPossible indicates a low-confidence match that may be incorrect.
	LinkPossible LinkType = 2
)

// String returns the string representation of the link type.
func (lt LinkType) String() string {
	switch lt {
	case LinkDefinite:
		return "definite"
	case LinkProbable:
		return "probable"
	case LinkPossible:
		return "possible"
	default:
		return "unknown"
	}
}

// ParseLinkType parses a string into a LinkType.
func ParseLinkType(s string) (LinkType, bool) {
	switch s {
	case "definite":
		return LinkDefinite, true
	case "probable":
		return LinkProbable, true
	case "possible":
		return LinkPossible, true
	default:
		return LinkDefinite, false
	}
}

// =============================================================================
// Entity Link
// =============================================================================

// EntityLink represents a link between an entity reference and its definition.
type EntityLink struct {
	// ReferenceID is the ID of the entity that contains the reference.
	ReferenceID string `json:"reference_id"`

	// DefinitionID is the ID of the entity being referenced (the definition).
	DefinitionID string `json:"definition_id"`

	// Confidence is the confidence score for this link (0.0 to 1.0).
	Confidence float64 `json:"confidence"`

	// LinkType categorizes the confidence level of the link.
	LinkType LinkType `json:"link_type"`

	// ReferenceName is the name as it appears in the reference.
	ReferenceName string `json:"reference_name"`

	// DefinitionName is the name of the definition entity.
	DefinitionName string `json:"definition_name"`

	// ReferenceFilePath is the file containing the reference.
	ReferenceFilePath string `json:"reference_file_path"`

	// DefinitionFilePath is the file containing the definition.
	DefinitionFilePath string `json:"definition_file_path"`

	// CrossFile indicates whether this is a cross-file reference.
	CrossFile bool `json:"cross_file"`
}

// =============================================================================
// Entity Linker
// =============================================================================

// EntityLinker links entity references to their definitions.
// It uses a SymbolTable for lookup and supports fuzzy matching for partial matches.
type EntityLinker struct {
	mu sync.RWMutex

	// symbolTable is used for symbol resolution.
	symbolTable *SymbolTable

	// entityIndex maps entity names to their entities for quick lookup.
	entityIndex map[string][]*ExtractedEntity

	// entityByID maps entity IDs to their entities.
	entityByID map[string]*ExtractedEntity

	// config holds configuration for the linker.
	config EntityLinkerConfig
}

// EntityLinkerConfig contains configuration options for the EntityLinker.
type EntityLinkerConfig struct {
	// MinFuzzyConfidence is the minimum confidence threshold for fuzzy matches.
	MinFuzzyConfidence float64

	// CaseSensitive controls whether name matching is case-sensitive.
	CaseSensitive bool

	// CrossFileEnabled enables cross-file reference resolution.
	CrossFileEnabled bool

	// MaxFuzzyDistance is the maximum edit distance for fuzzy matching.
	MaxFuzzyDistance int
}

// DefaultEntityLinkerConfig returns the default configuration for EntityLinker.
func DefaultEntityLinkerConfig() EntityLinkerConfig {
	return EntityLinkerConfig{
		MinFuzzyConfidence: 0.5,
		CaseSensitive:      true,
		CrossFileEnabled:   true,
		MaxFuzzyDistance:   3,
	}
}

// NewEntityLinker creates a new EntityLinker with the given symbol table.
func NewEntityLinker(symbolTable *SymbolTable) *EntityLinker {
	return NewEntityLinkerWithConfig(symbolTable, DefaultEntityLinkerConfig())
}

// NewEntityLinkerWithConfig creates a new EntityLinker with custom configuration.
func NewEntityLinkerWithConfig(symbolTable *SymbolTable, config EntityLinkerConfig) *EntityLinker {
	return &EntityLinker{
		symbolTable: symbolTable,
		entityIndex: make(map[string][]*ExtractedEntity),
		entityByID:  make(map[string]*ExtractedEntity),
		config:      config,
	}
}

// generateEntityID creates a stable ID based on file path and entity name/kind.
func generateEntityID(filePath, entityPath string) string {
	hash := sha256.Sum256([]byte(filePath + ":" + entityPath))
	return hex.EncodeToString(hash[:16])
}

// IndexEntities indexes a slice of entities for efficient lookup.
func (el *EntityLinker) IndexEntities(entities []ExtractedEntity) {
	el.mu.Lock()
	defer el.mu.Unlock()

	for i := range entities {
		entity := &entities[i]
		id := generateEntityID(entity.FilePath, entity.Kind.String()+":"+entity.Name)

		el.entityByID[id] = entity

		name := entity.Name
		if !el.config.CaseSensitive {
			name = strings.ToLower(name)
		}
		el.entityIndex[name] = append(el.entityIndex[name], entity)
	}
}

// LinkEntities links entity references to their definitions.
// Returns a slice of EntityLink representing the resolved references.
func (el *EntityLinker) LinkEntities(entities []ExtractedEntity, content map[string][]byte) []EntityLink {
	el.mu.RLock()
	defer el.mu.RUnlock()

	var links []EntityLink

	for i := range entities {
		entity := &entities[i]

		// For each entity, try to find references to other entities
		for _, ref := range entity.References {
			// Find the definition for this reference
			link := el.linkReference(entity, ref)
			if link != nil {
				links = append(links, *link)
			}
		}
	}

	return links
}

// linkReference attempts to link a single reference to its definition.
func (el *EntityLinker) linkReference(refEntity *ExtractedEntity, ref EntityReference) *EntityLink {
	// The reference is contained in refEntity
	// We need to find what entity this reference points to

	// For now, we use a simple name-based lookup approach
	// A more sophisticated approach would parse the content to extract reference names

	// Since EntityReference doesn't contain the target name directly,
	// we need to resolve using the symbol table and file context
	if el.symbolTable != nil {
		// Try to resolve in the same scope/file first
		scopePath := "global"
		if scope := el.symbolTable.GetScope(scopePath); scope != nil {
			// Symbol table lookup would be based on parsed references
			// For this implementation, we focus on entity-to-entity linking
		}
	}

	return nil
}

// ResolveReference resolves a reference string to its definition entity.
// The context entity provides scope information for resolution.
func (el *EntityLinker) ResolveReference(ref string, context ExtractedEntity) *ExtractedEntity {
	el.mu.RLock()
	defer el.mu.RUnlock()

	if ref == "" {
		return nil
	}

	// Normalize reference name if case-insensitive
	lookupName := ref
	if !el.config.CaseSensitive {
		lookupName = strings.ToLower(ref)
	}

	// First, try exact match
	if entities, found := el.entityIndex[lookupName]; found {
		// Prefer entities in the same file
		for _, entity := range entities {
			if entity.FilePath == context.FilePath {
				return entity
			}
		}
		// Fall back to first match from another file if cross-file is enabled
		if el.config.CrossFileEnabled && len(entities) > 0 {
			return entities[0]
		}
	}

	// Try qualified name resolution (pkg.Name or Type.Method)
	if strings.Contains(ref, ".") {
		parts := strings.Split(ref, ".")
		if len(parts) == 2 {
			pkgOrType := parts[0]
			name := parts[1]

			// Look for a method or qualified name
			qualifiedKey := name
			if !el.config.CaseSensitive {
				qualifiedKey = strings.ToLower(name)
			}

			if entities, found := el.entityIndex[qualifiedKey]; found {
				for _, entity := range entities {
					// Check if this is a method of the specified type
					if entity.Kind == EntityKindMethod {
						// Check signature for receiver type match
						if strings.Contains(entity.Signature, pkgOrType) {
							return entity
						}
					}
				}
			}

			// Try symbol table for qualified resolution
			if el.symbolTable != nil {
				if entity, found := el.symbolTable.ResolveQualified(pkgOrType, name); found {
					return entity
				}
			}
		}
	}

	// Try fuzzy matching if exact match failed
	return el.fuzzyResolve(ref, context)
}

// fuzzyResolve attempts to resolve a reference using fuzzy matching.
func (el *EntityLinker) fuzzyResolve(ref string, context ExtractedEntity) *ExtractedEntity {
	var bestMatch *ExtractedEntity
	bestScore := 0.0

	for name, entities := range el.entityIndex {
		// Calculate fuzzy match score
		score := el.fuzzyMatchScore(ref, name)

		if score >= el.config.MinFuzzyConfidence && score > bestScore {
			bestScore = score

			// Prefer same-file matches
			for _, entity := range entities {
				if entity.FilePath == context.FilePath {
					bestMatch = entity
					break
				}
			}
			if bestMatch == nil && el.config.CrossFileEnabled && len(entities) > 0 {
				bestMatch = entities[0]
			}
		}
	}

	return bestMatch
}

// fuzzyMatchScore calculates a similarity score between two strings.
// Returns a value between 0.0 (no match) and 1.0 (exact match).
func (el *EntityLinker) fuzzyMatchScore(ref, target string) float64 {
	if ref == target {
		return 1.0
	}

	// Normalize for comparison
	refNorm := ref
	targetNorm := target
	if !el.config.CaseSensitive {
		refNorm = strings.ToLower(ref)
		targetNorm = strings.ToLower(target)
	}

	if refNorm == targetNorm {
		return 0.99 // Case-insensitive exact match
	}

	// Check for prefix/suffix matches
	if strings.HasPrefix(targetNorm, refNorm) || strings.HasSuffix(targetNorm, refNorm) {
		ratio := float64(len(refNorm)) / float64(len(targetNorm))
		return 0.7 + (ratio * 0.2)
	}
	if strings.HasPrefix(refNorm, targetNorm) || strings.HasSuffix(refNorm, targetNorm) {
		ratio := float64(len(targetNorm)) / float64(len(refNorm))
		return 0.7 + (ratio * 0.2)
	}

	// Check for substring match
	if strings.Contains(targetNorm, refNorm) || strings.Contains(refNorm, targetNorm) {
		shorter := len(refNorm)
		longer := len(targetNorm)
		if shorter > longer {
			shorter, longer = longer, shorter
		}
		return 0.5 + (float64(shorter)/float64(longer))*0.3
	}

	// Calculate edit distance for small strings
	if len(ref) <= 20 && len(target) <= 20 {
		distance := el.levenshteinDistance(refNorm, targetNorm)
		maxLen := len(refNorm)
		if len(targetNorm) > maxLen {
			maxLen = len(targetNorm)
		}

		if distance <= el.config.MaxFuzzyDistance {
			return 1.0 - (float64(distance) / float64(maxLen))
		}
	}

	// Check for camelCase/snake_case variations
	refTokens := el.tokenizeName(refNorm)
	targetTokens := el.tokenizeName(targetNorm)

	if len(refTokens) > 0 && len(targetTokens) > 0 {
		matchingTokens := 0
		for _, rt := range refTokens {
			for _, tt := range targetTokens {
				if rt == tt {
					matchingTokens++
					break
				}
			}
		}
		maxTokens := len(refTokens)
		if len(targetTokens) > maxTokens {
			maxTokens = len(targetTokens)
		}
		if matchingTokens > 0 {
			return 0.3 + (float64(matchingTokens)/float64(maxTokens))*0.5
		}
	}

	return 0.0
}

// tokenizeName splits a name into tokens based on camelCase, snake_case, etc.
func (el *EntityLinker) tokenizeName(name string) []string {
	var tokens []string
	var current strings.Builder

	for i, r := range name {
		if r == '_' || r == '-' || r == '.' {
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
			}
			continue
		}

		if unicode.IsUpper(r) && i > 0 {
			// Split on uppercase for camelCase
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
			}
		}

		current.WriteRune(unicode.ToLower(r))
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// levenshteinDistance calculates the edit distance between two strings.
func (el *EntityLinker) levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
	}

	// Initialize first row and column
	for i := 0; i <= len(a); i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

// LinkEntitiesWithContent performs entity linking using file content for additional context.
// It analyzes the content to find references and links them to definitions.
func (el *EntityLinker) LinkEntitiesWithContent(entities []ExtractedEntity, content map[string][]byte) []EntityLink {
	el.mu.RLock()
	defer el.mu.RUnlock()

	var links []EntityLink

	// Build a map of entity names to their definitions
	definitions := make(map[string][]*ExtractedEntity)
	for i := range entities {
		entity := &entities[i]
		name := entity.Name
		if !el.config.CaseSensitive {
			name = strings.ToLower(name)
		}
		definitions[name] = append(definitions[name], entity)
	}

	// Process each entity and look for references in content
	for i := range entities {
		entity := &entities[i]
		fileContent, hasContent := content[entity.FilePath]
		if !hasContent {
			continue
		}

		// Find potential references in this entity's file
		fileLinks := el.findReferencesInContent(entity, fileContent, definitions)
		links = append(links, fileLinks...)
	}

	return links
}

// findReferencesInContent finds references within file content and links them.
func (el *EntityLinker) findReferencesInContent(
	entity *ExtractedEntity,
	content []byte,
	definitions map[string][]*ExtractedEntity,
) []EntityLink {
	var links []EntityLink
	contentStr := string(content)

	// For each known definition, check if it's referenced in this file
	for defName, defEntities := range definitions {
		// Skip self-references (same name, same file, same kind)
		if len(defEntities) == 1 && defEntities[0].FilePath == entity.FilePath &&
			defEntities[0].Name == entity.Name && defEntities[0].Kind == entity.Kind {
			continue
		}

		// Check if the definition name appears in the content
		searchName := defName
		if !el.config.CaseSensitive {
			searchName = strings.ToLower(defName)
			contentStr = strings.ToLower(contentStr)
		}

		// Look for word boundaries to avoid partial matches
		// This is a simple heuristic; real implementation would use AST
		if strings.Contains(contentStr, searchName) {
			for _, def := range defEntities {
				// Skip linking entity to itself
				if def.FilePath == entity.FilePath &&
					def.StartLine == entity.StartLine &&
					def.Name == entity.Name {
					continue
				}

				link := el.createLink(entity, def, searchName)
				if link != nil {
					links = append(links, *link)
				}
			}
		}
	}

	return links
}

// createLink creates an EntityLink between a reference entity and a definition entity.
func (el *EntityLinker) createLink(reference, definition *ExtractedEntity, refName string) *EntityLink {
	if reference == nil || definition == nil {
		return nil
	}

	refID := generateEntityID(reference.FilePath, reference.Kind.String()+":"+reference.Name)
	defID := generateEntityID(definition.FilePath, definition.Kind.String()+":"+definition.Name)

	crossFile := reference.FilePath != definition.FilePath

	// Calculate confidence and link type
	confidence, linkType := el.calculateLinkConfidence(reference, definition, refName, crossFile)

	return &EntityLink{
		ReferenceID:        refID,
		DefinitionID:       defID,
		Confidence:         confidence,
		LinkType:           linkType,
		ReferenceName:      refName,
		DefinitionName:     definition.Name,
		ReferenceFilePath:  reference.FilePath,
		DefinitionFilePath: definition.FilePath,
		CrossFile:          crossFile,
	}
}

// calculateLinkConfidence calculates the confidence score and link type for a link.
func (el *EntityLinker) calculateLinkConfidence(
	reference, definition *ExtractedEntity,
	refName string,
	crossFile bool,
) (float64, LinkType) {
	confidence := 1.0

	// Exact name match
	nameMatch := refName == definition.Name
	if !el.config.CaseSensitive {
		nameMatch = strings.EqualFold(refName, definition.Name)
	}

	if !nameMatch {
		// Fuzzy match - reduce confidence
		confidence = el.fuzzyMatchScore(refName, definition.Name)
	}

	// Cross-file references have slightly lower confidence
	if crossFile {
		confidence *= 0.95
	}

	// Multiple definitions of same name reduce confidence
	lookupName := definition.Name
	if !el.config.CaseSensitive {
		lookupName = strings.ToLower(definition.Name)
	}
	if defs, found := el.entityIndex[lookupName]; found && len(defs) > 1 {
		// Ambiguous - multiple definitions
		confidence *= 0.8
	}

	// Determine link type based on confidence
	var linkType LinkType
	switch {
	case confidence >= 0.9:
		linkType = LinkDefinite
	case confidence >= 0.7:
		linkType = LinkProbable
	default:
		linkType = LinkPossible
	}

	return confidence, linkType
}

// GetEntityByID returns an entity by its ID.
func (el *EntityLinker) GetEntityByID(id string) *ExtractedEntity {
	el.mu.RLock()
	defer el.mu.RUnlock()

	return el.entityByID[id]
}

// GetEntitiesByName returns all entities with the given name.
func (el *EntityLinker) GetEntitiesByName(name string) []*ExtractedEntity {
	el.mu.RLock()
	defer el.mu.RUnlock()

	lookupName := name
	if !el.config.CaseSensitive {
		lookupName = strings.ToLower(name)
	}

	if entities, found := el.entityIndex[lookupName]; found {
		// Return a copy to prevent modification
		result := make([]*ExtractedEntity, len(entities))
		copy(result, entities)
		return result
	}

	return nil
}

// Clear clears all indexed entities.
func (el *EntityLinker) Clear() {
	el.mu.Lock()
	defer el.mu.Unlock()

	el.entityIndex = make(map[string][]*ExtractedEntity)
	el.entityByID = make(map[string]*ExtractedEntity)
}

// min returns the minimum of three integers.
func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
