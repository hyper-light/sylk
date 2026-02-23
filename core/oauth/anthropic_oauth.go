package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	DefaultAnthropicOAuthIssuer = "https://console.anthropic.com"

	defaultAnthropicOAuthAuthURL      = "https://claude.ai/oauth/authorize"
	defaultAnthropicOAuthTokenURL     = "https://console.anthropic.com/v1/oauth/token"
	defaultAnthropicOAuthRedirectURL  = "https://console.anthropic.com/oauth/code/callback"
	defaultAnthropicOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	defaultAnthropicOAuthScopes       = "org:create_api_key user:profile user:inference"
	defaultAnthropicOAuthTimeout      = 15 * time.Minute
	defaultAnthropicOAuthPollInterval = 5 * time.Second
	defaultAnthropicOAuthRefreshSkew  = 2 * time.Minute
	defaultAnthropicOAuthRetryBudget  = 45 * time.Second
	defaultAnthropicOAuthCallbackHost = "127.0.0.1"
	defaultAnthropicOAuthCallbackPath = "/oauth2callback"

	anthropicOAuthAuthMode   = "oauth"
	anthropicOAuthBetaHeader = "oauth-2025-04-20"
)

var (
	ErrAnthropicAuthNotConfigured = errors.New("anthropic oauth is not configured")
	ErrAnthropicAuthTimeout       = errors.New("anthropic oauth timed out")
	ErrAnthropicAuthDenied        = errors.New("anthropic oauth authorization denied")
	ErrAnthropicAuthCodeRequired  = errors.New("anthropic oauth authorization code is required")
	ErrAnthropicMissingToken      = errors.New("missing anthropic oauth access token")
	ErrAnthropicMissingRefresh    = errors.New("missing anthropic oauth refresh token")
)

// AnthropicOAuthAuth stores Anthropic OAuth credentials for token refresh and reuse.
type AnthropicOAuthAuth struct {
	AuthMode          string    `yaml:"auth_mode" json:"auth_mode"`
	Issuer            string    `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	ClientID          string    `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	AccessToken       string    `yaml:"access_token" json:"access_token"`
	RefreshToken      string    `yaml:"refresh_token,omitempty" json:"refresh_token,omitempty"`
	TokenType         string    `yaml:"token_type,omitempty" json:"token_type,omitempty"`
	Scope             string    `yaml:"scope,omitempty" json:"scope,omitempty"`
	ObtainedAt        time.Time `yaml:"obtained_at,omitempty" json:"obtained_at,omitempty"`
	AccessTokenExpiry time.Time `yaml:"access_token_expiry,omitempty" json:"access_token_expiry,omitempty"`
}

// AnthropicOAuthChallenge contains browser authorization details.
type AnthropicOAuthChallenge struct {
	AuthURL     string `json:"auth_url"`
	RedirectURL string `json:"redirect_url"`
	State       string `json:"state"`

	CallbackHost string `json:"callback_host"`
	CallbackPort int    `json:"callback_port"`
	CallbackPath string `json:"callback_path"`

	CodeVerifier string `json:"-"`
}

type AnthropicOAuthStore interface {
	Save(auth *AnthropicOAuthAuth) error
	Load() (*AnthropicOAuthAuth, error)
	Delete() error
}

type AnthropicAuthService interface {
	BeginAuth(ctx context.Context) (*AnthropicOAuthChallenge, error)
	CompleteAuth(ctx context.Context, challenge *AnthropicOAuthChallenge, timeout time.Duration) (*AnthropicOAuthAuth, error)
	CompleteAuthCode(ctx context.Context, challenge *AnthropicOAuthChallenge, code string) (*AnthropicOAuthAuth, error)
	Refresh(ctx context.Context, current *AnthropicOAuthAuth) (*AnthropicOAuthAuth, error)
	Resolve(ctx context.Context) (*AnthropicOAuthAuth, error)
	Save(ctx context.Context, auth *AnthropicOAuthAuth) error
	Load(ctx context.Context) (*AnthropicOAuthAuth, error)
	Delete(ctx context.Context) error
}

type AnthropicOAuthSession struct {
	Challenge *AnthropicOAuthChallenge
	Results   <-chan AnthropicOAuthResult
	Cancel    context.CancelFunc
}

type AnthropicOAuthResult struct {
	Auth *AnthropicOAuthAuth
	Err  error
}

func StartAnthropicOAuthSession(
	ctx context.Context,
	service AnthropicAuthService,
	timeout time.Duration,
) (*AnthropicOAuthSession, error) {
	if service == nil {
		return nil, fmt.Errorf("anthropic auth service is required")
	}
	challenge, err := service.BeginAuth(ctx)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	results := make(chan AnthropicOAuthResult, 1)
	go runAnthropicOAuthSession(sessionCtx, service, challenge, timeout, results)
	return &AnthropicOAuthSession{
		Challenge: challenge,
		Results:   results,
		Cancel:    cancel,
	}, nil
}

func runAnthropicOAuthSession(
	ctx context.Context,
	service AnthropicAuthService,
	challenge *AnthropicOAuthChallenge,
	timeout time.Duration,
	results chan<- AnthropicOAuthResult,
) {
	defer close(results)
	results <- completeAnthropicOAuthSession(ctx, service, challenge, timeout)
}

func completeAnthropicOAuthSession(
	ctx context.Context,
	service AnthropicAuthService,
	challenge *AnthropicOAuthChallenge,
	timeout time.Duration,
) AnthropicOAuthResult {
	auth, err := service.CompleteAuth(ctx, challenge, timeout)
	if err != nil {
		return AnthropicOAuthResult{Err: err}
	}
	if err := service.Save(ctx, auth); err != nil {
		return AnthropicOAuthResult{Err: err}
	}
	return AnthropicOAuthResult{Auth: auth}
}

type AnthropicAuthServiceConfig struct {
	Issuer       string
	AuthURL      string
	TokenURL     string
	ClientID     string
	Scopes       []string
	CallbackHost string
	CallbackPort int
	CallbackPath string

	HTTPClient   *http.Client
	Store        AnthropicOAuthStore
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

type anthropicAuthService struct {
	issuer       string
	authURL      string
	tokenURL     string
	clientID     string
	scopes       string
	callbackHost string
	callbackPort int
	callbackPath string

	httpClient   *http.Client
	store        AnthropicOAuthStore
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

func NewAnthropicAuthService(cfg AnthropicAuthServiceConfig) AnthropicAuthService {
	lookupEnv := resolveEnvLookup(cfg.LookupEnv)
	return &anthropicAuthService{
		issuer:       resolveAnthropicIssuer(cfg.Issuer),
		authURL:      resolveAnthropicAuthURL(cfg.AuthURL),
		tokenURL:     resolveAnthropicTokenURL(cfg.TokenURL),
		clientID:     resolveAnthropicClientID(cfg.ClientID, lookupEnv),
		scopes:       resolveAnthropicScopes(cfg.Scopes, lookupEnv),
		callbackHost: resolveAnthropicCallbackHost(cfg.CallbackHost, lookupEnv),
		callbackPort: resolveAnthropicCallbackPort(cfg.CallbackPort, lookupEnv),
		callbackPath: resolveAnthropicCallbackPath(cfg.CallbackPath),
		httpClient:   resolveServiceHTTPClient(cfg.HTTPClient),
		store:        resolveAnthropicStore(cfg.Store, lookupEnv),
		lookupEnv:    lookupEnv,
		dotEnvPaths:  append([]string(nil), cfg.DotEnvPaths...),
		pollInterval: resolvePositiveDuration(cfg.PollInterval, defaultAnthropicOAuthPollInterval),
		refreshSkew:  resolvePositiveDuration(cfg.RefreshSkew, defaultAnthropicOAuthRefreshSkew),
		retryBudget:  resolvePositiveDuration(cfg.RetryBudget, defaultAnthropicOAuthRetryBudget),
		userAgent:    resolveServiceUserAgent(cfg.UserAgent),
		now:          resolveNow(cfg.Now),
		sleep:        resolveSleep(cfg.Sleep),
		randFloat64:  resolveRandomFloat64(cfg.RandFloat64),
	}
}

func resolveAnthropicIssuer(issuer string) string {
	trimmed := strings.TrimSpace(issuer)
	if trimmed == "" {
		return DefaultAnthropicOAuthIssuer
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveAnthropicAuthURL(authURL string) string {
	trimmed := strings.TrimSpace(authURL)
	if trimmed == "" {
		return defaultAnthropicOAuthAuthURL
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveAnthropicTokenURL(tokenURL string) string {
	trimmed := strings.TrimSpace(tokenURL)
	if trimmed == "" {
		return defaultAnthropicOAuthTokenURL
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveAnthropicClientID(clientID string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(clientID); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "ANTHROPIC_OAUTH_CLIENT_ID"); v != "" {
		return v
	}
	return defaultAnthropicOAuthClientID
}

func resolveAnthropicScopes(scopes []string, lookupEnv func(string) (string, bool)) string {
	if len(scopes) > 0 {
		trimmed := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			s := strings.TrimSpace(scope)
			if s == "" {
				continue
			}
			trimmed = append(trimmed, s)
		}
		if len(trimmed) > 0 {
			return strings.Join(trimmed, " ")
		}
	}
	if v := firstEnvValue(lookupEnv, "ANTHROPIC_OAUTH_SCOPES"); v != "" {
		return v
	}
	return defaultAnthropicOAuthScopes
}

func resolveAnthropicCallbackHost(host string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(host); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "ANTHROPIC_OAUTH_CALLBACK_HOST"); v != "" {
		return v
	}
	return defaultAnthropicOAuthCallbackHost
}

func resolveAnthropicCallbackPort(port int, lookupEnv func(string) (string, bool)) int {
	if port > 0 {
		return port
	}
	if v := firstEnvValue(lookupEnv, "ANTHROPIC_OAUTH_CALLBACK_PORT"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err == nil && parsed > 0 && parsed <= 65535 {
			return parsed
		}
	}
	return 0
}

func resolveAnthropicCallbackPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultAnthropicOAuthCallbackPath
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func resolveAnthropicStore(store AnthropicOAuthStore, lookupEnv func(string) (string, bool)) AnthropicOAuthStore {
	if store != nil {
		return store
	}
	return NewDefaultAnthropicOAuthStore(DefaultAnthropicOAuthAuthPath(), lookupEnv)
}

func (s *anthropicAuthService) BeginAuth(ctx context.Context) (*AnthropicOAuthChallenge, error) {
	_ = ctx
	if strings.TrimSpace(s.clientID) == "" {
		return nil, ErrAnthropicAuthNotConfigured
	}
	codeVerifier, codeChallenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state := codeVerifier
	redirectURL := anthropicRedirectURL()
	authURL := s.authorizationURL(state, redirectURL, codeChallenge)
	return &AnthropicOAuthChallenge{
		AuthURL:      authURL,
		RedirectURL:  redirectURL,
		State:        state,
		CodeVerifier: codeVerifier,
	}, nil
}

func anthropicRedirectURL() string {
	return defaultAnthropicOAuthRedirectURL
}

func (s *anthropicAuthService) authorizationURL(state, redirectURL, codeChallenge string) string {
	values := url.Values{}
	values.Set("code", "true")
	values.Set("client_id", s.clientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("response_type", "code")
	values.Set("scope", s.scopes)
	values.Set("state", state)
	values.Set("code_challenge_method", "S256")
	values.Set("code_challenge", codeChallenge)
	return s.authURL + "?" + values.Encode()
}

func (s *anthropicAuthService) CompleteAuth(
	ctx context.Context,
	challenge *AnthropicOAuthChallenge,
	timeout time.Duration,
) (*AnthropicOAuthAuth, error) {
	_ = ctx
	_ = timeout
	if err := validateAnthropicChallenge(challenge); err != nil {
		return nil, err
	}
	return nil, ErrAnthropicAuthCodeRequired
}

func (s *anthropicAuthService) CompleteAuthCode(
	ctx context.Context,
	challenge *AnthropicOAuthChallenge,
	code string,
) (*AnthropicOAuthAuth, error) {
	if err := validateAnthropicChallenge(challenge); err != nil {
		return nil, err
	}
	codeValue, stateValue, err := parseAnthropicAuthorizationCode(code)
	if err != nil {
		return nil, err
	}
	if stateValue != "" && challenge.State != "" && stateValue != challenge.State {
		return nil, fmt.Errorf("anthropic oauth state mismatch")
	}
	token, err := s.exchangeCode(ctx, challenge, codeValue, stateValue)
	if err != nil {
		return nil, err
	}
	auth := s.tokensToAuth(token)
	if err := validateAnthropicAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func parseAnthropicAuthorizationCode(input string) (string, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", ErrAnthropicAuthCodeRequired
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		parsed, err := url.Parse(trimmed)
		if err == nil {
			if code := strings.TrimSpace(parsed.Query().Get("code")); code != "" {
				state := strings.TrimSpace(parsed.Query().Get("state"))
				if state == "" && parsed.Fragment != "" {
					_, state = splitAnthropicCodeAndState(parsed.Fragment)
					if state == "" {
						state = strings.TrimSpace(strings.TrimPrefix(parsed.Fragment, "state="))
					}
				}
				return code, state, nil
			}
			if parsed.Fragment != "" {
				code, state := splitAnthropicCodeAndState(parsed.Fragment)
				if code != "" {
					return code, state, nil
				}
			}
		}
	}
	code, state := splitAnthropicCodeAndState(trimmed)
	if code == "" {
		return "", "", ErrAnthropicAuthCodeRequired
	}
	return code, state, nil
}

func splitAnthropicCodeAndState(input string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(input), "#", 2)
	code := strings.TrimSpace(parts[0])
	state := ""
	if len(parts) == 2 {
		state = strings.TrimSpace(parts[1])
	}
	if strings.Contains(code, "=") {
		if values, err := url.ParseQuery(code); err == nil {
			parsedCode := strings.TrimSpace(values.Get("code"))
			if parsedCode != "" {
				code = parsedCode
			}
			if state == "" {
				state = strings.TrimSpace(values.Get("state"))
			}
		}
	}
	state = strings.TrimPrefix(state, "state=")
	return strings.TrimSpace(code), strings.TrimSpace(state)
}

func (s *anthropicAuthService) exchangeCode(
	ctx context.Context,
	challenge *AnthropicOAuthChallenge,
	code string,
	state string,
) (*anthropicOAuthTokenResponse, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  challenge.RedirectURL,
		"client_id":     s.clientID,
		"code_verifier": challenge.CodeVerifier,
	}
	if state != "" {
		payload["state"] = state
	}
	body, _, err := s.doAnthropicRetriableJSONPost(ctx, s.tokenURL, payload)
	if err != nil {
		return nil, fmt.Errorf("exchange anthropic oauth code: %w", err)
	}
	return parseAnthropicTokenResponse(body)
}

func validateAnthropicChallenge(challenge *AnthropicOAuthChallenge) error {
	if challenge == nil {
		return fmt.Errorf("anthropic oauth challenge is required")
	}
	if strings.TrimSpace(challenge.AuthURL) == "" {
		return fmt.Errorf("anthropic oauth challenge missing auth URL")
	}
	if strings.TrimSpace(challenge.RedirectURL) == "" {
		return fmt.Errorf("anthropic oauth challenge missing redirect URL")
	}
	if strings.TrimSpace(challenge.State) == "" {
		return fmt.Errorf("anthropic oauth challenge missing state")
	}
	return nil
}

func (s *anthropicAuthService) waitForCallback(ctx context.Context, challenge *AnthropicOAuthChallenge) (string, error) {
	addr := fmt.Sprintf("%s:%d", challenge.CallbackHost, challenge.CallbackPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen oauth callback: %w", err)
	}
	defer listener.Close()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := s.newCallbackServer(challenge, codeCh, errCh)

	go runGoogleCallbackServer(server, listener, errCh)
	defer shutdownGoogleCallbackServer(server)

	return waitForGoogleCallbackResult(ctx, codeCh, errCh)
}

func (s *anthropicAuthService) newCallbackServer(
	challenge *AnthropicOAuthChallenge,
	codeCh chan<- string,
	errCh chan<- error,
) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(challenge.CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		handleAnthropicOAuthCallback(w, r, challenge, codeCh, errCh)
	})
	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func handleAnthropicOAuthCallback(
	w http.ResponseWriter,
	r *http.Request,
	challenge *AnthropicOAuthChallenge,
	codeCh chan<- string,
	errCh chan<- error,
) {
	query := r.URL.Query()
	if anthropicCallbackHasError(query, errCh) {
		writeGoogleCallbackResponse(w, http.StatusUnauthorized, "Authentication failed.")
		return
	}
	if query.Get("state") != challenge.State {
		select {
		case errCh <- fmt.Errorf("anthropic oauth state mismatch"):
		default:
		}
		writeGoogleCallbackResponse(w, http.StatusBadRequest, "Invalid state.")
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		select {
		case errCh <- fmt.Errorf("anthropic oauth callback missing code"):
		default:
		}
		writeGoogleCallbackResponse(w, http.StatusBadRequest, "Missing code.")
		return
	}
	select {
	case codeCh <- code:
	default:
	}
	writeGoogleCallbackResponse(w, http.StatusOK, "Authentication complete. You can return to Sylk.")
}

func anthropicCallbackHasError(query url.Values, errCh chan<- error) bool {
	errorCode := strings.TrimSpace(query.Get("error"))
	if errorCode == "" {
		return false
	}
	errorDescription := strings.TrimSpace(query.Get("error_description"))
	if errorCode == "access_denied" {
		select {
		case errCh <- fmt.Errorf("%w: %s", ErrAnthropicAuthDenied, errorDescription):
		default:
		}
		return true
	}
	select {
	case errCh <- fmt.Errorf("anthropic oauth callback error: %s %s", errorCode, errorDescription):
	default:
	}
	return true
}

func parseAnthropicTokenResponse(body []byte) (*anthropicOAuthTokenResponse, error) {
	var token anthropicOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decode anthropic token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, ErrAnthropicMissingToken
	}
	return &token, nil
}

func (s *anthropicAuthService) Refresh(ctx context.Context, current *AnthropicOAuthAuth) (*AnthropicOAuthAuth, error) {
	if current == nil {
		return nil, ErrAnthropicAuthNotConfigured
	}
	refreshToken := strings.TrimSpace(current.RefreshToken)
	if refreshToken == "" {
		return nil, ErrAnthropicMissingRefresh
	}
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     firstNonEmpty(strings.TrimSpace(current.ClientID), s.clientID),
	}
	body, _, err := s.doAnthropicRetriableJSONPost(ctx, s.tokenURL, payload)
	if err != nil {
		return nil, fmt.Errorf("refresh anthropic oauth token: %w", err)
	}
	token, err := parseAnthropicTokenResponse(body)
	if err != nil {
		return nil, err
	}
	updated := s.mergeAnthropicRefresh(current, token)
	if err := validateAnthropicAuth(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *anthropicAuthService) mergeAnthropicRefresh(
	current *AnthropicOAuthAuth,
	token *anthropicOAuthTokenResponse,
) *AnthropicOAuthAuth {
	updated := *current
	updated.AccessToken = chooseTokenValue(token.AccessToken, updated.AccessToken)
	updated.RefreshToken = chooseTokenValue(token.RefreshToken, updated.RefreshToken)
	updated.TokenType = chooseTokenValue(token.TokenType, updated.TokenType)
	updated.Scope = chooseTokenValue(token.Scope, updated.Scope)
	updated.AuthMode = anthropicOAuthAuthMode
	updated.Issuer = s.issuer
	updated.ClientID = firstNonEmpty(updated.ClientID, s.clientID)
	updated.ObtainedAt = s.now().UTC()
	if token.ExpiresIn > 0 {
		updated.AccessTokenExpiry = s.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &updated
}

func (s *anthropicAuthService) Resolve(ctx context.Context) (*AnthropicOAuthAuth, error) {
	auth, err := s.resolveFromStore(ctx)
	if err == nil {
		return auth, nil
	}
	storeErr := err

	auth, resolved, err := s.resolveFromSource(ctx, s.resolveFromEnv, false)
	if resolved || err != nil {
		return auth, err
	}
	auth, resolved, err = s.resolveFromSource(ctx, s.resolveFromDotEnv, false)
	if resolved || err != nil {
		return auth, err
	}
	if storeErr != nil && !errors.Is(storeErr, ErrAnthropicAuthNotConfigured) {
		return nil, storeErr
	}
	return nil, ErrAnthropicAuthNotConfigured
}

func (s *anthropicAuthService) resolveFromSource(
	ctx context.Context,
	source func() (*AnthropicOAuthAuth, error),
	persist bool,
) (*AnthropicOAuthAuth, bool, error) {
	auth, err := source()
	if err == nil {
		refreshed, refreshErr := s.maybeRefreshAnthropicAuth(ctx, auth, persist)
		return refreshed, true, refreshErr
	}
	if errors.Is(err, ErrAnthropicAuthNotConfigured) {
		return nil, false, nil
	}
	return nil, true, err
}

func (s *anthropicAuthService) resolveFromStore(ctx context.Context) (*AnthropicOAuthAuth, error) {
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrAnthropicAuthNotConfigured
	}
	s.applyAnthropicAuthDefaults(auth)
	if err := validateAnthropicAuth(auth); err != nil {
		return nil, err
	}
	return s.maybeRefreshAnthropicAuth(ctx, auth, true)
}

func (s *anthropicAuthService) resolveFromEnv() (*AnthropicOAuthAuth, error) {
	return s.resolveFromLookup(s.lookupEnv)
}

func (s *anthropicAuthService) resolveFromDotEnv() (*AnthropicOAuthAuth, error) {
	values := loadDotEnvValues(s.resolveDotEnvPaths())
	return s.resolveFromLookup(mapLookup(values))
}

func (s *anthropicAuthService) resolveFromLookup(
	lookup func(string) (string, bool),
) (*AnthropicOAuthAuth, error) {
	accessToken := firstEnvValue(lookup, "ANTHROPIC_OAUTH_ACCESS_TOKEN", "ANTHROPIC_ACCESS_TOKEN")
	refreshToken := firstEnvValue(lookup, "ANTHROPIC_OAUTH_REFRESH_TOKEN")
	if strings.TrimSpace(accessToken) == "" && strings.TrimSpace(refreshToken) == "" {
		return nil, ErrAnthropicAuthNotConfigured
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, ErrAnthropicMissingToken
	}
	auth := &AnthropicOAuthAuth{
		AuthMode:     anthropicOAuthAuthMode,
		Issuer:       firstNonEmpty(firstEnvValue(lookup, "ANTHROPIC_OAUTH_ISSUER"), s.issuer),
		ClientID:     firstNonEmpty(firstEnvValue(lookup, "ANTHROPIC_OAUTH_CLIENT_ID"), s.clientID),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    firstNonEmpty(firstEnvValue(lookup, "ANTHROPIC_OAUTH_TOKEN_TYPE"), "Bearer"),
		Scope:        firstEnvValue(lookup, "ANTHROPIC_OAUTH_SCOPE"),
	}
	if expiry := firstEnvValue(lookup, "ANTHROPIC_OAUTH_ACCESS_TOKEN_EXPIRY"); expiry != "" {
		if t, err := time.Parse(time.RFC3339, expiry); err == nil {
			auth.AccessTokenExpiry = t.UTC()
		}
	}
	if err := validateAnthropicAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *anthropicAuthService) resolveDotEnvPaths() []string {
	if len(s.dotEnvPaths) > 0 {
		return append([]string(nil), s.dotEnvPaths...)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(cwd, ".env"), filepath.Join(cwd, ".env.local")}
}

func (s *anthropicAuthService) maybeRefreshAnthropicAuth(
	ctx context.Context,
	auth *AnthropicOAuthAuth,
	persist bool,
) (*AnthropicOAuthAuth, error) {
	if !s.shouldRefreshAnthropicAuth(auth) {
		return auth, nil
	}
	if strings.TrimSpace(auth.RefreshToken) == "" {
		return auth, nil
	}
	return s.refreshSingleflight(ctx, auth, persist)
}

func (s *anthropicAuthService) shouldRefreshAnthropicAuth(auth *AnthropicOAuthAuth) bool {
	if auth == nil || auth.AccessTokenExpiry.IsZero() {
		return false
	}
	now := s.now().UTC()
	return now.Add(s.refreshSkew).After(auth.AccessTokenExpiry)
}

func (s *anthropicAuthService) refreshSingleflight(
	ctx context.Context,
	auth *AnthropicOAuthAuth,
	persist bool,
) (*AnthropicOAuthAuth, error) {
	key := "anthropic-refresh:" + strings.TrimSpace(auth.RefreshToken)
	ch := s.refreshGroup.DoChan(key, func() (any, error) {
		return s.executeRefresh(ctx, auth, persist)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		updated, _ := result.Val.(*AnthropicOAuthAuth)
		if updated == nil {
			return nil, ErrAnthropicAuthNotConfigured
		}
		return updated, nil
	}
}

func (s *anthropicAuthService) executeRefresh(
	ctx context.Context,
	auth *AnthropicOAuthAuth,
	persist bool,
) (*AnthropicOAuthAuth, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	current := *auth
	updated, err := s.Refresh(ctx, &current)
	if err != nil {
		return nil, err
	}
	if persist {
		if saveErr := s.store.Save(updated); saveErr != nil {
			return nil, saveErr
		}
	}
	return updated, nil
}

func (s *anthropicAuthService) Save(ctx context.Context, auth *AnthropicOAuthAuth) error {
	_ = ctx
	if auth == nil {
		return fmt.Errorf("anthropic oauth payload is required")
	}
	s.applyAnthropicAuthDefaults(auth)
	if auth.ObtainedAt.IsZero() {
		auth.ObtainedAt = s.now().UTC()
	}
	if err := validateAnthropicAuth(auth); err != nil {
		return err
	}
	return s.store.Save(auth)
}

func (s *anthropicAuthService) Load(ctx context.Context) (*AnthropicOAuthAuth, error) {
	_ = ctx
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrAnthropicAuthNotConfigured
	}
	s.applyAnthropicAuthDefaults(auth)
	if err := validateAnthropicAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *anthropicAuthService) Delete(ctx context.Context) error {
	_ = ctx
	return s.store.Delete()
}

func (s *anthropicAuthService) applyAnthropicAuthDefaults(auth *AnthropicOAuthAuth) {
	if auth == nil {
		return
	}
	auth.AuthMode = anthropicOAuthAuthMode
	auth.Issuer = firstNonEmpty(auth.Issuer, s.issuer)
	auth.ClientID = firstNonEmpty(auth.ClientID, s.clientID)
	auth.TokenType = firstNonEmpty(auth.TokenType, "Bearer")
}

func validateAnthropicAuth(auth *AnthropicOAuthAuth) error {
	if auth == nil {
		return ErrAnthropicAuthNotConfigured
	}
	if strings.TrimSpace(auth.AccessToken) == "" {
		return ErrAnthropicMissingToken
	}
	return nil
}

func (s *anthropicAuthService) tokensToAuth(token *anthropicOAuthTokenResponse) *AnthropicOAuthAuth {
	auth := &AnthropicOAuthAuth{
		AuthMode:     anthropicOAuthAuthMode,
		Issuer:       s.issuer,
		ClientID:     s.clientID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    firstNonEmpty(token.TokenType, "Bearer"),
		Scope:        token.Scope,
		ObtainedAt:   s.now().UTC(),
	}
	if token.ExpiresIn > 0 {
		auth.AccessTokenExpiry = s.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return auth
}

func (s *anthropicAuthService) doAnthropicRetriableFormPost(
	ctx context.Context,
	endpoint string,
	values url.Values,
) ([]byte, http.Header, error) {
	body := []byte(values.Encode())
	baseBackoff := s.retryBaseBackoff()
	start := s.now()
	var lastErr error
	for attempt := 0; attempt < defaultOAuthMaxAttempts; attempt++ {
		if s.retryBudgetExceeded(start, attempt) {
			break
		}
		respBody, headers, err := s.executeFormPost(ctx, endpoint, body)
		if err == nil {
			return respBody, headers, nil
		}
		lastErr = err
		retry, wait := s.shouldRetryFormError(err, headers, attempt, baseBackoff)
		if !retry {
			return nil, headers, err
		}
		if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
			return nil, headers, sleepErr
		}
	}
	return nil, nil, coalesceRetriableError(lastErr)
}

func (s *anthropicAuthService) doAnthropicRetriableJSONPost(
	ctx context.Context,
	endpoint string,
	payload any,
) ([]byte, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	baseBackoff := s.retryBaseBackoff()
	start := s.now()
	var lastErr error
	for attempt := 0; attempt < defaultOAuthMaxAttempts; attempt++ {
		if s.retryBudgetExceeded(start, attempt) {
			break
		}
		respBody, headers, err := s.executeJSONPost(ctx, endpoint, body)
		if err == nil {
			return respBody, headers, nil
		}
		lastErr = err
		retry, wait := s.shouldRetryFormError(err, headers, attempt, baseBackoff)
		if !retry {
			return nil, headers, err
		}
		if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
			return nil, headers, sleepErr
		}
	}
	return nil, nil, coalesceRetriableError(lastErr)
}

func (s *anthropicAuthService) retryBaseBackoff() time.Duration {
	if s.pollInterval > 0 {
		return s.pollInterval
	}
	return defaultAnthropicOAuthPollInterval
}

func (s *anthropicAuthService) retryBudgetExceeded(start time.Time, attempt int) bool {
	if s.retryBudget <= 0 || attempt <= 0 {
		return false
	}
	return s.now().Sub(start) >= s.retryBudget
}

func (s *anthropicAuthService) executeFormPost(
	ctx context.Context,
	endpoint string,
	body []byte,
) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("X-Request-ID", uuid.NewString())
	req.Header.Set("anthropic-beta", anthropicOAuthBetaHeader)
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

func (s *anthropicAuthService) executeJSONPost(
	ctx context.Context,
	endpoint string,
	body []byte,
) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("X-Request-ID", uuid.NewString())
	req.Header.Set("anthropic-beta", anthropicOAuthBetaHeader)
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

func (s *anthropicAuthService) shouldRetryFormError(
	err error,
	headers http.Header,
	attempt int,
	baseBackoff time.Duration,
) (bool, time.Duration) {
	if exhaustedRetriableAttempt(attempt) {
		return false, 0
	}
	if retry, wait := s.retryNetworkError(err, baseBackoff, attempt); retry {
		return true, wait
	}
	return s.retryHTTPError(err, headers, baseBackoff, attempt)
}

func (s *anthropicAuthService) retryNetworkError(
	err error,
	baseBackoff time.Duration,
	attempt int,
) (bool, time.Duration) {
	if asOAuthHTTPError(err) != nil || !isRetriableNetworkError(err) {
		return false, 0
	}
	wait := fullJitterBackoff(baseBackoff, defaultBackoffMax, attempt, s.randFloat64())
	return true, wait
}

func (s *anthropicAuthService) retryHTTPError(
	err error,
	headers http.Header,
	baseBackoff time.Duration,
	attempt int,
) (bool, time.Duration) {
	apiErr := asOAuthHTTPError(err)
	if apiErr == nil || !shouldRetryOAuthStatus(apiErr.StatusCode) {
		return false, 0
	}
	wait := fullJitterBackoff(baseBackoff, defaultBackoffMax, attempt, s.randFloat64())
	wait = retryAfterOrDefault(headers.Get("Retry-After"), wait)
	return true, wait
}

type anthropicOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

func DefaultAnthropicOAuthAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "anthropic_oauth_auth.yaml")
}

func DefaultAnthropicOAuthAuthKeyPath(authPath string) string {
	if strings.TrimSpace(authPath) == "" {
		return ""
	}
	return authPath + ".key"
}

func NewDefaultAnthropicOAuthStore(path string, lookupEnv func(string) (string, bool)) AnthropicOAuthStore {
	return &anthropicOAuthStoreAdapter{
		store: NewDefaultGoogleOAuthStore(path, lookupEnv),
	}
}

type anthropicOAuthStoreAdapter struct {
	store GoogleOAuthStore
}

func (s *anthropicOAuthStoreAdapter) Save(auth *AnthropicOAuthAuth) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("anthropic oauth store is not configured")
	}
	return s.store.Save(anthropicToGoogleOAuth(auth))
}

func (s *anthropicOAuthStoreAdapter) Load() (*AnthropicOAuthAuth, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	return googleToAnthropicOAuth(auth), nil
}

func (s *anthropicOAuthStoreAdapter) Delete() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Delete()
}

func anthropicToGoogleOAuth(auth *AnthropicOAuthAuth) *GoogleOAuthAuth {
	if auth == nil {
		return nil
	}
	return &GoogleOAuthAuth{
		AuthMode:          auth.AuthMode,
		Issuer:            auth.Issuer,
		ClientID:          auth.ClientID,
		AccessToken:       auth.AccessToken,
		RefreshToken:      auth.RefreshToken,
		TokenType:         auth.TokenType,
		Scope:             auth.Scope,
		ObtainedAt:        auth.ObtainedAt,
		AccessTokenExpiry: auth.AccessTokenExpiry,
	}
}

func googleToAnthropicOAuth(auth *GoogleOAuthAuth) *AnthropicOAuthAuth {
	if auth == nil {
		return nil
	}
	return &AnthropicOAuthAuth{
		AuthMode:          auth.AuthMode,
		Issuer:            auth.Issuer,
		ClientID:          auth.ClientID,
		AccessToken:       auth.AccessToken,
		RefreshToken:      auth.RefreshToken,
		TokenType:         auth.TokenType,
		Scope:             auth.Scope,
		ObtainedAt:        auth.ObtainedAt,
		AccessTokenExpiry: auth.AccessTokenExpiry,
	}
}
