package academic

import (
	"errors"
	"testing"

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
