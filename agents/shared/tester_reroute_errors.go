package shared

import "errors"

var (
	ErrRouteTestExecutionToTester = errors.New("route test execution to tester")
	ErrRouteTestToolingToTester   = errors.New("route test tooling to tester")
)
