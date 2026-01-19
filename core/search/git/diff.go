package git

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Diff status constants matching git's status output.
const (
	DiffStatusAdded    = "A"
	DiffStatusModified = "M"
	DiffStatusDeleted  = "D"
	DiffStatusRenamed  = "R"
	DiffStatusCopied   = "C"
)

// hunkHeaderRegex matches diff hunk headers like "@@ -1,3 +1,4 @@".
var hunkHeaderRegex = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// GetDiff returns diff between two commits.
func (c *GitClient) GetDiff(fromHash, toHash string) ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	output, err := c.runGitCommand("diff", "--raw", "-M", "-C", fromHash, toHash)
	if err != nil {
		return nil, err
	}

	files := parseRawDiffOutput(output)
	return c.enrichDiffsWithHunks(files, fromHash, toHash)
}

// GetWorkingTreeDiff returns diff of unstaged changes.
func (c *GitClient) GetWorkingTreeDiff() ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	output, err := c.runGitCommand("diff", "--raw", "-M", "-C")
	if err != nil {
		return nil, err
	}

	files := parseRawDiffOutput(output)
	return c.enrichWorkingTreeDiffs(files)
}

// GetStagedDiff returns diff of staged changes.
func (c *GitClient) GetStagedDiff() ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	output, err := c.runGitCommand("diff", "--raw", "-M", "-C", "--cached")
	if err != nil {
		return nil, err
	}

	files := parseRawDiffOutput(output)
	return c.enrichStagedDiffs(files)
}

// GetFileDiff returns diff for a specific file between commits.
// Returns nil if the file was not changed.
func (c *GitClient) GetFileDiff(path, fromHash, toHash string) (*FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getFileDiffInternal(path, fromHash, toHash)
}

// getFileDiffInternal performs the file diff without locking.
func (c *GitClient) getFileDiffInternal(path, fromHash, toHash string) (*FileDiff, error) {
	output, err := c.runGitCommand("diff", "--raw", "-M", "-C", fromHash, toHash, "--", path)
	if err != nil {
		return nil, err
	}

	return c.extractFirstDiff(output, fromHash, toHash)
}

// extractFirstDiff extracts the first diff from raw output.
func (c *GitClient) extractFirstDiff(output, fromHash, toHash string) (*FileDiff, error) {
	files := parseRawDiffOutput(output)
	if len(files) == 0 {
		return nil, nil
	}

	diffs, err := c.enrichDiffsWithHunks(files, fromHash, toHash)
	if err != nil || len(diffs) == 0 {
		return nil, err
	}

	return &diffs[0], nil
}

// parseRawDiffOutput parses git diff --raw output.
func parseRawDiffOutput(output string) []FileDiff {
	if strings.TrimSpace(output) == "" {
		return []FileDiff{}
	}

	var diffs []FileDiff
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if diff := parseRawDiffLine(line); diff != nil {
			diffs = append(diffs, *diff)
		}
	}

	return diffs
}

// parseRawDiffLine parses a single line from git diff --raw output.
func parseRawDiffLine(line string) *FileDiff {
	if !strings.HasPrefix(line, ":") {
		return nil
	}

	parts, status := splitRawDiffLine(line)
	if parts == nil {
		return nil
	}

	return buildFileDiff(parts, status)
}

// splitRawDiffLine splits a raw diff line into its components.
// Returns nil if the line is invalid.
func splitRawDiffLine(line string) ([]string, string) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) < 2 {
		return nil, ""
	}

	metaParts := strings.Fields(parts[0])
	if len(metaParts) < 5 {
		return nil, ""
	}

	return parts, metaParts[4]
}

// buildFileDiff creates a FileDiff from parsed components.
func buildFileDiff(parts []string, status string) *FileDiff {
	diff := &FileDiff{
		Status: extractStatusLetter(status),
	}

	assignPaths(diff, parts)
	return diff
}

// assignPaths assigns paths to the diff based on number of parts.
func assignPaths(diff *FileDiff, parts []string) {
	if len(parts) == 3 {
		diff.OldPath = parts[1]
		diff.Path = parts[2]
	} else {
		diff.Path = parts[1]
	}
}

// extractStatusLetter extracts the single letter status from git's status.
func extractStatusLetter(status string) string {
	if len(status) == 0 {
		return ""
	}
	// Status can be like "R100" for renames with 100% similarity
	return string(status[0])
}

// enrichDiffsWithHunks adds hunk information to file diffs.
func (c *GitClient) enrichDiffsWithHunks(files []FileDiff, fromHash, toHash string) ([]FileDiff, error) {
	if len(files) == 0 {
		return files, nil
	}

	for i := range files {
		if err := c.addHunksToFileDiff(&files[i], fromHash, toHash); err != nil {
			continue // Skip files that fail to get hunks
		}
	}

	return files, nil
}

// enrichWorkingTreeDiffs adds hunk information for working tree diffs.
func (c *GitClient) enrichWorkingTreeDiffs(files []FileDiff) ([]FileDiff, error) {
	if len(files) == 0 {
		return files, nil
	}

	for i := range files {
		if err := c.addWorkingTreeHunks(&files[i]); err != nil {
			continue
		}
	}

	return files, nil
}

// enrichStagedDiffs adds hunk information for staged diffs.
func (c *GitClient) enrichStagedDiffs(files []FileDiff) ([]FileDiff, error) {
	if len(files) == 0 {
		return files, nil
	}

	for i := range files {
		if err := c.addStagedHunks(&files[i]); err != nil {
			continue
		}
	}

	return files, nil
}

// addHunksToFileDiff adds hunk information to a single file diff.
func (c *GitClient) addHunksToFileDiff(diff *FileDiff, fromHash, toHash string) error {
	output, err := c.runGitCommand("diff", fromHash, toHash, "--", diff.Path)
	if err != nil {
		return err
	}

	diff.Hunks, diff.Binary = parseDiffHunks(output)
	diff.Additions, diff.Deletions = countChanges(diff.Hunks)
	return nil
}

// addWorkingTreeHunks adds hunks for working tree changes.
func (c *GitClient) addWorkingTreeHunks(diff *FileDiff) error {
	output, err := c.runGitCommand("diff", "--", diff.Path)
	if err != nil {
		return err
	}

	diff.Hunks, diff.Binary = parseDiffHunks(output)
	diff.Additions, diff.Deletions = countChanges(diff.Hunks)
	return nil
}

// addStagedHunks adds hunks for staged changes.
func (c *GitClient) addStagedHunks(diff *FileDiff) error {
	output, err := c.runGitCommand("diff", "--cached", "--", diff.Path)
	if err != nil {
		return err
	}

	diff.Hunks, diff.Binary = parseDiffHunks(output)
	diff.Additions, diff.Deletions = countChanges(diff.Hunks)
	return nil
}

// parseDiffHunks parses unified diff output into hunks.
// Returns the hunks and whether the file is binary.
func parseDiffHunks(output string) ([]DiffHunk, bool) {
	if strings.Contains(output, "Binary files") {
		return nil, true
	}

	return parseHunksFromOutput(output), false
}

// parseHunksFromOutput parses diff output into a slice of hunks.
func parseHunksFromOutput(output string) []DiffHunk {
	var hunks []DiffHunk
	var currentHunk *DiffHunk

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		currentHunk, hunks = processHunkLine(scanner.Text(), currentHunk, hunks)
	}

	return appendFinalHunk(hunks, currentHunk)
}

// processHunkLine processes a single line during hunk parsing.
func processHunkLine(line string, current *DiffHunk, hunks []DiffHunk) (*DiffHunk, []DiffHunk) {
	if hunk := parseHunkHeader(line); hunk != nil {
		hunks = appendFinalHunk(hunks, current)
		return hunk, hunks
	}

	if current != nil && isHunkContentLine(line) {
		current.Lines = append(current.Lines, line)
	}

	return current, hunks
}

// appendFinalHunk appends the current hunk to the list if it exists.
func appendFinalHunk(hunks []DiffHunk, current *DiffHunk) []DiffHunk {
	if current != nil {
		return append(hunks, *current)
	}
	return hunks
}

// parseHunkHeader parses a hunk header line.
func parseHunkHeader(line string) *DiffHunk {
	matches := hunkHeaderRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	hunk := &DiffHunk{
		OldStart: parseIntOrDefault(matches[1], 1),
		OldLines: parseIntOrDefault(matches[2], 1),
		NewStart: parseIntOrDefault(matches[3], 1),
		NewLines: parseIntOrDefault(matches[4], 1),
	}

	return hunk
}

// parseIntOrDefault parses an integer string or returns a default value.
func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return n
}

// hunkContentPrefixes contains valid prefix characters for hunk content lines.
var hunkContentPrefixes = map[byte]bool{
	'+':  true,
	'-':  true,
	' ':  true,
	'\\': true,
}

// isHunkContentLine checks if a line is part of a hunk's content.
func isHunkContentLine(line string) bool {
	if len(line) == 0 {
		return false
	}
	return hunkContentPrefixes[line[0]]
}

// countChanges counts additions and deletions from hunk lines.
func countChanges(hunks []DiffHunk) (additions, deletions int) {
	for _, hunk := range hunks {
		a, d := countHunkChanges(hunk)
		additions += a
		deletions += d
	}
	return
}

// countHunkChanges counts additions and deletions in a single hunk.
func countHunkChanges(hunk DiffHunk) (additions, deletions int) {
	for _, line := range hunk.Lines {
		additions += countAddition(line)
		deletions += countDeletion(line)
	}
	return
}

// countAddition returns 1 if line is an addition, 0 otherwise.
func countAddition(line string) int {
	if len(line) > 0 && line[0] == '+' {
		return 1
	}
	return 0
}

// countDeletion returns 1 if line is a deletion, 0 otherwise.
func countDeletion(line string) int {
	if len(line) > 0 && line[0] == '-' {
		return 1
	}
	return 0
}
