package lsp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadTimeout is the max time for a single HTTP download.
// Derived from: largest LSP binary (~100MB haskell-language-server)
// at minimum 1MB/s = 100s; 5 minutes provides headroom.
const downloadTimeout = 5 * time.Minute

// maxDownloadBytes caps the download size to prevent unbounded disk usage.
// Derived from: haskell-language-server ~100MB, lua-language-server ~40MB;
// 512MB provides generous headroom.
const maxDownloadBytes = 512 * 1024 * 1024

// downloadFile fetches a URL and writes it to destPath.
// Returns the SHA-256 hex checksum of the downloaded file.
// Uses a temp file in the same directory for atomic writes.
func downloadFile(ctx context.Context, client *http.Client, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d for %s", resp.StatusCode, url)
	}

	return writeToFileAtomic(resp.Body, destPath)
}

// writeToFileAtomic streams r into destPath via a temp file, computing SHA-256.
func writeToFileAtomic(r io.Reader, destPath string) (string, error) {
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(r, maxDownloadBytes)
	written, err := io.Copy(tmp, io.TeeReader(limited, hasher))
	if err != nil {
		return "", fmt.Errorf("write download: %w", err)
	}
	if written >= maxDownloadBytes {
		return "", fmt.Errorf("download exceeds %d bytes", maxDownloadBytes)
	}

	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}
	tmpPath = "" // prevent deferred removal
	return checksum, nil
}

// extractTarGz extracts a .tar.gz file to destDir.
// Rejects paths containing ".." for path traversal protection.
func extractTarGz(src, destDir string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	return extractTar(tar.NewReader(gz), destDir)
}

// extractTar processes a tar reader, extracting files and directories.
func extractTar(tr *tar.Reader, destDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		target, err := sanitizePath(destDir, hdr.Name)
		if err != nil {
			continue // skip unsafe entries
		}

		if err := extractTarEntry(tr, hdr, target); err != nil {
			return err
		}
	}
}

// extractTarEntry writes a single tar entry to the filesystem.
func extractTarEntry(tr *tar.Reader, hdr *tar.Header, target string) error {
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0755)
	case tar.TypeReg:
		return writeFileFromReader(tr, target, hdr.FileInfo().Mode())
	default:
		return nil // skip symlinks, etc.
	}
}

// extractZip extracts a .zip file to destDir.
// Rejects paths containing ".." for path traversal protection.
func extractZip(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		target, err := sanitizePath(destDir, f.Name)
		if err != nil {
			continue // skip unsafe entries
		}
		if err := extractZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractZipEntry writes a single zip entry to the filesystem.
func extractZipEntry(f *zip.File, target string) error {
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	return writeFileFromReader(rc, target, f.Mode())
}

// extractGz decompresses a single-file .gz archive to destPath.
func extractGz(src, destPath string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open gz: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	return writeFileFromReader(gz, destPath, 0755)
}

// writeFileFromReader creates parent dirs and writes r to path with the given mode.
func writeFileFromReader(r io.Reader, path string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file %s: %w", path, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// sanitizePath validates and resolves a path within destDir.
// Returns an error if the path escapes destDir (path traversal).
func sanitizePath(destDir, name string) (string, error) {
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("unsafe path: %s", name)
	}
	cleaned := filepath.Clean(name)
	target := filepath.Join(destDir, cleaned)
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes dest: %s", name)
	}
	return target, nil
}

// makeExecutable sets the file to mode 0755.
func makeExecutable(path string) error {
	return os.Chmod(path, 0755)
}

// computeSHA256 returns the hex-encoded SHA-256 hash of a file.
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
