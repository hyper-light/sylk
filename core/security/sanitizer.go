package security

import (
	"path/filepath"
	"sync"
)

const DefaultRedactText = "[REDACTED]"

type SanitizeResult struct {
	Sanitized       bool
	RedactionCount  int
	MatchedPatterns []string
	IsStubbed       bool
}

type SecretDetection struct {
	Findings []SecretFinding
}

type SecretFinding struct {
	PatternName string
	Severity    SecretSeverity
	Position    int
	Context     string
}

type SanitizerMetrics struct {
	mu              sync.RWMutex
	RedactionCounts map[string]int64
	SkippedFiles    map[string]int64
	TotalScanned    int64
	TotalRedacted   int64
}

type SecretSanitizer struct {
	mu             sync.RWMutex
	patterns       *PatternManager
	sensitiveFiles []string
	redactText     string
	metrics        *SanitizerMetrics
}

func NewSecretSanitizer() *SecretSanitizer {
	return &SecretSanitizer{
		patterns:       NewPatternManager(),
		sensitiveFiles: SensitiveFilePatterns(),
		redactText:     DefaultRedactText,
		metrics:        newSanitizerMetrics(),
	}
}

func newSanitizerMetrics() *SanitizerMetrics {
	return &SanitizerMetrics{
		RedactionCounts: make(map[string]int64),
		SkippedFiles:    make(map[string]int64),
	}
}

func (s *SecretSanitizer) SetRedactText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redactText = text
}

func (s *SecretSanitizer) SanitizeForIndex(path string, content []byte) ([]byte, *SanitizeResult) {
	filename := filepath.Base(path)
	s.recordScanned()

	if s.isSensitiveFile(filename) {
		s.recordSkippedFile(filename)
		return s.createStub(filename), &SanitizeResult{IsStubbed: true, Sanitized: true}
	}

	return s.sanitizeContent(content)
}

func (s *SecretSanitizer) isSensitiveFile(filename string) bool {
	for _, pattern := range s.sensitiveFiles {
		if matched, _ := matchGlob(pattern, filename); matched {
			return true
		}
	}
	return false
}

func (s *SecretSanitizer) createStub(filename string) []byte {
	return []byte("# " + filename + "\n(contents not indexed for security)")
}

func (s *SecretSanitizer) sanitizeContent(content []byte) ([]byte, *SanitizeResult) {
	result := &SanitizeResult{}
	text := string(content)

	s.mu.RLock()
	patterns := s.patterns.Patterns()
	redactText := s.redactText
	s.mu.RUnlock()

	for _, p := range patterns {
		matches := p.Pattern.FindAllStringIndex(text, -1)
		if len(matches) > 0 {
			result.MatchedPatterns = append(result.MatchedPatterns, p.Name)
			result.RedactionCount += len(matches)
			text = p.Pattern.ReplaceAllString(text, redactText)
			s.recordRedaction(p.Name, len(matches))
		}
	}

	result.Sanitized = result.RedactionCount > 0
	return []byte(text), result
}

func (s *SecretSanitizer) CheckUserPrompt(prompt string) *SecretDetection {
	detection := &SecretDetection{}

	s.mu.RLock()
	patterns := s.patterns.Patterns()
	s.mu.RUnlock()

	for _, p := range patterns {
		matches := p.Pattern.FindAllStringIndex(prompt, -1)
		for _, match := range matches {
			finding := SecretFinding{
				PatternName: p.Name,
				Severity:    p.Severity,
				Position:    match[0],
				Context:     maskContext(prompt, match[0], match[1]),
			}
			detection.Findings = append(detection.Findings, finding)
		}
	}

	return detection
}

func maskContext(text string, start, end int) string {
	contextStart := start - 10
	if contextStart < 0 {
		contextStart = 0
	}
	contextEnd := end + 10
	if contextEnd > len(text) {
		contextEnd = len(text)
	}

	prefix := text[contextStart:start]
	suffix := text[end:contextEnd]

	return prefix + "***" + suffix
}

func (s *SecretSanitizer) SanitizeToolOutput(output string) (string, int) {
	if output == "" {
		return output, 0
	}

	s.mu.RLock()
	patterns := s.patterns.Patterns()
	redactText := s.redactText
	s.mu.RUnlock()

	totalRedactions := 0
	for _, p := range patterns {
		matches := p.Pattern.FindAllStringIndex(output, -1)
		if len(matches) > 0 {
			totalRedactions += len(matches)
			output = p.Pattern.ReplaceAllString(output, redactText)
			s.recordRedaction(p.Name, len(matches))
		}
	}

	return output, totalRedactions
}

func (s *SecretSanitizer) recordRedaction(patternName string, count int) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	s.metrics.RedactionCounts[patternName] += int64(count)
	s.metrics.TotalRedacted += int64(count)
}

func (s *SecretSanitizer) recordSkippedFile(filename string) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	s.metrics.SkippedFiles[filename]++
}

func (s *SecretSanitizer) recordScanned() {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	s.metrics.TotalScanned++
}

func (s *SecretSanitizer) GetMetrics() SanitizerMetrics {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	result := SanitizerMetrics{
		RedactionCounts: make(map[string]int64),
		SkippedFiles:    make(map[string]int64),
		TotalScanned:    s.metrics.TotalScanned,
		TotalRedacted:   s.metrics.TotalRedacted,
	}

	for k, v := range s.metrics.RedactionCounts {
		result.RedactionCounts[k] = v
	}
	for k, v := range s.metrics.SkippedFiles {
		result.SkippedFiles[k] = v
	}

	return result
}

func (s *SecretSanitizer) AddPattern(p *SecretPattern) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patterns.AddPattern(p)
}

func (s *SecretSanitizer) RemovePattern(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.patterns.RemovePattern(name)
}

func (d *SecretDetection) HasFindings() bool {
	return len(d.Findings) > 0
}

func (d *SecretDetection) HighestSeverity() SecretSeverity {
	highest := SecretSeverityLow
	for _, f := range d.Findings {
		if severityRank(f.Severity) > severityRank(highest) {
			highest = f.Severity
		}
	}
	return highest
}

func severityRank(s SecretSeverity) int {
	switch s {
	case SecretSeverityCritical:
		return 4
	case SecretSeverityHigh:
		return 3
	case SecretSeverityMedium:
		return 2
	default:
		return 1
	}
}
