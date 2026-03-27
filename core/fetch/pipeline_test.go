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

func TestPipelineExecute_QueuesAsyncIngestWithoutBlockingGroundedResponse(t *testing.T) {
	policyCfg := DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	ingested := make(chan string, 1)
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
						Body:    io.NopCloser(strings.NewReader("<html><head><title>Async Fetch Test</title></head><body><p>Ground me</p></body></html>")),
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
		Ingest: func(_ context.Context, entry *QuarantineEntry, _ *Provenance, _ *ExtractResult) error {
			ingested <- entry.URL
			return nil
		},
	})

	resp := pipeline.Execute(context.Background(), &FetchRequest{
		URL:         "http://docs.safe.test/reference",
		SourceAgent: "academic",
		Reason:      "test async fetch",
		SessionID:   "sess-1",
		AsyncIngest: true,
	})

	if !resp.Success {
		t.Fatalf("execute failed: %s", resp.Error)
	}
	if resp.Ingested {
		t.Fatal("expected async ingest response to remain non-blocking")
	}
	if resp.IngestStatus != IngestStatusQueued {
		t.Fatalf("ingest status = %q, want %q", resp.IngestStatus, IngestStatusQueued)
	}
	if resp.IngestJobID == "" {
		t.Fatal("expected async ingest job id")
	}
	job, ok := pipeline.GetIngestJob(resp.IngestJobID)
	if !ok {
		t.Fatalf("expected ingest job %q to be retrievable", resp.IngestJobID)
	}
	if job.Status != IngestStatusQueued && job.Status != IngestStatusRunning && job.Status != IngestStatusCompleted {
		t.Fatalf("unexpected initial ingest job status %q", job.Status)
	}

	select {
	case gotURL := <-ingested:
		if gotURL != "http://docs.safe.test/reference" {
			t.Fatalf("ingested url = %q, want request url", gotURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async ingest to run")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok = pipeline.GetIngestJob(resp.IngestJobID)
		if ok && job.Status == IngestStatusCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected ingest job %q to complete, got %#v", resp.IngestJobID, job)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
