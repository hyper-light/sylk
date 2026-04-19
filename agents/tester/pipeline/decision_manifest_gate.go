// Cross-pipeline decision-coherence gate enforced just-in-time at
// test-authoring time. Without this gate, the prompt teaches the
// query-first / declare-second contract but nothing forces the LLM to
// actually follow it — which is exactly how the parallel-pipeline
// "pytest vs unittest" bug arises.
package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/manifest"
)

// requireTestFrameworkDecision blocks write_test until the manifest holds
// a winning test_framework decision for the relevant scope. This is the
// just-in-time enforcement of the cross-pipeline coherence contract: the
// prompt teaches the LLM to query/declare; this gate guarantees it.
//
// The error message is precise enough to drive the next LLM iteration
// without ambiguity: it names the missing scope dimensions and the
// adopt/declare guidance, so the model's next turn fixes the omission
// rather than thrashing.
//
// Returns nil when the gate is satisfied. Returns a non-nil error that
// the write_test handler propagates to the LLM as a structured tool
// failure when the gate refuses.
func (pt *PipelineTester) requireTestFrameworkDecision(ctx context.Context, outputPath string) error {
	client := pt.decisionManifestClient()
	if !client.IsConfigured() {
		// Test/dev environments without a live orchestrator manifest:
		// degrade gracefully. Production wiring always configures the
		// client; this only fires under fixtures that don't spin up the
		// orchestrator bus and would otherwise be unable to exercise
		// the deterministic write path.
		return nil
	}

	scope := manifest.Scope{}
	if lang := inferTesterLanguageFromPath(outputPath); lang != "" {
		scope[manifest.DimensionLanguage] = lang
	}
	if dir := writeTestScopeDir(outputPath); dir != "" {
		scope[manifest.DimensionPath] = dir
	}

	result, err := client.Query(ctx, manifest.QueryDecisionsInput{
		Domain: "test_framework",
		Scope:  scope,
	})
	if err != nil {
		// Surface the manifest infrastructure error as a tool failure so
		// the LLM does not silently proceed to author tests with no
		// decision. The orchestrator-side store should not normally fail
		// (it's a single indexed read on the BunSQLite handle), so this
		// path indicates something is genuinely wrong.
		return fmt.Errorf("decision manifest unavailable: %w. Cannot proceed with write_test until cross-pipeline coherence can be checked", err)
	}
	if result == nil || result.Winner == nil {
		return fmt.Errorf(
			"no test_framework decision exists in the cross-pipeline manifest for scope %s. "+
				"Before authoring tests, call query_decisions(domain=\"test_framework\") to see if a peer pipeline already declared a framework. "+
				"If a winner exists, ADOPT it (write your tests using that framework, do not declare again). "+
				"If no winner exists, call declare_decision(domain=\"test_framework\", value=\"<your-choice>\", confidence=\"tentative\") to record your intent so parallel pipelines see it. "+
				"Then retry write_test.",
			describeScope(scope),
		)
	}
	return nil
}

// decisionManifestClient returns a fresh manifest client for the pipeline
// tester. Mirrors the coordinationClient() helper: stateless construction,
// reuses the agent's bus + pending-wait infrastructure.
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
