package git

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// BranchStashMeta describes a branch-associated stash entry.
type BranchStashMeta struct {
	BranchName string
	RefName    string // refs/sylk/stash/{branch}-{unixNano}
	Timestamp  time.Time
	FileCount  int
}

// StashForBranch stashes the given files and stores the stash hash under
// a branch-specific ref (refs/sylk/stash/{branch}-{unixNano}).
// Caller must hold NO lock — acquires Lock internally.
func (c *GitClient) StashForBranch(branchName string, paths []string) (*BranchStashMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if len(paths) == 0 {
		return nil, nil
	}

	// Create the stash commit via standard stash mechanism.
	if err := c.stashFilesImpl(paths); err != nil {
		return nil, err
	}

	// Read the stash hash that was just created.
	stashRef, err := c.repo.Reference(plumbing.ReferenceName("refs/stash"), true)
	if err != nil {
		return nil, fmt.Errorf("read stash ref: %w", err)
	}

	// Copy the stash hash to a branch-specific sylk ref.
	now := time.Now()
	refName := fmt.Sprintf("%s%s-%d", stashRefPrefix, branchName, now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), stashRef.Hash())
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, fmt.Errorf("set branch stash ref: %w", err)
	}

	return &BranchStashMeta{
		BranchName: branchName,
		RefName:    refName,
		Timestamp:  now,
		FileCount:  len(paths),
	}, nil
}

// UnstashForBranch finds the latest stash ref for the given branch,
// restores the stash, and removes the sylk ref.
// Caller must hold NO lock — acquires Lock internally.
func (c *GitClient) UnstashForBranch(branchName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	// Find the latest branch stash ref.
	prefix := stashRefPrefix + branchName + "-"
	ref, err := c.findLatestSylkRef(prefix)
	if err != nil {
		return fmt.Errorf("no stash found for branch %s", branchName)
	}

	// Set refs/stash to the branch stash hash so unstashFilesImpl can pop it.
	stashTarget := plumbing.NewHashReference(
		plumbing.ReferenceName("refs/stash"), ref.Hash,
	)
	if err := c.repo.Storer.SetReference(stashTarget); err != nil {
		return fmt.Errorf("set stash ref: %w", err)
	}

	// Restore the stash.
	if err := c.unstashFilesImpl(); err != nil {
		return err
	}

	// Remove the sylk ref.
	return c.repo.Storer.RemoveReference(plumbing.ReferenceName(ref.Name))
}

// HasBranchStash reports whether there is at least one stash ref for
// the given branch name.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) HasBranchStash(branchName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return false
	}

	prefix := stashRefPrefix + branchName + "-"
	_, err := c.findLatestSylkRef(prefix)
	return err == nil
}

// ListBranchStashes enumerates all branch-specific stash refs, grouped
// by branch name.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) ListBranchStashes() (map[string][]BranchStashMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	refs, err := c.listSylkRefs(stashRefPrefix, parseBackupRef)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]BranchStashMeta, len(refs))
	for _, ref := range refs {
		branch := extractBranchFromStashRef(ref.Name)
		if branch == "" {
			continue
		}
		result[branch] = append(result[branch], BranchStashMeta{
			BranchName: branch,
			RefName:    ref.Name,
			Timestamp:  ref.Timestamp,
		})
	}

	return result, nil
}

// findLatestSylkRef finds the newest ref matching the given prefix.
// Caller must hold at least RLock.
func (c *GitClient) findLatestSylkRef(prefix string) (*SnapshotRef, error) {
	refs, err := c.listSylkRefs(prefix, parseBackupRef)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("no refs with prefix %s", prefix)
	}
	return &refs[0], nil // Newest first.
}

// extractBranchFromStashRef extracts the branch name from a stash ref name.
// Format: refs/sylk/stash/{branch}-{unixNano}
func extractBranchFromStashRef(refName string) string {
	suffix := strings.TrimPrefix(refName, stashRefPrefix)
	dashIdx := strings.LastIndex(suffix, "-")
	if dashIdx <= 0 {
		return ""
	}
	return suffix[:dashIdx]
}
