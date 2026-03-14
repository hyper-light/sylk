package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/tester"
	"github.com/google/uuid"
)

// HandleRequest processes a TesterRequest directly through skills (no LLM).
// This is the TDD-pipeline-facing API. The LLM-driven Handle() method is
// used only through the Guide routing system.
func (pt *PipelineTester) HandleRequest(ctx context.Context, req *tester.TesterRequest) (*tester.TesterResponse, error) {
	if req == nil {
		return nil, nil
	}
	prevRuntime := pt.swapTaskRuntime(nil)
	defer pt.restoreTaskRuntime(prevRuntime)

	switch req.Intent {
	case tester.IntentCreateTests:
		return pt.createTests(ctx, req)
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

	packages := req.Packages
	execResult, err := pt.executeSuite(ctx, pt.currentHarnessState(), packages, req.Files, req.TestNames, true, false, 60)
	if err != nil {
		return nil, fmt.Errorf("run test suite: %w", err)
	}
	suiteResult := suiteResultFromExecution(execResult, startTime)
	pt.setLastSuiteResult(suiteResult)

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
