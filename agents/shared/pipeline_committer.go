package shared

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

// SessionVFSPipelineCommitterBackend is the structural subset of
// *versioning.SessionVFS used by NewSessionVFSPipelineCommitter. Defined as
// an interface so the committer can be unit-tested without a full session.
type SessionVFSPipelineCommitterBackend interface {
	HasPipeline(pipelineID string) bool
	MergePipelineIntoGreen(ctx context.Context, pipelineID string, cert versioning.PipelineInspectorCertificate) (versioning.MergePipelineResult, error)
	RollbackPipelineIfTracked(pipelineID string) (bool, error)
	CurrentVersion() versioning.SemanticVersion
}

// NewSessionVFSPipelineCommitter wraps a session-lookup as a
// PipelineCommitter for inspector skill registration. The lookup receives
// the active session ID extracted from the handler's request context and
// returns the matching SessionVFS (nil if the session no longer exists).
//
// The pipeline inspector wires this at construction time so handoff_to_ot
// and discard_pipeline perform the lifecycle mutation themselves rather
// than broadcasting status and waiting for an out-of-process actor (the
// orchestrator, historically) to react.
func NewSessionVFSPipelineCommitter(sessionLookup func(sessionID string) SessionVFSPipelineCommitterBackend) PipelineCommitter {
	if sessionLookup == nil {
		return nil
	}
	return &sessionVFSPipelineCommitter{sessionLookup: sessionLookup}
}

type sessionVFSPipelineCommitter struct {
	sessionLookup func(sessionID string) SessionVFSPipelineCommitterBackend
}

func (c *sessionVFSPipelineCommitter) lookupSession(ctx context.Context) SessionVFSPipelineCommitterBackend {
	if c == nil || c.sessionLookup == nil {
		return nil
	}
	sessionID := strings.TrimSpace(string(versioning.SessionIDFromContext(ctx)))
	if sessionID == "" {
		return nil
	}
	return c.sessionLookup(sessionID)
}

func (c *sessionVFSPipelineCommitter) MergePipelineIntoGreen(ctx context.Context, pipelineID string, cert versioning.PipelineInspectorCertificate) (versioning.MergePipelineResult, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	svfs := c.lookupSession(ctx)
	if svfs == nil {
		return versioning.MergePipelineResult{PipelineID: pipelineID}, nil
	}
	if !svfs.HasPipeline(pipelineID) {
		// Idempotent: pipeline already merged or never created. Return a
		// zero-draft result with the current green version so the caller
		// can record the handoff without treating a retry as an error.
		return versioning.MergePipelineResult{
			PipelineID:    pipelineID,
			HadDraft:      false,
			MergedVersion: svfs.CurrentVersion(),
		}, nil
	}
	return svfs.MergePipelineIntoGreen(ctx, pipelineID, cert)
}

func (c *sessionVFSPipelineCommitter) Rollback(ctx context.Context, pipelineID string) error {
	pipelineID = strings.TrimSpace(pipelineID)
	svfs := c.lookupSession(ctx)
	if svfs == nil {
		return nil
	}
	_, err := svfs.RollbackPipelineIfTracked(pipelineID)
	return err
}
