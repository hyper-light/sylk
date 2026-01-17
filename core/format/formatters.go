package format

import (
	"github.com/adalundhe/sylk/core/detect"
)

var BuiltinFormatters = []*FormatterDefinition{
	goimportsFormatter(),
	gofmtFormatter(),
	biomeFormatter(),
	prettierFormatter(),
	ruffFormatFormatter(),
	blackFormatter(),
	rustfmtFormatter(),
	clangFormatFormatter(),
	shfmtFormatter(),
	rubocopFormatter(),
	terraformFmtFormatter(),
}

func goimportsFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "goimports",
		Name:        "goimports",
		Command:     "goimports",
		Args:        []string{"-w", "$FILE"},
		Extensions:  []string{".go"},
		ConfigFiles: []string{},
		Priority:    100,
		Enabled:     binaryExists("goimports"),
	}
}

func gofmtFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "gofmt",
		Name:        "gofmt",
		Command:     "gofmt",
		Args:        []string{"-w", "$FILE"},
		Extensions:  []string{".go"},
		ConfigFiles: []string{},
		Priority:    50,
		Enabled:     binaryExists("gofmt"),
	}
}

func biomeFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "biome",
		Name:        "Biome",
		Command:     "biome",
		Args:        []string{"format", "--write", "$FILE"},
		Extensions:  []string{".js", ".jsx", ".ts", ".tsx", ".json", ".jsonc"},
		ConfigFiles: []string{"biome.json", "biome.jsonc"},
		Priority:    100,
		Enabled:     biomeEnabled,
	}
}

func prettierFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "prettier",
		Name:        "Prettier",
		Command:     "prettier",
		Args:        []string{"--write", "$FILE"},
		Extensions:  []string{".js", ".jsx", ".ts", ".tsx", ".json", ".css", ".scss", ".less", ".html", ".vue", ".svelte", ".md", ".yaml", ".yml"},
		ConfigFiles: []string{".prettierrc", ".prettierrc.json", ".prettierrc.yaml", ".prettierrc.yml", ".prettierrc.js", ".prettierrc.cjs", "prettier.config.js", "prettier.config.cjs"},
		Priority:    50,
		Enabled:     prettierEnabled,
	}
}

func ruffFormatFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "ruff-format",
		Name:        "Ruff Format",
		Command:     "ruff",
		Args:        []string{"format", "$FILE"},
		Extensions:  []string{".py", ".pyi"},
		ConfigFiles: []string{"ruff.toml", ".ruff.toml", "pyproject.toml"},
		Priority:    100,
		Enabled:     ruffEnabled,
	}
}

func blackFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "black",
		Name:        "Black",
		Command:     "black",
		Args:        []string{"$FILE"},
		Extensions:  []string{".py", ".pyi"},
		ConfigFiles: []string{"pyproject.toml"},
		Priority:    50,
		Enabled:     binaryExists("black"),
	}
}

func rustfmtFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "rustfmt",
		Name:        "rustfmt",
		Command:     "rustfmt",
		Args:        []string{"$FILE"},
		Extensions:  []string{".rs"},
		ConfigFiles: []string{"rustfmt.toml", ".rustfmt.toml"},
		Priority:    100,
		Enabled:     binaryExists("rustfmt"),
	}
}

func clangFormatFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "clang-format",
		Name:        "clang-format",
		Command:     "clang-format",
		Args:        []string{"-i", "$FILE"},
		Extensions:  []string{".c", ".h", ".cpp", ".hpp", ".cc", ".cxx", ".hxx", ".c++", ".h++"},
		ConfigFiles: []string{".clang-format", "_clang-format"},
		Priority:    100,
		Enabled:     clangFormatEnabled,
	}
}

func shfmtFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "shfmt",
		Name:        "shfmt",
		Command:     "shfmt",
		Args:        []string{"-w", "$FILE"},
		Extensions:  []string{".sh", ".bash"},
		ConfigFiles: []string{".editorconfig"},
		Priority:    100,
		Enabled:     binaryExists("shfmt"),
	}
}

func rubocopFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "rubocop",
		Name:        "RuboCop",
		Command:     "rubocop",
		Args:        []string{"-a", "$FILE"},
		Extensions:  []string{".rb", ".rake"},
		ConfigFiles: []string{".rubocop.yml"},
		Priority:    100,
		Enabled:     rubocopEnabled,
	}
}

func terraformFmtFormatter() *FormatterDefinition {
	return &FormatterDefinition{
		ID:          "terraform-fmt",
		Name:        "terraform fmt",
		Command:     "terraform",
		Args:        []string{"fmt", "$FILE"},
		Extensions:  []string{".tf", ".tfvars"},
		ConfigFiles: []string{},
		Priority:    100,
		Enabled:     binaryExists("terraform"),
	}
}

func binaryExists(name string) func(string) bool {
	return func(_ string) bool {
		return detect.Which(name) != ""
	}
}

func biomeEnabled(root string) bool {
	if detect.Which("biome") == "" {
		return false
	}
	return detect.FileExists(root, "biome.json", "biome.jsonc")
}

func prettierEnabled(root string) bool {
	if detect.Which("prettier") == "" {
		return false
	}
	return hasNodeModules(root, "prettier") || hasPrettierConfig(root)
}

func hasNodeModules(root, pkg string) bool {
	ok, _ := detect.HasDependency(root, pkg)
	return ok
}

func hasPrettierConfig(root string) bool {
	configs := []string{
		".prettierrc", ".prettierrc.json", ".prettierrc.yaml", ".prettierrc.yml",
		".prettierrc.js", ".prettierrc.cjs", "prettier.config.js", "prettier.config.cjs",
	}
	return detect.FileExists(root, configs...)
}

func ruffEnabled(root string) bool {
	if detect.Which("ruff") == "" {
		return false
	}
	return detect.FileExists(root, "ruff.toml", ".ruff.toml") || hasRuffInPyproject(root)
}

func hasRuffInPyproject(root string) bool {
	ok, _ := detect.HasPythonDependency(root, "ruff")
	return ok
}

func clangFormatEnabled(root string) bool {
	if detect.Which("clang-format") == "" {
		return false
	}
	return detect.FileExists(root, ".clang-format", "_clang-format")
}

func rubocopEnabled(root string) bool {
	if detect.Which("rubocop") == "" {
		return false
	}
	ok, _ := detect.HasGemDependency(root, "rubocop")
	return ok || detect.FileExists(root, ".rubocop.yml")
}
