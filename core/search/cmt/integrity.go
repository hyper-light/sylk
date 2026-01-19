package cmt

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// Issue Types
// =============================================================================

// IssueType categorizes integrity issues.
type IssueType int

const (
	// IssueMissing indicates a file is in the CMT but not on disk.
	IssueMissing IssueType = iota

	// IssueExtra indicates a file is on disk but not in the CMT.
	IssueExtra

	// IssueModified indicates a file's content has changed.
	IssueModified

	// IssueMerkleHash indicates a Merkle hash doesn't match computed value.
	IssueMerkleHash
)

// =============================================================================
// IntegrityIssue
// =============================================================================

// IntegrityIssue represents a detected integrity problem.
type IntegrityIssue struct {
	Type    IssueType
	Path    string
	Details string
}

// =============================================================================
// IntegrityReport
// =============================================================================

// IntegrityReport contains results of integrity check.
type IntegrityReport struct {
	Valid      bool
	TotalFiles int64
	Issues     []IntegrityIssue
	CheckedAt  time.Time
	Duration   time.Duration
}

// =============================================================================
// IntegrityChecker
// =============================================================================

// IntegrityChecker validates CMT against filesystem.
type IntegrityChecker struct {
	root     *Node
	rootPath string
	workers  int
}

// integrityWorkItem is a work item for parallel file checking.
type integrityWorkItem struct {
	path string
	info *FileInfo
}

// NewIntegrityChecker creates a new checker.
func NewIntegrityChecker(root *Node, rootPath string, workers int) *IntegrityChecker {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	return &IntegrityChecker{
		root:     root,
		rootPath: rootPath,
		workers:  workers,
	}
}

// Check performs full integrity validation.
func (c *IntegrityChecker) Check(ctx context.Context) (*IntegrityReport, error) {
	start := time.Now()
	report := c.initReport(start)

	if c.root == nil {
		return c.finalizeReport(report, start), nil
	}

	treePaths := c.collectTreePaths()
	report.TotalFiles = int64(len(treePaths))

	issues, err := c.gatherAllIssues(ctx, treePaths)
	if err != nil {
		return nil, err
	}

	report.Issues = issues
	return c.finalizeReport(report, start), nil
}

// initReport creates an initial empty report.
func (c *IntegrityChecker) initReport(start time.Time) *IntegrityReport {
	return &IntegrityReport{
		Issues:    make([]IntegrityIssue, 0),
		CheckedAt: start,
		Valid:     true,
	}
}

// finalizeReport sets final report fields.
func (c *IntegrityChecker) finalizeReport(report *IntegrityReport, start time.Time) *IntegrityReport {
	report.Valid = len(report.Issues) == 0
	report.Duration = time.Since(start)
	return report
}

// gatherAllIssues collects all integrity issues.
func (c *IntegrityChecker) gatherAllIssues(ctx context.Context, treePaths map[string]*FileInfo) ([]IntegrityIssue, error) {
	var allIssues []IntegrityIssue

	fileIssues, err := c.checkFilesParallel(ctx, treePaths)
	if err != nil {
		return nil, err
	}
	allIssues = append(allIssues, fileIssues...)

	extraIssues, err := c.findExtraFiles(ctx, treePaths)
	if err != nil {
		return nil, err
	}
	allIssues = append(allIssues, extraIssues...)

	merkleIssues := c.checkMerkleHashes()
	allIssues = append(allIssues, merkleIssues...)

	return allIssues, nil
}

// collectTreePaths returns all file paths from the tree.
func (c *IntegrityChecker) collectTreePaths() map[string]*FileInfo {
	paths := make(map[string]*FileInfo)
	c.collectPathsRecursive(c.root, paths)
	return paths
}

// collectPathsRecursive traverses the tree collecting paths.
func (c *IntegrityChecker) collectPathsRecursive(node *Node, paths map[string]*FileInfo) {
	if node == nil {
		return
	}
	if node.Info != nil {
		paths[node.Key] = node.Info
	}
	c.collectPathsRecursive(node.Left, paths)
	c.collectPathsRecursive(node.Right, paths)
}

// checkFilesParallel checks tree files against disk in parallel.
func (c *IntegrityChecker) checkFilesParallel(ctx context.Context, paths map[string]*FileInfo) ([]IntegrityIssue, error) {
	work := c.createWorkChannel(paths)
	results := make(chan *IntegrityIssue, len(paths))
	errChan := make(chan error, 1)

	c.startWorkers(ctx, work, results, errChan)

	issues := c.collectResults(results)
	return issues, c.checkForErrors(errChan)
}

// createWorkChannel creates and populates the work channel.
func (c *IntegrityChecker) createWorkChannel(paths map[string]*FileInfo) <-chan integrityWorkItem {
	work := make(chan integrityWorkItem, len(paths))
	for path, info := range paths {
		work <- integrityWorkItem{path: path, info: info}
	}
	close(work)
	return work
}

// startWorkers starts the worker pool.
func (c *IntegrityChecker) startWorkers(ctx context.Context, work <-chan integrityWorkItem, results chan<- *IntegrityIssue, errChan chan<- error) {
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.fileCheckWorker(ctx, work, results, errChan)
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()
}

// collectResults collects all results from the results channel.
func (c *IntegrityChecker) collectResults(results <-chan *IntegrityIssue) []IntegrityIssue {
	var issues []IntegrityIssue
	for issue := range results {
		if issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// checkForErrors checks for errors from workers.
func (c *IntegrityChecker) checkForErrors(errChan <-chan error) error {
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

// fileCheckWorker processes work items and sends results.
func (c *IntegrityChecker) fileCheckWorker(
	ctx context.Context,
	work <-chan integrityWorkItem,
	results chan<- *IntegrityIssue,
	errChan chan<- error,
) {
	for item := range work {
		if err := ctx.Err(); err != nil {
			c.sendError(errChan, err)
			return
		}
		results <- c.checkSingleFile(item.path, item.info)
	}
}

// sendError sends an error to the error channel if possible.
func (c *IntegrityChecker) sendError(errChan chan<- error, err error) {
	select {
	case errChan <- err:
	default:
	}
}

// checkSingleFile checks a single file against tree info.
func (c *IntegrityChecker) checkSingleFile(path string, info *FileInfo) *IntegrityIssue {
	fullPath := filepath.Join(c.rootPath, path)

	stat, err := os.Stat(fullPath)
	if err != nil {
		return c.handleStatError(path, err)
	}

	if stat.IsDir() {
		return nil
	}

	return c.verifyFileHash(fullPath, path, info)
}

// handleStatError handles errors from os.Stat.
func (c *IntegrityChecker) handleStatError(path string, err error) *IntegrityIssue {
	if os.IsNotExist(err) {
		return &IntegrityIssue{
			Type:    IssueMissing,
			Path:    path,
			Details: "file not found on disk",
		}
	}
	return &IntegrityIssue{
		Type:    IssueMissing,
		Path:    path,
		Details: err.Error(),
	}
}

// verifyFileHash verifies the file hash matches.
func (c *IntegrityChecker) verifyFileHash(fullPath, path string, info *FileInfo) *IntegrityIssue {
	currentHash, err := computeFileHash(fullPath)
	if err != nil {
		return &IntegrityIssue{
			Type:    IssueModified,
			Path:    path,
			Details: "failed to read file: " + err.Error(),
		}
	}

	if currentHash != info.ContentHash {
		return &IntegrityIssue{
			Type:    IssueModified,
			Path:    path,
			Details: "content hash mismatch",
		}
	}

	return nil
}

// computeFileHash computes SHA-256 hash of a file.
func computeFileHash(path string) (Hash, error) {
	f, err := os.Open(path)
	if err != nil {
		return Hash{}, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Hash{}, err
	}

	var hash Hash
	copy(hash[:], h.Sum(nil))
	return hash, nil
}

// findExtraFiles finds files on disk that aren't in the tree.
func (c *IntegrityChecker) findExtraFiles(ctx context.Context, treePaths map[string]*FileInfo) ([]IntegrityIssue, error) {
	var issues []IntegrityIssue

	err := filepath.WalkDir(c.rootPath, func(path string, d os.DirEntry, err error) error {
		return c.processExtraFilesEntry(ctx, path, d, err, treePaths, &issues)
	})

	return issues, filterContextError(err)
}

// processExtraFilesEntry processes a single entry when finding extra files.
func (c *IntegrityChecker) processExtraFilesEntry(ctx context.Context, path string, d os.DirEntry, err error, treePaths map[string]*FileInfo, issues *[]IntegrityIssue) error {
	if err != nil {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return c.checkIfExtraFile(path, d, treePaths, issues)
}

// checkIfExtraFile checks if a file is extra and records it.
func (c *IntegrityChecker) checkIfExtraFile(path string, d os.DirEntry, treePaths map[string]*FileInfo, issues *[]IntegrityIssue) error {
	if isHidden(d.Name()) {
		return skipHiddenEntry(d)
	}

	if d.IsDir() {
		return nil
	}

	relPath, err := filepath.Rel(c.rootPath, path)
	if err != nil {
		return nil
	}

	c.recordExtraFile(relPath, treePaths, issues)
	return nil
}

// recordExtraFile records an extra file if not in tree.
func (c *IntegrityChecker) recordExtraFile(relPath string, treePaths map[string]*FileInfo, issues *[]IntegrityIssue) {
	if _, exists := treePaths[relPath]; !exists {
		*issues = append(*issues, IntegrityIssue{
			Type:    IssueExtra,
			Path:    relPath,
			Details: "file not tracked in tree",
		})
	}
}

// filterContextError filters out context errors.
func filterContextError(err error) error {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return nil
	}
	return err
}

// isHidden returns true if the name starts with a dot.
func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// checkMerkleHashes verifies all Merkle hashes in the tree.
func (c *IntegrityChecker) checkMerkleHashes() []IntegrityIssue {
	var issues []IntegrityIssue
	c.checkMerkleRecursive(c.root, &issues)
	return issues
}

// checkMerkleRecursive recursively verifies Merkle hashes.
func (c *IntegrityChecker) checkMerkleRecursive(node *Node, issues *[]IntegrityIssue) {
	if node == nil {
		return
	}

	c.checkMerkleRecursive(node.Left, issues)
	c.checkMerkleRecursive(node.Right, issues)

	if !VerifyNodeHash(node) {
		*issues = append(*issues, IntegrityIssue{
			Type:    IssueMerkleHash,
			Path:    node.Key,
			Details: "merkle hash mismatch",
		})
	}
}

// CheckFile checks a single file.
func (c *IntegrityChecker) CheckFile(ctx context.Context, path string) (*IntegrityIssue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	node := Get(c.root, path)
	if node == nil {
		return nil, nil
	}

	return c.checkSingleFile(path, node.Info), nil
}
