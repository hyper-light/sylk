package tools

import (
	"bytes"
	"io"
	"sync"
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

type OutputHandler struct {
	config    OutputHandlerConfig
	parsers   map[string]OutputParser
	truncator *SmartTruncator
	mu        sync.RWMutex
}

func NewOutputHandler(config OutputHandlerConfig) *OutputHandler {
	detector := NewImportanceDetector(config.ImportancePatterns)
	truncatorConfig := SmartTruncatorConfig{
		KeepFirstLines: config.KeepFirstLines,
		KeepLastLines:  config.KeepLastLines,
		MaxBufferSize:  config.MaxBufferSize,
	}

	return &OutputHandler{
		config:    config,
		parsers:   make(map[string]OutputParser),
		truncator: NewSmartTruncator(truncatorConfig, detector),
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
	if parser := h.getParser(tool); parser != nil {
		if parsed, err := parser.Parse(stdout, stderr); err == nil {
			return &ProcessedOutput{
				Type:     OutputTypeParsed,
				Parsed:   parsed,
				FullSize: len(stdout) + len(stderr),
			}
		}
	}

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
