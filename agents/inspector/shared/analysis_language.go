package shared

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxAnalysisFiles = 256

var analysisLanguageByExtension = map[string]string{
	".go":  "go",
	".py":  "python",
	".pyi": "python",
}

var goFileExtensions = map[string]struct{}{
	".go": {},
}

func detectAnalysisLanguage(ctx context.Context, runner *ToolRunner, paths []string) string {
	scores := scoreLanguageTargets(normalizeAnalysisTargets(paths))
	if language := highestScoredLanguage(scores); language != "" {
		return language
	}
	files, err := collectAnalysisFiles(ctx, runner, paths, nil)
	if err == nil {
		scoreLanguageFiles(scores, files)
	}
	if language := highestScoredLanguage(scores); language != "" {
		return language
	}
	return strings.TrimSpace(inferExecutionLanguage(runner.currentWorkingDir(), runner.currentFileAccess()))
}

func normalizeAnalysisTargets(paths []string) []string {
	if len(paths) == 0 {
		return []string{"."}
	}
	targets := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		target := strings.TrimSpace(path)
		switch target {
		case "", "./...", "...":
			target = "."
		}
		target = filepath.Clean(target)
		if target == "" {
			target = "."
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return []string{"."}
	}
	return targets
}

func normalizeGoPackageExecutionTargets(runner *ToolRunner, paths []string) []string {
	targets := normalizeAnalysisTargets(paths)
	if len(paths) == 0 {
		targets = []string{"./..."}
	}
	root := strings.TrimSpace(runner.currentWorkingDir())
	normalized := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		clean := normalizeGoPackageExecutionTarget(root, target)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	if len(normalized) == 0 {
		return []string{"./..."}
	}
	return normalized
}

func normalizeGoFileExecutionTargets(
	ctx context.Context,
	runner *ToolRunner,
	paths []string,
) ([]string, error) {
	files, err := collectAnalysisFiles(ctx, runner, paths, goFileExtensions)
	if err != nil {
		return nil, err
	}
	normalized := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	root := strings.TrimSpace(runner.currentWorkingDir())
	for _, file := range files {
		clean := normalizeWorkspaceFileTarget(root, file)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	return normalized, nil
}

func normalizeGoPackageExecutionTarget(root string, target string) string {
	target = strings.TrimSpace(target)
	switch target {
	case "", "...", "./...":
		return "./..."
	}
	wildcard := strings.HasSuffix(target, string(filepath.Separator)+"...") || strings.HasSuffix(target, "/...")
	if wildcard {
		target = strings.TrimSuffix(target, string(filepath.Separator)+"...")
		target = strings.TrimSuffix(target, "/...")
		if strings.TrimSpace(target) == "" {
			target = "."
		}
	}
	if filepath.IsAbs(target) {
		if rel, ok := workspaceRelativePath(root, target); ok {
			target = rel
		} else {
			target = filepath.Clean(target)
		}
	} else {
		target = filepath.Clean(target)
	}
	if target == "" || target == "." {
		if wildcard {
			return "./..."
		}
		return "."
	}
	if filepath.IsAbs(target) {
		if wildcard {
			return target + "/..."
		}
		return target
	}
	if strings.HasPrefix(target, ".."+string(filepath.Separator)) || target == ".." {
		if wildcard {
			return target + "/..."
		}
		return target
	}
	if wildcard {
		return "./" + strings.TrimPrefix(filepath.ToSlash(target), "./") + "/..."
	}
	return "./" + strings.TrimPrefix(filepath.ToSlash(target), "./")
}

func normalizeWorkspaceFileTarget(root string, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	clean := filepath.Clean(target)
	if filepath.IsAbs(clean) {
		if rel, ok := workspaceRelativePath(root, clean); ok {
			return filepath.Clean(rel)
		}
		return clean
	}
	return filepath.Clean(clean)
}

func normalizeToolReportedPath(runner *ToolRunner, target string) string {
	return normalizeWorkspaceFileTarget(runner.currentWorkingDir(), target)
}

func workspaceRelativePath(root string, target string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(target) {
		return "", false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func collectAnalysisFiles(
	ctx context.Context,
	runner *ToolRunner,
	paths []string,
	allowedExtensions map[string]struct{},
) ([]string, error) {
	targets := normalizeAnalysisTargets(paths)
	files := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	if fa := runner.currentFileAccess(); fa != nil {
		for _, target := range targets {
			if err := collectAnalysisFilesFromFileAccess(ctx, fa, target, allowedExtensions, seen, &files); err != nil {
				return nil, err
			}
		}
		sort.Strings(files)
		return files, nil
	}
	for _, target := range targets {
		if err := collectAnalysisFilesFromDisk(runner.currentWorkingDir(), target, allowedExtensions, seen, &files); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func collectAnalysisFilesFromFileAccess(
	ctx context.Context,
	fa FileAccess,
	target string,
	allowedExtensions map[string]struct{},
	seen map[string]struct{},
	files *[]string,
) error {
	if len(*files) >= maxAnalysisFiles {
		return nil
	}
	info, err := fa.Stat(ctx, target)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		appendAnalysisFile(target, allowedExtensions, seen, files)
		return nil
	}
	entries, err := fa.ListDir(ctx, target)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if len(*files) >= maxAnalysisFiles {
			return nil
		}
		child := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := collectAnalysisFilesFromFileAccess(ctx, fa, child, allowedExtensions, seen, files); err != nil {
				return err
			}
			continue
		}
		appendAnalysisFile(child, allowedExtensions, seen, files)
	}
	return nil
}

func collectAnalysisFilesFromDisk(
	root string,
	target string,
	allowedExtensions map[string]struct{},
	seen map[string]struct{},
	files *[]string,
) error {
	absoluteTarget, relativeTarget := diskAnalysisTarget(root, target)
	return walkDiskAnalysisTarget(absoluteTarget, relativeTarget, allowedExtensions, seen, files)
}

func diskAnalysisTarget(root, target string) (string, string) {
	if filepath.IsAbs(target) {
		relative := target
		if rel, err := filepath.Rel(root, target); err == nil {
			relative = rel
		}
		return target, filepath.Clean(relative)
	}
	return filepath.Join(root, target), filepath.Clean(target)
}

func walkDiskAnalysisTarget(
	absoluteTarget string,
	relativeTarget string,
	allowedExtensions map[string]struct{},
	seen map[string]struct{},
	files *[]string,
) error {
	if len(*files) >= maxAnalysisFiles {
		return nil
	}
	info, err := os.Stat(absoluteTarget)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		appendAnalysisFile(relativeTarget, allowedExtensions, seen, files)
		return nil
	}
	entries, err := os.ReadDir(absoluteTarget)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if len(*files) >= maxAnalysisFiles {
			return nil
		}
		childAbs := filepath.Join(absoluteTarget, entry.Name())
		childRel := filepath.Join(relativeTarget, entry.Name())
		if entry.IsDir() {
			if err := walkDiskAnalysisTarget(childAbs, childRel, allowedExtensions, seen, files); err != nil {
				return err
			}
			continue
		}
		appendAnalysisFile(childRel, allowedExtensions, seen, files)
	}
	return nil
}

func appendAnalysisFile(
	path string,
	allowedExtensions map[string]struct{},
	seen map[string]struct{},
	files *[]string,
) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return
	}
	if !shouldIncludeAnalysisFile(clean, allowedExtensions) {
		return
	}
	if _, ok := seen[clean]; ok {
		return
	}
	seen[clean] = struct{}{}
	*files = append(*files, clean)
}

func shouldIncludeAnalysisFile(path string, allowedExtensions map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	if len(allowedExtensions) == 0 {
		_, ok := analysisLanguageByExtension[ext]
		return ok
	}
	_, ok := allowedExtensions[ext]
	return ok
}

func scoreLanguageTargets(targets []string) map[string]int {
	scores := make(map[string]int)
	scoreLanguageFiles(scores, targets)
	return scores
}

func scoreLanguageFiles(scores map[string]int, files []string) {
	for _, file := range files {
		if language := languageForPath(file); language != "" {
			scores[language]++
		}
	}
}

func languageForPath(path string) string {
	return analysisLanguageByExtension[strings.ToLower(filepath.Ext(path))]
}

func highestScoredLanguage(scores map[string]int) string {
	bestLanguage := ""
	bestScore := 0
	for language, score := range scores {
		if score > bestScore {
			bestLanguage = language
			bestScore = score
		}
	}
	return bestLanguage
}
