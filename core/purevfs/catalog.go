package purevfs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/detect"
)

type FrameworkProfile struct {
	ID               string
	Name             string
	Language         string
	TestCommand      string
	BuildCommand     string
	RunCommand       string
	WatchCommand     string
	ConfigFiles      []string
	IndicatorFiles   []string
	TestFilePatterns []string
	Priority         int
	Enabled          func(projectDir string) bool
}

type RuntimePolicy struct {
	TempVars          []string
	HomeVars          []string
	CacheVars         map[string]string
	OutVars           map[string]string
	StaticEnv         map[string]string
	Toolchains        []string
	ExecutableAliases map[string][]string
	ManifestRO        []string
	CacheBucket       string
}

type RuntimeProfile struct {
	ID            string
	Language      string
	Aliases       []string
	Extensions    []string
	ManifestFiles []string
	LockFiles     []string
	Frameworks    []FrameworkProfile
	Policy        RuntimePolicy
}

type ExecRoots struct {
	Workspace string
	Temp      string
	Cache     string
	Home      string
	Out       string
}

type ExecCellPlan struct {
	Language          string
	WorkspaceRoot     string
	TempRoot          string
	CacheRoot         string
	HomeRoot          string
	OutRoot           string
	Toolchains        []string
	ExecutableAliases map[string][]string
	Env               map[string]string
}

type DetectedFramework struct {
	Language    string
	ID          string
	Name        string
	TestCommand string
	Priority    int
}

type ProjectRuntimeSummary struct {
	PrimaryLanguage string
	Languages       []string
	Frameworks      []DetectedFramework
	ExecPlans       map[string]ExecCellPlan
}

type Catalog struct {
	profiles    []RuntimeProfile
	byLanguage  map[string]RuntimeProfile
	byAlias     map[string]string
	byExtension map[string]string
}

var (
	defaultCatalogOnce sync.Once
	defaultCatalog     *Catalog
)

func DefaultCatalog() *Catalog {
	defaultCatalogOnce.Do(func() {
		defaultCatalog = newCatalog(defaultProfiles())
	})
	return defaultCatalog
}

func newCatalog(profiles []RuntimeProfile) *Catalog {
	c := &Catalog{
		profiles:    make([]RuntimeProfile, len(profiles)),
		byLanguage:  make(map[string]RuntimeProfile, len(profiles)),
		byAlias:     make(map[string]string, len(profiles)*2),
		byExtension: make(map[string]string, len(profiles)*4),
	}
	copy(c.profiles, profiles)
	for _, profile := range profiles {
		c.byLanguage[profile.Language] = profile
		c.byAlias[strings.ToLower(profile.Language)] = profile.Language
		for _, alias := range profile.Aliases {
			c.byAlias[strings.ToLower(alias)] = profile.Language
		}
		for _, ext := range profile.Extensions {
			if ext == "" {
				continue
			}
			c.byExtension[strings.ToLower(ext)] = profile.Language
		}
	}
	return c
}

func (c *Catalog) Profiles() []RuntimeProfile {
	out := make([]RuntimeProfile, len(c.profiles))
	copy(out, c.profiles)
	return out
}

func (c *Catalog) Profile(language string) (RuntimeProfile, bool) {
	if c == nil {
		return RuntimeProfile{}, false
	}
	if canonical, ok := c.byAlias[strings.ToLower(strings.TrimSpace(language))]; ok {
		profile, exists := c.byLanguage[canonical]
		return profile, exists
	}
	return RuntimeProfile{}, false
}

func (c *Catalog) InferPrimaryLanguage(files []string, workerType string) string {
	workerType = strings.TrimSpace(workerType)
	switch workerType {
	case "designer":
		return "typescript"
	case "tester":
		if len(files) == 0 {
			return "go"
		}
	}

	scores := make(map[string]int)
	for _, file := range files {
		if lang, ok := c.byExtension[strings.ToLower(filepath.Ext(file))]; ok {
			scores[lang]++
		}
	}
	bestLang := ""
	bestScore := 0
	for lang, score := range scores {
		if score > bestScore {
			bestLang = lang
			bestScore = score
		}
	}
	return bestLang
}

func InferPrimaryLanguage(files []string, workerType string) string {
	return DefaultCatalog().InferPrimaryLanguage(files, workerType)
}

func (c *Catalog) PlanExecCell(language string, roots ExecRoots) (ExecCellPlan, bool) {
	profile, ok := c.Profile(language)
	if !ok {
		return ExecCellPlan{}, false
	}

	cacheRoot := roots.Cache
	if profile.Policy.CacheBucket != "" {
		cacheRoot = filepath.Join(roots.Cache, profile.Policy.CacheBucket)
	}

	plan := ExecCellPlan{
		Language:          profile.Language,
		WorkspaceRoot:     roots.Workspace,
		TempRoot:          roots.Temp,
		CacheRoot:         cacheRoot,
		HomeRoot:          roots.Home,
		OutRoot:           roots.Out,
		Toolchains:        append([]string(nil), profile.Policy.Toolchains...),
		ExecutableAliases: copyStringSliceMap(profile.Policy.ExecutableAliases),
		Env:               make(map[string]string),
	}

	for _, key := range profile.Policy.TempVars {
		plan.Env[key] = roots.Temp
	}
	for _, key := range profile.Policy.HomeVars {
		plan.Env[key] = roots.Home
	}
	for key, rel := range profile.Policy.CacheVars {
		plan.Env[key] = resolveEnvRoot(cacheRoot, rel)
	}
	for key, rel := range profile.Policy.OutVars {
		plan.Env[key] = resolveEnvRoot(roots.Out, rel)
	}
	for key, tmpl := range profile.Policy.StaticEnv {
		plan.Env[key] = renderEnvTemplate(tmpl, roots, cacheRoot)
	}
	return plan, true
}

func resolveEnvRoot(root, rel string) string {
	if strings.TrimSpace(rel) == "" {
		return root
	}
	return filepath.Join(root, rel)
}

func copyStringSliceMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string][]string, len(src))
	for key, values := range src {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func renderEnvTemplate(tmpl string, roots ExecRoots, cacheRoot string) string {
	replacer := strings.NewReplacer(
		"${workspace}", roots.Workspace,
		"${tmp}", roots.Temp,
		"${cache}", cacheRoot,
		"${home}", roots.Home,
		"${out}", roots.Out,
	)
	return replacer.Replace(tmpl)
}

func (c *Catalog) DetectProject(root string) ProjectRuntimeSummary {
	root = strings.TrimSpace(root)
	if root == "" {
		return ProjectRuntimeSummary{}
	}

	extCounts := scanProjectExtensions(root)
	type scoredLang struct {
		lang  string
		score int
	}

	frameworks := make([]DetectedFramework, 0)
	scored := make([]scoredLang, 0, len(c.profiles))
	for _, profile := range c.profiles {
		score := 0
		for _, file := range profile.ManifestFiles {
			if detect.FileExists(root, file) {
				score += 10
			}
		}
		for _, file := range profile.LockFiles {
			if detect.FileExists(root, file) {
				score += 6
			}
		}
		for _, ext := range profile.Extensions {
			score += extCounts[strings.ToLower(ext)]
		}
		for _, framework := range profile.Frameworks {
			if framework.Enabled != nil && framework.Enabled(root) {
				score += 8 + framework.Priority/20
				frameworks = append(frameworks, DetectedFramework{
					Language:    profile.Language,
					ID:          framework.ID,
					Name:        framework.Name,
					TestCommand: framework.TestCommand,
					Priority:    framework.Priority,
				})
			}
		}
		if score > 0 {
			scored = append(scored, scoredLang{lang: profile.Language, score: score})
		}
	}

	slices.SortFunc(scored, func(a, b scoredLang) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		case a.lang < b.lang:
			return -1
		case a.lang > b.lang:
			return 1
		default:
			return 0
		}
	})
	slices.SortFunc(frameworks, func(a, b DetectedFramework) int {
		switch {
		case a.Priority > b.Priority:
			return -1
		case a.Priority < b.Priority:
			return 1
		case a.Language < b.Language:
			return -1
		case a.Language > b.Language:
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})

	summary := ProjectRuntimeSummary{
		Frameworks: frameworks,
		ExecPlans:  make(map[string]ExecCellPlan),
	}
	if len(scored) > 0 {
		summary.PrimaryLanguage = scored[0].lang
	}
	roots := DefaultLogicalExecRoots(root)
	for _, entry := range scored {
		summary.Languages = append(summary.Languages, entry.lang)
		if plan, ok := c.PlanExecCell(entry.lang, roots); ok {
			summary.ExecPlans[entry.lang] = plan
		}
	}
	return summary
}

func scanProjectExtensions(root string) map[string]int {
	counts := make(map[string]int)
	_ = filepath.WalkDir(root, func(filePath string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".idea", ".vscode", "dist", "build", "target":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(filePath))
		if ext != "" {
			counts[ext]++
		}
		return nil
	})
	return counts
}

func defaultProfiles() []RuntimeProfile {
	return []RuntimeProfile{
		goProfile(),
		javascriptProfile(),
		typescriptProfile(),
		tsxProfile(),
		pythonProfile(),
		rustProfile(),
		rubyProfile(),
		phpProfile(),
		javaProfile(),
		kotlinProfile(),
		scalaProfile(),
		cProfile(),
		cppProfile(),
		csharpProfile(),
		swiftProfile(),
		zigProfile(),
		dartProfile(),
		elixirProfile(),
		erlangProfile(),
		luaProfile(),
		haskellProfile(),
		bashProfile(),
		htmlProfile(),
		cssProfile(),
		jsonProfile(),
		yamlProfile(),
		tomlConfigProfile(),
		propertiesProfile(),
		markdownProfile(),
		hclProfile(),
		vueProfile(),
		svelteProfile(),
		rProfile(),
		juliaProfile(),
	}
}

func goProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "go",
		Language:      "go",
		Aliases:       []string{"golang"},
		Extensions:    []string{".go"},
		ManifestFiles: []string{"go.mod"},
		LockFiles:     []string{"go.sum"},
		Frameworks: []FrameworkProfile{
			framework("go-test", "Go Test", "go", "go test ./...", "", "", "", []string{"go.mod"}, nil, []string{"*_test.go"}, 100,
				func(root string) bool { return detect.Which("go") != "" && detect.FileExists(root, "go.mod") }),
		},
		Policy: RuntimePolicy{
			TempVars:    []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"},
			HomeVars:    []string{"HOME"},
			CacheVars:   map[string]string{"GOCACHE": "", "GOMODCACHE": "modcache"},
			OutVars:     map[string]string{"GOTESTSUM_JUNITFILE": "junit.xml"},
			StaticEnv:   map[string]string{"GOENV": "off"},
			Toolchains:  []string{"go"},
			ManifestRO:  []string{"go.mod", "go.sum"},
			CacheBucket: "go",
		},
	}
}

func javascriptProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "javascript",
		Language:      "javascript",
		Aliases:       []string{"js", "node"},
		Extensions:    []string{".js", ".jsx", ".mjs", ".cjs"},
		ManifestFiles: []string{"package.json"},
		LockFiles:     []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"},
		Frameworks: []FrameworkProfile{
			framework("vitest", "Vitest", "javascript", "npx vitest run", "", "node", "npx vitest", []string{"vitest.config.ts", "vitest.config.js", "vite.config.ts", "vite.config.js"}, []string{"package.json"}, []string{"*.test.js", "*.spec.js"}, 100,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "vitest")
					return ok || detect.FileExists(root, "vitest.config.ts", "vitest.config.js")
				}),
			framework("jest", "Jest", "javascript", "npx jest", "", "node", "npx jest --watch", []string{"jest.config.js", "jest.config.ts", "jest.config.mjs", "jest.config.cjs"}, []string{"package.json"}, []string{"*.test.js", "*.spec.js"}, 90,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "jest")
					return ok || detect.FileExists(root, "jest.config.js", "jest.config.ts")
				}),
			framework("mocha", "Mocha", "javascript", "npx mocha", "", "node", "npx mocha --watch", []string{".mocharc.js", ".mocharc.json", ".mocharc.yml"}, []string{"package.json"}, []string{"test/**/*.js", "*.test.js", "*.spec.js"}, 80,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "mocha")
					return ok || detect.FileExists(root, ".mocharc.js", ".mocharc.json", ".mocharc.yml")
				}),
		},
		Policy: nodePolicy("javascript"),
	}
}

func typescriptProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "typescript",
		Language:      "typescript",
		Aliases:       []string{"ts"},
		Extensions:    []string{".ts", ".tsx"},
		ManifestFiles: []string{"package.json", "tsconfig.json"},
		LockFiles:     []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"},
		Frameworks: []FrameworkProfile{
			framework("vitest", "Vitest", "typescript", "npx vitest run", "npx tsc -p tsconfig.json --noEmit", "node", "npx vitest", []string{"vitest.config.ts", "vitest.config.js", "vite.config.ts", "vite.config.js"}, []string{"tsconfig.json"}, []string{"*.test.ts", "*.spec.ts", "*.test.tsx", "*.spec.tsx"}, 100,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "vitest")
					return ok || detect.FileExists(root, "vitest.config.ts", "vite.config.ts")
				}),
			framework("jest", "Jest", "typescript", "npx jest", "npx tsc -p tsconfig.json --noEmit", "node", "npx jest --watch", []string{"jest.config.ts", "jest.config.js"}, []string{"tsconfig.json"}, []string{"*.test.ts", "*.spec.ts"}, 90,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "jest")
					return ok || detect.FileExists(root, "jest.config.ts", "jest.config.js")
				}),
		},
		Policy: nodePolicy("typescript"),
	}
}

func tsxProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "tsx",
		Language:      "tsx",
		Aliases:       []string{"typescriptreact", "javascriptreact"},
		Extensions:    []string{".tsx"},
		ManifestFiles: []string{"package.json", "tsconfig.json"},
		LockFiles:     []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"},
		Frameworks: []FrameworkProfile{
			framework("vitest", "Vitest", "tsx", "npx vitest run", "npx tsc -p tsconfig.json --noEmit", "node", "npx vitest", []string{"vitest.config.ts", "vitest.config.js", "vite.config.ts", "vite.config.js"}, []string{"tsconfig.json"}, []string{"*.test.tsx", "*.spec.tsx"}, 100,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "vitest")
					return ok || detect.FileExists(root, "vitest.config.ts", "vite.config.ts")
				}),
			framework("jest", "Jest", "tsx", "npx jest", "npx tsc -p tsconfig.json --noEmit", "node", "npx jest --watch", []string{"jest.config.ts", "jest.config.js"}, []string{"tsconfig.json"}, []string{"*.test.tsx", "*.spec.tsx"}, 90,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "jest")
					return ok || detect.FileExists(root, "jest.config.ts", "jest.config.js")
				}),
		},
		Policy: nodePolicy("tsx"),
	}
}

func nodePolicy(bucket string) RuntimePolicy {
	return RuntimePolicy{
		TempVars: []string{"TMPDIR", "TMP", "TEMP"},
		HomeVars: []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"},
		CacheVars: map[string]string{
			"npm_config_cache":      "npm",
			"YARN_CACHE_FOLDER":     "yarn",
			"PNPM_HOME":             "pnpm",
			"BUN_INSTALL_CACHE_DIR": "bun",
			"XDG_CACHE_HOME":        "",
		},
		OutVars: map[string]string{
			"NYC_OUTPUT": "coverage",
		},
		StaticEnv: map[string]string{
			"NODE_REPL_HISTORY": "${home}/.node_repl_history",
		},
		Toolchains:  []string{"node", "npm", "pnpm", "yarn"},
		ManifestRO:  []string{"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "tsconfig.json"},
		CacheBucket: bucket,
	}
}

func readMostlyPolicy(bucket string, toolchains ...string) RuntimePolicy {
	return RuntimePolicy{
		TempVars: []string{"TMPDIR", "TMP", "TEMP"},
		HomeVars: []string{"HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"},
		CacheVars: map[string]string{
			"XDG_CACHE_HOME": "",
		},
		Toolchains:  append([]string(nil), toolchains...),
		CacheBucket: bucket,
	}
}

func shellPolicy(bucket string) RuntimePolicy {
	return RuntimePolicy{
		TempVars: []string{"TMPDIR", "TMP", "TEMP"},
		HomeVars: []string{"HOME"},
		CacheVars: map[string]string{
			"XDG_CACHE_HOME": "",
		},
		Toolchains:  []string{"bash", "sh"},
		CacheBucket: bucket,
	}
}

func pythonProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "python",
		Language:      "python",
		Aliases:       []string{"py"},
		Extensions:    []string{".py"},
		ManifestFiles: []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "tox.ini"},
		LockFiles:     []string{"poetry.lock", "Pipfile.lock", "uv.lock"},
		Frameworks: []FrameworkProfile{
			framework("pytest", "pytest", "python", "python -m pytest", "python -m compileall .", "python", "ptw", []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini"}, nil, []string{"test_*.py", "*_test.py"}, 100,
				func(root string) bool {
					if detect.Which("python") == "" && detect.Which("python3") == "" {
						return false
					}
					ok, _ := detect.HasPythonDependency(root, "pytest")
					return ok || detect.FileExists(root, "pytest.ini", "tox.ini")
				}),
			framework("python-unittest", "unittest", "python", "python -m unittest discover", "python -m compileall .", "python", "", []string{"pyproject.toml", "setup.py", "requirements.txt"}, nil, []string{"test*.py"}, 80,
				func(root string) bool { return detect.Which("python") != "" || detect.Which("python3") != "" }),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"},
			CacheVars: map[string]string{
				"PIP_CACHE_DIR":         "pip",
				"PYTHONPYCACHEPREFIX":   "pycache",
				"XDG_CACHE_HOME":        "",
				"PYTEST_DEBUG_TEMPROOT": "pytest-tmp",
			},
			OutVars: map[string]string{
				"COVERAGE_FILE": "coverage/.coverage",
			},
			StaticEnv: map[string]string{
				"PYTHONNOUSERSITE": "1",
			},
			Toolchains: []string{"python", "python3", "pip", "pip3", "pytest"},
			ExecutableAliases: map[string][]string{
				"python": {"python3"},
				"pip":    {"pip3"},
			},
			ManifestRO:  []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "tox.ini"},
			CacheBucket: "python",
		},
	}
}

func rustProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "rust",
		Language:      "rust",
		Aliases:       []string{"rs"},
		Extensions:    []string{".rs"},
		ManifestFiles: []string{"Cargo.toml"},
		LockFiles:     []string{"Cargo.lock"},
		Frameworks: []FrameworkProfile{
			framework("cargo-test", "Cargo Test", "rust", "cargo test", "cargo build", "cargo run", "cargo watch -x test", []string{"Cargo.toml"}, nil, []string{"tests/*.rs", "*_test.rs"}, 100,
				func(root string) bool { return detect.Which("cargo") != "" && detect.FileExists(root, "Cargo.toml") }),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"CARGO_HOME":       "cargo-home",
				"CARGO_TARGET_DIR": "target",
				"RUSTUP_HOME":      "rustup",
			},
			OutVars: map[string]string{},
			StaticEnv: map[string]string{
				"CARGO_INCREMENTAL": "0",
			},
			Toolchains:  []string{"cargo", "rustc"},
			ManifestRO:  []string{"Cargo.toml", "Cargo.lock"},
			CacheBucket: "rust",
		},
	}
}

func rubyProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "ruby",
		Language:      "ruby",
		Aliases:       []string{"rb"},
		Extensions:    []string{".rb"},
		ManifestFiles: []string{"Gemfile"},
		LockFiles:     []string{"Gemfile.lock"},
		Frameworks: []FrameworkProfile{
			framework("rspec", "RSpec", "ruby", "bundle exec rspec", "bundle exec rake build", "ruby", "bundle exec guard", []string{".rspec", "spec/spec_helper.rb"}, nil, []string{"*_spec.rb"}, 100,
				func(root string) bool {
					ok, _ := detect.HasGemDependency(root, "rspec")
					return ok || detect.FileExists(root, ".rspec")
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"BUNDLE_USER_HOME": "bundle",
				"GEM_HOME":         "gems",
				"GEM_PATH":         "gems",
			},
			StaticEnv:   map[string]string{},
			Toolchains:  []string{"ruby", "bundle"},
			ManifestRO:  []string{"Gemfile", "Gemfile.lock"},
			CacheBucket: "ruby",
		},
	}
}

func phpProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "php",
		Language:      "php",
		Aliases:       []string{"php8"},
		Extensions:    []string{".php"},
		ManifestFiles: []string{"composer.json", "phpunit.xml", "phpunit.xml.dist"},
		LockFiles:     []string{"composer.lock"},
		Frameworks: []FrameworkProfile{
			framework("phpunit", "PHPUnit", "php", "./vendor/bin/phpunit", "composer install --no-interaction", "php", "", []string{"phpunit.xml", "phpunit.xml.dist"}, []string{"composer.json"}, []string{"*Test.php"}, 100,
				func(root string) bool {
					return detect.FileExists(root, "phpunit.xml", "phpunit.xml.dist", "vendor/bin/phpunit")
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"COMPOSER_CACHE_DIR": "composer-cache",
				"COMPOSER_HOME":      "composer-home",
			},
			Toolchains:  []string{"php", "composer"},
			ManifestRO:  []string{"composer.json", "composer.lock", "phpunit.xml", "phpunit.xml.dist"},
			CacheBucket: "php",
		},
	}
}

func javaProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "java",
		Language:      "java",
		Aliases:       []string{"jvm"},
		Extensions:    []string{".java"},
		ManifestFiles: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		LockFiles:     []string{"gradle.lockfile", "mvnw", "gradlew"},
		Frameworks: []FrameworkProfile{
			framework("maven-test", "Maven Test", "java", "mvn test", "mvn package -DskipTests", "java", "mvn -o test", []string{"pom.xml"}, nil, []string{"*Test.java"}, 100,
				func(root string) bool { return detect.FileExists(root, "pom.xml") && detect.Which("mvn") != "" }),
			framework("gradle-test", "Gradle Test", "java", "./gradlew test", "./gradlew build -x test", "java", "./gradlew test --continuous", []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, nil, []string{"*Test.java"}, 95,
				func(root string) bool {
					return detect.FileExists(root, "build.gradle", "build.gradle.kts") && (detect.FileExists(root, "gradlew", "gradlew.bat") || detect.Which("gradle") != "")
				}),
		},
		Policy: jvmPolicy("java"),
	}
}

func kotlinProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "kotlin",
		Language:      "kotlin",
		Aliases:       []string{"kt"},
		Extensions:    []string{".kt", ".kts"},
		ManifestFiles: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "pom.xml"},
		LockFiles:     []string{"gradle.lockfile", "mvnw", "gradlew"},
		Frameworks: []FrameworkProfile{
			framework("gradle-test", "Gradle Test", "kotlin", "./gradlew test", "./gradlew build -x test", "kotlin", "./gradlew test --continuous", []string{"build.gradle", "build.gradle.kts"}, nil, []string{"*Test.kt"}, 100,
				func(root string) bool {
					return detect.FileExists(root, "build.gradle", "build.gradle.kts") && (detect.FileExists(root, "gradlew", "gradlew.bat") || detect.Which("gradle") != "")
				}),
			framework("maven-test", "Maven Test", "kotlin", "mvn test", "mvn package -DskipTests", "kotlin", "mvn -o test", []string{"pom.xml"}, nil, []string{"*Test.kt"}, 90,
				func(root string) bool { return detect.FileExists(root, "pom.xml") && detect.Which("mvn") != "" }),
		},
		Policy: jvmPolicy("kotlin"),
	}
}

func scalaProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "scala",
		Language:      "scala",
		Extensions:    []string{".scala", ".sc"},
		ManifestFiles: []string{"build.sbt", "project/build.properties", "build.sc"},
		LockFiles:     []string{"project/build.properties"},
		Frameworks: []FrameworkProfile{
			framework("sbt-test", "sbt test", "scala", "sbt test", "sbt compile", "sbt run", "~test", []string{"build.sbt", "project/build.properties"}, nil, []string{"*Spec.scala", "*Test.scala"}, 100,
				func(root string) bool { return detect.FileExists(root, "build.sbt") && detect.Which("sbt") != "" }),
			framework("mill-test", "mill test", "scala", "mill __.test", "mill __.compile", "mill run", "", []string{"build.sc"}, nil, []string{"*Spec.scala", "*Test.scala"}, 90,
				func(root string) bool { return detect.FileExists(root, "build.sc") && detect.Which("mill") != "" }),
		},
		Policy: scalaPolicy(),
	}
}

func jvmPolicy(bucket string) RuntimePolicy {
	return RuntimePolicy{
		TempVars: []string{"TMPDIR", "TMP", "TEMP"},
		HomeVars: []string{"HOME"},
		CacheVars: map[string]string{
			"GRADLE_USER_HOME": "gradle",
			"MAVEN_CONFIG":     "maven",
		},
		OutVars: map[string]string{
			"JUNIT_XML_DIR": "junit",
		},
		StaticEnv: map[string]string{
			"JAVA_TOOL_OPTIONS": "-Djava.io.tmpdir=${tmp}",
			"MAVEN_OPTS":        "-Dmaven.repo.local=${cache}/m2",
		},
		Toolchains:  []string{"java", "javac", "mvn", "gradle"},
		ManifestRO:  []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		CacheBucket: bucket,
	}
}

func scalaPolicy() RuntimePolicy {
	policy := jvmPolicy("scala")
	policy.CacheVars["COURSIER_CACHE"] = "coursier"
	policy.Toolchains = []string{"java", "javac", "scala", "scalac", "sbt", "mill"}
	policy.ManifestRO = []string{"build.sbt", "project/build.properties", "build.sc"}
	return policy
}

func cProfile() RuntimeProfile {
	return nativeCProfile("c", []string{".c", ".h"})
}

func cppProfile() RuntimeProfile {
	return nativeCProfile("cpp", []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"})
}

func nativeCProfile(language string, exts []string) RuntimeProfile {
	return RuntimeProfile{
		ID:            language,
		Language:      language,
		Aliases:       []string{language},
		Extensions:    exts,
		ManifestFiles: []string{"CMakeLists.txt", "Makefile", "configure.ac"},
		LockFiles:     nil,
		Frameworks: []FrameworkProfile{
			framework("ctest", "CTest", language, "ctest --output-on-failure", "cmake -S . -B build && cmake --build build", "", "", []string{"CMakeLists.txt"}, nil, []string{"*_test.c", "*_test.cpp"}, 100,
				func(root string) bool {
					return detect.FileExists(root, "CMakeLists.txt") && detect.Which("ctest") != ""
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"CCACHE_DIR": "ccache",
			},
			Toolchains:  []string{"cc", "gcc", "clang", "cmake", "ctest", "make"},
			ManifestRO:  []string{"CMakeLists.txt", "Makefile", "configure.ac"},
			CacheBucket: language,
		},
	}
}

func csharpProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "csharp",
		Language:      "csharp",
		Aliases:       []string{"c#", "cs", "dotnet"},
		Extensions:    []string{".cs"},
		ManifestFiles: []string{"Directory.Build.props", "NuGet.config"},
		LockFiles:     []string{"packages.lock.json"},
		Frameworks: []FrameworkProfile{
			framework("dotnet-test", ".NET Test", "csharp", "dotnet test", "dotnet build", "dotnet run", "dotnet watch test", []string{"*.sln", "*.csproj"}, nil, []string{"*Tests.cs"}, 100,
				func(root string) bool {
					return detect.Which("dotnet") != "" && hasAnyExtension(root, ".sln", ".csproj", ".fsproj", ".vbproj")
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME", "DOTNET_CLI_HOME"},
			CacheVars: map[string]string{
				"NUGET_PACKAGES": "nuget/packages",
			},
			StaticEnv: map[string]string{
				"MSBUILDDISABLENODEREUSE": "1",
			},
			Toolchains:  []string{"dotnet"},
			ManifestRO:  []string{"NuGet.config", "Directory.Build.props"},
			CacheBucket: "dotnet",
		},
	}
}

func swiftProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "swift",
		Language:      "swift",
		Aliases:       []string{"swiftpm"},
		Extensions:    []string{".swift"},
		ManifestFiles: []string{"Package.swift"},
		Frameworks: []FrameworkProfile{
			framework("swift-test", "Swift Test", "swift", "swift test", "swift build", "swift run", "", []string{"Package.swift"}, nil, []string{"*Tests.swift"}, 100,
				func(root string) bool { return detect.Which("swift") != "" && detect.FileExists(root, "Package.swift") }),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"SWIFTPM_MODULECACHE_OVERRIDE": "module-cache",
				"CLANG_MODULE_CACHE_PATH":      "clang-module-cache",
			},
			Toolchains:  []string{"swift"},
			ManifestRO:  []string{"Package.swift"},
			CacheBucket: "swift",
		},
	}
}

func zigProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "zig",
		Language:      "zig",
		Extensions:    []string{".zig"},
		ManifestFiles: []string{"build.zig"},
		Frameworks: []FrameworkProfile{
			framework("zig-test", "Zig Test", "zig", "zig test", "zig build", "zig run", "", []string{"build.zig"}, nil, []string{"*_test.zig"}, 100,
				func(root string) bool { return detect.Which("zig") != "" && detect.FileExists(root, "build.zig") }),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"ZIG_LOCAL_CACHE_DIR":  "local-cache",
				"ZIG_GLOBAL_CACHE_DIR": "global-cache",
			},
			Toolchains:  []string{"zig"},
			ManifestRO:  []string{"build.zig"},
			CacheBucket: "zig",
		},
	}
}

func dartProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "dart",
		Language:      "dart",
		Extensions:    []string{".dart"},
		ManifestFiles: []string{"pubspec.yaml"},
		LockFiles:     []string{"pubspec.lock"},
		Frameworks: []FrameworkProfile{
			framework("dart-test", "Dart Test", "dart", "dart test", "dart compile exe", "dart run", "", []string{"pubspec.yaml"}, nil, []string{"*_test.dart"}, 100,
				func(root string) bool {
					return (detect.Which("dart") != "" || detect.Which("flutter") != "") && detect.FileExists(root, "pubspec.yaml")
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"PUB_CACHE": "pub-cache",
			},
			Toolchains:  []string{"dart", "flutter"},
			ManifestRO:  []string{"pubspec.yaml", "pubspec.lock"},
			CacheBucket: "dart",
		},
	}
}

func elixirProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "elixir",
		Language:      "elixir",
		Extensions:    []string{".ex", ".exs"},
		ManifestFiles: []string{"mix.exs"},
		LockFiles:     []string{"mix.lock"},
		Frameworks: []FrameworkProfile{
			framework("exunit", "ExUnit", "elixir", "mix test", "mix compile", "iex -S mix", "mix test.watch", []string{"mix.exs"}, nil, []string{"*_test.exs"}, 100,
				func(root string) bool { return detect.Which("mix") != "" && detect.FileExists(root, "mix.exs") }),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"MIX_HOME": "mix",
				"HEX_HOME": "hex",
			},
			Toolchains:  []string{"mix", "elixir", "erl"},
			ManifestRO:  []string{"mix.exs", "mix.lock"},
			CacheBucket: "elixir",
		},
	}
}

func erlangProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "erlang",
		Language:      "erlang",
		Extensions:    []string{".erl", ".hrl"},
		ManifestFiles: []string{"rebar.config"},
		LockFiles:     []string{"rebar.lock"},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"REBAR_CACHE_DIR": "rebar",
			},
			Toolchains:  []string{"erl", "rebar3"},
			ManifestRO:  []string{"rebar.config", "rebar.lock"},
			CacheBucket: "erlang",
		},
	}
}

func luaProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "lua",
		Language:      "lua",
		Extensions:    []string{".lua"},
		ManifestFiles: []string{"*.rockspec"},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"LUAROCKS_CONFIG": "luarocks",
			},
			Toolchains:  []string{"lua", "luarocks"},
			CacheBucket: "lua",
		},
	}
}

func haskellProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "haskell",
		Language:      "haskell",
		Aliases:       []string{"hs"},
		Extensions:    []string{".hs"},
		ManifestFiles: []string{"stack.yaml", "cabal.project"},
		LockFiles:     []string{"stack.yaml.lock"},
		Frameworks: []FrameworkProfile{
			framework("stack-test", "Stack Test", "haskell", "stack test", "stack build", "stack run", "", []string{"stack.yaml"}, nil, []string{"*Spec.hs", "*Test.hs"}, 100,
				func(root string) bool { return detect.FileExists(root, "stack.yaml") && detect.Which("stack") != "" }),
			framework("cabal-test", "Cabal Test", "haskell", "cabal test", "cabal build", "cabal run", "", []string{"cabal.project"}, nil, []string{"*Spec.hs", "*Test.hs"}, 90,
				func(root string) bool {
					return (detect.FileExists(root, "cabal.project") || hasAnyExtension(root, ".cabal")) && detect.Which("cabal") != ""
				}),
		},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"STACK_ROOT":     "stack",
				"CABAL_DIR":      "cabal",
				"XDG_CACHE_HOME": "",
			},
			Toolchains:  []string{"ghc", "cabal", "stack"},
			ManifestRO:  []string{"stack.yaml", "cabal.project"},
			CacheBucket: "haskell",
		},
	}
}

func bashProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "bash",
		Language:   "bash",
		Aliases:    []string{"shell", "shellscript"},
		Extensions: []string{".sh", ".bash"},
		Policy:     shellPolicy("bash"),
	}
}

func htmlProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "html",
		Language:   "html",
		Extensions: []string{".html", ".htm"},
		Policy:     readMostlyPolicy("html"),
	}
}

func cssProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "css",
		Language:   "css",
		Extensions: []string{".css"},
		Policy:     readMostlyPolicy("css"),
	}
}

func jsonProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "json",
		Language:   "json",
		Aliases:    []string{"jsonc"},
		Extensions: []string{".json"},
		Policy:     readMostlyPolicy("json"),
	}
}

func yamlProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "yaml",
		Language:   "yaml",
		Aliases:    []string{"yml"},
		Extensions: []string{".yaml", ".yml"},
		Policy:     readMostlyPolicy("yaml", "yq"),
	}
}

func tomlConfigProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "toml",
		Language:      "toml",
		Extensions:    []string{".toml"},
		ManifestFiles: []string{"pyproject.toml", "Cargo.toml", "Project.toml"},
		Policy:        readMostlyPolicy("toml", "taplo"),
	}
}

func propertiesProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "properties",
		Language:   "properties",
		Extensions: []string{".properties"},
		Policy:     readMostlyPolicy("properties"),
	}
}

func markdownProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:         "markdown",
		Language:   "markdown",
		Aliases:    []string{"md"},
		Extensions: []string{".md", ".markdown"},
		Policy:     readMostlyPolicy("markdown"),
	}
}

func hclProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "hcl",
		Language:      "hcl",
		Aliases:       []string{"terraform"},
		Extensions:    []string{".tf", ".tfvars"},
		ManifestFiles: []string{"main.tf", "terraform.tf", "providers.tf", ".terraform.lock.hcl"},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"TF_DATA_DIR": "terraform-data",
			},
			Toolchains:  []string{"terraform"},
			ManifestRO:  []string{"main.tf", "terraform.tf", "providers.tf", ".terraform.lock.hcl"},
			CacheBucket: "hcl",
		},
	}
}

func vueProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "vue",
		Language:      "vue",
		Extensions:    []string{".vue"},
		ManifestFiles: []string{"package.json", "vite.config.ts", "vite.config.js"},
		LockFiles:     []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"},
		Frameworks: []FrameworkProfile{
			framework("vitest", "Vitest", "vue", "npx vitest run", "npx vue-tsc --noEmit", "node", "npx vitest", []string{"vitest.config.ts", "vite.config.ts", "vite.config.js"}, []string{"package.json"}, []string{"*.test.ts", "*.spec.ts", "*.test.js", "*.spec.js"}, 100,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "vitest")
					return ok || detect.FileExists(root, "vitest.config.ts", "vite.config.ts", "vite.config.js")
				}),
		},
		Policy: nodePolicy("vue"),
	}
}

func svelteProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "svelte",
		Language:      "svelte",
		Extensions:    []string{".svelte"},
		ManifestFiles: []string{"package.json", "vite.config.ts", "vite.config.js", "svelte.config.js", "svelte.config.ts"},
		LockFiles:     []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"},
		Frameworks: []FrameworkProfile{
			framework("vitest", "Vitest", "svelte", "npx vitest run", "npx svelte-check", "node", "npx vitest", []string{"vitest.config.ts", "vite.config.ts", "vite.config.js"}, []string{"package.json"}, []string{"*.test.ts", "*.spec.ts", "*.test.js", "*.spec.js"}, 100,
				func(root string) bool {
					ok, _ := detect.HasDependency(root, "vitest")
					return ok || detect.FileExists(root, "vitest.config.ts", "vite.config.ts", "vite.config.js")
				}),
		},
		Policy: nodePolicy("svelte"),
	}
}

func rProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "r",
		Language:      "r",
		Extensions:    []string{".r", ".R"},
		ManifestFiles: []string{"DESCRIPTION", "renv.lock"},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"RENV_PATHS_CACHE": "renv",
			},
			Toolchains:  []string{"R", "Rscript"},
			ManifestRO:  []string{"DESCRIPTION", "renv.lock"},
			CacheBucket: "r",
		},
	}
}

func juliaProfile() RuntimeProfile {
	return RuntimeProfile{
		ID:            "julia",
		Language:      "julia",
		Extensions:    []string{".jl"},
		ManifestFiles: []string{"Project.toml"},
		LockFiles:     []string{"Manifest.toml"},
		Policy: RuntimePolicy{
			TempVars: []string{"TMPDIR", "TMP", "TEMP"},
			HomeVars: []string{"HOME"},
			CacheVars: map[string]string{
				"JULIA_DEPOT_PATH": "depot",
			},
			Toolchains:  []string{"julia"},
			ManifestRO:  []string{"Project.toml", "Manifest.toml"},
			CacheBucket: "julia",
		},
	}
}

func framework(
	id, name, language, testCmd, buildCmd, runCmd, watchCmd string,
	configFiles, indicatorFiles, testPatterns []string,
	priority int,
	enabled func(projectDir string) bool,
) FrameworkProfile {
	return FrameworkProfile{
		ID:               id,
		Name:             name,
		Language:         language,
		TestCommand:      testCmd,
		BuildCommand:     buildCmd,
		RunCommand:       runCmd,
		WatchCommand:     watchCmd,
		ConfigFiles:      append([]string(nil), configFiles...),
		IndicatorFiles:   append([]string(nil), indicatorFiles...),
		TestFilePatterns: append([]string(nil), testPatterns...),
		Priority:         priority,
		Enabled:          enabled,
	}
}

func hasAnyExtension(root string, exts ...string) bool {
	normalized := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		normalized[strings.ToLower(ext)] = struct{}{}
	}
	found := false
	_ = filepath.WalkDir(root, func(filePath string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := normalized[strings.ToLower(filepath.Ext(filePath))]; ok {
			found = true
		}
		return nil
	})
	return found
}
