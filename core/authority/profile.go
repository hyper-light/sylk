package authority

import (
	"context"
	"io/fs"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

type FileScope string

const (
	FileScopeNone              FileScope = "none"
	FileScopeDiskRead          FileScope = "disk-read"
	FileScopeGlobalRead        FileScope = "global-read"
	FileScopeGlobalReadWrite   FileScope = "global-read-write"
	FileScopePipelineRead      FileScope = "pipeline-read"
	FileScopePipelineReadWrite FileScope = "pipeline-read-write"
)

type ExecScope string

const (
	ExecScopeNone        ExecScope = "none"
	ExecScopeDisk        ExecScope = "disk"
	ExecScopeGlobalVFS   ExecScope = "global-vfs"
	ExecScopePipelineVFS ExecScope = "pipeline-vfs"
)

type Profile struct {
	AgentType      string
	FileScope      FileScope
	WorkspaceViews []versioning.WorkspaceView
	ExecScope      ExecScope

	// PeerConsultTargets enumerates the agent types this agent is
	// permitted to address via consult_peer. See docs/COMMS_MATRIX.md.
	// Self-targeting is ALWAYS excluded regardless of list contents —
	// the accessor `PermittedConsultTargets` filters out the caller's
	// own type so a config mistake cannot re-enable self-consult.
	//
	// Empty list ⇒ consult_peer is not registered for this agent type
	// at all (knowledge agents are reactive; orchestrator/guide/
	// guardian don't initiate peer consults).
	PeerConsultTargets []string

	// PeerChallengeTargets enumerates the agent types this agent is
	// permitted to address via challenge_peer. Challenges are higher-
	// stakes than consults (they cast doubt on peer commitments), so
	// these lists are strictly tighter. Same self-exclusion rule.
	//
	// Empty list ⇒ challenge_peer is not registered for this agent.
	PeerChallengeTargets []string

	// AllowsCrossPipelineConsult controls whether this agent may pass
	// a `target_pipeline_id` to consult_peer that differs from its own
	// pipeline. Most pipeline agents have this as true so they can
	// consult same-type siblings in adjacent pipelines; global and
	// knowledge agents have it as false because they either operate
	// at global scope or don't initiate at all.
	AllowsCrossPipelineConsult bool
}

// knowledgeAgents are purely reactive: they respond to consults via
// their message handlers and emit advisories on typed channels, but
// they MUST NOT initiate peer consults or challenges. The screenshot-
// captured bug ("archivalist formally challenging tester-pipeline")
// is impossible with this list empty. Same reasoning for librarian
// and academic. See docs/COMMS_MATRIX.md.
var profiles = map[string]Profile{
	"academic": {
		AgentType: "academic",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
		// Reactive; no outbound consult or challenge.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"architect": {
		AgentType: "architect",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
		// Architect uses global_review protocol for challenges; its
		// peer-consult surface is limited to knowledge + orchestrator.
		PeerConsultTargets:   []string{"librarian", "archivalist", "academic", "orchestrator"},
		PeerChallengeTargets: nil,
	},
	"archivalist": {
		AgentType: "archivalist",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
		// Reactive; no outbound consult or challenge.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"designer": {
		AgentType:      "designer",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"engineer", "tester-pipeline", "inspector-pipeline",
		},
		PeerChallengeTargets: []string{
			"engineer", "tester-pipeline", "inspector-pipeline",
		},
		AllowsCrossPipelineConsult: true,
	},
	"engineer": {
		AgentType:      "engineer",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"designer", "tester-pipeline", "inspector-pipeline",
		},
		PeerChallengeTargets: []string{
			"designer", "tester-pipeline", "inspector-pipeline",
		},
		AllowsCrossPipelineConsult: true,
	},
	"global-editor": {
		AgentType:      "global-editor",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
		// global-editor operates in global merge scope; no initiated
		// peer consults in the current design.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"guardian": {
		AgentType:      "guardian",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
		// Guardian is a policy enforcer; no peer-consult role.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"guide": {
		AgentType:      "guide",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
		// Guide is the router, not a peer. It dispatches but does not
		// initiate peer consults itself.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"inspector": {
		AgentType:      "inspector",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
		// Global inspector. Consult / challenge targets stay at the
		// global scope; it does NOT reach into per-task pipelines.
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"orchestrator", "architect", "tester-global",
		},
		PeerChallengeTargets: []string{
			"tester-global", "architect", "orchestrator",
		},
	},
	"inspector-pipeline": {
		AgentType:      "inspector-pipeline",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"engineer", "designer", "tester-pipeline",
		},
		PeerChallengeTargets: []string{
			"engineer", "designer", "tester-pipeline",
		},
		AllowsCrossPipelineConsult: true,
	},
	"librarian": {
		AgentType: "librarian",
		FileScope: FileScopeDiskRead,
		// Librarian reads across all three layers — disk (committed source
		// of truth), global (in-flight session overlay), and pipeline-local
		// drafts (engineer/tester work in progress). Originally the
		// librarian was strictly disk-only as a hack to prevent confusion
		// about "what actually exists." Practice exposed that confusion is
		// avoided more robustly by *naming the layer* on every read tool
		// (read_workspace_file requires a `view` param) and requiring
		// layer attribution in every response (see prompts/librarian).
		// FileScope stays at FileScopeDiskRead so writes remain blocked
		// across all layers — librarian is read-only by role.
		WorkspaceViews: []versioning.WorkspaceView{
			versioning.WorkspaceViewDisk,
			versioning.WorkspaceViewGlobal,
			versioning.WorkspaceViewPipeline,
		},
		ExecScope: ExecScopeDisk,
		// Reactive; no outbound consult or challenge.
		PeerConsultTargets:   nil,
		PeerChallengeTargets: nil,
	},
	"orchestrator": {
		AgentType:      "orchestrator",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
		// Orchestrator can consult knowledge agents + architect for
		// planning clarifications; uses DAG/control-plane channels
		// for authority escalations rather than challenge_peer.
		PeerConsultTargets:   []string{"librarian", "archivalist", "academic", "architect"},
		PeerChallengeTargets: nil,
	},
	"tester": {
		// "tester" (singleton root tester) uses the same profile as
		// tester-global for peer-interaction purposes.
		AgentType:      "tester",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"architect", "orchestrator", "inspector",
		},
		PeerChallengeTargets: []string{
			"architect", "orchestrator", "inspector",
		},
	},
	"tester-global": {
		AgentType:      "tester-global",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"architect", "orchestrator", "inspector",
		},
		PeerChallengeTargets: []string{
			"architect", "orchestrator", "inspector",
		},
	},
	"tester-pipeline": {
		AgentType:      "tester-pipeline",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
		PeerConsultTargets: []string{
			"librarian", "archivalist", "academic",
			"engineer", "designer", "inspector-pipeline",
		},
		PeerChallengeTargets: []string{
			"engineer", "designer", "inspector-pipeline",
		},
		AllowsCrossPipelineConsult: true,
	},
}

func ProfileFor(agentType string) Profile {
	trimmed := CanonicalAgentType(agentType)
	if profile, ok := profiles[trimmed]; ok {
		return cloneProfile(profile)
	}
	return Profile{
		AgentType: trimmed,
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
	}
}

func cloneProfile(profile Profile) Profile {
	cloned := profile
	cloned.WorkspaceViews = append([]versioning.WorkspaceView(nil), profile.WorkspaceViews...)
	cloned.PeerConsultTargets = append([]string(nil), profile.PeerConsultTargets...)
	cloned.PeerChallengeTargets = append([]string(nil), profile.PeerChallengeTargets...)
	return cloned
}

// CanonicalAgentType normalizes an agent type slug for authority checks.
// Agent-facing schemas use lowercase hyphenated names, but route metadata and
// model-authored JSON can drift in case or separators. Authority decisions must
// not depend on that surface spelling.
func CanonicalAgentType(agentType string) string {
	trimmed := strings.ToLower(strings.TrimSpace(agentType))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	return trimmed
}

// PermittedConsultTargets returns the agent types this agent may
// address via consult_peer, with the caller's own type filtered out.
// Result is sorted and deduplicated so consumers (skill enum, tests,
// error messages) get a stable ordering. Never returns the agent's
// own type — self-target is unrepresentable regardless of config.
//
// An unknown agent type returns nil (no permitted targets).
func PermittedConsultTargets(agentType string) []string {
	agentType = CanonicalAgentType(agentType)
	return filterSelfAndDedupe(ProfileFor(agentType).PeerConsultTargets, agentType)
}

// PermittedChallengeTargets returns the agent types this agent may
// address via challenge_peer. Same self-exclusion + dedupe + sort as
// PermittedConsultTargets. Challenge lists are strictly tighter than
// consult lists by design: challenges cast doubt on peer commitments
// and should be harder to initiate.
func PermittedChallengeTargets(agentType string) []string {
	agentType = CanonicalAgentType(agentType)
	return filterSelfAndDedupe(ProfileFor(agentType).PeerChallengeTargets, agentType)
}

// CanConsult reports whether callerType may address targetType via
// consult_peer. Self-target always returns false.
func CanConsult(callerType, targetType string) bool {
	callerType = CanonicalAgentType(callerType)
	targetType = CanonicalAgentType(targetType)
	if callerType == "" || targetType == "" || callerType == targetType {
		return false
	}
	for _, t := range ProfileFor(callerType).PeerConsultTargets {
		if CanonicalAgentType(t) == targetType {
			return true
		}
	}
	return false
}

// CanChallenge reports whether callerType may address targetType via
// challenge_peer. Self-target always returns false.
func CanChallenge(callerType, targetType string) bool {
	callerType = CanonicalAgentType(callerType)
	targetType = CanonicalAgentType(targetType)
	if callerType == "" || targetType == "" || callerType == targetType {
		return false
	}
	for _, t := range ProfileFor(callerType).PeerChallengeTargets {
		if CanonicalAgentType(t) == targetType {
			return true
		}
	}
	return false
}

// filterSelfAndDedupe drops empty entries and the agent's own type,
// deduplicates, and returns a sorted slice. Used by both Permitted*
// accessors.
func filterSelfAndDedupe(raw []string, selfType string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		entry = CanonicalAgentType(entry)
		if entry == "" || entry == selfType {
			continue
		}
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	sortStrings(out)
	return out
}

// sortStrings sorts in place using a tiny insertion sort. The lists
// are small (≤ 10 entries typically), so importing "sort" for one
// call is overkill.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1] > s[j] {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

func (p Profile) AllowsFileReads() bool {
	return p.FileScope != FileScopeNone
}

func (p Profile) AllowsFileWrites() bool {
	switch p.FileScope {
	case FileScopeGlobalReadWrite, FileScopePipelineReadWrite:
		return true
	default:
		return false
	}
}

func (p Profile) AllowsWorkspaceTools() bool {
	return len(p.WorkspaceViews) > 0
}

func (p Profile) AllowsWorkspaceView(view versioning.WorkspaceView) bool {
	for _, allowed := range p.WorkspaceViews {
		if allowed == view {
			return true
		}
	}
	return false
}

func RestrictFileAccess(agentType string, delegate versioning.FileAccess) versioning.FileAccess {
	if delegate == nil {
		return nil
	}
	return restrictedFileAccess{
		profile:  ProfileFor(agentType),
		delegate: delegate,
	}
}

func RestrictWorkspaceViews(agentType string, delegate versioning.WorkspaceViewAccess) versioning.WorkspaceViewAccess {
	if delegate == nil {
		return nil
	}
	return restrictedWorkspaceViews{
		profile:  ProfileFor(agentType),
		delegate: delegate,
	}
}

type restrictedFileAccess struct {
	profile  Profile
	delegate versioning.FileAccess
}

type visiblePathDelegate interface {
	RegisterVisiblePath(path string)
	VisiblePaths(root string) []string
}

type modificationDelegate interface {
	Modifications() []versioning.FileModification
}

func (r restrictedFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ReadFile(ctx, path)
}

func (r restrictedFileAccess) MkdirAll(ctx context.Context, path string) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.MkdirAll(ctx, path)
}

func (r restrictedFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.WriteFile(ctx, path, content)
}

func (r restrictedFileAccess) EditFile(ctx context.Context, path string, edits []versioning.FileEdit) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.EditFile(ctx, path, edits)
}

func (r restrictedFileAccess) DeleteFile(ctx context.Context, path string) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.DeleteFile(ctx, path)
}

func (r restrictedFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	if !r.profile.AllowsFileReads() {
		return false, versioning.ErrPermissionDenied
	}
	return r.delegate.Exists(ctx, path)
}

func (r restrictedFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ListDir(ctx, dir)
}

func (r restrictedFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Glob(ctx, root, pattern, exclude)
}

func (r restrictedFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]versioning.GrepMatch, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (r restrictedFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Stat(ctx, path)
}

func (r restrictedFileAccess) WorkingDir() string {
	return r.delegate.WorkingDir()
}

func (r restrictedFileAccess) IsReadOnly() bool {
	return !r.allowsWrites() || r.delegate.IsReadOnly()
}

func (r restrictedFileAccess) RegisterVisiblePath(path string) {
	if !r.allowsWrites() {
		return
	}
	delegate, ok := r.delegate.(visiblePathDelegate)
	if !ok {
		return
	}
	delegate.RegisterVisiblePath(path)
}

func (r restrictedFileAccess) VisiblePaths(root string) []string {
	if !r.profile.AllowsFileReads() {
		return nil
	}
	delegate, ok := r.delegate.(visiblePathDelegate)
	if !ok {
		return nil
	}
	return delegate.VisiblePaths(root)
}

func (r restrictedFileAccess) Modifications() []versioning.FileModification {
	if !r.profile.AllowsFileReads() {
		return nil
	}
	delegate, ok := r.delegate.(modificationDelegate)
	if !ok {
		return nil
	}
	return delegate.Modifications()
}

func (r restrictedFileAccess) allowsWrites() bool {
	if !r.profile.AllowsFileWrites() {
		return false
	}
	if _, ok := versioning.Underlying(r.delegate).(*versioning.DiskFileAccess); ok {
		switch r.profile.FileScope {
		case FileScopeGlobalReadWrite, FileScopePipelineReadWrite:
			return false
		}
	}
	return true
}

type restrictedWorkspaceViews struct {
	profile  Profile
	delegate versioning.WorkspaceViewAccess
}

func (r restrictedWorkspaceViews) WorkspaceViewsDelegate() versioning.WorkspaceViewAccess {
	return r.delegate
}

func (r restrictedWorkspaceViews) ReadFile(ctx context.Context, view versioning.WorkspaceView, path string, pipelineID string) ([]byte, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ReadFile(ctx, view, path, pipelineID)
}

func (r restrictedWorkspaceViews) Glob(ctx context.Context, view versioning.WorkspaceView, root, pattern string, exclude []string, pipelineID string) ([]string, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Glob(ctx, view, root, pattern, exclude, pipelineID)
}

func (r restrictedWorkspaceViews) Grep(ctx context.Context, view versioning.WorkspaceView, root, pattern, include string, contextLines, maxMatches int, pipelineID string) ([]versioning.GrepMatch, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Grep(ctx, view, root, pattern, include, contextLines, maxMatches, pipelineID)
}

func (r restrictedWorkspaceViews) InspectPath(ctx context.Context, path string, pipelineID string) (*versioning.WorkspacePathState, error) {
	if !r.profile.AllowsWorkspaceTools() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.InspectPath(ctx, path, pipelineID)
}

func (r restrictedWorkspaceViews) SummarizePaths(ctx context.Context, paths []string, pipelineID string) (*versioning.WorkspaceSummary, error) {
	if !r.profile.AllowsWorkspaceTools() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.SummarizePaths(ctx, paths, pipelineID)
}

func (r restrictedWorkspaceViews) DefaultView() versioning.WorkspaceView {
	defaultView := r.delegate.DefaultView()
	if r.profile.AllowsWorkspaceView(defaultView) {
		return defaultView
	}
	if len(r.profile.WorkspaceViews) == 0 {
		return ""
	}
	return r.profile.WorkspaceViews[0]
}
