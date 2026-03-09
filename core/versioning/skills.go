package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

// VersioningSkills manages filesystem versioning skills using the new
// WAL-based architecture (no branches, no DAGStore).
type VersioningSkills struct {
	wal         SemanticWAL
	diskFlusher *DiskFlusher
	differ      Differ
	sessionID   SessionID
}

// VersioningSkillsConfig configuration for versioning skills.
type VersioningSkillsConfig struct {
	WAL         SemanticWAL
	DiskFlusher *DiskFlusher
	Differ      Differ
	SessionID   SessionID
}

// NewVersioningSkills creates a new versioning skills manager.
func NewVersioningSkills(cfg VersioningSkillsConfig) *VersioningSkills {
	differ := cfg.Differ
	if differ == nil {
		differ = NewMyersDiffer(3)
	}

	return &VersioningSkills{
		wal:         cfg.WAL,
		diskFlusher: cfg.DiskFlusher,
		differ:      differ,
		sessionID:   cfg.SessionID,
	}
}

// RegisterSkills registers all versioning skills with a skill registry.
func (vs *VersioningSkills) RegisterSkills(registry *skills.Registry) error {
	skillDefs := vs.buildSkillDefinitions()

	for _, skill := range skillDefs {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register skill %s: %w", skill.Name, err)
		}
	}

	return nil
}

func (vs *VersioningSkills) buildSkillDefinitions() []*skills.Skill {
	return []*skills.Skill{
		vs.buildGetHistorySkill(),
		vs.buildDiffVersionsSkill(),
		vs.buildRollbackVersionSkill(),
		vs.buildVersionStatusSkill(),
	}
}

// =============================================================================
// Skill Input/Output Types
// =============================================================================

// GetHistoryInput is input for the get_history skill.
type GetHistoryInput struct {
	FilePath string `json:"file_path"`
	Limit    int    `json:"limit,omitempty"`
}

// GetHistoryOutput is output from get_history skill.
type GetHistoryOutput struct {
	FilePath string           `json:"file_path"`
	Versions []VersionSummary `json:"versions"`
	Total    int              `json:"total"`
}

// VersionSummary provides summary of a version.
type VersionSummary struct {
	Version    string    `json:"version"`
	Kind       string    `json:"kind"`
	PipelineID string    `json:"pipeline_id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// DiffVersionsInput is input for the diff_versions skill.
type DiffVersionsInput struct {
	BaseVersion string `json:"base_version"`
	NewVersion  string `json:"new_version"`
}

// DiffVersionsOutput is output from diff_versions skill.
type DiffVersionsOutput struct {
	BaseVersion string        `json:"base_version"`
	NewVersion  string        `json:"new_version"`
	Hunks       []DiffHunkOut `json:"hunks"`
	Stats       DiffStatsOut  `json:"stats"`
}

// DiffHunkOut is a serializable diff hunk.
type DiffHunkOut struct {
	OldStart int           `json:"old_start"`
	OldCount int           `json:"old_count"`
	NewStart int           `json:"new_start"`
	NewCount int           `json:"new_count"`
	Lines    []DiffLineOut `json:"lines"`
}

// DiffLineOut is a serializable diff line.
type DiffLineOut struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
}

// DiffStatsOut is serializable diff stats.
type DiffStatsOut struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Changes   int `json:"changes"`
}

// RollbackVersionInput is input for the rollback_version skill.
type RollbackVersionInput struct {
	TargetVersion string `json:"target_version"`
}

// RollbackVersionOutput is output from rollback_version skill.
type RollbackVersionOutput struct {
	PreviousVersion string `json:"previous_version"`
	RolledBackTo    string `json:"rolled_back_to"`
	Success         bool   `json:"success"`
}

// VersionStatusInput is input for the version_status skill.
type VersionStatusInput struct{}

// VersionStatusOutput is output from version_status skill.
type VersionStatusOutput struct {
	CurrentVersion string `json:"current_version"`
	PendingChanges int    `json:"pending_changes"`
	WALEntries     int    `json:"wal_entries"`
}

// =============================================================================
// Skill Definitions
// =============================================================================

func (vs *VersioningSkills) buildGetHistorySkill() *skills.Skill {
	return skills.NewSkill("get_history").
		Description("Get the version history from the WAL. Returns a list of all version entries with metadata.").
		Domain("versioning").
		Keywords("history", "versions", "log", "commits").
		Priority(90).
		StringParam("file_path", "Optional file path to filter history for", false).
		IntParam("limit", "Maximum number of versions to return (default: 50)", false).
		Handler(vs.handleGetHistory).
		Build()
}

func (vs *VersioningSkills) buildDiffVersionsSkill() *skills.Skill {
	return skills.NewSkill("diff_versions").
		Description("Compare two WAL versions and show the file-level differences.").
		Domain("versioning").
		Keywords("diff", "compare", "changes", "delta").
		Priority(85).
		StringParam("base_version", "Base version (major.minor) to compare from", true).
		StringParam("new_version", "New version (major.minor) to compare to", true).
		Handler(vs.handleDiffVersions).
		Build()
}

func (vs *VersioningSkills) buildRollbackVersionSkill() *skills.Skill {
	return skills.NewSkill("rollback_version").
		Description("Rollback to a previous version. Major version targets restore disk, minor targets restore global VFS overlay.").
		Domain("versioning").
		Keywords("rollback", "revert", "restore", "undo").
		Priority(75).
		StringParam("target_version", "Version (major.minor) to rollback to", true).
		Handler(vs.handleRollbackVersion).
		Build()
}

func (vs *VersioningSkills) buildVersionStatusSkill() *skills.Skill {
	return skills.NewSkill("version_status").
		Description("Returns the current version, pending change count, and WAL entry count.").
		Domain("versioning").
		Keywords("version", "status", "current").
		Priority(90).
		Handler(vs.handleVersionStatus).
		Build()
}

// =============================================================================
// Skill Handlers
// =============================================================================

func (vs *VersioningSkills) handleGetHistory(_ context.Context, input json.RawMessage) (any, error) {
	var in GetHistoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	// Read from WAL index.
	entries, err := vs.wal.GetDeltasSince(VersionZero)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}

	summaries := make([]VersionSummary, 0, min(len(entries), limit))
	for _, e := range entries {
		if len(summaries) >= limit {
			break
		}

		// Filter by file path if specified.
		if in.FilePath != "" && !entryAffectsFile(e, in.FilePath) {
			continue
		}

		kind := "delta"
		if e.Kind == WALEntryKindCheckpoint {
			kind = "checkpoint"
		}
		summaries = append(summaries, VersionSummary{
			Version:    e.Version.String(),
			Kind:       kind,
			PipelineID: e.PipelineID,
			Timestamp:  e.Timestamp,
		})
	}

	return &GetHistoryOutput{
		FilePath: in.FilePath,
		Versions: summaries,
		Total:    len(summaries),
	}, nil
}

func entryAffectsFile(e VersionedWALEntry, filePath string) bool {
	for _, d := range e.Deltas {
		if d.Path == filePath {
			return true
		}
	}
	return false
}

func (vs *VersioningSkills) handleDiffVersions(_ context.Context, input json.RawMessage) (any, error) {
	var in DiffVersionsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	baseEntry, err := vs.wal.GetEntry(parseSemanticVersion(in.BaseVersion))
	if err != nil {
		return nil, fmt.Errorf("get base version: %w", err)
	}

	newEntry, err := vs.wal.GetEntry(parseSemanticVersion(in.NewVersion))
	if err != nil {
		return nil, fmt.Errorf("get new version: %w", err)
	}

	// Compare content across all deltas.
	baseContent := aggregateContent(baseEntry.Deltas)
	newContent := aggregateContent(newEntry.Deltas)

	diff := vs.differ.DiffBytes(baseContent, newContent)

	hunks := make([]DiffHunkOut, 0, len(diff.Hunks))
	for _, h := range diff.Hunks {
		lines := make([]DiffLineOut, 0, len(h.Lines))
		for _, l := range h.Lines {
			lines = append(lines, DiffLineOut{
				Type:    diffLineTypeToString(l.Type),
				Content: l.Content,
				OldLine: l.OldLine,
				NewLine: l.NewLine,
			})
		}
		hunks = append(hunks, DiffHunkOut{
			OldStart: h.OldStart,
			OldCount: h.OldCount,
			NewStart: h.NewStart,
			NewCount: h.NewCount,
			Lines:    lines,
		})
	}

	return &DiffVersionsOutput{
		BaseVersion: in.BaseVersion,
		NewVersion:  in.NewVersion,
		Hunks:       hunks,
		Stats: DiffStatsOut{
			Additions: diff.Stats.Additions,
			Deletions: diff.Stats.Deletions,
			Changes:   diff.Stats.Changes,
		},
	}, nil
}

func (vs *VersioningSkills) handleRollbackVersion(ctx context.Context, input json.RawMessage) (any, error) {
	var in RollbackVersionInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	target := parseSemanticVersion(in.TargetVersion)
	prev := vs.wal.CurrentVersion()

	if err := vs.diskFlusher.RollbackToVersion(ctx, target); err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}

	return &RollbackVersionOutput{
		PreviousVersion: prev.String(),
		RolledBackTo:    target.String(),
		Success:         true,
	}, nil
}

func (vs *VersioningSkills) handleVersionStatus(_ context.Context, _ json.RawMessage) (any, error) {
	pending := vs.diskFlusher.PendingChanges()

	return &VersionStatusOutput{
		CurrentVersion: vs.wal.CurrentVersion().String(),
		PendingChanges: len(pending),
		WALEntries:     vs.wal.IndexLen(),
	}, nil
}

// =============================================================================
// Helpers
// =============================================================================

func parseSemanticVersion(s string) SemanticVersion {
	var major, minor uint32
	fmt.Sscanf(s, "%d.%d", &major, &minor)
	return SemanticVersion{Major: major, Minor: minor}
}

func aggregateContent(deltas []WALFileDelta) []byte {
	var buf []byte
	for _, d := range deltas {
		buf = append(buf, d.NewContent...)
	}
	return buf
}

func diffLineTypeToString(t DiffLineType) string {
	switch t {
	case DiffLineContext:
		return "context"
	case DiffLineAdd:
		return "add"
	case DiffLineDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// =============================================================================
// Legacy types kept for compilation compatibility during transition
// =============================================================================

// Branch represents a named version branch (legacy, unused in new architecture).
type Branch struct {
	Name        string    `json:"name"`
	HeadVersion VersionID `json:"head_version"`
	BaseVersion VersionID `json:"base_version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	SessionID   SessionID `json:"session_id"`
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
}

// BranchInfo provides summary info about a branch (legacy).
type BranchInfo struct {
	Name         string    `json:"name"`
	HeadVersion  string    `json:"head_version"`
	CommitCount  int       `json:"commit_count"`
	LastModified time.Time `json:"last_modified"`
	IsDefault    bool      `json:"is_default"`
	IsCurrent    bool      `json:"is_current"`
}
