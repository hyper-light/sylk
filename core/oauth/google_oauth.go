package oauth

import (
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

const (
	DefaultGoogleOAuthIssuer = "https://oauth2.googleapis.com"

	defaultGoogleOAuthAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthTokenURL     = "https://oauth2.googleapis.com/token"
	defaultGoogleOAuthTimeout      = 15 * time.Minute
	defaultGoogleOAuthPollInterval = 5 * time.Second
	defaultGoogleOAuthRefreshSkew  = 2 * time.Minute
	defaultGoogleOAuthRetryBudget  = 45 * time.Second
	defaultGoogleOAuthCallbackHost = "127.0.0.1"
	defaultGoogleOAuthCallbackPath = "/oauth2callback"
	defaultGoogleOAuthLocation     = "us-central1"

	defaultGoogleOAuthClientID = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	// Installed app secrets are not treated as confidential by Google.
	defaultGoogleOAuthClientSecret = "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"

	defaultGoogleOAuthScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"

	googleOAuthAuthMode = "oauth"
)

var (
	ErrGoogleAuthNotConfigured = errors.New("google oauth is not configured")
	ErrGoogleAuthTimeout       = errors.New("google oauth timed out")
	ErrGoogleAuthDenied        = errors.New("google oauth authorization denied")
	ErrGoogleMissingToken      = errors.New("missing google oauth access token")
	ErrGoogleMissingRefresh    = errors.New("missing google oauth refresh token")
	ErrGoogleMissingProject    = errors.New("missing google cloud project for oauth")
)

// GoogleOAuthAuth stores Google OAuth credentials for token refresh and reuse.
type GoogleOAuthAuth struct {
	AuthMode          string    `yaml:"auth_mode" json:"auth_mode"`
	Issuer            string    `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	ClientID          string    `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	AccessToken       string    `yaml:"access_token" json:"access_token"`
	RefreshToken      string    `yaml:"refresh_token,omitempty" json:"refresh_token,omitempty"`
	TokenType         string    `yaml:"token_type,omitempty" json:"token_type,omitempty"`
	Scope             string    `yaml:"scope,omitempty" json:"scope,omitempty"`
	IDToken           string    `yaml:"id_token,omitempty" json:"id_token,omitempty"`
	ObtainedAt        time.Time `yaml:"obtained_at,omitempty" json:"obtained_at,omitempty"`
	AccessTokenExpiry time.Time `yaml:"access_token_expiry,omitempty" json:"access_token_expiry,omitempty"`
	ProjectID         string    `yaml:"project_id,omitempty" json:"project_id,omitempty"`
	Location          string    `yaml:"location,omitempty" json:"location,omitempty"`
}

// GoogleOAuthChallenge contains all information needed to authorize in a browser.
type GoogleOAuthChallenge struct {
	AuthURL     string `json:"auth_url"`
	RedirectURL string `json:"redirect_url"`
	State       string `json:"state"`

	CallbackHost string `json:"callback_host"`
	CallbackPort int    `json:"callback_port"`
	CallbackPath string `json:"callback_path"`

	CodeVerifier string `json:"-"`
}

type GoogleOAuthStore interface {
	Save(auth *GoogleOAuthAuth) error
	Load() (*GoogleOAuthAuth, error)
	Delete() error
}

type GoogleAuthService interface {
	BeginAuth(ctx context.Context) (*GoogleOAuthChallenge, error)
	CompleteAuth(ctx context.Context, challenge *GoogleOAuthChallenge, timeout time.Duration) (*GoogleOAuthAuth, error)
	Refresh(ctx context.Context, current *GoogleOAuthAuth) (*GoogleOAuthAuth, error)
	Resolve(ctx context.Context) (*GoogleOAuthAuth, error)
	Save(ctx context.Context, auth *GoogleOAuthAuth) error
	Load(ctx context.Context) (*GoogleOAuthAuth, error)
	Delete(ctx context.Context) error
}

type GoogleOAuthSession struct {
	Challenge *GoogleOAuthChallenge
	Results   <-chan GoogleOAuthResult
	Cancel    context.CancelFunc
}

type GoogleOAuthResult struct {
	Auth *GoogleOAuthAuth
	Err  error
}

func StartGoogleOAuthSession(
	ctx context.Context,
	service GoogleAuthService,
	timeout time.Duration,
) (*GoogleOAuthSession, error) {
	if service == nil {
		return nil, fmt.Errorf("google auth service is required")
	}
	challenge, err := service.BeginAuth(ctx)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	results := make(chan GoogleOAuthResult, 1)
	go runGoogleOAuthSession(sessionCtx, service, challenge, timeout, results)
	return &GoogleOAuthSession{
		Challenge: challenge,
		Results:   results,
		Cancel:    cancel,
	}, nil
}

func runGoogleOAuthSession(
	ctx context.Context,
	service GoogleAuthService,
	challenge *GoogleOAuthChallenge,
	timeout time.Duration,
	results chan<- GoogleOAuthResult,
) {
	defer close(results)
	results <- completeGoogleOAuthSession(ctx, service, challenge, timeout)
}

func completeGoogleOAuthSession(
	ctx context.Context,
	service GoogleAuthService,
	challenge *GoogleOAuthChallenge,
	timeout time.Duration,
) GoogleOAuthResult {
	auth, err := service.CompleteAuth(ctx, challenge, timeout)
	if err != nil {
		return GoogleOAuthResult{Err: err}
	}
	if err := service.Save(ctx, auth); err != nil {
		return GoogleOAuthResult{Err: err}
	}
	return GoogleOAuthResult{Auth: auth}
}

type GoogleAuthServiceConfig struct {
	Issuer       string
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	CallbackHost string
	CallbackPort int
	CallbackPath string

	ProjectID string
	Location  string

	HTTPClient   *http.Client
	Store        GoogleOAuthStore
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

type googleAuthService struct {
	issuer       string
	authURL      string
	tokenURL     string
	clientID     string
	clientSecret string
	scopes       string
	callbackHost string
	callbackPort int
	callbackPath string

	projectID string
	location  string

	httpClient   *http.Client
	store        GoogleOAuthStore
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

func NewGoogleAuthService(cfg GoogleAuthServiceConfig) GoogleAuthService {
	lookupEnv := resolveEnvLookup(cfg.LookupEnv)
	return &googleAuthService{
		issuer:       resolveGoogleIssuer(cfg.Issuer),
		authURL:      resolveGoogleAuthURL(cfg.AuthURL),
		tokenURL:     resolveGoogleTokenURL(cfg.TokenURL),
		clientID:     resolveGoogleClientID(cfg.ClientID, lookupEnv),
		clientSecret: resolveGoogleClientSecret(cfg.ClientSecret, lookupEnv),
		scopes:       resolveGoogleScopes(cfg.Scopes, lookupEnv),
		callbackHost: resolveGoogleCallbackHost(cfg.CallbackHost, lookupEnv),
		callbackPort: resolveGoogleCallbackPort(cfg.CallbackPort, lookupEnv),
		callbackPath: resolveGoogleCallbackPath(cfg.CallbackPath),
		projectID:    resolveGoogleProjectID(cfg.ProjectID, lookupEnv),
		location:     resolveGoogleLocation(cfg.Location, lookupEnv),
		httpClient:   resolveServiceHTTPClient(cfg.HTTPClient),
		store:        resolveGoogleStore(cfg.Store, lookupEnv),
		lookupEnv:    lookupEnv,
		dotEnvPaths:  append([]string(nil), cfg.DotEnvPaths...),
		pollInterval: resolvePositiveDuration(cfg.PollInterval, defaultGoogleOAuthPollInterval),
		refreshSkew:  resolvePositiveDuration(cfg.RefreshSkew, defaultGoogleOAuthRefreshSkew),
		retryBudget:  resolvePositiveDuration(cfg.RetryBudget, defaultGoogleOAuthRetryBudget),
		userAgent:    resolveServiceUserAgent(cfg.UserAgent),
		now:          resolveNow(cfg.Now),
		sleep:        resolveSleep(cfg.Sleep),
		randFloat64:  resolveRandomFloat64(cfg.RandFloat64),
	}
}

func resolveGoogleIssuer(issuer string) string {
	trimmed := strings.TrimSpace(issuer)
	if trimmed == "" {
		return DefaultGoogleOAuthIssuer
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveGoogleAuthURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return defaultGoogleOAuthAuthURL
}

func resolveGoogleTokenURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return defaultGoogleOAuthTokenURL
}

func resolveGoogleClientID(clientID string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(clientID); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_OAUTH_CLIENT_ID"); v != "" {
		return v
	}
	return defaultGoogleOAuthClientID
}

func resolveGoogleClientSecret(secret string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(secret); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_OAUTH_CLIENT_SECRET"); v != "" {
		return v
	}
	return defaultGoogleOAuthClientSecret
}

func resolveGoogleScopes(scopes []string, lookupEnv func(string) (string, bool)) string {
	if joined := strings.TrimSpace(strings.Join(scopes, " ")); joined != "" {
		return joined
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_OAUTH_SCOPES"); v != "" {
		return v
	}
	return defaultGoogleOAuthScopes
}

func resolveGoogleCallbackHost(host string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(host); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_OAUTH_CALLBACK_HOST"); v != "" {
		return v
	}
	return defaultGoogleOAuthCallbackHost
}

func resolveGoogleCallbackPort(port int, lookupEnv func(string) (string, bool)) int {
	if port > 0 {
		return port
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_OAUTH_CALLBACK_PORT"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err == nil && parsed > 0 && parsed <= 65535 {
			return parsed
		}
	}
	return 0
}

func resolveGoogleCallbackPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultGoogleOAuthCallbackPath
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func resolveGoogleProjectID(projectID string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(projectID); trimmed != "" {
		return trimmed
	}
	return firstEnvValue(lookupEnv, "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
}

func resolveGoogleLocation(location string, lookupEnv func(string) (string, bool)) string {
	if trimmed := strings.TrimSpace(location); trimmed != "" {
		return trimmed
	}
	if v := firstEnvValue(lookupEnv, "GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION"); v != "" {
		return v
	}
	return defaultGoogleOAuthLocation
}

func resolveGoogleStore(store GoogleOAuthStore, lookupEnv func(string) (string, bool)) GoogleOAuthStore {
	if store != nil {
		return store
	}
	return NewDefaultGoogleOAuthStore(DefaultGoogleOAuthAuthPath(), lookupEnv)
}

func (s *googleAuthService) BeginAuth(ctx context.Context) (*GoogleOAuthChallenge, error) {
	_ = ctx
	if strings.TrimSpace(s.clientID) == "" {
		return nil, ErrGoogleAuthNotConfigured
	}
	port, err := resolveGoogleBeginPort(s.callbackPort)
	if err != nil {
		return nil, err
	}
	state, err := randomOAuthString(24)
	if err != nil {
		return nil, err
	}
	codeVerifier, codeChallenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	redirectURL := googleRedirectURL(port, s.callbackPath)
	authURL := s.authorizationURL(state, redirectURL, codeChallenge)
	return &GoogleOAuthChallenge{
		AuthURL:      authURL,
		RedirectURL:  redirectURL,
		State:        state,
		CallbackHost: s.callbackHost,
		CallbackPort: port,
		CallbackPath: s.callbackPath,
		CodeVerifier: codeVerifier,
	}, nil
}

func resolveGoogleBeginPort(configured int) (int, error) {
	if configured > 0 {
		return configured, nil
	}
	return findOpenPort()
}

func findOpenPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate callback port: %w", err)
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, fmt.Errorf("invalid callback address")
	}
	return addr.Port, nil
}

func (s *googleAuthService) authorizationURL(state, redirectURL, codeChallenge string) string {
	values := url.Values{}
	values.Set("client_id", s.clientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("response_type", "code")
	values.Set("scope", s.scopes)
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	values.Set("state", state)
	values.Set("code_challenge_method", "S256")
	values.Set("code_challenge", codeChallenge)
	return s.authURL + "?" + values.Encode()
}

func googleRedirectURL(port int, callbackPath string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
}

func (s *googleAuthService) CompleteAuth(
	ctx context.Context,
	challenge *GoogleOAuthChallenge,
	timeout time.Duration,
) (*GoogleOAuthAuth, error) {
	if err := validateGoogleChallenge(challenge); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultGoogleOAuthTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	code, err := s.waitForCallback(waitCtx, challenge)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrGoogleAuthTimeout
		}
		return nil, err
	}
	tokens, err := s.exchangeCode(waitCtx, challenge, code)
	if err != nil {
		return nil, err
	}
	return s.tokensToAuth(tokens), nil
}

func validateGoogleChallenge(challenge *GoogleOAuthChallenge) error {
	if challenge == nil {
		return fmt.Errorf("google oauth challenge is required")
	}
	if strings.TrimSpace(challenge.AuthURL) == "" {
		return fmt.Errorf("google oauth challenge missing auth URL")
	}
	if strings.TrimSpace(challenge.RedirectURL) == "" {
		return fmt.Errorf("google oauth challenge missing redirect URL")
	}
	if strings.TrimSpace(challenge.State) == "" {
		return fmt.Errorf("google oauth challenge missing state")
	}
	return nil
}

func (s *googleAuthService) waitForCallback(ctx context.Context, challenge *GoogleOAuthChallenge) (string, error) {
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

func (s *googleAuthService) newCallbackServer(
	challenge *GoogleOAuthChallenge,
	codeCh chan<- string,
	errCh chan<- error,
) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(challenge.CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		handleGoogleOAuthCallback(w, r, challenge, codeCh, errCh)
	})
	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func runGoogleCallbackServer(server *http.Server, listener net.Listener, errCh chan<- error) {
	err := server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		select {
		case errCh <- err:
		default:
		}
	}
}

func shutdownGoogleCallbackServer(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func waitForGoogleCallbackResult(ctx context.Context, codeCh <-chan string, errCh <-chan error) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-codeCh:
		return code, nil
	case err := <-errCh:
		return "", err
	}
}

func handleGoogleOAuthCallback(
	w http.ResponseWriter,
	r *http.Request,
	challenge *GoogleOAuthChallenge,
	codeCh chan<- string,
	errCh chan<- error,
) {
	query := r.URL.Query()
	if callbackHasError(query, errCh) {
		writeGoogleCallbackResponse(w, http.StatusUnauthorized, "Authentication failed.")
		return
	}
	if query.Get("state") != challenge.State {
		select {
		case errCh <- fmt.Errorf("google oauth state mismatch"):
		default:
		}
		writeGoogleCallbackResponse(w, http.StatusBadRequest, "Invalid state.")
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		select {
		case errCh <- fmt.Errorf("google oauth callback missing code"):
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

func callbackHasError(query url.Values, errCh chan<- error) bool {
	errorCode := strings.TrimSpace(query.Get("error"))
	if errorCode == "" {
		return false
	}
	errorDescription := strings.TrimSpace(query.Get("error_description"))
	if errorCode == "access_denied" {
		select {
		case errCh <- fmt.Errorf("%w: %s", ErrGoogleAuthDenied, errorDescription):
		default:
		}
		return true
	}
	select {
	case errCh <- fmt.Errorf("google oauth callback error: %s %s", errorCode, errorDescription):
	default:
	}
	return true
}

func writeGoogleCallbackResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = io.WriteString(w, message)
}

func (s *googleAuthService) exchangeCode(
	ctx context.Context,
	challenge *GoogleOAuthChallenge,
	code string,
) (*googleOAuthTokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", challenge.RedirectURL)
	values.Set("client_id", s.clientID)
	if strings.TrimSpace(s.clientSecret) != "" {
		values.Set("client_secret", s.clientSecret)
	}
	values.Set("code_verifier", challenge.CodeVerifier)
	body, _, err := s.doGoogleRetriableFormPost(ctx, s.tokenURL, values)
	if err != nil {
		return nil, fmt.Errorf("exchange google oauth code: %w", err)
	}
	return parseGoogleTokenResponse(body)
}

func parseGoogleTokenResponse(body []byte) (*googleOAuthTokenResponse, error) {
	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decode google token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, ErrGoogleMissingToken
	}
	return &token, nil
}

func (s *googleAuthService) Refresh(ctx context.Context, current *GoogleOAuthAuth) (*GoogleOAuthAuth, error) {
	if current == nil {
		return nil, ErrGoogleAuthNotConfigured
	}
	refreshToken := strings.TrimSpace(current.RefreshToken)
	if refreshToken == "" {
		return nil, ErrGoogleMissingRefresh
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", firstNonEmpty(strings.TrimSpace(current.ClientID), s.clientID))
	if strings.TrimSpace(s.clientSecret) != "" {
		values.Set("client_secret", s.clientSecret)
	}
	body, _, err := s.doGoogleRetriableFormPost(ctx, s.tokenURL, values)
	if err != nil {
		return nil, fmt.Errorf("refresh google oauth token: %w", err)
	}
	token, err := parseGoogleTokenResponse(body)
	if err != nil {
		return nil, err
	}
	updated := s.mergeGoogleRefresh(current, token)
	if err := validateGoogleAuth(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *googleAuthService) mergeGoogleRefresh(
	current *GoogleOAuthAuth,
	token *googleOAuthTokenResponse,
) *GoogleOAuthAuth {
	updated := *current
	updated.AccessToken = chooseTokenValue(token.AccessToken, updated.AccessToken)
	updated.RefreshToken = chooseTokenValue(token.RefreshToken, updated.RefreshToken)
	updated.TokenType = chooseTokenValue(token.TokenType, updated.TokenType)
	updated.Scope = chooseTokenValue(token.Scope, updated.Scope)
	updated.IDToken = chooseTokenValue(token.IDToken, updated.IDToken)
	updated.AuthMode = googleOAuthAuthMode
	updated.Issuer = s.issuer
	updated.ClientID = firstNonEmpty(updated.ClientID, s.clientID)
	updated.ObtainedAt = s.now().UTC()
	if token.ExpiresIn > 0 {
		updated.AccessTokenExpiry = s.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &updated
}

func (s *googleAuthService) Resolve(ctx context.Context) (*GoogleOAuthAuth, error) {
	auth, resolved, err := s.resolveGoogleFromSource(ctx, s.resolveGoogleFromEnv, false)
	if resolved || err != nil {
		return auth, err
	}
	auth, resolved, err = s.resolveGoogleFromSource(ctx, s.resolveGoogleFromDotEnv, false)
	if resolved || err != nil {
		return auth, err
	}
	return s.resolveGoogleFromStore(ctx)
}

func (s *googleAuthService) resolveGoogleFromSource(
	ctx context.Context,
	source func() (*GoogleOAuthAuth, error),
	persist bool,
) (*GoogleOAuthAuth, bool, error) {
	auth, err := source()
	if err == nil {
		refreshed, refreshErr := s.maybeRefreshGoogleAuth(ctx, auth, persist)
		return refreshed, true, refreshErr
	}
	if errors.Is(err, ErrGoogleAuthNotConfigured) {
		return nil, false, nil
	}
	return nil, true, err
}

func (s *googleAuthService) resolveGoogleFromStore(ctx context.Context) (*GoogleOAuthAuth, error) {
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrGoogleAuthNotConfigured
	}
	s.applyGoogleAuthDefaults(auth)
	if err := validateGoogleAuth(auth); err != nil {
		return nil, err
	}
	return s.maybeRefreshGoogleAuth(ctx, auth, true)
}

func (s *googleAuthService) resolveGoogleFromEnv() (*GoogleOAuthAuth, error) {
	return s.resolveGoogleFromLookup(s.lookupEnv)
}

func (s *googleAuthService) resolveGoogleFromDotEnv() (*GoogleOAuthAuth, error) {
	values := loadDotEnvValues(s.resolveGoogleDotEnvPaths())
	return s.resolveGoogleFromLookup(mapLookup(values))
}

func (s *googleAuthService) resolveGoogleFromLookup(
	lookup func(string) (string, bool),
) (*GoogleOAuthAuth, error) {
	accessToken := firstEnvValue(lookup, "GOOGLE_OAUTH_ACCESS_TOKEN")
	refreshToken := firstEnvValue(lookup, "GOOGLE_OAUTH_REFRESH_TOKEN")
	if strings.TrimSpace(accessToken) == "" && strings.TrimSpace(refreshToken) == "" {
		return nil, ErrGoogleAuthNotConfigured
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, ErrGoogleMissingToken
	}
	auth := &GoogleOAuthAuth{
		AuthMode:     googleOAuthAuthMode,
		Issuer:       firstNonEmpty(firstEnvValue(lookup, "GOOGLE_OAUTH_ISSUER"), s.issuer),
		ClientID:     firstNonEmpty(firstEnvValue(lookup, "GOOGLE_OAUTH_CLIENT_ID"), s.clientID),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    firstNonEmpty(firstEnvValue(lookup, "GOOGLE_OAUTH_TOKEN_TYPE"), "Bearer"),
		Scope:        firstEnvValue(lookup, "GOOGLE_OAUTH_SCOPE"),
		IDToken:      firstEnvValue(lookup, "GOOGLE_OAUTH_ID_TOKEN"),
		ProjectID: firstNonEmpty(
			firstEnvValue(lookup, "GOOGLE_OAUTH_PROJECT_ID"),
			firstEnvValue(lookup, "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"),
			s.projectID,
		),
		Location: firstNonEmpty(
			firstEnvValue(lookup, "GOOGLE_OAUTH_LOCATION"),
			firstEnvValue(lookup, "GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION"),
			s.location,
		),
	}
	if expiry := firstEnvValue(lookup, "GOOGLE_OAUTH_ACCESS_TOKEN_EXPIRY"); expiry != "" {
		if t, err := time.Parse(time.RFC3339, expiry); err == nil {
			auth.AccessTokenExpiry = t.UTC()
		}
	}
	if err := validateGoogleAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *googleAuthService) resolveGoogleDotEnvPaths() []string {
	if len(s.dotEnvPaths) > 0 {
		return append([]string(nil), s.dotEnvPaths...)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(cwd, ".env"), filepath.Join(cwd, ".env.local")}
}

func (s *googleAuthService) maybeRefreshGoogleAuth(
	ctx context.Context,
	auth *GoogleOAuthAuth,
	persist bool,
) (*GoogleOAuthAuth, error) {
	if !s.shouldRefreshGoogleAuth(auth) {
		return auth, nil
	}
	if strings.TrimSpace(auth.RefreshToken) == "" {
		return auth, nil
	}
	return s.refreshGoogleAuthSingleflight(ctx, auth, persist)
}

func (s *googleAuthService) shouldRefreshGoogleAuth(auth *GoogleOAuthAuth) bool {
	if auth == nil {
		return false
	}
	if auth.AccessTokenExpiry.IsZero() {
		return false
	}
	now := s.now().UTC()
	return now.Add(s.refreshSkew).After(auth.AccessTokenExpiry)
}

func (s *googleAuthService) refreshGoogleAuthSingleflight(
	ctx context.Context,
	auth *GoogleOAuthAuth,
	persist bool,
) (*GoogleOAuthAuth, error) {
	key := "google-refresh:" + strings.TrimSpace(auth.RefreshToken)
	ch := s.refreshGroup.DoChan(key, func() (any, error) {
		return s.executeGoogleRefresh(ctx, auth, persist)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		updated, _ := result.Val.(*GoogleOAuthAuth)
		if updated == nil {
			return nil, ErrGoogleAuthNotConfigured
		}
		return updated, nil
	}
}

func (s *googleAuthService) executeGoogleRefresh(
	ctx context.Context,
	auth *GoogleOAuthAuth,
	persist bool,
) (*GoogleOAuthAuth, error) {
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

func (s *googleAuthService) Save(ctx context.Context, auth *GoogleOAuthAuth) error {
	_ = ctx
	if auth == nil {
		return fmt.Errorf("google auth payload is required")
	}
	s.applyGoogleAuthDefaults(auth)
	if auth.ObtainedAt.IsZero() {
		auth.ObtainedAt = s.now().UTC()
	}
	if err := validateGoogleAuth(auth); err != nil {
		return err
	}
	return s.store.Save(auth)
}

func (s *googleAuthService) Load(ctx context.Context) (*GoogleOAuthAuth, error) {
	_ = ctx
	auth, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrGoogleAuthNotConfigured
	}
	s.applyGoogleAuthDefaults(auth)
	if err := validateGoogleAuth(auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *googleAuthService) Delete(ctx context.Context) error {
	_ = ctx
	return s.store.Delete()
}

func (s *googleAuthService) applyGoogleAuthDefaults(auth *GoogleOAuthAuth) {
	if auth == nil {
		return
	}
	auth.AuthMode = googleOAuthAuthMode
	auth.Issuer = firstNonEmpty(auth.Issuer, s.issuer)
	auth.ClientID = firstNonEmpty(auth.ClientID, s.clientID)
	auth.TokenType = firstNonEmpty(auth.TokenType, "Bearer")
	auth.ProjectID = firstNonEmpty(auth.ProjectID, s.projectID)
	auth.Location = firstNonEmpty(auth.Location, s.location)
}

func validateGoogleAuth(auth *GoogleOAuthAuth) error {
	if auth == nil {
		return ErrGoogleAuthNotConfigured
	}
	if strings.TrimSpace(auth.AccessToken) == "" {
		return ErrGoogleMissingToken
	}
	if strings.TrimSpace(auth.ProjectID) == "" {
		return ErrGoogleMissingProject
	}
	return nil
}

func (s *googleAuthService) tokensToAuth(token *googleOAuthTokenResponse) *GoogleOAuthAuth {
	auth := &GoogleOAuthAuth{
		AuthMode:     googleOAuthAuthMode,
		Issuer:       s.issuer,
		ClientID:     s.clientID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    firstNonEmpty(token.TokenType, "Bearer"),
		Scope:        token.Scope,
		IDToken:      token.IDToken,
		ObtainedAt:   s.now().UTC(),
		ProjectID:    s.projectID,
		Location:     s.location,
	}
	if token.ExpiresIn > 0 {
		auth.AccessTokenExpiry = s.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return auth
}

func (s *googleAuthService) doGoogleRetriableFormPost(
	ctx context.Context,
	endpoint string,
	values url.Values,
) ([]byte, http.Header, error) {
	body := []byte(values.Encode())
	baseBackoff := s.googleRetryBaseBackoff()
	start := s.now()
	var lastErr error
	for attempt := 0; attempt < defaultOAuthMaxAttempts; attempt++ {
		if s.googleRetryBudgetExceeded(start, attempt) {
			break
		}
		respBody, headers, err := s.executeGoogleFormPost(ctx, endpoint, body)
		if err == nil {
			return respBody, headers, nil
		}
		lastErr = err
		retry, wait := s.shouldRetryGoogleFormError(err, headers, attempt, baseBackoff)
		if !retry {
			return nil, headers, err
		}
		if sleepErr := s.sleep(ctx, wait); sleepErr != nil {
			return nil, headers, sleepErr
		}
	}
	return nil, nil, coalesceRetriableError(lastErr)
}

func (s *googleAuthService) googleRetryBaseBackoff() time.Duration {
	if s.pollInterval > 0 {
		return s.pollInterval
	}
	return defaultGoogleOAuthPollInterval
}

func (s *googleAuthService) googleRetryBudgetExceeded(start time.Time, attempt int) bool {
	if s.retryBudget <= 0 || attempt <= 0 {
		return false
	}
	return s.now().Sub(start) >= s.retryBudget
}

func (s *googleAuthService) executeGoogleFormPost(
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

func (s *googleAuthService) shouldRetryGoogleFormError(
	err error,
	headers http.Header,
	attempt int,
	baseBackoff time.Duration,
) (bool, time.Duration) {
	if exhaustedRetriableAttempt(attempt) {
		return false, 0
	}
	if retry, wait := s.retryGoogleNetworkError(err, baseBackoff, attempt); retry {
		return true, wait
	}
	return s.retryGoogleHTTPError(err, headers, baseBackoff, attempt)
}

func (s *googleAuthService) retryGoogleNetworkError(
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

func (s *googleAuthService) retryGoogleHTTPError(
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

func randomOAuthString(bytesLen int) (string, error) {
	if bytesLen <= 0 {
		bytesLen = 32
	}
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func generatePKCE() (string, string, error) {
	verifier, err := randomOAuthString(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func DefaultGoogleOAuthAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sylk", "google_oauth_auth.yaml")
}

func NewDefaultGoogleOAuthStore(path string, lookupEnv func(string) (string, bool)) GoogleOAuthStore {
	fileStore := NewFileGoogleOAuthStore(path)
	keyring := NewSystemKeyringGoogleOAuthStore(path)
	if keyring == nil {
		return fileStore
	}
	return NewFallbackGoogleOAuthStore(keyring, fileStore)
}

type FallbackGoogleOAuthStore struct {
	primary   GoogleOAuthStore
	secondary GoogleOAuthStore
}

func NewFallbackGoogleOAuthStore(primary, secondary GoogleOAuthStore) *FallbackGoogleOAuthStore {
	return &FallbackGoogleOAuthStore{primary: primary, secondary: secondary}
}

func (s *FallbackGoogleOAuthStore) Save(auth *GoogleOAuthAuth) error {
	var errs []error
	if s.primary != nil {
		if err := s.primary.Save(auth); err != nil {
			errs = append(errs, err)
		}
	}
	if s.secondary != nil {
		if err := s.secondary.Save(auth); err != nil {
			errs = append(errs, err)
		}
	}
	return joinStoreErrors(errs)
}

func (s *FallbackGoogleOAuthStore) Load() (*GoogleOAuthAuth, error) {
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

func (s *FallbackGoogleOAuthStore) Delete() error {
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
	return joinStoreErrors(errs)
}

func joinStoreErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

type FileGoogleOAuthStore struct {
	path string
}

func NewFileGoogleOAuthStore(path string) *FileGoogleOAuthStore {
	return &FileGoogleOAuthStore{path: path}
}

func (s *FileGoogleOAuthStore) Save(auth *GoogleOAuthAuth) error {
	if auth == nil {
		return fmt.Errorf("google oauth payload is required")
	}
	if strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("google oauth store path is empty")
	}
	encoded, err := yaml.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal google oauth: %w", err)
	}
	if err := writeFileAtomically(s.path, encoded, 0600); err != nil {
		return fmt.Errorf("persist google oauth file: %w", err)
	}
	return nil
}

func (s *FileGoogleOAuthStore) Load() (*GoogleOAuthAuth, error) {
	if strings.TrimSpace(s.path) == "" {
		return nil, fmt.Errorf("google oauth store path is empty")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read google oauth file: %w", err)
	}
	var auth GoogleOAuthAuth
	if err := yaml.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parse google oauth file: %w", err)
	}
	return &auth, nil
}

func (s *FileGoogleOAuthStore) Delete() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type SystemKeyringGoogleOAuthStore struct {
	service string
	account string
	backend systemKeyringBackend
}

func NewSystemKeyringGoogleOAuthStore(path string) *SystemKeyringGoogleOAuthStore {
	backend := detectSystemKeyringBackend()
	if backend == keyringBackendNone {
		return nil
	}
	return &SystemKeyringGoogleOAuthStore{
		service: "sylk.google.oauth.auth",
		account: keyringAccountForPath(path),
		backend: backend,
	}
}

func (s *SystemKeyringGoogleOAuthStore) Save(auth *GoogleOAuthAuth) error {
	if auth == nil {
		return fmt.Errorf("google oauth payload is required")
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
		return fmt.Errorf("save keyring google oauth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SystemKeyringGoogleOAuthStore) Load() (*GoogleOAuthAuth, error) {
	cmd := s.buildLoadCommand()
	if cmd == nil {
		return nil, nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isKeyringNotFoundError(err, out) {
			return nil, nil
		}
		return nil, fmt.Errorf("load keyring google oauth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode keyring google oauth payload: %w", err)
	}
	var auth GoogleOAuthAuth
	if err := yaml.Unmarshal(raw, &auth); err != nil {
		return nil, fmt.Errorf("parse keyring google oauth payload: %w", err)
	}
	return &auth, nil
}

func (s *SystemKeyringGoogleOAuthStore) Delete() error {
	cmd := s.buildDeleteCommand()
	if cmd == nil {
		return nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isKeyringNotFoundError(err, out) {
			return nil
		}
		return fmt.Errorf("delete keyring google oauth: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SystemKeyringGoogleOAuthStore) buildStoreCommand(secret string) (*exec.Cmd, io.Reader) {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command("security", "add-generic-password", "-U", "-s", s.service, "-a", s.account, "-w", secret), nil
	case keyringBackendLinuxSecretTool:
		cmd := exec.Command("secret-tool", "store", "--label=Sylk Google OAuth", "service", s.service, "account", s.account)
		return cmd, strings.NewReader(secret)
	default:
		return nil, nil
	}
}

func (s *SystemKeyringGoogleOAuthStore) buildLoadCommand() *exec.Cmd {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command("security", "find-generic-password", "-s", s.service, "-a", s.account, "-w")
	case keyringBackendLinuxSecretTool:
		return exec.Command("secret-tool", "lookup", "service", s.service, "account", s.account)
	default:
		return nil
	}
}

func (s *SystemKeyringGoogleOAuthStore) buildDeleteCommand() *exec.Cmd {
	switch s.backend {
	case keyringBackendDarwinSecurity:
		return exec.Command("security", "delete-generic-password", "-s", s.service, "-a", s.account)
	case keyringBackendLinuxSecretTool:
		return exec.Command("secret-tool", "clear", "service", s.service, "account", s.account)
	default:
		return nil
	}
}

type googleOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// googleOAuthTransport injects a fresh Bearer token into every request.
type googleOAuthTransport struct {
	base    http.RoundTripper
	service GoogleAuthService
}

func (t *googleOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	auth, err := t.service.Resolve(req.Context())
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	return t.base.RoundTrip(cloned)
}

// NewGoogleOAuthHTTPClient creates an HTTP client that injects Bearer tokens
// resolved from GoogleAuthService for every request.
func NewGoogleOAuthHTTPClient(service GoogleAuthService, base *http.Client) *http.Client {
	if base == nil {
		base = newDefaultHTTPClient(defaultHTTPTimeout)
	}
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	cloned := *base
	cloned.Transport = &googleOAuthTransport{
		base:    rt,
		service: service,
	}
	return &cloned
}

func fullJitterWithCeil(base, ceil time.Duration, attempt int, jitter float64) time.Duration {
	raw := float64(base) * math.Pow(2, float64(attempt))
	capped := time.Duration(raw)
	if capped > ceil {
		capped = ceil
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

func resolveGoogleRandomFloat64(randFloat64 func() float64) func() float64 {
	if randFloat64 != nil {
		return randFloat64
	}
	return mathrand.Float64
}
