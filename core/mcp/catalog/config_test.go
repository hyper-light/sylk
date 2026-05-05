package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogMergePrecedence(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeYAML(t, globalPath, `servers:
  - id: github
    transport: stdio
    command: gh
    domains: [git]
  - id: docs
    transport: http
    endpoint: https://example.com/mcp
`)
	writeYAML(t, projectPath, `servers:
  - id: docs
    transport: http
    endpoint: https://override.example.com/mcp
    trusted_read_only: true
`)
	mgr := NewManager()
	if err := mgr.LoadGlobal(globalPath); err != nil {
		t.Fatalf("load global: %v", err)
	}
	if err := mgr.LoadProject(projectPath); err != nil {
		t.Fatalf("load project: %v", err)
	}
	cat := mgr.Effective()
	if len(cat.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cat.Servers))
	}
	docs := cat.Servers["docs"]
	if docs.Endpoint != "https://override.example.com/mcp" {
		t.Fatalf("project layer didn't override: %s", docs.Endpoint)
	}
	if !docs.TrustedReadOnly {
		t.Fatal("project layer didn't apply trusted_read_only")
	}
	github := cat.Servers["github"]
	if github.Command != "gh" {
		t.Fatalf("global layer dropped: %+v", github)
	}
}

func TestCatalogReloadPicksUpMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.yaml")
	writeYAML(t, path, `servers:
  - id: a
    transport: stdio
    command: a
`)
	mgr := NewManager()
	if err := mgr.LoadProject(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(mgr.Effective().Servers) != 1 {
		t.Fatalf("expected 1 server")
	}
	// Replace contents and bump mtime.
	writeYAML(t, path, `servers:
  - id: a
    transport: stdio
    command: a
  - id: b
    transport: stdio
    command: b
`)
	bumpMtime(t, path)
	changed, err := mgr.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !changed {
		t.Fatal("expected reload to report change")
	}
	if len(mgr.Effective().Servers) != 2 {
		t.Fatalf("expected 2 servers after reload, got %d", len(mgr.Effective().Servers))
	}
}

func writeYAML(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func bumpMtime(t *testing.T, path string) {
	t.Helper()
	// Touch via re-write so mtime advances on every platform.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}
