// Package nodirectprovidercall enforces that LLM provider calls flow
// through providers.ProviderAdapter (the instrumented wrapper) rather
// than directly through provider-specific SDKs.
//
// The lint flags imports of provider SDK packages (anthropic, google
// genai, openai) from outside the core/providers package itself —
// agents must use the abstract ProviderAdapter interface so the
// fabric chokepoint captures every LLM round-trip with token usage,
// retries, and model swaps.
package nodirectprovidercall

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nodirectprovidercall",
	Doc: "enforces that LLM provider calls go through the instrumented providers.ProviderAdapter; " +
		"importing an LLM provider SDK directly bypasses the fabric chokepoint and the " +
		"retry/Retry-After/instrumentation policy.",
	Run: run,
}

// bannedImports are package paths whose imports are forbidden outside
// the allowlist. The provider SDKs each go here; the abstract
// providers.ProviderAdapter package and its tests are allowlisted.
var bannedImports = map[string]string{
	"github.com/anthropics/anthropic-sdk-go":              "anthropic-sdk-go",
	"github.com/anthropics/anthropic-sdk-go/option":       "anthropic-sdk-go/option",
	"google.golang.org/genai":                             "google.golang.org/genai",
	"github.com/openai/openai-go":                         "openai-go",
}

// allowedPathPrefixes lists import paths that legitimately depend on
// the provider SDKs.
var allowedPathPrefixes = []string{
	"github.com/adalundhe/sylk/core/providers",
	"github.com/adalundhe/sylk/core/oauth",
}

func run(pass *analysis.Pass) (interface{}, error) {
	if isAllowedPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			display, banned := bannedImports[path]
			if !banned {
				continue
			}
			pass.Reportf(imp.Pos(),
				"direct import of LLM provider SDK %q is forbidden outside core/providers; depend on providers.ProviderAdapter so the fabric chokepoint captures the round-trip",
				display,
			)
		}
		_ = ast.Inspect
	}
	return nil, nil
}

func isAllowedPackage(pkgPath string) bool {
	for _, prefix := range allowedPathPrefixes {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}
