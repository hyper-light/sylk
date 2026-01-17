package test

import (
	"sort"
)

type TestFrameworkSelector struct {
	frameworks []*TestFrameworkDefinition
}

func NewTestFrameworkSelector() *TestFrameworkSelector {
	return &TestFrameworkSelector{
		frameworks: BuiltinFrameworks,
	}
}

func NewTestFrameworkSelectorWith(frameworks []*TestFrameworkDefinition) *TestFrameworkSelector {
	return &TestFrameworkSelector{
		frameworks: frameworks,
	}
}

func (s *TestFrameworkSelector) SelectFramework(projectDir string) *TestFrameworkDefinition {
	frameworks := s.SelectFrameworks(projectDir)
	if len(frameworks) == 0 {
		return nil
	}
	return frameworks[0]
}

func (s *TestFrameworkSelector) SelectFrameworks(projectDir string) []*TestFrameworkDefinition {
	var matches []*TestFrameworkDefinition

	for _, f := range s.frameworks {
		if isFrameworkEnabled(f, projectDir) {
			matches = append(matches, f)
		}
	}

	sortFrameworksByPriority(matches)
	return matches
}

func (s *TestFrameworkSelector) SelectFrameworkByLanguage(projectDir, lang string) *TestFrameworkDefinition {
	frameworks := s.SelectFrameworksByLanguage(projectDir, lang)
	if len(frameworks) == 0 {
		return nil
	}
	return frameworks[0]
}

func (s *TestFrameworkSelector) SelectFrameworksByLanguage(projectDir, lang string) []*TestFrameworkDefinition {
	var matches []*TestFrameworkDefinition

	for _, f := range s.frameworks {
		if f.Language == lang && isFrameworkEnabled(f, projectDir) {
			matches = append(matches, f)
		}
	}

	sortFrameworksByPriority(matches)
	return matches
}

func (s *TestFrameworkSelector) DetectFrameworks(projectDir string) []DetectionResult {
	var results []DetectionResult

	for _, f := range s.frameworks {
		result := buildFrameworkDetection(f, projectDir)
		if result.Confidence > 0 {
			results = append(results, result)
		}
	}

	sortDetectionResultsByConfidence(results)
	return results
}

func isFrameworkEnabled(f *TestFrameworkDefinition, projectDir string) bool {
	if f.Enabled == nil {
		return true
	}
	return f.Enabled(projectDir)
}

func sortFrameworksByPriority(frameworks []*TestFrameworkDefinition) {
	sort.Slice(frameworks, func(i, j int) bool {
		return frameworks[i].Priority > frameworks[j].Priority
	})
}

func buildFrameworkDetection(f *TestFrameworkDefinition, projectDir string) DetectionResult {
	enabled := isFrameworkEnabled(f, projectDir)
	confidence := computeFrameworkConfidence(f, enabled)

	return DetectionResult{
		FrameworkID: f.ID,
		Confidence:  confidence,
		Reason:      computeFrameworkReason(enabled),
	}
}

func computeFrameworkConfidence(f *TestFrameworkDefinition, enabled bool) float64 {
	if !enabled {
		return 0.0
	}
	return float64(f.Priority) / 100.0
}

func computeFrameworkReason(enabled bool) string {
	if !enabled {
		return "framework not available or not configured"
	}
	return "framework available and configured"
}

func sortDetectionResultsByConfidence(results []DetectionResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})
}
