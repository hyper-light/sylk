package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasDependency(t *testing.T) {
	tests := []struct {
		name        string
		pkgJSON     string
		pkg         string
		expected    bool
		expectError bool
	}{
		{
			name: "finds dependency in dependencies",
			pkgJSON: `{
				"dependencies": {
					"express": "^4.18.0",
					"lodash": "^4.17.21"
				}
			}`,
			pkg:      "express",
			expected: true,
		},
		{
			name: "finds dependency in devDependencies",
			pkgJSON: `{
				"devDependencies": {
					"jest": "^29.0.0",
					"typescript": "^5.0.0"
				}
			}`,
			pkg:      "jest",
			expected: true,
		},
		{
			name: "finds dependency when in both sections",
			pkgJSON: `{
				"dependencies": {
					"react": "^18.0.0"
				},
				"devDependencies": {
					"react-dom": "^18.0.0"
				}
			}`,
			pkg:      "react",
			expected: true,
		},
		{
			name: "returns false for missing dependency",
			pkgJSON: `{
				"dependencies": {
					"express": "^4.18.0"
				}
			}`,
			pkg:      "nonexistent",
			expected: false,
		},
		{
			name: "returns false for empty dependencies",
			pkgJSON: `{
				"dependencies": {},
				"devDependencies": {}
			}`,
			pkg:      "express",
			expected: false,
		},
		{
			name:        "handles malformed JSON gracefully",
			pkgJSON:     `{"dependencies": {invalid json`,
			pkg:         "express",
			expected:    false,
			expectError: true,
		},
		{
			name:     "handles empty package name",
			pkgJSON:  `{"dependencies": {"express": "^4.18.0"}}`,
			pkg:      "",
			expected: false,
		},
		{
			name: "handles package name with special characters",
			pkgJSON: `{
				"dependencies": {
					"@babel/core": "^7.0.0",
					"@types/node": "^18.0.0"
				}
			}`,
			pkg:      "@babel/core",
			expected: true,
		},
		{
			name: "handles scoped package not found",
			pkgJSON: `{
				"dependencies": {
					"@babel/core": "^7.0.0"
				}
			}`,
			pkg:      "@babel/preset-env",
			expected: false,
		},
		{
			name:     "handles minimal valid JSON",
			pkgJSON:  `{}`,
			pkg:      "express",
			expected: false,
		},
		{
			name: "handles package name with numbers",
			pkgJSON: `{
				"dependencies": {
					"es6-promise": "^4.0.0"
				}
			}`,
			pkg:      "es6-promise",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.pkgJSON != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(tt.pkgJSON), 0644)
				if err != nil {
					t.Fatalf("failed to write package.json: %v", err)
				}
			}

			found, err := HasDependency(tmpDir, tt.pkg)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if found != tt.expected {
				t.Errorf("HasDependency() = %v, want %v", found, tt.expected)
			}
		})
	}

	t.Run("handles missing package.json gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := HasDependency(tmpDir, "express")
		if err != nil {
			t.Errorf("expected no error for missing file, got: %v", err)
		}
		if found {
			t.Error("expected false for missing package.json")
		}
	})
}

func TestHasGemDependency(t *testing.T) {
	tests := []struct {
		name        string
		gemfile     string
		gem         string
		expected    bool
		expectError bool
	}{
		{
			name:     "finds gem with single quotes",
			gemfile:  "gem 'rails', '~> 7.0'\ngem 'puma', '~> 5.0'",
			gem:      "rails",
			expected: true,
		},
		{
			name:     "finds gem with double quotes",
			gemfile:  `gem "rails", "~> 7.0"`,
			gem:      "rails",
			expected: true,
		},
		{
			name:     "finds gem with parentheses syntax",
			gemfile:  "gem('rails', '~> 7.0')",
			gem:      "rails",
			expected: true,
		},
		{
			name:     "returns false for missing gem",
			gemfile:  "gem 'rails', '~> 7.0'",
			gem:      "sinatra",
			expected: false,
		},
		{
			name:     "handles empty Gemfile",
			gemfile:  "",
			gem:      "rails",
			expected: false,
		},
		{
			name:     "ignores comments",
			gemfile:  "# gem 'rails', '~> 7.0'\ngem 'puma'",
			gem:      "rails",
			expected: false,
		},
		{
			name:     "handles gem with version constraints",
			gemfile:  "gem 'nokogiri', '>= 1.10.0', '< 2.0'",
			gem:      "nokogiri",
			expected: true,
		},
		{
			name:     "handles gem with group",
			gemfile:  "gem 'rspec', group: :test",
			gem:      "rspec",
			expected: true,
		},
		{
			name:     "handles gem with require false",
			gemfile:  "gem 'bootsnap', require: false",
			gem:      "bootsnap",
			expected: true,
		},
		{
			name:     "does not match partial gem names",
			gemfile:  "gem 'rails'",
			gem:      "rail",
			expected: false,
		},
		{
			name:     "handles empty gem name",
			gemfile:  "gem 'rails'",
			gem:      "",
			expected: false,
		},
		{
			name:     "handles gem with hyphen in name",
			gemfile:  "gem 'ruby-oci8'",
			gem:      "ruby-oci8",
			expected: true,
		},
		{
			name:     "handles gem with underscore in name",
			gemfile:  "gem 'active_model_serializers'",
			gem:      "active_model_serializers",
			expected: true,
		},
		{
			name:     "handles multiple gems on separate lines",
			gemfile:  "gem 'rails'\ngem 'puma'\ngem 'pg'",
			gem:      "puma",
			expected: true,
		},
		{
			name: "handles Gemfile with source",
			gemfile: `source 'https://rubygems.org'
gem 'rails'`,
			gem:      "rails",
			expected: true,
		},
		{
			name: "handles Gemfile with group block",
			gemfile: `group :development, :test do
  gem 'rspec-rails'
end`,
			gem:      "rspec-rails",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.gemfile != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "Gemfile"), []byte(tt.gemfile), 0644)
				if err != nil {
					t.Fatalf("failed to write Gemfile: %v", err)
				}
			}

			found, err := HasGemDependency(tmpDir, tt.gem)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if found != tt.expected {
				t.Errorf("HasGemDependency() = %v, want %v", found, tt.expected)
			}
		})
	}

	t.Run("handles missing Gemfile gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := HasGemDependency(tmpDir, "rails")
		if err != nil {
			t.Errorf("expected no error for missing file, got: %v", err)
		}
		if found {
			t.Error("expected false for missing Gemfile")
		}
	})
}

func TestHasPythonDependency(t *testing.T) {
	tests := []struct {
		name            string
		requirementsTxt string
		pyprojectToml   string
		pkg             string
		expected        bool
		expectError     bool
	}{
		{
			name:            "finds package in requirements.txt",
			requirementsTxt: "flask==2.0.0\nrequests>=2.28.0",
			pkg:             "flask",
			expected:        true,
		},
		{
			name:            "finds package with version specifier ==",
			requirementsTxt: "django==4.2.0",
			pkg:             "django",
			expected:        true,
		},
		{
			name:            "finds package with version specifier >=",
			requirementsTxt: "numpy>=1.24.0",
			pkg:             "numpy",
			expected:        true,
		},
		{
			name:            "finds package with version specifier <=",
			requirementsTxt: "pandas<=2.0.0",
			pkg:             "pandas",
			expected:        true,
		},
		{
			name:            "finds package with version specifier ~=",
			requirementsTxt: "scipy~=1.10.0",
			pkg:             "scipy",
			expected:        true,
		},
		{
			name:            "finds package with version specifier !=",
			requirementsTxt: "urllib3!=1.25.0",
			pkg:             "urllib3",
			expected:        true,
		},
		{
			name:            "finds package with extras",
			requirementsTxt: "uvicorn[standard]>=0.20.0",
			pkg:             "uvicorn",
			expected:        true,
		},
		{
			name:            "returns false for missing package",
			requirementsTxt: "flask==2.0.0",
			pkg:             "django",
			expected:        false,
		},
		{
			name:            "handles empty requirements.txt",
			requirementsTxt: "",
			pkg:             "flask",
			expected:        false,
		},
		{
			name:            "ignores comments",
			requirementsTxt: "# flask==2.0.0\ndjango==4.0.0",
			pkg:             "flask",
			expected:        false,
		},
		{
			name:            "ignores -r includes",
			requirementsTxt: "-r base.txt\nflask==2.0.0",
			pkg:             "flask",
			expected:        true,
		},
		{
			name:            "ignores -e editable installs",
			requirementsTxt: "-e git+https://github.com/user/repo.git#egg=mypackage\nflask",
			pkg:             "flask",
			expected:        true,
		},
		{
			name:            "normalizes hyphens to underscores",
			requirementsTxt: "scikit-learn>=1.0.0",
			pkg:             "scikit_learn",
			expected:        true,
		},
		{
			name:            "normalizes underscores to hyphens (reverse lookup)",
			requirementsTxt: "scikit_learn>=1.0.0",
			pkg:             "scikit-learn",
			expected:        true,
		},
		{
			name:            "normalizes dots to underscores",
			requirementsTxt: "zope.interface>=5.0.0",
			pkg:             "zope_interface",
			expected:        true,
		},
		{
			name:            "case insensitive matching",
			requirementsTxt: "Flask==2.0.0",
			pkg:             "flask",
			expected:        true,
		},
		{
			name:            "handles empty package name",
			requirementsTxt: "flask==2.0.0",
			pkg:             "",
			expected:        false,
		},
		{
			name: "finds package in pyproject.toml dependencies section",
			pyprojectToml: `[project]
name = "myproject"
dependencies = [
    "flask>=2.0.0",
    "requests"
]`,
			pkg:      "flask",
			expected: true,
		},
		{
			name: "finds package in pyproject.toml with poetry style",
			pyprojectToml: `[tool.poetry.dependencies]
python = "^3.9"
flask = "^2.0.0"`,
			pkg:      "flask",
			expected: true,
		},
		{
			name: "finds package in pyproject.toml optional-dependencies",
			pyprojectToml: `[project.optional-dependencies]
dev = ["pytest", "black"]`,
			pkg:      "pytest",
			expected: true,
		},
		{
			name:            "prefers requirements.txt over pyproject.toml when package found",
			requirementsTxt: "flask==2.0.0",
			pyprojectToml: `[tool.poetry.dependencies]
django = "^4.0.0"`,
			pkg:      "flask",
			expected: true,
		},
		{
			name:            "falls back to pyproject.toml when not in requirements.txt",
			requirementsTxt: "requests==2.28.0",
			pyprojectToml: `[tool.poetry.dependencies]
flask = "^2.0.0"`,
			pkg:      "flask",
			expected: true,
		},
		{
			name: "handles pyproject.toml with inline table dependencies",
			pyprojectToml: `[tool.poetry.dependencies]
flask = {version = "^2.0.0", optional = true}`,
			pkg:      "flask",
			expected: true,
		},
		{
			name:            "handles package with @ direct reference",
			requirementsTxt: "mypackage @ https://example.com/mypackage.tar.gz",
			pkg:             "mypackage",
			expected:        true,
		},
		{
			name:            "handles package with environment markers",
			requirementsTxt: `pywin32>=1.0 ; sys_platform == "win32"`,
			pkg:             "pywin32",
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			if tt.requirementsTxt != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "requirements.txt"), []byte(tt.requirementsTxt), 0644)
				if err != nil {
					t.Fatalf("failed to write requirements.txt: %v", err)
				}
			}

			if tt.pyprojectToml != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "pyproject.toml"), []byte(tt.pyprojectToml), 0644)
				if err != nil {
					t.Fatalf("failed to write pyproject.toml: %v", err)
				}
			}

			found, err := HasPythonDependency(tmpDir, tt.pkg)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if found != tt.expected {
				t.Errorf("HasPythonDependency() = %v, want %v", found, tt.expected)
			}
		})
	}

	t.Run("handles missing manifest files gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := HasPythonDependency(tmpDir, "flask")
		if err != nil {
			t.Errorf("expected no error for missing files, got: %v", err)
		}
		if found {
			t.Error("expected false for missing manifest files")
		}
	})
}

func TestHasPythonDependency_Normalization(t *testing.T) {
	tests := []struct {
		name    string
		content string
		queries []string
	}{
		{
			name:    "hyphen variations",
			content: "scikit-learn>=1.0.0",
			queries: []string{"scikit-learn", "scikit_learn", "Scikit-Learn", "SCIKIT_LEARN"},
		},
		{
			name:    "underscore variations",
			content: "my_package>=1.0.0",
			queries: []string{"my_package", "my-package", "My_Package", "MY-PACKAGE"},
		},
		{
			name:    "dot variations",
			content: "zope.interface>=5.0.0",
			queries: []string{"zope.interface", "zope_interface", "zope-interface"},
		},
		{
			name:    "mixed variations",
			content: "Foo-Bar_Baz.qux>=1.0.0",
			queries: []string{"foo-bar_baz.qux", "foo_bar_baz_qux", "FOO_BAR_BAZ_QUX"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			err := os.WriteFile(filepath.Join(tmpDir, "requirements.txt"), []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("failed to write requirements.txt: %v", err)
			}

			for _, query := range tt.queries {
				found, err := HasPythonDependency(tmpDir, query)
				if err != nil {
					t.Errorf("unexpected error for query %q: %v", query, err)
				}
				if !found {
					t.Errorf("HasPythonDependency(%q) = false, want true", query)
				}
			}
		})
	}
}

func TestHasCargoDependency(t *testing.T) {
	tests := []struct {
		name        string
		cargoToml   string
		crate       string
		expected    bool
		expectError bool
	}{
		{
			name: "finds crate in dependencies",
			cargoToml: `[package]
name = "myproject"

[dependencies]
serde = "1.0"
tokio = "1.0"`,
			crate:    "serde",
			expected: true,
		},
		{
			name: "finds crate in dev-dependencies",
			cargoToml: `[package]
name = "myproject"

[dev-dependencies]
criterion = "0.4"`,
			crate:    "criterion",
			expected: true,
		},
		{
			name: "finds crate in build-dependencies",
			cargoToml: `[package]
name = "myproject"

[build-dependencies]
cc = "1.0"`,
			crate:    "cc",
			expected: true,
		},
		{
			name: "finds crate with detailed section syntax",
			cargoToml: `[package]
name = "myproject"

[dependencies.serde]
version = "1.0"
features = ["derive"]`,
			crate:    "serde",
			expected: true,
		},
		{
			name: "finds crate with inline table syntax",
			cargoToml: `[dependencies]
serde = { version = "1.0", features = ["derive"] }`,
			crate:    "serde",
			expected: true,
		},
		{
			name: "returns false for missing crate with no dependencies section",
			cargoToml: `[package]
name = "myproject"
version = "1.0"`,
			crate:    "tokio",
			expected: false,
		},
		{
			name: "normalizes hyphens to underscores",
			cargoToml: `[dependencies]
async-std = "1.0"`,
			crate:    "async_std",
			expected: true,
		},
		{
			name: "normalizes underscores to hyphens (reverse lookup)",
			cargoToml: `[dependencies]
async_std = "1.0"`,
			crate:    "async-std",
			expected: true,
		},
		{
			name: "case insensitive matching",
			cargoToml: `[dependencies]
Serde = "1.0"`,
			crate:    "serde",
			expected: true,
		},
		{
			name: "handles empty crate name with no dependencies section",
			cargoToml: `[package]
name = "myproject"`,
			crate:    "",
			expected: false,
		},
		{
			name: "handles crate with path dependency",
			cargoToml: `[dependencies]
mylib = { path = "../mylib" }`,
			crate:    "mylib",
			expected: true,
		},
		{
			name: "handles crate with git dependency",
			cargoToml: `[dependencies]
mylib = { git = "https://github.com/user/mylib" }`,
			crate:    "mylib",
			expected: true,
		},
		{
			name: "handles detailed dev-dependencies section",
			cargoToml: `[dev-dependencies.pretty_assertions]
version = "1.0"`,
			crate:    "pretty_assertions",
			expected: true,
		},
		{
			name: "handles detailed build-dependencies section",
			cargoToml: `[build-dependencies.cc]
version = "1.0"`,
			crate:    "cc",
			expected: true,
		},
		{
			name: "does not match crate in non-dependency section",
			cargoToml: `[package]
name = "serde"
version = "1.0"`,
			crate:    "serde",
			expected: false,
		},
		{
			name: "handles workspace dependencies",
			cargoToml: `[workspace.dependencies]
serde = "1.0"`,
			crate:    "serde",
			expected: true,
		},
		{
			name: "handles target-specific dependencies",
			cargoToml: `[target.'cfg(windows)'.dependencies]
winapi = "0.3"`,
			crate:    "winapi",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.cargoToml != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "Cargo.toml"), []byte(tt.cargoToml), 0644)
				if err != nil {
					t.Fatalf("failed to write Cargo.toml: %v", err)
				}
			}

			found, err := HasCargoDependency(tmpDir, tt.crate)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if found != tt.expected {
				t.Errorf("HasCargoDependency() = %v, want %v", found, tt.expected)
			}
		})
	}

	t.Run("handles missing Cargo.toml gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := HasCargoDependency(tmpDir, "serde")
		if err != nil {
			t.Errorf("expected no error for missing file, got: %v", err)
		}
		if found {
			t.Error("expected false for missing Cargo.toml")
		}
	})
}

func TestHasGoDependency(t *testing.T) {
	tests := []struct {
		name        string
		goMod       string
		pkg         string
		expected    bool
		expectError bool
	}{
		{
			name: "finds package in require block",
			goMod: `module example.com/myproject

go 1.21

require (
	github.com/gin-gonic/gin v1.9.0
	github.com/stretchr/testify v1.8.0
)`,
			pkg:      "github.com/gin-gonic/gin",
			expected: true,
		},
		{
			name: "finds package with single require statement",
			goMod: `module example.com/myproject

go 1.21

require github.com/gin-gonic/gin v1.9.0`,
			pkg:      "github.com/gin-gonic/gin",
			expected: true,
		},
		{
			name: "returns false for missing package",
			goMod: `module example.com/myproject

require github.com/gin-gonic/gin v1.9.0`,
			pkg:      "github.com/labstack/echo",
			expected: false,
		},
		{
			name: "handles empty go.mod",
			goMod: `module example.com/myproject

go 1.21`,
			pkg:      "github.com/gin-gonic/gin",
			expected: false,
		},
		{
			name: "ignores comments",
			goMod: `module example.com/myproject

// require github.com/gin-gonic/gin v1.9.0
require github.com/labstack/echo v4.0.0`,
			pkg:      "github.com/gin-gonic/gin",
			expected: false,
		},
		{
			name: "handles indirect dependencies",
			goMod: `module example.com/myproject

require (
	github.com/gin-gonic/gin v1.9.0
	golang.org/x/sys v0.0.0 // indirect
)`,
			pkg:      "golang.org/x/sys",
			expected: true,
		},
		{
			name: "matches sub-package paths",
			goMod: `module example.com/myproject

require github.com/aws/aws-sdk-go v1.44.0`,
			pkg:      "github.com/aws/aws-sdk-go/service/s3",
			expected: true,
		},
		{
			name: "matches parent package when querying sub-package",
			goMod: `module example.com/myproject

require github.com/aws/aws-sdk-go/service/s3 v1.44.0`,
			pkg:      "github.com/aws/aws-sdk-go",
			expected: true,
		},
		{
			name: "handles empty package name",
			goMod: `module example.com/myproject

require github.com/gin-gonic/gin v1.9.0`,
			pkg:      "",
			expected: false,
		},
		{
			name: "handles replace directives (still finds original)",
			goMod: `module example.com/myproject

require github.com/gin-gonic/gin v1.9.0

replace github.com/gin-gonic/gin => ../local-gin`,
			pkg:      "github.com/gin-gonic/gin",
			expected: true,
		},
		{
			name: "handles multiple require blocks",
			goMod: `module example.com/myproject

require (
	github.com/gin-gonic/gin v1.9.0
)

require (
	github.com/labstack/echo v4.0.0
)`,
			pkg:      "github.com/labstack/echo",
			expected: true,
		},
		{
			name: "handles pseudo-versions",
			goMod: `module example.com/myproject

require github.com/example/pkg v0.0.0-20230101120000-abcdef123456`,
			pkg:      "github.com/example/pkg",
			expected: true,
		},
		{
			name: "handles require with +incompatible",
			goMod: `module example.com/myproject

require github.com/example/v2 v2.0.0+incompatible`,
			pkg:      "github.com/example/v2",
			expected: true,
		},
		{
			name: "does not match partial module paths",
			goMod: `module example.com/myproject

require github.com/gin-gonic/gin v1.9.0`,
			pkg:      "github.com/gin-gonic/ginx",
			expected: false,
		},
		{
			name: "handles retract directive (unrelated to dependency)",
			goMod: `module example.com/myproject

go 1.21

require github.com/gin-gonic/gin v1.9.0

retract v1.0.0`,
			pkg:      "github.com/gin-gonic/gin",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.goMod != "" {
				err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(tt.goMod), 0644)
				if err != nil {
					t.Fatalf("failed to write go.mod: %v", err)
				}
			}

			found, err := HasGoDependency(tmpDir, tt.pkg)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if found != tt.expected {
				t.Errorf("HasGoDependency() = %v, want %v", found, tt.expected)
			}
		})
	}

	t.Run("handles missing go.mod gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := HasGoDependency(tmpDir, "github.com/gin-gonic/gin")
		if err != nil {
			t.Errorf("expected no error for missing file, got: %v", err)
		}
		if found {
			t.Error("expected false for missing go.mod")
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("hasKeyInMaps", func(t *testing.T) {
		map1 := map[string]string{"a": "1", "b": "2"}
		map2 := map[string]string{"c": "3", "d": "4"}

		if !hasKeyInMaps("a", map1, map2) {
			t.Error("hasKeyInMaps should find 'a' in map1")
		}
		if !hasKeyInMaps("c", map1, map2) {
			t.Error("hasKeyInMaps should find 'c' in map2")
		}
		if hasKeyInMaps("x", map1, map2) {
			t.Error("hasKeyInMaps should not find 'x'")
		}
		if hasKeyInMaps("a") {
			t.Error("hasKeyInMaps with no maps should return false")
		}
	})

	t.Run("normalizePythonPackageName", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"Flask", "flask"},
			{"scikit-learn", "scikit_learn"},
			{"zope.interface", "zope_interface"},
			{"My-Package.Name", "my_package_name"},
			{"", ""},
		}
		for _, tt := range tests {
			if got := normalizePythonPackageName(tt.input); got != tt.expected {
				t.Errorf("normalizePythonPackageName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("normalizeCrateName", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"serde", "serde"},
			{"async-std", "async_std"},
			{"Tokio", "tokio"},
			{"My-Crate", "my_crate"},
			{"", ""},
		}
		for _, tt := range tests {
			if got := normalizeCrateName(tt.input); got != tt.expected {
				t.Errorf("normalizeCrateName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("extractPythonPackageName", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"flask==2.0.0", "flask"},
			{"requests>=2.28.0", "requests"},
			{"numpy", "numpy"},
			{"uvicorn[standard]>=0.20.0", "uvicorn"},
			{"package @ https://example.com/pkg.tar.gz", "package"},
			{"package ; sys_platform == 'win32'", "package"},
			{"pkg~=1.0", "pkg"},
			{"pkg!=1.0", "pkg"},
			{"pkg<2.0", "pkg"},
			{"pkg>1.0", "pkg"},
			{"pkg<=2.0", "pkg"},
		}
		for _, tt := range tests {
			if got := extractPythonPackageName(tt.input); got != tt.expected {
				t.Errorf("extractPythonPackageName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("isGemDeclaration", func(t *testing.T) {
		tests := []struct {
			input    string
			expected bool
		}{
			{"gem 'rails'", true},
			{"gem('rails')", true},
			{"  gem 'rails'", false},
			{"source 'https://rubygems.org'", false},
			{"", false},
		}
		for _, tt := range tests {
			if got := isGemDeclaration(tt.input); got != tt.expected {
				t.Errorf("isGemDeclaration(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		}
	})

	t.Run("containsGemName", func(t *testing.T) {
		tests := []struct {
			line     string
			gem      string
			expected bool
		}{
			{"gem 'rails'", "rails", true},
			{`gem "rails"`, "rails", true},
			{"gem 'rails'", "rail", false},
			{"gem 'rails'", "", false},
		}
		for _, tt := range tests {
			if got := containsGemName(tt.line, tt.gem); got != tt.expected {
				t.Errorf("containsGemName(%q, %q) = %v, want %v", tt.line, tt.gem, got, tt.expected)
			}
		}
	})

	t.Run("matchGoModule", func(t *testing.T) {
		tests := []struct {
			modPath  string
			pkg      string
			expected bool
		}{
			{"github.com/gin-gonic/gin", "github.com/gin-gonic/gin", true},
			{"github.com/aws/aws-sdk-go", "github.com/aws/aws-sdk-go/service/s3", true},
			{"github.com/aws/aws-sdk-go/service/s3", "github.com/aws/aws-sdk-go", true},
			{"github.com/gin-gonic/gin", "github.com/gin-gonic/ginx", false},
			{"github.com/gin-gonic/gin", "github.com/labstack/echo", false},
		}
		for _, tt := range tests {
			if got := matchGoModule(tt.modPath, tt.pkg); got != tt.expected {
				t.Errorf("matchGoModule(%q, %q) = %v, want %v", tt.modPath, tt.pkg, got, tt.expected)
			}
		}
	})
}

func TestReadFileIfExists(t *testing.T) {
	t.Run("returns data for existing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		content := []byte("test content")
		if err := os.WriteFile(testFile, content, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		data, err := readFileIfExists(testFile)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("got %q, want %q", string(data), string(content))
		}
	})

	t.Run("returns nil for non-existent file", func(t *testing.T) {
		tmpDir := t.TempDir()
		data, err := readFileIfExists(filepath.Join(tmpDir, "nonexistent.txt"))
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if data != nil {
			t.Errorf("expected nil data, got: %v", data)
		}
	})
}

func TestScanFileForMatch(t *testing.T) {
	t.Run("returns true when matcher finds match", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("line1\ntarget\nline3"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		found, err := scanFileForMatch(testFile, func(line string) bool {
			return line == "target"
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find match")
		}
	})

	t.Run("returns false when no match", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("line1\nline2\nline3"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		found, err := scanFileForMatch(testFile, func(line string) bool {
			return line == "notfound"
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected no match")
		}
	})

	t.Run("returns false for non-existent file", func(t *testing.T) {
		tmpDir := t.TempDir()
		found, err := scanFileForMatch(filepath.Join(tmpDir, "nonexistent.txt"), func(line string) bool {
			return true
		})
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if found {
			t.Error("expected false for non-existent file")
		}
	})

	t.Run("skips empty lines and comments", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("\n# comment\n  \ntarget"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		linesChecked := 0
		_, _ = scanFileForMatch(testFile, func(line string) bool {
			linesChecked++
			return false
		})
		if linesChecked != 1 {
			t.Errorf("expected 1 line checked (skipping empty and comments), got %d", linesChecked)
		}
	})
}

func TestEdgeCases(t *testing.T) {
	t.Run("handles very long lines in requirements.txt", func(t *testing.T) {
		tmpDir := t.TempDir()
		longComment := "# " + string(make([]byte, 10000))
		content := longComment + "\nflask==2.0.0"
		if err := os.WriteFile(filepath.Join(tmpDir, "requirements.txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := HasPythonDependency(tmpDir, "flask")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find flask")
		}
	})

	t.Run("handles Windows line endings in package.json", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := "{\r\n\"dependencies\": {\r\n\"express\": \"^4.18.0\"\r\n}\r\n}"
		if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := HasDependency(tmpDir, "express")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find express")
		}
	})

	t.Run("handles Unicode package names", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := `{"dependencies": {"日本語パッケージ": "1.0.0"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := HasDependency(tmpDir, "日本語パッケージ")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find Unicode package")
		}
	})

	t.Run("handles whitespace-only package.json", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("   \n\t  "), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := HasDependency(tmpDir, "express")
		if err == nil {
			t.Log("whitespace-only JSON may or may not error depending on parser")
		}
		if found {
			t.Error("should not find package in whitespace-only file")
		}
	})

	t.Run("handles root path with trailing slash", func(t *testing.T) {
		tmpDir := t.TempDir() + "/"
		content := `{"dependencies": {"express": "^4.18.0"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := HasDependency(tmpDir, "express")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find express with trailing slash in path")
		}
	})
}
