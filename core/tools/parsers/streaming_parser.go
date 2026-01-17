package parsers

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type EventType int

const (
	EventTypeError EventType = iota
	EventTypeWarning
	EventTypeTestResult
	EventTypeProgress
)

type ParsedEvent interface {
	Type() EventType
}

type ErrorEvent struct {
	File     string
	Line     int
	Column   int
	Message  string
	Severity string
}

func (e *ErrorEvent) Type() EventType { return EventTypeError }

type WarningEvent struct {
	File    string
	Line    int
	Message string
}

func (e *WarningEvent) Type() EventType { return EventTypeWarning }

type TestResultEvent struct {
	Name     string
	Status   string
	Duration float64
}

func (e *TestResultEvent) Type() EventType { return EventTypeTestResult }

type ProgressEvent struct {
	Percent int
	Message string
}

func (e *ProgressEvent) Type() EventType { return EventTypeProgress }

type StreamingParser interface {
	OnLine(line string) []ParsedEvent
	Reset()
}

type StreamingParserRegistry struct {
	mu      sync.RWMutex
	parsers map[string]StreamingParser
}

func NewStreamingParserRegistry() *StreamingParserRegistry {
	r := &StreamingParserRegistry{
		parsers: make(map[string]StreamingParser),
	}
	r.registerDefaults()
	return r
}

func (r *StreamingParserRegistry) registerDefaults() {
	r.Register("go test", NewGoStreamingParser())
	r.Register("go build", NewGoStreamingParser())
	r.Register("npm test", NewGenericStreamingParser())
	r.Register("pytest", NewPytestStreamingParser())
}

func (r *StreamingParserRegistry) Register(pattern string, parser StreamingParser) {
	r.mu.Lock()
	r.parsers[pattern] = parser
	r.mu.Unlock()
}

func (r *StreamingParserRegistry) Get(tool string) StreamingParser {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for pattern, parser := range r.parsers {
		if strings.Contains(tool, pattern) {
			return parser
		}
	}
	return nil
}

type GoStreamingParser struct {
	errorPattern *regexp.Regexp
	testPattern  *regexp.Regexp
	failPattern  *regexp.Regexp
	skipPattern  *regexp.Regexp
}

func NewGoStreamingParser() *GoStreamingParser {
	return &GoStreamingParser{
		errorPattern: regexp.MustCompile(`^(.+):(\d+):(\d+):\s+(.+)$`),
		testPattern:  regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (.+) \((\d+\.?\d*)s\)$`),
		failPattern:  regexp.MustCompile(`^FAIL\s+(\S+)`),
		skipPattern:  regexp.MustCompile(`^\s+(.+_test\.go:\d+): (.+)$`),
	}
}

func (p *GoStreamingParser) OnLine(line string) []ParsedEvent {
	var events []ParsedEvent

	if event := p.parseError(line); event != nil {
		events = append(events, event)
	}

	if event := p.parseTestResult(line); event != nil {
		events = append(events, event)
	}

	if event := p.parseFailLine(line); event != nil {
		events = append(events, event)
	}

	return events
}

func (p *GoStreamingParser) parseError(line string) *ErrorEvent {
	matches := p.errorPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	lineNum, _ := strconv.Atoi(matches[2])
	colNum, _ := strconv.Atoi(matches[3])

	return &ErrorEvent{
		File:     matches[1],
		Line:     lineNum,
		Column:   colNum,
		Message:  matches[4],
		Severity: "error",
	}
}

func (p *GoStreamingParser) parseTestResult(line string) *TestResultEvent {
	matches := p.testPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	duration, _ := strconv.ParseFloat(matches[3], 64)

	return &TestResultEvent{
		Name:     matches[2],
		Status:   strings.ToLower(matches[1]),
		Duration: duration,
	}
}

func (p *GoStreamingParser) parseFailLine(line string) *ErrorEvent {
	matches := p.failPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	return &ErrorEvent{
		File:     matches[1],
		Message:  "package failed",
		Severity: "error",
	}
}

func (p *GoStreamingParser) Reset() {}

type PytestStreamingParser struct {
	testPattern     *regexp.Regexp
	errorPattern    *regexp.Regexp
	progressPattern *regexp.Regexp
}

func NewPytestStreamingParser() *PytestStreamingParser {
	return &PytestStreamingParser{
		testPattern:     regexp.MustCompile(`^(.+)::\w+\s+(PASSED|FAILED|SKIPPED)`),
		errorPattern:    regexp.MustCompile(`^E\s+(.+)$`),
		progressPattern: regexp.MustCompile(`^\s*(\d+)%`),
	}
}

func (p *PytestStreamingParser) OnLine(line string) []ParsedEvent {
	var events []ParsedEvent

	if event := p.parseTest(line); event != nil {
		events = append(events, event)
	}

	if event := p.parseError(line); event != nil {
		events = append(events, event)
	}

	if event := p.parseProgress(line); event != nil {
		events = append(events, event)
	}

	return events
}

func (p *PytestStreamingParser) parseTest(line string) *TestResultEvent {
	matches := p.testPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	return &TestResultEvent{
		Name:   matches[1],
		Status: strings.ToLower(matches[2]),
	}
}

func (p *PytestStreamingParser) parseError(line string) *ErrorEvent {
	matches := p.errorPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	return &ErrorEvent{
		Message:  matches[1],
		Severity: "error",
	}
}

func (p *PytestStreamingParser) parseProgress(line string) *ProgressEvent {
	matches := p.progressPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	percent, _ := strconv.Atoi(matches[1])

	return &ProgressEvent{
		Percent: percent,
	}
}

func (p *PytestStreamingParser) Reset() {}

type GenericStreamingParser struct {
	errorPatterns   []*regexp.Regexp
	warningPatterns []*regexp.Regexp
}

func NewGenericStreamingParser() *GenericStreamingParser {
	return &GenericStreamingParser{
		errorPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)error[:\s](.+)$`),
			regexp.MustCompile(`(?i)failed[:\s](.+)$`),
			regexp.MustCompile(`^ERR!?\s+(.+)$`),
		},
		warningPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)warning[:\s](.+)$`),
			regexp.MustCompile(`(?i)warn[:\s](.+)$`),
		},
	}
}

func (p *GenericStreamingParser) OnLine(line string) []ParsedEvent {
	var events []ParsedEvent

	if event := p.parseError(line); event != nil {
		events = append(events, event)
	}

	if event := p.parseWarning(line); event != nil {
		events = append(events, event)
	}

	return events
}

func (p *GenericStreamingParser) parseError(line string) *ErrorEvent {
	for _, pattern := range p.errorPatterns {
		if matches := pattern.FindStringSubmatch(line); matches != nil {
			return &ErrorEvent{
				Message:  matches[1],
				Severity: "error",
			}
		}
	}
	return nil
}

func (p *GenericStreamingParser) parseWarning(line string) *WarningEvent {
	for _, pattern := range p.warningPatterns {
		if matches := pattern.FindStringSubmatch(line); matches != nil {
			return &WarningEvent{
				Message: matches[1],
			}
		}
	}
	return nil
}

func (p *GenericStreamingParser) Reset() {}

type StreamingEventHandler interface {
	OnEvent(event ParsedEvent)
}

type StreamingParserWrapper struct {
	parser  StreamingParser
	handler StreamingEventHandler
}

func NewStreamingParserWrapper(parser StreamingParser, handler StreamingEventHandler) *StreamingParserWrapper {
	return &StreamingParserWrapper{
		parser:  parser,
		handler: handler,
	}
}

func (w *StreamingParserWrapper) ProcessLine(line string) {
	events := w.parser.OnLine(line)
	for _, event := range events {
		w.handler.OnEvent(event)
	}
}

func (w *StreamingParserWrapper) Reset() {
	w.parser.Reset()
}

type EventCollector struct {
	mu     sync.Mutex
	events []ParsedEvent
}

func NewEventCollector() *EventCollector {
	return &EventCollector{
		events: make([]ParsedEvent, 0),
	}
}

func (c *EventCollector) OnEvent(event ParsedEvent) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *EventCollector) Events() []ParsedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]ParsedEvent, len(c.events))
	copy(result, c.events)
	return result
}

func (c *EventCollector) Errors() []*ErrorEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errors []*ErrorEvent
	for _, e := range c.events {
		if err, ok := e.(*ErrorEvent); ok {
			errors = append(errors, err)
		}
	}
	return errors
}

func (c *EventCollector) HasFatalError() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.events {
		if c.isFatalError(e) {
			return true
		}
	}
	return false
}

func (c *EventCollector) isFatalError(e ParsedEvent) bool {
	err, ok := e.(*ErrorEvent)
	if !ok {
		return false
	}
	return err.Severity == "fatal" || err.Severity == "error"
}

func (c *EventCollector) Clear() {
	c.mu.Lock()
	c.events = c.events[:0]
	c.mu.Unlock()
}
