package tools

import (
	"bytes"
	"sync"
	"testing"
)

func TestNewOutputHandler(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.truncator == nil {
		t.Error("expected non-nil truncator")
	}
}

func TestOutputHandler_RegisterParser(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	parser := &mockParser{result: "parsed"}
	h.RegisterParser("go build", parser)

	if p := h.getParser("go build"); p == nil {
		t.Error("expected registered parser")
	}
	if p := h.getParser("unknown"); p != nil {
		t.Error("expected nil for unregistered tool")
	}
}

func TestOutputHandler_ProcessOutput_WithParser(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	parser := &mockParser{result: "parsed result"}
	h.RegisterParser("test-tool", parser)

	result := h.ProcessOutput("test-tool", []byte("stdout"), []byte("stderr"))

	if result.Type != OutputTypeParsed {
		t.Errorf("expected OutputTypeParsed, got %v", result.Type)
	}
	if result.Parsed != "parsed result" {
		t.Errorf("expected 'parsed result', got %v", result.Parsed)
	}
}

func TestOutputHandler_ProcessOutput_WithoutParser(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	stdout := []byte("line1\nError: something\nline3")
	result := h.ProcessOutput("unknown-tool", stdout, nil)

	if result.Type != OutputTypeTruncated {
		t.Errorf("expected OutputTypeTruncated, got %v", result.Type)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if result.FullSize != len(stdout) {
		t.Errorf("expected FullSize=%d, got %d", len(stdout), result.FullSize)
	}
}

func TestOutputHandler_ProcessOutput_ParserFails(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	parser := &mockParser{err: true}
	h.RegisterParser("failing-tool", parser)

	result := h.ProcessOutput("failing-tool", []byte("stdout"), nil)

	if result.Type != OutputTypeTruncated {
		t.Errorf("expected fallback to truncated, got %v", result.Type)
	}
}

func TestOutputHandler_CreateStreams(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	var buf bytes.Buffer
	stdout, stderr := h.CreateStreams(&buf)

	if stdout == nil || stderr == nil {
		t.Fatal("expected non-nil streams")
	}

	stdout.Write([]byte("stdout content"))
	stderr.Write([]byte("stderr content"))

	if !bytes.Contains(buf.Bytes(), []byte("stdout content")) {
		t.Error("expected stdout in buffer")
	}
	if !bytes.Contains(buf.Bytes(), []byte("stderr content")) {
		t.Error("expected stderr in buffer")
	}
}

func TestStreamWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	sw := NewStreamWriter(&buf, 1024)

	n, err := sw.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 4 {
		t.Errorf("expected n=4, got %d", n)
	}

	if string(sw.Bytes()) != "test" {
		t.Errorf("expected 'test' in buffer, got %q", sw.Bytes())
	}
	if buf.String() != "test" {
		t.Errorf("expected 'test' passed through, got %q", buf.String())
	}
}

func TestStreamWriter_Write_MaxSize(t *testing.T) {
	sw := NewStreamWriter(nil, 10)

	sw.Write([]byte("12345"))
	sw.Write([]byte("67890"))
	sw.Write([]byte("excess"))

	if len(sw.Bytes()) != 10 {
		t.Errorf("expected 10 bytes in buffer, got %d", len(sw.Bytes()))
	}
}

func TestStreamWriter_Write_NoPassTo(t *testing.T) {
	sw := NewStreamWriter(nil, 1024)

	n, err := sw.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 4 {
		t.Errorf("expected n=4, got %d", n)
	}
	if string(sw.Bytes()) != "test" {
		t.Errorf("expected 'test' in buffer, got %q", sw.Bytes())
	}
}

func TestStreamWriter_Concurrent(t *testing.T) {
	sw := NewStreamWriter(nil, 10000)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sw.Write([]byte("data"))
			sw.Bytes()
		}()
	}
	wg.Wait()
}

func TestOutputHandler_ProcessOutput_EmptyInput(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	h := NewOutputHandler(config)

	result := h.ProcessOutput("tool", nil, nil)

	if result.Type != OutputTypeTruncated {
		t.Errorf("expected OutputTypeTruncated, got %v", result.Type)
	}
	if result.FullSize != 0 {
		t.Errorf("expected FullSize=0, got %d", result.FullSize)
	}
}

func TestOutputHandler_ProcessOutput_LargeOutput(t *testing.T) {
	config := OutputHandlerConfig{
		MaxBufferSize:      100,
		KeepFirstLines:     2,
		KeepLastLines:      2,
		ImportancePatterns: DefaultImportancePatterns(),
	}
	h := NewOutputHandler(config)

	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line content")
	}
	stdout := []byte(join(lines, "\n"))

	result := h.ProcessOutput("tool", stdout, nil)

	if len(result.FirstN) != 2 {
		t.Errorf("expected 2 first lines, got %d", len(result.FirstN))
	}
	if len(result.LastM) != 2 {
		t.Errorf("expected 2 last lines, got %d", len(result.LastM))
	}
}

func join(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	result := s[0]
	for _, str := range s[1:] {
		result += sep + str
	}
	return result
}

type mockParser struct {
	result any
	err    bool
}

func (m *mockParser) Parse(stdout, stderr []byte) (any, error) {
	if m.err {
		return nil, errMock
	}
	return m.result, nil
}

var errMock = &mockError{}

type mockError struct{}

func (e *mockError) Error() string { return "mock error" }
