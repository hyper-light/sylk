package librarian

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRepositoryBriefInfersRepoFacts(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module github.com/example/project\n\ngo 1.24\n\nrequire github.com/stretchr/testify v1.10.0\n")
	mustWriteFile(t, filepath.Join(root, "Makefile"), "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n\nlint:\n\tgolangci-lint run\n")
	mustWriteFile(t, filepath.Join(root, "CODEOWNERS"), "/cmd/ @platform\n/internal/cache/ @backend\n")
	mustWriteFile(t, filepath.Join(root, "cmd", "app", "main.go"), "package main\n\nfunc main() {}\n")
	mustWriteFile(t, filepath.Join(root, "internal", "cache", "cache.go"), "package cache\n\ntype Store struct{}\n")
	mustWriteFile(t, filepath.Join(root, ".golangci.yml"), "run:\n  timeout: 3m\n")

	l := &Librarian{config: Config{WorkingDirectory: root}}
	brief, err := l.buildRepositoryBrief(context.Background(), repoBriefParams{
		IncludeOwnership: true,
		Depth:            4,
	})
	if err != nil {
		t.Fatalf("buildRepositoryBrief() error = %v", err)
	}
	if brief.Summary == nil {
		t.Fatal("expected summary")
	}
	if len(brief.Modules) == 0 || brief.Modules[0].Ecosystem != "go" {
		t.Fatalf("expected go module, got %+v", brief.Modules)
	}
	if len(brief.Ownership) == 0 {
		t.Fatal("expected ownership rules from CODEOWNERS")
	}
	if !containsString(brief.Summary.EntryPoints, "cmd/app/main.go") {
		t.Fatalf("expected entrypoint in %+v", brief.Summary.EntryPoints)
	}
	if !containsDependency(brief.Summary.Dependencies, "github.com/stretchr/testify") {
		t.Fatalf("expected dependencies to contain testify: %+v", brief.Summary.Dependencies)
	}
	if !hasCommand(brief.InferredCommands, "make build") {
		t.Fatalf("expected inferred commands to include make build: %+v", brief.InferredCommands)
	}
	if !hasCommand(brief.InferredCommands, "go test ./...") {
		t.Fatalf("expected inferred commands to include go test ./...: %+v", brief.InferredCommands)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func hasCommand(commands []RepositoryCommand, expected string) bool {
	for _, command := range commands {
		if command.Command == expected {
			return true
		}
	}
	return false
}

func containsDependency(deps []string, expected string) bool {
	for _, dep := range deps {
		if dep == expected {
			return true
		}
	}
	return false
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}
