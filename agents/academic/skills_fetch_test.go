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
