package relations

import (
	"testing"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/extractors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Call Graph Extractor Tests - Go
// =============================================================================

func TestCallGraphExtractor_Go_DirectCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func helper() string {
	return "hello"
}

func main() {
	msg := helper()
	println(msg)
}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find main -> helper call
	var foundHelperCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "main" && rel.TargetEntity.Name == "helper" {
			foundHelperCall = true
			assert.Equal(t, knowledge.RelCalls, rel.RelationType)
			assert.Equal(t, 1.0, rel.Confidence)
		}
	}
	assert.True(t, foundHelperCall, "Should find main calling helper")
}

func TestCallGraphExtractor_Go_MethodCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

type Service struct{}

func (s *Service) Process() string {
	return "processed"
}

func (s *Service) Run() {
	result := s.Process()
	println(result)
}
`

	content := map[string][]byte{
		"/test/service.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/service.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Run -> Process call
	var foundProcessCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Run" && rel.TargetEntity.Name == "Process" {
			foundProcessCall = true
			assert.Equal(t, knowledge.RelCalls, rel.RelationType)
		}
	}
	assert.True(t, foundProcessCall, "Should find Run calling Process")
}

func TestCallGraphExtractor_Go_ConditionalCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func helperA() {}
func helperB() {}

func process(flag bool) {
	if flag {
		helperA()
	} else {
		helperB()
	}
}
`

	content := map[string][]byte{
		"/test/conditional.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/conditional.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find conditional calls with reduced confidence
	var foundConditionalCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" &&
			(rel.TargetEntity.Name == "helperA" || rel.TargetEntity.Name == "helperB") {
			foundConditionalCall = true
			assert.Equal(t, knowledge.RelCalls, rel.RelationType)
			// Conditional calls should have lower confidence
			assert.LessOrEqual(t, rel.Confidence, 1.0)
		}
	}
	assert.True(t, foundConditionalCall, "Should find conditional calls")
}

// TestCallGraphExtractor_Go_CallsAfterConditional verifies that calls AFTER
// a conditional block are correctly marked as direct (not conditional).
// This is the primary test for the W4C.3 bug fix.
func TestCallGraphExtractor_Go_CallsAfterConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func helperBefore() {}
func helperInside() {}
func helperAfter() {}

func process(flag bool) {
	helperBefore()
	if flag {
		helperInside()
	}
	helperAfter()
}
`

	content := map[string][]byte{
		"/test/scope.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/scope.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Track calls and their types
	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	// helperBefore should be direct (confidence 1.0)
	assert.Equal(t, 1.0, callTypes["helperBefore"], "helperBefore should be direct call (before conditional)")

	// helperInside should be conditional (confidence 0.8)
	assert.Equal(t, 0.8, callTypes["helperInside"], "helperInside should be conditional call (inside if)")

	// helperAfter should be direct (confidence 1.0) - THIS IS THE KEY BUG FIX TEST
	assert.Equal(t, 1.0, callTypes["helperAfter"], "helperAfter should be direct call (after conditional exits)")
}

// TestCallGraphExtractor_Go_NestedConditionals verifies nested conditional tracking.
func TestCallGraphExtractor_Go_NestedConditionals(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func outerCall() {}
func innerCall() {}
func afterInner() {}
func afterOuter() {}

func process(a, b bool) {
	if a {
		outerCall()
		if b {
			innerCall()
		}
		afterInner()
	}
	afterOuter()
}
`

	content := map[string][]byte{
		"/test/nested.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/nested.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	// All calls inside outer if should be conditional
	assert.Equal(t, 0.8, callTypes["outerCall"], "outerCall should be conditional (inside outer if)")
	assert.Equal(t, 0.8, callTypes["innerCall"], "innerCall should be conditional (inside nested if)")
	assert.Equal(t, 0.8, callTypes["afterInner"], "afterInner should be conditional (still inside outer if)")

	// Call after outer if should be direct
	assert.Equal(t, 1.0, callTypes["afterOuter"], "afterOuter should be direct (after all conditionals)")
}

// TestCallGraphExtractor_Go_SwitchConditional verifies switch statement tracking.
func TestCallGraphExtractor_Go_SwitchConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func beforeSwitch() {}
func inCase() {}
func afterSwitch() {}

func process(x int) {
	beforeSwitch()
	switch x {
	case 1:
		inCase()
	}
	afterSwitch()
}
`

	content := map[string][]byte{
		"/test/switch.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/switch.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["beforeSwitch"], "beforeSwitch should be direct")
	assert.Equal(t, 0.8, callTypes["inCase"], "inCase should be conditional (inside switch)")
	assert.Equal(t, 1.0, callTypes["afterSwitch"], "afterSwitch should be direct (after switch)")
}

// TestCallGraphExtractor_Go_ForLoopConditional verifies for loop tracking.
func TestCallGraphExtractor_Go_ForLoopConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func beforeLoop() {}
func inLoop() {}
func afterLoop() {}

func process() {
	beforeLoop()
	for i := 0; i < 10; i++ {
		inLoop()
	}
	afterLoop()
}
`

	content := map[string][]byte{
		"/test/forloop.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/forloop.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["beforeLoop"], "beforeLoop should be direct")
	assert.Equal(t, 0.8, callTypes["inLoop"], "inLoop should be conditional (inside for loop)")
	assert.Equal(t, 1.0, callTypes["afterLoop"], "afterLoop should be direct (after loop)")
}

// TestCallGraphExtractor_Go_RangeLoopConditional verifies range loop tracking.
func TestCallGraphExtractor_Go_RangeLoopConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func beforeRange() {}
func inRange() {}
func afterRange() {}

func process(items []int) {
	beforeRange()
	for _, item := range items {
		_ = item
		inRange()
	}
	afterRange()
}
`

	content := map[string][]byte{
		"/test/range.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/range.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["beforeRange"], "beforeRange should be direct")
	assert.Equal(t, 0.8, callTypes["inRange"], "inRange should be conditional (inside range loop)")
	assert.Equal(t, 1.0, callTypes["afterRange"], "afterRange should be direct (after range)")
}

// TestCallGraphExtractor_Go_SelectConditional verifies select statement tracking.
func TestCallGraphExtractor_Go_SelectConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func beforeSelect() {}
func inSelect() {}
func afterSelect() {}

func process(ch chan int) {
	beforeSelect()
	select {
	case <-ch:
		inSelect()
	}
	afterSelect()
}
`

	content := map[string][]byte{
		"/test/select.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/select.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["beforeSelect"], "beforeSelect should be direct")
	assert.Equal(t, 0.8, callTypes["inSelect"], "inSelect should be conditional (inside select)")
	assert.Equal(t, 1.0, callTypes["afterSelect"], "afterSelect should be direct (after select)")
}

// TestCallGraphExtractor_Go_TypeSwitchConditional verifies type switch tracking.
func TestCallGraphExtractor_Go_TypeSwitchConditional(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func beforeTypeSwitch() {}
func inTypeSwitch() {}
func afterTypeSwitch() {}

func process(x interface{}) {
	beforeTypeSwitch()
	switch x.(type) {
	case int:
		inTypeSwitch()
	}
	afterTypeSwitch()
}
`

	content := map[string][]byte{
		"/test/typeswitch.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/typeswitch.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["beforeTypeSwitch"], "beforeTypeSwitch should be direct")
	assert.Equal(t, 0.8, callTypes["inTypeSwitch"], "inTypeSwitch should be conditional (inside type switch)")
	assert.Equal(t, 1.0, callTypes["afterTypeSwitch"], "afterTypeSwitch should be direct (after type switch)")
}

// TestCallGraphExtractor_Go_MultipleConditionalBlocks verifies multiple sequential conditionals.
func TestCallGraphExtractor_Go_MultipleConditionalBlocks(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func before() {}
func inFirst() {}
func between() {}
func inSecond() {}
func after() {}

func process(a, b bool) {
	before()
	if a {
		inFirst()
	}
	between()
	if b {
		inSecond()
	}
	after()
}
`

	content := map[string][]byte{
		"/test/multiple.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/multiple.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	callTypes := make(map[string]float64)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callTypes[rel.TargetEntity.Name] = rel.Confidence
		}
	}

	assert.Equal(t, 1.0, callTypes["before"], "before should be direct")
	assert.Equal(t, 0.8, callTypes["inFirst"], "inFirst should be conditional")
	assert.Equal(t, 1.0, callTypes["between"], "between should be direct (between conditionals)")
	assert.Equal(t, 0.8, callTypes["inSecond"], "inSecond should be conditional")
	assert.Equal(t, 1.0, callTypes["after"], "after should be direct (after all conditionals)")
}

func TestCallGraphExtractor_Go_MultipleCalls(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func step1() {}
func step2() {}
func step3() {}

func workflow() {
	step1()
	step2()
	step3()
}
`

	content := map[string][]byte{
		"/test/workflow.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/workflow.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find all three calls
	callees := make(map[string]bool)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "workflow" {
			callees[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, callees["step1"], "Should find workflow calling step1")
	assert.True(t, callees["step2"], "Should find workflow calling step2")
	assert.True(t, callees["step3"], "Should find workflow calling step3")
}

// =============================================================================
// Call Graph Extractor Tests - TypeScript
// =============================================================================

func TestCallGraphExtractor_TS_DirectCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `function helper(): string {
	return "hello";
}

function main(): void {
	const msg = helper();
	console.log(msg);
}
`

	content := map[string][]byte{
		"/test/main.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/main.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find main -> helper call
	var foundHelperCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "main" && rel.TargetEntity.Name == "helper" {
			foundHelperCall = true
			assert.Equal(t, knowledge.RelCalls, rel.RelationType)
		}
	}
	assert.True(t, foundHelperCall, "Should find main calling helper")
}

func TestCallGraphExtractor_TS_AsyncCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `async function fetchData(): Promise<string> {
	return "data";
}

async function processData(): Promise<void> {
	const data = await fetchData();
	console.log(data);
}
`

	content := map[string][]byte{
		"/test/async.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/async.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find processData -> fetchData call
	var foundFetchCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "processData" && rel.TargetEntity.Name == "fetchData" {
			foundFetchCall = true
		}
	}
	assert.True(t, foundFetchCall, "Should find processData calling fetchData")
}

func TestCallGraphExtractor_TS_ClassMethodCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `class Service {
	private helper(): string {
		return "hello";
	}

	public run(): void {
		const msg = this.helper();
		console.log(msg);
	}
}
`

	content := map[string][]byte{
		"/test/service.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/service.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find run -> helper call
	var foundHelperCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "run" && rel.TargetEntity.Name == "helper" {
			foundHelperCall = true
		}
	}
	assert.True(t, foundHelperCall, "Should find run calling helper")
}

// =============================================================================
// Call Graph Extractor Tests - Python
// =============================================================================

func TestCallGraphExtractor_Py_DirectCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `def helper() -> str:
    return "hello"

def main() -> None:
    msg = helper()
    print(msg)
`

	content := map[string][]byte{
		"/test/main.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/main.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find main -> helper call
	var foundHelperCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "main" && rel.TargetEntity.Name == "helper" {
			foundHelperCall = true
		}
	}
	assert.True(t, foundHelperCall, "Should find main calling helper")
}

func TestCallGraphExtractor_Py_MethodCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `class Service:
    def helper(self) -> str:
        return "hello"

    def run(self) -> None:
        msg = self.helper()
        print(msg)
`

	content := map[string][]byte{
		"/test/service.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/service.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find run -> helper call
	var foundHelperCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "run" && rel.TargetEntity.Name == "helper" {
			foundHelperCall = true
		}
	}
	assert.True(t, foundHelperCall, "Should find run calling helper")
}

func TestCallGraphExtractor_Py_ConditionalCall(t *testing.T) {
	extractor := NewCallGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `def helper_a() -> None:
    pass

def helper_b() -> None:
    pass

def process(flag: bool) -> None:
    if flag:
        helper_a()
    else:
        helper_b()
`

	content := map[string][]byte{
		"/test/conditional.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/conditional.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find conditional calls
	callees := make(map[string]bool)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "process" {
			callees[rel.TargetEntity.Name] = true
		}
	}
	assert.True(t, callees["helper_a"] || callees["helper_b"], "Should find conditional calls")
}

// =============================================================================
// Import Graph Extractor Tests - Go
// =============================================================================

func TestImportGraphExtractor_Go_SingleImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	require.NotEmpty(t, relations, "Should find import relation")

	var foundFmtImport bool
	for _, rel := range relations {
		if rel.TargetEntity.Name == "fmt" {
			foundFmtImport = true
			assert.Equal(t, knowledge.RelImports, rel.RelationType)
		}
	}
	assert.True(t, foundFmtImport, "Should find fmt import")
}

func TestImportGraphExtractor_Go_MultipleImports(t *testing.T) {
	extractor := NewImportGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	imports := make(map[string]bool)
	for _, rel := range relations {
		if rel.RelationType == knowledge.RelImports {
			imports[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, imports["fmt"], "Should find fmt import")
	assert.True(t, imports["os"], "Should find os import")
	assert.True(t, imports["strings"], "Should find strings import")
}

func TestImportGraphExtractor_Go_AliasedImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

import (
	f "fmt"
	s "strings"
)

func main() {}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find at least 2 imports (fmt and strings)
	require.GreaterOrEqual(t, len(relations), 2, "Should find aliased imports")

	imports := make(map[string]bool)
	for _, rel := range relations {
		if rel.RelationType == knowledge.RelImports {
			imports[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, imports["fmt"], "Should find fmt import")
	assert.True(t, imports["strings"], "Should find strings import")
}

// =============================================================================
// Import Graph Extractor Tests - TypeScript
// =============================================================================

func TestImportGraphExtractor_TS_DefaultImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `import React from 'react';

function App() {
	return <div>Hello</div>;
}
`

	content := map[string][]byte{
		"/test/app.tsx": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/app.tsx", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	var foundReactImport bool
	for _, rel := range relations {
		if rel.TargetEntity.Name == "react" {
			foundReactImport = true
			assert.Equal(t, knowledge.RelImports, rel.RelationType)
		}
	}
	assert.True(t, foundReactImport, "Should find react import")
}

func TestImportGraphExtractor_TS_NamedImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `import { useState, useEffect } from 'react';

function App() {
	const [count, setCount] = useState(0);
}
`

	content := map[string][]byte{
		"/test/app.tsx": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/app.tsx", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	var foundReactImport bool
	for _, rel := range relations {
		if rel.TargetEntity.Name == "react" {
			foundReactImport = true
		}
	}
	assert.True(t, foundReactImport, "Should find react import")
}

func TestImportGraphExtractor_TS_RelativeImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `import { helper } from './utils';
import { Config } from '../config';

function main() {
	helper();
}
`

	content := map[string][]byte{
		"/test/src/main.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/src/main.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(relations), 2, "Should find both relative imports")
}

func TestImportGraphExtractor_TS_TypeImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `import type { User } from './types';
import { createUser } from './utils';

const user: User = createUser();
`

	content := map[string][]byte{
		"/test/main.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/main.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(relations), 2, "Should find type and value imports")
}

// =============================================================================
// Import Graph Extractor Tests - Python
// =============================================================================

func TestImportGraphExtractor_Py_SimpleImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `import os
import sys

def main():
    print(os.getcwd())
`

	content := map[string][]byte{
		"/test/main.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/main.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	imports := make(map[string]bool)
	for _, rel := range relations {
		if rel.RelationType == knowledge.RelImports {
			imports[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, imports["os"], "Should find os import")
	assert.True(t, imports["sys"], "Should find sys import")
}

func TestImportGraphExtractor_Py_FromImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `from typing import Dict, List, Optional
from dataclasses import dataclass

@dataclass
class User:
    name: str
`

	content := map[string][]byte{
		"/test/models.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/models.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	imports := make(map[string]bool)
	for _, rel := range relations {
		if rel.RelationType == knowledge.RelImports {
			imports[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, imports["typing"], "Should find typing import")
	assert.True(t, imports["dataclasses"], "Should find dataclasses import")
}

func TestImportGraphExtractor_Py_AliasedImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `import numpy as np
import pandas as pd

def process(data):
    return np.array(data)
`

	content := map[string][]byte{
		"/test/process.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/process.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	imports := make(map[string]bool)
	for _, rel := range relations {
		if rel.RelationType == knowledge.RelImports {
			imports[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, imports["numpy"], "Should find numpy import")
	assert.True(t, imports["pandas"], "Should find pandas import")
}

func TestImportGraphExtractor_Py_RelativeImport(t *testing.T) {
	extractor := NewImportGraphExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `from .utils import helper
from ..config import Config

def main():
    helper()
`

	content := map[string][]byte{
		"/test/pkg/main.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/pkg/main.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(relations), 2, "Should find relative imports")
}

// =============================================================================
// Type Relation Extractor Tests - Go
// =============================================================================

func TestTypeRelationExtractor_Go_StructEmbedding(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

type Base struct {
	Name string
}

type Derived struct {
	Base
	Age int
}
`

	content := map[string][]byte{
		"/test/types.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/types.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Derived embeds Base
	var foundEmbedding bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Derived" && rel.TargetEntity.Name == "Base" {
			foundEmbedding = true
			assert.Equal(t, knowledge.RelContains, rel.RelationType)
		}
	}
	assert.True(t, foundEmbedding, "Should find struct embedding")
}

func TestTypeRelationExtractor_Go_InterfaceEmbedding(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

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

	content := map[string][]byte{
		"/test/io.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/io.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find ReadWriter extends Reader and Writer
	embeddedInterfaces := make(map[string]bool)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "ReadWriter" && rel.RelationType == knowledge.RelExtends {
			embeddedInterfaces[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, embeddedInterfaces["Reader"], "Should find Reader embedding")
	assert.True(t, embeddedInterfaces["Writer"], "Should find Writer embedding")
}

func TestTypeRelationExtractor_Go_FieldTypeUsage(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

type Config struct {
	Name string
}

type Service struct {
	config *Config
	logger Logger
}
`

	content := map[string][]byte{
		"/test/service.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/service.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Service uses Config
	var foundUsage bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Service" && rel.TargetEntity.Name == "Config" {
			foundUsage = true
			assert.Equal(t, knowledge.RelUses, rel.RelationType)
		}
	}
	assert.True(t, foundUsage, "Should find type usage in field")
}

// =============================================================================
// Type Relation Extractor Tests - TypeScript
// =============================================================================

func TestTypeRelationExtractor_TS_ClassExtends(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `class Animal {
	name: string;
}

class Dog extends Animal {
	bark(): void {}
}
`

	content := map[string][]byte{
		"/test/animals.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/animals.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Dog extends Animal
	var foundExtends bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Dog" && rel.TargetEntity.Name == "Animal" {
			foundExtends = true
			assert.Equal(t, knowledge.RelExtends, rel.RelationType)
		}
	}
	assert.True(t, foundExtends, "Should find class extends")
}

func TestTypeRelationExtractor_TS_ClassImplements(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `interface Runnable {
	run(): void;
}

interface Stoppable {
	stop(): void;
}

class Service implements Runnable, Stoppable {
	run(): void {}
	stop(): void {}
}
`

	content := map[string][]byte{
		"/test/service.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/service.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Service implements Runnable and Stoppable
	implements := make(map[string]bool)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Service" && rel.RelationType == knowledge.RelImplements {
			implements[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, implements["Runnable"], "Should find Runnable implementation")
	assert.True(t, implements["Stoppable"], "Should find Stoppable implementation")
}

func TestTypeRelationExtractor_TS_InterfaceExtends(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `interface Base {
	id: string;
}

interface Extended extends Base {
	name: string;
}
`

	content := map[string][]byte{
		"/test/interfaces.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/interfaces.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Extended extends Base
	var foundExtends bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Extended" && rel.TargetEntity.Name == "Base" {
			foundExtends = true
			assert.Equal(t, knowledge.RelExtends, rel.RelationType)
		}
	}
	assert.True(t, foundExtends, "Should find interface extends")
}

func TestTypeRelationExtractor_TS_ExtendsAndImplements(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	tsExtractor := extractors.NewTypeScriptExtractor()

	source := `class Base {
	init(): void {}
}

interface Loggable {
	log(): void;
}

class Derived extends Base implements Loggable {
	log(): void {}
}
`

	content := map[string][]byte{
		"/test/combined.ts": []byte(source),
	}

	entities, err := tsExtractor.Extract("/test/combined.ts", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find both extends and implements
	var foundExtends, foundImplements bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Derived" {
			if rel.TargetEntity.Name == "Base" && rel.RelationType == knowledge.RelExtends {
				foundExtends = true
			}
			if rel.TargetEntity.Name == "Loggable" && rel.RelationType == knowledge.RelImplements {
				foundImplements = true
			}
		}
	}

	assert.True(t, foundExtends, "Should find extends")
	assert.True(t, foundImplements, "Should find implements")
}

// =============================================================================
// Type Relation Extractor Tests - Python
// =============================================================================

func TestTypeRelationExtractor_Py_SingleInheritance(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `class Animal:
    def speak(self):
        pass

class Dog(Animal):
    def speak(self):
        return "Woof!"
`

	content := map[string][]byte{
		"/test/animals.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/animals.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Dog extends Animal
	var foundExtends bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Dog" && rel.TargetEntity.Name == "Animal" {
			foundExtends = true
			assert.Equal(t, knowledge.RelExtends, rel.RelationType)
		}
	}
	assert.True(t, foundExtends, "Should find class inheritance")
}

func TestTypeRelationExtractor_Py_MultipleInheritance(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `class Flyable:
    def fly(self):
        pass

class Swimmable:
    def swim(self):
        pass

class Duck(Flyable, Swimmable):
    pass
`

	content := map[string][]byte{
		"/test/duck.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/duck.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find Duck extends both Flyable and Swimmable
	parents := make(map[string]bool)
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Duck" && rel.RelationType == knowledge.RelExtends {
			parents[rel.TargetEntity.Name] = true
		}
	}

	assert.True(t, parents["Flyable"], "Should find Flyable inheritance")
	assert.True(t, parents["Swimmable"], "Should find Swimmable inheritance")
}

func TestTypeRelationExtractor_Py_GenericInheritance(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	pyExtractor := extractors.NewPythonExtractor()

	source := `from typing import Generic, TypeVar

T = TypeVar('T')

class Container(Generic[T]):
    def get(self) -> T:
        pass

class StringContainer(Container[str]):
    pass
`

	content := map[string][]byte{
		"/test/generics.py": []byte(source),
	}

	entities, err := pyExtractor.Extract("/test/generics.py", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	// Should find StringContainer extends Container
	var foundExtends bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "StringContainer" && rel.TargetEntity.Name == "Container" {
			foundExtends = true
		}
	}
	assert.True(t, foundExtends, "Should find generic class inheritance")
}

// =============================================================================
// Cross-File Relationship Tests
// =============================================================================

func TestCallGraphExtractor_CrossFile(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	sourceUtils := `package main

func Helper() string {
	return "hello"
}
`

	sourceMain := `package main

func Main() {
	msg := Helper()
	println(msg)
}
`

	content := map[string][]byte{
		"/test/utils.go": []byte(sourceUtils),
		"/test/main.go":  []byte(sourceMain),
	}

	// Extract entities from both files
	var allEntities []extractors.Entity

	entitiesUtils, err := goExtractor.Extract("/test/utils.go", []byte(sourceUtils))
	require.NoError(t, err)
	allEntities = append(allEntities, entitiesUtils...)

	entitiesMain, err := goExtractor.Extract("/test/main.go", []byte(sourceMain))
	require.NoError(t, err)
	allEntities = append(allEntities, entitiesMain...)

	relations, err := extractor.ExtractRelations(allEntities, content)
	require.NoError(t, err)

	// Should find Main -> Helper cross-file call
	var foundCrossFileCall bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Main" && rel.TargetEntity.Name == "Helper" {
			foundCrossFileCall = true
			// Verify cross-file reference
			assert.NotEqual(t, rel.SourceEntity.FilePath, rel.TargetEntity.FilePath)
		}
	}
	assert.True(t, foundCrossFileCall, "Should find cross-file call")
}

func TestTypeRelationExtractor_CrossFile(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	sourceBase := `package main

type Base struct {
	ID string
}
`

	sourceDerived := `package main

type Derived struct {
	Base
	Name string
}
`

	content := map[string][]byte{
		"/test/base.go":    []byte(sourceBase),
		"/test/derived.go": []byte(sourceDerived),
	}

	// Extract entities from both files
	var allEntities []extractors.Entity

	entitiesBase, err := goExtractor.Extract("/test/base.go", []byte(sourceBase))
	require.NoError(t, err)
	allEntities = append(allEntities, entitiesBase...)

	entitiesDerived, err := goExtractor.Extract("/test/derived.go", []byte(sourceDerived))
	require.NoError(t, err)
	allEntities = append(allEntities, entitiesDerived...)

	relations, err := extractor.ExtractRelations(allEntities, content)
	require.NoError(t, err)

	// Should find Derived embeds Base
	var foundCrossFileRelation bool
	for _, rel := range relations {
		if rel.SourceEntity.Name == "Derived" && rel.TargetEntity.Name == "Base" {
			foundCrossFileRelation = true
		}
	}
	assert.True(t, foundCrossFileRelation, "Should find cross-file type relation")
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCallGraphExtractor_EmptyFile(t *testing.T) {
	extractor := NewCallGraphExtractor()

	content := map[string][]byte{
		"/test/empty.go": []byte(""),
	}

	relations, err := extractor.ExtractRelations(nil, content)
	require.NoError(t, err)
	assert.Empty(t, relations)
}

func TestImportGraphExtractor_EmptyFile(t *testing.T) {
	extractor := NewImportGraphExtractor()

	content := map[string][]byte{
		"/test/empty.go": []byte(""),
	}

	relations, err := extractor.ExtractRelations(nil, content)
	require.NoError(t, err)
	assert.Empty(t, relations)
}

func TestTypeRelationExtractor_EmptyFile(t *testing.T) {
	extractor := NewTypeRelationExtractor()

	content := map[string][]byte{
		"/test/empty.go": []byte(""),
	}

	relations, err := extractor.ExtractRelations(nil, content)
	require.NoError(t, err)
	assert.Empty(t, relations)
}

func TestCallGraphExtractor_NoFunctions(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

type Config struct {
	Name string
}

var globalVar = "hello"
`

	content := map[string][]byte{
		"/test/types.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/types.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)
	assert.Empty(t, relations, "Should have no call relations without functions")
}

func TestTypeRelationExtractor_NoTypes(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func main() {
	println("hello")
}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)
	assert.Empty(t, relations, "Should have no type relations without types")
}

// =============================================================================
// Stable ID Tests
// =============================================================================

func TestRelationID_Stability(t *testing.T) {
	id1 := generateRelationID("source1", "target1", "calls")
	id2 := generateRelationID("source1", "target1", "calls")
	id3 := generateRelationID("source1", "target2", "calls")

	assert.Equal(t, id1, id2, "Same inputs should produce same ID")
	assert.NotEqual(t, id1, id3, "Different targets should produce different IDs")
}

func TestCallType_String(t *testing.T) {
	assert.Equal(t, "direct", CallTypeDirect.String())
	assert.Equal(t, "indirect", CallTypeIndirect.String())
	assert.Equal(t, "conditional", CallTypeConditional.String())
}

func TestCallType_Parse(t *testing.T) {
	ct, ok := ParseCallType("direct")
	assert.True(t, ok)
	assert.Equal(t, CallTypeDirect, ct)

	ct, ok = ParseCallType("indirect")
	assert.True(t, ok)
	assert.Equal(t, CallTypeIndirect, ct)

	_, ok = ParseCallType("invalid")
	assert.False(t, ok)
}

func TestImportType_String(t *testing.T) {
	assert.Equal(t, "standard", ImportTypeStandard.String())
	assert.Equal(t, "relative", ImportTypeRelative.String())
	assert.Equal(t, "package", ImportTypePackage.String())
	assert.Equal(t, "alias", ImportTypeAlias.String())
	assert.Equal(t, "selective", ImportTypeSelective.String())
}

func TestImportType_Parse(t *testing.T) {
	it, ok := ParseImportType("relative")
	assert.True(t, ok)
	assert.Equal(t, ImportTypeRelative, it)

	_, ok = ParseImportType("invalid")
	assert.False(t, ok)
}

func TestTypeRelationType_String(t *testing.T) {
	assert.Equal(t, "extends", TypeRelationExtends.String())
	assert.Equal(t, "implements", TypeRelationImplements.String())
	assert.Equal(t, "embeds", TypeRelationEmbeds.String())
	assert.Equal(t, "uses", TypeRelationUses.String())
}

func TestTypeRelationType_Parse(t *testing.T) {
	trt, ok := ParseTypeRelationType("implements")
	assert.True(t, ok)
	assert.Equal(t, TypeRelationImplements, trt)

	_, ok = ParseTypeRelationType("invalid")
	assert.False(t, ok)
}

// =============================================================================
// Evidence Snippet Tests
// =============================================================================

func TestCallGraphExtractor_EvidenceSnippet(t *testing.T) {
	extractor := NewCallGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

func helper() string {
	return "hello"
}

func main() {
	msg := helper()
	println(msg)
}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	for _, rel := range relations {
		if rel.SourceEntity.Name == "main" && rel.TargetEntity.Name == "helper" {
			require.NotEmpty(t, rel.Evidence)
			assert.Contains(t, rel.Evidence[0].Snippet, "helper")
			assert.Greater(t, rel.Evidence[0].StartLine, 0)
		}
	}
}

func TestImportGraphExtractor_EvidenceSnippet(t *testing.T) {
	extractor := NewImportGraphExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`

	content := map[string][]byte{
		"/test/main.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/main.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	for _, rel := range relations {
		if rel.TargetEntity.Name == "fmt" {
			require.NotEmpty(t, rel.Evidence)
			assert.Contains(t, rel.Evidence[0].Snippet, "fmt")
		}
	}
}

func TestTypeRelationExtractor_EvidenceSnippet(t *testing.T) {
	extractor := NewTypeRelationExtractor()
	goExtractor := extractors.NewGoExtractor()

	source := `package main

type Base struct{}

type Derived struct {
	Base
}
`

	content := map[string][]byte{
		"/test/types.go": []byte(source),
	}

	entities, err := goExtractor.Extract("/test/types.go", []byte(source))
	require.NoError(t, err)

	relations, err := extractor.ExtractRelations(entities, content)
	require.NoError(t, err)

	for _, rel := range relations {
		if rel.SourceEntity.Name == "Derived" && rel.TargetEntity.Name == "Base" {
			require.NotEmpty(t, rel.Evidence)
			assert.Greater(t, rel.Evidence[0].StartLine, 0)
		}
	}
}
