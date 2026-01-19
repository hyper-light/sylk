package extractors

import (
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/knowledge"
)

// PythonExtractor extracts entities from Python source files using regex patterns.
type PythonExtractor struct {
	// Compiled regex patterns for different entity types
	functionPattern     *regexp.Regexp
	asyncFunctionPattern *regexp.Regexp
	classPattern        *regexp.Regexp
	methodPattern       *regexp.Regexp
	asyncMethodPattern  *regexp.Regexp
	decoratorPattern    *regexp.Regexp
}

// NewPythonExtractor creates a new Python entity extractor.
func NewPythonExtractor() *PythonExtractor {
	return &PythonExtractor{
		// Function declarations: def name(...):
		functionPattern: regexp.MustCompile(`(?m)^([\t ]*)def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Async function declarations: async def name(...):
		asyncFunctionPattern: regexp.MustCompile(`(?m)^([\t ]*)async\s+def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Class declarations: class Name or class Name(Base):
		classPattern: regexp.MustCompile(`(?m)^([\t ]*)class\s+(\w+)(\s*\([^)]*\))?\s*:`),

		// Method declarations (indented def inside class)
		methodPattern: regexp.MustCompile(`(?m)^([\t ]+)def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Async method declarations
		asyncMethodPattern: regexp.MustCompile(`(?m)^([\t ]+)async\s+def\s+(\w+)\s*\(([^)]*)\)(\s*->\s*[^:]+)?\s*:`),

		// Decorator pattern to detect decorated functions
		decoratorPattern: regexp.MustCompile(`(?m)^([\t ]*)@[\w.]+(\([^)]*\))?\s*$`),
	}
}

// Extract parses Python source code and extracts functions, classes, and methods.
func (e *PythonExtractor) Extract(filePath string, content []byte) ([]Entity, error) {
	if len(content) == 0 {
		return []Entity{}, nil
	}

	source := string(content)
	lines := strings.Split(source, "\n")
	var entities []Entity

	// First pass: extract classes
	classEntities := e.extractClasses(filePath, source, lines)
	entities = append(entities, classEntities...)

	// Build a map of class ranges for method association
	classRanges := make(map[string]struct {
		id        string
		startLine int
		endLine   int
		indent    int
	})

	for _, ce := range classEntities {
		indent := e.getIndentLevel(lines[ce.StartLine-1])
		classRanges[ce.Name] = struct {
			id        string
			startLine int
			endLine   int
			indent    int
		}{
			id:        ce.ID,
			startLine: ce.StartLine,
			endLine:   ce.EndLine,
			indent:    indent,
		}
	}

	// Extract functions (top-level only) and methods (within classes)
	entities = append(entities, e.extractFunctionsAndMethods(filePath, source, lines, classRanges)...)

	return entities, nil
}

// extractClasses extracts class declarations.
func (e *PythonExtractor) extractClasses(filePath, source string, lines []string) []Entity {
	var entities []Entity
	matches := e.classPattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)

			// Get indentation
			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}

			// Extract class name (group 2)
			name := source[match[4]:match[5]]

			// Find the end of the class (based on indentation)
			endLine := e.findPythonBlockEnd(lines, startLine, len(indent))

			// Build signature
			var sig strings.Builder
			sig.WriteString("class ")
			sig.WriteString(name)
			if match[6] != -1 && match[7] != -1 {
				sig.WriteString(source[match[6]:match[7]])
			}

			entity := Entity{
				ID:        generateEntityID(filePath, "class:"+name),
				Name:      name,
				Kind:      knowledge.EntityKindType,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// extractFunctionsAndMethods extracts function and method declarations.
func (e *PythonExtractor) extractFunctionsAndMethods(filePath, source string, lines []string, classRanges map[string]struct {
	id        string
	startLine int
	endLine   int
	indent    int
}) []Entity {
	var entities []Entity

	// Extract regular functions and methods
	entities = append(entities, e.extractFuncsByPattern(filePath, source, lines, classRanges, e.functionPattern, false)...)

	// Extract async functions and methods
	entities = append(entities, e.extractFuncsByPattern(filePath, source, lines, classRanges, e.asyncFunctionPattern, true)...)

	return entities
}

// extractFuncsByPattern extracts functions matching the given pattern.
func (e *PythonExtractor) extractFuncsByPattern(filePath, source string, lines []string, classRanges map[string]struct {
	id        string
	startLine int
	endLine   int
	indent    int
}, pattern *regexp.Regexp, isAsync bool) []Entity {
	var entities []Entity
	matches := pattern.FindAllStringSubmatchIndex(source, -1)

	for _, match := range matches {
		if len(match) >= 6 {
			startPos := match[0]
			startLine := e.posToLine(source, startPos)

			// Get indentation
			indent := ""
			if match[2] != -1 && match[3] != -1 {
				indent = source[match[2]:match[3]]
			}
			indentLevel := len(indent)

			// Extract function name (group 2)
			name := source[match[4]:match[5]]

			// Find the end of the function (based on indentation)
			endLine := e.findPythonBlockEnd(lines, startLine, indentLevel)

			// Build signature
			var sig strings.Builder
			if isAsync {
				sig.WriteString("async ")
			}
			sig.WriteString("def ")
			sig.WriteString(name)
			sig.WriteString("(")
			if match[6] != -1 && match[7] != -1 {
				params := strings.TrimSpace(source[match[6]:match[7]])
				sig.WriteString(params)
			}
			sig.WriteString(")")
			if match[8] != -1 && match[9] != -1 {
				sig.WriteString(source[match[8]:match[9]])
			}

			// Determine if this is a method (inside a class) or a top-level function
			var parentID string
			var kind knowledge.EntityKind
			var entityPath string

			// Check if this function is inside any class
			parentClass := ""
			for className, classInfo := range classRanges {
				// Method must be:
				// 1. Within the class line range
				// 2. Have greater indentation than the class definition
				if startLine > classInfo.startLine && startLine <= classInfo.endLine && indentLevel > classInfo.indent {
					parentClass = className
					parentID = classInfo.id
					break
				}
			}

			if parentClass != "" {
				kind = knowledge.EntityKindMethod
				entityPath = parentClass + "." + name
			} else {
				kind = knowledge.EntityKindFunction
				entityPath = "func:" + name
			}

			entity := Entity{
				ID:        generateEntityID(filePath, entityPath),
				Name:      name,
				Kind:      kind,
				FilePath:  filePath,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig.String(),
				ParentID:  parentID,
			}
			entities = append(entities, entity)
		}
	}

	return entities
}

// posToLine converts a byte position to a line number (1-indexed).
func (e *PythonExtractor) posToLine(source string, pos int) int {
	return strings.Count(source[:pos], "\n") + 1
}

// getIndentLevel returns the number of leading whitespace characters.
func (e *PythonExtractor) getIndentLevel(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 4 // Treat tab as 4 spaces
		} else {
			break
		}
	}
	return count
}

// findPythonBlockEnd finds where a Python block ends based on indentation.
// A block ends when we find a line with equal or less indentation that is not empty/comment.
func (e *PythonExtractor) findPythonBlockEnd(lines []string, startLine int, baseIndent int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}

	lastNonEmptyLine := startLine
	inBlock := false

	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		currentIndent := e.getIndentLevel(line)

		// First line with content after the definition starts the block
		if !inBlock {
			if currentIndent > baseIndent {
				inBlock = true
				lastNonEmptyLine = i + 1
			}
			continue
		}

		// We're inside the block
		if currentIndent > baseIndent {
			lastNonEmptyLine = i + 1
		} else {
			// Found a line at the same or lower indentation level - block ends
			break
		}
	}

	// If we never entered a block (e.g., single-line function with pass),
	// just return the start line
	if !inBlock {
		// Check if the function body is on the same line (e.g., "def f(): pass")
		if startLine <= len(lines) {
			defLine := lines[startLine-1]
			if strings.Contains(defLine, ":") {
				afterColon := strings.SplitN(defLine, ":", 2)
				if len(afterColon) > 1 && strings.TrimSpace(afterColon[1]) != "" {
					return startLine
				}
			}
		}
		return startLine
	}

	return lastNonEmptyLine
}
