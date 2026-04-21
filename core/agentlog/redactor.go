package agentlog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Redactor strips sensitive values from strings and nested structures
// before they are persisted to disk. Implementations must be safe for
// concurrent use.
type Redactor interface {
	RedactString(s string) string
	RedactValue(v any) any
}

// RedactorConfig controls redactor construction. Empty config yields a
// redactor with only the built-in secret patterns; ChainRedactor always
// applies the pattern layer.
type RedactorConfig struct {
	EnvVars            []string
	FieldNames         []string
	ExtraPatterns      []string
	IncludeFingerprint bool
}

// NewRedactor returns the canonical three-layer chain: built-in +
// user-supplied patterns, env-value snapshot, field-name denylist.
// Env-value snapshot captures the values of cfg.EnvVars at call time —
// subsequent changes to the process environment are not picked up.
func NewRedactor(cfg RedactorConfig, getEnv func(string) string) Redactor {
	if getEnv == nil {
		getEnv = func(string) string { return "" }
	}
	patterns := builtinSecretPatterns()
	for _, raw := range cfg.ExtraPatterns {
		if re, err := regexp.Compile(raw); err == nil {
			patterns = append(patterns, re)
		}
	}

	snapshot := make(map[string]string, len(cfg.EnvVars))
	for _, name := range cfg.EnvVars {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if v := strings.TrimSpace(getEnv(name)); v != "" {
			snapshot[name] = v
		}
	}

	fieldSet := make(map[string]struct{}, len(cfg.FieldNames))
	for _, f := range cfg.FieldNames {
		if f = strings.TrimSpace(strings.ToLower(f)); f != "" {
			fieldSet[f] = struct{}{}
		}
	}

	return &chainRedactor{
		patterns:           patterns,
		envValues:          snapshot,
		fieldNames:         fieldSet,
		includeFingerprint: cfg.IncludeFingerprint,
	}
}

// chainRedactor implements the three-layer pipeline: patterns on raw
// strings, env-value substitution, field-name scrubbing on structured
// data. Exactly one concrete Redactor is exposed so consumers don't
// have to compose multiple layers themselves.
type chainRedactor struct {
	patterns           []*regexp.Regexp
	envValues          map[string]string
	fieldNames         map[string]struct{}
	includeFingerprint bool
}

// RedactString applies the pattern and env-value layers to a string.
// Field-name redaction does not apply at this level (no structure to
// inspect); callers with structured data should use RedactValue.
func (c *chainRedactor) RedactString(s string) string {
	if s == "" {
		return s
	}
	// Env-value substitution first: these are literal known secrets,
	// so we can replace with fingerprinted placeholders without a
	// regex scan of the string for every env var.
	for _, v := range c.envValues {
		if len(v) < minSecretLen {
			continue
		}
		s = strings.ReplaceAll(s, v, c.placeholder(v))
	}
	// Pattern layer: regex over the remaining text. Each pattern
	// replaces full matches with fingerprinted placeholders.
	for _, re := range c.patterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			return c.placeholder(match)
		})
	}
	return s
}

// RedactValue walks any (map/slice/string) and redacts nested values.
// Struct fields are not redacted by reflection — consumers should pass
// map[string]any or JSON-serialized structures. Unknown scalar types
// are returned unchanged; strings are redacted; maps have their values
// redacted (and keys in FieldNames replaced entirely).
func (c *chainRedactor) RedactValue(v any) any {
	return c.redact(v, 0)
}

func (c *chainRedactor) redact(v any, depth int) any {
	if depth > maxRedactDepth {
		return v
	}
	switch typed := v.(type) {
	case nil:
		return nil
	case string:
		return c.RedactString(typed)
	case []byte:
		return c.RedactString(string(typed))
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			if c.isSensitiveField(k) {
				// Preserve the key, replace the value. Fingerprint
				// the stringified value so the same secret is still
				// correlatable across records without being exposed.
				out[k] = c.placeholder(stringify(val))
				continue
			}
			out[k] = c.redact(val, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, val := range typed {
			out[i] = c.redact(val, depth+1)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		for i, s := range typed {
			out[i] = c.RedactString(s)
		}
		return out
	default:
		return v
	}
}

func (c *chainRedactor) isSensitiveField(name string) bool {
	if len(c.fieldNames) == 0 {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := c.fieldNames[name]; ok {
		return true
	}
	// Common camelCase / snake_case variants: treat any field whose
	// lowercase name contains an exact denylist entry as sensitive.
	// This catches "AuthHeader", "api_key_header", etc. without
	// requiring exhaustive enumeration.
	for f := range c.fieldNames {
		if strings.Contains(name, f) {
			return true
		}
	}
	return false
}

// placeholder returns the redaction marker, optionally with a short
// fingerprint so distinct secrets remain distinguishable across
// records without revealing the plaintext.
func (c *chainRedactor) placeholder(secret string) string {
	if !c.includeFingerprint || len(secret) == 0 {
		return redactionMarker
	}
	sum := sha256.Sum256([]byte(secret))
	return redactionMarker[:len(redactionMarker)-1] + ":" + hex.EncodeToString(sum[:3]) + "]"
}

// --- built-ins ---

const (
	redactionMarker = "[REDACTED]"
	minSecretLen    = 8
	maxRedactDepth  = 16
)

var (
	builtinPatternsOnce sync.Once
	builtinPatterns     []*regexp.Regexp
)

// builtinSecretPatterns returns the compiled regexes for well-known
// secret shapes. Patterns are sized so they only match plausible
// secret lengths; each pattern returns a full-match replacement.
func builtinSecretPatterns() []*regexp.Regexp {
	builtinPatternsOnce.Do(func() {
		raw := []string{
			// Anthropic API keys
			`sk-ant-[A-Za-z0-9_\-]{20,}`,
			// OpenAI API keys (new and legacy formats)
			`sk-(?:proj-)?[A-Za-z0-9_\-]{20,}`,
			// AWS access keys
			`AKIA[0-9A-Z]{16}`,
			`ASIA[0-9A-Z]{16}`,
			// GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_)
			`gh[pousr]_[A-Za-z0-9_]{36,}`,
			// JWTs (three base64 segments separated by dots)
			`ey[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`,
			// Bearer header
			`(?i)bearer\s+[A-Za-z0-9._\-]{16,}`,
			// Basic auth header
			`(?i)basic\s+[A-Za-z0-9+/=]{16,}`,
		}
		builtinPatterns = make([]*regexp.Regexp, 0, len(raw))
		for _, p := range raw {
			if re, err := regexp.Compile(p); err == nil {
				builtinPatterns = append(builtinPatterns, re)
			}
		}
	})
	out := make([]*regexp.Regexp, len(builtinPatterns))
	copy(out, builtinPatterns)
	return out
}

// stringify produces a best-effort string form of an arbitrary value
// for fingerprinting purposes. Non-strings are rendered with their
// Go default format.
func stringify(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
