package toolruntime

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

func TestClaimsBackendExecutesSurfaceAndBoundsOutput(t *testing.T) {
	surface := claimsToolSurface{allowed: true, output: "long output"}
	backend := NewClaimsBackend(ClaimsBackendConfig{Surface: surface, OutputSummaryLimit: len("long"), Clock: fixedClaimsToolClock})
	data, err := backend.HandleToolRuntimeExecution(context.Background(), claims.ToolRuntimeExecutionRequest{
		Call: claims.ExpectedToolCall{ID: "call-1", Tool: claims.ToolRuntimeToolExecute, Arguments: map[string]any{
			"tool_name": "go_test",
			"args":      map[string]any{"package": "./core/claims"},
		}},
		Claim:       &claims.Claim{ID: "claim-1", AgentID: "issuer", SessionID: "session"},
		Participant: claims.ParticipantRegistration{RouteKey: "sys:tool_runtime"},
		Requested:   claims.ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "allowed"},
	})
	if err != nil {
		t.Fatalf("HandleToolRuntimeExecution: %v", err)
	}
	if data.OutputSummary != "long" || data.ExitStatus != 0 || data.FailureReason != "" {
		t.Fatalf("tool data = %+v, want bounded success", data)
	}
}

func TestClaimsBackendSurfaceDenialProducesStructuredFailure(t *testing.T) {
	backend := NewClaimsBackend(ClaimsBackendConfig{Surface: claimsToolSurface{}, Clock: fixedClaimsToolClock})
	data, err := backend.HandleToolRuntimeExecution(context.Background(), claims.ToolRuntimeExecutionRequest{
		Call:  claims.ExpectedToolCall{ID: "call-1", Tool: claims.ToolRuntimeToolExecute, Arguments: map[string]any{"tool_name": "danger"}},
		Claim: &claims.Claim{ID: "claim-1", AgentID: "issuer", SessionID: "session"},
		Requested: claims.ToolRuntimeExecutionArtifactData{
			ToolName:       "danger",
			PolicyDecision: "allowed",
		},
	})
	if err != nil {
		t.Fatalf("HandleToolRuntimeExecution: %v", err)
	}
	if data.ExitStatus == 0 || data.FailureReason == "" {
		t.Fatalf("tool data = %+v, want structured denial failure", data)
	}
}

func TestClaimsBackendValidateAndQueryPolicyDoNotExecuteSurface(t *testing.T) {
	surface := &countingClaimsToolSurface{allowed: true}
	backend := NewClaimsBackend(ClaimsBackendConfig{Surface: surface, Clock: fixedClaimsToolClock})
	for _, tool := range []string{claims.ToolRuntimeToolValidateInvocation, claims.ToolRuntimeToolQueryPolicy} {
		data, err := backend.HandleToolRuntimeExecution(context.Background(), claims.ToolRuntimeExecutionRequest{
			Call: claims.ExpectedToolCall{ID: "call-" + tool, Tool: tool, Arguments: map[string]any{
				"tool_name": "go_test",
				"args":      map[string]any{"package": "./core/claims"},
			}},
			Claim:       &claims.Claim{ID: "claim-" + tool, AgentID: "issuer", SessionID: "session"},
			Participant: claims.ParticipantRegistration{RouteKey: "sys:tool_runtime"},
			Requested:   claims.ToolRuntimeExecutionArtifactData{ToolName: "go_test"},
		})
		if err != nil {
			t.Fatalf("%s HandleToolRuntimeExecution: %v", tool, err)
		}
		if data.FailureReason != "" || data.ExitStatus != 0 {
			t.Fatalf("%s data = %+v, want successful non-executing policy result", tool, data)
		}
	}
	if surface.executions != 0 {
		t.Fatalf("executions = %d, want validate/query policy to avoid execution", surface.executions)
	}
	if surface.validations != 1 {
		t.Fatalf("validations = %d, want validate_invocation only", surface.validations)
	}
	if surface.allowChecks != 2 {
		t.Fatalf("allow checks = %d, want both calls checked", surface.allowChecks)
	}
}

func TestClaimsBackendRequestedPolicyDenialDoesNotExecuteSurface(t *testing.T) {
	surface := &countingClaimsToolSurface{allowed: true}
	backend := NewClaimsBackend(ClaimsBackendConfig{Surface: surface, Clock: fixedClaimsToolClock})
	data, err := backend.HandleToolRuntimeExecution(context.Background(), claims.ToolRuntimeExecutionRequest{
		Call: claims.ExpectedToolCall{ID: "call-denied", Tool: claims.ToolRuntimeToolExecuteTool, Arguments: map[string]any{
			"tool_name":       "go_test",
			"args":            map[string]any{"package": "./core/claims"},
			"policy_decision": "denied",
			"failure_reason":  "policy denied",
		}},
		Claim:       &claims.Claim{ID: "claim-denied", AgentID: "issuer", SessionID: "session"},
		Participant: claims.ParticipantRegistration{RouteKey: "sys:tool_runtime"},
		Requested:   claims.ToolRuntimeExecutionArtifactData{ToolName: "go_test", PolicyDecision: "denied", FailureReason: "policy denied"},
	})
	if err != nil {
		t.Fatalf("HandleToolRuntimeExecution: %v", err)
	}
	if data.PolicyDecision != "denied" || data.ExitStatus == 0 || data.FailureReason != "policy denied" {
		t.Fatalf("tool data = %+v, want policy denial evidence", data)
	}
	if surface.executions != 0 || surface.validations != 0 {
		t.Fatalf("surface calls executions=%d validations=%d, want none", surface.executions, surface.validations)
	}
}

type claimsToolSurface struct {
	allowed bool
	output  string
}

func (s claimsToolSurface) AgentID() string { return "issuer" }

func (s claimsToolSurface) CapabilityScope() string { return "test" }

func (s claimsToolSurface) Allows(string) bool { return s.allowed }

func (s claimsToolSurface) SyncActiveFromLoaded() []string { return nil }

func (s claimsToolSurface) BuildToolDefinitions() []providers.Tool { return nil }

func (s claimsToolSurface) ValidateBatch([]Invocation) error { return nil }

func (s claimsToolSurface) Execute(context.Context, Invocation) (ExecutionResult, error) {
	return ExecutionResult{ToolName: "go_test", Output: s.output}, nil
}

func (s claimsToolSurface) ExecuteApproved(context.Context, Invocation, *GuardianControlGrant) (ExecutionResult, error) {
	return s.Execute(context.Background(), Invocation{})
}

func fixedClaimsToolClock() time.Time {
	return time.Unix(1, 0).UTC()
}

type countingClaimsToolSurface struct {
	allowed     bool
	executions  int
	validations int
	allowChecks int
}

func (s *countingClaimsToolSurface) AgentID() string { return "issuer" }

func (s *countingClaimsToolSurface) CapabilityScope() string { return "test" }

func (s *countingClaimsToolSurface) Allows(string) bool {
	s.allowChecks++
	return s.allowed
}

func (s *countingClaimsToolSurface) SyncActiveFromLoaded() []string { return nil }

func (s *countingClaimsToolSurface) BuildToolDefinitions() []providers.Tool { return nil }

func (s *countingClaimsToolSurface) ValidateBatch([]Invocation) error {
	s.validations++
	return nil
}

func (s *countingClaimsToolSurface) Execute(context.Context, Invocation) (ExecutionResult, error) {
	s.executions++
	return ExecutionResult{ToolName: "go_test", Output: "executed"}, nil
}

func (s *countingClaimsToolSurface) ExecuteApproved(context.Context, Invocation, *GuardianControlGrant) (ExecutionResult, error) {
	return s.Execute(context.Background(), Invocation{})
}
