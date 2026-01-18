package parser

import (
	"strings"
	"testing"
)

// =============================================================================
// PythonParser Tests
// =============================================================================

func TestPythonParser_Extensions(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	extensions := parser.Extensions()

	expected := []string{".py", ".pyi", ".pyw"}
	if len(extensions) != len(expected) {
		t.Errorf("expected %d extensions, got %d", len(expected), len(extensions))
	}

	for _, ext := range expected {
		found := false
		for _, got := range extensions {
			if got == ext {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected extension %s not found", ext)
		}
	}
}

func TestPythonParser_EmptyContent(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	result, err := parser.Parse([]byte{}, "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if len(result.Symbols) != 0 {
		t.Error("empty content should have no symbols")
	}
}

func TestPythonParser_ParseFunctions(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
def greet(name: str) -> str:
    return f"Hello, {name}"

def _private_func():
    pass

def __double_underscore():
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	funcSymbols := filterSymbolsByKind(result.Symbols, KindFunction)
	if len(funcSymbols) != 3 {
		t.Errorf("expected 3 functions, got %d", len(funcSymbols))
	}

	greet := findSymbolByName(result.Symbols, "greet")
	if greet == nil {
		t.Fatal("greet function not found")
	}
	if !greet.Exported {
		t.Error("greet should be exported (no underscore prefix)")
	}

	private := findSymbolByName(result.Symbols, "_private_func")
	if private == nil {
		t.Fatal("_private_func not found")
	}
	if private.Exported {
		t.Error("_private_func should not be exported")
	}
}

func TestPythonParser_ParseAsyncFunctions(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
async def fetch_data(url: str):
    async with aiohttp.ClientSession() as session:
        return await session.get(url)

async def process():
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	funcSymbols := filterSymbolsByKind(result.Symbols, KindFunction)
	if len(funcSymbols) != 2 {
		t.Errorf("expected 2 async functions, got %d", len(funcSymbols))
	}

	fetchData := findSymbolByName(result.Symbols, "fetch_data")
	if fetchData == nil {
		t.Fatal("fetch_data function not found")
	}
	if !strings.Contains(fetchData.Signature, "async") {
		t.Error("async function signature should contain 'async'")
	}
}

func TestPythonParser_ParseClasses(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
class User:
    def __init__(self, name: str):
        self.name = name

class _PrivateClass:
    pass

class ApiClient(BaseClient):
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	classSymbols := filterSymbolsByKind(result.Symbols, KindClass)
	if len(classSymbols) != 3 {
		t.Errorf("expected 3 classes, got %d", len(classSymbols))
	}

	user := findSymbolByName(result.Symbols, "User")
	if user == nil {
		t.Fatal("User class not found")
	}
	if !user.Exported {
		t.Error("User should be exported")
	}

	private := findSymbolByName(result.Symbols, "_PrivateClass")
	if private == nil {
		t.Fatal("_PrivateClass not found")
	}
	if private.Exported {
		t.Error("_PrivateClass should not be exported")
	}
}

func TestPythonParser_ParseDecorators(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
@staticmethod
def static_func():
    pass

@property
def name(self):
    return self._name

@app.route("/")
@login_required
def index():
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	staticFunc := findSymbolByName(result.Symbols, "static_func")
	if staticFunc == nil {
		t.Fatal("static_func not found")
	}
	if !strings.Contains(staticFunc.Signature, "@staticmethod") {
		t.Errorf("signature should contain decorator, got %q", staticFunc.Signature)
	}

	index := findSymbolByName(result.Symbols, "index")
	if index == nil {
		t.Fatal("index not found")
	}
	// Should have both decorators
	if !strings.Contains(index.Signature, "@app") {
		t.Errorf("signature should contain @app decorator, got %q", index.Signature)
	}
}

func TestPythonParser_ParseImports(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
import os
import sys, json
from typing import List, Dict
from collections.abc import Mapping
from . import local_module
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedImports := []string{"os", "sys", "json", "typing", "collections.abc", "."}
	for _, exp := range expectedImports {
		found := false
		for _, imp := range result.Imports {
			if imp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("import %q not found in %v", exp, result.Imports)
		}
	}
}

func TestPythonParser_ParseImportAs(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
import numpy as np
import pandas as pd
from datetime import datetime as dt
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should extract the module name, not the alias
	found := false
	for _, imp := range result.Imports {
		if imp == "numpy" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("import 'numpy' not found in %v", result.Imports)
	}
}

func TestPythonParser_ParseComments(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
# This is a comment
def func():
    pass  # inline comment

# Another comment
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should not be empty")
	}
	if !strings.Contains(result.Comments, "This is a comment") {
		t.Error("comments should contain 'This is a comment'")
	}
}

func TestPythonParser_ParseDocstrings(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
"""
Module docstring here.
This explains the module.
"""

def func():
    """Function docstring."""
    pass

class MyClass:
    '''Class docstring with single quotes.'''
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should include docstrings")
	}
	if !strings.Contains(result.Comments, "Module docstring") {
		t.Error("comments should contain module docstring")
	}
}

func TestPythonParser_ParseMetadata(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()

	tests := []struct {
		name       string
		path       string
		stubFile   bool
	}{
		{"regular python", "test.py", false},
		{"stub file", "test.pyi", true},
		{"pyw file", "script.pyw", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, _ := parser.Parse([]byte("def func(): pass"), tt.path)

			if result.Metadata["language"] != "python" {
				t.Error("language should be 'python'")
			}

			_, hasStub := result.Metadata["stub_file"]
			if hasStub != tt.stubFile {
				t.Errorf("stub_file metadata mismatch for %s", tt.path)
			}
		})
	}
}

func TestPythonParser_LineNumbers(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
def first():
    pass

def second():
    pass

class Third:
    pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	first := findSymbolByName(result.Symbols, "first")
	if first == nil || first.Line != 2 {
		line := 0
		if first != nil {
			line = first.Line
		}
		t.Errorf("first should be at line 2, got %d", line)
	}

	second := findSymbolByName(result.Symbols, "second")
	if second == nil || second.Line != 5 {
		line := 0
		if second != nil {
			line = second.Line
		}
		t.Errorf("second should be at line 5, got %d", line)
	}

	third := findSymbolByName(result.Symbols, "Third")
	if third == nil || third.Line != 8 {
		line := 0
		if third != nil {
			line = third.Line
		}
		t.Errorf("Third should be at line 8, got %d", line)
	}
}

func TestPythonParser_NestedDefinitions(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
def outer():
    def inner():
        pass
    return inner

class Outer:
    class Inner:
        pass
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should find all definitions including nested ones
	outer := findSymbolByName(result.Symbols, "outer")
	if outer == nil {
		t.Error("outer function not found")
	}

	inner := findSymbolByName(result.Symbols, "inner")
	if inner == nil {
		t.Error("nested inner function not found")
	}
}

func TestPythonParser_FunctionSignature(t *testing.T) {
	t.Parallel()

	parser := NewPythonParser()
	content := `
def greet(name: str, greeting: str = "Hello") -> str:
    return f"{greeting}, {name}"
`
	result, err := parser.Parse([]byte(content), "test.py")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	greet := findSymbolByName(result.Symbols, "greet")
	if greet == nil {
		t.Fatal("greet function not found")
	}
	if !strings.Contains(greet.Signature, "name: str") {
		t.Errorf("signature should contain parameters, got %q", greet.Signature)
	}
	if !strings.Contains(greet.Signature, "-> str") {
		t.Errorf("signature should contain return type, got %q", greet.Signature)
	}
}
