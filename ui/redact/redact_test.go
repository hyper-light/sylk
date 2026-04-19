package redact

import (
	"errors"
	"testing"
)

func TestText_RedactsSecretsWithoutTrimming(t *testing.T) {
	input := " token=sk-abcdefghijklmnopqrstuvwxyz123456 \n"
	got := Text(input)
	if got == input {
		t.Fatalf("expected redaction, got %q", got)
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("expected trailing newline to be preserved, got %q", got)
	}
}

func TestError_RedactsWhenNeeded(t *testing.T) {
	err := Error(errors.New("bearer abcdefghijklmnopqrstuvwxyz1234"))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() == "bearer abcdefghijklmnopqrstuvwxyz1234" {
		t.Fatalf("expected redaction, got %q", err.Error())
	}
}

// TestText_RedactsCoreSecurityPatterns is the UI-09 acceptance
// test: the UI redactor must now catch the canonical pattern set
// from core/security (AWS, GitHub, Anthropic, Stripe, connection
// strings, RSA private keys). Before UI-09 the UI used a 4-pattern
// local subset that missed every one of these.
//
// Each subcase feeds a known-secret fragment into Text and asserts
// the raw fragment does not survive. We do not assert the exact
// replacement string to avoid coupling to DefaultRedactText; we
// just check that the literal secret is no longer present.
func TestText_RedactsCoreSecurityPatterns(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		secret  string
	}{
		{"aws_access_key", "creds: AKIAIOSFODNN7EXAMPLE stored", "AKIAIOSFODNN7EXAMPLE"},
		{"github_pat", "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"stripe_live", "key=sk_live_abcdefghijklmnopqrstuvwx", "sk_live_abcdefghijklmnopqrstuvwx"},
		{"anthropic_key", "auth: sk-ant-" + string_of_length(93), "sk-ant-" + string_of_length(93)},
		{"rsa_private_key", "-----BEGIN RSA PRIVATE KEY-----", "-----BEGIN RSA PRIVATE KEY-----"},
		{"postgres_connection", "db: postgres://user:password@host:5432/db", "postgres://user:password@host:5432/db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Text(tc.input)
			if got == tc.input {
				t.Fatalf("%s: expected redaction, got unchanged: %q", tc.name, got)
			}
			if contains(got, tc.secret) {
				t.Errorf("%s: secret survived redaction: %q (redacted = %q)", tc.name, tc.secret, got)
			}
		})
	}
}

// TestText_NonSecretInputReturnsUnchanged verifies the idempotence
// of the no-op path: ordinary chat text, code snippets, error
// messages without secret content must not be modified. A
// regression that over-matches (e.g. treating "api" as a hint) would
// corrupt every chat response.
func TestText_NonSecretInputReturnsUnchanged(t *testing.T) {
	cases := []string{
		"Hello, world!",
		"func main() { fmt.Println(\"hi\") }",
		"The user wants to refactor the api handler.",
		"connection refused: tcp 127.0.0.1:5432",
		"",
	}
	for _, input := range cases {
		if got := Text(input); got != input {
			t.Errorf("non-secret input mutated: input=%q, got=%q", input, got)
		}
	}
}

// TestError_NilReturnsNil protects the nil-pass-through contract —
// call sites use redact.Error eagerly on any error without checking
// for nil, and a panic or non-nil return here would break error
// bubbling all the way up to the chat pipeline.
func TestError_NilReturnsNil(t *testing.T) {
	if got := Error(nil); got != nil {
		t.Errorf("Error(nil) = %v, want nil", got)
	}
}

// contains + string_of_length are tiny helpers to avoid pulling
// strings/fmt into test scope for two call sites. Keep local.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// string_of_length returns a string of exactly n hyphen-safe chars,
// used to build fake anthropic keys whose pattern demands 93 chars.
func string_of_length(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + (i % 26))
	}
	return string(buf)
}
