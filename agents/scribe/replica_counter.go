package scribe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// AcquireReplicaGeneration returns the next replica-generation number
// for (sessionDir, agentType) and atomically persists the new value.
//
// Persistence: a single tiny text file at
//
//	{sessionDir}/agents/{agentType}/replica_counter
//
// holds a base-10 integer. On read+increment we write to a sibling
// .tmp file, fsync, then atomically rename — surviving partial-write
// crashes. A package-level mutex serializes concurrent boots within
// the same process; cross-process contention is rare in practice
// (one boot per agent type per session) and the rename is atomic on
// POSIX filesystems regardless.
//
// Returns 1 for the first boot of an agent type in a session.
//
// Best-effort: if the directory cannot be created or written to (e.g.,
// session dir doesn't exist yet, read-only filesystem), returns 0 and
// no error. The scribe still functions; replica-generation just
// stays at the unset sentinel and consumers treat it as "unknown."
// Hard errors are returned only for genuinely unexpected I/O failures
// callers may want to surface in tests.
func AcquireReplicaGeneration(sessionDir, agentType string) (int, error) {
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(agentType) == "" {
		return 0, nil
	}
	dir := filepath.Join(sessionDir, "agents", agentType)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Best-effort: surface as zero so the scribe still works.
		return 0, nil
	}

	replicaCounterMu.Lock()
	defer replicaCounterMu.Unlock()

	path := filepath.Join(dir, "replica_counter")
	current := readReplicaCounter(path)
	next := current + 1
	if err := writeReplicaCounter(path, next); err != nil {
		return 0, fmt.Errorf("scribe replica counter: write: %w", err)
	}
	return next, nil
}

// CurrentReplicaGeneration returns the most recently allocated
// generation for (sessionDir, agentType) without incrementing. Used
// by recall queries that need to know the "now" generation. Returns
// 0 if the counter file doesn't exist.
func CurrentReplicaGeneration(sessionDir, agentType string) int {
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(agentType) == "" {
		return 0
	}
	path := filepath.Join(sessionDir, "agents", agentType, "replica_counter")
	return readReplicaCounter(path)
}

// replicaCounterMu serializes counter mutations within this process.
// Cross-process serialization relies on the atomic-rename pattern
// being POSIX-atomic (which it is on every filesystem sylk targets).
var replicaCounterMu sync.Mutex

func readReplicaCounter(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist (or unreadable) → start from 0.
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || value < 0 {
		// Corrupt counter file → start fresh. Better to over-count
		// than to mis-attribute new narrations to a prior life.
		return 0
	}
	return value
}

func writeReplicaCounter(path string, value int) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strconv.Itoa(value)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
