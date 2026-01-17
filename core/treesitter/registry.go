package treesitter

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/ebitengine/purego"
)

type GrammarInfo struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions,omitempty"`
	Filenames  []string `json:"filenames,omitempty"`
	Repository string   `json:"repository"`
	Version    string   `json:"version,omitempty"`
	Installed  bool     `json:"installed"`
}

type GrammarRegistry struct {
	languages   map[string]*Language
	byExtension map[string]*Language
	byFilename  map[string]*Language
	grammarDir  string
	downloader  GrammarDownloaderService
	mu          sync.RWMutex
}

type GrammarDownloaderService interface {
	Download(info GrammarInfo) (string, error)
}

var KnownGrammars = map[string]GrammarInfo{
	"go":         {Name: "go", Extensions: []string{".go"}, Repository: "github.com/tree-sitter/tree-sitter-go"},
	"rust":       {Name: "rust", Extensions: []string{".rs"}, Repository: "github.com/tree-sitter/tree-sitter-rust"},
	"python":     {Name: "python", Extensions: []string{".py", ".pyi"}, Repository: "github.com/tree-sitter/tree-sitter-python"},
	"javascript": {Name: "javascript", Extensions: []string{".js", ".mjs", ".cjs"}, Repository: "github.com/tree-sitter/tree-sitter-javascript"},
	"typescript": {Name: "typescript", Extensions: []string{".ts", ".mts"}, Repository: "github.com/tree-sitter/tree-sitter-typescript"},
	"tsx":        {Name: "tsx", Extensions: []string{".tsx"}, Repository: "github.com/tree-sitter/tree-sitter-typescript"},
	"jsx":        {Name: "jsx", Extensions: []string{".jsx"}, Repository: "github.com/tree-sitter/tree-sitter-javascript"},
	"java":       {Name: "java", Extensions: []string{".java"}, Repository: "github.com/tree-sitter/tree-sitter-java"},
	"c":          {Name: "c", Extensions: []string{".c", ".h"}, Repository: "github.com/tree-sitter/tree-sitter-c"},
	"cpp":        {Name: "cpp", Extensions: []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"}, Repository: "github.com/tree-sitter/tree-sitter-cpp"},
	"c_sharp":    {Name: "c_sharp", Extensions: []string{".cs"}, Repository: "github.com/tree-sitter/tree-sitter-c-sharp"},
	"ruby":       {Name: "ruby", Extensions: []string{".rb", ".rake"}, Repository: "github.com/tree-sitter/tree-sitter-ruby"},
	"swift":      {Name: "swift", Extensions: []string{".swift"}, Repository: "github.com/alex-pinkus/tree-sitter-swift"},
	"kotlin":     {Name: "kotlin", Extensions: []string{".kt", ".kts"}, Repository: "github.com/fwcd/tree-sitter-kotlin"},
	"scala":      {Name: "scala", Extensions: []string{".scala", ".sc"}, Repository: "github.com/tree-sitter/tree-sitter-scala"},
	"php":        {Name: "php", Extensions: []string{".php"}, Repository: "github.com/tree-sitter/tree-sitter-php"},
	"html":       {Name: "html", Extensions: []string{".html", ".htm"}, Repository: "github.com/tree-sitter/tree-sitter-html"},
	"css":        {Name: "css", Extensions: []string{".css"}, Repository: "github.com/tree-sitter/tree-sitter-css"},
	"scss":       {Name: "scss", Extensions: []string{".scss"}, Repository: "github.com/serenadeai/tree-sitter-scss"},
	"json":       {Name: "json", Extensions: []string{".json"}, Repository: "github.com/tree-sitter/tree-sitter-json"},
	"yaml":       {Name: "yaml", Extensions: []string{".yaml", ".yml"}, Repository: "github.com/ikatyang/tree-sitter-yaml"},
	"toml":       {Name: "toml", Extensions: []string{".toml"}, Repository: "github.com/ikatyang/tree-sitter-toml"},
	"markdown":   {Name: "markdown", Extensions: []string{".md", ".markdown"}, Repository: "github.com/ikatyang/tree-sitter-markdown"},
	"bash":       {Name: "bash", Extensions: []string{".sh", ".bash", ".zsh"}, Repository: "github.com/tree-sitter/tree-sitter-bash"},
	"sql":        {Name: "sql", Extensions: []string{".sql"}, Repository: "github.com/DerekStride/tree-sitter-sql"},
	"hcl":        {Name: "hcl", Extensions: []string{".hcl", ".tf"}, Repository: "github.com/MichaHoffmann/tree-sitter-hcl"},
	"dockerfile": {Name: "dockerfile", Filenames: []string{"Dockerfile", "Dockerfile.dev", "Dockerfile.prod"}, Repository: "github.com/camdencheek/tree-sitter-dockerfile"},
	"lua":        {Name: "lua", Extensions: []string{".lua"}, Repository: "github.com/Azganoth/tree-sitter-lua"},
	"elixir":     {Name: "elixir", Extensions: []string{".ex", ".exs"}, Repository: "github.com/elixir-lang/tree-sitter-elixir"},
	"zig":        {Name: "zig", Extensions: []string{".zig"}, Repository: "github.com/maxxnino/tree-sitter-zig"},
	"ocaml":      {Name: "ocaml", Extensions: []string{".ml", ".mli"}, Repository: "github.com/tree-sitter/tree-sitter-ocaml"},
	"haskell":    {Name: "haskell", Extensions: []string{".hs"}, Repository: "github.com/tree-sitter/tree-sitter-haskell"},
	"clojure":    {Name: "clojure", Extensions: []string{".clj", ".cljs", ".cljc"}, Repository: "github.com/sogaiu/tree-sitter-clojure"},
	"vue":        {Name: "vue", Extensions: []string{".vue"}, Repository: "github.com/ikatyang/tree-sitter-vue"},
	"svelte":     {Name: "svelte", Extensions: []string{".svelte"}, Repository: "github.com/Himujjal/tree-sitter-svelte"},
	"graphql":    {Name: "graphql", Extensions: []string{".graphql", ".gql"}, Repository: "github.com/bkegley/tree-sitter-graphql"},
	"proto":      {Name: "proto", Extensions: []string{".proto"}, Repository: "github.com/mitchellh/tree-sitter-proto"},
	"erlang":     {Name: "erlang", Extensions: []string{".erl", ".hrl"}, Repository: "github.com/AbstractMachinesLab/tree-sitter-erlang"},
	"r":          {Name: "r", Extensions: []string{".r", ".R"}, Repository: "github.com/r-lib/tree-sitter-r"},
	"julia":      {Name: "julia", Extensions: []string{".jl"}, Repository: "github.com/tree-sitter/tree-sitter-julia"},
	"perl":       {Name: "perl", Extensions: []string{".pl", ".pm"}, Repository: "github.com/tree-sitter-perl/tree-sitter-perl"},
	"dart":       {Name: "dart", Extensions: []string{".dart"}, Repository: "github.com/UserNobworty/tree-sitter-dart"},
	"make":       {Name: "make", Filenames: []string{"Makefile", "makefile", "GNUmakefile"}, Repository: "github.com/alemuller/tree-sitter-make"},
	"cmake":      {Name: "cmake", Filenames: []string{"CMakeLists.txt"}, Extensions: []string{".cmake"}, Repository: "github.com/uyha/tree-sitter-cmake"},
	"nix":        {Name: "nix", Extensions: []string{".nix"}, Repository: "github.com/cstrahan/tree-sitter-nix"},
	"vim":        {Name: "vim", Extensions: []string{".vim"}, Repository: "github.com/neovim/tree-sitter-vim"},
	"regex":      {Name: "regex", Extensions: []string{".regex"}, Repository: "github.com/tree-sitter/tree-sitter-regex"},
	"comment":    {Name: "comment", Extensions: []string{}, Repository: "github.com/stsewd/tree-sitter-comment"},
}

func NewGrammarRegistry(grammarDir string) *GrammarRegistry {
	return &GrammarRegistry{
		languages:   make(map[string]*Language),
		byExtension: make(map[string]*Language),
		byFilename:  make(map[string]*Language),
		grammarDir:  grammarDir,
	}
}

func (r *GrammarRegistry) SetDownloader(d GrammarDownloaderService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downloader = d
}

func (r *GrammarRegistry) LoadLanguage(name string) (*Language, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLanguageLocked(name)
}

func (r *GrammarRegistry) loadLanguageLocked(name string) (*Language, error) {
	if lang, ok := r.languages[name]; ok {
		return lang, nil
	}

	libPath := r.findGrammarLibrary(name)
	if libPath == "" {
		return r.downloadAndLoad(name)
	}

	return r.loadFromPath(name, libPath)
}

func (r *GrammarRegistry) downloadAndLoad(name string) (*Language, error) {
	info, ok := KnownGrammars[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrGrammarNotFound, name)
	}

	if r.downloader == nil {
		return nil, ErrGrammarNotInstalled
	}

	libPath, err := r.downloader.Download(info)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDownloadFailed, name, err)
	}

	return r.loadFromPath(name, libPath)
}

func (r *GrammarRegistry) loadFromPath(name, libPath string) (*Language, error) {
	lang, err := r.loadGrammarLibrary(name, libPath)
	if err != nil {
		return nil, err
	}

	r.languages[name] = lang
	r.updateMappings(name, lang)
	return lang, nil
}

func (r *GrammarRegistry) updateMappings(name string, lang *Language) {
	info, ok := KnownGrammars[name]
	if !ok {
		return
	}

	for _, ext := range info.Extensions {
		r.byExtension[ext] = lang
	}
	for _, filename := range info.Filenames {
		r.byFilename[filename] = lang
	}
}

func (r *GrammarRegistry) GetLanguageForFile(filePath string) (*Language, error) {
	r.mu.RLock()
	lang := r.lookupLanguage(filePath)
	r.mu.RUnlock()

	if lang != nil {
		return lang, nil
	}

	grammarName := r.detectGrammarName(filePath)
	if grammarName == "" {
		return nil, fmt.Errorf("%w for: %s", ErrGrammarNotFound, filePath)
	}

	return r.LoadLanguage(grammarName)
}

func (r *GrammarRegistry) lookupLanguage(filePath string) *Language {
	filename := filepath.Base(filePath)
	if lang, ok := r.byFilename[filename]; ok {
		return lang
	}

	ext := filepath.Ext(filePath)
	if lang, ok := r.byExtension[ext]; ok {
		return lang
	}
	return nil
}

func (r *GrammarRegistry) detectGrammarName(filePath string) string {
	filename := filepath.Base(filePath)
	ext := filepath.Ext(filePath)

	for name, info := range KnownGrammars {
		if containsString(info.Filenames, filename) {
			return name
		}
		if containsString(info.Extensions, ext) {
			return name
		}
	}
	return ""
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func (r *GrammarRegistry) loadGrammarLibrary(name, libPath string) (*Language, error) {
	if err := Initialize(); err != nil {
		return nil, err
	}

	handle, err := openGrammarLibrary(libPath)
	if err != nil {
		return nil, err
	}

	return createLanguage(handle, name, libPath)
}

func openGrammarLibrary(libPath string) (uintptr, error) {
	handle, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", libPath, err)
	}
	return handle, nil
}

func createLanguage(handle uintptr, name, libPath string) (*Language, error) {
	langPtr, err := loadLanguageSymbol(handle, name)
	if err != nil {
		_ = purego.Dlclose(handle)
		return nil, err
	}

	if err := validateABIVersion(langPtr); err != nil {
		_ = purego.Dlclose(handle)
		return nil, err
	}

	return &Language{
		ptr:       langPtr,
		name:      name,
		libPath:   libPath,
		libHandle: handle,
	}, nil
}

func loadLanguageSymbol(handle uintptr, name string) (TSLanguage, error) {
	symbolName := "tree_sitter_" + name
	var langFunc func() TSLanguage
	purego.RegisterLibFunc(&langFunc, handle, symbolName)

	langPtr := langFunc()
	if langPtr == 0 {
		return 0, fmt.Errorf("failed to get language from %s", symbolName)
	}
	return langPtr, nil
}

func validateABIVersion(langPtr TSLanguage) error {
	version := LanguageVersion(langPtr)
	if version < 13 || version > 14 {
		return fmt.Errorf("%w: version %d", ErrIncompatibleABI, version)
	}
	return nil
}

func (r *GrammarRegistry) findGrammarLibrary(name string) string {
	paths := r.grammarSearchPaths(name)
	for _, path := range paths {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func (r *GrammarRegistry) grammarSearchPaths(name string) []string {
	libName := grammarLibraryName(name)
	return []string{
		filepath.Join(r.grammarDir, name, libName),
		filepath.Join(r.grammarDir, libName),
		filepath.Join("/usr/local/lib/tree-sitter", libName),
		filepath.Join("/usr/lib/tree-sitter", libName),
	}
}

func grammarLibraryName(name string) string {
	return "libtree-sitter-" + name + libraryExtension()
}

func libraryExtension() string {
	switch libraryName() {
	case "libtree-sitter.dylib":
		return ".dylib"
	case "tree-sitter.dll":
		return ".dll"
	default:
		return ".so"
	}
}

func (r *GrammarRegistry) IsInstalled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.languages[name]; ok {
		return true
	}
	return r.findGrammarLibrary(name) != ""
}

func (r *GrammarRegistry) ListLanguages() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	languages := make([]string, 0, len(r.languages))
	for name := range r.languages {
		languages = append(languages, name)
	}
	return languages
}

func (r *GrammarRegistry) UnloadLanguage(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	lang, ok := r.languages[name]
	if !ok {
		return nil
	}

	r.removeFromMappings(name)
	delete(r.languages, name)

	if lang.libHandle != 0 {
		return purego.Dlclose(lang.libHandle)
	}
	return nil
}

func (r *GrammarRegistry) removeFromMappings(name string) {
	info, ok := KnownGrammars[name]
	if !ok {
		return
	}

	for _, ext := range info.Extensions {
		delete(r.byExtension, ext)
	}
	for _, filename := range info.Filenames {
		delete(r.byFilename, filename)
	}
}

func (r *GrammarRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var lastErr error
	for name, lang := range r.languages {
		if lang.libHandle != 0 {
			if err := purego.Dlclose(lang.libHandle); err != nil {
				lastErr = err
			}
		}
		delete(r.languages, name)
	}

	r.byExtension = make(map[string]*Language)
	r.byFilename = make(map[string]*Language)
	return lastErr
}

func ListKnownGrammars() []GrammarInfo {
	result := make([]GrammarInfo, 0, len(KnownGrammars))
	for _, info := range KnownGrammars {
		result = append(result, info)
	}
	return result
}

func GetGrammarInfo(name string) (GrammarInfo, bool) {
	info, ok := KnownGrammars[name]
	return info, ok
}

func DetectLanguageForFile(filePath string) (string, bool) {
	filename := filepath.Base(filePath)
	if name := detectByFilename(filename); name != "" {
		return name, true
	}

	ext := filepath.Ext(filePath)
	if name := detectByExtension(ext); name != "" {
		return name, true
	}

	return "", false
}

func detectByFilename(filename string) string {
	for name, info := range KnownGrammars {
		if containsString(info.Filenames, filename) {
			return name
		}
	}
	return ""
}

func detectByExtension(ext string) string {
	for name, info := range KnownGrammars {
		if containsString(info.Extensions, ext) {
			return name
		}
	}
	return ""
}
