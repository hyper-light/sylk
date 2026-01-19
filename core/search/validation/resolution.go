// Package validation provides cross-source validation for the Sylk Document Search System.
package validation

import (
	"context"
	"errors"
	"sync"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNoDiscrepancies indicates no discrepancies to resolve.
	ErrNoDiscrepancies = errors.New("no discrepancies to resolve")

	// ErrResolverClosed indicates the resolver has been closed.
	ErrResolverClosed = errors.New("resolver is closed")

	// ErrApplyFailed indicates a resolution apply operation failed.
	ErrApplyFailed = errors.New("failed to apply resolution")
)

// =============================================================================
// ResolutionAction
// =============================================================================

// ResolutionAction indicates what action to take to resolve a discrepancy.
type ResolutionAction int

const (
	// ActionNone indicates no action is needed.
	ActionNone ResolutionAction = iota

	// ActionReindex indicates the file should be reindexed in CMT.
	ActionReindex

	// ActionRemoveFromIndex indicates the file should be removed from CMT.
	ActionRemoveFromIndex

	// ActionUpdateMetadata indicates only metadata needs updating.
	ActionUpdateMetadata
)

// String returns a human-readable name for the resolution action.
func (a ResolutionAction) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionReindex:
		return "reindex"
	case ActionRemoveFromIndex:
		return "remove_from_index"
	case ActionUpdateMetadata:
		return "update_metadata"
	default:
		return "unknown"
	}
}

// =============================================================================
// Resolution
// =============================================================================

// Resolution describes how to resolve a discrepancy.
type Resolution struct {
	// Discrepancy is the original discrepancy being resolved.
	Discrepancy *Discrepancy

	// Action indicates what should be done.
	Action ResolutionAction

	// TruthSource is which source should be considered authoritative.
	TruthSource ValidationSource

	// Description explains the resolution.
	Description string

	// Applied indicates whether the resolution was applied.
	Applied bool

	// AppliedAt is when the resolution was applied.
	AppliedAt time.Time

	// Error contains any error from applying the resolution.
	Error error
}

// Clone creates a deep copy of the Resolution.
func (r *Resolution) Clone() *Resolution {
	if r == nil {
		return nil
	}
	return &Resolution{
		Discrepancy: r.Discrepancy.Clone(),
		Action:      r.Action,
		TruthSource: r.TruthSource,
		Description: r.Description,
		Applied:     r.Applied,
		AppliedAt:   r.AppliedAt,
		Error:       r.Error,
	}
}

// =============================================================================
// ResolutionHandler
// =============================================================================

// ResolutionHandler applies resolutions to the actual data stores.
// This interface allows the resolver to work without direct dependencies.
type ResolutionHandler interface {
	// Reindex triggers reindexing of a file from filesystem.
	Reindex(ctx context.Context, path string) error

	// RemoveFromIndex removes a file from the index.
	RemoveFromIndex(ctx context.Context, path string) error

	// UpdateMetadata updates only the metadata for a file.
	UpdateMetadata(ctx context.Context, path string) error
}

// =============================================================================
// ResolverConfig
// =============================================================================

// ResolverConfig configures the discrepancy resolver.
type ResolverConfig struct {
	// DryRun when true prevents actual changes.
	DryRun bool

	// AutoResolveMinor when true auto-resolves minor discrepancies.
	AutoResolveMinor bool

	// MaxConcurrent limits parallel resolution operations.
	MaxConcurrent int
}

// DefaultResolverConfig returns default resolver configuration.
func DefaultResolverConfig() ResolverConfig {
	return ResolverConfig{
		DryRun:           false,
		AutoResolveMinor: true,
		MaxConcurrent:    4,
	}
}

// =============================================================================
// DiscrepancyResolver
// =============================================================================

// DiscrepancyResolver resolves discrepancies between validation sources.
// Resolution priority: Filesystem > Git > CMT
type DiscrepancyResolver struct {
	config  ResolverConfig
	handler ResolutionHandler

	mu     sync.RWMutex
	closed bool
}

// NewDiscrepancyResolver creates a new discrepancy resolver.
// Handler may be nil for dry-run only mode.
func NewDiscrepancyResolver(config ResolverConfig, handler ResolutionHandler) *DiscrepancyResolver {
	return &DiscrepancyResolver{
		config:  config,
		handler: handler,
	}
}

// =============================================================================
// Resolve Methods
// =============================================================================

// Resolve determines resolutions for the given discrepancies.
func (r *DiscrepancyResolver) Resolve(
	ctx context.Context,
	discrepancies []*Discrepancy,
) ([]*Resolution, error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	if len(discrepancies) == 0 {
		return nil, ErrNoDiscrepancies
	}

	resolutions := make([]*Resolution, 0, len(discrepancies))
	for _, d := range discrepancies {
		resolution := r.resolveOne(d)
		resolutions = append(resolutions, resolution)
	}

	return resolutions, nil
}

// resolveOne determines the resolution for a single discrepancy.
func (r *DiscrepancyResolver) resolveOne(d *Discrepancy) *Resolution {
	resolution := &Resolution{
		Discrepancy: d.Clone(),
	}

	truthSource := r.determineTruthSource(d)
	resolution.TruthSource = truthSource
	resolution.Action, resolution.Description = r.determineAction(d, truthSource)

	return resolution
}

// determineTruthSource selects the authoritative source based on priority.
// Priority: Filesystem > Git > CMT
func (r *DiscrepancyResolver) determineTruthSource(d *Discrepancy) ValidationSource {
	if d.HasSource(SourceFilesystem) {
		return SourceFilesystem
	}
	if d.HasSource(SourceGit) {
		return SourceGit
	}
	return SourceCMT
}

// determineAction decides what action to take based on discrepancy type.
func (r *DiscrepancyResolver) determineAction(
	d *Discrepancy,
	truthSource ValidationSource,
) (ResolutionAction, string) {
	truthState := d.GetState(truthSource)
	cmtState := d.GetState(SourceCMT)

	return r.computeAction(d.Type, truthState, cmtState, truthSource)
}

// computeAction computes the resolution action based on states.
func (r *DiscrepancyResolver) computeAction(
	discType DiscrepancyType,
	truthState *FileState,
	cmtState *FileState,
	truthSource ValidationSource,
) (ResolutionAction, string) {
	switch discType {
	case DiscrepancyMissing:
		return r.handleMissing(truthState, cmtState, truthSource)
	case DiscrepancyContentMismatch, DiscrepancySizeMismatch:
		return ActionReindex, "Content differs, reindex from " + truthSource.String()
	case DiscrepancyModTimeMismatch:
		return ActionUpdateMetadata, "Update metadata from " + truthSource.String()
	default:
		return ActionNone, "Unknown discrepancy type"
	}
}

// handleMissing handles missing file discrepancies.
func (r *DiscrepancyResolver) handleMissing(
	truthState *FileState,
	cmtState *FileState,
	truthSource ValidationSource,
) (ResolutionAction, string) {
	// File exists in truth source but not in CMT - reindex
	if truthState != nil && truthState.Exists {
		if cmtState == nil || !cmtState.Exists {
			return ActionReindex, "File exists in " + truthSource.String() + ", add to index"
		}
	}

	// File doesn't exist in truth source but is in CMT - remove
	if truthState != nil && !truthState.Exists {
		if cmtState != nil && cmtState.Exists {
			return ActionRemoveFromIndex, "File missing from " + truthSource.String() + ", remove from index"
		}
	}

	return ActionNone, "Cannot determine action for missing file"
}

// =============================================================================
// Apply Methods
// =============================================================================

// Apply applies the given resolutions.
// In dry-run mode, no actual changes are made.
func (r *DiscrepancyResolver) Apply(
	ctx context.Context,
	resolutions []*Resolution,
) ([]*Resolution, error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	if r.config.DryRun {
		return r.dryRunApply(resolutions)
	}

	return r.actualApply(ctx, resolutions)
}

// dryRunApply marks resolutions as applied without changes.
func (r *DiscrepancyResolver) dryRunApply(resolutions []*Resolution) ([]*Resolution, error) {
	results := make([]*Resolution, len(resolutions))
	for i, res := range resolutions {
		applied := res.Clone()
		applied.Description = "[DRY-RUN] " + applied.Description
		results[i] = applied
	}
	return results, nil
}

// actualApply performs the actual resolution operations.
func (r *DiscrepancyResolver) actualApply(
	ctx context.Context,
	resolutions []*Resolution,
) ([]*Resolution, error) {
	if r.handler == nil {
		return nil, ErrApplyFailed
	}

	results := make([]*Resolution, len(resolutions))
	for i, res := range resolutions {
		results[i] = r.applyOne(ctx, res)
	}
	return results, nil
}

// applyOne applies a single resolution.
func (r *DiscrepancyResolver) applyOne(ctx context.Context, res *Resolution) *Resolution {
	applied := res.Clone()
	var err error

	switch res.Action {
	case ActionReindex:
		err = r.handler.Reindex(ctx, res.Discrepancy.Path)
	case ActionRemoveFromIndex:
		err = r.handler.RemoveFromIndex(ctx, res.Discrepancy.Path)
	case ActionUpdateMetadata:
		err = r.handler.UpdateMetadata(ctx, res.Discrepancy.Path)
	case ActionNone:
		// No action needed
	}

	applied.Applied = err == nil
	applied.AppliedAt = time.Now()
	applied.Error = err

	return applied
}

// =============================================================================
// ResolveAndApply
// =============================================================================

// ResolveAndApply resolves and applies in one operation.
func (r *DiscrepancyResolver) ResolveAndApply(
	ctx context.Context,
	discrepancies []*Discrepancy,
) ([]*Resolution, error) {
	resolutions, err := r.Resolve(ctx, discrepancies)
	if err != nil {
		return nil, err
	}

	return r.Apply(ctx, resolutions)
}

// =============================================================================
// Configuration Methods
// =============================================================================

// IsDryRun returns whether the resolver is in dry-run mode.
func (r *DiscrepancyResolver) IsDryRun() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config.DryRun
}

// SetDryRun sets the dry-run mode.
func (r *DiscrepancyResolver) SetDryRun(dryRun bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config.DryRun = dryRun
}

// =============================================================================
// Lifecycle Methods
// =============================================================================

// Close closes the resolver.
func (r *DiscrepancyResolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// checkClosed returns an error if the resolver is closed.
func (r *DiscrepancyResolver) checkClosed() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return ErrResolverClosed
	}
	return nil
}

// =============================================================================
// IsMinorDiscrepancy
// =============================================================================

// IsMinorDiscrepancy returns true if the discrepancy is minor.
// Minor discrepancies can be auto-resolved without user intervention.
func IsMinorDiscrepancy(d *Discrepancy) bool {
	if d == nil {
		return false
	}
	return d.Type == DiscrepancyModTimeMismatch
}
