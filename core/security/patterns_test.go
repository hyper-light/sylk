package security

import (
	"regexp"
	"sync"
	"testing"
)

func TestDefaultPatterns_APIKey(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("api_key_generic")
	if p == nil {
		t.Fatal("api_key_generic pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`API_KEY=abc123def456ghi789jkl012mno`, true},
		{`api-key: "abcdefghijklmnopqrstuvwx"`, true},
		{`apiKey = 'a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6'`, true},
		{`api_key: short`, false},
		{`this is just normal text`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_PrivateKey(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()

	tests := []struct {
		patternName string
		input       string
		matches     bool
	}{
		{"private_key_rsa", "-----BEGIN RSA PRIVATE KEY-----", true},
		{"private_key_dsa", "-----BEGIN DSA PRIVATE KEY-----", true},
		{"private_key_ec", "-----BEGIN EC PRIVATE KEY-----", true},
		{"private_key_openssh", "-----BEGIN OPENSSH PRIVATE KEY-----", true},
		{"private_key_generic", "-----BEGIN PRIVATE KEY-----", true},
		{"private_key_rsa", "-----BEGIN PUBLIC KEY-----", false},
	}

	for _, tt := range tests {
		p := pm.GetPattern(tt.patternName)
		if p == nil {
			t.Fatalf("pattern %s not found", tt.patternName)
		}
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("%s input %q: got %v, want %v", tt.patternName, tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_AWS(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("aws_access_key")
	if p == nil {
		t.Fatal("aws_access_key pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`AKIAIOSFODNN7EXAMPLE`, true},
		{`AKIAI44QH8DHBEXAMPLE`, true},
		{`AKIA1234567890ABCDEF`, true},
		{`ASIA1234567890ABCDEF`, false},
		{`regular text`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_GitHub(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()

	tests := []struct {
		patternName string
		input       string
		matches     bool
	}{
		{"github_pat", "ghp_1234567890abcdefghijklmnopqrstuvwxyz", true},
		{"github_oauth", "gho_1234567890abcdefghijklmnopqrstuvwxyz", true},
		{"github_app", "ghu_1234567890abcdefghijklmnopqrstuvwxyz", true},
		{"github_pat", "github_token_abc", false},
	}

	for _, tt := range tests {
		p := pm.GetPattern(tt.patternName)
		if p == nil {
			t.Fatalf("pattern %s not found", tt.patternName)
		}
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("%s input %q: got %v, want %v", tt.patternName, tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_OpenAI(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("openai_key")
	if p == nil {
		t.Fatal("openai_key pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJ`, true},
		{`sk-proj-abc`, false},
		{`sk-short`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_JWT(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("jwt_token")
	if p == nil {
		t.Fatal("jwt_token pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`, true},
		{`not.a.jwt`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_Password(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("password_assignment")
	if p == nil {
		t.Fatal("password_assignment pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`password = "supersecret123"`, true},
		{`PASSWORD: "mysecretpassword"`, true},
		{`pwd = "longenoughpassword"`, true},
		{`password = "short"`, false},
		{`the password is used`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestDefaultPatterns_ConnectionString(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	p := pm.GetPattern("connection_string")
	if p == nil {
		t.Fatal("connection_string pattern not found")
	}

	tests := []struct {
		input   string
		matches bool
	}{
		{`mongodb://user:pass@localhost:27017/db`, true},
		{`postgres://admin:secret@host:5432/database`, true},
		{`mysql://root:password@127.0.0.1:3306/mydb`, true},
		{`redis://default:mypassword@redis.host:6379`, true},
		{`https://api.example.com`, false},
	}

	for _, tt := range tests {
		if got := p.Pattern.MatchString(tt.input); got != tt.matches {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.matches)
		}
	}
}

func TestPatternManager_AddPattern(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	initialCount := len(pm.Patterns())

	custom := &SecretPattern{
		Name:     "custom_secret",
		Pattern:  regexp.MustCompile(`CUSTOM_[A-Z0-9]{20}`),
		Severity: SecretSeverityHigh,
	}
	pm.AddPattern(custom)

	if len(pm.Patterns()) != initialCount+1 {
		t.Error("pattern count should increase by 1")
	}

	if pm.GetPattern("custom_secret") == nil {
		t.Error("custom pattern should be retrievable")
	}
}

func TestPatternManager_RemovePattern(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	initialCount := len(pm.Patterns())

	if !pm.RemovePattern("api_key_generic") {
		t.Error("should return true when removing existing pattern")
	}

	if len(pm.Patterns()) != initialCount-1 {
		t.Error("pattern count should decrease by 1")
	}

	if pm.GetPattern("api_key_generic") != nil {
		t.Error("removed pattern should not be retrievable")
	}

	if pm.RemovePattern("nonexistent") {
		t.Error("should return false when removing nonexistent pattern")
	}
}

func TestPatternManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_ = pm.Patterns()
		}()

		go func(n int) {
			defer wg.Done()
			pm.AddPattern(&SecretPattern{
				Name:     "concurrent_" + string(rune('a'+n%26)),
				Pattern:  regexp.MustCompile(`test`),
				Severity: SecretSeverityLow,
			})
		}(i)

		go func() {
			defer wg.Done()
			_ = pm.GetPattern("api_key_generic")
		}()
	}

	wg.Wait()
}

func TestSensitiveFilePatterns(t *testing.T) {
	t.Parallel()

	patterns := SensitiveFilePatterns()
	if len(patterns) == 0 {
		t.Error("should return default sensitive file patterns")
	}

	expected := []string{".env", "*.pem", "*.key"}
	for _, e := range expected {
		found := false
		for _, p := range patterns {
			if p == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected pattern %q not found", e)
		}
	}
}

func TestIsSensitiveFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename  string
		sensitive bool
	}{
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{"config.env", true},
		{"server.pem", true},
		{"private.key", true},
		{"credentials.json", true},
		{"my-secret-file.txt", true},
		{"id_rsa", true},
		{"id_ed25519", true},
		{".npmrc", true},
		{".pypirc", true},
		{"service-account-key.json", true},
		{"main.go", false},
		{"README.md", false},
		{"package.json", false},
		{"config.yaml", false},
	}

	for _, tt := range tests {
		if got := IsSensitiveFile(tt.filename); got != tt.sensitive {
			t.Errorf("IsSensitiveFile(%q): got %v, want %v", tt.filename, got, tt.sensitive)
		}
	}
}

func TestNoFalsePositives(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()

	normalCode := []string{
		`func getAPIKey() string { return os.Getenv("API_KEY") }`,
		`// This is a comment about passwords`,
		`const passwordMinLength = 8`,
		`type AWSConfig struct { Region string }`,
		`fmt.Println("Hello, world!")`,
		`var keyCount = 10`,
		`func GenerateKey() []byte { return nil }`,
		`if password == "" { return error }`,
	}

	for _, code := range normalCode {
		for _, p := range pm.Patterns() {
			if p.Pattern.MatchString(code) {
				if p.Name != "password_assignment" {
					t.Errorf("pattern %s matched normal code: %q", p.Name, code)
				}
			}
		}
	}
}

func TestPatternSeverities(t *testing.T) {
	t.Parallel()

	pm := NewPatternManager()

	criticalPatterns := []string{
		"private_key_rsa", "private_key_generic", "aws_access_key",
		"github_pat", "openai_key", "anthropic_key", "connection_string",
	}

	for _, name := range criticalPatterns {
		p := pm.GetPattern(name)
		if p == nil {
			t.Errorf("pattern %s not found", name)
			continue
		}
		if p.Severity != SecretSeverityCritical {
			t.Errorf("pattern %s should be critical, got %s", name, p.Severity)
		}
	}
}

func TestGlobMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		name    string
		matches bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "test.py", false},
		{".env.*", ".env.local", true},
		{".env.*", ".env", false},
		{"*credentials*", "my-credentials-file.json", true},
		{"*secret*", "app-secret-key", true},
		{"id_rsa", "id_rsa", true},
		{"id_rsa", "id_rsa.pub", false},
	}

	for _, tt := range tests {
		got, err := matchGlob(tt.pattern, tt.name)
		if err != nil {
			t.Errorf("matchGlob(%q, %q) error: %v", tt.pattern, tt.name, err)
			continue
		}
		if got != tt.matches {
			t.Errorf("matchGlob(%q, %q): got %v, want %v", tt.pattern, tt.name, got, tt.matches)
		}
	}
}
