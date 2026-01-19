// Package context provides CV.2 StartupIndexer for parallel codebase indexing on startup.
// It uses GoroutineBudget for controlled parallelism and FileHandleBudget for file reads.
package context

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/search/indexer"
)

// StartupIndexer handles parallel codebase indexing on application startup.
// It integrates with GoroutineBudget for controlled parallelism and
// FileHandleBudget for safe file access.
type StartupIndexer struct {
	contentStore     *UniversalContentStore
	goroutineBudget  *concurrency.GoroutineBudget
	fileHandleBudget *resources.FileHandleBudget
	progressCallback func(phase string, indexed, total int)
	logger           *slog.Logger
	agentID          string
}

// StartupIndexConfig holds configuration for startup indexing.
type StartupIndexConfig struct {
	MaxConcurrency  int
	BatchSize       int
	PriorityPaths   []string
	ExcludePaths    []string
	ExcludePatterns []string
	MaxFileSize     int64
	SecurityFilters []string
}

// FileInfo holds metadata for a file to be indexed.
type FileInfo struct {
	Path     string
	RelPath  string
	Size     int64
	ModTime  time.Time
	Priority int
}

// IndexProgress represents progress of the indexing operation.
type IndexProgress struct {
	Phase   string
	Indexed int
	Total   int
	Errors  []error
}

// StartupIndexerConfig holds configuration for creating a StartupIndexer.
type StartupIndexerConfig struct {
	ContentStore     *UniversalContentStore
	GoroutineBudget  *concurrency.GoroutineBudget
	FileHandleBudget *resources.FileHandleBudget
	Logger           *slog.Logger
	AgentID          string
}

var (
	ErrNilContentStore     = errors.New("content store cannot be nil")
	ErrNilGoroutineBudget  = errors.New("goroutine budget cannot be nil")
	ErrNilFileHandleBudget = errors.New("file handle budget cannot be nil")
	ErrRootNotDirectory    = errors.New("root path is not a directory")
	ErrIndexCancelled      = errors.New("indexing cancelled")
)

// DefaultStartupIndexConfig returns sensible default configuration.
func DefaultStartupIndexConfig() *StartupIndexConfig {
	return &StartupIndexConfig{
		MaxConcurrency: runtime.NumCPU() * 2,
		BatchSize:      100,
		PriorityPaths: []string{
			"cmd/", "src/", "lib/", "pkg/", "internal/", "core/",
			"app/", "components/", "services/", "handlers/",
		},
		ExcludePaths: []string{
			".git", "node_modules", "vendor", "__pycache__", ".next",
			"dist", "build", "target", ".cache", "coverage",
		},
		ExcludePatterns: []string{
			"*.min.js", "*.min.css", "*.map", "*.lock", "*.sum",
			"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		},
		MaxFileSize: 1 << 20, // 1MB
		SecurityFilters: []string{
			".env", ".env.*", "credentials.json", "secrets.json",
			"*.pem", "*.key", "id_rsa", "id_dsa", "*.p12",
		},
	}
}

// NewStartupIndexer creates a new startup indexer.
func NewStartupIndexer(cfg StartupIndexerConfig) (*StartupIndexer, error) {
	if err := validateIndexerConfig(cfg); err != nil {
		return nil, err
	}

	return &StartupIndexer{
		contentStore:     cfg.ContentStore,
		goroutineBudget:  cfg.GoroutineBudget,
		fileHandleBudget: cfg.FileHandleBudget,
		logger:           normalizeIndexerLogger(cfg.Logger),
		agentID:          normalizeAgentID(cfg.AgentID),
	}, nil
}

func validateIndexerConfig(cfg StartupIndexerConfig) error {
	if cfg.ContentStore == nil {
		return ErrNilContentStore
	}
	if cfg.GoroutineBudget == nil {
		return ErrNilGoroutineBudget
	}
	if cfg.FileHandleBudget == nil {
		return ErrNilFileHandleBudget
	}
	return nil
}

func normalizeIndexerLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

func normalizeAgentID(agentID string) string {
	if agentID == "" {
		return "startup-indexer"
	}
	return agentID
}

// SetProgressCallback sets the callback for progress updates.
func (s *StartupIndexer) SetProgressCallback(cb func(phase string, indexed, total int)) {
	s.progressCallback = cb
}

// IndexProject indexes the codebase at the given root path.
func (s *StartupIndexer) IndexProject(ctx context.Context, rootPath string, config *StartupIndexConfig) error {
	// Check for context cancellation early
	if err := ctx.Err(); err != nil {
		return err
	}

	if config == nil {
		config = DefaultStartupIndexConfig()
	}

	if err := s.validateRootPath(rootPath); err != nil {
		return err
	}

	s.goroutineBudget.RegisterAgent(s.agentID, "archivalist")
	defer s.goroutineBudget.UnregisterAgent(s.agentID)

	return s.runIndexing(ctx, rootPath, config)
}

func (s *StartupIndexer) validateRootPath(rootPath string) error {
	info, err := os.Stat(rootPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrRootNotDirectory
	}
	return nil
}

func (s *StartupIndexer) runIndexing(ctx context.Context, rootPath string, config *StartupIndexConfig) error {
	s.reportProgress("scanning", 0, 0)

	files, err := s.scanAndPrioritize(ctx, rootPath, config)
	if err != nil {
		return err
	}

	total := len(files)
	if total == 0 {
		s.reportProgress("complete", 0, 0)
		return nil
	}

	s.reportProgress("indexing", 0, total)
	return s.indexFilesParallel(ctx, files, config, total)
}

func (s *StartupIndexer) reportProgress(phase string, indexed, total int) {
	if s.progressCallback != nil {
		s.progressCallback(phase, indexed, total)
	}
}

// scanAndPrioritize walks the directory and returns prioritized file list.
func (s *StartupIndexer) scanAndPrioritize(
	ctx context.Context,
	rootPath string,
	config *StartupIndexConfig,
) ([]*FileInfo, error) {
	scanner := indexer.NewScanner(indexer.ScanConfig{
		RootPath:        rootPath,
		ExcludePatterns: config.ExcludePatterns,
		MaxFileSize:     config.MaxFileSize,
	})

	fileCh, err := scanner.Scan(ctx)
	if err != nil {
		return nil, err
	}

	return s.collectAndPrioritize(ctx, fileCh, rootPath, config)
}

func (s *StartupIndexer) collectAndPrioritize(
	ctx context.Context,
	fileCh <-chan *indexer.FileInfo,
	rootPath string,
	config *StartupIndexConfig,
) ([]*FileInfo, error) {
	var files []*FileInfo

	for scanInfo := range fileCh {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		file := s.processScannedFile(scanInfo, rootPath, config)
		if file != nil {
			files = append(files, file)
		}
	}

	s.sortByPriority(files)
	return files, nil
}

func (s *StartupIndexer) processScannedFile(
	scanInfo *indexer.FileInfo,
	rootPath string,
	config *StartupIndexConfig,
) *FileInfo {
	relPath, _ := filepath.Rel(rootPath, scanInfo.Path)

	if s.shouldExcludeFile(relPath, scanInfo.Name, config) {
		return nil
	}

	return &FileInfo{
		Path:     scanInfo.Path,
		RelPath:  relPath,
		Size:     scanInfo.Size,
		ModTime:  scanInfo.ModTime,
		Priority: s.calculatePriority(relPath, scanInfo.ModTime, config.PriorityPaths),
	}
}

func (s *StartupIndexer) shouldExcludeFile(relPath, name string, config *StartupIndexConfig) bool {
	if s.matchesExcludePath(relPath, config.ExcludePaths) {
		return true
	}
	return s.matchesSecurityFilter(name, relPath, config.SecurityFilters)
}

func (s *StartupIndexer) matchesExcludePath(relPath string, excludePaths []string) bool {
	for _, exclude := range excludePaths {
		if strings.HasPrefix(relPath, exclude) {
			return true
		}
	}
	return false
}

func (s *StartupIndexer) matchesSecurityFilter(name, relPath string, filters []string) bool {
	for _, filter := range filters {
		if matchesSecurityPattern(name, filter) || matchesSecurityPattern(relPath, filter) {
			return true
		}
	}
	return false
}

func matchesSecurityPattern(name, pattern string) bool {
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(name, pattern[1:])
	}
	if strings.Contains(pattern, "*") {
		matched, _ := filepath.Match(pattern, name)
		return matched
	}
	return name == pattern
}

func (s *StartupIndexer) sortByPriority(files []*FileInfo) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].Priority > files[j].Priority
	})
}

func (s *StartupIndexer) calculatePriority(relPath string, modTime time.Time, priorityPaths []string) int {
	priority := 0
	priority += s.calculateAgePriority(modTime)
	priority += s.calculatePathPriority(relPath, priorityPaths)
	priority += s.calculateExtensionPriority(relPath)
	return priority
}

func (s *StartupIndexer) calculateAgePriority(modTime time.Time) int {
	age := time.Since(modTime)
	switch {
	case age < 24*time.Hour:
		return 100
	case age < 7*24*time.Hour:
		return 50
	case age < 30*24*time.Hour:
		return 25
	default:
		return 0
	}
}

func (s *StartupIndexer) calculatePathPriority(relPath string, priorityPaths []string) int {
	for i, pp := range priorityPaths {
		if strings.HasPrefix(relPath, pp) {
			return 50 - i
		}
	}
	return 0
}

func (s *StartupIndexer) calculateExtensionPriority(relPath string) int {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".go", ".ts", ".tsx", ".py", ".rs":
		return 30
	case ".js", ".jsx", ".java", ".cpp", ".c":
		return 20
	case ".md", ".yaml", ".yml", ".json", ".toml":
		return 10
	default:
		return 0
	}
}

// indexFilesParallel indexes files using controlled parallelism.
func (s *StartupIndexer) indexFilesParallel(
	ctx context.Context,
	files []*FileInfo,
	config *StartupIndexConfig,
	total int,
) error {
	sem := make(chan struct{}, config.MaxConcurrency)
	var wg sync.WaitGroup
	indexed := &atomic.Int32{}
	errorsCh := make(chan error, total)

	for _, file := range files {
		if err := s.acquireWorkerSlot(ctx, sem); err != nil {
			return err
		}

		wg.Add(1)
		go s.indexFileWorker(ctx, file, sem, &wg, indexed, total, errorsCh)
	}

	wg.Wait()
	close(errorsCh)

	s.logIndexingErrors(errorsCh, total)
	s.reportProgress("complete", total, total)
	return nil
}

func (s *StartupIndexer) acquireWorkerSlot(ctx context.Context, sem chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case sem <- struct{}{}:
		return nil
	}
}

func (s *StartupIndexer) indexFileWorker(
	ctx context.Context,
	file *FileInfo,
	sem chan struct{},
	wg *sync.WaitGroup,
	indexed *atomic.Int32,
	total int,
	errorsCh chan<- error,
) {
	defer wg.Done()
	defer func() { <-sem }()

	if err := s.indexSingleFile(ctx, file); err != nil {
		s.sendError(errorsCh, file.Path, err)
	}

	current := indexed.Add(1)
	s.reportProgress("indexing", int(current), total)
}

func (s *StartupIndexer) sendError(errorsCh chan<- error, path string, err error) {
	select {
	case errorsCh <- errors.New(path + ": " + err.Error()):
	default:
	}
}

func (s *StartupIndexer) logIndexingErrors(errorsCh <-chan error, total int) {
	var errs []error
	for err := range errorsCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		s.logger.Warn("some files failed to index",
			"count", len(errs),
			"total", total,
		)
	}
}

// indexSingleFile indexes a single file with budget tracking.
func (s *StartupIndexer) indexSingleFile(ctx context.Context, file *FileInfo) error {
	if err := s.goroutineBudget.AcquireWithContext(ctx, s.agentID); err != nil {
		return err
	}
	defer s.goroutineBudget.Release(s.agentID)

	return s.readAndIndexFile(ctx, file)
}

func (s *StartupIndexer) readAndIndexFile(ctx context.Context, file *FileInfo) error {
	content, err := s.readFileContent(ctx, file.Path)
	if err != nil {
		return err
	}

	entry := s.createContentEntry(file, content)
	return s.contentStore.IndexContent(entry)
}

func (s *StartupIndexer) readFileContent(ctx context.Context, path string) ([]byte, error) {
	session := s.fileHandleBudget.RegisterSession("startup-indexer")
	agent := session.RegisterAgent(s.agentID, "archivalist")

	if err := agent.Acquire(ctx); err != nil {
		return nil, err
	}
	defer agent.Release()

	return os.ReadFile(path)
}

func (s *StartupIndexer) createContentEntry(file *FileInfo, content []byte) *ContentEntry {
	return &ContentEntry{
		ID:           GenerateContentID(content),
		ContentType:  ContentTypeCodeFile,
		Content:      string(content),
		TokenCount:   estimateTokenCount(content),
		Timestamp:    file.ModTime,
		RelatedFiles: []string{file.RelPath},
		Metadata: map[string]string{
			"path":     file.RelPath,
			"language": detectLanguageFromPath(file.Path),
			"size":     formatFileSize(file.Size),
		},
	}
}

func estimateTokenCount(content []byte) int {
	// Rough estimate: ~4 characters per token
	return len(content) / 4
}

func detectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	languages := map[string]string{
		".go":   "go",
		".ts":   "typescript",
		".tsx":  "typescript",
		".js":   "javascript",
		".jsx":  "javascript",
		".py":   "python",
		".rs":   "rust",
		".java": "java",
		".cpp":  "cpp",
		".c":    "c",
		".rb":   "ruby",
		".php":  "php",
		".md":   "markdown",
		".yaml": "yaml",
		".yml":  "yaml",
		".json": "json",
		".toml": "toml",
	}

	if lang, ok := languages[ext]; ok {
		return lang
	}
	return "unknown"
}

func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return string(rune(size)) + " B"
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	return string(rune(size/div)) + " " + units[exp]
}

// GetIndexedCount returns the count of indexed entries for a session.
func (s *StartupIndexer) GetIndexedCount() (int64, error) {
	return s.contentStore.EntryCount()
}

// IsIndexed checks if a specific file path has been indexed.
func (s *StartupIndexer) IsIndexed(ctx context.Context, relPath string) (bool, error) {
	results, err := s.contentStore.Search(relPath, nil, 1)
	if err != nil {
		return false, err
	}
	return len(results) > 0, nil
}

// IncrementalIndex indexes only files modified since the last index time.
func (s *StartupIndexer) IncrementalIndex(
	ctx context.Context,
	rootPath string,
	since time.Time,
	config *StartupIndexConfig,
) error {
	if config == nil {
		config = DefaultStartupIndexConfig()
	}

	s.goroutineBudget.RegisterAgent(s.agentID, "archivalist")
	defer s.goroutineBudget.UnregisterAgent(s.agentID)

	files, err := s.scanModifiedFiles(ctx, rootPath, since, config)
	if err != nil {
		return err
	}

	total := len(files)
	if total == 0 {
		s.reportProgress("complete", 0, 0)
		return nil
	}

	s.reportProgress("indexing", 0, total)
	return s.indexFilesParallel(ctx, files, config, total)
}

func (s *StartupIndexer) scanModifiedFiles(
	ctx context.Context,
	rootPath string,
	since time.Time,
	config *StartupIndexConfig,
) ([]*FileInfo, error) {
	allFiles, err := s.scanAndPrioritize(ctx, rootPath, config)
	if err != nil {
		return nil, err
	}

	return filterModifiedSince(allFiles, since), nil
}

func filterModifiedSince(files []*FileInfo, since time.Time) []*FileInfo {
	var modified []*FileInfo
	for _, f := range files {
		if f.ModTime.After(since) {
			modified = append(modified, f)
		}
	}
	return modified
}

// WalkDir is a helper that wraps filepath.WalkDir with context cancellation.
func WalkDir(ctx context.Context, root string, fn func(path string, d fs.DirEntry, err error) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fn(path, d, err)
	})
}
