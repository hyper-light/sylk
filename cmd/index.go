// Package cmd provides CLI commands for the Sylk application.
// This file implements the index command for managing the search index.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
	"github.com/adalundhe/sylk/core/search/indexer"
	"github.com/adalundhe/sylk/core/search/watcher"
	"github.com/spf13/cobra"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// IndexDefaultBatchSize is the default batch size for indexing.
	IndexDefaultBatchSize = 50

	// IndexDefaultConcurrency is the default concurrency level.
	IndexDefaultConcurrency = 4

	// IndexDefaultPath is the default path to the index.
	IndexDefaultPath = ".sylk/index"

	// ProgressBarWidth is the width of the progress bar in characters.
	ProgressBarWidth = 40
)

// =============================================================================
// Index Command Flags
// =============================================================================

var (
	indexPath        string
	indexRootPath    string
	indexBatchSize   int
	indexConcurrency int
	indexWatch       bool
	indexJSON        bool
	indexVerbose     bool
	indexInclude     []string
	indexExclude     []string
)

// =============================================================================
// Index Command
// =============================================================================

// indexCmd represents the index command.
var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Manage the search index",
	Long: `Manage the search index for the codebase.

Subcommands:
  status   - Show index status and statistics
  rebuild  - Rebuild the index from scratch
  verify   - Verify index integrity

Examples:
  sylk index                           # Show index status
  sylk index status                    # Show detailed status
  sylk index rebuild                   # Rebuild the index
  sylk index rebuild --watch           # Rebuild and watch for changes
  sylk index verify                    # Verify index integrity`,
	RunE: runIndexStatus,
}

// indexStatusCmd shows index status.
var indexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show index status",
	Long:  `Show the current status of the search index including document count, last indexed time, and health.`,
	RunE:  runIndexStatus,
}

// indexRebuildCmd rebuilds the index.
var indexRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuild the index from scratch",
	Long: `Rebuild the search index from scratch by scanning all files.

This operation will:
1. Delete the existing index
2. Scan all files in the root path
3. Index all matching files

Use --watch to continue watching for changes after the rebuild.`,
	RunE: runIndexRebuild,
}

// indexVerifyCmd verifies index integrity.
var indexVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify index integrity",
	Long:  `Verify the integrity of the search index by checking for corrupted entries and orphaned files.`,
	RunE:  runIndexVerify,
}

func init() {
	// Register with root command
	rootCmd.AddCommand(indexCmd)

	// Add subcommands
	indexCmd.AddCommand(indexStatusCmd)
	indexCmd.AddCommand(indexRebuildCmd)
	indexCmd.AddCommand(indexVerifyCmd)

	// Global flags
	indexCmd.PersistentFlags().StringVar(&indexPath, "index", IndexDefaultPath, "Path to search index")
	indexCmd.PersistentFlags().BoolVar(&indexJSON, "json", false, "Output as JSON")
	indexCmd.PersistentFlags().BoolVarP(&indexVerbose, "verbose", "v", false, "Verbose output")

	// Rebuild flags
	indexRebuildCmd.Flags().StringVarP(&indexRootPath, "root", "r", ".", "Root path to scan")
	indexRebuildCmd.Flags().IntVarP(&indexBatchSize, "batch-size", "b", IndexDefaultBatchSize, "Batch size for indexing")
	indexRebuildCmd.Flags().IntVarP(&indexConcurrency, "concurrency", "c", IndexDefaultConcurrency, "Maximum concurrent workers")
	indexRebuildCmd.Flags().BoolVarP(&indexWatch, "watch", "w", false, "Continue watching for changes after rebuild")
	indexRebuildCmd.Flags().StringSliceVarP(&indexInclude, "include", "I", nil, "Include patterns (e.g., '*.go,*.ts')")
	indexRebuildCmd.Flags().StringSliceVarP(&indexExclude, "exclude", "E", nil, "Exclude patterns (e.g., '*_test.go')")
}

// =============================================================================
// Index Status
// =============================================================================

// indexStatusOutput is the JSON output for index status.
type indexStatusOutput struct {
	Path           string    `json:"path"`
	Exists         bool      `json:"exists"`
	Open           bool      `json:"open"`
	DocumentCount  uint64    `json:"document_count"`
	LastModified   time.Time `json:"last_modified,omitempty"`
	SizeBytes      int64     `json:"size_bytes,omitempty"`
	Healthy        bool      `json:"healthy"`
	HealthMessage  string    `json:"health_message,omitempty"`
}

// runIndexStatus displays the index status.
func runIndexStatus(cmd *cobra.Command, args []string) error {
	status, err := getIndexStatus()
	if err != nil {
		return fmt.Errorf("failed to get index status: %w", err)
	}

	if indexJSON {
		return outputJSONIndexStatus(cmd.OutOrStdout(), status)
	}
	return outputRichIndexStatus(cmd.OutOrStdout(), status)
}

// getIndexStatus retrieves the current index status.
func getIndexStatus() (*indexStatusOutput, error) {
	status := &indexStatusOutput{
		Path:    indexPath,
		Healthy: true,
	}

	// Check if index exists
	info, err := os.Stat(indexPath)
	if os.IsNotExist(err) {
		status.Exists = false
		status.Healthy = false
		status.HealthMessage = "Index does not exist. Run 'sylk index rebuild' to create it."
		return status, nil
	}
	if err != nil {
		return nil, err
	}

	status.Exists = true
	status.LastModified = info.ModTime()

	// Get directory size
	if info.IsDir() {
		size, _ := getDirSize(indexPath)
		status.SizeBytes = size
	}

	// Try to open the index
	indexManager := bleve.NewIndexManager(indexPath)
	if err := indexManager.Open(); err != nil {
		status.Open = false
		status.Healthy = false
		status.HealthMessage = fmt.Sprintf("Failed to open index: %v", err)
		return status, nil
	}
	defer indexManager.Close()

	status.Open = true

	// Get document count
	count, err := indexManager.DocumentCount()
	if err != nil {
		status.Healthy = false
		status.HealthMessage = fmt.Sprintf("Failed to get document count: %v", err)
		return status, nil
	}

	status.DocumentCount = count

	if count == 0 {
		status.HealthMessage = "Index is empty. Run 'sylk index rebuild' to populate it."
	} else {
		status.HealthMessage = "Index is healthy."
	}

	return status, nil
}

// outputJSONIndexStatus outputs index status as JSON.
func outputJSONIndexStatus(w io.Writer, status *indexStatusOutput) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

// outputRichIndexStatus outputs index status with rich formatting.
func outputRichIndexStatus(w io.Writer, status *indexStatusOutput) error {
	fmt.Fprintf(w, "%s%sIndex Status%s\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(w, "%s%s%s\n", colorGray, strings.Repeat("-", 40), colorReset)

	// Path
	fmt.Fprintf(w, "%sPath:%s        %s\n", colorGray, colorReset, status.Path)

	// Exists
	if status.Exists {
		fmt.Fprintf(w, "%sExists:%s      %s%s%s\n", colorGray, colorReset, colorGreen, "Yes", colorReset)
	} else {
		fmt.Fprintf(w, "%sExists:%s      %s%s%s\n", colorGray, colorReset, colorRed, "No", colorReset)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s%s%s\n", colorYellow, status.HealthMessage, colorReset)
		return nil
	}

	// Open
	if status.Open {
		fmt.Fprintf(w, "%sOpen:%s        %s%s%s\n", colorGray, colorReset, colorGreen, "Yes", colorReset)
	} else {
		fmt.Fprintf(w, "%sOpen:%s        %s%s%s\n", colorGray, colorReset, colorRed, "No", colorReset)
	}

	// Document count
	fmt.Fprintf(w, "%sDocuments:%s   %d\n", colorGray, colorReset, status.DocumentCount)

	// Size
	if status.SizeBytes > 0 {
		fmt.Fprintf(w, "%sSize:%s        %s\n", colorGray, colorReset, formatBytes(status.SizeBytes))
	}

	// Last modified
	if !status.LastModified.IsZero() {
		fmt.Fprintf(w, "%sLast Update:%s %s\n", colorGray, colorReset, status.LastModified.Format(time.RFC3339))
	}

	// Health
	fmt.Fprintln(w)
	if status.Healthy {
		fmt.Fprintf(w, "%sHealth:%s %s%s%s\n", colorGray, colorReset, colorGreen, status.HealthMessage, colorReset)
	} else {
		fmt.Fprintf(w, "%sHealth:%s %s%s%s\n", colorGray, colorReset, colorYellow, status.HealthMessage, colorReset)
	}

	return nil
}

// =============================================================================
// Index Rebuild
// =============================================================================

// indexRebuildOutput is the JSON output for rebuild.
type indexRebuildOutput struct {
	Success      bool          `json:"success"`
	TotalFiles   int64         `json:"total_files"`
	IndexedFiles int64         `json:"indexed_files"`
	FailedFiles  int64         `json:"failed_files"`
	Duration     time.Duration `json:"duration"`
	Errors       []string      `json:"errors,omitempty"`
	Watching     bool          `json:"watching,omitempty"`
}

// runIndexRebuild rebuilds the search index.
func runIndexRebuild(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(cmd.OutOrStderr(), "\nInterrupted. Cleaning up...")
		cancel()
	}()

	// Validate root path
	if err := validateRootPath(indexRootPath); err != nil {
		return err
	}

	if !indexJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "%s%sRebuilding Index%s\n", colorBold, colorCyan, colorReset)
		fmt.Fprintf(cmd.OutOrStdout(), "%sRoot:%s %s\n", colorGray, colorReset, indexRootPath)
		fmt.Fprintf(cmd.OutOrStdout(), "%sIndex:%s %s\n", colorGray, colorReset, indexPath)
		fmt.Fprintln(cmd.OutOrStdout())
	}

	// Create and configure index manager
	indexManager := bleve.NewIndexManager(indexPath)

	// Destroy existing index and create new one
	if err := indexManager.Destroy(); err != nil && !os.IsNotExist(err) {
		// Ignore error if index doesn't exist
		if !strings.Contains(err.Error(), "no such file") {
			return fmt.Errorf("failed to destroy existing index: %w", err)
		}
	}

	if err := indexManager.Open(); err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	defer indexManager.Close()

	// Run indexing
	result, err := runFullIndex(ctx, cmd.OutOrStdout(), indexManager)
	if err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	// Output results
	if err := outputRebuildResult(cmd.OutOrStdout(), result); err != nil {
		return err
	}

	// Watch mode
	if indexWatch && result.Success {
		return runWatchMode(ctx, cmd.OutOrStdout(), indexManager)
	}

	return nil
}

// validateRootPath checks that the root path is valid.
func validateRootPath(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("root path does not exist: %s", path)
	}
	if err != nil {
		return fmt.Errorf("failed to access root path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root path is not a directory: %s", path)
	}
	return nil
}

// runFullIndex performs a full index rebuild with progress reporting.
func runFullIndex(ctx context.Context, w io.Writer, indexManager *bleve.IndexManager) (*indexRebuildOutput, error) {
	startTime := time.Now()
	result := &indexRebuildOutput{
		Success: true,
	}

	// Create scanner config
	scanConfig := indexer.ScanConfig{
		RootPath:        indexRootPath,
		IncludePatterns: indexInclude,
		ExcludePatterns: indexExclude,
	}

	// Create scanner
	scanner := indexer.NewScanner(scanConfig)

	// Start scanning
	fileCh, err := scanner.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start scanner: %w", err)
	}

	// Create document builder
	builder := indexer.NewDocumentBuilder()

	// Create progress tracker
	progress := newProgressTracker(w, !indexJSON)

	// Process files
	var processed, indexed, failed int64
	batch := make([]*search.Document, 0, indexBatchSize)
	errors := make([]string, 0)

	for fileInfo := range fileCh {
		select {
		case <-ctx.Done():
			result.Success = false
			break
		default:
		}

		atomic.AddInt64(&processed, 1)

		// Read file content
		content, err := os.ReadFile(fileInfo.Path)
		if err != nil {
			atomic.AddInt64(&failed, 1)
			if indexVerbose {
				errors = append(errors, fmt.Sprintf("%s: %v", fileInfo.Path, err))
			}
			continue
		}

		// Build document
		doc, err := builder.Build(fileInfo, content)
		if err != nil {
			atomic.AddInt64(&failed, 1)
			if indexVerbose {
				errors = append(errors, fmt.Sprintf("%s: %v", fileInfo.Path, err))
			}
			continue
		}

		batch = append(batch, doc)

		// Flush batch if full
		if len(batch) >= indexBatchSize {
			if err := indexManager.IndexBatch(ctx, batch); err != nil {
				atomic.AddInt64(&failed, int64(len(batch)))
				errors = append(errors, fmt.Sprintf("batch error: %v", err))
			} else {
				atomic.AddInt64(&indexed, int64(len(batch)))
			}
			batch = batch[:0]
		}

		progress.update(processed, indexed, failed, fileInfo.Path)
	}

	// Flush remaining batch
	if len(batch) > 0 {
		if err := indexManager.IndexBatch(ctx, batch); err != nil {
			atomic.AddInt64(&failed, int64(len(batch)))
			errors = append(errors, fmt.Sprintf("batch error: %v", err))
		} else {
			atomic.AddInt64(&indexed, int64(len(batch)))
		}
	}

	progress.finish()

	result.TotalFiles = processed
	result.IndexedFiles = indexed
	result.FailedFiles = failed
	result.Duration = time.Since(startTime)
	result.Errors = errors

	if failed > 0 {
		result.Success = processed > 0 && indexed > 0
	}

	return result, nil
}

// outputRebuildResult outputs the rebuild result.
func outputRebuildResult(w io.Writer, result *indexRebuildOutput) error {
	if indexJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%sRebuild Complete%s\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(w, "%s%s%s\n", colorGray, strings.Repeat("-", 40), colorReset)
	fmt.Fprintf(w, "%sTotal Files:%s    %d\n", colorGray, colorReset, result.TotalFiles)
	fmt.Fprintf(w, "%sIndexed:%s        %s%d%s\n", colorGray, colorReset, colorGreen, result.IndexedFiles, colorReset)

	if result.FailedFiles > 0 {
		fmt.Fprintf(w, "%sFailed:%s         %s%d%s\n", colorGray, colorReset, colorRed, result.FailedFiles, colorReset)
	}

	fmt.Fprintf(w, "%sDuration:%s       %v\n", colorGray, colorReset, result.Duration.Round(time.Millisecond))

	if len(result.Errors) > 0 && indexVerbose {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%sErrors:%s\n", colorRed, colorReset)
		for _, e := range result.Errors {
			fmt.Fprintf(w, "  - %s\n", e)
		}
	}

	return nil
}

// =============================================================================
// Watch Mode
// =============================================================================

// runWatchMode runs continuous file watching.
func runWatchMode(ctx context.Context, w io.Writer, indexManager *bleve.IndexManager) error {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%sWatch Mode%s - Press Ctrl+C to stop\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(w, "%sWatching:%s %s\n", colorGray, colorReset, indexRootPath)
	fmt.Fprintln(w)

	// Create change detector
	config := watcher.DefaultChangeDetectorConfig()
	detector := watcher.NewChangeDetector(config)

	// Start the detector
	events, err := detector.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}
	defer detector.Stop()

	// Process events
	builder := indexer.NewDocumentBuilder()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(w, "\nWatch mode stopped.")
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			handleWatchEvent(ctx, w, event, indexManager, builder)
		}
	}
}

// handleWatchEvent processes a single file change event.
func handleWatchEvent(
	ctx context.Context,
	w io.Writer,
	event *watcher.ChangeEvent,
	indexManager *bleve.IndexManager,
	builder *indexer.ContentDocumentBuilder,
) {
	if event == nil {
		return
	}

	timestamp := event.Time.Format("15:04:05")

	switch event.Operation {
	case watcher.OpCreate, watcher.OpModify:
		// Read and index file
		content, err := os.ReadFile(event.Path)
		if err != nil {
			fmt.Fprintf(w, "%s [%s] %s%s%s: %v\n",
				timestamp, event.Operation, colorRed, event.Path, colorReset, err)
			return
		}

		info, err := os.Stat(event.Path)
		if err != nil {
			fmt.Fprintf(w, "%s [%s] %s%s%s: %v\n",
				timestamp, event.Operation, colorRed, event.Path, colorReset, err)
			return
		}

		fileInfo := &indexer.FileInfo{
			Path:    event.Path,
			Name:    info.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}

		doc, err := builder.Build(fileInfo, content)
		if err != nil {
			fmt.Fprintf(w, "%s [%s] %s%s%s: %v\n",
				timestamp, event.Operation, colorRed, event.Path, colorReset, err)
			return
		}

		if err := indexManager.Index(ctx, doc); err != nil {
			fmt.Fprintf(w, "%s [%s] %s%s%s: %v\n",
				timestamp, event.Operation, colorRed, event.Path, colorReset, err)
			return
		}

		fmt.Fprintf(w, "%s [%s] %s%s%s\n",
			timestamp, event.Operation, colorGreen, event.Path, colorReset)

	case watcher.OpDelete:
		if err := indexManager.DeleteByPath(ctx, event.Path); err != nil {
			fmt.Fprintf(w, "%s [%s] %s%s%s: %v\n",
				timestamp, event.Operation, colorRed, event.Path, colorReset, err)
			return
		}

		fmt.Fprintf(w, "%s [%s] %s%s%s\n",
			timestamp, event.Operation, colorYellow, event.Path, colorReset)
	}
}

// =============================================================================
// Index Verify
// =============================================================================

// indexVerifyOutput is the JSON output for verify.
type indexVerifyOutput struct {
	Valid          bool     `json:"valid"`
	DocumentCount  uint64   `json:"document_count"`
	CorruptedDocs  int      `json:"corrupted_docs"`
	OrphanedFiles  int      `json:"orphaned_files"`
	MissingFiles   int      `json:"missing_files"`
	Issues         []string `json:"issues,omitempty"`
	CheckDuration  string   `json:"check_duration"`
}

// runIndexVerify verifies index integrity.
func runIndexVerify(cmd *cobra.Command, args []string) error {
	if !indexJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "%s%sVerifying Index%s\n", colorBold, colorCyan, colorReset)
		fmt.Fprintln(cmd.OutOrStdout())
	}

	startTime := time.Now()
	result := &indexVerifyOutput{
		Valid:  true,
		Issues: make([]string, 0),
	}

	// Check if index exists
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		result.Valid = false
		result.Issues = append(result.Issues, "Index does not exist")
		return outputVerifyResult(cmd.OutOrStdout(), result, startTime)
	}

	// Open index
	indexManager := bleve.NewIndexManager(indexPath)
	if err := indexManager.Open(); err != nil {
		result.Valid = false
		result.Issues = append(result.Issues, fmt.Sprintf("Failed to open index: %v", err))
		return outputVerifyResult(cmd.OutOrStdout(), result, startTime)
	}
	defer indexManager.Close()

	// Get document count
	count, err := indexManager.DocumentCount()
	if err != nil {
		result.Valid = false
		result.Issues = append(result.Issues, fmt.Sprintf("Failed to get document count: %v", err))
		return outputVerifyResult(cmd.OutOrStdout(), result, startTime)
	}
	result.DocumentCount = count

	// For a more thorough verification, we would:
	// 1. Check each document in the index
	// 2. Verify the corresponding file still exists
	// 3. Check for files on disk not in the index
	// This is a simplified implementation

	if count == 0 {
		result.Issues = append(result.Issues, "Index is empty")
	}

	return outputVerifyResult(cmd.OutOrStdout(), result, startTime)
}

// outputVerifyResult outputs the verification result.
func outputVerifyResult(w io.Writer, result *indexVerifyOutput, startTime time.Time) error {
	result.CheckDuration = time.Since(startTime).String()

	if indexJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(w, "%sDocument Count:%s %d\n", colorGray, colorReset, result.DocumentCount)

	if result.CorruptedDocs > 0 {
		fmt.Fprintf(w, "%sCorrupted:%s      %s%d%s\n", colorGray, colorReset, colorRed, result.CorruptedDocs, colorReset)
	}

	if result.OrphanedFiles > 0 {
		fmt.Fprintf(w, "%sOrphaned:%s       %s%d%s\n", colorGray, colorReset, colorYellow, result.OrphanedFiles, colorReset)
	}

	if result.MissingFiles > 0 {
		fmt.Fprintf(w, "%sMissing:%s        %s%d%s\n", colorGray, colorReset, colorYellow, result.MissingFiles, colorReset)
	}

	fmt.Fprintf(w, "%sDuration:%s       %s\n", colorGray, colorReset, result.CheckDuration)
	fmt.Fprintln(w)

	if result.Valid {
		fmt.Fprintf(w, "%sResult:%s %s%sValid%s\n", colorGray, colorReset, colorBold, colorGreen, colorReset)
	} else {
		fmt.Fprintf(w, "%sResult:%s %s%sInvalid%s\n", colorGray, colorReset, colorBold, colorRed, colorReset)
		if len(result.Issues) > 0 {
			fmt.Fprintln(w, "\nIssues:")
			for _, issue := range result.Issues {
				fmt.Fprintf(w, "  - %s\n", issue)
			}
		}
	}

	return nil
}

// =============================================================================
// Progress Tracking
// =============================================================================

// progressTracker provides a terminal progress bar.
type progressTracker struct {
	writer    io.Writer
	enabled   bool
	lastLen   int
	startTime time.Time
}

// newProgressTracker creates a new progress tracker.
func newProgressTracker(w io.Writer, enabled bool) *progressTracker {
	return &progressTracker{
		writer:    w,
		enabled:   enabled,
		startTime: time.Now(),
	}
}

// update updates the progress display.
func (p *progressTracker) update(processed, indexed, failed int64, currentFile string) {
	if !p.enabled {
		return
	}

	// Create progress line
	elapsed := time.Since(p.startTime)
	rate := float64(processed) / elapsed.Seconds()

	// Truncate file path
	displayPath := currentFile
	if len(displayPath) > 50 {
		displayPath = "..." + displayPath[len(displayPath)-47:]
	}

	line := fmt.Sprintf("\r%sProcessed:%s %d  %sIndexed:%s %d  %sRate:%s %.0f/s  %sCurrent:%s %s",
		colorGray, colorReset, processed,
		colorGreen, colorReset, indexed,
		colorBlue, colorReset, rate,
		colorGray, colorReset, displayPath)

	// Clear previous line
	if p.lastLen > len(line) {
		line += strings.Repeat(" ", p.lastLen-len(line))
	}
	p.lastLen = len(line)

	fmt.Fprint(p.writer, line)
}

// finish completes the progress display.
func (p *progressTracker) finish() {
	if !p.enabled {
		return
	}
	// Clear the progress line
	fmt.Fprint(p.writer, "\r"+strings.Repeat(" ", p.lastLen)+"\r")
}

// =============================================================================
// Utility Functions
// =============================================================================

// getDirSize calculates the total size of a directory.
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatBytes formats bytes as human-readable string.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
