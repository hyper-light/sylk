// Package test provides test framework detection and execution support for the Sylk project.
// It enables automatic detection of test frameworks across multiple languages and provides
// a unified interface for running tests, collecting coverage, and parsing results.
package test

import (
	"sync"
	"time"
)

// TestFrameworkID is a unique identifier for a test framework.
// Examples: "go-test", "jest", "pytest", "cargo-test", "rspec"
type TestFrameworkID string

// Common test framework identifiers
const (
	FrameworkGoTest    TestFrameworkID = "go-test"
	FrameworkJest      TestFrameworkID = "jest"
	FrameworkPytest    TestFrameworkID = "pytest"
	FrameworkCargoTest TestFrameworkID = "cargo-test"
	FrameworkRSpec     TestFrameworkID = "rspec"
	FrameworkMocha     TestFrameworkID = "mocha"
	FrameworkVitest    TestFrameworkID = "vitest"
	FrameworkPHPUnit   TestFrameworkID = "phpunit"
	FrameworkJUnit     TestFrameworkID = "junit"
	FrameworkNUnit     TestFrameworkID = "nunit"
)

// TestStatus represents the outcome of a test execution.
type TestStatus string

const (
	// TestStatusPassed indicates the test passed successfully.
	TestStatusPassed TestStatus = "passed"
	// TestStatusFailed indicates the test failed with assertion errors.
	TestStatusFailed TestStatus = "failed"
	// TestStatusSkipped indicates the test was skipped or ignored.
	TestStatusSkipped TestStatus = "skipped"
	// TestStatusError indicates the test encountered an unexpected error.
	TestStatusError TestStatus = "error"
)

// TestFrameworkDefinition describes a test framework and how to interact with it.
// It contains all the information needed to detect, run, and parse results from
// a specific test framework.
type TestFrameworkDefinition struct {
	// ID is the unique identifier for this framework.
	ID TestFrameworkID

	// Name is the human-readable name of the framework.
	Name string

	// Language is the programming language this framework supports.
	// Examples: "go", "javascript", "typescript", "python", "rust", "ruby"
	Language string

	// RunCommand is the command template to run all tests.
	// May contain placeholders like {dir} for the test directory.
	RunCommand string

	// RunFileCommand is the command template to run tests in a specific file.
	// May contain placeholders like {file} for the test file path.
	RunFileCommand string

	// RunSingleCommand is the command template to run a single test by name.
	// May contain placeholders like {file} and {test} for test identification.
	RunSingleCommand string

	// CoverageCommand is the command template to run tests with coverage.
	// May contain placeholders similar to RunCommand.
	CoverageCommand string

	// WatchCommand is the command template to run tests in watch mode.
	// Empty if the framework does not support watch mode.
	WatchCommand string

	// TestFilePatterns are glob patterns that match test files for this framework.
	// Examples: ["*_test.go"], ["*.test.js", "*.spec.js"], ["test_*.py", "*_test.py"]
	TestFilePatterns []string

	// ConfigFiles are filenames that indicate this framework is configured.
	// Examples: ["jest.config.js", "jest.config.ts"], ["pytest.ini", "pyproject.toml"]
	ConfigFiles []string

	// Priority determines selection order when multiple frameworks are detected.
	// Higher values indicate higher priority. Default is 0.
	Priority int

	// Enabled determines if this framework is available in the current environment.
	// It should check for required executables, config files, etc.
	// Returns true if the framework can be used.
	Enabled func(projectDir string) bool

	// ParseOutput parses the raw test output and returns structured results.
	// The implementation should handle the specific output format of the framework.
	ParseOutput func(output []byte) (*TestResult, error)
}

// TestResult contains the aggregated results of a test run.
type TestResult struct {
	// Status is the overall status of the test run.
	Status TestStatus

	// Passed is the number of tests that passed.
	Passed int

	// Failed is the number of tests that failed.
	Failed int

	// Skipped is the number of tests that were skipped.
	Skipped int

	// Total is the total number of tests run.
	Total int

	// Duration is how long the test run took.
	Duration time.Duration

	// Failures contains details about each failed test.
	Failures []TestFailure

	// Coverage contains code coverage information if collected.
	Coverage *CoverageResult

	// Output is the raw output from the test command.
	Output string

	// Error contains any error that occurred during test execution.
	Error error
}

// TestFailure contains details about a single test failure.
type TestFailure struct {
	// TestName is the name of the failed test.
	TestName string

	// File is the path to the file containing the failed test.
	File string

	// Line is the line number where the failure occurred.
	Line int

	// Message is the failure message or assertion error.
	Message string

	// Expected is the expected value in assertion failures.
	Expected string

	// Actual is the actual value in assertion failures.
	Actual string

	// StackTrace is the stack trace at the point of failure.
	StackTrace string
}

// CoverageResult contains code coverage information from a test run.
type CoverageResult struct {
	// Percentage is the overall code coverage percentage (0-100).
	Percentage float64

	// Lines contains line coverage information.
	Lines *CoverageMetric

	// Branches contains branch coverage information.
	Branches *CoverageMetric

	// Functions contains function coverage information.
	Functions *CoverageMetric

	// UncoveredFiles lists files with zero coverage.
	UncoveredFiles []string

	// FileCoverage maps file paths to their coverage percentages.
	FileCoverage map[string]float64
}

// CoverageMetric represents a single coverage metric with hit/total counts.
type CoverageMetric struct {
	// Covered is the number of covered items (lines, branches, or functions).
	Covered int

	// Total is the total number of items.
	Total int

	// Percentage is the coverage percentage (0-100).
	Percentage float64
}

// TestConfig contains user configuration overrides for test execution.
type TestConfig struct {
	// Disabled prevents this framework from being used when true.
	Disabled bool

	// RunCommand overrides the default run command.
	RunCommand string

	// RunFileCommand overrides the default run file command.
	RunFileCommand string

	// RunSingleCommand overrides the default run single test command.
	RunSingleCommand string

	// CoverageEnabled enables coverage collection when running tests.
	CoverageEnabled bool

	// CoverageCommand overrides the default coverage command.
	CoverageCommand string

	// WatchEnabled enables watch mode when running tests.
	WatchEnabled bool

	// WatchCommand overrides the default watch command.
	WatchCommand string

	// Timeout is the maximum duration for test execution.
	Timeout time.Duration

	// Environment contains additional environment variables for test execution.
	Environment map[string]string

	// Args contains additional arguments to pass to the test command.
	Args []string
}

// DetectionResult contains the result of detecting a test framework in a project.
type DetectionResult struct {
	// FrameworkID is the identifier of the detected framework.
	FrameworkID TestFrameworkID

	// Confidence is how confident we are in this detection (0.0-1.0).
	// 1.0 means certain (e.g., config file found), lower values indicate inference.
	Confidence float64

	// Reason explains why this framework was detected.
	Reason string

	// ConfigFile is the path to the config file that triggered detection, if any.
	ConfigFile string

	// TestFiles are example test files found that match this framework.
	TestFiles []string
}

// TestFrameworkRegistry provides thread-safe storage and retrieval of test framework definitions.
type TestFrameworkRegistry struct {
	mu         sync.RWMutex
	frameworks map[TestFrameworkID]*TestFrameworkDefinition
}

// NewTestFrameworkRegistry creates a new empty TestFrameworkRegistry.
func NewTestFrameworkRegistry() *TestFrameworkRegistry {
	return &TestFrameworkRegistry{
		frameworks: make(map[TestFrameworkID]*TestFrameworkDefinition),
	}
}

// Register adds a test framework definition to the registry.
// Returns an error if a framework with the same ID is already registered.
func (r *TestFrameworkRegistry) Register(def *TestFrameworkDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyFrameworkID
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.frameworks[def.ID]; exists {
		return &FrameworkExistsError{ID: def.ID}
	}

	r.frameworks[def.ID] = def
	return nil
}

// Get retrieves a test framework definition by its ID.
// Returns the definition and true if found, nil and false otherwise.
func (r *TestFrameworkRegistry) Get(id TestFrameworkID) (*TestFrameworkDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.frameworks[id]
	return def, ok
}

// List returns all registered test framework definitions.
// The returned slice is a copy and safe to modify.
func (r *TestFrameworkRegistry) List() []*TestFrameworkDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*TestFrameworkDefinition, 0, len(r.frameworks))
	for _, def := range r.frameworks {
		result = append(result, def)
	}
	return result
}

// GetByLanguage returns all test framework definitions for a specific language.
// The language comparison is case-sensitive.
func (r *TestFrameworkRegistry) GetByLanguage(lang string) []*TestFrameworkDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*TestFrameworkDefinition
	for _, def := range r.frameworks {
		if def.Language == lang {
			result = append(result, def)
		}
	}
	return result
}

// Errors for TestFrameworkRegistry operations.
var (
	// ErrNilDefinition is returned when attempting to register a nil definition.
	ErrNilDefinition = &RegistryError{Message: "cannot register nil framework definition"}

	// ErrEmptyFrameworkID is returned when a framework definition has an empty ID.
	ErrEmptyFrameworkID = &RegistryError{Message: "framework definition must have a non-empty ID"}
)

// RegistryError represents an error in registry operations.
type RegistryError struct {
	Message string
}

func (e *RegistryError) Error() string {
	return e.Message
}

// FrameworkExistsError is returned when attempting to register a framework
// with an ID that is already registered.
type FrameworkExistsError struct {
	ID TestFrameworkID
}

func (e *FrameworkExistsError) Error() string {
	return "framework already registered: " + string(e.ID)
}
