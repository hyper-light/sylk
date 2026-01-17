package tools

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/tools/parsers"
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

type mockParseCache struct {
	template *parsers.ParseTemplate
	result   *parsers.TemplateResult
	err      error
}

func (m *mockParseCache) Get(toolPattern string) *parsers.ParseTemplate {
	return m.template
}

func (m *mockParseCache) Apply(_ *parsers.ParseTemplate, _, _ []byte) (*parsers.TemplateResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestNewOutputHandlerWithCache(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	cache := &mockParseCache{}

	h := NewOutputHandlerWithCache(config, cache)

	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.parseCache == nil {
		t.Error("expected non-nil parseCache")
	}
}

func TestOutputHandler_ProcessOutput_WithParseCache(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	template := &parsers.ParseTemplate{ToolPattern: "test-tool"}
	result := &parsers.TemplateResult{Fields: map[string][]string{"key": {"value"}}}
	cache := &mockParseCache{template: template, result: result}

	h := NewOutputHandlerWithCache(config, cache)

	output := h.ProcessOutput("test-tool", []byte("stdout"), []byte("stderr"))

	if output.Type != OutputTypeParsed {
		t.Errorf("expected OutputTypeParsed, got %v", output.Type)
	}
	if output.Parsed == nil {
		t.Error("expected non-nil Parsed")
	}
}

func TestOutputHandler_ProcessOutput_ParseCacheNoTemplate(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	cache := &mockParseCache{template: nil}

	h := NewOutputHandlerWithCache(config, cache)

	output := h.ProcessOutput("unknown-tool", []byte("stdout"), nil)

	if output.Type != OutputTypeTruncated {
		t.Errorf("expected OutputTypeTruncated, got %v", output.Type)
	}
}

func TestOutputHandler_ProcessOutput_ParseCacheError(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	template := &parsers.ParseTemplate{ToolPattern: "test-tool"}
	cache := &mockParseCache{template: template, err: errors.New("apply failed")}

	h := NewOutputHandlerWithCache(config, cache)

	output := h.ProcessOutput("test-tool", []byte("stdout"), nil)

	if output.Type != OutputTypeTruncated {
		t.Errorf("expected fallback to OutputTypeTruncated, got %v", output.Type)
	}
}

func TestOutputHandler_ProcessOutput_ParserPrecedence(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	template := &parsers.ParseTemplate{ToolPattern: "test-tool"}
	cacheResult := &parsers.TemplateResult{Fields: map[string][]string{"cache": {"result"}}}
	cache := &mockParseCache{template: template, result: cacheResult}

	h := NewOutputHandlerWithCache(config, cache)

	parser := &mockParser{result: "parser result"}
	h.RegisterParser("test-tool", parser)

	output := h.ProcessOutput("test-tool", []byte("stdout"), nil)

	if output.Type != OutputTypeParsed {
		t.Errorf("expected OutputTypeParsed, got %v", output.Type)
	}
	if output.Parsed != "parser result" {
		t.Errorf("expected parser to take precedence, got %v", output.Parsed)
	}
}

func TestOutputHandler_ProcessOutput_CacheFallbackWhenParserFails(t *testing.T) {
	config := DefaultOutputHandlerConfig()
	template := &parsers.ParseTemplate{ToolPattern: "test-tool"}
	cacheResult := &parsers.TemplateResult{Fields: map[string][]string{"cache": {"result"}}}
	cache := &mockParseCache{template: template, result: cacheResult}

	h := NewOutputHandlerWithCache(config, cache)

	parser := &mockParser{err: true}
	h.RegisterParser("test-tool", parser)

	output := h.ProcessOutput("test-tool", []byte("stdout"), nil)

	if output.Type != OutputTypeParsed {
		t.Errorf("expected OutputTypeParsed from cache fallback, got %v", output.Type)
	}
	if _, ok := output.Parsed.(*parsers.TemplateResult); !ok {
		t.Errorf("expected cache result type, got %T", output.Parsed)
	}
}
