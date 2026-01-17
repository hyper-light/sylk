package parsers

import (
	"testing"
)

func TestESLintParser_ParseJSON(t *testing.T) {
	p := NewESLintParser()

	jsonOutput := `[{
		"filePath": "/project/src/file.js",
		"messages": [
			{"line": 10, "column": 5, "message": "Unexpected var", "ruleId": "no-var", "severity": 2},
			{"line": 15, "column": 1, "message": "Missing semicolon", "ruleId": "semi", "severity": 1}
		]
	}]`

	result, err := p.Parse([]byte(jsonOutput), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*ESLintResult)
	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(r.Errors))
	}
	if len(r.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(r.Warnings))
	}

	if r.Errors[0].File != "/project/src/file.js" {
		t.Errorf("expected file path, got %q", r.Errors[0].File)
	}
	if r.Errors[0].Line != 10 {
		t.Errorf("expected line 10, got %d", r.Errors[0].Line)
	}
	if r.Errors[0].Code != "no-var" {
		t.Errorf("expected rule 'no-var', got %q", r.Errors[0].Code)
	}
}

func TestESLintParser_ParseText(t *testing.T) {
	p := NewESLintParser()

	textOutput := `/project/src/file.js
  10:5  error  Unexpected var  no-var
  15:1  warning  Missing semicolon  semi`

	result, err := p.Parse([]byte(textOutput), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*ESLintResult)
	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(r.Errors))
	}
	if len(r.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(r.Warnings))
	}
}

func TestESLintParser_ParseEmpty(t *testing.T) {
	p := NewESLintParser()

	result, err := p.Parse([]byte("[]"), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*ESLintResult)
	if len(r.Errors) != 0 || len(r.Warnings) != 0 {
		t.Error("expected empty result for empty JSON array")
	}
}

func TestESLintParser_ParseInvalidJSON(t *testing.T) {
	p := NewESLintParser()

	result, err := p.Parse([]byte("not json"), nil)
	if err != nil {
		t.Fatalf("Parse should not error on non-JSON: %v", err)
	}

	r := result.(*ESLintResult)
	if r == nil {
		t.Error("expected non-nil result for text fallback")
	}
}

func TestESLintParser_MultipleFiles(t *testing.T) {
	p := NewESLintParser()

	jsonOutput := `[
		{"filePath": "/a.js", "messages": [{"line": 1, "column": 1, "message": "err1", "ruleId": "r1", "severity": 2}]},
		{"filePath": "/b.js", "messages": [{"line": 2, "column": 2, "message": "err2", "ruleId": "r2", "severity": 2}]}
	]`

	result, _ := p.Parse([]byte(jsonOutput), nil)
	r := result.(*ESLintResult)

	if len(r.Errors) != 2 {
		t.Errorf("expected 2 errors from 2 files, got %d", len(r.Errors))
	}
}

func TestESLintParser_SeverityLevels(t *testing.T) {
	p := NewESLintParser()

	jsonOutput := `[{"filePath": "/f.js", "messages": [
		{"line": 1, "column": 1, "message": "err", "ruleId": "r", "severity": 2},
		{"line": 2, "column": 1, "message": "warn", "ruleId": "r", "severity": 1},
		{"line": 3, "column": 1, "message": "off", "ruleId": "r", "severity": 0}
	]}]`

	result, _ := p.Parse([]byte(jsonOutput), nil)
	r := result.(*ESLintResult)

	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error (severity 2), got %d", len(r.Errors))
	}
	if len(r.Warnings) != 2 {
		t.Errorf("expected 2 warnings (severity 0,1), got %d", len(r.Warnings))
	}
}
