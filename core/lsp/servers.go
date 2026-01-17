package lsp

import (
	"github.com/adalundhe/sylk/core/detect"
)

var BuiltinServers = []*LanguageServerDefinition{
	goplsServer(),
	typescriptServer(),
	eslintServer(),
	biomeServer(),
	pyrightServer(),
	ruffLSPServer(),
	rustAnalyzerServer(),
	clangdServer(),
	solargraphServer(),
	jdtlsServer(),
	yamlLanguageServer(),
	terraformLSServer(),
}

func goplsServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerGopls,
		Name:        "gopls",
		Command:     "gopls",
		Args:        []string{"serve"},
		Extensions:  []string{".go"},
		LanguageIDs: []string{"go"},
		RootMarkers: []string{"go.mod", "go.sum"},
		Enabled:     binaryAvailable("gopls"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceGo,
			Package: "golang.org/x/tools/gopls@latest",
			Binary:  "gopls",
		},
	}
}

func typescriptServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerTypeScript,
		Name:        "TypeScript Language Server",
		Command:     "typescript-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
		RootMarkers: []string{"tsconfig.json", "jsconfig.json", "package.json"},
		Enabled:     binaryAvailable("typescript-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "typescript-language-server",
			Binary:  "typescript-language-server",
		},
	}
}

func eslintServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "eslint-lsp",
		Name:        "ESLint Language Server",
		Command:     "vscode-eslint-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
		RootMarkers: []string{".eslintrc", ".eslintrc.js", ".eslintrc.json", ".eslintrc.yml", "eslint.config.js"},
		Enabled:     eslintEnabled,
	}
}

func biomeServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "biome-lsp",
		Name:        "Biome Language Server",
		Command:     "biome",
		Args:        []string{"lsp-proxy"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".json", ".jsonc"},
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact", "json", "jsonc"},
		RootMarkers: []string{"biome.json", "biome.jsonc"},
		Enabled:     biomeEnabled,
	}
}

func pyrightServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerPyright,
		Name:        "Pyright",
		Command:     "pyright-langserver",
		Args:        []string{"--stdio"},
		Extensions:  []string{".py", ".pyi"},
		LanguageIDs: []string{"python"},
		RootMarkers: []string{"pyproject.toml", "pyrightconfig.json", "setup.py", "requirements.txt"},
		Enabled:     binaryAvailable("pyright-langserver"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "pyright",
			Binary:  "pyright-langserver",
		},
	}
}

func ruffLSPServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "ruff-lsp",
		Name:        "Ruff LSP",
		Command:     "ruff",
		Args:        []string{"server"},
		Extensions:  []string{".py", ".pyi"},
		LanguageIDs: []string{"python"},
		RootMarkers: []string{"ruff.toml", ".ruff.toml", "pyproject.toml"},
		Enabled:     ruffEnabled,
	}
}

func rustAnalyzerServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerRustAna,
		Name:        "rust-analyzer",
		Command:     "rust-analyzer",
		Args:        []string{},
		Extensions:  []string{".rs"},
		LanguageIDs: []string{"rust"},
		RootMarkers: []string{"Cargo.toml", "Cargo.lock"},
		Enabled:     binaryAvailable("rust-analyzer"),
	}
}

func clangdServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerClangd,
		Name:        "clangd",
		Command:     "clangd",
		Args:        []string{"--background-index"},
		Extensions:  []string{".c", ".h", ".cpp", ".hpp", ".cc", ".cxx", ".hxx"},
		LanguageIDs: []string{"c", "cpp"},
		RootMarkers: []string{"compile_commands.json", ".clangd", "CMakeLists.txt", "Makefile"},
		Enabled:     binaryAvailable("clangd"),
	}
}

func solargraphServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "solargraph",
		Name:        "Solargraph",
		Command:     "solargraph",
		Args:        []string{"stdio"},
		Extensions:  []string{".rb", ".rake"},
		LanguageIDs: []string{"ruby"},
		RootMarkers: []string{"Gemfile", ".solargraph.yml"},
		Enabled:     solargraphEnabled,
	}
}

func jdtlsServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "jdtls",
		Name:        "Eclipse JDT Language Server",
		Command:     "jdtls",
		Args:        []string{},
		Extensions:  []string{".java"},
		LanguageIDs: []string{"java"},
		RootMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".project"},
		Enabled:     binaryAvailable("jdtls"),
	}
}

func yamlLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "yaml-language-server",
		Name:        "YAML Language Server",
		Command:     "yaml-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".yaml", ".yml"},
		LanguageIDs: []string{"yaml"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("yaml-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "yaml-language-server",
			Binary:  "yaml-language-server",
		},
	}
}

func terraformLSServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          "terraform-ls",
		Name:        "Terraform Language Server",
		Command:     "terraform-ls",
		Args:        []string{"serve"},
		Extensions:  []string{".tf", ".tfvars"},
		LanguageIDs: []string{"terraform"},
		RootMarkers: []string{".terraform", "main.tf"},
		Enabled:     binaryAvailable("terraform-ls"),
	}
}

func binaryAvailable(name string) func() bool {
	return func() bool {
		return detect.Which(name) != ""
	}
}

func eslintEnabled() bool {
	return detect.Which("vscode-eslint-language-server") != ""
}

func biomeEnabled() bool {
	return detect.Which("biome") != ""
}

func ruffEnabled() bool {
	return detect.Which("ruff") != ""
}

func solargraphEnabled() bool {
	return detect.Which("solargraph") != ""
}
