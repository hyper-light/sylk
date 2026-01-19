package knowledge

import (
	"strings"
	"sync"
	"testing"
)

// =============================================================================
// PipelineStage Tests
// =============================================================================

func TestPipelineStage_String(t *testing.T) {
	tests := []struct {
		stage    PipelineStage
		expected string
	}{
		{StageEntityExtraction, "entity_extraction"},
		{StageRelationExtraction, "relation_extraction"},
		{StageEntityLinking, "entity_linking"},
		{StageRelationValidation, "relation_validation"},
		{PipelineStage(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.stage.String(); got != tt.expected {
			t.Errorf("PipelineStage(%d).String() = %q, expected %q", tt.stage, got, tt.expected)
		}
	}
}

func TestParsePipelineStage(t *testing.T) {
	tests := []struct {
		input    string
		expected PipelineStage
		ok       bool
	}{
		{"entity_extraction", StageEntityExtraction, true},
		{"relation_extraction", StageRelationExtraction, true},
		{"entity_linking", StageEntityLinking, true},
		{"relation_validation", StageRelationValidation, true},
		{"invalid", StageEntityExtraction, false},
		{"", StageEntityExtraction, false},
	}

	for _, tt := range tests {
		got, ok := ParsePipelineStage(tt.input)
		if ok != tt.ok {
			t.Errorf("ParsePipelineStage(%q) ok = %v, expected %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParsePipelineStage(%q) = %v, expected %v", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// PipelineConfig Tests
// =============================================================================

func TestDefaultPipelineConfig(t *testing.T) {
	config := DefaultPipelineConfig()

	// All stages should be enabled by default
	if !config.EnabledStages[StageEntityExtraction] {
		t.Error("Expected StageEntityExtraction to be enabled by default")
	}
	if !config.EnabledStages[StageRelationExtraction] {
		t.Error("Expected StageRelationExtraction to be enabled by default")
	}
	if !config.EnabledStages[StageEntityLinking] {
		t.Error("Expected StageEntityLinking to be enabled by default")
	}
	if !config.EnabledStages[StageRelationValidation] {
		t.Error("Expected StageRelationValidation to be enabled by default")
	}

	// Parallel extraction should be enabled
	if !config.ParallelExtraction {
		t.Error("Expected ParallelExtraction to be enabled by default")
	}

	// MaxWorkers should be positive
	if config.MaxWorkers <= 0 {
		t.Errorf("Expected MaxWorkers > 0, got %d", config.MaxWorkers)
	}

	// Continue on error should be enabled
	if !config.ContinueOnError {
		t.Error("Expected ContinueOnError to be enabled by default")
	}

	// Deduplication should be enabled
	if !config.DeduplicateEntities {
		t.Error("Expected DeduplicateEntities to be enabled by default")
	}
	if !config.DeduplicateRelations {
		t.Error("Expected DeduplicateRelations to be enabled by default")
	}
}

// =============================================================================
// ExtractionPipeline Creation Tests
// =============================================================================

func TestNewExtractionPipeline(t *testing.T) {
	pipeline := NewExtractionPipeline()

	if pipeline == nil {
		t.Fatal("NewExtractionPipeline returned nil")
	}

	// Should have default extractors registered
	if !pipeline.HasExtractor("go") {
		t.Error("Expected Go extractor to be registered")
	}
	if !pipeline.HasExtractor("typescript") {
		t.Error("Expected TypeScript extractor to be registered")
	}
	if !pipeline.HasExtractor("javascript") {
		t.Error("Expected JavaScript extractor to be registered")
	}
	if !pipeline.HasExtractor("python") {
		t.Error("Expected Python extractor to be registered")
	}
}

func TestNewExtractionPipelineWithConfig(t *testing.T) {
	config := DefaultPipelineConfig()
	config.MaxWorkers = 8
	config.MinConfidence = 0.5

	pipeline := NewExtractionPipelineWithConfig(config)

	if pipeline == nil {
		t.Fatal("NewExtractionPipelineWithConfig returned nil")
	}

	gotConfig := pipeline.GetConfig()
	if gotConfig.MaxWorkers != 8 {
		t.Errorf("Expected MaxWorkers 8, got %d", gotConfig.MaxWorkers)
	}
	if gotConfig.MinConfidence != 0.5 {
		t.Errorf("Expected MinConfidence 0.5, got %f", gotConfig.MinConfidence)
	}
}

func TestExtractionPipeline_RegisterExtractor(t *testing.T) {
	pipeline := NewExtractionPipeline()

	// Create a mock extractor
	mockExtractor := &mockEntityExtractor{}

	pipeline.RegisterExtractor("rust", mockExtractor)

	if !pipeline.HasExtractor("rust") {
		t.Error("Expected Rust extractor to be registered")
	}

	// Should be case-insensitive
	if !pipeline.HasExtractor("RUST") {
		t.Error("Expected extractor lookup to be case-insensitive")
	}
}

func TestExtractionPipeline_SupportedLanguages(t *testing.T) {
	pipeline := NewExtractionPipeline()

	languages := pipeline.SupportedLanguages()

	expectedLangs := []string{"go", "typescript", "javascript", "python"}
	for _, lang := range expectedLangs {
		found := false
		for _, l := range languages {
			if l == lang {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected language %q to be in supported languages", lang)
		}
	}
}

func TestExtractionPipeline_SetConfig(t *testing.T) {
	pipeline := NewExtractionPipeline()

	newConfig := DefaultPipelineConfig()
	newConfig.ParallelExtraction = false
	newConfig.MaxWorkers = 1

	pipeline.SetConfig(newConfig)

	gotConfig := pipeline.GetConfig()
	if gotConfig.ParallelExtraction {
		t.Error("Expected ParallelExtraction to be false after SetConfig")
	}
	if gotConfig.MaxWorkers != 1 {
		t.Errorf("Expected MaxWorkers 1, got %d", gotConfig.MaxWorkers)
	}
}

// =============================================================================
// Language Detection Tests
// =============================================================================

func TestExtractionPipeline_DetectLanguage(t *testing.T) {
	pipeline := NewExtractionPipeline()

	tests := []struct {
		filePath string
		expected string
	}{
		{"/path/to/file.go", "go"},
		{"/path/to/file.ts", "typescript"},
		{"/path/to/file.tsx", "typescript"},
		{"/path/to/file.js", "javascript"},
		{"/path/to/file.jsx", "javascript"},
		{"/path/to/file.mjs", "javascript"},
		{"/path/to/file.cjs", "javascript"},
		{"/path/to/file.py", "python"},
		{"/path/to/file.pyw", "python"},
		{"/path/to/file.rs", ""},    // Not supported
		{"/path/to/file.java", ""},  // Not supported
		{"/path/to/file.txt", ""},   // Not a code file
		{"/path/to/file", ""},       // No extension
	}

	for _, tt := range tests {
		// Access through Extract to test internal detectLanguage
		files := map[string][]byte{tt.filePath: []byte("")}
		result := pipeline.Extract(files)

		if tt.expected == "" {
			// Should have no entities for unsupported files
			if len(result.Entities) != 0 {
				t.Errorf("Expected no entities for %q, got %d", tt.filePath, len(result.Entities))
			}
		}
	}
}

// =============================================================================
// Full Extraction Pipeline Tests
// =============================================================================

func TestExtractionPipeline_ExtractGoFile(t *testing.T) {
	pipeline := NewExtractionPipeline()

	goCode := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
	helper()
}

func helper() {
	fmt.Println("Helper")
}

type MyStruct struct {
	Name string
}

func (m *MyStruct) Method() string {
	return m.Name
}
`

	files := map[string][]byte{
		"/test/main.go": []byte(goCode),
	}

	result := pipeline.Extract(files)

	// Should have entities
	if len(result.Entities) == 0 {
		t.Fatal("Expected entities to be extracted from Go file")
	}

	// Check for specific entities
	foundMain := false
	foundHelper := false
	foundStruct := false
	foundMethod := false
	foundPackage := false

	for _, entity := range result.Entities {
		switch entity.Name {
		case "main":
			if entity.Kind == EntityKindFunction {
				foundMain = true
			} else if entity.Kind == EntityKindPackage {
				foundPackage = true
			}
		case "helper":
			foundHelper = true
		case "MyStruct":
			foundStruct = true
		case "Method":
			foundMethod = true
		}
	}

	if !foundMain {
		t.Error("Expected to find main function")
	}
	if !foundHelper {
		t.Error("Expected to find helper function")
	}
	if !foundStruct {
		t.Error("Expected to find MyStruct")
	}
	if !foundMethod {
		t.Error("Expected to find Method")
	}
	if !foundPackage {
		t.Error("Expected to find package")
	}

	// Should have file metrics
	if len(result.FileMetrics) != 1 {
		t.Errorf("Expected 1 file metric, got %d", len(result.FileMetrics))
	}

	// Check file metric
	if result.FileMetrics[0].FilePath != "/test/main.go" {
		t.Errorf("Expected file path '/test/main.go', got %q", result.FileMetrics[0].FilePath)
	}
	if result.FileMetrics[0].Language != "go" {
		t.Errorf("Expected language 'go', got %q", result.FileMetrics[0].Language)
	}
}

func TestExtractionPipeline_ExtractTypeScriptFile(t *testing.T) {
	pipeline := NewExtractionPipeline()

	tsCode := `
export function greet(name: string): string {
	return "Hello, " + name;
}

export class Person {
	name: string;

	constructor(name: string) {
		this.name = name;
	}

	sayHello(): string {
		return greet(this.name);
	}
}

export interface Greeter {
	greet(name: string): string;
}

export type GreetFunction = (name: string) => string;
`

	files := map[string][]byte{
		"/test/app.ts": []byte(tsCode),
	}

	result := pipeline.Extract(files)

	// Should have entities
	if len(result.Entities) == 0 {
		t.Fatal("Expected entities to be extracted from TypeScript file")
	}

	// Check for specific entities
	foundGreet := false
	foundPerson := false
	foundGreeter := false
	foundGreetFunction := false

	for _, entity := range result.Entities {
		switch entity.Name {
		case "greet":
			foundGreet = true
		case "Person":
			foundPerson = true
		case "Greeter":
			foundGreeter = true
		case "GreetFunction":
			foundGreetFunction = true
		}
	}

	if !foundGreet {
		t.Error("Expected to find greet function")
	}
	if !foundPerson {
		t.Error("Expected to find Person class")
	}
	if !foundGreeter {
		t.Error("Expected to find Greeter interface")
	}
	if !foundGreetFunction {
		t.Error("Expected to find GreetFunction type")
	}
}

func TestExtractionPipeline_ExtractPythonFile(t *testing.T) {
	pipeline := NewExtractionPipeline()

	pyCode := `
def greet(name):
    return f"Hello, {name}"

async def async_greet(name):
    return await fetch_greeting(name)

class Person:
    def __init__(self, name):
        self.name = name

    def say_hello(self):
        return greet(self.name)

    async def async_hello(self):
        return await async_greet(self.name)
`

	files := map[string][]byte{
		"/test/app.py": []byte(pyCode),
	}

	result := pipeline.Extract(files)

	// Should have entities
	if len(result.Entities) == 0 {
		t.Fatal("Expected entities to be extracted from Python file")
	}

	// Check for specific entities
	foundGreet := false
	foundAsyncGreet := false
	foundPerson := false
	foundInit := false
	foundSayHello := false

	for _, entity := range result.Entities {
		switch entity.Name {
		case "greet":
			foundGreet = true
		case "async_greet":
			foundAsyncGreet = true
		case "Person":
			foundPerson = true
		case "__init__":
			foundInit = true
		case "say_hello":
			foundSayHello = true
		}
	}

	if !foundGreet {
		t.Error("Expected to find greet function")
	}
	if !foundAsyncGreet {
		t.Error("Expected to find async_greet function")
	}
	if !foundPerson {
		t.Error("Expected to find Person class")
	}
	if !foundInit {
		t.Error("Expected to find __init__ method")
	}
	if !foundSayHello {
		t.Error("Expected to find say_hello method")
	}
}

// =============================================================================
// Multi-Language Project Tests
// =============================================================================

func TestExtractionPipeline_MultiLanguageProject(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/project/main.go": []byte(`package main

func main() {
	runServer()
}

func runServer() {
	// Start server
}
`),
		"/project/frontend/app.ts": []byte(`
export function initApp(): void {
	console.log("App initialized");
}

export class App {
	init(): void {
		initApp();
	}
}
`),
		"/project/scripts/deploy.py": []byte(`
def deploy():
    print("Deploying...")

def cleanup():
    print("Cleaning up...")
`),
	}

	result := pipeline.Extract(files)

	// Should have entities from all languages
	if len(result.Entities) == 0 {
		t.Fatal("Expected entities from multi-language project")
	}

	// Count entities by file
	entitiesByFile := make(map[string]int)
	for _, entity := range result.Entities {
		entitiesByFile[entity.FilePath]++
	}

	if entitiesByFile["/project/main.go"] == 0 {
		t.Error("Expected entities from Go file")
	}
	if entitiesByFile["/project/frontend/app.ts"] == 0 {
		t.Error("Expected entities from TypeScript file")
	}
	if entitiesByFile["/project/scripts/deploy.py"] == 0 {
		t.Error("Expected entities from Python file")
	}

	// Should have file metrics for each file
	if len(result.FileMetrics) != 3 {
		t.Errorf("Expected 3 file metrics, got %d", len(result.FileMetrics))
	}
}

// =============================================================================
// Incremental Extraction Tests
// =============================================================================

func TestExtractionPipeline_ExtractIncremental(t *testing.T) {
	pipeline := NewExtractionPipeline()

	// Initial extraction
	initialFiles := map[string][]byte{
		"/project/main.go": []byte(`package main

func main() {}
func helper() {}
`),
		"/project/utils.go": []byte(`package main

func utility() {}
`),
	}

	initialResult := pipeline.Extract(initialFiles)
	initialEntityCount := len(initialResult.Entities)

	if initialEntityCount == 0 {
		t.Fatal("Expected entities from initial extraction")
	}

	// Incremental extraction with changed file
	changedFiles := map[string][]byte{
		"/project/main.go": []byte(`package main

func main() {}
func helper() {}
func newFunction() {}
`),
	}

	incrementalResult := pipeline.ExtractIncremental(changedFiles, initialResult.Entities)

	// Should have more entities (newFunction added)
	if len(incrementalResult.Entities) <= initialEntityCount {
		t.Errorf("Expected more entities after incremental extraction, initial: %d, after: %d",
			initialEntityCount, len(incrementalResult.Entities))
	}

	// Should find the new function
	foundNew := false
	for _, entity := range incrementalResult.Entities {
		if entity.Name == "newFunction" {
			foundNew = true
			break
		}
	}

	if !foundNew {
		t.Error("Expected to find newFunction in incremental result")
	}

	// Should still have utility from unchanged file
	foundUtility := false
	for _, entity := range incrementalResult.Entities {
		if entity.Name == "utility" {
			foundUtility = true
			break
		}
	}

	if !foundUtility {
		t.Error("Expected to preserve utility from unchanged file")
	}
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestExtractionPipeline_ContinueOnError(t *testing.T) {
	config := DefaultPipelineConfig()
	config.ContinueOnError = true
	pipeline := NewExtractionPipelineWithConfig(config)

	files := map[string][]byte{
		"/test/valid.go": []byte(`package main
func valid() {}
`),
		"/test/invalid.go": []byte(`this is not valid go code {{{`),
	}

	result := pipeline.Extract(files)

	// Should still have entities from valid file
	foundValid := false
	for _, entity := range result.Entities {
		if entity.Name == "valid" {
			foundValid = true
			break
		}
	}

	if !foundValid {
		t.Error("Expected to find valid function despite invalid file")
	}

	// Should have file metrics for both files
	if len(result.FileMetrics) != 2 {
		t.Errorf("Expected 2 file metrics, got %d", len(result.FileMetrics))
	}
}

func TestExtractionPipeline_EmptyFiles(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/empty.go": []byte(""),
	}

	result := pipeline.Extract(files)

	// Should not crash on empty files
	if result.TotalDuration == 0 {
		t.Error("Expected non-zero duration")
	}
}

func TestExtractionPipeline_UnsupportedLanguage(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/file.rs": []byte(`fn main() {}`),
	}

	result := pipeline.Extract(files)

	// Should not have entities for unsupported language
	if len(result.Entities) != 0 {
		t.Errorf("Expected no entities for unsupported language, got %d", len(result.Entities))
	}

	// Should have no errors (unsupported languages are silently skipped)
	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors for unsupported language, got %d", len(result.Errors))
	}
}

// =============================================================================
// Deduplication Tests
// =============================================================================

func TestExtractionPipeline_DeduplicateEntities(t *testing.T) {
	config := DefaultPipelineConfig()
	config.DeduplicateEntities = true
	pipeline := NewExtractionPipelineWithConfig(config)

	// Go file with duplicate-looking content
	goCode := `package main

func main() {}
`

	files := map[string][]byte{
		"/test/main.go": []byte(goCode),
	}

	result := pipeline.Extract(files)

	// Count entities with same name
	nameCounts := make(map[string]int)
	for _, entity := range result.Entities {
		key := entity.FilePath + ":" + entity.Name + ":" + entity.Kind.String()
		nameCounts[key]++
	}

	// Should not have duplicates
	for key, count := range nameCounts {
		if count > 1 {
			t.Errorf("Found duplicate entity: %s (count: %d)", key, count)
		}
	}
}

func TestExtractionPipeline_DeduplicateRelations(t *testing.T) {
	config := DefaultPipelineConfig()
	config.DeduplicateRelations = true
	pipeline := NewExtractionPipelineWithConfig(config)

	goCode := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
}
`

	files := map[string][]byte{
		"/test/main.go": []byte(goCode),
	}

	result := pipeline.Extract(files)

	// Count relations between same entities
	relationCounts := make(map[string]int)
	for _, relation := range result.Relations {
		if relation.SourceEntity == nil || relation.TargetEntity == nil {
			continue
		}
		key := relation.SourceEntity.Name + "->" + relation.RelationType.String() + "->" + relation.TargetEntity.Name
		relationCounts[key]++
	}

	// Should not have duplicate relations
	for key, count := range relationCounts {
		if count > 1 {
			t.Errorf("Found duplicate relation: %s (count: %d)", key, count)
		}
	}
}

// =============================================================================
// Language Filter Tests
// =============================================================================

func TestExtractionPipeline_LanguageFilters(t *testing.T) {
	config := DefaultPipelineConfig()
	config.LanguageFilters = []string{"go"} // Only process Go files
	pipeline := NewExtractionPipelineWithConfig(config)

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
		"/test/app.ts": []byte(`export function app() {}`),
		"/test/script.py": []byte(`def script(): pass`),
	}

	result := pipeline.Extract(files)

	// Should only have entities from Go file
	for _, entity := range result.Entities {
		if !strings.HasSuffix(entity.FilePath, ".go") {
			t.Errorf("Expected only Go entities, found entity from %s", entity.FilePath)
		}
	}
}

// =============================================================================
// Parallel Extraction Tests
// =============================================================================

func TestExtractionPipeline_ParallelExtraction(t *testing.T) {
	config := DefaultPipelineConfig()
	config.ParallelExtraction = true
	config.MaxWorkers = 4
	pipeline := NewExtractionPipelineWithConfig(config)

	// Create multiple files
	files := make(map[string][]byte)
	for i := 0; i < 10; i++ {
		files["/test/file"+string(rune('0'+i))+".go"] = []byte(`package main
func func` + string(rune('0'+i)) + `() {}
`)
	}

	result := pipeline.Extract(files)

	// Should have entities from all files
	if len(result.Entities) < 10 {
		t.Errorf("Expected at least 10 entities, got %d", len(result.Entities))
	}

	// Should have metrics for all files
	if len(result.FileMetrics) != 10 {
		t.Errorf("Expected 10 file metrics, got %d", len(result.FileMetrics))
	}
}

func TestExtractionPipeline_SequentialExtraction(t *testing.T) {
	config := DefaultPipelineConfig()
	config.ParallelExtraction = false
	pipeline := NewExtractionPipelineWithConfig(config)

	files := make(map[string][]byte)
	for i := 0; i < 5; i++ {
		files["/test/file"+string(rune('0'+i))+".go"] = []byte(`package main
func func` + string(rune('0'+i)) + `() {}
`)
	}

	result := pipeline.Extract(files)

	// Should have entities from all files
	if len(result.Entities) < 5 {
		t.Errorf("Expected at least 5 entities, got %d", len(result.Entities))
	}
}

// =============================================================================
// Progress Callback Tests
// =============================================================================

func TestExtractionPipeline_ProgressCallback(t *testing.T) {
	pipeline := NewExtractionPipeline()

	var progressCalls []ProgressInfo
	var mu sync.Mutex

	pipeline.SetProgressCallback(func(info ProgressInfo) {
		mu.Lock()
		progressCalls = append(progressCalls, info)
		mu.Unlock()
	})

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	pipeline.Extract(files)

	mu.Lock()
	defer mu.Unlock()

	// Should have received progress callbacks
	if len(progressCalls) == 0 {
		t.Error("Expected progress callbacks to be called")
	}

	// Check for entity extraction stage
	foundEntityStage := false
	for _, info := range progressCalls {
		if info.Stage == StageEntityExtraction {
			foundEntityStage = true
			break
		}
	}

	if !foundEntityStage {
		t.Error("Expected progress callback for entity extraction stage")
	}
}

// =============================================================================
// Pipeline Stage Tests
// =============================================================================

func TestExtractionPipeline_DisabledStages(t *testing.T) {
	config := DefaultPipelineConfig()
	config.EnabledStages[StageRelationExtraction] = false
	config.EnabledStages[StageEntityLinking] = false
	config.EnabledStages[StageRelationValidation] = false
	pipeline := NewExtractionPipelineWithConfig(config)

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	result := pipeline.Extract(files)

	// Should have entities (stage enabled)
	if len(result.Entities) == 0 {
		t.Error("Expected entities when entity extraction is enabled")
	}

	// Should not have relations (stage disabled)
	if len(result.Relations) != 0 {
		t.Errorf("Expected no relations when stage is disabled, got %d", len(result.Relations))
	}

	// Should not have links (stage disabled)
	if len(result.Links) != 0 {
		t.Errorf("Expected no links when stage is disabled, got %d", len(result.Links))
	}

	// Should not have validation results (stage disabled)
	if len(result.ValidationResults) != 0 {
		t.Errorf("Expected no validation results when stage is disabled, got %d", len(result.ValidationResults))
	}
}

func TestExtractionPipeline_OnlyEntityExtraction(t *testing.T) {
	config := DefaultPipelineConfig()
	config.EnabledStages = map[PipelineStage]bool{
		StageEntityExtraction:   true,
		StageRelationExtraction: false,
		StageEntityLinking:      false,
		StageRelationValidation: false,
	}
	pipeline := NewExtractionPipelineWithConfig(config)

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	result := pipeline.Extract(files)

	if len(result.Entities) == 0 {
		t.Error("Expected entities")
	}
	if len(result.Relations) != 0 {
		t.Error("Expected no relations")
	}
}

// =============================================================================
// Extraction Summary Tests
// =============================================================================

func TestExtractionPipeline_Summarize(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

import "fmt"

func main() {
	helper()
}

func helper() {
	fmt.Println("helper")
}

type MyStruct struct {}

func (m *MyStruct) Method() {}
`),
	}

	result := pipeline.Extract(files)
	summary := pipeline.Summarize(result)

	// Check basic counts
	if summary.TotalFiles != 1 {
		t.Errorf("Expected TotalFiles 1, got %d", summary.TotalFiles)
	}

	if summary.TotalEntities == 0 {
		t.Error("Expected non-zero TotalEntities")
	}

	// Check entities by kind
	if summary.EntitiesByKind["function"] == 0 {
		t.Error("Expected function entities")
	}

	// Check entities by language
	if summary.EntitiesByLanguage["go"] == 0 {
		t.Error("Expected Go language entities")
	}

	// Duration should be non-zero
	if summary.Duration == 0 {
		t.Error("Expected non-zero Duration")
	}
}

// =============================================================================
// Visibility Detection Tests
// =============================================================================

func TestExtractionPipeline_VisibilityDetection(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

func PublicFunc() {}
func privateFunc() {}
`),
		"/test/app.py": []byte(`
def public_func():
    pass

def _internal_func():
    pass

def __private_func():
    pass
`),
	}

	result := pipeline.Extract(files)

	// Check Go visibility
	for _, entity := range result.Entities {
		if entity.FilePath == "/test/main.go" {
			switch entity.Name {
			case "PublicFunc":
				if entity.Visibility != VisibilityPublic {
					t.Errorf("Expected PublicFunc to be public, got %s", entity.Visibility)
				}
			case "privateFunc":
				if entity.Visibility != VisibilityPrivate {
					t.Errorf("Expected privateFunc to be private, got %s", entity.Visibility)
				}
			}
		}
	}

	// Check Python visibility
	for _, entity := range result.Entities {
		if entity.FilePath == "/test/app.py" {
			switch entity.Name {
			case "public_func":
				if entity.Visibility != VisibilityPublic {
					t.Errorf("Expected public_func to be public, got %s", entity.Visibility)
				}
			case "_internal_func":
				if entity.Visibility != VisibilityInternal {
					t.Errorf("Expected _internal_func to be internal, got %s", entity.Visibility)
				}
			case "__private_func":
				if entity.Visibility != VisibilityPrivate {
					t.Errorf("Expected __private_func to be private, got %s", entity.Visibility)
				}
			}
		}
	}
}

// =============================================================================
// Relation Extraction Tests
// =============================================================================

func TestExtractionPipeline_RelationExtraction(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

import "fmt"

func main() {
	helper()
	fmt.Println("Hello")
}

func helper() {
	fmt.Println("helper")
}
`),
	}

	result := pipeline.Extract(files)

	// Should have some relations
	if len(result.Relations) == 0 {
		t.Error("Expected relations to be extracted")
	}

	// Check for import relations
	hasImportRelation := false
	for _, relation := range result.Relations {
		if relation.RelationType == RelImports {
			hasImportRelation = true
			break
		}
	}

	if !hasImportRelation {
		t.Error("Expected import relation")
	}
}

// =============================================================================
// Entity Linking Tests
// =============================================================================

func TestExtractionPipeline_EntityLinking(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

func main() {
	helper()
}

func helper() {
	// helper implementation
}
`),
	}

	result := pipeline.Extract(files)

	// Links may or may not be found depending on the implementation
	// This test ensures the linking stage runs without error
	if result.EndTime.Before(result.StartTime) {
		t.Error("End time should be after start time")
	}
}

// =============================================================================
// Validation Tests
// =============================================================================

func TestExtractionPipeline_RelationValidation(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

func main() {
	helper()
}

func helper() {}
`),
	}

	result := pipeline.Extract(files)

	// Validation results should exist for each relation
	if len(result.Relations) > 0 && len(result.ValidationResults) == 0 {
		t.Error("Expected validation results for relations")
	}

	// Each relation should have a corresponding validation result
	if len(result.ValidationResults) != len(result.Relations) {
		t.Errorf("Expected %d validation results, got %d",
			len(result.Relations), len(result.ValidationResults))
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestExtractionPipeline_EmptyFileMap(t *testing.T) {
	pipeline := NewExtractionPipeline()

	result := pipeline.Extract(map[string][]byte{})

	if len(result.Entities) != 0 {
		t.Errorf("Expected no entities for empty file map, got %d", len(result.Entities))
	}

	if len(result.Relations) != 0 {
		t.Errorf("Expected no relations for empty file map, got %d", len(result.Relations))
	}

	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors for empty file map, got %d", len(result.Errors))
	}
}

func TestExtractionPipeline_NilContent(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": nil,
	}

	// Should not panic
	result := pipeline.Extract(files)

	// Should handle nil content gracefully
	if result.TotalDuration == 0 {
		t.Error("Expected non-zero duration")
	}
}

func TestExtractionPipeline_LargeFile(t *testing.T) {
	pipeline := NewExtractionPipeline()

	// Generate a large Go file
	var code strings.Builder
	code.WriteString("package main\n\n")
	for i := 0; i < 100; i++ {
		code.WriteString("func func")
		code.WriteString(string(rune('0' + i/10)))
		code.WriteString(string(rune('0' + i%10)))
		code.WriteString("() {}\n")
	}

	files := map[string][]byte{
		"/test/large.go": []byte(code.String()),
	}

	result := pipeline.Extract(files)

	// Should extract all functions
	if len(result.Entities) < 100 {
		t.Errorf("Expected at least 100 entities, got %d", len(result.Entities))
	}
}

// =============================================================================
// Result Timing Tests
// =============================================================================

func TestExtractionPipeline_ResultTiming(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	result := pipeline.Extract(files)

	// Start time should be set
	if result.StartTime.IsZero() {
		t.Error("Expected StartTime to be set")
	}

	// End time should be set
	if result.EndTime.IsZero() {
		t.Error("Expected EndTime to be set")
	}

	// End time should be after start time
	if result.EndTime.Before(result.StartTime) {
		t.Error("Expected EndTime to be after StartTime")
	}

	// Total duration should match
	expectedDuration := result.EndTime.Sub(result.StartTime)
	if result.TotalDuration != expectedDuration {
		t.Errorf("Expected TotalDuration %v, got %v", expectedDuration, result.TotalDuration)
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestExtractionPipeline_ConcurrentAccess(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	// Run multiple extractions concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := pipeline.Extract(files)
			if len(result.Entities) == 0 {
				t.Error("Expected entities from concurrent extraction")
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// Mock Extractor for Testing
// =============================================================================

type mockEntityExtractor struct{}

func (m *mockEntityExtractor) ExtractEntities(filePath string, content []byte) ([]ExtractedEntity, error) {
	return []ExtractedEntity{
		{
			Name:      "MockFunction",
			Kind:      EntityKindFunction,
			FilePath:  filePath,
			StartLine: 1,
			EndLine:   5,
		},
	}, nil
}

// =============================================================================
// Extractor Registration Tests
// =============================================================================

func TestExtractionPipeline_CustomExtractor(t *testing.T) {
	pipeline := NewExtractionPipeline()

	mockExtractor := &mockEntityExtractor{}
	pipeline.RegisterExtractor("mock", mockExtractor)

	// Note: We can't directly test this without modifying detectLanguage
	// But we can verify the extractor is registered
	if !pipeline.HasExtractor("mock") {
		t.Error("Expected mock extractor to be registered")
	}
}

// =============================================================================
// File Metrics Tests
// =============================================================================

func TestExtractionPipeline_FileMetrics(t *testing.T) {
	pipeline := NewExtractionPipeline()

	files := map[string][]byte{
		"/test/main.go": []byte(`package main

func main() {}
func helper() {}
func another() {}
`),
	}

	result := pipeline.Extract(files)

	if len(result.FileMetrics) != 1 {
		t.Fatalf("Expected 1 file metric, got %d", len(result.FileMetrics))
	}

	metric := result.FileMetrics[0]

	if metric.FilePath != "/test/main.go" {
		t.Errorf("Expected FilePath '/test/main.go', got %q", metric.FilePath)
	}

	if metric.Language != "go" {
		t.Errorf("Expected Language 'go', got %q", metric.Language)
	}

	if metric.EntitiesCount == 0 {
		t.Error("Expected non-zero EntitiesCount")
	}

	if metric.Duration == 0 {
		t.Error("Expected non-zero Duration")
	}

	if metric.Error != nil {
		t.Errorf("Expected no error, got %v", metric.Error)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestExtractionPipeline_FullWorkflow(t *testing.T) {
	pipeline := NewExtractionPipeline()

	// Simulate a small project
	files := map[string][]byte{
		"/project/main.go": []byte(`package main

import "./pkg/utils"

func main() {
	utils.Helper()
}
`),
		"/project/pkg/utils/utils.go": []byte(`package utils

func Helper() {
	internal()
}

func internal() {}
`),
	}

	result := pipeline.Extract(files)

	// Verify all stages completed
	if len(result.Entities) == 0 {
		t.Error("Expected entities")
	}

	// Verify file metrics
	if len(result.FileMetrics) != 2 {
		t.Errorf("Expected 2 file metrics, got %d", len(result.FileMetrics))
	}

	// Verify no critical errors
	for _, err := range result.Errors {
		if err.Error != nil {
			t.Logf("Warning: Extraction error: %s", err.Message)
		}
	}

	// Verify timing
	if result.TotalDuration == 0 {
		t.Error("Expected non-zero total duration")
	}

	// Get summary
	summary := pipeline.Summarize(result)
	if summary.TotalFiles != 2 {
		t.Errorf("Expected 2 total files in summary, got %d", summary.TotalFiles)
	}
}

func TestExtractionPipeline_IncrementalWorkflow(t *testing.T) {
	pipeline := NewExtractionPipeline()

	// Initial extraction
	initialFiles := map[string][]byte{
		"/project/main.go": []byte(`package main

func main() {}
`),
		"/project/utils.go": []byte(`package main

func utility() {}
`),
	}

	initialResult := pipeline.Extract(initialFiles)

	// Verify initial extraction
	initialCount := len(initialResult.Entities)
	if initialCount == 0 {
		t.Fatal("Expected entities from initial extraction")
	}

	// Simulate file change - add new function
	changedFiles := map[string][]byte{
		"/project/main.go": []byte(`package main

func main() {}
func newFunction() {}
`),
	}

	// Incremental extraction
	incrementalResult := pipeline.ExtractIncremental(changedFiles, initialResult.Entities)

	// Should have preserved unchanged entities and added new ones
	newCount := len(incrementalResult.Entities)
	if newCount <= initialCount {
		t.Errorf("Expected more entities after adding function, initial: %d, after: %d",
			initialCount, newCount)
	}

	// Verify new function exists
	hasNewFunc := false
	for _, entity := range incrementalResult.Entities {
		if entity.Name == "newFunction" {
			hasNewFunc = true
			break
		}
	}

	if !hasNewFunc {
		t.Error("Expected newFunction in incremental result")
	}

	// Verify unchanged utility still exists
	hasUtility := false
	for _, entity := range incrementalResult.Entities {
		if entity.Name == "utility" {
			hasUtility = true
			break
		}
	}

	if !hasUtility {
		t.Error("Expected utility to be preserved from unchanged file")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkExtractionPipeline_SmallFile(b *testing.B) {
	pipeline := NewExtractionPipeline()
	files := map[string][]byte{
		"/test/main.go": []byte(`package main
func main() {}
`),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipeline.Extract(files)
	}
}

func BenchmarkExtractionPipeline_MediumFile(b *testing.B) {
	pipeline := NewExtractionPipeline()

	var code strings.Builder
	code.WriteString("package main\n\n")
	for i := 0; i < 50; i++ {
		code.WriteString("func func")
		code.WriteString(string(rune('0' + i/10)))
		code.WriteString(string(rune('0' + i%10)))
		code.WriteString("() {}\n")
	}

	files := map[string][]byte{
		"/test/main.go": []byte(code.String()),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipeline.Extract(files)
	}
}

func BenchmarkExtractionPipeline_ParallelVsSequential(b *testing.B) {
	files := make(map[string][]byte)
	for i := 0; i < 10; i++ {
		files["/test/file"+string(rune('0'+i))+".go"] = []byte(`package main
func func` + string(rune('0'+i)) + `() {}
`)
	}

	b.Run("Parallel", func(b *testing.B) {
		config := DefaultPipelineConfig()
		config.ParallelExtraction = true
		pipeline := NewExtractionPipelineWithConfig(config)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pipeline.Extract(files)
		}
	})

	b.Run("Sequential", func(b *testing.B) {
		config := DefaultPipelineConfig()
		config.ParallelExtraction = false
		pipeline := NewExtractionPipelineWithConfig(config)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pipeline.Extract(files)
		}
	})
}
