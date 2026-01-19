package extractors

import (
	"testing"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Go Extractor Tests
// =============================================================================

func TestGoExtractor_BasicFunction(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func hello(name string) string {
	return "Hello, " + name
}
`

	entities, err := extractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2) // package + function

	// Check package
	pkg := entities[0]
	assert.Equal(t, "main", pkg.Name)
	assert.Equal(t, knowledge.EntityKindPackage, pkg.Kind)

	// Check function
	fn := entities[1]
	assert.Equal(t, "hello", fn.Name)
	assert.Equal(t, knowledge.EntityKindFunction, fn.Kind)
	assert.Equal(t, "/test/main.go", fn.FilePath)
	assert.Equal(t, 3, fn.StartLine)
	assert.Equal(t, 5, fn.EndLine)
	assert.Contains(t, fn.Signature, "func hello(name string) string")
}

func TestGoExtractor_MultipleReturnValues(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func divide(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}
`

	entities, err := extractor.Extract("/test/math.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	fn := entities[1]
	assert.Equal(t, "divide", fn.Name)
	assert.Contains(t, fn.Signature, "(int, error)")
}

func TestGoExtractor_StructAndMethod(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package user

type User struct {
	Name string
	Age  int
}

func (u *User) Greet() string {
	return "Hello, " + u.Name
}

func (u User) GetAge() int {
	return u.Age
}
`

	entities, err := extractor.Extract("/test/user.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 4) // package + struct + 2 methods

	// Check struct
	structEntity := entities[1]
	assert.Equal(t, "User", structEntity.Name)
	assert.Equal(t, knowledge.EntityKindStruct, structEntity.Kind)
	assert.Contains(t, structEntity.Signature, "type User struct")

	// Check pointer receiver method
	greetMethod := entities[2]
	assert.Equal(t, "Greet", greetMethod.Name)
	assert.Equal(t, knowledge.EntityKindMethod, greetMethod.Kind)
	assert.NotEmpty(t, greetMethod.ParentID)
	assert.Contains(t, greetMethod.Signature, "func (u *User) Greet")

	// Check value receiver method
	getAgeMethod := entities[3]
	assert.Equal(t, "GetAge", getAgeMethod.Name)
	assert.Equal(t, knowledge.EntityKindMethod, getAgeMethod.Kind)
}

func TestGoExtractor_Interface(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package io

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Writer interface {
	Write(p []byte) (n int, err error)
}

type ReadWriter interface {
	Reader
	Writer
}
`

	entities, err := extractor.Extract("/test/io.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 6) // package + 3 interfaces + 2 methods

	// Check Reader interface
	reader := entities[1]
	assert.Equal(t, "Reader", reader.Name)
	assert.Equal(t, knowledge.EntityKindInterface, reader.Kind)

	// Check Read method in interface
	readMethod := entities[2]
	assert.Equal(t, "Read", readMethod.Name)
	assert.Equal(t, knowledge.EntityKindMethod, readMethod.Kind)
	assert.Equal(t, reader.ID, readMethod.ParentID)
}

func TestGoExtractor_TypeAlias(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package types

type ID string

type Handler func(ctx Context) error

type StringSlice []string
`

	entities, err := extractor.Extract("/test/types.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 4) // package + 3 types

	idType := entities[1]
	assert.Equal(t, "ID", idType.Name)
	assert.Equal(t, knowledge.EntityKindType, idType.Kind)
	assert.Contains(t, idType.Signature, "type ID string")

	handlerType := entities[2]
	assert.Equal(t, "Handler", handlerType.Name)
	assert.Equal(t, knowledge.EntityKindType, handlerType.Kind)

	sliceType := entities[3]
	assert.Equal(t, "StringSlice", sliceType.Name)
	assert.Equal(t, knowledge.EntityKindType, sliceType.Kind)
}

func TestGoExtractor_VariadicFunction(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func sum(nums ...int) int {
	total := 0
	for _, n := range nums {
		total += n
	}
	return total
}
`

	entities, err := extractor.Extract("/test/variadic.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	fn := entities[1]
	assert.Equal(t, "sum", fn.Name)
	assert.Contains(t, fn.Signature, "...int")
}

func TestGoExtractor_GenericFunction(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package generic

func Map[T, U any](slice []T, f func(T) U) []U {
	result := make([]U, len(slice))
	for i, v := range slice {
		result[i] = f(v)
	}
	return result
}
`

	entities, err := extractor.Extract("/test/generic.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	fn := entities[1]
	assert.Equal(t, "Map", fn.Name)
	assert.Equal(t, knowledge.EntityKindFunction, fn.Kind)
}

func TestGoExtractor_EmptyFile(t *testing.T) {
	extractor := NewGoExtractor()

	entities, err := extractor.Extract("/test/empty.go", []byte(""))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestGoExtractor_SyntaxError(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func broken( {
	// missing closing paren
}
`

	entities, err := extractor.Extract("/test/broken.go", []byte(source))
	require.NoError(t, err) // Should not error, just return empty
	assert.Empty(t, entities)
}

func TestGoExtractor_StableIDs(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func hello() {}
`

	// Extract twice
	entities1, _ := extractor.Extract("/test/main.go", []byte(source))
	entities2, _ := extractor.Extract("/test/main.go", []byte(source))

	require.Len(t, entities1, 2)
	require.Len(t, entities2, 2)

	// IDs should be the same across extractions
	assert.Equal(t, entities1[0].ID, entities2[0].ID)
	assert.Equal(t, entities1[1].ID, entities2[1].ID)
}

// =============================================================================
// TypeScript Extractor Tests
// =============================================================================

func TestTypeScriptExtractor_BasicFunction(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `function greet(name: string): string {
	return "Hello, " + name;
}
`

	entities, err := extractor.Extract("/test/greet.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "greet", fn.Name)
	assert.Equal(t, knowledge.EntityKindFunction, fn.Kind)
	assert.Equal(t, "/test/greet.ts", fn.FilePath)
	assert.Equal(t, 1, fn.StartLine)
	assert.Contains(t, fn.Signature, "function greet")
}

func TestTypeScriptExtractor_ExportedFunction(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export function calculateSum(a: number, b: number): number {
	return a + b;
}
`

	entities, err := extractor.Extract("/test/math.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "calculateSum", fn.Name)
	assert.Contains(t, fn.Signature, "export ")
}

func TestTypeScriptExtractor_AsyncFunction(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export async function fetchData(url: string): Promise<Data> {
	const response = await fetch(url);
	return response.json();
}
`

	entities, err := extractor.Extract("/test/api.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "fetchData", fn.Name)
	assert.Contains(t, fn.Signature, "async")
}

func TestTypeScriptExtractor_Class(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export class User {
	private name: string;

	constructor(name: string) {
		this.name = name;
	}

	public greet(): string {
		return "Hello, " + this.name;
	}

	static create(name: string): User {
		return new User(name);
	}
}
`

	entities, err := extractor.Extract("/test/user.ts", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 3) // class + methods

	// Check class
	classEntity := entities[0]
	assert.Equal(t, "User", classEntity.Name)
	assert.Equal(t, knowledge.EntityKindType, classEntity.Kind)
	assert.Contains(t, classEntity.Signature, "export class User")

	// Check methods
	var greetFound, createFound bool
	for _, e := range entities[1:] {
		if e.Name == "greet" {
			greetFound = true
			assert.Equal(t, knowledge.EntityKindMethod, e.Kind)
			assert.Equal(t, classEntity.ID, e.ParentID)
		}
		if e.Name == "create" {
			createFound = true
			assert.Contains(t, e.Signature, "static")
		}
	}
	assert.True(t, greetFound, "greet method should be found")
	assert.True(t, createFound, "create method should be found")
}

func TestTypeScriptExtractor_Interface(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export interface UserConfig {
	name: string;
	age?: number;
	email: string;
}

interface InternalConfig extends UserConfig {
	secret: string;
}
`

	entities, err := extractor.Extract("/test/config.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	// Check exported interface
	userConfig := entities[0]
	assert.Equal(t, "UserConfig", userConfig.Name)
	assert.Equal(t, knowledge.EntityKindInterface, userConfig.Kind)
	assert.Contains(t, userConfig.Signature, "export interface UserConfig")

	// Check internal interface
	internalConfig := entities[1]
	assert.Equal(t, "InternalConfig", internalConfig.Name)
	assert.Equal(t, knowledge.EntityKindInterface, internalConfig.Kind)
	assert.Contains(t, internalConfig.Signature, "extends UserConfig")
}

func TestTypeScriptExtractor_TypeAlias(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export type ID = string;

type Handler<T> = (event: T) => void;

type UserStatus = "active" | "inactive" | "pending";
`

	entities, err := extractor.Extract("/test/types.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 3)

	idType := entities[0]
	assert.Equal(t, "ID", idType.Name)
	assert.Equal(t, knowledge.EntityKindType, idType.Kind)
	assert.Contains(t, idType.Signature, "export type ID")

	handlerType := entities[1]
	assert.Equal(t, "Handler", handlerType.Name)
	assert.Contains(t, handlerType.Signature, "<T>")
}

func TestTypeScriptExtractor_GenericInterface(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export interface Repository<T, ID> {
	findById(id: ID): Promise<T | null>;
	save(entity: T): Promise<T>;
	delete(id: ID): Promise<void>;
}
`

	entities, err := extractor.Extract("/test/repository.ts", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	repo := entities[0]
	assert.Equal(t, "Repository", repo.Name)
	assert.Equal(t, knowledge.EntityKindInterface, repo.Kind)
	assert.Contains(t, repo.Signature, "<T, ID>")
}

func TestTypeScriptExtractor_AbstractClass(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export abstract class BaseService {
	protected abstract execute(): void;

	public run(): void {
		this.execute();
	}
}
`

	entities, err := extractor.Extract("/test/service.ts", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 1)

	classEntity := entities[0]
	assert.Equal(t, "BaseService", classEntity.Name)
	assert.Contains(t, classEntity.Signature, "abstract class")
}

func TestTypeScriptExtractor_EmptyFile(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	entities, err := extractor.Extract("/test/empty.ts", []byte(""))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestTypeScriptExtractor_StableIDs(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `function hello() {}
`

	entities1, _ := extractor.Extract("/test/main.ts", []byte(source))
	entities2, _ := extractor.Extract("/test/main.ts", []byte(source))

	require.Len(t, entities1, 1)
	require.Len(t, entities2, 1)

	assert.Equal(t, entities1[0].ID, entities2[0].ID)
}

func TestTypeScriptExtractor_ClassExtends(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `export class AdminUser extends User implements Auditable {
	private permissions: string[];

	checkPermission(perm: string): boolean {
		return this.permissions.includes(perm);
	}
}
`

	entities, err := extractor.Extract("/test/admin.ts", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 1)

	classEntity := entities[0]
	assert.Equal(t, "AdminUser", classEntity.Name)
	assert.Contains(t, classEntity.Signature, "extends User")
	assert.Contains(t, classEntity.Signature, "implements Auditable")
}

// =============================================================================
// Python Extractor Tests
// =============================================================================

func TestPythonExtractor_BasicFunction(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `def greet(name: str) -> str:
    return f"Hello, {name}"
`

	entities, err := extractor.Extract("/test/greet.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "greet", fn.Name)
	assert.Equal(t, knowledge.EntityKindFunction, fn.Kind)
	assert.Equal(t, "/test/greet.py", fn.FilePath)
	assert.Equal(t, 1, fn.StartLine)
	assert.Contains(t, fn.Signature, "def greet(name: str)")
	assert.Contains(t, fn.Signature, "-> str")
}

func TestPythonExtractor_AsyncFunction(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `async def fetch_data(url: str) -> dict:
    async with aiohttp.ClientSession() as session:
        async with session.get(url) as response:
            return await response.json()
`

	entities, err := extractor.Extract("/test/api.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "fetch_data", fn.Name)
	assert.Contains(t, fn.Signature, "async def")
}

func TestPythonExtractor_Class(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class User:
    def __init__(self, name: str):
        self.name = name

    def greet(self) -> str:
        return f"Hello, {self.name}"

    @staticmethod
    def create(name: str) -> "User":
        return User(name)
`

	entities, err := extractor.Extract("/test/user.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 4) // class + 3 methods

	// Check class
	classEntity := entities[0]
	assert.Equal(t, "User", classEntity.Name)
	assert.Equal(t, knowledge.EntityKindType, classEntity.Kind)
	assert.Contains(t, classEntity.Signature, "class User")

	// Check methods are linked to class
	methodCount := 0
	for _, e := range entities {
		if e.Kind == knowledge.EntityKindMethod {
			methodCount++
			assert.Equal(t, classEntity.ID, e.ParentID)
		}
	}
	assert.Equal(t, 3, methodCount)
}

func TestPythonExtractor_ClassInheritance(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class AdminUser(User, Auditable):
    def __init__(self, name: str, permissions: list):
        super().__init__(name)
        self.permissions = permissions

    def check_permission(self, perm: str) -> bool:
        return perm in self.permissions
`

	entities, err := extractor.Extract("/test/admin.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 1)

	classEntity := entities[0]
	assert.Equal(t, "AdminUser", classEntity.Name)
	assert.Contains(t, classEntity.Signature, "(User, Auditable)")
}

func TestPythonExtractor_NestedClass(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class Outer:
    class Inner:
        def inner_method(self):
            pass

    def outer_method(self):
        pass
`

	entities, err := extractor.Extract("/test/nested.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 2) // At least Outer and Inner classes

	var outerFound, innerFound bool
	for _, e := range entities {
		if e.Name == "Outer" && e.Kind == knowledge.EntityKindType {
			outerFound = true
		}
		if e.Name == "Inner" && e.Kind == knowledge.EntityKindType {
			innerFound = true
		}
	}
	assert.True(t, outerFound, "Outer class should be found")
	assert.True(t, innerFound, "Inner class should be found")
}

func TestPythonExtractor_MultipleFunctions(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `def add(a: int, b: int) -> int:
    return a + b

def subtract(a: int, b: int) -> int:
    return a - b

def multiply(a: int, b: int) -> int:
    return a * b
`

	entities, err := extractor.Extract("/test/math.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 3)

	names := []string{entities[0].Name, entities[1].Name, entities[2].Name}
	assert.Contains(t, names, "add")
	assert.Contains(t, names, "subtract")
	assert.Contains(t, names, "multiply")
}

func TestPythonExtractor_DefaultArguments(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `def connect(host: str, port: int = 8080, timeout: float = 30.0) -> Connection:
    return Connection(host, port, timeout)
`

	entities, err := extractor.Extract("/test/connection.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	fn := entities[0]
	assert.Equal(t, "connect", fn.Name)
	assert.Contains(t, fn.Signature, "port: int = 8080")
}

func TestPythonExtractor_DecoratedFunction(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `@decorator
def decorated_func():
    pass

@property
def my_property(self):
    return self._value
`

	entities, err := extractor.Extract("/test/decorated.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	// Both decorated functions should be found
	names := []string{entities[0].Name, entities[1].Name}
	assert.Contains(t, names, "decorated_func")
	assert.Contains(t, names, "my_property")
}

func TestPythonExtractor_EmptyFile(t *testing.T) {
	extractor := NewPythonExtractor()

	entities, err := extractor.Extract("/test/empty.py", []byte(""))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestPythonExtractor_OnlyComments(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `# This is a comment
# Another comment
"""
A docstring at module level
"""
`

	entities, err := extractor.Extract("/test/comments.py", []byte(source))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestPythonExtractor_StableIDs(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `def hello():
    pass
`

	entities1, _ := extractor.Extract("/test/main.py", []byte(source))
	entities2, _ := extractor.Extract("/test/main.py", []byte(source))

	require.Len(t, entities1, 1)
	require.Len(t, entities2, 1)

	assert.Equal(t, entities1[0].ID, entities2[0].ID)
}

func TestPythonExtractor_AsyncMethod(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class AsyncService:
    async def fetch(self, url: str) -> dict:
        async with aiohttp.ClientSession() as session:
            return await session.get(url)

    async def process(self, data: dict) -> None:
        await self.save(data)
`

	entities, err := extractor.Extract("/test/async_service.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 3) // class + 2 async methods

	asyncMethodCount := 0
	for _, e := range entities {
		if e.Kind == knowledge.EntityKindMethod && e.Name != "__init__" {
			asyncMethodCount++
			assert.Contains(t, e.Signature, "async def")
		}
	}
	assert.Equal(t, 2, asyncMethodCount)
}

func TestPythonExtractor_FunctionEndLine(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `def multiline_func():
    x = 1
    y = 2
    z = 3
    return x + y + z

def next_func():
    pass
`

	entities, err := extractor.Extract("/test/multiline.py", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	// First function should span multiple lines
	fn1 := entities[0]
	assert.Equal(t, "multiline_func", fn1.Name)
	assert.Equal(t, 1, fn1.StartLine)
	assert.Equal(t, 5, fn1.EndLine)

	// Second function
	fn2 := entities[1]
	assert.Equal(t, "next_func", fn2.Name)
	assert.Equal(t, 7, fn2.StartLine)
}

// =============================================================================
// Entity Interface Tests
// =============================================================================

func TestEntityExtractorInterface(t *testing.T) {
	// Verify all extractors implement the interface
	var _ EntityExtractor = (*GoExtractor)(nil)
	var _ EntityExtractor = (*TypeScriptExtractor)(nil)
	var _ EntityExtractor = (*PythonExtractor)(nil)
}

func TestGenerateEntityID_Deterministic(t *testing.T) {
	id1 := generateEntityID("/path/to/file.go", "func:main")
	id2 := generateEntityID("/path/to/file.go", "func:main")
	id3 := generateEntityID("/path/to/file.go", "func:other")

	assert.Equal(t, id1, id2, "Same inputs should produce same ID")
	assert.NotEqual(t, id1, id3, "Different entity paths should produce different IDs")
}

func TestGenerateEntityID_DifferentFiles(t *testing.T) {
	id1 := generateEntityID("/path/to/file1.go", "func:main")
	id2 := generateEntityID("/path/to/file2.go", "func:main")

	assert.NotEqual(t, id1, id2, "Same entity name in different files should have different IDs")
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestGoExtractor_OnlyPackageDeclaration(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main
`

	entities, err := extractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 1)

	assert.Equal(t, "main", entities[0].Name)
	assert.Equal(t, knowledge.EntityKindPackage, entities[0].Kind)
}

func TestTypeScriptExtractor_OnlyImports(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `import { Component } from 'react';
import type { Props } from './types';
`

	entities, err := extractor.Extract("/test/imports.ts", []byte(source))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestPythonExtractor_OnlyImports(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `import os
from typing import Dict, List
from dataclasses import dataclass
`

	entities, err := extractor.Extract("/test/imports.py", []byte(source))
	require.NoError(t, err)
	assert.Empty(t, entities)
}

func TestGoExtractor_ChannelTypes(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func producer() chan int {
	ch := make(chan int)
	return ch
}

func consumer(ch <-chan int) {
	for v := range ch {
		println(v)
	}
}
`

	entities, err := extractor.Extract("/test/channels.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 3) // package + 2 functions

	producer := entities[1]
	assert.Equal(t, "producer", producer.Name)
	assert.Contains(t, producer.Signature, "chan int")

	consumer := entities[2]
	assert.Equal(t, "consumer", consumer.Name)
	assert.Contains(t, consumer.Signature, "<-chan int")
}

func TestGoExtractor_MapTypes(t *testing.T) {
	extractor := NewGoExtractor()

	source := `package main

func getConfig() map[string]interface{} {
	return make(map[string]interface{})
}
`

	entities, err := extractor.Extract("/test/maps.go", []byte(source))
	require.NoError(t, err)
	require.Len(t, entities, 2)

	fn := entities[1]
	assert.Equal(t, "getConfig", fn.Name)
	assert.Contains(t, fn.Signature, "map[string]interface{}")
}

func TestTypeScriptExtractor_PrivateMethods(t *testing.T) {
	extractor := NewTypeScriptExtractor()

	source := `class Service {
	private internalMethod(): void {
		// private implementation
	}

	public publicMethod(): void {
		this.internalMethod();
	}
}
`

	entities, err := extractor.Extract("/test/service.ts", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 3) // class + 2 methods

	var privateFound, publicFound bool
	for _, e := range entities {
		if e.Name == "internalMethod" {
			privateFound = true
			assert.Contains(t, e.Signature, "private")
		}
		if e.Name == "publicMethod" {
			publicFound = true
			assert.Contains(t, e.Signature, "public")
		}
	}
	assert.True(t, privateFound)
	assert.True(t, publicFound)
}

func TestPythonExtractor_ClassMethodVsStaticMethod(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class Factory:
    @classmethod
    def create_from_dict(cls, data: dict) -> "Factory":
        return cls(**data)

    @staticmethod
    def validate(data: dict) -> bool:
        return "name" in data
`

	entities, err := extractor.Extract("/test/factory.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 3) // class + 2 methods

	var createFound, validateFound bool
	for _, e := range entities {
		if e.Name == "create_from_dict" {
			createFound = true
		}
		if e.Name == "validate" {
			validateFound = true
		}
	}
	assert.True(t, createFound)
	assert.True(t, validateFound)
}

func TestPythonExtractor_DunderMethods(t *testing.T) {
	extractor := NewPythonExtractor()

	source := `class Container:
    def __init__(self, items: list):
        self._items = items

    def __len__(self) -> int:
        return len(self._items)

    def __iter__(self):
        return iter(self._items)

    def __getitem__(self, index: int):
        return self._items[index]
`

	entities, err := extractor.Extract("/test/container.py", []byte(source))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entities), 5) // class + 4 dunder methods

	dunderMethods := []string{"__init__", "__len__", "__iter__", "__getitem__"}
	foundMethods := make(map[string]bool)

	for _, e := range entities {
		if e.Kind == knowledge.EntityKindMethod {
			foundMethods[e.Name] = true
		}
	}

	for _, dm := range dunderMethods {
		assert.True(t, foundMethods[dm], "Should find %s method", dm)
	}
}
