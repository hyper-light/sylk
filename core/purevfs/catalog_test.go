package purevfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInferPrimaryLanguage(t *testing.T) {
	catalog := DefaultCatalog()

	got := catalog.InferPrimaryLanguage([]string{
		"src/main.kt",
		"src/util.kt",
		"src/main.go",
	}, "engineer")
	if got != "kotlin" {
		t.Fatalf("InferPrimaryLanguage() = %q, want kotlin", got)
	}
}

func TestPlanExecCellGo(t *testing.T) {
	catalog := DefaultCatalog()
	roots := ExecRoots{
		Workspace: "/workspace",
		Temp:      "/tmp",
		Cache:     "/cache",
		Home:      "/home/agent",
		Out:       "/out",
	}

	plan, ok := catalog.PlanExecCell("go", roots)
	if !ok {
		t.Fatal("PlanExecCell(go) returned !ok")
	}
	if plan.Env["GOCACHE"] != "/cache/go" {
		t.Fatalf("GOCACHE = %q, want /cache/go", plan.Env["GOCACHE"])
	}
	if plan.Env["GOMODCACHE"] != "/cache/go/modcache" {
		t.Fatalf("GOMODCACHE = %q, want /cache/go/modcache", plan.Env["GOMODCACHE"])
	}
}

func TestDetectProjectPopulatesExecPlans(t *testing.T) {
	root := t.TempDir()
	mustWriteCatalogFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.24.0\n")
	mustWriteCatalogFile(t, filepath.Join(root, "pkg/service/service.go"), "package service\n")

	summary := DefaultCatalog().DetectProject(root)
	if summary.PrimaryLanguage != "go" {
		t.Fatalf("PrimaryLanguage = %q, want go", summary.PrimaryLanguage)
	}
	plan, ok := summary.ExecPlans["go"]
	if !ok {
		t.Fatal("expected go exec plan in summary")
	}
	if plan.Env["GOCACHE"] != "/cache/go" {
		t.Fatalf("summary GOCACHE = %q, want /cache/go", plan.Env["GOCACHE"])
	}
}

func TestDefaultCatalogSupportsAllTreeSitterLanguages(t *testing.T) {
	catalog := DefaultCatalog()
	languages := []string{
		"go",
		"rust",
		"python",
		"javascript",
		"typescript",
		"tsx",
		"java",
		"c",
		"cpp",
		"ruby",
		"swift",
		"kotlin",
		"json",
		"yaml",
		"toml",
		"html",
		"css",
		"bash",
		"markdown",
		"properties",
		"scala",
		"hcl",
		"vue",
		"svelte",
		"lua",
		"php",
		"elixir",
		"haskell",
		"zig",
	}

	for _, language := range languages {
		if _, ok := catalog.Profile(language); !ok {
			t.Fatalf("Profile(%q) returned !ok", language)
		}
	}
}

func mustWriteCatalogFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
