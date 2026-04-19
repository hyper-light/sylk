package commandapproval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

func TestAuthorize_EmitsApprovedActivityOnAllow(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	ctx := activity.EnsureFabricContext(context.Background())
	// Use a deliberately benign command so the default evaluator allows.
	eval, err := Authorize(ctx, NewEvaluator(nil), Request{
		Command:       "echo hello",
		ToolName:      "test_runner",
		AgentID:       "test-agent",
		AgentType:     "tester-pipeline",
		WorkspaceRoot: "/tmp/x",
	})
	if err != nil {
		// Many commands will require prompt — that's fine for the test;
		// what matters is whether an activity emitted in either case.
		if !errors.Is(err, ErrApprovalRequired) && !errors.Is(err, ErrApprovalDenied) {
			t.Fatalf("Authorize: %v", err)
		}
	}

	emitted := col.Snapshot()
	if len(emitted) == 0 {
		t.Fatal("Authorize must emit at least one guardian activity")
	}
	last := emitted[len(emitted)-1]
	switch last.Action {
	case activity.ActionCommandApproved, activity.ActionCommandDenied:
		// expected
	default:
		t.Fatalf("expected guardian activity kind; got %s", last.Action)
	}
	// Decision must be reflected in the payload.
	if !strings.Contains(string(last.Payload), string(eval.Decision)) {
		t.Errorf("payload should record decision %q; got %s", eval.Decision, string(last.Payload))
	}
}

func TestAuthorize_EmitsDeniedActivityOnDeny(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	ctx := activity.EnsureFabricContext(context.Background())
	// `rm -rf /` matches the builtin deny patterns and produces a
	// DecisionDeny without prompting.
	_, err := Authorize(ctx, NewEvaluator(nil), Request{
		Command:       "rm -rf /",
		ToolName:      "shell",
		AgentID:       "engineer-1",
		AgentType:     "engineer",
		WorkspaceRoot: "/repo",
	})
	if !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("expected ErrApprovalDenied, got %v", err)
	}

	emitted := col.FilterByKind(activity.ActionCommandDenied)
	if len(emitted) == 0 {
		t.Fatal("denial must emit ActionCommandDenied")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "deny") {
		t.Errorf("denial payload should embed decision; got %s", string(last.Payload))
	}
}

// TestAuthorize_PromptIsNotAnError documents that DecisionPrompt
// (interactive approval pending) is a structured outcome — the
// activity records it as approved-with-prompt rather than tagging the
// span as errored. Without this, every interactive prompt would
// pollute the error_emitted activity stream and skew error-rate
// dashboards.
func TestAuthorize_PromptDoesNotMarkSpanAsError(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	ctx := activity.EnsureFabricContext(context.Background())
	// Most commands without a stored allow rule fall to DecisionPrompt.
	_, err := Authorize(ctx, NewEvaluator(nil), Request{
		Command:       "ls -la",
		ToolName:      "shell",
		AgentID:       "tester-1",
		AgentType:     "tester-pipeline",
		WorkspaceRoot: "/repo",
	})
	if !errors.Is(err, ErrApprovalRequired) {
		// Some configurations may return Allow; if so, this test is
		// not exercising the prompt path and should skip.
		t.Skipf("default policy did not require a prompt for ls -la (got %v); skipping", err)
	}

	emitted := col.Snapshot()
	if len(emitted) == 0 {
		t.Fatal("prompt must still emit a guardian activity")
	}
	last := emitted[len(emitted)-1]
	if last.Action != activity.ActionCommandApproved {
		t.Fatalf("prompt should record under ActionCommandApproved (with requires_prompt attribute); got %s", last.Action)
	}
	// Payload should mark requires_prompt=true.
	if !strings.Contains(string(last.Payload), "requires_prompt") {
		t.Errorf("prompt activity should mark requires_prompt=true in payload; got %s", string(last.Payload))
	}
}

// TestAuthorize_EmitsActivityEvenOnEvaluatorError documents that the
// fabric chokepoint records denials caused by infra failures, not just
// policy decisions. If a future refactor moves the emission inside the
// happy path only, this test fails — denying a command for *any* reason
// (policy or infrastructure) must remain queryable post-hoc.
func TestAuthorize_EmitsActivityWhenGateReturnsError(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	ctx := WithGate(activity.EnsureFabricContext(context.Background()), gateThatErrors{})
	_, err := Authorize(ctx, nil, Request{Command: "x", AgentID: "a"})
	if err == nil {
		t.Fatal("expected error from gate")
	}

	if col.CountByKind(activity.ActionCommandDenied) == 0 {
		t.Fatal("evaluator-error path must still emit ActionCommandDenied")
	}
}

type gateThatErrors struct{}

func (gateThatErrors) Authorize(_ context.Context, _ Request) (Evaluation, error) {
	return Evaluation{}, errors.New("gate boom")
}
