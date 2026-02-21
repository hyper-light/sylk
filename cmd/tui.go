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

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
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

// liveFallbackClassifierDefaultTarget controls where free-form prompts route
// when live Guide falls back to the local mock classifier.
const liveFallbackClassifierDefaultTarget = "guide"

// mockModeClassifierDefaultTarget controls where free-form prompts route in
// `tui --mock`. Use Architect so mock mode exercises the real planning agent.
const mockModeClassifierDefaultTarget = "architect"
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
	var arch *architect.Architect
	g, err := bootstrapGuide(ctx, guideBus, mockMode)
	if err != nil {
		activityBus.Close()
		_ = guideBus.Close()
		return ui.Deps{}, nil, fmt.Errorf("guide: %w", err)
	}
	arch, err = bootstrapArchitect(guideBus)
	if err != nil {
		_ = g.Stop()
		activityBus.Close()
		_ = guideBus.Close()
		return ui.Deps{}, nil, fmt.Errorf("architect: %w", err)
	}
	if err := registerArchitectWithGuide(g, arch); err != nil {
		_ = arch.Stop()
		_ = g.Stop()
		activityBus.Close()
		_ = guideBus.Close()
		return ui.Deps{}, nil, fmt.Errorf("register architect: %w", err)
	}

	cleanup := func() error {
		var errs []error
		if arch != nil {
			if err := arch.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
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
		AuthRefresh:    buildAuthRefreshHook(g, arch),
	}

	return deps, cleanup, nil
}

// bootstrapMockGuide creates and starts the Guide with a mock classifier that
// routes all natural language queries to Guide itself.
func bootstrapGuide(ctx context.Context, bus guide.EventBus, mockMode bool) (*guide.Guide, error) {
	if mockMode {
		return bootstrapMockGuide(ctx, bus)
	}
	return bootstrapLiveGuide(ctx, bus)
}

// bootstrapLiveGuide creates and starts a Guide that prefers Gemini routing.
// If Gemini auth is unavailable, it falls back to a local mock classifier so
// chat remains functional and explicit @agent routing continues to work.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus) (*guide.Guide, error) {
	cfg := guide.Config{
		Bus:     bus,
		AgentID: "guide",
	}
	selfResponder, err := buildMockGuideSelfResponder(ctx)
	if err != nil {
		return nil, err
	}
	cfg.SelfResponder = selfResponder
	client, err := buildGuideGeminiClient(ctx, false)
	if err == nil && client != nil {
		g, newErr := guide.NewWithGeminiClient(client, cfg)
		if newErr != nil {
			return nil, newErr
		}
		if startErr := g.Start(ctx); startErr != nil {
			return nil, startErr
		}
		return g, nil
	}
	anthropicKey := resolveProviderAPIKey("anthropic")
	if anthropicKey != "" {
		g, newErr := guide.NewWithAPIKey(anthropicKey, cfg)
		if newErr == nil {
			if startErr := g.Start(ctx); startErr == nil {
				return g, nil
			}
		}
	}
	mockClient := &guide.MockClassifierClient{DefaultTarget: liveFallbackClassifierDefaultTarget}
	g, newErr := guide.NewWithClassifier(mockClient, cfg)
	if newErr != nil {
		return nil, newErr
	}
	if startErr := g.Start(ctx); startErr != nil {
		return nil, startErr
	}
	return g, nil
}

func bootstrapMockGuide(ctx context.Context, bus guide.EventBus) (*guide.Guide, error) {
	mockClient := &guide.MockClassifierClient{DefaultTarget: mockModeClassifierDefaultTarget}
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

func bootstrapArchitect(bus guide.EventBus) (*architect.Architect, error) {
	cfg := architect.Config{
		EnableLLM: true,
		Model:     architect.DefaultArchitectModel,
	}
	a, err := architect.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := a.Start(bus); err != nil {
		return nil, err
	}
	return a, nil
}

func registerArchitectWithGuide(g *guide.Guide, a *architect.Architect) error {
	if g == nil || a == nil {
		return nil
	}
	if err := g.RegisterRouter(a); err != nil {
		return err
	}
	g.MarkAgentReady("architect")
	return nil
}

func buildAuthRefreshHook(g *guide.Guide, arch *architect.Architect) func(ctx context.Context, provider string) error {
	return func(ctx context.Context, provider string) error {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "google":
			return refreshGuideGoogleAuth(ctx, g)
		case "anthropic":
			refreshArchitectAuth(arch)
			return nil
		default:
			return nil
		}
	}
}

func refreshGuideGoogleAuth(ctx context.Context, g *guide.Guide) error {
	if g == nil {
		return nil
	}
	client, err := buildGuideGeminiClient(ctx, false)
	if err != nil {
		return err
	}
	g.SetClassifier(guide.NewGeminiClassifier(client, guide.DefaultRouterConfig()))
	g.SetSelfResponder(guide.NewFallbackGuideResponder(
		guide.NewGeminiGuideResponder(client, guide.DefaultRouterConfig()),
		guide.NewStaticGuideResponder(),
	))
	if cache := g.RouteCache(); cache != nil {
		cache.Clear()
	}
	return nil
}

func refreshArchitectAuth(arch *architect.Architect) {
	if arch == nil {
		return
	}
	arch.RefreshPlannerAuth()
}

func buildMockGuideSelfResponder(ctx context.Context) (guide.GuideSelfResponder, error) {
	client, err := buildGuideGeminiClient(ctx, false)
	if err != nil {
		return guide.NewStaticGuideResponder(), nil
	}
	return guide.NewGeminiGuideResponder(client, guide.DefaultRouterConfig()), nil
}

func buildGuideGeminiClient(ctx context.Context, allowInteractiveOAuth bool) (*genai.Client, error) {
	authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
	apiKey := resolveGeminiAPIKey()
	auth, err := resolveGuideOAuthAuth(ctx, authSvc, apiKey != "", allowInteractiveOAuth)
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

func resolveGuideOAuthAuth(
	ctx context.Context,
	authSvc oauth.GoogleAuthService,
	allowAPIKeyFallback bool,
	allowInteractive bool,
) (*oauth.GoogleOAuthAuth, error) {
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
	if !allowInteractive {
		return nil, oauth.ErrGoogleAuthNotConfigured
	}
	session, startErr := oauth.StartGoogleOAuthSession(ctx, authSvc, guideOAuthLoginTimeout)
	if startErr != nil {
		return nil, fmt.Errorf("start google oauth: %w", startErr)
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
	return resolveProviderAPIKey("google")
}

func resolveProviderAPIKey(provider string) string {
	if key := resolveProviderFromEnv(provider); key != "" {
		return key
	}
	if key := resolveProviderFromSecureStore(provider); key != "" {
		return key
	}
	key, err := llm.ResolveAPIKey(provider)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func resolveProviderFromEnv(provider string) string {
	for _, key := range providerEnvCandidates(provider) {
		if value := resolveEnvValue(key); value != "" {
			return value
		}
	}
	return ""
}

func providerEnvCandidates(provider string) []string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case "anthropic":
		return []string{"ANTHROPIC_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	default:
		return []string{strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"}
	}
}

func resolveProviderFromSecureStore(provider string) string {
	dirs, err := storage.ResolveDirs()
	if err != nil || dirs == nil {
		return ""
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return ""
	}
	key, err := manager.GetAPIKey(provider)
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
