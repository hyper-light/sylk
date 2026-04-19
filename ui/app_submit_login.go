package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/storage"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/browser"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/login"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/redact"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

func (m *AppModel) handleSubmit(submit msg.SubmitPromptMsg) tea.Cmd {
	targetAgent := m.resolveSubmitTarget(submit.TargetAgent)
	submit.TargetAgent = targetAgent
	submit.SessionID = m.resolveRouteSessionID(submit.SessionID)

	// If the target agent is already streaming, steer it. Otherwise dispatch
	// normally — multiple agents can run concurrently.
	steerTarget := targetAgent
	if steerTarget == "" {
		steerTarget = m.engagedAgentID
	}
	if stream := m.activeStreamForAgent(steerTarget); stream != nil {
		return m.publishSteerAction(submit.Text)
	}

	// Push a user entry to chat.
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceUser,
		SessionID: submit.SessionID,
		Content:   submit.Text,
		Height:    -1,
	}
	m.chat.PushEntry(entry)

	// Push a system message before the thinking placeholder so it appears above the response.
	sysEntry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		SessionID: submit.SessionID,
		Content:   routingStatusText(targetAgent),
		Height:    -1,
	}
	m.chat.PushEntry(sysEntry)

	// Push a thinking placeholder so the spinner appears immediately.
	m.chat.BeginThinking(thinkingAgentType(targetAgent))

	// Route through Guide bus.
	return m.publishRouteRequest(submit)
}

func isInlineExCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), ":")
}

// ---------------------------------------------------------------------------
// Chat commands (/login, etc.)
// ---------------------------------------------------------------------------

// parseChatCommand returns the command name if text starts with "/".
func parseChatCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	cmd := strings.TrimPrefix(trimmed, "/")
	cmd = strings.SplitN(cmd, " ", 2)[0]
	cmd = strings.ToLower(cmd)
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

func parseChatCommandArgs(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return ""
	}
	parts := strings.SplitN(body, " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// handleChatCommand dispatches a "/command" from chat input.
func (m *AppModel) handleChatCommand(cmd string, submit msg.SubmitPromptMsg) tea.Cmd {
	switch cmd {
	case "clear":
		m.chat.Clear()
		return nil
	case "debug-ui":
		return m.handleDebugUICommand(parseChatCommandArgs(submit.Text))
	case "login":
		return m.handleLoginCommand(submit)
	default:
		m.pushSystemChat(fmt.Sprintf("Unknown command: /%s", cmd))
		return nil
	}
}

// pushSystemChat adds a system message to the chat panel.
func (m *AppModel) pushSystemChat(content string) {
	m.chat.PushEntry(&chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		Content:   content,
		Height:    -1,
	})
}

// handleLoginCommand activates the top-panel login flow for /login.
func (m *AppModel) handleLoginCommand(submit msg.SubmitPromptMsg) tea.Cmd {
	// Echo the user command to chat.
	m.suppressLoginResult = false
	m.loginPanel.Activate()
	inputH := m.inputHeight()
	mainH := max(m.height-inputH-statusBarHeight, 1)
	m.loginPanel.SetSize(m.width, mainH)
	m.overlay = overlayLogin
	m.viewDirty = true
	return nil
}

// deactivateLogin closes the login panel and restores the normal view.
func (m *AppModel) deactivateLogin() {
	m.cancelLoginFlow()
	m.loginPanel.Deactivate()
	m.overlay = overlayNone
	m.viewDirty = true
	m.invalidateRenderedSlots()
}

func (m *AppModel) cancelLoginFlow() {
	if m.oauthSessions == nil {
		m.pendingAnthropicOAuth = nil
		return
	}
	m.oauthSessions.CancelCurrent()
	m.pendingAnthropicOAuth = nil
}

// handleLoginPanelResult processes results from the login panel.
func (m *AppModel) handleLoginPanelResult(result login.LoginResult) tea.Cmd {
	switch result.Action {
	case login.ActionAPIKey:
		// Keep panel open — handleLoginResult will close on success
		// or show an inline error on failure.
		return func() tea.Msg {
			err := saveAPIKeySecure(m.ctx, result.Provider, result.APIKey)
			return msg.LoginResultMsg{
				Provider: result.Provider,
				Method:   "apikey",
				Success:  err == nil,
				Error:    loginErrorString(err),
			}
		}

	case login.ActionOAuthStart:
		// Keep panel visible in OAuth step for status updates.
		return m.startOAuthFlow(result.Provider)

	case login.ActionOAuthSubmit:
		m.loginPanel.SetOAuthStatus("Completing OAuth authorization...")
		return m.completeOAuthCodeFlow(result.Provider, result.OAuthCode)

	case login.ActionCancelled:
		m.suppressLoginResult = true
		m.deactivateLogin()
		return nil

	default:
		return nil
	}
}

// startOAuthFlow launches the appropriate OAuth flow for the provider.
func (m *AppModel) startOAuthFlow(provider string) tea.Cmd {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if !supportsOAuthProvider(provider) {
		m.loginPanel.SetError(fmt.Sprintf("OAuth not supported for %s", provider))
		return nil
	}
	if m.oauthSessions == nil {
		m.oauthSessions = newOAuthSessionManager()
	}
	flowID := m.oauthSessions.Begin(provider)
	switch provider {
	case "google":
		return m.startGoogleOAuthCmd(provider, flowID)
	case "openai":
		return m.startOpenAIOAuthCmd(provider, flowID)
	case "anthropic":
		return m.startAnthropicOAuthCmd(provider, flowID)
	default:
		return nil
	}
}

func (m *AppModel) completeOAuthCodeFlow(provider, code string) tea.Cmd {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "anthropic" {
		m.loginPanel.SetError(fmt.Sprintf("OAuth code flow is not supported for %s", provider))
		return nil
	}
	pending := m.pendingAnthropicOAuth
	if pending == nil || pending.service == nil || pending.challenge == nil {
		m.loginPanel.SetError("No active Anthropic OAuth flow. Start OAuth again.")
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		auth, err := pending.service.CompleteAuthCode(ctx, pending.challenge, code)
		if err == nil {
			err = pending.service.Save(ctx, auth)
		}
		if err != nil {
			return msg.LoginResultMsg{
				Provider: provider,
				Method:   "oauth",
				FlowID:   pending.flowID,
				Error:    err.Error(),
			}
		}
		return msg.LoginResultMsg{
			Provider: provider,
			Method:   "oauth",
			FlowID:   pending.flowID,
			Success:  true,
		}
	}
}

func supportsOAuthProvider(provider string) bool {
	switch provider {
	case "google", "openai", "anthropic":
		return true
	default:
		return false
	}
}

// oauthTimeout is the max duration for an OAuth flow before it's considered failed.
// Derived from: browser-based OAuth typically completes within a few minutes;
// 10 minutes is generous enough for slow networks or manual steps.
const oauthTimeout = 10 * time.Minute

type oauthSessionStartedMsg struct {
	Provider           string
	FlowID             uint64
	URL                string
	UserCode           string
	NeedsCode          bool
	Instructions       string
	AnthropicService   oauth.AnthropicAuthService
	AnthropicChallenge *oauth.AnthropicOAuthChallenge
	Wait               tea.Cmd
	Cancel             context.CancelFunc
}

type pendingAnthropicOAuthCode struct {
	flowID    uint64
	service   oauth.AnthropicAuthService
	challenge *oauth.AnthropicOAuthChallenge
	cancel    context.CancelFunc
}

// startGoogleOAuthCmd starts Google OAuth and emits a session-started message.
func (m *AppModel) startGoogleOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		authSvc := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{})
		session, err := oauth.StartGoogleOAuthSession(ctx, authSvc, oauthTimeout)
		if err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider: provider,
			FlowID:   flowID,
			URL:      session.Challenge.AuthURL,
			Wait:     waitGoogleOAuthResultCmd(provider, flowID, session),
			Cancel:   session.Cancel,
		}
	}
}

// startOpenAIOAuthCmd starts OpenAI device auth and emits a session-started message.
func (m *AppModel) startOpenAIOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx

	return func() tea.Msg {
		authSvc := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{})
		session, err := oauth.StartDeviceAuthSession(ctx, authSvc, oauthTimeout)
		if err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider: provider,
			FlowID:   flowID,
			URL:      session.Challenge.VerificationURL,
			UserCode: session.Challenge.UserCode,
			Wait:     waitOpenAIOAuthResultCmd(provider, flowID, session),
			Cancel:   session.Cancel,
		}
	}
}

// startAnthropicOAuthCmd starts Anthropic OAuth and emits a session-started message.
func (m *AppModel) startAnthropicOAuthCmd(provider string, flowID uint64) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		authSvc := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{})
		flowCtx, cancel := context.WithCancel(ctx)
		challenge, err := authSvc.BeginAuth(flowCtx)
		if err != nil {
			cancel()
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: err.Error()}
		}
		return oauthSessionStartedMsg{
			Provider:           provider,
			FlowID:             flowID,
			URL:                challenge.AuthURL,
			NeedsCode:          true,
			Instructions:       "Paste the authorization code shown in your browser.",
			AnthropicService:   authSvc,
			AnthropicChallenge: challenge,
			Cancel:             cancel,
		}
	}
}

func waitGoogleOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.GoogleOAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func waitOpenAIOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.DeviceAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func waitAnthropicOAuthResultCmd(
	provider string,
	flowID uint64,
	session *oauth.AnthropicOAuthSession,
) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-session.Results
		if !ok {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: "oauth session ended unexpectedly"}
		}
		if result.Err != nil {
			return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Error: result.Err.Error()}
		}
		return msg.LoginResultMsg{Provider: provider, Method: "oauth", FlowID: flowID, Success: true}
	}
}

func (m *AppModel) handleOAuthSessionStarted(start oauthSessionStartedMsg) tea.Cmd {
	if m.oauthSessions == nil {
		m.oauthSessions = newOAuthSessionManager()
	}
	if m.overlay != overlayLogin || !m.loginPanel.Active() || m.suppressLoginResult {
		m.oauthSessions.Abort(start.Provider, start.FlowID, start.Cancel)
		return nil
	}
	if !m.oauthSessions.Attach(start.Provider, start.FlowID, start.Cancel) {
		return nil
	}
	if start.NeedsCode && strings.EqualFold(start.Provider, "anthropic") {
		if start.AnthropicService == nil || start.AnthropicChallenge == nil {
			m.oauthSessions.Abort(start.Provider, start.FlowID, start.Cancel)
			m.loginPanel.SetError("Anthropic OAuth could not be initialized")
			return nil
		}
		m.pendingAnthropicOAuth = &pendingAnthropicOAuthCode{
			flowID:    start.FlowID,
			service:   start.AnthropicService,
			challenge: start.AnthropicChallenge,
			cancel:    start.Cancel,
		}
		m.loginPanel.SetOAuthCodeEntry(true, start.Instructions)
		m.loginPanel.SetDeviceCode(start.UserCode)
		m.loginPanel.SetOAuthURL(start.URL)
		m.loginPanel.SetOAuthStatus(formatOAuthStatus(start.Provider))
		if err := openURL(start.URL); err != nil {
			m.statusBar.SetFlash("Could not open browser automatically. Use the URL shown in the login panel.")
		}
		return nil
	}

	m.pendingAnthropicOAuth = nil
	m.loginPanel.SetOAuthCodeEntry(false, "")
	m.loginPanel.SetDeviceCode(start.UserCode)
	m.loginPanel.SetOAuthURL(start.URL)
	m.loginPanel.SetOAuthStatus(formatOAuthStatus(start.Provider))
	if err := openURL(start.URL); err != nil {
		m.statusBar.SetFlash("Could not open browser automatically. Use the URL shown in the login panel.")
	}
	return start.Wait
}

func formatOAuthStatus(provider string) string {
	return fmt.Sprintf("Authorize %s in your browser:", loginProviderLabel(provider))
}

// handleLoginResult processes the async LoginResultMsg from an auth flow.
func (m *AppModel) handleLoginResult(result msg.LoginResultMsg) tea.Cmd {
	if !m.acceptLoginResult(result) {
		return nil
	}
	if result.Success {
		m.handleSuccessfulLoginResult(result)
		return nil
	}
	m.handleFailedLoginResult(result)
	return nil
}

func (m *AppModel) acceptLoginResult(result msg.LoginResultMsg) bool {
	if !m.completeOAuthLoginResult(result) {
		return false
	}
	if m.suppressLoginResult && !result.Success {
		m.suppressLoginResult = false
		return false
	}
	m.suppressLoginResult = false
	return true
}

func (m *AppModel) completeOAuthLoginResult(result msg.LoginResultMsg) bool {
	if result.Method != "oauth" {
		return true
	}
	if m.oauthSessions == nil {
		return false
	}
	if m.keepAnthropicOAuthPending(result) {
		return true
	}
	if !m.oauthSessions.Complete(result.Provider, result.FlowID) {
		return false
	}
	m.finishAnthropicOAuthResult(result.Provider)
	return true
}

func (m *AppModel) keepAnthropicOAuthPending(result msg.LoginResultMsg) bool {
	return strings.EqualFold(result.Provider, "anthropic") &&
		!result.Success &&
		m.pendingAnthropicOAuth != nil &&
		m.pendingAnthropicOAuth.flowID == result.FlowID
}

func (m *AppModel) finishAnthropicOAuthResult(provider string) {
	if !strings.EqualFold(provider, "anthropic") {
		return
	}
	if m.pendingAnthropicOAuth != nil && m.pendingAnthropicOAuth.cancel != nil {
		m.pendingAnthropicOAuth.cancel()
	}
	m.pendingAnthropicOAuth = nil
	m.loginPanel.SetOAuthCodeEntry(false, "")
}

func (m *AppModel) handleSuccessfulLoginResult(result msg.LoginResultMsg) {
	if m.loginPanel.Active() {
		m.deactivateLogin()
	}
	m.statusBar.SetFlash(fmt.Sprintf("Authenticated with %s via %s",
		loginProviderLabel(result.Provider), loginMethodLabel(result.Method)))
	m.recordAuthPreference(result.Provider, result.Method)
	m.statusBar.SetAuthStatus(result.Provider, true)
}

func (m *AppModel) recordAuthPreference(provider, method string) {
	if m.deps.AuthRegistry != nil {
		m.deps.AuthRegistry.NotifyCredentialChanged(provider, method)
		m.agentPanel.SetOpenAIAuthMethod(m.deps.AuthRegistry.ActiveMethod("openai"))
		return
	}
	_ = credentials.SaveAuthPref(provider, method)
	if strings.EqualFold(provider, "openai") {
		m.agentPanel.SetOpenAIAuthMethod(method)
	}
}

func (m *AppModel) handleFailedLoginResult(result msg.LoginResultMsg) {
	errText := redactSecrets(result.Error)
	if m.loginPanel.Active() {
		m.loginPanel.SetError(errText)
		return
	}
	m.statusBar.SetFlash(fmt.Sprintf("Login failed: %s", errText))
}

// handleModelChange spawns a background command to swap the agent's LLM model.
func (m *AppModel) handleModelChange(change msg.ModelChangeMsg) tea.Cmd {
	if m.deps.ModelSwap == nil {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		err := m.deps.ModelSwap(ctx, change.AgentType, change.ModelID)
		return msg.ModelSwapResultMsg{
			AgentID:   change.AgentID,
			AgentType: change.AgentType,
			ModelID:   change.ModelID,
			Err:       err,
		}
	}
}

// handleModelSwapResult processes the result of a backend model swap.
// On success, persists the selection. On error, reverts the optimistic UI
// update and logs a warning.
func (m *AppModel) handleModelSwapResult(result msg.ModelSwapResultMsg) tea.Cmd {
	if result.Err != nil {
		// Revert the UI's optimistic ModelID update to the persisted model.
		prev := m.agentPanel.PersistedModelFor(result.AgentType)
		m.agentPanel.RevertModelID(result.AgentID, prev)
		slog.Warn("model swap failed",
			"agent", result.AgentID,
			"agent_type", result.AgentType,
			"model", result.ModelID,
			"error", result.Err)
		return nil
	}
	if m.deps.ModelSave != nil {
		provider := agentpkg.DeriveProvider(result.ModelID)
		m.deps.ModelSave(result.AgentType, provider, result.ModelID)
	}
	return nil
}

// loginProviderLabel returns a human-readable label for a provider ID.
func loginProviderLabel(provider string) string {
	switch provider {
	case "google":
		return "Google (Gemini)"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	default:
		return provider
	}
}

// loginMethodLabel returns a human-readable label for an auth method.
func loginMethodLabel(method string) string {
	switch method {
	case "oauth":
		return "OAuth"
	case "apikey":
		return "API key"
	default:
		return method
	}
}

func saveAPIKeySecure(ctx context.Context, provider, key string) error {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return err
	}
	if err := dirs.EnsureAll(); err != nil {
		return err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return err
	}
	if err := manager.SetAPIKey(ctx, provider, key, nil); err != nil {
		return err
	}
	return removeLegacyAPIKeyEntry(provider)
}

func resolveLoginPanelAPIKey(provider string) string {
	return resolveLoginPanelAPIKeyWithResolvers(
		provider,
		resolveSecureProviderAPIKey,
		llm.ResolveAPIKey,
	)
}

func resolveLoginPanelAPIKeyWithResolvers(
	provider string,
	secureResolver func(string) (string, error),
	legacyResolver func(string) (string, error),
) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return ""
	}
	if secureResolver != nil {
		if key, err := secureResolver(normalized); err == nil && llm.ValidateKeyFormat(normalized, key) {
			return key
		}
	}
	if legacyResolver == nil {
		return ""
	}
	key, err := legacyResolver(normalized)
	if err != nil || !llm.ValidateKeyFormat(normalized, key) {
		return ""
	}
	return key
}

func resolveSecureProviderAPIKey(provider string) (string, error) {
	dirs, err := storage.ResolveDirs()
	if err != nil {
		return "", err
	}
	manager, err := credentials.NewManager(dirs, "default")
	if err != nil {
		return "", err
	}
	return manager.GetAPIKey(provider)
}

func removeLegacyAPIKeyEntry(provider string) error {
	creds, err := llm.LoadCredentials()
	if err != nil {
		return err
	}
	if _, ok := creds[provider]; !ok {
		return nil
	}
	delete(creds, provider)
	if len(creds) == 0 {
		path := strings.TrimSpace(llm.DefaultCredentialsPath())
		if path == "" {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return llm.SaveCredentials(creds)
}

func loginErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func redactSecrets(text string) string {
	return redact.Text(text)
}

// openURL attempts to open an OAuth URL in the user's default browser.
func openURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme")
	}
	return browser.Open(parsed.String())
}

var explicitTargetAliases = map[string]string{
	"arch":    "architect",
	"planner": "architect",
}
