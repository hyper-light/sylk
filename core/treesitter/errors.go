package treesitter

import "errors"

var (
	ErrParseFailed         = errors.New("parse failed")
	ErrLibraryNotLoaded    = errors.New("tree-sitter library not loaded")
	ErrLanguageNotLoaded   = errors.New("language grammar not loaded")
	ErrGrammarNotFound     = errors.New("grammar not found")
	ErrGrammarNotInstalled = errors.New("grammar not installed")
	ErrInvalidQuery        = errors.New("invalid query pattern")
	ErrDownloadFailed      = errors.New("grammar download failed")
	ErrCompileFailed       = errors.New("grammar compilation failed")
	ErrIncompatibleABI     = errors.New("incompatible grammar ABI version")
)
