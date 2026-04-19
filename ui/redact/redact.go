package redact

import (
	"errors"
	"sync"

	"github.com/adalundhe/sylk/core/security"
)

// UI-09: route UI-visible redaction through the canonical
// core/security pattern library instead of the tiny package-local
// regex set. The core library carries ~30 secret patterns (AWS,
// GitHub, Stripe, OpenAI, Anthropic, OpenSSH, connection strings,
// JWT, etc.) and the same SecretSanitizer the indexer uses for
// on-disk redaction — having the UI render a different subset
// creates a disclosure gap where a secret caught during index time
// survives into chat display.
//
// The sanitizer is a process-wide singleton: it's stateless with
// respect to per-message input (no memoization on content), its
// pattern table is effectively immutable after startup, and the
// concurrency guards inside SecretSanitizer (RWMutex around
// patterns + atomic counters) make it safe for fan-in from every
// chat stream goroutine at once.
var (
	sanitizerOnce sync.Once
	sanitizer     *security.SecretSanitizer
)

func getSanitizer() *security.SecretSanitizer {
	sanitizerOnce.Do(func() {
		sanitizer = security.NewSecretSanitizer()
	})
	return sanitizer
}

// Text redacts secrets while preserving surrounding whitespace and
// formatting. Non-secret content is returned unchanged. Contract is
// deliberately simple — callers drop the result straight into chat
// rows, stream chunks, error strings, etc.
func Text(input string) string {
	if input == "" {
		return ""
	}
	redacted, _ := getSanitizer().SanitizeString(input)
	return redacted
}

// Error returns a redacted error. If the underlying message carried
// no secret, the original err is returned by identity so callers can
// still type-assert against sentinels (errors.Is behavior preserved).
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
