package correlation

import "errors"

// Configuration validation errors.
var (
	// ErrInvalidCorrelationWindow indicates the correlation window is negative.
	ErrInvalidCorrelationWindow = errors.New("correlation window must be non-negative")
	// ErrInvalidMinFailures indicates the minimum failures threshold is less than 1.
	ErrInvalidMinFailures = errors.New("minimum failures for global must be at least 1")
	// ErrInvalidBackoffBase indicates the base backoff is negative.
	ErrInvalidBackoffBase = errors.New("global backoff base must be non-negative")
	// ErrInvalidBackoffMax indicates the max backoff is less than base backoff.
	ErrInvalidBackoffMax = errors.New("global backoff max must be greater than or equal to base")
	// ErrInvalidRecoveryRampUp indicates the recovery ramp up is out of range.
	ErrInvalidRecoveryRampUp = errors.New("recovery ramp up must be between 0 and 1")
)
