package versioning

import (
	"fmt"
	"path/filepath"
	"strings"
)

func openSessionSemanticWAL(cfg SessionVFSConfig) (SemanticWAL, error) {
	dir := strings.TrimSpace(cfg.StorageRoot)
	if dir == "" {
		dir = filepath.Join(
			cfg.WorkingDir,
			".sylk",
			"sessions",
			string(cfg.SessionID),
			"versioning",
			"wal",
		)
	}

	wal, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("session vfs: open semantic wal %s: %w", dir, err)
	}
	return wal, nil
}
