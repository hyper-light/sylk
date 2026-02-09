package ingestion

import (
	"bytes"
	"path/filepath"
)

// FileClass holds the result of file type classification.
type FileClass struct {
	Lang    string // tree-sitter grammar name, empty if no grammar available
	DocType string // maps to search.DocumentType values
}

// DocType constants matching search.DocumentType string values.
const (
	docTypeSourceCode = "source_code"
	docTypeMarkdown   = "markdown"
	docTypeConfig     = "config"
	docTypeNote       = "note"
	docTypeWebContent = "web_content"
)

// ClassifyFile determines the language and document type for a file.
// Uses extension, filename, then shebang detection. Falls back to note.
func ClassifyFile(path string, header []byte) FileClass {
	if fc, ok := classifyByPath(path); ok {
		return fc
	}
	return classifyByContent(header)
}

// classifyByPath tries extension then filename classification.
func classifyByPath(path string) (FileClass, bool) {
	if fc, ok := classifyByExtension(extractExtension(path)); ok {
		return fc, true
	}
	return classifyByFilename(filepath.Base(path))
}

// classifyByContent tries shebang detection, then falls back to note.
func classifyByContent(header []byte) FileClass {
	if fc, ok := classifyByShebang(header); ok {
		return fc
	}
	return FileClass{DocType: docTypeNote}
}

// classifyByExtension looks up the extension in the unified map.
func classifyByExtension(ext string) (FileClass, bool) {
	e, ok := extensionMap[ext]
	if !ok {
		return FileClass{}, false
	}
	return FileClass{Lang: e.lang, DocType: e.docType}, true
}

// classifyByFilename matches known filenames without extensions.
func classifyByFilename(base string) (FileClass, bool) {
	e, ok := filenameMap[base]
	if !ok {
		return FileClass{}, false
	}
	return FileClass{Lang: e.lang, DocType: e.docType}, true
}

// classifyByShebang parses a #! line to identify the interpreter.
func classifyByShebang(header []byte) (FileClass, bool) {
	line := extractFirstLine(header)
	if !bytes.HasPrefix(line, []byte("#!")) {
		return FileClass{}, false
	}
	interpreter := extractInterpreter(line)
	lang, ok := interpreterMap[interpreter]
	if !ok {
		return FileClass{}, false
	}
	return FileClass{Lang: lang, DocType: docTypeSourceCode}, true
}

// extractInterpreter parses the interpreter name from a shebang line.
// Handles both #!/usr/bin/env python and #!/usr/bin/python forms.
func extractInterpreter(line []byte) string {
	parts := bytes.Fields(line[2:]) // skip #!
	if len(parts) == 0 {
		return ""
	}
	prog := filepath.Base(string(parts[0]))
	if prog == "env" && len(parts) > 1 {
		prog = filepath.Base(string(parts[1]))
	}
	return normalizeInterpreter(prog)
}

// normalizeInterpreter strips version suffixes: python3.11 → python.
func normalizeInterpreter(prog string) string {
	for i, c := range prog {
		if c >= '0' && c <= '9' {
			return prog[:i]
		}
	}
	return prog
}

// extractFirstLine returns bytes up to the first newline.
func extractFirstLine(data []byte) []byte {
	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return data
	}
	return data[:idx]
}

// =========================================================================
// Backward-compatible wrappers (replace SupportedLanguages map)
// =========================================================================

// GetLanguage returns the tree-sitter grammar name for the given extension.
// Returns empty string if no grammar is available.
func GetLanguage(ext string) string {
	if e, ok := extensionMap[ext]; ok {
		return e.lang
	}
	return ""
}

// IsSupportedExtension returns true if the extension has a tree-sitter grammar.
func IsSupportedExtension(ext string) bool {
	e, ok := extensionMap[ext]
	return ok && e.lang != ""
}

// =========================================================================
// Unified extension map (single source of truth)
// =========================================================================

// fileTypeEntry maps an extension to its tree-sitter grammar and document type.
type fileTypeEntry struct {
	lang    string // tree-sitter grammar name, empty if no grammar
	docType string // document type for chunker selection
}

// extensionMap is the authoritative mapping of file extensions to types.
// Merges treesitter/manager.go extToLang + additional non-parseable types.
var extensionMap = map[string]fileTypeEntry{
	// --- Source code with tree-sitter grammars ---
	".go":   {lang: "go", docType: docTypeSourceCode},
	".rs":   {lang: "rust", docType: docTypeSourceCode},
	".py":   {lang: "python", docType: docTypeSourceCode},
	".pyi":  {lang: "python", docType: docTypeSourceCode},
	".js":   {lang: "javascript", docType: docTypeSourceCode},
	".mjs":  {lang: "javascript", docType: docTypeSourceCode},
	".cjs":  {lang: "javascript", docType: docTypeSourceCode},
	".jsx":  {lang: "javascript", docType: docTypeSourceCode},
	".ts":   {lang: "typescript", docType: docTypeSourceCode},
	".mts":  {lang: "typescript", docType: docTypeSourceCode},
	".tsx":  {lang: "tsx", docType: docTypeSourceCode},
	".java": {lang: "java", docType: docTypeSourceCode},
	".c":    {lang: "c", docType: docTypeSourceCode},
	".h":    {lang: "c", docType: docTypeSourceCode},
	".cpp":  {lang: "cpp", docType: docTypeSourceCode},
	".cc":   {lang: "cpp", docType: docTypeSourceCode},
	".cxx":  {lang: "cpp", docType: docTypeSourceCode},
	".hpp":  {lang: "cpp", docType: docTypeSourceCode},
	".hxx":  {lang: "cpp", docType: docTypeSourceCode},
	".rb":   {lang: "ruby", docType: docTypeSourceCode},
	".rake": {lang: "ruby", docType: docTypeSourceCode},

	".swift":  {lang: "swift", docType: docTypeSourceCode},
	".kt":     {lang: "kotlin", docType: docTypeSourceCode},
	".kts":    {lang: "kotlin", docType: docTypeSourceCode},
	".scala":  {lang: "scala", docType: docTypeSourceCode},
	".sc":     {lang: "scala", docType: docTypeSourceCode},
	".lua":    {lang: "lua", docType: docTypeSourceCode},
	".php":    {lang: "php", docType: docTypeSourceCode},
	".ex":     {lang: "elixir", docType: docTypeSourceCode},
	".exs":    {lang: "elixir", docType: docTypeSourceCode},
	".hs":     {lang: "haskell", docType: docTypeSourceCode},
	".zig":    {lang: "zig", docType: docTypeSourceCode},
	".vue":    {lang: "vue", docType: docTypeSourceCode},
	".svelte": {lang: "svelte", docType: docTypeSourceCode},

	".bash": {lang: "bash", docType: docTypeSourceCode},
	".sh":   {lang: "bash", docType: docTypeSourceCode},
	".css":  {lang: "css", docType: docTypeSourceCode},

	// --- Markup with tree-sitter ---
	".md":   {lang: "markdown", docType: docTypeMarkdown},
	".html": {lang: "html", docType: docTypeWebContent},

	// --- Config with tree-sitter ---
	".json":       {lang: "json", docType: docTypeConfig},
	".yaml":       {lang: "yaml", docType: docTypeConfig},
	".yml":        {lang: "yaml", docType: docTypeConfig},
	".toml":       {lang: "toml", docType: docTypeConfig},
	".properties": {lang: "properties", docType: docTypeConfig},
	".tf":         {lang: "hcl", docType: docTypeConfig},
	".tfvars":     {lang: "hcl", docType: docTypeConfig},

	// --- Source code without tree-sitter ---
	".proto":   {docType: docTypeSourceCode},
	".sql":     {docType: docTypeSourceCode},
	".graphql": {docType: docTypeSourceCode},
	".gql":     {docType: docTypeSourceCode},
	".thrift":  {docType: docTypeSourceCode},
	".pl":      {docType: docTypeSourceCode},
	".pm":      {docType: docTypeSourceCode},
	".r":       {docType: docTypeSourceCode},
	".R":       {docType: docTypeSourceCode},
	".m":       {docType: docTypeSourceCode},
	".bat":     {docType: docTypeSourceCode},
	".cmd":     {docType: docTypeSourceCode},
	".ps1":     {docType: docTypeSourceCode},
	".gradle":  {docType: docTypeSourceCode},
	".cmake":   {docType: docTypeSourceCode},
	".scss":    {docType: docTypeSourceCode},
	".sass":    {docType: docTypeSourceCode},
	".less":    {docType: docTypeSourceCode},
	".bzl":     {docType: docTypeSourceCode},
	".star":    {docType: docTypeSourceCode},

	// --- Config without tree-sitter ---
	".xml":    {docType: docTypeConfig},
	".ini":    {docType: docTypeConfig},
	".cfg":    {docType: docTypeConfig},
	".conf":   {docType: docTypeConfig},
	".env":    {docType: docTypeConfig},
	".csv":    {docType: docTypeConfig},
	".tsv":    {docType: docTypeConfig},
	".hcl":    {docType: docTypeConfig},
	".plist":  {docType: docTypeConfig},
	".lock":   {docType: docTypeConfig},
	".sum":    {docType: docTypeConfig},
	".xhtml":  {docType: docTypeWebContent},
	".htm":    {docType: docTypeWebContent},
	".svg":    {docType: docTypeWebContent},
	".avsc":   {docType: docTypeConfig},
	".jsonnet": {docType: docTypeConfig},

	// --- Docs/text without tree-sitter ---
	".txt":  {docType: docTypeNote},
	".rst":  {docType: docTypeMarkdown},
	".adoc": {docType: docTypeMarkdown},
	".log":  {docType: docTypeNote},
}

// filenameMap maps known filenames (no extension or special names) to types.
var filenameMap = map[string]fileTypeEntry{
	// Build systems
	"Makefile":       {docType: docTypeSourceCode},
	"GNUmakefile":    {docType: docTypeSourceCode},
	"CMakeLists.txt": {docType: docTypeSourceCode},
	"BUILD":          {docType: docTypeSourceCode},
	"BUILD.bazel":    {docType: docTypeSourceCode},
	"WORKSPACE":      {docType: docTypeSourceCode},
	"WORKSPACE.bazel": {docType: docTypeSourceCode},
	"Rakefile":       {docType: docTypeSourceCode},
	"Gemfile":        {docType: docTypeConfig},
	"Vagrantfile":    {docType: docTypeConfig},
	"Jenkinsfile":    {docType: docTypeSourceCode},

	// Container/deploy
	"Dockerfile":    {docType: docTypeConfig},
	".dockerignore": {docType: docTypeConfig},
	".gitignore":    {docType: docTypeConfig},
	".gitmodules":   {docType: docTypeConfig},
	".gitattributes": {docType: docTypeConfig},
	".editorconfig": {docType: docTypeConfig},
	".eslintrc":     {docType: docTypeConfig},
	".prettierrc":   {docType: docTypeConfig},
	".babelrc":      {docType: docTypeConfig},

	// Documentation
	"LICENSE":      {docType: docTypeNote},
	"OWNERS":       {docType: docTypeNote},
	"CODEOWNERS":   {docType: docTypeNote},
	"MAINTAINERS":  {docType: docTypeNote},
	"AUTHORS":      {docType: docTypeNote},
	"README":       {docType: docTypeMarkdown},
	"CHANGELOG":    {docType: docTypeMarkdown},
	"CONTRIBUTING": {docType: docTypeMarkdown},
	"SECURITY":     {docType: docTypeMarkdown},
}

// interpreterMap maps shebang interpreter names to tree-sitter grammar names.
var interpreterMap = map[string]string{
	"python": "python",
	"ruby":   "ruby",
	"node":   "javascript",
	"bash":   "bash",
	"sh":     "bash",
	"zsh":    "bash",
	"perl":   "",    // no tree-sitter, but still source_code
	"php":    "php",
	"lua":    "lua",
	"elixir": "elixir",
}
