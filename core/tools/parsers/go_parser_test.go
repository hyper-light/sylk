package parsers

import (
	"testing"
)

func TestGoParser_ParseBuildErrors(t *testing.T) {
	p := NewGoParser()

	output := `main.go:10:5: undefined: foo
pkg/util.go:25:12: cannot use x (type int) as type string`

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GoParseResult)
	if len(r.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(r.Errors))
	}

	if r.Errors[0].File != "main.go" {
		t.Errorf("expected file 'main.go', got %q", r.Errors[0].File)
	}
	if r.Errors[0].Line != 10 {
		t.Errorf("expected line 10, got %d", r.Errors[0].Line)
	}
	if r.Errors[0].Column != 5 {
		t.Errorf("expected column 5, got %d", r.Errors[0].Column)
	}
}

func TestGoParser_ParseTestResults(t *testing.T) {
	p := NewGoParser()

	output := `=== RUN   TestFoo
--- PASS: TestFoo (0.01s)
=== RUN   TestBar
--- FAIL: TestBar (0.05s)
=== RUN   TestSkip
--- SKIP: TestSkip (0.00s)`

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GoParseResult)
	if len(r.Tests) != 3 {
		t.Fatalf("expected 3 tests, got %d", len(r.Tests))
	}

	if r.Tests[0].Name != "TestFoo" || r.Tests[0].Status != TestStatusPass {
		t.Errorf("expected TestFoo PASS, got %s %s", r.Tests[0].Name, r.Tests[0].Status)
	}
	if r.Tests[1].Name != "TestBar" || r.Tests[1].Status != TestStatusFail {
		t.Errorf("expected TestBar FAIL, got %s %s", r.Tests[1].Name, r.Tests[1].Status)
	}
	if r.Tests[2].Name != "TestSkip" || r.Tests[2].Status != TestStatusSkip {
		t.Errorf("expected TestSkip SKIP, got %s %s", r.Tests[2].Name, r.Tests[2].Status)
	}
}

func TestGoParser_ParseEmpty(t *testing.T) {
	p := NewGoParser()

	result, err := p.Parse(nil, nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GoParseResult)
	if len(r.Errors) != 0 || len(r.Tests) != 0 {
		t.Error("expected empty result for empty input")
	}
}

func TestGoParser_ParseStderr(t *testing.T) {
	p := NewGoParser()

	stderr := []byte("file.go:1:1: syntax error")
	result, err := p.Parse(nil, stderr)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GoParseResult)
	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error from stderr, got %d", len(r.Errors))
	}
}

func TestGoParser_ParseMixedOutput(t *testing.T) {
	p := NewGoParser()

	output := `=== RUN   TestExample
main.go:5:2: error here
--- FAIL: TestExample (0.10s)`

	result, err := p.Parse([]byte(output), nil)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	r := result.(*GoParseResult)
	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(r.Errors))
	}
	if len(r.Tests) != 1 {
		t.Errorf("expected 1 test, got %d", len(r.Tests))
	}
}

func TestGoParser_TestDuration(t *testing.T) {
	p := NewGoParser()

	output := `--- PASS: TestSlow (12.50s)`
	result, _ := p.Parse([]byte(output), nil)

	r := result.(*GoParseResult)
	if len(r.Tests) != 1 {
		t.Fatal("expected 1 test")
	}
	if r.Tests[0].Duration != 12.50 {
		t.Errorf("expected duration 12.50, got %f", r.Tests[0].Duration)
	}
}
