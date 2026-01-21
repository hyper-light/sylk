package versioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Filesystem Skills (Wave 9 - FS.9)
// =============================================================================
//
// Skills for version control operations that agents can invoke:
// - create_branch: Create a new version branch
// - switch_branch: Switch to an existing branch
// - merge_branch: Merge branches together
// - get_history: Get file version history
// - diff_versions: Compare two versions
// - rollback_version: Rollback to a previous version
// - list_branches: List all branches
//
// These skills integrate the versioning system with the agent workflow.

// =============================================================================
// Errors
// =============================================================================

var (
	ErrBranchNotFound       = errors.New("branch not found")
	ErrBranchExists         = errors.New("branch already exists")
	ErrBranchConflict       = errors.New("branch conflict during merge")
	ErrInvalidBranchName    = errors.New("invalid branch name")
	ErrCannotDeleteMain     = errors.New("cannot delete main branch")
	ErrNoVersionsToMerge    = errors.New("no versions to merge")
	ErrRollbackFailed       = errors.New("rollback failed")
	ErrSkillsNotInitialized = errors.New("versioning skills not initialized")
)

// =============================================================================
// Branch
// =============================================================================

// Branch represents a named version branch
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

// BranchInfo provides summary info about a branch
type BranchInfo struct {
	Name         string    `json:"name"`
	HeadVersion  string    `json:"head_version"`
	CommitCount  int       `json:"commit_count"`
	LastModified time.Time `json:"last_modified"`
	IsDefault    bool      `json:"is_default"`
	IsCurrent    bool      `json:"is_current"`
}

// =============================================================================
// Skill Input Types
// =============================================================================

// CreateBranchInput is input for the create_branch skill
type CreateBranchInput struct {
	BranchName  string `json:"branch_name"`
	FromVersion string `json:"from_version,omitempty"`
	Description string `json:"description,omitempty"`
}

// SwitchBranchInput is input for the switch_branch skill
type SwitchBranchInput struct {
	BranchName string `json:"branch_name"`
}

// MergeBranchInput is input for the merge_branch skill
type MergeBranchInput struct {
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Message      string `json:"message,omitempty"`
	Strategy     string `json:"strategy,omitempty"` // "auto", "ours", "theirs", "manual"
}

// GetHistoryInput is input for the get_history skill
type GetHistoryInput struct {
	FilePath string `json:"file_path"`
	Limit    int    `json:"limit,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// DiffVersionsInput is input for the diff_versions skill
type DiffVersionsInput struct {
	FilePath     string `json:"file_path,omitempty"`
	BaseVersion  string `json:"base_version"`
	NewVersion   string `json:"new_version"`
	ContextLines int    `json:"context_lines,omitempty"`
}

// RollbackVersionInput is input for the rollback_version skill
type RollbackVersionInput struct {
	FilePath  string `json:"file_path"`
	VersionID string `json:"version_id"`
	Message   string `json:"message,omitempty"`
}

// ListBranchesInput is input for the list_branches skill
type ListBranchesInput struct {
	IncludeCommitCount bool `json:"include_commit_count,omitempty"`
}

// =============================================================================
// Skill Output Types
// =============================================================================

// CreateBranchOutput is output from create_branch skill
type CreateBranchOutput struct {
	BranchName  string `json:"branch_name"`
	HeadVersion string `json:"head_version"`
	BaseVersion string `json:"base_version"`
	CreatedAt   string `json:"created_at"`
}

// SwitchBranchOutput is output from switch_branch skill
type SwitchBranchOutput struct {
	PreviousBranch string `json:"previous_branch"`
	CurrentBranch  string `json:"current_branch"`
	HeadVersion    string `json:"head_version"`
}

// MergeBranchOutput is output from merge_branch skill
type MergeBranchOutput struct {
	MergedVersion string   `json:"merged_version"`
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch"`
	FilesChanged  int      `json:"files_changed"`
	Conflicts     []string `json:"conflicts,omitempty"`
	Success       bool     `json:"success"`
}

// GetHistoryOutput is output from get_history skill
type GetHistoryOutput struct {
	FilePath string           `json:"file_path"`
	Versions []VersionSummary `json:"versions"`
	Total    int              `json:"total"`
}

// VersionSummary provides summary of a version
type VersionSummary struct {
	VersionID   string    `json:"version_id"`
	Timestamp   time.Time `json:"timestamp"`
	SessionID   string    `json:"session_id"`
	PipelineID  string    `json:"pipeline_id,omitempty"`
	IsMerge     bool      `json:"is_merge"`
	ContentSize int64     `json:"content_size"`
	ParentCount int       `json:"parent_count"`
}

// DiffVersionsOutput is output from diff_versions skill
type DiffVersionsOutput struct {
	FilePath    string        `json:"file_path,omitempty"`
	BaseVersion string        `json:"base_version"`
	NewVersion  string        `json:"new_version"`
	Hunks       []DiffHunkOut `json:"hunks"`
	Stats       DiffStatsOut  `json:"stats"`
}

// DiffHunkOut is a serializable diff hunk
type DiffHunkOut struct {
	OldStart int           `json:"old_start"`
	OldCount int           `json:"old_count"`
	NewStart int           `json:"new_start"`
	NewCount int           `json:"new_count"`
	Lines    []DiffLineOut `json:"lines"`
}

// DiffLineOut is a serializable diff line
type DiffLineOut struct {
	Type    string `json:"type"` // "context", "add", "delete"
	Content string `json:"content"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
}

// DiffStatsOut is serializable diff stats
type DiffStatsOut struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Changes   int `json:"changes"`
}

// RollbackVersionOutput is output from rollback_version skill
type RollbackVersionOutput struct {
	FilePath        string `json:"file_path"`
	PreviousVersion string `json:"previous_version"`
	RolledBackTo    string `json:"rolled_back_to"`
	NewVersion      string `json:"new_version"`
	Success         bool   `json:"success"`
}

// ListBranchesOutput is output from list_branches skill
type ListBranchesOutput struct {
	Branches      []BranchInfo `json:"branches"`
	CurrentBranch string       `json:"current_branch"`
	Total         int          `json:"total"`
}

// =============================================================================
// VersioningSkills Manager
// =============================================================================

// VersioningSkills manages filesystem versioning skills
type VersioningSkills struct {
	cvs           CVS
	dagStore      DAGStore
	blobStore     BlobStore
	differ        Differ
	branches      map[string]*Branch
	currentBranch string
	sessionID     SessionID
}

// VersioningSkillsConfig configuration for versioning skills
type VersioningSkillsConfig struct {
	CVS       CVS
	DAGStore  DAGStore
	BlobStore BlobStore
	Differ    Differ
	SessionID SessionID
}

// NewVersioningSkills creates a new versioning skills manager
func NewVersioningSkills(cfg VersioningSkillsConfig) *VersioningSkills {
	differ := cfg.Differ
	if differ == nil {
		differ = NewMyersDiffer(3)
	}

	vs := &VersioningSkills{
		cvs:           cfg.CVS,
		dagStore:      cfg.DAGStore,
		blobStore:     cfg.BlobStore,
		differ:        differ,
		branches:      make(map[string]*Branch),
		currentBranch: "main",
		sessionID:     cfg.SessionID,
	}

	vs.branches["main"] = &Branch{
		Name:      "main",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		SessionID: cfg.SessionID,
		IsDefault: true,
	}

	return vs
}

// RegisterSkills registers all versioning skills with a skill registry
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
		vs.buildCreateBranchSkill(),
		vs.buildSwitchBranchSkill(),
		vs.buildMergeBranchSkill(),
		vs.buildGetHistorySkill(),
		vs.buildDiffVersionsSkill(),
		vs.buildRollbackVersionSkill(),
		vs.buildListBranchesSkill(),
	}
}

// =============================================================================
// Skill Definitions
// =============================================================================

func (vs *VersioningSkills) buildCreateBranchSkill() *skills.Skill {
	return skills.NewSkill("create_branch").
		Description("Create a new version branch from the current or specified version. Branches allow parallel development and experimentation.").
		Domain("versioning").
		Keywords("branch", "create", "fork", "version").
		Priority(80).
		StringParam("branch_name", "Name of the new branch to create", true).
		StringParam("from_version", "Version ID to branch from (defaults to current HEAD)", false).
		StringParam("description", "Optional description for the branch", false).
		Handler(vs.handleCreateBranch).
		Build()
}

func (vs *VersioningSkills) buildSwitchBranchSkill() *skills.Skill {
	return skills.NewSkill("switch_branch").
		Description("Switch to an existing branch. Changes the working context to the specified branch's HEAD.").
		Domain("versioning").
		Keywords("branch", "switch", "checkout").
		Priority(80).
		StringParam("branch_name", "Name of the branch to switch to", true).
		Handler(vs.handleSwitchBranch).
		Build()
}

func (vs *VersioningSkills) buildMergeBranchSkill() *skills.Skill {
	return skills.NewSkill("merge_branch").
		Description("Merge one branch into another. Combines changes from the source branch into the target branch.").
		Domain("versioning").
		Keywords("branch", "merge", "combine").
		Priority(70).
		StringParam("source_branch", "Branch to merge from", true).
		StringParam("target_branch", "Branch to merge into (defaults to current branch)", false).
		StringParam("message", "Optional merge commit message", false).
		EnumParam("strategy", "Merge strategy to use", []string{"auto", "ours", "theirs", "manual"}, false).
		Handler(vs.handleMergeBranch).
		Build()
}

func (vs *VersioningSkills) buildGetHistorySkill() *skills.Skill {
	return skills.NewSkill("get_history").
		Description("Get the version history for a file. Returns a list of all versions with metadata.").
		Domain("versioning").
		Keywords("history", "versions", "log", "commits").
		Priority(90).
		StringParam("file_path", "Path to the file to get history for", true).
		IntParam("limit", "Maximum number of versions to return (default: 50)", false).
		StringParam("branch", "Branch to get history from (defaults to current branch)", false).
		Handler(vs.handleGetHistory).
		Build()
}

func (vs *VersioningSkills) buildDiffVersionsSkill() *skills.Skill {
	return skills.NewSkill("diff_versions").
		Description("Compare two versions of a file and show the differences. Returns unified diff format.").
		Domain("versioning").
		Keywords("diff", "compare", "changes", "delta").
		Priority(85).
		StringParam("file_path", "Path to the file (optional if versions are specified)", false).
		StringParam("base_version", "Base version ID to compare from", true).
		StringParam("new_version", "New version ID to compare to", true).
		IntParam("context_lines", "Number of context lines around changes (default: 3)", false).
		Handler(vs.handleDiffVersions).
		Build()
}

func (vs *VersioningSkills) buildRollbackVersionSkill() *skills.Skill {
	return skills.NewSkill("rollback_version").
		Description("Rollback a file to a previous version. Creates a new version with the content from the specified version.").
		Domain("versioning").
		Keywords("rollback", "revert", "restore", "undo").
		Priority(75).
		StringParam("file_path", "Path to the file to rollback", true).
		StringParam("version_id", "Version ID to rollback to", true).
		StringParam("message", "Optional message for the rollback", false).
		Handler(vs.handleRollbackVersion).
		Build()
}

func (vs *VersioningSkills) buildListBranchesSkill() *skills.Skill {
	return skills.NewSkill("list_branches").
		Description("List all branches. Shows branch names, HEAD versions, and which branch is currently active.").
		Domain("versioning").
		Keywords("branches", "list", "show").
		Priority(85).
		BoolParam("include_commit_count", "Include commit count for each branch", false).
		Handler(vs.handleListBranches).
		Build()
}

// =============================================================================
// Skill Handlers
// =============================================================================

func (vs *VersioningSkills) handleCreateBranch(ctx context.Context, input json.RawMessage) (any, error) {
	var in CreateBranchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if in.BranchName == "" {
		return nil, ErrInvalidBranchName
	}

	if _, exists := vs.branches[in.BranchName]; exists {
		return nil, ErrBranchExists
	}

	baseVersion, err := vs.resolveBaseVersion(in.FromVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base version: %w", err)
	}

	branch := &Branch{
		Name:        in.BranchName,
		HeadVersion: baseVersion,
		BaseVersion: baseVersion,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		SessionID:   vs.sessionID,
		Description: in.Description,
		IsDefault:   false,
	}

	vs.branches[in.BranchName] = branch

	return &CreateBranchOutput{
		BranchName:  branch.Name,
		HeadVersion: branch.HeadVersion.String(),
		BaseVersion: branch.BaseVersion.String(),
		CreatedAt:   branch.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (vs *VersioningSkills) handleSwitchBranch(ctx context.Context, input json.RawMessage) (any, error) {
	var in SwitchBranchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if in.BranchName == "" {
		return nil, ErrInvalidBranchName
	}

	branch, exists := vs.branches[in.BranchName]
	if !exists {
		return nil, ErrBranchNotFound
	}

	previousBranch := vs.currentBranch
	vs.currentBranch = in.BranchName

	return &SwitchBranchOutput{
		PreviousBranch: previousBranch,
		CurrentBranch:  in.BranchName,
		HeadVersion:    branch.HeadVersion.String(),
	}, nil
}

func (vs *VersioningSkills) handleMergeBranch(ctx context.Context, input json.RawMessage) (any, error) {
	var in MergeBranchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	sourceBranch, exists := vs.branches[in.SourceBranch]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrBranchNotFound, in.SourceBranch)
	}

	targetBranchName := in.TargetBranch
	if targetBranchName == "" {
		targetBranchName = vs.currentBranch
	}

	targetBranch, exists := vs.branches[targetBranchName]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrBranchNotFound, targetBranchName)
	}

	mergedVersionID, err := vs.performMerge(ctx, sourceBranch, targetBranch, in.Strategy)
	if err != nil {
		return &MergeBranchOutput{
			SourceBranch: in.SourceBranch,
			TargetBranch: targetBranchName,
			Conflicts:    []string{err.Error()},
			Success:      false,
		}, nil
	}

	targetBranch.HeadVersion = mergedVersionID
	targetBranch.UpdatedAt = time.Now()

	return &MergeBranchOutput{
		MergedVersion: mergedVersionID.String(),
		SourceBranch:  in.SourceBranch,
		TargetBranch:  targetBranchName,
		FilesChanged:  1,
		Success:       true,
	}, nil
}

func (vs *VersioningSkills) handleGetHistory(ctx context.Context, input json.RawMessage) (any, error) {
	var in GetHistoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if in.FilePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	versions, err := vs.dagStore.GetHistory(in.FilePath, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get history: %w", err)
	}

	summaries := make([]VersionSummary, 0, len(versions))
	for _, v := range versions {
		summaries = append(summaries, VersionSummary{
			VersionID:   v.ID.String(),
			Timestamp:   v.Timestamp,
			SessionID:   string(v.SessionID),
			PipelineID:  v.PipelineID,
			IsMerge:     v.IsMerge,
			ContentSize: v.ContentSize,
			ParentCount: len(v.Parents),
		})
	}

	return &GetHistoryOutput{
		FilePath: in.FilePath,
		Versions: summaries,
		Total:    len(summaries),
	}, nil
}

func (vs *VersioningSkills) handleDiffVersions(ctx context.Context, input json.RawMessage) (any, error) {
	var in DiffVersionsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if in.BaseVersion == "" || in.NewVersion == "" {
		return nil, fmt.Errorf("both base_version and new_version are required")
	}

	baseVersionID, err := ParseVersionID(in.BaseVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid base_version: %w", err)
	}

	newVersionID, err := ParseVersionID(in.NewVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid new_version: %w", err)
	}

	baseContent, err := vs.getVersionContent(baseVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get base version content: %w", err)
	}

	newContent, err := vs.getVersionContent(newVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get new version content: %w", err)
	}

	contextLines := in.ContextLines
	if contextLines <= 0 {
		contextLines = 3
	}

	differ := NewMyersDiffer(contextLines)
	diff := differ.DiffBytes(baseContent, newContent)

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
		FilePath:    in.FilePath,
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

	if in.FilePath == "" || in.VersionID == "" {
		return nil, fmt.Errorf("file_path and version_id are required")
	}

	targetVersionID, err := ParseVersionID(in.VersionID)
	if err != nil {
		return nil, fmt.Errorf("invalid version_id: %w", err)
	}

	currentHead, err := vs.dagStore.GetHead(in.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get current HEAD: %w", err)
	}

	targetContent, err := vs.getVersionContent(targetVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get target version content: %w", err)
	}

	meta := WriteMetadata{
		SessionID: vs.sessionID,
		Message:   in.Message,
	}

	if meta.Message == "" {
		meta.Message = fmt.Sprintf("Rollback to version %s", in.VersionID[:8])
	}

	newVersionID, err := vs.cvs.Write(ctx, in.FilePath, targetContent, meta)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRollbackFailed, err)
	}

	return &RollbackVersionOutput{
		FilePath:        in.FilePath,
		PreviousVersion: currentHead.ID.String(),
		RolledBackTo:    in.VersionID,
		NewVersion:      newVersionID.String(),
		Success:         true,
	}, nil
}

func (vs *VersioningSkills) handleListBranches(ctx context.Context, input json.RawMessage) (any, error) {
	var in ListBranchesInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	branches := make([]BranchInfo, 0, len(vs.branches))
	for name, branch := range vs.branches {
		info := BranchInfo{
			Name:         name,
			HeadVersion:  branch.HeadVersion.String(),
			LastModified: branch.UpdatedAt,
			IsDefault:    branch.IsDefault,
			IsCurrent:    name == vs.currentBranch,
		}

		if in.IncludeCommitCount {
			info.CommitCount = vs.countBranchCommits(branch)
		}

		branches = append(branches, info)
	}

	return &ListBranchesOutput{
		Branches:      branches,
		CurrentBranch: vs.currentBranch,
		Total:         len(branches),
	}, nil
}

// =============================================================================
// Helper Methods
// =============================================================================

func (vs *VersioningSkills) resolveBaseVersion(fromVersion string) (VersionID, error) {
	if fromVersion != "" {
		return ParseVersionID(fromVersion)
	}

	currentBranch, exists := vs.branches[vs.currentBranch]
	if !exists {
		return VersionID{}, ErrBranchNotFound
	}

	return currentBranch.HeadVersion, nil
}

func (vs *VersioningSkills) getVersionContent(versionID VersionID) ([]byte, error) {
	version, err := vs.dagStore.Get(versionID)
	if err != nil {
		return nil, err
	}

	return vs.blobStore.Get(version.ContentHash)
}

func (vs *VersioningSkills) performMerge(ctx context.Context, source, target *Branch, strategy string) (VersionID, error) {
	if vs.cvs == nil {
		return VersionID{}, ErrSkillsNotInitialized
	}

	resolver := vs.createMergeResolver(strategy)

	return vs.cvs.Merge(ctx, source.HeadVersion, target.HeadVersion, resolver)
}

func (vs *VersioningSkills) createMergeResolver(strategy string) ConflictResolver {
	switch strategy {
	case "ours":
		return &SkillOursConflictResolver{}
	case "theirs":
		return &SkillTheirsConflictResolver{}
	default:
		return &SkillAutoConflictResolver{}
	}
}

func (vs *VersioningSkills) countBranchCommits(branch *Branch) int {
	if vs.dagStore == nil {
		return 0
	}

	ancestors, err := vs.dagStore.GetAncestors(branch.HeadVersion, -1)
	if err != nil {
		return 0
	}

	return len(ancestors) + 1
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
// Branch Management Methods
// =============================================================================

// GetCurrentBranch returns the current branch name
func (vs *VersioningSkills) GetCurrentBranch() string {
	return vs.currentBranch
}

// GetBranch returns a branch by name
func (vs *VersioningSkills) GetBranch(name string) (*Branch, error) {
	branch, exists := vs.branches[name]
	if !exists {
		return nil, ErrBranchNotFound
	}
	return branch, nil
}

// DeleteBranch deletes a branch
func (vs *VersioningSkills) DeleteBranch(name string) error {
	if name == "main" {
		return ErrCannotDeleteMain
	}

	if _, exists := vs.branches[name]; !exists {
		return ErrBranchNotFound
	}

	if vs.currentBranch == name {
		vs.currentBranch = "main"
	}

	delete(vs.branches, name)
	return nil
}

// UpdateBranchHead updates a branch's HEAD version
func (vs *VersioningSkills) UpdateBranchHead(name string, versionID VersionID) error {
	branch, exists := vs.branches[name]
	if !exists {
		return ErrBranchNotFound
	}

	branch.HeadVersion = versionID
	branch.UpdatedAt = time.Now()
	return nil
}

// =============================================================================
// Conflict Resolvers for Skills
// =============================================================================

// SkillAutoConflictResolver automatically resolves conflicts
type SkillAutoConflictResolver struct{}

func (r *SkillAutoConflictResolver) ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	if len(conflict.Resolutions) > 1 {
		return &conflict.Resolutions[1], nil
	}
	if len(conflict.Resolutions) > 0 {
		return &conflict.Resolutions[0], nil
	}
	return &Resolution{
		Label:       "Auto-resolved",
		Description: "Accepted remote changes",
		ResultOp:    conflict.Op2,
	}, nil
}

func (r *SkillAutoConflictResolver) ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error) {
	resolutions := make([]*Resolution, len(conflicts))
	for i, conflict := range conflicts {
		res, err := r.ResolveConflict(ctx, conflict)
		if err != nil {
			return nil, err
		}
		resolutions[i] = res
	}
	return resolutions, nil
}

func (r *SkillAutoConflictResolver) AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool) {
	if conflict == nil || policy == AutoResolvePolicyNone {
		return nil, false
	}
	res, _ := r.ResolveConflict(context.Background(), conflict)
	return res, true
}

// SkillOursConflictResolver always takes our changes
type SkillOursConflictResolver struct{}

func (r *SkillOursConflictResolver) ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	if len(conflict.Resolutions) > 0 {
		return &conflict.Resolutions[0], nil
	}
	return &Resolution{
		Label:       "Keep ours",
		Description: "Kept our changes",
		ResultOp:    conflict.Op1,
	}, nil
}

func (r *SkillOursConflictResolver) ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error) {
	resolutions := make([]*Resolution, len(conflicts))
	for i, conflict := range conflicts {
		res, err := r.ResolveConflict(ctx, conflict)
		if err != nil {
			return nil, err
		}
		resolutions[i] = res
	}
	return resolutions, nil
}

func (r *SkillOursConflictResolver) AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool) {
	if conflict == nil {
		return nil, false
	}
	res, _ := r.ResolveConflict(context.Background(), conflict)
	return res, true
}

// SkillTheirsConflictResolver always takes their changes
type SkillTheirsConflictResolver struct{}

func (r *SkillTheirsConflictResolver) ResolveConflict(ctx context.Context, conflict *Conflict) (*Resolution, error) {
	if len(conflict.Resolutions) > 1 {
		return &conflict.Resolutions[1], nil
	}
	if len(conflict.Resolutions) > 0 {
		return &conflict.Resolutions[0], nil
	}
	return &Resolution{
		Label:       "Keep theirs",
		Description: "Accepted their changes",
		ResultOp:    conflict.Op2,
	}, nil
}

func (r *SkillTheirsConflictResolver) ResolveConflictBatch(ctx context.Context, conflicts []*Conflict) ([]*Resolution, error) {
	resolutions := make([]*Resolution, len(conflicts))
	for i, conflict := range conflicts {
		res, err := r.ResolveConflict(ctx, conflict)
		if err != nil {
			return nil, err
		}
		resolutions[i] = res
	}
	return resolutions, nil
}

func (r *SkillTheirsConflictResolver) AutoResolve(conflict *Conflict, policy AutoResolvePolicy) (*Resolution, bool) {
	if conflict == nil {
		return nil, false
	}
	res, _ := r.ResolveConflict(context.Background(), conflict)
	return res, true
}
