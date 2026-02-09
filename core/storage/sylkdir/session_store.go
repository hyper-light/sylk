// Package sylkdir provides session storage with per-version data directories.
package sylkdir

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

// SessionStatus represents the state of a session.
type SessionStatus string

const (
	// SessionActive indicates the session is currently in use.
	SessionActive SessionStatus = "active"
	// SessionCommitted indicates the session has been merged to global.
	SessionCommitted SessionStatus = "committed"
)

// SessionMeta contains session metadata.
type SessionMeta struct {
	ID          uint32        `json:"id"`
	StringID    string        `json:"string_id"`
	CreatedAt   time.Time     `json:"created_at"`
	Status      SessionStatus `json:"status"`
	CommittedAt *time.Time    `json:"committed_at,omitempty"`
}

// BaseSnapshot captures global state at session start.
type BaseSnapshot struct {
	GlobalVersion     SemanticVersion `json:"global_version"`     // Global KG version at session start
	CommittedSessions []uint32        `json:"committed_sessions"`
	SnapshotAt        time.Time       `json:"snapshot_at"`
	NextNodeID        uint32          `json:"next_node_id"`
}

// VersionStats tracks changes in a version.
type VersionStats struct {
	NodesCreated   uint32 `json:"nodes_created"`
	EdgesCreated   uint32 `json:"edges_created"`
	VectorsCreated uint32 `json:"vectors_created"`
	DocsBytes      uint64 `json:"docs_bytes"`
}

// SemanticVersion represents a semantic version (major.minor.patch).
type SemanticVersion struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
	Patch uint16 `json:"patch"`
}

// String returns the version as "vMAJOR.MINOR.PATCH".
func (v SemanticVersion) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// DirName returns the directory name for this version.
func (v SemanticVersion) DirName() string {
	return v.String()
}

// IsZero returns true if this is the zero version.
func (v SemanticVersion) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0
}

// Equal returns true if two versions are equal.
func (v SemanticVersion) Equal(other SemanticVersion) bool {
	return v.Major == other.Major && v.Minor == other.Minor && v.Patch == other.Patch
}

// BumpMajor returns a new version with major incremented.
func (v SemanticVersion) BumpMajor() SemanticVersion {
	return SemanticVersion{Major: v.Major + 1, Minor: 0, Patch: 0}
}

// BumpMinor returns a new version with minor incremented.
func (v SemanticVersion) BumpMinor() SemanticVersion {
	return SemanticVersion{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
}

// BumpPatch returns a new version with patch incremented.
func (v SemanticVersion) BumpPatch() SemanticVersion {
	return SemanticVersion{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}

// ParseSemanticVersion parses a version string like "v1.0.0".
func ParseSemanticVersion(s string) (SemanticVersion, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return SemanticVersion{}, fmt.Errorf("invalid version format: %s", s)
	}
	major, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return SemanticVersion{}, err
	}
	minor, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return SemanticVersion{}, err
	}
	patch, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil {
		return SemanticVersion{}, err
	}
	return SemanticVersion{Major: uint16(major), Minor: uint16(minor), Patch: uint16(patch)}, nil
}

// Version represents a checkpoint in session history.
type Version struct {
	ID        SemanticVersion `json:"id"`
	ParentID  SemanticVersion `json:"parent_id"`
	Name      string          `json:"name,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Trigger   string          `json:"trigger"` // "explicit", "auto_delta", "implicit"
	Stats     VersionStats    `json:"stats"`
}

// VersionManifest tracks the version DAG for a session.
type VersionManifest struct {
	SessionID uint32          `json:"session_id"`
	Head      SemanticVersion `json:"head"`
	Versions  []Version       `json:"versions"`
}

// SessionStore manages per-session storage directories.
type SessionStore struct {
	sylkDir *SylkDir
}

// NewSessionStore creates a new session store.
func NewSessionStore(sd *SylkDir) *SessionStore {
	return &SessionStore{
		sylkDir: sd,
	}
}

// Create creates a new session with initial version.
func (s *SessionStore) Create(sessionID uint32, baseSnapshot *BaseSnapshot) (*Session, error) {
	stringID := fmt.Sprintf("ses_%03d", sessionID)
	return s.CreateNamed(sessionID, stringID, baseSnapshot)
}

// CreateNamed creates a new session with a custom string ID.
func (s *SessionStore) CreateNamed(sessionID uint32, stringID string, baseSnapshot *BaseSnapshot) (*Session, error) {
	sessionPath := s.sessionPath(stringID)

	// Create session directory structure
	dirs := []string{
		sessionPath,
		filepath.Join(sessionPath, "base"),
		filepath.Join(sessionPath, "versions"),
		filepath.Join(sessionPath, "delta"),
		filepath.Join(sessionPath, "state"),
		filepath.Join(sessionPath, "agents"),
		filepath.Join(sessionPath, "messages"),
		filepath.Join(sessionPath, "wal"),
		filepath.Join(sessionPath, "data", "nodes"),
		filepath.Join(sessionPath, "data", "vectors"),
		filepath.Join(sessionPath, "data", "docs"),
		filepath.Join(sessionPath, "data", "chunks"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create session dir %s: %w", dir, err)
		}
	}

	// Create session meta
	meta := &SessionMeta{
		ID:        sessionID,
		StringID:  stringID,
		CreatedAt: time.Now(),
		Status:    SessionActive,
	}

	if err := s.writeMeta(sessionPath, meta); err != nil {
		return nil, fmt.Errorf("write session meta: %w", err)
	}

	// Write base snapshot
	if baseSnapshot == nil {
		baseSnapshot = &BaseSnapshot{
			CommittedSessions: []uint32{},
			SnapshotAt:        time.Now(),
			NextNodeID:        1,
		}
	}
	if err := s.writeBaseSnapshot(sessionPath, baseSnapshot); err != nil {
		return nil, fmt.Errorf("write base snapshot: %w", err)
	}

	// Create version manifest with initial version v1.0.0
	initialVersion := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	manifest := &VersionManifest{
		SessionID: sessionID,
		Head:      initialVersion,
		Versions: []Version{
			{
				ID:        initialVersion,
				ParentID:  SemanticVersion{}, // Zero value = no parent
				Name:      "initial",
				CreatedAt: time.Now(),
				Trigger:   "implicit",
				Stats:     VersionStats{},
			},
		},
	}

	if err := s.writeManifest(sessionPath, manifest); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	// Create initial version directory
	if err := s.createVersionDirSemver(sessionPath, initialVersion); err != nil {
		return nil, fmt.Errorf("create initial version: %w", err)
	}

	// Create delta tracker
	delta := NewDeltaTracker(filepath.Join(sessionPath, "delta", "tracker.json"))
	if err := delta.Persist(); err != nil {
		return nil, fmt.Errorf("persist delta tracker: %w", err)
	}

	sess := &Session{
		store:        s,
		path:         sessionPath,
		Meta:         meta,
		BaseSnapshot: baseSnapshot,
		Manifest:     manifest,
	}

	// Open shared data files for nodes and vectors.
	nodeDF, err := OpenSharedDataFile(sess.SharedNodeDataPath())
	if err != nil {
		return nil, fmt.Errorf("open node data file: %w", err)
	}
	sess.NodeDataFile = nodeDF

	vecDF, err := OpenSharedDataFile(sess.SharedVectorDataPath())
	if err != nil {
		nodeDF.Close()
		return nil, fmt.Errorf("open vector data file: %w", err)
	}
	sess.VectorDataFile = vecDF

	docDF, err := OpenSharedDataFile(sess.SharedDocDataPath())
	if err != nil {
		nodeDF.Close()
		vecDF.Close()
		return nil, fmt.Errorf("open doc data file: %w", err)
	}
	sess.DocDataFile = docDF

	chunkDF, err := OpenSharedDataFile(sess.SharedChunkDataPath())
	if err != nil {
		nodeDF.Close()
		vecDF.Close()
		docDF.Close()
		return nil, fmt.Errorf("open chunk data file: %w", err)
	}
	sess.ChunkDataFile = chunkDF

	docIDMap, err := LoadDocIDMap(sess.DocIDMapPath())
	if err != nil {
		// No existing map — create fresh.
		docIDMap = NewDocIDMap(sess.DocIDMapPath())
	}
	sess.DocIDMap = docIDMap

	// Open session WAL
	sessionWAL, err := OpenSessionWAL(SessionWALConfig{
		Dir:         filepath.Join(sessionPath, "wal"),
		SyncOnWrite: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open session wal: %w", err)
	}
	sess.WAL = sessionWAL
	sess.DeltaTracker = delta
	sess.CheckpointCtrl = s.buildCheckpointController(delta, sessionWAL)

	// Create per-version Bleve store and open HEAD.
	vbs := NewVersionBleveStore(sess)
	if err := vbs.OpenHead(); err != nil {
		return nil, fmt.Errorf("open head bleve: %w", err)
	}
	sess.BleveStore = vbs

	return sess, nil
}

// Load loads an existing session.
func (s *SessionStore) Load(stringID string) (*Session, error) {
	sessionPath := s.sessionPath(stringID)

	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("session %s not found", stringID)
	}

	meta, err := s.loadMeta(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("load meta: %w", err)
	}

	baseSnapshot, err := s.loadBaseSnapshot(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("load base snapshot: %w", err)
	}

	manifest, err := s.loadManifest(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	sess := &Session{
		store:        s,
		path:         sessionPath,
		Meta:         meta,
		BaseSnapshot: baseSnapshot,
		Manifest:     manifest,
	}

	// Open shared data files and WAL for active sessions.
	if meta.Status == SessionActive {
		nodeDF, dfErr := OpenSharedDataFile(sess.SharedNodeDataPath())
		if dfErr == nil {
			sess.NodeDataFile = nodeDF
		}
		vecDF, dfErr := OpenSharedDataFile(sess.SharedVectorDataPath())
		if dfErr == nil {
			sess.VectorDataFile = vecDF
		}
		docDF, dfErr := OpenSharedDataFile(sess.SharedDocDataPath())
		if dfErr == nil {
			sess.DocDataFile = docDF
		}
		chunkDF, dfErr := OpenSharedDataFile(sess.SharedChunkDataPath())
		if dfErr == nil {
			sess.ChunkDataFile = chunkDF
		}
		docIDMap, dimErr := LoadDocIDMap(sess.DocIDMapPath())
		if dimErr != nil {
			docIDMap = NewDocIDMap(sess.DocIDMapPath())
		}
		sess.DocIDMap = docIDMap

		walDir := filepath.Join(sessionPath, "wal")
		if _, statErr := os.Stat(walDir); statErr == nil {
			sessionWAL, walErr := OpenSessionWAL(SessionWALConfig{Dir: walDir})
			if walErr == nil {
				sess.WAL = sessionWAL
			}
		}
	}

	// Load delta tracker and checkpoint controller for active sessions.
	if meta.Status == SessionActive {
		delta := NewDeltaTracker(filepath.Join(sessionPath, "delta", "tracker.json"))
		_ = delta.Load() // Non-fatal: fresh start on error
		sess.DeltaTracker = delta
		sess.CheckpointCtrl = s.buildCheckpointController(delta, sess.WAL)
	}

	// Open or create per-version Bleve for active sessions.
	if meta.Status == SessionActive {
		vbs := NewVersionBleveStore(sess)
		bleveDBPath := filepath.Join(sess.VersionPath(manifest.Head), "bleve", "documents.bleve")

		if _, statErr := os.Stat(bleveDBPath); statErr == nil {
			// Bleve exists on disk — reopen if not locked.
			if !isBoltLocked(filepath.Join(bleveDBPath, "store", "root.bolt")) {
				if err := vbs.OpenHead(); err == nil {
					sess.BleveStore = vbs
				}
			}
		} else if os.IsNotExist(statErr) {
			// No Bleve on disk — bootstrap from JSONL.
			sess.BleveStore = vbs
			if err := vbs.RebuildHead(); err != nil {
				return nil, fmt.Errorf("bootstrap version bleve: %w", err)
			}
		}

		// Migration: remove old flat bleve directory if present
		oldBlevePath := filepath.Join(sessionPath, "bleve")
		if _, err := os.Stat(oldBlevePath); err == nil {
			os.RemoveAll(oldBlevePath)
		}
	}

	return sess, nil
}

// List returns all session IDs.
func (s *SessionStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.sylkDir.SessionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var sessions []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "ses_") {
			sessions = append(sessions, entry.Name())
		}
	}
	return sessions, nil
}

// SetActive updates the active symlink to point to the given session.
func (s *SessionStore) SetActive(stringID string) error {
	activePath := filepath.Join(s.sylkDir.SessionsPath(), "active")
	sessionPath := s.sessionPath(stringID)

	// Verify session exists
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		return fmt.Errorf("session %s not found", stringID)
	}

	// Remove existing symlink
	os.Remove(activePath)

	// Create new symlink (relative path for portability)
	return os.Symlink(stringID, activePath)
}

// GetActive returns the currently active session ID.
func (s *SessionStore) GetActive() (string, error) {
	activePath := filepath.Join(s.sylkDir.SessionsPath(), "active")

	target, err := os.Readlink(activePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	return target, nil
}

// sessionPath returns the path to a session directory.
func (s *SessionStore) sessionPath(stringID string) string {
	return filepath.Join(s.sylkDir.SessionsPath(), stringID)
}

// createVersionDirSemver creates a version directory with semantic versioning.
func (s *SessionStore) createVersionDirSemver(sessionPath string, version SemanticVersion) error {
	versionPath := filepath.Join(sessionPath, "versions", version.DirName())

	dirs := []string{
		versionPath,
		filepath.Join(versionPath, "nodes"),
		filepath.Join(versionPath, "edges"),
		filepath.Join(versionPath, "vectors"),
		filepath.Join(versionPath, "docs"),
		filepath.Join(versionPath, "chunks"),
		filepath.Join(versionPath, "bleve"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	// Create version meta
	vMeta := map[string]interface{}{
		"version":    version.String(),
		"created_at": time.Now().Format(time.RFC3339),
	}

	data, _ := json.MarshalIndent(vMeta, "", "  ")
	if err := os.WriteFile(filepath.Join(versionPath, "meta.json"), data, 0644); err != nil {
		return err
	}

	// Create empty deletions.json
	deletions := map[string]interface{}{
		"nodes": []uint32{},
		"edges": []interface{}{},
	}
	data, _ = json.MarshalIndent(deletions, "", "  ")
	if err := os.WriteFile(filepath.Join(versionPath, "deletions.json"), data, 0644); err != nil {
		return err
	}

	// Create per-version edge data file (edges remain per-version in sessions).
	edgeFile := filepath.Join(versionPath, "edges", "data.bin")
	if err := os.WriteFile(edgeFile, []byte{}, 0644); err != nil {
		return fmt.Errorf("create data file %s: %w", edgeFile, err)
	}

	// Create empty offset indexes for nodes, vectors, and docs.
	nodeIdx := NewOffsetIndex(filepath.Join(versionPath, "nodes", "index.bin"), offsetIndexMinCapacity)
	if err := nodeIdx.Save(); err != nil {
		return fmt.Errorf("create node index: %w", err)
	}
	vecIdx := NewOffsetIndex(filepath.Join(versionPath, "vectors", "index.bin"), offsetIndexMinCapacity)
	if err := vecIdx.Save(); err != nil {
		return fmt.Errorf("create vector index: %w", err)
	}
	docIdx := NewOffsetIndex(filepath.Join(versionPath, "docs", "index.bin"), offsetIndexMinCapacity)
	if err := docIdx.Save(); err != nil {
		return fmt.Errorf("create doc index: %w", err)
	}
	chunkIdx := NewOffsetIndex(filepath.Join(versionPath, "chunks", "index.bin"), offsetIndexMinCapacity)
	if err := chunkIdx.Save(); err != nil {
		return fmt.Errorf("create chunk index: %w", err)
	}

	return nil
}

func (s *SessionStore) writeMeta(sessionPath string, meta *SessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sessionPath, "meta.json"), data, 0644)
}

func (s *SessionStore) loadMeta(sessionPath string) (*SessionMeta, error) {
	data, err := os.ReadFile(filepath.Join(sessionPath, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *SessionStore) writeBaseSnapshot(sessionPath string, snapshot *BaseSnapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sessionPath, "base", "snapshot.json"), data, 0644)
}

func (s *SessionStore) loadBaseSnapshot(sessionPath string) (*BaseSnapshot, error) {
	data, err := os.ReadFile(filepath.Join(sessionPath, "base", "snapshot.json"))
	if err != nil {
		return nil, err
	}
	var snapshot BaseSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *SessionStore) writeManifest(sessionPath string, manifest *VersionManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sessionPath, "versions", "manifest.json"), data, 0644)
}

func (s *SessionStore) loadManifest(sessionPath string) (*VersionManifest, error) {
	data, err := os.ReadFile(filepath.Join(sessionPath, "versions", "manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest VersionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// buildCheckpointController creates a CheckpointController from calibration data.
func (s *SessionStore) buildCheckpointController(delta *DeltaTracker, wal *SessionWAL) *CheckpointController {
	calibration, _ := LoadCalibration(s.sylkDir.RootPath())
	walBytesFn := func() int64 { return 0 }
	if wal != nil {
		walBytesFn = wal.WALBytes
	}
	return NewCheckpointController(CheckpointControllerConfig{
		Calibration: calibration,
		Delta:       delta,
		WALBytes:    walBytesFn,
	})
}

// Session represents an active session.
type Session struct {
	store        *SessionStore
	path         string
	Meta         *SessionMeta
	BaseSnapshot *BaseSnapshot
	Manifest     *VersionManifest
	BleveStore     *VersionBleveStore     // Per-version Bleve store (nil until opened)
	WAL            *SessionWAL            // Session WAL (nil for global/committed sessions)
	DeltaTracker   *DeltaTracker          // Mutation delta tracker (nil for committed sessions)
	CheckpointCtrl *CheckpointController  // Adaptive checkpoint controller (nil for committed sessions)
	NodeDataFile   *SharedDataFile        // Shared node data file (nil for committed sessions)
	VectorDataFile *SharedDataFile        // Shared vector data file (nil for committed sessions)
	DocDataFile    *SharedDataFile        // Shared doc data file (nil for committed sessions)
	ChunkDataFile  *SharedDataFile        // Shared chunk ref data file (nil for committed sessions)
	DocIDMap       *DocIDMap              // String→uint32 doc ID mapping (nil for committed sessions)

	// Shared node ID counter for ingestion. Initialized from BaseSnapshot.NextNodeID.
	nextNodeID uint32

	// Registered version stores (set by constructors for checkpoint integration).
	nodeStore     *VersionNodeStore
	vectorStore   *VersionVectorStore
	docStore      *VersionDocStore
	chunkRefStore *ChunkRefStore

	// Pending* fields hold entities from session ingestion that bypass
	// the session disk stores. Consumed directly by CommitToGlobal.
	// When PendingNodes is non-nil (even empty), PendingMode is active:
	// writeEntities skips G1 session store writes entirely.
	PendingNodes     []*Node
	PendingEdges     []*Edge
	PendingDocs      []*VersionDocument
	PendingChunkRefs []*ChunkRef
	PendingVectors   []*VersionVector
}

// RegisterNodeStore is called by NewVersionNodeStore to enable checkpoint index saving.
func (sess *Session) RegisterNodeStore(s *VersionNodeStore) {
	sess.nodeStore = s
}

// RegisterVectorStore is called by NewVersionVectorStore to enable checkpoint index saving.
func (sess *Session) RegisterVectorStore(s *VersionVectorStore) {
	sess.vectorStore = s
}

// RegisterDocStore is called by NewVersionDocStore to enable checkpoint index saving.
func (sess *Session) RegisterDocStore(s *VersionDocStore) {
	sess.docStore = s
}

// RegisterChunkRefStore is called by NewChunkRefStore to enable checkpoint index saving.
func (sess *Session) RegisterChunkRefStore(s *ChunkRefStore) {
	sess.chunkRefStore = s
}

// InsertDocument creates a document with chunked content, embeddings, and
// graph structure. This is the primary entry point for document ingestion.
// The embedder may be nil, in which case no vectors are created.
func (sess *Session) InsertDocument(ctx context.Context, req *JointDocRequest, e embedder.Embedder) (*JointDocResult, error) {
	jdi := NewJointDocIngestion(sess, sess.nextAllocID(), e)
	return jdi.Insert(ctx, req)
}

// nextAllocID returns a pointer to the session's shared node ID counter.
// Initializes from BaseSnapshot.NextNodeID on first use.
func (sess *Session) nextAllocID() *uint32 {
	if sess.nextNodeID == 0 && sess.BaseSnapshot != nil {
		sess.nextNodeID = sess.BaseSnapshot.NextNodeID
	}
	return &sess.nextNodeID
}

// SharedNodeDataPath returns the path to the session's shared node data file.
func (sess *Session) SharedNodeDataPath() string {
	return filepath.Join(sess.path, "data", "nodes", "data.bin")
}

// SharedVectorDataPath returns the path to the session's shared vector data file.
func (sess *Session) SharedVectorDataPath() string {
	return filepath.Join(sess.path, "data", "vectors", "data.bin")
}

// SharedDocDataPath returns the path to the session's shared doc data file.
func (sess *Session) SharedDocDataPath() string {
	return filepath.Join(sess.path, "data", "docs", "data.bin")
}

// DocIDMapPath returns the path to the session's doc ID map file.
func (sess *Session) DocIDMapPath() string {
	return filepath.Join(sess.path, "data", "docs", "id_map.bin")
}

// DocIndexPath returns the path to the doc offset index for a version.
func (sess *Session) DocIndexPath(version SemanticVersion) string {
	return filepath.Join(sess.VersionPath(version), "docs", "index.bin")
}

// NodeIndexPath returns the path to the node offset index for a version.
func (sess *Session) NodeIndexPath(version SemanticVersion) string {
	return filepath.Join(sess.VersionPath(version), "nodes", "index.bin")
}

// VectorIndexPath returns the path to the vector offset index for a version.
func (sess *Session) VectorIndexPath(version SemanticVersion) string {
	return filepath.Join(sess.VersionPath(version), "vectors", "index.bin")
}

// SharedChunkDataPath returns the path to the session's shared chunk ref data file.
func (sess *Session) SharedChunkDataPath() string {
	return filepath.Join(sess.path, "data", "chunks", "data.bin")
}

// ChunkIndexPath returns the path to the chunk ref offset index for a version.
func (sess *Session) ChunkIndexPath(version SemanticVersion) string {
	return filepath.Join(sess.VersionPath(version), "chunks", "index.bin")
}

// Path returns the session directory path.
func (sess *Session) Path() string {
	return sess.path
}

// VersionPath returns the path to a specific version directory.
func (sess *Session) VersionPath(version SemanticVersion) string {
	return filepath.Join(sess.path, "versions", version.DirName())
}

// HeadVersionPath returns the path to the HEAD version directory.
func (sess *Session) HeadVersionPath() string {
	return sess.VersionPath(sess.Manifest.Head)
}

// DocsPath returns the path to the docs directory for a version.
func (sess *Session) DocsPath(version SemanticVersion) string {
	return filepath.Join(sess.VersionPath(version), "docs")
}

// CheckpointType specifies how to bump the version.
type CheckpointType string

const (
	CheckpointMajor CheckpointType = "major" // Breaking changes
	CheckpointMinor CheckpointType = "minor" // New features
	CheckpointPatch CheckpointType = "patch" // Bug fixes
)

// Checkpoint creates a new version checkpoint with semantic versioning.
// Each version is a cumulative snapshot containing all data from parent versions.
func (sess *Session) Checkpoint(name string, checkpointType CheckpointType) (SemanticVersion, error) {
	var newVersion SemanticVersion
	oldHead := sess.Manifest.Head

	switch checkpointType {
	case CheckpointMajor:
		newVersion = oldHead.BumpMajor()
	case CheckpointMinor:
		newVersion = oldHead.BumpMinor()
	case CheckpointPatch:
		newVersion = oldHead.BumpPatch()
	default:
		newVersion = oldHead.BumpPatch()
	}

	v := Version{
		ID:        newVersion,
		ParentID:  oldHead,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger:   string(checkpointType),
		Stats:     VersionStats{},
	}

	// Create version directory
	if err := sess.store.createVersionDirSemver(sess.path, newVersion); err != nil {
		return SemanticVersion{}, err
	}

	// Snapshot parent data into new version for cumulative semantics
	if err := sess.snapshotParentData(oldHead, newVersion); err != nil {
		return SemanticVersion{}, fmt.Errorf("snapshot parent data: %w", err)
	}

	// Snapshot Bleve (close parent, copy, open new)
	if sess.BleveStore != nil {
		if err := sess.BleveStore.SnapshotBleve(oldHead, newVersion); err != nil {
			return SemanticVersion{}, fmt.Errorf("snapshot bleve: %w", err)
		}
	}

	sess.Manifest.Versions = append(sess.Manifest.Versions, v)
	sess.Manifest.Head = newVersion

	// Persist manifest
	if err := sess.store.writeManifest(sess.path, sess.Manifest); err != nil {
		return SemanticVersion{}, err
	}

	// Reopen HEAD Bleve for the new version (rebuild from doc data if needed).
	if sess.BleveStore != nil {
		if err := sess.BleveStore.OpenHead(); err != nil {
			return SemanticVersion{}, fmt.Errorf("reopen head bleve after checkpoint: %w", err)
		}
	}

	return newVersion, nil
}

// Checkout switches HEAD to a different version.
// Since versions are cumulative snapshots, checkout closes the current HEAD's
// Bleve and opens the target version's. If the target version's Bleve is
// missing (lazy snapshot), it will be rebuilt from doc data on demand.
func (sess *Session) Checkout(version SemanticVersion) error {
	// Verify version exists
	found := false
	for _, v := range sess.Manifest.Versions {
		if v.ID.Equal(version) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("version %s not found", version.String())
	}

	// Close current HEAD Bleve before changing HEAD
	if sess.BleveStore != nil {
		if err := sess.BleveStore.CloseHead(); err != nil {
			return fmt.Errorf("close head bleve: %w", err)
		}
	}

	sess.Manifest.Head = version
	if err := sess.store.writeManifest(sess.path, sess.Manifest); err != nil {
		return err
	}

	// Open checked-out version's Bleve (cumulative, so it has everything)
	if sess.BleveStore != nil {
		if err := sess.BleveStore.OpenHead(); err != nil {
			return fmt.Errorf("open checkout head bleve: %w", err)
		}
	}

	return nil
}

// GetAncestorChain returns the ancestor chain from HEAD to root.
func (sess *Session) GetAncestorChain() []SemanticVersion {
	versionMap := make(map[string]*Version)
	for i := range sess.Manifest.Versions {
		versionMap[sess.Manifest.Versions[i].ID.String()] = &sess.Manifest.Versions[i]
	}

	var chain []SemanticVersion
	current := sess.Manifest.Head

	for !current.IsZero() {
		chain = append(chain, current)
		if v, ok := versionMap[current.String()]; ok {
			current = v.ParentID
		} else {
			break
		}
	}

	return chain
}

// ListVersions returns all versions in the session.
func (sess *Session) ListVersions() []Version {
	return sess.Manifest.Versions
}

// VersionCount returns the number of versions.
func (sess *Session) VersionCount() int {
	return len(sess.Manifest.Versions)
}

// Save persists session state.
func (sess *Session) Save() error {
	if err := sess.store.writeMeta(sess.path, sess.Meta); err != nil {
		return err
	}
	return sess.store.writeManifest(sess.path, sess.Manifest)
}

// snapshotParentData creates cumulative snapshots by cloning node/vector/doc
// offset indexes and copying per-version edge data files from parent to target.
func (sess *Session) snapshotParentData(parent, target SemanticVersion) error {
	// Clone node index (from in-memory if available, else from disk).
	if err := sess.cloneNodeIndex(parent, target); err != nil {
		return fmt.Errorf("clone node index: %w", err)
	}

	// Clone vector index.
	if err := sess.cloneVectorIndex(parent, target); err != nil {
		return fmt.Errorf("clone vector index: %w", err)
	}

	// Clone doc index.
	if err := sess.cloneDocIndex(parent, target); err != nil {
		return fmt.Errorf("clone doc index: %w", err)
	}

	// Clone chunk ref index.
	if err := sess.cloneChunkIndex(parent, target); err != nil {
		return fmt.Errorf("clone chunk index: %w", err)
	}

	// Copy per-version edge data file.
	src := filepath.Join(sess.VersionPath(parent), "edges", "data.bin")
	dst := filepath.Join(sess.VersionPath(target), "edges", "data.bin")
	if err := copyFileIfExists(src, dst); err != nil {
		return fmt.Errorf("copy edges/data.bin: %w", err)
	}

	return nil
}

// cloneNodeIndex clones the parent version's node offset index for the target.
func (sess *Session) cloneNodeIndex(parent, target SemanticVersion) error {
	targetPath := sess.NodeIndexPath(target)

	// Try in-memory index from registered store first.
	if sess.nodeStore != nil {
		if idx := sess.nodeStore.getInMemoryIndex(parent); idx != nil {
			cloned := idx.Clone(targetPath)
			sess.nodeStore.RegisterIndex(target, cloned)
			return cloned.Save()
		}
	}

	// Fall back to copying the on-disk index file.
	return copyFileIfExists(sess.NodeIndexPath(parent), targetPath)
}

// cloneVectorIndex clones the parent version's vector offset index for the target.
func (sess *Session) cloneVectorIndex(parent, target SemanticVersion) error {
	targetPath := sess.VectorIndexPath(target)

	// Try in-memory index from registered store first.
	if sess.vectorStore != nil {
		if idx := sess.vectorStore.getInMemoryIndex(parent); idx != nil {
			cloned := idx.Clone(targetPath)
			sess.vectorStore.RegisterIndex(target, cloned)
			return cloned.Save()
		}
	}

	// Fall back to copying the on-disk index file.
	return copyFileIfExists(sess.VectorIndexPath(parent), targetPath)
}

// cloneDocIndex clones the parent version's doc offset index for the target.
func (sess *Session) cloneDocIndex(parent, target SemanticVersion) error {
	targetPath := sess.DocIndexPath(target)

	// Try in-memory index from registered store first.
	if sess.docStore != nil {
		if idx := sess.docStore.getInMemoryIndex(parent); idx != nil {
			cloned := idx.Clone(targetPath)
			sess.docStore.RegisterIndex(target, cloned)
			return cloned.Save()
		}
	}

	// Fall back to copying the on-disk index file.
	return copyFileIfExists(sess.DocIndexPath(parent), targetPath)
}

// cloneChunkIndex clones the parent version's chunk ref offset index for the target.
func (sess *Session) cloneChunkIndex(parent, target SemanticVersion) error {
	targetPath := sess.ChunkIndexPath(target)

	// Try in-memory index from registered store first.
	if sess.chunkRefStore != nil {
		if idx := sess.chunkRefStore.getInMemoryIndex(parent); idx != nil {
			cloned := idx.Clone(targetPath)
			sess.chunkRefStore.RegisterIndex(target, cloned)
			return cloned.Save()
		}
	}

	// Fall back to copying the on-disk index file.
	return copyFileIfExists(sess.ChunkIndexPath(parent), targetPath)
}

// CloseBleve closes the per-version Bleve store.
// Safe to call on a session with no BleveStore (no-op).
func (sess *Session) CloseBleve() error {
	if sess.BleveStore == nil {
		return nil
	}
	return sess.BleveStore.CloseAll()
}

// CloseWAL closes the session WAL. Safe to call when WAL is nil.
func (sess *Session) CloseWAL() error {
	if sess.WAL == nil {
		return nil
	}
	return sess.WAL.Close()
}

// ExecuteCheckpoint dispatches a checkpoint operation based on granularity.
// Errors are non-fatal from the caller's perspective — data is already durable.
func (sess *Session) ExecuteCheckpoint(granularity CheckpointGranularity) error {
	if sess.CheckpointCtrl != nil {
		sess.CheckpointCtrl.SetCheckpointing(true)
		defer sess.CheckpointCtrl.SetCheckpointing(false)
	}
	switch granularity {
	case GranularityLight:
		return sess.checkpointLight()
	case GranularityStandard, GranularityFullMerge:
		return sess.checkpointStandard()
	default:
		return nil
	}
}

// checkpointLight performs a WAL fsync only.
func (sess *Session) checkpointLight() error {
	if sess.WAL == nil {
		return nil
	}
	return sess.WAL.Sync()
}

// checkpointStandard creates a version checkpoint and truncates the WAL.
func (sess *Session) checkpointStandard() error {
	if _, err := sess.Checkpoint("auto_checkpoint", CheckpointPatch); err != nil {
		return fmt.Errorf("session checkpoint: %w", err)
	}
	if err := sess.truncateWALAfterCheckpoint(); err != nil {
		return err
	}
	return sess.resetDeltaTracker()
}

// truncateWALAfterCheckpoint checkpoints the WAL at the current sequence.
func (sess *Session) truncateWALAfterCheckpoint() error {
	if sess.WAL == nil {
		return nil
	}
	return sess.WAL.Checkpoint(sess.WAL.LastSequence())
}

// resetDeltaTracker resets and persists the delta tracker after a checkpoint.
func (sess *Session) resetDeltaTracker() error {
	if sess.DeltaTracker == nil {
		return nil
	}
	sess.DeltaTracker.Reset(0)
	return sess.DeltaTracker.Persist()
}

// Close closes all session resources (DeltaTracker, SharedDataFiles, DocIDMap, Bleve, and WAL).
func (sess *Session) Close() error {
	if sess.DeltaTracker != nil {
		_ = sess.DeltaTracker.Persist()
	}
	if sess.NodeDataFile != nil {
		sess.NodeDataFile.Close()
	}
	if sess.VectorDataFile != nil {
		sess.VectorDataFile.Close()
	}
	if sess.DocDataFile != nil {
		sess.DocDataFile.Close()
	}
	if sess.ChunkDataFile != nil {
		sess.ChunkDataFile.Close()
	}
	if sess.DocIDMap != nil {
		_ = sess.DocIDMap.Save()
	}
	bleveErr := sess.CloseBleve()
	walErr := sess.CloseWAL()
	if bleveErr != nil {
		return bleveErr
	}
	return walErr
}

// Search performs full-text search against the session's HEAD version Bleve.
// Returns nil, nil if no Bleve store is open.
func (sess *Session) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	if sess.BleveStore == nil {
		return nil, nil
	}
	return sess.BleveStore.Search(ctx, req)
}

// RebuildBleve rebuilds the HEAD version's Bleve from JSONL source of truth.
// This is used for recovery when the Bleve database is missing or corrupt.
func (sess *Session) RebuildBleve() error {
	if sess.BleveStore != nil {
		if err := sess.BleveStore.CloseAll(); err != nil {
			return fmt.Errorf("close bleve before rebuild: %w", err)
		}
	}
	sess.BleveStore = NewVersionBleveStore(sess)
	return sess.BleveStore.RebuildHead()
}

// SessionStoreStats contains statistics about session storage.
type SessionStoreStats struct {
	TotalSessions    int
	ActiveSessions   int
	CommittedSessions int
	TotalVersions    int
}

// Stats returns statistics about the session store.
func (s *SessionStore) Stats() (SessionStoreStats, error) {
	stats := SessionStoreStats{}

	sessions, err := s.List()
	if err != nil {
		return stats, err
	}

	stats.TotalSessions = len(sessions)

	for _, stringID := range sessions {
		sess, err := s.Load(stringID)
		if err != nil {
			continue
		}

		if sess.Meta.Status == SessionActive {
			stats.ActiveSessions++
		} else {
			stats.CommittedSessions++
		}

		stats.TotalVersions += sess.VersionCount()
	}

	return stats, nil
}

// isBoltLocked returns true if the bolt database file at path is exclusively
// locked by another process or goroutine. Uses a non-blocking flock probe.
// Returns false if the file doesn't exist or cannot be opened.
func isBoltLocked(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Try non-blocking exclusive lock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// EWOULDBLOCK means another process holds the lock.
		return true
	}
	// We acquired it — release immediately.
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// parseSessionID extracts the numeric ID from a string ID like "ses_001".
func parseSessionID(stringID string) (uint32, error) {
	if !strings.HasPrefix(stringID, "ses_") {
		return 0, fmt.Errorf("invalid session ID format: %s", stringID)
	}
	numStr := strings.TrimPrefix(stringID, "ses_")
	num, err := strconv.ParseUint(numStr, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(num), nil
}
