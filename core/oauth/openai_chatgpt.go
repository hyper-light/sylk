package oauth

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultOpenAIIssuer      = "https://auth.openai.com"
	DefaultOpenAIClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultDeviceAuthTimeout = 15 * time.Minute
	DefaultPollInterval      = 5 * time.Second
	defaultHTTPTimeout       = 30 * time.Second
	defaultOAuthMaxAttempts  = 3
)

const (
	openAIAuthModeChatGPT = "chatgpt"
)

var (
	ErrAuthNotConfigured     = errors.New("openai chatgpt auth is not configured")
	ErrDeviceAuthTimeout     = errors.New("device authorization timed out")
	ErrMissingRefreshToken   = errors.New("missing refresh token")
	ErrMissingAccessToken    = errors.New("missing access token")
	ErrMissingChatGPTAccount = errors.New("missing chatgpt account id")
)

type OpenAIChatGPTAuth struct {
	AuthMode          string    `yaml:"auth_mode" json:"auth_mode"`
	Issuer            string    `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	ClientID          string    `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	AccessToken       string    `yaml:"access_token" json:"access_token"`
	RefreshToken      string    `yaml:"refresh_token,omitempty" json:"refresh_token,omitempty"`
	IDToken           string    `yaml:"id_token,omitempty" json:"id_token,omitempty"`
	ChatGPTAccountID  string    `yaml:"chatgpt_account_id" json:"chatgpt_account_id"`
	ChatGPTPlanType   string    `yaml:"chatgpt_plan_type,omitempty" json:"chatgpt_plan_type,omitempty"`
	ObtainedAt        time.Time `yaml:"obtained_at,omitempty" json:"obtained_at,omitempty"`
	AccessTokenExpiry time.Time `yaml:"access_token_expiry,omitempty" json:"access_token_expiry,omitempty"`
}

type DeviceCodeChallenge struct {
	VerificationURL string        `json:"verification_url"`
	UserCode        string        `json:"user_code"`
	DeviceAuthID    string        `json:"device_auth_id"`
	PollInterval    time.Duration `json:"poll_interval"`
}

type OpenAIAuthStore interface {
	Save(auth *OpenAIChatGPTAuth) error
	Load() (*OpenAIChatGPTAuth, error)
	Delete() error
}

type OpenAIAuthService interface {
	BeginDeviceAuth(ctx context.Context) (*DeviceCodeChallenge, error)
	CompleteDeviceAuth(ctx context.Context, challenge *DeviceCodeChallenge, timeout time.Duration) (*OpenAIChatGPTAuth, error)
	Refresh(ctx context.Context, current *OpenAIChatGPTAuth) (*OpenAIChatGPTAuth, error)
	Resolve(ctx context.Context) (*OpenAIChatGPTAuth, error)
	Save(ctx context.Context, auth *OpenAIChatGPTAuth) error
	Load(ctx context.Context) (*OpenAIChatGPTAuth, error)
	Delete(ctx context.Context) error
}

type OpenAIAuthServiceConfig struct {
	Issuer       string
	ClientID     string
	HTTPClient   *http.Client
	Store        OpenAIAuthStore
	LookupEnv    func(string) (string, bool)
	DotEnvPaths  []string
	PollInterval time.Duration
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
}

type openAIAuthService struct {
	issuer       string
	clientID     string
	httpClient   *http.Client
	store        OpenAIAuthStore
	lookupEnv    func(string) (string, bool)
	dotEnvPaths  []string
	pollInterval time.Duration
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
}

func NewOpenAIAuthService(cfg OpenAIAuthServiceConfig) OpenAIAuthService {
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = DefaultOpenAIIssuer
	}
	issuer = strings.TrimRight(issuer, "/")

	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		clientID = DefaultOpenAIClientID
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	store := cfg.Store
	if store == nil {
		store = NewFileOpenAIAuthStore(DefaultOpenAIAuthPath())
	}

	lookupEnv := cfg.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	sleep := cfg.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}

	return &openAIAuthService{
		issuer:       issuer,
		clientID:     clientID,
		httpClient:   httpClient,
		store:        store,
		lookupEnv:    lookupEnv,
		dotEnvPaths:  append([]string(nil), cfg.DotEnvPaths...),
		pollInterval: pollInterval,
		now:          now,
		sleep:        sleep,
	}
}

func (s *openAIAuthService) BeginDeviceAuth(ctx context.Context) (*DeviceCodeChallenge, error) {
	challenge, err := s.requestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}
	return challenge, nil
}

func (s *openAIAuthService) CompleteDeviceAuth(ctx context.Context, challenge *DeviceCodeChallenge, timeout time.Duration) (*OpenAIChatGPTAuth, error) {
	if challenge == nil {
		return nil, fmt.Errorf("device challenge is required")
	}
	if strings.TrimSpace(challenge.DeviceAuthID) == "" {
		return nil, fmt.Errorf("device challenge missing device_auth_id")
	}
	if strings.TrimSpace(challenge.UserCode) == "" {
		return nil, fmt.Errorf("device challenge missing user_code")
	}
	if timeout <= 0 {
		timeout = DefaultDeviceAuthTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poll, err := s.pollForAuthorizationCode(ctx, challenge)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrDeviceAuthTimeout
		}
		return nil, err
	}

	tokens, err := s.exchangeAuthorizationCode(ctx, poll.AuthorizationCode, poll.CodeVerifier)
	if err != nil {
		return nil, err
	}

	auth, err := s.tokensToAuth(tokens)
	if err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) Refresh(ctx context.Context, current *OpenAIChatGPTAuth) (*OpenAIChatGPTAuth, error) {
	if current == nil {
		return nil, ErrAuthNotConfigured
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		return nil, ErrMissingRefreshToken
	}

	tokens, err := s.refreshTokens(ctx, current.RefreshToken)
	if err != nil {
		return nil, err
	}

	updated := *current
	if strings.TrimSpace(tokens.AccessToken) != "" {
		updated.AccessToken = tokens.AccessToken
	}
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		updated.RefreshToken = tokens.RefreshToken
	}
	if strings.TrimSpace(tokens.IDToken) != "" {
		updated.IDToken = tokens.IDToken
	}
	updated.AuthMode = openAIAuthModeChatGPT
	updated.Issuer = s.issuer
	updated.ClientID = s.clientID
	updated.ObtainedAt = s.now().UTC()
	if tokens.ExpiresIn > 0 {
		updated.AccessTokenExpiry = s.now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}

	accountID := firstNonEmpty(
		claimsAccountID(updated.IDToken),
		claimsAccountID(updated.AccessToken),
		updated.ChatGPTAccountID,
	)
	if strings.TrimSpace(accountID) == "" {
		return nil, ErrMissingChatGPTAccount
	}
	updated.ChatGPTAccountID = accountID

	planType := firstNonEmpty(
		claimsPlanType(updated.IDToken),
		claimsPlanType(updated.AccessToken),
		updated.ChatGPTPlanType,
	)
	updated.ChatGPTPlanType = planType

	if err := validateAuth(&updated); err != nil {
		return nil, err
	}

	return &updated, nil
}

func (s *openAIAuthService) Resolve(ctx context.Context) (*OpenAIChatGPTAuth, error) {
	_ = ctx

	if auth, err := s.resolveFromEnv(); err == nil {
		return auth, nil
	} else if !errors.Is(err, ErrAuthNotConfigured) {
		return nil, err
	}

	if auth, err := s.resolveFromDotEnv(); err == nil {
		return auth, nil
	} else if !errors.Is(err, ErrAuthNotConfigured) {
		return nil, err
	}

	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrAuthNotConfigured
	}
	if auth.AuthMode == "" {
		auth.AuthMode = openAIAuthModeChatGPT
	}
	if auth.Issuer == "" {
		auth.Issuer = s.issuer
	}
	if auth.ClientID == "" {
		auth.ClientID = s.clientID
	}
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) Save(ctx context.Context, auth *OpenAIChatGPTAuth) error {
	_ = ctx
	if auth == nil {
		return fmt.Errorf("auth payload is required")
	}
	auth.AuthMode = openAIAuthModeChatGPT
	if strings.TrimSpace(auth.Issuer) == "" {
		auth.Issuer = s.issuer
	}
	if strings.TrimSpace(auth.ClientID) == "" {
		auth.ClientID = s.clientID
	}
	if auth.ObtainedAt.IsZero() {
		auth.ObtainedAt = s.now().UTC()
	}
	if err := validateAuth(auth); err != nil {
		return err
	}
	return s.store.Save(auth)
}

func (s *openAIAuthService) Load(ctx context.Context) (*OpenAIChatGPTAuth, error) {
	_ = ctx
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrAuthNotConfigured
	}
	if auth.AuthMode == "" {
		auth.AuthMode = openAIAuthModeChatGPT
	}
	if auth.Issuer == "" {
		auth.Issuer = s.issuer
	}
	if auth.ClientID == "" {
		auth.ClientID = s.clientID
	}
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) Delete(ctx context.Context) error {
	_ = ctx
	return s.store.Delete()
}

func (s *openAIAuthService) requestDeviceCode(ctx context.Context) (*DeviceCodeChallenge, error) {
	endpoint := s.issuer + "/api/accounts/deviceauth/usercode"
	payload := map[string]string{"client_id": s.clientID}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	respBody, _, err := s.doRetriableRequest(ctx, endpoint, "application/json", body)
	if err != nil {
		return nil, fmt.Errorf("request device code failed: %w", err)
	}

	var decoded deviceCodeResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode device code response: %w", err)
	}

	userCode := firstNonEmpty(decoded.UserCode, decoded.UserCodeAlt)
	if strings.TrimSpace(decoded.DeviceAuthID) == "" || strings.TrimSpace(userCode) == "" {
		return nil, fmt.Errorf("device code response missing required fields")
	}

	pollInterval := decoded.IntervalSeconds.Duration()
	if pollInterval <= 0 {
		pollInterval = s.pollInterval
	}

	return &DeviceCodeChallenge{
		VerificationURL: s.issuer + "/codex/device",
		UserCode:        userCode,
		DeviceAuthID:    decoded.DeviceAuthID,
		PollInterval:    pollInterval,
	}, nil
}

func (s *openAIAuthService) pollForAuthorizationCode(ctx context.Context, challenge *DeviceCodeChallenge) (*deviceTokenResponse, error) {
	endpoint := s.issuer + "/api/accounts/deviceauth/token"
	interval := challenge.PollInterval
	if interval <= 0 {
		interval = s.pollInterval
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		payload := map[string]string{
			"device_auth_id": challenge.DeviceAuthID,
			"user_code":      challenge.UserCode,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var decoded deviceTokenResponse
			if err := json.Unmarshal(respBody, &decoded); err != nil {
				return nil, fmt.Errorf("decode device token response: %w", err)
			}
			if strings.TrimSpace(decoded.AuthorizationCode) == "" || strings.TrimSpace(decoded.CodeVerifier) == "" {
				return nil, fmt.Errorf("device token response missing required fields")
			}
			return &decoded, nil
		case http.StatusForbidden, http.StatusNotFound:
			if err := s.sleep(ctx, interval); err != nil {
				return nil, err
			}
			continue
		case http.StatusTooManyRequests:
			wait := retryAfterOrDefault(resp.Header.Get("Retry-After"), interval)
			if err := s.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		default:
			return nil, fmt.Errorf("device token poll failed: %w", statusError(resp.StatusCode, respBody))
		}
	}
}

func (s *openAIAuthService) exchangeAuthorizationCode(ctx context.Context, code, codeVerifier string) (*oauthTokenResponse, error) {
	endpoint := s.issuer + "/oauth/token"
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.issuer+"/deviceauth/callback")
	form.Set("client_id", s.clientID)
	form.Set("code_verifier", codeVerifier)
	form.Set("scope", "openid profile email offline_access")

	respBody, _, err := s.doRetriableRequest(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code failed: %w", err)
	}

	var decoded oauthTokenResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode oauth token response: %w", err)
	}
	if strings.TrimSpace(decoded.AccessToken) == "" {
		return nil, ErrMissingAccessToken
	}
	return &decoded, nil
}

func (s *openAIAuthService) refreshTokens(ctx context.Context, refreshToken string) (*oauthTokenResponse, error) {
	endpoint := s.issuer + "/oauth/token"
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", s.clientID)
	form.Set("scope", "openid profile email")

	respBody, _, err := s.doRetriableRequest(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("refresh token failed: %w", err)
	}

	var decoded oauthTokenResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}
	if strings.TrimSpace(decoded.AccessToken) == "" {
		return nil, ErrMissingAccessToken
	}
	return &decoded, nil
}

func (s *openAIAuthService) tokensToAuth(tokens *oauthTokenResponse) (*OpenAIChatGPTAuth, error) {
	if tokens == nil {
		return nil, fmt.Errorf("token response is required")
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return nil, ErrMissingAccessToken
	}

	accountID := firstNonEmpty(claimsAccountID(tokens.IDToken), claimsAccountID(tokens.AccessToken))
	if strings.TrimSpace(accountID) == "" {
		return nil, ErrMissingChatGPTAccount
	}

	planType := firstNonEmpty(claimsPlanType(tokens.IDToken), claimsPlanType(tokens.AccessToken))

	auth := &OpenAIChatGPTAuth{
		AuthMode:         openAIAuthModeChatGPT,
		Issuer:           s.issuer,
		ClientID:         s.clientID,
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		IDToken:          tokens.IDToken,
		ChatGPTAccountID: accountID,
		ChatGPTPlanType:  planType,
		ObtainedAt:       s.now().UTC(),
	}
	if tokens.ExpiresIn > 0 {
		auth.AccessTokenExpiry = s.now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) resolveFromEnv() (*OpenAIChatGPTAuth, error) {
	accessToken := firstEnvValue(s.lookupEnv,
		"OPENAI_ACCESS_TOKEN",
		"CHATGPT_ACCESS_TOKEN",
	)
	accountID := firstEnvValue(s.lookupEnv,
		"OPENAI_CHATGPT_ACCOUNT_ID",
		"CHATGPT_ACCOUNT_ID",
	)
	if accessToken == "" && accountID == "" {
		return nil, ErrAuthNotConfigured
	}
	if accessToken == "" {
		return nil, ErrMissingAccessToken
	}
	if accountID == "" {
		return nil, ErrMissingChatGPTAccount
	}

	auth := &OpenAIChatGPTAuth{
		AuthMode:         openAIAuthModeChatGPT,
		Issuer:           firstNonEmpty(firstEnvValue(s.lookupEnv, "OPENAI_AUTH_ISSUER"), s.issuer),
		ClientID:         firstNonEmpty(firstEnvValue(s.lookupEnv, "OPENAI_AUTH_CLIENT_ID"), s.clientID),
		AccessToken:      accessToken,
		RefreshToken:     firstEnvValue(s.lookupEnv, "OPENAI_REFRESH_TOKEN", "CHATGPT_REFRESH_TOKEN"),
		IDToken:          firstEnvValue(s.lookupEnv, "OPENAI_ID_TOKEN", "CHATGPT_ID_TOKEN"),
		ChatGPTAccountID: accountID,
		ChatGPTPlanType:  firstEnvValue(s.lookupEnv, "OPENAI_CHATGPT_PLAN_TYPE", "CHATGPT_PLAN_TYPE"),
	}
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) resolveFromDotEnv() (*OpenAIChatGPTAuth, error) {
	paths := s.dotEnvPaths
	if len(paths) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, ErrAuthNotConfigured
		}
		paths = []string{filepath.Join(cwd, ".env"), filepath.Join(cwd, ".env.local")}
	}

	values := make(map[string]string)
	for _, p := range paths {
		parsed, err := parseDotEnvFile(p)
		if err != nil {
			continue
		}
		for k, v := range parsed {
			if _, exists := values[k]; !exists {
				values[k] = v
			}
		}
	}

	lookup := func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}

	accessToken := firstEnvValue(lookup, "OPENAI_ACCESS_TOKEN", "CHATGPT_ACCESS_TOKEN")
	accountID := firstEnvValue(lookup, "OPENAI_CHATGPT_ACCOUNT_ID", "CHATGPT_ACCOUNT_ID")
	if accessToken == "" && accountID == "" {
		return nil, ErrAuthNotConfigured
	}
	if accessToken == "" {
		return nil, ErrMissingAccessToken
	}
	if accountID == "" {
		return nil, ErrMissingChatGPTAccount
	}

	auth := &OpenAIChatGPTAuth{
		AuthMode:         openAIAuthModeChatGPT,
		Issuer:           firstNonEmpty(firstEnvValue(lookup, "OPENAI_AUTH_ISSUER"), s.issuer),
		ClientID:         firstNonEmpty(firstEnvValue(lookup, "OPENAI_AUTH_CLIENT_ID"), s.clientID),
		AccessToken:      accessToken,
		RefreshToken:     firstEnvValue(lookup, "OPENAI_REFRESH_TOKEN", "CHATGPT_REFRESH_TOKEN"),
		IDToken:          firstEnvValue(lookup, "OPENAI_ID_TOKEN", "CHATGPT_ID_TOKEN"),
		ChatGPTAccountID: accountID,
		ChatGPTPlanType:  firstEnvValue(lookup, "OPENAI_CHATGPT_PLAN_TYPE", "CHATGPT_PLAN_TYPE"),
	}
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func validateAuth(auth *OpenAIChatGPTAuth) error {
	if auth == nil {
		return ErrAuthNotConfigured
	}
	if strings.TrimSpace(auth.AccessToken) == "" {
		return ErrMissingAccessToken
	}
	if strings.TrimSpace(auth.ChatGPTAccountID) == "" {
		return ErrMissingChatGPTAccount
	}
	return nil
}

func firstEnvValue(lookup func(string) (string, bool), keys ...string) string {
	for _, key := range keys {
		if v, ok := lookup(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func retryAfterOrDefault(value string, fallback time.Duration) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(trimmed); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(trimmed); err == nil {
		until := time.Until(when)
		if until > 0 {
			return until
		}
	}
	return fallback
}

func shouldRetryOAuthStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (s *openAIAuthService) doRetriableRequest(
	ctx context.Context,
	endpoint string,
	contentType string,
	body []byte,
) ([]byte, http.Header, error) {
	backoff := s.pollInterval
	if backoff <= 0 {
		backoff = DefaultPollInterval
	}

	var lastErr error
	for attempt := 0; attempt < defaultOAuthMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "application/json")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < defaultOAuthMaxAttempts {
				wait := backoffForAttempt(backoff, attempt)
				if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
					return nil, nil, sleepErr
				}
				continue
			}
			return nil, nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt+1 < defaultOAuthMaxAttempts {
				wait := backoffForAttempt(backoff, attempt)
				if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
					return nil, nil, sleepErr
				}
				continue
			}
			return nil, nil, readErr
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, resp.Header, nil
		}

		lastErr = statusError(resp.StatusCode, respBody)
		if attempt+1 >= defaultOAuthMaxAttempts || !shouldRetryOAuthStatus(resp.StatusCode) {
			return nil, resp.Header, lastErr
		}
		wait := retryAfterOrDefault(resp.Header.Get("Retry-After"), backoffForAttempt(backoff, attempt))
		if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
			return nil, resp.Header, sleepErr
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request failed after retries")
	}
	return nil, nil, lastErr
}

func backoffForAttempt(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if attempt < 0 {
		attempt = 0
	}
	backoff := base << attempt
	max := 30 * time.Second
	if backoff > max {
		return max
	}
	return backoff
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func statusError(status int, body []byte) error {
	message := extractErrorMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	return fmt.Errorf("status %d: %s", status, message)
}

func extractErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		if len(trimmed) > 300 {
			return trimmed[:300] + "..."
		}
		return trimmed
	}

	if v := mapString(payload, "error_description"); v != "" {
		return v
	}
	if v := mapString(payload, "message"); v != "" {
		return v
	}
	if rawErr, ok := payload["error"]; ok {
		switch e := rawErr.(type) {
		case string:
			if strings.TrimSpace(e) != "" {
				return e
			}
		case map[string]any:
			if v := mapString(e, "message"); v != "" {
				return v
			}
			if v := mapString(e, "code"); v != "" {
				return v
			}
		}
	}

	if len(trimmed) > 300 {
		return trimmed[:300] + "..."
	}
	return trimmed
}

func mapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func claimsAccountID(jwt string) string {
	claims, err := parseJWTClaims(jwt)
	if err != nil {
		return ""
	}
	if v := mapString(claims, "chatgpt_account_id"); v != "" {
		return v
	}
	if authObj, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if v := mapString(authObj, "chatgpt_account_id"); v != "" {
			return v
		}
	}
	if v := mapString(claims, "organization_id"); v != "" {
		return v
	}
	return ""
}

func claimsPlanType(jwt string) string {
	claims, err := parseJWTClaims(jwt)
	if err != nil {
		return ""
	}
	if v := mapString(claims, "chatgpt_plan_type"); v != "" {
		return v
	}
	if authObj, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if v := mapString(authObj, "chatgpt_plan_type"); v != "" {
			return v
		}
	}
	return ""
}

func parseJWTClaims(jwt string) (map[string]any, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func DefaultOpenAIAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "openai_chatgpt_auth.yaml")
}

type FileOpenAIAuthStore struct {
	path string
}

func NewFileOpenAIAuthStore(path string) *FileOpenAIAuthStore {
	return &FileOpenAIAuthStore{path: path}
}

func (s *FileOpenAIAuthStore) Save(auth *OpenAIChatGPTAuth) error {
	if auth == nil {
		return fmt.Errorf("auth payload is required")
	}
	if strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("auth store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("creating auth directory: %w", err)
	}

	encoded, err := yaml.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp.%d", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, encoded, 0600); err != nil {
		return fmt.Errorf("write temp auth file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("persist auth file: %w", err)
	}
	return nil
}

func (s *FileOpenAIAuthStore) Load() (*OpenAIChatGPTAuth, error) {
	if strings.TrimSpace(s.path) == "" {
		return nil, fmt.Errorf("auth store path is empty")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read auth file: %w", err)
	}
	var auth OpenAIChatGPTAuth
	if err := yaml.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}
	return &auth, nil
}

func (s *FileOpenAIAuthStore) Delete() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type deviceCodeResponse struct {
	DeviceAuthID    string          `json:"device_auth_id"`
	UserCode        string          `json:"user_code"`
	UserCodeAlt     string          `json:"usercode"`
	IntervalSeconds oauthIntervalIn `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type oauthIntervalIn struct {
	seconds int64
}

func (o *oauthIntervalIn) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		o.seconds = 0
		return nil
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			o.seconds = 0
			return nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid interval value %q: %w", s, err)
		}
		o.seconds = v
		return nil
	}

	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		f, ferr := strconv.ParseFloat(trimmed, 64)
		if ferr != nil {
			return err
		}
		o.seconds = int64(f)
		return nil
	}
	o.seconds = n
	return nil
}

func (o oauthIntervalIn) Duration() time.Duration {
	if o.seconds <= 0 {
		return 0
	}
	return time.Duration(o.seconds) * time.Second
}

func parseDotEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, "\"'")
		if key == "" || val == "" {
			continue
		}
		if _, exists := result[key]; !exists {
			result[key] = val
		}
	}
	return result, nil
}
