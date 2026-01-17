package treesitter

import (
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

type Parser struct {
	inner *sitter.Parser
	lang  *sitter.Language
	mu    sync.Mutex
}

func NewParser() *Parser {
	return &Parser{
		inner: sitter.NewParser(),
	}
}

func (p *Parser) SetLanguage(lang *sitter.Language) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.inner.SetLanguage(lang); err != nil {
		return err
	}
	p.lang = lang
	return nil
}

func (p *Parser) SetLanguageByName(name string) error {
	lang, err := LoadGrammar(name)
	if err != nil {
		return err
	}
	return p.SetLanguage(lang)
}

func (p *Parser) Parse(content []byte, oldTree *Tree) (*Tree, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var old *sitter.Tree
	if oldTree != nil {
		old = oldTree.inner
	}

	tree := p.inner.Parse(content, old)
	if tree == nil {
		return nil, ErrParseFailed
	}

	return &Tree{
		inner:  tree,
		source: content,
	}, nil
}

func (p *Parser) ParseString(content string) (*Tree, error) {
	return p.Parse([]byte(content), nil)
}

func (p *Parser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inner.Reset()
}

func (p *Parser) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inner.Close()
}

func (p *Parser) Language() *sitter.Language {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lang
}
