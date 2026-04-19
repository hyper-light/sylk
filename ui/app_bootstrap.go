package ui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/search/git"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/bridge"
	"github.com/adalundhe/sylk/ui/chat"
	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/committree"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/editor"
	"github.com/adalundhe/sylk/ui/editor/preview"
	"github.com/adalundhe/sylk/ui/editor/register"
	"github.com/adalundhe/sylk/ui/fieldmanual"
	"github.com/adalundhe/sylk/ui/filetree"
	"github.com/adalundhe/sylk/ui/fonts"
	"github.com/adalundhe/sylk/ui/gitpanel"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/interrupt"
	knowledgepkg "github.com/adalundhe/sylk/ui/knowledge"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/login"
	markdownpkg "github.com/adalundhe/sylk/ui/markdown"
	"github.com/adalundhe/sylk/ui/memoryview"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/pane"
	"github.com/adalundhe/sylk/ui/planview"
	"github.com/adalundhe/sylk/ui/queue"
	"github.com/adalundhe/sylk/ui/search"
	sessionpkg "github.com/adalundhe/sylk/ui/session"
	"github.com/adalundhe/sylk/ui/status"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
)

func New(ctx context.Context, cfg Config, deps Deps) *AppModel {
	// Ensure managed LSP binaries are discoverable before any server
	// selection occurs. Must precede NewManager / selector creation.
	lsp.EnsureManagedBinOnPath()

	appCtx, appCancel := context.WithCancel(ctx)
	th := cfg.Theme()
	app := newAppModel(appCtx, appCancel, cfg, deps, th)
	app.initializePromptQueue(th)
	app.initializeWAL()
	app.configureLoginClipboard()
	app.configureAgentPanel(deps)
	app.seedAgents(deps)
	app.initializePaneState(th)
	app.initializePlanAndInput(th, cfg, deps)
	app.initializeNerdFonts(deps)
	app.initializeAuthStatus(deps)
	app.initializeGitState(th, cfg, deps)
	app.initializePipelineBridge(deps)
	return app
}

func newAppModel(
	appCtx context.Context,
	appCancel context.CancelFunc,
	cfg Config,
	deps Deps,
	th *theme.Theme,
) *AppModel {
	return &AppModel{
		ctx:                appCtx,
		cancel:             appCancel,
		config:             cfg,
		deps:               deps,
		layout:             layout.NewManager(0, 0, defaultPanels, defaultModeCandidates),
		focus:              layout.NewFocusManager(defaultTabOrder),
		chat:               chat.New(th, cfg.ChatHistoryCapacity),
		statusBar:          status.New(th, deps.SessionManager),
		sessionPanel:       sessionpkg.New(deps.SessionManager, th),
		agentPanel:         agentpkg.New(th),
		codePanel:          codepkg.New(th),
		knowledgePanel:     knowledgepkg.New(th),
		memoryView:         memoryview.New(th),
		memoryIndexedIDs:   make(map[string]struct{}),
		fileTree:           filetree.New(th),
		editorOverlay:      editor.New(th),
		editorCache:        editor.NewEditorCache(editor.CacheConfig{}),
		modalOverlay:       modal.New(th),
		searchOverlay:      search.New(th, search.NewProviderRegistry()),
		fieldManualOverlay: fieldmanual.New(th),
		loginPanel: login.New(th,
			func(provider string) string {
				return resolveLoginPanelAPIKey(provider)
			},
			llm.ValidateKeyFormat,
			nil,
		),
		activityBridge:         bridge.NewActivityBridge("tui.activity", deps.GuideBus),
		tokenUsageBridge:       bridge.NewTokenUsageBridge("tui.token_usage", deps.GuideBus),
		accountantBridge:       bridge.NewAccountantBridge("tui.accountant", deps.Accountant),
		sessionBridge:          bridge.NewSessionBridge(deps.SessionManager, deps.Scope),
		streamBridge:           bridge.NewStreamBridge(deps.Scope),
		guideBridge:            bridge.NewGuideBridge(deps.GuideBus, deps.Scope, "default"),
		lspManager:             lsp.NewManager(deps.Scope),
		lspInstalling:          make(map[lsp.ServerID]bool),
		interruptHandler:       interrupt.NewHandlerWithThreshold(time.Duration(cfg.InterruptThresholdMs) * time.Millisecond),
		clipboard:              register.NewOSClipboard(),
		scrollSpring:           harmonica.NewSpring(harmonica.FPS(scrollFPS), scrollFrequency, scrollDamping),
		bounceSpring:           harmonica.NewSpring(harmonica.FPS(scrollFPS), bounceFrequency, bounceDamping),
		hoverMouseLine:         -1,
		highlightLine:          -1,
		tabDragIdx:             -1,
		tabHoverClose:          -1,
		previewTabHoverClose:   -1,
		mdPreviewTabHoverClose: -1,
		mdTooltipTab:           -1,
		viewMode:               ViewChat,
		agentContextTokens:     make(map[string]int),
		agentRuntimeContexts:   make(map[string]runtimeContextState),
		agentReplicaCounts:     make(map[string]int),
		streamUsage:            make(map[string]streamUsageEntry),
		streamedResponses:      make(map[string]streamedResponseState),
		activeStreams:          make(map[string]*activeStreamEntry),
		deferredStreams:        make(map[string]*activeStreamEntry),
		nestedStreams:          make(map[string]*activeStreamEntry),
		reroutedStreamCIDs:     make(map[string]time.Time),
		oauthSessions:          newOAuthSessionManager(),
		focusGradient:          th.Palette.IdleFocusRingGradient(),
		idleFocusGradient:      th.Palette.IdleFocusRingGradient(),
		activeFocusGradient:    th.Palette.FocusRingGradient(),
		focusRingStart:         time.Now(),
		lastFocusBorderBucket:  -1,
		agentContextModels:     make(map[string]string),
	}
}

func (m *AppModel) initializePromptQueue(th *theme.Theme) {
	m.promptQueue = queue.New(queue.MaxCapacity)
	m.queueGradient = th.Palette.QueueGradient()
}

func (m *AppModel) initializeWAL() {
	m.walLogger, m.walCloser = newTUIWALLogger()
}

func (m *AppModel) configureLoginClipboard() {
	m.loginPanel.SetClipboard(m.clipboard.Get)
	m.loginPanel.SetClipboardWrite(m.clipboard.Set)
}

func (m *AppModel) configureAgentPanel(deps Deps) {
	if deps.AgentModelStore != nil {
		m.agentPanel.SetModelStore(deps.AgentModelStore)
	}
	if deps.AuthRegistry != nil {
		m.agentPanel.SetOpenAIAuthMethod(deps.AuthRegistry.ActiveMethod("openai"))
	}
}

func (m *AppModel) seedAgents(deps Deps) {
	for _, seed := range deps.SeedAgents {
		m.agentPanel.SeedAgent(seed.ID, seed.AgentType, seed.Name, seed.SupportedModels, seed.PersistedModelID, seed.PersistedProviderID)
	}
}

func (m *AppModel) initializePaneState(th *theme.Theme) {
	m.comp = compositor.New()
	m.previewPanel = preview.New(th)
	m.mdPreviewPanel = markdownpkg.New(th)
	m.paneCounter = 1
	m.focusedPane = 1
	m.paneTree = pane.NewLeaf(1)
	m.paneEditors = map[pane.PaneID]*editorPaneState{
		1: {editor: editor.New(th)},
	}
}

func (m *AppModel) initializePlanAndInput(th *theme.Theme, cfg Config, deps Deps) {
	m.planView = planview.New(&th.Palette)
	m.lspBridge = bridge.NewLSPBridge(m.lspManager, deps.Scope)
	m.input = inputpkg.New(th, cfg.InputHistoryCapacity,
		&tabCompleter{tabOrderFn: m.focusedTabOrder},
		&slashCommandCompleter{},
	)
	m.input.SetSlashValidator(isKnownSlashCommand)
	m.syncFocusState()
}

func (m *AppModel) initializeNerdFonts(deps Deps) {
	if deps.NerdFontsDetected {
		m.nerdFontsDetected = true
	} else if deps.GitClient == nil {
		m.nerdFontsDetected = fonts.Detected()
	}
	m.fileTree.SetNerdFonts(m.nerdFontsDetected)
	m.statusBar.SetNerdFonts(m.nerdFontsDetected)
	m.agentPanel.SetNerdFonts(m.nerdFontsDetected)
}

func (m *AppModel) initializeAuthStatus(deps Deps) {
	if deps.AuthRegistry == nil {
		return
	}
	for _, provider := range []string{"google", "anthropic", "openai"} {
		m.statusBar.SetAuthStatus(provider, deps.AuthRegistry.IsAvailable(provider))
	}
}

func (m *AppModel) initializeGitState(th *theme.Theme, cfg Config, deps Deps) {
	if m.initializeSeededGitState(th, cfg, deps) {
		return
	}
	m.initializeDetectedGitState(th, cfg, deps)
}

func (m *AppModel) initializeSeededGitState(th *theme.Theme, cfg Config, deps Deps) bool {
	if deps.GitClient == nil || !deps.GitClient.IsGitRepo() {
		return false
	}
	m.gitClient = deps.GitClient
	m.gitBus = deps.GitBus
	m.gitBridge = bridge.NewGitBridge(m.gitBus, deps.Scope)
	m.initializeGitPanels(th, cfg)
	m.gitWatcher = deps.GitWatcher
	m.safetyGuard = deps.SafetyGuard
	return true
}

func (m *AppModel) initializeDetectedGitState(th *theme.Theme, cfg Config, deps Deps) {
	gc, err := git.NewGitClient(cfg.ProjectRoot)
	if err != nil || !gc.IsGitRepo() {
		return
	}
	m.gitClient = gc
	m.gitBus = git.NewGitBus(gc)
	m.gitBridge = bridge.NewGitBridge(m.gitBus, deps.Scope)
	m.initializeGitPanels(th, cfg)
	if sw, err := git.NewStatusWatcher(gc); err == nil {
		m.gitWatcher = sw
	}
	if sg, err := git.NewSafetyGuard(gc, m.gitBus, git.DefaultSafetyConfig(), m.gitWatcher); err == nil {
		m.safetyGuard = sg
	}
}

func (m *AppModel) initializeGitPanels(th *theme.Theme, cfg Config) {
	configPath := filepath.Join(cfg.ProjectRoot, ".sylk", "config.yaml")
	m.gitPanel = gitpanel.New(th, m.gitBus, configPath)
	m.commitTree = committree.New(th)
}

func (m *AppModel) initializePipelineBridge(deps Deps) {
	if deps.GuideBus == nil && deps.VariantRegistry == nil {
		return
	}
	m.pipelineBridge = bridge.NewPipelineBridge("tui.pipeline", deps.GuideBus, deps.VariantRegistry, deps.Scope)
}

var (
	uiDebugLogger     *slog.Logger
	uiDebugLoggerOnce sync.Once
)

func uiDebugFileLog() *slog.Logger {
	uiDebugLoggerOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			uiDebugLogger = slog.Default()
			return
		}
		uiDebugLogger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	return uiDebugLogger
}

func newTUIWALLogger() (*slog.Logger, io.Closer) {
	logger, closer, err := agentlog.NewWALLogger("tui")
	if err != nil {
		return nil, nil
	}
	return logger, closer
}

// Init starts all event bridges, the tick/blink timers, and background LSP provisioning.
func (m *AppModel) Init() tea.Cmd {
	m.viewDirty = true
	m.blinkEpoch = time.Now()
	cmds := []tea.Cmd{
		m.startBridges(),
		m.ensureTick(false),
		m.provisionLSPServers(),
	}
	if bc := m.ensureBlinkAfterDispatch(); bc != nil {
		cmds = append(cmds, bc)
	}
	if !m.nerdFontsDetected {
		cmds = append(cmds, m.installNerdFontsCmd())
	}
	if m.gitWatcher != nil {
		m.gitWatcher.Start(m.ctx)
		cmds = append(cmds, m.gitWatchCmd())
	}
	// Run crash recovery + GC for the safety guard in the background.
	if m.safetyGuard != nil {
		cmds = append(cmds, m.safetyRecoveryCmd())
	}
	if cmd := m.deferredAuthPollCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// installNerdFontsCmd returns a Cmd that downloads and installs Nerd Font
// symbols in the background, cancellable via the app context.
func (m *AppModel) installNerdFontsCmd() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		err := fonts.InstallCtx(ctx)
		return msg.NerdFontsResultMsg{Available: err == nil}
	}
}

// gitWatchCmd returns a Cmd that blocks on the git status watcher channel,
// returning the next status update as a GitStatusMsg. Returns nil when the
// watcher is not active.
func (m *AppModel) gitWatchCmd() tea.Cmd {
	if m.gitWatcher == nil {
		return nil
	}
	ch := m.gitWatcher.Events()
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return nil
		}
		return msg.GitStatusMsg{
			StatusMap:   update.StatusMap,
			TrackedSet:  update.TrackedSet,
			TrackedDirs: update.TrackedDirs,
		}
	}
}

// nudgeGitWatcher signals the watcher to refresh soon. Safe to call when
// the watcher is nil (non-git projects).
func (m *AppModel) nudgeGitWatcher() {
	if m.gitWatcher != nil {
		m.gitWatcher.Nudge()
	}
}

// safetyRecoveryDoneMsg signals that background safety recovery + GC completed.
type safetyRecoveryDoneMsg struct{}

// safetyRecoveryCmd returns a Cmd that runs crash recovery and GC in the
// background, keeping the startup path non-blocking.
func (m *AppModel) safetyRecoveryCmd() tea.Cmd {
	sg := m.safetyGuard
	return func() tea.Msg {
		// Phase 1: Recover incomplete operations.
		ops, err := sg.RecoverOnStartup()
		if err == nil {
			for _, op := range ops {
				_ = sg.ResolveIncomplete(op, git.RecoveryResume)
			}
		}

		// Phase 2: GC old snapshots and journal segments.
		_, _ = sg.GCSnapshots()
		_ = sg.GCJournal()

		return safetyRecoveryDoneMsg{}
	}
}

// deferredAuthPollCmd returns a Cmd that re-checks auth availability after
// the Phase 4 background ProbeAll has had time to complete. ProbeAll runs
// in a scope.Go goroutine and typically finishes in <1ms; the 200ms delay
// provides a generous margin. This bridges the gap between the synchronous
// UI init (which reads an empty registry) and the async probe.
func (m *AppModel) deferredAuthPollCmd() tea.Cmd {
	reg := m.deps.AuthRegistry
	if reg == nil {
		return nil
	}
	providers := [3]string{"google", "anthropic", "openai"}
	return func() tea.Msg {
		time.Sleep(200 * time.Millisecond)
		statuses := make(map[string]bool, len(providers))
		methods := make(map[string]string, len(providers))
		for _, p := range providers {
			statuses[p] = reg.IsAvailable(p)
			methods[p] = reg.ActiveMethod(p)
		}
		return msg.AuthStatusMsg{Providers: statuses, Methods: methods}
	}
}

// syncStagedFiles pushes the git panel's staged-files state to the commit tree
// so the [Commit] badge reflects current staging.
func (m *AppModel) syncStagedFiles() {
	if m.commitTree != nil && m.gitPanel != nil {
		m.commitTree.SetHasStagedFiles(m.gitPanel.HasAnyStagedFiles())
	}
}
