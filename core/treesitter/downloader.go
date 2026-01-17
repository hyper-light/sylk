package treesitter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type GrammarDownloader struct {
	grammarDir    string
	httpClient    *http.Client
	compilerPaths []string
}

type DownloadResult struct {
	LibPath   string
	Source    string
	Duration  time.Duration
	FromCache bool
}

func NewGrammarDownloader(grammarDir string) *GrammarDownloader {
	return &GrammarDownloader{
		grammarDir: grammarDir,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		compilerPaths: []string{
			"cc", "clang", "gcc",
		},
	}
}

func (d *GrammarDownloader) Download(info GrammarInfo) (string, error) {
	return d.DownloadWithContext(context.Background(), info)
}

func (d *GrammarDownloader) DownloadWithContext(ctx context.Context, info GrammarInfo) (string, error) {
	libPath := d.localLibraryPath(info.Name)
	if fileExists(libPath) {
		return libPath, nil
	}

	if err := d.ensureGrammarDir(info.Name); err != nil {
		return "", err
	}

	return d.downloadOrCompile(ctx, info)
}

func (d *GrammarDownloader) localLibraryPath(name string) string {
	return filepath.Join(d.grammarDir, name, grammarLibraryName(name))
}

func (d *GrammarDownloader) ensureGrammarDir(name string) error {
	dir := filepath.Join(d.grammarDir, name)
	return os.MkdirAll(dir, 0755)
}

func (d *GrammarDownloader) downloadOrCompile(ctx context.Context, info GrammarInfo) (string, error) {
	libPath, err := d.tryPrebuiltDownload(ctx, info)
	if err == nil {
		return libPath, nil
	}

	return d.compileFromSource(ctx, info)
}

func (d *GrammarDownloader) tryPrebuiltDownload(ctx context.Context, info GrammarInfo) (string, error) {
	urls := d.prebuiltURLs(info)
	for _, url := range urls {
		libPath, err := d.downloadPrebuilt(ctx, info.Name, url)
		if err == nil {
			return libPath, nil
		}
	}
	return "", ErrPrebuiltNotAvailable
}

func (d *GrammarDownloader) prebuiltURLs(info GrammarInfo) []string {
	repo := info.Repository
	name := info.Name
	ext := libraryExtension()
	arch := runtime.GOARCH
	os := runtime.GOOS

	return []string{
		prebuiltReleaseURL(repo, name, os, arch, ext),
		prebuiltReleaseURLAlt(repo, name, os, arch, ext),
	}
}

func prebuiltReleaseURL(repo, name, osName, arch, ext string) string {
	return fmt.Sprintf("https://%s/releases/latest/download/libtree-sitter-%s-%s-%s%s",
		repo, name, osName, arch, ext)
}

func prebuiltReleaseURLAlt(repo, name, osName, arch, ext string) string {
	return fmt.Sprintf("https://%s/releases/download/latest/libtree-sitter-%s-%s-%s%s",
		repo, name, osName, arch, ext)
}

func (d *GrammarDownloader) downloadPrebuilt(ctx context.Context, name, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}

	return d.saveLibrary(name, resp.Body)
}

func (d *GrammarDownloader) saveLibrary(name string, reader io.Reader) (string, error) {
	libPath := d.localLibraryPath(name)
	return libPath, writeFile(libPath, reader)
}

func writeFile(path string, reader io.Reader) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	return err
}

func (d *GrammarDownloader) compileFromSource(ctx context.Context, info GrammarInfo) (string, error) {
	compiler := d.findCompiler()
	if compiler == "" {
		return "", ErrCompilerNotFound
	}

	sourceDir, err := d.cloneRepository(ctx, info)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(sourceDir)

	return d.compile(ctx, info.Name, sourceDir, compiler)
}

func (d *GrammarDownloader) findCompiler() string {
	for _, name := range d.compilerPaths {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func (d *GrammarDownloader) cloneRepository(ctx context.Context, info GrammarInfo) (string, error) {
	tmpDir, err := os.MkdirTemp("", "ts-grammar-*")
	if err != nil {
		return "", err
	}

	repoURL := formatRepoURL(info.Repository)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", repoURL, tmpDir)
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("git clone failed: %w", err)
	}

	return tmpDir, nil
}

func formatRepoURL(repo string) string {
	if strings.HasPrefix(repo, "https://") {
		return repo
	}
	return "https://" + repo + ".git"
}

func (d *GrammarDownloader) compile(ctx context.Context, name, sourceDir, compiler string) (string, error) {
	srcPath := findParserSource(sourceDir)
	if srcPath == "" {
		return "", ErrParserSourceNotFound
	}

	libPath := d.localLibraryPath(name)
	args := compileArgs(srcPath, libPath)

	cmd := exec.CommandContext(ctx, compiler, args...)
	cmd.Dir = sourceDir

	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("compile failed: %s: %w", string(output), err)
	}

	return libPath, nil
}

func findParserSource(sourceDir string) string {
	candidates := []string{
		filepath.Join(sourceDir, "src", "parser.c"),
		filepath.Join(sourceDir, "parser.c"),
	}
	for _, path := range candidates {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func compileArgs(srcPath, libPath string) []string {
	base := []string{
		"-shared",
		"-fPIC",
		"-O2",
		"-I" + filepath.Dir(srcPath),
		"-o", libPath,
		srcPath,
	}
	return appendScannerIfExists(base, srcPath)
}

func appendScannerIfExists(args []string, srcPath string) []string {
	scannerPath := filepath.Join(filepath.Dir(srcPath), "scanner.c")
	if fileExists(scannerPath) {
		return append(args, scannerPath)
	}
	return args
}

func (d *GrammarDownloader) Remove(name string) error {
	dir := filepath.Join(d.grammarDir, name)
	return os.RemoveAll(dir)
}

func (d *GrammarDownloader) IsInstalled(name string) bool {
	return fileExists(d.localLibraryPath(name))
}

func (d *GrammarDownloader) ListInstalled() ([]string, error) {
	entries, err := os.ReadDir(d.grammarDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return filterInstalled(entries, d.grammarDir), nil
}

func filterInstalled(entries []os.DirEntry, grammarDir string) []string {
	var installed []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		libPath := filepath.Join(grammarDir, name, grammarLibraryName(name))
		if fileExists(libPath) {
			installed = append(installed, name)
		}
	}
	return installed
}

func (d *GrammarDownloader) GrammarDir() string {
	return d.grammarDir
}

var (
	ErrPrebuiltNotAvailable = &downloadError{msg: "prebuilt binary not available"}
	ErrCompilerNotFound     = &downloadError{msg: "no C compiler found (need cc, clang, or gcc)"}
	ErrParserSourceNotFound = &downloadError{msg: "parser.c not found in repository"}
)

type downloadError struct {
	msg string
}

func (e *downloadError) Error() string {
	return e.msg
}
