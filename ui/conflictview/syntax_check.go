package conflictview

import (
	"github.com/adalundhe/sylk/core/treesitter"
)

// SyntaxWarning describes parse errors found in a resolved file.
type SyntaxWarning struct {
	Path   string
	Errors []treesitter.ParseError
}

// ValidateResolvedFiles parses each resolved file through tree-sitter and
// returns warnings for files with parse errors. Returns nil if all files
// parse cleanly or if no grammars are available.
func ValidateResolvedFiles(entries []ConflictFileEntry) []SyntaxWarning {
	var warnings []SyntaxWarning

	for i := range entries {
		e := &entries[i]
		if e.Resolution == ResUnresolved || e.MergedContent == "" {
			continue
		}

		lang := treesitter.DetectLanguage(e.Path)
		if lang == "" || !treesitter.IsGrammarAvailable(lang) {
			continue
		}

		parser := treesitter.NewParser()
		if err := parser.SetLanguageByName(lang); err != nil {
			parser.Close()
			continue
		}

		tree, err := parser.ParseString(e.MergedContent)
		if err != nil || tree == nil {
			parser.Close()
			continue
		}

		root := tree.RootNode()
		if root != nil && root.HasError() {
			errs := treesitter.CollectParseErrors(root)
			if len(errs) > 0 {
				warnings = append(warnings, SyntaxWarning{
					Path:   e.Path,
					Errors: errs,
				})
			}
		}

		tree.Close()
		parser.Close()
	}

	return warnings
}
