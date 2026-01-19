package messaging

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCrossDomainRequest(t *testing.T) {
	targets := []domain.Domain{domain.DomainLibrarian, domain.DomainAcademic}
	req := NewCrossDomainRequest("test query", targets)

	require.NotNil(t, req)
	assert.NotEmpty(t, req.ID)
	assert.Equal(t, "test query", req.Query)
	assert.Equal(t, targets, req.TargetDomains)
	assert.Equal(t, PriorityNormal, req.Priority)
	assert.False(t, req.CreatedAt.IsZero())
}

func TestCrossDomainRequest_Builders(t *testing.T) {
	req := NewCrossDomainRequest("query", []domain.Domain{domain.DomainLibrarian}).
		WithSource([]domain.Domain{domain.DomainGuide}).
		WithRequestor("guide-agent").
		WithSession("session-123").
		WithPriority(PriorityHigh).
		WithTimeout(30*time.Second).
		WithMetadata("key", "value")

	assert.Equal(t, []domain.Domain{domain.DomainGuide}, req.SourceDomains)
	assert.Equal(t, "guide-agent", req.RequestorID)
	assert.Equal(t, "session-123", req.SessionID)
	assert.Equal(t, PriorityHigh, req.Priority)
	assert.Equal(t, 30*time.Second, req.Timeout)
	assert.Equal(t, "value", req.Metadata["key"])
}

func TestCrossDomainRequest_IsSingleDomain(t *testing.T) {
	tests := []struct {
		name     string
		targets  []domain.Domain
		expected bool
	}{
		{"empty", nil, true},
		{"single", []domain.Domain{domain.DomainLibrarian}, true},
		{"multiple", []domain.Domain{domain.DomainLibrarian, domain.DomainAcademic}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := NewCrossDomainRequest("query", tc.targets)
			assert.Equal(t, tc.expected, req.IsSingleDomain())
		})
	}
}

func TestCrossDomainRequest_DomainCount(t *testing.T) {
	req := NewCrossDomainRequest("query", []domain.Domain{
		domain.DomainLibrarian,
		domain.DomainAcademic,
		domain.DomainArchivalist,
	})

	assert.Equal(t, 3, req.DomainCount())
}

func TestCrossDomainResult_HasError(t *testing.T) {
	tests := []struct {
		name     string
		error    string
		expected bool
	}{
		{"no error", "", false},
		{"with error", "something failed", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CrossDomainResult{Error: tc.error}
			assert.Equal(t, tc.expected, result.HasError())
		})
	}
}

func TestNewCrossDomainResponse(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")

	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, "req-123", resp.RequestID)
	assert.Empty(t, resp.Results)
	assert.NotNil(t, resp.Sources)
	assert.False(t, resp.CompletedAt.IsZero())
}

func TestCrossDomainResponse_AddResult(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")

	result1 := CrossDomainResult{
		Domain:   domain.DomainLibrarian,
		Content:  "librarian content",
		SourceID: "lib-source-1",
	}
	result2 := CrossDomainResult{
		Domain: domain.DomainAcademic,
		Error:  "failed",
	}

	resp.AddResult(result1)
	resp.AddResult(result2)

	assert.Len(t, resp.Results, 2)
	assert.Equal(t, 2, resp.TotalDomains)
	assert.Equal(t, 1, resp.SuccessDomains)
	assert.Equal(t, 1, resp.FailedDomains)
	assert.Equal(t, domain.DomainLibrarian, resp.Sources["lib-source-1"])
}

func TestCrossDomainResponse_SetSynthesisNotes(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")
	resp.SetSynthesisNotes("these results were synthesized")

	assert.Equal(t, "these results were synthesized", resp.SynthesisNotes)
}

func TestCrossDomainResponse_AddConflict(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")

	conflict := ConflictInfo{
		Domains:     []domain.Domain{domain.DomainLibrarian, domain.DomainAcademic},
		Description: "conflicting information",
		Severity:    SeverityMedium,
	}

	resp.AddConflict(conflict)

	assert.True(t, resp.HasConflicts())
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, "conflicting information", resp.Conflicts[0].Description)
}

func TestCrossDomainResponse_HasResults(t *testing.T) {
	tests := []struct {
		name           string
		successDomains int
		expected       bool
	}{
		{"no results", 0, false},
		{"with results", 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &CrossDomainResponse{SuccessDomains: tc.successDomains}
			assert.Equal(t, tc.expected, resp.HasResults())
		})
	}
}

func TestCrossDomainResponse_GetDomainResult(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")
	resp.Results = []CrossDomainResult{
		{Domain: domain.DomainLibrarian, Content: "librarian"},
		{Domain: domain.DomainAcademic, Content: "academic"},
	}

	libResult := resp.GetDomainResult(domain.DomainLibrarian)
	require.NotNil(t, libResult)
	assert.Equal(t, "librarian", libResult.Content)

	nilResult := resp.GetDomainResult(domain.DomainArchivalist)
	assert.Nil(t, nilResult)
}

func TestConflictSeverity(t *testing.T) {
	assert.Equal(t, ConflictSeverity("low"), SeverityLow)
	assert.Equal(t, ConflictSeverity("medium"), SeverityMedium)
	assert.Equal(t, ConflictSeverity("high"), SeverityHigh)
}

func TestNewCrossDomainConsultation(t *testing.T) {
	consultation := NewCrossDomainConsultation("librarian", "academic", "need research help")

	require.NotNil(t, consultation)
	assert.NotEmpty(t, consultation.ID)
	assert.Equal(t, "librarian", consultation.FromAgent)
	assert.Equal(t, "academic", consultation.ToAgent)
	assert.Equal(t, "need research help", consultation.Query)
	assert.Equal(t, PriorityNormal, consultation.Priority)
}

func TestCrossDomainConsultation_Builders(t *testing.T) {
	consultation := NewCrossDomainConsultation("librarian", "academic", "query").
		WithDomains(domain.DomainLibrarian, domain.DomainAcademic).
		WithContext("extra context").
		WithSession("session-456").
		WithParentRequest("parent-req-789").
		WithPriority(PriorityHigh).
		WithMetadata("reason", "cross-reference")

	assert.Equal(t, domain.DomainLibrarian, consultation.FromDomain)
	assert.Equal(t, domain.DomainAcademic, consultation.ToDomain)
	assert.Equal(t, "extra context", consultation.Context)
	assert.Equal(t, "session-456", consultation.SessionID)
	assert.Equal(t, "parent-req-789", consultation.ParentRequestID)
	assert.Equal(t, PriorityHigh, consultation.Priority)
	assert.Equal(t, "cross-reference", consultation.Metadata["reason"])
}

func TestNewConsultationResponse(t *testing.T) {
	resp := NewConsultationResponse("consult-123", "academic")

	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, "consult-123", resp.ConsultationID)
	assert.Equal(t, "academic", resp.FromAgent)
}

func TestConsultationResponse_Builders(t *testing.T) {
	resp := NewConsultationResponse("consult-123", "academic").
		WithContent("research findings").
		WithDomain(domain.DomainAcademic).
		WithConfidence(0.85).
		WithSources([]string{"source1", "source2"}).
		WithDuration(100 * time.Millisecond)

	assert.Equal(t, "research findings", resp.Content)
	assert.Equal(t, domain.DomainAcademic, resp.FromDomain)
	assert.Equal(t, 0.85, resp.Confidence)
	assert.Equal(t, []string{"source1", "source2"}, resp.Sources)
	assert.Equal(t, 100*time.Millisecond, resp.Took)
}

func TestConsultationResponse_WithError(t *testing.T) {
	resp := NewConsultationResponse("consult-123", "academic").
		WithError("something failed")

	assert.True(t, resp.HasError())
	assert.Equal(t, "something failed", resp.Error)
}

func TestConsultationResponse_HasError(t *testing.T) {
	resp := NewConsultationResponse("consult-123", "academic")
	assert.False(t, resp.HasError())

	resp.Error = "error"
	assert.True(t, resp.HasError())
}

func TestCrossDomainRequest_JSONSerialization(t *testing.T) {
	req := NewCrossDomainRequest("test query", []domain.Domain{domain.DomainLibrarian}).
		WithSession("session-123").
		WithPriority(PriorityHigh)

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded CrossDomainRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, req.ID, decoded.ID)
	assert.Equal(t, req.Query, decoded.Query)
	assert.Equal(t, req.SessionID, decoded.SessionID)
}

func TestCrossDomainResponse_JSONSerialization(t *testing.T) {
	resp := NewCrossDomainResponse("req-123")
	resp.AddResult(CrossDomainResult{
		Domain:  domain.DomainLibrarian,
		Content: "test content",
	})

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded CrossDomainResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.ID, decoded.ID)
	assert.Equal(t, resp.RequestID, decoded.RequestID)
	assert.Len(t, decoded.Results, 1)
}

func TestCrossDomainConsultation_JSONSerialization(t *testing.T) {
	consultation := NewCrossDomainConsultation("from", "to", "query").
		WithDomains(domain.DomainLibrarian, domain.DomainAcademic).
		WithSession("session-123")

	data, err := json.Marshal(consultation)
	require.NoError(t, err)

	var decoded CrossDomainConsultation
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, consultation.ID, decoded.ID)
	assert.Equal(t, consultation.FromAgent, decoded.FromAgent)
	assert.Equal(t, consultation.ToAgent, decoded.ToAgent)
	assert.Equal(t, consultation.FromDomain, decoded.FromDomain)
	assert.Equal(t, consultation.ToDomain, decoded.ToDomain)
}

func TestConsultationResponse_JSONSerialization(t *testing.T) {
	resp := NewConsultationResponse("consult-123", "academic").
		WithContent("findings").
		WithConfidence(0.9).
		WithSources([]string{"s1", "s2"})

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded CrossDomainConsultationResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.ID, decoded.ID)
	assert.Equal(t, resp.Content, decoded.Content)
	assert.Equal(t, resp.Confidence, decoded.Confidence)
	assert.Equal(t, resp.Sources, decoded.Sources)
}
