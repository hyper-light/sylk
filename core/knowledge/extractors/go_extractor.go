// Package extractors provides language-specific entity extraction from source code.
package extractors

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/adalundhe/sylk/core/knowledge"
)

// Entity represents a code entity extracted from source files.
type Entity struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Kind      knowledge.EntityKind `json:"kind"`
	FilePath  string               `json:"file_path"`
	StartLine int                  `json:"start_line"`
	EndLine   int                  `json:"end_line"`
	Signature string               `json:"signature,omitempty"`
	ParentID  string               `json:"parent_id,omitempty"`
}

// EntityExtractor is the interface for language-specific entity extractors.
type EntityExtractor interface {
	Extract(filePath string, content []byte) ([]Entity, error)
}

// generateEntityID creates a stable ID based on file path and entity path.
func generateEntityID(filePath, entityPath string) string {
	hash := sha256.Sum256([]byte(filePath + ":" + entityPath))
	return hex.EncodeToString(hash[:16])
}

// GoExtractor extracts entities from Go source files using go/ast and go/parser.
type GoExtractor struct{}

// NewGoExtractor creates a new Go entity extractor.
func NewGoExtractor() *GoExtractor {
	return &GoExtractor{}
}

// Extract parses Go source code and extracts functions, types, methods, and interfaces.
func (e *GoExtractor) Extract(filePath string, content []byte) ([]Entity, error) {
	if len(content) == 0 {
		return []Entity{}, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		// Return empty slice on parse errors (syntax errors)
		return []Entity{}, nil
	}

	var entities []Entity

	// Extract package name as a package entity
	if file.Name != nil {
		pkgEntity := Entity{
			ID:        generateEntityID(filePath, "package:"+file.Name.Name),
			Name:      file.Name.Name,
			Kind:      knowledge.EntityKindPackage,
			FilePath:  filePath,
			StartLine: fset.Position(file.Package).Line,
			EndLine:   fset.Position(file.Package).Line,
			Signature: "package " + file.Name.Name,
		}
		entities = append(entities, pkgEntity)
	}

	// Walk through all declarations
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			entities = append(entities, e.extractFunc(fset, filePath, d)...)
		case *ast.GenDecl:
			entities = append(entities, e.extractGenDecl(fset, filePath, d)...)
		}
	}

	return entities, nil
}

// extractFunc extracts function and method declarations.
func (e *GoExtractor) extractFunc(fset *token.FileSet, filePath string, fn *ast.FuncDecl) []Entity {
	var entities []Entity

	startLine := fset.Position(fn.Pos()).Line
	endLine := fset.Position(fn.End()).Line

	// Build the signature
	var sig strings.Builder
	sig.WriteString("func ")

	// Check if it's a method (has receiver)
	var entityPath string
	var kind knowledge.EntityKind
	var parentID string

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		// It's a method
		kind = knowledge.EntityKindMethod
		recvType := e.getReceiverType(fn.Recv.List[0].Type)
		sig.WriteString("(")
		if len(fn.Recv.List[0].Names) > 0 {
			sig.WriteString(fn.Recv.List[0].Names[0].Name)
			sig.WriteString(" ")
		}
		sig.WriteString(recvType)
		sig.WriteString(") ")
		entityPath = fmt.Sprintf("%s.%s", recvType, fn.Name.Name)
		parentID = generateEntityID(filePath, "type:"+recvType)
	} else {
		// It's a function
		kind = knowledge.EntityKindFunction
		entityPath = "func:" + fn.Name.Name
	}

	sig.WriteString(fn.Name.Name)
	sig.WriteString(e.formatFuncParams(fn.Type))

	entity := Entity{
		ID:        generateEntityID(filePath, entityPath),
		Name:      fn.Name.Name,
		Kind:      kind,
		FilePath:  filePath,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sig.String(),
		ParentID:  parentID,
	}

	entities = append(entities, entity)
	return entities
}

// extractGenDecl extracts type, const, and var declarations.
func (e *GoExtractor) extractGenDecl(fset *token.FileSet, filePath string, decl *ast.GenDecl) []Entity {
	var entities []Entity

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			entities = append(entities, e.extractTypeSpec(fset, filePath, s)...)
		}
	}

	return entities
}

// extractTypeSpec extracts type declarations (structs, interfaces, type aliases).
func (e *GoExtractor) extractTypeSpec(fset *token.FileSet, filePath string, spec *ast.TypeSpec) []Entity {
	var entities []Entity

	startLine := fset.Position(spec.Pos()).Line
	endLine := fset.Position(spec.End()).Line

	var kind knowledge.EntityKind
	var sig strings.Builder

	switch t := spec.Type.(type) {
	case *ast.StructType:
		kind = knowledge.EntityKindStruct
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
		sig.WriteString(" struct")

		entity := Entity{
			ID:        generateEntityID(filePath, "type:"+spec.Name.Name),
			Name:      spec.Name.Name,
			Kind:      kind,
			FilePath:  filePath,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: sig.String(),
		}
		entities = append(entities, entity)

		// Extract struct methods will be captured as FuncDecl with receivers

	case *ast.InterfaceType:
		kind = knowledge.EntityKindInterface
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
		sig.WriteString(" interface")

		entity := Entity{
			ID:        generateEntityID(filePath, "type:"+spec.Name.Name),
			Name:      spec.Name.Name,
			Kind:      kind,
			FilePath:  filePath,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: sig.String(),
		}
		entities = append(entities, entity)

		// Extract interface methods
		if t.Methods != nil {
			parentID := entity.ID
			for _, method := range t.Methods.List {
				if len(method.Names) > 0 {
					methodName := method.Names[0].Name
					methodStart := fset.Position(method.Pos()).Line
					methodEnd := fset.Position(method.End()).Line

					var methodSig strings.Builder
					methodSig.WriteString(methodName)
					if funcType, ok := method.Type.(*ast.FuncType); ok {
						methodSig.WriteString(e.formatFuncParams(funcType))
					}

					methodEntity := Entity{
						ID:        generateEntityID(filePath, fmt.Sprintf("%s.%s", spec.Name.Name, methodName)),
						Name:      methodName,
						Kind:      knowledge.EntityKindMethod,
						FilePath:  filePath,
						StartLine: methodStart,
						EndLine:   methodEnd,
						Signature: methodSig.String(),
						ParentID:  parentID,
					}
					entities = append(entities, methodEntity)
				}
			}
		}

	default:
		// Type alias or other type definition
		kind = knowledge.EntityKindType
		sig.WriteString("type ")
		sig.WriteString(spec.Name.Name)
		sig.WriteString(" ")
		sig.WriteString(e.formatType(spec.Type))

		entity := Entity{
			ID:        generateEntityID(filePath, "type:"+spec.Name.Name),
			Name:      spec.Name.Name,
			Kind:      kind,
			FilePath:  filePath,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: sig.String(),
		}
		entities = append(entities, entity)
	}

	return entities
}

// getReceiverType extracts the type name from a receiver expression.
func (e *GoExtractor) getReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return "*" + ident.Name
		}
		return "*unknown"
	default:
		return "unknown"
	}
}

// formatFuncParams formats function parameters and return types.
func (e *GoExtractor) formatFuncParams(ft *ast.FuncType) string {
	var sb strings.Builder
	sb.WriteString("(")

	if ft.Params != nil {
		var params []string
		for _, param := range ft.Params.List {
			paramType := e.formatType(param.Type)
			if len(param.Names) > 0 {
				for _, name := range param.Names {
					params = append(params, name.Name+" "+paramType)
				}
			} else {
				params = append(params, paramType)
			}
		}
		sb.WriteString(strings.Join(params, ", "))
	}
	sb.WriteString(")")

	if ft.Results != nil && len(ft.Results.List) > 0 {
		sb.WriteString(" ")
		if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
			sb.WriteString(e.formatType(ft.Results.List[0].Type))
		} else {
			sb.WriteString("(")
			var results []string
			for _, result := range ft.Results.List {
				resultType := e.formatType(result.Type)
				if len(result.Names) > 0 {
					for _, name := range result.Names {
						results = append(results, name.Name+" "+resultType)
					}
				} else {
					results = append(results, resultType)
				}
			}
			sb.WriteString(strings.Join(results, ", "))
			sb.WriteString(")")
		}
	}

	return sb.String()
}

// formatType formats a type expression as a string.
func (e *GoExtractor) formatType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + e.formatType(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + e.formatType(t.Elt)
		}
		return "[...]" + e.formatType(t.Elt)
	case *ast.MapType:
		return "map[" + e.formatType(t.Key) + "]" + e.formatType(t.Value)
	case *ast.SelectorExpr:
		return e.formatType(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func" + e.formatFuncParams(t)
	case *ast.ChanType:
		switch t.Dir {
		case ast.SEND:
			return "chan<- " + e.formatType(t.Value)
		case ast.RECV:
			return "<-chan " + e.formatType(t.Value)
		default:
			return "chan " + e.formatType(t.Value)
		}
	case *ast.Ellipsis:
		return "..." + e.formatType(t.Elt)
	default:
		return "unknown"
	}
}
