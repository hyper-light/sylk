package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

const testerVerificationArtifactTitle = "Tester Verification Handoff"

// publishVerificationArtifact packages the verification handoff for a single
// downstream recipient and publishes it via the coordination client. The
// recipient-specific summary, evidence_refs, and failure_focus shape what
// that recipient sees in the artifact's recipient_view block; bulk evidence
// (suite results, diagnoses, authored files) ships the same way for every
// recipient because the workspace is the workspace.
func (pt *PipelineTester) publishVerificationArtifact(ctx context.Context, spec agentshared.PipelineTesterFinalizeTargetSpec) (*coordination.Artifact, error) {
	target := strings.TrimSpace(spec.Target)
	if target == "" {
		return nil, fmt.Errorf("verification handoff target is required")
	}
	input, err := pt.buildVerificationArtifactInput(spec)
	if err != nil {
		return nil, err
	}
	return pt.coordinationClient().PublishArtifact(ctx, input)
}

func (pt *PipelineTester) buildVerificationArtifactInput(spec agentshared.PipelineTesterFinalizeTargetSpec) (coordination.PublishArtifactInput, error) {
	target := strings.TrimSpace(spec.Target)
	task := pt.currentTaskSnapshot()
	if task == nil {
		return coordination.PublishArtifactInput{}, fmt.Errorf("tester task context unavailable")
	}
	taskID := strings.TrimSpace(task.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(pt.pipelineID)
	}
	if taskID == "" {
		return coordination.PublishArtifactInput{}, fmt.Errorf("tester task context unavailable")
	}

	suite := pt.lastSuiteSnapshot()
	if suite == nil {
		return coordination.PublishArtifactInput{}, fmt.Errorf("run_test_suite must complete before publishing the verification handoff artifact")
	}

	plan := pt.planSnapshot()
	diagnoses := pt.diagnosisSnapshots()
	authoredFiles := pt.createdArtifacts()
	harness := pt.currentHarnessState()
	contract := agentshared.BuildTaskExecutionContract(task)
	upstreamArtifacts := relevantArtifactsFromContract(contract)
	upstreamIDs := relevantArtifactIDs(upstreamArtifacts)
	payload := map[string]any{
		"current_request":         agentshared.PipelineCurrentRequest(task),
		"original_task_objective": strings.TrimSpace(task.Prompt),
		"criteria_context": map[string]any{
			"acceptance_criteria": cloneJSONValue(task.Context["acceptance_criteria"]),
			"success_criteria":    cloneJSONValue(task.Context["success_criteria"]),
			"test_requirements":   cloneJSONValue(task.Context["test_requirements"]),
			"guidelines":          cloneJSONValue(task.Context["guidelines"]),
			"risk_factors":        cloneJSONValue(task.Context["risk_factors"]),
			"implementation_guide": strings.TrimSpace(
				verificationStringValue(task.Context["implementation_guide"]),
			),
		},
		"upstream_artifacts":  upstreamArtifacts,
		"test_plan":           plan,
		"authored_test_files": authoredFiles,
		"suite_result":        suite,
		"failures":            suiteFailurePayloads(suite),
		"diagnoses":           diagnoses,
		"recipient_view":      buildRecipientView(spec, suite),
	}
	if harness != nil {
		payload["harness"] = map[string]any{
			"framework_id":        harness.FrameworkID,
			"framework_name":      harness.FrameworkName,
			"run_command":         harness.RunCommand,
			"coverage_command":    harness.CoverageCommand,
			"recommended_outputs": cloneJSONValue(harness.RecommendedOutputs),
			"existing_test_files": append([]string(nil), harness.ExistingTestFiles...),
		}
	}

	recipientSummary := strings.TrimSpace(spec.Summary)
	if recipientSummary == "" {
		recipientSummary = buildVerificationArtifactSummary(suite, authoredFiles, diagnoses)
	}

	return coordination.PublishArtifactInput{
		TaskID:              taskID,
		TaskName:            pt.currentTaskName(),
		Kind:                "verification_result",
		Title:               testerVerificationArtifactTitle + " for " + target,
		Summary:             recipientSummary,
		ScopeKind:           coordination.ScopeKindTestSurface,
		ScopeKey:            taskID,
		Payload:             payload,
		Evidence:            buildVerificationEvidence(authoredFiles, diagnoses, spec.EvidenceRefs),
		UpstreamArtifactIDs: upstreamIDs,
		IdempotencyKey:      testerVerificationArtifactID(taskID, suite, target),
	}, nil
}

// buildRecipientView packages the LLM's per-target judgment — narrative,
// evidence pointers, and the subset of failures the recipient should
// prioritize — so each recipient sees a focused entry into the bulk evidence.
func buildRecipientView(spec agentshared.PipelineTesterFinalizeTargetSpec, suite *tester.TestSuiteResult) map[string]any {
	view := map[string]any{
		"target":  strings.TrimSpace(spec.Target),
		"summary": strings.TrimSpace(spec.Summary),
	}
	if refs := normalizeStringSliceForView(spec.EvidenceRefs); len(refs) > 0 {
		view["evidence_refs"] = refs
	}
	focus := normalizeStringSliceForView(spec.FailureFocus)
	if len(focus) > 0 {
		view["failure_focus"] = focus
		view["focused_failures"] = focusedFailures(suite, focus)
	}
	return view
}

func normalizeStringSliceForView(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func focusedFailures(suite *tester.TestSuiteResult, focus []string) []map[string]any {
	if suite == nil || len(focus) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(focus))
	for _, f := range focus {
		wanted[strings.ToLower(strings.TrimSpace(f))] = struct{}{}
	}
	out := make([]map[string]any, 0)
	for _, result := range suite.Results {
		switch result.Status {
		case tester.StatusFailed, tester.StatusError:
		default:
			continue
		}
		name := strings.ToLower(strings.TrimSpace(result.Name))
		if _, ok := wanted[name]; !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":          strings.TrimSpace(result.Name),
			"package":       strings.TrimSpace(result.Package),
			"status":        result.Status,
			"error_message": strings.TrimSpace(result.ErrorMessage),
		})
	}
	return out
}

func buildVerificationArtifactSummary(
	suite *tester.TestSuiteResult,
	authoredFiles []string,
	diagnoses []*testershared.DiagnosisReport,
) string {
	if suite == nil {
		return "Tester verification handoff artifact."
	}
	total := suite.TotalTests
	if total <= 0 {
		total = len(suite.Results)
	}
	parts := []string{
		fmt.Sprintf(
			"Tester verification handoff after suite execution: %d passed, %d failed, %d skipped, %d errors across %d tests.",
			suite.Passed,
			suite.Failed,
			suite.Skipped,
			suite.Errors,
			total,
		),
	}
	if len(authoredFiles) > 0 {
		parts = append(parts, fmt.Sprintf("%d authored test artifact(s) are attached.", len(authoredFiles)))
	}
	if len(diagnoses) > 0 {
		parts = append(parts, fmt.Sprintf("%d diagnosis report(s) are included.", len(diagnoses)))
	}
	return strings.Join(parts, " ")
}

func testerVerificationArtifactID(taskID string, suite *tester.TestSuiteResult, target string) string {
	taskID = strings.TrimSpace(taskID)
	target = strings.TrimSpace(target)
	suiteID := ""
	if suite != nil {
		suiteID = strings.TrimSpace(suite.SuiteID)
	}
	parts := []string{"tester-verification-handoff"}
	if taskID != "" {
		parts = append(parts, taskID)
	}
	if suiteID != "" {
		parts = append(parts, suiteID)
	}
	if target != "" {
		parts = append(parts, target)
	}
	return strings.Join(parts, ":")
}

func suiteFailurePayloads(suite *tester.TestSuiteResult) []map[string]any {
	if suite == nil || len(suite.Results) == 0 {
		return nil
	}
	failures := make([]map[string]any, 0)
	for _, result := range suite.Results {
		switch result.Status {
		case tester.StatusFailed, tester.StatusError:
		default:
			continue
		}
		failures = append(failures, map[string]any{
			"name":          strings.TrimSpace(result.Name),
			"package":       strings.TrimSpace(result.Package),
			"status":        result.Status,
			"error_message": strings.TrimSpace(result.ErrorMessage),
			"output":        strings.TrimSpace(result.Output),
			"stack_trace":   strings.TrimSpace(result.StackTrace),
		})
	}
	return failures
}

func relevantArtifactsFromContract(contract *agentshared.TaskExecutionContract) []map[string]any {
	if contract == nil || contract.Ledger == nil || len(contract.Ledger.RelevantArtifacts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(contract.Ledger.RelevantArtifacts))
	for _, artifact := range contract.Ledger.RelevantArtifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":            strings.TrimSpace(artifact.ID),
			"kind":          strings.TrimSpace(artifact.Kind),
			"summary":       strings.TrimSpace(artifact.Summary),
			"producer_type": strings.TrimSpace(artifact.ProducerType),
			"scope_key":     strings.TrimSpace(artifact.ScopeKey),
		})
	}
	return out
}

func relevantArtifactIDs(artifacts []map[string]any) []string {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]string, 0, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		id := strings.TrimSpace(verificationStringValue(artifact["id"]))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func buildVerificationEvidence(authoredFiles []string, diagnoses []*testershared.DiagnosisReport, recipientRefs []string) []coordination.EvidenceRef {
	seen := make(map[string]struct{})
	evidence := make([]coordination.EvidenceRef, 0, len(authoredFiles)+len(diagnoses)+len(recipientRefs))
	appendRef := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := kind + ":" + value
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		evidence = append(evidence, coordination.EvidenceRef{Kind: kind, Value: value})
	}
	for _, path := range authoredFiles {
		appendRef("file", path)
	}
	for _, report := range diagnoses {
		if report == nil {
			continue
		}
		for _, cause := range report.RootCauses {
			appendRef("file", cause.File)
		}
	}
	for _, ref := range recipientRefs {
		appendRef("recipient_evidence", ref)
	}
	return evidence
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return value
	}
	return cloned
}

func verificationStringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}
