package pipeline

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/inspector/shared"
)

type InspectionStageResult struct {
	Criteria *shared.InspectorCriteria
	Result   *shared.InspectorResult
}

// ResponseText implements the Guide response text contract so inspector stage
// snapshots render as short summaries instead of raw JSON.
func (r *InspectionStageResult) ResponseText() string {
	if r == nil {
		return ""
	}

	lines := make([]string, 0, 2)
	if r.Criteria != nil {
		lines = append(lines, fmt.Sprintf(
			"Defined criteria contract: %d success criteria, %d quality gates, %d constraints.",
			len(r.Criteria.SuccessCriteria),
			len(r.Criteria.QualityGates),
			len(r.Criteria.Constraints),
		))
	}
	if r.Result != nil {
		status := "failed"
		if r.Result.Passed {
			status = "passed"
		}
		lines = append(lines, fmt.Sprintf(
			"Latest validation %s: %d criteria met, %d failed, %d issues, %d feedback loops.",
			status,
			len(r.Result.CriteriaMet),
			len(r.Result.CriteriaFailed),
			len(r.Result.Issues),
			r.Result.LoopCount,
		))
	}
	return strings.Join(lines, "\n")
}

func (pi *PipelineInspector) stageSnapshot(taskID string) (*InspectionStageResult, error) {
	taskID = strings.TrimSpace(taskID)
	criteria := pi.CriteriaForTask(taskID)
	if criteria == nil {
		return nil, fmt.Errorf("no criteria defined for task %s", taskID)
	}
	return &InspectionStageResult{
		Criteria: criteria,
		Result:   pi.ResultForTask(taskID),
	}, nil
}

func (pi *PipelineInspector) CriteriaForTask(taskID string) *shared.InspectorCriteria {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	pi.mu.RLock()
	criteria := pi.criteria[taskID]
	pi.mu.RUnlock()
	return cloneInspectorCriteria(criteria)
}

func (pi *PipelineInspector) ResultForTask(taskID string) *shared.InspectorResult {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	pi.mu.RLock()
	result := pi.results[taskID]
	pi.mu.RUnlock()
	return cloneInspectorResult(result)
}

func cloneInspectorCriteria(criteria *shared.InspectorCriteria) *shared.InspectorCriteria {
	if criteria == nil {
		return nil
	}
	cloned := *criteria
	cloned.SuccessCriteria = append([]shared.SuccessCriterion(nil), criteria.SuccessCriteria...)
	cloned.QualityGates = append([]shared.QualityGate(nil), criteria.QualityGates...)
	cloned.Constraints = append([]shared.Constraint(nil), criteria.Constraints...)
	return &cloned
}

func cloneInspectorResult(result *shared.InspectorResult) *shared.InspectorResult {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Issues = append([]shared.ValidationIssue(nil), result.Issues...)
	cloned.CriteriaMet = append([]string(nil), result.CriteriaMet...)
	cloned.CriteriaFailed = append([]string(nil), result.CriteriaFailed...)
	if result.QualityGateResults != nil {
		cloned.QualityGateResults = make(map[string]bool, len(result.QualityGateResults))
		for key, value := range result.QualityGateResults {
			cloned.QualityGateResults[key] = value
		}
	}
	cloned.FeedbackHistory = append([]shared.InspectorFeedback(nil), result.FeedbackHistory...)
	return &cloned
}

func stringValue(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}
