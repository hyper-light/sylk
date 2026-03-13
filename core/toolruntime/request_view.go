package toolruntime

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/providers"
)

// RequestView overlays transient request-scoped tool activation on top of the
// shared runtime state. It never mutates the shared active set.
type RequestView struct {
	runtime         *Runtime
	transientActive map[string]struct{}
	restricted      bool
}

func (v *RequestView) AgentID() string {
	if v == nil || v.runtime == nil {
		return ""
	}
	return v.runtime.AgentID()
}

func (v *RequestView) CapabilityScope() string {
	if v == nil || v.runtime == nil {
		return ""
	}
	return v.runtime.CapabilityScope()
}

func (v *RequestView) Allows(name string) bool {
	if v == nil || v.runtime == nil {
		return false
	}
	return v.runtime.Allows(name)
}

func (v *RequestView) SyncActiveFromLoaded() []string {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.syncActiveFromLoaded(v.transientActive, v.restricted)
}

func (v *RequestView) BuildToolDefinitions() []providers.Tool {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.buildToolDefinitions(v.transientActive, v.restricted)
}

func (v *RequestView) ValidateBatch(invocations []Invocation) error {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.ValidateBatch(invocations)
}

func (v *RequestView) Execute(ctx context.Context, inv Invocation) (ExecutionResult, error) {
	if v == nil || v.runtime == nil {
		return ExecutionResult{}, fmt.Errorf("tool runtime is not configured")
	}
	rawResult, err := v.ExecuteRaw(ctx, inv)
	result := ExecutionResult{
		ToolName:        rawResult.ToolName,
		ToolDefsDirty:   rawResult.ToolDefsDirty,
		ActivatedSkills: append([]string(nil), rawResult.ActivatedSkills...),
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

func (v *RequestView) ExecuteRaw(ctx context.Context, inv Invocation) (RawExecutionResult, error) {
	if v == nil || v.runtime == nil {
		return RawExecutionResult{}, fmt.Errorf("tool runtime is not configured")
	}
	return v.runtime.executeRaw(ctx, inv, nil, v.transientActive, v.restricted)
}

func (v *RequestView) ExecuteApproved(
	ctx context.Context,
	inv Invocation,
	grant *GuardianControlGrant,
) (ExecutionResult, error) {
	rawResult, err := v.ExecuteApprovedRaw(ctx, inv, grant)
	result := ExecutionResult{
		ToolName:        rawResult.ToolName,
		ToolDefsDirty:   rawResult.ToolDefsDirty,
		ActivatedSkills: append([]string(nil), rawResult.ActivatedSkills...),
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

func (v *RequestView) ExecuteApprovedRaw(
	ctx context.Context,
	inv Invocation,
	grant *GuardianControlGrant,
) (RawExecutionResult, error) {
	if v == nil || v.runtime == nil {
		return RawExecutionResult{}, fmt.Errorf("tool runtime is not configured")
	}
	return v.runtime.executeRaw(ctx, inv, grant, v.transientActive, v.restricted)
}
