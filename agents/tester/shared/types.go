// Package shared provides types and utilities shared between the pipeline
// tester and global tester agent variants.
package shared

import "time"

// Additional TestCategory values for advanced test classification.
// These extend the base categories defined in agents/tester/types.go.
const (
	CategoryRaceCondition = "race_condition"
	CategoryDeadlock      = "deadlock"
	CategoryMemoryLeak    = "memory_leak"
	CategoryResourceLeak  = "resource_leak"
	CategorySecurity      = "security"
	CategoryFuzz          = "fuzz"
	CategoryNegative      = "negative"
	CategoryEdgeCase      = "edge_case"
	CategoryBoundary      = "boundary"

	// Design-specific test categories.
	CategoryAccessibility     = "accessibility"
	CategoryTokenUsage        = "token_usage"
	CategoryComponentAPI      = "component_api"
	CategoryDesignConsistency = "design_consistency"
)

// RiskLevel classifies the severity of a risk area.
type RiskLevel string

const (
	RiskCritical RiskLevel = "critical"
	RiskHigh     RiskLevel = "high"
	RiskMedium   RiskLevel = "medium"
	RiskLow      RiskLevel = "low"
)

// RiskCategory classifies the type of defect risk.
type RiskCategory string

const (
	RiskConcurrency RiskCategory = "concurrency"
	RiskResource    RiskCategory = "resource"
	RiskBoundary    RiskCategory = "boundary"
	RiskSecurity    RiskCategory = "security"
	RiskLogic       RiskCategory = "logic"
	RiskState       RiskCategory = "state"

	// Design-specific risk categories.
	RiskTokenMisuse       RiskCategory = "token_misuse"
	RiskAccessibility     RiskCategory = "accessibility"
	RiskComponentPattern  RiskCategory = "component_pattern"
	RiskStyleInconsistency RiskCategory = "style_inconsistency"
)

// TestPlan represents a deliberate testing strategy with rationale.
type TestPlan struct {
	ID          string            `json:"id"`
	Rationale   string            `json:"rationale"`
	RiskAreas   []RiskArea        `json:"risk_areas"`
	PlannedCase []PlannedTestCase `json:"planned_cases"`
	CreatedAt   time.Time         `json:"created_at"`
}

// PlannedTestCase is a test case with a failure hypothesis and input strategy.
type PlannedTestCase struct {
	Name              string `json:"name"`
	Category          string `json:"category"`
	FailureHypothesis string `json:"failure_hypothesis"`
	InputStrategy     string `json:"input_strategy"`
	ExpectedBehavior  string `json:"expected_behavior"`
	TargetFile        string `json:"target_file"`
	TargetFunction    string `json:"target_function,omitempty"`
}

// RiskArea identifies a risk area in the codebase.
type RiskArea struct {
	File        string       `json:"file"`
	Function    string       `json:"function,omitempty"`
	Category    RiskCategory `json:"category"`
	Level       RiskLevel    `json:"level"`
	Description string       `json:"description"`
}

// HarnessNeed describes test infrastructure required by the global tester.
type HarnessNeed struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

// DiagnosisReport captures root cause analysis with investigation trail.
type DiagnosisReport struct {
	ID            string         `json:"id"`
	TestName      string         `json:"test_name"`
	RootCauses    []RootCause    `json:"root_causes"`
	Trail         []InvestigStep `json:"trail"`
	Confidence    float64        `json:"confidence"`
	SuggestedFix  []SuggestedFix `json:"suggested_fixes"`
	IsProductBug  bool           `json:"is_product_bug"`
	CreatedAt     time.Time      `json:"created_at"`
}

// RootCause describes the file, line, and nature of a defect.
type RootCause struct {
	File        string       `json:"file"`
	Line        int          `json:"line"`
	Description string       `json:"description"`
	Category    RiskCategory `json:"category"`
}

// InvestigStep is a single investigation action with finding and conclusion.
type InvestigStep struct {
	Action     string `json:"action"`
	Finding    string `json:"finding"`
	Conclusion string `json:"conclusion"`
}

// SuggestedFix proposes a fix for product or test code.
type SuggestedFix struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Description string `json:"description"`
	FixCode     string `json:"fix_code,omitempty"`
	IsTestFix   bool   `json:"is_test_fix"`
}

// TestFailure captures a single test failure for diagnosis.
type TestFailure struct {
	TestName     string `json:"test_name"`
	Package      string `json:"package"`
	Output       string `json:"output"`
	ErrorMessage string `json:"error_message"`
	StackTrace   string `json:"stack_trace,omitempty"`
}

// PipelineTesterConfig configures the pipeline tester variant.
type PipelineTesterConfig struct {
	Model           string        `json:"model"`
	ReasoningEffort string        `json:"reasoning_effort"`
	MaxToolRuns     int           `json:"max_tool_runs"`
	MaxTokens       int           `json:"max_tokens"`
	DefaultTimeout  time.Duration `json:"default_timeout"`
	AgentID         string        `json:"agent_id"`
	SessionID       string        `json:"session_id"`
}

// DefaultPipelineTesterConfig returns sensible defaults.
func DefaultPipelineTesterConfig() PipelineTesterConfig {
	return PipelineTesterConfig{
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
		MaxToolRuns:     32,
		MaxTokens:       16384,
		DefaultTimeout:  60 * time.Second,
	}
}

// GlobalTesterConfig configures the global tester variant.
type GlobalTesterConfig struct {
	Model                  string        `json:"model"`
	ReasoningEffort        string        `json:"reasoning_effort"`
	MaxToolRuns            int           `json:"max_tool_runs"`
	MaxTokens              int           `json:"max_tokens"`
	DefaultTimeout         time.Duration `json:"default_timeout"`
	CoverageThreshold      float64       `json:"coverage_threshold"`
	MutationScoreThreshold float64       `json:"mutation_score_threshold"`
	FlakyThreshold         int           `json:"flaky_threshold"`
	FlakyRunCount          int           `json:"flaky_run_count"`
	ParallelTests          int           `json:"parallel_tests"`
	EnableMutationTesting  bool          `json:"enable_mutation_testing"`
	EnableFlakyDetection   bool          `json:"enable_flaky_detection"`
	AgentID                string        `json:"agent_id"`
	SessionID              string        `json:"session_id"`
}

// DefaultGlobalTesterConfig returns sensible defaults.
func DefaultGlobalTesterConfig() GlobalTesterConfig {
	return GlobalTesterConfig{
		Model:                  "gpt-5.3-codex",
		ReasoningEffort:        "xhigh",
		MaxToolRuns:            32,
		MaxTokens:              16384,
		DefaultTimeout:         120 * time.Second,
		CoverageThreshold:      80.0,
		MutationScoreThreshold: 70.0,
		FlakyThreshold:         20,
		FlakyRunCount:          5,
		ParallelTests:          4,
		EnableMutationTesting:  true,
		EnableFlakyDetection:   true,
	}
}

// ProtocolPhase identifies a phase within a tester protocol.
type ProtocolPhase string

const (
	PhaseAnalyzeRisks       ProtocolPhase = "analyze_risks"
	PhasePlanTests          ProtocolPhase = "plan_tests"
	PhaseImplementTests     ProtocolPhase = "implement_tests"
	PhaseExecuteTests       ProtocolPhase = "execute_tests"
	PhaseDiagnose           ProtocolPhase = "diagnose"
	PhaseDispatchFeedback   ProtocolPhase = "dispatch_feedback"
	PhaseAssembleBatch      ProtocolPhase = "assemble_batch"
	PhaseIntegrationRisks   ProtocolPhase = "integration_risks"
	PhaseArchitectStrategy  ProtocolPhase = "architect_strategy"
	PhaseConstructHarness   ProtocolPhase = "construct_harness"
	PhaseDiagnoseEscalate   ProtocolPhase = "diagnose_escalate"
)

// ProtocolState tracks progress through a testing protocol.
type ProtocolState struct {
	CurrentPhase ProtocolPhase `json:"current_phase"`
	CompletedAt  time.Time     `json:"completed_at,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// BatchContext tracks which pipelines' work to validate (global tester).
type BatchContext struct {
	PipelineIDs  []string          `json:"pipeline_ids"`
	ChangedFiles []string          `json:"changed_files"`
	TaskSpecs    map[string]string `json:"task_specs"`
}
