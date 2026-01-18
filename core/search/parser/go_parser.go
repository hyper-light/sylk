package parser

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// GoParser extracts symbols, comments, and imports from Go source files.
type GoParser struct{}

// NewGoParser creates a new Go parser instance.
func NewGoParser() *GoParser {
	return &GoParser{}
}

// Extensions returns the file extensions handled by this parser.
func (p *GoParser) Extensions() []string {
	return []string{".go"}
}

// Parse extracts symbols, comments, imports, and metadata from Go source code.
func (p *GoParser) Parse(content []byte, path string) (*ParseResult, error) {
	if len(content) == 0 {
		return NewParseResult(), nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		// Return partial result for malformed files
		return p.parsePartial(content)
	}

	result := NewParseResult()
	p.extractImports(file, result)
	p.extractComments(file, result)
	p.extractDeclarations(file, fset, result)
	p.extractMetadata(file, result)

	return result, nil
}

// parsePartial attempts to extract what we can from malformed content.
func (p *GoParser) parsePartial(content []byte) (*ParseResult, error) {
	result := NewParseResult()
	lines := strings.Split(string(content), "\n")

	for i, line := range lines {
		p.parsePartialLine(line, i+1, result)
	}

	return result, nil
}

// parsePartialLine extracts symbols from a single line of malformed content.
func (p *GoParser) parsePartialLine(line string, lineNum int, result *ParseResult) {
	trimmed := strings.TrimSpace(line)

	if strings.HasPrefix(trimmed, "func ") {
		p.extractPartialFunc(trimmed, lineNum, result)
	} else if strings.HasPrefix(trimmed, "type ") {
		p.extractPartialType(trimmed, lineNum, result)
	}
}

// extractPartialFunc extracts function info from a line prefix.
func (p *GoParser) extractPartialFunc(line string, lineNum int, result *ParseResult) {
	rest := strings.TrimPrefix(line, "func ")
	parts := strings.SplitN(rest, "(", 2)
	if len(parts) < 1 {
		return
	}

	name := strings.TrimSpace(parts[0])
	if name == "" {
		return
	}

	result.Symbols = append(result.Symbols, Symbol{
		Name:     name,
		Kind:     KindFunction,
		Line:     lineNum,
		Exported: isExported(name),
	})
}

// extractPartialType extracts type info from a line prefix.
func (p *GoParser) extractPartialType(line string, lineNum int, result *ParseResult) {
	rest := strings.TrimPrefix(line, "type ")
	parts := strings.Fields(rest)
	if len(parts) < 1 {
		return
	}

	name := parts[0]
	result.Symbols = append(result.Symbols, Symbol{
		Name:     name,
		Kind:     KindType,
		Line:     lineNum,
		Exported: isExported(name),
	})
}

// extractImports extracts import paths from the AST.
func (p *GoParser) extractImports(file *ast.File, result *ParseResult) {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		result.Imports = append(result.Imports, path)
	}
}

// extractComments concatenates all comments from the file.
func (p *GoParser) extractComments(file *ast.File, result *ParseResult) {
	var buf bytes.Buffer

	for _, cg := range file.Comments {
		for _, c := range cg.List {
			text := cleanComment(c.Text)
			if text != "" {
				buf.WriteString(text)
				buf.WriteString("\n")
			}
		}
	}

	result.Comments = strings.TrimSpace(buf.String())
}

// extractDeclarations extracts functions, types, interfaces, vars, and consts.
func (p *GoParser) extractDeclarations(file *ast.File, fset *token.FileSet, result *ParseResult) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			p.extractFunc(d, fset, result)
		case *ast.GenDecl:
			p.extractGenDecl(d, fset, result)
		}
	}
}

// extractFunc extracts a function or method declaration.
func (p *GoParser) extractFunc(fn *ast.FuncDecl, fset *token.FileSet, result *ParseResult) {
	name := fn.Name.Name
	sig := buildFuncSignature(fn)
	line := fset.Position(fn.Pos()).Line

	result.Symbols = append(result.Symbols, Symbol{
		Name:      name,
		Kind:      KindFunction,
		Signature: sig,
		Line:      line,
		Exported:  isExported(name),
	})
}

// extractGenDecl extracts type, var, and const declarations.
func (p *GoParser) extractGenDecl(decl *ast.GenDecl, fset *token.FileSet, result *ParseResult) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			p.extractTypeSpec(s, decl.Tok, fset, result)
		case *ast.ValueSpec:
			p.extractValueSpec(s, decl.Tok, fset, result)
		}
	}
}

// extractTypeSpec extracts a type declaration (struct, interface, alias).
func (p *GoParser) extractTypeSpec(spec *ast.TypeSpec, tok token.Token, fset *token.FileSet, result *ParseResult) {
	name := spec.Name.Name
	line := fset.Position(spec.Pos()).Line
	kind := determineTypeKind(spec)

	result.Symbols = append(result.Symbols, Symbol{
		Name:     name,
		Kind:     kind,
		Line:     line,
		Exported: isExported(name),
	})
}

// extractValueSpec extracts variable or constant declarations.
func (p *GoParser) extractValueSpec(spec *ast.ValueSpec, tok token.Token, fset *token.FileSet, result *ParseResult) {
	kind := KindVariable
	if tok == token.CONST {
		kind = KindConst
	}

	for _, ident := range spec.Names {
		name := ident.Name
		if name == "_" {
			continue
		}

		line := fset.Position(ident.Pos()).Line
		result.Symbols = append(result.Symbols, Symbol{
			Name:     name,
			Kind:     kind,
			Line:     line,
			Exported: isExported(name),
		})
	}
}

// extractMetadata extracts package name and other metadata.
func (p *GoParser) extractMetadata(file *ast.File, result *ParseResult) {
	result.Metadata["package"] = file.Name.Name
}

// =============================================================================
// Helper Functions
// =============================================================================

// isExported returns true if the identifier starts with an uppercase letter.
func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// cleanComment removes comment markers and trims whitespace.
func cleanComment(text string) string {
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	return strings.TrimSpace(text)
}

// determineTypeKind determines if a type spec is an interface or regular type.
func determineTypeKind(spec *ast.TypeSpec) string {
	if _, ok := spec.Type.(*ast.InterfaceType); ok {
		return KindInterface
	}
	return KindType
}

// buildFuncSignature builds a function signature string.
func buildFuncSignature(fn *ast.FuncDecl) string {
	var buf bytes.Buffer
	buf.WriteString("func ")

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		buf.WriteString("(")
		writeFieldList(&buf, fn.Recv)
		buf.WriteString(") ")
	}

	buf.WriteString(fn.Name.Name)
	buf.WriteString("(")
	writeFieldList(&buf, fn.Type.Params)
	buf.WriteString(")")

	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		buf.WriteString(" ")
		writeResults(&buf, fn.Type.Results)
	}

	return buf.String()
}

// writeFieldList writes a field list to the buffer.
func writeFieldList(buf *bytes.Buffer, fields *ast.FieldList) {
	if fields == nil {
		return
	}

	for i, field := range fields.List {
		if i > 0 {
			buf.WriteString(", ")
		}
		writeField(buf, field)
	}
}

// writeField writes a single field to the buffer.
func writeField(buf *bytes.Buffer, field *ast.Field) {
	for i, name := range field.Names {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(name.Name)
	}

	if len(field.Names) > 0 {
		buf.WriteString(" ")
	}

	writeTypeExpr(buf, field.Type)
}

// writeResults writes function results to the buffer.
func writeResults(buf *bytes.Buffer, results *ast.FieldList) {
	if len(results.List) == 1 && len(results.List[0].Names) == 0 {
		writeTypeExpr(buf, results.List[0].Type)
		return
	}

	buf.WriteString("(")
	writeFieldList(buf, results)
	buf.WriteString(")")
}

// writeTypeExpr writes a type expression to the buffer.
func writeTypeExpr(buf *bytes.Buffer, expr ast.Expr) {
	switch t := expr.(type) {
	case *ast.Ident:
		buf.WriteString(t.Name)
	case *ast.StarExpr:
		buf.WriteString("*")
		writeTypeExpr(buf, t.X)
	case *ast.ArrayType:
		buf.WriteString("[]")
		writeTypeExpr(buf, t.Elt)
	case *ast.MapType:
		buf.WriteString("map[")
		writeTypeExpr(buf, t.Key)
		buf.WriteString("]")
		writeTypeExpr(buf, t.Value)
	case *ast.SelectorExpr:
		writeTypeExpr(buf, t.X)
		buf.WriteString(".")
		buf.WriteString(t.Sel.Name)
	case *ast.InterfaceType:
		buf.WriteString("interface{}")
	case *ast.FuncType:
		buf.WriteString("func(...)")
	case *ast.ChanType:
		buf.WriteString("chan ")
		writeTypeExpr(buf, t.Value)
	case *ast.Ellipsis:
		buf.WriteString("...")
		writeTypeExpr(buf, t.Elt)
	default:
		buf.WriteString("...")
	}
}

// init registers the Go parser in the default registry.
func init() {
	RegisterParser(NewGoParser())
}
