package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type CredentialAuditAction string

const (
	AuditActionGranted  CredentialAuditAction = "granted"
	AuditActionDenied   CredentialAuditAction = "denied"
	AuditActionResolved CredentialAuditAction = "resolved"
	AuditActionRevoked  CredentialAuditAction = "revoked"
	AuditActionPending  CredentialAuditAction = "pending"
	AuditActionExpired  CredentialAuditAction = "expired"
)

type CredentialAuditEntry struct {
	ID         string
	Timestamp  time.Time
	Action     CredentialAuditAction
	AgentID    string
	AgentType  string
	Provider   string
	ToolCallID string
	HandleID   string
	Reason     string
	PrevHash   string
	EntryHash  string
}

type CredentialAuditStorage interface {
	Append(entry *CredentialAuditEntry) error
	Load() ([]*CredentialAuditEntry, error)
}

type CredentialAuditLog struct {
	mu        sync.RWMutex
	entries   []*CredentialAuditEntry
	hashChain string
	storage   CredentialAuditStorage
	idCounter uint64
}

func NewCredentialAuditLog(storage CredentialAuditStorage) *CredentialAuditLog {
	log := &CredentialAuditLog{
		entries: make([]*CredentialAuditEntry, 0),
		storage: storage,
	}

	if storage != nil {
		log.loadFromStorage()
	}

	return log
}

func (l *CredentialAuditLog) loadFromStorage() {
	entries, err := l.storage.Load()
	if err != nil || len(entries) == 0 {
		return
	}

	l.entries = entries
	if len(entries) > 0 {
		l.hashChain = entries[len(entries)-1].EntryHash
	}
	l.idCounter = uint64(len(entries))
}

func (l *CredentialAuditLog) LogGranted(
	agentID, agentType, provider, toolCallID, handleID, reason string,
) *CredentialAuditEntry {
	return l.logEntry(AuditActionGranted, agentID, agentType, provider, toolCallID, handleID, reason)
}

func (l *CredentialAuditLog) LogDenied(
	agentID, agentType, provider, reason string,
) *CredentialAuditEntry {
	return l.logEntry(AuditActionDenied, agentID, agentType, provider, "", "", reason)
}

func (l *CredentialAuditLog) LogResolved(handleID string) *CredentialAuditEntry {
	return l.logEntry(AuditActionResolved, "", "", "", "", handleID, "")
}

func (l *CredentialAuditLog) LogRevoked(handleID string) *CredentialAuditEntry {
	return l.logEntry(AuditActionRevoked, "", "", "", "", handleID, "")
}

func (l *CredentialAuditLog) LogPending(
	agentID, agentType, provider, reason string,
) *CredentialAuditEntry {
	return l.logEntry(AuditActionPending, agentID, agentType, provider, "", "", reason)
}

func (l *CredentialAuditLog) LogExpired(handleID string) *CredentialAuditEntry {
	return l.logEntry(AuditActionExpired, "", "", "", "", handleID, "")
}

func (l *CredentialAuditLog) logEntry(
	action CredentialAuditAction,
	agentID, agentType, provider, toolCallID, handleID, reason string,
) *CredentialAuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.idCounter++
	entry := &CredentialAuditEntry{
		ID:         generateAuditID(l.idCounter),
		Timestamp:  time.Now(),
		Action:     action,
		AgentID:    agentID,
		AgentType:  agentType,
		Provider:   provider,
		ToolCallID: toolCallID,
		HandleID:   handleID,
		Reason:     reason,
		PrevHash:   l.hashChain,
	}

	entry.EntryHash = computeEntryHash(entry)
	l.hashChain = entry.EntryHash
	l.entries = append(l.entries, entry)

	if l.storage != nil {
		_ = l.storage.Append(entry)
	}

	return entry
}

func generateAuditID(counter uint64) string {
	h := sha256.Sum256([]byte(time.Now().String() + string(rune(counter))))
	return "caud_" + hex.EncodeToString(h[:8])
}

func computeEntryHash(entry *CredentialAuditEntry) string {
	data := entry.ID + entry.Timestamp.Format(time.RFC3339Nano) +
		string(entry.Action) + entry.AgentID + entry.AgentType +
		entry.Provider + entry.ToolCallID + entry.HandleID +
		entry.Reason + entry.PrevHash

	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

var ErrTamperedLog = errors.New("audit log has been tampered with")

func (l *CredentialAuditLog) Verify() error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.verifyChain()
}

func (l *CredentialAuditLog) verifyChain() error {
	prevHash := ""

	for _, entry := range l.entries {
		if entry.PrevHash != prevHash {
			return ErrTamperedLog
		}

		expectedHash := computeEntryHash(entry)
		if entry.EntryHash != expectedHash {
			return ErrTamperedLog
		}

		prevHash = entry.EntryHash
	}

	return nil
}

type AuditQueryFilter struct {
	AgentType string
	Provider  string
	Action    CredentialAuditAction
	From      time.Time
	To        time.Time
	HandleID  string
	AgentID   string
}

func (l *CredentialAuditLog) Query(filter AuditQueryFilter) []*CredentialAuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	results := make([]*CredentialAuditEntry, 0)

	for _, entry := range l.entries {
		if matchesFilter(entry, filter) {
			results = append(results, entry)
		}
	}

	return results
}

func matchesFilter(entry *CredentialAuditEntry, filter AuditQueryFilter) bool {
	if !matchAgentType(entry, filter) {
		return false
	}
	if !matchProvider(entry, filter) {
		return false
	}
	if !matchAction(entry, filter) {
		return false
	}
	return matchTimeAndIDs(entry, filter)
}

func matchAgentType(entry *CredentialAuditEntry, filter AuditQueryFilter) bool {
	return filter.AgentType == "" || entry.AgentType == filter.AgentType
}

func matchProvider(entry *CredentialAuditEntry, filter AuditQueryFilter) bool {
	return filter.Provider == "" || entry.Provider == filter.Provider
}

func matchAction(entry *CredentialAuditEntry, filter AuditQueryFilter) bool {
	return filter.Action == "" || entry.Action == filter.Action
}

func matchTimeAndIDs(entry *CredentialAuditEntry, filter AuditQueryFilter) bool {
	if !filter.From.IsZero() && entry.Timestamp.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && entry.Timestamp.After(filter.To) {
		return false
	}
	if filter.HandleID != "" && entry.HandleID != filter.HandleID {
		return false
	}
	if filter.AgentID != "" && entry.AgentID != filter.AgentID {
		return false
	}
	return true
}

func (l *CredentialAuditLog) Entries() []*CredentialAuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*CredentialAuditEntry, len(l.entries))
	for i, e := range l.entries {
		result[i] = copyEntry(e)
	}
	return result
}

func copyEntry(e *CredentialAuditEntry) *CredentialAuditEntry {
	return &CredentialAuditEntry{
		ID:         e.ID,
		Timestamp:  e.Timestamp,
		Action:     e.Action,
		AgentID:    e.AgentID,
		AgentType:  e.AgentType,
		Provider:   e.Provider,
		ToolCallID: e.ToolCallID,
		HandleID:   e.HandleID,
		Reason:     e.Reason,
		PrevHash:   e.PrevHash,
		EntryHash:  e.EntryHash,
	}
}

func (l *CredentialAuditLog) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

func (l *CredentialAuditLog) LastHash() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.hashChain
}
