package versioning

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var ErrPersistentSessionStateDisabled = errors.New("session vfs: persistent session state is disabled in strict RAM mode")

func openSessionSemanticWAL(cfg SessionVFSConfig) (SemanticWAL, error) {
	if cfg.PersistSessionState {
		return nil, ErrPersistentSessionStateDisabled
	}
	if strings.TrimSpace(cfg.StorageRoot) != "" {
		return nil, fmt.Errorf("session vfs: storage root %s: %w", filepath.Clean(cfg.StorageRoot), ErrPersistentSessionStateDisabled)
	}
	return NewMemoryVersionedWAL(), nil
}
