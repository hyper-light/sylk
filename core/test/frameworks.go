package test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/detect"
)

var BuiltinFrameworks = []*TestFrameworkDefinition{
	goTestFramework(),
	jestFramework(),
	vitestFramework(),
	mochaFramework(),
	bunTestFramework(),
	nodeTestFramework(),
	pytestFramework(),
	pythonUnitTestFramework(),
	cargoTestFramework(),
	rspecFramework(),
	phpunitFramework(),
	mavenTestFramework(),
	gradleTestFramework(),
	sbtTestFramework(),
	millTestFramework(),
	dotnetTestFramework(),
	cTestFramework(),
	swiftTestFramework(),
	zigTestFramework(),
	dartTestFramework(),
	exUnitFramework(),
	rebar3EUnitFramework(),
	stackTestFramework(),
	cabalTestFramework(),
}

func goTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkGoTest,
		Name:             "Go Test",
		Language:         "go",
		RunCommand:       "go test ./...",
		RunFileCommand:   "go test {file}",
		RunSingleCommand: "go test -run {test} {file}",
		CoverageCommand:  "go test -coverprofile=coverage.out ./...",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_test.go"},
		ConfigFiles:      []string{"go.mod"},
		Priority:         100,
		Enabled:          goTestEnabled,
	}
}

func jestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkJest,
		Name:             "Jest",
		Language:         "javascript",
		RunCommand:       "npx jest",
		RunFileCommand:   "npx jest {file}",
		RunSingleCommand: "npx jest {file} -t {test}",
		CoverageCommand:  "npx jest --coverage",
		WatchCommand:     "npx jest --watch",
		TestFilePatterns: []string{"*.test.js", "*.spec.js", "*.test.ts", "*.spec.ts", "*.test.jsx", "*.spec.jsx", "*.test.tsx", "*.spec.tsx"},
		ConfigFiles:      []string{"jest.config.js", "jest.config.ts", "jest.config.mjs", "jest.config.cjs"},
		Priority:         90,
		Enabled:          jestEnabled,
	}
}

func vitestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkVitest,
		Name:             "Vitest",
		Language:         "javascript",
		RunCommand:       "npx vitest run",
		RunFileCommand:   "npx vitest run {file}",
		RunSingleCommand: "npx vitest run {file} -t {test}",
		CoverageCommand:  "npx vitest run --coverage",
		WatchCommand:     "npx vitest",
		TestFilePatterns: []string{"*.test.js", "*.spec.js", "*.test.ts", "*.spec.ts"},
		ConfigFiles:      []string{"vitest.config.js", "vitest.config.ts", "vite.config.js", "vite.config.ts"},
		Priority:         100,
		Enabled:          vitestEnabled,
	}
}

func mochaFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkMocha,
		Name:             "Mocha",
		Language:         "javascript",
		RunCommand:       "npx mocha",
		RunFileCommand:   "npx mocha {file}",
		RunSingleCommand: "npx mocha {file} --grep {test}",
		CoverageCommand:  "npx nyc mocha",
		WatchCommand:     "npx mocha --watch",
		TestFilePatterns: []string{"*.test.js", "*.spec.js", "test/**/*.js"},
		ConfigFiles:      []string{".mocharc.js", ".mocharc.json", ".mocharc.yml"},
		Priority:         80,
		Enabled:          mochaEnabled,
	}
}

func bunTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkBunTest,
		Name:             "Bun Test",
		Language:         "javascript",
		RunCommand:       "bun test",
		RunFileCommand:   "bun test {file}",
		RunSingleCommand: "bun test {file} --test-name-pattern {test}",
		CoverageCommand:  "bun test --coverage",
		WatchCommand:     "bun test --watch",
		TestFilePatterns: []string{"*.test.js", "*.spec.js", "*.test.ts", "*.spec.ts"},
		ConfigFiles:      []string{"bunfig.toml", "package.json"},
		Priority:         70,
		Enabled:          bunTestEnabled,
	}
}

func nodeTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkNodeTest,
		Name:             "Node Test Runner",
		Language:         "javascript",
		RunCommand:       "node --test",
		RunFileCommand:   "node --test {file}",
		RunSingleCommand: "node --test --test-name-pattern {test} {file}",
		CoverageCommand:  "node --test --experimental-test-coverage",
		WatchCommand:     "node --test --watch",
		TestFilePatterns: []string{"*.test.js", "*.spec.js", "*.test.mjs", "*.spec.mjs"},
		ConfigFiles:      []string{"package.json"},
		Priority:         60,
		Enabled:          nodeTestEnabled,
	}
}

func pytestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkPytest,
		Name:             "pytest",
		Language:         "python",
		RunCommand:       "python -m pytest",
		RunFileCommand:   "python -m pytest {file}",
		RunSingleCommand: "python -m pytest {file}::{test}",
		CoverageCommand:  "python -m pytest --cov",
		WatchCommand:     "ptw",
		TestFilePatterns: []string{"test_*.py", "*_test.py"},
		ConfigFiles:      []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini"},
		Priority:         100,
		Enabled:          pytestEnabled,
	}
}

func pythonUnitTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkPythonUnitTest,
		Name:             "unittest",
		Language:         "python",
		RunCommand:       "python -m unittest discover",
		RunFileCommand:   "python -m unittest {file}",
		RunSingleCommand: "python -m unittest {test}",
		CoverageCommand:  "python -m unittest discover",
		WatchCommand:     "",
		TestFilePatterns: []string{"test*.py", "*_test.py"},
		ConfigFiles:      []string{"pyproject.toml", "setup.cfg", "setup.py"},
		Priority:         80,
		Enabled:          pythonUnitTestEnabled,
	}
}

func cargoTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkCargoTest,
		Name:             "Cargo Test",
		Language:         "rust",
		RunCommand:       "cargo test",
		RunFileCommand:   "cargo test --test {file}",
		RunSingleCommand: "cargo test {test}",
		CoverageCommand:  "cargo tarpaulin",
		WatchCommand:     "cargo watch -x test",
		TestFilePatterns: []string{"**/tests/*.rs", "**/*_test.rs"},
		ConfigFiles:      []string{"Cargo.toml"},
		Priority:         100,
		Enabled:          cargoTestEnabled,
	}
}

func rspecFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkRSpec,
		Name:             "RSpec",
		Language:         "ruby",
		RunCommand:       "bundle exec rspec",
		RunFileCommand:   "bundle exec rspec {file}",
		RunSingleCommand: "bundle exec rspec {file}:{line}",
		CoverageCommand:  "bundle exec rspec --format documentation",
		WatchCommand:     "bundle exec guard",
		TestFilePatterns: []string{"*_spec.rb", "spec/**/*_spec.rb"},
		ConfigFiles:      []string{".rspec", "spec/spec_helper.rb"},
		Priority:         100,
		Enabled:          rspecEnabled,
	}
}

func phpunitFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkPHPUnit,
		Name:             "PHPUnit",
		Language:         "php",
		RunCommand:       "./vendor/bin/phpunit",
		RunFileCommand:   "./vendor/bin/phpunit {file}",
		RunSingleCommand: "./vendor/bin/phpunit --filter {test}",
		CoverageCommand:  "./vendor/bin/phpunit --coverage-text",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Test.php", "tests/**/*Test.php"},
		ConfigFiles:      []string{"phpunit.xml", "phpunit.xml.dist"},
		Priority:         100,
		Enabled:          phpunitEnabled,
	}
}

func mavenTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkMavenTest,
		Name:             "Maven Test",
		Language:         "java",
		RunCommand:       "mvn test",
		RunFileCommand:   "mvn -Dtest={test} test",
		RunSingleCommand: "mvn -Dtest={test} test",
		CoverageCommand:  "mvn test",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Test.java", "*Test.kt"},
		ConfigFiles:      []string{"pom.xml"},
		Priority:         95,
		Enabled:          mavenTestEnabled,
	}
}

func gradleTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkGradleTest,
		Name:             "Gradle Test",
		Language:         "java",
		RunCommand:       "./gradlew test",
		RunFileCommand:   "./gradlew test --tests {test}",
		RunSingleCommand: "./gradlew test --tests {test}",
		CoverageCommand:  "./gradlew test",
		WatchCommand:     "./gradlew test --continuous",
		TestFilePatterns: []string{"*Test.java", "*Test.kt"},
		ConfigFiles:      []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		Priority:         100,
		Enabled:          gradleTestEnabled,
	}
}

func sbtTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkSBTTest,
		Name:             "sbt test",
		Language:         "scala",
		RunCommand:       "sbt test",
		RunFileCommand:   "sbt testOnly {test}",
		RunSingleCommand: "sbt testOnly {test}",
		CoverageCommand:  "sbt test",
		WatchCommand:     "sbt ~test",
		TestFilePatterns: []string{"*Spec.scala", "*Test.scala"},
		ConfigFiles:      []string{"build.sbt"},
		Priority:         100,
		Enabled:          sbtTestEnabled,
	}
}

func millTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkMillTest,
		Name:             "mill test",
		Language:         "scala",
		RunCommand:       "mill __.test",
		RunFileCommand:   "mill __.test",
		RunSingleCommand: "mill __.test",
		CoverageCommand:  "mill __.test",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Spec.scala", "*Test.scala"},
		ConfigFiles:      []string{"build.sc"},
		Priority:         90,
		Enabled:          millTestEnabled,
	}
}

func dotnetTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkDotNetTest,
		Name:             ".NET Test",
		Language:         "csharp",
		RunCommand:       "dotnet test",
		RunFileCommand:   "dotnet test --filter FullyQualifiedName~{test}",
		RunSingleCommand: "dotnet test --filter FullyQualifiedName~{test}",
		CoverageCommand:  "dotnet test --collect:\"XPlat Code Coverage\"",
		WatchCommand:     "dotnet watch test",
		TestFilePatterns: []string{"*Tests.cs", "*Test.cs"},
		ConfigFiles:      nil,
		Priority:         100,
		Enabled:          dotnetTestEnabled,
	}
}

func cTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkCTest,
		Name:             "CTest",
		Language:         "cpp",
		RunCommand:       "ctest --output-on-failure",
		RunFileCommand:   "ctest --output-on-failure -R {test}",
		RunSingleCommand: "ctest --output-on-failure -R {test}",
		CoverageCommand:  "ctest --output-on-failure",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_test.c", "*_test.cpp", "*_test.cc", "*_test.cxx"},
		ConfigFiles:      []string{"CMakeLists.txt"},
		Priority:         100,
		Enabled:          cTestEnabled,
	}
}

func swiftTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkSwiftTest,
		Name:             "Swift Test",
		Language:         "swift",
		RunCommand:       "swift test",
		RunFileCommand:   "swift test --filter {test}",
		RunSingleCommand: "swift test --filter {test}",
		CoverageCommand:  "swift test --enable-code-coverage",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Tests.swift"},
		ConfigFiles:      []string{"Package.swift"},
		Priority:         100,
		Enabled:          swiftTestEnabled,
	}
}

func zigTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkZigTest,
		Name:             "Zig Test",
		Language:         "zig",
		RunCommand:       "zig test build.zig",
		RunFileCommand:   "zig test {file}",
		RunSingleCommand: "zig test {file}",
		CoverageCommand:  "zig test build.zig",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_test.zig"},
		ConfigFiles:      []string{"build.zig"},
		Priority:         100,
		Enabled:          zigTestEnabled,
	}
}

func dartTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkDartTest,
		Name:             "Dart Test",
		Language:         "dart",
		RunCommand:       "dart test",
		RunFileCommand:   "dart test {file}",
		RunSingleCommand: "dart test --name {test} {file}",
		CoverageCommand:  "dart test --coverage=coverage",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_test.dart"},
		ConfigFiles:      []string{"pubspec.yaml"},
		Priority:         100,
		Enabled:          dartTestEnabled,
	}
}

func exUnitFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkExUnit,
		Name:             "ExUnit",
		Language:         "elixir",
		RunCommand:       "mix test",
		RunFileCommand:   "mix test {file}",
		RunSingleCommand: "mix test {file}:{line}",
		CoverageCommand:  "mix test --cover",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_test.exs"},
		ConfigFiles:      []string{"mix.exs"},
		Priority:         100,
		Enabled:          exUnitEnabled,
	}
}

func rebar3EUnitFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkRebar3EUnit,
		Name:             "rebar3 eunit",
		Language:         "erlang",
		RunCommand:       "rebar3 eunit",
		RunFileCommand:   "rebar3 eunit --module={test}",
		RunSingleCommand: "rebar3 eunit --module={test}",
		CoverageCommand:  "rebar3 cover",
		WatchCommand:     "",
		TestFilePatterns: []string{"*_tests.erl", "*_SUITE.erl"},
		ConfigFiles:      []string{"rebar.config"},
		Priority:         100,
		Enabled:          rebar3EUnitEnabled,
	}
}

func stackTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkStackTest,
		Name:             "stack test",
		Language:         "haskell",
		RunCommand:       "stack test",
		RunFileCommand:   "stack test",
		RunSingleCommand: "stack test",
		CoverageCommand:  "stack test",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Spec.hs", "*Test.hs"},
		ConfigFiles:      []string{"stack.yaml"},
		Priority:         100,
		Enabled:          stackTestEnabled,
	}
}

func cabalTestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkCabalTest,
		Name:             "cabal test",
		Language:         "haskell",
		RunCommand:       "cabal test",
		RunFileCommand:   "cabal test",
		RunSingleCommand: "cabal test",
		CoverageCommand:  "cabal test",
		WatchCommand:     "",
		TestFilePatterns: []string{"*Spec.hs", "*Test.hs"},
		ConfigFiles:      []string{"cabal.project"},
		Priority:         90,
		Enabled:          cabalTestEnabled,
	}
}

func goTestEnabled(projectDir string) bool {
	return detect.Which("go") != "" && detect.FileExists(projectDir, "go.mod")
}

func jestEnabled(projectDir string) bool {
	ok, _ := detect.HasDependency(projectDir, "jest")
	return ok || detect.FileExists(projectDir, "jest.config.js", "jest.config.ts")
}

func vitestEnabled(projectDir string) bool {
	ok, _ := detect.HasDependency(projectDir, "vitest")
	return ok || detect.FileExists(projectDir, "vitest.config.js", "vitest.config.ts")
}

func mochaEnabled(projectDir string) bool {
	ok, _ := detect.HasDependency(projectDir, "mocha")
	return ok || detect.FileExists(projectDir, ".mocharc.js", ".mocharc.json")
}

func bunTestEnabled(projectDir string) bool {
	return detect.Which("bun") != "" && detect.FileExists(projectDir, "package.json")
}

func nodeTestEnabled(projectDir string) bool {
	return detect.Which("node") != "" && detect.FileExists(projectDir, "package.json")
}

func pythonInterpreterAvailable() bool {
	return detect.Which("python") != "" || detect.Which("python3") != ""
}

func pytestEnabled(projectDir string) bool {
	if !pythonInterpreterAvailable() {
		return false
	}
	ok, _ := detect.HasPythonDependency(projectDir, "pytest")
	return ok || detect.FileExists(projectDir, "pytest.ini", "tox.ini")
}

func pythonUnitTestEnabled(projectDir string) bool {
	if !pythonInterpreterAvailable() {
		return false
	}
	if !dirExists(projectDir, "tests", "test") {
		return hasMatchingFile(projectDir, func(_ string, name string) bool {
			matched, _ := filepath.Match("test*.py", name)
			if matched {
				return true
			}
			matched, _ = filepath.Match("*_test.py", name)
			return matched
		})
	}
	return true
}

func cargoTestEnabled(projectDir string) bool {
	return detect.Which("cargo") != "" && detect.FileExists(projectDir, "Cargo.toml")
}

func rspecEnabled(projectDir string) bool {
	ok, _ := detect.HasGemDependency(projectDir, "rspec")
	return ok || detect.FileExists(projectDir, ".rspec")
}

func phpunitEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "phpunit.xml", "phpunit.xml.dist", "vendor/bin/phpunit")
}

func mavenTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "pom.xml") &&
		(detect.Which("mvn") != "" || detect.FileExists(projectDir, "mvnw", "mvnw.cmd"))
}

func gradleTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "build.gradle", "build.gradle.kts") &&
		(detect.Which("gradle") != "" || detect.FileExists(projectDir, "gradlew", "gradlew.bat"))
}

func sbtTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "build.sbt") && detect.Which("sbt") != ""
}

func millTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "build.sc") && detect.Which("mill") != ""
}

func dotnetTestEnabled(projectDir string) bool {
	return detect.Which("dotnet") != "" && hasAnyExtension(projectDir, ".sln", ".csproj", ".fsproj", ".vbproj")
}

func cTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "CMakeLists.txt") &&
		(detect.Which("ctest") != "" || detect.Which("cmake") != "")
}

func swiftTestEnabled(projectDir string) bool {
	return detect.Which("swift") != "" && detect.FileExists(projectDir, "Package.swift")
}

func zigTestEnabled(projectDir string) bool {
	return detect.Which("zig") != "" && detect.FileExists(projectDir, "build.zig")
}

func dartTestEnabled(projectDir string) bool {
	return (detect.Which("dart") != "" || detect.Which("flutter") != "") &&
		detect.FileExists(projectDir, "pubspec.yaml")
}

func exUnitEnabled(projectDir string) bool {
	return detect.Which("mix") != "" && detect.FileExists(projectDir, "mix.exs")
}

func rebar3EUnitEnabled(projectDir string) bool {
	return detect.Which("rebar3") != "" && detect.FileExists(projectDir, "rebar.config")
}

func stackTestEnabled(projectDir string) bool {
	return detect.FileExists(projectDir, "stack.yaml") && detect.Which("stack") != ""
}

func cabalTestEnabled(projectDir string) bool {
	return (detect.FileExists(projectDir, "cabal.project") || hasAnyExtension(projectDir, ".cabal")) &&
		detect.Which("cabal") != ""
}

func FallbackFrameworkForLanguage(language string) *TestFrameworkDefinition {
	language = CanonicalFrameworkLanguage(language)
	var best *TestFrameworkDefinition
	for _, def := range BuiltinFrameworks {
		if CanonicalFrameworkLanguage(def.Language) != language {
			continue
		}
		if best == nil || def.Priority > best.Priority {
			best = def
		}
	}
	return best
}

func CanonicalFrameworkLanguage(language string) string {
	switch strings.TrimSpace(strings.ToLower(language)) {
	case "js":
		return "javascript"
	case "ts", "typescript", "tsx", "vue", "svelte":
		return "javascript"
	case "golang":
		return "go"
	case "c#":
		return "csharp"
	case "kotlin":
		return "java"
	case "c":
		return "cpp"
	default:
		return strings.TrimSpace(strings.ToLower(language))
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
			case ".git", "node_modules", "vendor", "dist", "build", "target", "bin", "obj":
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

func dirExists(root string, dirs ...string) bool {
	for _, dir := range dirs {
		info, err := os.Stat(filepath.Join(root, dir))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func hasMatchingFile(root string, match func(relPath, name string) bool) bool {
	found := false
	_ = filepath.WalkDir(root, func(filePath string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "target", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, filePath)
		if relErr != nil {
			return nil
		}
		if match(filepath.ToSlash(rel), d.Name()) {
			found = true
		}
		return nil
	})
	return found
}
