package orchestrator

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/events"
)

func (b *DAGBridge) flushLayerDraftToDisk(ctx context.Context, dagID string, layerIdx int) error {
	b.mu.RLock()
	meta := b.activeDAGs[dagID]
	sessionVFSLookup := b.sessionVFS
	b.mu.RUnlock()

	if meta == nil || sessionVFSLookup == nil {
		return nil
	}

	svfs := sessionVFSLookup(meta.SessionID)
	if svfs == nil {
		return fmt.Errorf("dag bridge: missing session VFS for session %s", meta.SessionID)
	}

	pending := svfs.DiskFlusher().PendingChanges()
	if len(pending) == 0 {
		return nil
	}

	result, err := svfs.DiskFlusher().Flush(ctx)
	if err != nil {
		b.publishActivity(events.EventTypeAgentError,
			fmt.Sprintf("Failed to commit DAG layer %d draft changes: %v", layerIdx, err))
		return fmt.Errorf("dag bridge: flush session draft for dag %s layer %d: %w", dagID, layerIdx, err)
	}

	b.publishActivity(events.EventTypeSuccess,
		fmt.Sprintf("Committed DAG layer %d draft changes to disk (%d written, %d deleted)", layerIdx, result.FilesWritten, result.FilesDeleted))
	return nil
}
