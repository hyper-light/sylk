package claims

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDiagnoseJSONTruncation_BalancedReturnsNil exercises the happy
// path: well-formed JSON arrays and objects pass through cleanly.
// The downstream json.Unmarshal handles shape errors; this helper
// must only fire when the structure is mid-emit.
func TestDiagnoseJSONTruncation_BalancedReturnsNil(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty array", "[]"},
		{"single object", `[{"title":"x"}]`},
		{"nested arrays and objects", `[{"title":"x","scope":[{"k":"v"}],"validations":[{"d":"x"}]}]`},
		{"escaped braces in strings", `[{"title":"the {brace}","key":"a]b[c"}]`},
		{"backslash-escaped quote", `[{"title":"line1\"line2"}]`},
		{"deeply nested", `[[[[[]]]]]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := diagnoseJSONTruncation("test", "claims_json", json.RawMessage(tc.raw)); err != nil {
				t.Fatalf("expected nil error for balanced %q, got: %v", tc.raw, err)
			}
		})
	}
}

// TestDiagnoseJSONTruncation_UnclosedBracket detects the exact
// failure pattern from the engineer's tools log: array opener
// without closer because LLM streamed a truncated function-call arg.
func TestDiagnoseJSONTruncation_UnclosedBracket(t *testing.T) {
	cases := []struct {
		name             string
		raw              string
		mustMentionBoth  bool
		mustMentionBrack bool
	}{
		{"single unclosed [", `[{"title":"x","validations":[{"d":"x"}]}`, false, true},
		{"missing both [ ]", `[`, false, true},
		{"single object missing }", `[{"title":"x"`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := diagnoseJSONTruncation("post_action", "claims_json", json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected truncation error for %q, got nil", tc.raw)
			}
			msg := err.Error()
			if !strings.Contains(msg, "post_action") {
				t.Errorf("error should name the tool: %s", msg)
			}
			if !strings.Contains(msg, "claims_json") {
				t.Errorf("error should name the param: %s", msg)
			}
			if !strings.Contains(msg, "Re-emit") {
				t.Errorf("error should hint to re-emit: %s", msg)
			}
		})
	}
}

// TestDiagnoseJSONTruncation_BracketsInsideStringsAreIgnored ensures
// the bracket counter respects string literals so payloads with `[`
// in titles don't false-positive as truncated.
func TestDiagnoseJSONTruncation_BracketsInsideStringsAreIgnored(t *testing.T) {
	raw := `[{"title":"contains [unbalanced ] brackets {and braces}","key":"v"}]`
	if err := diagnoseJSONTruncation("test", "p", json.RawMessage(raw)); err != nil {
		t.Fatalf("brackets inside strings should not count as unbalanced; got: %v", err)
	}
}

// TestDiagnoseJSONTruncation_EscapedQuoteHandledCorrectly ensures
// that backslash-escaped quotes inside strings don't confuse the
// inString tracker. A common LLM emission shape.
func TestDiagnoseJSONTruncation_EscapedQuoteHandledCorrectly(t *testing.T) {
	raw := `[{"title":"he said \"hi\" then [waved]"}]`
	if err := diagnoseJSONTruncation("test", "p", json.RawMessage(raw)); err != nil {
		t.Fatalf("escaped quotes should be handled; got: %v", err)
	}
}

// TestDiagnoseJSONTruncation_TruncatedMidStringSurfacesSpecificError
// produces the dedicated "truncated mid-string" message when the
// trailing closing quote is missing — the LLM cut off mid-emission.
func TestDiagnoseJSONTruncation_TruncatedMidStringSurfacesSpecificError(t *testing.T) {
	raw := `[{"title":"unclosed string`
	err := diagnoseJSONTruncation("test", "p", json.RawMessage(raw))
	if err == nil {
		t.Fatal("expected error for unclosed string")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("error should mention truncated string: %s", err.Error())
	}
}

// TestDiagnoseJSONTruncation_EmptyInputReturnsNil — empty raw is
// handled by requireRawJSONParam upstream; this helper passes through.
func TestDiagnoseJSONTruncation_EmptyInputReturnsNil(t *testing.T) {
	if err := diagnoseJSONTruncation("test", "p", json.RawMessage("")); err != nil {
		t.Fatalf("empty input should return nil, got: %v", err)
	}
}
