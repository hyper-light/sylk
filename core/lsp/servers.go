package lsp

import (
	"os"
	"path/filepath"

	"github.com/adalundhe/sylk/core/detect"
)

// ---------------------------------------------------------------------------
// ServerID constants
// ---------------------------------------------------------------------------

const (
	ServerESLint        ServerID = "eslint-lsp"
	ServerBiome         ServerID = "biome-lsp"
	ServerRuff          ServerID = "ruff-lsp"
	ServerSolargraph    ServerID = "solargraph"
	ServerJDTLS         ServerID = "jdtls"
	ServerYAML          ServerID = "yaml-language-server"
	ServerTerraformLS   ServerID = "terraform-ls"
	ServerBash          ServerID = "bash-language-server"
	ServerVSCodeHTML    ServerID = "vscode-html-language-server"
	ServerVSCodeCSS     ServerID = "vscode-css-language-server"
	ServerVSCodeJSON    ServerID = "vscode-json-language-server"
	ServerVue           ServerID = "vue-language-server"
	ServerSvelte        ServerID = "svelte-language-server"
	ServerIntelephense  ServerID = "intelephense"
	ServerKotlinLS      ServerID = "kotlin-language-server"
	ServerTaplo         ServerID = "taplo"
	ServerElixirLS      ServerID = "elixir-ls"
	ServerHaskellLS     ServerID = "haskell-language-server"
	ServerMarksman      ServerID = "marksman"
	ServerMetals        ServerID = "metals"
	ServerSourceKitLSP  ServerID = "sourcekit-lsp"
)

// ---------------------------------------------------------------------------
// Managed binary resolution
// ---------------------------------------------------------------------------

// managedBinDir is the directory where Sylk installs LSP binaries.
// Derived from: ~/.sylk is the established data directory (see treesitter/grammar.go).
var managedBinDir = computeManagedBinDir()

func computeManagedBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "lsp", "bin")
}

// EnsureManagedBinOnPath prepends the managed binary directory to PATH
// so that detect.Which and exec.LookPath find installed LSP servers.
// Call once at application startup before any server selection occurs.
func EnsureManagedBinOnPath() {
	if managedBinDir == "" {
		return
	}
	path := os.Getenv("PATH")
	os.Setenv("PATH", managedBinDir+string(os.PathListSeparator)+path)
}

// isBinaryManaged checks if a binary exists in the managed bin directory.
func isBinaryManaged(name string) bool {
	if managedBinDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(managedBinDir, name))
	return err == nil && !info.IsDir()
}

// resolveCommand returns the absolute path for a server binary.
// Checks system PATH first, then the managed bin directory.
// Falls back to the bare name for exec.LookPath resolution.
func resolveCommand(name string) string {
	if p := detect.Which(name); p != "" {
		return p
	}
	if managedBinDir != "" {
		managed := filepath.Join(managedBinDir, name)
		if info, err := os.Stat(managed); err == nil && !info.IsDir() {
			return managed
		}
	}
	return name
}

// binaryAvailable returns an Enabled function that checks both system PATH
// and the managed bin directory.
func binaryAvailable(name string) func() bool {
	return func() bool {
		return detect.Which(name) != "" || isBinaryManaged(name)
	}
}

// ---------------------------------------------------------------------------
// BuiltinServers — all registered language server definitions
// ---------------------------------------------------------------------------

var BuiltinServers = []*LanguageServerDefinition{
	// Tier 1: primary servers for major languages
	goplsServer(),
	typescriptServer(),
	pyrightServer(),
	ruffLSPServer(),
	rustAnalyzerServer(),
	clangdServer(),

	// Tier 2: supplementary / secondary servers
	eslintServer(),
	biomeServer(),
	solargraphServer(),
	jdtlsServer(),
	yamlLanguageServer(),
	terraformLSServer(),

	// New servers
	bashLanguageServer(),
	vscodeHTMLServer(),
	vscodeCSSServer(),
	vscodeJSONServer(),
	vueLanguageServer(),
	svelteLanguageServer(),
	intelephenseServer(),
	luaLanguageServer(),
	kotlinLanguageServer(),
	zlsServer(),
	taploServer(),
	elixirLSServer(),
	haskellLanguageServer(),
	marksmanServer(),
	metalsServer(),
	sourcekitLSPServer(),
}

// ---------------------------------------------------------------------------
// Existing server definitions (with AutoDownload where applicable)
// ---------------------------------------------------------------------------

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
		InitializationOptions: map[string]interface{}{
			"settings": map[string]interface{}{
				"staticcheck": true,
				"analyses": map[string]interface{}{
					"shadow":       true,
					"unusedparams": true,
					"unusedwrite":  true,
					"nilness":      true,
				},
				"hints": map[string]interface{}{
					"compositeLiteralFields": true,
					"constantValues":         true,
					"parameterNames":         true,
				},
			},
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
		InitializationOptions: map[string]interface{}{
			"preferences": map[string]interface{}{
				"includeInlayParameterNameHints":      "all",
				"includeInlayFunctionParameterTypeHints": true,
				"includeInlayVariableTypeHints":        true,
			},
		},
	}
}

func eslintServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerESLint,
		Name:        "ESLint Language Server",
		Command:     "vscode-eslint-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
		RootMarkers: []string{".eslintrc", ".eslintrc.js", ".eslintrc.json", ".eslintrc.yml", "eslint.config.js"},
		Enabled:     binaryAvailable("vscode-eslint-language-server"),
		InitializationOptions: map[string]interface{}{
			"validate":  "on",
			"run":       "onType",
			"rulesCustomizations": []interface{}{},
		},
	}
}

func biomeServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerBiome,
		Name:        "Biome Language Server",
		Command:     "biome",
		Args:        []string{"lsp-proxy"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".json", ".jsonc"},
		LanguageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact", "json", "jsonc"},
		RootMarkers: []string{"biome.json", "biome.jsonc"},
		Enabled:     binaryAvailable("biome"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "@biomejs/biome",
			Binary:  "biome",
		},
		InitializationOptions: map[string]interface{}{
			"linter": map[string]interface{}{
				"enabled": true,
			},
		},
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
		InitializationOptions: map[string]interface{}{
			"python": map[string]interface{}{
				"analysis": map[string]interface{}{
					"typeCheckingMode": "standard",
					"diagnosticMode":   "workspace",
				},
			},
		},
	}
}

func ruffLSPServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerRuff,
		Name:        "Ruff LSP",
		Command:     "ruff",
		Args:        []string{"server"},
		Extensions:  []string{".py", ".pyi"},
		LanguageIDs: []string{"python"},
		RootMarkers: []string{"ruff.toml", ".ruff.toml", "pyproject.toml"},
		Enabled:     binaryAvailable("ruff"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "ruff",
			Owner:  "astral-sh",
			Repo:   "ruff",
		},
		InitializationOptions: map[string]interface{}{
			"settings": map[string]interface{}{
				"lint": map[string]interface{}{
					"enable": true,
				},
			},
		},
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
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "rust-analyzer",
			Owner:  "rust-lang",
			Repo:   "rust-analyzer",
		},
		InitializationOptions: map[string]interface{}{
			"check": map[string]interface{}{
				"command": "clippy",
			},
		},
	}
}

func clangdServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerClangd,
		Name:        "clangd",
		Command:     "clangd",
		Args:        []string{"--background-index", "--clang-tidy"},
		Extensions:  []string{".c", ".h", ".cpp", ".hpp", ".cc", ".cxx", ".hxx"},
		LanguageIDs: []string{"c", "cpp"},
		RootMarkers: []string{"compile_commands.json", ".clangd", "CMakeLists.txt", "Makefile"},
		Enabled:     binaryAvailable("clangd"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceSystem,
			Binary: "clangd",
		},
		InitializationOptions: map[string]interface{}{
			"clangdFileStatus": true,
		},
	}
}

func solargraphServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerSolargraph,
		Name:        "Solargraph",
		Command:     "solargraph",
		Args:        []string{"stdio"},
		Extensions:  []string{".rb", ".rake"},
		LanguageIDs: []string{"ruby"},
		RootMarkers: []string{"Gemfile", ".solargraph.yml"},
		Enabled:     binaryAvailable("solargraph"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceGem,
			Package: "solargraph",
			Binary:  "solargraph",
		},
		InitializationOptions: map[string]interface{}{
			"diagnostics": true,
		},
	}
}

func jdtlsServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerJDTLS,
		Name:        "Eclipse JDT Language Server",
		Command:     "jdtls",
		Args:        []string{},
		Extensions:  []string{".java"},
		LanguageIDs: []string{"java"},
		RootMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".project"},
		Enabled:     binaryAvailable("jdtls"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "jdtls",
			Owner:  "eclipse-jdtls",
			Repo:   "eclipse.jdt.ls",
		},
		InitializationOptions: map[string]interface{}{
			"settings": map[string]interface{}{
				"java": map[string]interface{}{
					"configuration": map[string]interface{}{
						"updateBuildConfiguration": "automatic",
					},
					"import": map[string]interface{}{
						"gradle": map[string]interface{}{"enabled": true},
						"maven":  map[string]interface{}{"enabled": true},
					},
				},
			},
		},
	}
}

func yamlLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerYAML,
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
		InitializationOptions: map[string]interface{}{
			"yaml": map[string]interface{}{
				"validate": true,
				"hover":    true,
				"schemaStore": map[string]interface{}{
					"enable": true,
				},
			},
		},
	}
}

func terraformLSServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerTerraformLS,
		Name:        "Terraform Language Server",
		Command:     "terraform-ls",
		Args:        []string{"serve"},
		Extensions:  []string{".tf", ".tfvars"},
		LanguageIDs: []string{"terraform"},
		RootMarkers: []string{".terraform", "main.tf"},
		Enabled:     binaryAvailable("terraform-ls"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "terraform-ls",
			Owner:  "hashicorp",
			Repo:   "terraform-ls",
		},
		InitializationOptions: map[string]interface{}{
			"experimentalFeatures": map[string]interface{}{
				"validateOnSave": true,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// New server definitions
// ---------------------------------------------------------------------------

func bashLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerBash,
		Name:        "Bash Language Server",
		Command:     "bash-language-server",
		Args:        []string{"start"},
		Extensions:  []string{".sh", ".bash"},
		LanguageIDs: []string{"bash", "shellscript"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("bash-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "bash-language-server",
			Binary:  "bash-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"bashIde": map[string]interface{}{
				"enableSourceErrorDiagnostics": true,
			},
		},
	}
}

func vscodeHTMLServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerVSCodeHTML,
		Name:        "HTML Language Server",
		Command:     "vscode-html-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".html"},
		LanguageIDs: []string{"html"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("vscode-html-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "vscode-langservers-extracted",
			Binary:  "vscode-html-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"provideFormatter": true,
		},
	}
}

func vscodeCSSServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerVSCodeCSS,
		Name:        "CSS Language Server",
		Command:     "vscode-css-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".css"},
		LanguageIDs: []string{"css"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("vscode-css-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "vscode-langservers-extracted",
			Binary:  "vscode-css-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"provideFormatter": true,
			"css":              map[string]interface{}{"validate": true},
			"less":             map[string]interface{}{"validate": true},
			"scss":             map[string]interface{}{"validate": true},
		},
	}
}

func vscodeJSONServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerVSCodeJSON,
		Name:        "JSON Language Server",
		Command:     "vscode-json-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".json"},
		LanguageIDs: []string{"json"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("vscode-json-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "vscode-langservers-extracted",
			Binary:  "vscode-json-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"provideFormatter": true,
			"json": map[string]interface{}{
				"validate": map[string]interface{}{"enable": true},
			},
		},
	}
}

func vueLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerVue,
		Name:        "Vue Language Server",
		Command:     "vue-language-server",
		Args:        []string{"--stdio"},
		Extensions:  []string{".vue"},
		LanguageIDs: []string{"vue"},
		RootMarkers: []string{"vue.config.js", "nuxt.config.ts", "nuxt.config.js"},
		Enabled:     binaryAvailable("vue-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "@vue/language-server",
			Binary:  "vue-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"vue": map[string]interface{}{
				"hybridMode": false,
			},
		},
	}
}

func svelteLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerSvelte,
		Name:        "Svelte Language Server",
		Command:     "svelteserver",
		Args:        []string{"--stdio"},
		Extensions:  []string{".svelte"},
		LanguageIDs: []string{"svelte"},
		RootMarkers: []string{"svelte.config.js", "svelte.config.ts"},
		Enabled:     binaryAvailable("svelteserver"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "svelte-language-server",
			Binary:  "svelteserver",
		},
		InitializationOptions: map[string]interface{}{
			"svelte": map[string]interface{}{
				"plugin": map[string]interface{}{
					"svelte": map[string]interface{}{
						"diagnostics": map[string]interface{}{"enable": true},
					},
				},
			},
		},
	}
}

func intelephenseServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerIntelephense,
		Name:        "Intelephense",
		Command:     "intelephense",
		Args:        []string{"--stdio"},
		Extensions:  []string{".php"},
		LanguageIDs: []string{"php"},
		RootMarkers: []string{"composer.json", "composer.lock"},
		Enabled:     binaryAvailable("intelephense"),
		AutoDownload: &AutoDownloadConfig{
			Source:  SourceNPM,
			Package: "intelephense",
			Binary:  "intelephense",
		},
		InitializationOptions: map[string]interface{}{
			"clearCache": false,
			"globalStoragePath": "",
			"licenceKey":        "",
		},
	}
}

func luaLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerLuaLS,
		Name:        "Lua Language Server",
		Command:     "lua-language-server",
		Args:        []string{},
		Extensions:  []string{".lua"},
		LanguageIDs: []string{"lua"},
		RootMarkers: []string{".luarc.json", ".luarc.jsonc"},
		Enabled:     binaryAvailable("lua-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "lua-language-server",
			Owner:  "LuaLS",
			Repo:   "lua-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"settings": map[string]interface{}{
				"Lua": map[string]interface{}{
					"diagnostics": map[string]interface{}{
						"enable": true,
					},
					"workspace": map[string]interface{}{
						"checkThirdParty": false,
					},
					"telemetry": map[string]interface{}{
						"enable": false,
					},
				},
			},
		},
	}
}

func kotlinLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerKotlinLS,
		Name:        "Kotlin Language Server",
		Command:     "kotlin-language-server",
		Args:        []string{},
		Extensions:  []string{".kt", ".kts"},
		LanguageIDs: []string{"kotlin"},
		RootMarkers: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		Enabled:     binaryAvailable("kotlin-language-server"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "kotlin-language-server",
			Owner:  "fwcd",
			Repo:   "kotlin-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"kotlin": map[string]interface{}{
				"linting": map[string]interface{}{
					"debounceTime": 250,
				},
				"completion": map[string]interface{}{
					"snippets": map[string]interface{}{"enabled": true},
				},
			},
		},
	}
}

func zlsServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerZLS,
		Name:        "ZLS",
		Command:     "zls",
		Args:        []string{},
		Extensions:  []string{".zig"},
		LanguageIDs: []string{"zig"},
		RootMarkers: []string{"build.zig", "build.zig.zon"},
		Enabled:     binaryAvailable("zls"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "zls",
			Owner:  "zigtools",
			Repo:   "zls",
		},
		InitializationOptions: map[string]interface{}{
			"enable_snippets":             true,
			"enable_ast_check_diagnostics": true,
			"enable_import_detection":      true,
			"warn_style":                  true,
		},
	}
}

func taploServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerTaplo,
		Name:        "Taplo",
		Command:     "taplo",
		Args:        []string{"lsp", "stdio"},
		Extensions:  []string{".toml"},
		LanguageIDs: []string{"toml"},
		RootMarkers: []string{},
		Enabled:     binaryAvailable("taplo"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "taplo",
			Owner:  "tamasfe",
			Repo:   "taplo",
		},
		InitializationOptions: map[string]interface{}{
			"activateFeatures": []interface{}{"diagnostics", "formatting"},
		},
	}
}

func elixirLSServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerElixirLS,
		Name:        "Elixir LS",
		Command:     "language_server.sh",
		Args:        []string{},
		Extensions:  []string{".ex", ".exs"},
		LanguageIDs: []string{"elixir"},
		RootMarkers: []string{"mix.exs", "mix.lock"},
		Enabled:     binaryAvailable("language_server.sh"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "language_server.sh",
			Owner:  "elixir-lsp",
			Repo:   "elixir-ls",
		},
		InitializationOptions: map[string]interface{}{
			"elixirLS": map[string]interface{}{
				"dialyzerEnabled": true,
				"fetchDeps":       false,
				"suggestSpecs":    true,
			},
		},
	}
}

func haskellLanguageServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerHaskellLS,
		Name:        "Haskell Language Server",
		Command:     "haskell-language-server-wrapper",
		Args:        []string{"--lsp"},
		Extensions:  []string{".hs"},
		LanguageIDs: []string{"haskell"},
		RootMarkers: []string{"stack.yaml", "cabal.project", "hie.yaml"},
		Enabled:     binaryAvailable("haskell-language-server-wrapper"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "haskell-language-server-wrapper",
			Owner:  "haskell",
			Repo:   "haskell-language-server",
		},
		InitializationOptions: map[string]interface{}{
			"haskell": map[string]interface{}{
				"plugin": map[string]interface{}{
					"hlint": map[string]interface{}{
						"diagnosticsOn": true,
					},
				},
			},
		},
	}
}

func marksmanServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerMarksman,
		Name:        "Marksman",
		Command:     "marksman",
		Args:        []string{"server"},
		Extensions:  []string{".md"},
		LanguageIDs: []string{"markdown"},
		RootMarkers: []string{".marksman.toml"},
		Enabled:     binaryAvailable("marksman"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "marksman",
			Owner:  "artempyanykh",
			Repo:   "marksman",
		},
	}
}

func metalsServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerMetals,
		Name:        "Metals",
		Command:     "metals",
		Args:        []string{},
		Extensions:  []string{".scala", ".sc"},
		LanguageIDs: []string{"scala"},
		RootMarkers: []string{"build.sbt", "build.sc", ".metals"},
		Enabled:     binaryAvailable("metals"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceGitHub,
			Binary: "metals",
			Owner:  "scalameta",
			Repo:   "metals",
		},
		InitializationOptions: map[string]interface{}{
			"isHttpEnabled":            false,
			"statusBarProvider":        "off",
			"showImplicitArguments":    true,
			"showImplicitConversionsAndClasses": true,
			"showInferredType":         true,
		},
	}
}

func sourcekitLSPServer() *LanguageServerDefinition {
	return &LanguageServerDefinition{
		ID:          ServerSourceKitLSP,
		Name:        "SourceKit-LSP",
		Command:     "sourcekit-lsp",
		Args:        []string{},
		Extensions:  []string{".swift"},
		LanguageIDs: []string{"swift"},
		RootMarkers: []string{"Package.swift", ".build"},
		Enabled:     binaryAvailable("sourcekit-lsp"),
		AutoDownload: &AutoDownloadConfig{
			Source: SourceSystem,
			Binary: "sourcekit-lsp",
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// findDefinition looks up a server definition by ID in BuiltinServers.
func findDefinition(id ServerID) *LanguageServerDefinition {
	for _, def := range BuiltinServers {
		if def.ID == id {
			return def
		}
	}
	return nil
}
