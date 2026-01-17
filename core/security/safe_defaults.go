package security

var defaultSafeCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"find": true, "grep": true, "rg": true, "fd": true,
	"wc": true, "file": true, "stat": true, "which": true,
	"pwd": true, "echo": true, "env": true, "whoami": true,
	"git":    true,
	"go":     true,
	"npm":    true,
	"node":   true,
	"python": true,
	"pip":    true,
	"cargo":  true,
	"make":   true,
	"cmake":  true,
}

var defaultSafeDomains = map[string]bool{
	"pkg.go.dev":            true,
	"proxy.golang.org":      true,
	"npmjs.com":             true,
	"registry.npmjs.org":    true,
	"pypi.org":              true,
	"crates.io":             true,
	"golang.org":            true,
	"go.dev":                true,
	"docs.python.org":       true,
	"nodejs.org":            true,
	"developer.mozilla.org": true,
	"github.com":            true,
	"gitlab.com":            true,
	"bitbucket.org":         true,
	"stackoverflow.com":     true,
	"docs.rs":               true,
	"doc.rust-lang.org":     true,
}

var defaultSafePathPatterns = []string{
	"*.go", "*.py", "*.js", "*.ts", "*.jsx", "*.tsx",
	"*.rs", "*.c", "*.cpp", "*.h", "*.hpp",
	"*.json", "*.yaml", "*.yml", "*.toml",
	"*.md", "*.txt", "*.log",
	"Makefile", "Dockerfile", "*.dockerfile",
	"go.mod", "go.sum", "package.json", "Cargo.toml",
}

func DefaultSafeCommands() map[string]bool {
	result := make(map[string]bool, len(defaultSafeCommands))
	for k, v := range defaultSafeCommands {
		result[k] = v
	}
	return result
}

func DefaultSafeDomains() map[string]bool {
	result := make(map[string]bool, len(defaultSafeDomains))
	for k, v := range defaultSafeDomains {
		result[k] = v
	}
	return result
}

func DefaultSafePathPatterns() []string {
	result := make([]string, len(defaultSafePathPatterns))
	copy(result, defaultSafePathPatterns)
	return result
}
