package parser

import (
	"strings"
	"testing"
)

// =============================================================================
// TypeScriptParser Tests
// =============================================================================

func TestTypeScriptParser_Extensions(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	extensions := parser.Extensions()

	expected := []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
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

func TestTypeScriptParser_EmptyContent(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	result, err := parser.Parse([]byte{}, "test.ts")

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

func TestTypeScriptParser_ParseFunctions(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
function greet(name: string): string {
	return "Hello, " + name;
}

export function publicFunc() {}

async function fetchData() {}

export async function asyncPublic() {}
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	funcSymbols := filterSymbolsByKind(result.Symbols, KindFunction)
	if len(funcSymbols) != 4 {
		t.Errorf("expected 4 functions, got %d", len(funcSymbols))
	}

	publicFunc := findSymbolByName(result.Symbols, "publicFunc")
	if publicFunc == nil {
		t.Fatal("publicFunc not found")
	}
	if !publicFunc.Exported {
		t.Error("publicFunc should be exported")
	}

	greet := findSymbolByName(result.Symbols, "greet")
	if greet == nil {
		t.Fatal("greet not found")
	}
	if greet.Exported {
		t.Error("greet should not be exported")
	}
}

func TestTypeScriptParser_ParseClasses(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
class User {
	constructor(public name: string) {}
}

export class ApiClient {}

export abstract class BaseService {}
`
	result, err := parser.Parse([]byte(content), "test.ts")

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
	if user.Exported {
		t.Error("User should not be exported")
	}

	apiClient := findSymbolByName(result.Symbols, "ApiClient")
	if apiClient == nil {
		t.Fatal("ApiClient not found")
	}
	if !apiClient.Exported {
		t.Error("ApiClient should be exported")
	}
}

func TestTypeScriptParser_ParseInterfaces(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
interface UserProps {
	name: string;
	age: number;
}

export interface ApiResponse<T> {
	data: T;
	error?: string;
}
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	ifaceSymbols := filterSymbolsByKind(result.Symbols, KindInterface)
	if len(ifaceSymbols) != 2 {
		t.Errorf("expected 2 interfaces, got %d", len(ifaceSymbols))
	}
}

func TestTypeScriptParser_ParseTypes(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
type ID = string | number;

export type UserRole = "admin" | "user" | "guest";

type Handler = (event: Event) => void;
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	typeSymbols := filterSymbolsByKind(result.Symbols, KindType)
	if len(typeSymbols) != 3 {
		t.Errorf("expected 3 types, got %d", len(typeSymbols))
	}
}

func TestTypeScriptParser_ParseConstants(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
const MAX_SIZE = 1024;

export const API_URL = "https://api.example.com";

const config = { debug: true };
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	constSymbols := filterSymbolsByKind(result.Symbols, KindConst)
	if len(constSymbols) != 3 {
		t.Errorf("expected 3 constants, got %d", len(constSymbols))
	}
}

func TestTypeScriptParser_ParseVariables(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
let counter = 0;

export let debugMode = false;

var legacyVar = "old";
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	varSymbols := filterSymbolsByKind(result.Symbols, KindVariable)
	if len(varSymbols) != 3 {
		t.Errorf("expected 3 variables, got %d", len(varSymbols))
	}
}

func TestTypeScriptParser_ParseImports(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
import React from 'react';
import { useState, useEffect } from 'react';
import type { Config } from './types';
import * as utils from './utils';
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result.Imports) != 4 {
		t.Errorf("expected 4 imports, got %d", len(result.Imports))
	}

	expectedImports := []string{"react", "./types", "./utils"}
	for _, exp := range expectedImports {
		found := false
		for _, imp := range result.Imports {
			if imp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("import %q not found", exp)
		}
	}
}

func TestTypeScriptParser_ParseComments(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
// Single line comment
function test() {}

/*
 * Multi-line comment
 * with JSDoc style
 */
class Example {}

/** Inline doc comment */
const value = 42;
`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should not be empty")
	}
	if !strings.Contains(result.Comments, "Single line comment") {
		t.Error("comments should contain single line comment")
	}
	if !strings.Contains(result.Comments, "Multi-line comment") {
		t.Error("comments should contain multi-line comment")
	}
}

func TestTypeScriptParser_ParseMetadata(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"typescript", "test.ts", "typescript"},
		{"tsx", "Component.tsx", "typescript"},
		{"javascript", "script.js", "javascript"},
		{"jsx", "Component.jsx", "javascript"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, _ := parser.Parse([]byte("const x = 1;"), tt.path)
			if result.Metadata["language"] != tt.expected {
				t.Errorf("expected language %q, got %q", tt.expected, result.Metadata["language"])
			}
		})
	}
}

func TestTypeScriptParser_LineNumbers(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
function first() {}

function second() {}

function third() {}
`
	result, err := parser.Parse([]byte(content), "test.ts")

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
	if second == nil || second.Line != 4 {
		line := 0
		if second != nil {
			line = second.Line
		}
		t.Errorf("second should be at line 4, got %d", line)
	}
}

func TestTypeScriptParser_FunctionSignature(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `function greet(name: string): string {
	return name;
}`
	result, err := parser.Parse([]byte(content), "test.ts")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	greet := findSymbolByName(result.Symbols, "greet")
	if greet == nil {
		t.Fatal("greet function not found")
	}
	if !strings.Contains(greet.Signature, "greet") {
		t.Error("signature should contain function name")
	}
	if !strings.Contains(greet.Signature, "name: string") {
		t.Errorf("signature should contain parameters, got %q", greet.Signature)
	}
}

func TestTypeScriptParser_JSXFile(t *testing.T) {
	t.Parallel()

	parser := NewTypeScriptParser()
	content := `
import React from 'react';

export function Button({ children, onClick }) {
	return <button onClick={onClick}>{children}</button>;
}

export const Icon = () => <span>icon</span>;
`
	result, err := parser.Parse([]byte(content), "Button.jsx")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	button := findSymbolByName(result.Symbols, "Button")
	if button == nil {
		t.Error("Button function not found")
	}
}
