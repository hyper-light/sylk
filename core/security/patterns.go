package security

import (
	"regexp"
	"sync"
)

type SecretSeverity string

const (
	SecretSeverityLow      SecretSeverity = "low"
	SecretSeverityMedium   SecretSeverity = "medium"
	SecretSeverityHigh     SecretSeverity = "high"
	SecretSeverityCritical SecretSeverity = "critical"
)

type SecretPattern struct {
	Name     string
	Pattern  *regexp.Regexp
	Severity SecretSeverity
}

type PatternManager struct {
	mu       sync.RWMutex
	patterns []*SecretPattern
}

func NewPatternManager() *PatternManager {
	pm := &PatternManager{
		patterns: make([]*SecretPattern, 0),
	}
	pm.loadDefaultPatterns()
	return pm
}

func (pm *PatternManager) loadDefaultPatterns() {
	pm.patterns = append(pm.patterns, defaultSecretPatterns()...)
}

func defaultSecretPatterns() []*SecretPattern {
	return []*SecretPattern{
		newPattern("api_key_generic", `(?i)(api[_-]?key|apikey)\s*[=:]\s*['"]?[a-zA-Z0-9_\-]{20,}`, SecretSeverityHigh),
		newPattern("private_key_rsa", `-----BEGIN\s+RSA\s+PRIVATE\s+KEY-----`, SecretSeverityCritical),
		newPattern("private_key_dsa", `-----BEGIN\s+DSA\s+PRIVATE\s+KEY-----`, SecretSeverityCritical),
		newPattern("private_key_ec", `-----BEGIN\s+EC\s+PRIVATE\s+KEY-----`, SecretSeverityCritical),
		newPattern("private_key_openssh", `-----BEGIN\s+OPENSSH\s+PRIVATE\s+KEY-----`, SecretSeverityCritical),
		newPattern("private_key_generic", `-----BEGIN\s+PRIVATE\s+KEY-----`, SecretSeverityCritical),
		newPattern("aws_access_key", `AKIA[0-9A-Z]{16}`, SecretSeverityCritical),
		newPattern("aws_secret_key", `(?i)aws[_-]?secret[_-]?access[_-]?key\s*[=:]\s*['"]?[A-Za-z0-9/+=]{40}`, SecretSeverityCritical),
		newPattern("github_pat", `ghp_[a-zA-Z0-9]{36}`, SecretSeverityCritical),
		newPattern("github_oauth", `gho_[a-zA-Z0-9]{36}`, SecretSeverityCritical),
		newPattern("github_app", `ghu_[a-zA-Z0-9]{36}`, SecretSeverityHigh),
		newPattern("openai_key", `sk-[a-zA-Z0-9]{20,}`, SecretSeverityCritical),
		newPattern("anthropic_key", `sk-ant-[a-zA-Z0-9\-]{93}`, SecretSeverityCritical),
		newPattern("slack_token", `xox[baprs]-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*`, SecretSeverityHigh),
		newPattern("slack_webhook", `https://hooks\.slack\.com/services/T[a-zA-Z0-9_]+/B[a-zA-Z0-9_]+/[a-zA-Z0-9_]+`, SecretSeverityHigh),
		newPattern("stripe_key", `sk_live_[0-9a-zA-Z]{24}`, SecretSeverityCritical),
		newPattern("stripe_test", `sk_test_[0-9a-zA-Z]{24}`, SecretSeverityMedium),
		newPattern("password_assignment", `(?i)(password|passwd|pwd)\s*[=:]\s*['"][^'"]{8,}['"]`, SecretSeverityHigh),
		newPattern("connection_string", `(?i)(mongodb|mysql|postgres|postgresql|redis):\/\/[^\s'"]+:[^\s'"]+@`, SecretSeverityCritical),
		newPattern("bearer_token", `(?i)bearer\s+[a-zA-Z0-9_\-\.=]{20,}`, SecretSeverityHigh),
		newPattern("basic_auth", `(?i)basic\s+[a-zA-Z0-9+/=]{20,}`, SecretSeverityHigh),
		newPattern("jwt_token", `eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*`, SecretSeverityHigh),
		newPattern("google_api_key", `AIza[0-9A-Za-z\-_]{35}`, SecretSeverityHigh),
		newPattern("heroku_api_key", `(?i)heroku[_-]?api[_-]?key\s*[=:]\s*['"]?[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, SecretSeverityHigh),
		newPattern("npm_token", `npm_[a-zA-Z0-9]{36}`, SecretSeverityHigh),
		newPattern("pypi_token", `pypi-[a-zA-Z0-9_-]{32,}`, SecretSeverityHigh),
	}
}

func newPattern(name, pattern string, severity SecretSeverity) *SecretPattern {
	return &SecretPattern{
		Name:     name,
		Pattern:  regexp.MustCompile(pattern),
		Severity: severity,
	}
}

func (pm *PatternManager) Patterns() []*SecretPattern {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]*SecretPattern, len(pm.patterns))
	copy(result, pm.patterns)
	return result
}

func (pm *PatternManager) AddPattern(p *SecretPattern) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.patterns = append(pm.patterns, p)
}

func (pm *PatternManager) RemovePattern(name string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i, p := range pm.patterns {
		if p.Name == name {
			pm.patterns = append(pm.patterns[:i], pm.patterns[i+1:]...)
			return true
		}
	}
	return false
}

func (pm *PatternManager) GetPattern(name string) *SecretPattern {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, p := range pm.patterns {
		if p.Name == name {
			return p
		}
	}
	return nil
}

var defaultSensitiveFilePatterns = []string{
	".env",
	".env.*",
	"*.env",
	".env.local",
	".env.development",
	".env.production",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
	"*.jks",
	"*credentials*",
	"*secret*",
	".npmrc",
	".pypirc",
	".netrc",
	".htpasswd",
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"*.ppk",
	"known_hosts",
	"authorized_keys",
	"shadow",
	"passwd",
	"*.keystore",
	"*.truststore",
	"service-account*.json",
	"gcloud*.json",
	"firebase*.json",
	"aws-credentials",
	".aws/credentials",
}

func SensitiveFilePatterns() []string {
	result := make([]string, len(defaultSensitiveFilePatterns))
	copy(result, defaultSensitiveFilePatterns)
	return result
}

func IsSensitiveFile(filename string) bool {
	for _, pattern := range defaultSensitiveFilePatterns {
		matched, _ := matchGlob(pattern, filename)
		if matched {
			return true
		}
	}
	return false
}

func matchGlob(pattern, name string) (bool, error) {
	if pattern == name {
		return true, nil
	}
	return matchGlobRecursive(pattern, name)
}

func matchGlobRecursive(pattern, name string) (bool, error) {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			return matchStar(pattern, name)
		case '?':
			if len(name) == 0 {
				return false, nil
			}
			pattern = pattern[1:]
			name = name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false, nil
			}
			pattern = pattern[1:]
			name = name[1:]
		}
	}
	return len(name) == 0, nil
}

func matchStar(pattern, name string) (bool, error) {
	for i := len(name); i >= 0; i-- {
		matched, err := matchGlobRecursive(pattern[1:], name[i:])
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}
