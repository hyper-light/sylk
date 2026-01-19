package git

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Blame-specific errors.
var (
	ErrInvalidLineRange = errors.New("invalid line range")
	ErrLineOutOfBounds  = errors.New("line number out of bounds")
)

// blameCacheEntry wraps a BlameResult with expiration time.
type blameCacheEntry struct {
	result    *BlameResult
	expiresAt time.Time
}

// BlameCache caches blame results to avoid repeated computation.
type BlameCache struct {
	cache map[string]*blameCacheEntry
	mu    sync.RWMutex
	ttl   time.Duration
}

// NewBlameCache creates a blame cache with the given TTL.
func NewBlameCache(ttl time.Duration) *BlameCache {
	return &BlameCache{
		cache: make(map[string]*blameCacheEntry),
		ttl:   ttl,
	}
}

// Get retrieves a cached blame result if it exists and is not expired.
func (c *BlameCache) Get(path string) (*BlameResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[path]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.result, true
}

// Set stores a blame result in the cache.
func (c *BlameCache) Set(path string, result *BlameResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[path] = &blameCacheEntry{
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Delete removes an entry from the cache.
func (c *BlameCache) Delete(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, path)
}

// Clear removes all entries from the cache.
func (c *BlameCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*blameCacheEntry)
}

// GetBlameInfo returns blame information for a file.
// Uses the command-line git blame for reliable porcelain output.
func (c *GitClient) GetBlameInfo(path string) (*BlameResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	output, err := c.runGitCommand("blame", "--porcelain", path)
	if err != nil {
		return nil, err
	}

	lines, err := parseBlameOutput(output)
	if err != nil {
		return nil, err
	}

	return &BlameResult{
		Path:  path,
		Lines: lines,
	}, nil
}

// GetBlameInfoCached returns blame with caching support.
func (c *GitClient) GetBlameInfoCached(path string, cache *BlameCache) (*BlameResult, error) {
	if cached, found := cache.Get(path); found {
		return cached, nil
	}

	result, err := c.GetBlameInfo(path)
	if err != nil {
		return nil, err
	}

	cache.Set(path, result)
	return result, nil
}

// GetBlameRange returns blame for a specific line range (1-indexed, inclusive).
func (c *GitClient) GetBlameRange(path string, startLine, endLine int) ([]BlameLine, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if err := validateLineRange(startLine, endLine); err != nil {
		return nil, err
	}

	return c.getBlameRangeInternal(path, startLine, endLine)
}

// getBlameRangeInternal performs the blame range operation without locking.
func (c *GitClient) getBlameRangeInternal(path string, startLine, endLine int) ([]BlameLine, error) {
	rangeArg := fmt.Sprintf("-L%d,%d", startLine, endLine)
	output, err := c.runGitCommand("blame", "--porcelain", rangeArg, path)
	if err != nil {
		return nil, err
	}

	return parseAndValidateBlameRange(output, startLine, endLine)
}

// parseAndValidateBlameRange parses and validates blame output for a range.
func parseAndValidateBlameRange(output string, startLine, endLine int) ([]BlameLine, error) {
	lines, err := parseBlameOutput(output)
	if err != nil {
		return nil, err
	}

	expectedLines := endLine - startLine + 1
	if len(lines) != expectedLines {
		return nil, ErrLineOutOfBounds
	}

	return lines, nil
}

// validateLineRange checks if the line range is valid.
func validateLineRange(start, end int) error {
	if start < 1 {
		return ErrInvalidLineRange
	}
	if start > end {
		return ErrInvalidLineRange
	}
	return nil
}

// parseBlameOutput parses git blame --porcelain output into BlameLines.
func parseBlameOutput(output string) ([]BlameLine, error) {
	if output == "" {
		return []BlameLine{}, nil
	}

	return scanBlameLines(output)
}

// scanBlameLines scans through blame output and extracts lines.
func scanBlameLines(output string) ([]BlameLine, error) {
	var lines []BlameLine
	scanner := bufio.NewScanner(strings.NewReader(output))
	commitInfo := make(map[string]*blameCommitMeta)
	var currentLine *BlameLine

	for scanner.Scan() {
		currentLine, lines = processAndCollect(scanner.Text(), currentLine, commitInfo, lines)
	}

	return lines, scanner.Err()
}

// processAndCollect processes a line and collects completed entries.
func processAndCollect(text string, current *BlameLine, commits map[string]*blameCommitMeta, lines []BlameLine) (*BlameLine, []BlameLine) {
	entry, done := processLine(text, current, commits)
	if done && entry != nil {
		return nil, append(lines, *entry)
	}
	return entry, lines
}

// blameCommitMeta holds author and date for a commit.
type blameCommitMeta struct {
	author      string
	authorEmail string
	authorTime  time.Time
}

// processLine handles a single line of porcelain output.
// Returns the current entry and whether it's complete.
func processLine(line string, current *BlameLine, commits map[string]*blameCommitMeta) (*BlameLine, bool) {
	if isContentLine(line) {
		return handleContentLine(line, current)
	}

	if isHeaderLine(line) {
		return parseHeaderLine(line, commits), false
	}

	return handleMetadataLine(line, current, commits)
}

// isContentLine checks if the line contains content (starts with tab).
func isContentLine(line string) bool {
	return strings.HasPrefix(line, "\t")
}

// handleContentLine processes a content line and completes the entry.
func handleContentLine(line string, current *BlameLine) (*BlameLine, bool) {
	if current != nil {
		current.Content = line[1:]
	}
	return current, true
}

// handleMetadataLine processes metadata lines.
func handleMetadataLine(line string, current *BlameLine, commits map[string]*blameCommitMeta) (*BlameLine, bool) {
	if current != nil {
		parseMetadataLine(line, current, commits)
	}
	return current, false
}

// isHeaderLine checks if a line is a blame header line.
func isHeaderLine(line string) bool {
	if len(line) < 40 {
		return false
	}
	if !hasValidHexPrefix(line) {
		return false
	}
	return len(line) == 40 || line[40] == ' '
}

// hasValidHexPrefix checks if the first 40 characters are hex digits.
func hasValidHexPrefix(line string) bool {
	for i := 0; i < 40; i++ {
		if !isHexChar(line[i]) {
			return false
		}
	}
	return true
}

// isHexChar checks if a character is a valid hexadecimal character.
func isHexChar(c byte) bool {
	return isDigit(c) || isLowerHex(c) || isUpperHex(c)
}

// isDigit checks if c is a decimal digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// isLowerHex checks if c is a lowercase hex letter.
func isLowerHex(c byte) bool {
	return c >= 'a' && c <= 'f'
}

// isUpperHex checks if c is an uppercase hex letter.
func isUpperHex(c byte) bool {
	return c >= 'A' && c <= 'F'
}

// parseHeaderLine parses a blame header line.
func parseHeaderLine(line string, commits map[string]*blameCommitMeta) *BlameLine {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil
	}

	hash := parts[0]
	finalLine, _ := strconv.Atoi(parts[2])

	entry := &BlameLine{
		CommitHash: hash,
		LineNumber: finalLine,
	}

	// Copy cached metadata if available
	if meta, ok := commits[hash]; ok {
		entry.Author = meta.author
		entry.AuthorEmail = meta.authorEmail
		entry.AuthorTime = meta.authorTime
	}

	return entry
}

// parseMetadataLine extracts metadata from blame output lines.
func parseMetadataLine(line string, entry *BlameLine, commits map[string]*blameCommitMeta) {
	if handleAuthorLine(line, entry, commits) {
		return
	}
	if handleAuthorMailLine(line, entry, commits) {
		return
	}
	handleAuthorTimeLine(line, entry, commits)
}

// handleAuthorLine processes author metadata.
func handleAuthorLine(line string, entry *BlameLine, commits map[string]*blameCommitMeta) bool {
	if !strings.HasPrefix(line, "author ") {
		return false
	}
	entry.Author = strings.TrimPrefix(line, "author ")
	updateBlameCommitMeta(commits, entry)
	return true
}

// handleAuthorMailLine processes author email metadata.
func handleAuthorMailLine(line string, entry *BlameLine, commits map[string]*blameCommitMeta) bool {
	if !strings.HasPrefix(line, "author-mail ") {
		return false
	}
	email := strings.TrimPrefix(line, "author-mail ")
	entry.AuthorEmail = strings.Trim(email, "<>")
	updateBlameCommitMeta(commits, entry)
	return true
}

// handleAuthorTimeLine processes author time metadata.
func handleAuthorTimeLine(line string, entry *BlameLine, commits map[string]*blameCommitMeta) {
	if !strings.HasPrefix(line, "author-time ") {
		return
	}
	timestamp, err := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64)
	if err != nil {
		return
	}
	entry.AuthorTime = time.Unix(timestamp, 0)
	updateBlameCommitMeta(commits, entry)
}

// updateBlameCommitMeta updates the cached metadata for a commit.
func updateBlameCommitMeta(commits map[string]*blameCommitMeta, entry *BlameLine) {
	meta := getOrCreateCommitMeta(commits, entry.CommitHash)
	copyEntryToMeta(entry, meta)
}

// getOrCreateCommitMeta gets or creates metadata for a commit.
func getOrCreateCommitMeta(commits map[string]*blameCommitMeta, hash string) *blameCommitMeta {
	if _, ok := commits[hash]; !ok {
		commits[hash] = &blameCommitMeta{}
	}
	return commits[hash]
}

// copyEntryToMeta copies non-empty entry fields to metadata.
func copyEntryToMeta(entry *BlameLine, meta *blameCommitMeta) {
	if entry.Author != "" {
		meta.author = entry.Author
	}
	if entry.AuthorEmail != "" {
		meta.authorEmail = entry.AuthorEmail
	}
	if !entry.AuthorTime.IsZero() {
		meta.authorTime = entry.AuthorTime
	}
}
