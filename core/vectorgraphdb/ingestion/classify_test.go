package ingestion

import (
	"testing"
)

func TestClassifyFile_Extensions(t *testing.T) {
	tests := []struct {
		path    string
		lang    string
		docType string
	}{
		// Tree-sitter parseable source code
		{"main.go", "go", "source_code"},
		{"lib.rs", "rust", "source_code"},
		{"app.py", "python", "source_code"},
		{"index.js", "javascript", "source_code"},
		{"index.ts", "typescript", "source_code"},
		{"App.tsx", "tsx", "source_code"},
		{"Main.java", "java", "source_code"},
		{"foo.c", "c", "source_code"},
		{"bar.cpp", "cpp", "source_code"},
		{"test.rb", "ruby", "source_code"},
		{"view.swift", "swift", "source_code"},
		{"build.kt", "kotlin", "source_code"},
		{"main.lua", "lua", "source_code"},
		{"index.php", "php", "source_code"},
		{"lib.ex", "elixir", "source_code"},
		{"Main.hs", "haskell", "source_code"},
		{"main.zig", "zig", "source_code"},

		// Tree-sitter markup/web
		{"README.md", "markdown", "markdown"},
		{"index.html", "html", "web_content"},

		// Tree-sitter config
		{"config.json", "json", "config"},
		{"values.yaml", "yaml", "config"},
		{"settings.yml", "yaml", "config"},
		{"pyproject.toml", "toml", "config"},
		{"main.tf", "hcl", "config"},

		// Non-parseable source code
		{"api.proto", "", "source_code"},
		{"schema.sql", "", "source_code"},
		{"query.graphql", "", "source_code"},
		{"build.gradle", "", "source_code"},
		{"rules.bzl", "", "source_code"},

		// Non-parseable config
		{"pom.xml", "", "config"},
		{"settings.ini", "", "config"},
		{"app.cfg", "", "config"},
		{"nginx.conf", "", "config"},
		{".env", "", "config"},

		// Non-parseable docs
		{"notes.txt", "", "note"},
		{"docs.rst", "", "markdown"},

		// Web content
		{"page.htm", "", "web_content"},
		{"icon.svg", "", "web_content"},
	}

	for _, tt := range tests {
		fc := ClassifyFile(tt.path, nil)
		if fc.Lang != tt.lang {
			t.Errorf("ClassifyFile(%q).Lang = %q, want %q", tt.path, fc.Lang, tt.lang)
		}
		if fc.DocType != tt.docType {
			t.Errorf("ClassifyFile(%q).DocType = %q, want %q", tt.path, fc.DocType, tt.docType)
		}
	}
}

func TestClassifyFile_Filenames(t *testing.T) {
	tests := []struct {
		path    string
		docType string
	}{
		{"Makefile", "source_code"},
		{"Dockerfile", "config"},
		{"BUILD", "source_code"},
		{"BUILD.bazel", "source_code"},
		{"WORKSPACE", "source_code"},
		{".gitignore", "config"},
		{".dockerignore", "config"},
		{".editorconfig", "config"},
		{"LICENSE", "note"},
		{"OWNERS", "note"},
		{"README", "markdown"},
		{"CHANGELOG", "markdown"},
		{"CONTRIBUTING", "markdown"},
	}

	for _, tt := range tests {
		fc := ClassifyFile(tt.path, nil)
		if fc.DocType != tt.docType {
			t.Errorf("ClassifyFile(%q).DocType = %q, want %q", tt.path, fc.DocType, tt.docType)
		}
		if fc.Lang != "" {
			t.Errorf("ClassifyFile(%q).Lang = %q, want empty", tt.path, fc.Lang)
		}
	}
}

func TestClassifyFile_Shebang(t *testing.T) {
	tests := []struct {
		name    string
		header  []byte
		lang    string
		docType string
	}{
		{"env python", []byte("#!/usr/bin/env python\n"), "python", "source_code"},
		{"env python3", []byte("#!/usr/bin/env python3\n"), "python", "source_code"},
		{"env python3.11", []byte("#!/usr/bin/env python3.11\n"), "python", "source_code"},
		{"direct bash", []byte("#!/bin/bash\n"), "bash", "source_code"},
		{"direct sh", []byte("#!/bin/sh\n"), "bash", "source_code"},
		{"env ruby", []byte("#!/usr/bin/env ruby\n"), "ruby", "source_code"},
		{"env node", []byte("#!/usr/bin/env node\n"), "javascript", "source_code"},
		{"env perl", []byte("#!/usr/bin/env perl\n"), "", "source_code"},
	}

	for _, tt := range tests {
		// Use path with no extension so extension/filename don't match
		fc := ClassifyFile("script", tt.header)
		if fc.Lang != tt.lang {
			t.Errorf("%s: Lang = %q, want %q", tt.name, fc.Lang, tt.lang)
		}
		if fc.DocType != tt.docType {
			t.Errorf("%s: DocType = %q, want %q", tt.name, fc.DocType, tt.docType)
		}
	}
}

func TestClassifyFile_Fallback(t *testing.T) {
	fc := ClassifyFile("unknown_file_no_ext", nil)
	if fc.Lang != "" {
		t.Errorf("fallback Lang = %q, want empty", fc.Lang)
	}
	if fc.DocType != "note" {
		t.Errorf("fallback DocType = %q, want %q", fc.DocType, "note")
	}
}

func TestGetLanguage_BackwardCompat(t *testing.T) {
	tests := []struct {
		ext  string
		lang string
	}{
		{".go", "go"},
		{".py", "python"},
		{".ts", "typescript"},
		{".proto", ""},
		{".unknown", ""},
	}

	for _, tt := range tests {
		got := GetLanguage(tt.ext)
		if got != tt.lang {
			t.Errorf("GetLanguage(%q) = %q, want %q", tt.ext, got, tt.lang)
		}
	}
}

func TestIsSupportedExtension_BackwardCompat(t *testing.T) {
	if !IsSupportedExtension(".go") {
		t.Error("IsSupportedExtension(.go) = false, want true")
	}
	if IsSupportedExtension(".proto") {
		t.Error("IsSupportedExtension(.proto) = true, want false (no grammar)")
	}
	if IsSupportedExtension(".unknown") {
		t.Error("IsSupportedExtension(.unknown) = true, want false")
	}
}
