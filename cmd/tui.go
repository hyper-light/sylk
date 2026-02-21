package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui"
	"github.com/spf13/cobra"
	"google.golang.org/genai"
)

var (
	tuiTheme string
	tuiMock  bool
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive terminal UI",
	Long:  `Launch Sylk's terminal UI with multi-agent chat, session management, and code viewing.`,
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	tuiCmd.Flags().StringVar(&tuiTheme, "theme", "dark", "Color theme (dark or light)")
	tuiCmd.Flags().BoolVar(&tuiMock, "mock", false, "Run with mock backend (no real agents)")
}

func runTUI(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	deps, cleanup, err := bootstrapDeps(ctx, tuiMock)
	if err != nil {
		stop()
		return fmt.Errorf("bootstrap: %w", err)
	}
	// Pass stop to ui.Run so it can restore default signal handling
	// between p.Run() returning and app.Shutdown() — allowing a second
	// Ctrl+C to force-kill the process during slow shutdown.
	deps.SignalStop = stop

	cfg := ui.DefaultConfig()
	cfg.ThemeMode = parseThemeMode(tuiTheme)
	cfg.MockMode = tuiMock

	runErr := ui.Run(ctx, cfg, deps)
	stop()
	cleanupErr := cleanup()
	return errors.Join(runErr, cleanupErr)
}

// activityBusBuffer is the channel size for the activity event bus.
const activityBusBuffer = 1000

// mockClassifierDefaultTarget controls where free-form prompts route in --mock mode.
// Use Guide itself so users can exercise real guide behavior without external backends.
const mockClassifierDefaultTarget = "guide"
const guideOAuthLoginTimeout = 10 * time.Minute

// bootstrapDeps initializes the core systems needed by the TUI.
// Returns a Deps struct and a cleanup function.
func bootstrapDeps(ctx context.Context, mockMode bool) (ui.Deps, func() error, error) {
	// TUI goroutines are infrastructure, not agent workloads.
	// A nil budget skips agent-level budget tracking.
	scope := concurrency.NewGoroutineScope(ctx, "tui", nil)
	// TUI infrastructure goroutines (LSP readloops, bridges, fan-in) must
	// survive for the entire editing session. The default 5m max lifetime
	// would kill these, breaking diagnostics, hover, and references.
	scope.SetMaxLifetime(24 * time.Hour)

	activityBus := events.NewActivityEventBus(activityBusBuffer)
	activityBus.Start()

	sessionMgr := session.NewManager(session.ManagerConfig{
		Scope: scope,
	})

	guideBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	streamMgr := guide.NewStreamManager(guide.DefaultStreamConfig())

	var g *guide.Guide
	if mockMode {
		var err error
		g, err = bootstrapMockGuide(ctx, guideBus)
		if err != nil {
			activityBus.Close()
			_ = guideBus.Close()
			return ui.Deps{}, nil, fmt.Errorf("mock guide: %w", err)
		}
	}

	cleanup := func() error {
		var errs []error
		if g != nil {
			if err := g.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := guideBus.Close(); err != nil {
			errs = append(errs, err)
		}
		activityBus.Close()
		return errors.Join(errs...)
	}

	deps := ui.Deps{
		ActivityBus:    activityBus,
		SessionManager: sessionMgr,
		GuideBus:       guideBus,
		StreamManager:  streamMgr,
		Guide:          g,
		Scope:          scope,
	}

	return deps, cleanup, nil
}

// bootstrapMockGuide creates and starts the Guide with a mock classifier that
// routes all natural language queries to Guide itself.
func bootstrapMockGuide(ctx context.Context, bus guide.EventBus) (*guide.Guide, error) {
	mockClient := &guide.MockClassifierClient{DefaultTarget: mockClassifierDefaultTarget}
	cfg := guide.Config{
		Bus:     bus,
		AgentID: "guide",
	}
	selfResponder, err := buildMockGuideSelfResponder(ctx)
	if err != nil {
		return nil, err
	}
	cfg.SelfResponder = selfResponder

	g, err := guide.NewWithClassifier(mockClient, cfg)
	if err != nil {
		return nil, err
	}

	if err := g.Start(ctx); err != nil {
		return nil, err
	}

	return g, nil
}

func buildMockGuideSelfResponder(ctx context.Context) (guide.GuideSelfResponder, error) {
	client, err := buildGuideGeminiClient(ctx)
	if err != nil {
		return nil, err
	}
	return guide.NewGeminiGuideResponder(client, guide.DefaultRouterConfig()), nil
}

func buildGuideGeminiClient(ctx context.Context) (*genai.Client, error) {
	authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
	apiKey := resolveGeminiAPIKey()
	auth, err := resolveGuideOAuthAuth(ctx, authSvc, apiKey != "")
	if err == nil && auth != nil {
		return newGuideOAuthGeminiClient(ctx, authSvc, auth)
	}
	if apiKey != "" {
		return genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey, Backend: genai.BackendGeminiAPI})
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("google oauth is not configured and no Gemini API key is available")
}

func resolveGuideOAuthAuth(ctx context.Context, authSvc oauth.GoogleAuthService, allowAPIKeyFallback bool) (*oauth.GoogleOAuthAuth, error) {
	auth, err := authSvc.Resolve(ctx)
	if err == nil {
		return auth, nil
	}
	if allowAPIKeyFallback {
		return nil, nil
	}
	if !errors.Is(err, oauth.ErrGoogleAuthNotConfigured) {
		return nil, fmt.Errorf("resolve google oauth auth: %w", err)
	}
	return startGuideOAuthSession(ctx, authSvc)
}

func startGuideOAuthSession(ctx context.Context, authSvc oauth.GoogleAuthService) (*oauth.GoogleOAuthAuth, error) {
	session, err := oauth.StartGoogleOAuthSession(ctx, authSvc, guideOAuthLoginTimeout)
	if err != nil {
		return nil, fmt.Errorf("start google oauth: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\nGuide requires Google OAuth login.\nOpen this URL to authenticate:\n%s\n\n", session.Challenge.AuthURL)
	result, ok := <-session.Results
	if !ok {
		return nil, fmt.Errorf("google oauth session ended unexpectedly")
	}
	if result.Err != nil {
		return nil, fmt.Errorf("complete google oauth: %w", result.Err)
	}
	return result.Auth, nil
}

func newGuideOAuthGeminiClient(
	ctx context.Context,
	authSvc oauth.GoogleAuthService,
	auth *oauth.GoogleOAuthAuth,
) (*genai.Client, error) {
	if auth == nil {
		return nil, fmt.Errorf("google oauth auth payload is missing")
	}
	projectID := strings.TrimSpace(auth.ProjectID)
	if projectID == "" {
		return nil, fmt.Errorf("google oauth is missing project_id (set GOOGLE_CLOUD_PROJECT or GOOGLE_OAUTH_PROJECT_ID)")
	}
	location := strings.TrimSpace(auth.Location)
	if location == "" {
		location = "us-central1"
	}
	return genai.NewClient(ctx, &genai.ClientConfig{
		Backend:    genai.BackendVertexAI,
		Project:    projectID,
		Location:   location,
		HTTPClient: oauth.NewGoogleOAuthHTTPClient(authSvc, nil),
	})
}

func resolveGeminiAPIKey() string {
	if key := resolveEnvValue("GEMINI_API_KEY"); key != "" {
		return key
	}
	if key := resolveEnvValue("GOOGLE_API_KEY"); key != "" {
		return key
	}
	key, err := llm.ResolveAPIKey("google")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveEnvValue(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	if value := resolveFromDotEnv(".env.local", key); value != "" {
		return value
	}
	return resolveFromDotEnv(".env", key)
}

func resolveFromDotEnv(path string, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if value, ok := parseDotEnvLine(line, key); ok {
			return value
		}
	}
	return ""
}

func parseDotEnvLine(line string, key string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "export ")
	name, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", false
	}
	if strings.TrimSpace(name) != key {
		return "", false
	}
	value = strings.TrimSpace(value)
	return strings.Trim(value, `"'`), true
}

// parseThemeMode converts a theme flag string to a ThemeMode.
func parseThemeMode(s string) ui.ThemeMode {
	if s == "light" {
		return ui.ThemeLight
	}
	return ui.ThemeDark
}
