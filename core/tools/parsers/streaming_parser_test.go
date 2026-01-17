package parsers

import (
	"sync"
	"testing"
)

func TestGoStreamingParser_ParseError(t *testing.T) {
	p := NewGoStreamingParser()

	events := p.OnLine("main.go:10:5: undefined: foo")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	err, ok := events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("expected ErrorEvent, got %T", events[0])
	}

	if err.File != "main.go" {
		t.Errorf("expected file main.go, got %q", err.File)
	}
	if err.Line != 10 {
		t.Errorf("expected line 10, got %d", err.Line)
	}
	if err.Column != 5 {
		t.Errorf("expected column 5, got %d", err.Column)
	}
	if err.Message != "undefined: foo" {
		t.Errorf("unexpected message: %q", err.Message)
	}
}

func TestGoStreamingParser_ParseTestResult(t *testing.T) {
	p := NewGoStreamingParser()

	tests := []struct {
		line     string
		name     string
		status   string
		duration float64
	}{
		{"--- PASS: TestFoo (0.005s)", "TestFoo", "pass", 0.005},
		{"--- FAIL: TestBar (1.234s)", "TestBar", "fail", 1.234},
		{"--- SKIP: TestBaz (0.001s)", "TestBaz", "skip", 0.001},
	}

	for _, tc := range tests {
		events := p.OnLine(tc.line)
		if len(events) != 1 {
			t.Errorf("expected 1 event for %q", tc.line)
			continue
		}

		result, ok := events[0].(*TestResultEvent)
		if !ok {
			t.Errorf("expected TestResultEvent for %q", tc.line)
			continue
		}

		if result.Name != tc.name {
			t.Errorf("expected name %q, got %q", tc.name, result.Name)
		}
		if result.Status != tc.status {
			t.Errorf("expected status %q, got %q", tc.status, result.Status)
		}
		if result.Duration != tc.duration {
			t.Errorf("expected duration %f, got %f", tc.duration, result.Duration)
		}
	}
}

func TestGoStreamingParser_NoMatch(t *testing.T) {
	p := NewGoStreamingParser()

	events := p.OnLine("ok  mypackage  0.005s")
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
}

func TestPytestStreamingParser_ParseTest(t *testing.T) {
	p := NewPytestStreamingParser()

	tests := []struct {
		line   string
		name   string
		status string
	}{
		{"test_foo.py::test_bar PASSED", "test_foo.py", "passed"},
		{"test_baz.py::test_qux FAILED", "test_baz.py", "failed"},
		{"test_skip.py::test_it SKIPPED", "test_skip.py", "skipped"},
	}

	for _, tc := range tests {
		events := p.OnLine(tc.line)
		if len(events) != 1 {
			t.Errorf("expected 1 event for %q, got %d", tc.line, len(events))
			continue
		}

		result, ok := events[0].(*TestResultEvent)
		if !ok {
			t.Errorf("expected TestResultEvent for %q", tc.line)
			continue
		}

		if result.Name != tc.name {
			t.Errorf("expected name %q, got %q", tc.name, result.Name)
		}
		if result.Status != tc.status {
			t.Errorf("expected status %q, got %q", tc.status, result.Status)
		}
	}
}

func TestPytestStreamingParser_ParseError(t *testing.T) {
	p := NewPytestStreamingParser()

	events := p.OnLine("E   AssertionError: expected 1, got 2")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	err, ok := events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("expected ErrorEvent, got %T", events[0])
	}

	if err.Message != "AssertionError: expected 1, got 2" {
		t.Errorf("unexpected message: %q", err.Message)
	}
}

func TestPytestStreamingParser_ParseProgress(t *testing.T) {
	p := NewPytestStreamingParser()

	events := p.OnLine("  50%")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	progress, ok := events[0].(*ProgressEvent)
	if !ok {
		t.Fatalf("expected ProgressEvent, got %T", events[0])
	}

	if progress.Percent != 50 {
		t.Errorf("expected 50%%, got %d%%", progress.Percent)
	}
}

func TestGenericStreamingParser_ParseError(t *testing.T) {
	p := NewGenericStreamingParser()

	tests := []string{
		"Error: something went wrong",
		"error: another problem",
		"Failed: build failed",
		"ERR! npm ERR code 1",
	}

	for _, line := range tests {
		events := p.OnLine(line)
		if len(events) != 1 {
			t.Errorf("expected 1 event for %q, got %d", line, len(events))
			continue
		}

		if _, ok := events[0].(*ErrorEvent); !ok {
			t.Errorf("expected ErrorEvent for %q", line)
		}
	}
}

func TestGenericStreamingParser_ParseWarning(t *testing.T) {
	p := NewGenericStreamingParser()

	tests := []string{
		"Warning: deprecated function",
		"warning: unused variable",
		"WARN: memory low",
	}

	for _, line := range tests {
		events := p.OnLine(line)
		if len(events) != 1 {
			t.Errorf("expected 1 event for %q, got %d", line, len(events))
			continue
		}

		if _, ok := events[0].(*WarningEvent); !ok {
			t.Errorf("expected WarningEvent for %q", line)
		}
	}
}

func TestStreamingParserRegistry_Get(t *testing.T) {
	r := NewStreamingParserRegistry()

	tests := []struct {
		tool     string
		hasMatch bool
	}{
		{"go test ./...", true},
		{"go build", true},
		{"npm test", true},
		{"pytest -v", true},
		{"unknown-tool", false},
	}

	for _, tc := range tests {
		parser := r.Get(tc.tool)
		if tc.hasMatch && parser == nil {
			t.Errorf("expected parser for %q", tc.tool)
		}
		if !tc.hasMatch && parser != nil {
			t.Errorf("unexpected parser for %q", tc.tool)
		}
	}
}

func TestStreamingParserRegistry_Register(t *testing.T) {
	r := NewStreamingParserRegistry()

	custom := NewGenericStreamingParser()
	r.Register("custom-tool", custom)

	got := r.Get("custom-tool test")
	if got != custom {
		t.Error("expected custom parser")
	}
}

func TestEventCollector(t *testing.T) {
	c := NewEventCollector()

	c.OnEvent(&ErrorEvent{Message: "error1", Severity: "error"})
	c.OnEvent(&WarningEvent{Message: "warning1"})
	c.OnEvent(&ErrorEvent{Message: "error2", Severity: "warning"})

	events := c.Events()
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}

	errors := c.Errors()
	if len(errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(errors))
	}

	if !c.HasFatalError() {
		t.Error("expected HasFatalError to return true")
	}
}

func TestEventCollector_NoFatalError(t *testing.T) {
	c := NewEventCollector()

	c.OnEvent(&WarningEvent{Message: "just a warning"})
	c.OnEvent(&ErrorEvent{Message: "minor", Severity: "warning"})

	if c.HasFatalError() {
		t.Error("expected HasFatalError to return false")
	}
}

func TestEventCollector_Clear(t *testing.T) {
	c := NewEventCollector()

	c.OnEvent(&ErrorEvent{Message: "error"})
	c.Clear()

	if len(c.Events()) != 0 {
		t.Error("expected empty events after Clear")
	}
}

func TestStreamingParserWrapper(t *testing.T) {
	parser := NewGoStreamingParser()
	collector := NewEventCollector()
	wrapper := NewStreamingParserWrapper(parser, collector)

	wrapper.ProcessLine("main.go:1:1: error")
	wrapper.ProcessLine("--- PASS: TestFoo (0.001s)")

	events := collector.Events()
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestEventTypes(t *testing.T) {
	tests := []struct {
		event    ParsedEvent
		expected EventType
	}{
		{&ErrorEvent{}, EventTypeError},
		{&WarningEvent{}, EventTypeWarning},
		{&TestResultEvent{}, EventTypeTestResult},
		{&ProgressEvent{}, EventTypeProgress},
	}

	for _, tc := range tests {
		if tc.event.Type() != tc.expected {
			t.Errorf("expected type %d, got %d", tc.expected, tc.event.Type())
		}
	}
}

func TestEventCollector_Concurrent(t *testing.T) {
	c := NewEventCollector()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.OnEvent(&ErrorEvent{Message: "concurrent", Severity: "error"})
			c.Events()
			c.Errors()
			c.HasFatalError()
		}(i)
	}
	wg.Wait()

	if len(c.Events()) != 100 {
		t.Errorf("expected 100 events, got %d", len(c.Events()))
	}
}

func TestGoStreamingParser_ParseFail(t *testing.T) {
	p := NewGoStreamingParser()

	events := p.OnLine("FAIL	github.com/example/pkg")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	err, ok := events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("expected ErrorEvent, got %T", events[0])
	}

	if err.File != "github.com/example/pkg" {
		t.Errorf("unexpected file: %q", err.File)
	}
	if err.Message != "package failed" {
		t.Errorf("unexpected message: %q", err.Message)
	}
}

func TestParserReset(t *testing.T) {
	parsers := []StreamingParser{
		NewGoStreamingParser(),
		NewPytestStreamingParser(),
		NewGenericStreamingParser(),
	}

	for _, p := range parsers {
		p.Reset()
	}
}
