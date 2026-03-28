package academic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/skills"
)

func TestFetchFailureResult_ApprovalDeniedDelegates(t *testing.T) {
	_, err := fetchFailureResult("web_fetch", &fetch.FetchResponse{
		URL:            "https://example.com/spec",
		ApprovalDenied: true,
		Error:          "fetch denied by user: not now",
	})
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("expected delegated requested error, got %v", err)
	}
	payload, ok := skills.DelegatedPayload(err)
	if !ok {
		t.Fatal("expected delegated payload")
	}
	data, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("delegated payload type = %T, want map[string]any", payload)
	}
	if data["status"] != "approval_denied" {
		t.Fatalf("status = %v, want approval_denied", data["status"])
	}
	if msg := skills.DelegatedMessage(err); msg == "" {
		t.Fatal("expected delegated user message")
	}
}

func TestFetchFailureResult_NonApprovalReturnsStructuredFailure(t *testing.T) {
	output, err := fetchFailureResult("web_fetch", &fetch.FetchResponse{
		URL:      "https://example.com/spec",
		Error:    "content blocked by Guardian inspection",
		Duration: 0,
	})
	if err != nil {
		t.Fatalf("fetchFailureResult() error = %v", err)
	}
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("output type = %T, want map[string]any", output)
	}
	if result["success"] != false {
		t.Fatalf("success = %v, want false", result["success"])
	}
	if result["error"] != "content blocked by Guardian inspection" {
		t.Fatalf("error = %v, want blocked message", result["error"])
	}
}

func TestNormalizeGroundSourceTool_AutoDetectsDocumentURLs(t *testing.T) {
	toolName, err := normalizeGroundSourceTool("https://example.com/spec.pdf", "auto")
	if err != nil {
		t.Fatalf("normalizeGroundSourceTool() error = %v", err)
	}
	if toolName != "fetch_document" {
		t.Fatalf("toolName = %q, want fetch_document", toolName)
	}
}

func TestAppendFetchPersistenceFields_ExposesAsyncPersistenceMetadata(t *testing.T) {
	result := map[string]any{}
	appendFetchPersistenceFields(result, &fetch.FetchResponse{
		IngestStatus: fetch.IngestStatusQueued,
		IngestJobID:  "ingest_123",
		Ingested:     false,
	})
	if result["persistence_status"] != string(fetch.IngestStatusQueued) {
		t.Fatalf("persistence_status = %v, want %q", result["persistence_status"], fetch.IngestStatusQueued)
	}
	if result["persistence_job_id"] != "ingest_123" {
		t.Fatalf("persistence_job_id = %v, want ingest_123", result["persistence_job_id"])
	}
	if result["ingested"] != false {
		t.Fatalf("ingested = %v, want false", result["ingested"])
	}
}

func TestExtractLinks_ResolvesFiltersAndDeduplicates(t *testing.T) {
	got := extractLinks("https://example.com/reference/index.html?x=1#top", &fetch.ExtractResult{
		Links: []string{
			"/docs/start",
			"guide.html",
			"https://other.example.com/report#summary",
			"#section",
			"mailto:test@example.com",
			"javascript:void(0)",
			"//cdn.example.com/lib.js",
			"/docs/start",
		},
	})

	want := []string{
		"https://example.com/docs/start",
		"https://example.com/reference/guide.html",
		"https://other.example.com/report",
		"https://cdn.example.com/lib.js",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractLinks() = %#v, want %#v", got, want)
	}
}

func TestAcademicExecuteCrawl_FollowsExtractedLinks(t *testing.T) {
	rootURL := "http://docs.safe.test/reference/index.html"
	pages := map[string]string{
		rootURL: `<html><body>
			<a href="/guide">Guide</a>
			<a href="chapter1.html">Chapter 1</a>
			<a href="#section">Section</a>
			<a href="mailto:test@example.com">Email</a>
			<a href="/guide">Guide Again</a>
		</body></html>`,
		"http://docs.safe.test/guide":                   "<html><body><p>Guide content</p></body></html>",
		"http://docs.safe.test/reference/chapter1.html": "<html><body><p>Chapter content</p></body></html>",
	}

	policyCfg := fetch.DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := fetch.NewPipeline(fetch.PipelineConfig{
		Policy:     fetch.NewFetchPolicy(policyCfg),
		Consent:    fetch.NewConsentGate(fetch.ConsentGateConfig{AutoApproveDomains: []string{"*"}}),
		Quarantine: fetch.NewQuarantineBuffer(8, 1024*1024),
		Client: fetch.NewClient(fetch.ClientConfig{
			MaxBytes:       1024 * 1024,
			RequestTimeout: 5 * time.Second,
			UserAgent:      "Sylk-Academic/1.0",
			Transport: crawlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, ok := pages[req.URL.String()]
				if !ok {
					return &http.Response{
						StatusCode: 404,
						Status:     "404 Not Found",
						Header: http.Header{
							"Content-Type": []string{"text/plain; charset=utf-8"},
						},
						Body:    io.NopCloser(strings.NewReader("missing")),
						Request: req,
					}, nil
				}
				return &http.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Header: http.Header{
						"Content-Type": []string{"text/html; charset=utf-8"},
					},
					Body:    io.NopCloser(strings.NewReader(body)),
					Request: req,
				}, nil
			}),
		}),
		Extractor: fetch.NewContentExtractor(),
		Inspect: func(context.Context, *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
			return fetch.VerdictClean, nil, nil
		},
	})

	a := &Academic{fetchPipeline: pipeline}
	output, err := a.executeCrawl(context.Background(), rootURL, "follow the docs", true, 5)
	if err != nil {
		t.Fatalf("executeCrawl() error = %v", err)
	}

	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("output type = %T, want map[string]any", output)
	}
	if result["links_found"] != 2 {
		t.Fatalf("links_found = %v, want 2", result["links_found"])
	}

	linksFetched, ok := result["links_fetched"].([]map[string]any)
	if !ok {
		t.Fatalf("links_fetched type = %T, want []map[string]any", result["links_fetched"])
	}
	if len(linksFetched) != 2 {
		t.Fatalf("len(links_fetched) = %d, want 2", len(linksFetched))
	}
	if linksFetched[0]["url"] != "http://docs.safe.test/guide" {
		t.Fatalf("first followed url = %v, want guide", linksFetched[0]["url"])
	}
	if linksFetched[1]["url"] != "http://docs.safe.test/reference/chapter1.html" {
		t.Fatalf("second followed url = %v, want chapter", linksFetched[1]["url"])
	}
	if !strings.Contains(linksFetched[0]["content_preview"].(string), "Guide content") {
		t.Fatalf("guide preview = %q, want guide content", linksFetched[0]["content_preview"])
	}
	if !strings.Contains(linksFetched[1]["content_preview"].(string), "Chapter content") {
		t.Fatalf("chapter preview = %q, want chapter content", linksFetched[1]["content_preview"])
	}
}

type crawlRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn crawlRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
