package shared

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DiagnosisEngine performs root cause analysis on test failures.
// Core principle: ALWAYS assume product code is faulty first.
type DiagnosisEngine interface {
	DiagnoseFailure(ctx context.Context, failure TestFailure, sourceFiles []string) (*DiagnosisReport, error)
}

// NewDiagnosisEngine returns a default diagnosis engine that builds
// structured reports from failure data. The actual root cause analysis
// is driven by the LLM through skills — this engine provides the
// framework and data structures.
func NewDiagnosisEngine() DiagnosisEngine {
	return &defaultDiagnosisEngine{}
}

type defaultDiagnosisEngine struct{}

func (d *defaultDiagnosisEngine) DiagnoseFailure(
	_ context.Context,
	failure TestFailure,
	_ []string,
) (*DiagnosisReport, error) {
	return &DiagnosisReport{
		ID:           uuid.New().String()[:8],
		TestName:     failure.TestName,
		RootCauses:   nil,
		Trail:        nil,
		Confidence:   0,
		SuggestedFix: nil,
		IsProductBug: true,
		CreatedAt:    time.Now(),
	}, nil
}
