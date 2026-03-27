package fetch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPipelineExecute_ExtractsTextWithoutIngestion(t *testing.T) {
	policyCfg := DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := NewPipeline(PipelineConfig{
		Policy:     NewFetchPolicy(policyCfg),
		Consent:    NewConsentGate(ConsentGateConfig{AutoApproveDomains: []string{"*"}}),
		Quarantine: NewQuarantineBuffer(8, 1024*1024),
		Client: &Client{
			httpClient: &http.Client{
				Timeout: 5 * time.Second,
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: 200,
						Status:     "200 OK",
						Header: http.Header{
							"Content-Type": []string{"text/html; charset=utf-8"},
						},
						Body:    io.NopCloser(strings.NewReader("<html><head><title>Fetch Test</title></head><body><h1>Hello</h1><p>Research content</p></body></html>")),
						Request: req,
					}, nil
				}),
			},
			maxBytes:     1024 * 1024,
			userAgent:    "Sylk-Academic/1.0",
			tlsValidator: nil,
		},
		Extractor: NewContentExtractor(),
		Inspect: func(context.Context, *QuarantineEntry) (QuarantineVerdict, []InspectionFinding, error) {
			return VerdictClean, nil, nil
		},
	})

	resp := pipeline.Execute(context.Background(), &FetchRequest{
		URL:         "http://docs.safe.test/reference",
		SourceAgent: "academic",
		Reason:      "test fetch",
		SessionID:   "sess-1",
	})

	if !resp.Success {
		t.Fatalf("execute failed: %s", resp.Error)
	}
	if resp.Ingested {
		t.Fatal("expected ingested=false when no ingest function is configured")
	}
	if resp.Extracted == nil {
		t.Fatal("expected extracted content")
	}
	if resp.Extracted.Title != "Fetch Test" {
		t.Fatalf("title = %q, want %q", resp.Extracted.Title, "Fetch Test")
	}
	if !strings.Contains(resp.Extracted.Text, "Research content") {
		t.Fatalf("expected extracted text to contain body content, got %q", resp.Extracted.Text)
	}
}

func TestPipelineExecute_MarksApprovalDeniedWhenConsentRejected(t *testing.T) {
	policyCfg := DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := NewPipeline(PipelineConfig{
		Policy: NewFetchPolicy(policyCfg),
		Consent: NewConsentGate(ConsentGateConfig{Callback: func(context.Context, *FetchProposal) (*ConsentResult, error) {
			return &ConsentResult{Granted: false, Reason: "not now"}, nil
		}}),
		Quarantine: NewQuarantineBuffer(8, 1024*1024),
		Client: &Client{
			httpClient: &http.Client{
				Timeout: 5 * time.Second,
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: 200,
						Status:     "200 OK",
						Header: http.Header{
							"Content-Type": []string{"text/html; charset=utf-8"},
						},
						Body:    io.NopCloser(strings.NewReader("<html><body>unreachable</body></html>")),
						Request: req,
					}, nil
				}),
			},
			maxBytes:     1024 * 1024,
			userAgent:    "Sylk-Academic/1.0",
			tlsValidator: nil,
		},
		Extractor: NewContentExtractor(),
		Inspect: func(context.Context, *QuarantineEntry) (QuarantineVerdict, []InspectionFinding, error) {
			return VerdictClean, nil, nil
		},
	})

	resp := pipeline.Execute(context.Background(), &FetchRequest{
		URL:         "http://docs.safe.test/reference",
		SourceAgent: "academic",
		Reason:      "test denied fetch",
		SessionID:   "sess-1",
	})

	if resp.Success {
		t.Fatal("expected denied fetch to fail")
	}
	if !resp.ApprovalDenied {
		t.Fatal("expected approval_denied to be set")
	}
	if !strings.Contains(resp.Error, "fetch denied by user") {
		t.Fatalf("error = %q, want user denial", resp.Error)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
