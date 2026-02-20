package git

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
)

// ErrForcePushRequired indicates that a push would require --force because
// the remote has diverged from the local branch.
var ErrForcePushRequired = errors.New("force push required: remote has diverged")

// checkPushSafety verifies that the local branch is a fast-forward of the
// remote tracking ref. Returns ErrForcePushRequired if the remote has
// commits not present locally.
// Caller must hold at least c.mu.RLock().
func (c *GitClient) checkPushSafety(branchName, remoteName string) error {
	if remoteName == "" {
		remoteName = "origin"
	}

	localHash, err := c.resolveLocalBranch(branchName)
	if err != nil {
		return nil // Can't resolve local → skip safety check.
	}

	remoteHash, err := c.resolveRemoteTrackingRef(branchName, remoteName)
	if err != nil {
		return nil // No remote tracking ref → first push, safe.
	}

	if localHash == remoteHash {
		return nil // Up to date.
	}

	return c.verifyAncestry(localHash, remoteHash, branchName)
}

// resolveLocalBranch returns the hash of a local branch ref.
func (c *GitClient) resolveLocalBranch(name string) (plumbing.Hash, error) {
	refName := plumbing.NewBranchReferenceName(name)
	ref, err := c.repo.Reference(refName, true)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// resolveRemoteTrackingRef returns the hash of the remote tracking ref.
func (c *GitClient) resolveRemoteTrackingRef(branchName, remoteName string) (plumbing.Hash, error) {
	refName := plumbing.ReferenceName(
		fmt.Sprintf("refs/remotes/%s/%s", remoteName, branchName),
	)
	ref, err := c.repo.Reference(refName, true)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// verifyAncestry checks that the remote commit is an ancestor of the local commit.
func (c *GitClient) verifyAncestry(localHash, remoteHash plumbing.Hash, branchName string) error {
	localCommit, err := c.repo.CommitObject(localHash)
	if err != nil {
		return nil // Can't load commit → skip check.
	}

	remoteCommit, err := c.repo.CommitObject(remoteHash)
	if err != nil {
		return nil
	}

	isAncestor, err := remoteCommit.IsAncestor(localCommit)
	if err != nil {
		return nil // Ancestry check failed → skip.
	}

	if !isAncestor {
		return fmt.Errorf("%w: %s diverged from %s",
			ErrForcePushRequired, branchName, "remote")
	}

	return nil
}
