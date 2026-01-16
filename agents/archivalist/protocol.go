package archivalist

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol message types
type MessageType string

const (
	MsgTypeRegister  MessageType = "register"
	MsgTypeWrite     MessageType = "write"
	MsgTypeBatch     MessageType = "batch"
	MsgTypeRead      MessageType = "read"
	MsgTypeBriefing  MessageType = "briefing"
	MsgTypeHeartbeat MessageType = "heartbeat"
)

// Response status codes (compact)
type StatusCode string

const (
	StatusOK       StatusCode = "OK"
	StatusMerged   StatusCode = "MERGED"
	StatusConflict StatusCode = "CONFLICT"
	StatusError    StatusCode = "ERROR"
	StatusPartial  StatusCode = "PARTIAL"
)

// BriefingTier controls response verbosity
type BriefingTier string

const (
	BriefingMicro    BriefingTier = "micro"    // ~20 tokens
	BriefingStandard BriefingTier = "standard" // ~500 tokens
	BriefingFull     BriefingTier = "full"     // ~2000 tokens
)

// Scope represents a category path for scoped operations
type Scope string

const (
	ScopeFiles    Scope = "files"
	ScopePatterns Scope = "patterns"
	ScopeFailures Scope = "failures"
	ScopeIntents  Scope = "intents"
	ScopeResume   Scope = "resume"
	ScopeAll      Scope = "all"
)

// ParseScope parses a scoped path like "patterns.auth_state"
func ParseScope(s string) (category Scope, key string) {
	parts := strings.SplitN(s, ".", 2)
	category = Scope(parts[0])
	if len(parts) > 1 {
		key = parts[1]
	}
	return
}

// Request is the compact request format from agents
type Request struct {
	// Message type (inferred from which fields are set)
	Register *RegisterRequest `json:"register,omitempty"`
	Write    *WriteRequest    `json:"write,omitempty"`
	Batch    *BatchRequest    `json:"batch,omitempty"`
	Read     *ReadRequest     `json:"read,omitempty"`
	Briefing *BriefingRequest `json:"briefing,omitempty"`

	// Common fields
	AgentID string `json:"id,omitempty"` // Short agent ID
	Version string `json:"v,omitempty"`  // Version vector
	Verbose bool   `json:"verbose,omitempty"`
}

// RegisterRequest registers a new agent
type RegisterRequest struct {
	Name     string `json:"name"`               // Agent name (e.g., "opus_main")
	ParentID string `json:"parent,omitempty"`   // Parent agent ID for sub-agents
	Session  string `json:"session,omitempty"`  // Session ID (auto-generated if empty)
}

// WriteRequest is a single scoped write operation
type WriteRequest struct {
	Scope           string         `json:"s"`                 // Scoped path (e.g., "patterns.auth")
	Data            map[string]any `json:"d"`                 // Write data
	ExpectNoConflict bool          `json:"optimistic,omitempty"` // Skip conflict check
}

// BatchRequest combines multiple writes
type BatchRequest struct {
	Writes []WriteRequest `json:"w"`
}

// ReadRequest for delta or full reads
type ReadRequest struct {
	Scope string `json:"s,omitempty"`     // Scope to read (default: all)
	Since string `json:"since,omitempty"` // Version for delta (omit for full)
	Limit int    `json:"limit,omitempty"` // Max entries
}

// BriefingRequest for agent handoffs
type BriefingRequest struct {
	Tier     BriefingTier `json:"tier,omitempty"` // micro, standard, full
	AgentID  string       `json:"for,omitempty"`  // Get briefing for specific agent
}

// Response is the compact response format
type Response struct {
	Status  StatusCode `json:"s"`
	Version string     `json:"v,omitempty"`

	// For registration
	AgentID string `json:"id,omitempty"`

	// For conflicts
	ConflictType   string `json:"ct,omitempty"`   // Type of conflict
	ConflictDetail string `json:"cd,omitempty"`   // Brief explanation

	// For batch operations
	Succeeded int `json:"ok,omitempty"`    // Number succeeded
	Failed    int `json:"fail,omitempty"`  // Number failed

	// For reads (only if verbose or needed)
	Data  any    `json:"d,omitempty"`
	Delta []DeltaEntry `json:"delta,omitempty"`

	// For briefings
	Briefing string `json:"b,omitempty"` // Compact briefing text

	// Error message (only on error)
	Error string `json:"e,omitempty"`
}

// DeltaEntry represents a single change in delta response
type DeltaEntry struct {
	Version   string         `json:"v"`
	Type      string         `json:"t"`  // add, update, delete
	Scope     string         `json:"s"`
	Key       string         `json:"k,omitempty"`
	Data      map[string]any `json:"d,omitempty"`
	AgentID   string         `json:"a,omitempty"`
}

// Compact response builders

// OKResponse creates a simple OK response
func OKResponse(version string) Response {
	return Response{Status: StatusOK, Version: version}
}

// MergedResponse creates a merged response with optional detail
func MergedResponse(version, detail string) Response {
	return Response{
		Status:         StatusMerged,
		Version:        version,
		ConflictDetail: detail,
	}
}

// ConflictResponse creates a conflict response
func ConflictResponse(version, conflictType, detail string) Response {
	return Response{
		Status:         StatusConflict,
		Version:        version,
		ConflictType:   conflictType,
		ConflictDetail: detail,
	}
}

// ErrorResponse creates an error response
func ErrorResponse(err string) Response {
	return Response{Status: StatusError, Error: err}
}

// PartialResponse for batch operations with some failures
func PartialResponse(version string, succeeded, failed int) Response {
	return Response{
		Status:    StatusPartial,
		Version:   version,
		Succeeded: succeeded,
		Failed:    failed,
	}
}

// BatchOKResponse for successful batch operations
func BatchOKResponse(version string, count int) Response {
	return Response{
		Status:    StatusOK,
		Version:   version,
		Succeeded: count,
	}
}

// ToCompactString converts response to minimal string format
func (r Response) ToCompactString() string {
	switch r.Status {
	case StatusOK:
		if r.Succeeded > 0 {
			return fmt.Sprintf("OK %s %d/%d", r.Version, r.Succeeded, r.Succeeded)
		}
		return fmt.Sprintf("OK %s", r.Version)
	case StatusMerged:
		if r.ConflictDetail != "" {
			return fmt.Sprintf("MERGED %s %s", r.Version, r.ConflictDetail)
		}
		return fmt.Sprintf("MERGED %s", r.Version)
	case StatusConflict:
		return fmt.Sprintf("CONFLICT %s %s:%s", r.Version, r.ConflictType, r.ConflictDetail)
	case StatusPartial:
		return fmt.Sprintf("PARTIAL %s %d/%d", r.Version, r.Succeeded, r.Succeeded+r.Failed)
	case StatusError:
		return fmt.Sprintf("ERROR %s", r.Error)
	default:
		return string(r.Status)
	}
}

// ParseCompactResponse parses minimal string format back to Response
func ParseCompactResponse(s string) (Response, error) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return Response{}, fmt.Errorf("empty response")
	}

	r := Response{Status: StatusCode(parts[0])}
	parseResponseByStatus(&r, parts)
	return r, nil
}

func parseResponseByStatus(r *Response, parts []string) {
	parsers := map[StatusCode]func(*Response, []string){
		StatusOK:       parseOKResponse,
		StatusMerged:   parseOKResponse,
		StatusConflict: parseConflictResponse,
		StatusPartial:  parsePartialResponse,
		StatusError:    parseErrorResponse,
	}
	if parser, ok := parsers[r.Status]; ok {
		parser(r, parts)
	}
}

func parseOKResponse(r *Response, parts []string) {
	if len(parts) > 1 {
		r.Version = parts[1]
	}
	if len(parts) > 2 {
		r.ConflictDetail = strings.Join(parts[2:], " ")
	}
}

func parseConflictResponse(r *Response, parts []string) {
	if len(parts) > 1 {
		r.Version = parts[1]
	}
	if len(parts) > 2 {
		conflictParts := strings.SplitN(parts[2], ":", 2)
		r.ConflictType = conflictParts[0]
		if len(conflictParts) > 1 {
			r.ConflictDetail = conflictParts[1]
		}
	}
}

func parsePartialResponse(r *Response, parts []string) {
	if len(parts) > 1 {
		r.Version = parts[1]
	}
	if len(parts) > 2 {
		fmt.Sscanf(parts[2], "%d/%d", &r.Succeeded, &r.Failed)
		r.Failed = r.Failed - r.Succeeded
	}
}

func parseErrorResponse(r *Response, parts []string) {
	if len(parts) > 1 {
		r.Error = strings.Join(parts[1:], " ")
	}
}

// MicroBriefing generates a ~20 token briefing
func MicroBriefing(task string, step, total int, modifiedFiles []string, blocker string) string {
	files := "none"
	if len(modifiedFiles) > 0 {
		shortFiles := make([]string, len(modifiedFiles))
		for i, f := range modifiedFiles {
			// Extract just filename
			parts := strings.Split(f, "/")
			shortFiles[i] = parts[len(parts)-1] + "(m)"
		}
		files = strings.Join(shortFiles, ",")
	}

	block := "none"
	if blocker != "" {
		block = blocker
	}

	return fmt.Sprintf("%s:%d/%d:%s:block=%s", task, step, total, files, block)
}

// ParseMicroBriefing parses micro briefing back to components
func ParseMicroBriefing(s string) (task string, step, total int, files []string, blocker string) {
	parts := strings.Split(s, ":")
	if len(parts) >= 1 {
		task = parts[0]
	}
	if len(parts) >= 2 {
		fmt.Sscanf(parts[1], "%d/%d", &step, &total)
	}
	if len(parts) >= 3 && parts[2] != "none" {
		fileStrs := strings.Split(parts[2], ",")
		for _, f := range fileStrs {
			files = append(files, strings.TrimSuffix(f, "(m)"))
		}
	}
	if len(parts) >= 4 {
		blocker = strings.TrimPrefix(parts[3], "block=")
		if blocker == "none" {
			blocker = ""
		}
	}
	return
}

// JSON helpers for when compact isn't enough

// ToJSON converts response to JSON bytes
func (r Response) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// ParseRequest parses a request from JSON
func ParseRequest(data []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	return &req, nil
}

// GetMessageType determines the type of request
func (r *Request) GetMessageType() MessageType {
	switch {
	case r.Register != nil:
		return MsgTypeRegister
	case r.Write != nil:
		return MsgTypeWrite
	case r.Batch != nil:
		return MsgTypeBatch
	case r.Read != nil:
		return MsgTypeRead
	case r.Briefing != nil:
		return MsgTypeBriefing
	default:
		return MsgTypeHeartbeat
	}
}
