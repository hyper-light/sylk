// Package durable implements the append-only operation journal MCP runtime
// uses to maintain authoritative truth across crashes, restarts, and
// network partitions.
//
// The contract: every meaningful MCP transition appends to the WAL BEFORE
// the live transport completes. The journal is the source of truth; the
// transport is for timeliness.
//
// Layout (mirrors design doc §"Storage Layout"):
//
//	.sylk/sessions/<session_id>/mcp/
//	    ops/<op_id>/
//	        wal/events-NNNNNN.wal     (one record per event, JSONL)
//	        projection.snapshot.json   (latest reduced state)
//	    servers/<server_id>/
//	        capability.snapshot.json
//	    blobs/<sha256>                  (out-of-band binary content)
//
// The journal is per-operation so a single server with many in-flight
// operations doesn't serialize on a shared WAL fd. A single per-session
// index file (events.idx) maintains a sequence cursor for recovery.
package durable

import (
	"time"
)

// EventKind enumerates the durable MCP transitions. Matching agentlog's
// EventType values lets the global session log reuse the same vocabulary.
type EventKind string

const (
	EventOpRequested          EventKind = "op_requested"
	EventApprovalRequested    EventKind = "approval_requested"
	EventApprovalResolved     EventKind = "approval_resolved"
	EventElicitationRequested EventKind = "elicitation_requested"
	EventElicitationResolved  EventKind = "elicitation_resolved"
	EventOpProgress           EventKind = "op_progress"
	EventBlobStored           EventKind = "blob_stored"
	EventContentIndexed       EventKind = "content_indexed"
	EventOpCompleted          EventKind = "op_completed"
	EventOpFailed             EventKind = "op_failed"
	EventOpCancelled          EventKind = "op_cancelled"
	EventCatalogInvalidated   EventKind = "catalog_invalidated"
	EventCapabilitiesSynced   EventKind = "capabilities_synced"
	EventTransportConnected    EventKind = "transport_connected"
	EventTransportDisconnected EventKind = "transport_disconnected"
	EventServerRegistered     EventKind = "server_registered"
)

// OpStatus is the projection-side view of an operation's lifecycle.
type OpStatus string

const (
	StatusRequested          OpStatus = "requested"
	StatusApprovalPending    OpStatus = "approval_pending"
	StatusApprovalDenied     OpStatus = "approval_denied"
	StatusRunning            OpStatus = "running"
	StatusElicitationPending OpStatus = "elicitation_pending"
	StatusCompleted          OpStatus = "completed"
	StatusFailed             OpStatus = "failed"
	StatusCancelled          OpStatus = "cancelled"
)

// Event is one record in an operation's WAL.
type Event struct {
	Sequence       uint64    `json:"sequence"`
	Timestamp      time.Time `json:"timestamp"`
	Kind           EventKind `json:"kind"`
	OperationID    string    `json:"operation_id"`
	SessionID      string    `json:"session_id"`
	CorrelationID  string    `json:"correlation_id"`
	AgentID        string    `json:"agent_id"`
	ServerID       string    `json:"server_id"`
	CapabilityKind string    `json:"capability_kind"`
	LocalName      string    `json:"local_name,omitempty"`
	RemoteName     string    `json:"remote_name,omitempty"`
	ArgumentsHash  string    `json:"arguments_hash,omitempty"`
	PolicyHash     string    `json:"policy_hash,omitempty"`
	BlobRefs       []string  `json:"blob_refs,omitempty"`
	ResultSummary  string    `json:"result_summary,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	Payload        []byte    `json:"payload,omitempty"`
}

// Operation is the projection-side aggregate built by reducing the WAL.
type Operation struct {
	OperationID       string    `json:"operation_id"`
	SessionID         string    `json:"session_id"`
	CorrelationID     string    `json:"correlation_id"`
	AgentID           string    `json:"agent_id"`
	ServerID          string    `json:"server_id"`
	CapabilityScope   string    `json:"capability_scope"`
	CapabilityKind    string    `json:"capability_kind"`
	LocalName         string    `json:"local_name"`
	RemoteName        string    `json:"remote_name"`
	ArgumentsHash     string    `json:"arguments_hash"`
	PolicyFingerprint string    `json:"policy_fingerprint"`
	Status            OpStatus  `json:"status"`
	BlobRefs          []string  `json:"blob_refs,omitempty"`
	ResultSummary     string    `json:"result_summary,omitempty"`
	Error             string    `json:"error,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}
