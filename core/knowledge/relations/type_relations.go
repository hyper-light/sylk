package relations

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/extractors"
)

// =============================================================================
// Type Relation Type Enum
// =============================================================================

// TypeRelationType represents the kind of relationship between types.
type TypeRelationType int

const (
	TypeRelationExtends    TypeRelationType = 0 // Class/type extends another (inheritance)
	TypeRelationImplements TypeRelationType = 1 // Type implements an interface
	TypeRelationEmbeds     TypeRelationType = 2 // Go struct embedding
	TypeRelationUses       TypeRelationType = 3 // Type uses another as field/parameter
)

// String returns the string representation of the type relation type.
func (trt TypeRelationType) String() string {
	switch trt {
	case TypeRelationExtends:
		return "extends"
	case TypeRelationImplements:
		return "implements"
	case TypeRelationEmbeds:
		return "embeds"
	case TypeRelationUses:
		return "uses"
	default:
		return "unknown"
	}
}

// ParseTypeRelationType parses a string into a TypeRelationType.
func ParseTypeRelationType(s string) (TypeRelationType, bool) {
	switch s {
	case "extends":
		return TypeRelationExtends, true
	case "implements":
		return TypeRelationImplements, true
	case "embeds":
		return TypeRelationEmbeds, true
	case "uses":
		return TypeRelationUses, true
	default:
		return TypeRelationExtends, false
	}
}

// =============================================================================
// Type Relation
// =============================================================================

// TypeRelation represents a relationship between two types.
type TypeRelation struct {
	ID           string           `json:"id"`
	TypeA        string           `json:"type_a"`
	TypeB        string           `json:"type_b"`
	RelationType TypeRelationType `json:"relation_type"`
	FilePath     string           `json:"file_path"`
	Line         int              `json:"line"`
}

// =============================================================================
// Type Relation Extractor
// =============================================================================

// TypeRelationExtractor extracts type relationships from source code.
type TypeRelationExtractor struct {
	// TypeScript/JavaScript patterns
	tsClassExtendsPattern    *regexp.Regexp
	tsClassImplementsPattern *regexp.Regexp
	tsInterfaceExtendsPattern *regexp.Regexp

	// Python patterns
	pyClassInheritancePattern *regexp.Regexp
}

// NewTypeRelationExtractor creates a new TypeRelationExtractor.
func NewTypeRelationExtractor() *TypeRelationExtractor {
	return &TypeRelationExtractor{
		// TypeScript: class X extends Y
		tsClassExtendsPattern: regexp.MustCompile(`(?m)class\s+(\w+)(?:<[^>]*>)?\s+extends\s+(\w+)(?:<[^>]*>)?`),
		// TypeScript: class X implements Y, Z
		tsClassImplementsPattern: regexp.MustCompile(`(?m)class\s+(\w+)(?:<[^>]*>)?(?:\s+extends\s+\w+(?:<[^>]*>)?)?\s+implements\s+([\w,\s<>]+)\s*\{`),
		// TypeScript: interface X extends Y, Z
		tsInterfaceExtendsPattern: regexp.MustCompile(`(?m)interface\s+(\w+)(?:<[^>]*>)?\s+extends\s+([\w,\s<>]+)\s*\{`),

		// Python: class X(Y, Z):
		pyClassInheritancePattern: regexp.MustCompile(`(?m)^[\t ]*class\s+(\w+)\s*\(([^)]+)\)\s*:`),
	}
}

// ExtractRelations extracts type relationships from the provided entities and content.
func (e *TypeRelationExtractor) ExtractRelations(entities []extractors.Entity, content map[string][]byte) ([]knowledge.ExtractedRelation, error) {
	var relations []knowledge.ExtractedRelation

	// Build entity lookup maps
	typeEntities := make(map[string][]extractors.Entity)
	for _, entity := range entities {
		if entity.Kind == knowledge.EntityKindType ||
			entity.Kind == knowledge.EntityKindStruct ||
			entity.Kind == knowledge.EntityKindInterface {
			typeEntities[entity.Name] = append(typeEntities[entity.Name], entity)
		}
	}

	// Process each file
	for filePath, fileContent := range content {
		fileRelations := e.extractTypeRelationsFromFile(filePath, fileContent, typeEntities)
		relations = append(relations, fileRelations...)
	}

	return relations, nil
}

// extractTypeRelationsFromFile extracts type relationships from a single file.
func (e *TypeRelationExtractor) extractTypeRelationsFromFile(
	filePath string,
	content []byte,
	typeEntities map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Determine language by extension
	if strings.HasSuffix(filePath, ".go") {
		relations = e.extractGoTypeRelations(filePath, content, typeEntities)
	} else if strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".jsx") {
		relations = e.extractTSTypeRelations(filePath, content, typeEntities)
	} else if strings.HasSuffix(filePath, ".py") {
		relations = e.extractPyTypeRelations(filePath, content, typeEntities)
	}

	return relations
}

// extractGoTypeRelations extracts type relationships from Go source code using AST.
func (e *TypeRelationExtractor) extractGoTypeRelations(
	filePath string,
	content []byte,
	typeEntities map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	if len(content) == 0 {
		return relations
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil {
		return relations
	}

	lines := strings.Split(string(content), "\n")

	// Walk through all declarations
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			typeName := typeSpec.Name.Name
			line := fset.Position(typeSpec.Pos()).Line

			switch t := typeSpec.Type.(type) {
			case *ast.StructType:
				// Extract struct embedding and field type usage
				relations = append(relations, e.extractGoStructRelations(filePath, typeName, t, line, lines, typeEntities, fset)...)

			case *ast.InterfaceType:
				// Extract interface embedding
				relations = append(relations, e.extractGoInterfaceRelations(filePath, typeName, t, line, lines, typeEntities, fset)...)
			}
		}
	}

	return relations
}

// extractGoStructRelations extracts relationships from a Go struct type.
func (e *TypeRelationExtractor) extractGoStructRelations(
	filePath string,
	structName string,
	structType *ast.StructType,
	structLine int,
	lines []string,
	typeEntities map[string][]extractors.Entity,
	fset *token.FileSet,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	if structType.Fields == nil {
		return relations
	}

	for _, field := range structType.Fields.List {
		fieldLine := fset.Position(field.Pos()).Line

		// Check for embedded fields (no name, just type)
		if len(field.Names) == 0 {
			// This is an embedded type
			embeddedTypeName := e.extractGoTypeName(field.Type)
			if embeddedTypeName != "" {
				rel := e.createTypeRelation(
					filePath, structName, embeddedTypeName,
					TypeRelationEmbeds, fieldLine, lines, typeEntities,
				)
				relations = append(relations, rel)
			}
		} else {
			// Regular field - extract "uses" relationship
			fieldTypeName := e.extractGoTypeName(field.Type)
			if fieldTypeName != "" && !e.isBuiltinType(fieldTypeName) {
				rel := e.createTypeRelation(
					filePath, structName, fieldTypeName,
					TypeRelationUses, fieldLine, lines, typeEntities,
				)
				relations = append(relations, rel)
			}
		}
	}

	return relations
}

// extractGoInterfaceRelations extracts relationships from a Go interface type.
func (e *TypeRelationExtractor) extractGoInterfaceRelations(
	filePath string,
	interfaceName string,
	interfaceType *ast.InterfaceType,
	interfaceLine int,
	lines []string,
	typeEntities map[string][]extractors.Entity,
	fset *token.FileSet,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	if interfaceType.Methods == nil {
		return relations
	}

	for _, method := range interfaceType.Methods.List {
		methodLine := fset.Position(method.Pos()).Line

		// Check for embedded interfaces (no names, just type)
		if len(method.Names) == 0 {
			embeddedTypeName := e.extractGoTypeName(method.Type)
			if embeddedTypeName != "" {
				// Interface embedding is treated as "extends" for interfaces
				rel := e.createTypeRelation(
					filePath, interfaceName, embeddedTypeName,
					TypeRelationExtends, methodLine, lines, typeEntities,
				)
				relations = append(relations, rel)
			}
		}
	}

	return relations
}

// extractGoTypeName extracts the type name from an AST expression.
func (e *TypeRelationExtractor) extractGoTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return e.extractGoTypeName(t.X)
	case *ast.SelectorExpr:
		// pkg.Type - return just the type name
		return t.Sel.Name
	case *ast.ArrayType:
		return e.extractGoTypeName(t.Elt)
	case *ast.MapType:
		// For maps, we could extract both key and value types
		// For simplicity, return empty (could be extended)
		return ""
	case *ast.IndexExpr:
		// Generic type: T[U]
		return e.extractGoTypeName(t.X)
	case *ast.IndexListExpr:
		// Generic type with multiple params: T[U, V]
		return e.extractGoTypeName(t.X)
	default:
		return ""
	}
}

// isBuiltinType checks if a type name is a Go builtin type.
func (e *TypeRelationExtractor) isBuiltinType(typeName string) bool {
	builtins := map[string]bool{
		"bool": true, "string": true, "int": true, "int8": true, "int16": true,
		"int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true,
		"uint32": true, "uint64": true, "uintptr": true, "byte": true, "rune": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"error": true, "any": true,
	}
	return builtins[typeName]
}

// extractTSTypeRelations extracts type relationships from TypeScript/JavaScript source code.
func (e *TypeRelationExtractor) extractTSTypeRelations(
	filePath string,
	content []byte,
	typeEntities map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")

	// Extract class extends relationships
	extendsMatches := e.tsClassExtendsPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range extendsMatches {
		if len(match) >= 6 && match[2] != -1 && match[3] != -1 && match[4] != -1 && match[5] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			className := source[match[2]:match[3]]
			parentName := source[match[4]:match[5]]

			rel := e.createTypeRelation(filePath, className, parentName, TypeRelationExtends, line, lines, typeEntities)
			relations = append(relations, rel)
		}
	}

	// Extract class implements relationships
	implementsMatches := e.tsClassImplementsPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range implementsMatches {
		if len(match) >= 6 && match[2] != -1 && match[3] != -1 && match[4] != -1 && match[5] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			className := source[match[2]:match[3]]
			interfacesPart := source[match[4]:match[5]]

			// Parse multiple interfaces
			interfaces := e.parseTypeList(interfacesPart)
			for _, iface := range interfaces {
				rel := e.createTypeRelation(filePath, className, iface, TypeRelationImplements, line, lines, typeEntities)
				relations = append(relations, rel)
			}
		}
	}

	// Extract interface extends relationships
	interfaceExtendsMatches := e.tsInterfaceExtendsPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range interfaceExtendsMatches {
		if len(match) >= 6 && match[2] != -1 && match[3] != -1 && match[4] != -1 && match[5] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			interfaceName := source[match[2]:match[3]]
			parentsPart := source[match[4]:match[5]]

			// Parse multiple parent interfaces
			parents := e.parseTypeList(parentsPart)
			for _, parent := range parents {
				rel := e.createTypeRelation(filePath, interfaceName, parent, TypeRelationExtends, line, lines, typeEntities)
				relations = append(relations, rel)
			}
		}
	}

	return relations
}

// extractPyTypeRelations extracts type relationships from Python source code.
func (e *TypeRelationExtractor) extractPyTypeRelations(
	filePath string,
	content []byte,
	typeEntities map[string][]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")

	// Extract class inheritance
	inheritanceMatches := e.pyClassInheritancePattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range inheritanceMatches {
		if len(match) >= 6 && match[2] != -1 && match[3] != -1 && match[4] != -1 && match[5] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			className := source[match[2]:match[3]]
			parentsPart := source[match[4]:match[5]]

			// Parse multiple parent classes
			parents := e.parsePythonParents(parentsPart)
			for _, parent := range parents {
				rel := e.createTypeRelation(filePath, className, parent, TypeRelationExtends, line, lines, typeEntities)
				relations = append(relations, rel)
			}
		}
	}

	return relations
}

// parseTypeList parses a comma-separated list of type names, handling generics.
func (e *TypeRelationExtractor) parseTypeList(typeList string) []string {
	var types []string
	var current strings.Builder
	depth := 0

	for _, ch := range typeList {
		switch ch {
		case '<':
			depth++
			current.WriteRune(ch)
		case '>':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				typeName := strings.TrimSpace(current.String())
				// Remove generic parameters for the type name
				if idx := strings.Index(typeName, "<"); idx > 0 {
					typeName = typeName[:idx]
				}
				if typeName != "" {
					types = append(types, typeName)
				}
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	// Handle last type
	typeName := strings.TrimSpace(current.String())
	if idx := strings.Index(typeName, "<"); idx > 0 {
		typeName = typeName[:idx]
	}
	if typeName != "" {
		types = append(types, typeName)
	}

	return types
}

// parsePythonParents parses Python parent class list, handling metaclass and keywords.
func (e *TypeRelationExtractor) parsePythonParents(parentList string) []string {
	var parents []string

	// Split by comma, but handle nested parentheses
	var current strings.Builder
	depth := 0

	for _, ch := range parentList {
		switch ch {
		case '(':
			depth++
			current.WriteRune(ch)
		case ')':
			depth--
			current.WriteRune(ch)
		case '[':
			depth++
			current.WriteRune(ch)
		case ']':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				parent := strings.TrimSpace(current.String())
				if parent != "" && !strings.Contains(parent, "=") {
					// Remove generic type parameters like [T]
					if idx := strings.Index(parent, "["); idx > 0 {
						parent = parent[:idx]
					}
					parents = append(parents, parent)
				}
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	// Handle last parent
	parent := strings.TrimSpace(current.String())
	if parent != "" && !strings.Contains(parent, "=") {
		if idx := strings.Index(parent, "["); idx > 0 {
			parent = parent[:idx]
		}
		parents = append(parents, parent)
	}

	return parents
}

// createTypeRelation creates an ExtractedRelation for a type relationship.
func (e *TypeRelationExtractor) createTypeRelation(
	filePath string,
	typeA string,
	typeB string,
	relationType TypeRelationType,
	line int,
	lines []string,
	typeEntities map[string][]extractors.Entity,
) knowledge.ExtractedRelation {
	// Generate stable relation ID
	relationID := generateRelationID(typeA, typeB, relationType.String())

	// Map TypeRelationType to knowledge.RelationType
	var knowledgeRelType knowledge.RelationType
	switch relationType {
	case TypeRelationExtends:
		knowledgeRelType = knowledge.RelExtends
	case TypeRelationImplements:
		knowledgeRelType = knowledge.RelImplements
	case TypeRelationEmbeds:
		knowledgeRelType = knowledge.RelContains
	case TypeRelationUses:
		knowledgeRelType = knowledge.RelUses
	default:
		knowledgeRelType = knowledge.RelReferences
	}

	// Create source entity (TypeA)
	sourceEntity := &knowledge.ExtractedEntity{
		Name:     typeA,
		Kind:     knowledge.EntityKindType,
		FilePath: filePath,
	}

	// Try to find existing entity for TypeA
	if entities, found := typeEntities[typeA]; found && len(entities) > 0 {
		// Prefer entity from same file
		for _, entity := range entities {
			if entity.FilePath == filePath {
				sourceEntity.Kind = entity.Kind
				sourceEntity.StartLine = entity.StartLine
				sourceEntity.EndLine = entity.EndLine
				sourceEntity.Signature = entity.Signature
				break
			}
		}
		if sourceEntity.StartLine == 0 {
			sourceEntity.Kind = entities[0].Kind
			sourceEntity.StartLine = entities[0].StartLine
			sourceEntity.EndLine = entities[0].EndLine
			sourceEntity.Signature = entities[0].Signature
		}
	}

	// Create target entity (TypeB)
	targetEntity := &knowledge.ExtractedEntity{
		Name:     typeB,
		Kind:     knowledge.EntityKindType,
		FilePath: "", // May be external
	}

	// Try to find existing entity for TypeB
	if entities, found := typeEntities[typeB]; found && len(entities) > 0 {
		targetEntity.Kind = entities[0].Kind
		targetEntity.FilePath = entities[0].FilePath
		targetEntity.StartLine = entities[0].StartLine
		targetEntity.EndLine = entities[0].EndLine
		targetEntity.Signature = entities[0].Signature
	}

	// Extract evidence snippet
	snippet := ""
	if line > 0 && line <= len(lines) {
		snippet = strings.TrimSpace(lines[line-1])
	}

	evidence := []knowledge.EvidenceSpan{
		{
			FilePath:  filePath,
			StartLine: line,
			EndLine:   line,
			Snippet:   snippet,
		},
	}

	// Calculate confidence
	confidence := 1.0
	if targetEntity.FilePath == "" {
		// External type reference
		confidence = 0.8
	}

	_ = relationID // For stable ID generation

	return knowledge.ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: knowledgeRelType,
		Evidence:     evidence,
		Confidence:   confidence,
		ExtractedAt:  time.Now(),
	}
}
