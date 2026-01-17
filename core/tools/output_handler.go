package tools

import (
	"bytes"
	"io"
	"sync"

	"github.com/adalundhe/sylk/core/tools/parsers"
)

type OutputType string

const (
	OutputTypeParsed    OutputType = "parsed"
	OutputTypeTruncated OutputType = "truncated"
	OutputTypeRaw       OutputType = "raw"
)

type ProcessedOutput struct {
	Type      OutputType
	Parsed    any
	Summary   string
	Important []string
	FirstN    []string
	LastM     []string
	FullSize  int
}

type OutputHandlerConfig struct {
	MaxBufferSize      int
	KeepFirstLines     int
	KeepLastLines      int
	ImportancePatterns []string
}

func DefaultOutputHandlerConfig() OutputHandlerConfig {
	return OutputHandlerConfig{
		MaxBufferSize:      1024 * 1024,
		KeepFirstLines:     50,
		KeepLastLines:      100,
		ImportancePatterns: DefaultImportancePatterns(),
	}
}

type OutputParser interface {
	Parse(stdout, stderr []byte) (any, error)
}

// ParseCache provides access to LLM-learned parse templates
type ParseCache interface {
	Get(toolPattern string) *parsers.ParseTemplate
	Apply(template *parsers.ParseTemplate, stdout, stderr []byte) (*parsers.TemplateResult, error)
}

type OutputHandler struct {
	config     OutputHandlerConfig
	parsers    map[string]OutputParser
	parseCache ParseCache
	truncator  *SmartTruncator
	mu         sync.RWMutex
}

// NewOutputHandler creates a new output handler without parse cache
func NewOutputHandler(config OutputHandlerConfig) *OutputHandler {
	return newOutputHandlerWithCache(config, nil)
}

// NewOutputHandlerWithCache creates a new output handler with parse cache
func NewOutputHandlerWithCache(config OutputHandlerConfig, cache ParseCache) *OutputHandler {
	return newOutputHandlerWithCache(config, cache)
}

func newOutputHandlerWithCache(config OutputHandlerConfig, cache ParseCache) *OutputHandler {
	detector := NewImportanceDetector(config.ImportancePatterns)
	truncatorConfig := SmartTruncatorConfig{
		KeepFirstLines: config.KeepFirstLines,
		KeepLastLines:  config.KeepLastLines,
		MaxBufferSize:  config.MaxBufferSize,
	}

	return &OutputHandler{
		config:     config,
		parsers:    make(map[string]OutputParser),
		parseCache: cache,
		truncator:  NewSmartTruncator(truncatorConfig, detector),
	}
}

func (h *OutputHandler) RegisterParser(tool string, parser OutputParser) {
	h.mu.Lock()
	h.parsers[tool] = parser
	h.mu.Unlock()
}

func (h *OutputHandler) getParser(tool string) OutputParser {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.parsers[tool]
}

func (h *OutputHandler) ProcessOutput(tool string, stdout, stderr []byte) *ProcessedOutput {
	if output := h.tryToolParser(tool, stdout, stderr); output != nil {
		return output
	}

	if output := h.tryParseCache(tool, stdout, stderr); output != nil {
		return output
	}

	return h.createTruncatedOutput(stdout, stderr)
}

func (h *OutputHandler) tryToolParser(tool string, stdout, stderr []byte) *ProcessedOutput {
	parser := h.getParser(tool)
	if parser == nil {
		return nil
	}

	parsed, err := parser.Parse(stdout, stderr)
	if err != nil {
		return nil
	}

	return &ProcessedOutput{
		Type:     OutputTypeParsed,
		Parsed:   parsed,
		FullSize: len(stdout) + len(stderr),
	}
}

func (h *OutputHandler) tryParseCache(tool string, stdout, stderr []byte) *ProcessedOutput {
	if h.parseCache == nil {
		return nil
	}

	template := h.parseCache.Get(tool)
	if template == nil {
		return nil
	}

	parsed, err := h.parseCache.Apply(template, stdout, stderr)
	if err != nil {
		return nil
	}

	return &ProcessedOutput{
		Type:     OutputTypeParsed,
		Parsed:   parsed,
		FullSize: len(stdout) + len(stderr),
	}
}

func (h *OutputHandler) createTruncatedOutput(stdout, stderr []byte) *ProcessedOutput {
	truncated := h.truncator.Truncate(stdout, stderr)
	return &ProcessedOutput{
		Type:      OutputTypeTruncated,
		Summary:   truncated.Summary,
		Important: truncated.ImportantLines,
		FirstN:    truncated.FirstLines,
		LastM:     truncated.LastLines,
		FullSize:  len(stdout) + len(stderr),
	}
}

type StreamWriter struct {
	buffer  *bytes.Buffer
	passTo  io.Writer
	mu      sync.Mutex
	maxSize int
}

func NewStreamWriter(passTo io.Writer, maxSize int) *StreamWriter {
	return &StreamWriter{
		buffer:  &bytes.Buffer{},
		passTo:  passTo,
		maxSize: maxSize,
	}
}

func (w *StreamWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.passTo != nil {
		w.passTo.Write(p)
	}

	if w.buffer.Len()+len(p) <= w.maxSize {
		w.buffer.Write(p)
	}
	return len(p), nil
}

func (w *StreamWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Bytes()
}

func (h *OutputHandler) CreateStreams(streamTo io.Writer) (stdout, stderr *StreamWriter) {
	stdout = NewStreamWriter(streamTo, h.config.MaxBufferSize)
	stderr = NewStreamWriter(streamTo, h.config.MaxBufferSize)
	return stdout, stderr
}
