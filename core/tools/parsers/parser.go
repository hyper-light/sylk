package parsers

import (
	"path/filepath"
	"sync"
)

type OutputParser interface {
	Parse(stdout, stderr []byte) (any, error)
}

type ParserRegistry struct {
	parsers map[string]OutputParser
	mu      sync.RWMutex
}

func NewParserRegistry() *ParserRegistry {
	r := &ParserRegistry{
		parsers: make(map[string]OutputParser),
	}
	r.registerDefaults()
	return r
}

func (r *ParserRegistry) registerDefaults() {
	r.Register("go build", NewGoParser())
	r.Register("go test", NewGoParser())
	r.Register("go vet", NewGoParser())
	r.Register("eslint", NewESLintParser())
	r.Register("git status", NewGitParser())
	r.Register("git diff", NewGitParser())
	r.Register("git log", NewGitParser())
}

func (r *ParserRegistry) Register(pattern string, parser OutputParser) {
	r.mu.Lock()
	r.parsers[pattern] = parser
	r.mu.Unlock()
}

func (r *ParserRegistry) Get(tool string) OutputParser {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if parser, ok := r.parsers[tool]; ok {
		return parser
	}

	for pattern, parser := range r.parsers {
		if matched, _ := filepath.Match(pattern, tool); matched {
			return parser
		}
	}
	return nil
}

func (r *ParserRegistry) Has(tool string) bool {
	return r.Get(tool) != nil
}

type ParseError struct {
	File    string
	Line    int
	Column  int
	Message string
	Code    string
}

type ParseWarning struct {
	File    string
	Line    int
	Column  int
	Message string
	Code    string
}

type TestResult struct {
	Name     string
	Package  string
	Status   TestStatus
	Duration float64
	Output   string
}

type TestStatus string

const (
	TestStatusPass TestStatus = "pass"
	TestStatusFail TestStatus = "fail"
	TestStatusSkip TestStatus = "skip"
)
