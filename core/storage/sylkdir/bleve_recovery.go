package sylkdir

import "fmt"

// BleveRecoveryConfig holds the dependencies for Bleve crash recovery.
type BleveRecoveryConfig struct {
	SylkDir    *SylkDir
	GlobalMeta *GlobalMeta
	BleveStore *GlobalVersionBleveStore
}

// BleveRecoveryResult describes the outcome of Bleve recovery.
type BleveRecoveryResult struct {
	Recovered     bool
	FromVersion   SemanticVersion // Last indexed version before recovery
	ToVersion     SemanticVersion // HEAD version after recovery
	DocsReindexed int
}

// RecoverBleve checks whether the Bleve index is behind HEAD and rebuilds
// if needed. This handles the case where a commit succeeded (data files and
// manifest updated) but the process crashed before Bleve indexing finished.
func RecoverBleve(cfg BleveRecoveryConfig) (*BleveRecoveryResult, error) {
	if err := validateBleveRecoveryConfig(cfg); err != nil {
		return nil, err
	}

	head := cfg.GlobalMeta.GetHead()
	lastIndexed := cfg.GlobalMeta.GetLastBleveIndexed()

	result := &BleveRecoveryResult{
		FromVersion: lastIndexed,
		ToVersion:   head,
	}

	if !needsBleveRecovery(head, lastIndexed) {
		return result, nil
	}

	count, err := rebuildBleveAtHead(cfg)
	if err != nil {
		return result, err
	}

	result.Recovered = true
	result.DocsReindexed = count

	if err := cfg.GlobalMeta.SetLastBleveIndexed(head); err != nil {
		return result, fmt.Errorf("bleve recovery: update last indexed: %w", err)
	}

	return result, nil
}

// validateBleveRecoveryConfig checks that all required fields are set.
func validateBleveRecoveryConfig(cfg BleveRecoveryConfig) error {
	if cfg.SylkDir == nil || cfg.GlobalMeta == nil || cfg.BleveStore == nil {
		return fmt.Errorf("bleve recovery: missing required config fields")
	}
	return nil
}

// needsBleveRecovery returns true if HEAD is ahead of the last indexed version.
func needsBleveRecovery(head, lastIndexed SemanticVersion) bool {
	if head.IsZero() {
		return false
	}
	return !head.Equal(lastIndexed)
}

// rebuildBleveAtHead rebuilds the Bleve index from HEAD's JSONL docs.
// Returns the number of documents reindexed.
func rebuildBleveAtHead(cfg BleveRecoveryConfig) (int, error) {
	head := cfg.GlobalMeta.GetHead()
	cfg.BleveStore.SetHead(head)

	if err := cfg.BleveStore.RebuildHead(); err != nil {
		return 0, fmt.Errorf("bleve recovery: rebuild at %s: %w", head.String(), err)
	}

	count, err := cfg.BleveStore.DocumentCount()
	if err != nil {
		return 0, fmt.Errorf("bleve recovery: count docs: %w", err)
	}

	return int(count), nil
}
