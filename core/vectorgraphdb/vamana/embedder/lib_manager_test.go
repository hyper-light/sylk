package embedder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryManager_URLs(t *testing.T) {
	tokURL, err := tokenizerDownloadURL()
	if err != nil {
		if !IsPlatformSupported() {
			t.Skipf("platform not supported: %s", PlatformDescription())
		}
		t.Fatalf("tokenizerDownloadURL: %v", err)
	}
	t.Logf("Tokenizer URL: %s", tokURL)

	ortURL, isZip, err := onnxRuntimeDownloadURL()
	if err != nil {
		t.Fatalf("onnxRuntimeDownloadURL: %v", err)
	}
	t.Logf("ONNX Runtime URL: %s (zip=%v)", ortURL, isZip)

	t.Logf("Tokenizer lib name: %s", tokenizerLibraryName())
	t.Logf("ONNX Runtime lib name: %s", onnxRuntimeLibraryName())
}

func TestLibraryManager_PlatformSupport(t *testing.T) {
	supported := IsPlatformSupported()
	t.Logf("Platform: %s, supported: %v", PlatformDescription(), supported)

	if !supported {
		t.Skip("platform not supported for ONNX embedding")
	}
}

func TestLibraryManager_EnsureLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping library download test in short mode")
	}

	if !IsPlatformSupported() {
		t.Skipf("platform not supported: %s", PlatformDescription())
	}

	tmpDir := t.TempDir()
	mgr, err := NewLibraryManagerWithPath(tmpDir)
	if err != nil {
		t.Fatalf("NewLibraryManagerWithPath: %v", err)
	}

	t.Log("Downloading libraries (this may take a minute)...")
	paths, err := mgr.EnsureLibraries()
	if err != nil {
		t.Fatalf("EnsureLibraries: %v", err)
	}

	t.Logf("Tokenizers path: %s", paths.TokenizersPath)
	t.Logf("ONNX Runtime path: %s", paths.ONNXRuntimePath)
	t.Logf("ONNX library name: %s", paths.ONNXLibraryName)

	if _, err := os.Stat(paths.TokenizersPath); err != nil {
		t.Errorf("tokenizers library not found: %v", err)
	}

	ortLibPath := filepath.Join(paths.ONNXRuntimePath, paths.ONNXLibraryName)
	if _, err := os.Stat(ortLibPath); err != nil {
		t.Errorf("ONNX runtime library not found at %s: %v", ortLibPath, err)
	}

	entries, _ := os.ReadDir(tmpDir)
	t.Logf("Downloaded files:")
	for _, e := range entries {
		info, _ := e.Info()
		t.Logf("  %s (%d bytes)", e.Name(), info.Size())
	}
}

func TestLibraryManager_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping library download test in short mode")
	}

	if !IsPlatformSupported() {
		t.Skipf("platform not supported: %s", PlatformDescription())
	}

	tmpDir := t.TempDir()
	mgr, err := NewLibraryManagerWithPath(tmpDir)
	if err != nil {
		t.Fatalf("NewLibraryManagerWithPath: %v", err)
	}

	paths1, err := mgr.EnsureLibraries()
	if err != nil {
		t.Fatalf("first EnsureLibraries: %v", err)
	}

	paths2, err := mgr.EnsureLibraries()
	if err != nil {
		t.Fatalf("second EnsureLibraries: %v", err)
	}

	if paths1.TokenizersPath != paths2.TokenizersPath {
		t.Error("tokenizers path changed between calls")
	}

	if paths1.ONNXRuntimePath != paths2.ONNXRuntimePath {
		t.Error("ONNX runtime path changed between calls")
	}

	t.Log("Idempotent: second call used cached libraries")
}
