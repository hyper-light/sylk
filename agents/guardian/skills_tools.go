package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// ---------------------------------------------------------------------------
// read_file — Read-only filesystem access
// ---------------------------------------------------------------------------

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func readFileSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read file contents for safety context gathering. Guardian is read-only.").
		Domain("filesystem").
		Keywords("read", "file", "content", "view").
		Priority(80).
		StringParam("path", "Path to file", true).
		IntParam("offset", "Starting line offset (0-based)", false).
		IntParam("limit", "Max lines to return", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params readFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}
			fa := versioning.NewDiskFileAccess(resolveGuardianRoot(g, ""), true)
			result, err := versioning.ReadFileToolResult(ctx, fa, params.Path, params.Offset, params.Limit)
			if err != nil {
				return nil, err
			}
			return result, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// glob — Find files by pattern
// ---------------------------------------------------------------------------

type globParams struct {
	Pattern string   `json:"pattern"`
	Path    string   `json:"path,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

func globSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files by glob pattern for safety context.").
		Domain("filesystem").
		Keywords("glob", "find", "pattern", "match").
		Priority(75).
		StringParam("pattern", "Glob pattern", true).
		StringParam("path", "Base path", false).
		ArrayParam("exclude", "Excluded patterns", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			root := resolveGuardianRoot(g, params.Path)
			matches, err := runGuardianGlob(root, params.Pattern, params.Exclude)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pattern": params.Pattern, "matches": matches, "count": len(matches)}, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// grep — Search file contents
// ---------------------------------------------------------------------------

type grepParams struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Include    string `json:"include,omitempty"`
	MaxMatches int    `json:"max_matches,omitempty"`
}

func grepSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("grep").
		Description("Search files with regex for safety context extraction.").
		Domain("filesystem").
		Keywords("grep", "search", "regex", "find text").
		Priority(75).
		StringParam("pattern", "Regular expression pattern", true).
		StringParam("path", "Path to search", false).
		StringParam("include", "Include filename pattern", false).
		IntParam("max_matches", "Maximum matches", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params grepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			re, err := regexp.Compile(params.Pattern)
			if err != nil {
				return nil, err
			}
			root := resolveGuardianRoot(g, params.Path)
			max := params.MaxMatches
			if max <= 0 {
				max = 100
			}
			matches, err := runGuardianGrep(root, re, params.Include, max)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"pattern":   params.Pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": len(matches) >= max,
			}, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// ask_user_question — Communication skill
// ---------------------------------------------------------------------------

type askUserInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

func askUserQuestionSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("ask_user_question").
		Description("Ask the user a clarifying question during conversation.").
		Domain("communication").
		Keywords("ask", "question", "clarify", "user").
		Priority(70).
		StringParam("question", "Question to ask", true).
		ArrayParam("options", "Answer options", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params askUserInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Question) == "" {
				return nil, fmt.Errorf("question is required")
			}
			return map[string]any{
				"question":        params.Question,
				"options":         params.Options,
				"awaiting_answer": true,
			}, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func resolveGuardianPath(g *Guardian, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if g.fileAccess != nil {
		wd := g.fileAccess.WorkingDir()
		if wd != "" {
			return filepath.Join(wd, path)
		}
	}
	return path
}

func resolveGuardianRoot(g *Guardian, path string) string {
	if strings.TrimSpace(path) == "" {
		if g.fileAccess != nil {
			return g.fileAccess.WorkingDir()
		}
		return "."
	}
	return resolveGuardianPath(g, path)
}

func sliceContent(path string, content string, offset int, limit int) map[string]any {
	lines := strings.Split(content, "\n")
	start := offset
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	max := limit
	if max <= 0 {
		max = 1000
	}
	end := start + max
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{
		"path":        path,
		"content":     strings.Join(lines[start:end], "\n"),
		"offset":      start,
		"limit":       max,
		"total_lines": len(lines),
		"truncated":   end < len(lines),
	}
}

type guardianGrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func runGuardianGlob(root string, pattern string, exclude []string) ([]string, error) {
	results := make([]string, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		for _, ex := range exclude {
			if matched, _ := filepath.Match(ex, rel); matched {
				return nil
			}
		}
		if matched, _ := filepath.Match(pattern, rel); matched {
			results = append(results, rel)
		}
		return nil
	})
	return results, err
}

func runGuardianGrep(root string, re *regexp.Regexp, include string, max int) ([]guardianGrepMatch, error) {
	results := make([]guardianGrepMatch, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" {
			if matched, _ := filepath.Match(include, info.Name()); !matched {
				return nil
			}
		}
		if len(results) >= max {
			return filepath.SkipAll
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		lines := strings.Split(string(content), "\n")
		for idx, line := range lines {
			if len(results) >= max {
				return filepath.SkipAll
			}
			if re.MatchString(line) {
				results = append(results, guardianGrepMatch{
					File:    rel,
					Line:    idx + 1,
					Content: strings.TrimSpace(line),
				})
			}
		}
		return nil
	})
	return results, err
}
