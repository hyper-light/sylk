package boot

import (
	"strings"

	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

const maxBodyLen = 4000

type RichSerializer struct {
	files    map[uint32]*ingestion.FileNode
	symbols  map[uint32]*ingestion.SymbolNode
	packages map[uint32]string
	calls    map[uint32][]string
	calledBy map[uint32][]string
}

func NewRichSerializer() *RichSerializer {
	return &RichSerializer{
		files:    make(map[uint32]*ingestion.FileNode),
		symbols:  make(map[uint32]*ingestion.SymbolNode),
		packages: make(map[uint32]string),
		calls:    make(map[uint32][]string),
		calledBy: make(map[uint32][]string),
	}
}

func (s *RichSerializer) RegisterFile(f *ingestion.FileNode) {
	s.files[f.ID] = f
}

func (s *RichSerializer) RegisterSymbol(sym *ingestion.SymbolNode) {
	s.symbols[sym.ID] = sym
}

func (s *RichSerializer) SetPackage(fileID uint32, pkg string) {
	s.packages[fileID] = pkg
}

func (s *RichSerializer) AddCall(callerID uint32, calleeName string) {
	s.calls[callerID] = append(s.calls[callerID], calleeName)
}

func (s *RichSerializer) AddCalledBy(calleeID uint32, callerName string) {
	s.calledBy[calleeID] = append(s.calledBy[calleeID], callerName)
}

type SymbolContext struct {
	FilePath  string
	Package   string
	Kind      string
	Name      string
	Signature string
	Calls     []string
	CalledBy  []string
	Body      string
}

func (s *RichSerializer) GetContext(sym *ingestion.SymbolNode, body string) *SymbolContext {
	ctx := &SymbolContext{
		Kind:      sym.Kind.String(),
		Name:      sym.Name,
		Signature: sym.Signature,
		Body:      truncateBody(body),
	}

	if f, ok := s.files[sym.FileID]; ok {
		ctx.FilePath = f.Path
	}

	if pkg, ok := s.packages[sym.FileID]; ok {
		ctx.Package = pkg
	}

	if calls, ok := s.calls[sym.ID]; ok {
		ctx.Calls = calls
	}

	if calledBy, ok := s.calledBy[sym.ID]; ok {
		ctx.CalledBy = calledBy
	}

	return ctx
}

func (s *RichSerializer) Serialize(ctx *SymbolContext) string {
	var b strings.Builder

	b.WriteString("# path: ")
	b.WriteString(ctx.FilePath)
	b.WriteByte('\n')

	if ctx.Package != "" {
		b.WriteString("# package: ")
		b.WriteString(ctx.Package)
		b.WriteByte('\n')
	}

	b.WriteString("# kind: ")
	b.WriteString(ctx.Kind)
	b.WriteByte('\n')

	b.WriteString("# name: ")
	b.WriteString(ctx.Name)
	b.WriteByte('\n')

	if ctx.Signature != "" {
		b.WriteString("# signature: ")
		b.WriteString(ctx.Signature)
		b.WriteByte('\n')
	}

	if len(ctx.Calls) > 0 {
		b.WriteString("# calls: ")
		b.WriteString(strings.Join(ctx.Calls, ", "))
		b.WriteByte('\n')
	}

	if len(ctx.CalledBy) > 0 {
		b.WriteString("# called_by: ")
		b.WriteString(strings.Join(ctx.CalledBy, ", "))
		b.WriteByte('\n')
	}

	if ctx.Body != "" {
		b.WriteString("# body:\n")
		b.WriteString(ctx.Body)
	}

	return b.String()
}

func (s *RichSerializer) SerializeSymbol(sym *ingestion.SymbolNode, body string) string {
	ctx := s.GetContext(sym, body)
	return s.Serialize(ctx)
}

func truncateBody(body string) string {
	if len(body) <= maxBodyLen {
		return body
	}
	return body[:maxBodyLen]
}

type SimpleSerializer struct{}

func NewSimpleSerializer() *SimpleSerializer {
	return &SimpleSerializer{}
}

func (s *SimpleSerializer) Serialize(filePath, kind, name, signature, body string) string {
	var b strings.Builder

	b.WriteString("# path: ")
	b.WriteString(filePath)
	b.WriteByte('\n')

	b.WriteString("# kind: ")
	b.WriteString(kind)
	b.WriteByte('\n')

	b.WriteString("# name: ")
	b.WriteString(name)
	b.WriteByte('\n')

	if signature != "" {
		b.WriteString("# signature: ")
		b.WriteString(signature)
		b.WriteByte('\n')
	}

	if body != "" {
		b.WriteString("# body:\n")
		b.WriteString(truncateBody(body))
	}

	return b.String()
}
