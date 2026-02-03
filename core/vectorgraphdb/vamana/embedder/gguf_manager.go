//go:build gguf

package embedder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type GGUFManager struct {
	cacheDir string
}

func NewGGUFManager(cacheDir string) *GGUFManager {
	return &GGUFManager{cacheDir: cacheDir}
}

func (m *GGUFManager) EnsureModel(ctx context.Context, tier ModelTier) (string, error) {
	spec := tier.Spec()
	if spec.Format != FormatGGUF {
		return "", fmt.Errorf("tier %s does not use GGUF format", tier)
	}

	modelPath := filepath.Join(m.cacheDir, spec.GGUFFile)

	if _, err := os.Stat(modelPath); err == nil {
		return modelPath, nil
	}

	if err := os.MkdirAll(m.cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	url := m.buildDownloadURL(spec)
	if err := m.downloadModel(ctx, url, modelPath); err != nil {
		return "", fmt.Errorf("failed to download model: %w", err)
	}

	return modelPath, nil
}

func (m *GGUFManager) buildDownloadURL(spec ModelSpec) string {
	return fmt.Sprintf(
		"https://huggingface.co/%s/resolve/main/%s",
		spec.HFRepo,
		spec.GGUFFile,
	)
}

func (m *GGUFManager) downloadModel(ctx context.Context, url, destPath string) error {
	tmpPath := destPath + ".tmp"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write model file: %w", err)
	}

	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (m *GGUFManager) ModelPath(tier ModelTier) string {
	spec := tier.Spec()
	return filepath.Join(m.cacheDir, spec.GGUFFile)
}

func (m *GGUFManager) IsModelCached(tier ModelTier) bool {
	path := m.ModelPath(tier)
	_, err := os.Stat(path)
	return err == nil
}

func (m *GGUFManager) CacheDir() string {
	return m.cacheDir
}
