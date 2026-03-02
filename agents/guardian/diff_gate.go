package guardian

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/security"
	"github.com/google/uuid"
)

// DiffGate reviews diffs for suspicious patterns before commits.
type DiffGate struct {
	sanitizer           *security.SecretSanitizer
	suspiciousPatterns  []*regexp.Regexp
}

// NewDiffGate creates a diff gate with optional additional suspicious patterns.
func NewDiffGate(sanitizer *security.SecretSanitizer, extraPatterns []string) *DiffGate {
	patterns := make([]*regexp.Regexp, 0, len(extraPatterns))
	for _, p := range extraPatterns {
		re, err := regexp.Compile(p)
		if err == nil {
			patterns = append(patterns, re)
		}
	}
	return &DiffGate{
		sanitizer:          sanitizer,
		suspiciousPatterns: patterns,
	}
}

// ReviewDiff analyses a diff string and returns findings.
func (dg *DiffGate) ReviewDiff(diffContent string) DiffReviewResult {
	findings := make([]Finding, 0)
	stats := dg.computeStats(diffContent)

	// Large deletions.
	findings = append(findings, dg.checkLargeDeletions(diffContent, stats)...)

	// Sensitive file changes.
	findings = append(findings, dg.checkSensitiveFiles(diffContent)...)

	// Credential insertions.
	findings = append(findings, dg.checkCredentialInsertions(diffContent)...)

	// Binary blobs.
	if stats.BinaryFiles > 0 {
		findings = append(findings, Finding{
			ID:       uuid.New().String(),
			Type:     FindingBinaryBlob,
			Severity: SeverityMedium,
			Title:    "Binary file additions detected",
			Timestamp: time.Now(),
			Data:     map[string]any{"binary_count": stats.BinaryFiles},
		})
	}

	// Custom suspicious patterns.
	findings = append(findings, dg.checkCustomPatterns(diffContent)...)

	return DiffReviewResult{
		Clean:    len(findings) == 0,
		Findings: findings,
		Stats:    stats,
	}
}

// PreCommitCheck orchestrates a full pre-commit review:
// dirty status → credential scan → diff review → composite result.
func (dg *DiffGate) PreCommitCheck(diffContent string, stagedPaths []string, readFile func(string) (string, error)) DiffReviewResult {
	result := dg.ReviewDiff(diffContent)

	// Scan staged files for credentials.
	if dg.sanitizer != nil {
		for _, path := range stagedPaths {
			content, err := readFile(path)
			if err != nil {
				continue
			}
			detection := dg.sanitizer.CheckUserPrompt(content)
			if detection == nil {
				continue
			}
			for _, m := range detection.Findings {
				result.Findings = append(result.Findings, Finding{
					ID:         uuid.New().String(),
					Type:       FindingCredentialLeak,
					Severity:   SeverityCritical,
					Confidence: 0.95,
					Title:      "Staged credential: " + m.PatternName,
					Detail:     "File: " + path,
					FilePath:   path,
					Pattern:    m.PatternName,
					Timestamp:  time.Now(),
				})
			}
		}
	}

	result.Clean = len(result.Findings) == 0
	return result
}

func (dg *DiffGate) computeStats(diff string) DiffStats {
	var stats DiffStats
	lines := strings.Split(diff, "\n")
	currentFile := ""

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			stats.FilesChanged++
			currentFile = extractDiffFileName(line)
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.LinesAdded++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.LinesDeleted++
		}
		if strings.Contains(line, "Binary files") {
			stats.BinaryFiles++
		}
		if isSensitivePath(currentFile) {
			stats.SensitiveFiles++
		}
	}
	return stats
}

func (dg *DiffGate) checkLargeDeletions(diff string, stats DiffStats) []Finding {
	findings := make([]Finding, 0)

	// Total large deletion.
	if stats.LinesDeleted > 200 {
		findings = append(findings, Finding{
			ID:       uuid.New().String(),
			Type:     FindingLargeDeletion,
			Severity: SeverityHigh,
			Title:    "Large total deletion detected",
			Detail:   "More than 200 lines deleted across all files",
			Timestamp: time.Now(),
			Data:     map[string]any{"lines_deleted": stats.LinesDeleted},
		})
	}

	// Per-file large deletion (>50 lines).
	perFileDeleted := countPerFileDeletedLines(diff)
	for file, count := range perFileDeleted {
		if count > 50 {
			findings = append(findings, Finding{
				ID:       uuid.New().String(),
				Type:     FindingLargeDeletion,
				Severity: SeverityMedium,
				Title:    "Large deletion in single file",
				Detail:   file,
				FilePath: file,
				Timestamp: time.Now(),
				Data:     map[string]any{"lines_deleted": count},
			})
		}
	}

	return findings
}

func (dg *DiffGate) checkSensitiveFiles(diff string) []Finding {
	findings := make([]Finding, 0)
	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		if !strings.HasPrefix(line, "diff --git") {
			continue
		}
		file := extractDiffFileName(line)
		if isSensitivePath(file) {
			findings = append(findings, Finding{
				ID:       uuid.New().String(),
				Type:     FindingSensitiveFile,
				Severity: SeverityHigh,
				Title:    "Sensitive file modified",
				Detail:   file,
				FilePath: file,
				Timestamp: time.Now(),
			})
		}
	}
	return findings
}

func (dg *DiffGate) checkCredentialInsertions(diff string) []Finding {
	if dg.sanitizer == nil {
		return nil
	}
	findings := make([]Finding, 0)
	// Only scan added lines.
	lines := strings.Split(diff, "\n")
	var addedContent strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			addedContent.WriteString(line[1:])
			addedContent.WriteString("\n")
		}
	}
	detection := dg.sanitizer.CheckUserPrompt(addedContent.String())
	if detection == nil {
		return findings
	}
	for _, m := range detection.Findings {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingCredentialLeak,
			Severity:   SeverityCritical,
			Confidence: 0.95,
			Title:      "Credential in diff additions: " + m.PatternName,
			Pattern:    m.PatternName,
			Timestamp:  time.Now(),
		})
	}
	return findings
}

func (dg *DiffGate) checkCustomPatterns(diff string) []Finding {
	findings := make([]Finding, 0)
	for _, re := range dg.suspiciousPatterns {
		if locs := re.FindAllStringIndex(diff, -1); len(locs) > 0 {
			findings = append(findings, Finding{
				ID:       uuid.New().String(),
				Type:     FindingSuspiciousDiff,
				Severity: SeverityMedium,
				Title:    "Custom suspicious pattern matched",
				Pattern:  re.String(),
				Timestamp: time.Now(),
				Data:     map[string]any{"match_count": len(locs)},
			})
		}
	}
	return findings
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var sensitiveExtensions = map[string]bool{
	".env": true, ".pem": true, ".key": true, ".p12": true, ".pfx": true,
	".cer": true, ".crt": true, ".jks": true, ".keystore": true,
}

var sensitiveNames = map[string]bool{
	".env": true, ".env.local": true, ".env.production": true,
	"credentials.json": true, "service-account.json": true,
	"id_rsa": true, "id_ed25519": true, "id_ecdsa": true,
}

var sensitivePatterns = []string{
	"*.yml", "*.yaml", // CI configs
}

func isSensitivePath(path string) bool {
	base := filepath.Base(path)
	ext := filepath.Ext(path)

	if sensitiveExtensions[ext] {
		return true
	}
	if sensitiveNames[base] {
		return true
	}
	// CI config files.
	if strings.Contains(path, ".github/workflows") || strings.Contains(path, ".gitlab-ci") {
		return true
	}
	for _, pattern := range sensitivePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			// Only flag if also in a CI-related directory.
			if strings.Contains(path, "ci") || strings.Contains(path, "deploy") {
				return true
			}
		}
	}
	return false
}

func extractDiffFileName(line string) string {
	// "diff --git a/path/to/file b/path/to/file"
	parts := strings.Fields(line)
	if len(parts) >= 4 {
		return strings.TrimPrefix(parts[2], "a/")
	}
	return ""
}

func countPerFileDeletedLines(diff string) map[string]int {
	counts := make(map[string]int)
	lines := strings.Split(diff, "\n")
	currentFile := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			currentFile = extractDiffFileName(line)
		}
		if currentFile != "" && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			counts[currentFile]++
		}
	}
	return counts
}
