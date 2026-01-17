package treesitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGrammarDownloader(t *testing.T) {
	dir := "/tmp/test-grammars"
	d := NewGrammarDownloader(dir)

	assert.NotNil(t, d)
	assert.Equal(t, dir, d.grammarDir)
	assert.NotNil(t, d.httpClient)
	assert.NotEmpty(t, d.compilerPaths)
}

func TestGrammarDownloaderGrammarDir(t *testing.T) {
	dir := "/custom/grammar/dir"
	d := NewGrammarDownloader(dir)
	assert.Equal(t, dir, d.GrammarDir())
}

func TestLocalLibraryPath(t *testing.T) {
	d := NewGrammarDownloader("/grammars")
	path := d.localLibraryPath("go")

	assert.Contains(t, path, "go")
	assert.Contains(t, path, "libtree-sitter-go")
}

func TestDownloadErrorTypes(t *testing.T) {
	t.Run("prebuilt not available", func(t *testing.T) {
		assert.Contains(t, ErrPrebuiltNotAvailable.Error(), "prebuilt")
	})

	t.Run("compiler not found", func(t *testing.T) {
		assert.Contains(t, ErrCompilerNotFound.Error(), "compiler")
	})

	t.Run("parser source not found", func(t *testing.T) {
		assert.Contains(t, ErrParserSourceNotFound.Error(), "parser.c")
	})
}

func TestPrebuiltURLs(t *testing.T) {
	d := NewGrammarDownloader("/grammars")
	info := GrammarInfo{
		Name:       "go",
		Repository: "github.com/tree-sitter/tree-sitter-go",
	}

	urls := d.prebuiltURLs(info)
	assert.Len(t, urls, 2)

	for _, url := range urls {
		assert.Contains(t, url, "tree-sitter-go")
		assert.Contains(t, url, "libtree-sitter-go")
	}
}

func TestPrebuiltReleaseURL(t *testing.T) {
	url := prebuiltReleaseURL("github.com/tree-sitter/tree-sitter-go", "go", "darwin", "arm64", ".dylib")

	assert.Contains(t, url, "github.com/tree-sitter/tree-sitter-go")
	assert.Contains(t, url, "libtree-sitter-go")
	assert.Contains(t, url, "darwin")
	assert.Contains(t, url, "arm64")
	assert.Contains(t, url, ".dylib")
}

func TestFormatRepoURL(t *testing.T) {
	t.Run("without https prefix", func(t *testing.T) {
		url := formatRepoURL("github.com/tree-sitter/tree-sitter-go")
		assert.Equal(t, "https://github.com/tree-sitter/tree-sitter-go.git", url)
	})

	t.Run("with https prefix", func(t *testing.T) {
		url := formatRepoURL("https://github.com/tree-sitter/tree-sitter-go")
		assert.Equal(t, "https://github.com/tree-sitter/tree-sitter-go", url)
	})
}

func TestFindCompiler(t *testing.T) {
	d := NewGrammarDownloader("/grammars")
	_ = d.findCompiler()
}

func TestCompileArgs(t *testing.T) {
	args := compileArgs("/src/parser.c", "/out/lib.so")

	assert.Contains(t, args, "-shared")
	assert.Contains(t, args, "-fPIC")
	assert.Contains(t, args, "-O2")
	assert.Contains(t, args, "/src/parser.c")
	assert.Contains(t, args, "/out/lib.so")
}

func TestAppendScannerIfExists(t *testing.T) {
	t.Run("scanner does not exist", func(t *testing.T) {
		args := []string{"-shared"}
		result := appendScannerIfExists(args, "/nonexistent/parser.c")
		assert.Equal(t, args, result)
	})

	t.Run("scanner exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		parserPath := filepath.Join(tmpDir, "parser.c")
		scannerPath := filepath.Join(tmpDir, "scanner.c")

		require.NoError(t, os.WriteFile(parserPath, []byte(""), 0644))
		require.NoError(t, os.WriteFile(scannerPath, []byte(""), 0644))

		args := []string{"-shared"}
		result := appendScannerIfExists(args, parserPath)

		assert.Len(t, result, 2)
		assert.Contains(t, result[1], "scanner.c")
	})
}

func TestFindParserSource(t *testing.T) {
	t.Run("src/parser.c exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcDir := filepath.Join(tmpDir, "src")
		require.NoError(t, os.MkdirAll(srcDir, 0755))

		parserPath := filepath.Join(srcDir, "parser.c")
		require.NoError(t, os.WriteFile(parserPath, []byte(""), 0644))

		result := findParserSource(tmpDir)
		assert.Equal(t, parserPath, result)
	})

	t.Run("parser.c in root", func(t *testing.T) {
		tmpDir := t.TempDir()
		parserPath := filepath.Join(tmpDir, "parser.c")
		require.NoError(t, os.WriteFile(parserPath, []byte(""), 0644))

		result := findParserSource(tmpDir)
		assert.Equal(t, parserPath, result)
	})

	t.Run("no parser.c", func(t *testing.T) {
		tmpDir := t.TempDir()
		result := findParserSource(tmpDir)
		assert.Empty(t, result)
	})
}

func TestDownloaderIsInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	t.Run("not installed", func(t *testing.T) {
		assert.False(t, d.IsInstalled("go"))
	})

	t.Run("installed", func(t *testing.T) {
		grammarDir := filepath.Join(tmpDir, "go")
		require.NoError(t, os.MkdirAll(grammarDir, 0755))

		libPath := filepath.Join(grammarDir, grammarLibraryName("go"))
		require.NoError(t, os.WriteFile(libPath, []byte("fake lib"), 0644))

		assert.True(t, d.IsInstalled("go"))
	})
}

func TestDownloaderRemove(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	grammarDir := filepath.Join(tmpDir, "go")
	require.NoError(t, os.MkdirAll(grammarDir, 0755))

	libPath := filepath.Join(grammarDir, "lib.so")
	require.NoError(t, os.WriteFile(libPath, []byte(""), 0644))

	err := d.Remove("go")
	require.NoError(t, err)

	_, err = os.Stat(grammarDir)
	assert.True(t, os.IsNotExist(err))
}

func TestDownloaderListInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	t.Run("empty directory", func(t *testing.T) {
		list, err := d.ListInstalled()
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("with installed grammars", func(t *testing.T) {
		for _, name := range []string{"go", "rust"} {
			grammarDir := filepath.Join(tmpDir, name)
			require.NoError(t, os.MkdirAll(grammarDir, 0755))

			libPath := filepath.Join(grammarDir, grammarLibraryName(name))
			require.NoError(t, os.WriteFile(libPath, []byte(""), 0644))
		}

		list, err := d.ListInstalled()
		require.NoError(t, err)
		assert.Len(t, list, 2)
		assert.Contains(t, list, "go")
		assert.Contains(t, list, "rust")
	})
}

func TestDownloaderListInstalledNonexistent(t *testing.T) {
	d := NewGrammarDownloader("/nonexistent/path")
	list, err := d.ListInstalled()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestFilterInstalled(t *testing.T) {
	tmpDir := t.TempDir()

	goDir := filepath.Join(tmpDir, "go")
	require.NoError(t, os.MkdirAll(goDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(goDir, grammarLibraryName("go")), []byte(""), 0644))

	emptyDir := filepath.Join(tmpDir, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0755))

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	installed := filterInstalled(entries, tmpDir)
	assert.Len(t, installed, 1)
	assert.Contains(t, installed, "go")
}

func TestDownloadPrebuilt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake library content"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	grammarDir := filepath.Join(tmpDir, "test")
	require.NoError(t, os.MkdirAll(grammarDir, 0755))

	ctx := context.Background()
	libPath, err := d.downloadPrebuilt(ctx, "test", server.URL)
	require.NoError(t, err)
	assert.FileExists(t, libPath)

	content, err := os.ReadFile(libPath)
	require.NoError(t, err)
	assert.Equal(t, "fake library content", string(content))
}

func TestDownloadPrebuiltNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	ctx := context.Background()
	_, err := d.downloadPrebuilt(ctx, "test", server.URL)
	assert.Error(t, err)
}

func TestDownloadPrebuiltContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.downloadPrebuilt(ctx, "test", server.URL)
	assert.Error(t, err)
}

func TestDownloadWithContextCached(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	grammarDir := filepath.Join(tmpDir, "cached")
	require.NoError(t, os.MkdirAll(grammarDir, 0755))

	libPath := filepath.Join(grammarDir, grammarLibraryName("cached"))
	require.NoError(t, os.WriteFile(libPath, []byte("cached lib"), 0644))

	info := GrammarInfo{Name: "cached", Repository: "github.com/test/test"}
	ctx := context.Background()

	result, err := d.DownloadWithContext(ctx, info)
	require.NoError(t, err)
	assert.Equal(t, libPath, result)
}

func TestEnsureGrammarDir(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewGrammarDownloader(tmpDir)

	err := d.ensureGrammarDir("newgrammar")
	require.NoError(t, err)

	expectedDir := filepath.Join(tmpDir, "newgrammar")
	info, err := os.Stat(expectedDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	content := "test content"
	err := writeFile(path, strings.NewReader(content))
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestWriteFileError(t *testing.T) {
	err := writeFile("/nonexistent/path/file.txt", strings.NewReader("test"))
	assert.Error(t, err)
}

func BenchmarkPrebuiltURLs(b *testing.B) {
	d := NewGrammarDownloader("/grammars")
	info := GrammarInfo{
		Name:       "go",
		Repository: "github.com/tree-sitter/tree-sitter-go",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.prebuiltURLs(info)
	}
}
