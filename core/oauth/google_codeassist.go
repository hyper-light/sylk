package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	defaultCodeAssistEndpoint = "https://cloudcode-pa.googleapis.com"
	codeAssistAPIVersion      = "v1internal"
	codeAssistPollDelay       = 5 * time.Second
	codeAssistMaxPolls        = 60 // 5min max
)

// codeAssistEndpoint is the base URL for Code Assist API calls.
// Tests override this via overrideCodeAssistEndpoint.
var codeAssistEndpoint = defaultCodeAssistEndpoint

// CodeAssistSetupResult contains the project and tier obtained from Code Assist onboarding.
type CodeAssistSetupResult struct {
	ProjectID string
	TierID    string
	TierName  string
}

type codeAssistClientMetadata struct {
	IdeType    string `json:"ideType,omitempty"`
	IdeVersion string `json:"ideVersion,omitempty"`
	Platform   string `json:"platform,omitempty"`
	IdeName    string `json:"ideName,omitempty"`
	PluginType string `json:"pluginType,omitempty"`
}

type loadCodeAssistRequest struct {
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

type loadCodeAssistResponse struct {
	CurrentTier             *codeAssistTier  `json:"currentTier,omitempty"`
	AllowedTiers            []codeAssistTier `json:"allowedTiers,omitempty"`
	IneligibleTiers         []ineligibleTier `json:"ineligibleTiers,omitempty"`
	CloudAICompanionProject string           `json:"cloudaicompanionProject,omitempty"`
}

type codeAssistTier struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

type ineligibleTier struct {
	ReasonCode    string `json:"reasonCode"`
	ReasonMessage string `json:"reasonMessage"`
	TierID        string `json:"tierId"`
}

type onboardUserRequest struct {
	TierID                  string                   `json:"tierId,omitempty"`
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

type longRunningOperation struct {
	Name     string               `json:"name"`
	Done     bool                 `json:"done"`
	Response *onboardUserResponse `json:"response,omitempty"`
}

type onboardUserResponse struct {
	CloudAICompanionProject *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"cloudaicompanionProject,omitempty"`
}

// CodeAssistEndpointForTrace returns the current Code Assist endpoint for diagnostics.
func CodeAssistEndpointForTrace() string {
	return codeAssistEndpoint
}

func buildCodeAssistURL(method string) string {
	return codeAssistEndpoint + "/" + codeAssistAPIVersion + ":" + method
}

func buildCodeAssistOperationURL(name string) string {
	return codeAssistEndpoint + "/" + codeAssistAPIVersion + "/" + name
}

// buildCodeAssistSetupMetadata returns metadata for the setup/onboarding flow.
// Matches gemini-cli setup.ts which uses IDE_UNSPECIFIED + PLATFORM_UNSPECIFIED.
func buildCodeAssistSetupMetadata() codeAssistClientMetadata {
	return codeAssistClientMetadata{
		IdeType:    "IDE_UNSPECIFIED",
		PluginType: "GEMINI",
		Platform:   "PLATFORM_UNSPECIFIED",
	}
}

// buildCodeAssistMetadata returns metadata for runtime API calls (streaming, etc.).
func buildCodeAssistMetadata() codeAssistClientMetadata {
	return codeAssistClientMetadata{
		IdeType:    "GEMINI_CLI",
		IdeName:    "sylk",
		PluginType: "GEMINI",
		Platform:   codeAssistPlatform(),
	}
}

// codeAssistPlatform maps runtime.GOOS + runtime.GOARCH to the protobuf enum
// google.internal.cloud.code.v1internal.ClientMetadata.Platform.
// Values sourced from gemini-cli packages/core/src/code_assist/types.ts.
func codeAssistPlatform() string {
	key := runtime.GOOS + "/" + runtime.GOARCH
	switch key {
	case "darwin/amd64":
		return "DARWIN_AMD64"
	case "darwin/arm64":
		return "DARWIN_ARM64"
	case "linux/amd64":
		return "LINUX_AMD64"
	case "linux/arm64":
		return "LINUX_ARM64"
	case "windows/amd64":
		return "WINDOWS_AMD64"
	default:
		return "PLATFORM_UNSPECIFIED"
	}
}

func codeAssistLoadCodeAssist(
	ctx context.Context,
	httpClient *http.Client,
	projectID string,
) (*loadCodeAssistResponse, error) {
	reqBody := loadCodeAssistRequest{
		CloudAICompanionProject: projectID,
		Metadata:                buildCodeAssistSetupMetadata(),
	}
	body, err := doCodeAssistJSONPost(ctx, httpClient, buildCodeAssistURL("loadCodeAssist"), &reqBody)
	if err != nil {
		return nil, fmt.Errorf("code assist loadCodeAssist: %w", err)
	}
	var resp loadCodeAssistResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("code assist loadCodeAssist decode: %w", err)
	}
	return &resp, nil
}

func codeAssistOnboardUser(
	ctx context.Context,
	httpClient *http.Client,
	tierID string,
	projectID string,
) (*longRunningOperation, error) {
	reqBody := onboardUserRequest{
		TierID:                  tierID,
		CloudAICompanionProject: projectID,
		Metadata:                buildCodeAssistSetupMetadata(),
	}
	body, err := doCodeAssistJSONPost(ctx, httpClient, buildCodeAssistURL("onboardUser"), &reqBody)
	if err != nil {
		return nil, fmt.Errorf("code assist onboardUser: %w", err)
	}
	var op longRunningOperation
	if err := json.Unmarshal(body, &op); err != nil {
		return nil, fmt.Errorf("code assist onboardUser decode: %w", err)
	}
	return &op, nil
}

func codeAssistGetOperation(
	ctx context.Context,
	httpClient *http.Client,
	operationName string,
) (*longRunningOperation, error) {
	body, err := doCodeAssistJSONGet(ctx, httpClient, buildCodeAssistOperationURL(operationName))
	if err != nil {
		return nil, fmt.Errorf("code assist getOperation: %w", err)
	}
	var op longRunningOperation
	if err := json.Unmarshal(body, &op); err != nil {
		return nil, fmt.Errorf("code assist getOperation decode: %w", err)
	}
	return &op, nil
}

func codeAssistPollOperation(
	ctx context.Context,
	httpClient *http.Client,
	operationName string,
) (*longRunningOperation, error) {
	for range codeAssistMaxPolls {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(codeAssistPollDelay):
		}
		op, err := codeAssistGetOperation(ctx, httpClient, operationName)
		if err != nil {
			return nil, err
		}
		if op.Done {
			return op, nil
		}
	}
	return nil, fmt.Errorf("code assist operation %s did not complete within polling limit", operationName)
}

func doCodeAssistJSONPost(
	ctx context.Context,
	httpClient *http.Client,
	url string,
	payload any,
) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return executeCodeAssistRequest(httpClient, req)
}

func doCodeAssistJSONGet(
	ctx context.Context,
	httpClient *http.Client,
	url string,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return executeCodeAssistRequest(httpClient, req)
}

func executeCodeAssistRequest(httpClient *http.Client, req *http.Request) ([]byte, error) {
	codeAssistTrace("http", "request", map[string]any{
		"method": req.Method,
		"url":    req.URL.String(),
	})
	resp, err := httpClient.Do(req)
	if err != nil {
		codeAssistTrace("http", "transport_error", map[string]any{
			"method": req.Method,
			"url":    req.URL.String(),
			"error":  err.Error(),
		})
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	codeAssistTrace("http", "response", map[string]any{
		"method":      req.Method,
		"url":         req.URL.String(),
		"status_code": resp.StatusCode,
		"body_length": len(body),
		"body_preview": codeAssistBodyPreview(body, 500),
	})
	if !isHTTPSuccess(resp.StatusCode) {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func codeAssistBodyPreview(body []byte, maxLen int) string {
	if len(body) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(body))
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SetupCodeAssist orchestrates the Code Assist onboarding flow.
// It calls loadCodeAssist to check current status, and if no project is
// provisioned, triggers onboardUser and polls the LRO until done.
func SetupCodeAssist(
	ctx context.Context,
	httpClient *http.Client,
	projectID string,
) (*CodeAssistSetupResult, error) {
	codeAssistTrace("setup", "start", map[string]any{
		"project_id": projectID,
		"endpoint":   codeAssistEndpoint,
	})

	loaded, err := codeAssistLoadCodeAssist(ctx, httpClient, projectID)
	if err != nil {
		codeAssistTrace("setup", "load_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}

	codeAssistTrace("setup", "load_success", map[string]any{
		"has_current_tier":    loaded.CurrentTier != nil,
		"allowed_tier_count":  len(loaded.AllowedTiers),
		"ineligible_count":   len(loaded.IneligibleTiers),
		"companion_project":  loaded.CloudAICompanionProject,
	})

	// Already onboarded — use the existing project.
	if loaded.CurrentTier != nil {
		codeAssistTrace("setup", "already_onboarded", map[string]any{
			"project":   loaded.CloudAICompanionProject,
			"tier_id":   loaded.CurrentTier.ID,
			"tier_name": loaded.CurrentTier.Name,
		})
		return &CodeAssistSetupResult{
			ProjectID: loaded.CloudAICompanionProject,
			TierID:    loaded.CurrentTier.ID,
			TierName:  loaded.CurrentTier.Name,
		}, nil
	}

	// No allowed tiers and ineligible tiers exist — user cannot onboard.
	if len(loaded.AllowedTiers) == 0 && len(loaded.IneligibleTiers) > 0 {
		codeAssistTrace("setup", "ineligible", map[string]any{
			"ineligible_count": len(loaded.IneligibleTiers),
		})
		return nil, buildIneligibleError(loaded.IneligibleTiers)
	}

	tierID, tierName := selectOnboardTier(loaded.AllowedTiers)
	codeAssistTrace("setup", "onboarding", map[string]any{
		"tier_id":   tierID,
		"tier_name": tierName,
	})

	// Onboard — for free-tier, leave cloudaicompanionProject empty so
	// the backend provisions a managed project.
	op, err := codeAssistOnboardUser(ctx, httpClient, tierID, "")
	if err != nil {
		codeAssistTrace("setup", "onboard_failed", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}

	codeAssistTrace("setup", "onboard_response", map[string]any{
		"operation_name": op.Name,
		"done":           op.Done,
	})

	if !op.Done {
		codeAssistTrace("setup", "polling_start", map[string]any{
			"operation": op.Name,
		})
		op, err = codeAssistPollOperation(ctx, httpClient, op.Name)
		if err != nil {
			codeAssistTrace("setup", "poll_failed", map[string]any{
				"error": err.Error(),
			})
			return nil, err
		}
	}

	resultProject := extractOperationProjectID(op)
	if resultProject == "" {
		codeAssistTrace("setup", "no_project_in_response", nil)
		return nil, fmt.Errorf("code assist onboarding completed but no project returned")
	}

	codeAssistTrace("setup", "complete", map[string]any{
		"project_id": resultProject,
		"tier_id":    tierID,
		"tier_name":  tierName,
	})
	return &CodeAssistSetupResult{
		ProjectID: resultProject,
		TierID:    tierID,
		TierName:  tierName,
	}, nil
}

func selectOnboardTier(allowed []codeAssistTier) (string, string) {
	for _, t := range allowed {
		if t.IsDefault {
			return t.ID, t.Name
		}
	}
	if len(allowed) > 0 {
		return allowed[0].ID, allowed[0].Name
	}
	return "free-tier", "Free Tier"
}

func extractOperationProjectID(op *longRunningOperation) string {
	if op == nil || op.Response == nil || op.Response.CloudAICompanionProject == nil {
		return ""
	}
	return strings.TrimSpace(op.Response.CloudAICompanionProject.ID)
}

func buildIneligibleError(tiers []ineligibleTier) error {
	reasons := make([]string, 0, len(tiers))
	for _, t := range tiers {
		reasons = append(reasons, fmt.Sprintf("%s: %s", t.TierID, t.ReasonMessage))
	}
	return fmt.Errorf("code assist: user ineligible for onboarding: %s", strings.Join(reasons, "; "))
}
