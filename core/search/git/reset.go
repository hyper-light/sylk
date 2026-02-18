package git

import (
	"fmt"

	gogit "github.com/go-git/go-git/v5"
)

// ResetMode specifies how much state to discard during a reset.
type ResetMode int8

const (
	ResetHard  ResetMode = iota // Reset index + working tree (discard changes).
	ResetMixed                  // Reset index only (keep working tree).
	ResetSoft                   // Reset HEAD only (keep index + working tree).
)

// Reset moves HEAD (and optionally the index/working tree) to the given commit.
func (c *GitClient) Reset(commitHash string, mode ResetMode) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	hash, err := c.resolveCommitHash(commitHash)
	if err != nil {
		return fmt.Errorf("resolve commit: %w", err)
	}

	if _, err := c.repo.CommitObject(hash); err != nil {
		return fmt.Errorf("commit not found: %w", err)
	}

	var goMode gogit.ResetMode
	switch mode {
	case ResetHard:
		goMode = gogit.HardReset
	case ResetMixed:
		goMode = gogit.MixedReset
	case ResetSoft:
		goMode = gogit.SoftReset
	default:
		goMode = gogit.MixedReset
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Reset(&gogit.ResetOptions{
		Commit: hash,
		Mode:   goMode,
	})
}
