package pipeline

import (
	"encoding/json"
	"sort"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
)

func (pt *PipelineTester) recordSuccessfulToolOutput(toolName, output string) {
	if strings.TrimSpace(toolName) != "diagnose_failure" {
		return
	}
	output = strings.TrimSpace(output)
	if output == "" || !json.Valid([]byte(output)) {
		return
	}
	var report testershared.DiagnosisReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return
	}
	pt.storeDiagnosis(&report)
}

func (pt *PipelineTester) storeDiagnosis(report *testershared.DiagnosisReport) {
	if report == nil || strings.TrimSpace(report.TestName) == "" {
		return
	}
	copyReport := *report
	copyReport.RootCauses = append([]testershared.RootCause(nil), report.RootCauses...)
	copyReport.Trail = append([]testershared.InvestigStep(nil), report.Trail...)
	copyReport.SuggestedFix = append([]testershared.SuggestedFix(nil), report.SuggestedFix...)

	pt.mu.Lock()
	defer pt.mu.Unlock()
	if pt.diagnoses == nil {
		pt.diagnoses = make(map[string]*testershared.DiagnosisReport)
	}
	pt.diagnoses[strings.TrimSpace(report.TestName)] = &copyReport
}

func (pt *PipelineTester) diagnosisSnapshots() []*testershared.DiagnosisReport {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if len(pt.diagnoses) == 0 {
		return nil
	}
	names := make([]string, 0, len(pt.diagnoses))
	for name := range pt.diagnoses {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*testershared.DiagnosisReport, 0, len(names))
	for _, name := range names {
		report := pt.diagnoses[name]
		if report == nil {
			continue
		}
		copyReport := *report
		copyReport.RootCauses = append([]testershared.RootCause(nil), report.RootCauses...)
		copyReport.Trail = append([]testershared.InvestigStep(nil), report.Trail...)
		copyReport.SuggestedFix = append([]testershared.SuggestedFix(nil), report.SuggestedFix...)
		out = append(out, &copyReport)
	}
	return out
}

func (pt *PipelineTester) currentTaskSnapshot() *agentshared.PipelineTaskInput {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if pt.currentTask == nil {
		return nil
	}
	copyTask := *pt.currentTask
	if pt.currentTask.Context != nil {
		copyTask.Context = make(map[string]any, len(pt.currentTask.Context))
		for key, value := range pt.currentTask.Context {
			copyTask.Context[key] = value
		}
	}
	if pt.currentTask.ParentResults != nil {
		copyTask.ParentResults = make(map[string]any, len(pt.currentTask.ParentResults))
		for key, value := range pt.currentTask.ParentResults {
			copyTask.ParentResults[key] = value
		}
	}
	return &copyTask
}
