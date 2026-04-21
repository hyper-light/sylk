package agentlog

import (
	"strings"
	"testing"
)

func TestRedactor_AnthropicKeyPattern(t *testing.T) {
	r := NewRedactor(RedactorConfig{IncludeFingerprint: false}, nil)
	in := "authorization: sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890"
	got := r.RedactString(in)
	if strings.Contains(got, "sk-ant-api03") {
		t.Fatalf("anthropic key leaked through redactor: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker in output: %q", got)
	}
}

func TestRedactor_FingerprintDistinguishesSecrets(t *testing.T) {
	r := NewRedactor(RedactorConfig{IncludeFingerprint: true}, nil)
	a := r.RedactString("sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaa")
	b := r.RedactString("sk-ant-api03-bbbbbbbbbbbbbbbbbbbbbbbb")
	if a == b {
		t.Fatalf("fingerprinted redactions collided: %q / %q", a, b)
	}
	if !strings.HasPrefix(a, "[REDACTED:") || !strings.HasPrefix(b, "[REDACTED:") {
		t.Fatalf("fingerprint format changed: %q / %q", a, b)
	}
}

func TestRedactor_EnvValueSnapshot(t *testing.T) {
	secret := "correct-horse-battery-staple-12345"
	getEnv := func(name string) string {
		if name == "SYLK_TEST_SECRET" {
			return secret
		}
		return ""
	}
	r := NewRedactor(RedactorConfig{
		EnvVars:            []string{"SYLK_TEST_SECRET"},
		IncludeFingerprint: false,
	}, getEnv)
	got := r.RedactString("connection string: postgres://user:" + secret + "@host/db")
	if strings.Contains(got, secret) {
		t.Fatalf("env value leaked: %q", got)
	}
}

func TestRedactor_FieldNameDenylist(t *testing.T) {
	r := NewRedactor(RedactorConfig{
		FieldNames:         []string{"api_key", "authorization"},
		IncludeFingerprint: false,
	}, nil)
	in := map[string]any{
		"body":          "hello world",
		"api_key":       "plainvalue",
		"Authorization": "bearer xyz",
		"nested": map[string]any{
			"inner_token": "secret",
		},
	}
	out, ok := r.RedactValue(in).(map[string]any)
	if !ok {
		t.Fatal("expected map output")
	}
	if out["api_key"] == "plainvalue" {
		t.Fatalf("api_key not redacted: %#v", out["api_key"])
	}
	if out["Authorization"] == "bearer xyz" {
		t.Fatalf("Authorization not redacted: %#v", out["Authorization"])
	}
	if out["body"] != "hello world" {
		t.Fatalf("non-sensitive field altered: %#v", out["body"])
	}
}

func TestRedactor_NestedStructures(t *testing.T) {
	r := NewRedactor(RedactorConfig{IncludeFingerprint: false}, nil)
	in := map[string]any{
		"list": []any{
			"clean text",
			"sk-ant-api03-zzzzzzzzzzzzzzzzzzzzzzzz",
		},
		"nested": map[string]any{
			"quote": "bearer eyJabc.def.ghi.jklmnopqr",
		},
	}
	out := r.RedactValue(in).(map[string]any)
	list := out["list"].([]any)
	if list[0] != "clean text" {
		t.Fatalf("clean text got modified: %v", list[0])
	}
	if s, _ := list[1].(string); !strings.Contains(s, "[REDACTED]") {
		t.Fatalf("nested list secret not redacted: %v", list[1])
	}
}

func TestRedactor_NoopForNil(t *testing.T) {
	r := NewRedactor(RedactorConfig{}, nil)
	if got := r.RedactString(""); got != "" {
		t.Fatalf("empty string returned: %q", got)
	}
	if got := r.RedactValue(nil); got != nil {
		t.Fatalf("nil value altered: %v", got)
	}
}
