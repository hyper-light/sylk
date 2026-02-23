package chat

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// mdParser is a package-level goldmark parser singleton configured with
// GFM extensions (tables, strikethrough, task lists, autolinks).
// The parser is stateless and goroutine-safe — one instance shared
// across all renders.
var mdParser parser.Parser

func init() {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
	)
	mdParser = md.Parser()
}

// parseMarkdown parses source bytes into a goldmark AST.
func parseMarkdown(source []byte) ast.Node {
	return mdParser.Parse(text.NewReader(source))
}
