// Package sylkdir provides session storage with per-version data directories.
package sylkdir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	CommittedSessions []uint32  `json:"committed_sessions"`
	SnapshotAt        time.Time `json:"snapshot_at"`
	NextNodeID        uint32    `json:"next_node_id"`
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

	// Initialize delta tracker
	if err := s.initDeltaTracker(sessionPath); err != nil {
		return nil, fmt.Errorf("init delta tracker: %w", err)
	}

	return &Session{
		store:        s,
		path:         sessionPath,
		Meta:         meta,
		BaseSnapshot: baseSnapshot,
		Manifest:     manifest,
	}, nil
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

	return &Session{
		store:        s,
		path:         sessionPath,
		Meta:         meta,
		BaseSnapshot: baseSnapshot,
		Manifest:     manifest,
	}, nil
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
	return os.WriteFile(filepath.Join(versionPath, "deletions.json"), data, 0644)
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

func (s *SessionStore) initDeltaTracker(sessionPath string) error {
	tracker := map[string]interface{}{
		"nodes_created":   0,
		"edges_created":   0,
		"edges_modified":  0,
		"vectors_created": 0,
		"docs_bytes":      0,
		"last_checkpoint": time.Now().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(tracker, "", "  ")
	return os.WriteFile(filepath.Join(sessionPath, "delta", "tracker.json"), data, 0644)
}

// Session represents an active session.
type Session struct {
	store        *SessionStore
	path         string
	Meta         *SessionMeta
	BaseSnapshot *BaseSnapshot
	Manifest     *VersionManifest
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
func (sess *Session) Checkpoint(name string, checkpointType CheckpointType) (SemanticVersion, error) {
	var newVersion SemanticVersion
	switch checkpointType {
	case CheckpointMajor:
		newVersion = sess.Manifest.Head.BumpMajor()
	case CheckpointMinor:
		newVersion = sess.Manifest.Head.BumpMinor()
	case CheckpointPatch:
		newVersion = sess.Manifest.Head.BumpPatch()
	default:
		newVersion = sess.Manifest.Head.BumpPatch()
	}

	v := Version{
		ID:        newVersion,
		ParentID:  sess.Manifest.Head,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger:   string(checkpointType),
		Stats:     VersionStats{},
	}

	// Create version directory
	if err := sess.store.createVersionDirSemver(sess.path, newVersion); err != nil {
		return SemanticVersion{}, err
	}

	sess.Manifest.Versions = append(sess.Manifest.Versions, v)
	sess.Manifest.Head = newVersion

	// Persist manifest
	if err := sess.store.writeManifest(sess.path, sess.Manifest); err != nil {
		return SemanticVersion{}, err
	}

	return newVersion, nil
}

// Checkout switches HEAD to a different version.
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

	sess.Manifest.Head = version
	return sess.store.writeManifest(sess.path, sess.Manifest)
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
