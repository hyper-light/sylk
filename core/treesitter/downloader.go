package treesitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DownloadPermission int

const (
	PermissionDenied DownloadPermission = iota
	PermissionGrantedOnce
	PermissionGrantedSession
	PermissionGrantedAlways
)

type PermissionRequest struct {
	Grammar     string
	Source      string
	Action      string
	Reason      string
	Checksum    string
	RequireUser bool
}

type PermissionResponse struct {
	Granted    bool
	Scope      DownloadPermission
	Reason     string
	ApprovedBy string
}

type PermissionCallback func(ctx context.Context, req PermissionRequest) (PermissionResponse, error)

type AuditEntry struct {
	Timestamp time.Time
	Action    string
	Grammar   string
	Source    string
	Result    string
	Checksum  string
	Error     string
}

type AuditCallback func(entry AuditEntry)

type ProxyConfig struct {
	Enabled  bool
	ProxyURL string
}

type GrammarDownloader struct {
	httpClient     *http.Client
	cacheDir       string
	buildDir       string
	baseURL        string
	checksums      map[string]string
	permissionCb   PermissionCallback
	auditCb        AuditCallback
	proxyConfig    ProxyConfig
	sessionPerms   map[string]DownloadPermission
	alwaysPerms    map[string]bool
	sandboxEnabled bool
	allowedDomains []string
}

type DownloaderOption func(*GrammarDownloader)

func WithHTTPClient(client *http.Client) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.httpClient = client
	}
}

func WithDownloadCacheDir(dir string) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.cacheDir = dir
	}
}

func WithDownloadBaseURL(url string) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.baseURL = url
	}
}

func WithPermissionCallback(cb PermissionCallback) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.permissionCb = cb
	}
}

func WithAuditCallback(cb AuditCallback) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.auditCb = cb
	}
}

func WithProxyConfig(cfg ProxyConfig) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.proxyConfig = cfg
		if cfg.Enabled && cfg.ProxyURL != "" {
			proxyURL := parseURL(cfg.ProxyURL)
			if proxyURL != nil {
				transport := &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
				}
				d.httpClient = &http.Client{
					Transport: transport,
					Timeout:   60 * time.Second,
				}
			}
		}
	}
}

func parseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	return u
}

func WithSandboxEnabled(enabled bool) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.sandboxEnabled = enabled
	}
}

func WithAllowedDomains(domains []string) DownloaderOption {
	return func(d *GrammarDownloader) {
		d.allowedDomains = domains
	}
}

func NewGrammarDownloader(opts ...DownloaderOption) (*GrammarDownloader, error) {
	dataDir := sylkDataDir()
	if dataDir == "" {
		return nil, fmt.Errorf("cannot determine data directory")
	}

	d := &GrammarDownloader{
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		cacheDir:     filepath.Join(dataDir, "grammars"),
		buildDir:     filepath.Join(dataDir, "grammar-build"),
		baseURL:      "https://github.com",
		checksums:    make(map[string]string),
		sessionPerms: make(map[string]DownloadPermission),
		alwaysPerms:  make(map[string]bool),
		allowedDomains: []string{
			"github.com",
			"raw.githubusercontent.com",
		},
	}

	for _, opt := range opts {
		opt(d)
	}

	if err := os.MkdirAll(d.cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	if err := os.MkdirAll(d.buildDir, 0755); err != nil {
		return nil, fmt.Errorf("create build dir: %w", err)
	}

	return d, nil
}

type GrammarInfo struct {
	Name       string `json:"name"`
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	SourcePath string `json:"source_path"`
}

var knownGrammars = map[string]GrammarInfo{
	"go":         {Name: "go", Repo: "tree-sitter/tree-sitter-go", Branch: "master", SourcePath: "src"},
	"python":     {Name: "python", Repo: "tree-sitter/tree-sitter-python", Branch: "master", SourcePath: "src"},
	"javascript": {Name: "javascript", Repo: "tree-sitter/tree-sitter-javascript", Branch: "master", SourcePath: "src"},
	"typescript": {Name: "typescript", Repo: "tree-sitter/tree-sitter-typescript", Branch: "master", SourcePath: "typescript/src"},
	"tsx":        {Name: "tsx", Repo: "tree-sitter/tree-sitter-typescript", Branch: "master", SourcePath: "tsx/src"},
	"rust":       {Name: "rust", Repo: "tree-sitter/tree-sitter-rust", Branch: "master", SourcePath: "src"},
	"c":          {Name: "c", Repo: "tree-sitter/tree-sitter-c", Branch: "master", SourcePath: "src"},
	"cpp":        {Name: "cpp", Repo: "tree-sitter/tree-sitter-cpp", Branch: "master", SourcePath: "src"},
	"java":       {Name: "java", Repo: "tree-sitter/tree-sitter-java", Branch: "master", SourcePath: "src"},
	"ruby":       {Name: "ruby", Repo: "tree-sitter/tree-sitter-ruby", Branch: "master", SourcePath: "src"},
	"json":       {Name: "json", Repo: "tree-sitter/tree-sitter-json", Branch: "master", SourcePath: "src"},
	"yaml":       {Name: "yaml", Repo: "tree-sitter-grammars/tree-sitter-yaml", Branch: "master", SourcePath: "src"},
	"toml":       {Name: "toml", Repo: "tree-sitter-grammars/tree-sitter-toml", Branch: "master", SourcePath: "src"},
	"html":       {Name: "html", Repo: "tree-sitter/tree-sitter-html", Branch: "master", SourcePath: "src"},
	"css":        {Name: "css", Repo: "tree-sitter/tree-sitter-css", Branch: "master", SourcePath: "src"},
	"bash":       {Name: "bash", Repo: "tree-sitter/tree-sitter-bash", Branch: "master", SourcePath: "src"},
	"markdown":   {Name: "markdown", Repo: "tree-sitter-grammars/tree-sitter-markdown", Branch: "main", SourcePath: "src"},
	"properties": {Name: "properties", Repo: "tree-sitter-grammars/tree-sitter-properties", Branch: "master", SourcePath: "src"},
	"hcl":        {Name: "hcl", Repo: "tree-sitter-grammars/tree-sitter-hcl", Branch: "main", SourcePath: "src"},
	"scala":      {Name: "scala", Repo: "tree-sitter/tree-sitter-scala", Branch: "master", SourcePath: "src"},
	"vue":        {Name: "vue", Repo: "tree-sitter-grammars/tree-sitter-vue", Branch: "main", SourcePath: "src"},
	"svelte":     {Name: "svelte", Repo: "tree-sitter-grammars/tree-sitter-svelte", Branch: "master", SourcePath: "src"},
	"lua":        {Name: "lua", Repo: "tree-sitter-grammars/tree-sitter-lua", Branch: "main", SourcePath: "src"},
	"php":        {Name: "php", Repo: "tree-sitter/tree-sitter-php", Branch: "master", SourcePath: "php/src"},
	"kotlin":     {Name: "kotlin", Repo: "fwcd/tree-sitter-kotlin", Branch: "main", SourcePath: "src"},
	"elixir":     {Name: "elixir", Repo: "elixir-lang/tree-sitter-elixir", Branch: "main", SourcePath: "src"},
	"haskell":    {Name: "haskell", Repo: "tree-sitter/tree-sitter-haskell", Branch: "master", SourcePath: "src"},
	"zig":        {Name: "zig", Repo: "tree-sitter-grammars/tree-sitter-zig", Branch: "master", SourcePath: "src"},
}

func (d *GrammarDownloader) EnsureGrammar(ctx context.Context, name string) (string, error) {
	if err := validateGrammarName(name); err != nil {
		return "", err
	}

	libPath := filepath.Join(d.cacheDir, grammarLibName(name))
	if _, err := os.Stat(libPath); err == nil {
		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "grammar_cache_hit",
			Grammar:   name,
			Source:    libPath,
			Result:    "success",
		})
		return libPath, nil
	}

	info, ok := knownGrammars[name]
	if !ok {
		return "", fmt.Errorf("unknown grammar %q: not in registry", name)
	}

	if err := d.requestPermission(ctx, name, info); err != nil {
		return "", err
	}

	return d.buildGrammar(ctx, info)
}

func (d *GrammarDownloader) requestPermission(ctx context.Context, name string, info GrammarInfo) error {
	if d.alwaysPerms[name] {
		return nil
	}

	if perm, ok := d.sessionPerms[name]; ok && perm >= PermissionGrantedSession {
		return nil
	}

	if d.permissionCb == nil {
		return fmt.Errorf("grammar %q not installed; user permission required to download (no permission callback configured)", name)
	}

	req := PermissionRequest{
		Grammar:     name,
		Source:      fmt.Sprintf("%s/%s", d.baseURL, info.Repo),
		Action:      "download_and_compile",
		Reason:      fmt.Sprintf("Grammar %q is required for parsing but not installed", name),
		RequireUser: true,
	}

	if checksum, ok := d.checksums[name]; ok {
		req.Checksum = checksum
	}

	resp, err := d.permissionCb(ctx, req)
	if err != nil {
		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "permission_request_error",
			Grammar:   name,
			Source:    req.Source,
			Result:    "error",
			Error:     err.Error(),
		})
		return fmt.Errorf("permission request failed: %w", err)
	}

	if !resp.Granted {
		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "permission_denied",
			Grammar:   name,
			Source:    req.Source,
			Result:    "denied",
			Error:     resp.Reason,
		})
		return fmt.Errorf("permission denied for grammar %q: %s", name, resp.Reason)
	}

	d.audit(AuditEntry{
		Timestamp: time.Now(),
		Action:    "permission_granted",
		Grammar:   name,
		Source:    req.Source,
		Result:    fmt.Sprintf("granted_%s", scopeString(resp.Scope)),
	})

	switch resp.Scope {
	case PermissionGrantedAlways:
		d.alwaysPerms[name] = true
		d.sessionPerms[name] = PermissionGrantedAlways
	case PermissionGrantedSession:
		d.sessionPerms[name] = PermissionGrantedSession
	}

	return nil
}

func scopeString(scope DownloadPermission) string {
	switch scope {
	case PermissionGrantedOnce:
		return "once"
	case PermissionGrantedSession:
		return "session"
	case PermissionGrantedAlways:
		return "always"
	default:
		return "unknown"
	}
}

func (d *GrammarDownloader) audit(entry AuditEntry) {
	if d.auditCb != nil {
		d.auditCb(entry)
	}
}

func (d *GrammarDownloader) buildGrammar(ctx context.Context, info GrammarInfo) (string, error) {
	repoDir := filepath.Join(d.buildDir, info.Repo)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "clone_start",
			Grammar:   info.Name,
			Source:    fmt.Sprintf("%s/%s", d.baseURL, info.Repo),
		})

		if err := d.cloneRepo(ctx, info, repoDir); err != nil {
			d.audit(AuditEntry{
				Timestamp: time.Now(),
				Action:    "clone_failed",
				Grammar:   info.Name,
				Result:    "error",
				Error:     err.Error(),
			})
			return "", fmt.Errorf("clone repo: %w", err)
		}

		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "clone_complete",
			Grammar:   info.Name,
			Result:    "success",
		})
	}

	srcDir := filepath.Join(repoDir, info.SourcePath)

	d.audit(AuditEntry{
		Timestamp: time.Now(),
		Action:    "compile_start",
		Grammar:   info.Name,
		Source:    srcDir,
	})

	libPath, err := d.compileGrammar(ctx, info.Name, srcDir)
	if err != nil {
		d.audit(AuditEntry{
			Timestamp: time.Now(),
			Action:    "compile_failed",
			Grammar:   info.Name,
			Result:    "error",
			Error:     err.Error(),
		})
		return "", fmt.Errorf("compile grammar: %w", err)
	}

	checksum := d.computeChecksum(libPath)
	d.audit(AuditEntry{
		Timestamp: time.Now(),
		Action:    "compile_complete",
		Grammar:   info.Name,
		Source:    libPath,
		Result:    "success",
		Checksum:  checksum,
	})

	return libPath, nil
}

func (d *GrammarDownloader) cloneRepo(ctx context.Context, info GrammarInfo, dest string) error {
	repoURL := fmt.Sprintf("%s/%s.git", d.baseURL, info.Repo)

	if !d.isDomainAllowed(d.baseURL) {
		return fmt.Errorf("domain not allowed: %s", d.baseURL)
	}

	args := []string{"clone", "--depth=1", "--branch", info.Branch, repoURL, dest}

	var cmd *exec.Cmd
	if d.sandboxEnabled {
		cmd = d.sandboxedGitCommand(ctx, args)
	} else {
		cmd = exec.CommandContext(ctx, "git", args...)
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if d.proxyConfig.Enabled && d.proxyConfig.ProxyURL != "" {
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("HTTP_PROXY=%s", d.proxyConfig.ProxyURL),
			fmt.Sprintf("HTTPS_PROXY=%s", d.proxyConfig.ProxyURL),
			fmt.Sprintf("http_proxy=%s", d.proxyConfig.ProxyURL),
			fmt.Sprintf("https_proxy=%s", d.proxyConfig.ProxyURL),
		)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", repoURL, err)
	}

	return nil
}

func (d *GrammarDownloader) isDomainAllowed(urlStr string) bool {
	for _, domain := range d.allowedDomains {
		if strings.Contains(urlStr, domain) {
			return true
		}
	}
	return false
}

func (d *GrammarDownloader) sandboxedGitCommand(ctx context.Context, args []string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return d.seatbeltGitCommand(ctx, args)
	case "linux":
		return d.bubblewrapGitCommand(ctx, args)
	default:
		return exec.CommandContext(ctx, "git", args...)
	}
}

func (d *GrammarDownloader) seatbeltGitCommand(ctx context.Context, args []string) *exec.Cmd {
	profile := `
(version 1)
(allow default)
(deny network* (remote ip "*:*"))
(allow network* (remote ip "localhost:*"))
(allow network* (remote ip "127.0.0.1:*"))
`
	profilePath := filepath.Join(os.TempDir(), fmt.Sprintf("sylk-git-sandbox-%d.sb", os.Getpid()))
	os.WriteFile(profilePath, []byte(profile), 0600)

	sandboxArgs := []string{"-f", profilePath, "git"}
	sandboxArgs = append(sandboxArgs, args...)

	cmd := exec.CommandContext(ctx, "sandbox-exec", sandboxArgs...)
	return cmd
}

func (d *GrammarDownloader) bubblewrapGitCommand(ctx context.Context, args []string) *exec.Cmd {
	bwrapArgs := []string{
		"--unshare-all",
		"--share-net",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf",
		"--ro-bind", "/etc/ssl", "/etc/ssl",
		"--bind", d.buildDir, d.buildDir,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--die-with-parent",
		"git",
	}
	bwrapArgs = append(bwrapArgs, args...)

	return exec.CommandContext(ctx, "bwrap", bwrapArgs...)
}

func (d *GrammarDownloader) compileGrammar(ctx context.Context, name, srcDir string) (string, error) {
	parserC := filepath.Join(srcDir, "parser.c")
	if _, err := os.Stat(parserC); err != nil {
		return "", fmt.Errorf("parser.c not found in %s", srcDir)
	}

	var cSources, cxxSources []string
	cSources = append(cSources, parserC)

	scannerC := filepath.Join(srcDir, "scanner.c")
	if _, err := os.Stat(scannerC); err == nil {
		cSources = append(cSources, scannerC)
	}
	scannerCC := filepath.Join(srcDir, "scanner.cc")
	if _, err := os.Stat(scannerCC); err == nil {
		cxxSources = append(cxxSources, scannerCC)
	}

	libName := grammarLibName(name)
	libPath := filepath.Join(d.cacheDir, libName)

	if len(cxxSources) == 0 {
		return d.compilePureC(ctx, srcDir, cSources, libPath)
	}
	return d.compileMixed(ctx, srcDir, cSources, cxxSources, libPath)
}

func (d *GrammarDownloader) compilePureC(ctx context.Context, srcDir string, sources []string, libPath string) (string, error) {
	args := []string{"-shared", "-fPIC", "-O2", "-std=c11", "-I", srcDir}
	args = append(args, sources...)
	args = append(args, "-o", libPath)

	cmd := exec.CommandContext(ctx, "gcc", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("compile failed: %w\noutput: %s", err, output)
	}
	return libPath, nil
}

func (d *GrammarDownloader) compileMixed(ctx context.Context, srcDir string, cSources, cxxSources []string, libPath string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "grammar-build-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var objects []string

	for i, src := range cSources {
		obj := filepath.Join(tmpDir, fmt.Sprintf("c_%d.o", i))
		args := []string{"-c", "-fPIC", "-O2", "-std=c11", "-I", srcDir, src, "-o", obj}
		cmd := exec.CommandContext(ctx, "gcc", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("compile %s failed: %w\noutput: %s", src, err, output)
		}
		objects = append(objects, obj)
	}

	for i, src := range cxxSources {
		obj := filepath.Join(tmpDir, fmt.Sprintf("cxx_%d.o", i))
		args := []string{"-c", "-fPIC", "-O2", "-std=c++11", "-I", srcDir, src, "-o", obj}
		cmd := exec.CommandContext(ctx, "g++", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("compile %s failed: %w\noutput: %s", src, err, output)
		}
		objects = append(objects, obj)
	}

	args := []string{"-shared", "-fPIC", "-o", libPath}
	args = append(args, objects...)
	args = append(args, "-lstdc++")
	cmd := exec.CommandContext(ctx, "gcc", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("link failed: %w\noutput: %s", err, output)
	}

	return libPath, nil
}

func (d *GrammarDownloader) sandboxedCompileCommand(ctx context.Context, compiler string, args []string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return d.seatbeltCompileCommand(ctx, compiler, args)
	case "linux":
		return d.bubblewrapCompileCommand(ctx, compiler, args)
	default:
		return exec.CommandContext(ctx, compiler, args...)
	}
}

func (d *GrammarDownloader) seatbeltCompileCommand(ctx context.Context, compiler string, args []string) *exec.Cmd {
	profile := `
(version 1)
(allow default)
(deny network*)
`
	profilePath := filepath.Join(os.TempDir(), fmt.Sprintf("sylk-compile-sandbox-%d.sb", os.Getpid()))
	os.WriteFile(profilePath, []byte(profile), 0600)

	sandboxArgs := []string{"-f", profilePath, compiler}
	sandboxArgs = append(sandboxArgs, args...)

	return exec.CommandContext(ctx, "sandbox-exec", sandboxArgs...)
}

func (d *GrammarDownloader) bubblewrapCompileCommand(ctx context.Context, compiler string, args []string) *exec.Cmd {
	bwrapArgs := []string{
		"--unshare-all",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--bind", d.buildDir, d.buildDir,
		"--bind", d.cacheDir, d.cacheDir,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--die-with-parent",
		compiler,
	}
	bwrapArgs = append(bwrapArgs, args...)

	return exec.CommandContext(ctx, "bwrap", bwrapArgs...)
}

func hasCCScanner(sources []string) bool {
	for _, s := range sources {
		if strings.HasSuffix(s, ".cc") || strings.HasSuffix(s, ".cpp") {
			return true
		}
	}
	return false
}

func buildCompilerArgs(srcDir string, sources []string, libPath string) []string {
	args := []string{
		"-shared",
		"-fPIC",
		"-O2",
		"-I", srcDir,
	}

	if hasCCScanner(sources) {
		args = append(args, "-std=c++11")
	} else {
		args = append(args, "-std=c11")
	}

	switch runtime.GOOS {
	case "darwin":
		args = append(args,
			"-dynamiclib",
			"-Wl,-install_name,@rpath/"+filepath.Base(libPath),
		)
	case "linux":
		args = append(args,
			"-Wl,-soname,"+filepath.Base(libPath),
		)
	}

	args = append(args, sources...)
	args = append(args, "-o", libPath)

	return args
}

func (d *GrammarDownloader) computeChecksum(libPath string) string {
	data, err := os.ReadFile(libPath)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (d *GrammarDownloader) RegisterChecksum(name, sha256sum string) {
	d.checksums[name] = sha256sum
}

func (d *GrammarDownloader) VerifyChecksum(name, libPath string) error {
	expected, ok := d.checksums[name]
	if !ok {
		return nil
	}

	actual := d.computeChecksum(libPath)
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, expected, actual)
	}

	return nil
}

func (d *GrammarDownloader) ListAvailable() []string {
	names := make([]string, 0, len(knownGrammars))
	for name := range knownGrammars {
		names = append(names, name)
	}
	return names
}

func (d *GrammarDownloader) ListInstalled() ([]string, error) {
	entries, err := os.ReadDir(d.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var installed []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "libtree-sitter-") {
			grammarName := extractGrammarName(name)
			if grammarName != "" {
				installed = append(installed, grammarName)
			}
		}
	}

	return installed, nil
}

func extractGrammarName(libName string) string {
	name := strings.TrimPrefix(libName, "libtree-sitter-")
	name = strings.TrimPrefix(name, "tree-sitter-")

	for _, ext := range []string{".dylib", ".so", ".dll"} {
		name = strings.TrimSuffix(name, ext)
	}

	return name
}

func (d *GrammarDownloader) CacheDir() string {
	return d.cacheDir
}

func (d *GrammarDownloader) Clean() error {
	if err := os.RemoveAll(d.buildDir); err != nil {
		return fmt.Errorf("remove build dir: %w", err)
	}
	return os.MkdirAll(d.buildDir, 0755)
}

func (d *GrammarDownloader) CleanAll() error {
	if err := os.RemoveAll(d.cacheDir); err != nil {
		return fmt.Errorf("remove cache dir: %w", err)
	}
	if err := os.RemoveAll(d.buildDir); err != nil {
		return fmt.Errorf("remove build dir: %w", err)
	}
	if err := os.MkdirAll(d.cacheDir, 0755); err != nil {
		return err
	}
	return os.MkdirAll(d.buildDir, 0755)
}

func (d *GrammarDownloader) ExportManifest() ([]byte, error) {
	installed, err := d.ListInstalled()
	if err != nil {
		return nil, err
	}

	manifest := struct {
		Installed []string          `json:"installed"`
		Checksums map[string]string `json:"checksums"`
	}{
		Installed: installed,
		Checksums: d.checksums,
	}

	return json.MarshalIndent(manifest, "", "  ")
}

func (d *GrammarDownloader) RevokeSessionPermissions() {
	d.sessionPerms = make(map[string]DownloadPermission)
}

func (d *GrammarDownloader) GetPermissionStatus(name string) DownloadPermission {
	if d.alwaysPerms[name] {
		return PermissionGrantedAlways
	}
	if perm, ok := d.sessionPerms[name]; ok {
		return perm
	}
	return PermissionDenied
}
