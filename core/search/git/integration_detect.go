package git

import (
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// walkLimit caps the number of commits walked on the target branch
// when building the patch-ID set for integration detection.
const walkLimit = 1000

// IntegrationResult describes whether a commit has already been integrated
// into the target branch (e.g., via a previous cherry-pick).
type IntegrationResult struct {
	CommitHash string
	Subject    string
	Integrated bool
	PatchID    string
}

// DetectIntegratedCommits checks whether the given commits have already been
// applied to the target branch by comparing patch-IDs. Patch-IDs are
// content-based hashes of the diff, making them insensitive to commit
// metadata (author, date, message) and rebase operations.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) DetectIntegratedCommits(commitHashes []string, targetBranch string) ([]IntegrationResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	// Build patch-ID set for the target branch.
	targetRef, err := c.repo.Reference(
		plumbing.NewBranchReferenceName(targetBranch), true,
	)
	if err != nil {
		return nil, err
	}

	targetPatchIDs := c.buildPatchIDSet(targetRef.Hash())

	// Check each source commit against the target set.
	results := make([]IntegrationResult, len(commitHashes))
	for i, hashStr := range commitHashes {
		hash := plumbing.NewHash(hashStr)
		co, cErr := c.repo.CommitObject(hash)
		if cErr != nil {
			results[i] = IntegrationResult{CommitHash: hashStr}
			continue
		}

		pid := c.computePatchID(co)
		results[i] = IntegrationResult{
			CommitHash: hashStr,
			Subject:    extractSubject(co.Message),
			PatchID:    pid,
			Integrated: pid != "" && targetPatchIDs[pid],
		}
	}

	return results, nil
}

// buildPatchIDSet walks the target branch up to walkLimit commits and
// returns a set of patch-IDs. Caller must hold at least RLock.
func (c *GitClient) buildPatchIDSet(from plumbing.Hash) map[string]bool {
	patchIDs := make(map[string]bool, walkLimit)
	hash := from

	for range walkLimit {
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			break
		}
		if pid := c.computePatchID(co); pid != "" {
			patchIDs[pid] = true
		}
		if len(co.ParentHashes) == 0 {
			break
		}
		hash = co.ParentHashes[0] // First-parent only.
	}

	return patchIDs
}

// computePatchID computes a content-based hash of a commit's diff against
// its first parent. The hash is computed from the unified diff text,
// stripping line numbers (only content lines contribute).
// Returns "" for root commits or on error.
func (c *GitClient) computePatchID(co *object.Commit) string {
	if len(co.ParentHashes) == 0 {
		return ""
	}

	parent, err := c.repo.CommitObject(co.ParentHashes[0])
	if err != nil {
		return ""
	}

	parentTree, err := parent.Tree()
	if err != nil {
		return ""
	}

	commitTree, err := co.Tree()
	if err != nil {
		return ""
	}

	changes, err := object.DiffTree(parentTree, commitTree)
	if err != nil {
		return ""
	}

	patch, err := changes.Patch()
	if err != nil {
		return ""
	}

	// Hash the patch content — file names + chunk content, excluding
	// line numbers and metadata for position-independence.
	h := sha256.New()
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if from != nil {
			fmt.Fprintf(h, "F:%s\n", from.Path())
		}
		if to != nil {
			fmt.Fprintf(h, "T:%s\n", to.Path())
		}
		for _, chunk := range fp.Chunks() {
			fmt.Fprintf(h, "%d:", chunk.Type())
			_, _ = io.WriteString(h, chunk.Content())
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}
