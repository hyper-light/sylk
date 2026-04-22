package versioning

import (
	"path/filepath"
	"strings"
)

// openSessionSemanticWAL selects the semantic-WAL backend for a
// session. When StorageRoot is set, the disk-backed VersionedWAL is
// opened at StorageRoot/semantic-wal/; the semantic WAL's entries
// survive process restart. When StorageRoot is empty, the session
// runs in pure RAM mode via the memory WAL — useful for tests that
// don't care about persistence.
//
// The older strict-RAM gate (ErrPersistentSessionStateDisabled) was
// retired when the parallel-global-VFS design landed:
// docs/PARALLEL_GLOBAL_VFS.md requires durability of merges +
// commit-queue transitions across restarts, and the VersionedWAL is
// the canonical on-disk representation.
func openSessionSemanticWAL(cfg SessionVFSConfig) (SemanticWAL, error) {
	root := strings.TrimSpace(cfg.StorageRoot)
	if root == "" {
		return NewMemoryVersionedWAL(), nil
	}
	return OpenVersionedWAL(VersionedWALConfig{
		Dir: filepath.Join(root, "semantic-wal"),
	})
}
