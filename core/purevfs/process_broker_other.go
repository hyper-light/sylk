//go:build !linux && !darwin && !windows

package purevfs

import "context"

func isWindowsPlatform() bool {
	return false
}

func platformExecutionCapabilities() ExecutionCapabilities {
	return ExecutionCapabilities{}
}

func strictExecutionProbe() error {
	return ErrStrictExecutionUnavailable
}

func mountExecutionRoot(context.Context, BrokerRunRequest, *projectedRoot) (mountedExecutionRoot, error) {
	return nil, ErrStrictExecutionUnavailable
}

func runMountedExecution(context.Context, BrokerRunRequest, string, *brokerMemoryGuard) (*BrokerRunResult, error) {
	return nil, ErrStrictExecutionUnavailable
}
