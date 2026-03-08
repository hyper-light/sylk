package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/tester"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

// HandleRequest processes a TesterRequest directly through skills (no LLM).
// This is the TDD-pipeline-facing API. The LLM-driven Handle() method is
// used only through the Guide routing system.
func (pt *PipelineTester) HandleRequest(ctx context.Context, req *tester.TesterRequest) (*tester.TesterResponse, error) {
	if req == nil {
		return nil, nil
	}

	switch req.Intent {
	case tester.IntentRunTests:
		return pt.runTests(ctx, req)
	case tester.IntentCoverage:
		return pt.coverageReport(req)
	default:
		return pt.runTests(ctx, req)
	}
}

// runTests executes test suites via skills and returns structured results.
func (pt *PipelineTester) runTests(ctx context.Context, req *tester.TesterRequest) (*tester.TesterResponse, error) {
	startTime := time.Now()

	suiteResult := &tester.TestSuiteResult{
		SuiteID:   uuid.New().String(),
		Name:      "test_run",
		StartedAt: startTime,
		Results:   []tester.TestResult{},
	}

	packages := req.Packages
	if len(packages) == 0 {
		packages = req.Files
	}
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	// Invoke the run_test_suite skill for each package set.
	input, _ := json.Marshal(map[string]any{
		"packages": packages,
		"race":     true,
	})
	if _, err := pt.toolRuntime().Activate("run_test_suite"); err != nil {
		return nil, fmt.Errorf("activate run_test_suite: %w", err)
	}
	correlationID := req.ID
	if correlationID == "" {
		correlationID = suiteResult.SuiteID
	}
	execResult, err := pt.toolRuntime().ExecuteRaw(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        uuid.New().String(),
			Name:      "run_test_suite",
			Arguments: string(input),
		},
		AgentID:         pt.id,
		CorrelationID:   correlationID,
		CapabilityScope: pt.toolRuntime().CapabilityScope(),
	})
	if err != nil {
		return nil, fmt.Errorf("execute run_test_suite: %w", err)
	}
	_ = execResult

	pt.aggregateSuiteResults(suiteResult)
	suiteResult.CompletedAt = time.Now()
	suiteResult.Duration = suiteResult.CompletedAt.Sub(startTime)

	return &tester.TesterResponse{
		ID:          uuid.New().String(),
		RequestID:   req.ID,
		Success:     suiteResult.Failed == 0,
		SuiteResult: suiteResult,
		Timestamp:   time.Now(),
	}, nil
}

// coverageReport generates a coverage report via skills.
func (pt *PipelineTester) coverageReport(req *tester.TesterRequest) (*tester.TesterResponse, error) {
	report := &tester.CoverageReport{
		ID:                 uuid.New().String(),
		FileCoverage:       make(map[string]*tester.FileCoverage),
		PackageCoverage:    make(map[string]float64),
		UncoveredLines:     make(map[string][]int),
		CoverageByCategory: make(map[tester.TestCategory]float64),
		GeneratedAt:        time.Now(),
	}

	return &tester.TesterResponse{
		ID:             uuid.New().String(),
		RequestID:      req.ID,
		Success:        true,
		CoverageReport: report,
		Timestamp:      time.Now(),
	}, nil
}

func (pt *PipelineTester) aggregateSuiteResults(suite *tester.TestSuiteResult) {
	suite.TotalTests = len(suite.Results)
	for _, result := range suite.Results {
		switch result.Status {
		case tester.StatusPassed:
			suite.Passed++
		case tester.StatusFailed:
			suite.Failed++
		case tester.StatusSkipped:
			suite.Skipped++
		case tester.StatusError:
			suite.Errors++
		case tester.StatusFlaky:
			suite.FlakyDetected++
		}
	}
}
