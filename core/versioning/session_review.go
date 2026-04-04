package versioning

import (
	"context"
	"fmt"
	"strings"
)

// ReviewCandidate captures a completed pipeline draft that still needs
// explicit global-review acceptance before it becomes dependency-visible.
type ReviewCandidate struct {
	ID          string
	PipelineID  string
	BaseVersion SemanticVersion
	Mods        []FileModification
}

type sessionReviewState struct {
	candidates map[string]*ReviewCandidate
	activeID   string
}

func newSessionReviewState() *sessionReviewState {
	return &sessionReviewState{
		candidates: make(map[string]*ReviewCandidate),
	}
}

func cloneReviewCandidate(candidate *ReviewCandidate) *ReviewCandidate {
	if candidate == nil {
		return nil
	}
	return &ReviewCandidate{
		ID:          strings.TrimSpace(candidate.ID),
		PipelineID:  strings.TrimSpace(candidate.PipelineID),
		BaseVersion: candidate.BaseVersion,
		Mods:        cloneFileModifications(candidate.Mods),
	}
}

func cloneFileModifications(mods []FileModification) []FileModification {
	if len(mods) == 0 {
		return nil
	}
	out := make([]FileModification, len(mods))
	for i, mod := range mods {
		out[i] = mod
		out[i].NewContent = cloneBytes(mod.NewContent)
		out[i].OldContent = cloneBytes(mod.OldContent)
	}
	return out
}

// ExtractReviewCandidate closes a completed pipeline draft and stores its
// persistent modifications as a review candidate instead of merging them into
// the dependency-visible global checkpoint immediately.
func (s *SessionVFS) ExtractReviewCandidate(pipelineID string) (*ReviewCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}
	pipe := s.pipelines[pipelineID]
	if pipe == nil || pipe.VFS == nil {
		return nil, ErrVFSNotFound
	}

	candidate := &ReviewCandidate{
		ID:          strings.TrimSpace(pipelineID),
		PipelineID:  strings.TrimSpace(pipelineID),
		BaseVersion: pipe.BaseVersion,
		Mods:        persistentFileModifications(pipe.VFS.GetModifications()),
	}

	s.mergePipe.UnregisterPipeline(pipelineID)
	_ = s.vfsManager.ClosePipelineVFS(pipelineID)
	delete(s.pipelines, pipelineID)

	if len(candidate.Mods) == 0 {
		return nil, nil
	}
	if s.reviews == nil {
		s.reviews = newSessionReviewState()
	}
	s.reviews.candidates[candidate.ID] = cloneReviewCandidate(candidate)
	return cloneReviewCandidate(candidate), nil
}

// ReviewVFS returns the active review overlay used by the current global
// inspector/tester checkpoint audit.
func (s *SessionVFS) ReviewVFS() *PipelineVFS { return s.reviewVFS }

// NewReviewFileAccess creates a FileAccess backed by the active review overlay.
func (s *SessionVFS) NewReviewFileAccess(readOnly bool) FileAccess {
	if readOnly {
		return NewReadOnlyVFSFileAccess(s.reviewVFS, s.workingDir)
	}
	return NewVFSFileAccess(s.reviewVFS, s.workingDir)
}

// ActivateReviewCandidate materializes the requested candidate into the active
// review overlay. Re-activating the already-active candidate is a no-op.
func (s *SessionVFS) ActivateReviewCandidate(ctx context.Context, candidateID string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrVFSClosed
	}
	if s.reviews == nil {
		s.mu.Unlock()
		return ErrVFSNotFound
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		s.mu.Unlock()
		return fmt.Errorf("review candidate id is required")
	}
	if s.reviews.activeID == candidateID {
		s.mu.Unlock()
		return nil
	}
	candidate := cloneReviewCandidate(s.reviews.candidates[candidateID])
	reviewVFS := s.reviewVFS
	currentVersion := s.wal.CurrentVersion()
	s.mu.Unlock()

	if candidate == nil {
		return ErrVFSNotFound
	}
	if currentVersion != candidate.BaseVersion {
		return fmt.Errorf(
			"activate review candidate %s: checkpoint advanced from %s to %s",
			candidateID,
			candidate.BaseVersion.String(),
			currentVersion.String(),
		)
	}

	if s.draftMu != nil {
		s.draftMu.Lock()
		defer s.draftMu.Unlock()
	}
	reviewVFS.ResetOverlay()
	for _, mod := range candidate.Mods {
		tm := transformedFileMod{mod: mod}
		if _, err := applyTransformedModToVFS(contextWithoutCancel(ctx), reviewVFS, tm); err != nil {
			reviewVFS.ResetOverlay()
			return fmt.Errorf("activate review candidate %s: apply review overlay: %w", candidateID, err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviews == nil {
		s.reviews = newSessionReviewState()
	}
	s.reviews.activeID = candidateID
	return nil
}

// AcceptActiveReviewCandidate promotes the current review overlay into the
// dependency-visible global checkpoint and clears the transient review state.
func (s *SessionVFS) AcceptActiveReviewCandidate(ctx context.Context, pipelineID string) (SemanticVersion, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return SemanticVersion{}, false, ErrVFSClosed
	}
	activeID := ""
	var candidate *ReviewCandidate
	currentVersion := s.wal.CurrentVersion()
	if s.reviews != nil {
		activeID = strings.TrimSpace(s.reviews.activeID)
		candidate = cloneReviewCandidate(s.reviews.candidates[activeID])
	}
	globalVFS := s.globalVFS
	reviewVFS := s.reviewVFS
	wal := s.wal
	s.mu.Unlock()

	if activeID == "" {
		return s.CurrentVersion(), false, nil
	}
	if candidate == nil {
		return SemanticVersion{}, false, fmt.Errorf("accept review candidate %s: candidate is unavailable", activeID)
	}
	if currentVersion != candidate.BaseVersion {
		return SemanticVersion{}, false, fmt.Errorf(
			"accept review candidate %s: checkpoint advanced from %s to %s",
			activeID,
			candidate.BaseVersion.String(),
			currentVersion.String(),
		)
	}

	transformed := make([]transformedFileMod, 0, len(candidate.Mods))
	for _, mod := range candidate.Mods {
		transformed = append(transformed, transformedFileMod{mod: mod})
	}
	if len(transformed) == 0 {
		reviewVFS.ResetOverlay()
		s.clearActiveReviewCandidate(activeID)
		return s.CurrentVersion(), false, nil
	}

	if s.draftMu != nil {
		s.draftMu.Lock()
		defer s.draftMu.Unlock()
	}
	deltas := make([]WALFileDelta, 0, len(transformed))
	for _, tm := range transformed {
		delta, err := applyTransformedModToVFS(contextWithoutCancel(ctx), globalVFS, tm)
		if err != nil {
			return SemanticVersion{}, false, fmt.Errorf("accept review candidate %s: apply checkpoint: %w", activeID, err)
		}
		if delta != nil {
			deltas = append(deltas, *delta)
		}
	}
	if len(deltas) == 0 {
		reviewVFS.ResetOverlay()
		s.clearActiveReviewCandidate(activeID)
		return s.CurrentVersion(), false, nil
	}
	ver, err := wal.AppendDelta(strings.TrimSpace(firstNonEmpty(pipelineID, activeID)), deltas)
	if err != nil {
		return SemanticVersion{}, false, fmt.Errorf("accept review candidate %s: wal append: %w", activeID, err)
	}

	reviewVFS.ResetOverlay()
	s.clearActiveReviewCandidate(activeID)
	return ver, true, nil
}

func (s *SessionVFS) clearActiveReviewCandidate(candidateID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviews == nil {
		return
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID != "" {
		delete(s.reviews.candidates, candidateID)
		if s.reviews.activeID == candidateID {
			s.reviews.activeID = ""
		}
		return
	}
	if s.reviews.activeID != "" {
		delete(s.reviews.candidates, s.reviews.activeID)
		s.reviews.activeID = ""
	}
}

// DiscardReviewCandidate removes a staged review candidate without promoting it.
func (s *SessionVFS) DiscardReviewCandidate(candidateID string) {
	if s == nil {
		return
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return
	}
	if s.draftMu != nil {
		s.draftMu.Lock()
		defer s.draftMu.Unlock()
	}
	s.reviewVFS.ResetOverlay()
	s.clearActiveReviewCandidate(candidateID)
}
