package shared

import (
	"context"
	"fmt"
	"strings"
)

type taskExecutionCallValidator func(*TaskExecutionContract, *TaskExecutionState, string, map[string]any) error
type taskExecutionCompletionValidator func(*TaskExecutionContract, *TaskExecutionState) error

var (
	taskExecutionCallValidatorSet = map[string]taskExecutionCallValidator{
		"designer":           validateDesignerTaskCall,
		"engineer":           validateEngineerTaskCall,
		"inspector-pipeline": validatePipelineInspectorCallAdapter,
		"tester-pipeline":    validatePipelineTesterCallAdapter,
	}
	taskExecutionCompletionValidatorSet = map[string]taskExecutionCompletionValidator{
		"designer":           validateDesignerCompletion,
		"engineer":           validateEngineerCompletion,
		"inspector-pipeline": validatePipelineInspectorCompletion,
		"tester-pipeline":    validatePipelineTesterCompletion,
	}
)

func ValidateTaskExecutionCall(ctx context.Context, role, toolName string, input map[string]any) error {
	contract := TaskExecutionContractFromContext(ctx)
	state := TaskExecutionStateFromContext(ctx)
	if contract == nil || state == nil {
		return nil
	}
	validator, ok := taskExecutionCallValidatorSet[role]
	if !ok {
		return nil
	}
	return validator(contract, state, toolName, input)
}

func ValidateTaskExecutionCompletion(ctx context.Context, role string) error {
	contract := TaskExecutionContractFromContext(ctx)
	state := TaskExecutionStateFromContext(ctx)
	if contract == nil || state == nil {
		return nil
	}
	validator, ok := taskExecutionCompletionValidatorSet[role]
	if !ok {
		return nil
	}
	return validator(contract, state)
}

func RecordTaskExecutionSuccess(ctx context.Context, toolName string, input map[string]any, output string) {
	state := TaskExecutionStateFromContext(ctx)
	if state == nil {
		return
	}
	state.recordSuccess(toolName, input, output)
}

func validateEngineerTaskCall(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	target := extractTaskToolPath(toolName, input)
	operation := contract.OperationForPath(target)
	if !requiresReadBeforeMutation(toolName, operation) {
		return nil
	}
	if state.sawReadPath(target) {
		return nil
	}
	return fmt.Errorf("%s targets requested %s path %s; inspect that path before mutating it", toolName, operation, target)
}

func validateDesignerTaskCall(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	target := extractTaskToolPath(toolName, input)
	operation := contract.OperationForPath(target)
	if !requiresReadBeforeMutation(toolName, operation) {
		return nil
	}
	if state.sawReadPath(target) {
		return nil
	}
	return fmt.Errorf("%s targets requested %s path %s; inspect that path before mutating it", toolName, operation, target)
}

func validateEngineerCompletion(_ *TaskExecutionContract, _ *TaskExecutionState) error {
	return nil
}

func validateDesignerCompletion(_ *TaskExecutionContract, _ *TaskExecutionState) error {
	return nil
}

func validatePipelineTesterTaskCall(_ *TaskExecutionContract, _ *TaskExecutionState, _ string, _ map[string]any) error {
	return nil
}

func validatePipelineTesterCompletion(_ *TaskExecutionContract, _ *TaskExecutionState) error {
	return nil
}

func validatePipelineInspectorCompletion(_ *TaskExecutionContract, _ *TaskExecutionState) error {
	return nil
}

func validatePipelineInspectorTaskCall(
	_ *TaskExecutionContract,
	_ *TaskExecutionState,
	toolName string,
	input map[string]any,
) error {
	return validatePipelineInspectorReadOnlyCall(toolName, input)
}

func requiresReadBeforeMutation(toolName, operation string) bool {
	return mutationToolRequiresRead(toolName) && operationRequiresReadBeforeMutation(operation)
}

func validatePipelineInspectorReadOnlyCall(toolName string, input map[string]any) error {
	if !isInspectorWorkspaceMutationTool(toolName) {
		return nil
	}
	target := extractTaskToolPath(toolName, input)
	if target == "" {
		target = "<workspace>"
	}
	return fmt.Errorf("%s is not permitted for pipeline inspector; inspectors must not implement or mutate %s and should publish coordination artifacts instead", toolName, target)
}

func mutationToolRequiresRead(toolName string) bool {
	switch toolName {
	case "edit_pipeline_file", "delete_pipeline_file", "write_pipeline_file":
		return true
	default:
		return false
	}
}

func operationRequiresReadBeforeMutation(operation string) bool {
	return operation == "modify" || operation == "delete"
}

func validatePipelineInspectorCallAdapter(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	return validatePipelineInspectorTaskCall(contract, state, toolName, input)
}

func validatePipelineTesterCallAdapter(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	return validatePipelineTesterTaskCall(contract, state, toolName, input)
}

func isInspectorWorkspaceMutationTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "prepare_pipeline_write_context",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"write_test":
		return true
	default:
		return false
	}
}
