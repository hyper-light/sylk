package shared

import (
	"encoding/json"
	"strings"
)

// ConsultResponsePayload is the canonical typed response shape returned by
// any agent that answers an inter-agent consult. Consumers (chat UI, peer
// protocol validators, archivalist loggers) read fields directly — no
// JSON-decoding, no guessing at which key a particular agent happens to
// use this week.
//
// Why this exists: the pre-typed-payload era had every consult response
// riding in RouteResponse.Data as either a free-form string that happened
// to be JSON or a map[string]any with agent-specific keys. The chat UI
// rendered whatever it pulled off "summary"; agents emitting different
// shapes surfaced as raw JSON in the user's chat panel (Bug 3 from the
// 2026-04-19 triage). Typed payloads eliminate the parsing gap at the
// source: producers fill the struct, consumers read fields.
type ConsultResponsePayload struct {
	// Summary is the one-line phrase the chat panel renders in the
	// inter-agent row. Required — a missing Summary surfaces as "agent
	// returned empty consult response" in the UI.
	Summary string `json:"summary"`

	// Details carries the long-form answer (markdown). Optional.
	// Consumers that want the full response text (archivalist, protocol
	// logs) read this; the chat row uses Summary only.
	Details string `json:"details,omitempty"`

	// Decision is populated for boolean / enum consults (check intent,
	// go/no-go calls). Optional; empty for open-ended consults.
	Decision string `json:"decision,omitempty"`

	// Citations carries source references the consulted agent used.
	// Optional.
	Citations []ConsultCitation `json:"citations,omitempty"`

	// Metadata holds agent-specific extras (retrieval scores, tool-call
	// traces, etc.) that don't fit the core fields. Consumers generally
	// ignore this except for telemetry.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ConsultCitation names a single source the consulted agent referenced.
type ConsultCitation struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Note  string `json:"note,omitempty"`
}

// ChallengeResponsePayload is the canonical typed response for an agent
// answering a peer challenge (validate_work, process_validation, global
// review challenge responses). Every challenge response carries the
// challenge id it resolves and a typed Status that the protocol validator
// can evaluate without string-matching.
type ChallengeResponsePayload struct {
	ChallengeID     string             `json:"challenge_id"`
	RequestingAgent string             `json:"requesting_agent,omitempty"`
	RespondingAgent string             `json:"responding_agent,omitempty"`
	Status          ChallengeStatus    `json:"status"`
	Summary         string             `json:"summary"`
	Findings        []ChallengeFinding `json:"findings,omitempty"`
	EvidenceRefs    []string           `json:"evidence_refs,omitempty"`
}

// ChallengeStatus enumerates the legal terminal states of a challenge
// response. The protocol validator switches on these values; string-free
// comparison eliminates the "status happened to be spelled differently"
// class of bug that plagued the prose-based protocol.
type ChallengeStatus string

const (
	ChallengeStatusPassed       ChallengeStatus = "passed"
	ChallengeStatusFailed       ChallengeStatus = "failed"
	ChallengeStatusNeedsChanges ChallengeStatus = "needs_changes"
	ChallengeStatusInconclusive ChallengeStatus = "inconclusive"
)

// ChallengeFinding is a single dated finding a responding agent produces
// as part of answering a challenge. The chat UI groups findings by
// severity; the protocol store persists them.
type ChallengeFinding struct {
	Severity string `json:"severity,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// StoreResponsePayload is the canonical typed response for archival store
// operations (archivalist). Every store emission confirms what was
// persisted so the caller can log or link to it.
type StoreResponsePayload struct {
	StoredID string `json:"stored_id,omitempty"`
	Summary  string `json:"summary"`
}

// InterAgentResponsePayload is the interface any typed inter-agent
// response value implements. RouteResponse.Data values matching this
// interface are rendered/validated using their declared Summary
// field; untyped values fall back to defensive parsing (scheduled for
// deletion once every producer has migrated).
//
// The interface is intentionally open — any package can satisfy it by
// declaring `InterAgentSummary() string`. An earlier design used an
// unexported sealed marker, which would have forced every participating
// type (archivalist SubmissionResult, pipeline PipelineValidationResult,
// per-agent result types) to live in the shared package or be wrapped.
// Both added ceremony without meaningful safety: accidental widening is
// caught by the summarizer's InterAgentPayloadSummary short-circuit
// test matrix.
type InterAgentResponsePayload interface {
	// InterAgentSummary returns the one-line summary for UI rendering.
	// Required; the chat panel reads this field and never inspects the
	// underlying struct.
	InterAgentSummary() string
}

// InterAgentSummary returns the summary field. Implements InterAgentResponsePayload.
func (p *ConsultResponsePayload) InterAgentSummary() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Summary)
}

// InterAgentSummary returns the challenge summary. Implements InterAgentResponsePayload.
func (p *ChallengeResponsePayload) InterAgentSummary() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Summary)
}

// InterAgentSummary returns the store summary. Implements InterAgentResponsePayload.
func (p *StoreResponsePayload) InterAgentSummary() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Summary)
}

// DecodeConsultResponsePayloadFromLLM parses an LLM-produced JSON string
// into a ConsultResponsePayload. Producers that ask the LLM for structured
// JSON output call this at the return boundary so the wire payload is
// typed from that point forward.
//
// Falls back to a "summary-only" payload when the LLM emitted free-form
// prose rather than JSON — in that case Summary is the whole trimmed text
// (truncated for sanity) so the row still renders something meaningful.
// This preserves backward compatibility with agents whose prompts don't
// yet enforce JSON output; those migrations happen agent-by-agent.
func DecodeConsultResponsePayloadFromLLM(raw string) *ConsultResponsePayload {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return &ConsultResponsePayload{Summary: ""}
	}
	var payload ConsultResponsePayload
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil && strings.TrimSpace(payload.Summary) != "" {
		return &payload
	}
	// Not JSON (or missing Summary) — treat the whole text as the summary.
	// Preserve the first sentence / first 240 chars so the chat row has
	// something sensible; the full body lives in Details.
	summary := normalizeInlineString(trimmed)
	if summary == "" {
		summary = trimmed
	}
	if len(summary) > 240 {
		summary = summary[:240] + "…"
	}
	return &ConsultResponsePayload{
		Summary: summary,
		Details: trimmed,
	}
}

// InterAgentPayloadSummary extracts the typed summary from data if data
// implements InterAgentResponsePayload. Returns "" when data is untyped,
// signaling the caller should fall back to the defensive-parse path. Over
// time every producer will emit typed payloads and the fallback can be
// deleted.
func InterAgentPayloadSummary(data any) string {
	if payload, ok := data.(InterAgentResponsePayload); ok {
		return payload.InterAgentSummary()
	}
	return ""
}
