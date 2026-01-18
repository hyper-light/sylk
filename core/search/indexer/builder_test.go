package indexer

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/parser"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewDocumentBuilder(t *testing.T) {
	builder := NewDocumentBuilder()

	assert.NotNil(t, builder, "NewDocumentBuilder should return a non-nil builder")
	assert.NotNil(t, builder.registry, "Builder should have a non-nil registry")
}

func TestNewDocumentBuilderWithRegistry(t *testing.T) {
	customRegistry := parser.NewParserRegistry()
	builder := NewDocumentBuilderWithRegistry(customRegistry)

	assert.NotNil(t, builder, "NewDocumentBuilderWithRegistry should return a non-nil builder")
	assert.Same(t, customRegistry, builder.registry, "Builder should use the provided registry")
}

func TestNewDocumentBuilderWithNilRegistry(t *testing.T) {
	builder := NewDocumentBuilderWithRegistry(nil)

	assert.NotNil(t, builder, "Builder should not be nil even with nil registry")
	assert.NotNil(t, builder.registry, "Builder should use default registry when nil is provided")
}

// =============================================================================
// Go Source File Tests
// =============================================================================

func TestBuild_GoSourceFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main

import "fmt"

// HelloWorld prints a greeting message.
func HelloWorld() {
	fmt.Println("Hello, World!")
}

type Config struct {
	Name string
}
`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err, "Building Go source file should not return an error")
	require.NotNil(t, doc, "Document should not be nil")

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, "Document type should be source_code")
	assert.Equal(t, "/project/main.go", doc.Path, "Path should match")
	assert.Equal(t, "go", doc.Language, "Language should be 'go'")
	assert.NotEmpty(t, doc.ID, "Document ID should not be empty")
	assert.NotEmpty(t, doc.Checksum, "Checksum should not be empty")
	assert.Equal(t, string(content), doc.Content, "Content should match")

	// Verify symbols were extracted
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted symbols")
	assert.Contains(t, doc.Symbols, "HelloWorld", "Should contain HelloWorld function")
	assert.Contains(t, doc.Symbols, "Config", "Should contain Config type")

	// Verify imports were extracted
	assert.Contains(t, doc.Imports, "fmt", "Should contain fmt import")
}

func TestBuild_GoSourceFile_ExtractsComments(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main

// Package comment for main.
// This is a multi-line comment.

// ProcessData handles data processing.
func ProcessData() {}
`)
	info := &FileInfo{
		Path:      "/project/process.go",
		Name:      "process.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.NotEmpty(t, doc.Comments, "Comments should be extracted")
	assert.Contains(t, doc.Comments, "ProcessData handles data processing", "Should contain function comment")
}

// =============================================================================
// TypeScript/JavaScript Tests
// =============================================================================

func TestBuild_TypeScriptFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`import { useState } from 'react';

// Component for displaying user profile
export function UserProfile(props: UserProps) {
	const [name, setName] = useState('');
	return <div>{name}</div>;
}

export interface UserProps {
	id: number;
	name: string;
}
`)
	info := &FileInfo{
		Path:      "/project/src/UserProfile.tsx",
		Name:      "UserProfile.tsx",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".tsx",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, "Document type should be source_code")
	assert.Equal(t, "typescript", doc.Language, "Language should be typescript for .tsx")
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted symbols")
	assert.Contains(t, doc.Imports, "react", "Should contain react import")
}

func TestBuild_JavaScriptFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`const express = require('express');

// Initialize the app
function initApp() {
	const app = express();
	return app;
}

module.exports = { initApp };
`)
	info := &FileInfo{
		Path:      "/project/app.js",
		Name:      "app.js",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".js",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type)
	assert.Equal(t, "javascript", doc.Language, "Language should be javascript for .js")
}

// =============================================================================
// Python Tests
// =============================================================================

func TestBuild_PythonFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`import os
from typing import List

def process_files(paths: List[str]) -> int:
    """Process multiple files and return count."""
    count = 0
    for path in paths:
        if os.path.exists(path):
            count += 1
    return count

class FileProcessor:
    """Handles file processing operations."""
    def __init__(self, base_path: str):
        self.base_path = base_path
`)
	info := &FileInfo{
		Path:      "/project/processor.py",
		Name:      "processor.py",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".py",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type)
	assert.Equal(t, "python", doc.Language)
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted symbols")
	assert.Contains(t, doc.Imports, "os", "Should contain os import")
	assert.Contains(t, doc.Imports, "typing", "Should contain typing import")
}

// =============================================================================
// Markdown Tests
// =============================================================================

func TestBuild_MarkdownFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`# Project README

## Introduction

This is a sample project demonstrating the document builder.

## Installation

Run the following command:

` + "```bash\nnpm install\n```" + `

## Links

Check the [documentation](https://example.com/docs) for more info.
`)
	info := &FileInfo{
		Path:      "/project/README.md",
		Name:      "README.md",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".md",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeMarkdown, doc.Type, "Document type should be markdown")
	assert.Equal(t, "markdown", doc.Language, "Language should be markdown")
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted headings")
	assert.Contains(t, doc.Imports, "https://example.com/docs", "Should contain link URL")
}

func TestBuild_MarkdownVariant(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`# Title

Some content here.
`)
	info := &FileInfo{
		Path:      "/project/doc.markdown",
		Name:      "doc.markdown",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".markdown",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeMarkdown, doc.Type, "Document type should be markdown for .markdown extension")
}

// =============================================================================
// Config File Tests
// =============================================================================

func TestBuild_JSONConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`{
	"name": "my-project",
	"version": "1.0.0",
	"dependencies": {
		"lodash": "^4.17.21"
	}
}`)
	info := &FileInfo{
		Path:      "/project/package.json",
		Name:      "package.json",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".json",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config")
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted keys")
}

func TestBuild_YAMLConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`# Kubernetes deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 3
`)
	info := &FileInfo{
		Path:      "/project/deployment.yaml",
		Name:      "deployment.yaml",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".yaml",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config")
	assert.True(t, len(doc.Symbols) > 0, "Should have extracted keys")
	assert.Contains(t, doc.Comments, "Kubernetes deployment", "Should extract YAML comments")
}

func TestBuild_YMLConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`name: CI Pipeline
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
`)
	info := &FileInfo{
		Path:      "/project/.github/workflows/ci.yml",
		Name:      "ci.yml",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".yml",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config for .yml")
}

func TestBuild_TOMLConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`# Project configuration
[package]
name = "my-crate"
version = "0.1.0"

[dependencies]
serde = "1.0"
`)
	info := &FileInfo{
		Path:      "/project/Cargo.toml",
		Name:      "Cargo.toml",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".toml",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config")
}

func TestBuild_XMLConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<project>
	<name>my-project</name>
	<version>1.0</version>
</project>
`)
	info := &FileInfo{
		Path:      "/project/pom.xml",
		Name:      "pom.xml",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".xml",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config for .xml")
}

func TestBuild_EnvConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`DATABASE_URL=postgres://localhost:5432/mydb
API_KEY=secret123
DEBUG=true
`)
	info := &FileInfo{
		Path:      "/project/.env",
		Name:      ".env",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".env",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config for .env")
}

func TestBuild_INIConfigFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`[database]
host=localhost
port=5432

[server]
address=0.0.0.0
`)
	info := &FileInfo{
		Path:      "/project/config.ini",
		Name:      "config.ini",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".ini",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeConfig, doc.Type, "Document type should be config for .ini")
}

// =============================================================================
// Unknown Extension Tests
// =============================================================================

func TestBuild_UnknownExtension_DefaultsToSourceCode(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`Some content in an unknown file format.
Could be source code, who knows!
`)
	info := &FileInfo{
		Path:      "/project/script.unknown",
		Name:      "script.unknown",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".unknown",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, "Unknown extension should default to source_code")
}

func TestBuild_NoExtension_DefaultsToSourceCode(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`#!/bin/bash
echo "Hello World"
`)
	info := &FileInfo{
		Path:      "/project/Makefile",
		Name:      "Makefile",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: "",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, "No extension should default to source_code")
}

// =============================================================================
// Additional Source Code Languages Tests
// =============================================================================

func TestBuild_RustFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`fn main() {
    println!("Hello, Rust!");
}
`)
	info := &FileInfo{
		Path:      "/project/main.rs",
		Name:      "main.rs",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".rs",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".rs should be source_code")
}

func TestBuild_JavaFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`public class Main {
    public static void main(String[] args) {
        System.out.println("Hello, Java!");
    }
}
`)
	info := &FileInfo{
		Path:      "/project/Main.java",
		Name:      "Main.java",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".java",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".java should be source_code")
}

func TestBuild_CFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`#include <stdio.h>

int main() {
    printf("Hello, C!");
    return 0;
}
`)
	info := &FileInfo{
		Path:      "/project/main.c",
		Name:      "main.c",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".c",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".c should be source_code")
}

func TestBuild_CppFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`#include <iostream>

int main() {
    std::cout << "Hello, C++!" << std::endl;
    return 0;
}
`)
	info := &FileInfo{
		Path:      "/project/main.cpp",
		Name:      "main.cpp",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".cpp",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".cpp should be source_code")
}

func TestBuild_HeaderFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`#ifndef MYHEADER_H
#define MYHEADER_H

void my_function();

#endif
`)
	info := &FileInfo{
		Path:      "/project/myheader.h",
		Name:      "myheader.h",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".h",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".h should be source_code")
}

func TestBuild_RubyFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`class Greeter
  def initialize(name)
    @name = name
  end

  def greet
    puts "Hello, #{@name}!"
  end
end
`)
	info := &FileInfo{
		Path:      "/project/greeter.rb",
		Name:      "greeter.rb",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".rb",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type, ".rb should be source_code")
}

// =============================================================================
// Checksum and ID Generation Tests
// =============================================================================

func TestBuild_GeneratesConsistentID(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main

func main() {}
`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc1, err := builder.Build(info, content)
	require.NoError(t, err)

	doc2, err := builder.Build(info, content)
	require.NoError(t, err)

	assert.Equal(t, doc1.ID, doc2.ID, "Same content should produce same ID")
	assert.Equal(t, doc1.Checksum, doc2.Checksum, "Same content should produce same checksum")
}

func TestBuild_DifferentContentDifferentID(t *testing.T) {
	builder := NewDocumentBuilder()
	content1 := []byte(`package main

func main() {}
`)
	content2 := []byte(`package main

func main() { println("hello") }
`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      100,
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc1, err := builder.Build(info, content1)
	require.NoError(t, err)

	info.Size = int64(len(content2))
	doc2, err := builder.Build(info, content2)
	require.NoError(t, err)

	assert.NotEqual(t, doc1.ID, doc2.ID, "Different content should produce different IDs")
	assert.NotEqual(t, doc1.Checksum, doc2.Checksum, "Different content should produce different checksums")
}

func TestBuild_IDAndChecksumMatch(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)
	require.NoError(t, err)

	// ID and Checksum are both SHA-256 of content
	assert.Equal(t, doc.ID, doc.Checksum, "ID and Checksum should be equal (both from content hash)")
}

// =============================================================================
// Error Cases Tests
// =============================================================================

func TestBuild_NilFileInfo_ReturnsError(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`some content`)

	doc, err := builder.Build(nil, content)

	assert.Nil(t, doc, "Document should be nil when FileInfo is nil")
	assert.ErrorIs(t, err, ErrNilFileInfo, "Should return ErrNilFileInfo")
}

func TestBuild_EmptyPath_ReturnsError(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`some content`)
	info := &FileInfo{
		Path:      "",
		Name:      "test.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	assert.Nil(t, doc, "Document should be nil when path is empty")
	assert.ErrorIs(t, err, ErrEmptyPath, "Should return ErrEmptyPath")
}

// =============================================================================
// Empty Content Tests
// =============================================================================

func TestBuild_EmptyContent_IsValid(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte{}
	info := &FileInfo{
		Path:      "/project/empty.go",
		Name:      "empty.go",
		Size:      0,
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err, "Empty content should be valid")
	require.NotNil(t, doc, "Document should not be nil")

	assert.Equal(t, "", doc.Content, "Content should be empty string")
	assert.NotEmpty(t, doc.ID, "ID should still be generated")
	assert.NotEmpty(t, doc.Checksum, "Checksum should still be generated")
	assert.Empty(t, doc.Symbols, "Symbols should be empty")
	assert.Empty(t, doc.Imports, "Imports should be empty")
}

func TestBuild_NilContent_IsValid(t *testing.T) {
	builder := NewDocumentBuilder()
	info := &FileInfo{
		Path:      "/project/empty.go",
		Name:      "empty.go",
		Size:      0,
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, nil)

	require.NoError(t, err, "Nil content should be valid (treated as empty)")
	require.NotNil(t, doc, "Document should not be nil")
}

// =============================================================================
// Timestamp Tests
// =============================================================================

func TestBuild_SetsTimestamps(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main`)
	modTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   modTime,
		Extension: ".go",
	}

	beforeBuild := time.Now()
	doc, err := builder.Build(info, content)
	afterBuild := time.Now()

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, modTime, doc.ModifiedAt, "ModifiedAt should match FileInfo.ModTime")
	assert.True(t, doc.IndexedAt.After(beforeBuild) || doc.IndexedAt.Equal(beforeBuild),
		"IndexedAt should be >= build start time")
	assert.True(t, doc.IndexedAt.Before(afterBuild) || doc.IndexedAt.Equal(afterBuild),
		"IndexedAt should be <= build end time")
}

// =============================================================================
// Parser Extraction Tests
// =============================================================================

func TestBuild_ParserExtractsSymbols(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package parser

type Scanner struct {
	config ScanConfig
}

func NewScanner() *Scanner {
	return &Scanner{}
}

func (s *Scanner) Scan() error {
	return nil
}

const MaxSize = 1024

var defaultTimeout = 30
`)
	info := &FileInfo{
		Path:      "/project/scanner.go",
		Name:      "scanner.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	// Check that various symbol types are extracted
	assert.Contains(t, doc.Symbols, "Scanner", "Should extract type Scanner")
	assert.Contains(t, doc.Symbols, "NewScanner", "Should extract function NewScanner")
	assert.Contains(t, doc.Symbols, "Scan", "Should extract method Scan")
	assert.Contains(t, doc.Symbols, "MaxSize", "Should extract const MaxSize")
	assert.Contains(t, doc.Symbols, "defaultTimeout", "Should extract var defaultTimeout")
}

func TestBuild_NoParserAvailable_StillBuildsDocument(t *testing.T) {
	// Create a builder with an empty registry
	emptyRegistry := parser.NewParserRegistry()
	builder := NewDocumentBuilderWithRegistry(emptyRegistry)

	content := []byte(`some content in an unknown format`)
	info := &FileInfo{
		Path:      "/project/file.xyz",
		Name:      "file.xyz",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".xyz",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err, "Should not error when no parser is available")
	require.NotNil(t, doc, "Should still build document")

	assert.Equal(t, string(content), doc.Content, "Content should be set")
	assert.Empty(t, doc.Symbols, "Symbols should be empty without parser")
	assert.Empty(t, doc.Imports, "Imports should be empty without parser")
	assert.Empty(t, doc.Comments, "Comments should be empty without parser")
}

// =============================================================================
// Document Validation Tests
// =============================================================================

func TestBuild_DocumentPassesValidation(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main

func main() {}
`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	// The built document should pass validation
	assert.NoError(t, doc.Validate(), "Built document should pass validation")
}

// =============================================================================
// Extension Detection Edge Cases
// =============================================================================

func TestBuild_CaseInsensitiveExtension(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`# Heading\n\nContent`)

	testCases := []struct {
		extension string
		expected  search.DocumentType
	}{
		{".MD", search.DocTypeMarkdown},
		{".Md", search.DocTypeMarkdown},
		{".JSON", search.DocTypeConfig},
		{".Json", search.DocTypeConfig},
		{".GO", search.DocTypeSourceCode},
		{".Go", search.DocTypeSourceCode},
	}

	for _, tc := range testCases {
		t.Run(tc.extension, func(t *testing.T) {
			info := &FileInfo{
				Path:      "/project/file" + tc.extension,
				Name:      "file" + tc.extension,
				Size:      int64(len(content)),
				ModTime:   time.Now(),
				Extension: tc.extension,
			}

			doc, err := builder.Build(info, content)

			require.NoError(t, err)
			require.NotNil(t, doc)
			assert.Equal(t, tc.expected, doc.Type, "Extension %s should produce type %s", tc.extension, tc.expected)
		})
	}
}

// =============================================================================
// Language Detection Tests
// =============================================================================

func TestBuild_LanguageDetection(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`content`)

	testCases := []struct {
		extension string
		language  string
	}{
		{".go", "go"},
		{".py", "python"},
		{".ts", "typescript"},
		{".tsx", "typescript"},
		{".js", "javascript"},
		{".jsx", "javascript"},
		{".md", "markdown"},
		{".markdown", "markdown"},
		{".rs", "rust"},
		{".java", "java"},
		{".c", "c"},
		{".cpp", "cpp"},
		{".h", "c"},
		{".rb", "ruby"},
		{".json", "json"},
		{".yaml", "yaml"},
		{".yml", "yaml"},
		{".toml", "toml"},
		{".xml", "xml"},
		{".env", "env"},
		{".ini", "ini"},
	}

	for _, tc := range testCases {
		t.Run(tc.extension, func(t *testing.T) {
			info := &FileInfo{
				Path:      "/project/file" + tc.extension,
				Name:      "file" + tc.extension,
				Size:      int64(len(content)),
				ModTime:   time.Now(),
				Extension: tc.extension,
			}

			doc, err := builder.Build(info, content)

			require.NoError(t, err)
			require.NotNil(t, doc)
			assert.Equal(t, tc.language, doc.Language, "Extension %s should have language %s", tc.extension, tc.language)
		})
	}
}

// =============================================================================
// JSX Extension Test
// =============================================================================

func TestBuild_JSXFile(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`import React from 'react';

export function App() {
	return <div>Hello World</div>;
}
`)
	info := &FileInfo{
		Path:      "/project/App.jsx",
		Name:      "App.jsx",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".jsx",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, search.DocTypeSourceCode, doc.Type)
	assert.Equal(t, "javascript", doc.Language)
}

// =============================================================================
// Symbol Name Extraction Tests
// =============================================================================

func TestBuild_SymbolNamesAreStrings(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte(`package main

type Config struct{}
func Process() {}
`)
	info := &FileInfo{
		Path:      "/project/main.go",
		Name:      "main.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	// Symbols should be a slice of strings (just names)
	for _, sym := range doc.Symbols {
		assert.IsType(t, "", sym, "Each symbol should be a string")
		assert.NotEmpty(t, sym, "Symbol names should not be empty")
	}
}

// =============================================================================
// Whitespace-Only Content Tests
// =============================================================================

func TestBuild_WhitespaceOnlyContent(t *testing.T) {
	builder := NewDocumentBuilder()
	content := []byte("   \n\t\n   \n")
	info := &FileInfo{
		Path:      "/project/whitespace.txt",
		Name:      "whitespace.txt",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".txt",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err, "Whitespace-only content should be valid")
	require.NotNil(t, doc)

	assert.NotEmpty(t, doc.ID, "ID should be generated")
	assert.NotEmpty(t, doc.Checksum, "Checksum should be generated")
}

// =============================================================================
// Large Symbol Count Tests
// =============================================================================

func TestBuild_ManySymbols(t *testing.T) {
	builder := NewDocumentBuilder()

	// Generate content with many functions
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	for i := 0; i < 100; i++ {
		sb.WriteString("func Function")
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(string(rune('0' + i/26)))
		sb.WriteString("() {}\n")
	}
	content := []byte(sb.String())

	info := &FileInfo{
		Path:      "/project/many.go",
		Name:      "many.go",
		Size:      int64(len(content)),
		ModTime:   time.Now(),
		Extension: ".go",
	}

	doc, err := builder.Build(info, content)

	require.NoError(t, err)
	require.NotNil(t, doc)

	// Should have extracted many symbols
	assert.True(t, len(doc.Symbols) >= 50, "Should extract many symbols")
}
