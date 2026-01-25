package embedder

import (
	"fmt"
	"os"
	"path/filepath"
)

type SetupResult struct {
	LibDir          string
	TokenizersPath  string
	ONNXRuntimePath string
	CGOLDFlags      string
	LDLibraryPath   string
	Ready           bool
}

func Setup() (*SetupResult, error) {
	if !IsPlatformSupported() {
		return nil, fmt.Errorf("platform %s not supported for ONNX embedding", PlatformDescription())
	}

	mgr, err := NewLibraryManager()
	if err != nil {
		return nil, fmt.Errorf("create library manager: %w", err)
	}

	fmt.Println("Downloading native libraries for ONNX embedding...")
	fmt.Printf("Platform: %s\n", PlatformDescription())
	fmt.Printf("Target directory: %s\n", mgr.LibDir())

	paths, err := mgr.EnsureLibraries()
	if err != nil {
		return nil, fmt.Errorf("download libraries: %w", err)
	}

	result := &SetupResult{
		LibDir:          mgr.LibDir(),
		TokenizersPath:  paths.TokenizersPath,
		ONNXRuntimePath: paths.ONNXRuntimePath,
		CGOLDFlags:      fmt.Sprintf("-L%s", mgr.LibDir()),
		LDLibraryPath:   mgr.LibDir(),
		Ready:           true,
	}

	fmt.Println("\nLibraries downloaded successfully!")
	fmt.Printf("  Tokenizers: %s\n", paths.TokenizersPath)
	fmt.Printf("  ONNX Runtime: %s/%s\n", paths.ONNXRuntimePath, paths.ONNXLibraryName)

	return result, nil
}

func SetupInstructions() string {
	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".sylk", "lib")

	return fmt.Sprintf(`
ONNX Embedding Setup Instructions
==================================

1. Run setup to download native libraries:
   go run ./cmd/sylk-setup

2. Build with ORT support:
   CGO_LDFLAGS="-L%s" go build -tags ORT ./...

3. Run with library path:
   LD_LIBRARY_PATH=%s ./sylk

Or add to your shell profile:
   export CGO_LDFLAGS="-L%s"
   export LD_LIBRARY_PATH="%s:$LD_LIBRARY_PATH"
`, libDir, libDir, libDir, libDir)
}

func CheckSetup() (*SetupResult, error) {
	mgr, err := NewLibraryManager()
	if err != nil {
		return nil, err
	}

	result := &SetupResult{
		LibDir: mgr.LibDir(),
	}

	tokPath := filepath.Join(mgr.LibDir(), tokenizerLibraryName())
	if _, err := os.Stat(tokPath); err == nil {
		result.TokenizersPath = tokPath
	}

	ortPath := filepath.Join(mgr.LibDir(), onnxRuntimeLibraryName())
	if _, err := os.Stat(ortPath); err == nil {
		result.ONNXRuntimePath = mgr.LibDir()
	}

	result.Ready = result.TokenizersPath != "" && result.ONNXRuntimePath != ""

	if result.Ready {
		result.CGOLDFlags = fmt.Sprintf("-L%s", mgr.LibDir())
		result.LDLibraryPath = mgr.LibDir()
	}

	return result, nil
}
