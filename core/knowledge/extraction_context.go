package knowledge

// =============================================================================
// Extraction Context
// =============================================================================

// ExtractionContext carries state during code extraction, including file
// metadata, symbol tables, and scope tracking for nested constructs.
type ExtractionContext struct {
	FilePath    string
	Language    string
	SymbolTable map[string]*ExtractedEntity
	ParentScope string
	ImportMap   map[string]string
}

// NewExtractionContext creates a new extraction context for a file.
func NewExtractionContext(filePath, language string) *ExtractionContext {
	return &ExtractionContext{
		FilePath:    filePath,
		Language:    language,
		SymbolTable: make(map[string]*ExtractedEntity),
		ParentScope: "",
		ImportMap:   make(map[string]string),
	}
}

// WithScope creates a new context with an updated parent scope.
// This allows for chainable scope nesting during extraction.
func (ec *ExtractionContext) WithScope(name string) *ExtractionContext {
	return &ExtractionContext{
		FilePath:    ec.FilePath,
		Language:    ec.Language,
		SymbolTable: ec.SymbolTable,
		ParentScope: name,
		ImportMap:   ec.ImportMap,
	}
}
