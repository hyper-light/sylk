package security

import (
	"time"
)

type AuditCategory string

const (
	AuditCategoryPermission AuditCategory = "permission"
	AuditCategoryFile       AuditCategory = "file"
	AuditCategoryProcess    AuditCategory = "process"
	AuditCategoryNetwork    AuditCategory = "network"
	AuditCategoryLLM        AuditCategory = "llm"
	AuditCategorySession    AuditCategory = "session"
	AuditCategoryConfig     AuditCategory = "config"
	AuditCategorySecret     AuditCategory = "secret"
)

type AuditSeverity string

const (
	AuditSeverityInfo     AuditSeverity = "info"
	AuditSeverityWarning  AuditSeverity = "warning"
	AuditSeveritySecurity AuditSeverity = "security"
	AuditSeverityCritical AuditSeverity = "critical"
)

type AuditEntry struct {
	ID        string        `json:"id"`
	Timestamp time.Time     `json:"timestamp"`
	Sequence  uint64        `json:"sequence"`
	Category  AuditCategory `json:"category"`
	EventType string        `json:"event_type"`
	Severity  AuditSeverity `json:"severity"`

	SessionID  string `json:"session_id"`
	ProjectID  string `json:"project_id"`
	AgentID    string `json:"agent_id,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`

	Action  string                 `json:"action"`
	Target  string                 `json:"target,omitempty"`
	Outcome string                 `json:"outcome"`
	Details map[string]interface{} `json:"details,omitempty"`

	PreviousHash string `json:"previous_hash"`
	EntryHash    string `json:"entry_hash"`
}

type AuditSignature struct {
	Timestamp    time.Time `json:"timestamp"`
	SequenceFrom uint64    `json:"sequence_from"`
	SequenceTo   uint64    `json:"sequence_to"`
	ChainHash    string    `json:"chain_hash"`
	Signature    []byte    `json:"signature"`
}

type IntegrityReport struct {
	StartTime          time.Time
	EndTime            time.Time
	EntriesVerified    int
	SignaturesVerified int
	Errors             []string
	Valid              bool
}

func NewAuditEntry(category AuditCategory, eventType, action string) AuditEntry {
	return AuditEntry{
		Category:  category,
		EventType: eventType,
		Action:    action,
		Severity:  AuditSeverityInfo,
	}
}

func (e *AuditEntry) WithSeverity(s AuditSeverity) *AuditEntry {
	e.Severity = s
	return e
}

func (e *AuditEntry) WithTarget(t string) *AuditEntry {
	e.Target = t
	return e
}

func (e *AuditEntry) WithOutcome(o string) *AuditEntry {
	e.Outcome = o
	return e
}

func (e *AuditEntry) WithSessionID(id string) *AuditEntry {
	e.SessionID = id
	return e
}

func (e *AuditEntry) WithAgentID(id string) *AuditEntry {
	e.AgentID = id
	return e
}

func (e *AuditEntry) WithDetails(d map[string]interface{}) *AuditEntry {
	e.Details = d
	return e
}
