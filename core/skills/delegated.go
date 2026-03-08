package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrDelegatedRequested is a sentinel error returned when a tool hands work
// off to another agent or control-plane path and the caller should stop its
// local loop without treating the operation as a failure.
var ErrDelegatedRequested = errors.New("delegated operation requested")

type delegatedError struct {
	payload any
	message string
}

func (e *delegatedError) Error() string {
	if msg := strings.TrimSpace(e.message); msg != "" {
		return msg
	}
	return ErrDelegatedRequested.Error()
}

func (e *delegatedError) Unwrap() error {
	return ErrDelegatedRequested
}

func (e *delegatedError) DelegatedPayload() any {
	if e == nil {
		return nil
	}
	return e.payload
}

func (e *delegatedError) DelegatedMessage() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.message)
}

// NewDelegatedError returns an error that preserves a machine-readable payload
// for the caller while still behaving like a normal sentinel error.
func NewDelegatedError(payload any, message string) error {
	return &delegatedError{
		payload: payload,
		message: strings.TrimSpace(message),
	}
}

// DelegatedPayload extracts the structured payload attached to a delegated
// sentinel error.
func DelegatedPayload(err error) (any, bool) {
	if err == nil {
		return nil, false
	}
	var carrier interface{ DelegatedPayload() any }
	if !errors.As(err, &carrier) {
		return nil, false
	}
	return carrier.DelegatedPayload(), true
}

// DelegatedMessage extracts the user-facing message attached to a delegated
// sentinel error, if present.
func DelegatedMessage(err error) string {
	if err == nil {
		return ""
	}
	var carrier interface{ DelegatedMessage() string }
	if !errors.As(err, &carrier) {
		return ""
	}
	return strings.TrimSpace(carrier.DelegatedMessage())
}

// MarshalDelegatedPayload encodes the payload attached to a delegated error.
func MarshalDelegatedPayload(err error) (string, error) {
	payload, ok := DelegatedPayload(err)
	if !ok {
		return "", fmt.Errorf("delegated payload not found")
	}
	switch value := payload.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(value), nil
	}
	encoded, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return "", marshalErr
	}
	return string(encoded), nil
}
