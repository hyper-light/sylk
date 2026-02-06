package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/detect"
)

// ProvisionTimeout bounds the total provisioning duration.
// Derived from: ~10 npm installs at ~60-90s + GitHub downloads ~30s each;
// 10 minutes provides ample headroom.
const ProvisionTimeout = 10 * time.Minute

// maxConcurrentInstalls bounds parallel downloads.
// Derived from: npm is I/O bound, GitHub is network bound;
// 4 balances throughput with resource consumption.
const maxConcurrentInstalls = 4

// Installer manages automatic LSP server provisioning.
type Installer struct {
	baseDir    string
	binDir     string
	manifest   *Manifest
	httpClient *http.Client
}

// NewInstaller creates an Installer rooted at ~/.sylk/lsp.
func NewInstaller() (*Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	baseDir := filepath.Join(home, ".sylk", "lsp")
	binDir := filepath.Join(baseDir, "bin")
	for _, dir := range []string{baseDir, binDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	m := NewManifest(baseDir)
	if err := m.Load(); err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	return &Installer{
		baseDir:    baseDir,
		binDir:     binDir,
		manifest:   m,
		httpClient: &http.Client{Timeout: downloadTimeout},
	}, nil
}

// Provision discovers which LSP servers a project needs and installs any
// that are missing from both the system PATH and the managed bin directory.
func (inst *Installer) Provision(ctx context.Context, projectRoot string) error {
	needed, err := DiscoverNeededServers(ctx, projectRoot)
	if err != nil {
		return fmt.Errorf("discover servers: %w", err)
	}

	missing := inst.filterMissing(needed)
	if len(missing) == 0 {
		return nil
	}

	return inst.installAll(ctx, missing)
}

// EnsureServer installs a single server if not already available.
func (inst *Installer) EnsureServer(ctx context.Context, id ServerID) error {
	def := findDefinition(id)
	if def == nil {
		return fmt.Errorf("unknown server: %s", id)
	}
	if def.AutoDownload == nil {
		return fmt.Errorf("no auto-download config for %s", id)
	}
	return inst.installOne(ctx, def)
}

// ---------------------------------------------------------------------------
// Filtering
// ---------------------------------------------------------------------------

// filterMissing returns definitions for servers that need installation.
func (inst *Installer) filterMissing(needed map[ServerID]bool) []*LanguageServerDefinition {
	var missing []*LanguageServerDefinition
	for id := range needed {
		def := findDefinition(id)
		if def == nil || def.AutoDownload == nil {
			continue
		}
		if inst.isInstallable(def) {
			missing = append(missing, def)
		}
	}
	return missing
}

// isInstallable returns true if the server has auto-download config,
// is not system-only, and its binary is not already available.
func (inst *Installer) isInstallable(def *LanguageServerDefinition) bool {
	if def.AutoDownload.Source == SourceSystem {
		return false
	}
	binary := def.AutoDownload.Binary
	return detect.Which(binary) == "" && !isBinaryManaged(binary)
}

// ---------------------------------------------------------------------------
// Parallel installation
// ---------------------------------------------------------------------------

// installAll installs all given servers with bounded concurrency.
func (inst *Installer) installAll(ctx context.Context, defs []*LanguageServerDefinition) error {
	sem := make(chan struct{}, maxConcurrentInstalls)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, def := range defs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(d *LanguageServerDefinition) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := inst.installOne(ctx, d); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", d.ID, err))
				mu.Unlock()
			}
		}(def)
	}

	wg.Wait()
	return errors.Join(errs...)
}

// installOne dispatches to the appropriate source handler.
func (inst *Installer) installOne(ctx context.Context, def *LanguageServerDefinition) error {
	handlers := map[string]func(context.Context, *AutoDownloadConfig) error{
		SourceNPM:    inst.installNPM,
		SourceGo:     inst.installGo,
		SourceGitHub: inst.installGitHub,
		SourceGem:    inst.installGem,
	}

	handler, ok := handlers[def.AutoDownload.Source]
	if !ok {
		return fmt.Errorf("unsupported source: %s", def.AutoDownload.Source)
	}
	if err := handler(ctx, def.AutoDownload); err != nil {
		return err
	}

	inst.recordInstall(def.ID, def.AutoDownload)
	return nil
}

// recordInstall updates the manifest after a successful install.
func (inst *Installer) recordInstall(id ServerID, cfg *AutoDownloadConfig) {
	binaryPath := filepath.Join(inst.binDir, cfg.Binary)
	checksum, _ := computeSHA256(binaryPath)

	inst.manifest.Set(&InstalledServer{
		ServerID:    id,
		Version:     "latest",
		Source:      cfg.Source,
		BinaryPath:  binaryPath,
		InstalledAt: time.Now(),
		Checksum:    checksum,
	})

	_ = inst.manifest.Save()
	detect.ClearCache()
}

// ---------------------------------------------------------------------------
// npm installation
// ---------------------------------------------------------------------------

// sharedNPMPackages maps npm package names to all binaries they provide.
// Used to deduplicate installs (e.g., vscode-langservers-extracted → 3 servers).
var sharedNPMPackages = map[string][]string{
	"vscode-langservers-extracted": {
		"vscode-html-language-server",
		"vscode-css-language-server",
		"vscode-json-language-server",
	},
}

func (inst *Installer) installNPM(ctx context.Context, cfg *AutoDownloadConfig) error {
	if detect.Which("npm") == "" {
		return fmt.Errorf("npm not found on PATH")
	}

	prefixDir := filepath.Join(inst.baseDir, "npm", sanitizePackageName(cfg.Package))
	cmd := exec.CommandContext(ctx, "npm", "install", "--prefix", prefixDir, cfg.Package)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install %s: %w", cfg.Package, err)
	}

	return inst.symlinkNPMBinaries(prefixDir, cfg)
}

// symlinkNPMBinaries creates symlinks from the npm bin stubs to the managed bin dir.
// Handles shared packages that provide multiple binaries.
func (inst *Installer) symlinkNPMBinaries(prefixDir string, cfg *AutoDownloadConfig) error {
	binaries := sharedNPMPackages[cfg.Package]
	if binaries == nil {
		binaries = []string{cfg.Binary}
	}

	for _, bin := range binaries {
		src := filepath.Join(prefixDir, "node_modules", ".bin", bin)
		dst := filepath.Join(inst.binDir, bin)
		os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlink %s: %w", bin, err)
		}
	}
	return nil
}

// sanitizePackageName converts scoped npm names to filesystem-safe names.
// e.g., "@biomejs/biome" → "biomejs-biome"
func sanitizePackageName(pkg string) string {
	return strings.NewReplacer("/", "-", "@", "").Replace(pkg)
}

// ---------------------------------------------------------------------------
// Go installation
// ---------------------------------------------------------------------------

func (inst *Installer) installGo(ctx context.Context, cfg *AutoDownloadConfig) error {
	goPath := detect.Which("go")
	if goPath == "" {
		return fmt.Errorf("go not found on PATH")
	}

	cmd := exec.CommandContext(ctx, goPath, "install", cfg.Package)
	cmd.Env = append(os.Environ(), "GOBIN="+inst.binDir)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s: %w", cfg.Package, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GitHub release installation
// ---------------------------------------------------------------------------

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (inst *Installer) installGitHub(ctx context.Context, cfg *AutoDownloadConfig) error {
	release, err := inst.fetchLatestRelease(ctx, cfg.Owner, cfg.Repo)
	if err != nil {
		return err
	}

	asset := matchPlatformAsset(release.Assets)
	if asset == nil {
		return fmt.Errorf("no matching asset for %s/%s on %s/%s",
			cfg.Owner, cfg.Repo, runtime.GOOS, runtime.GOARCH)
	}

	return inst.downloadAndExtract(ctx, asset, cfg.Binary)
}

func (inst *Installer) fetchLatestRelease(ctx context.Context, owner, repo string) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := inst.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API %d for %s/%s", resp.StatusCode, owner, repo)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &release, nil
}

// ---------------------------------------------------------------------------
// Platform asset matching
// ---------------------------------------------------------------------------

type platformPattern struct {
	osNames   []string
	archNames []string
}

// platformPatterns maps GOOS/GOARCH to name fragments used in GitHub release assets.
var platformPatterns = map[string]platformPattern{
	"linux/amd64":  {osNames: []string{"linux"}, archNames: []string{"x86_64", "amd64", "x64"}},
	"linux/arm64":  {osNames: []string{"linux"}, archNames: []string{"aarch64", "arm64"}},
	"darwin/amd64": {osNames: []string{"darwin", "macos", "apple"}, archNames: []string{"x86_64", "amd64", "x64"}},
	"darwin/arm64": {osNames: []string{"darwin", "macos", "apple"}, archNames: []string{"aarch64", "arm64"}},
}

// matchPlatformAsset finds the asset matching the current OS and architecture.
// Falls back to a single-asset release if no platform-specific match is found.
func matchPlatformAsset(assets []githubAsset) *githubAsset {
	pattern := platformPatterns[runtime.GOOS+"/"+runtime.GOARCH]

	for i := range assets {
		if matchesPattern(assets[i].Name, pattern) {
			return &assets[i]
		}
	}

	return singleDownloadableAsset(assets)
}

func matchesPattern(name string, p platformPattern) bool {
	lower := strings.ToLower(name)
	return containsAny(lower, p.osNames) && containsAny(lower, p.archNames)
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// singleDownloadableAsset returns the asset if there is exactly one
// downloadable archive (handles platform-independent packages).
func singleDownloadableAsset(assets []githubAsset) *githubAsset {
	var candidate *githubAsset
	count := 0
	for i := range assets {
		if isDownloadableAsset(assets[i].Name) {
			candidate = &assets[i]
			count++
		}
	}
	if count == 1 {
		return candidate
	}
	return nil
}

// skipSuffixes are file extensions that are never the primary downloadable asset.
var skipSuffixes = []string{".txt", ".md", ".sha256", ".asc", ".sig", ".json"}

func isDownloadableAsset(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range skipSuffixes {
		if strings.HasSuffix(lower, s) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Download and extraction
// ---------------------------------------------------------------------------

func (inst *Installer) downloadAndExtract(ctx context.Context, asset *githubAsset, binary string) error {
	tmpDir, err := os.MkdirTemp(inst.baseDir, ".download-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, asset.Name)
	if _, err := downloadFile(ctx, inst.httpClient, asset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("mkdir extract: %w", err)
	}

	if err := extractToDir(archivePath, extractDir, binary); err != nil {
		return err
	}

	return inst.placeBinary(extractDir, binary)
}

// archiveHandler maps a file suffix to its extraction function.
type archiveHandler struct {
	suffix  string
	extract func(string, string) error
}

var archiveHandlers = []archiveHandler{
	{".tar.gz", extractTarGz},
	{".tgz", extractTarGz},
	{".zip", extractZip},
}

// extractToDir extracts the archive into extractDir.
// For .gz (non-tar), extracts the single file named as binary.
// For unrecognized formats, copies the file as-is (raw binary).
func extractToDir(archivePath, extractDir, binary string) error {
	lower := strings.ToLower(archivePath)
	for _, h := range archiveHandlers {
		if strings.HasSuffix(lower, h.suffix) {
			return h.extract(archivePath, extractDir)
		}
	}
	if strings.HasSuffix(lower, ".gz") {
		return extractGz(archivePath, filepath.Join(extractDir, binary))
	}
	return copyFile(archivePath, filepath.Join(extractDir, binary))
}

// ---------------------------------------------------------------------------
// Binary placement
// ---------------------------------------------------------------------------

func (inst *Installer) placeBinary(extractDir, binary string) error {
	src, err := findBinary(extractDir, binary)
	if err != nil {
		return err
	}
	dst := filepath.Join(inst.binDir, binary)
	if err := moveFile(src, dst); err != nil {
		return err
	}
	return makeExecutable(dst)
}

// findBinary walks extractDir looking for a file named binary.
func findBinary(dir, name string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path) == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", dir, err)
	}
	if found == "" {
		return "", fmt.Errorf("binary %q not found in archive", name)
	}
	return found, nil
}

// moveFile tries os.Rename, falling back to copy for cross-device moves.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Gem installation
// ---------------------------------------------------------------------------

func (inst *Installer) installGem(ctx context.Context, cfg *AutoDownloadConfig) error {
	if detect.Which("gem") == "" {
		return fmt.Errorf("gem not found on PATH")
	}

	gemDir := filepath.Join(inst.baseDir, "gem", cfg.Package)
	cmd := exec.CommandContext(ctx, "gem", "install",
		"--install-dir", gemDir,
		"--bindir", inst.binDir,
		"--no-document",
		cfg.Package,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gem install %s: %w", cfg.Package, err)
	}
	return nil
}
