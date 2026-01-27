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

// Version represents a checkpoint in session history.
type Version struct {
	ID        uint32       `json:"id"`
	ParentID  uint32       `json:"parent_id"`
	Name      string       `json:"name,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	Trigger   string       `json:"trigger"` // "explicit", "auto_delta", "implicit"
	Stats     VersionStats `json:"stats"`
}

// VersionManifest tracks the version DAG for a session.
type VersionManifest struct {
	SessionID   uint32    `json:"session_id"`
	Head        uint32    `json:"head"`
	NextVersion uint32    `json:"next_version"`
	Versions    []Version `json:"versions"`
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

	// Create version manifest with initial version
	manifest := &VersionManifest{
		SessionID:   sessionID,
		Head:        1,
		NextVersion: 2,
		Versions: []Version{
			{
				ID:        1,
				ParentID:  0,
				Name:      "session_start",
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
	if err := s.createVersionDir(sessionPath, 1); err != nil {
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

// createVersionDir creates a version directory with all subdirectories.
func (s *SessionStore) createVersionDir(sessionPath string, versionID uint32) error {
	versionPath := filepath.Join(sessionPath, "versions", fmt.Sprintf("v%06d", versionID))

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
		"id":         versionID,
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
func (sess *Session) VersionPath(versionID uint32) string {
	return filepath.Join(sess.path, "versions", fmt.Sprintf("v%06d", versionID))
}

// HeadVersionPath returns the path to the HEAD version directory.
func (sess *Session) HeadVersionPath() string {
	return sess.VersionPath(sess.Manifest.Head)
}

// DocsPath returns the path to the docs directory for a version.
func (sess *Session) DocsPath(versionID uint32) string {
	return filepath.Join(sess.VersionPath(versionID), "docs")
}

// Checkpoint creates a new version checkpoint.
func (sess *Session) Checkpoint(name string, trigger string) (uint32, error) {
	newID := sess.Manifest.NextVersion
	sess.Manifest.NextVersion++

	v := Version{
		ID:        newID,
		ParentID:  sess.Manifest.Head,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger:   trigger,
		Stats:     VersionStats{}, // TODO: populate from delta tracker
	}

	// Create version directory
	if err := sess.store.createVersionDir(sess.path, newID); err != nil {
		return 0, err
	}

	sess.Manifest.Versions = append(sess.Manifest.Versions, v)
	sess.Manifest.Head = newID

	// Persist manifest
	if err := sess.store.writeManifest(sess.path, sess.Manifest); err != nil {
		return 0, err
	}

	return newID, nil
}

// Checkout switches HEAD to a different version.
func (sess *Session) Checkout(versionID uint32) error {
	// Verify version exists
	found := false
	for _, v := range sess.Manifest.Versions {
		if v.ID == versionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("version %d not found", versionID)
	}

	sess.Manifest.Head = versionID
	return sess.store.writeManifest(sess.path, sess.Manifest)
}

// GetAncestorChain returns the ancestor chain from HEAD to root.
func (sess *Session) GetAncestorChain() []uint32 {
	versionMap := make(map[uint32]*Version)
	for i := range sess.Manifest.Versions {
		versionMap[sess.Manifest.Versions[i].ID] = &sess.Manifest.Versions[i]
	}

	var chain []uint32
	current := sess.Manifest.Head

	for current != 0 {
		chain = append(chain, current)
		if v, ok := versionMap[current]; ok {
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
