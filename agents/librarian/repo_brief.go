package librarian

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/search/codebase"
	"github.com/adalundhe/sylk/core/skills"
)

type repoBriefParams struct {
	Focus              string `json:"focus,omitempty"`
	Depth              int    `json:"depth,omitempty"`
	IncludeHotspots    bool   `json:"include_hotspots,omitempty"`
	IncludeOwnership   bool   `json:"include_ownership,omitempty"`
	IncludeSymbolGraph bool   `json:"include_symbol_graph,omitempty"`
	SymbolLimit        int    `json:"symbol_limit,omitempty"`
}

type symbolReferenceGraphParams struct {
	Include    string   `json:"include,omitempty"`
	Symbols    []string `json:"symbols,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	MaxMatches int      `json:"max_matches,omitempty"`
}

type repoInventory struct {
	root         string
	directories  []string
	files        []string
	keyPaths     []string
	entryPoints  []string
	languages    []LanguageInfo
	dependencies []string
	modules      []RepositoryModule
}

func repoBriefSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("repo_brief").
		Description("Build a reusable repository brief: structure, key paths, inferred commands, module/dependency map, ownership hints, hotspots, and a compact symbol reference graph.").
		Domain("code").
		Keywords("repo brief", "codebase brief", "module map", "ownership", "hotspots", "commands").
		Priority(88).
		StringParam("focus", "Limit analysis to a subdirectory", false).
		IntParam("depth", "Directory depth for structural summary (default: 4)", false).
		BoolParam("include_hotspots", "Include git-derived hotspot summary", false).
		BoolParam("include_ownership", "Include CODEOWNERS-derived ownership summary", false).
		BoolParam("include_symbol_graph", "Include symbol reference graph", false).
		IntParam("symbol_limit", "Maximum symbols in the graph", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params repoBriefParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			result, err := l.buildRepositoryBrief(ctx, params)
			if err == nil && result != nil {
				if acc := claims.AccumulatorFromContext(ctx); acc != nil {
					acc.Record("repo_brief_focus", params.Focus)
					acc.RecordJSON("repo_brief_summary", result)
					acc.Note("Repo brief generated")
				}
			}
			return result, err
		}).
		Build()
}

func symbolReferenceGraphSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("symbol_reference_graph").
		Description("Build a compact symbol definition/reference graph from the repository using structural symbol search plus textual reference counting.").
		Domain("code").
		Keywords("symbol graph", "reference graph", "call graph", "definitions", "references").
		Priority(80).
		StringParam("include", "Optional file glob filter such as '**/*.go'", false).
		ArrayParam("symbols", "Optional explicit symbol names to graph", "string", false).
		IntParam("limit", "Maximum definitions to include (default: 10)", false).
		IntParam("max_matches", "Maximum text matches per symbol (default: 200)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params symbolReferenceGraphParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			graph, err := l.buildSymbolReferenceGraph(ctx, params)
			if err != nil {
				return map[string]any{
					"status": "unavailable",
					"reason": err.Error(),
				}, nil
			}
			return map[string]any{
				"status": "ok",
				"graph":  graph,
				"count":  len(graph),
			}, nil
		}).
		Build()
}

func (l *Librarian) buildRepositoryBrief(ctx context.Context, params repoBriefParams) (*RepositoryBrief, error) {
	if params.Depth <= 0 {
		params.Depth = 4
	}
	if params.SymbolLimit <= 0 {
		params.SymbolLimit = 10
	}

	root := strings.TrimSpace(l.config.WorkingDirectory)
	if root == "" {
		root = "."
	}
	if focus := strings.TrimSpace(params.Focus); focus != "" {
		root = resolvePath(root, focus)
	}

	inventory, err := scanRepository(root, params.Depth)
	if err != nil {
		return nil, err
	}
	summary := &CodebaseSummary{
		DirectoryStructure: buildDirectoryTree(inventory.root, inventory.directories, params.Depth),
		KeyPaths:           inventory.keyPaths,
		CodeStyle:          detectCodeStyle(inventory.files),
		Languages:          inventory.languages,
		Dependencies:       inventory.dependencies,
		EntryPoints:        inventory.entryPoints,
		Metadata: map[string]string{
			"focus_root": inventory.root,
		},
	}

	brief := &RepositoryBrief{
		Root:             inventory.root,
		Summary:          summary,
		InferredCommands: inferRepositoryCommands(inventory.root),
		Modules:          inventory.modules,
	}
	if params.IncludeOwnership {
		brief.Ownership = parseOwnershipRules(inventory.root)
	}
	if params.IncludeHotspots {
		brief.Hotspots = collectRepositoryHotspots(ctx, inventory.root, 10)
	}
	if params.IncludeSymbolGraph {
		graph, err := l.buildSymbolReferenceGraph(ctx, symbolReferenceGraphParams{
			Limit: params.SymbolLimit,
		})
		if err == nil {
			brief.SymbolGraph = graph
		}
	}
	return brief, nil
}

func (l *Librarian) buildSymbolReferenceGraph(ctx context.Context, params symbolReferenceGraphParams) ([]SymbolReferenceNode, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}
	maxMatches := params.MaxMatches
	if maxMatches <= 0 {
		maxMatches = 200
	}

	includes := []string{}
	if include := strings.TrimSpace(params.Include); include != "" {
		includes = append(includes, include)
	}

	symbolDefs := make([]codebase.SymbolMatch, 0, limit)
	if len(params.Symbols) == 0 {
		allPattern, err := regexp.Compile(".*")
		if err != nil {
			return nil, err
		}
		defs, err := codebase.SearchSymbols(ctx, l.config.WorkingDirectory, allPattern, includes, limit)
		if err != nil {
			return nil, err
		}
		symbolDefs = defs
	} else {
		for _, symbol := range params.Symbols {
			symbol = strings.TrimSpace(symbol)
			if symbol == "" {
				continue
			}
			pattern, err := regexp.Compile("^" + regexp.QuoteMeta(symbol) + "$")
			if err != nil {
				return nil, err
			}
			defs, err := codebase.SearchSymbols(ctx, l.config.WorkingDirectory, pattern, includes, 1)
			if err != nil {
				return nil, err
			}
			symbolDefs = append(symbolDefs, defs...)
		}
	}

	graph := make([]SymbolReferenceNode, 0, len(symbolDefs))
	for _, def := range symbolDefs {
		pattern, err := regexp.Compile(`\b` + regexp.QuoteMeta(def.Name) + `\b`)
		if err != nil {
			return nil, err
		}
		matches, err := codebase.Search(ctx, codebase.SearchConfig{
			Root:            l.config.WorkingDirectory,
			Pattern:         pattern,
			IncludePatterns: includes,
			MaxMatches:      maxMatches,
		})
		if err != nil {
			return nil, err
		}
		files := uniqueMatchFiles(matches)
		graph = append(graph, SymbolReferenceNode{
			Name:           def.Name,
			Kind:           def.Kind,
			DefinitionFile: def.File,
			ReferenceCount: len(matches),
			RelatedFiles:   files,
		})
	}

	sort.Slice(graph, func(i, j int) bool {
		if graph[i].ReferenceCount == graph[j].ReferenceCount {
			return graph[i].Name < graph[j].Name
		}
		return graph[i].ReferenceCount > graph[j].ReferenceCount
	})
	if len(graph) > limit {
		graph = graph[:limit]
	}
	return graph, nil
}

func scanRepository(root string, depth int) (*repoInventory, error) {
	root = filepath.Clean(root)
	inventory := &repoInventory{
		root: root,
	}

	languageLines := make(map[string]int)
	languageFiles := make(map[string]int)
	dirs := make(map[string]struct{})
	keyPaths := make(map[string]struct{})
	entryPoints := make(map[string]struct{})

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			dirs["."] = struct{}{}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if shouldSkipRepoDir(d.Name()) {
				return filepath.SkipDir
			}
			if pathDepth(rel) <= depth {
				dirs[rel] = struct{}{}
			}
			return nil
		}
		if shouldSkipRepoFile(rel) {
			return nil
		}
		inventory.files = append(inventory.files, rel)
		if keyPath(rel) {
			keyPaths[rel] = struct{}{}
		}
		if entryPoint(rel) {
			entryPoints[rel] = struct{}{}
		}
		if lang, ok := languageForFile(rel); ok {
			languageFiles[lang]++
			languageLines[lang] += estimateFileLines(filepath.Join(root, rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	inventory.directories = mapKeysSorted(dirs)
	inventory.keyPaths = mapKeysSorted(keyPaths)
	inventory.entryPoints = mapKeysSorted(entryPoints)
	inventory.languages = buildLanguageInfo(languageFiles, languageLines)
	inventory.modules = detectRepositoryModules(root)
	inventory.dependencies = uniqueDependencies(inventory.modules)
	return inventory, nil
}

func buildDirectoryTree(root string, directories []string, depth int) string {
	if len(directories) == 0 {
		return filepath.Base(root)
	}
	lines := make([]string, 0, len(directories)+1)
	lines = append(lines, filepath.Base(root)+"/")
	for _, dir := range directories {
		if dir == "." {
			continue
		}
		if pathDepth(dir) > depth {
			continue
		}
		indent := strings.Repeat("  ", pathDepth(dir))
		lines = append(lines, indent+filepath.Base(dir)+"/")
	}
	return strings.Join(lines, "\n")
}

func detectCodeStyle(files []string) CodeStyleInfo {
	style := CodeStyleInfo{
		IndentStyle: "spaces",
		IndentSize:  2,
		LineLimit:   100,
		NamingConv:  "mixed",
		TestPattern: "*_test.go",
		DocStyle:    "line_comments",
	}
	for _, file := range files {
		switch {
		case strings.HasSuffix(file, ".go"):
			style.IndentStyle = "tabs"
			style.IndentSize = 4
			style.LineLimit = 100
			style.NamingConv = "go_export_conventions"
			style.TestPattern = "*_test.go"
			return style
		case strings.HasSuffix(file, ".ts"), strings.HasSuffix(file, ".tsx"), strings.HasSuffix(file, ".js"), strings.HasSuffix(file, ".jsx"):
			style.IndentStyle = "spaces"
			style.IndentSize = 2
			style.LineLimit = 100
			style.NamingConv = "camelCase/PascalCase"
			style.TestPattern = "*.test.ts(x)"
			return style
		}
	}
	return style
}

func inferRepositoryCommands(root string) []RepositoryCommand {
	commands := make([]RepositoryCommand, 0, 8)
	seen := make(map[string]struct{})

	add := func(kind, command string, confidence float64, evidence ...string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		key := kind + ":" + command
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		commands = append(commands, RepositoryCommand{
			Kind:       kind,
			Command:    command,
			Confidence: confidence,
			Evidence:   filterNonEmpty(evidence),
		})
	}

	makefilePath := findFirstExisting(root, "Makefile", "makefile")
	if makefilePath != "" {
		targets := parseMakeTargets(makefilePath)
		if targets["build"] {
			add("build", "make build", 0.95, relTo(root, makefilePath))
		}
		if targets["test"] {
			add("test", "make test", 0.95, relTo(root, makefilePath))
		}
		if targets["lint"] {
			add("lint", "make lint", 0.95, relTo(root, makefilePath))
		}
	}

	goModPath := filepath.Join(root, "go.mod")
	if fileExists(goModPath) {
		add("build", "go build ./...", 0.9, "go.mod")
		add("test", "go test ./...", 0.9, "go.mod")
		add("check", "go vet ./...", 0.8, "go.mod")
		if fileExists(filepath.Join(root, ".golangci.yml")) || fileExists(filepath.Join(root, ".golangci.yaml")) {
			add("lint", "golangci-lint run", 0.9, ".golangci.yml")
		}
	}

	packageJSON := filepath.Join(root, "package.json")
	if fileExists(packageJSON) {
		pm := detectNodePackageManager(root)
		for script := range parsePackageScripts(packageJSON) {
			add(scriptKind(script), fmt.Sprintf("%s %s", pm, script), 0.92, "package.json")
		}
	}

	if fileExists(filepath.Join(root, "Taskfile.yml")) || fileExists(filepath.Join(root, "Taskfile.yaml")) {
		add("build", "task build", 0.8, "Taskfile.yml")
		add("test", "task test", 0.8, "Taskfile.yml")
	}
	if fileExists(filepath.Join(root, "Justfile")) {
		add("build", "just build", 0.8, "Justfile")
		add("test", "just test", 0.8, "Justfile")
	}

	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Kind == commands[j].Kind {
			return commands[i].Command < commands[j].Command
		}
		return commands[i].Kind < commands[j].Kind
	})
	return commands
}

func detectRepositoryModules(root string) []RepositoryModule {
	modules := make([]RepositoryModule, 0, 4)
	if goMod := filepath.Join(root, "go.mod"); fileExists(goMod) {
		if module, ok := parseGoModule(goMod); ok {
			modules = append(modules, module)
		}
	}
	if packageJSON := filepath.Join(root, "package.json"); fileExists(packageJSON) {
		if module, ok := parsePackageModule(packageJSON); ok {
			modules = append(modules, module)
		}
	}
	return modules
}

func parseGoModule(path string) (RepositoryModule, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return RepositoryModule{}, false
	}
	module := RepositoryModule{
		Path:           filepath.Dir(path),
		Ecosystem:      "go",
		DependencyFile: relTo(filepath.Dir(path), path),
	}
	lines := strings.Split(string(content), "\n")
	inRequireBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "module "):
			module.Name = strings.TrimSpace(strings.TrimPrefix(line, "module "))
		case strings.HasPrefix(line, "require ("):
			inRequireBlock = true
		case inRequireBlock && line == ")":
			inRequireBlock = false
		case strings.HasPrefix(line, "require "):
			fields := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(fields) > 0 {
				module.Dependencies = append(module.Dependencies, fields[0])
			}
		case inRequireBlock:
			fields := strings.Fields(line)
			if len(fields) > 0 {
				module.Dependencies = append(module.Dependencies, fields[0])
			}
		}
	}
	module.Dependencies = uniqueStrings(module.Dependencies)
	return module, module.Name != ""
}

func parsePackageModule(path string) (RepositoryModule, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return RepositoryModule{}, false
	}
	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(content, &pkg); err != nil {
		return RepositoryModule{}, false
	}
	deps := make([]string, 0, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for dep := range pkg.Dependencies {
		deps = append(deps, dep)
	}
	for dep := range pkg.DevDependencies {
		deps = append(deps, dep)
	}
	return RepositoryModule{
		Name:           pkg.Name,
		Path:           filepath.Dir(path),
		Ecosystem:      "node",
		DependencyFile: relTo(filepath.Dir(path), path),
		Dependencies:   uniqueStrings(deps),
	}, pkg.Name != ""
}

func parseOwnershipRules(root string) []OwnershipRule {
	codeownersPath := findFirstExisting(root, "CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS")
	if codeownersPath == "" {
		return nil
	}
	content, err := os.ReadFile(codeownersPath)
	if err != nil {
		return nil
	}
	rules := make([]OwnershipRule, 0)
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rules = append(rules, OwnershipRule{
			Pattern: fields[0],
			Owners:  fields[1:],
			Source:  relTo(root, codeownersPath),
		})
	}
	return rules
}

func collectRepositoryHotspots(ctx context.Context, root string, limit int) []RepositoryHotspot {
	output, err := runCommandInDir(ctx, root, "git", "log", "--format=__AUTHOR__%an", "--name-only", "--since=180.days")
	if err != nil {
		return nil
	}
	changeCount := make(map[string]int)
	contributors := make(map[string]map[string]struct{})
	currentAuthor := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "__AUTHOR__") {
			currentAuthor = strings.TrimPrefix(line, "__AUTHOR__")
			continue
		}
		changeCount[line]++
		if contributors[line] == nil {
			contributors[line] = make(map[string]struct{})
		}
		if currentAuthor != "" {
			contributors[line][currentAuthor] = struct{}{}
		}
	}
	hotspots := make([]RepositoryHotspot, 0, len(changeCount))
	for path, count := range changeCount {
		hotspots = append(hotspots, RepositoryHotspot{
			Path:         path,
			ChangeCount:  count,
			Contributors: mapKeysSorted(contributors[path]),
		})
	}
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].ChangeCount == hotspots[j].ChangeCount {
			return hotspots[i].Path < hotspots[j].Path
		}
		return hotspots[i].ChangeCount > hotspots[j].ChangeCount
	})
	if limit > 0 && len(hotspots) > limit {
		hotspots = hotspots[:limit]
	}
	return hotspots
}

func parseMakeTargets(path string) map[string]bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	targets := make(map[string]bool)
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") {
			continue
		}
		if strings.Contains(line, ":") {
			target := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
			if target != "" && !strings.Contains(target, " ") {
				targets[target] = true
			}
		}
	}
	return targets
}

func parsePackageScripts(path string) map[string]struct{} {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(content, &pkg); err != nil {
		return nil
	}
	result := make(map[string]struct{}, len(pkg.Scripts))
	for name := range pkg.Scripts {
		result[name] = struct{}{}
	}
	return result
}

func detectNodePackageManager(root string) string {
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm run"
	case fileExists(filepath.Join(root, "yarn.lock")):
		return "yarn"
	case fileExists(filepath.Join(root, "bun.lock")) || fileExists(filepath.Join(root, "bun.lockb")):
		return "bun run"
	default:
		return "npm run"
	}
}

func scriptKind(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "test":
		return "test"
	case "lint":
		return "lint"
	case "build":
		return "build"
	case "start", "dev":
		return "run"
	default:
		return "script"
	}
}

func uniqueMatchFiles(matches []codebase.Match) []string {
	seen := make(map[string]struct{}, len(matches))
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, ok := seen[match.File]; ok {
			continue
		}
		seen[match.File] = struct{}{}
		files = append(files, match.File)
	}
	sort.Strings(files)
	return files
}

func buildLanguageInfo(fileCounts map[string]int, lineCounts map[string]int) []LanguageInfo {
	totalFiles := 0
	for _, count := range fileCounts {
		totalFiles += count
	}
	languages := make([]LanguageInfo, 0, len(fileCounts))
	for name, fileCount := range fileCounts {
		percentage := 0.0
		if totalFiles > 0 {
			percentage = float64(fileCount) / float64(totalFiles)
		}
		languages = append(languages, LanguageInfo{
			Name:       name,
			FileCount:  fileCount,
			LineCount:  lineCounts[name],
			Percentage: percentage,
		})
	}
	sort.Slice(languages, func(i, j int) bool {
		if languages[i].FileCount == languages[j].FileCount {
			return languages[i].Name < languages[j].Name
		}
		return languages[i].FileCount > languages[j].FileCount
	})
	return languages
}

func uniqueDependencies(modules []RepositoryModule) []string {
	seen := make(map[string]struct{})
	deps := make([]string, 0)
	for _, module := range modules {
		for _, dep := range module.Dependencies {
			if dep == "" {
				continue
			}
			if _, ok := seen[dep]; ok {
				continue
			}
			seen[dep] = struct{}{}
			deps = append(deps, dep)
		}
	}
	sort.Strings(deps)
	return deps
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func mapKeysSorted[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shouldSkipRepoDir(name string) bool {
	switch name {
	case ".git", ".sylk", "node_modules", "dist", "build", "coverage", ".next":
		return true
	default:
		return strings.HasPrefix(name, ".cache")
	}
}

func shouldSkipRepoFile(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".png"), strings.HasSuffix(base, ".jpg"), strings.HasSuffix(base, ".jpeg"), strings.HasSuffix(base, ".gif"), strings.HasSuffix(base, ".svg"):
		return true
	case strings.HasSuffix(base, ".lock"), strings.HasSuffix(base, ".sum"):
		return false
	default:
		return false
	}
}

func languageForFile(path string) (string, bool) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".go":
		return "Go", true
	case ".ts", ".tsx":
		return "TypeScript", true
	case ".js", ".jsx":
		return "JavaScript", true
	case ".py":
		return "Python", true
	case ".rs":
		return "Rust", true
	case ".md":
		return "Markdown", true
	case ".json":
		return "JSON", true
	case ".yaml", ".yml":
		return "YAML", true
	case ".sh":
		return "Shell", true
	default:
		return "", false
	}
}

func estimateFileLines(path string) int {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 1<<20 {
		return 0
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(content), "\n") + 1
}

func keyPath(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case "go.mod", "package.json", "README.md", "README", "Makefile", "Taskfile.yml", "Taskfile.yaml", "Justfile", "CODEOWNERS":
		return true
	}
	return strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "apps/")
}

func entryPoint(rel string) bool {
	base := filepath.Base(rel)
	return base == "main.go" || strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "apps/")
}

func pathDepth(rel string) int {
	if rel == "." || rel == "" {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}

func findFirstExisting(root string, candidates ...string) string {
	for _, candidate := range candidates {
		full := filepath.Join(root, candidate)
		if fileExists(full) {
			return full
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func relTo(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func filterNonEmpty(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}
