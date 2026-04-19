// Helpers used by Tier 4 auto-publish. The original
// requireTestFrameworkDecision JIT gate is gone — see docs/FABRIC.md
// "no-gates, auto-publish, ambient awareness." Cross-pipeline coherence
// is now enforced by the manifest auto-publish hook on
// detect_test_harness + write_test plus the ambient_context envelope on
// every tool result; never by blocking the agent's primary work.
package pipeline

import (
	"path/filepath"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/manifest"
)

// decisionManifestClient returns a fresh manifest client for the pipeline
// tester. Mirrors the coordinationClient() helper: stateless construction,
// reuses the agent's bus + pending-wait infrastructure. Used by Tier 4
// auto-publish to declare typed framework decisions as a side-effect of
// detect_test_harness / plan_tests / write_test / finalize_pipeline.
func (pt *PipelineTester) decisionManifestClient() agentshared.DecisionManifestClient {
	return agentshared.DecisionManifestClient{
		BusProvider:     func() guide.EventBus { return pt.bus },
		SourceAgentID:   func() string { return pt.id },
		SourceAgentType: func() string { return "tester-pipeline" },
		SessionID:       func() string { return pt.config.SessionID },
		RegisterPending: pt.registerPendingWait,
		ClearPending:    pt.clearPendingWait,
		Timeout:         agentshared.DefaultConsultationTimeout,
	}
}

// inferTesterLanguageFromPath maps a file path's extension to the
// test_framework domain's language dimension. Empty when the extension
// is unrecognized — the gate then runs with a less-specific scope, which
// still produces a meaningful query (an ambient Python decision still
// matches a Python test even if the gate doesn't recognize the suffix).
func inferTesterLanguageFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".lua":
		return "lua"
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh":
		return "cpp"
	case ".c", ".h":
		return "c"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".cs":
		return "csharp"
	case ".scala":
		return "scala"
	case ".ex", ".exs":
		return "elixir"
	case ".erl":
		return "erlang"
	case ".php":
		return "php"
	}
	return ""
}

// writeTestScopeDir extracts the directory portion of the test path
// for the manifest scope's path dimension. Trailing slash is added so
// prefix-match in ScopeMatchesQuery aligns with the directory boundary.
func writeTestScopeDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "" || dir == "." {
		return ""
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}

// describeScope renders a scope coordinate as a short human-readable
// string for inclusion in the gate error message ({language: python,
// path: tests/}).
func describeScope(s manifest.Scope) string {
	if len(s) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(s))
	if v, ok := s[manifest.DimensionLanguage]; ok {
		parts = append(parts, "language: "+v)
	}
	if v, ok := s[manifest.DimensionPath]; ok {
		parts = append(parts, "path: "+v)
	}
	if v, ok := s[manifest.DimensionSurface]; ok {
		parts = append(parts, "surface: "+v)
	}
	for k, v := range s {
		switch k {
		case manifest.DimensionLanguage, manifest.DimensionPath, manifest.DimensionSurface:
			continue
		}
		parts = append(parts, string(k)+": "+v)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
