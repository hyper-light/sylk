package relations

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/extractors"
)

// =============================================================================
// Import Type Enum
// =============================================================================

// ImportType represents the type of import relationship.
type ImportType int

const (
	ImportTypeStandard ImportType = 0 // Standard library/module import
	ImportTypeRelative ImportType = 1 // Relative path import (e.g., ./foo, ../bar)
	ImportTypePackage  ImportType = 2 // External package import
	ImportTypeAlias    ImportType = 3 // Aliased import (e.g., import x as y)
	ImportTypeSelective ImportType = 4 // Selective import (e.g., from x import y)
)

// String returns the string representation of the import type.
func (it ImportType) String() string {
	switch it {
	case ImportTypeStandard:
		return "standard"
	case ImportTypeRelative:
		return "relative"
	case ImportTypePackage:
		return "package"
	case ImportTypeAlias:
		return "alias"
	case ImportTypeSelective:
		return "selective"
	default:
		return "unknown"
	}
}

// ParseImportType parses a string into an ImportType.
func ParseImportType(s string) (ImportType, bool) {
	switch s {
	case "standard":
		return ImportTypeStandard, true
	case "relative":
		return ImportTypeRelative, true
	case "package":
		return ImportTypePackage, true
	case "alias":
		return ImportTypeAlias, true
	case "selective":
		return ImportTypeSelective, true
	default:
		return ImportTypeStandard, false
	}
}

// =============================================================================
// Import Relation
// =============================================================================

// ImportRelation represents an import/dependency relationship.
type ImportRelation struct {
	ID               string     `json:"id"`
	SourceFileID     string     `json:"source_file_id"`
	ImportedModuleID string     `json:"imported_module_id"`
	ImportType       ImportType `json:"import_type"`
	ImportPath       string     `json:"import_path"`
	Alias            string     `json:"alias,omitempty"`
	ImportedNames    []string   `json:"imported_names,omitempty"`
	Line             int        `json:"line"`
}

// =============================================================================
// Import Graph Extractor
// =============================================================================

// ImportGraphExtractor extracts import/dependency relationships from source code.
type ImportGraphExtractor struct {
	// Go import patterns
	goSingleImportPattern *regexp.Regexp
	goMultiImportPattern  *regexp.Regexp
	goImportLinePattern   *regexp.Regexp

	// TypeScript/JavaScript import patterns
	tsImportPattern        *regexp.Regexp
	tsImportFromPattern    *regexp.Regexp
	tsRequirePattern       *regexp.Regexp
	tsDynamicImportPattern *regexp.Regexp

	// Python import patterns
	pyImportPattern     *regexp.Regexp
	pyFromImportPattern *regexp.Regexp
}

// NewImportGraphExtractor creates a new ImportGraphExtractor.
func NewImportGraphExtractor() *ImportGraphExtractor {
	return &ImportGraphExtractor{
		// Go: import "package" or import alias "package"
		goSingleImportPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+(?:(\w+)\s+)?["']([^"']+)["']`),
		// Go: import ( ... )
		goMultiImportPattern: regexp.MustCompile(`(?ms)import\s*\(\s*(.*?)\s*\)`),
		// Go: individual import line within import block
		goImportLinePattern: regexp.MustCompile(`(?m)^\s*(?:(\w+)\s+)?["']([^"']+)["']`),

		// TypeScript/JavaScript: import ... from "module"
		tsImportPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+(?:type\s+)?(?:(\*\s+as\s+\w+)|(\{[^}]*\})|(\w+))?(?:\s*,\s*(?:(\{[^}]*\})|(\w+)))?\s*from\s*["']([^"']+)["']`),
		// TypeScript/JavaScript: import "module" (side-effect)
		tsImportFromPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+["']([^"']+)["']`),
		// CommonJS: require("module")
		tsRequirePattern: regexp.MustCompile(`(?m)require\s*\(\s*["']([^"']+)["']\s*\)`),
		// Dynamic import: import("module")
		tsDynamicImportPattern: regexp.MustCompile(`(?m)import\s*\(\s*["']([^"']+)["']\s*\)`),

		// Python: import module or import module as alias
		pyImportPattern: regexp.MustCompile(`(?m)^[\t ]*import\s+([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)*)(?:\s+as\s+(\w+))?`),
		// Python: from module import ... or from module import ... as alias
		pyFromImportPattern: regexp.MustCompile(`(?m)^[\t ]*from\s+([a-zA-Z_\.][a-zA-Z0-9_\.]*)\s+import\s+(.+)`),
	}
}

// ExtractRelations extracts import relationships from the provided entities and content.
func (e *ImportGraphExtractor) ExtractRelations(entities []extractors.Entity, content map[string][]byte) ([]knowledge.ExtractedRelation, error) {
	var relations []knowledge.ExtractedRelation

	// Build entity lookup by file path
	fileEntities := make(map[string]extractors.Entity)
	for _, entity := range entities {
		if entity.Kind == knowledge.EntityKindFile || entity.Kind == knowledge.EntityKindPackage {
			fileEntities[entity.FilePath] = entity
		}
	}

	// Process each file
	for filePath, fileContent := range content {
		fileRelations := e.extractImportsFromFile(filePath, fileContent, fileEntities)
		relations = append(relations, fileRelations...)
	}

	return relations, nil
}

// extractImportsFromFile extracts imports from a single file.
func (e *ImportGraphExtractor) extractImportsFromFile(
	filePath string,
	content []byte,
	fileEntities map[string]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation

	// Determine language by extension
	if strings.HasSuffix(filePath, ".go") {
		relations = e.extractGoImports(filePath, content, fileEntities)
	} else if strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".jsx") {
		relations = e.extractTSImports(filePath, content, fileEntities)
	} else if strings.HasSuffix(filePath, ".py") {
		relations = e.extractPyImports(filePath, content, fileEntities)
	}

	return relations
}

// extractGoImports extracts import statements from Go source code.
func (e *ImportGraphExtractor) extractGoImports(
	filePath string,
	content []byte,
	fileEntities map[string]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")

	// Handle single-line imports
	singleMatches := e.goSingleImportPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range singleMatches {
		if len(match) >= 6 {
			line := strings.Count(source[:match[0]], "\n") + 1
			alias := ""
			if match[2] != -1 && match[3] != -1 {
				alias = source[match[2]:match[3]]
			}
			importPath := source[match[4]:match[5]]

			rel := e.createImportRelation(filePath, importPath, alias, nil, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	// Handle multi-line import blocks
	multiMatches := e.goMultiImportPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range multiMatches {
		if len(match) >= 4 && match[2] != -1 && match[3] != -1 {
			blockStart := strings.Count(source[:match[0]], "\n") + 1
			block := source[match[2]:match[3]]
			blockLines := strings.Split(block, "\n")

			for i, blockLine := range blockLines {
				lineMatches := e.goImportLinePattern.FindAllStringSubmatch(blockLine, -1)
				for _, lineMatch := range lineMatches {
					if len(lineMatch) >= 3 {
						alias := lineMatch[1]
						importPath := lineMatch[2]
						line := blockStart + i + 1

						rel := e.createImportRelation(filePath, importPath, alias, nil, line, lines, fileEntities)
						relations = append(relations, rel)
					}
				}
			}
		}
	}

	return relations
}

// extractTSImports extracts import statements from TypeScript/JavaScript source code.
func (e *ImportGraphExtractor) extractTSImports(
	filePath string,
	content []byte,
	fileEntities map[string]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")

	// Handle ES6 imports: import X from "module"
	importMatches := e.tsImportPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range importMatches {
		if len(match) >= 14 && match[12] != -1 && match[13] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			importPath := source[match[12]:match[13]]

			// Extract imported names
			var importedNames []string
			alias := ""

			// Namespace import: * as X
			if match[2] != -1 && match[3] != -1 {
				namespaceImport := source[match[2]:match[3]]
				if strings.Contains(namespaceImport, " as ") {
					parts := strings.Split(namespaceImport, " as ")
					if len(parts) >= 2 {
						alias = strings.TrimSpace(parts[1])
					}
				}
			}

			// Named imports: { a, b, c }
			if match[4] != -1 && match[5] != -1 {
				namedImports := source[match[4]:match[5]]
				namedImports = strings.Trim(namedImports, "{}")
				for _, name := range strings.Split(namedImports, ",") {
					name = strings.TrimSpace(name)
					// Handle "x as y" renames
					if strings.Contains(name, " as ") {
						parts := strings.Split(name, " as ")
						name = strings.TrimSpace(parts[0])
					}
					if name != "" {
						importedNames = append(importedNames, name)
					}
				}
			}

			// Default import: import X
			if match[6] != -1 && match[7] != -1 {
				defaultImport := source[match[6]:match[7]]
				importedNames = append(importedNames, defaultImport)
			}

			rel := e.createImportRelation(filePath, importPath, alias, importedNames, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	// Handle side-effect imports: import "module"
	sideEffectMatches := e.tsImportFromPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range sideEffectMatches {
		// Skip if already matched by the more specific pattern
		if len(match) >= 4 && match[2] != -1 && match[3] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			importPath := source[match[2]:match[3]]

			// Check if this line was already processed
			lineContent := ""
			if line <= len(lines) {
				lineContent = lines[line-1]
			}
			if strings.Contains(lineContent, "from") {
				continue // Already handled by tsImportPattern
			}

			rel := e.createImportRelation(filePath, importPath, "", nil, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	// Handle require(): const x = require("module")
	requireMatches := e.tsRequirePattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range requireMatches {
		if len(match) >= 4 && match[2] != -1 && match[3] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			importPath := source[match[2]:match[3]]

			rel := e.createImportRelation(filePath, importPath, "", nil, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	return relations
}

// extractPyImports extracts import statements from Python source code.
func (e *ImportGraphExtractor) extractPyImports(
	filePath string,
	content []byte,
	fileEntities map[string]extractors.Entity,
) []knowledge.ExtractedRelation {
	var relations []knowledge.ExtractedRelation
	source := string(content)
	lines := strings.Split(source, "\n")

	// Handle: import module or import module as alias
	importMatches := e.pyImportPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range importMatches {
		if len(match) >= 4 && match[2] != -1 && match[3] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			importPath := source[match[2]:match[3]]
			alias := ""
			if len(match) >= 6 && match[4] != -1 && match[5] != -1 {
				alias = source[match[4]:match[5]]
			}

			rel := e.createImportRelation(filePath, importPath, alias, nil, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	// Handle: from module import x, y, z or from module import x as alias
	fromMatches := e.pyFromImportPattern.FindAllStringSubmatchIndex(source, -1)
	for _, match := range fromMatches {
		if len(match) >= 6 && match[2] != -1 && match[3] != -1 && match[4] != -1 && match[5] != -1 {
			line := strings.Count(source[:match[0]], "\n") + 1
			importPath := source[match[2]:match[3]]
			importsPart := source[match[4]:match[5]]

			// Parse imported names
			var importedNames []string
			// Handle parenthesized imports
			importsPart = strings.Trim(importsPart, "()")
			// Handle continuation lines (backslash)
			importsPart = strings.ReplaceAll(importsPart, "\\", "")
			importsPart = strings.ReplaceAll(importsPart, "\n", "")

			for _, name := range strings.Split(importsPart, ",") {
				name = strings.TrimSpace(name)
				// Handle "x as y" renames
				if strings.Contains(name, " as ") {
					parts := strings.Split(name, " as ")
					name = strings.TrimSpace(parts[0])
				}
				if name != "" && name != "*" {
					importedNames = append(importedNames, name)
				}
			}

			rel := e.createImportRelation(filePath, importPath, "", importedNames, line, lines, fileEntities)
			relations = append(relations, rel)
		}
	}

	return relations
}

// createImportRelation creates an ExtractedRelation for an import.
func (e *ImportGraphExtractor) createImportRelation(
	sourceFilePath string,
	importPath string,
	alias string,
	importedNames []string,
	line int,
	lines []string,
	fileEntities map[string]extractors.Entity,
) knowledge.ExtractedRelation {
	// Determine import type
	importType := e.determineImportType(importPath, importedNames)

	// Generate stable relation ID
	relationID := generateRelationID(sourceFilePath, importPath, "imports")

	// Create source entity (the file doing the import)
	sourceEntity := &knowledge.ExtractedEntity{
		Name:      filepath.Base(sourceFilePath),
		Kind:      knowledge.EntityKindFile,
		FilePath:  sourceFilePath,
		StartLine: 1,
		EndLine:   len(lines),
	}

	// Try to find existing entity for source file
	if entity, found := fileEntities[sourceFilePath]; found {
		sourceEntity.Name = entity.Name
		sourceEntity.Kind = entity.Kind
	}

	// Create target entity (the imported module)
	// Try to resolve relative imports
	resolvedPath := e.resolveImportPath(sourceFilePath, importPath)
	targetEntity := &knowledge.ExtractedEntity{
		Name:      filepath.Base(importPath),
		Kind:      knowledge.EntityKindImport,
		FilePath:  resolvedPath,
		StartLine: 0,
		EndLine:   0,
	}

	// Try to find existing entity for target module
	if entity, found := fileEntities[resolvedPath]; found {
		targetEntity.Name = entity.Name
		targetEntity.Kind = entity.Kind
		targetEntity.StartLine = entity.StartLine
		targetEntity.EndLine = entity.EndLine
	}

	// Extract evidence snippet
	snippet := ""
	if line > 0 && line <= len(lines) {
		snippet = strings.TrimSpace(lines[line-1])
	}

	evidence := []knowledge.EvidenceSpan{
		{
			FilePath:  sourceFilePath,
			StartLine: line,
			EndLine:   line,
			Snippet:   snippet,
		},
	}

	// Calculate confidence
	confidence := 1.0
	if importType == ImportTypeRelative {
		// Relative imports might not resolve correctly
		confidence = 0.9
	}

	_ = relationID // We include this for stable ID generation but ExtractedRelation doesn't have ID field

	return knowledge.ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: knowledge.RelImports,
		Evidence:     evidence,
		Confidence:   confidence,
		ExtractedAt:  time.Now(),
	}
}

// determineImportType determines the type of import based on the path and names.
func (e *ImportGraphExtractor) determineImportType(importPath string, importedNames []string) ImportType {
	// Relative imports
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") || strings.HasPrefix(importPath, ".") {
		return ImportTypeRelative
	}

	// Selective imports (from x import y)
	if len(importedNames) > 0 {
		return ImportTypeSelective
	}

	// Standard library detection (heuristic)
	// Go: single word without dots typically is standard library
	// Python: common standard library modules
	// Node: node: prefix
	if !strings.Contains(importPath, "/") && !strings.Contains(importPath, ".") {
		return ImportTypeStandard
	}

	if strings.HasPrefix(importPath, "node:") {
		return ImportTypeStandard
	}

	return ImportTypePackage
}

// resolveImportPath attempts to resolve a relative import path to an absolute path.
func (e *ImportGraphExtractor) resolveImportPath(sourceFilePath, importPath string) string {
	// Handle relative imports
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		sourceDir := filepath.Dir(sourceFilePath)
		resolved := filepath.Join(sourceDir, importPath)
		return filepath.Clean(resolved)
	}

	// For non-relative imports, return as-is (would need module resolution for full accuracy)
	return importPath
}
