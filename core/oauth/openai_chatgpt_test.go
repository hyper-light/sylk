package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
