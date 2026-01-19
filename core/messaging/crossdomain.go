package messaging

import (
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/google/uuid"
)

// CrossDomainRequest represents a request that may span multiple domains.
type CrossDomainRequest struct {
	ID            string          `json:"id"`
	Query         string          `json:"query"`
	SourceDomains []domain.Domain `json:"source_domains"`
	TargetDomains []domain.Domain `json:"target_domains"`
	RequestorID   string          `json:"requestor_id"`
	SessionID     string          `json:"session_id"`
	Priority      Priority        `json:"priority"`
	Timeout       time.Duration   `json:"timeout,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
}

// NewCrossDomainRequest creates a new cross-domain request.
func NewCrossDomainRequest(query string, targets []domain.Domain) *CrossDomainRequest {
	return &CrossDomainRequest{
		ID:            uuid.New().String(),
		Query:         query,
		TargetDomains: targets,
		Priority:      PriorityNormal,
		CreatedAt:     time.Now(),
	}
}

// WithSource sets the source domains.
func (r *CrossDomainRequest) WithSource(sources []domain.Domain) *CrossDomainRequest {
	r.SourceDomains = sources
	return r
}

// WithRequestor sets the requestor ID.
func (r *CrossDomainRequest) WithRequestor(requestorID string) *CrossDomainRequest {
	r.RequestorID = requestorID
	return r
}

// WithSession sets the session ID.
func (r *CrossDomainRequest) WithSession(sessionID string) *CrossDomainRequest {
	r.SessionID = sessionID
	return r
}

// WithPriority sets the request priority.
func (r *CrossDomainRequest) WithPriority(priority Priority) *CrossDomainRequest {
	r.Priority = priority
	return r
}

// WithTimeout sets the request timeout.
func (r *CrossDomainRequest) WithTimeout(timeout time.Duration) *CrossDomainRequest {
	r.Timeout = timeout
	return r
}

// WithMetadata adds metadata.
func (r *CrossDomainRequest) WithMetadata(key string, value any) *CrossDomainRequest {
	if r.Metadata == nil {
		r.Metadata = make(map[string]any)
	}
	r.Metadata[key] = value
	return r
}

// IsSingleDomain returns true if the request targets only one domain.
func (r *CrossDomainRequest) IsSingleDomain() bool {
	return len(r.TargetDomains) <= 1
}

// DomainCount returns the number of target domains.
func (r *CrossDomainRequest) DomainCount() int {
	return len(r.TargetDomains)
}

// CrossDomainResult represents a single domain's contribution to a response.
type CrossDomainResult struct {
	Domain      domain.Domain `json:"domain"`
	Content     string        `json:"content"`
	Score       float64       `json:"score"`
	SourceID    string        `json:"source_id"`
	RetrievedAt time.Time     `json:"retrieved_at"`
	Took        time.Duration `json:"took"`
	Error       string        `json:"error,omitempty"`
}

// HasError returns true if this result has an error.
func (r *CrossDomainResult) HasError() bool {
	return r.Error != ""
}

// CrossDomainResponse represents the aggregated response from multiple domains.
type CrossDomainResponse struct {
	ID             string                   `json:"id"`
	RequestID      string                   `json:"request_id"`
	Results        []CrossDomainResult      `json:"results"`
	Sources        map[string]domain.Domain `json:"sources"`
	SynthesisNotes string                   `json:"synthesis_notes,omitempty"`
	Conflicts      []ConflictInfo           `json:"conflicts,omitempty"`
	TotalDomains   int                      `json:"total_domains"`
	SuccessDomains int                      `json:"success_domains"`
	FailedDomains  int                      `json:"failed_domains"`
	Took           time.Duration            `json:"took"`
	CompletedAt    time.Time                `json:"completed_at"`
}

// NewCrossDomainResponse creates a new cross-domain response.
func NewCrossDomainResponse(requestID string) *CrossDomainResponse {
	return &CrossDomainResponse{
		ID:          uuid.New().String(),
		RequestID:   requestID,
		Results:     make([]CrossDomainResult, 0),
		Sources:     make(map[string]domain.Domain),
		CompletedAt: time.Now(),
	}
}

// AddResult adds a domain result to the response.
func (r *CrossDomainResponse) AddResult(result CrossDomainResult) {
	r.Results = append(r.Results, result)

	if result.SourceID != "" {
		r.Sources[result.SourceID] = result.Domain
	}

	r.TotalDomains++
	if result.HasError() {
		r.FailedDomains++
	} else {
		r.SuccessDomains++
	}
}

// SetSynthesisNotes sets the synthesis notes.
func (r *CrossDomainResponse) SetSynthesisNotes(notes string) {
	r.SynthesisNotes = notes
}

// AddConflict adds a conflict to the response.
func (r *CrossDomainResponse) AddConflict(conflict ConflictInfo) {
	r.Conflicts = append(r.Conflicts, conflict)
}

// HasConflicts returns true if there are any conflicts.
func (r *CrossDomainResponse) HasConflicts() bool {
	return len(r.Conflicts) > 0
}

// HasResults returns true if there are any successful results.
func (r *CrossDomainResponse) HasResults() bool {
	return r.SuccessDomains > 0
}

// GetDomainResult returns the result for a specific domain.
func (r *CrossDomainResponse) GetDomainResult(d domain.Domain) *CrossDomainResult {
	for i := range r.Results {
		if r.Results[i].Domain == d {
			return &r.Results[i]
		}
	}
	return nil
}

// ConflictInfo represents a conflict between domain results.
type ConflictInfo struct {
	Domains     []domain.Domain  `json:"domains"`
	Description string           `json:"description"`
	Severity    ConflictSeverity `json:"severity"`
	Resolution  string           `json:"resolution,omitempty"`
}

// ConflictSeverity indicates the severity of a conflict.
type ConflictSeverity string

const (
	SeverityLow    ConflictSeverity = "low"
	SeverityMedium ConflictSeverity = "medium"
	SeverityHigh   ConflictSeverity = "high"
)

// CrossDomainConsultation represents a consultation request between agents.
type CrossDomainConsultation struct {
	ID              string         `json:"id"`
	FromAgent       string         `json:"from_agent"`
	FromDomain      domain.Domain  `json:"from_domain"`
	ToAgent         string         `json:"to_agent"`
	ToDomain        domain.Domain  `json:"to_domain"`
	Query           string         `json:"query"`
	Context         string         `json:"context,omitempty"`
	Priority        Priority       `json:"priority"`
	SessionID       string         `json:"session_id"`
	ParentRequestID string         `json:"parent_request_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// NewCrossDomainConsultation creates a new consultation request.
func NewCrossDomainConsultation(from, to string, query string) *CrossDomainConsultation {
	return &CrossDomainConsultation{
		ID:        uuid.New().String(),
		FromAgent: from,
		ToAgent:   to,
		Query:     query,
		Priority:  PriorityNormal,
		CreatedAt: time.Now(),
	}
}

// WithDomains sets the source and target domains.
func (c *CrossDomainConsultation) WithDomains(from, to domain.Domain) *CrossDomainConsultation {
	c.FromDomain = from
	c.ToDomain = to
	return c
}

// WithContext sets additional context for the consultation.
func (c *CrossDomainConsultation) WithContext(ctx string) *CrossDomainConsultation {
	c.Context = ctx
	return c
}

// WithSession sets the session ID.
func (c *CrossDomainConsultation) WithSession(sessionID string) *CrossDomainConsultation {
	c.SessionID = sessionID
	return c
}

// WithParentRequest sets the parent request ID.
func (c *CrossDomainConsultation) WithParentRequest(parentID string) *CrossDomainConsultation {
	c.ParentRequestID = parentID
	return c
}

// WithPriority sets the consultation priority.
func (c *CrossDomainConsultation) WithPriority(priority Priority) *CrossDomainConsultation {
	c.Priority = priority
	return c
}

// WithMetadata adds metadata.
func (c *CrossDomainConsultation) WithMetadata(key string, value any) *CrossDomainConsultation {
	if c.Metadata == nil {
		c.Metadata = make(map[string]any)
	}
	c.Metadata[key] = value
	return c
}

// CrossDomainConsultationResponse represents the response to a consultation.
type CrossDomainConsultationResponse struct {
	ID             string        `json:"id"`
	ConsultationID string        `json:"consultation_id"`
	FromAgent      string        `json:"from_agent"`
	FromDomain     domain.Domain `json:"from_domain"`
	Content        string        `json:"content"`
	Confidence     float64       `json:"confidence"`
	Sources        []string      `json:"sources,omitempty"`
	Took           time.Duration `json:"took"`
	Error          string        `json:"error,omitempty"`
	CompletedAt    time.Time     `json:"completed_at"`
}

// NewConsultationResponse creates a new consultation response.
func NewConsultationResponse(consultationID, fromAgent string) *CrossDomainConsultationResponse {
	return &CrossDomainConsultationResponse{
		ID:             uuid.New().String(),
		ConsultationID: consultationID,
		FromAgent:      fromAgent,
		CompletedAt:    time.Now(),
	}
}

// WithContent sets the response content.
func (r *CrossDomainConsultationResponse) WithContent(content string) *CrossDomainConsultationResponse {
	r.Content = content
	return r
}

// WithDomain sets the responding domain.
func (r *CrossDomainConsultationResponse) WithDomain(d domain.Domain) *CrossDomainConsultationResponse {
	r.FromDomain = d
	return r
}

// WithConfidence sets the confidence score.
func (r *CrossDomainConsultationResponse) WithConfidence(conf float64) *CrossDomainConsultationResponse {
	r.Confidence = conf
	return r
}

// WithSources sets the source references.
func (r *CrossDomainConsultationResponse) WithSources(sources []string) *CrossDomainConsultationResponse {
	r.Sources = sources
	return r
}

// WithDuration sets the processing duration.
func (r *CrossDomainConsultationResponse) WithDuration(took time.Duration) *CrossDomainConsultationResponse {
	r.Took = took
	return r
}

// WithError sets an error.
func (r *CrossDomainConsultationResponse) WithError(err string) *CrossDomainConsultationResponse {
	r.Error = err
	return r
}

// HasError returns true if the response has an error.
func (r *CrossDomainConsultationResponse) HasError() bool {
	return r.Error != ""
}
