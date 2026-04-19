package purevfs

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
)

// InstrumentBroker wraps any ExecutionBroker with Activity Fabric
// chokepoint instrumentation. Every Run call emits a command_executed
// activity (Fine resolution) carrying argv, exit code, duration, and
// scope. Every Capabilities call is captured implicitly via FabricContext
// propagation (the broker may downstream-emit other activities; this
// wrapper does not cover that).
//
// Idempotent — wrapping an already-instrumented broker returns the
// same instance.
func InstrumentBroker(b ExecutionBroker) ExecutionBroker {
	if b == nil {
		return nil
	}
	if _, already := b.(*instrumentedBroker); already {
		return b
	}
	return &instrumentedBroker{wrapped: b}
}

// UnderlyingBroker strips any chain of fabric-instrumentation wrappers
// and returns the innermost ExecutionBroker. Callers that type-assert
// for capability detection should call this first so the wrapper is
// transparent.
func UnderlyingBroker(b ExecutionBroker) ExecutionBroker {
	for {
		w, ok := b.(*instrumentedBroker)
		if !ok || w == nil {
			return b
		}
		b = w.wrapped
	}
}

type instrumentedBroker struct {
	wrapped ExecutionBroker
}

func (b *instrumentedBroker) Capabilities(ctx context.Context) (ExecutionCapabilities, error) {
	// Capability probes are not high-signal — pass through without an
	// extra span. Capability calls happen frequently and don't carry
	// causal weight; instrumenting them would dilute the broker
	// activity stream.
	return b.wrapped.Capabilities(ctx)
}

func (b *instrumentedBroker) Run(ctx context.Context, req BrokerRunRequest) (*BrokerRunResult, error) {
	binary := ""
	if len(req.Argv) > 0 {
		binary = req.Argv[0]
	}
	span := activity.StartSpan(ctx, activity.ActionCommandExecuted, activity.Subject{
		PathPrefix:     req.Plan.WorkingDir,
		TargetArtifact: binary,
	})
	span.SetAttribute("argv", strings.Join(req.Argv, " "))
	span.SetAttribute("argc", len(req.Argv))
	span.SetAttribute("language", req.Plan.Language)
	span.SetAttribute("framework_id", req.Plan.FrameworkID)
	span.SetAttribute("intent", string(req.Plan.Intent))
	defer func() { span.End() }()

	result, err := b.wrapped.Run(span.Context(), req)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	if result != nil {
		span.SetAttribute("exit_code", result.ExitCode)
		span.SetAttribute("stdout_bytes", len(result.Stdout))
		span.SetAttribute("stderr_bytes", len(result.Stderr))
		span.SetAttribute("stdout_truncated", result.StdoutTruncated)
		span.SetAttribute("stderr_truncated", result.StderrTruncated)
	}
	return result, nil
}

var _ ExecutionBroker = (*instrumentedBroker)(nil)
