package test

import (
	"github.com/adalundhe/sylk/core/detect"
)

var BuiltinFrameworks = []*TestFrameworkDefinition{
	goTestFramework(),
	jestFramework(),
	vitestFramework(),
	mochaFramework(),
	pytestFramework(),
	cargoTestFramework(),
	rspecFramework(),
	phpunitFramework(),
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
		Priority:         100,
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
		Priority:         90,
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

func pytestFramework() *TestFrameworkDefinition {
	return &TestFrameworkDefinition{
		ID:               FrameworkPytest,
		Name:             "pytest",
		Language:         "python",
		RunCommand:       "pytest",
		RunFileCommand:   "pytest {file}",
		RunSingleCommand: "pytest {file}::{test}",
		CoverageCommand:  "pytest --cov",
		WatchCommand:     "ptw",
		TestFilePatterns: []string{"test_*.py", "*_test.py"},
		ConfigFiles:      []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini"},
		Priority:         100,
		Enabled:          pytestEnabled,
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

func pytestEnabled(projectDir string) bool {
	if detect.Which("pytest") == "" {
		return false
	}
	ok, _ := detect.HasPythonDependency(projectDir, "pytest")
	return ok || detect.FileExists(projectDir, "pytest.ini")
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
