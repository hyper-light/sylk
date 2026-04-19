package pipeline

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/manifest"
)

// TestRequireTestFrameworkDecision_NoOpWhenClientNotConfigured guards the
// degrade-gracefully path: a tester instance with no manifest wiring
// (test fixture without the orchestrator bus) must NOT block write_test
// on the gate. Production wiring always configures the client; this is
// the fixture-friendliness escape hatch that keeps deterministic-path
// tests runnable without spinning up the full orchestrator.
func TestRequireTestFrameworkDecision_NoOpWhenClientNotConfigured(t *testing.T) {
	pt := &PipelineTester{} // no bus, no pending wait → IsConfigured() == false
	if err := pt.requireTestFrameworkDecision(context.Background(), "tests/test_init.py"); err != nil {
		t.Fatalf("gate must be a no-op without manifest wiring, got %v", err)
	}
}

// TestInferTesterLanguageFromPath covers the path-extension → language
// mapping the gate uses to populate the manifest scope. Recognized
// extensions enrich the scope; unrecognized extensions return empty so
// the gate runs with a less-specific scope rather than refusing the write.
func TestInferTesterLanguageFromPath(t *testing.T) {
	cases := map[string]string{
		"tests/test_init.py":            "python",
		"tests/integration/test_api.py": "python",
		"internal/auth/auth_test.go":    "go",
		"src/lib.rs":                    "rust",
		"src/components/Button.test.ts": "typescript",
		"src/utils.test.tsx":            "typescript",
		"spec/billing.test.js":          "javascript",
		"scripts/enemies/test_boss.lua": "lua",
		"src/network/socket_test.cpp":   "cpp",
		"src/parser_test.c":             "c",
		"unknown.xyz":                   "",
		"":                              "",
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := inferTesterLanguageFromPath(path); got != want {
				t.Errorf("inferTesterLanguageFromPath(%q) = %q, want %q", path, got, want)
			}
		})
	}
}

// TestWriteTestScopeDir verifies the directory extraction used for the
// scope's path dimension. The trailing slash matters — without it, prefix
// matching could over-match (e.g. "tests" matching "tests_legacy/foo.py").
func TestWriteTestScopeDir(t *testing.T) {
	cases := map[string]string{
		"tests/test_init.py":                  "tests/",
		"services/api/tests/test_login.py":    "services/api/tests/",
		"single_file.py":                      "",
		"":                                    "",
		"./local.py":                          "",
		"deeply/nested/path/to/file_test.go":  "deeply/nested/path/to/",
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := writeTestScopeDir(path); got != want {
				t.Errorf("writeTestScopeDir(%q) = %q, want %q", path, got, want)
			}
		})
	}
}

// TestDescribeScope keeps the human-readable scope rendering stable
// across the gate's error messages. Order should be deterministic for
// the well-known dimensions so error logs / re-prompts compare cleanly.
func TestDescribeScope(t *testing.T) {
	cases := []struct {
		name string
		s    manifest.Scope
		want string
	}{
		{
			name: "empty",
			s:    manifest.Scope{},
			want: "{}",
		},
		{
			name: "language only",
			s:    manifest.Scope{manifest.DimensionLanguage: "python"},
			want: "{language: python}",
		},
		{
			name: "language + path",
			s: manifest.Scope{
				manifest.DimensionLanguage: "python",
				manifest.DimensionPath:     "services/api/tests/",
			},
			want: "{language: python, path: services/api/tests/}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeScope(tc.s); got != tc.want {
				t.Errorf("describeScope = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDecisionManifestClient_IsConfigured covers the wire-or-not contract
// that the gate uses to decide whether to enforce. Helper-side test for
// the client method since the gate's enforcement decision hinges on it.
func TestDecisionManifestClient_IsConfigured(t *testing.T) {
	zero := shared.DecisionManifestClient{}
	if zero.IsConfigured() {
		t.Fatal("zero-value client must report unconfigured")
	}
}
