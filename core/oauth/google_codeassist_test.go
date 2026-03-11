package oauth

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func overrideCodeAssistEndpoint(url string) {
	codeAssistEndpoint = url
}

func restoreCodeAssistEndpoint(url string) {
	codeAssistEndpoint = url
}

func newCodeAssistTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("loopback sockets unavailable in this environment: %v", err)
		}
		t.Fatalf("failed to listen for test server: %v", err)
	}

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupCodeAssist_AlreadyOnboarded(t *testing.T) {
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := loadCodeAssistResponse{
			CurrentTier:             &codeAssistTier{ID: "free-tier", Name: "Free"},
			CloudAICompanionProject: "proj-123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	result, err := SetupCodeAssist(context.Background(), srv.Client(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProjectID != "proj-123" {
		t.Errorf("expected project proj-123, got %s", result.ProjectID)
	}
	if result.TierID != "free-tier" {
		t.Errorf("expected tier free-tier, got %s", result.TierID)
	}
}

func TestSetupCodeAssist_OnboardAndPoll(t *testing.T) {
	var callCount atomic.Int32
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case n == 1: // loadCodeAssist
			resp := loadCodeAssistResponse{
				AllowedTiers: []codeAssistTier{{ID: "free-tier", Name: "Free", IsDefault: true}},
			}
			json.NewEncoder(w).Encode(resp)
		case n == 2: // onboardUser
			op := longRunningOperation{Name: "operations/op-1", Done: false}
			json.NewEncoder(w).Encode(op)
		default: // getOperation (poll)
			op := longRunningOperation{
				Name: "operations/op-1",
				Done: true,
				Response: &onboardUserResponse{
					CloudAICompanionProject: &struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					}{ID: "managed-proj", Name: "Managed Project"},
				},
			}
			json.NewEncoder(w).Encode(op)
		}
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	result, err := SetupCodeAssist(context.Background(), srv.Client(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProjectID != "managed-proj" {
		t.Errorf("expected project managed-proj, got %s", result.ProjectID)
	}
	if result.TierID != "free-tier" {
		t.Errorf("expected tier free-tier, got %s", result.TierID)
	}
}

func TestSetupCodeAssist_Ineligible(t *testing.T) {
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := loadCodeAssistResponse{
			IneligibleTiers: []ineligibleTier{
				{TierID: "paid", ReasonCode: "NO_BILLING", ReasonMessage: "billing not configured"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	_, err := SetupCodeAssist(context.Background(), srv.Client(), "")
	if err == nil {
		t.Fatal("expected error for ineligible user")
	}
}

func TestSetupCodeAssist_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use a server that never responds — the canceled context should
	// cause the HTTP call to fail immediately.
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should never reach here with canceled context.
		w.WriteHeader(http.StatusOK)
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	_, err := SetupCodeAssist(ctx, srv.Client(), "")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestSelectOnboardTier_DefaultTier(t *testing.T) {
	tiers := []codeAssistTier{
		{ID: "paid", Name: "Paid"},
		{ID: "free", Name: "Free", IsDefault: true},
	}
	id, name := selectOnboardTier(tiers)
	if id != "free" {
		t.Errorf("expected free, got %s", id)
	}
	if name != "Free" {
		t.Errorf("expected Free, got %s", name)
	}
}

func TestSelectOnboardTier_FallbackFirst(t *testing.T) {
	tiers := []codeAssistTier{
		{ID: "first", Name: "First Tier"},
	}
	id, _ := selectOnboardTier(tiers)
	if id != "first" {
		t.Errorf("expected first, got %s", id)
	}
}

func TestSelectOnboardTier_Empty(t *testing.T) {
	id, _ := selectOnboardTier(nil)
	if id != "free-tier" {
		t.Errorf("expected free-tier fallback, got %s", id)
	}
}

func TestBuildCodeAssistMetadata(t *testing.T) {
	meta := buildCodeAssistMetadata()
	if meta.IdeType != "GEMINI_CLI" {
		t.Errorf("expected GEMINI_CLI, got %s", meta.IdeType)
	}
	if meta.IdeName != "sylk" {
		t.Errorf("expected sylk, got %s", meta.IdeName)
	}
	if meta.Platform == "" {
		t.Error("expected non-empty platform")
	}
	// Platform must be a protobuf enum value including arch, not a free-form string.
	validPlatforms := map[string]bool{
		"DARWIN_AMD64": true, "DARWIN_ARM64": true,
		"LINUX_AMD64": true, "LINUX_ARM64": true,
		"WINDOWS_AMD64": true, "PLATFORM_UNSPECIFIED": true,
	}
	if !validPlatforms[meta.Platform] {
		t.Errorf("expected protobuf enum value, got %q", meta.Platform)
	}
}

func TestCodeAssistPlatform(t *testing.T) {
	p := codeAssistPlatform()
	switch p {
	case "DARWIN_AMD64", "DARWIN_ARM64", "LINUX_AMD64", "LINUX_ARM64", "WINDOWS_AMD64", "PLATFORM_UNSPECIFIED":
	default:
		t.Errorf("unexpected platform enum: %q", p)
	}
}

func TestExtractOperationProjectID_Nil(t *testing.T) {
	if id := extractOperationProjectID(nil); id != "" {
		t.Errorf("expected empty, got %s", id)
	}
	if id := extractOperationProjectID(&longRunningOperation{}); id != "" {
		t.Errorf("expected empty, got %s", id)
	}
}

func TestSetupCodeAssist_OnboardImmediateComplete(t *testing.T) {
	var callCount atomic.Int32
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case n == 1: // loadCodeAssist
			resp := loadCodeAssistResponse{
				AllowedTiers: []codeAssistTier{{ID: "free-tier", Name: "Free"}},
			}
			json.NewEncoder(w).Encode(resp)
		case n == 2: // onboardUser — immediately done
			op := longRunningOperation{
				Name: "operations/op-2",
				Done: true,
				Response: &onboardUserResponse{
					CloudAICompanionProject: &struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					}{ID: "instant-proj", Name: "Instant"},
				},
			}
			json.NewEncoder(w).Encode(op)
		}
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	result, err := SetupCodeAssist(context.Background(), srv.Client(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProjectID != "instant-proj" {
		t.Errorf("expected instant-proj, got %s", result.ProjectID)
	}
}

func TestSetupCodeAssist_ServerError(t *testing.T) {
	srv := newCodeAssistTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))

	orig := codeAssistEndpoint
	t.Cleanup(func() { restoreCodeAssistEndpoint(orig) })
	overrideCodeAssistEndpoint(srv.URL)

	_, err := SetupCodeAssist(context.Background(), srv.Client(), "")
	if err == nil {
		t.Fatal("expected error for server error")
	}
}
