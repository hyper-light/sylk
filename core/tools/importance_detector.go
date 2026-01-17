package tools

import (
	"regexp"
	"sync"
)

type ImportanceDetector struct {
	patterns []*regexp.Regexp
	mu       sync.RWMutex
}

func NewImportanceDetector(patterns []string) *ImportanceDetector {
	d := &ImportanceDetector{
		patterns: make([]*regexp.Regexp, 0, len(patterns)),
	}
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			d.patterns = append(d.patterns, re)
		}
	}
	return d
}

func DefaultImportancePatterns() []string {
	return []string{
		`(?i)error`,
		`(?i)warning`,
		`(?i)failed`,
		`(?i)exception`,
		`(?i)panic`,
		`\w+\.\w+:\d+:\d+`,
		`^\s+at\s+`,
	}
}

func (d *ImportanceDetector) IsImportant(line string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, pattern := range d.patterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func (d *ImportanceDetector) AddPattern(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.patterns = append(d.patterns, re)
	d.mu.Unlock()
	return nil
}

func (d *ImportanceDetector) PatternCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.patterns)
}
