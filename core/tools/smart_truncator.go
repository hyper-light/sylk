package tools

import (
	"fmt"
	"strings"
)

type TruncatedOutput struct {
	Summary        string
	ImportantLines []string
	FirstLines     []string
	LastLines      []string
	TotalLines     int
	Truncated      bool
}

type SmartTruncatorConfig struct {
	KeepFirstLines int
	KeepLastLines  int
	MaxBufferSize  int
}

func DefaultSmartTruncatorConfig() SmartTruncatorConfig {
	return SmartTruncatorConfig{
		KeepFirstLines: 50,
		KeepLastLines:  100,
		MaxBufferSize:  1024 * 1024,
	}
}

type SmartTruncator struct {
	detector  *ImportanceDetector
	keepFirst int
	keepLast  int
	maxSize   int
}

func NewSmartTruncator(config SmartTruncatorConfig, detector *ImportanceDetector) *SmartTruncator {
	return &SmartTruncator{
		detector:  detector,
		keepFirst: config.KeepFirstLines,
		keepLast:  config.KeepLastLines,
		maxSize:   config.MaxBufferSize,
	}
}

func (t *SmartTruncator) Truncate(stdout, stderr []byte) *TruncatedOutput {
	combined := append(stdout, stderr...)
	lines := strings.Split(string(combined), "\n")

	result := &TruncatedOutput{
		TotalLines: len(lines),
		Truncated:  len(combined) > t.maxSize,
	}

	result.FirstLines = t.extractFirstLines(lines)
	result.LastLines = t.extractLastLines(lines)
	result.ImportantLines = t.extractImportantLines(lines, result.FirstLines, result.LastLines)
	result.Summary = t.generateSummary(result)

	return result
}

func (t *SmartTruncator) extractFirstLines(lines []string) []string {
	if len(lines) <= t.keepFirst {
		return lines
	}
	return lines[:t.keepFirst]
}

func (t *SmartTruncator) extractLastLines(lines []string) []string {
	if len(lines) <= t.keepLast {
		return nil
	}
	return lines[len(lines)-t.keepLast:]
}

func (t *SmartTruncator) buildSeenSet(first, last []string) map[string]bool {
	seen := make(map[string]bool)
	for _, line := range first {
		seen[line] = true
	}
	for _, line := range last {
		seen[line] = true
	}
	return seen
}

func (t *SmartTruncator) extractImportantLines(lines, first, last []string) []string {
	seen := t.buildSeenSet(first, last)
	var important []string
	for _, line := range lines {
		if seen[line] || !t.detector.IsImportant(line) {
			continue
		}
		important = append(important, line)
		seen[line] = true
	}
	return important
}

func (t *SmartTruncator) countErrorsAndWarnings(lines []string) (errors, warnings int) {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") {
			errors++
		}
		if strings.Contains(lower, "warning") {
			warnings++
		}
	}
	return errors, warnings
}

func (t *SmartTruncator) generateSummary(r *TruncatedOutput) string {
	allLines := append(r.ImportantLines, r.FirstLines...)
	allLines = append(allLines, r.LastLines...)

	errorCount, warningCount := t.countErrorsAndWarnings(allLines)

	if errorCount > 0 || warningCount > 0 {
		return fmt.Sprintf("%d errors, %d warnings in %d lines", errorCount, warningCount, r.TotalLines)
	}
	return fmt.Sprintf("%d lines of output", r.TotalLines)
}
