package shared

import (
	"context"
	"strings"
	"testing"
)

func TestBuildTaskExecutionContract_TracksRequestedOperations(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "engineer",
		Context: map[string]any{
			"pipeline_stage": "execute",
			"affected_files": []map[string]any{
				{"path": "src/hello_cli/cli.py", "operation": "create", "reason": "new CLI entrypoint"},
				{"path": "./tests/test_cli.py", "operation": "modify"},
			},
			"workspace": map[string]any{
				"read_set":     []string{"README.md"},
				"write_set":    []string{"src/hello_cli/cli.py", "tests/test_cli.py"},
				"test_surface": []string{"tests/test_cli.py"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.PreImplementation {
		t.Fatal("execute stage should not be pre-implementation")
	}
	if got := contract.OperationForPath("src/hello_cli/cli.py"); got != "create" {
		t.Fatalf("operation for create path = %q, want create", got)
	}
	if got := contract.OperationForPath("tests/test_cli.py"); got != "modify" {
		t.Fatalf("operation for modify path = %q, want modify", got)
	}
	if len(contract.WriteSet) != 2 {
		t.Fatalf("write_set len = %d, want 2", len(contract.WriteSet))
	}
}

func TestBuildTaskExecutionContract_TesterClassifiesAuthoringDeliverables(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "tester-pipeline",
		Prompt:    "Add some tests for the CLI behavior before implementation lands.",
		Context: map[string]any{
			"pipeline_stage":    "test",
			"test_requirements": []string{"Write spec-driven failing tests for greet() and main()."},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if !hasIntent(contract.Intents, TaskIntentPlanTests) {
		t.Fatalf("expected plan_tests intent, got %v", contract.Intents)
	}
	if !hasIntent(contract.Intents, TaskIntentAuthorTests) {
		t.Fatalf("expected author_tests intent, got %v", contract.Intents)
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableTestPlan) {
		t.Fatalf("expected test_plan deliverable, got %v", contract.Deliverables)
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableTestArtifact) {
		t.Fatalf("expected test_artifact deliverable, got %v", contract.Deliverables)
	}
}

func TestBuildTaskExecutionContract_TesterClassifiesAuthoringFromImplementationScope(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "tester-pipeline",
		Prompt:    "Create hello_cli Python package with CLI entry point and initializer.",
		Context: map[string]any{
			"pipeline_stage": "test",
			"affected_files": []map[string]any{
				{"path": "src/hello_cli/__init__.py", "operation": "create"},
				{"path": "src/hello_cli/cli.py", "operation": "create"},
			},
			"workspace": map[string]any{
				"write_set":    []string{"src/hello_cli/__init__.py", "src/hello_cli/cli.py"},
				"test_surface": []string{"tests/test_cli.py"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if !hasIntent(contract.Intents, TaskIntentAuthorTests) {
		t.Fatalf("expected author_tests intent from test-stage implementation scope, got %v", contract.Intents)
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableTestArtifact) {
		t.Fatalf("expected test_artifact deliverable from test-stage implementation scope, got %v", contract.Deliverables)
	}
}

func TestBuildTaskExecutionContract_TesterClassifiesPlanOnlyRequest(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "tester-pipeline",
		Prompt:    "Plan test coverage for this CLI module. Do not write or execute tests yet.",
		Context: map[string]any{
			"pipeline_stage": "test",
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if !hasIntent(contract.Intents, TaskIntentPlanTests) {
		t.Fatalf("expected plan_tests intent, got %v", contract.Intents)
	}
	if hasIntent(contract.Intents, TaskIntentAuthorTests) {
		t.Fatalf("did not expect author_tests intent, got %v", contract.Intents)
	}
	if len(contract.Deliverables) != 1 || contract.Deliverables[0] != TaskDeliverableTestPlan {
		t.Fatalf("unexpected deliverables: %v", contract.Deliverables)
	}
}

func TestBuildTaskExecutionContract_InspectorClassifiesContractSynthesis(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "inspector-pipeline",
		Prompt:    "Inspect this task and define the explicit implementation contract before work begins.",
		Context: map[string]any{
			"pipeline_stage": "inspect",
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if !contract.PreImplementation {
		t.Fatal("expected inspect-stage inspector contract to remain pre-implementation without evidence")
	}
	if contract.HasImplementationEvidence {
		t.Fatal("did not expect implementation evidence")
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableCriteriaContract) {
		t.Fatalf("expected criteria_contract deliverable, got %v", contract.Deliverables)
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableHandoffContract) {
		t.Fatalf("expected handoff_contract deliverable, got %v", contract.Deliverables)
	}
	if hasDeliverable(contract.Deliverables, TaskDeliverableCriteriaEvaluation) {
		t.Fatalf("did not expect criteria_evaluation deliverable, got %v", contract.Deliverables)
	}
}

func TestBuildTaskExecutionContract_InspectorClassifiesValidationFromParentResults(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "inspector-pipeline",
		Prompt:    "Validate the implementation and publish the inspection findings.",
		Context: map[string]any{
			"pipeline_stage": "inspect",
		},
		ParentResults: map[string]any{
			"engineer": map[string]any{
				"state":  "succeeded",
				"output": map[string]any{"summary": "implemented CLI entrypoint"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.PreImplementation {
		t.Fatal("did not expect validation contract to stay pre-implementation when parent results provide evidence")
	}
	if !contract.HasImplementationEvidence {
		t.Fatal("expected implementation evidence from parent results")
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableCriteriaEvaluation) {
		t.Fatalf("expected criteria_evaluation deliverable, got %v", contract.Deliverables)
	}
	if !hasDeliverable(contract.Deliverables, TaskDeliverableQualityGrade) {
		t.Fatalf("expected quality_grade deliverable, got %v", contract.Deliverables)
	}
	if hasDeliverable(contract.Deliverables, TaskDeliverableCriteriaContract) {
		t.Fatalf("did not expect criteria_contract deliverable, got %v", contract.Deliverables)
	}
}

func TestBuildTaskExecutionContract_EngineerBuildsTaskScopedReviewLedger(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "engineer",
		Context: map[string]any{
			"coordination_packet": map[string]any{
				"pending_reviews": []any{
					map[string]any{
						"id":          "rev-1",
						"artifact_id": "art-1",
						"summary":     "Address the failing CLI behavior",
					},
				},
				"relevant_artifacts": []any{
					map[string]any{
						"id":            "art-1",
						"kind":          "verification_result",
						"summary":       "CLI test failure details",
						"producer_type": "tester-pipeline",
					},
				},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.TaskID != "task-1" {
		t.Fatalf("TaskID = %q, want task-1", contract.TaskID)
	}
	if contract.Ledger == nil || contract.Ledger.TaskID != "task-1" {
		t.Fatalf("ledger = %#v, want task-scoped ledger", contract.Ledger)
	}
	if !contract.HasPendingReviews() {
		t.Fatal("expected pending reviews from coordination packet")
	}
	for _, want := range []TaskExecutionDeliverable{
		TaskDeliverableReviewIntake,
		TaskDeliverableReviewContext,
		TaskDeliverableReviewAddressed,
		TaskDeliverableReviewResolution,
	} {
		if !hasDeliverable(contract.Deliverables, want) {
			t.Fatalf("expected deliverable %q, got %v", want, contract.Deliverables)
		}
	}
}

func TestValidateTaskExecutionCall_InspectorBlocksPreimplementationValidation(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:                     "inspect",
		PreImplementation:         true,
		HasImplementationEvidence: false,
	})

	err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "validate_criteria", nil)
	if err == nil {
		t.Fatal("expected validate_criteria to be blocked")
	}
	if !strings.Contains(err.Error(), "pre-implementation inspect stage") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskExecutionCall_InspectorBlocksWorkspaceMutation(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:             "inspect",
		PreImplementation: true,
	})

	err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "write_pipeline_file", map[string]any{"path": "src/hello_cli/cli.py"})
	if err == nil {
		t.Fatal("expected inspector workspace mutation to be blocked")
	}
	if !strings.Contains(err.Error(), "must not implement or mutate") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskExecutionCall_InspectorBlocksReleaseUntilContractDeliverablesMet(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:             "inspect",
		PreImplementation: true,
		CriteriaDefined:   true,
		Deliverables:      []TaskExecutionDeliverable{TaskDeliverableCriteriaContract, TaskDeliverableScopeInspection, TaskDeliverablePendingValidation, TaskDeliverableHandoffContract},
		Intents:           []TaskExecutionIntent{TaskIntentSynthesizeContract},
		RequestedFiles:    nil,
		ReadSet:           nil,
		WriteSet:          nil,
		TestSurface:       nil,
	})

	RecordTaskExecutionSuccess(ctx, "inspect_workspace_state", map[string]any{"path": "src/hello_cli/cli.py"})
	RecordTaskExecutionSuccess(ctx, "get_validation_status", nil)

	if err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "coord_release_scope", nil); err == nil {
		t.Fatal("expected coord_release_scope to be blocked before handoff artifact publication")
	}

	RecordTaskExecutionSuccess(ctx, "coord_publish_artifact", nil)

	if err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "coord_release_scope", nil); err != nil {
		t.Fatalf("coord_release_scope blocked after contract deliverables were satisfied: %v", err)
	}
}

func TestValidateTaskExecutionCompletion_InspectorAllowsSeededContractSynthesis(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:             "inspect",
		PreImplementation: true,
		CriteriaDefined:   true,
		Deliverables: []TaskExecutionDeliverable{
			TaskDeliverableCriteriaContract,
			TaskDeliverableScopeInspection,
			TaskDeliverablePendingValidation,
			TaskDeliverableHandoffContract,
		},
	})

	RecordTaskExecutionSuccess(ctx, "inspect_workspace_state", map[string]any{"path": "src/hello_cli/cli.py"})
	RecordTaskExecutionSuccess(ctx, "get_validation_status", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "inspector-pipeline"); err == nil {
		t.Fatal("expected inspector completion to be blocked before artifact publication")
	}

	RecordTaskExecutionSuccess(ctx, "coord_publish_artifact", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "inspector-pipeline"); err != nil {
		t.Fatalf("expected seeded contract synthesis completion to succeed: %v", err)
	}
}

func TestValidateTaskExecutionCompletion_InspectorRequiresValidationGrade(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:                     "inspect",
		HasImplementationEvidence: true,
		CriteriaDefined:           true,
		Deliverables: []TaskExecutionDeliverable{
			TaskDeliverableCriteriaEvaluation,
			TaskDeliverableQualityChecks,
			TaskDeliverableValidationReport,
			TaskDeliverableQualityGrade,
		},
	})

	RecordTaskExecutionSuccess(ctx, "validate_criteria", nil)
	RecordTaskExecutionSuccess(ctx, "coord_publish_artifact", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "inspector-pipeline"); err == nil {
		t.Fatal("expected inspector validation completion to require grade_task_quality")
	}

	RecordTaskExecutionSuccess(ctx, "grade_task_quality", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "inspector-pipeline"); err != nil {
		t.Fatalf("expected inspector validation completion to succeed after grading: %v", err)
	}
}

func TestValidateTaskExecutionCall_EngineerBlocksReleaseUntilTaskReviewResolved(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		TaskID: "task-1",
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/hello_cli/cli.py", Operation: "modify"},
		},
		Ledger: &TaskExecutionLedger{
			TaskID:     "task-1",
			WorkerType: "engineer",
			Seeded:     true,
			PendingReviews: []TaskExecutionReview{
				{ID: "rev-1", ArtifactID: "art-1", Summary: "Address failing CLI test"},
			},
		},
		Deliverables: []TaskExecutionDeliverable{
			TaskDeliverableReviewIntake,
			TaskDeliverableReviewContext,
			TaskDeliverableReviewAddressed,
			TaskDeliverableReviewResolution,
			TaskDeliverableRequestedChange,
		},
	})

	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_resolve_artifact", map[string]any{"review_id": "rev-1"}); err == nil {
		t.Fatal("expected coord_resolve_artifact to be blocked before review inspection/addressing")
	}
	RecordTaskExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "src/hello_cli/cli.py"})
	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_resolve_artifact", map[string]any{"review_id": "rev-1"}); err == nil {
		t.Fatal("expected coord_resolve_artifact to stay blocked before requested change is applied")
	}
	RecordTaskExecutionSuccess(ctx, "write_pipeline_file", map[string]any{"path": "src/hello_cli/cli.py"})
	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_resolve_artifact", map[string]any{"review_id": "rev-1"}); err != nil {
		t.Fatalf("coord_resolve_artifact blocked after review context + change evidence: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_release_scope", nil); err == nil {
		t.Fatal("expected coord_release_scope to stay blocked before review resolution")
	}
	RecordTaskExecutionSuccess(ctx, "coord_resolve_artifact", map[string]any{"review_id": "rev-1"})
	if err := ValidateTaskExecutionCompletion(ctx, "engineer"); err != nil {
		t.Fatalf("engineer completion blocked after review resolution: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_release_scope", nil); err != nil {
		t.Fatalf("coord_release_scope blocked after review resolution: %v", err)
	}
}

func TestValidateTaskExecutionCompletion_DesignerRequiresTaskScopedReviewResolution(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		TaskID: "task-2",
		Ledger: &TaskExecutionLedger{
			TaskID:     "task-2",
			WorkerType: "designer",
			Seeded:     true,
			PendingReviews: []TaskExecutionReview{
				{ID: "rev-2", ArtifactID: "art-2", Summary: "Review the UX implications"},
			},
		},
		Deliverables: []TaskExecutionDeliverable{
			TaskDeliverableReviewIntake,
			TaskDeliverableReviewContext,
			TaskDeliverableReviewAddressed,
			TaskDeliverableReviewResolution,
		},
	})

	RecordTaskExecutionSuccess(ctx, "component_search", nil)
	RecordTaskExecutionSuccess(ctx, "coord_publish_artifact", nil)
	if err := ValidateTaskExecutionCompletion(ctx, "designer"); err == nil {
		t.Fatal("expected designer completion to require coord_resolve_artifact for the pending review")
	}
	if err := ValidateTaskExecutionCall(ctx, "designer", "coord_resolve_artifact", map[string]any{"review_id": "rev-2"}); err != nil {
		t.Fatalf("coord_resolve_artifact blocked after review context + addressing artifact: %v", err)
	}
	RecordTaskExecutionSuccess(ctx, "coord_resolve_artifact", map[string]any{"review_id": "rev-2"})
	if err := ValidateTaskExecutionCompletion(ctx, "designer"); err != nil {
		t.Fatalf("designer completion blocked after review resolution: %v", err)
	}
}

func TestValidateTaskExecutionCall_TesterRequiresPlanBeforeWrite(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan, TaskDeliverableTestArtifact},
	})

	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "write_test", map[string]any{"output_file": "tests/test_cli.py"}); err == nil {
		t.Fatal("expected write_test to be blocked before prerequisites")
	}

	RecordTaskExecutionSuccess(ctx, "check_inspector_gate", nil)
	RecordTaskExecutionSuccess(ctx, "detect_test_harness", nil)
	RecordTaskExecutionSuccess(ctx, "analyze_risk", nil)
	RecordTaskExecutionSuccess(ctx, "plan_tests", nil)

	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "write_test", map[string]any{"output_file": "tests/test_cli.py"}); err != nil {
		t.Fatalf("write_test blocked after prerequisites: %v", err)
	}
}

func TestValidateTaskExecutionCall_TesterAllowsStructuredWriteTestEvidence(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan, TaskDeliverableTestArtifact},
	})

	RecordTaskExecutionSuccess(ctx, "check_inspector_gate", nil)
	RecordTaskExecutionSuccess(ctx, "detect_test_harness", nil)

	input := map[string]any{
		"output_file": "tests/test_cli.py",
		"test_case": map[string]any{
			"name":               "TestMainGreetsByName",
			"target_file":        "src/hello_cli/cli.py",
			"failure_hypothesis": "main may ignore --name and default to World",
			"expected_behavior":  "main prints Hello, Alice! when --name Alice is passed",
			"input_strategy":     "invoke main with argv containing --name Alice",
		},
	}
	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "write_test", input); err != nil {
		t.Fatalf("structured write_test blocked: %v", err)
	}

	RecordTaskExecutionSuccess(ctx, "write_test", input)
	if err := ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err != nil {
		t.Fatalf("tester completion blocked after structured write evidence: %v", err)
	}
}

func TestValidateTaskExecutionCall_TesterBlocksFeedbackBeforeWriteOrExecution(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan, TaskDeliverableTestArtifact},
	})

	RecordTaskExecutionSuccess(ctx, "check_inspector_gate", nil)
	RecordTaskExecutionSuccess(ctx, "detect_test_harness", nil)
	RecordTaskExecutionSuccess(ctx, "analyze_risk", nil)
	RecordTaskExecutionSuccess(ctx, "plan_tests", nil)

	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "report_to_engineer", nil); err == nil {
		t.Fatal("expected report_to_engineer to be blocked after planning without write or execution")
	}
	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "coord_release_scope", nil); err == nil {
		t.Fatal("expected coord_release_scope to be blocked after planning without write or execution")
	}

	RecordTaskExecutionSuccess(ctx, "write_test", map[string]any{"output_file": "tests/test_cli.py"})

	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "report_to_engineer", nil); err != nil {
		t.Fatalf("report_to_engineer blocked after write_test: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "coord_release_scope", nil); err != nil {
		t.Fatalf("coord_release_scope blocked after write_test: %v", err)
	}
}

func TestValidateTaskExecutionCall_TesterBlocksVerificationArtifactBeforeEvidence(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan, TaskDeliverableTestArtifact, TaskDeliverableSuiteExecution},
	})

	RecordTaskExecutionSuccess(ctx, "check_inspector_gate", nil)
	RecordTaskExecutionSuccess(ctx, "detect_test_harness", nil)
	RecordTaskExecutionSuccess(ctx, "analyze_risk", nil)
	RecordTaskExecutionSuccess(ctx, "plan_tests", nil)

	err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "coord_publish_artifact", map[string]any{"kind": "verification_result"})
	if err == nil {
		t.Fatal("expected verification_result artifact publication to be blocked before write or execution evidence")
	}
	if !strings.Contains(err.Error(), "verification_result") {
		t.Fatalf("unexpected error: %v", err)
	}

	RecordTaskExecutionSuccess(ctx, "write_test", map[string]any{"output_file": "tests/test_cli.py"})
	RecordTaskExecutionSuccess(ctx, "run_test_suite", nil)

	if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", "coord_publish_artifact", map[string]any{"kind": "verification_result"}); err != nil {
		t.Fatalf("verification_result artifact blocked after evidence: %v", err)
	}
}

func TestValidateTaskExecutionCompletion_TesterBlocksPrematureFinalizeAfterPlan(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan, TaskDeliverableTestArtifact},
	})

	RecordTaskExecutionSuccess(ctx, "check_inspector_gate", nil)
	RecordTaskExecutionSuccess(ctx, "detect_test_harness", nil)
	RecordTaskExecutionSuccess(ctx, "analyze_risk", nil)
	RecordTaskExecutionSuccess(ctx, "plan_tests", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err == nil {
		t.Fatal("expected tester completion to be blocked after plan_tests without write or execution")
	}

	RecordTaskExecutionSuccess(ctx, "write_test", map[string]any{"output_file": "tests/test_cli.py"})

	if err := ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err != nil {
		t.Fatalf("tester completion blocked after write_test: %v", err)
	}
}

func TestValidateTaskExecutionCompletion_TesterAllowsPlanOnlyCompletion(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:        "test",
		Deliverables: []TaskExecutionDeliverable{TaskDeliverableTestPlan},
	})

	RecordTaskExecutionSuccess(ctx, "plan_tests", nil)

	if err := ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err != nil {
		t.Fatalf("plan-only tester completion blocked: %v", err)
	}
}

func TestValidateTaskExecutionCall_DesignerRequiresPlanningByRequestedOperation(t *testing.T) {
	contract := &TaskExecutionContract{
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/ui/Button.tsx", Operation: "create"},
			{Path: "src/ui/Card.tsx", Operation: "modify"},
		},
	}
	ctx := taskExecutionContext(contract)

	if err := ValidateTaskExecutionCall(ctx, "designer", "write_pipeline_file", map[string]any{"path": "src/ui/Button.tsx"}); err == nil {
		t.Fatal("expected create path write to require component_create")
	}
	RecordTaskExecutionSuccess(ctx, "component_create", nil)
	if err := ValidateTaskExecutionCall(ctx, "designer", "write_pipeline_file", map[string]any{"path": "src/ui/Button.tsx"}); err != nil {
		t.Fatalf("write_pipeline_file blocked after component_create: %v", err)
	}

	if err := ValidateTaskExecutionCall(ctx, "designer", "edit_pipeline_file", map[string]any{"path": "src/ui/Card.tsx"}); err == nil {
		t.Fatal("expected modify path edit to require component_modify")
	}
	RecordTaskExecutionSuccess(ctx, "component_modify", nil)
	if err := ValidateTaskExecutionCall(ctx, "designer", "edit_pipeline_file", map[string]any{"path": "src/ui/Card.tsx"}); err != nil {
		t.Fatalf("edit_pipeline_file blocked after component_modify: %v", err)
	}
}

func TestValidateTaskExecutionCall_EngineerAllowsCreateButRequiresReadForModify(t *testing.T) {
	contract := &TaskExecutionContract{
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/hello_cli/cli.py", Operation: "create"},
			{Path: "src/hello_cli/config.py", Operation: "modify"},
		},
	}
	ctx := taskExecutionContext(contract)

	if err := ValidateTaskExecutionCall(ctx, "engineer", "write_pipeline_file", map[string]any{"path": "src/hello_cli/cli.py"}); err != nil {
		t.Fatalf("create path should be writable without prior read: %v", err)
	}

	if err := ValidateTaskExecutionCall(ctx, "engineer", "edit_pipeline_file", map[string]any{"path": "src/hello_cli/config.py"}); err == nil {
		t.Fatal("expected modify path edit to require prior read")
	}
	RecordTaskExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "src/hello_cli/config.py"})
	if err := ValidateTaskExecutionCall(ctx, "engineer", "edit_pipeline_file", map[string]any{"path": "src/hello_cli/config.py"}); err != nil {
		t.Fatalf("edit_pipeline_file blocked after read: %v", err)
	}
}

func taskExecutionContext(contract *TaskExecutionContract) context.Context {
	ctx := context.Background()
	ctx = WithTaskExecutionContract(ctx, contract)
	ctx = WithTaskExecutionState(ctx, NewTaskExecutionState())
	return ctx
}

func hasIntent(intents []TaskExecutionIntent, want TaskExecutionIntent) bool {
	for _, intent := range intents {
		if intent == want {
			return true
		}
	}
	return false
}

func hasDeliverable(deliverables []TaskExecutionDeliverable, want TaskExecutionDeliverable) bool {
	for _, deliverable := range deliverables {
		if deliverable == want {
			return true
		}
	}
	return false
}
