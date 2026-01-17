package treesitter

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLibraryName(t *testing.T) {
	name := libraryName()
	switch runtime.GOOS {
	case "darwin":
		assert.Equal(t, "libtree-sitter.dylib", name)
	case "windows":
		assert.Equal(t, "tree-sitter.dll", name)
	default:
		assert.Equal(t, "libtree-sitter.so", name)
	}
}

func TestBuildSearchPaths(t *testing.T) {
	paths := buildSearchPaths()
	assert.NotEmpty(t, paths)

	// Should contain sylk data dir path
	dataDir := getSylkDataDir()
	if dataDir != "" {
		expectedPath := filepath.Join(dataDir, "lib", libraryName())
		assert.Contains(t, paths, expectedPath)
	}

	// Should contain standard system paths
	assert.Contains(t, paths, "/usr/local/lib/"+libraryName())
	assert.Contains(t, paths, "/usr/lib/"+libraryName())

	// Platform-specific paths
	if runtime.GOOS == "darwin" {
		assert.Contains(t, paths, "/opt/homebrew/lib/"+libraryName())
	}
}

func TestGetSylkDataDir(t *testing.T) {
	// Test with XDG_DATA_HOME set
	t.Run("with XDG_DATA_HOME", func(t *testing.T) {
		originalXDG := os.Getenv("XDG_DATA_HOME")
		defer os.Setenv("XDG_DATA_HOME", originalXDG)

		os.Setenv("XDG_DATA_HOME", "/custom/data")
		result := getSylkDataDir()
		assert.Equal(t, "/custom/data/sylk", result)
	})

	// Test without XDG_DATA_HOME
	t.Run("without XDG_DATA_HOME", func(t *testing.T) {
		originalXDG := os.Getenv("XDG_DATA_HOME")
		defer os.Setenv("XDG_DATA_HOME", originalXDG)

		os.Setenv("XDG_DATA_HOME", "")
		result := getSylkDataDir()
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "sylk")
	})
}

func TestFileExists(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.txt")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		assert.True(t, fileExists(tmpFile.Name()))
	})

	t.Run("non-existing file", func(t *testing.T) {
		assert.False(t, fileExists("/non/existent/path/file.txt"))
	})
}

func TestCStringToGo(t *testing.T) {
	t.Run("nil pointer", func(t *testing.T) {
		result := cStringToGo(nil)
		assert.Equal(t, "", result)
	})

	t.Run("empty string", func(t *testing.T) {
		data := []byte{0}
		result := cStringToGo(&data[0])
		assert.Equal(t, "", result)
	})

	t.Run("valid string", func(t *testing.T) {
		data := []byte{'h', 'e', 'l', 'l', 'o', 0}
		result := cStringToGo(&data[0])
		assert.Equal(t, "hello", result)
	})

	t.Run("unicode string", func(t *testing.T) {
		data := []byte("test\x00")
		result := cStringToGo(&data[0])
		assert.Equal(t, "test", result)
	})
}

func TestCStringToGoN(t *testing.T) {
	t.Run("nil pointer", func(t *testing.T) {
		result := cStringToGoN(nil, 5)
		assert.Equal(t, "", result)
	})

	t.Run("zero length", func(t *testing.T) {
		data := []byte{'h', 'e', 'l', 'l', 'o'}
		result := cStringToGoN(&data[0], 0)
		assert.Equal(t, "", result)
	})

	t.Run("valid string", func(t *testing.T) {
		data := []byte{'h', 'e', 'l', 'l', 'o'}
		result := cStringToGoN(&data[0], 5)
		assert.Equal(t, "hello", result)
	})

	t.Run("partial string", func(t *testing.T) {
		data := []byte{'h', 'e', 'l', 'l', 'o'}
		result := cStringToGoN(&data[0], 3)
		assert.Equal(t, "hel", result)
	})
}

func TestAddOffset(t *testing.T) {
	data := []byte{'a', 'b', 'c', 'd', 'e'}

	t.Run("zero offset", func(t *testing.T) {
		result := addOffset(&data[0], 0)
		assert.Equal(t, byte('a'), *result)
	})

	t.Run("positive offset", func(t *testing.T) {
		result := addOffset(&data[0], 2)
		assert.Equal(t, byte('c'), *result)
	})
}

func TestTSPointZeroValue(t *testing.T) {
	var point TSPoint
	assert.Equal(t, uint32(0), point.Row)
	assert.Equal(t, uint32(0), point.Column)
}

func TestTSNodeZeroValue(t *testing.T) {
	var node TSNode
	assert.Equal(t, [4]uint32{0, 0, 0, 0}, node.Context)
	assert.Equal(t, uintptr(0), node.ID)
	assert.Equal(t, uintptr(0), node.Tree)
}

func TestTSInputEditZeroValue(t *testing.T) {
	var edit TSInputEdit
	assert.Equal(t, uint32(0), edit.StartByte)
	assert.Equal(t, uint32(0), edit.OldEndByte)
	assert.Equal(t, uint32(0), edit.NewEndByte)
}

func TestTSQueryMatchZeroValue(t *testing.T) {
	var match TSQueryMatch
	assert.Equal(t, uint32(0), match.ID)
	assert.Equal(t, uint16(0), match.PatternIndex)
	assert.Equal(t, uint16(0), match.CaptureCount)
}

func TestIsInitialized(t *testing.T) {
	// Before initialization, should be false (unless already initialized)
	// This test is somewhat limited without the actual library
	initialized := IsInitialized()
	if libHandle == 0 {
		assert.False(t, initialized)
	}
}

func TestFindTreeSitterLibrary(t *testing.T) {
	// This test checks the function runs without panic
	// The result depends on whether tree-sitter is installed
	path := findTreeSitterLibrary()
	// path may be empty if tree-sitter is not installed
	if path != "" {
		assert.True(t, fileExists(path))
	}
}

// Integration tests that require the library to be installed
func TestInitializeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	err := Initialize()
	if err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	assert.True(t, IsInitialized())
}

func TestParserOperationsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	err := Initialize()
	if err != nil {
		t.Skipf("tree-sitter library not available: %v", err)
	}

	parser := ParserNew()
	require.NotEqual(t, TSParser(0), parser)

	// Clean up
	ParserDelete(parser)
}

// Test edge cases
func TestNodeChildByFieldNameEmpty(t *testing.T) {
	var node TSNode
	result := NodeChildByFieldName(node, "")
	assert.Equal(t, TSNode{}, result)
}

func TestQueryNewEmptyPattern(t *testing.T) {
	// This should return an error
	_, _, _, err := QueryNew(0, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty query pattern")
}

// Benchmark tests
func BenchmarkCStringToGo(b *testing.B) {
	data := []byte("this is a test string with some length\x00")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cStringToGo(&data[0])
	}
}

func BenchmarkCStringToGoN(b *testing.B) {
	data := []byte("this is a test string with some length")
	length := len(data)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cStringToGoN(&data[0], length)
	}
}

func BenchmarkAddOffset(b *testing.B) {
	data := []byte("abcdefghij")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = addOffset(&data[0], i%10)
	}
}
