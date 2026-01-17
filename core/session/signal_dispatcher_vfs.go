package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/adalundhe/sylk/core/versioning"
	"github.com/gobwas/glob"
)

var (
	ErrNilSignal      = errors.New("signal cannot be nil")
	ErrEmptyFilePath  = errors.New("file path cannot be empty")
	ErrEmptySessionID = errors.New("session ID cannot be empty")
	ErrNoPatterns     = errors.New("at least one pattern is required")
)

// PatternCompileError represents a pattern compilation failure
type PatternCompileError struct {
	Pattern string
	Err     error
}

func (e *PatternCompileError) Error() string {
	return fmt.Sprintf("failed to compile pattern %q: %v", e.Pattern, e.Err)
}

func (e *PatternCompileError) Unwrap() error {
	return e.Err
}

// File change signal types
const (
	SignalFileCreated   SignalType = "file_created"
	SignalFileModified  SignalType = "file_modified"
	SignalFileDeleted   SignalType = "file_deleted"
	SignalFileLocked    SignalType = "file_locked"
	SignalFileUnlocked  SignalType = "file_unlocked"
	SignalMergeConflict SignalType = "merge_conflict"
)

// FileChangeType identifies the type of file change
type FileChangeType string

const (
	FileChangeCreated  FileChangeType = "created"
	FileChangeModified FileChangeType = "modified"
	FileChangeDeleted  FileChangeType = "deleted"
)

// FileChangeSignal carries file change information across sessions
type FileChangeSignal struct {
	CrossSessionSignal
	FilePath   string               `json:"file_path"`
	OldVersion versioning.VersionID `json:"old_version,omitempty"`
	NewVersion versioning.VersionID `json:"new_version,omitempty"`
	ChangeType FileChangeType       `json:"change_type"`
	ChangedBy  string               `json:"changed_by"`
	PipelineID string               `json:"pipeline_id,omitempty"`
}

// FileSubscription tracks a session's interest in file changes
type FileSubscription struct {
	SessionID string
	Patterns  []string
	globs     []glob.Glob
}

// SignalDispatcherVFSConfig holds configuration for SignalDispatcherVFS
type SignalDispatcherVFSConfig struct {
	BaseDispatcher *CrossSessionSignalDispatcher
}

// SignalDispatcherVFS extends CrossSessionSignalDispatcher with file change handling
type SignalDispatcherVFS struct {
	base          *CrossSessionSignalDispatcher
	subscriptions map[string]*FileSubscription
	mu            sync.RWMutex
}

// NewSignalDispatcherVFS creates a new VFS-aware signal dispatcher
func NewSignalDispatcherVFS(cfg SignalDispatcherVFSConfig) *SignalDispatcherVFS {
	return &SignalDispatcherVFS{
		base:          cfg.BaseDispatcher,
		subscriptions: make(map[string]*FileSubscription),
	}
}

// BroadcastFileChange sends file change to all interested sessions
func (d *SignalDispatcherVFS) BroadcastFileChange(signal *FileChangeSignal) error {
	if err := d.validateSignal(signal); err != nil {
		return err
	}

	d.populateSignalDefaults(signal)

	interestedSessions := d.findInterestedSessions(signal.FilePath)
	return d.sendToSessions(signal, interestedSessions)
}

func (d *SignalDispatcherVFS) validateSignal(signal *FileChangeSignal) error {
	if signal == nil {
		return ErrNilSignal
	}
	if signal.FilePath == "" {
		return ErrEmptyFilePath
	}
	return nil
}

func (d *SignalDispatcherVFS) populateSignalDefaults(signal *FileChangeSignal) {
	signal.FromSession = d.base.SessionID()
	signal.Type = d.signalTypeFromChangeType(signal.ChangeType)
}

func (d *SignalDispatcherVFS) signalTypeFromChangeType(ct FileChangeType) SignalType {
	switch ct {
	case FileChangeCreated:
		return SignalFileCreated
	case FileChangeModified:
		return SignalFileModified
	case FileChangeDeleted:
		return SignalFileDeleted
	default:
		return SignalFileModified
	}
}

func (d *SignalDispatcherVFS) findInterestedSessions(filePath string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var sessions []string
	for sessionID, sub := range d.subscriptions {
		if d.matchesPatterns(filePath, sub.globs) {
			sessions = append(sessions, sessionID)
		}
	}
	return sessions
}

func (d *SignalDispatcherVFS) matchesPatterns(filePath string, globs []glob.Glob) bool {
	for _, g := range globs {
		if g.Match(filePath) || g.Match(filepath.Base(filePath)) {
			return true
		}
	}
	return false
}

func (d *SignalDispatcherVFS) sendToSessions(signal *FileChangeSignal, sessions []string) error {
	if len(sessions) == 0 {
		return nil
	}

	for _, sessionID := range sessions {
		if err := d.sendToSession(signal, sessionID); err != nil {
			return err
		}
	}
	return nil
}

func (d *SignalDispatcherVFS) sendToSession(signal *FileChangeSignal, sessionID string) error {
	baseSignal := d.toBaseSignal(signal, sessionID)
	return d.base.SendSignal(baseSignal)
}

func (d *SignalDispatcherVFS) toBaseSignal(signal *FileChangeSignal, targetSession string) CrossSessionSignal {
	payload, _ := json.Marshal(signal)
	return CrossSessionSignal{
		Type:        signal.Type,
		FromSession: signal.FromSession,
		ToSession:   targetSession,
		Timestamp:   signal.Timestamp,
		Payload:     string(payload),
	}
}

// SubscribeFileChanges registers for file change notifications
func (d *SignalDispatcherVFS) SubscribeFileChanges(sessionID string, patterns []string) error {
	if sessionID == "" {
		return ErrEmptySessionID
	}
	if len(patterns) == 0 {
		return ErrNoPatterns
	}

	globs, err := d.compilePatterns(patterns)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.subscriptions[sessionID] = &FileSubscription{
		SessionID: sessionID,
		Patterns:  patterns,
		globs:     globs,
	}
	return nil
}

func (d *SignalDispatcherVFS) compilePatterns(patterns []string) ([]glob.Glob, error) {
	globs := make([]glob.Glob, 0, len(patterns))
	for _, pattern := range patterns {
		g, err := glob.Compile(pattern)
		if err != nil {
			return nil, &PatternCompileError{Pattern: pattern, Err: err}
		}
		globs = append(globs, g)
	}
	return globs, nil
}

// UnsubscribeFileChanges removes file change subscription
func (d *SignalDispatcherVFS) UnsubscribeFileChanges(sessionID string) error {
	if sessionID == "" {
		return ErrEmptySessionID
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.subscriptions, sessionID)
	return nil
}

// GetSubscription returns the subscription for a session
func (d *SignalDispatcherVFS) GetSubscription(sessionID string) (*FileSubscription, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	sub, ok := d.subscriptions[sessionID]
	return sub, ok
}

// GetSubscribedSessions returns all session IDs with subscriptions
func (d *SignalDispatcherVFS) GetSubscribedSessions() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	sessions := make([]string, 0, len(d.subscriptions))
	for sessionID := range d.subscriptions {
		sessions = append(sessions, sessionID)
	}
	return sessions
}

// BroadcastFileLocked sends file lock signal
func (d *SignalDispatcherVFS) BroadcastFileLocked(filePath, lockedBy string) error {
	signal := &FileChangeSignal{
		FilePath:  filePath,
		ChangedBy: lockedBy,
	}
	signal.Type = SignalFileLocked
	return d.broadcastLockSignal(signal)
}

// BroadcastFileUnlocked sends file unlock signal
func (d *SignalDispatcherVFS) BroadcastFileUnlocked(filePath, unlockedBy string) error {
	signal := &FileChangeSignal{
		FilePath:  filePath,
		ChangedBy: unlockedBy,
	}
	signal.Type = SignalFileUnlocked
	return d.broadcastLockSignal(signal)
}

func (d *SignalDispatcherVFS) broadcastLockSignal(signal *FileChangeSignal) error {
	if err := d.validateSignal(signal); err != nil {
		return err
	}

	signal.FromSession = d.base.SessionID()
	interestedSessions := d.findInterestedSessions(signal.FilePath)
	return d.sendToSessions(signal, interestedSessions)
}

// BroadcastMergeConflict sends merge conflict signal
func (d *SignalDispatcherVFS) BroadcastMergeConflict(
	filePath string,
	baseVersion, ourVersion, theirVersion versioning.VersionID,
	conflictingSession string,
) error {
	signal := &FileChangeSignal{
		FilePath:   filePath,
		OldVersion: baseVersion,
		NewVersion: ourVersion,
		ChangedBy:  conflictingSession,
	}
	signal.Type = SignalMergeConflict
	signal.FromSession = d.base.SessionID()

	interestedSessions := d.findInterestedSessions(filePath)
	return d.sendToSessions(signal, interestedSessions)
}

// SessionID returns the session ID from the base dispatcher
func (d *SignalDispatcherVFS) SessionID() string {
	return d.base.SessionID()
}

// Base returns the underlying signal dispatcher
func (d *SignalDispatcherVFS) Base() *CrossSessionSignalDispatcher {
	return d.base
}
