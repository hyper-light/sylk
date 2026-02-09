package sylkdir

import (
	"fmt"
	"os"
)

// RecoveryConfig holds dependencies for startup crash recovery.
type RecoveryConfig struct {
	SylkDir    *SylkDir
	GlobalMeta *GlobalMeta
	CommitWAL  *CommitWAL              // nil skips commit recovery
	BleveStore *GlobalVersionBleveStore // nil skips Bleve recovery
}

// RecoveryResult describes the outcome of startup recovery.
type RecoveryResult struct {
	IncompleteCommits  int
	VersionsRemoved    []SemanticVersion
	BleveRecovered     bool
	BleveDocsReindexed int
}

// RunRecovery performs startup crash recovery in two phases:
//  1. Find and clean up incomplete commits (partial version directories).
//  2. Rebuild the Bleve index if it is behind HEAD.
func RunRecovery(cfg RecoveryConfig) (*RecoveryResult, error) {
	if err := validateRecoveryConfig(cfg); err != nil {
		return nil, err
	}

	result := &RecoveryResult{}

	if err := recoverCommits(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: commits: %w", err)
	}

	if err := recoverBleveIndex(cfg, result); err != nil {
		return result, fmt.Errorf("recovery: bleve: %w", err)
	}

	return result, nil
}

// validateRecoveryConfig checks that required fields are set.
func validateRecoveryConfig(cfg RecoveryConfig) error {
	if cfg.SylkDir == nil || cfg.GlobalMeta == nil {
		return fmt.Errorf("recovery: SylkDir and GlobalMeta are required")
	}
	return nil
}

// recoverCommits finds incomplete commits and removes their partial version directories.
func recoverCommits(cfg RecoveryConfig, result *RecoveryResult) error {
	if cfg.CommitWAL == nil {
		return nil
	}

	incomplete, err := cfg.CommitWAL.FindIncompleteCommits()
	if err != nil {
		return fmt.Errorf("find incomplete commits: %w", err)
	}

	result.IncompleteCommits = len(incomplete)

	for _, ic := range incomplete {
		if err := removePartialVersion(cfg.SylkDir, ic.GlobalVersion); err != nil {
			return err
		}
		result.VersionsRemoved = append(result.VersionsRemoved, ic.GlobalVersion)
	}

	return truncateAfterRecovery(cfg.CommitWAL, incomplete)
}

// removePartialVersion removes a partially-written global version directory.
func removePartialVersion(sd *SylkDir, version SemanticVersion) error {
	versionPath := sd.GlobalVersionPath(version)
	if _, err := os.Stat(versionPath); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(versionPath)
}

// truncateAfterRecovery truncates the commit WAL if incomplete commits were found.
func truncateAfterRecovery(cwal *CommitWAL, incomplete []IncompleteCommit) error {
	if len(incomplete) == 0 {
		return nil
	}
	return cwal.Truncate()
}

// recoverBleveIndex rebuilds the Bleve index if it is behind HEAD.
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
