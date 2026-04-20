package shared

import "testing"

// TestProtocolError_DoesNotDoublePrefixScope locks the cosmetic
// fix: when a RuleID already carries its scope ("pipeline.finalize.
// challenge_target_mismatch"), Error() must not render it as
// "pipeline.pipeline.finalize.challenge_target_mismatch". That
// doubled string was appearing in tester logs and in the UI error
// row, confusing both log-grep and the LLM's own retry reasoning.
func TestProtocolError_DoesNotDoublePrefixScope(t *testing.T) {
	err := NewPipelineProtocolError(
		"pipeline.finalize.challenge_target_mismatch",
		"finalize_pipeline",
		"a pending challenge from X is awaiting your response",
		"retry with targets=[{target:\"X\"}]",
	)
	got := err.Error()

	if got[:18] != "pipeline.pipeline." && got[:9] == "pipeline." {
		// single prefix — good.
	} else if len(got) >= 18 && got[:18] == "pipeline.pipeline." {
		t.Errorf("Error() double-prefixed the scope: %q", got)
	}
}

// TestProtocolError_PrependsScopeWhenRuleIDMissingIt covers the
// other leg of the fix: if a RuleID is short-form ("finalize.foo")
// and does NOT include the scope prefix, Error() still prepends the
// scope so log consumers have a consistent full-form string.
func TestProtocolError_PrependsScopeWhenRuleIDMissingIt(t *testing.T) {
	err := &ProtocolError{
		Scope:        "pipeline",
		RuleID:       "finalize.short_form",
		HumanMessage: "short-form rule id",
	}
	got := err.Error()
	if got == "" {
		t.Fatal("Error() returned empty string")
	}
	// Must begin with "pipeline.finalize.short_form"
	want := "pipeline.finalize.short_form"
	if got[:len(want)] != want {
		t.Errorf("Error() = %q, want prefix %q", got, want)
	}
}

// TestProtocolError_NoRuleIDOnlyScope guards the edge case where
// only Scope is set — the renderer should still produce something
// readable rather than a stray period.
func TestProtocolError_NoRuleIDOnlyScope(t *testing.T) {
	err := &ProtocolError{
		Scope:        "pipeline",
		HumanMessage: "unhelpful but well-formed",
	}
	got := err.Error()
	want := "pipeline: unhelpful but well-formed"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestProtocolError_NilReceiverReturnsEmpty documents the
// fmt.Errorf-compatible nil-safe contract.
func TestProtocolError_NilReceiverReturnsEmpty(t *testing.T) {
	var err *ProtocolError
	if got := err.Error(); got != "" {
		t.Errorf("nil.Error() = %q, want empty string", got)
	}
}
