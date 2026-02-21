package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIAuthService_DeviceCodeFlow(t *testing.T) {
	t.Parallel()

	accountID := "org_test_workspace"
	planType := "pro"
	idToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  planType,
		},
	})
	accessToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})

	var pollCalls int32
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				if r.Method != http.MethodPost {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				return jsonResponse(http.StatusOK, `{"device_auth_id":"dev_123","user_code":"ABCD-1234","interval":"1"}`), nil
			case "/api/accounts/deviceauth/token":
				call := atomic.AddInt32(&pollCalls, 1)
				if call < 2 {
					return jsonResponse(http.StatusForbidden, `{"message":"pending"}`), nil
				}
				return jsonResponse(http.StatusOK, `{"authorization_code":"auth_code_1","code_verifier":"verifier_1","code_challenge":"challenge_1"}`), nil
			case "/oauth/token":
				if r.Method != http.MethodPost {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				bodyBytes, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				values := parseFormBody(t, bodyBytes)
				if got := values.Get("grant_type"); got != "authorization_code" {
					t.Fatalf("unexpected grant_type: %s", got)
				}
				if got := values.Get("code"); got != "auth_code_1" {
					t.Fatalf("unexpected auth code: %s", got)
				}
				if got := values.Get("code_verifier"); got != "verifier_1" {
					t.Fatalf("unexpected code verifier: %s", got)
				}
				payload := fmt.Sprintf(`{"access_token":%q,"id_token":%q,"refresh_token":"refresh_1","expires_in":3600}`, accessToken, idToken)
				return jsonResponse(http.StatusOK, payload), nil
			default:
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
		}),
	}

	storePath := filepath.Join(t.TempDir(), "openai_auth.yaml")
	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client_123",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(storePath),
		DotEnvPaths: []string{},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	challenge, err := svc.BeginDeviceAuth(context.Background())
	if err != nil {
		t.Fatalf("BeginDeviceAuth() error: %v", err)
	}
	if challenge.UserCode != "ABCD-1234" {
		t.Fatalf("unexpected user code: %q", challenge.UserCode)
	}
	if challenge.DeviceAuthID != "dev_123" {
		t.Fatalf("unexpected device_auth_id: %q", challenge.DeviceAuthID)
	}
	if challenge.PollInterval != time.Second {
		t.Fatalf("unexpected poll interval: %v", challenge.PollInterval)
	}

	auth, err := svc.CompleteDeviceAuth(context.Background(), challenge, 30*time.Second)
	if err != nil {
		t.Fatalf("CompleteDeviceAuth() error: %v", err)
	}
	if auth.AuthMode != "chatgpt" {
		t.Fatalf("unexpected auth mode: %q", auth.AuthMode)
	}
	if auth.AccessToken != accessToken {
		t.Fatalf("unexpected access token")
	}
	if auth.ChatGPTAccountID != accountID {
		t.Fatalf("unexpected account id: %q", auth.ChatGPTAccountID)
	}
	if auth.ChatGPTPlanType != planType {
		t.Fatalf("unexpected plan type: %q", auth.ChatGPTPlanType)
	}

	if err := svc.Save(context.Background(), auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := svc.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.ChatGPTAccountID != accountID {
		t.Fatalf("loaded account id mismatch: %q", loaded.ChatGPTAccountID)
	}
}

func TestOpenAIAuthService_Refresh(t *testing.T) {
	t.Parallel()

	newIDToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh",
			"chatgpt_plan_type":  "team",
		},
	})
	newAccessToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh",
		},
	})

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			values := parseFormBody(t, bodyBytes)
			if values.Get("grant_type") != "refresh_token" {
				t.Fatalf("expected refresh_token grant")
			}
			if values.Get("refresh_token") != "refresh_old" {
				t.Fatalf("unexpected refresh token")
			}
			payload := fmt.Sprintf(`{"access_token":%q,"id_token":%q,"refresh_token":"refresh_new","expires_in":7200}`, newAccessToken, newIDToken)
			return jsonResponse(http.StatusOK, payload), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client_refresh",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
	})

	current := &OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		AccessToken:      "old_access",
		RefreshToken:     "refresh_old",
		ChatGPTAccountID: "org_refresh",
	}

	updated, err := svc.Refresh(context.Background(), current)
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if updated.AccessToken != newAccessToken {
		t.Fatalf("access token not updated")
	}
	if updated.RefreshToken != "refresh_new" {
		t.Fatalf("refresh token not updated")
	}
	if updated.ChatGPTAccountID != "org_refresh" {
		t.Fatalf("account id mismatch: %q", updated.ChatGPTAccountID)
	}
	if updated.ChatGPTPlanType != "team" {
		t.Fatalf("plan type mismatch: %q", updated.ChatGPTPlanType)
	}
}

type asyncDeviceAuthService struct {
	challenge     *DeviceCodeChallenge
	auth          *OpenAIChatGPTAuth
	beginErr      error
	completeErr   error
	saveErr       error
	completeDelay time.Duration
	saveCalls     int32
}

func (m *asyncDeviceAuthService) BeginDeviceAuth(context.Context) (*DeviceCodeChallenge, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	return m.challenge, nil
}

func (m *asyncDeviceAuthService) CompleteDeviceAuth(ctx context.Context, _ *DeviceCodeChallenge, _ time.Duration) (*OpenAIChatGPTAuth, error) {
	if m.completeDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.completeDelay):
		}
	}
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.auth, nil
}

func (m *asyncDeviceAuthService) Refresh(context.Context, *OpenAIChatGPTAuth) (*OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *asyncDeviceAuthService) Resolve(context.Context) (*OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *asyncDeviceAuthService) Save(context.Context, *OpenAIChatGPTAuth) error {
	atomic.AddInt32(&m.saveCalls, 1)
	return m.saveErr
}

func (m *asyncDeviceAuthService) Load(context.Context) (*OpenAIChatGPTAuth, error) {
	return nil, errors.New("not implemented")
}

func (m *asyncDeviceAuthService) Delete(context.Context) error {
	return errors.New("not implemented")
}

func TestStartDeviceAuthSession_ReturnsChallengeImmediately(t *testing.T) {
	t.Parallel()

	service := &asyncDeviceAuthService{
		challenge: &DeviceCodeChallenge{
			VerificationURL: "https://auth.openai.com/codex/device",
			UserCode:        "ABCD-EFGH",
			DeviceAuthID:    "device_123",
			PollInterval:    time.Second,
		},
		auth: &OpenAIChatGPTAuth{
			AuthMode:         "chatgpt",
			AccessToken:      "tok_new",
			ChatGPTAccountID: "org_new",
		},
		completeDelay: 120 * time.Millisecond,
	}

	start := time.Now()
	session, err := StartDeviceAuthSession(context.Background(), service, time.Minute)
	if err != nil {
		t.Fatalf("StartDeviceAuthSession() error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("StartDeviceAuthSession() should return immediately, took %v", elapsed)
	}
	if session == nil || session.Challenge == nil {
		t.Fatalf("expected challenge to be returned")
	}
	if session.Challenge.UserCode != "ABCD-EFGH" {
		t.Fatalf("unexpected user code: %q", session.Challenge.UserCode)
	}

	select {
	case result := <-session.Results:
		if result.Err != nil {
			t.Fatalf("unexpected device auth result error: %v", result.Err)
		}
		if result.Auth == nil || result.Auth.AccessToken != "tok_new" {
			t.Fatalf("unexpected auth result: %+v", result.Auth)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for async device auth result")
	}

	if got := atomic.LoadInt32(&service.saveCalls); got != 1 {
		t.Fatalf("expected Save() to be called once, got %d", got)
	}
}

func TestStartDeviceAuthSession_CancelStopsCompletion(t *testing.T) {
	t.Parallel()

	service := &asyncDeviceAuthService{
		challenge: &DeviceCodeChallenge{
			VerificationURL: "https://auth.openai.com/codex/device",
			UserCode:        "WXYZ-9876",
			DeviceAuthID:    "device_cancel",
			PollInterval:    time.Second,
		},
		auth:          &OpenAIChatGPTAuth{AccessToken: "unused", ChatGPTAccountID: "org_unused"},
		completeDelay: 2 * time.Second,
	}

	session, err := StartDeviceAuthSession(context.Background(), service, time.Minute)
	if err != nil {
		t.Fatalf("StartDeviceAuthSession() error: %v", err)
	}
	session.Cancel()

	select {
	case result := <-session.Results:
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("expected context cancellation error, got %v", result.Err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for canceled device auth result")
	}

	if got := atomic.LoadInt32(&service.saveCalls); got != 0 {
		t.Fatalf("expected Save() not to run after cancel, got %d", got)
	}
}

func TestOpenAIAuthService_ResolveEnvFirst(t *testing.T) {
	t.Setenv("OPENAI_ACCESS_TOKEN", "env_access")
	t.Setenv("OPENAI_CHATGPT_ACCOUNT_ID", "org_env")

	store := NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml"))
	if err := store.Save(&OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		AccessToken:      "file_access",
		ChatGPTAccountID: "org_file",
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{Store: store})
	auth, err := svc.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth.AccessToken != "env_access" {
		t.Fatalf("expected env access token, got %q", auth.AccessToken)
	}
	if auth.ChatGPTAccountID != "org_env" {
		t.Fatalf("expected env account id, got %q", auth.ChatGPTAccountID)
	}
}

func TestOpenAIAuthService_ResolveDotEnv(t *testing.T) {
	t.Parallel()

	dotenvPath := filepath.Join(t.TempDir(), ".env.local")
	content := strings.Join([]string{
		"OPENAI_ACCESS_TOKEN=dotenv_access",
		"OPENAI_CHATGPT_ACCOUNT_ID=org_dotenv",
		"OPENAI_CHATGPT_PLAN_TYPE=plus",
	}, "\n")
	if err := os.WriteFile(dotenvPath, []byte(content), 0600); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{dotenvPath},
	})

	auth, err := svc.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth.AccessToken != "dotenv_access" {
		t.Fatalf("unexpected access token: %q", auth.AccessToken)
	}
	if auth.ChatGPTAccountID != "org_dotenv" {
		t.Fatalf("unexpected account id: %q", auth.ChatGPTAccountID)
	}
	if auth.ChatGPTPlanType != "plus" {
		t.Fatalf("unexpected plan type: %q", auth.ChatGPTPlanType)
	}
}

func TestFileOpenAIAuthStore_SaveLoadDelete(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.yaml")
	store := NewFileOpenAIAuthStore(path)

	auth := &OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		Issuer:           "https://auth.openai.com",
		ClientID:         "client",
		AccessToken:      "token",
		RefreshToken:     "refresh",
		ChatGPTAccountID: "org_123",
		ObtainedAt:       time.Now().UTC().Round(time.Second),
	}
	if err := store.Save(auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.AccessToken != auth.AccessToken {
		t.Fatalf("loaded access token mismatch")
	}
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	again, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after delete error: %v", err)
	}
	if again != nil {
		t.Fatalf("expected nil auth after delete")
	}
}

func TestParseDotEnvFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\nOPENAI_ACCESS_TOKEN=abc\nOPENAI_CHATGPT_ACCOUNT_ID='org_1'\nINVALID_LINE\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}
	values, err := parseDotEnvFile(path)
	if err != nil {
		t.Fatalf("parseDotEnvFile() error: %v", err)
	}
	if values["OPENAI_ACCESS_TOKEN"] != "abc" {
		t.Fatalf("unexpected token value")
	}
	if values["OPENAI_CHATGPT_ACCOUNT_ID"] != "org_1" {
		t.Fatalf("unexpected account value")
	}
}

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "none", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(headerJSON) + "." + enc(payloadJSON) + "." + enc([]byte("sig"))
}

func TestExtractErrorMessage(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":{"message":"bad request"}}`)
	if got := extractErrorMessage(body); got != "bad request" {
		t.Fatalf("unexpected error message: %q", got)
	}

	plain := []byte("plain text error")
	if got := extractErrorMessage(plain); got != "plain text error" {
		t.Fatalf("unexpected plain message: %q", got)
	}
}

func TestRetryAfterOrDefault(t *testing.T) {
	t.Parallel()

	if got := retryAfterOrDefault("10", time.Second); got != 10*time.Second {
		t.Fatalf("expected 10s, got %v", got)
	}

	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	got := retryAfterOrDefault(future, time.Second)
	if got <= 0 {
		t.Fatalf("expected positive duration from http date, got %v", got)
	}

	if got := retryAfterOrDefault("invalid", 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback, got %v", got)
	}
}

func TestDeviceCodeFailureIncludesAPIMessage(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/accounts/deviceauth/usercode" {
				return jsonResponse(http.StatusBadRequest, `{"error_description":"not enabled"}`), nil
			}
			return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
	})

	_, err := svc.BeginDeviceAuth(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAIAuthService_RequestDeviceCode_Retries429WithRetryAfter(t *testing.T) {
	t.Parallel()

	var calls int32
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/accounts/deviceauth/usercode" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				resp := jsonResponse(http.StatusTooManyRequests, `{"message":"rate limited"}`)
				resp.Header.Set("Retry-After", "0")
				return resp, nil
			}
			return jsonResponse(http.StatusOK, `{"device_auth_id":"dev_1","user_code":"CODE-1","interval":"1"}`), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	challenge, err := svc.BeginDeviceAuth(context.Background())
	if err != nil {
		t.Fatalf("BeginDeviceAuth() error: %v", err)
	}
	if challenge.DeviceAuthID != "dev_1" {
		t.Fatalf("unexpected challenge: %+v", challenge)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected retry to occur")
	}
}

func TestOpenAIAuthService_Refresh_Retries503(t *testing.T) {
	t.Parallel()

	var calls int32
	newIDToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh_retry",
			"chatgpt_plan_type":  "pro",
		},
	})
	newAccessToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh_retry",
		},
	})

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return jsonResponse(http.StatusServiceUnavailable, `{"message":"try later"}`), nil
			}
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"id_token":%q,"refresh_token":"refresh_new","expires_in":120}`, newAccessToken, newIDToken)), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	current := &OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		AccessToken:      "old",
		RefreshToken:     "refresh_old",
		ChatGPTAccountID: "org_refresh_retry",
	}
	updated, err := svc.Refresh(context.Background(), current)
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if updated.RefreshToken != "refresh_new" {
		t.Fatalf("expected refreshed token, got %q", updated.RefreshToken)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected retry to occur")
	}
}

func TestExchangeAuthorizationCodeForm(t *testing.T) {
	t.Parallel()

	issuer := "https://auth.openai.com"
	expectedRedirect := issuer + "/deviceauth/callback"
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			values := parseFormBody(t, bodyBytes)
			if got := values.Get("redirect_uri"); got != expectedRedirect {
				t.Fatalf("unexpected redirect uri: %q", got)
			}
			if got := values.Get("scope"); got == "" {
				t.Fatal("scope missing")
			}
			return jsonResponse(http.StatusOK, `{"access_token":"token","id_token":"","refresh_token":"r"}`), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      issuer,
		ClientID:    "client",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
	})

	_, err := svc.(*openAIAuthService).exchangeAuthorizationCode(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("exchangeAuthorizationCode() error: %v", err)
	}
}

func TestOpenAIAuthService_ResolveRefreshesExpiringStoredAuth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 21, 10, 0, 0, 0, time.UTC)
	newIDToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh_auto",
		},
	})
	newAccessToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_refresh_auto",
		},
	})

	var refreshCalls int32
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			atomic.AddInt32(&refreshCalls, 1)
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"id_token":%q,"refresh_token":"refresh_new","expires_in":3600}`, newAccessToken, newIDToken)), nil
		}),
	}

	storePath := filepath.Join(t.TempDir(), "openai_auth.yaml")
	store := NewFileOpenAIAuthStore(storePath)
	if err := store.Save(&OpenAIChatGPTAuth{
		AuthMode:          "chatgpt",
		AccessToken:       "old_access",
		RefreshToken:      "refresh_old",
		ChatGPTAccountID:  "org_refresh_auto",
		AccessTokenExpiry: now.Add(30 * time.Second),
		ObtainedAt:        now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed auth store: %v", err)
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       store,
		DotEnvPaths: []string{},
		Now: func() time.Time {
			return now
		},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	auth, err := svc.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if auth.AccessToken != newAccessToken {
		t.Fatalf("expected refreshed access token")
	}
	if atomic.LoadInt32(&refreshCalls) != 1 {
		t.Fatalf("expected one refresh call, got %d", atomic.LoadInt32(&refreshCalls))
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("Load() persisted auth: %v", err)
	}
	if persisted == nil || persisted.RefreshToken != "refresh_new" {
		t.Fatalf("expected refreshed auth persisted, got %#v", persisted)
	}
}

func TestOpenAIAuthService_ResolveRefreshSingleflight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 21, 10, 0, 0, 0, time.UTC)
	newIDToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_sf",
		},
	})
	newAccessToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "org_sf",
		},
	})

	var refreshCalls int32
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/oauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			atomic.AddInt32(&refreshCalls, 1)
			time.Sleep(20 * time.Millisecond)
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"id_token":%q,"refresh_token":"refresh_new","expires_in":3600}`, newAccessToken, newIDToken)), nil
		}),
	}

	store := NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml"))
	if err := store.Save(&OpenAIChatGPTAuth{
		AuthMode:          "chatgpt",
		AccessToken:       "old_access",
		RefreshToken:      "refresh_old",
		ChatGPTAccountID:  "org_sf",
		AccessTokenExpiry: now.Add(10 * time.Second),
		ObtainedAt:        now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       store,
		DotEnvPaths: []string{},
		Now: func() time.Time {
			return now
		},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	var wg sync.WaitGroup
	errCh := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			auth, err := svc.Resolve(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			if auth.AccessToken != newAccessToken {
				errCh <- fmt.Errorf("unexpected access token")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Resolve() error: %v", err)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("expected exactly one refresh call, got %d", got)
	}
}

func TestOpenAIAuthService_PollAuthorizationCodeHandlesSlowDownAndAccessDenied(t *testing.T) {
	t.Parallel()

	var calls int32
	var sleeps []time.Duration
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/accounts/deviceauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return jsonResponse(http.StatusBadRequest, `{"error":"slow_down"}`), nil
			}
			return jsonResponse(http.StatusOK, `{"authorization_code":"auth_code","code_verifier":"verifier","code_challenge":"challenge"}`), nil
		}),
	}

	svc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  client,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})

	challenge := &DeviceCodeChallenge{
		DeviceAuthID: "dev_1",
		UserCode:     "CODE-1",
		PollInterval: time.Second,
	}
	token, err := svc.(*openAIAuthService).pollForAuthorizationCode(context.Background(), challenge)
	if err != nil {
		t.Fatalf("pollForAuthorizationCode() error: %v", err)
	}
	if token.AuthorizationCode != "auth_code" {
		t.Fatalf("unexpected token response: %+v", token)
	}
	if len(sleeps) == 0 || sleeps[0] < time.Second {
		t.Fatalf("expected slow_down backoff sleep >= 1s, got %v", sleeps)
	}

	denyClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/accounts/deviceauth/token" {
				return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
			}
			return jsonResponse(http.StatusBadRequest, `{"error":"access_denied","error_description":"declined"}`), nil
		}),
	}
	denySvc := NewOpenAIAuthService(OpenAIAuthServiceConfig{
		Issuer:      "https://auth.openai.com",
		ClientID:    "client",
		HTTPClient:  denyClient,
		Store:       NewFileOpenAIAuthStore(filepath.Join(t.TempDir(), "openai_auth.yaml")),
		DotEnvPaths: []string{},
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})
	_, err = denySvc.(*openAIAuthService).pollForAuthorizationCode(context.Background(), challenge)
	if err == nil || !errors.Is(err, ErrAuthAccessDenied) {
		t.Fatalf("expected access denied error, got %v", err)
	}
}

func TestEncryptedFileOpenAIAuthStore_SaveLoadAndNoPlaintextLeak(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.enc.yaml")
	store := NewEncryptedFileOpenAIAuthStore(path, EncryptedFileOpenAIAuthStoreConfig{})
	auth := &OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		AccessToken:      "secret_access_token",
		RefreshToken:     "secret_refresh_token",
		ChatGPTAccountID: "org_secure",
	}
	if err := store.Save(auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.AccessToken != auth.AccessToken || loaded.RefreshToken != auth.RefreshToken {
		t.Fatalf("loaded auth mismatch: %#v", loaded)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read encrypted file: %v", err)
	}
	if strings.Contains(string(raw), auth.AccessToken) || strings.Contains(string(raw), auth.RefreshToken) {
		t.Fatal("encrypted auth file leaked plaintext tokens")
	}
}

func TestEncryptedFileOpenAIAuthStore_LoadLegacyPlaintext(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.yaml")
	legacy := OpenAIChatGPTAuth{
		AuthMode:         "chatgpt",
		AccessToken:      "legacy_access",
		ChatGPTAccountID: "org_legacy",
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy payload: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	store := NewEncryptedFileOpenAIAuthStore(path, EncryptedFileOpenAIAuthStoreConfig{})
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.AccessToken != legacy.AccessToken {
		t.Fatalf("expected legacy token, got %#v", loaded)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func parseFormBody(t *testing.T, body []byte) url.Values {
	t.Helper()
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	return values
}
