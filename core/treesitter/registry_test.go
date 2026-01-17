package treesitter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownGrammars(t *testing.T) {
	t.Run("has at least 30 languages", func(t *testing.T) {
		assert.GreaterOrEqual(t, len(KnownGrammars), 30)
	})

	t.Run("all entries have name and repository", func(t *testing.T) {
		for key, info := range KnownGrammars {
			assert.Equal(t, key, info.Name, "key should match name")
			assert.NotEmpty(t, info.Repository, "repository should not be empty for %s", key)
		}
	})

	t.Run("common languages exist", func(t *testing.T) {
		commonLangs := []string{"go", "rust", "python", "javascript", "typescript", "java", "c", "cpp"}
		for _, lang := range commonLangs {
			_, ok := KnownGrammars[lang]
			assert.True(t, ok, "should have %s grammar", lang)
		}
	})

	t.Run("go has correct extensions", func(t *testing.T) {
		info := KnownGrammars["go"]
		assert.Contains(t, info.Extensions, ".go")
	})

	t.Run("python has correct extensions", func(t *testing.T) {
		info := KnownGrammars["python"]
		assert.Contains(t, info.Extensions, ".py")
		assert.Contains(t, info.Extensions, ".pyi")
	})

	t.Run("dockerfile has filenames", func(t *testing.T) {
		info := KnownGrammars["dockerfile"]
		assert.Contains(t, info.Filenames, "Dockerfile")
	})
}

func TestNewGrammarRegistry(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	assert.NotNil(t, registry)
	assert.NotNil(t, registry.languages)
	assert.NotNil(t, registry.byExtension)
	assert.NotNil(t, registry.byFilename)
	assert.Equal(t, "/tmp/grammars", registry.grammarDir)
}

func TestGrammarRegistrySetDownloader(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	assert.Nil(t, registry.downloader)

	mockDownloader := &mockGrammarDownloader{}
	registry.SetDownloader(mockDownloader)
	assert.NotNil(t, registry.downloader)
}

type mockGrammarDownloader struct {
	downloadCalled bool
	returnPath     string
	returnErr      error
}

func (m *mockGrammarDownloader) Download(info GrammarInfo) (string, error) {
	m.downloadCalled = true
	return m.returnPath, m.returnErr
}

func TestDetectLanguageForFile(t *testing.T) {
	tests := []struct {
		filePath string
		expected string
		found    bool
	}{
		{"main.go", "go", true},
		{"test.py", "python", true},
		{"app.js", "javascript", true},
		{"app.ts", "typescript", true},
		{"app.tsx", "tsx", true},
		{"style.css", "css", true},
		{"config.yaml", "yaml", true},
		{"config.yml", "yaml", true},
		{"data.json", "json", true},
		{"Dockerfile", "dockerfile", true},
		{"Makefile", "make", true},
		{"script.sh", "bash", true},
		{"unknown.xyz", "", false},
		{"/path/to/file.go", "go", true},
		{"/path/to/Dockerfile", "dockerfile", true},
	}

	for _, tc := range tests {
		t.Run(tc.filePath, func(t *testing.T) {
			lang, found := DetectLanguageForFile(tc.filePath)
			assert.Equal(t, tc.found, found)
			if tc.found {
				assert.Equal(t, tc.expected, lang)
			}
		})
	}
}

func TestGetGrammarInfo(t *testing.T) {
	t.Run("existing grammar", func(t *testing.T) {
		info, ok := GetGrammarInfo("go")
		assert.True(t, ok)
		assert.Equal(t, "go", info.Name)
		assert.Contains(t, info.Extensions, ".go")
	})

	t.Run("non-existing grammar", func(t *testing.T) {
		_, ok := GetGrammarInfo("nonexistent")
		assert.False(t, ok)
	})
}

func TestListKnownGrammars(t *testing.T) {
	grammars := ListKnownGrammars()
	assert.GreaterOrEqual(t, len(grammars), 30)

	names := make(map[string]bool)
	for _, g := range grammars {
		names[g.Name] = true
	}
	assert.True(t, names["go"])
	assert.True(t, names["python"])
	assert.True(t, names["javascript"])
}

func TestContainsString(t *testing.T) {
	slice := []string{"a", "b", "c"}

	t.Run("found", func(t *testing.T) {
		assert.True(t, containsString(slice, "a"))
		assert.True(t, containsString(slice, "b"))
		assert.True(t, containsString(slice, "c"))
	})

	t.Run("not found", func(t *testing.T) {
		assert.False(t, containsString(slice, "d"))
		assert.False(t, containsString(slice, ""))
	})

	t.Run("empty slice", func(t *testing.T) {
		assert.False(t, containsString([]string{}, "a"))
	})

	t.Run("nil slice", func(t *testing.T) {
		assert.False(t, containsString(nil, "a"))
	})
}

func TestGrammarRegistryListLanguages(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	languages := registry.ListLanguages()
	assert.Empty(t, languages)
}

func TestGrammarRegistryIsInstalled(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/nonexistent")
	assert.False(t, registry.IsInstalled("go"))
}

func TestGrammarRegistryClose(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	err := registry.Close()
	assert.NoError(t, err)
}

func TestGrammarRegistryUnloadLanguage(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	err := registry.UnloadLanguage("nonexistent")
	assert.NoError(t, err)
}

func TestGrammarLibraryName(t *testing.T) {
	name := grammarLibraryName("go")
	assert.Contains(t, name, "libtree-sitter-go")
}

func TestLibraryExtension(t *testing.T) {
	ext := libraryExtension()
	assert.NotEmpty(t, ext)
	assert.True(t, ext == ".so" || ext == ".dylib" || ext == ".dll")
}

func TestGrammarRegistryGrammarSearchPaths(t *testing.T) {
	registry := NewGrammarRegistry("/custom/grammars")
	paths := registry.grammarSearchPaths("go")

	assert.NotEmpty(t, paths)
	assert.Contains(t, paths[0], "/custom/grammars")
	assert.Contains(t, paths[0], "go")
}

func TestGrammarRegistryFindGrammarLibrary(t *testing.T) {
	registry := NewGrammarRegistry("/nonexistent")
	path := registry.findGrammarLibrary("go")
	assert.Empty(t, path)
}

func TestGrammarRegistryLoadLanguageNotFound(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	registry := NewGrammarRegistry("/nonexistent")
	_, err := registry.LoadLanguage("go")
	assert.Error(t, err)
}

func TestGrammarRegistryLoadLanguageUnknown(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	registry := NewGrammarRegistry("/nonexistent")
	_, err := registry.LoadLanguage("unknown_language_xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGrammarRegistryGetLanguageForFile(t *testing.T) {
	registry := NewGrammarRegistry("/nonexistent")

	t.Run("unknown extension", func(t *testing.T) {
		_, err := registry.GetLanguageForFile("test.xyz")
		assert.Error(t, err)
	})
}

func TestDetectByFilename(t *testing.T) {
	t.Run("dockerfile", func(t *testing.T) {
		name := detectByFilename("Dockerfile")
		assert.Equal(t, "dockerfile", name)
	})

	t.Run("makefile", func(t *testing.T) {
		name := detectByFilename("Makefile")
		assert.Equal(t, "make", name)
	})

	t.Run("unknown", func(t *testing.T) {
		name := detectByFilename("unknown.txt")
		assert.Empty(t, name)
	})
}

func TestDetectByExtension(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		name := detectByExtension(".go")
		assert.Equal(t, "go", name)
	})

	t.Run("python", func(t *testing.T) {
		name := detectByExtension(".py")
		assert.Equal(t, "python", name)
	})

	t.Run("unknown", func(t *testing.T) {
		name := detectByExtension(".xyz")
		assert.Empty(t, name)
	})
}

func TestGrammarRegistryLookupLanguage(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")

	t.Run("empty registry", func(t *testing.T) {
		lang := registry.lookupLanguage("test.go")
		assert.Nil(t, lang)
	})
}

func TestGrammarRegistryDetectGrammarName(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")

	t.Run("by extension", func(t *testing.T) {
		name := registry.detectGrammarName("test.go")
		assert.Equal(t, "go", name)
	})

	t.Run("by filename", func(t *testing.T) {
		name := registry.detectGrammarName("Dockerfile")
		assert.Equal(t, "dockerfile", name)
	})

	t.Run("unknown", func(t *testing.T) {
		name := registry.detectGrammarName("unknown.xyz")
		assert.Empty(t, name)
	})
}

func TestGrammarRegistryUpdateMappings(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	lang := &Language{name: "go"}

	registry.updateMappings("go", lang)
	assert.Equal(t, lang, registry.byExtension[".go"])
}

func TestGrammarRegistryRemoveFromMappings(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	lang := &Language{name: "go"}

	registry.byExtension[".go"] = lang
	registry.removeFromMappings("go")
	assert.Nil(t, registry.byExtension[".go"])
}

func TestGrammarInfoFields(t *testing.T) {
	info := GrammarInfo{
		Name:       "test",
		Extensions: []string{".test"},
		Filenames:  []string{"Testfile"},
		Repository: "github.com/test/test",
		Version:    "1.0.0",
		Installed:  true,
	}

	assert.Equal(t, "test", info.Name)
	assert.Equal(t, []string{".test"}, info.Extensions)
	assert.Equal(t, []string{"Testfile"}, info.Filenames)
	assert.Equal(t, "github.com/test/test", info.Repository)
	assert.Equal(t, "1.0.0", info.Version)
	assert.True(t, info.Installed)
}

func TestGrammarRegistryIntegration(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "grammar-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registry := NewGrammarRegistry(tmpDir)
	defer registry.Close()

	t.Run("load unknown grammar fails", func(t *testing.T) {
		_, err := registry.LoadLanguage("nonexistent_language")
		assert.Error(t, err)
	})

	t.Run("get language for unknown file fails", func(t *testing.T) {
		_, err := registry.GetLanguageForFile("test.unknownext")
		assert.Error(t, err)
	})
}

func TestOpenGrammarLibrary(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := openGrammarLibrary("/nonexistent/lib.so")
		assert.Error(t, err)
	})
}

func TestValidateABIVersion(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	t.Run("zero language pointer", func(t *testing.T) {
		err := validateABIVersion(TSLanguage(0))
		assert.Error(t, err)
	})
}

func TestGrammarRegistryThreadSafety(t *testing.T) {
	registry := NewGrammarRegistry("/tmp/grammars")
	defer registry.Close()

	done := make(chan bool, 10)

	for i := 0; i < 5; i++ {
		go func() {
			registry.ListLanguages()
			registry.IsInstalled("go")
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		go func() {
			_ = registry.detectGrammarName("test.go")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func BenchmarkDetectLanguageForFile(b *testing.B) {
	for i := 0; i < b.N; i++ {
		DetectLanguageForFile("test.go")
	}
}

func BenchmarkContainsString(b *testing.B) {
	slice := []string{".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cpp", ".rs"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		containsString(slice, ".go")
	}
}

func BenchmarkGrammarRegistryDetectGrammarName(b *testing.B) {
	registry := NewGrammarRegistry("/tmp/grammars")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.detectGrammarName("test.go")
	}
}

func TestCreateLanguage(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	t.Run("invalid handle", func(t *testing.T) {
		_, err := createLanguage(0, "go", "/fake/path")
		assert.Error(t, err)
	})
}

func TestLoadLanguageSymbol(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	t.Run("invalid handle", func(t *testing.T) {
		_, err := loadLanguageSymbol(0, "go")
		assert.Error(t, err)
	})
}

func TestGrammarRegistryDownloadAndLoad(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	registry := NewGrammarRegistry("/tmp/grammars")

	t.Run("unknown grammar", func(t *testing.T) {
		registry.mu.Lock()
		_, err := registry.downloadAndLoad("unknown_xyz")
		registry.mu.Unlock()
		assert.Error(t, err)
	})

	t.Run("no downloader", func(t *testing.T) {
		registry.mu.Lock()
		_, err := registry.downloadAndLoad("go")
		registry.mu.Unlock()
		assert.Error(t, err)
	})
}

func TestGrammarSearchPathsContents(t *testing.T) {
	registry := NewGrammarRegistry("/my/grammars")
	paths := registry.grammarSearchPaths("rust")

	hasCustomPath := false
	hasSystemPath := false

	for _, p := range paths {
		if filepath.HasPrefix(p, "/my/grammars") {
			hasCustomPath = true
		}
		if filepath.HasPrefix(p, "/usr/") {
			hasSystemPath = true
		}
	}

	assert.True(t, hasCustomPath)
	assert.True(t, hasSystemPath)
}
