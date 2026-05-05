package claims

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// These tests pin the lenient-parsing contract for LLM-produced tool inputs.
// Real failures observed in production:
//   - validations: "ensure tests pass" (string instead of array)
//   - validations: {description, quality_bar, type} (single object instead of array)
//   - claims_json: "{...}" (object instead of array, sometimes double-encoded)
// Each test pins one failure shape so the production error never returns.

func TestUnwrapJSONArray_AcceptsCanonicalArray(t *testing.T) {
	in := json.RawMessage(`[{"title":"a"}]`)
	got := unwrapJSONArray(in)
	if string(got) != string(in) {
		t.Fatalf("canonical array mutated: got %q want %q", got, in)
	}
}

func TestUnwrapJSONArray_PromotesSingleObjectToArray(t *testing.T) {
	in := json.RawMessage(`{"title":"a"}`)
	got := unwrapJSONArray(in)
	want := `[{"title":"a"}]`
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnwrapJSONArray_UnwrapsDoubleEncodedString(t *testing.T) {
	in := json.RawMessage(`"[{\"title\":\"a\"}]"`)
	got := unwrapJSONArray(in)
	want := `[{"title":"a"}]`
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnwrapJSONArray_UnwrapsDoubleEncodedObjectAndPromotes(t *testing.T) {
	// An LLM that double-encoded a single object should still end up as an array.
	in := json.RawMessage(`"{\"title\":\"a\"}"`)
	got := unwrapJSONArray(in)
	want := `[{"title":"a"}]`
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnwrapJSONArray_PassesThroughEmpty(t *testing.T) {
	if got := unwrapJSONArray(nil); got != nil {
		t.Fatalf("nil input mutated: got %q", got)
	}
	if got := unwrapJSONArray(json.RawMessage("")); string(got) != "" {
		t.Fatalf("empty input mutated: got %q", got)
	}
}

func TestFlexibleValidations_AcceptsCanonicalArray(t *testing.T) {
	raw := []byte(`[{"description":"unit tests pass","quality_bar":"100% pass","type":"test"},{"description":"lint clean","type":"inspection"}]`)
	var v flexibleValidations
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleValidations{
		{Description: "unit tests pass", QualityBar: "100% pass", Type: "test"},
		{Description: "lint clean", Type: "inspection"},
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("got %+v want %+v", v, want)
	}
}

func TestFlexibleValidations_AcceptsSingleString(t *testing.T) {
	// This is the production failure mode. Record it.
	raw := []byte(`"ensure tests pass"`)
	var v flexibleValidations
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleValidations{{Description: "ensure tests pass"}}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("got %+v want %+v", v, want)
	}
}

func TestFlexibleValidations_AcceptsSingleObject(t *testing.T) {
	raw := []byte(`{"description":"unit tests pass","quality_bar":"100% pass","type":"test"}`)
	var v flexibleValidations
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleValidations{{Description: "unit tests pass", QualityBar: "100% pass", Type: "test"}}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("got %+v want %+v", v, want)
	}
}

func TestFlexibleValidations_AcceptsStringArray(t *testing.T) {
	raw := []byte(`["criterion a","criterion b"]`)
	var v flexibleValidations
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleValidations{
		{Description: "criterion a"},
		{Description: "criterion b"},
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("got %+v want %+v", v, want)
	}
}

func TestFlexibleValidations_AcceptsNullAndEmpty(t *testing.T) {
	cases := [][]byte{[]byte(`null`), []byte(`""`), []byte(`[]`)}
	for _, raw := range cases {
		var v flexibleValidations
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("unmarshal %q: %v", raw, err)
		}
		if len(v) != 0 {
			t.Fatalf("unmarshal %q: got %+v want empty", raw, v)
		}
	}
}

func TestFlexibleArtifacts_AcceptsCanonicalArray(t *testing.T) {
	raw := []byte(`[{"kind":"test_output","reference":"pass"},{"kind":"diff","reference":"foo.go"}]`)
	var a flexibleArtifacts
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleArtifacts{
		{Kind: "test_output", Reference: "pass"},
		{Kind: "diff", Reference: "foo.go"},
	}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("got %+v want %+v", a, want)
	}
}

func TestFlexibleArtifacts_AcceptsSingleString(t *testing.T) {
	raw := []byte(`"src/foo.go:42"`)
	var a flexibleArtifacts
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleArtifacts{{Reference: "src/foo.go:42"}}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("got %+v want %+v", a, want)
	}
}

func TestFlexibleArtifacts_AcceptsSingleObject(t *testing.T) {
	raw := []byte(`{"kind":"test_output","reference":"pass"}`)
	var a flexibleArtifacts
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleArtifacts{{Kind: "test_output", Reference: "pass"}}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("got %+v want %+v", a, want)
	}
}

func TestFlexibleArtifacts_AcceptsStringArray(t *testing.T) {
	raw := []byte(`["ref1","ref2"]`)
	var a flexibleArtifacts
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := flexibleArtifacts{{Reference: "ref1"}, {Reference: "ref2"}}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("got %+v want %+v", a, want)
	}
}

// End-to-end: unmarshal a full claimInput payload that combines every
// failure shape in one document. This is the integration test for the
// post_action handler — if this passes, the production error is fixed.
func TestClaimInput_AcceptsAllLenientShapesTogether(t *testing.T) {
	raw := []byte(`{
		"title": "Add login",
		"description": "Implement /login",
		"subject": "engineer",
		"scope": "auth",
		"validations": "ensure tests pass"
	}`)
	var ci claimInput
	if err := json.Unmarshal(raw, &ci); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ci.Title != "Add login" || ci.Subject != "engineer" {
		t.Fatalf("got %+v", ci)
	}
	if len(ci.Scope) != 1 || ci.Scope[0].Key != "auth" {
		t.Fatalf("scope = %+v", ci.Scope)
	}
	if len(ci.Validations) != 1 || ci.Validations[0].Description != "ensure tests pass" {
		t.Fatalf("validations = %+v", ci.Validations)
	}
}

func TestTestamentInput_AcceptsAllLenientShapesTogether(t *testing.T) {
	raw := []byte(`{
		"claim_id": "clm_123",
		"summary": "Tests pass",
		"confidence": "high",
		"artifacts": "go test ./... — 42 passed"
	}`)
	var ti testamentInput
	if err := json.Unmarshal(raw, &ti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ti.ClaimID != "clm_123" || ti.Summary != "Tests pass" || ti.Confidence != "high" {
		t.Fatalf("got %+v", ti)
	}
	if len(ti.Artifacts) != 1 || ti.Artifacts[0].Reference != "go test ./... — 42 passed" {
		t.Fatalf("artifacts = %+v", ti.Artifacts)
	}
}

// requireRawJSONParam is the schema-echoing pre-check that fires before
// unwrapJSONArray + json.Unmarshal. The production failure these tests pin:
// the LLM calls post_action with only action_type set (no claims_json), and
// the handler responds with the opaque "unexpected end of JSON input"
// instead of telling the agent the field is missing. After the fix, the
// error names the field and shows the canonical shape inline so the agent
// can self-correct on retry.
func TestRequireRawJSONParam_DetectsMissingAndEmptyShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"empty bytes", json.RawMessage("")},
		{"whitespace", json.RawMessage("   ")},
		{"json null", json.RawMessage("null")},
		{"empty string", json.RawMessage(`""`)},
		{"empty array", json.RawMessage(`[]`)},
		{"empty object", json.RawMessage(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireRawJSONParam("post_action", "claims_json", tc.raw, "<shape>")
			if err == nil {
				t.Fatalf("expected error for %s; got nil", tc.name)
			}
			msg := err.Error()
			for _, want := range []string{"post_action", "claims_json", "missing or empty", "<shape>"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q missing %q", msg, want)
				}
			}
		})
	}
}

func TestRequireRawJSONParam_AcceptsNonEmpty(t *testing.T) {
	if err := requireRawJSONParam("post_action", "claims_json", json.RawMessage(`[{"title":"x"}]`), "<shape>"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// post_action top-level: pipe a single object (not an array) through the
// unwrap+unmarshal pipeline used by PostActionSkill.
func TestPostActionPipeline_AcceptsSingleClaimObject(t *testing.T) {
	rawArg := json.RawMessage(`{"title":"Add login","description":"Implement /login","subject":"engineer","scope":"auth","validations":"ensure tests pass"}`)
	unwrapped := unwrapJSONArray(rawArg)
	var inputs []claimInput
	if err := json.Unmarshal(unwrapped, &inputs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(inputs) != 1 || inputs[0].Title != "Add login" {
		t.Fatalf("got %+v", inputs)
	}
	if len(inputs[0].Validations) != 1 || inputs[0].Validations[0].Description != "ensure tests pass" {
		t.Fatalf("validations = %+v", inputs[0].Validations)
	}
}
