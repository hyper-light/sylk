package treesitter

import "errors"

var (
	ErrParseFailed     = errors.New("parse failed")
	ErrGrammarNotFound = errors.New("grammar not found")
	ErrNoLanguageSet   = errors.New("no language set on parser")
)
