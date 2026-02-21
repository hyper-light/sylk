package redact

import (
	"errors"
	"regexp"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{16,}`),
	regexp.MustCompile(`(?i)(access_token|refresh_token|id_token)\s*[:=]\s*["']?[A-Za-z0-9._-]{8,}`),
	regexp.MustCompile(`(?i)([?&]code=)[^&\s]+`),
}

// Text redacts secrets while preserving surrounding whitespace/formatting.
func Text(input string) string {
	redacted := input
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllString(redacted, "[REDACTED]")
	}
	return redacted
}

// Error returns a redacted error while preserving non-secret errors as-is.
func Error(err error) error {
	if err == nil {
		return nil
	}
	redacted := Text(err.Error())
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}
