package sylkdir

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultGlobalRef = "main"

// GlobalCommitGraph tracks immutable commits and mutable refs for the global graph.
type GlobalCommitGraph struct {
	HeadCommitID string            `json:"head_commit_id,omitempty"`
	ActiveRef    string            `json:"active_ref,omitempty"`  // empty means detached HEAD
	DefaultRef   string            `json:"default_ref,omitempty"` // usually "main"
	Refs         map[string]string `json:"refs,omitempty"`        // ref name -> commit ID
	Commits      []GlobalCommit    `json:"commits,omitempty"`
}

// GlobalCommit is an immutable commit object pointing at a storage generation.
type GlobalCommit struct {
	ID              string             `json:"id"`
	StorageVersion  SemanticVersion    `json:"storage_version"`
	ParentCommitIDs []string           `json:"parent_commit_ids,omitempty"`
	SessionID       uint32             `json:"session_id"`
	SessionVer      SemanticVersion    `json:"session_version"`
	CreatedAt       time.Time          `json:"created_at"`
	Stats           GlobalVersionStats `json:"stats"`
}

func copyGlobalCommitGraph(g *GlobalCommitGraph) *GlobalCommitGraph {
	if g == nil {
		return nil
	}
	out := &GlobalCommitGraph{
		HeadCommitID: g.HeadCommitID,
		ActiveRef:    g.ActiveRef,
		DefaultRef:   g.DefaultRef,
		Refs:         make(map[string]string, len(g.Refs)),
		Commits:      make([]GlobalCommit, len(g.Commits)),
	}
	for k, v := range g.Refs {
		out.Refs[k] = v
	}
	for i, c := range g.Commits {
		out.Commits[i] = GlobalCommit{
			ID:              c.ID,
			StorageVersion:  c.StorageVersion,
			ParentCommitIDs: append([]string(nil), c.ParentCommitIDs...),
			SessionID:       c.SessionID,
			SessionVer:      c.SessionVer,
			CreatedAt:       c.CreatedAt,
			Stats:           c.Stats,
		}
	}
	return out
}

func legacyCommitID(version SemanticVersion) string {
	return "legacy_" + strings.TrimPrefix(version.DirName(), "v")
}

func newGlobalCommitID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("c_%x_%s", time.Now().UTC().UnixNano(), hex.EncodeToString(buf[:])), nil
}

func (m *GlobalMeta) ensureCommitGraphLocked() {
	if m.Manifest == nil {
		m.CommitGraph = nil
		return
	}
	if m.CommitGraph == nil || len(m.CommitGraph.Commits) == 0 {
		m.CommitGraph = buildCommitGraphFromManifest(m.Manifest)
	}
	normalizeCommitGraph(m.Manifest, m.CommitGraph)
}

func buildCommitGraphFromManifest(manifest *GlobalVersionManifest) *GlobalCommitGraph {
	if manifest == nil {
		return nil
	}
	versionToCommit := make(map[string]string, len(manifest.Versions))
	commits := make([]GlobalCommit, 0, len(manifest.Versions))
	for _, gv := range manifest.Versions {
		id := legacyCommitID(gv.ID)
		versionToCommit[gv.ID.String()] = id
	}
	for _, gv := range manifest.Versions {
		parents := make([]string, 0, 1)
		if !gv.ParentID.IsZero() {
			if parentID, ok := versionToCommit[gv.ParentID.String()]; ok {
				parents = append(parents, parentID)
			}
		}
		commits = append(commits, GlobalCommit{
			ID:              versionToCommit[gv.ID.String()],
			StorageVersion:  gv.ID,
			ParentCommitIDs: parents,
			SessionID:       gv.SessionID,
			SessionVer:      gv.SessionVer,
			CreatedAt:       gv.CreatedAt,
			Stats:           gv.Stats,
		})
	}

	headCommitID := versionToCommit[manifest.Head.String()]
	graph := &GlobalCommitGraph{
		HeadCommitID: headCommitID,
		ActiveRef:    defaultGlobalRef,
		DefaultRef:   defaultGlobalRef,
		Refs:         map[string]string{defaultGlobalRef: headCommitID},
		Commits:      commits,
	}
	normalizeCommitGraph(manifest, graph)
	return graph
}

func normalizeCommitGraph(manifest *GlobalVersionManifest, graph *GlobalCommitGraph) {
	if graph == nil {
		return
	}
	if graph.DefaultRef == "" {
		graph.DefaultRef = defaultGlobalRef
	}
	if graph.Refs == nil {
		graph.Refs = make(map[string]string)
	}

	commitByID := make(map[string]GlobalCommit, len(graph.Commits))
	commitByVersion := make(map[string]GlobalCommit, len(graph.Commits))
	for _, c := range graph.Commits {
		commitByID[c.ID] = c
		commitByVersion[c.StorageVersion.String()] = c
	}

	for name, id := range graph.Refs {
		if _, ok := commitByID[id]; !ok {
			delete(graph.Refs, name)
		}
	}

	if manifest != nil {
		if headCommit, ok := commitByVersion[manifest.Head.String()]; ok {
			graph.HeadCommitID = headCommit.ID
		}
	}
	if graph.HeadCommitID == "" {
		if id, ok := graph.Refs[graph.DefaultRef]; ok {
			graph.HeadCommitID = id
		}
	}
	if graph.HeadCommitID == "" && len(graph.Commits) > 0 {
		graph.HeadCommitID = graph.Commits[len(graph.Commits)-1].ID
	}
	if graph.ActiveRef != "" {
		id, ok := graph.Refs[graph.ActiveRef]
		if !ok || id != graph.HeadCommitID {
			graph.ActiveRef = ""
		}
	}
	if graph.ActiveRef == "" {
		if id, ok := graph.Refs[graph.DefaultRef]; ok && id == graph.HeadCommitID {
			graph.ActiveRef = graph.DefaultRef
		}
	}
	if _, ok := graph.Refs[graph.DefaultRef]; !ok && graph.HeadCommitID != "" && graph.ActiveRef != "" {
		graph.Refs[graph.DefaultRef] = graph.HeadCommitID
	}
}

func commitByID(commits []GlobalCommit, id string) (GlobalCommit, bool) {
	for _, c := range commits {
		if c.ID == id {
			return c, true
		}
	}
	return GlobalCommit{}, false
}

func commitByVersion(commits []GlobalCommit, version SemanticVersion) (GlobalCommit, bool) {
	for _, c := range commits {
		if c.StorageVersion.Equal(version) {
			return c, true
		}
	}
	return GlobalCommit{}, false
}

// AllocateGlobalVersion returns the next unique storage generation version.
// Versions remain immutable storage IDs, independent of branch topology.
func (m *GlobalMeta) AllocateGlobalVersion() (SemanticVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return SemanticVersion{}, ErrMetaNotLoaded
	}
	if m.Manifest == nil {
		return SemanticVersion{}, fmt.Errorf("sylkdir: manifest not initialized")
	}

	var maxMajor uint16
	for _, gv := range m.Manifest.Versions {
		if gv.ID.Major > maxMajor {
			maxMajor = gv.ID.Major
		}
	}
	if m.Version.Major > maxMajor {
		maxMajor = m.Version.Major
	}
	if maxMajor == 0 {
		maxMajor = 1
	}
	return SemanticVersion{Major: maxMajor + 1, Minor: 0, Patch: 0}, nil
}

// GetHeadCommitID returns the current HEAD commit ID, or "" if no graph exists.
func (m *GlobalMeta) GetHeadCommitID() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return ""
	}
	return m.CommitGraph.HeadCommitID
}

// GetActiveRef returns the checked out ref name, or "" for detached HEAD.
func (m *GlobalMeta) GetActiveRef() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return ""
	}
	return m.CommitGraph.ActiveRef
}

// GetCommitGraph returns a deep copy of the commit graph.
func (m *GlobalMeta) GetCommitGraph() *GlobalCommitGraph {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ensureCommitGraphLocked()
	return copyGlobalCommitGraph(m.CommitGraph)
}

// ListBranches returns branch names in sorted order.
func (m *GlobalMeta) ListBranches() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return nil
	}
	names := make([]string, 0, len(m.CommitGraph.Refs))
	for name := range m.CommitGraph.Refs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CreateBranch creates a new branch at the current HEAD commit.
func (m *GlobalMeta) CreateBranch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createBranchLocked(name, "")
}

// CreateBranchAt creates a new branch at a specific commit ID.
func (m *GlobalMeta) CreateBranchAt(name, commitID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createBranchLocked(name, commitID)
}

func (m *GlobalMeta) createBranchLocked(name, commitID string) error {
	if !m.loaded {
		return ErrMetaNotLoaded
	}
	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return fmt.Errorf("sylkdir: commit graph not initialized")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("sylkdir: branch name is required")
	}
	if _, exists := m.CommitGraph.Refs[name]; exists {
		return fmt.Errorf("sylkdir: branch %q already exists", name)
	}
	if commitID == "" {
		commitID = m.CommitGraph.HeadCommitID
	}
	if _, ok := commitByID(m.CommitGraph.Commits, commitID); !ok {
		return fmt.Errorf("sylkdir: commit %q not found", commitID)
	}

	return m.mutateAndSave(func() error {
		m.ensureCommitGraphLocked()
		m.CommitGraph.Refs[name] = commitID
		if m.CommitGraph.DefaultRef == "" {
			m.CommitGraph.DefaultRef = defaultGlobalRef
		}
		return nil
	})
}

// CheckoutBranch checks out a named branch and moves HEAD to its tip.
func (m *GlobalMeta) CheckoutBranch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}
	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return fmt.Errorf("sylkdir: commit graph not initialized")
	}

	commitID, ok := m.CommitGraph.Refs[name]
	if !ok {
		return fmt.Errorf("sylkdir: branch %q not found", name)
	}
	commit, ok := commitByID(m.CommitGraph.Commits, commitID)
	if !ok {
		return fmt.Errorf("sylkdir: commit %q not found", commitID)
	}

	return m.mutateAndSave(func() error {
		m.ensureCommitGraphLocked()
		m.CommitGraph.ActiveRef = name
		m.CommitGraph.HeadCommitID = commit.ID
		if m.Manifest != nil {
			m.Manifest.Head = commit.StorageVersion
		}
		m.Version = commit.StorageVersion
		return nil
	})
}

// CheckoutCommitID detaches HEAD at a specific commit.
func (m *GlobalMeta) CheckoutCommitID(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.loaded {
		return ErrMetaNotLoaded
	}
	m.ensureCommitGraphLocked()
	if m.CommitGraph == nil {
		return fmt.Errorf("sylkdir: commit graph not initialized")
	}
	commit, ok := commitByID(m.CommitGraph.Commits, id)
	if !ok {
		return fmt.Errorf("sylkdir: commit %q not found", id)
	}

	return m.mutateAndSave(func() error {
		m.ensureCommitGraphLocked()
		m.CommitGraph.ActiveRef = ""
		m.CommitGraph.HeadCommitID = commit.ID
		if m.Manifest != nil {
			m.Manifest.Head = commit.StorageVersion
		}
		m.Version = commit.StorageVersion
		return nil
	})
}
