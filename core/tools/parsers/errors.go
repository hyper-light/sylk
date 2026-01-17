package parsers

import (
	"errors"
	"fmt"
)

var (
	ErrEmptyToolPattern = errors.New("tool pattern cannot be empty")
	ErrNilTemplate      = errors.New("template cannot be nil")
)

type InvalidPatternError struct {
	Field string
	Err   error
}

func (e *InvalidPatternError) Error() string {
	return fmt.Sprintf("invalid pattern for field %q: %v", e.Field, e.Err)
}

type ValidationError struct {
	Field    string
	Expected any
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed for field %q: expected %v", e.Field, e.Expected)
}
