package oauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

const (
	DefaultOpenAIIssuer      = "https://auth.openai.com"
	DefaultOpenAIClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultDeviceAuthTimeout = 15 * time.Minute
	DefaultPollInterval      = 5 * time.Second
	DefaultRefreshSkew       = 2 * time.Minute
	defaultHTTPTimeout       = 30 * time.Second
	defaultOAuthMaxAttempts  = 3
	defaultOAuthRetryBudget  = 45 * time.Second
	defaultBackoffMax        = 30 * time.Second
	defaultUserAgent         = "sylk/1.0 (+https://github.com/adalundhe/sylk)"
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
	ErrAuthAccessDenied      = errors.New("openai chatgpt device authorization denied")
	ErrAuthExpiredToken      = errors.New("openai chatgpt device authorization expired")
	ErrAuthInvalidGrant      = errors.New("openai chatgpt refresh token invalid")
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

type DeviceAuthSession struct {
	Challenge *DeviceCodeChallenge
	Results   <-chan DeviceAuthResult
	Cancel    context.CancelFunc
}

type DeviceAuthResult struct {
	Auth *OpenAIChatGPTAuth
	Err  error
}

func StartDeviceAuthSession(
	ctx context.Context,
	service OpenAIAuthService,
	timeout time.Duration,
) (*DeviceAuthSession, error) {
	if service == nil {
		return nil, fmt.Errorf("openai auth service is required")
	}
	challenge, err := service.BeginDeviceAuth(ctx)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	results := make(chan DeviceAuthResult, 1)
	go runDeviceAuthSession(sessionCtx, service, challenge, timeout, results)
	return &DeviceAuthSession{
		Challenge: challenge,
		Results:   results,
		Cancel:    cancel,
	}, nil
}

func runDeviceAuthSession(
	ctx context.Context,
	service OpenAIAuthService,
	challenge *DeviceCodeChallenge,
	timeout time.Duration,
	results chan<- DeviceAuthResult,
) {
	defer close(results)
	results <- completeDeviceAuthSession(ctx, service, challenge, timeout)
}

func completeDeviceAuthSession(
	ctx context.Context,
	service OpenAIAuthService,
	challenge *DeviceCodeChallenge,
	timeout time.Duration,
) DeviceAuthResult {
	auth, err := service.CompleteDeviceAuth(ctx, challenge, timeout)
	if err != nil {
		return DeviceAuthResult{Err: err}
	}
	if err := service.Save(ctx, auth); err != nil {
		return DeviceAuthResult{Err: err}
	}
	return DeviceAuthResult{Auth: auth}
}

type OpenAIAuthServiceConfig struct {
	Issuer       string
	ClientID     string
	HTTPClient   *http.Client
	Store        OpenAIAuthStore
	LookupEnv    func(string) (string, bool)
	DotEnvPaths  []string
	PollInterval time.Duration
	RefreshSkew  time.Duration
	RetryBudget  time.Duration
	UserAgent    string
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
	RandFloat64  func() float64
}

type openAIAuthService struct {
	issuer       string
	clientID     string
	httpClient   *http.Client
	store        OpenAIAuthStore
	lookupEnv    func(string) (string, bool)
	dotEnvPaths  []string
	pollInterval time.Duration
	refreshSkew  time.Duration
	retryBudget  time.Duration
	userAgent    string
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	randFloat64  func() float64

	refreshMu    sync.Mutex
	refreshGroup singleflight.Group
}

func NewOpenAIAuthService(cfg OpenAIAuthServiceConfig) OpenAIAuthService {
	lookupEnv := resolveEnvLookup(cfg.LookupEnv)
	return &openAIAuthService{
		issuer:       resolveServiceIssuer(cfg.Issuer),
		clientID:     resolveServiceClientID(cfg.ClientID),
		httpClient:   resolveServiceHTTPClient(cfg.HTTPClient),
		store:        resolveServiceStore(cfg.Store, lookupEnv),
		lookupEnv:    lookupEnv,
		dotEnvPaths:  append([]string(nil), cfg.DotEnvPaths...),
		pollInterval: resolvePositiveDuration(cfg.PollInterval, DefaultPollInterval),
		refreshSkew:  resolvePositiveDuration(cfg.RefreshSkew, DefaultRefreshSkew),
		retryBudget:  resolvePositiveDuration(cfg.RetryBudget, defaultOAuthRetryBudget),
		userAgent:    resolveServiceUserAgent(cfg.UserAgent),
		now:          resolveNow(cfg.Now),
		sleep:        resolveSleep(cfg.Sleep),
		randFloat64:  resolveRandomFloat64(cfg.RandFloat64),
	}
}

func resolveServiceIssuer(issuer string) string {
	trimmed := strings.TrimSpace(issuer)
	if trimmed == "" {
		trimmed = DefaultOpenAIIssuer
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveServiceClientID(clientID string) string {
	trimmed := strings.TrimSpace(clientID)
	if trimmed != "" {
		return trimmed
	}
	return DefaultOpenAIClientID
}

func resolveEnvLookup(lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup != nil {
		return lookup
	}
	return os.LookupEnv
}

func resolveServiceHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return newDefaultHTTPClient(defaultHTTPTimeout)
}

func resolveServiceStore(store OpenAIAuthStore, lookupEnv func(string) (string, bool)) OpenAIAuthStore {
	if store != nil {
		return store
	}
	return NewDefaultOpenAIAuthStore(DefaultOpenAIAuthPath(), lookupEnv)
}

func resolvePositiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func resolveServiceUserAgent(userAgent string) string {
	trimmed := strings.TrimSpace(userAgent)
	if trimmed != "" {
		return trimmed
	}
	return defaultUserAgent
}

func resolveNow(now func() time.Time) func() time.Time {
	if now != nil {
		return now
	}
	return time.Now
}

func resolveSleep(sleep func(context.Context, time.Duration) error) func(context.Context, time.Duration) error {
	if sleep != nil {
		return sleep
	}
	return defaultSleep
}

func resolveRandomFloat64(randFloat64 func() float64) func() float64 {
	if randFloat64 != nil {
		return randFloat64
	}
	return mathrand.Float64
}

func (s *openAIAuthService) BeginDeviceAuth(ctx context.Context) (*DeviceCodeChallenge, error) {
	challenge, err := s.requestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}
	return challenge, nil
}

func (s *openAIAuthService) CompleteDeviceAuth(ctx context.Context, challenge *DeviceCodeChallenge, timeout time.Duration) (*OpenAIChatGPTAuth, error) {
	if err := validateDeviceCodeChallenge(challenge); err != nil {
		return nil, err
	}
	timeout = resolveDeviceAuthTimeout(timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	poll, err := s.pollAuthorizationCodeWithTimeout(ctx, challenge)
	if err != nil {
		return nil, err
	}
	return s.exchangePolledAuthorizationCode(ctx, poll)
}

func validateDeviceCodeChallenge(challenge *DeviceCodeChallenge) error {
	if challenge == nil {
		return fmt.Errorf("device challenge is required")
	}
	if strings.TrimSpace(challenge.DeviceAuthID) == "" {
		return fmt.Errorf("device challenge missing device_auth_id")
	}
	if strings.TrimSpace(challenge.UserCode) == "" {
		return fmt.Errorf("device challenge missing user_code")
	}
	return nil
}

func resolveDeviceAuthTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return DefaultDeviceAuthTimeout
}

func (s *openAIAuthService) pollAuthorizationCodeWithTimeout(
	ctx context.Context,
	challenge *DeviceCodeChallenge,
) (*deviceTokenResponse, error) {
	poll, err := s.pollForAuthorizationCode(ctx, challenge)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrDeviceAuthTimeout
	}
	return poll, err
}

func (s *openAIAuthService) exchangePolledAuthorizationCode(
	ctx context.Context,
	poll *deviceTokenResponse,
) (*OpenAIChatGPTAuth, error) {
	tokens, err := s.exchangeAuthorizationCode(ctx, poll.AuthorizationCode, poll.CodeVerifier)
	if err != nil {
		return nil, err
	}
	return s.tokensToAuth(tokens)
}

func (s *openAIAuthService) Refresh(ctx context.Context, current *OpenAIChatGPTAuth) (*OpenAIChatGPTAuth, error) {
	if err := validateRefreshInput(current); err != nil {
		return nil, err
	}
	tokens, err := s.refreshTokens(ctx, current.RefreshToken)
	if err != nil {
		return nil, err
	}
	updated := s.mergeRefreshedAuth(current, tokens)
	if err := s.enrichRefreshedAuth(&updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func validateRefreshInput(current *OpenAIChatGPTAuth) error {
	if current == nil {
		return ErrAuthNotConfigured
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		return ErrMissingRefreshToken
	}
	return nil
}

func (s *openAIAuthService) mergeRefreshedAuth(
	current *OpenAIChatGPTAuth,
	tokens *oauthTokenResponse,
) OpenAIChatGPTAuth {
	updated := *current
	updated.AccessToken = chooseTokenValue(tokens.AccessToken, updated.AccessToken)
	updated.RefreshToken = chooseTokenValue(tokens.RefreshToken, updated.RefreshToken)
	updated.IDToken = chooseTokenValue(tokens.IDToken, updated.IDToken)
	updated.AuthMode = openAIAuthModeChatGPT
	updated.Issuer = s.issuer
	updated.ClientID = s.clientID
	updated.ObtainedAt = s.now().UTC()
	if tokens.ExpiresIn > 0 {
		updated.AccessTokenExpiry = s.now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	return updated
}

func chooseTokenValue(candidate string, fallback string) string {
	if strings.TrimSpace(candidate) != "" {
		return candidate
	}
	return fallback
}

func (s *openAIAuthService) enrichRefreshedAuth(updated *OpenAIChatGPTAuth) error {
	accountID := firstNonEmpty(
		claimsAccountID(updated.IDToken),
		claimsAccountID(updated.AccessToken),
		updated.ChatGPTAccountID,
	)
	if strings.TrimSpace(accountID) == "" {
		return ErrMissingChatGPTAccount
	}
	updated.ChatGPTAccountID = accountID
	updated.ChatGPTPlanType = resolvePlanType(updated.IDToken, updated.AccessToken, updated.ChatGPTPlanType)
	return validateAuth(updated)
}

func resolvePlanType(idToken string, accessToken string, fallback string) string {
	return firstNonEmpty(claimsPlanType(idToken), claimsPlanType(accessToken), fallback)
}

func (s *openAIAuthService) Resolve(ctx context.Context) (*OpenAIChatGPTAuth, error) {
	auth, resolved, err := s.resolveFromRuntimeSources(ctx)
	if resolved || err != nil {
		return auth, err
	}
	return s.resolveFromStore(ctx)
}

func (s *openAIAuthService) resolveFromRuntimeSources(
	ctx context.Context,
) (*OpenAIChatGPTAuth, bool, error) {
	auth, resolved, err := s.resolveFromSource(ctx, s.resolveFromEnv, false)
	if resolved || err != nil {
		return auth, resolved, err
	}
	return s.resolveFromSource(ctx, s.resolveFromDotEnv, false)
}

func (s *openAIAuthService) resolveFromSource(
	ctx context.Context,
	source func() (*OpenAIChatGPTAuth, error),
	persist bool,
) (*OpenAIChatGPTAuth, bool, error) {
	auth, err := source()
	if err == nil {
		refreshed, refreshErr := s.maybeRefreshResolvedAuth(ctx, auth, persist)
		return refreshed, true, refreshErr
	}
	if errors.Is(err, ErrAuthNotConfigured) {
		return nil, false, nil
	}
	return nil, true, err
}

func (s *openAIAuthService) resolveFromStore(ctx context.Context) (*OpenAIChatGPTAuth, error) {
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrAuthNotConfigured
	}
	s.applyAuthDefaults(auth)
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return s.maybeRefreshResolvedAuth(ctx, auth, true)
}

func (s *openAIAuthService) Save(ctx context.Context, auth *OpenAIChatGPTAuth) error {
	_ = ctx
	if auth == nil {
		return fmt.Errorf("auth payload is required")
	}
	s.applyAuthDefaults(auth)
	s.applyObtainedAt(auth)
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
	s.applyAuthDefaults(auth)
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *openAIAuthService) applyAuthDefaults(auth *OpenAIChatGPTAuth) {
	if auth == nil {
		return
	}
	auth.AuthMode = openAIAuthModeChatGPT
	if strings.TrimSpace(auth.Issuer) == "" {
		auth.Issuer = s.issuer
	}
	if strings.TrimSpace(auth.ClientID) == "" {
		auth.ClientID = s.clientID
	}
}

func (s *openAIAuthService) applyObtainedAt(auth *OpenAIChatGPTAuth) {
	if auth == nil {
		return
	}
	if auth.ObtainedAt.IsZero() {
		auth.ObtainedAt = s.now().UTC()
	}
}

func (s *openAIAuthService) Delete(ctx context.Context) error {
	_ = ctx
	return s.store.Delete()
}

func (s *openAIAuthService) maybeRefreshResolvedAuth(
	ctx context.Context,
	auth *OpenAIChatGPTAuth,
	persist bool,
) (*OpenAIChatGPTAuth, error) {
	if auth == nil {
		return nil, ErrAuthNotConfigured
	}
	if !s.shouldRefresh(auth) {
		return auth, nil
	}
	if strings.TrimSpace(auth.RefreshToken) == "" {
		return auth, nil
	}
	return s.refreshWithSingleflight(ctx, auth, persist)
}

func (s *openAIAuthService) shouldRefresh(auth *OpenAIChatGPTAuth) bool {
	if auth == nil || auth.AccessTokenExpiry.IsZero() {
		return false
	}
	now := s.now().UTC()
	return now.Add(s.refreshSkew).After(auth.AccessTokenExpiry)
}

func (s *openAIAuthService) refreshWithSingleflight(
	ctx context.Context,
	auth *OpenAIChatGPTAuth,
	persist bool,
) (*OpenAIChatGPTAuth, error) {
	if auth == nil {
		return nil, ErrAuthNotConfigured
	}
	refreshToken, ok := normalizedRefreshToken(auth)
	if !ok {
		return auth, nil
	}
	ch := s.refreshGroup.DoChan(refreshSingleflightKey(refreshToken), func() (any, error) {
		return s.executeSingleflightRefresh(ctx, auth, persist)
	})
	return s.awaitSingleflightRefresh(ctx, ch)
}

func normalizedRefreshToken(auth *OpenAIChatGPTAuth) (string, bool) {
	refreshToken := strings.TrimSpace(auth.RefreshToken)
	if refreshToken == "" {
		return "", false
	}
	return refreshToken, true
}

func refreshSingleflightKey(refreshToken string) string {
	return "refresh:" + refreshToken
}

func (s *openAIAuthService) executeSingleflightRefresh(
	ctx context.Context,
	auth *OpenAIChatGPTAuth,
	persist bool,
) (*OpenAIChatGPTAuth, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	current := *auth
	updated, err := s.Refresh(ctx, &current)
	if err != nil {
		return nil, s.handleSingleflightRefreshError(err, persist)
	}
	if persist {
		if saveErr := s.store.Save(updated); saveErr != nil {
			return nil, saveErr
		}
	}
	return updated, nil
}

func (s *openAIAuthService) handleSingleflightRefreshError(err error, persist bool) error {
	return err
}

func (s *openAIAuthService) awaitSingleflightRefresh(
	ctx context.Context,
	ch <-chan singleflight.Result,
) (*OpenAIChatGPTAuth, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		return refreshResultAuth(result)
	}
}

func refreshResultAuth(result singleflight.Result) (*OpenAIChatGPTAuth, error) {
	if result.Err != nil {
		return nil, result.Err
	}
	updated, _ := result.Val.(*OpenAIChatGPTAuth)
	if updated == nil {
		return nil, ErrAuthNotConfigured
	}
	return updated, nil
}

func IsInvalidGrant(err error) bool {
	var apiErr *oauthHTTPError
	if errors.As(err, &apiErr) {
		if apiErr.Code == "invalid_grant" {
			return true
		}
		msg := strings.ToLower(apiErr.Message)
		return apiErr.StatusCode == http.StatusBadRequest &&
			(strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "refresh token"))
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant")
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
	interval := s.resolvePollInterval(challenge)
	for {
		outcome, err := s.pollDeviceAuthOnce(ctx, endpoint, challenge, interval)
		if err != nil {
			return nil, err
		}
		if outcome.err != nil {
			return nil, outcome.err
		}
		if outcome.token != nil {
			return outcome.token, nil
		}
		interval = outcome.nextInterval
		if err := s.sleep(ctx, outcome.wait); err != nil {
			return nil, err
		}
	}
}

func (s *openAIAuthService) resolvePollInterval(challenge *DeviceCodeChallenge) time.Duration {
	interval := DefaultPollInterval
	if s.pollInterval > 0 {
		interval = s.pollInterval
	}
	if challenge != nil && challenge.PollInterval > 0 {
		interval = challenge.PollInterval
	}
	return interval
}

func (s *openAIAuthService) pollDeviceAuthOnce(
	ctx context.Context,
	endpoint string,
	challenge *DeviceCodeChallenge,
	interval time.Duration,
) (devicePollOutcome, error) {
	if err := ctx.Err(); err != nil {
		return devicePollOutcome{}, err
	}
	body, err := s.marshalDevicePollPayload(challenge)
	if err != nil {
		return devicePollOutcome{}, err
	}
	resp, respBody, err := s.postDevicePoll(ctx, endpoint, body)
	if err != nil {
		return s.networkPollOutcome(interval), nil
	}
	return s.evaluateDevicePollResponse(resp, respBody, interval), nil
}

func (s *openAIAuthService) networkPollOutcome(interval time.Duration) devicePollOutcome {
	wait := fullJitterBackoff(interval, defaultBackoffMax, 0, s.randFloat64())
	return devicePollOutcome{wait: wait, nextInterval: interval}
}

type devicePollOutcome struct {
	token        *deviceTokenResponse
	wait         time.Duration
	nextInterval time.Duration
	err          error
}

func (s *openAIAuthService) marshalDevicePollPayload(challenge *DeviceCodeChallenge) ([]byte, error) {
	payload := map[string]string{
		"device_auth_id": challenge.DeviceAuthID,
		"user_code":      challenge.UserCode,
	}
	return json.Marshal(payload)
}

func (s *openAIAuthService) postDevicePoll(ctx context.Context, endpoint string, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("X-Request-ID", uuid.NewString())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, nil, readErr
	}
	return resp, respBody, nil
}

func (s *openAIAuthService) evaluateDevicePollResponse(resp *http.Response, body []byte, interval time.Duration) devicePollOutcome {
	if resp == nil {
		return devicePollOutcome{err: fmt.Errorf("device poll response is nil")}
	}
	if resp.StatusCode == http.StatusOK {
		token, err := parseDevicePollSuccess(body)
		return devicePollOutcome{token: token, err: err}
	}
	return s.evaluateDevicePollFailure(resp, body, interval)
}

func parseDevicePollSuccess(body []byte) (*deviceTokenResponse, error) {
	var decoded deviceTokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode device token response: %w", err)
	}
	if strings.TrimSpace(decoded.AuthorizationCode) == "" || strings.TrimSpace(decoded.CodeVerifier) == "" {
		return nil, fmt.Errorf("device token response missing required fields")
	}
	return &decoded, nil
}

func (s *openAIAuthService) evaluateDevicePollFailure(resp *http.Response, body []byte, interval time.Duration) devicePollOutcome {
	apiErr := statusError(resp.StatusCode, body)
	errorCode := oauthErrorCode(apiErr)
	if mapped := mapDeviceAuthErrorCode(errorCode, apiErr, interval); mapped != nil {
		return ensureNextPollInterval(*mapped, interval)
	}
	wait := pollWaitDuration(resp, interval)
	if isPollRetryableStatus(resp.StatusCode) {
		return devicePollOutcome{wait: wait, nextInterval: interval}
	}
	return devicePollOutcome{err: fmt.Errorf("device token poll failed: %w", apiErr)}
}

func oauthErrorCode(err error) string {
	oauthErr := asOAuthHTTPError(err)
	if oauthErr == nil {
		return ""
	}
	return oauthErr.Code
}

func ensureNextPollInterval(out devicePollOutcome, interval time.Duration) devicePollOutcome {
	if out.nextInterval != 0 {
		return out
	}
	out.nextInterval = interval
	return out
}

func mapDeviceAuthErrorCode(code string, apiErr error, interval time.Duration) *devicePollOutcome {
	resolver, ok := deviceAuthErrorResolvers[code]
	if !ok {
		return nil
	}
	return resolver(apiErr, interval)
}

var deviceAuthErrorResolvers = map[string]func(error, time.Duration) *devicePollOutcome{
	"authorization_pending": deviceAuthPendingOutcome,
	"slow_down":             deviceAuthSlowDownOutcome,
	"expired_token":         deviceAuthExpiredOutcome,
	"access_denied":         deviceAuthDeniedOutcome,
}

func deviceAuthPendingOutcome(_ error, interval time.Duration) *devicePollOutcome {
	return &devicePollOutcome{wait: interval, nextInterval: interval}
}

func deviceAuthSlowDownOutcome(_ error, interval time.Duration) *devicePollOutcome {
	next := clampDuration(interval*2, DefaultPollInterval, defaultBackoffMax)
	return &devicePollOutcome{wait: next, nextInterval: next}
}

func deviceAuthExpiredOutcome(apiErr error, _ time.Duration) *devicePollOutcome {
	return &devicePollOutcome{err: fmt.Errorf("%w: %v", ErrAuthExpiredToken, apiErr)}
}

func deviceAuthDeniedOutcome(apiErr error, _ time.Duration) *devicePollOutcome {
	return &devicePollOutcome{err: fmt.Errorf("%w: %v", ErrAuthAccessDenied, apiErr)}
}

func isPollRetryableStatus(status int) bool {
	switch status {
	case http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func pollWaitDuration(resp *http.Response, interval time.Duration) time.Duration {
	if resp == nil {
		return interval
	}
	return retryAfterOrDefault(resp.Header.Get("Retry-After"), interval)
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
	return s.resolveAuthFromLookup(s.lookupEnv)
}

func (s *openAIAuthService) resolveFromDotEnv() (*OpenAIChatGPTAuth, error) {
	values := loadDotEnvValues(s.resolveDotEnvPaths())
	return s.resolveAuthFromLookup(mapLookup(values))
}

func (s *openAIAuthService) resolveAuthFromLookup(lookup func(string) (string, bool)) (*OpenAIChatGPTAuth, error) {
	accessToken := firstEnvValue(lookup, "OPENAI_ACCESS_TOKEN", "CHATGPT_ACCESS_TOKEN")
	accountID := firstEnvValue(lookup, "OPENAI_CHATGPT_ACCOUNT_ID", "CHATGPT_ACCOUNT_ID")
	if err := validateResolvedAuthValues(accessToken, accountID); err != nil {
		return nil, err
	}
	auth := s.newAuthFromLookup(lookup, accessToken, accountID)
	if err := validateAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func validateResolvedAuthValues(accessToken string, accountID string) error {
	if accessToken == "" && accountID == "" {
		return ErrAuthNotConfigured
	}
	if accessToken == "" {
		return ErrMissingAccessToken
	}
	if accountID == "" {
		return ErrMissingChatGPTAccount
	}
	return nil
}

func (s *openAIAuthService) newAuthFromLookup(
	lookup func(string) (string, bool),
	accessToken string,
	accountID string,
) *OpenAIChatGPTAuth {
	return &OpenAIChatGPTAuth{
		AuthMode:         openAIAuthModeChatGPT,
		Issuer:           firstNonEmpty(firstEnvValue(lookup, "OPENAI_AUTH_ISSUER"), s.issuer),
		ClientID:         firstNonEmpty(firstEnvValue(lookup, "OPENAI_AUTH_CLIENT_ID"), s.clientID),
		AccessToken:      accessToken,
		RefreshToken:     firstEnvValue(lookup, "OPENAI_REFRESH_TOKEN", "CHATGPT_REFRESH_TOKEN"),
		IDToken:          firstEnvValue(lookup, "OPENAI_ID_TOKEN", "CHATGPT_ID_TOKEN"),
		ChatGPTAccountID: accountID,
		ChatGPTPlanType:  firstEnvValue(lookup, "OPENAI_CHATGPT_PLAN_TYPE", "CHATGPT_PLAN_TYPE"),
	}
}

func (s *openAIAuthService) resolveDotEnvPaths() []string {
	if len(s.dotEnvPaths) > 0 {
		return append([]string(nil), s.dotEnvPaths...)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(cwd, ".env"), filepath.Join(cwd, ".env.local")}
}

func loadDotEnvValues(paths []string) map[string]string {
	values := make(map[string]string)
	for _, p := range paths {
		mergeDotEnvValues(values, p)
	}
	return values
}

func mergeDotEnvValues(values map[string]string, path string) {
	parsed, err := parseDotEnvFile(path)
	if err != nil {
		return
	}
	for k, v := range parsed {
		if _, exists := values[k]; !exists {
			values[k] = v
		}
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
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
	if seconds, ok := retryAfterSeconds(trimmed); ok {
		return seconds
	}
	if absolute, ok := retryAfterTime(trimmed); ok {
		return absolute
	}
	return fallback
}

func retryAfterSeconds(value string) (time.Duration, bool) {
	secs, err := strconv.Atoi(value)
	if err != nil || secs <= 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

func retryAfterTime(value string) (time.Duration, bool) {
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	until := time.Until(when)
	if until <= 0 {
		return 0, false
	}
	return until, true
}

func shouldRetryOAuthStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
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
	baseBackoff := s.retryBaseBackoff()
	start := s.now()
	var lastErr error
	for attempt := 0; attempt < defaultOAuthMaxAttempts; attempt++ {
		if s.retryBudgetExceeded(start, attempt) {
			break
		}
		outcome := s.runRetriableAttempt(ctx, endpoint, contentType, body, attempt, baseBackoff)
		if outcome.done {
			return outcome.body, outcome.headers, outcome.err
		}
		lastErr = outcome.err
		if sleepErr := s.sleep(ctx, outcome.wait); sleepErr != nil {
			return nil, outcome.headers, sleepErr
		}
	}
	return nil, nil, coalesceRetriableError(lastErr)
}

type retriableAttemptOutcome struct {
	body    []byte
	headers http.Header
	err     error
	wait    time.Duration
	done    bool
}

func (s *openAIAuthService) retryBaseBackoff() time.Duration {
	if s.pollInterval > 0 {
		return s.pollInterval
	}
	return DefaultPollInterval
}

func (s *openAIAuthService) retryBudgetExceeded(start time.Time, attempt int) bool {
	if s.retryBudget <= 0 {
		return false
	}
	if attempt <= 0 {
		return false
	}
	return s.now().Sub(start) >= s.retryBudget
}

func (s *openAIAuthService) runRetriableAttempt(
	ctx context.Context,
	endpoint string,
	contentType string,
	body []byte,
	attempt int,
	baseBackoff time.Duration,
) retriableAttemptOutcome {
	respBody, headers, err := s.executeRetriableAttempt(ctx, endpoint, contentType, body)
	if err == nil {
		return retriableAttemptOutcome{body: respBody, headers: headers, done: true}
	}
	retry, wait := s.shouldRetryRetriableAttempt(attempt, err, headers, baseBackoff)
	if !retry {
		return retriableAttemptOutcome{headers: headers, err: err, done: true}
	}
	return retriableAttemptOutcome{headers: headers, err: err, wait: wait}
}

func coalesceRetriableError(lastErr error) error {
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("request failed after retries")
}

func (s *openAIAuthService) executeRetriableAttempt(
	ctx context.Context,
	endpoint string,
	contentType string,
	body []byte,
) ([]byte, http.Header, error) {
	req, err := newRetriableRequest(ctx, endpoint, contentType, body, s.userAgent)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, resp.Header, readErr
	}
	if isHTTPSuccess(resp.StatusCode) {
		return respBody, resp.Header, nil
	}
	return nil, resp.Header, statusError(resp.StatusCode, respBody)
}

func newRetriableRequest(
	ctx context.Context,
	endpoint string,
	contentType string,
	body []byte,
	userAgent string,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Request-ID", uuid.NewString())
	return req, nil
}

func isHTTPSuccess(statusCode int) bool {
	return statusCode/100 == 2
}

func (s *openAIAuthService) shouldRetryRetriableAttempt(
	attempt int,
	err error,
	headers http.Header,
	baseBackoff time.Duration,
) (bool, time.Duration) {
	if exhaustedRetriableAttempt(attempt) {
		return false, 0
	}
	if retry, wait := s.retryWaitForNetworkError(err, baseBackoff, attempt); retry {
		return true, wait
	}
	return s.retryWaitForOAuthError(err, headers, baseBackoff, attempt)
}

func exhaustedRetriableAttempt(attempt int) bool {
	return attempt+1 >= defaultOAuthMaxAttempts
}

func (s *openAIAuthService) retryWaitForNetworkError(
	err error,
	baseBackoff time.Duration,
	attempt int,
) (bool, time.Duration) {
	apiErr := asOAuthHTTPError(err)
	if apiErr != nil {
		return false, 0
	}
	if !isRetriableNetworkError(err) {
		return false, 0
	}
	return true, fullJitterBackoff(baseBackoff, defaultBackoffMax, attempt, s.randFloat64())
}

func (s *openAIAuthService) retryWaitForOAuthError(
	err error,
	headers http.Header,
	baseBackoff time.Duration,
	attempt int,
) (bool, time.Duration) {
	apiErr := asOAuthHTTPError(err)
	if apiErr == nil {
		return false, 0
	}
	if !shouldRetryOAuthStatus(apiErr.StatusCode) {
		return false, 0
	}
	wait := fullJitterBackoff(baseBackoff, defaultBackoffMax, attempt, s.randFloat64())
	wait = retryAfterOrDefault(headers.Get("Retry-After"), wait)
	return true, wait
}

func fullJitterBackoff(base, max time.Duration, attempt int, jitter float64) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if max <= 0 {
		max = defaultBackoffMax
	}
	if attempt < 0 {
		attempt = 0
	}
	raw := float64(base) * math.Pow(2, float64(attempt))
	capped := time.Duration(raw)
	if capped > max {
		capped = max
	}
	if capped <= 0 {
		return base
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	wait := time.Duration(float64(capped) * jitter)
	if wait <= 0 {
		wait = time.Millisecond
	}
	return wait
}

func clampDuration(v, min, max time.Duration) time.Duration {
	if v < min {
		return min
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

func isRetriableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "temporarily unavailable")
}

func newDefaultHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
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

type oauthHTTPError struct {
	StatusCode int
	Code       string
	Message    string
	Body       string
}

func (e *oauthHTTPError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("status %d (%s): %s", e.StatusCode, e.Code, msg)
	}
	return fmt.Sprintf("status %d: %s", e.StatusCode, msg)
}

func asOAuthHTTPError(err error) *oauthHTTPError {
	var apiErr *oauthHTTPError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

func statusError(status int, body []byte) error {
	code, message := extractOAuthErrorCodeAndMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	return &oauthHTTPError{
		StatusCode: status,
		Code:       code,
		Message:    message,
		Body:       trimBodyForError(body),
	}
}

func extractErrorMessage(body []byte) string {
	_, message := extractOAuthErrorCodeAndMessage(body)
	return message
}

func extractOAuthErrorCodeAndMessage(body []byte) (string, string) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", trimBodyForError(body)
	}

	errorCode := mapString(payload, "error")
	if rawErr, ok := payload["error"]; ok {
		if nested, ok := rawErr.(map[string]any); ok {
			if v := mapString(nested, "code"); v != "" {
				errorCode = v
			}
		}
	}

	if v := mapString(payload, "error_description"); v != "" {
		return errorCode, v
	}
	if v := mapString(payload, "message"); v != "" {
		return errorCode, v
	}
	if rawErr, ok := payload["error"]; ok {
		switch e := rawErr.(type) {
		case string:
			if strings.TrimSpace(e) != "" {
				if errorCode == "" {
					errorCode = strings.TrimSpace(e)
				}
				return errorCode, strings.TrimSpace(e)
			}
		case map[string]any:
			if v := mapString(e, "message"); v != "" {
				if errorCode == "" {
					errorCode = mapString(e, "code")
				}
				return errorCode, v
			}
		}
	}

	return errorCode, trimBodyForError(body)
}

func trimBodyForError(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
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

func DefaultOpenAIAuthKeyPath(authPath string) string {
	if strings.TrimSpace(authPath) == "" {
		return ""
	}
	return authPath + ".key"
}

func NewDefaultOpenAIAuthStore(path string, lookupEnv func(string) (string, bool)) OpenAIAuthStore {
	encrypted := NewEncryptedFileOpenAIAuthStore(path, EncryptedFileOpenAIAuthStoreConfig{
		KeyPath:   DefaultOpenAIAuthKeyPath(path),
		LookupEnv: lookupEnv,
	})
	keyring := NewSystemKeyringOpenAIAuthStore(path)
	if keyring == nil {
		return encrypted
	}
	return NewFallbackOpenAIAuthStore(keyring, encrypted)
}

type FallbackOpenAIAuthStore struct {
	primary   OpenAIAuthStore
	secondary OpenAIAuthStore
}

func NewFallbackOpenAIAuthStore(primary, secondary OpenAIAuthStore) *FallbackOpenAIAuthStore {
	return &FallbackOpenAIAuthStore{
		primary:   primary,
		secondary: secondary,
	}
}

func (s *FallbackOpenAIAuthStore) Save(auth *OpenAIChatGPTAuth) error {
	var errs []error
	if s.primary != nil {
		err := s.primary.Save(auth)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	if s.secondary != nil {
		err := s.secondary.Save(auth)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return errors.Join(errs...)
}

func (s *FallbackOpenAIAuthStore) Load() (*OpenAIChatGPTAuth, error) {
	if s.primary != nil {
		auth, err := s.primary.Load()
		if err == nil && auth != nil {
			return auth, nil
		}
	}
	if s.secondary != nil {
		return s.secondary.Load()
	}
	return nil, nil
}

func (s *FallbackOpenAIAuthStore) Delete() error {
	var errs []error
	if s.primary != nil {
		if err := s.primary.Delete(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.secondary != nil {
		if err := s.secondary.Delete(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return errors.Join(errs...)
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
	encoded, err := yaml.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}

	if err := writeFileAtomically(s.path, encoded, 0600); err != nil {
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

type EncryptedFileOpenAIAuthStoreConfig struct {
	KeyPath   string
	LookupEnv func(string) (string, bool)
}

type EncryptedFileOpenAIAuthStore struct {
	path      string
	keyPath   string
	lookupEnv func(string) (string, bool)
}

type encryptedAuthEnvelope struct {
	Version    int    `yaml:"version"`
	Cipher     string `yaml:"cipher"`
	Nonce      string `yaml:"nonce"`
	Ciphertext string `yaml:"ciphertext"`
}

func NewEncryptedFileOpenAIAuthStore(path string, cfg EncryptedFileOpenAIAuthStoreConfig) *EncryptedFileOpenAIAuthStore {
	lookupEnv := cfg.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	keyPath := strings.TrimSpace(cfg.KeyPath)
	if keyPath == "" {
		keyPath = DefaultOpenAIAuthKeyPath(path)
	}
	return &EncryptedFileOpenAIAuthStore{
		path:      path,
		keyPath:   keyPath,
		lookupEnv: lookupEnv,
	}
}

func (s *EncryptedFileOpenAIAuthStore) Save(auth *OpenAIChatGPTAuth) error {
	if auth == nil {
		return fmt.Errorf("auth payload is required")
	}
	if strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("auth store path is empty")
	}
	key, err := s.resolveEncryptionKey()
	if err != nil {
		return err
	}

	plaintext, err := yaml.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("nonce generation failed: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("cipher init failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("gcm init failed: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(s.path))

	envelope := encryptedAuthEnvelope{
		Version:    1,
		Cipher:     "AES-256-GCM",
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := yaml.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal auth envelope: %w", err)
	}
	return writeFileAtomically(s.path, encoded, 0600)
}

func (s *EncryptedFileOpenAIAuthStore) Load() (*OpenAIChatGPTAuth, error) {
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

	// Backward compatibility: accept the legacy plaintext YAML shape.
	var legacy OpenAIChatGPTAuth
	if err := yaml.Unmarshal(data, &legacy); err == nil {
		if strings.TrimSpace(legacy.AccessToken) != "" && strings.TrimSpace(legacy.ChatGPTAccountID) != "" {
			_ = s.Save(&legacy)
			return &legacy, nil
		}
	}

	var envelope encryptedAuthEnvelope
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse auth envelope: %w", err)
	}
	if envelope.Cipher != "AES-256-GCM" || envelope.Ciphertext == "" || envelope.Nonce == "" {
		return nil, fmt.Errorf("invalid auth envelope")
	}

	key, err := s.resolveEncryptionKey()
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode auth nonce: %w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode auth ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher init failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm init failed: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(s.path))
	if err != nil {
		return nil, fmt.Errorf("decrypt auth payload: %w", err)
	}
	var auth OpenAIChatGPTAuth
	if err := yaml.Unmarshal(plaintext, &auth); err != nil {
		return nil, fmt.Errorf("parse auth payload: %w", err)
	}
	return &auth, nil
}

func (s *EncryptedFileOpenAIAuthStore) Delete() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *EncryptedFileOpenAIAuthStore) resolveEncryptionKey() ([]byte, error) {
	if v := firstEnvValue(s.lookupEnv, "SYLK_OPENAI_AUTH_KEY_B64"); v != "" {
		key, err := base64.RawStdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SYLK_OPENAI_AUTH_KEY_B64: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("SYLK_OPENAI_AUTH_KEY_B64 must decode to 32 bytes")
		}
		return key, nil
	}
	if v := firstEnvValue(s.lookupEnv, "SYLK_OPENAI_AUTH_PASSPHRASE"); v != "" {
		sum := sha256.Sum256([]byte(v))
		key := make([]byte, len(sum))
		copy(key, sum[:])
		return key, nil
	}

	if strings.TrimSpace(s.keyPath) == "" {
		return nil, fmt.Errorf("auth key path is empty")
	}

	keyData, err := os.ReadFile(s.keyPath)
	if err == nil {
		keyData = bytes.TrimSpace(keyData)
		if decoded, decErr := base64.RawStdEncoding.DecodeString(string(keyData)); decErr == nil && len(decoded) == 32 {
			return decoded, nil
		}
		if len(keyData) == 32 {
			key := make([]byte, 32)
			copy(key, keyData)
			return key, nil
		}
		return nil, fmt.Errorf("invalid auth key file format")
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read auth key file: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate auth key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.keyPath), 0700); err != nil {
		return nil, fmt.Errorf("creating auth key directory: %w", err)
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(key))
	if err := writeFileAtomically(s.keyPath, encoded, 0600); err != nil {
		return nil, fmt.Errorf("persist auth key file: %w", err)
	}
	return key, nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

type systemKeyringBackend int

const (
	keyringBackendNone systemKeyringBackend = iota
	keyringBackendDarwinSecurity
	keyringBackendLinuxSecretTool
)

type SystemKeyringOpenAIAuthStore struct {
	service string
	account string
	backend systemKeyringBackend
}

func NewSystemKeyringOpenAIAuthStore(path string) *SystemKeyringOpenAIAuthStore {
	backend := detectSystemKeyringBackend()
	if backend == keyringBackendNone {
		return nil
	}
	return &SystemKeyringOpenAIAuthStore{
		service: "sylk.openai.chatgpt.auth",
		account: keyringAccountForPath(path),
		backend: backend,
	}
}

func (s *SystemKeyringOpenAIAuthStore) Save(auth *OpenAIChatGPTAuth) error {
	if auth == nil {
		return fmt.Errorf("auth payload is required")
	}
	payload, err := yaml.Marshal(auth)
	if err != nil {
		return err
	}
	secret := base64.RawStdEncoding.EncodeToString(payload)
	cmd, stdin := s.buildStoreCommand(secret)
	if cmd == nil {
		return fmt.Errorf("keyring backend unavailable")
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("save keyring auth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SystemKeyringOpenAIAuthStore) Load() (*OpenAIChatGPTAuth, error) {
	cmd := s.buildLoadCommand()
	if cmd == nil {
		return nil, nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isKeyringNotFoundError(err, out) {
			return nil, nil
		}
		return nil, fmt.Errorf("load keyring auth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode keyring payload: %w", err)
	}
	var auth OpenAIChatGPTAuth
	if err := yaml.Unmarshal(raw, &auth); err != nil {
		return nil, fmt.Errorf("parse keyring payload: %w", err)
	}
	return &auth, nil
}

func (s *SystemKeyringOpenAIAuthStore) Delete() error {
	cmd := s.buildDeleteCommand()
	if cmd == nil {
		return nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isKeyringNotFoundError(err, out) {
			return nil
		}
		return fmt.Errorf("delete keyring auth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SystemKeyringOpenAIAuthStore) buildStoreCommand(secret string) (*exec.Cmd, io.Reader) {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command(
			"security",
			"add-generic-password",
			"-U",
			"-s", s.service,
			"-a", s.account,
			"-w", secret,
		), nil
	case keyringBackendLinuxSecretTool:
		cmd := exec.Command(
			"secret-tool",
			"store",
			"--label=Sylk OpenAI Auth",
			"service", s.service,
			"account", s.account,
		)
		return cmd, strings.NewReader(secret)
	default:
		return nil, nil
	}
}

func (s *SystemKeyringOpenAIAuthStore) buildLoadCommand() *exec.Cmd {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command(
			"security",
			"find-generic-password",
			"-s", s.service,
			"-a", s.account,
			"-w",
		)
	case keyringBackendLinuxSecretTool:
		return exec.Command(
			"secret-tool",
			"lookup",
			"service", s.service,
			"account", s.account,
		)
	default:
		return nil
	}
}

func (s *SystemKeyringOpenAIAuthStore) buildDeleteCommand() *exec.Cmd {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command(
			"security",
			"delete-generic-password",
			"-s", s.service,
			"-a", s.account,
		)
	case keyringBackendLinuxSecretTool:
		return exec.Command(
			"secret-tool",
			"clear",
			"service", s.service,
			"account", s.account,
		)
	default:
		return nil
	}
}

func detectSystemKeyringBackend() systemKeyringBackend {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("security"); err == nil {
			return keyringBackendDarwinSecurity
		}
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err == nil {
			return keyringBackendLinuxSecretTool
		}
	}
	return keyringBackendNone
}

func keyringAccountForPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("auth-%x", sum[:8])
}

func isKeyringNotFoundError(err error, output []byte) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(string(output)))
	return slices.Contains([]int{1, 44, 45}, exitErr.ExitCode()) ||
		strings.Contains(msg, "could not be found") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such secret")
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
