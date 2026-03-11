package sylkdir

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RecoveryConfig holds dependencies for startup crash recovery.
type RecoveryConfig struct {
	SylkDir        *SylkDir
	GlobalMeta     *GlobalMeta
	CanonicalIndex *CanonicalKeyIndex       // nil skips canonical index recovery
	CommitWAL      *CommitWAL               // nil skips commit recovery
	BleveStore     *GlobalVersionBleveStore // nil skips Bleve recovery
}

// RecoveryResult describes the outcome of startup recovery.
type RecoveryResult struct {
	IncompleteCommits  int
	VersionsRemoved    []SemanticVersion
	ManifestRepaired   bool
	CanonicalRebuilt   bool
	HeadBefore         SemanticVersion
	HeadAfter          SemanticVersion
	BleveRecovered     bool
	BleveDocsReindexed int
}

// RunRecovery performs startup crash recovery in four phases:
//  1. Remove incomplete unpublished commits from the WAL.
//  2. Validate committed versions and roll HEAD back to the newest valid version.
//  3. Rebuild the canonical index if recovery changed committed state.
//  4. Rebuild the Bleve index if it is behind HEAD.
func RunRecovery(cfg RecoveryConfig) (*RecoveryResult, error) {
	if err := validateRecoveryConfig(cfg); err != nil {
		return nil, err
	}

	result := &RecoveryResult{HeadBefore: cfg.GlobalMeta.GetHead()}

	if err := recoverCommits(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: commits: %w", err)
	}
	if err := repairCommittedState(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: manifest: %w", err)
	}
	if err := recoverCanonicalIndex(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: canonical: %w", err)
	}
	if err := recoverBleveIndex(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: bleve: %w", err)
	}

	result.HeadAfter = cfg.GlobalMeta.GetHead()
	return result, nil
}

func validateRecoveryConfig(cfg RecoveryConfig) error {
	if cfg.SylkDir == nil || cfg.GlobalMeta == nil {
		return fmt.Errorf("recovery: SylkDir and GlobalMeta are required")
	}
	return nil
}

// recoverCommits removes incomplete commits that were never fully published.
func recoverCommits(cfg RecoveryConfig, result *RecoveryResult) error {
	if cfg.CommitWAL == nil {
		return nil
	}

	incomplete, err := cfg.CommitWAL.FindIncompleteCommits()
	if err != nil {
		return fmt.Errorf("find incomplete commits: %w", err)
	}
	result.IncompleteCommits = len(incomplete)

	manifest := cfg.GlobalMeta.GetManifest()
	committed := committedSessionsCopy(cfg.GlobalMeta)
	for _, ic := range incomplete {
		if incompleteCommitWasPublished(ic, manifest, committed) && validateGlobalVersion(cfg.SylkDir, ic.GlobalVersion) == nil {
			continue
		}
		if err := removePartialVersion(cfg.SylkDir, ic.GlobalVersion); err != nil {
			return err
		}
		result.VersionsRemoved = append(result.VersionsRemoved, ic.GlobalVersion)
	}

	return truncateAfterRecovery(cfg.CommitWAL, incomplete)
}

func incompleteCommitWasPublished(ic IncompleteCommit, manifest *GlobalVersionManifest, committed []CommittedSession) bool {
	if manifest == nil || !manifest.Head.Equal(ic.GlobalVersion) {
		return false
	}
	if !manifestHasVersion(manifest, ic.GlobalVersion) {
		return false
	}
	for _, cs := range committed {
		if cs.SessionID == ic.SessionID && cs.GlobalVersion.Equal(ic.GlobalVersion) {
			return true
		}
	}
	return false
}

func removePartialVersion(sd *SylkDir, version SemanticVersion) error {
	versionPath := sd.GlobalVersionPath(version)
	if _, err := os.Stat(versionPath); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(versionPath)
}

func truncateAfterRecovery(cwal *CommitWAL, incomplete []IncompleteCommit) error {
	if len(incomplete) == 0 {
		return nil
	}
	return cwal.Truncate()
}

// repairCommittedState validates committed storage generations and repairs refs/head
// against the surviving commit graph.
func repairCommittedState(cfg RecoveryConfig, result *RecoveryResult) error {
	manifest := cfg.GlobalMeta.GetManifest()
	if manifest == nil {
		return nil
	}
	graph := cfg.GlobalMeta.GetCommitGraph()

	validOnDisk := make(map[string]bool, len(manifest.Versions))
	for _, gv := range manifest.Versions {
		validOnDisk[gv.ID.String()] = validateGlobalVersion(cfg.SylkDir, gv.ID) == nil
	}

	filteredVersions := make([]GlobalVersion, 0, len(manifest.Versions))
	for _, gv := range manifest.Versions {
		if validOnDisk[gv.ID.String()] {
			filteredVersions = append(filteredVersions, gv)
		}
	}
	if len(filteredVersions) == 0 {
		return fmt.Errorf("no valid global versions remain")
	}

	newManifest := &GlobalVersionManifest{Head: manifest.Head, Versions: filteredVersions}

	committed := committedSessionsCopy(cfg.GlobalMeta)
	filteredCommitted := make([]CommittedSession, 0, len(committed))
	for _, cs := range committed {
		if validOnDisk[cs.GlobalVersion.String()] {
			filteredCommitted = append(filteredCommitted, cs)
		}
	}

	repairedGraph, newHead, graphChanged := repairCommitGraph(graph, newManifest, validOnDisk)
	if newHead.IsZero() {
		return fmt.Errorf("no valid global head remains")
	}
	newManifest.Head = newHead

	var repairedLastBleve *SemanticVersion
	lastBleve := cfg.GlobalMeta.GetLastBleveIndexed()
	if !lastBleve.IsZero() && validOnDisk[lastBleve.String()] {
		v := lastBleve
		repairedLastBleve = &v
	}

	needsRepair := !newHead.Equal(manifest.Head) ||
		len(filteredVersions) != len(manifest.Versions) ||
		len(filteredCommitted) != len(committed) ||
		graphChanged ||
		(repairedLastBleve == nil) != lastBleve.IsZero() ||
		(repairedLastBleve != nil && !repairedLastBleve.Equal(lastBleve))
	if !needsRepair {
		return nil
	}

	if err := cfg.GlobalMeta.RepairState(newManifest, newHead, filteredCommitted, repairedGraph, repairedLastBleve); err != nil {
		return err
	}
	result.ManifestRepaired = true
	return nil
}

func repairCommitGraph(
	graph *GlobalCommitGraph,
	manifest *GlobalVersionManifest,
	validOnDisk map[string]bool,
) (*GlobalCommitGraph, SemanticVersion, bool) {
	if graph == nil || len(graph.Commits) == 0 {
		repaired := buildCommitGraphFromManifest(manifest)
		head := SemanticVersion{}
		if repaired != nil {
			if commit, ok := commitByID(repaired.Commits, repaired.HeadCommitID); ok {
				head = commit.StorageVersion
			}
		}
		return repaired, head, true
	}

	repaired := copyGlobalCommitGraph(graph)
	validCommits := make([]GlobalCommit, 0, len(repaired.Commits))
	commitSet := make(map[string]bool, len(repaired.Commits))
	for _, commit := range repaired.Commits {
		if !validOnDisk[commit.StorageVersion.String()] {
			continue
		}
		validCommits = append(validCommits, commit)
		commitSet[commit.ID] = true
	}
	repaired.Commits = validCommits
	for i := range repaired.Commits {
		parents := repaired.Commits[i].ParentCommitIDs[:0]
		for _, parent := range repaired.Commits[i].ParentCommitIDs {
			if commitSet[parent] {
				parents = append(parents, parent)
			}
		}
		repaired.Commits[i].ParentCommitIDs = parents
	}
	for name, id := range repaired.Refs {
		if !commitSet[id] {
			delete(repaired.Refs, name)
		}
	}

	headCommitID := repaired.HeadCommitID
	if !commitSet[headCommitID] {
		headCommitID = ""
	}
	activeRef := repaired.ActiveRef
	if activeRef != "" {
		if id, ok := repaired.Refs[activeRef]; ok {
			headCommitID = id
		} else {
			activeRef = ""
		}
	}
	if headCommitID == "" {
		if id, ok := repaired.Refs[repaired.DefaultRef]; ok {
			headCommitID = id
			activeRef = repaired.DefaultRef
		}
	}
	if headCommitID == "" && len(repaired.Refs) > 0 {
		names := make([]string, 0, len(repaired.Refs))
		for name := range repaired.Refs {
			names = append(names, name)
		}
		sort.Strings(names)
		activeRef = names[0]
		headCommitID = repaired.Refs[activeRef]
	}
	if headCommitID == "" && len(repaired.Commits) > 0 {
		headCommitID = repaired.Commits[len(repaired.Commits)-1].ID
		activeRef = ""
	}

	repaired.HeadCommitID = headCommitID
	repaired.ActiveRef = activeRef
	normalizeCommitGraph(manifest, repaired)

	head := SemanticVersion{}
	if commit, ok := commitByID(repaired.Commits, repaired.HeadCommitID); ok {
		head = commit.StorageVersion
	}
	return repaired, head, !commitGraphEqual(graph, repaired)
}

func commitGraphEqual(a, b *GlobalCommitGraph) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.HeadCommitID != b.HeadCommitID || a.ActiveRef != b.ActiveRef || a.DefaultRef != b.DefaultRef {
		return false
	}
	if len(a.Refs) != len(b.Refs) || len(a.Commits) != len(b.Commits) {
		return false
	}
	for k, v := range a.Refs {
		if b.Refs[k] != v {
			return false
		}
	}
	for i := range a.Commits {
		ac, bc := a.Commits[i], b.Commits[i]
		if ac.ID != bc.ID ||
			!ac.StorageVersion.Equal(bc.StorageVersion) ||
			ac.SessionID != bc.SessionID ||
			!ac.SessionVer.Equal(bc.SessionVer) ||
			!ac.CreatedAt.Equal(bc.CreatedAt) ||
			ac.Stats != bc.Stats ||
			len(ac.ParentCommitIDs) != len(bc.ParentCommitIDs) {
			return false
		}
		for j := range ac.ParentCommitIDs {
			if ac.ParentCommitIDs[j] != bc.ParentCommitIDs[j] {
				return false
			}
		}
	}
	return true
}

func validateGlobalVersion(sd *SylkDir, version SemanticVersion) error {
	versionPath := sd.GlobalVersionPath(version)
	required := []string{
		"meta.json",
		TombstoneFile,
		filepath.Join("nodes", "index.bin"),
		filepath.Join("vectors", "index.bin"),
		filepath.Join("docs", "index.bin"),
	}
	for _, rel := range required {
		path := filepath.Join(versionPath, rel)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("expected file, got directory: %s", path)
		}
	}
	return nil
}

func recoverCanonicalIndex(cfg RecoveryConfig, result *RecoveryResult) error {
	if cfg.CanonicalIndex == nil {
		return nil
	}
	if !result.ManifestRepaired && result.IncompleteCommits == 0 && canonicalIndexExists(cfg.SylkDir) {
		return nil
	}
	if err := cfg.CanonicalIndex.RebuildFromGlobalVersion(cfg.SylkDir, cfg.GlobalMeta.GetHead()); err != nil {
		return err
	}
	result.CanonicalRebuilt = true
	return nil
}

func canonicalIndexExists(sd *SylkDir) bool {
	_, err := os.Stat(filepath.Join(sd.GlobalCanonicalIndexPath(), "canonical_keys.idx"))
	return err == nil
}

func recoverBleveIndex(cfg RecoveryConfig, result *RecoveryResult) error {
	if cfg.BleveStore == nil {
		return nil
	}

	bleveResult, err := RecoverBleve(BleveRecoveryConfig{
		SylkDir:    cfg.SylkDir,
		GlobalMeta: cfg.GlobalMeta,
		BleveStore: cfg.BleveStore,
	})
	if err != nil {
		return err
	}

	result.BleveRecovered = bleveResult.Recovered
	result.BleveDocsReindexed = bleveResult.DocsReindexed
	return nil
}

func manifestHasVersion(manifest *GlobalVersionManifest, version SemanticVersion) bool {
	if manifest == nil {
		return false
	}
	for _, gv := range manifest.Versions {
		if gv.ID.Equal(version) {
			return true
		}
	}
	return false
}

func committedSessionsCopy(m *GlobalMeta) []CommittedSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]CommittedSession(nil), m.CommittedSessions...)
}
