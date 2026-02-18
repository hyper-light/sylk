package git

import (
	"errors"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
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

// GetBlameInfo returns blame information for a file at HEAD using go-git's
// native blame algorithm.
func (c *GitClient) GetBlameInfo(path string) (*BlameResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	commit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	result, err := gogit.Blame(commit, path)
	if err != nil {
		return nil, err
	}

	lines := make([]BlameLine, len(result.Lines))
	for i, line := range result.Lines {
		lines[i] = BlameLine{
			LineNumber:  i + 1,
			CommitHash:  line.Hash.String(),
			Author:      line.AuthorName,
			AuthorEmail: line.Author,
			AuthorTime:  line.Date,
			Content:     line.Text,
		}
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

// getBlameRangeInternal performs blame and slices the result to the requested
// range. go-git's Blame API always blames the entire file, so we slice after.
func (c *GitClient) getBlameRangeInternal(path string, startLine, endLine int) ([]BlameLine, error) {
	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	commit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	result, err := gogit.Blame(commit, path)
	if err != nil {
		return nil, err
	}

	totalLines := len(result.Lines)
	if startLine > totalLines || endLine > totalLines {
		return nil, ErrLineOutOfBounds
	}

	lines := make([]BlameLine, 0, endLine-startLine+1)
	for i := startLine - 1; i < endLine; i++ {
		line := result.Lines[i]
		lines = append(lines, BlameLine{
			LineNumber:  i + 1,
			CommitHash:  line.Hash.String(),
			Author:      line.AuthorName,
			AuthorEmail: line.Author,
			AuthorTime:  line.Date,
			Content:     line.Text,
		})
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
