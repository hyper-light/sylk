package embedder

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

const maxDocstringLen = 2000

type Serializer struct {
	files map[uint32]*ingestion.FileNode
}

func NewSerializer() *Serializer {
	return &Serializer{
		files: make(map[uint32]*ingestion.FileNode),
	}
}

func (s *Serializer) RegisterFile(f *ingestion.FileNode) {
	s.files[f.ID] = f
}

func (s *Serializer) SerializeSymbol(sym *ingestion.SymbolNode) string {
	var b strings.Builder

	file := s.files[sym.FileID]
	if file != nil {
		b.WriteString(file.Path)
		b.WriteString(" ")
	}

	b.WriteString(sym.Kind.String())
	b.WriteString(" ")
	b.WriteString(sym.Name)

	if sym.Signature != "" {
		b.WriteString(" ")
		b.WriteString(truncate(sym.Signature, maxDocstringLen))
	}

	return b.String()
}

func (s *Serializer) SerializeFile(f *ingestion.FileNode) string {
	return fmt.Sprintf("file %s %s %d lines", f.Path, f.Lang, f.LineCount)
}

func SerializeSymbolStandalone(name, kind, signature, filePath string) string {
	var b strings.Builder

	if filePath != "" {
		b.WriteString(filePath)
		b.WriteString(" ")
	}

	b.WriteString(kind)
	b.WriteString(" ")
	b.WriteString(name)

	if signature != "" {
		b.WriteString(" ")
		b.WriteString(truncate(signature, maxDocstringLen))
	}

	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
