package engineer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
)

// maxDiscoveryFiles bounds file scanning during code pattern discovery.
// Derived from the tool loop budget: at MaxToolRuns=16, each tool call should
// complete quickly. Scanning 50 files keeps discovery under ~100ms on typical
// codebases, leaving headroom for the LLM to call other tools.
const maxDiscoveryFiles = 50

// maxDiscoveryPatternsPerCategory caps unique patterns per category to prevent
// unbounded growth. A tool discovery scan that finds more than this many distinct
// patterns in a category has already captured sufficient signal for the LLM.
const maxDiscoveryPatternsPerCategory = 5

func discoverProjectToolsSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("discover_project_tools").
		Description("Scan the project root for build tools, package managers, and frameworks. Returns a structured inventory of detected tooling.").
		Domain("discovery").
		Keywords("discover", "scan", "detect", "tools", "framework", "project").
		Priority(75).
		StringParam("path", "Project root path to scan (default: working directory)", false).
		Usage("Call early in implementation to understand the project's tooling before making changes.").
		BestPractice("Run once at the start of a task. Cache the result mentally — do not re-scan.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			root := params.Path
			if root == "" {
				root = e.config.EngineerConfig.WorkingDirectory
			}
			if root == "" {
				root = "."
			}
			result, err := scanProjectTools(root)
			if err != nil || result == nil {
				return result, err
			}
			// Phase 4 refactor: publish detected tools as hints so
			// peers skip rediscovery. Emit one hint per detected tool
			// keyed by its category domain.
			if detected, ok := result["detected"].([]map[string]string); ok {
				for _, tool := range detected {
					shared.AutoPublishHint(ctx, shared.AutoPublishInput{
						SessionID:        e.config.SessionID,
						AuthorAgentID:    e.id,
						AuthorAgentType:  "engineer",
						AuthorPipelineID: e.pipelineID,
						TriggerSkill:     "discover_project_tools",
						Domain:           tool["category"],
						Value:            tool["tool"],
						Scope:            root,
						Evidence:         []string{"detected via " + tool["file"]},
					})
				}
			}
			return result, err
		}).
		Build()
}

func discoverCodePatternsSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("discover_code_patterns").
		Description("Scan the codebase for coding conventions: error handling, naming, test patterns, and import styles. Returns a structured summary.").
		Domain("discovery").
		Keywords("discover", "scan", "detect", "patterns", "conventions", "style").
		Priority(90).
		StringParam("path", "Directory to scan (default: working directory)", false).
		StringParam("language", "Primary language to focus on (e.g., go, typescript)", false).
		Usage("Call to understand existing code conventions before implementing. Helps ensure consistency.").
		BestPractice("Scan the specific package you are modifying, not the entire repo.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path     string `json:"path"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			root := params.Path
			if root == "" {
				root = e.config.EngineerConfig.WorkingDirectory
			}
			if root == "" {
				root = "."
			}
			result, err := scanCodePatterns(root, params.Language)
			if err != nil || result == nil {
				return result, err
			}
			// Phase 4 refactor: publish discovered conventions as
			// hints so downstream work (designer components, tester
			// test shape) can adopt them without rediscovery.
			publishPatternHints := func(domain string, values []string) {
				for _, v := range values {
					shared.AutoPublishHint(ctx, shared.AutoPublishInput{
						SessionID:        e.config.SessionID,
						AuthorAgentID:    e.id,
						AuthorAgentType:  "engineer",
						AuthorPipelineID: e.pipelineID,
						TriggerSkill:     "discover_code_patterns",
						Domain:           domain,
						Value:            v,
						Scope:            root,
					})
				}
			}
			if patterns, ok := result["error_handling"].([]string); ok {
				publishPatternHints("error_handling_convention", patterns)
			}
			if patterns, ok := result["test_patterns"].([]string); ok {
				publishPatternHints("test_convention", patterns)
			}
			return result, err
		}).
		Build()
}

// scanProjectTools inspects the project root for common configuration files
// and returns a structured inventory of detected tools and frameworks.
func scanProjectTools(root string) (map[string]any, error) {
	indicators := []struct {
		file     string
		tool     string
		category string
	}{
		{"go.mod", "go", "language"},
		{"Cargo.toml", "cargo", "language"},
		{"package.json", "npm", "package_manager"},
		{"yarn.lock", "yarn", "package_manager"},
		{"pnpm-lock.yaml", "pnpm", "package_manager"},
		{"pyproject.toml", "python", "language"},
		{"requirements.txt", "pip", "package_manager"},
		{"Makefile", "make", "build"},
		{"Dockerfile", "docker", "container"},
		{"docker-compose.yml", "docker-compose", "container"},
		{"docker-compose.yaml", "docker-compose", "container"},
		{".tool-versions", "asdf", "version_manager"},
		{".nvmrc", "nvm", "version_manager"},
		{".github/workflows", "github_actions", "ci"},
		{"Justfile", "just", "build"},
		{"CMakeLists.txt", "cmake", "build"},
		{"tsconfig.json", "typescript", "language"},
	}

	detected := make([]map[string]string, 0, len(indicators))
	for _, ind := range indicators {
		path := filepath.Join(root, ind.file)
		if _, err := os.Stat(path); err == nil {
			detected = append(detected, map[string]string{
				"file":     ind.file,
				"tool":     ind.tool,
				"category": ind.category,
			})
		}
	}

	return map[string]any{
		"root":     root,
		"detected": detected,
		"count":    len(detected),
	}, nil
}

// scanCodePatterns inspects source files for coding conventions.
func scanCodePatterns(root, language string) (map[string]any, error) {
	patterns := map[string]any{
		"root":     root,
		"language": language,
	}

	ext := languageExtension(language)

	var fileCount int
	var errorHandling []string
	var testPatterns []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if ext != "" && !strings.HasSuffix(info.Name(), ext) {
			return nil
		}
		if fileCount >= maxDiscoveryFiles {
			return filepath.SkipAll
		}
		fileCount++

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(content)

		// Detect error handling patterns (Go-specific)
		if language == "go" || language == "" {
			if strings.Contains(text, "if err != nil") {
				errorHandling = appendUnique(errorHandling, "go_err_check", maxDiscoveryPatternsPerCategory)
			}
			if strings.Contains(text, "fmt.Errorf") {
				errorHandling = appendUnique(errorHandling, "fmt_errorf_wrap", maxDiscoveryPatternsPerCategory)
			}
			if strings.Contains(text, "errors.Is") || strings.Contains(text, "errors.As") {
				errorHandling = appendUnique(errorHandling, "errors_is_as", maxDiscoveryPatternsPerCategory)
			}
		}

		// Detect test patterns
		if strings.HasSuffix(info.Name(), "_test.go") || strings.HasSuffix(info.Name(), ".test.ts") || strings.HasSuffix(info.Name(), ".test.js") {
			if strings.Contains(text, "func Test") {
				testPatterns = appendUnique(testPatterns, "go_test_functions", maxDiscoveryPatternsPerCategory)
			}
			if strings.Contains(text, "t.Run(") {
				testPatterns = appendUnique(testPatterns, "go_subtests", maxDiscoveryPatternsPerCategory)
			}
			if strings.Contains(text, "t.Helper()") {
				testPatterns = appendUnique(testPatterns, "go_test_helpers", maxDiscoveryPatternsPerCategory)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan code patterns: %w", err)
	}

	patterns["files_scanned"] = fileCount
	patterns["error_handling"] = errorHandling
	patterns["test_patterns"] = testPatterns

	return patterns, nil
}

func languageExtension(lang string) string {
	switch strings.ToLower(lang) {
	case "go":
		return ".go"
	case "typescript", "ts":
		return ".ts"
	case "javascript", "js":
		return ".js"
	case "python", "py":
		return ".py"
	case "rust", "rs":
		return ".rs"
	default:
		return ""
	}
}

func appendUnique(slice []string, val string, max int) []string {
	if len(slice) >= max {
		return slice
	}
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
