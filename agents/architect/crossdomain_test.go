package architect

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCrossDomainHandler(t *testing.T) {
	handler := NewCrossDomainHandler(nil)

	require.NotNil(t, handler)
	assert.Equal(t, 30*time.Second, handler.timeout)
	assert.Equal(t, 3, handler.maxConcurrent)
}

func TestNewCrossDomainHandler_WithConfig(t *testing.T) {
	config := &CrossDomainHandlerConfig{
		Timeout:       10 * time.Second,
		MaxConcurrent: 5,
		Logger:        slog.Default(),
	}

	handler := NewCrossDomainHandler(config)

	assert.Equal(t, 10*time.Second, handler.timeout)
	assert.Equal(t, 5, handler.maxConcurrent)
}

func TestCrossDomainHandler_HandleCrossDomain_NilContext(t *testing.T) {
	handler := NewCrossDomainHandler(nil)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "test query", result.Query)
	assert.Empty(t, result.DomainResults)
}

func TestCrossDomainHandler_HandleCrossDomain_EmptyContext(t *testing.T) {
	handler := NewCrossDomainHandler(nil)
	domainCtx := domain.NewDomainContext("test")

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.DomainResults)
}

func TestCrossDomainHandler_HandleCrossDomain_SingleDomain(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		return &DomainResult{
			Domain:  d,
			Query:   query,
			Content: "test content",
			Score:   0.9,
			Source:  "test-source",
		}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		QueryHandler: mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.DomainResults, 1)
	assert.Equal(t, 1, result.SuccessDomains)
	assert.Equal(t, 0, result.FailedDomains)
	assert.False(t, result.IsCrossDomain)
}

func TestCrossDomainHandler_HandleCrossDomain_MultipleDomains(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		return &DomainResult{
			Domain:  d,
			Query:   query,
			Content: "content from " + d.String(),
			Score:   0.8,
			Source:  d.String() + "-source",
		}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		QueryHandler: mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.DomainResults, 2)
	assert.Equal(t, 2, result.SuccessDomains)
	assert.Equal(t, 0, result.FailedDomains)
	assert.True(t, result.IsCrossDomain)
}

func TestCrossDomainHandler_HandleCrossDomain_WithErrors(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		if d == domain.DomainLibrarian {
			return nil, errors.New("domain error")
		}
		return &DomainResult{
			Domain:  d,
			Query:   query,
			Content: "success content",
		}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		QueryHandler: mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.DomainResults, 2)
	assert.Equal(t, 1, result.SuccessDomains)
	assert.Equal(t, 1, result.FailedDomains)
}

func TestCrossDomainHandler_HandleCrossDomain_NoHandler(t *testing.T) {
	handler := NewCrossDomainHandler(nil)

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.DomainResults, 1)
	assert.Equal(t, "no query handler configured", result.DomainResults[0].ErrorMsg)
}

func TestCrossDomainHandler_HandleCrossDomain_Timeout(t *testing.T) {
	slowHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return &DomainResult{Domain: d, Query: query}, nil
		}
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		Timeout:      100 * time.Millisecond,
		QueryHandler: slowHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.DomainResults, 1)
}

func TestCrossDomainHandler_SourceMapBuilding(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		return &DomainResult{
			Domain: d,
			Query:  query,
			Source: d.String() + "-source-id",
		}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		QueryHandler: mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.7, nil)
	domainCtx.SetCrossDomain(true)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result.SourceMap)
	assert.Len(t, result.SourceMap, 2)
	assert.Equal(t, domain.DomainLibrarian, result.SourceMap["librarian-source-id"])
	assert.Equal(t, domain.DomainAcademic, result.SourceMap["academic-source-id"])
}

func TestCrossDomainResult_HasResults(t *testing.T) {
	tests := []struct {
		name           string
		successDomains int
		expected       bool
	}{
		{"no results", 0, false},
		{"with results", 1, true},
		{"multiple results", 3, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := &CrossDomainResult{SuccessDomains: tc.successDomains}
			assert.Equal(t, tc.expected, result.HasResults())
		})
	}
}

func TestCrossDomainResult_GetDomainResult(t *testing.T) {
	result := &CrossDomainResult{
		DomainResults: []DomainResult{
			{Domain: domain.DomainLibrarian, Content: "librarian content"},
			{Domain: domain.DomainAcademic, Content: "academic content"},
		},
	}

	libResult := result.GetDomainResult(domain.DomainLibrarian)
	require.NotNil(t, libResult)
	assert.Equal(t, "librarian content", libResult.Content)

	acadResult := result.GetDomainResult(domain.DomainAcademic)
	require.NotNil(t, acadResult)
	assert.Equal(t, "academic content", acadResult.Content)

	nilResult := result.GetDomainResult(domain.DomainArchivalist)
	assert.Nil(t, nilResult)
}

func TestCrossDomainResult_SuccessfulResults(t *testing.T) {
	result := &CrossDomainResult{
		DomainResults: []DomainResult{
			{Domain: domain.DomainLibrarian, Content: "success"},
			{Domain: domain.DomainAcademic, ErrorMsg: "failed"},
			{Domain: domain.DomainArchivalist, Content: "success too"},
		},
		SuccessDomains: 2,
	}

	successful := result.SuccessfulResults()

	assert.Len(t, successful, 2)
	assert.Equal(t, domain.DomainLibrarian, successful[0].Domain)
	assert.Equal(t, domain.DomainArchivalist, successful[1].Domain)
}

func TestCrossDomainResult_FailedResults(t *testing.T) {
	testErr := errors.New("test error")
	result := &CrossDomainResult{
		DomainResults: []DomainResult{
			{Domain: domain.DomainLibrarian, Content: "success"},
			{Domain: domain.DomainAcademic, Error: testErr, ErrorMsg: "test error"},
			{Domain: domain.DomainArchivalist, ErrorMsg: "another error"},
		},
		FailedDomains: 2,
	}

	failed := result.FailedResults()

	assert.Len(t, failed, 2)
	assert.Equal(t, domain.DomainAcademic, failed[0].Domain)
	assert.Equal(t, domain.DomainArchivalist, failed[1].Domain)
}

func TestCrossDomainHandler_ConcurrencyLimit(t *testing.T) {
	concurrentCalls := 0
	maxConcurrent := 0
	var mu sync.Mutex

	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		mu.Lock()
		concurrentCalls++
		if concurrentCalls > maxConcurrent {
			maxConcurrent = concurrentCalls
		}
		mu.Unlock()

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		concurrentCalls--
		mu.Unlock()

		return &DomainResult{Domain: d, Query: query}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		MaxConcurrent: 2,
		QueryHandler:  mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	for _, d := range domain.KnowledgeDomains() {
		domainCtx.AddDomain(d, 0.8, nil)
	}
	domainCtx.SetCrossDomain(true)

	_, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	assert.LessOrEqual(t, maxConcurrent, 2)
}

func TestDomainResult_Fields(t *testing.T) {
	now := time.Now()
	result := DomainResult{
		Domain:      domain.DomainLibrarian,
		Query:       "test query",
		Content:     "test content",
		Score:       0.95,
		Source:      "test-source",
		Took:        100 * time.Millisecond,
		RetrievedAt: now,
	}

	assert.Equal(t, domain.DomainLibrarian, result.Domain)
	assert.Equal(t, "test query", result.Query)
	assert.Equal(t, "test content", result.Content)
	assert.Equal(t, 0.95, result.Score)
	assert.Equal(t, "test-source", result.Source)
	assert.Equal(t, 100*time.Millisecond, result.Took)
	assert.Equal(t, now, result.RetrievedAt)
}

func TestCrossDomainResult_AllKnowledgeDomains(t *testing.T) {
	mockHandler := func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
		return &DomainResult{
			Domain:  d,
			Query:   query,
			Content: "content from " + d.String(),
		}, nil
	}

	handler := NewCrossDomainHandler(&CrossDomainHandlerConfig{
		QueryHandler: mockHandler,
	})

	domainCtx := domain.NewDomainContext("test")
	for _, d := range domain.KnowledgeDomains() {
		domainCtx.AddDomain(d, 0.8, nil)
	}
	domainCtx.SetCrossDomain(true)

	result, err := handler.HandleCrossDomain(context.Background(), "test query", domainCtx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, len(domain.KnowledgeDomains()), result.TotalDomains)
	assert.Equal(t, len(domain.KnowledgeDomains()), result.SuccessDomains)
	assert.Equal(t, 0, result.FailedDomains)
}
