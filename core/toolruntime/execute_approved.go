package toolruntime

import (
	"context"
)

// ExecuteApproved executes a previously authorized tool invocation using a
// Guardian-issued grant instead of requesting a fresh approval.
func (r *Runtime) ExecuteApproved(
	ctx context.Context,
	inv Invocation,
	grant *GuardianControlGrant,
) (ExecutionResult, error) {
	rawResult, err := r.ExecuteApprovedRaw(ctx, inv, grant)
	result := ExecutionResult{
		ToolName:        rawResult.ToolName,
		ToolDefsDirty:   rawResult.ToolDefsDirty,
		ActivatedSkills: append([]string(nil), rawResult.ActivatedSkills...),
		Status:          rawResult.Status,
		Continuation:    rawResult.Continuation,
	}
	if rawResult.Data != nil {
		if output, marshalErr := marshalOutput(rawResult.Data); marshalErr == nil {
			result.Output = output
		} else if err == nil {
			return ExecutionResult{}, marshalErr
		}
	}
	return result, err
}

// ExecuteApprovedRaw is the raw-data variant of ExecuteApproved.
func (r *Runtime) ExecuteApprovedRaw(
	ctx context.Context,
	inv Invocation,
	grant *GuardianControlGrant,
) (RawExecutionResult, error) {
	return r.executeRaw(ctx, inv, grant, nil, false)
}
