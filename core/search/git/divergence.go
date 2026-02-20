package git

import (
	"github.com/go-git/go-git/v5/plumbing"
)

// DivergenceInfo describes how a local branch relates to its upstream.
type DivergenceInfo struct {
	BranchName   string
	UpstreamName string // e.g. "origin/main"
	Ahead        int
	Behind       int
}

// ComputeDivergence calculates ahead/behind counts for a single branch
// relative to its configured upstream tracking ref.
// Returns nil if the branch has no upstream configured.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) ComputeDivergence(branchName string) (*DivergenceInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.computeDivergenceInternal(branchName)
}

// ComputeDivergenceBatch calculates ahead/behind counts for multiple branches.
// Shares the upstream reachable set across branches with the same upstream.
// Branches without upstreams are silently skipped.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) ComputeDivergenceBatch(branches []string) ([]DivergenceInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	// Group branches by upstream for shared reachable set computation.
	type upstreamGroup struct {
		refName  string
		hash     plumbing.Hash
		branches []string
	}

	groups := make(map[string]*upstreamGroup)
	for _, name := range branches {
		remote, merge := c.branchUpstream(name)
		if remote == "" || merge == "" {
			continue
		}
		upstream := remote + "/" + merge
		if g, ok := groups[upstream]; ok {
			g.branches = append(g.branches, name)
			continue
		}
		upRef, err := c.repo.Reference(
			plumbing.NewRemoteReferenceName(remote, merge), true,
		)
		if err != nil {
			continue
		}
		groups[upstream] = &upstreamGroup{
			refName:  upstream,
			hash:     upRef.Hash(),
			branches: []string{name},
		}
	}

	var results []DivergenceInfo
	for _, g := range groups {
		upstreamSet := c.reachableSet(g.hash)
		for _, name := range g.branches {
			branchRef, err := c.repo.Reference(
				plumbing.NewBranchReferenceName(name), true,
			)
			if err != nil {
				continue
			}
			if branchRef.Hash() == g.hash {
				results = append(results, DivergenceInfo{
					BranchName:   name,
					UpstreamName: g.refName,
				})
				continue
			}
			branchSet := c.reachableSet(branchRef.Hash())
			ahead, _ := c.countExcluding(branchRef.Hash(), upstreamSet, 0)
			behind, _ := c.countExcluding(g.hash, branchSet, 0)
			results = append(results, DivergenceInfo{
				BranchName:   name,
				UpstreamName: g.refName,
				Ahead:        ahead,
				Behind:       behind,
			})
		}
	}

	return results, nil
}

// computeDivergenceInternal computes divergence for a single branch.
// Caller must hold at least RLock.
func (c *GitClient) computeDivergenceInternal(branchName string) (*DivergenceInfo, error) {
	remote, merge := c.branchUpstream(branchName)
	if remote == "" || merge == "" {
		return nil, nil
	}

	branchRef, err := c.repo.Reference(
		plumbing.NewBranchReferenceName(branchName), true,
	)
	if err != nil {
		return nil, err
	}

	upstreamRef, err := c.repo.Reference(
		plumbing.NewRemoteReferenceName(remote, merge), true,
	)
	if err != nil {
		return nil, nil // Upstream ref not fetched yet.
	}

	upstream := remote + "/" + merge

	if branchRef.Hash() == upstreamRef.Hash() {
		return &DivergenceInfo{
			BranchName:   branchName,
			UpstreamName: upstream,
		}, nil
	}

	branchSet := c.reachableSet(branchRef.Hash())
	upstreamSet := c.reachableSet(upstreamRef.Hash())

	ahead, _ := c.countExcluding(branchRef.Hash(), upstreamSet, 0)
	behind, _ := c.countExcluding(upstreamRef.Hash(), branchSet, 0)

	return &DivergenceInfo{
		BranchName:   branchName,
		UpstreamName: upstream,
		Ahead:        ahead,
		Behind:       behind,
	}, nil
}

// branchUpstream returns the (remote, merge) pair from git config for a branch.
// Returns ("", "") if no upstream is configured.
func (c *GitClient) branchUpstream(branchName string) (remote, merge string) {
	cfg, err := c.repo.Config()
	if err != nil {
		return "", ""
	}

	bc, ok := cfg.Branches[branchName]
	if !ok || bc.Remote == "" || bc.Merge == "" {
		return "", ""
	}

	// bc.Merge is a full ref like "refs/heads/main"; extract the short name.
	mergeName := bc.Merge.Short()
	return bc.Remote, mergeName
}
