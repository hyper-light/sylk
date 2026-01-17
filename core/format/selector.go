package format

import (
	"path/filepath"
	"sort"
	"strings"
)

// FormatterSelector selects appropriate formatters for files based on extension and availability.
type FormatterSelector struct {
	formatters []*FormatterDefinition
}

// NewFormatterSelector creates a FormatterSelector with builtin formatters.
func NewFormatterSelector() *FormatterSelector {
	return &FormatterSelector{
		formatters: BuiltinFormatters,
	}
}

// NewFormatterSelectorWith creates a FormatterSelector with custom formatters.
func NewFormatterSelectorWith(formatters []*FormatterDefinition) *FormatterSelector {
	return &FormatterSelector{
		formatters: formatters,
	}
}

// SelectFormatter returns the highest-priority enabled formatter for a file.
// Returns nil if no formatter is available for the file extension.
func (s *FormatterSelector) SelectFormatter(root, filePath string) *FormatterDefinition {
	formatters := s.SelectFormatters(root, filePath)
	if len(formatters) == 0 {
		return nil
	}
	return formatters[0]
}

// SelectFormatters returns all enabled formatters for a file, sorted by priority (highest first).
func (s *FormatterSelector) SelectFormatters(root, filePath string) []*FormatterDefinition {
	ext := normalizeExtension(filePath)
	var matches []*FormatterDefinition

	for _, f := range s.formatters {
		if matchesExtension(f, ext) && isFormatterEnabled(f, root) {
			matches = append(matches, f)
		}
	}

	sortByPriority(matches)
	return matches
}

// DetectFormatters returns detection results for all formatters for a file.
func (s *FormatterSelector) DetectFormatters(root, filePath string) []DetectionResult {
	ext := normalizeExtension(filePath)
	var results []DetectionResult

	for _, f := range s.formatters {
		if !matchesExtension(f, ext) {
			continue
		}
		result := buildDetectionResult(f, root)
		results = append(results, result)
	}

	sortDetectionResults(results)
	return results
}

func normalizeExtension(filePath string) string {
	return strings.ToLower(filepath.Ext(filePath))
}

func matchesExtension(f *FormatterDefinition, ext string) bool {
	for _, supported := range f.Extensions {
		if supported == ext {
			return true
		}
	}
	return false
}

func isFormatterEnabled(f *FormatterDefinition, root string) bool {
	if f.Enabled == nil {
		return true
	}
	return f.Enabled(root)
}

func sortByPriority(formatters []*FormatterDefinition) {
	sort.Slice(formatters, func(i, j int) bool {
		return formatters[i].Priority > formatters[j].Priority
	})
}

func buildDetectionResult(f *FormatterDefinition, root string) DetectionResult {
	available := isFormatterEnabled(f, root)
	return DetectionResult{
		FormatterID: f.ID,
		Confidence:  calculateConfidence(f, available),
		Reason:      buildReason(f, available),
	}
}

func calculateConfidence(f *FormatterDefinition, available bool) float64 {
	if !available {
		return 0.0
	}
	return float64(f.Priority) / 100.0
}

func buildReason(f *FormatterDefinition, available bool) string {
	if !available {
		return "binary not found or not configured"
	}
	return "binary available"
}

func sortDetectionResults(results []DetectionResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})
}
