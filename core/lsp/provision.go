package lsp

import (
	"context"
	"path/filepath"

	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
)

// languageServerMap maps tree-sitter grammar names to approved ServerIDs.
// Each grammar may map to multiple servers (e.g., python → pyright + ruff).
var languageServerMap = map[string][]ServerID{
	"go":         {ServerGopls},
	"typescript": {ServerTypeScript},
	"tsx":        {ServerTypeScript},
	"javascript": {ServerTypeScript},
	"rust":       {ServerRustAna},
	"python":     {ServerPyright, ServerRuff},
	"java":       {ServerJDTLS},
	"c":          {ServerClangd},
	"cpp":        {ServerClangd},
	"ruby":       {ServerSolargraph},
	"json":       {ServerVSCodeJSON},
	"yaml":       {ServerYAML},
	"html":       {ServerVSCodeHTML},
	"css":        {ServerVSCodeCSS},
	"bash":       {ServerBash},
	"toml":       {ServerTaplo},
	"hcl":        {ServerTerraformLS},
	"lua":        {ServerLuaLS},
	"kotlin":     {ServerKotlinLS},
	"scala":      {ServerMetals},
	"zig":        {ServerZLS},
	"vue":        {ServerVue},
	"svelte":     {ServerSvelte},
	"php":        {ServerIntelephense},
	"elixir":     {ServerElixirLS},
	"haskell":    {ServerHaskellLS},
	"markdown":   {ServerMarksman},
	"swift":      {ServerSourceKitLSP},
}

// DiscoverNeededServers detects which languages are used in a project and
// returns the set of LSP servers that should be available.
func DiscoverNeededServers(ctx context.Context, projectRoot string) (map[ServerID]bool, error) {
	languages, err := collectProjectLanguages(ctx, projectRoot)
	if err != nil {
		return nil, err
	}
	return resolveServersForLanguages(languages), nil
}

// collectProjectLanguages discovers files in the project and collects
// the set of unique tree-sitter grammar names present.
func collectProjectLanguages(ctx context.Context, projectRoot string) (map[string]bool, error) {
	files, err := ingestion.DiscoverFiles(ctx, projectRoot, nil, nil)
	if err != nil {
		return nil, err
	}

	languages := make(map[string]bool)
	for _, f := range files {
		ext := filepath.Ext(f.Path)
		if ext == "" {
			continue
		}
		lang := ingestion.GetLanguage(ext)
		if lang != "" {
			languages[lang] = true
		}
	}
	return languages, nil
}

// resolveServersForLanguages maps a set of grammar names to the servers needed.
func resolveServersForLanguages(languages map[string]bool) map[ServerID]bool {
	servers := make(map[ServerID]bool)
	for lang := range languages {
		for _, id := range languageServerMap[lang] {
			servers[id] = true
		}
	}
	return servers
}
