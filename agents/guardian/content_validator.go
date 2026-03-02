package guardian

import (
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/security"
	"github.com/google/uuid"
)

// ContentValidator composes SecretSanitizer (credential detection) with
// an InjectionScanner for prompt injection and content safety patterns.
type ContentValidator struct {
	sanitizer        *security.SecretSanitizer
	injectionScanner *InjectionScanner
	scanInjection    bool
}

// NewContentValidator creates a content validator with the given sanitizer.
func NewContentValidator(sanitizer *security.SecretSanitizer, scanInjection bool) *ContentValidator {
	return &ContentValidator{
		sanitizer:        sanitizer,
		injectionScanner: NewInjectionScanner(),
		scanInjection:    scanInjection,
	}
}

// ScanContent scans text content for credentials and injection patterns.
func (cv *ContentValidator) ScanContent(content string) []Finding {
	findings := make([]Finding, 0)

	// Credential scanning via SecretSanitizer.
	if cv.sanitizer != nil {
		credFindings := cv.scanCredentials(content)
		findings = append(findings, credFindings...)
	}

	// Injection scanning.
	if cv.scanInjection {
		injFindings := cv.injectionScanner.Scan(content)
		findings = append(findings, injFindings...)
	}

	return findings
}

// ScanPaths scans file contents at the given paths for credentials.
func (cv *ContentValidator) ScanPaths(paths []string, readFile func(string) (string, error)) []Finding {
	findings := make([]Finding, 0)
	for _, path := range paths {
		content, err := readFile(path)
		if err != nil {
			continue
		}
		for _, f := range cv.ScanContent(content) {
			f.FilePath = path
			findings = append(findings, f)
		}
	}
	return findings
}

func (cv *ContentValidator) scanCredentials(content string) []Finding {
	if cv.sanitizer == nil {
		return nil
	}
	detection := cv.sanitizer.CheckUserPrompt(content)
	if detection == nil || len(detection.Findings) == 0 {
		return nil
	}
	findings := make([]Finding, 0, len(detection.Findings))
	for _, m := range detection.Findings {
		line := strings.Count(content[:clampPosition(m.Position, len(content))], "\n") + 1
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingCredentialLeak,
			Severity:   mapSecretSeverity(m.Severity),
			Confidence: 0.95,
			Title:      "Credential detected: " + m.PatternName,
			Detail:     "Matched pattern: " + m.PatternName,
			Pattern:    m.PatternName,
			Line:       line,
			Timestamp:  time.Now(),
		})
	}
	return findings
}

func clampPosition(pos, max int) int {
	if pos < 0 {
		return 0
	}
	if pos > max {
		return max
	}
	return pos
}

func mapSecretSeverity(severity security.SecretSeverity) Severity {
	switch severity {
	case security.SecretSeverityCritical:
		return SeverityCritical
	case security.SecretSeverityHigh:
		return SeverityHigh
	case security.SecretSeverityMedium:
		return SeverityMedium
	case security.SecretSeverityLow:
		return SeverityLow
	default:
		return SeverityInfo
	}
}

// =============================================================================
// Injection Scanner
// =============================================================================

// InjectionScanner detects prompt injection and content safety issues.
type InjectionScanner struct {
	patterns []injectionPattern
}

type injectionPattern struct {
	Name     string
	Regex    *regexp.Regexp
	Severity Severity
}

// NewInjectionScanner creates a scanner with built-in patterns.
func NewInjectionScanner() *InjectionScanner {
	return &InjectionScanner{
		patterns: defaultInjectionPatterns(),
	}
}

// Scan checks content against all injection patterns.
func (s *InjectionScanner) Scan(content string) []Finding {
	findings := make([]Finding, 0)
	for _, pat := range s.patterns {
		if locs := pat.Regex.FindAllStringIndex(content, -1); len(locs) > 0 {
			for _, loc := range locs {
				matchText := content[loc[0]:loc[1]]
				line := countLines(content[:loc[0]])
				findings = append(findings, Finding{
					ID:         uuid.New().String(),
					Type:       FindingInjection,
					Severity:   pat.Severity,
					Confidence: 0.8,
					Title:      "Injection pattern: " + pat.Name,
					Detail:     "Matched: " + truncate(matchText, 100),
					Pattern:    pat.Name,
					Line:       line,
					Timestamp:  time.Now(),
				})
			}
		}
	}
	return findings
}

func defaultInjectionPatterns() []injectionPattern {
	return []injectionPattern{
		{
			Name:     "system_prompt_override",
			Regex:    regexp.MustCompile(`(?i)(you are now|ignore previous|new instructions|forget everything|disregard above)`),
			Severity: SeverityHigh,
		},
		{
			Name:     "role_confusion",
			Regex:    regexp.MustCompile(`(?i)(as an? (admin|root|system|superuser)|with (admin|root|sudo) access)`),
			Severity: SeverityHigh,
		},
		{
			Name:     "script_injection",
			Regex:    regexp.MustCompile(`(?i)(<script[^>]*>|<iframe[^>]*>|javascript:|data:text/html)`),
			Severity: SeverityCritical,
		},
		{
			Name:     "encoded_payload",
			Regex:    regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`),
			Severity: SeverityMedium,
		},
		{
			Name:     "prompt_delimiter",
			Regex:    regexp.MustCompile(`(?i)(###\s*(system|instruction|prompt)|<\|im_start\|>|<\|endoftext\|>)`),
			Severity: SeverityHigh,
		},
	}
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
