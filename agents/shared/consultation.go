package shared

import "time"

// DefaultConsultationTimeout is the synchronous consultation timeout shared
// by all agents. This is the maximum time an agent will block waiting for a
// response from another agent via the event bus. Derived from the escalation
// policy's CooldownDuration (30s) × 2, ensuring an agent can attempt
// escalation if the consultation times out.
const DefaultConsultationTimeout = 60 * time.Second

// ConsultationEvidence records the result of a cross-agent consultation request.
// It captures the query sent to a target agent, the response data, and timing
// information for observability and correlation tracking.
type ConsultationEvidence struct {
	Target      string    `json:"target"`
	Query       string    `json:"query"`
	Scope       string    `json:"scope"`
	Correlation string    `json:"correlation"`
	Success     bool      `json:"success"`
	Data        any       `json:"data,omitempty"`
	Error       string    `json:"error,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	ReceivedAt  time.Time `json:"received_at"`
}
