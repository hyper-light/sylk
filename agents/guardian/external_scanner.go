package guardian

import (
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/fetch"
	"github.com/google/uuid"
)

// ExternalContentScanner inspects fetched external content for security
// risks beyond what the basic InjectionScanner and SecretSanitizer handle.
// Covers malicious code patterns, supply chain indicators, data
// exfiltration, and polyglot file detection.
type ExternalContentScanner struct {
	maliciousPatterns   []externalPattern
	supplyChainPatterns []externalPattern
	exfiltrationPatterns []externalPattern
}

type externalPattern struct {
	Name     string
	Regex    *regexp.Regexp
	Exclude  func(match string) bool // Optional: return true to suppress the match
	Severity Severity
	Type     FindingType
}

// NewExternalContentScanner creates a scanner with all detection patterns.
func NewExternalContentScanner() *ExternalContentScanner {
	return &ExternalContentScanner{
		maliciousPatterns:    defaultMaliciousPatterns(),
		supplyChainPatterns:  defaultSupplyChainPatterns(),
		exfiltrationPatterns: defaultExfiltrationPatterns(),
	}
}

// FindingTypeMaliciousCode identifies malicious code patterns.
const FindingTypeMaliciousCode FindingType = "malicious_code"

// FindingTypeSupplyChain identifies supply chain attack indicators.
const FindingTypeSupplyChain FindingType = "supply_chain"

// FindingTypeExfiltration identifies data exfiltration patterns.
const FindingTypeExfiltration FindingType = "exfiltration"

// FindingTypePolyglot identifies polyglot files (valid as multiple types).
const FindingTypePolyglot FindingType = "polyglot"

// ScanExternalContent performs a comprehensive scan of fetched content.
// Returns all findings across all scanners.
func (s *ExternalContentScanner) ScanExternalContent(content string, contentType string) []Finding {
	findings := make([]Finding, 0)

	findings = append(findings, s.scanPatterns(content, s.maliciousPatterns)...)
	findings = append(findings, s.scanPatterns(content, s.supplyChainPatterns)...)
	findings = append(findings, s.scanPatterns(content, s.exfiltrationPatterns)...)
	findings = append(findings, s.detectPolyglot([]byte(content), contentType)...)

	return findings
}

// ToInspectionFindings converts Guardian Finding types to fetch.InspectionFinding.
func ToInspectionFindings(findings []Finding) []fetch.InspectionFinding {
	result := make([]fetch.InspectionFinding, len(findings))
	for i, f := range findings {
		result[i] = fetch.InspectionFinding{
			ID:         f.ID,
			Type:       string(f.Type),
			Severity:   f.Severity.String(),
			Confidence: f.Confidence,
			Title:      f.Title,
			Detail:     f.Detail,
			Line:       f.Line,
			Pattern:    f.Pattern,
		}
	}
	return result
}

// DetermineVerdict computes the quarantine verdict from findings.
func DetermineVerdict(findings []Finding) fetch.QuarantineVerdict {
	for _, f := range findings {
		if f.Severity >= SeverityCritical {
			return fetch.VerdictBlocked
		}
	}
	for _, f := range findings {
		if f.Severity >= SeverityHigh {
			return fetch.VerdictFlagged
		}
	}
	for _, f := range findings {
		if f.Severity >= SeverityMedium && f.Confidence >= 0.7 {
			return fetch.VerdictFlagged
		}
	}
	return fetch.VerdictClean
}

func (s *ExternalContentScanner) scanPatterns(content string, patterns []externalPattern) []Finding {
	findings := make([]Finding, 0)
	for _, pat := range patterns {
		locs := pat.Regex.FindAllStringIndex(content, 5) // Cap at 5 matches per pattern
		for _, loc := range locs {
			matchText := content[loc[0]:loc[1]]
			if pat.Exclude != nil && pat.Exclude(matchText) {
				continue
			}
			line := strings.Count(content[:loc[0]], "\n") + 1
			findings = append(findings, Finding{
				ID:         uuid.New().String(),
				Type:       pat.Type,
				Severity:   pat.Severity,
				Confidence: 0.75,
				Title:      pat.Name,
				Detail:     "Matched: " + truncate(matchText, 120),
				Line:       line,
				Pattern:    pat.Name,
				Timestamp:  time.Now(),
			})
		}
	}
	return findings
}

// detectPolyglot checks if content could be interpreted as multiple file types.
func (s *ExternalContentScanner) detectPolyglot(content []byte, contentType string) []Finding {
	findings := make([]Finding, 0)

	if len(content) < 8 {
		return findings
	}

	isHTML := strings.Contains(contentType, "html")
	isPDF := strings.Contains(contentType, "pdf")
	isJS := strings.Contains(contentType, "javascript")

	// PDF+HTML polyglot: PDF header with HTML tags.
	if isPDF && strings.Contains(string(content), "<script") {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypePolyglot,
			Severity:   SeverityCritical,
			Confidence: 0.9,
			Title:      "Polyglot file: PDF contains script tags",
			Detail:     "File claims to be PDF but contains HTML script elements",
			Timestamp:  time.Now(),
		})
	}

	// HTML+shell polyglot: HTML with shell shebangs.
	if isHTML && (strings.HasPrefix(string(content), "#!/") || strings.Contains(string(content), "\n#!/")) {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypePolyglot,
			Severity:   SeverityHigh,
			Confidence: 0.85,
			Title:      "Polyglot file: HTML contains shell shebang",
			Detail:     "File claims to be HTML but contains shell script markers",
			Timestamp:  time.Now(),
		})
	}

	// JS disguised as JSON.
	if strings.Contains(contentType, "json") && isJS {
		findings = append(findings, Finding{
			ID:         uuid.New().String(),
			Type:       FindingTypePolyglot,
			Severity:   SeverityHigh,
			Confidence: 0.8,
			Title:      "Polyglot file: JSON contains JavaScript",
			Timestamp:  time.Now(),
		})
	}

	return findings
}

func defaultMaliciousPatterns() []externalPattern {
	return []externalPattern{
		{
			Name:     "eval/exec injection",
			Regex:    regexp.MustCompile(`(?i)\b(eval|exec|system|popen|subprocess\.call|os\.system|Runtime\.exec)\s*\(`),
			Severity: SeverityHigh,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "shell command injection",
			Regex:    regexp.MustCompile(`(?i)(\$\(|` + "`" + `)[^)` + "`" + `]*(curl|wget|bash|sh|nc|ncat)\b`),
			Severity: SeverityCritical,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "reverse shell pattern",
			Regex:    regexp.MustCompile(`(?i)\b(bash\s+-i|nc\s+-[elp]|/dev/tcp/|mkfifo|socat\s+exec)`),
			Severity: SeverityCritical,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "obfuscated code (hex/unicode escape)",
			Regex:    regexp.MustCompile(`(\\x[0-9a-fA-F]{2}){8,}|(\\u[0-9a-fA-F]{4}){6,}`),
			Severity: SeverityHigh,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "base64 decode + exec",
			Regex:    regexp.MustCompile(`(?i)(base64[._-]?decode|atob)\s*\([^)]+\)\s*[;|&]\s*(eval|exec|system)`),
			Severity: SeverityCritical,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "dynamic import/require",
			Regex:    regexp.MustCompile(`(?i)(require|import)\s*\(\s*[^'"][^)]*\+`),
			Severity: SeverityMedium,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "process environment access",
			Regex:    regexp.MustCompile(`(?i)(process\.env|os\.environ|getenv)\s*[\[(]`),
			Severity: SeverityMedium,
			Type:     FindingTypeMaliciousCode,
		},
		{
			Name:     "file system manipulation",
			Regex:    regexp.MustCompile(`(?i)\b(os\.remove|shutil\.rmtree|fs\.unlink|rimraf)\s*\(`),
			Severity: SeverityHigh,
			Type:     FindingTypeMaliciousCode,
		},
	}
}

func defaultSupplyChainPatterns() []externalPattern {
	return []externalPattern{
		{
			Name:     "suspicious npm install script",
			Regex:    regexp.MustCompile(`"(preinstall|postinstall|preuninstall)"\s*:\s*"[^"]*\b(curl|wget|bash|node\s+-e)`),
			Severity: SeverityCritical,
			Type:     FindingTypeSupplyChain,
		},
		{
			Name:     "typosquat package indicator",
			Regex:    regexp.MustCompile(`(?i)(loadash|requets|coloours|axois|expresss|lodahs|requsts)\b`),
			Severity: SeverityHigh,
			Type:     FindingTypeSupplyChain,
		},
		{
			Name:  "dependency confusion marker",
			Regex: regexp.MustCompile(`(?i)"registry"\s*:\s*"https?://[^"]+"`),
			Exclude: func(match string) bool {
				lower := strings.ToLower(match)
				return strings.Contains(lower, "npmjs.org") ||
					strings.Contains(lower, "registry.yarnpkg.com") ||
					strings.Contains(lower, "pypi.org") ||
					strings.Contains(lower, "pkg.go.dev")
			},
			Severity: SeverityHigh,
			Type:     FindingTypeSupplyChain,
		},
		{
			Name:     "Go replace directive to suspicious host",
			Regex:    regexp.MustCompile(`(?m)^\s*replace\s+\S+\s+=>\s+\S+\.(tk|ml|ga|cf|gq|xyz)/`),
			Severity: SeverityHigh,
			Type:     FindingTypeSupplyChain,
		},
	}
}

func defaultExfiltrationPatterns() []externalPattern {
	return []externalPattern{
		{
			Name:     "outbound webhook in code",
			Regex:    regexp.MustCompile(`(?i)(fetch|axios|http\.post|requests\.post|curl)\s*\(\s*["']https?://[^"']*\b(webhook|exfil|callback|ngrok|pipedream|requestbin)\b`),
			Severity: SeverityCritical,
			Type:     FindingTypeExfiltration,
		},
		{
			Name:     "DNS exfiltration pattern",
			Regex:    regexp.MustCompile(`(?i)(dig|nslookup|host)\s+[A-Za-z0-9+/=]{20,}\.[a-z]+\.[a-z]+`),
			Severity: SeverityCritical,
			Type:     FindingTypeExfiltration,
		},
		{
			Name:     "environment variable exfiltration",
			Regex:    regexp.MustCompile(`(?i)(process\.env|os\.environ|getenv).*\b(fetch|post|send|upload|curl|wget)\b`),
			Severity: SeverityHigh,
			Type:     FindingTypeExfiltration,
		},
		{
			Name:     "encoded credential transmission",
			Regex:    regexp.MustCompile(`(?i)btoa\s*\(\s*(password|secret|token|api.?key|credential)`),
			Severity: SeverityCritical,
			Type:     FindingTypeExfiltration,
		},
	}
}
