package git

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// createSnapshot saves HEAD and the current branch as a snapshot ref.
// Returns nil if the repo has no HEAD (empty repo).
func (c *GitClient) createSnapshot(op GitOp) (*SnapshotRef, error) {
	head, err := c.repo.Head()
	if err != nil {
		return nil, nil // No HEAD — nothing to snapshot.
	}

	now := time.Now()
	refName := fmt.Sprintf("%s%s-%d", snapRefPrefix, op.String(), now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), head.Hash())

	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, fmt.Errorf("create snapshot ref: %w", err)
	}

	branchRef := ""
	if head.Name().IsBranch() {
		branchRef = head.Name().String()
	}

	return &SnapshotRef{
		Name:      refName,
		Hash:      head.Hash(),
		Op:        op,
		Timestamp: now,
		BranchRef: branchRef,
	}, nil
}

// createStepSnapshot saves HEAD as a per-step snapshot ref for incremental
// rollback during multi-step sequencer operations (rebase, cherry-pick).
// Ref format: "refs/sylk/seq-step/{stepIdx}-{unixNano}".
func (c *GitClient) createStepSnapshot(stepIdx int) (*SnapshotRef, error) {
	head, err := c.repo.Head()
	if err != nil {
		return nil, nil // No HEAD — nothing to snapshot.
	}

	now := time.Now()
	refName := fmt.Sprintf("%s%d-%d", seqStepRefPrefix, stepIdx, now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), head.Hash())

	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, fmt.Errorf("create step snapshot ref: %w", err)
	}

	branchRef := ""
	if head.Name().IsBranch() {
		branchRef = head.Name().String()
	}

	return &SnapshotRef{
		Name:      refName,
		Hash:      head.Hash(),
		Timestamp: now,
		BranchRef: branchRef,
	}, nil
}

// createBranchBackup saves a branch ref as a backup before deletion.
// Returns the created SnapshotRef for index tracking.
func (c *GitClient) createBranchBackup(name string, hash plumbing.Hash) (*SnapshotRef, error) {
	now := time.Now()
	refName := fmt.Sprintf("%s%s-%d", deletedRefPrefix, name, now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), hash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, err
	}
	return &SnapshotRef{Name: refName, Hash: hash, Timestamp: now}, nil
}

// createDetachedBackup saves a detached HEAD commit as a backup ref.
// Returns the created SnapshotRef for index tracking.
func (c *GitClient) createDetachedBackup(hash plumbing.Hash) (*SnapshotRef, error) {
	now := time.Now()
	short := hash.String()[:7]
	refName := fmt.Sprintf("%s%s-%d", detachedRefPrefix, short, now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), hash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, err
	}
	return &SnapshotRef{Name: refName, Hash: hash, Timestamp: now}, nil
}

// restoreSnapshot resets HEAD and the branch ref to the snapshot state.
func (c *GitClient) restoreSnapshot(snap *SnapshotRef) error {
	// If the snapshot recorded a branch, restore the branch ref.
	if snap.BranchRef != "" {
		branchRefName := plumbing.ReferenceName(snap.BranchRef)
		ref := plumbing.NewHashReference(branchRefName, snap.Hash)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return fmt.Errorf("restore branch ref: %w", err)
		}

		// Re-attach HEAD to the branch.
		symRef := plumbing.NewSymbolicReference(plumbing.HEAD, branchRefName)
		if err := c.repo.Storer.SetReference(symRef); err != nil {
			return fmt.Errorf("restore HEAD: %w", err)
		}
	} else {
		// Detached HEAD: set HEAD directly.
		ref := plumbing.NewHashReference(plumbing.HEAD, snap.Hash)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return fmt.Errorf("restore HEAD: %w", err)
		}
	}

	// Reset worktree to match.
	return c.resetWorktreeToHash(snap.Hash)
}

// listSnapshotRefs enumerates all snapshot refs, sorted newest-first.
func (c *GitClient) listSnapshotRefs() ([]SnapshotRef, error) {
	return c.listSylkRefs(snapRefPrefix, parseSnapshotRef)
}

// listDeletedBackups enumerates all deleted branch backup refs.
func (c *GitClient) listDeletedBackups() ([]SnapshotRef, error) {
	return c.listSylkRefs(deletedRefPrefix, parseBackupRef)
}

// listDetachedBackups enumerates all detached HEAD backup refs.
func (c *GitClient) listDetachedBackups() ([]SnapshotRef, error) {
	return c.listSylkRefs(detachedRefPrefix, parseBackupRef)
}

// gcSnapshotRefs removes snapshot refs older than retention or beyond max count.
// Returns the number of refs removed.
func (c *GitClient) gcSnapshotRefs(cfg SafetyConfig) (int, error) {
	refs, err := c.listSnapshotRefs()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-cfg.SnapshotRetention)
	removed := 0

	for i, ref := range refs {
		tooMany := i >= cfg.MaxSnapshots
		tooOld := ref.Timestamp.Before(cutoff)
		if !tooMany && !tooOld {
			continue
		}

		refName := plumbing.ReferenceName(ref.Name)
		if err := c.repo.Storer.RemoveReference(refName); err != nil {
			continue // Best-effort removal.
		}
		removed++
	}

	return removed, nil
}

// resetWorktreeToHash resets the working tree to match the given commit.
func (c *GitClient) resetWorktreeToHash(hash plumbing.Hash) error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Reset(&gogit.ResetOptions{
		Commit: hash,
		Mode:   gogit.HardReset,
	})
}

// listSylkRefs iterates refs with a given prefix and applies a parser.
func (c *GitClient) listSylkRefs(prefix string, parse func(string, plumbing.Hash) *SnapshotRef) ([]SnapshotRef, error) {
	iter, err := c.repo.Storer.IterReferences()
	if err != nil {
		return nil, err
	}

	var refs []SnapshotRef
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		if !strings.HasPrefix(name, prefix) {
			return nil
		}
		if snap := parse(name, ref.Hash()); snap != nil {
			refs = append(refs, *snap)
		}
		return nil
	})

	// Sort newest-first by timestamp.
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Timestamp.After(refs[j].Timestamp)
	})

	return refs, err
}

// parseSnapshotRef parses "refs/sylk/snap/{op}-{unix_ns}" into a SnapshotRef.
func parseSnapshotRef(name string, hash plumbing.Hash) *SnapshotRef {
	suffix := strings.TrimPrefix(name, snapRefPrefix)
	dashIdx := strings.LastIndex(suffix, "-")
	if dashIdx < 0 {
		return nil
	}

	opName := suffix[:dashIdx]
	tsStr := suffix[dashIdx+1:]

	op, ok := ParseGitOp(opName)
	if !ok {
		op = 0 // Unknown op — still keep the ref.
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil
	}

	return &SnapshotRef{
		Name:      name,
		Hash:      hash,
		Op:        op,
		Timestamp: time.Unix(0, ts),
	}
}

// parseBackupRef parses "refs/sylk/{deleted,detached}/{name}-{unix_ns}" into a SnapshotRef.
func parseBackupRef(name string, hash plumbing.Hash) *SnapshotRef {
	// Find the last dash to extract the timestamp.
	dashIdx := strings.LastIndex(name, "-")
	if dashIdx < 0 {
		return nil
	}

	tsStr := name[dashIdx+1:]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil
	}

	return &SnapshotRef{
		Name:      name,
		Hash:      hash,
		Timestamp: time.Unix(0, ts),
	}
}

// Ensure storer interface is available (compile-time check).
var _ storer.ReferenceStorer = (storer.ReferenceStorer)(nil)

// =============================================================================
// refIndex — in-memory cache of sylk refs
// =============================================================================

// refIndex maintains an in-memory cache of sylk refs, eliminating the need
// to iterate all git refs on every lookup. The index is lazily loaded from
// a single full ref scan and kept current via add/remove operations.
type refIndex struct {
	client *GitClient

	mu       sync.RWMutex
	snaps    []SnapshotRef
	deleted  []SnapshotRef
	detached []SnapshotRef
	aborts   []SnapshotRef
	stashes  []SnapshotRef
	seqSteps []SnapshotRef
	loaded   bool
}

func newRefIndex(client *GitClient) *refIndex {
	return &refIndex{client: client}
}

// ensureLoaded populates the index from a full ref scan if not already loaded.
// Uses double-checked locking: fast RLock path, slow Lock path.
func (idx *refIndex) ensureLoaded() {
	idx.mu.RLock()
	if idx.loaded {
		idx.mu.RUnlock()
		return
	}
	idx.mu.RUnlock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.loaded {
		return
	}
	idx.loadLocked()
}

// loadLocked scans all refs and populates the index. Caller must hold write lock.
func (idx *refIndex) loadLocked() {
	iter, err := idx.client.repo.Storer.IterReferences()
	if err != nil {
		idx.loaded = true // Prevent retry storm on persistent error.
		return
	}

	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		hash := ref.Hash()
		switch {
		case strings.HasPrefix(name, snapRefPrefix):
			if snap := parseSnapshotRef(name, hash); snap != nil {
				idx.snaps = append(idx.snaps, *snap)
			}
		case strings.HasPrefix(name, deletedRefPrefix):
			if snap := parseBackupRef(name, hash); snap != nil {
				idx.deleted = append(idx.deleted, *snap)
			}
		case strings.HasPrefix(name, detachedRefPrefix):
			if snap := parseBackupRef(name, hash); snap != nil {
				idx.detached = append(idx.detached, *snap)
			}
		case strings.HasPrefix(name, abortRefPrefix):
			if snap := parseBackupRef(name, hash); snap != nil {
				idx.aborts = append(idx.aborts, *snap)
			}
		case strings.HasPrefix(name, stashRefPrefix):
			if snap := parseBackupRef(name, hash); snap != nil {
				idx.stashes = append(idx.stashes, *snap)
			}
		case strings.HasPrefix(name, seqStepRefPrefix):
			if snap := parseBackupRef(name, hash); snap != nil {
				idx.seqSteps = append(idx.seqSteps, *snap)
			}
		}
		return nil
	})

	sortRefsDesc(idx.snaps)
	sortRefsDesc(idx.deleted)
	sortRefsDesc(idx.detached)
	sortRefsDesc(idx.aborts)
	sortRefsDesc(idx.stashes)
	sortRefsDesc(idx.seqSteps)
	idx.loaded = true
}

// addSnap inserts a snapshot ref, maintaining newest-first order.
func (idx *refIndex) addSnap(ref SnapshotRef) {
	idx.mu.Lock()
	idx.snaps = insertRefSorted(idx.snaps, ref)
	idx.mu.Unlock()
}

// addDeleted inserts a deleted branch backup ref.
func (idx *refIndex) addDeleted(ref SnapshotRef) {
	idx.mu.Lock()
	idx.deleted = insertRefSorted(idx.deleted, ref)
	idx.mu.Unlock()
}

// addDetached inserts a detached HEAD backup ref.
func (idx *refIndex) addDetached(ref SnapshotRef) {
	idx.mu.Lock()
	idx.detached = insertRefSorted(idx.detached, ref)
	idx.mu.Unlock()
}

// addAbort inserts an abort backup ref.
func (idx *refIndex) addAbort(ref SnapshotRef) {
	idx.mu.Lock()
	idx.aborts = insertRefSorted(idx.aborts, ref)
	idx.mu.Unlock()
}

// addStash inserts a branch stash ref.
func (idx *refIndex) addStash(ref SnapshotRef) {
	idx.mu.Lock()
	idx.stashes = insertRefSorted(idx.stashes, ref)
	idx.mu.Unlock()
}

// removeRef removes a ref by name from all index slices.
func (idx *refIndex) removeRef(name string) {
	idx.mu.Lock()
	idx.snaps = removeRefByName(idx.snaps, name)
	idx.deleted = removeRefByName(idx.deleted, name)
	idx.detached = removeRefByName(idx.detached, name)
	idx.aborts = removeRefByName(idx.aborts, name)
	idx.stashes = removeRefByName(idx.stashes, name)
	idx.seqSteps = removeRefByName(idx.seqSteps, name)
	idx.mu.Unlock()
}

// addSeqStep inserts a per-step sequencer snapshot ref.
func (idx *refIndex) addSeqStep(ref SnapshotRef) {
	idx.mu.Lock()
	idx.seqSteps = insertRefSorted(idx.seqSteps, ref)
	idx.mu.Unlock()
}

// listSeqSteps returns a copy of per-step sequencer snapshot refs (newest-first).
func (idx *refIndex) listSeqSteps() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.seqSteps...)
}

// listSnaps returns a copy of snapshot refs (newest-first).
func (idx *refIndex) listSnaps() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.snaps...)
}

// listDeleted returns a copy of deleted branch backup refs.
func (idx *refIndex) listDeleted() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.deleted...)
}

// listDetached returns a copy of detached HEAD backup refs.
func (idx *refIndex) listDetached() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.detached...)
}

// listAborts returns a copy of abort backup refs.
func (idx *refIndex) listAborts() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.aborts...)
}

// listStashes returns a copy of branch stash refs.
func (idx *refIndex) listStashes() []SnapshotRef {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return append([]SnapshotRef(nil), idx.stashes...)
}

// hasStashForBranch reports whether any stash refs exist for the given branch.
func (idx *refIndex) hasStashForBranch(branchName string) bool {
	idx.ensureLoaded()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	prefix := stashRefPrefix + branchName + "-"
	for _, ref := range idx.stashes {
		if strings.HasPrefix(ref.Name, prefix) {
			return true
		}
	}
	return false
}

// sortRefsDesc sorts refs by timestamp, newest first.
func sortRefsDesc(refs []SnapshotRef) {
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Timestamp.After(refs[j].Timestamp)
	})
}

// insertRefSorted inserts a ref into a newest-first sorted slice.
func insertRefSorted(refs []SnapshotRef, ref SnapshotRef) []SnapshotRef {
	pos := 0
	for pos < len(refs) && refs[pos].Timestamp.After(ref.Timestamp) {
		pos++
	}
	refs = append(refs, SnapshotRef{})
	copy(refs[pos+1:], refs[pos:])
	refs[pos] = ref
	return refs
}

// removeRefByName removes the first ref with the given name.
func removeRefByName(refs []SnapshotRef, name string) []SnapshotRef {
	for i, ref := range refs {
		if ref.Name == name {
			return append(refs[:i], refs[i+1:]...)
		}
	}
	return refs
}
