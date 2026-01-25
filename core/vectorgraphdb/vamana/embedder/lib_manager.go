package embedder

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	TokenizersVersion  = "v1.24.0"
	ONNXRuntimeVersion = "1.23.2"
	TokenizersBaseURL  = "https://github.com/daulet/tokenizers/releases/download"
	ONNXRuntimeBaseURL = "https://github.com/microsoft/onnxruntime/releases/download"
)

type LibraryManager struct {
	libDir string
	mu     sync.Mutex
}

type LibraryPaths struct {
	TokenizersPath  string
	ONNXRuntimePath string
	ONNXLibraryName string
}

func NewLibraryManager() (*LibraryManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	libDir := filepath.Join(home, ".sylk", "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return nil, fmt.Errorf("create lib dir: %w", err)
	}

	return &LibraryManager{libDir: libDir}, nil
}

func NewLibraryManagerWithPath(libDir string) (*LibraryManager, error) {
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return nil, fmt.Errorf("create lib dir: %w", err)
	}
	return &LibraryManager{libDir: libDir}, nil
}

func (m *LibraryManager) EnsureLibraries() (*LibraryPaths, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	paths := &LibraryPaths{}

	tokPath, err := m.ensureTokenizers()
	if err != nil {
		return nil, fmt.Errorf("ensure tokenizers: %w", err)
	}
	paths.TokenizersPath = tokPath

	ortPath, libName, err := m.ensureONNXRuntime()
	if err != nil {
		return nil, fmt.Errorf("ensure onnx runtime: %w", err)
	}
	paths.ONNXRuntimePath = ortPath
	paths.ONNXLibraryName = libName

	return paths, nil
}

func (m *LibraryManager) ensureTokenizers() (string, error) {
	libName := tokenizerLibraryName()
	libPath := filepath.Join(m.libDir, libName)

	if _, err := os.Stat(libPath); err == nil {
		return libPath, nil
	}

	url, err := tokenizerDownloadURL()
	if err != nil {
		return "", err
	}

	if err := m.downloadAndExtractTarGz(url, m.libDir, func(name string) bool {
		return strings.HasSuffix(name, libName)
	}); err != nil {
		return "", fmt.Errorf("download tokenizers: %w", err)
	}

	return libPath, nil
}

func (m *LibraryManager) ensureONNXRuntime() (string, string, error) {
	libName := onnxRuntimeLibraryName()
	libPath := filepath.Join(m.libDir, libName)

	if _, err := os.Stat(libPath); err == nil {
		return m.libDir, libName, nil
	}

	url, isZip, err := onnxRuntimeDownloadURL()
	if err != nil {
		return "", "", err
	}

	if isZip {
		if err := m.downloadAndExtractZip(url, m.libDir, func(name string) bool {
			return strings.Contains(name, "/lib/") && (strings.HasSuffix(name, ".dll") || strings.HasSuffix(name, ".lib"))
		}); err != nil {
			return "", "", fmt.Errorf("download onnx runtime: %w", err)
		}
	} else {
		if err := m.downloadAndExtractTarGz(url, m.libDir, func(name string) bool {
			return strings.Contains(name, "/lib/") && (strings.HasSuffix(name, ".so") || strings.HasSuffix(name, ".dylib") || strings.Contains(name, ".so."))
		}); err != nil {
			return "", "", fmt.Errorf("download onnx runtime: %w", err)
		}
	}

	if err := m.createONNXSymlinks(); err != nil {
		return "", "", fmt.Errorf("create symlinks: %w", err)
	}

	return m.libDir, libName, nil
}

func (m *LibraryManager) createONNXSymlinks() error {
	entries, err := os.ReadDir(m.libDir)
	if err != nil {
		return err
	}

	var versionedLib string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "libonnxruntime.so.") && !strings.HasSuffix(name, ".so") {
			if versionedLib == "" || len(name) > len(versionedLib) {
				versionedLib = name
			}
		}
		if strings.HasPrefix(name, "libonnxruntime.") && strings.HasSuffix(name, ".dylib") {
			versionedLib = name
		}
	}

	if versionedLib == "" {
		return nil
	}

	baseName := onnxRuntimeLibraryName()
	linkPath := filepath.Join(m.libDir, baseName)

	if _, err := os.Lstat(linkPath); err == nil {
		return nil
	}

	return os.Symlink(versionedLib, linkPath)
}

func (m *LibraryManager) downloadAndExtractTarGz(url, destDir string, filter func(string) bool) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		if !filter(header.Name) {
			continue
		}

		destPath := filepath.Join(destDir, filepath.Base(header.Name))
		if err := m.writeFile(destPath, tr, header.FileInfo().Mode()); err != nil {
			return err
		}
	}

	return nil
}

func (m *LibraryManager) downloadAndExtractZip(url, destDir string, filter func(string) bool) error {
	tmpFile, err := os.CreateTemp("", "onnxruntime-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("save zip: %w", err)
	}
	tmpFile.Close()

	zr, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		if !filter(f.Name) {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}

		destPath := filepath.Join(destDir, filepath.Base(f.Name))
		if err := m.writeFile(destPath, rc, f.Mode()); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}

	return nil
}

func (m *LibraryManager) writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file %s: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}

	return nil
}

func (m *LibraryManager) LibDir() string {
	return m.libDir
}

func tokenizerLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "tokenizers.lib"
	default:
		return "libtokenizers.a"
	}
}

func tokenizerDownloadURL() (string, error) {
	var platform string

	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			platform = "linux-amd64"
		case "arm64":
			platform = "linux-arm64"
		default:
			return "", fmt.Errorf("unsupported linux architecture: %s", runtime.GOARCH)
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			platform = "darwin-x86_64"
		case "arm64":
			platform = "darwin-arm64"
		default:
			return "", fmt.Errorf("unsupported darwin architecture: %s", runtime.GOARCH)
		}
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	return fmt.Sprintf("%s/%s/libtokenizers.%s.tar.gz", TokenizersBaseURL, TokenizersVersion, platform), nil
}

func onnxRuntimeLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}

func onnxRuntimeDownloadURL() (string, bool, error) {
	var platform string
	isZip := false

	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			platform = "linux-x64"
		case "arm64":
			platform = "linux-aarch64"
		default:
			return "", false, fmt.Errorf("unsupported linux architecture: %s", runtime.GOARCH)
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			platform = "osx-x86_64"
		case "arm64":
			platform = "osx-arm64"
		default:
			return "", false, fmt.Errorf("unsupported darwin architecture: %s", runtime.GOARCH)
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			platform = "win-x64"
			isZip = true
		default:
			return "", false, fmt.Errorf("unsupported windows architecture: %s", runtime.GOARCH)
		}
	default:
		return "", false, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	ext := "tgz"
	if isZip {
		ext = "zip"
	}

	url := fmt.Sprintf("%s/v%s/onnxruntime-%s-%s.%s",
		ONNXRuntimeBaseURL, ONNXRuntimeVersion, platform, ONNXRuntimeVersion, ext)

	return url, isZip, nil
}

func IsPlatformSupported() bool {
	switch runtime.GOOS {
	case "linux":
		return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
	case "darwin":
		return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
	case "windows":
		return runtime.GOARCH == "amd64"
	default:
		return false
	}
}

func PlatformDescription() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
