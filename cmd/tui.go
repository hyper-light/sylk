package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on http.DefaultServeMux when SYLK_PPROF is set
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/archivalist"
	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guardian"
	"github.com/adalundhe/sylk/agents/guide"
	inspectorGlobal "github.com/adalundhe/sylk/agents/inspector/global"
	inspectorPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspectorShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/orchestrator"
	"github.com/adalundhe/sylk/agents/scribe"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	globaltester "github.com/adalundhe/sylk/agents/tester/global"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	identityregistries "github.com/adalundhe/sylk/core/agents/identity/registries"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/activation"
	"github.com/adalundhe/sylk/core/container/daemon"
	"github.com/adalundhe/sylk/core/container/network"
	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/diagnostics"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	forestsvc "github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/llm/accounting"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/ui"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/fonts"
	"github.com/blevesearch/bleve/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
)

var (
	tuiTheme string
	tuiMock  bool
)

func init() {
	rootCmd.Flags().StringVar(&tuiTheme, "theme", "dark", "Color theme (dark or light)")
	rootCmd.Flags().BoolVar(&tuiMock, "mock", false, "Run with mock backend (no real agents)")
}

func runTUI(_ *cobra.Command, _ []string) error {
	startupTrace("run_tui_enter", "debug_log", diagnostics.StartupTracePath(), "activation_debug_log", diagnostics.ActivationTracePath())
	restoreStdLog := installTUIStdLogSink()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	// Kill watchdog: independent SIGINT/SIGTERM watcher that guarantees
	// the user can always force-quit by pressing Ctrl+C a second time,
	// even if cooperative shutdown is wedged in a blocking subsystem
	// close. Stops cleanly on successful exit (defer below); fires
	// os.Exit(130) on a second signal otherwise.
	stopKillWatchdog := installKillWatchdog()
	defer stopKillWatchdog()

	// Optional pprof endpoint for diagnosing memory/CPU at idle. Bind
	// to localhost only so this never accidentally exposes profiling
	// to the network. Profile heap with:
	//   go tool pprof -http=: http://127.0.0.1:6060/debug/pprof/heap
	if addr := strings.TrimSpace(os.Getenv("SYLK_PPROF")); addr != "" {
		if addr == "1" || addr == "true" {
			addr = "127.0.0.1:6060"
		}
		go func() {
			slog.Info("pprof: listening", "addr", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				slog.Warn("pprof: listen failed", "addr", addr, "error", err)
			}
		}()
	}

	// Resolve project root early so the parallel bootstrap can initialize
	// git resources concurrently with agent container creation.
	projectRoot := resolveProjectRoot()
	startupTrace("project_root_resolved", "project_root", projectRoot, "mock_mode", tuiMock)

	deps, cleanup, err := runBootstrapWithBootUI(ctx, tuiMock, projectRoot, stop)
	if err != nil {
		startupTrace("bootstrap_failed", "error", err.Error())
		stop()
		restoreErr := restoreStdLog()
		return errors.Join(fmt.Errorf("bootstrap: %w", err), restoreErr)
	}
	startupTrace("bootstrap_succeeded")
	// Pass stop to ui.Run so it can restore default signal handling
	// between p.Run() returning and app.Shutdown() — allowing a second
	// Ctrl+C to force-kill the process during slow shutdown.
	deps.SignalStop = stop

	cfg := ui.DefaultConfig()
	cfg.ThemeMode = parseThemeMode(tuiTheme)
	cfg.MockMode = tuiMock
	cfg.ProjectRoot = projectRoot

	startupTrace("ui_run_start")
	runErr := ui.Run(ctx, cfg, deps)
	if runErr != nil {
		startupTrace("ui_run_done", "error", runErr.Error())
	} else {
		startupTrace("ui_run_done")
	}
	stop()
	startupTrace("cleanup_start")
	cleanupErr := cleanup()
	if cleanupErr != nil {
		startupTrace("cleanup_done", "error", cleanupErr.Error())
	} else {
		startupTrace("cleanup_done")
	}
	restoreErr := restoreStdLog()
	return errors.Join(runErr, cleanupErr, restoreErr)
}

// resolveProjectRoot finds the project root: git root or CWD.
func resolveProjectRoot() string {
	cwd, _ := os.Getwd()
	if root, err := boot.FindGitRoot(cwd); err == nil {
		return root
	}
	return cwd
}

// shutdownGrace and shutdownHard bound the GoroutineScope drain at the
// end of cleanup. Sized for sub-second total shutdown: once parent ctx
// is cancelled, well-behaved workers exit in tens of milliseconds;
// 100ms grace is the cooperative window, 200ms hard is the force-
// cancel ceiling. Workers that don't drain within shutdownHard are
// abandoned to runtime exit reclamation.
// shutdownGrace and shutdownHard size the scope drain at the end of
// cleanup. SignalShutdown is called at the very start of cleanup, so
// every tracked worker has the entire parallel-close phase to wind
// down by the time we get here. Sized tight: stragglers blocked on
// non-cancellable external IO will exceed this window and get
// reported as leaks (now logged, not fatal — see cleanup tail), so
// no point waiting longer than the OS scheduler needs to deliver
// the cancellation signal to the workers that *are* responsive.
const (
	shutdownGrace = 25 * time.Millisecond
	shutdownHard  = 75 * time.Millisecond
)

const claimsStartupRecoveryActorID = "sys:claims_recovery"

func startupTrace(event string, fields ...any) {
	diagnostics.LogStartup(event, fields...)
}

// shutdownTimeout is the overall ceiling on the cleanup function.
// Derived from: 200ms supervisor + max(per-subsystem budget) for the
// parallel-close phase (containers at 1500ms is the slowest; every
// other subsystem fits the default 200ms once SignalShutdown has
// propagated cancellation) + 50ms drain + shutdownHard scope drain
// + headroom. Wall time is dominated by max(budget), not sum,
// because every subsystem closes in its own goroutine.
const shutdownTimeout = 2 * time.Second

// closeBudget bounds a single subsystem Close()/Stop() call. Sized
// for sub-second total cleanup: a well-behaved Close completes in
// tens-of-ms; 200ms covers DB flushes and channel drains without
// inflating cumulative wait. Subsystems that miss the budget have
// their fn-runner goroutine left running (tracked via wg, observable
// via shutdown logs) — the cooperative path moves on, the kill
// watchdog stays armed for the user's force-exit.
const closeBudget = 200 * time.Millisecond

// Per-subsystem budget for stopping containers. Sylk containers are
// in-process — Container.Stop is essentially a wait for the
// container's *own* scope to drain its tracked workers. The early
// SignalShutdown propagation (cleanup signals every container's
// scope at t=0) means those workers have already had the entire
// parallel-close phase to wind down by the time Container.Stop
// actually runs, so 200ms is enough for the agent's lifecycle
// transitions plus any post-stop hooks.
const closeBudgetContainers = 200 * time.Millisecond

// closeDrainGrace bounds the post-cleanup wait for tracked-but-still-
// running close goroutines to finish. Sized small: a Close that
// hasn't returned by closeBudget+drainGrace is genuinely stuck, and
// the kill watchdog (200ms after a second Ctrl+C) is the right
// escape. 50ms is the natural tail for fast closes that finish a
// hair after their budget elapses.
const closeDrainGrace = 50 * time.Millisecond

// closeWithBudget runs fn under the smaller of the per-call closeBudget
// and the parent ctx's remaining deadline, passing the bounded
// sub-context to fn so ctx-aware closes can honor it directly. The
// inner goroutine is tracked via wg so an operator inspecting
// shutdown state can see how many close goroutines are still in
// flight. On timeout, the inner fn goroutine is left running (it has
// the wg.Done deferred so the tracker observes when it completes);
// the kill watchdog provides the user-triggered escape and runtime
// exit reclaims everything.
//
// fn signature is ctx-aware: closes that already accept context
// (e.g. MemoryForest.CloseWithContext) plug in directly. For non-ctx
// Close()/Stop() methods, use closeAware/closeAwareVoid below to
// adapt — they ignore ctx but the surrounding closeWithBudget still
// bounds the wait via its select.
func closeWithBudget(ctx context.Context, wg *sync.WaitGroup, name string, fn func(context.Context) error) error {
	return closeWithBudgetN(ctx, wg, name, closeBudget, fn)
}

// closeWithBudgetN is the per-budget variant of closeWithBudget.
// Slow subsystems (containers, knowledge layers, activation) pass
// their own budget so a single 200ms one-size-fits-all does not
// truncate legitimate close work into a timeout error.
func closeWithBudgetN(ctx context.Context, wg *sync.WaitGroup, name string, budget time.Duration, fn func(context.Context) error) error {
	sub, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	wg.Add(1)
	done := make(chan error, 1)
	go func() {
		defer wg.Done()
		done <- fn(sub)
	}()
	select {
	case err := <-done:
		return err
	case <-sub.Done():
		// Two-stage handoff: when the budget elapses, give the inner
		// fn a brief grace window to deliver an already-prepared error
		// before declaring a timeout. ctx-aware closes (e.g. ChannelBus
		// .CloseWithContext) detect ctx cancellation and return their
		// own structured error within microseconds; without this
		// grace, Go's randomized select between done and sub.Done()
		// often returns the generic "timed out" message even though
		// the inner error was already on its way.
		select {
		case err := <-done:
			return err
		case <-time.After(closeBudgetHandoff):
		}
		// Timeout-after-grace is a soft failure: the goroutine is
		// tracked via the shutdown wg, the kill watchdog stays armed
		// for force-exit, and runtime exit reclaims everything. We
		// log it but do NOT return it as an error so a slow
		// (but non-faulty) close doesn't fail the whole cleanup. Real
		// fn errors still propagate via the case err := <-done branch.
		slog.Warn("shutdown: close exceeded budget; goroutine remains tracked via shutdown wg",
			"subsystem", name,
			"budget", budget,
			"reason", sub.Err().Error(),
		)
		return nil
	}
}

// closeBudgetHandoff is the grace window between budget expiry and
// declaring a timeout. Sized to comfortably absorb scheduler latency
// for an inner ctx-aware close that has already started returning its
// error: a Go channel send + select dispatch is sub-microsecond, so
// 5ms is generous and still imperceptible to the user.
const closeBudgetHandoff = 5 * time.Millisecond

// closeAware adapts a non-ctx Close() to the ctx-aware shape
// closeWithBudget expects. The returned function ignores ctx —
// non-ctx closes don't observe cancellation by design — but the
// surrounding closeWithBudget still bounds the wait via its select
// and the wg-tracked goroutine remains observable.
func closeAware(fn func() error) func(context.Context) error {
	return func(_ context.Context) error {
		return fn()
	}
}

// closeAwareVoid adapts a void Stop() to the ctx-aware shape.
func closeAwareVoid(fn func()) func(context.Context) error {
	return func(_ context.Context) error {
		fn()
		return nil
	}
}

// drainCloseGoroutines waits for all goroutines spawned by
// closeWithBudget to finish, bounded by closeDrainGrace. After the
// grace expires, any remaining goroutines are logged; they are
// reclaimed when the process exits (immediate via the kill watchdog
// or naturally on main return).
//
// The single observer goroutine spawned here is a one-shot: it
// exits as soon as wg.Wait returns. It is not in the close wg
// itself, but its lifecycle is bounded by the same wg's completion
// — when all close-goroutines exit, the observer exits. When close-
// goroutines never exit, the observer waits until process termination,
// which is reclaimed as part of normal Go runtime teardown.
func drainCloseGoroutines(wg *sync.WaitGroup) {
	drainCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(drainCh)
	}()
	select {
	case <-drainCh:
	case <-time.After(closeDrainGrace):
		slog.Warn("shutdown: close goroutines still in flight after grace; relying on watchdog or process exit",
			"grace", closeDrainGrace,
		)
	}
}

// shutdownContainers stops + removes every container in parallel,
// each container under its own bounded ctx. Per-container parallel
// dispatch ensures the cumulative wait is max(per-container
// stop+remove) instead of sum. Errors are joined; nothing fails
// the cleanup as a whole.
func shutdownContainers(ctx context.Context, rt container.ContainerRuntime, all []*container.Container) error {
	if rt == nil || len(all) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error
	for _, c := range all {
		wg.Add(1)
		c := c
		go func() {
			defer wg.Done()
			if c.IsRunning() {
				if err := rt.StopContainer(ctx, c); err != nil {
					errMu.Lock()
					errs = append(errs, err)
					errMu.Unlock()
				}
			}
			if err := rt.RemoveContainer(ctx, c); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// =============================================================================
// Typed result structs for Phase 2 parallel bootstrap goroutines.
// =============================================================================

type daemonContainerResult struct {
	c   *container.Container
	err error
}

type activationCtrlResult struct {
	ctrl *activation.ActivationController
	err  error
}

type fontResult struct {
	detected bool
}

type gitBootResult struct {
	client  *git.GitClient
	watcher *git.StatusWatcher
	bus     *git.GitBus
	guard   *git.SafetyGuard
}

type hydrateResult struct{ hydrated *providers.HydratedGoogleAuth }

type modelSwapperFunc func(ctx context.Context, agentType, modelID string) error

type modelSwapActivator interface {
	EnsureActive(ctx context.Context, agentType string) (*container.Container, error)
}

var errNoSwappableContainer = errors.New("no model-swappable agent found")

type bootstrapPhase1 struct {
	ctx         context.Context
	projectRoot string
	start       time.Time
	progress    *bootstrapProgressReporter

	bootIdentity    boot.ProcessIdentity
	phase0Board     *claims.ClaimsBoard
	scope           *concurrency.GoroutineScope
	guideBus        guide.EventBus
	activityPub     events.ActivityPublisher
	streamMgr       *guide.StreamManager
	sessionMgr      *session.Manager
	defaultSession  *session.Session
	bootOps         *boot.OperationsSequencer
	bootDeltaUnsub  func()
	bootDispatchers *claims.ServiceDispatcherRegistry
	descriptors     *handoff.DescriptorRegistry
	budget          *concurrency.GoroutineBudget
	containerReg    *container.ContainerRegistry
	creatorReg      *container.AgentCreatorRegistry
	serviceReg      *network.ServiceRegistry
	specReg         *container.AgentSpecRegistry
	quota           *container.ResourceQuota
	hookMut         *lifecycleHookMutator
	probeFact       *probeFactoryHolder
	runtime         *container.DefaultRuntime
	namespace       *network.NetworkNamespace
	daemonCtrl      *daemon.DaemonSetController
	daemonSpecMap   map[string]container.ContainerSpec

	authRegistry *credentials.AuthRegistry
	guideCfg     providers.GoogleConfig
	hydrateOnce  *hydrateOnceCell
	hydratedRef  atomic.Pointer[providers.HydratedGoogleAuth]

	googleGateway    *gateway.ProviderGateway
	anthropicGateway *gateway.ProviderGateway
	openaiGateway    *gateway.ProviderGateway

	// Typed identity + accounting surface. The identity factory is
	// constructed in phase1 against this run's default session; the
	// accountant is added in phase4, when the gateway hook swaps to a
	// MultiHook that fans out to accounting as well as activity.
	identityFactory atomic.Pointer[identity.Factory]
	accountant      atomic.Pointer[accounting.Accountant]
	llmEventHookRef atomic.Pointer[providers.MultiHook]

	identityReg *container.AgentIdentityRegistry
	actCtrlRef  atomic.Pointer[activation.ActivationController]
	gitSubsRef  atomic.Pointer[gitBootResult]

	planStore        *architect.PlanStore
	knowledgeStore   *knowledge.KnowledgeStore
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend
	forest           *forestsvc.MemoryForest
	forestContent    *ctxpkg.UniversalContentStore
	forestVectorDB   *vectorgraphdb.VectorGraphDB

	guideRef      atomic.Pointer[guide.Guide]
	guardianRef   atomic.Pointer[guardian.Guardian]
	orchRef       atomic.Pointer[orchestrator.Orchestrator]
	quarantineRef atomic.Pointer[fetch.QuarantineBuffer]
	handoffRef    atomic.Pointer[handoff.HandoffSupervisor]
}

type bootstrapPhase2 struct {
	guideResult    daemonContainerResult
	orchResult     daemonContainerResult
	guardianResult daemonContainerResult
	actResult      activationCtrlResult
	fontRes        fontResult
	gitRes         gitBootResult
}

type bootstrapPhase3 struct {
	guide          *guide.Guide
	orch           *orchestrator.Orchestrator
	activationCtrl *activation.ActivationController
	activator      guide.PodActivator
	scribeFactory  agentShared.ScribeFactory
}

type bootstrapPhase4 struct {
	seeds                  []ui.AgentSeed
	modelStore             *agentpkg.AgentModelStore
	modelSwapper           modelSwapperFunc
	knowledgeSync          *librarian.KnowledgeSyncService
	phase4Done             chan struct{}
	knowledgeBootStart     chan struct{}
	knowledgeBootStartOnce sync.Once
	supervisorRef          atomic.Pointer[handoff.HandoffSupervisor]
	bootLogger             *agentlog.BootEventLogger
}

func (p *bootstrapPhase4) StartKnowledgeBoot() {
	if p == nil || p.knowledgeBootStart == nil {
		startupTrace("knowledge_boot_release_noop")
		return
	}
	p.knowledgeBootStartOnce.Do(func() {
		startupTrace("knowledge_boot_release_close_start")
		close(p.knowledgeBootStart)
		startupTrace("knowledge_boot_release_close_done")
	})
}

func (p *bootstrapPhase4) waitForKnowledgeBootStart(ctx context.Context) bool {
	if p == nil || p.knowledgeBootStart == nil {
		startupTrace("knowledge_boot_wait_no_gate")
		return true
	}
	startupTrace("knowledge_boot_wait_start")
	select {
	case <-p.knowledgeBootStart:
		startupTrace("knowledge_boot_wait_released")
		return true
	case <-ctx.Done():
		startupTrace("knowledge_boot_wait_cancelled", "error", ctx.Err().Error())
		return false
	}
}

// busReadinessPublisher adapts knowledge.ReadinessPublisher to the guide EventBus.
type busReadinessPublisher struct {
	bus       guide.EventBus
	sessionID func() string
}

type containerIdentityAgentRefResolver struct {
	registry     *container.AgentIdentityRegistry
	bootIdentity boot.ProcessIdentity
}

func (r containerIdentityAgentRefResolver) ResolveAgentRef(_ context.Context, sessionID, agentID string) (claims.AgentRef, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return claims.AgentRef{}, false
	}
	if ref, ok := r.resolveBootProcessRef(sessionID, agentID); ok {
		return ref, true
	}
	if ref, ok := r.resolveRegisteredAgentRef(sessionID, agentID); ok {
		return ref, true
	}
	return r.resolveInfrastructureParticipantRef(sessionID, agentID)
}

func (r containerIdentityAgentRefResolver) resolveRegisteredAgentRef(sessionID, agentID string) (claims.AgentRef, bool) {
	if r.registry == nil {
		return claims.AgentRef{}, false
	}
	agentType, ok := r.registry.TypeOf(agentID)
	if !ok {
		if canonicalID, found := r.registry.Get(agentID); found {
			agentType = agentID
			agentID = canonicalID
			ok = true
		}
	}
	if !ok {
		return claims.AgentRef{}, false
	}
	labels := map[string]string{}
	if strings.TrimSpace(sessionID) != "" {
		labels["session_id"] = strings.TrimSpace(sessionID)
	}
	return claims.AgentRef{
		UID:        agentID,
		Type:       agentType,
		Name:       agentType,
		Category:   string(claims.ParticipantCategoryAgent),
		Generation: claims.InitialParticipantGeneration,
		Labels:     labels,
	}.Normalized(), true
}

func (r containerIdentityAgentRefResolver) resolveBootProcessRef(sessionID, agentID string) (claims.AgentRef, bool) {
	processUID := strings.TrimSpace(r.bootIdentity.ProcessUID)
	if processUID == "" {
		return claims.AgentRef{}, false
	}
	switch agentID {
	case r.bootIdentity.BootSequencerUID, boot.BootSequencerAgentID:
		return r.bootProcessAgentRef(sessionID, r.bootIdentity.BootSequencerUID, boot.BootSequencerAgentID), true
	case r.bootIdentity.IdentityRegistryUID, boot.IdentityRegistryAgentID:
		return r.bootProcessAgentRef(sessionID, r.bootIdentity.IdentityRegistryUID, boot.IdentityRegistryAgentID), true
	default:
		return claims.AgentRef{}, false
	}
}

func (r containerIdentityAgentRefResolver) bootProcessAgentRef(sessionID, uid, agentType string) claims.AgentRef {
	labels := map[string]string{"process_uid": strings.TrimSpace(r.bootIdentity.ProcessUID)}
	if strings.TrimSpace(sessionID) != "" {
		labels["session_id"] = strings.TrimSpace(sessionID)
	}
	return claims.AgentRef{
		UID:        strings.TrimSpace(uid),
		Type:       strings.TrimSpace(agentType),
		Name:       strings.TrimSpace(agentType),
		Category:   string(claims.ParticipantCategorySystem),
		Generation: claims.InitialParticipantGeneration,
		Labels:     labels,
	}.Normalized()
}

func (r containerIdentityAgentRefResolver) resolveInfrastructureParticipantRef(sessionID, participantID string) (claims.AgentRef, bool) {
	contract, ok := tuiInfrastructureParticipantContract(participantID)
	if !ok {
		return r.resolveBootReadinessParticipantRef(sessionID, participantID)
	}
	scope, ok := tuiInfrastructureParticipantScope(contract.ScopeKeys, sessionID, r.bootIdentity.ProcessUID)
	if !ok {
		return claims.AgentRef{}, false
	}
	uid, err := claims.DeriveParticipantUID(contract.Category, contract.ParticipantID, scope)
	if err != nil {
		return claims.AgentRef{}, false
	}
	return claims.AgentRef{
		UID:        uid,
		Type:       contract.ParticipantID,
		Name:       contract.ParticipantType,
		Category:   string(contract.Category),
		Generation: claims.InitialParticipantGeneration,
		Labels:     scope,
	}.Normalized(), true
}

func (r containerIdentityAgentRefResolver) resolveBootReadinessParticipantRef(sessionID, participantID string) (claims.AgentRef, bool) {
	participantType, ok := tuiBootReadinessParticipantType(participantID)
	if !ok {
		return claims.AgentRef{}, false
	}
	scope, ok := tuiInfrastructureParticipantScope([]string{"session_id"}, sessionID, r.bootIdentity.ProcessUID)
	if !ok {
		return claims.AgentRef{}, false
	}
	uid, err := claims.DeriveParticipantUID(claims.ParticipantCategoryService, participantID, scope)
	if err != nil {
		return claims.AgentRef{}, false
	}
	return claims.AgentRef{
		UID:        uid,
		Type:       strings.TrimSpace(participantID),
		Name:       participantType,
		Category:   string(claims.ParticipantCategoryService),
		Generation: claims.InitialParticipantGeneration,
		Labels:     scope,
	}.Normalized(), true
}

func tuiInfrastructureParticipantContract(participantID string) (claims.InfrastructureServiceContract, bool) {
	participantID = strings.TrimSpace(participantID)
	for _, contract := range claims.InfrastructureServiceContracts() {
		if contract.ParticipantID == participantID && contract.ParticipantID != boot.BootSequencerAgentID && contract.ParticipantID != boot.IdentityRegistryAgentID {
			return contract, true
		}
	}
	return claims.InfrastructureServiceContract{}, false
}

func tuiInfrastructureParticipantScope(scopeKeys []string, sessionID, processUID string) (map[string]string, bool) {
	values := map[string]string{
		"process_uid": strings.TrimSpace(processUID),
		"session_id":  strings.TrimSpace(sessionID),
	}
	scope := make(map[string]string, len(scopeKeys))
	for _, key := range scopeKeys {
		key = strings.TrimSpace(key)
		if key == "" || values[key] == "" {
			return nil, false
		}
		scope[key] = values[key]
	}
	return scope, len(scope) != 0
}

func tuiBootReadinessParticipantType(participantID string) (string, bool) {
	for _, participant := range tuiBootReadinessParticipants() {
		if participant.ParticipantID == strings.TrimSpace(participantID) {
			return participant.ParticipantType, true
		}
	}
	return "", false
}

func tuiBootReadinessParticipants() []boot.SystemParticipantActivation {
	participants := append([]boot.SystemParticipantActivation{}, boot.RequiredKnowledgeBackends()...)
	participants = append(participants, boot.RequiredInfrastructureServices()...)
	return append(participants, boot.RequiredUserFacingSurfaces()...)
}

func (p *busReadinessPublisher) PublishKnowledgeReady(event knowledge.ReadinessEvent) {
	msg := guide.NewKnowledgeReadyMessage(&guide.KnowledgeReadyPayload{
		Level:     int(event.Level),
		Searchers: event.Searchers,
	})
	if err := p.bus.Publish(guide.TopicKnowledgeReady, msg); err != nil {
		slog.Warn("tui_knowledge_ready_publish_failed",
			"level", int(event.Level),
			"searchers", event.Searchers,
			"error", err.Error(),
		)
	}
	p.publishKnowledgeBackendReadyTestament(event)
}

const knowledgeReadinessTestamentPublishTimeout = 5 * time.Second

func (p *busReadinessPublisher) publishKnowledgeBackendReadyTestament(event knowledge.ReadinessEvent) {
	if p == nil || !knowledgeReadinessEventHasTextSearch(event) || p.sessionID == nil {
		return
	}
	sessionID := strings.TrimSpace(p.sessionID())
	if sessionID == "" {
		return
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(sessionID)
	if board == nil {
		slog.Warn("tui_knowledge_ready_board_missing", "session_id", sessionID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeReadinessTestamentPublishTimeout)
	defer cancel()
	if _, err := claims.SubmitKnowledgeBackendReadyTestament(ctx, board, map[string]any{
		"level":     knowledgeReadinessLevelLabel(event.Level),
		"searchers": append([]string(nil), event.Searchers...),
	}); err != nil {
		slog.Warn("tui_knowledge_ready_testament_failed",
			"session_id", sessionID,
			"level", int(event.Level),
			"searchers", event.Searchers,
			"error", err.Error(),
		)
	}
}

func knowledgeReadinessEventHasTextSearch(event knowledge.ReadinessEvent) bool {
	for _, searcher := range event.Searchers {
		if searcher == "text" {
			return true
		}
	}
	return false
}

func knowledgeReadinessLevelLabel(level knowledge.ReadinessLevel) string {
	switch level {
	case knowledge.ReadinessPartial:
		return "partial"
	case knowledge.ReadinessFull:
		return "full"
	default:
		return "none"
	}
}

// globalBleveAdapter implements query.BleveIndex by bridging to the
// GlobalVersionBleveStore's raw bleve.Index.
type globalBleveAdapter struct {
	store *sylkdir.GlobalVersionBleveStore
}

// SearchInContext implements query.BleveIndex.
func (a *globalBleveAdapter) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	idx := a.store.RawIndex()
	if idx == nil {
		return &bleve.SearchResult{}, nil
	}
	return idx.SearchInContext(ctx, req)
}

// bleveStoreCloser wraps GlobalVersionBleveStore.CloseAll as io.Closer.
type bleveStoreCloser struct {
	store *sylkdir.GlobalVersionBleveStore
}

func (c *bleveStoreCloser) Close() error { return c.store.CloseAll() }

// buildBleveSearcher opens Bleve at the HEAD version and returns a ready-to-use
// BleveSearcher plus a Closer for cleanup. Called inside the boot goroutine.
func buildBleveSearcher(projectRoot string) (*query.BleveSearcher, io.Closer, error) {
	sd := sylkdir.New(projectRoot)
	meta := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		return nil, nil, fmt.Errorf("load global meta: %w", err)
	}
	head := meta.GetHead()

	bleveStore := sylkdir.NewGlobalVersionBleveStore(sd, head)
	if err := bleveStore.OpenHead(); err != nil {
		return nil, nil, fmt.Errorf("open head bleve: %w", err)
	}

	adapter := &globalBleveAdapter{store: bleveStore}
	searcher := query.NewBleveSearcher(adapter)
	closer := &bleveStoreCloser{store: bleveStore}

	return searcher, closer, nil
}

func buildMemoryForest(projectRoot string, budget *concurrency.GoroutineBudget) (*forestsvc.MemoryForest, *ctxpkg.UniversalContentStore, *vectorgraphdb.VectorGraphDB, error) {
	return buildMemoryForestForSession(projectRoot, "default", budget)
}

func buildMemoryForestForSession(projectRoot, sessionID string, budget *concurrency.GoroutineBudget) (*forestsvc.MemoryForest, *ctxpkg.UniversalContentStore, *vectorgraphdb.VectorGraphDB, error) {
	sd := sylkdir.New(projectRoot)
	if err := sd.Init(); err != nil {
		return nil, nil, nil, fmt.Errorf("init sylk dir for memory forest: %w", err)
	}
	sessionID = bootstrapSessionID(sessionID)

	basePath := filepath.Join(sd.SessionPath(sessionID), "state", "memory_forest")
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("create memory forest state dir: %w", err)
	}

	// The forest projection and content store both depend on the vectorgraph
	// nodes table, so they share a single session-scoped graph DB.
	contentVectorDB, err := vectorgraphdb.Open(filepath.Join(basePath, "content.sqlite"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open memory forest content vector db: %w", err)
	}

	forestLogger := slog.Default().With("component", "memory_forest")
	forest, err := forestsvc.New(forestsvc.Config{
		DB:        contentVectorDB.DB(),
		Logger:    forestLogger,
		MaxTraces: 2048,
	})
	if err != nil {
		_ = contentVectorDB.Close()
		return nil, nil, nil, fmt.Errorf("create memory forest: %w", err)
	}

	contentStore, err := ctxpkg.NewUniversalContentStore(ctxpkg.ContentStoreConfig{
		BlevePath:       filepath.Join(basePath, "documents.bleve"),
		VectorDB:        contentVectorDB,
		GoroutineBudget: budget,
		Logger:          forestLogger,
		Observer:        forest,
	})
	if err != nil {
		_ = forest.Close()
		_ = contentVectorDB.Close()
		return nil, nil, nil, fmt.Errorf("create memory forest content store: %w", err)
	}

	searcher := ctxpkg.NewTieredSearcher(ctxpkg.TieredSearcherConfig{
		HotCache: ctxpkg.NewDefaultHotCache(),
		Bleve:    contentStore.BleveIndex(),
	})
	forest.SetContentStore(contentStore)
	forest.SetSearcher(searcher)

	return forest, contentStore, contentVectorDB, nil
}

// hydrateOnceCell is a one-shot rendezvous: the hydration goroutine calls
// resolve() exactly once; consumers call result() and block until the value
// is available. After the first resolve, result() returns immediately.
type hydrateOnceCell struct {
	ch  chan struct{}
	val atomic.Pointer[providers.HydratedGoogleAuth]
}

func newHydrateOnce() *hydrateOnceCell {
	return &hydrateOnceCell{ch: make(chan struct{})}
}

func (h *hydrateOnceCell) resolve(v *providers.HydratedGoogleAuth) {
	h.val.Store(v)
	close(h.ch)
}

func (h *hydrateOnceCell) result() *providers.HydratedGoogleAuth {
	<-h.ch
	return h.val.Load()
}

// bootstrapDeps initializes the core systems needed by the TUI.
// Agents run inside containers managed by the container runtime.
// DaemonSets keep Guide and Orchestrator always-hot; on-demand agents
// activate via the ActivationController when the Guide routes requests.
//
// Startup is structured in 4 phases to maximize parallelism:
//
//	Phase 1: Infrastructure (sequential, ~15ms)
//	Phase 2: Parallel creation (Guide, Orch, ActivationCtrl, HealthSync, Fonts, Git)
//	Phase 3: Wiring (sequential, depends on Phase 2 results)
//	Phase 4: Post-wiring (Architect pre-activation, handoff, session)
func bootstrapDeps(ctx context.Context, mockMode bool, projectRoot string) (ui.Deps, func() error, error) {
	return bootstrapDepsWithProgress(ctx, mockMode, projectRoot, nil)
}

func bootstrapDepsWithProgress(ctx context.Context, mockMode bool, projectRoot string, progress *bootstrapProgressReporter) (ui.Deps, func() error, error) {
	_ = mockMode
	start := time.Now()
	startupTrace("bootstrap_deps_start", "project_root", projectRoot)
	loadBootstrapDotenv(projectRoot)

	progress.ReportStage("infrastructure", 0)
	startupTrace("bootstrap_phase1_start")
	phase1, err := buildBootstrapPhase1(ctx, projectRoot, start, progress)
	if err != nil {
		progress.ReportError("infrastructure", err)
		startupTrace("bootstrap_phase1_error", "error", err.Error())
		if phase1 != nil {
			cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		}
		return ui.Deps{}, nil, err
	}
	progress.ReportStage("system participants", 1)
	startupTrace("bootstrap_phase1_done", "elapsed_ms", time.Since(start).Milliseconds(), "session_id", phaseDefaultSessionID(phase1))
	startupTrace("bootstrap_phase2_start")
	phase2, err := runBootstrapPhase2(phase1)
	if err != nil {
		progress.ReportError("system participants", err)
		startupTrace("bootstrap_phase2_error", "error", err.Error())
		return ui.Deps{}, nil, err
	}
	progress.ReportStage("agent wiring", 2)
	startupTrace("bootstrap_phase2_done", "elapsed_ms", time.Since(start).Milliseconds())
	startupTrace("bootstrap_phase3_start")
	phase3, err := wireBootstrapPhase3(phase1, phase2)
	if err != nil {
		progress.ReportError("agent wiring", err)
		startupTrace("bootstrap_phase3_error", "error", err.Error())
		cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		return ui.Deps{}, nil, err
	}
	progress.ReportStage("services", 3)
	startupTrace("bootstrap_phase3_done", "elapsed_ms", time.Since(start).Milliseconds())
	startupTrace("bootstrap_phase4_start")
	phase4, err := startBootstrapPhase4(phase1, phase2, phase3)
	if err != nil {
		progress.ReportError("services", err)
		startupTrace("bootstrap_phase4_error", "error", err.Error())
		cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		return ui.Deps{}, nil, err
	}
	progress.ReportStage("surfaces", 4)
	startupTrace("bootstrap_phase4_done", "elapsed_ms", time.Since(start).Milliseconds())
	startupTrace("bootstrap_build_deps_start")
	deps, err := buildBootstrapDeps(phase1, phase2, phase3, phase4)
	if err != nil {
		progress.ReportError("surfaces", err)
		startupTrace("bootstrap_build_deps_error", "error", err.Error())
		cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		return ui.Deps{}, nil, err
	}
	progress.ReportStage("complete", 5)
	startupTrace("bootstrap_build_deps_done", "elapsed_ms", time.Since(start).Milliseconds())
	slog.Info("bootstrap critical path complete", "elapsed", time.Since(start))
	return deps, buildBootstrapCleanup(phase1, phase3, phase4), nil
}

func tuiRunSessionConfig() session.Config {
	cfg := session.DefaultConfig()
	cfg.Name = "default"
	if cfg.Metadata == nil {
		cfg.Metadata = make(map[string]any)
	}
	cfg.Metadata["scope"] = "tui_run"
	return cfg
}

func bootstrapSessionID(sessionID string) string {
	if sid := strings.TrimSpace(sessionID); sid != "" {
		return sid
	}
	return "default"
}

func phaseDefaultSessionID(phase1 *bootstrapPhase1) string {
	if phase1 != nil && phase1.defaultSession != nil {
		return bootstrapSessionID(phase1.defaultSession.ID())
	}
	return "default"
}

func sessionClaimsPath(s *session.Session) string {
	if s == nil || s.ClaimsBoard() == nil {
		return ""
	}
	return s.ClaimsBoard().SessionDir()
}

func validateBootstrapClaimsWiring(s *session.Session) error {
	if s == nil {
		return fmt.Errorf("claims startup wiring: session is nil")
	}
	board := s.ClaimsBoard()
	if board == nil {
		return fmt.Errorf("claims startup wiring: session %q board is nil", s.ID())
	}
	snap := board.WiringSnapshot()
	if strings.TrimSpace(snap.SessionID) == "" {
		return fmt.Errorf("claims startup wiring: session %q board has empty session id", s.ID())
	}
	if !snap.HasScope {
		return fmt.Errorf("claims startup wiring: session %q board missing scope", s.ID())
	}
	if !snap.HasDeltaBus {
		return fmt.Errorf("claims startup wiring: session %q board missing delta bus", s.ID())
	}
	if !snap.HasAgentRefResolver {
		return fmt.Errorf("claims startup wiring: session %q board missing agent ref resolver", s.ID())
	}
	return nil
}

func bootstrapPhase1Status(phase1 *bootstrapPhase1) boot.Phase1Status {
	status := boot.Phase1Status{GuideBusOpened: phase1 != nil && phase1.guideBus != nil}
	if phase1 == nil || phase1.defaultSession == nil || phase1.defaultSession.ClaimsBoard() == nil {
		return status
	}
	board := phase1.defaultSession.ClaimsBoard()
	status.WALOpened = phase1.defaultSession.DurableClaimsBoard() != nil && !board.LegacySessionNoWAL()
	status.WALReplayed = true
	status.WALPath = sessionClaimsPath(phase1.defaultSession)
	status.ReplaySequence = board.HighWaterSequence()
	status.Context = map[string]any{"session_id": phase1.defaultSession.ID()}
	return status
}

func bootstrapSystemParticipants(phase1 *bootstrapPhase1, phase2 bootstrapPhase2) []boot.SystemParticipantActivation {
	sessionID := phaseDefaultSessionID(phase1)
	activationControllerReady := phase2.actResult.err == nil && phase2.actResult.ctrl != nil
	return []boot.SystemParticipantActivation{
		boot.NewSystemParticipantActivation(boot.SystemBusAdministratorAgentID, "bus_administrator", activationControllerReady && phase1 != nil && phase1.guideBus != nil, map[string]any{
			"activation_controller_ready": activationControllerReady,
			"transport":                   "guide_channel_bus",
			"session_id":                  sessionID,
		}),
		boot.NewSystemParticipantActivation(boot.SystemSessionManagerAgentID, "session_manager", activationControllerReady && phase1 != nil && phase1.sessionMgr != nil && phase1.defaultSession != nil, map[string]any{
			"activation_controller_ready": activationControllerReady,
			"session_id":                  sessionID,
		}),
		boot.NewSystemParticipantActivation(boot.SystemFabricSubscriberAgentID, "fabric_subscriber", activationControllerReady && phase1 != nil && phase1.activityPub != nil && phase1.defaultSession != nil && phase1.defaultSession.ClaimsBoard() != nil, map[string]any{
			"activation_controller_ready": activationControllerReady,
			"claims_outbox":               claims.CurrentRolloutConfig().ClaimsOutbox,
			"session_id":                  sessionID,
		}),
	}
}

func bootstrapKnowledgeBackends(phase1 *bootstrapPhase1) []boot.SystemParticipantActivation {
	sessionID := phaseDefaultSessionID(phase1)
	backendReady := phase1 != nil && phase1.knowledgeBackend != nil
	storeReady := phase1 != nil && phase1.knowledgeStore != nil
	forestReady := phase1 != nil && phase1.forest != nil
	return []boot.SystemParticipantActivation{
		boot.NewServiceParticipantReadiness(boot.SystemKnowledgeGraphReaderID, "knowledge_graph_reader", backendReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemKnowledgeGraphWriterID, "knowledge_graph_writer", backendReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemDocumentDBReaderID, "document_db_reader", storeReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemDocumentDBWriterID, "document_db_writer", storeReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemMemoryForestAgentID, "memory_forest", forestReady, map[string]any{"session_id": sessionID}),
	}
}

func bootstrapInfrastructureServices(phase1 *bootstrapPhase1, phase2 bootstrapPhase2, phase3 bootstrapPhase3) []boot.SystemParticipantActivation {
	sessionID := phaseDefaultSessionID(phase1)
	vfsReady := phase3.orch != nil
	providerReady := phase1 != nil && phase1.googleGateway != nil && phase1.anthropicGateway != nil && phase1.openaiGateway != nil
	guardianReady := phase2.guardianResult.err == nil && phase1 != nil && phase1.guardianRef.Load() != nil
	return []boot.SystemParticipantActivation{
		boot.NewServiceParticipantReadiness(boot.SystemVFSPipelineProvisionerID, "vfs_pipeline_provisioner", vfsReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemVFSToolProvisionerID, "vfs_tool_provisioner", vfsReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemVFSGlobalProvisionerID, "vfs_global_provisioner", vfsReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemDAGProcessorID, "dag_processor", phase1 != nil && phase1.daemonCtrl != nil, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemToolRuntimeID, "tool_runtime", phase1 != nil && phase1.runtime != nil, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemProviderGatewayID, "provider_gateway", providerReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemGuardianServiceID, "guardian_service", guardianReady, map[string]any{"session_id": sessionID}),
	}
}

func bootstrapAlwaysHotAgents(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) []boot.SystemParticipantActivation {
	sessionID := phaseDefaultSessionID(phase1)
	agents := []boot.SystemParticipantActivation{
		boot.NewSystemParticipantActivation(boot.SystemGuideAgentID, "guide", phase3.guide != nil, map[string]any{"session_id": sessionID}),
	}
	if phase3.orch != nil {
		agents = append(agents, boot.NewSystemParticipantActivation("orchestrator", "orchestrator", true, map[string]any{"session_id": sessionID}))
	}
	if phase1 != nil && phase1.guardianRef.Load() != nil {
		agents = append(agents, boot.NewSystemParticipantActivation("guardian", "guardian", true, map[string]any{"session_id": sessionID}))
	}
	return agents
}

func bootstrapUserFacingSurfaces(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) []boot.SystemParticipantActivation {
	sessionID := phaseDefaultSessionID(phase1)
	boardReady := phase1 != nil && phase1.defaultSession != nil && phase1.defaultSession.ClaimsBoard() != nil
	return []boot.SystemParticipantActivation{
		boot.NewServiceParticipantReadiness(boot.SystemUIBridgeID, "ui_bridge", phase3.guide != nil && boardReady, map[string]any{"session_id": sessionID}),
		boot.NewServiceParticipantReadiness(boot.SystemCanonicalDeltaObserverID, "canonical_delta_observer", boardReady, map[string]any{"session_id": sessionID}),
	}
}

func loadBootstrapDotenv(projectRoot string) {
	if err := boot.LoadDotenv(projectRoot); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to load .env.local", "error", err)
	}
}

func buildBootstrapPhase1(ctx context.Context, projectRoot string, start time.Time, progress *bootstrapProgressReporter) (*bootstrapPhase1, error) {
	phase1 := &bootstrapPhase1{
		ctx:         ctx,
		projectRoot: projectRoot,
		start:       start,
		progress:    progress,
	}

	phase1.scope = concurrency.NewGoroutineScope(ctx, "tui", nil)
	phase1.scope.SetMaxLifetime(24 * time.Hour)
	phase0, err := boot.InitializePhase0(boot.Phase0Config{StartedAt: start, Scope: &concurrency.ScopeAdapter{Scope: phase1.scope}})
	if err != nil {
		return phase1, fmt.Errorf("claims operations phase 0: %w", err)
	}
	phase1.bootIdentity = phase0.Identity
	phase1.phase0Board = phase0.Board

	guideChannelBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	phase1.guideBus = guideChannelBus
	phase1.activityPub = events.NewMetadataCachingPublisher(
		guide.NewBusActivityPublisher(phase1.guideBus),
		map[string]events.AgentIdentityMetadata{
			"guide":              {AgentType: "guide", AgentName: "Guide"},
			"architect":          {AgentType: "architect", AgentName: "Architect"},
			"guardian":           {AgentType: "guardian", AgentName: "Guardian"},
			"orchestrator":       {AgentType: "orchestrator", AgentName: "Orchestrator"},
			"inspector":          {AgentType: "inspector", AgentName: "Inspector"},
			"tester":             {AgentType: "tester", AgentName: "Tester"},
			"librarian":          {AgentType: "librarian", AgentName: "Librarian"},
			"archivalist":        {AgentType: "archivalist", AgentName: "Archivalist"},
			"academic":           {AgentType: "academic", AgentName: "Academic"},
			"engineer":           {AgentType: "engineer", AgentName: "Engineer"},
			"designer":           {AgentType: "designer", AgentName: "Designer"},
			"inspector-pipeline": {AgentType: "inspector-pipeline", AgentName: "Inspector"},
			"tester-pipeline":    {AgentType: "tester-pipeline", AgentName: "Tester"},
		},
	)
	phase1.streamMgr = guide.NewStreamManager(guide.DefaultStreamConfig())
	// Bridge the guide ChannelBus into the claims DeltaBus surface so
	// every session board's amplifier publishes InboxDelta / TestamentDelta
	// onto the same transport pipeline workers subscribe to via
	// ClaimsInbox. Without this adapter, board emissions go to the
	// default NoopDeltaBus and orchestrator-dispatched claims never
	// reach worker inboxes.
	claimsDeltaBus := guide.NewClaimsBusAdapter(guideChannelBus)
	claimsRollout := claims.RolloutConfigFromEnvironment()
	claims.SetDefaultRolloutConfig(claimsRollout)
	phase1.knowledgeBackend = knowledgeruntime.NewCommittedKnowledgeBackend(projectRoot, slog.Default())
	if claimsRollout.ClaimsKnowledgeMirrorEnabled() {
		claims.SetDefaultRecallForwardEnrichmentProvider(knowledgeruntime.NewClaimsKnowledgeQueryIndex(phase1.knowledgeBackend))
	} else {
		claims.SetDefaultRecallForwardEnrichmentProvider(nil)
	}
	claimsProjectors := []claims.ClaimsProjector{}
	if claimsRollout.ClaimsKnowledgeMirrorEnabled() {
		claimsProjectors = append(claimsProjectors, knowledgeruntime.NewClaimsKnowledgeMirror(phase1.knowledgeBackend))
	}
	phase1.identityReg = container.NewAgentIdentityRegistryWithUID(phase1.bootIdentity.IdentityRegistryUID, []string{
		"guide", "architect", "engineer", "designer", "inspector", "tester",
		"inspector-pipeline", "tester-pipeline",
		"librarian", "archivalist", "academic", "orchestrator",
	})
	claimsResolver := containerIdentityAgentRefResolver{registry: phase1.identityReg, bootIdentity: phase1.bootIdentity}
	phase1.sessionMgr = session.NewManager(session.ManagerConfig{
		Scope:            phase1.scope,
		DeltaBus:         claimsDeltaBus,
		AgentRefResolver: claimsResolver,
		ClaimsProjectors: claimsProjectors,
		ClaimsRollout:    &claimsRollout,
	})
	phase1.bootDispatchers = claims.NewServiceDispatcherRegistry()
	phase1.descriptors = handoff.NewDescriptorRegistry()

	// Create the run's default session eagerly so the identity Factory
	// can bind to it at phase1. The ID is intentionally fresh for each
	// TUI process; the storage-layer "default" session is retained for
	// legacy paths, but it must not be replayed into a new run's UI.
	defaultSession, err := phase1.sessionMgr.Create(ctx, tuiRunSessionConfig())
	if err != nil {
		return phase1, fmt.Errorf("create default session: %w", err)
	}
	phase1.defaultSession = defaultSession
	_ = phase1.sessionMgr.Switch(defaultSession.ID())
	if err := validateBootstrapClaimsWiring(defaultSession); err != nil {
		return phase1, err
	}
	bootOps, err := boot.NewOperationsSequencer(boot.OperationsConfig{
		Board:                     defaultSession.ClaimsBoard(),
		ProcessIdentity:           phase1.bootIdentity,
		ServiceDispatcherRegistry: phase1.bootDispatchers,
		ServiceSubscriber:         claimsDeltaBus,
		ServiceScope:              &concurrency.ScopeAdapter{Scope: phase1.scope},
	})
	if err != nil {
		return phase1, fmt.Errorf("claims operations boot sequencer: %w", err)
	}
	phase1.bootOps = bootOps
	phase1.bootDeltaUnsub = startBootstrapClaimsDeltaTrace(defaultSession.ClaimsBoard())
	result, err := phase1.bootOps.CommitPhase1(ctx, bootstrapPhase1Status(phase1))
	traceBootPhaseCommit("claims_operations_phase1_commit", result, err, phase1.bootOps)
	reportBootstrapClaimsHealth(phase1)
	if err != nil {
		return phase1, fmt.Errorf("claims operations phase 1: %w", err)
	}
	factory, err := buildIdentityFactory(phase1.descriptors, defaultSession.ID())
	if err != nil {
		return phase1, fmt.Errorf("identity factory: %w", err)
	}
	phase1.identityFactory.Store(factory)

	pressureLevel := new(atomic.Int32)
	phase1.budget = concurrency.NewGoroutineBudget(pressureLevel)
	phase1.budget.RegisterAgent("tui", "tui")
	phase1.scope.SetBudget(phase1.budget)
	phase1.containerReg = container.NewContainerRegistry()
	phase1.serviceReg = network.NewServiceRegistry()
	phase1.specReg = container.NewAgentSpecRegistry(phase1.descriptors)
	phase1.quota = container.NewResourceQuota(quotaFromSpecs(phase1.specReg))
	phase1.quota.SetContextArchetypeMatcher(activation.NewActivationPredictor(activation.PredictorConfig{}))
	if observer := agentShared.NewContextBudgetObserver("tui.context_budget", phase1.guideBus, phase1.quota); observer != nil {
		if err := observer.Start(); err != nil {
			slog.Warn("failed to start context budget observer", "error", err)
		}
	}

	creatorReg := container.NewAgentCreatorRegistry()
	phase1.creatorReg = creatorReg
	phase1.hookMut = &lifecycleHookMutator{serviceReg: phase1.serviceReg}
	admission := container.NewAdmissionController(nil, []container.SpecMutator{phase1.hookMut})
	phase1.probeFact = &probeFactoryHolder{}

	phase1.runtime = container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:       phase1.budget,
		Registry:     phase1.containerReg,
		Quota:        phase1.quota,
		Admission:    admission,
		CreateAgent:  creatorReg.Creator(),
		ProbeFactory: phase1.probeFact.Build,
		ParentCtx:    ctx,
	})

	policies := container.BuildNetworkPolicies(phase1.descriptors.All())
	busBridge := network.NewBusBridge(func(topic, sourceAgent, targetAgent string, payload []byte) error {
		msg := guide.NewBridgeMessage(sourceAgent, targetAgent, payload)
		return phase1.guideBus.Publish(topic, msg)
	})
	phase1.namespace = network.NewNetworkNamespace(network.NetworkNamespaceConfig{
		PodID:    "system",
		Policies: policies,
		Sink:     busBridge,
	})

	phase1.daemonCtrl = daemon.NewDaemonSetController(phase1.runtime, phase1.containerReg)
	daemonSpecs := daemon.AgentDaemonSetSpecs(phase1.specReg)
	for _, spec := range daemonSpecs {
		phase1.daemonCtrl.Apply(spec)
	}
	phase1.daemonSpecMap = make(map[string]container.ContainerSpec, len(daemonSpecs))
	for _, spec := range daemonSpecs {
		phase1.daemonSpecMap[spec.Name] = spec.ContainerSpec
	}

	authResolver := buildAuthResolver()
	authPublisher := chainPublishers(
		buildAuthPublisher(phase1.guideBus),
		buildOnDemandAuthRefresher(phase1.containerReg),
	)
	phase1.authRegistry = credentials.NewAuthRegistry(authResolver, nil, authPublisher, slog.Default())
	phase1.authRegistry.PrimeAll()

	phase1.guideCfg = defaultGuideGoogleConfig(phase1.authRegistry)
	phase1.hydrateOnce = newHydrateOnce()

	googleGatewayCfg := gateway.DefaultGoogleAPIKeyConfig()
	if phase1.guideCfg.AuthMode == providers.GoogleAuthModeOAuth {
		googleGatewayCfg = gateway.DefaultGoogleOAuthConfig()
	}
	phase1.googleGateway = gateway.NewProviderGateway(googleGatewayCfg, slog.Default())
	phase1.anthropicGateway = gateway.NewProviderGateway(gateway.DefaultAnthropicConfig(), slog.Default())
	phase1.openaiGateway = gateway.NewProviderGateway(gateway.DefaultOpenAIConfig(), slog.Default())

	llmEventHook := providers.NewLLMEventPublisherHook(
		providers.NewLLMEventPublisher(phase1.activityPub),
	)
	phase1.googleGateway.SetEventHook(llmEventHook)
	phase1.anthropicGateway.SetEventHook(llmEventHook)
	phase1.openaiGateway.SetEventHook(llmEventHook)

	planLeaseManager := architect.NewPlanLeaseManager(architect.DefaultLLMRequestTimeout, architect.ReadyPlanMaxAge)
	phase1.planStore = architect.NewPlanStore(projectRoot, planLeaseManager, slog.Default())
	phase1.knowledgeStore = knowledge.NewKnowledgeStore(
		&busReadinessPublisher{
			bus: phase1.guideBus,
			sessionID: func() string {
				if phase1.defaultSession == nil {
					return ""
				}
				return phase1.defaultSession.ID()
			},
		},
		slog.Default(),
	)
	forest, forestContent, forestVectorDB, err := buildMemoryForestForSession(projectRoot, phase1.defaultSession.ID(), phase1.budget)
	if err != nil {
		return phase1, fmt.Errorf("bootstrap memory forest: %w", err)
	}
	phase1.forest = forest
	phase1.forestContent = forestContent
	phase1.forestVectorDB = forestVectorDB

	registerAgentCreators(
		creatorReg,
		phase1.identityReg,
		phase1.guideBus,
		phase1.activityPub,
		projectRoot,
		phase1.hydrateOnce,
		&phase1.hydratedRef,
		phase1.googleGateway,
		phase1.anthropicGateway,
		phase1.openaiGateway,
		phase1.authRegistry,
		&phase1.actCtrlRef,
		&phase1.gitSubsRef,
		phase1.planStore,
		phase1.knowledgeStore,
		phase1.knowledgeBackend,
		phase1.forest,
		phase1.daemonCtrl,
		&phase1.guideRef,
		&phase1.guardianRef,
		&phase1.orchRef,
		&phase1.quarantineRef,
		phase1.quota,
		phase1.budget,
		&phase1.identityFactory,
		phase1.defaultSession.ID(),
		phase1.scope,
	)

	slog.Info("bootstrap phase 1 complete", "elapsed", time.Since(start))
	return phase1, nil
}

func startDaemonContainerBootstrap(
	ctx context.Context,
	runtime *container.DefaultRuntime,
	specMap map[string]container.ContainerSpec,
	name string,
	ch chan<- daemonContainerResult,
) {
	go func() {
		spec, ok := specMap[name]
		if !ok {
			ch <- daemonContainerResult{err: fmt.Errorf("no daemon spec for %s", name)}
			return
		}
		c, err := runtime.CreateContainer(ctx, spec)
		if err != nil {
			ch <- daemonContainerResult{err: err}
			return
		}
		if err := runtime.StartContainer(ctx, c); err != nil {
			_ = runtime.RemoveContainer(ctx, c)
			ch <- daemonContainerResult{err: err}
			return
		}
		ch <- daemonContainerResult{c: c}
	}()
}

func startActivationControllerBootstrap(phase1 *bootstrapPhase1, ch chan<- activationCtrlResult) {
	go func() {
		activationPolicies, err := activation.AgentActivationPolicies(phase1.descriptors.All())
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		storageDir, err := activationStorageDir()
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		sessMgr := phase1.sessionMgr
		ctrl, err := activation.NewActivationController(activation.ActivationControllerConfig{
			Runtime:    phase1.runtime,
			Registry:   phase1.containerReg,
			Scope:      phase1.scope,
			Policies:   activationPolicies,
			StorageDir: storageDir,
			BoardProvider: func() *claims.ClaimsBoard {
				if sess, ok := sessMgr.GetActive(); ok {
					return claims.DefaultSessionBoardRegistry().Lookup(sess.ID())
				}
				return nil
			},
		})
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		if err := ctrl.Start(phase1.budget, phase1.quota); err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		ch <- activationCtrlResult{ctrl: ctrl}
	}()
}

func startHealthSyncerBootstrap(phase1 *bootstrapPhase1, ch chan<- error) {
	go func() {
		syncer := container.NewHealthSyncer(container.HealthSyncerConfig{
			ContainerRegistry: phase1.containerReg,
			ServiceRegistry:   phase1.serviceReg,
			Scope:             phase1.scope,
		})
		ch <- syncer.Start()
	}()
}

func startFontBootstrap(ch chan<- fontResult) {
	go func() {
		ch <- fontResult{detected: fonts.Detected()}
	}()
}

func startGitBootstrap(projectRoot string, gitSubsRef *atomic.Pointer[gitBootResult], ch chan<- gitBootResult) {
	go func() {
		var result gitBootResult
		gc, err := git.NewGitClient(projectRoot)
		if err != nil || !gc.IsGitRepo() {
			ch <- result
			return
		}
		result.client = gc
		result.bus = git.NewGitBus(gc)
		if sw, err := git.NewStatusWatcher(gc); err == nil {
			result.watcher = sw
		}
		if sg, err := git.NewSafetyGuard(gc, result.bus, git.DefaultSafetyConfig(), result.watcher); err == nil {
			result.guard = sg
		}
		gitSubsRef.Store(&result)
		ch <- result
	}()
}

func startHydrationBootstrap(
	ctx context.Context,
	guideCfg providers.GoogleConfig,
	hydrateOnce *hydrateOnceCell,
	hydratedRef *atomic.Pointer[providers.HydratedGoogleAuth],
	ch chan<- hydrateResult,
) {
	const (
		hydrateMaxAttempts = 3
		hydrateRetryDelay  = 500 * time.Millisecond
	)

	go func() {
		var hydrated *providers.HydratedGoogleAuth
		for attempt := 1; attempt <= hydrateMaxAttempts; attempt++ {
			result, err := providers.HydrateGoogleAuth(ctx, guideCfg)
			if err == nil {
				hydrated = result
				hydratedRef.Store(hydrated)
				break
			}
			slog.Warn("google auth hydration attempt failed",
				"attempt", attempt,
				"max", hydrateMaxAttempts,
				"error", err)
			if attempt < hydrateMaxAttempts {
				select {
				case <-ctx.Done():
				case <-time.After(hydrateRetryDelay):
				}
			}
		}
		hydrateOnce.resolve(hydrated)
		ch <- hydrateResult{hydrated: hydrated}
	}()
}

func cleanupPhase2Critical(phase1 *bootstrapPhase1, phase2 bootstrapPhase2) {
	for _, c := range []*container.Container{phase2.guideResult.c, phase2.orchResult.c} {
		if c != nil {
			_ = phase1.runtime.StopContainer(context.Background(), c)
			_ = phase1.runtime.RemoveContainer(context.Background(), c)
		}
	}
	cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
}

func runBootstrapPhase2(phase1 *bootstrapPhase1) (bootstrapPhase2, error) {
	phase2Start := time.Now()
	_, parallelCancel := context.WithCancel(phase1.ctx)
	defer parallelCancel()

	guideCh := make(chan daemonContainerResult, 1)
	orchCh := make(chan daemonContainerResult, 1)
	guardianCh := make(chan daemonContainerResult, 1)
	activationCh := make(chan activationCtrlResult, 1)
	healthCh := make(chan error, 1)
	fontCh := make(chan fontResult, 1)
	gitCh := make(chan gitBootResult, 1)
	hydrateCh := make(chan hydrateResult, 1)

	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "guide", guideCh)
	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "orchestrator", orchCh)
	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "guardian", guardianCh)
	startActivationControllerBootstrap(phase1, activationCh)
	startHealthSyncerBootstrap(phase1, healthCh)
	startFontBootstrap(fontCh)
	startGitBootstrap(phase1.projectRoot, &phase1.gitSubsRef, gitCh)
	startHydrationBootstrap(phase1.ctx, phase1.guideCfg, phase1.hydrateOnce, &phase1.hydratedRef, hydrateCh)

	var (
		phase2      bootstrapPhase2
		criticalErr error
	)

	for completed := 0; completed < 8; completed++ {
		select {
		case r := <-guideCh:
			phase2.guideResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("guide container: %w", r.err)
				parallelCancel()
			}
			guideCh = nil
		case r := <-orchCh:
			phase2.orchResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("orchestrator container: %w", r.err)
				parallelCancel()
			}
			orchCh = nil
		case r := <-guardianCh:
			phase2.guardianResult = r
			if r.err != nil {
				slog.Warn("guardian container failed during parallel bootstrap", "error", r.err)
			}
			guardianCh = nil
		case r := <-activationCh:
			phase2.actResult = r
			if r.err != nil {
				slog.Warn("activation controller failed during parallel bootstrap", "error", r.err)
			}
			activationCh = nil
		case err := <-healthCh:
			if err != nil {
				slog.Warn("health syncer start failed", "error", err)
			}
			healthCh = nil
		case r := <-fontCh:
			phase2.fontRes = r
			fontCh = nil
		case r := <-gitCh:
			phase2.gitRes = r
			gitCh = nil
		case <-hydrateCh:
			hydrateCh = nil
		}
	}

	if phase1.bootOps != nil {
		result, err := phase1.bootOps.CommitPhase2(phase1.ctx, boot.Phase2Status{
			Participants: bootstrapSystemParticipants(phase1, phase2),
			Context:      map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase2_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return phase2, fmt.Errorf("claims operations phase 2: %w", err)
		}
	}

	if criticalErr != nil {
		cleanupPhase2Critical(phase1, phase2)
		return phase2, criticalErr
	}

	phase1.daemonCtrl.InjectInstance("guide", phase2.guideResult.c)
	phase1.daemonCtrl.InjectInstance("orchestrator", phase2.orchResult.c)
	if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
		phase1.daemonCtrl.InjectInstance("guardian", phase2.guardianResult.c)
	}

	slog.Info("bootstrap phase 2 complete", "elapsed", time.Since(phase2Start))
	return phase2, nil
}

func wireBootstrapPhase3(phase1 *bootstrapPhase1, phase2 bootstrapPhase2) (bootstrapPhase3, error) {
	phase3Start := time.Now()

	g, err := extractAgent[*guide.Guide](phase1.containerReg, "guide")
	if err != nil {
		return bootstrapPhase3{}, fmt.Errorf("extract guide: %w", err)
	}
	phase1.guideRef.Store(g)

	// Bind the Guide's event logger to this run's default session directory
	// eagerly. Without this the logger binds lazily on the first
	// incoming route request — which means if the user never routes
	// anything, the guide's WAL + events.jsonl never get written.
	// Binding here guarantees a "GuideStarted" anchor lands on disk
	// and every subsequent logEvent call reaches the per-day stream.
	if phase1.defaultSession != nil {
		sessionID := phase1.defaultSession.ID()
		sessionDir := filepath.Join(".sylk", "sessions", sessionID)
		if err := g.BindSession(sessionDir, sessionID); err != nil {
			slog.Warn("guide: eager BindSession failed",
				"session_id", sessionID, "error", err)
		}
	}

	orch, err := extractAgent[*orchestrator.Orchestrator](phase1.containerReg, "orchestrator")
	if err != nil {
		return bootstrapPhase3{}, fmt.Errorf("extract orchestrator: %w", err)
	}
	phase1.orchRef.Store(orch)

	phase1.hookMut.SetGuide(g)
	phase1.probeFact.SetIsReady(g.IsAgentReady)
	if err := registerOrchestratorWithGuide(g, orch); err != nil {
		return bootstrapPhase3{}, fmt.Errorf("register orchestrator: %w", err)
	}

	phase3 := bootstrapPhase3{
		guide:          g,
		orch:           orch,
		activationCtrl: phase2.actResult.ctrl,
	}
	if phase3.activationCtrl != nil {
		phase1.actCtrlRef.Store(phase3.activationCtrl)
		if _, err := phase3.activationCtrl.AdoptContainer("guide", phase2.guideResult.c); err != nil {
			slog.Warn("adopt guide container", "error", err)
		}
		if _, err := phase3.activationCtrl.AdoptContainer("orchestrator", phase2.orchResult.c); err != nil {
			slog.Warn("adopt orchestrator container", "error", err)
		}
		if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
			if _, err := phase3.activationCtrl.AdoptContainer("guardian", phase2.guardianResult.c); err != nil {
				slog.Warn("adopt guardian container", "error", err)
			}
		}
	}

	preRegisterAgentRouting(g, phase1.identityReg)

	if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
		if grd, grdErr := extractAgent[*guardian.Guardian](phase1.containerReg, "guardian"); grdErr == nil {
			phase1.guardianRef.Store(grd)
			if phase2.gitRes.bus != nil {
				grd.SetGitSubsystems(phase2.gitRes.bus, phase2.gitRes.watcher)
			}
			wireGuardianPostBootDeps(
				grd,
				&phase1.actCtrlRef,
				phase1.daemonCtrl,
				&phase1.guideRef,
				&phase1.orchRef,
				phase1.authRegistry,
				phase1.openaiGateway,
				[]*gateway.ProviderGateway{phase1.googleGateway, phase1.anthropicGateway, phase1.openaiGateway},
				phase1.knowledgeStore,
				phase1.budget,
			)
		}
	}

	if phase3.activationCtrl != nil {
		phase3.activator = activation.NewControllerPodActivator(phase3.activationCtrl)
		g.SetActivator(phase3.activator)
	}
	g.SetServiceRegistry(phase1.serviceReg)
	g.SetProviderWrapper(phase1.googleGateway.Wrapper(gateway.PriorityUserInteractive))
	g.SetAgentRegistrar(func(podID, agentType string) {
		c := findRegisteredPodContainer(phase1.containerReg, podID, agentType)
		if c == nil {
			return
		}
		agent := c.Agent()
		if router, ok := agent.(guide.AgentRouter); ok {
			_ = registerAgentWithGuide(g, router, agentType)
		}
	})

	orch.SetProviderRefresher(func(refreshCtx context.Context, authMethod string) {
		refreshOrchestratorProvider(refreshCtx, orch, phase1.googleGateway, phase1.authRegistry, authMethod)
	})
	orch.SetTaskRouter(orchestrator.NewTaskRouter(orchestrator.TaskRouterConfig{
		Bus:                    phase1.guideBus,
		Scope:                  phase1.scope,
		AgentID:                orch.AgentID(),
		SessionID:              phaseDefaultSessionID(phase1),
		OnVisibleRouteTerminal: nil,
	}))
	if phase3.activator != nil {
		orch.SetActivator(phase3.activator)
	}
	orch.SetRegistrar(func(ctx context.Context, podID, agentType string) error {
		c := findRegisteredPodContainer(phase1.containerReg, podID, agentType)
		if c == nil {
			return fmt.Errorf("no container for pod %s agent type %s after activation", podID, agentType)
		}
		router, ok := c.Agent().(guide.AgentRouter)
		if !ok {
			return nil
		}
		info := router.GetRoutingInfo()
		if isTaskScopedPipelineWorker(podID, agentType) {
			info = taskScopedWorkerRoutingInfo(info, podID, agentType)
		}
		return registerRoutingInfoWithGuide(g, info)
	})
	orch.SetTaskPodInfra(phase1.runtime, phase1.specReg, func(sessionID string) *versioning.SessionVFS {
		return orch.EnsureSessionVFS(sessionID, phase1.projectRoot)
	})
	orch.SetContextQuota(phase1.quota)

	phase3.scribeFactory = buildScribeFactory(
		phase1.ctx,
		phase1.googleGateway,
		phase1.authRegistry,
		phase1.guideBus,
		phase1.scope,
		phase1.forest,
		&phase1.handoffRef,
	)
	if phase3.scribeFactory != nil {
		orch.SetScribeFactory(phase3.scribeFactory)
	}
	if phase1.bootOps != nil {
		result, err := phase1.bootOps.CommitPhase3(phase1.ctx, boot.Phase3Status{
			Backends: bootstrapKnowledgeBackends(phase1),
			Context:  map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase3_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return bootstrapPhase3{}, fmt.Errorf("claims operations phase 3: %w", err)
		}
		if err := recoverBootstrapClaimsWork(phase1); err != nil {
			startupTrace("claims_operations_recovery_error", "error", err.Error())
			return bootstrapPhase3{}, fmt.Errorf("claims operations recovery: %w", err)
		}
		startupTrace("claims_operations_recovery_done")
	}

	slog.Info("bootstrap phase 3 complete", "elapsed", time.Since(phase3Start))
	return phase3, nil
}

func recoverBootstrapClaimsWork(phase1 *bootstrapPhase1) error {
	board := bootstrapClaimsBoard(phase1)
	if board == nil {
		return nil
	}
	participants := serviceRecoveryParticipants(phase1.bootDispatchers, board.SessionID())
	result, err := claims.RecoverServiceWork(phase1.ctx, claims.ServiceRecoveryOptions{
		Board:        board,
		Dispatchers:  phase1.bootDispatchers,
		Participants: participants,
		ActorID:      claimsStartupRecoveryActorID,
	})
	logBootstrapClaimsRecovery(result, err)
	return err
}

func startBootstrapClaimsOperationsAudits(phase1 *bootstrapPhase1) error {
	board := bootstrapClaimsBoard(phase1)
	if board == nil || phase1 == nil || phase1.scope == nil {
		return nil
	}
	return claims.StartClaimsOperationsAudits(phase1.ctx, &concurrency.ScopeAdapter{Scope: phase1.scope}, claims.OperationsAuditOptions{
		Config:             board.OperationsConfig(),
		BoardRegistry:      claims.DefaultSessionBoardRegistry(),
		InboxRegistry:      claims.DefaultSessionInboxRegistry(),
		ServiceDispatchers: phase1.bootDispatchers,
		Boards:             []*claims.ClaimsBoard{board},
		DurableBoards:      bootstrapDurableClaimsBoards(phase1),
		EvidenceBoard:      board,
		SessionID:          board.SessionID(),
	})
}

func bootstrapDurableClaimsBoards(phase1 *bootstrapPhase1) []*claims.DurableBoard {
	if phase1 == nil || phase1.defaultSession == nil || phase1.defaultSession.DurableClaimsBoard() == nil {
		return nil
	}
	return []*claims.DurableBoard{phase1.defaultSession.DurableClaimsBoard()}
}

func bootstrapClaimsBoard(phase1 *bootstrapPhase1) *claims.ClaimsBoard {
	if phase1 == nil || phase1.defaultSession == nil {
		return nil
	}
	return phase1.defaultSession.ClaimsBoard()
}

func startBootstrapClaimsDeltaTrace(board *claims.ClaimsBoard) func() {
	if board == nil {
		return nil
	}
	startupTrace("claims_board_delta_watch_start",
		"session_id", board.SessionID(),
		"board_id", board.BoardID(),
		"high_water_sequence", board.HighWaterSequence(),
	)
	return board.SubscribeDelta(func(delta claims.BoardMutationDelta) error {
		startupTrace("claims_board_delta",
			"session_id", board.SessionID(),
			"board_id", board.BoardID(),
			"kind", delta.Kind,
			"claim_id", delta.ClaimID,
			"testament_id", delta.TestamentID,
			"from_status", string(delta.FromStatus),
			"to_status", string(delta.ToStatus),
			"agent_id", delta.AgentID,
			"summary_pending", delta.Summary.Pending,
			"summary_in_progress", delta.Summary.InProgress,
			"summary_testified", delta.Summary.Testified,
			"summary_accepted", delta.Summary.Accepted,
			"summary_rejected", delta.Summary.Rejected,
			"high_water_sequence", board.HighWaterSequence(),
		)
		return nil
	})
}

func traceBootPhaseCommit(event string, result boot.PhaseCommitResult, err error, ops *boot.OperationsSequencer) {
	fields := []any{
		"phase", string(result.Phase),
		"claim_id", result.ClaimID,
		"testament_id", result.TestamentID,
		"participant_claims", len(result.ParticipantClaimIDs),
		"participant_testaments", len(result.ParticipantTestamentIDs),
	}
	if err != nil {
		fields = append(fields, "error", err.Error())
	}
	startupTrace(event, fields...)
	traceBootHealth(event+"_health", ops)
}

func traceBootHealth(event string, ops *boot.OperationsSequencer) {
	if ops == nil {
		return
	}
	health := ops.BootHealth()
	startupTrace(event,
		"board_id", health.BoardID,
		"session_id", health.SessionID,
		"high_water_sequence", health.HighWaterSequence,
		"outbox_lag", health.OutboxLag,
		"terminal_failures", health.TerminalFailures,
		"phase_count", len(health.Phases),
	)
	for _, phase := range health.Phases {
		startupTrace(event+"_phase",
			"phase", string(phase.Phase),
			"outcome", phase.Outcome,
			"claim_id", phase.ClaimID,
			"testament_id", phase.TestamentID,
			"readiness_count", phase.ReadinessCount,
			"failure_count", phase.FailureCount,
			"warning_count", len(phase.Warnings),
		)
	}
}

func serviceRecoveryParticipants(registry *claims.ServiceDispatcherRegistry, sessionID string) []claims.ParticipantRegistration {
	if registry == nil {
		return nil
	}
	snapshot := registry.Snapshot(sessionID)
	participants := make([]claims.ParticipantRegistration, 0, len(snapshot.Dispatchers))
	for _, dispatcher := range snapshot.Dispatchers {
		if dispatcher.Participant.Validate() == nil {
			participants = append(participants, dispatcher.Participant)
		}
	}
	return participants
}

func logBootstrapClaimsRecovery(result claims.ServiceRecoveryResult, err error) {
	if err != nil {
		slog.Warn("claims startup recovery failed", "error", err)
		return
	}
	if claimsRecoveryNoop(result) {
		return
	}
	slog.Info("claims startup recovery complete",
		"scanned", result.Scanned,
		"redispatched", result.Redispatched,
		"failed", result.Failed,
		"validation_scanned", result.ValidationScanned,
		"validation_redispatched", result.ValidationRedispatched,
		"validation_failed", result.ValidationFailed,
		"continuations_recovered", result.ContinuationsRecovered,
		"bounded", result.Bounded)
}

func claimsRecoveryNoop(result claims.ServiceRecoveryResult) bool {
	return result.Scanned == 0 &&
		result.ValidationScanned == 0 &&
		result.ContinuationsRecovered == 0
}

func schedulePhase4Task(
	scope *concurrency.GoroutineScope,
	name string,
	timeout time.Duration,
	remaining *atomic.Int32,
	onDone func(),
	task func(context.Context) error,
) {
	remaining.Add(1)
	_ = scope.Go(name, timeout, func(bgCtx context.Context) error {
		defer onDone()
		return task(bgCtx)
	})
}

func waitForBootBleveReady(ctx context.Context, store *knowledge.KnowledgeStore) error {
	if store == nil {
		startupTrace("knowledge_boot_bleve_ready_store_nil")
		return nil
	}
	if err := store.WaitForPartial(ctx); err != nil {
		startupTrace("knowledge_boot_bleve_ready_partial_cancelled", "error", err.Error())
		return nil
	}
	startupTrace("knowledge_boot_bleve_ready_partial_observed")
	bgWaiter := store.BackgroundWaiter()
	if bgWaiter == nil {
		if ctx.Err() != nil {
			startupTrace("knowledge_boot_bleve_ready_no_waiter_cancelled", "error", ctx.Err().Error())
			return nil
		}
		startupTrace("knowledge_boot_bleve_ready_no_waiter_promote_full")
		store.PromoteFull()
		return nil
	}

	select {
	case <-bgWaiter.Ready():
		if ctx.Err() != nil {
			startupTrace("knowledge_boot_bleve_ready_ready_cancelled", "error", ctx.Err().Error())
			return nil
		}
		// Full promotion is the only user-visible transition needed here.
		// Reopening/adopting a fresh owned Bleve store during shutdown can block
		// on repository-sized I/O, so cleanup relies on backend.Close instead.
		startupTrace("knowledge_boot_bleve_ready_promote_full")
		store.PromoteFull()
	case <-ctx.Done():
		startupTrace("knowledge_boot_bleve_ready_wait_cancelled", "error", ctx.Err().Error())
	}
	return nil
}

func startLibrarianKnowledgeSync(phase1 *bootstrapPhase1, phase4 *bootstrapPhase4, initialSync bool) {
	if phase4 == nil || phase4.knowledgeSync != nil || phase1 == nil || phase1.knowledgeStore == nil || phase1.knowledgeBackend == nil {
		return
	}
	gitRes := phase1.gitSubsRef.Load()
	if gitRes == nil || gitRes.watcher == nil {
		slog.Warn("librarian knowledge sync unavailable", "reason", "git watcher unavailable")
		return
	}
	syncSvc, err := librarian.NewKnowledgeSyncService(librarian.KnowledgeSyncConfig{
		ProjectRoot: phase1.projectRoot,
		Watcher:     gitRes.watcher,
		Store:       phase1.knowledgeStore,
		Backend:     phase1.knowledgeBackend,
		Scope:       phase1.scope,
		Logger:      slog.Default(),
		InitialSync: initialSync,
	})
	if err != nil {
		slog.Warn("librarian knowledge sync setup failed", "error", err)
		return
	}
	if err := syncSvc.Start(); err != nil {
		slog.Warn("librarian knowledge sync start failed", "error", err)
		return
	}
	phase4.knowledgeSync = syncSvc
}

func schedulePhase4Activation(
	phase1 *bootstrapPhase1,
	phase3 bootstrapPhase3,
	handoffReady <-chan struct{},
	name string,
	timeout time.Duration,
	remaining *atomic.Int32,
	activationsRemaining *atomic.Int32,
	activationsDone chan struct{},
	onDone func(),
	logTiming bool,
	onReady func(context.Context) error,
) {
	activationsRemaining.Add(1)
	schedulePhase4Task(phase1.scope, "phase4-activate-"+name, timeout, remaining, onDone, func(bgCtx context.Context) error {
		defer func() {
			if activationsRemaining.Add(-1) == 0 {
				close(activationsDone)
			}
		}()

		if handoffReady != nil {
			select {
			case <-handoffReady:
			case <-bgCtx.Done():
				return nil
			}
		}

		start := time.Now()
		if logTiming {
			deadline, hasDeadline := bgCtx.Deadline()
			slog.Info("phase4: "+name+" activation start", "has_deadline", hasDeadline, "deadline", deadline)
		}
		if _, err := phase3.activationCtrl.EnsureActive(bgCtx, name); err != nil {
			attrs := []any{"error", err}
			if logTiming {
				attrs = append(attrs, "elapsed_ms", time.Since(start).Milliseconds())
			}
			slog.Warn("phase4: "+name+" activation failed", attrs...)
			return nil
		}
		if logTiming {
			slog.Info("phase4: "+name+" activation done", "elapsed_ms", time.Since(start).Milliseconds())
		}
		if bgCtx.Err() != nil || onReady == nil {
			return nil
		}
		return onReady(bgCtx)
	})
}

func registerPhase4Architect(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	arch, err := extractAgent[*architect.Architect](phase1.containerReg, "architect")
	if err != nil {
		return nil
	}
	_ = registerArchitectWithGuide(phase3.guide, arch)
	wireGlobalAgentPod(arch, "architect", phaseDefaultSessionID(phase1), phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Inspector(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	inspectorAgent, err := extractAgent[*inspectorGlobal.GlobalInspector](phase1.containerReg, "inspector")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, inspectorAgent, "inspector")
	wireGlobalAgentPod(inspectorAgent, "inspector", phaseDefaultSessionID(phase1), phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Tester(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	testerAgent, err := extractAgent[*globaltester.GlobalTester](phase1.containerReg, "tester")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, testerAgent, "tester")
	wireGlobalAgentPod(testerAgent, "tester", phaseDefaultSessionID(phase1), phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

// registerSessionAuditWiringHooks attaches per-session hooks on the
// orchestrator so every SessionVFS that opens gets a
// MergeAuditCoordinator + AuditRejectionSLATracker spun up against
// the registered global inspector + global tester spawners. The
// tear-down hook stops the wiring before the session's WALs close.
//
// Hook-time agent lookup: inspector / tester are looked up via the
// container registry each time a session opens. If either is not yet
// registered (phase-4 activation ordering), the wiring is skipped
// for that session and a warning logged — the session still runs,
// just without audit dispatch, which is the pre-existing fallback
// behavior.
func registerSessionAuditWiringHooks(phase1 *bootstrapPhase1, orch *orchestrator.Orchestrator) {
	registry := agentShared.NewSessionAuditWiringRegistry()

	orch.RegisterSessionOpenHook(func(svfs *versioning.SessionVFS) {
		inspectorAgent, inspectorErr := extractAgent[*inspectorGlobal.GlobalInspector](phase1.containerReg, "inspector")
		if inspectorErr != nil {
			slog.Warn("session audit wiring: inspector lookup failed",
				"session_id", string(svfs.SessionID()),
				"error", inspectorErr.Error(),
			)
			return
		}
		testerAgent, testerErr := extractAgent[*globaltester.GlobalTester](phase1.containerReg, "tester")
		if testerErr != nil {
			slog.Warn("session audit wiring: tester lookup failed",
				"session_id", string(svfs.SessionID()),
				"error", testerErr.Error(),
			)
			return
		}
		// Parent scope: use phase1.scope so the wiring's goroutines
		// are tracked under the top-level tui scope and torn down
		// with it. Without this, StartSessionAuditWiring would
		// create a standalone scope disconnected from cmd/tui.go's
		// shutdown lifecycle, leaking goroutines if the session
		// closes without firing the close hook (crash path).
		wiring, err := agentShared.StartSessionAuditWiring(context.Background(), agentShared.SessionAuditWiringConfig{
			Session:      svfs,
			Inspector:    inspectorAgent,
			Tester:       testerAgent,
			Bus:          phase1.guideBus,
			Logger:       slog.Default(),
			Scope:        phase1.scope,
			SLAThreshold: sessionAuditSLAThreshold,
		})
		if err != nil {
			slog.Warn("session audit wiring: start failed",
				"session_id", string(svfs.SessionID()),
				"error", err.Error(),
			)
			return
		}
		registry.Register(svfs.SessionID(), wiring)
		slog.Info("session audit wiring: started",
			"session_id", string(svfs.SessionID()),
		)
	})

	orch.RegisterSessionCloseHook(func(svfs *versioning.SessionVFS) {
		registry.RemoveAndStop(svfs.SessionID())
		slog.Info("session audit wiring: stopped",
			"session_id", string(svfs.SessionID()),
		)
	})

	// Issue #5 wiring: prime the new session's forest canopy with
	// the most-active branches from the most-recent prior session of
	// any agent type. Best-effort — a missing prior session
	// (errPrimingNoPriorSession) is expected on day-zero and logged
	// at debug, not warn. The call is synchronous because it's one
	// query + one tx (~ms) and shouldn't outrun session open.
	if phase1.forest != nil {
		orch.RegisterSessionOpenHook(func(svfs *versioning.SessionVFS) {
			sessionID := string(svfs.SessionID())
			result, err := phase1.forest.PrimeSession(context.Background(), forestsvc.SessionPrimingRequest{
				SessionID: sessionID,
			})
			if err != nil {
				slog.Debug("forest session priming skipped",
					"session_id", sessionID,
					"err", err.Error(),
				)
				return
			}
			if result.BranchesCopied > 0 {
				slog.Info("forest session primed",
					"session_id", sessionID,
					"source_session", result.SourceSessionID,
					"branches", result.BranchesCopied,
					"canopy_key", result.CanopyKey,
				)
			}
		})
	}

	// Stage 5 TUI state-resync consumer: subscribe to the session-
	// open resync topic so the chat / status surfaces can rebuild
	// their views after the session hooks have landed. The handler
	// is intentionally lightweight — it logs + lets downstream UI
	// components (that hook the bus topic directly if they want
	// richer projection) respond on their own cadence.
	if phase1.guideBus != nil {
		_, subErr := phase1.guideBus.SubscribeAsync(agentShared.SessionStateResyncTopic, func(msg *guide.Message) error {
			raw, ok := msg.Payload.([]byte)
			if !ok {
				return nil
			}
			var notif agentShared.SessionStateResyncNotification
			if err := json.Unmarshal(raw, &notif); err != nil {
				slog.Warn("tui: session state resync decode failed",
					"error", err.Error(),
				)
				return nil
			}
			slog.Info("tui: session state resync",
				"session_id", notif.SessionID,
				"green_version", notif.GreenVersion.String(),
				"commit_queue_depth", notif.CommitQueueDepth,
				"blocked_count", notif.BlockedCount,
				"auditing_count", notif.AuditingCount,
				"in_flight_replicas", notif.InFlightReplicas,
				"merge_descriptors", notif.MergeDescriptors,
				"elapsed_open_seconds", notif.ElapsedOpenSecs,
			)
			return nil
		})
		if subErr != nil {
			slog.Warn("tui: subscribe session state resync failed",
				"error", subErr.Error(),
			)
		}
	}
}

// sessionAuditSLAThreshold is the default rejection→remediation SLA
// window applied to every session. Beyond this without a remediation
// dispatched, the tracker emits an escalation event. Set to a
// practical default; can be exposed as a config knob in a later pass.
const sessionAuditSLAThreshold = 15 * time.Minute

func registerPhase4Librarian(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	lib, err := extractAgent[*librarian.Librarian](phase1.containerReg, "librarian")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, lib, "librarian")
	wireGlobalAgentPod(lib, "librarian", phaseDefaultSessionID(phase1), phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())

	// Attach the session VFS so the librarian can see the live
	// global overlay (and through the workspace-view layer, pipeline
	// overlays too). Without this the librarian's authority profile
	// (which permits disk / global / pipeline reads) is silently
	// reduced to disk-only by the SetSessionVFS default, and
	// workspace_read(op=list_changes) fails with "file access is
	// unavailable". The librarian is a singleton across sessions;
	// we bind it to this run's default session VFS at registration time.
	// Multi-session deployments that want per-session binding would
	// re-attach on session switch.
	if phase1.defaultSession != nil {
		if orch := phase1.orchRef.Load(); orch != nil {
			sessionID := phase1.defaultSession.ID()
			if svfs := orch.EnsureSessionVFS(sessionID, phase1.projectRoot); svfs != nil {
				lib.SetSessionVFS(svfs)
			}
		}
	}
	return nil
}

func registerPhase4Archivalist(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	archivalistAgent, err := extractAgent[*archivalist.Archivalist](phase1.containerReg, "archivalist")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, archivalistAgent, "archivalist")
	return nil
}

func registerPhase4Academic(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	academicAgent, err := extractAgent[*academic.Academic](phase1.containerReg, "academic")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, academicAgent, "academic")
	return nil
}

// buildIdentityFactory constructs the session-scoped identity.Factory.
// Called from phase1 (before any agent is created) so every agent
// constructor receives a non-nil Factory at Config time. The factory
// is session-scoped — one per session — aligned with the K8s
// namespace model (spec FIX_ID_AND_TOKENS.md).
func buildIdentityFactory(descriptors *handoff.DescriptorRegistry, sessionID string) (*identity.Factory, error) {
	ns := identity.Namespace(sessionID)
	models := identityregistries.NewHandoffModelRegistry(descriptors)
	pods := identityregistries.NewStaticPodRegistry(
		map[identity.PodID]identity.PodType{
			"guide":        identity.PodTypeDaemon,
			"orchestrator": identity.PodTypeDaemon,
			"guardian":     identity.PodTypeDaemon,
			"architect":    identity.PodTypeSingleton,
			"inspector":    identity.PodTypeSingleton,
			"tester":       identity.PodTypeSingleton,
			"librarian":    identity.PodTypeSingleton,
			"archivalist":  identity.PodTypeSingleton,
			"academic":     identity.PodTypeSingleton,
			"pipeline":     identity.PodTypePipeline,
		},
		"pipeline",
	)
	return identity.NewFactory(identity.FactoryConfig{
		Namespace: ns,
		Models:    models,
		Pods:      pods,
	})
}

// wireAccounting constructs the session-scoped accounting.Accountant,
// builds the MultiHook that fans every provider event to the
// accountant + activity publisher, and swaps the gateway hooks.
// Called from phase4 — accountant depends on the per-session WAL
// path and doesn't need to exist until after daemons are up.
func wireAccounting(phase1 *bootstrapPhase1, sessionID string) error {
	factory := phase1.identityFactory.Load()
	if factory == nil {
		return fmt.Errorf("identity factory not initialized")
	}
	ns := identity.Namespace(sessionID)

	walPath := filepath.Join(phase1.projectRoot, ".sylk", "sessions", sessionID, "accounting", "wal.jsonl")
	wal, err := accounting.OpenFileWAL(walPath)
	if err != nil {
		return fmt.Errorf("accounting wal: %w", err)
	}
	acc, err := accounting.New(accounting.Config{
		Namespace: ns,
		WAL:       wal,
		Logger:    slog.Default(),
	})
	if err != nil {
		_ = wal.Close()
		return fmt.Errorf("accountant: %w", err)
	}

	activityHook := providers.NewLLMEventPublisherHook(
		providers.NewLLMEventPublisher(phase1.activityPub),
	)
	accountantHook := accounting.NewHook(acc, slog.Default())
	multi := providers.NewMultiHook(accountantHook, activityHook)
	phase1.googleGateway.SetEventHook(multi)
	phase1.anthropicGateway.SetEventHook(multi)
	phase1.openaiGateway.SetEventHook(multi)

	phase1.accountant.Store(acc)
	phase1.llmEventHookRef.Store(multi)
	_ = factory // factory already stored at phase1
	return nil
}

func startBootstrapPhase4(
	phase1 *bootstrapPhase1,
	phase2 bootstrapPhase2,
	phase3 bootstrapPhase3,
) (*bootstrapPhase4, error) {
	phase4Start := time.Now()
	// The run default session + identity.Factory were created eagerly at
	// phase1 so daemon agents receive a non-nil Factory at New() time
	// (see buildBootstrapPhase1). phase4 just wires the Accountant
	// and the gateway MultiHook against the already-live factory.
	defaultSession := phase1.defaultSession
	if defaultSession == nil {
		return nil, fmt.Errorf("run default session not initialized at phase1")
	}
	if err := wireAccounting(phase1, defaultSession.ID()); err != nil {
		return nil, fmt.Errorf("accounting: %w", err)
	}
	phase3.orch.SignalReady()

	modelStore := agentpkg.NewAgentModelStore(filepath.Join(phase1.projectRoot, ".sylk", "config.yaml"))
	seeds := []ui.AgentSeed{
		{ID: "guide", AgentType: "guide", Name: "Guide"},
		{ID: "architect", AgentType: "architect", Name: "Architect"},
		{ID: "guardian", AgentType: "guardian", Name: "Guardian"},
		{ID: "inspector", AgentType: "inspector", Name: "Inspector"},
		{ID: "tester", AgentType: "tester", Name: "Tester"},
		{ID: "librarian", AgentType: "librarian", Name: "Librarian"},
		{ID: "archivalist", AgentType: "archivalist", Name: "Archivalist"},
		{ID: "academic", AgentType: "academic", Name: "Academic"},
	}
	populateSeedModels(seeds, phase1.containerReg)
	for i := range seeds {
		entry := modelStore.EntryFor(seeds[i].AgentType)
		seeds[i].PersistedModelID = resolvePersistedModelForCurrentAuth(entry.Model, entry.Provider, phase1.authRegistry)
		seeds[i].PersistedProviderID = entry.Provider
	}
	agentDefaults := make(map[string]agentpkg.AgentConfigEntry, len(seeds))
	for _, seed := range seeds {
		if dflt := agentpkg.DefaultModelForAgentType(seed.AgentType); dflt != "" {
			agentDefaults[seed.AgentType] = agentpkg.AgentConfigEntry{
				Provider: agentpkg.DeriveProvider(dflt),
				Model:    dflt,
			}
		}
	}
	if err := modelStore.EnsureDefaults(agentDefaults); err != nil {
		slog.Warn("agent model config: ensure defaults", "error", err)
	}

	phase4 := &bootstrapPhase4{
		seeds:              seeds,
		modelStore:         modelStore,
		modelSwapper:       buildModelSwapper(phase1.containerReg, phase3.activationCtrl, phase1.authRegistry, phase1.googleGateway, phase1.anthropicGateway, phase1.openaiGateway),
		phase4Done:         make(chan struct{}),
		knowledgeBootStart: make(chan struct{}),
	}
	if phase1.bootOps != nil {
		result, err := phase1.bootOps.CommitPhase4(phase1.ctx, boot.Phase4Status{
			Services: bootstrapInfrastructureServices(phase1, phase2, phase3),
			Context:  map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase4_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return nil, fmt.Errorf("claims operations phase 4: %w", err)
		}
		result, err = phase1.bootOps.CommitPhase5(phase1.ctx, boot.Phase5Status{
			Agents:  bootstrapAlwaysHotAgents(phase1, phase3),
			Context: map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase5_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return nil, fmt.Errorf("claims operations phase 5: %w", err)
		}
	}

	bootLogger, bootLogErr := agentlog.NewBootEventLogger(filepath.Join(phase1.projectRoot, ".sylk"))
	if bootLogErr != nil {
		slog.Warn("boot logger init failed (non-critical)", "error", bootLogErr)
	}
	phase4.bootLogger = bootLogger
	phase1.knowledgeStore.SetBootLogger(bootLogger)

	var (
		phase4Remaining      atomic.Int32
		activationsRemaining atomic.Int32
	)
	activationsDone := make(chan struct{})
	handoffReady := make(chan struct{})
	phase4Finish := func() {
		if phase4Remaining.Add(-1) > 0 {
			return
		}
		slog.Info("bootstrap phase 4 background complete", "elapsed", time.Since(phase4Start))
		close(phase4.phase4Done)
	}

	const phase4ActivationTimeout = 45 * time.Second
	if phase3.activationCtrl != nil {
		schedulePhase4Activation(phase1, phase3, handoffReady, "architect", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, true, func(context.Context) error {
			return registerPhase4Architect(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "inspector", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Inspector(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "tester", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Tester(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "librarian", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Librarian(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "archivalist", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Archivalist(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "academic", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Academic(phase1, phase3)
		})
	} else {
		close(activationsDone)
	}

	// Register per-session audit-loop wiring hooks on the orchestrator.
	// Each session's MergeAuditCoordinator + AuditRejectionSLATracker
	// are created here at session-open time and torn down at
	// session-close. The hooks look up inspector / tester agents
	// lazily at fire time so they don't depend on phase-4 ordering.
	// See docs/PARALLEL_GLOBAL_VFS.md §6.4.
	if orchRef := phase1.orchRef.Load(); orchRef != nil {
		registerSessionAuditWiringHooks(phase1, orchRef)
	}

	schedulePhase4Task(phase1.scope, "phase4-handoff-supervisor", 0, &phase4Remaining, phase4Finish, func(context.Context) error {
		sup := bootstrapHandoffSupervisor(
			phase3.guide,
			phase1.guideBus,
			phase1.serviceReg,
			phase1.containerReg,
			phase1.creatorReg,
			phase3.activationCtrl,
		)
		if sup != nil {
			phase4.supervisorRef.Store(sup)
			phase1.handoffRef.Store(sup)
			if phase3.activationCtrl != nil {
				phase3.activationCtrl.SetLifecycleCallbacks(
					func(c *container.Container) { registerHandoffContainer(sup, c) },
					func(c *container.Container) { unregisterHandoffContainer(sup, c) },
				)
			}
		}
		close(handoffReady)
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-auth-probe", 0, &phase4Remaining, phase4Finish, func(context.Context) error {
		phase1.authRegistry.ProbeAll()
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-boot-model-swap", phase4ActivationTimeout, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		select {
		case <-activationsDone:
		case <-bgCtx.Done():
			return nil
		}
		for _, seed := range phase4.seeds {
			if bgCtx.Err() != nil {
				return nil
			}
			dflt := agentpkg.DefaultModelForAgentType(seed.AgentType)
			persisted := phase4.modelStore.ModelFor(seed.AgentType)
			if persisted == "" || persisted == dflt {
				continue
			}
			if err := phase4.modelSwapper(bgCtx, seed.AgentType, persisted); err != nil {
				slog.Warn("boot model swap failed", "agent", seed.AgentType, "model", persisted, "error", err)
			} else {
				slog.Info("boot model swap applied", "agent", seed.AgentType, "model", persisted)
			}
		}
		return nil
	})
	// Knowledge boot emits user-visible progress through KnowledgeStore.
	// Keep these phase-4 workers registered for shutdown accounting, but
	// release them only after the TUI has attached its progress observer.
	schedulePhase4Task(phase1.scope, "phase4-knowledge-boot", 0, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		if !phase4.waitForKnowledgeBootStart(bgCtx) {
			return nil
		}
		startupTrace("knowledge_boot_pipeline_start")
		result, err := boot.BootWithConfig(bgCtx, boot.PipelineConfig{
			ProjectRoot: phase1.projectRoot,
			Logger:      phase4.bootLogger,
			OnProgress: func(phase string, current, total int64) {
				startupTrace("knowledge_boot_pipeline_progress", "phase", phase, "current", current, "total", total)
				phase1.progress.ReportKnowledgeProgress(phase, current, total)
				phase1.knowledgeStore.NotifyProgress(phase, current, total)
			},
			Scope: phase1.scope,
		})
		if err != nil {
			startupTrace("knowledge_boot_pipeline_error", "error", err.Error())
			phase1.progress.ReportError("knowledge boot", err)
			slog.Warn("knowledge boot failed (non-critical)", "error", err)
			startLibrarianKnowledgeSync(phase1, phase4, true)
			return nil
		}
		startupTrace("knowledge_boot_pipeline_done",
			"files_processed", result.FilesProcessed,
			"docs_created", result.DocsCreated,
			"vectors_created", result.VectorsCreated,
			"background_indexer", result.BackgroundIndexer != nil,
		)
		backend := phase1.knowledgeBackend
		if backend == nil {
			startupTrace("knowledge_boot_backend_missing")
			slog.Warn("knowledge backend unavailable (non-critical)")
			startLibrarianKnowledgeSync(phase1, phase4, false)
			return nil
		}
		var refreshErr error
		if bgIdx := result.BackgroundIndexer; bgIdx != nil && bgIdx.BleveStore() != nil {
			refreshErr = backend.RefreshWithBleveStore(bgCtx, bgIdx.BleveStore())
		} else {
			refreshErr = backend.RefreshFromDisk(bgCtx)
		}
		if refreshErr != nil {
			startupTrace("knowledge_boot_backend_refresh_error", "error", refreshErr.Error())
			slog.Warn("knowledge backend refresh failed (non-critical)", "error", refreshErr)
			startLibrarianKnowledgeSync(phase1, phase4, true)
			return nil
		}
		startupTrace("knowledge_boot_promote_partial_start")
		phase1.knowledgeStore.PromotePartial(query.NewBleveSearcher(backend), result.BackgroundIndexer, backend)
		startupTrace("knowledge_boot_promote_partial_done")
		startLibrarianKnowledgeSync(phase1, phase4, false)
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-bleve-ready", 0, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		if !phase4.waitForKnowledgeBootStart(bgCtx) {
			return nil
		}
		startupTrace("knowledge_boot_bleve_ready_wait_start")
		return waitForBootBleveReady(bgCtx, phase1.knowledgeStore)
	})

	return phase4, nil
}

func buildBootstrapCleanup(
	phase1 *bootstrapPhase1,
	phase3 bootstrapPhase3,
	phase4 *bootstrapPhase4,
) func() error {
	return func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()

		// Signal scope cancellation up front, on every scope sylk
		// owns: the TUI scope plus every container's own scope. Each
		// tracked worker — claims amplifier, phase4 background tasks,
		// activation helpers, in-container agent goroutines — sees
		// ctx.Done() at t=0 instead of only when its owner's Close is
		// called. By the time the parallel close phase runs, workers
		// have had the entire close-phase wall-time to wind down, so
		// Container.Stop's internal scope.Shutdown drains essentially
		// instantly instead of being the dominant cost.
		phase1.scope.SignalShutdown()
		for _, c := range phase1.containerReg.All() {
			c.SignalShutdown()
		}
		if phase1.bootDeltaUnsub != nil {
			phase1.bootDeltaUnsub()
			phase1.bootDeltaUnsub = nil
			startupTrace("claims_board_delta_watch_stopped")
		}

		select {
		case <-phase4.phase4Done:
		default:
		}

		var errs []error
		var errMu sync.Mutex
		addErr := func(err error) {
			if err == nil {
				return
			}
			errMu.Lock()
			errs = append(errs, err)
			errMu.Unlock()
		}

		// closeWG tracks fn-runner goroutines spawned by closeWithBudget.
		// dispatchWG tracks the outer parallel-dispatch wrappers.
		var closeWG sync.WaitGroup
		var dispatchWG sync.WaitGroup

		closeAsync := func(name string, fn func(context.Context) error) {
			dispatchWG.Add(1)
			go func() {
				defer dispatchWG.Done()
				addErr(closeWithBudget(shutdownCtx, &closeWG, name, fn))
			}()
		}

		closeAsyncBudget := func(name string, budget time.Duration, fn func(context.Context) error) {
			dispatchWG.Add(1)
			go func() {
				defer dispatchWG.Done()
				addErr(closeWithBudgetN(shutdownCtx, &closeWG, name, budget, fn))
			}()
		}

		// Phase A: supervisor first. Sequential because all subsequent
		// closes can run safely once the supervisor has signaled
		// agents to stop. Bounded by closeBudget like every other
		// close.
		if sup := phase4.supervisorRef.Load(); sup != nil {
			addErr(closeWithBudget(shutdownCtx, &closeWG, "supervisor", closeAware(sup.Stop)))
		}

		// Phase B: every remaining close runs in parallel — activation,
		// containers, all storage layers, gateways, bus. Cumulative
		// wait is max(closeBudget) instead of sum, keeping total
		// shutdown sub-second.
		if phase3.activationCtrl != nil {
			closeAsync("activation_ctrl", func(ctx context.Context) error {
				return phase3.activationCtrl.Shutdown(ctx)
			})
		}
		closeAsyncBudget("containers", closeBudgetContainers, func(ctx context.Context) error {
			return shutdownContainers(ctx, phase1.runtime, phase1.containerReg.All())
		})
		closeAsync("namespace", closeAwareVoid(phase1.namespace.Close))
		closeAsync("runtime", closeAwareVoid(phase1.runtime.Close))
		closeAsync("plan_store", closeAwareVoid(phase1.planStore.Close))
		if phase4.bootLogger != nil {
			closeAsync("boot_logger", closeAware(phase4.bootLogger.Close))
		}
		if phase4.knowledgeSync != nil {
			closeAsync("knowledge_sync", closeAware(phase4.knowledgeSync.Close))
		}
		closeAsync("knowledge_store", closeAware(phase1.knowledgeStore.Close))
		if phase1.forest != nil {
			// Forest is ctx-aware: bounded sub-ctx propagates all the
			// way to the wg.Wait inside the forest, so it returns at
			// the budget boundary instead of waiting on stuck workers.
			closeAsync("forest", phase1.forest.CloseWithContext)
		}
		if phase1.forestContent != nil {
			closeAsync("forest_content", closeAware(phase1.forestContent.Close))
		}
		if phase1.forestVectorDB != nil {
			closeAsync("forest_vector_db", closeAware(phase1.forestVectorDB.Close))
		}
		if phase1.knowledgeBackend != nil {
			closeAsync("knowledge_backend", closeAware(phase1.knowledgeBackend.Close))
		}
		closeAsync("google_gateway", closeAwareVoid(phase1.googleGateway.Stop))
		closeAsync("anthropic_gateway", closeAwareVoid(phase1.anthropicGateway.Stop))
		closeAsync("openai_gateway", closeAwareVoid(phase1.openaiGateway.Stop))
		closeAsync("guide_bus", phase1.guideBus.CloseWithContext)

		// Wait for all parallel dispatch wrappers (each ≤ closeBudget).
		// dispatchWG.Wait completes in ~closeBudget regardless of
		// how many closes are stuck.
		dispatchWG.Wait()
		drainCloseGoroutines(&closeWG)
		// Drain the scope LAST. Every long-running worker registered with
		// phase1.scope (librarian-knowledge-sync, phase4 background tasks,
		// orchestrator/architect helpers) must be cancellable via the
		// scope's parent ctx. Calling Shutdown here — after each owner's
		// Close has fired — gives those workers a clean handoff: their
		// owners' Close cancels their local ctx, scope.Shutdown's parent
		// cancel reinforces it, and the wg.Wait drains in-flight work
		// within the grace window. Doing it earlier (e.g. inside the UI's
		// Shutdown) would race the per-component Close calls and cause
		// the scope to report leaks that aren't actually leaks — just
		// not-yet-cancelled workers waiting on their owner's Close.
		if err := phase1.scope.Shutdown(shutdownGrace, shutdownHard); err != nil {
			// Goroutine leaks at scope drain are diagnostic, not fatal.
			// A leaked worker is one that was tracked correctly (the
			// "no untracked goroutines" invariant is upheld) but didn't
			// honor ctx cancellation within the scope's hard deadline —
			// typically a worker stuck in a non-ctx-aware blocking call
			// (LLM provider request, external SDK syscall). The
			// log line names every leaked worker so the underlying
			// blocking call can be located, while runtime exit reaps
			// the leftover goroutines. Failing the cleanup function
			// here would surface an exit-status-1 error for what is
			// actually a graceful-degradation case.
			var leakErr *concurrency.GoroutineLeakError
			if errors.As(err, &leakErr) {
				slog.Warn("shutdown: tracked goroutines did not drain within scope deadline; relying on process exit to reclaim",
					"leaked_count", leakErr.LeakedCount,
					"workers", leakErr.Workers,
				)
			} else {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
}

func buildBootstrapDeps(
	phase1 *bootstrapPhase1,
	phase2 bootstrapPhase2,
	phase3 bootstrapPhase3,
	phase4 *bootstrapPhase4,
) (ui.Deps, error) {
	deps := ui.Deps{
		ActivityPub:        phase1.activityPub,
		SessionManager:     phase1.sessionMgr,
		GuideBus:           phase1.guideBus,
		StreamManager:      phase1.streamMgr,
		Guide:              phase3.guide,
		Scope:              phase1.scope,
		AuthRegistry:       phase1.authRegistry,
		Accountant:         phase1.accountant.Load(),
		InterruptAllAgents: makeInterruptAllAgentsFn(phase3.activationCtrl, phase1.guideBus),
		NerdFontsDetected:  phase2.fontRes.detected,
		GitClient:          phase2.gitRes.client,
		GitWatcher:         phase2.gitRes.watcher,
		GitBus:             phase2.gitRes.bus,
		SafetyGuard:        phase2.gitRes.guard,
		SeedAgents:         phase4.seeds,
		ModelSwap:          phase4.modelSwapper,
		ModelSave: func(agentType, provider, modelID string) {
			if err := phase4.modelStore.SetEntry(agentType, provider, modelID); err != nil {
				slog.Warn("agent model config: save", "agent", agentType, "provider", provider, "model", modelID, "error", err)
			}
		},
		AgentModelStore: phase4.modelStore,
		KnowledgeStore:  phase1.knowledgeStore,
		StartKnowledgeBoot: func() {
			startupTrace("start_knowledge_boot_release")
			phase4.StartKnowledgeBoot()
		},
		Forest: phase1.forest,
	}
	if phase1.bootOps != nil {
		result, err := phase1.bootOps.CommitPhase6(phase1.ctx, boot.Phase6Status{
			Surfaces: bootstrapUserFacingSurfaces(phase1, phase3),
			Context:  map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase6_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return ui.Deps{}, fmt.Errorf("claims operations phase 6: %w", err)
		}
		result, err = phase1.bootOps.CommitPhase7(phase1.ctx, boot.Phase7Status{
			Context: map[string]any{"session_id": phaseDefaultSessionID(phase1)},
		})
		traceBootPhaseCommit("claims_operations_phase7_commit", result, err, phase1.bootOps)
		reportBootstrapClaimsHealth(phase1)
		if err != nil {
			return ui.Deps{}, fmt.Errorf("claims operations phase 7: %w", err)
		}
		if err := startBootstrapClaimsOperationsAudits(phase1); err != nil {
			startupTrace("claims_operations_audits_error", "error", err.Error())
			return ui.Deps{}, fmt.Errorf("claims operations audits: %w", err)
		}
		startupTrace("claims_operations_audits_started")
	}
	return deps, nil
}

func makeInterruptAllAgentsFn(
	activationCtrl *activation.ActivationController,
	bus guide.EventBus,
) func(sessionID, reason string) error {
	if activationCtrl == nil && bus == nil {
		return nil
	}
	return func(sessionID, reason string) error {
		var errs []error
		reason = strings.TrimSpace(reason)
		if activationCtrl != nil && bus != nil {
			actionData := map[string]any{
				"session_id": sessionID,
				"scope":      "session",
			}
			if reason != "" {
				actionData["reason"] = reason
			}
			for _, agentType := range activationCtrl.ActiveAgentTypes() {
				if strings.TrimSpace(agentType) == "" || agentType == "guide" {
					continue
				}
				action := &guide.ActionRequest{
					SourceAgentID: "tui",
					TargetAgentID: agentType,
					Action:        "cancel",
					Data:          actionData,
					FireAndForget: true,
					Timestamp:     time.Now(),
				}
				if err := bus.Publish(
					guide.TopicRequests(agentType, agentType),
					guide.NewActionMessage("", action),
				); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if bus != nil && strings.TrimSpace(sessionID) != "" {
			interruptReq := &guide.UserInterruptRequest{
				SessionID:     sessionID,
				SourceAgentID: "tui",
				Scope:         guide.UserInterruptScopeSession,
				Reason:        reason,
				Timestamp:     time.Now(),
			}
			if err := bus.Publish(
				guide.TopicGuideRequests,
				guide.NewUserInterruptMessage("", interruptReq),
			); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
}

// =============================================================================
// Agent Creator Registration
// =============================================================================

// registerAgentCreators populates the creator registry with factory closures
// for every agent type. Guide and Orchestrator block on hydrateOnce.result()
// during initial boot so all share a single hydration. After boot, daemon
// restart factories read hydratedRef atomically (already populated).
func registerAgentCreators(
	reg *container.AgentCreatorRegistry,
	ids *container.AgentIdentityRegistry,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	hydrateOnce *hydrateOnceCell,
	hydratedRef *atomic.Pointer[providers.HydratedGoogleAuth],
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	gitSubsRef *atomic.Pointer[gitBootResult],
	planStore *architect.PlanStore,
	knowledgeStore *knowledge.KnowledgeStore,
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
	forest agentShared.MemoryForestService,
	daemonCtrl *daemon.DaemonSetController,
	guideRef *atomic.Pointer[guide.Guide],
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	quota *container.ResourceQuota,
	budget *concurrency.GoroutineBudget,
	factoryRef *atomic.Pointer[identity.Factory],
	defaultSessionID string,
	scope *concurrency.GoroutineScope,
) {
	// Guide — Gemini with rule-based fallback.
	// First call blocks on hydrateOnce; subsequent calls (daemon restart)
	// read hydratedRef which is already populated.
	reg.Register("guide", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapLiveGuide(ctx, bus, actPub, h, googleGw, authRegistry, projectRoot, defaultSessionID, forest, scope, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef.Load())
	})

	// Architect — Anthropic LLM planner. Reads through session-scoped global
	// overlay when a session is active, falling back to read-only disk access.
	architectID, _ := ids.Get("architect")
	reg.Register("architect", func(ctx context.Context) (container.ContainerAgent, error) {
		return bootstrapArchitect(ctx, architectID, defaultSessionID, bus, actPub, projectRoot, anthropicGw, authRegistry, actCtrlRef, planStore, forest, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef, scope)
	})

	// Orchestrator — pipeline coordinator.
	orchestratorID, _ := ids.Get("orchestrator")
	reg.Register("orchestrator", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapOrchestrator(ctx, orchestratorID, defaultSessionID, bus, actPub, projectRoot, h, authRegistry, googleGw, forest, factoryRef.Load(), scope)
	})

	// Guardian — safety sidecar daemon.
	// On daemon restart the factory must re-wire post-boot deps (observability,
	// git, agent seeding) that Phase 3 originally set up.
	reg.Register("guardian", func(ctx context.Context) (container.ContainerAgent, error) {
		grd, err := bootstrapGuardian(ctx, bus, actPub, projectRoot, googleGw, anthropicGw, openaiGw, authRegistry, gitSubsRef, quarantineRef, forest, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef.Load(), scope)
		if err != nil {
			return nil, err
		}
		guardianRef.Store(grd)
		wireGuardianPostBootDeps(grd, actCtrlRef, daemonCtrl, guideRef, orchRef, authRegistry, openaiGw,
			[]*gateway.ProviderGateway{googleGw, anthropicGw, openaiGw}, knowledgeStore, budget)
		return grd, nil
	})

	// On-demand agents — created lazily by the ActivationController.
	registerOnDemandAgentCreators(reg, ids, bus, actPub, projectRoot, defaultSessionID, googleGw, anthropicGw, openaiGw, authRegistry, actCtrlRef, knowledgeStore, knowledgeBackend, forest, guardianRef, orchRef, quarantineRef, quota, factoryRef, scope)
}

// registerOnDemandAgentCreators registers factories for knowledge and pipeline agents.
// These are created lazily by the ActivationController when the Guide routes to them.
type onDemandAgentCreatorDeps struct {
	reg               *container.AgentCreatorRegistry
	ids               *container.AgentIdentityRegistry
	bus               guide.EventBus
	actPub            events.ActivityPublisher
	projectRoot       string
	fallbackSessionID string
	googleGw          *gateway.ProviderGateway
	anthropicGw       *gateway.ProviderGateway
	openaiGw          *gateway.ProviderGateway
	authRegistry      *credentials.AuthRegistry
	actCtrlRef        *atomic.Pointer[activation.ActivationController]
	knowledgeStore    *knowledge.KnowledgeStore
	knowledgeBackend  *knowledgeruntime.CommittedKnowledgeBackend
	forest            agentShared.MemoryForestService
	guardianRef       *atomic.Pointer[guardian.Guardian]
	orchRef           *atomic.Pointer[orchestrator.Orchestrator]
	quarantineRef     *atomic.Pointer[fetch.QuarantineBuffer]
	quota             *container.ResourceQuota
	factoryRef        *atomic.Pointer[identity.Factory]
	// scope is the top-level tui scope. Every on-demand agent's
	// SetScope is called with this before Start so claims-intake
	// OnResolved dispatches and accumulator flushes go through a
	// real tracked scope rather than falling into the sync fallback.
	// Without this, claims_intake_wiring logs scope_present=false
	// and every async amplifier emission / accumulator flush is
	// dropped.
	scope *concurrency.GoroutineScope
	// providerPool memoizes createSwapProvider results across agent
	// creator calls so pipeline agent re-creation (engineer/designer/
	// inspector-pipeline/tester-pipeline) doesn't repay provider
	// construction cost. Plan-independent — survives plan revisions.
	providerPool *providerWarmPool
}

// swapProvider returns a wrapped provider, preferring the warm pool
// when available. Falls back to direct createSwapProvider for any
// caller that hasn't been migrated.
func (d onDemandAgentCreatorDeps) swapProvider(
	ctx context.Context,
	model string,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	if d.providerPool != nil {
		return d.providerPool.Get(ctx, model, d.authRegistry, d.googleGw, d.anthropicGw, d.openaiGw, priority)
	}
	return createSwapProvider(ctx, model, d.authRegistry, d.googleGw, d.anthropicGw, d.openaiGw, priority)
}

// factory returns the session-scoped identity.Factory if it has been
// wired by wireIdentityAccounting; otherwise nil. On-demand agents
// created after phase4 should always see a non-nil value.
func (d onDemandAgentCreatorDeps) factory() *identity.Factory {
	if d.factoryRef == nil {
		return nil
	}
	return d.factoryRef.Load()
}

func (d onDemandAgentCreatorDeps) configuredModel(agentType, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	store := agentpkg.NewAgentModelStore(filepath.Join(d.projectRoot, ".sylk", "config.yaml"))
	entry := store.EntryFor(agentType)
	if entry.Model == "" {
		return effectiveModelForCurrentAuth(d.authRegistry, fallback)
	}
	return resolvePersistedModelForCurrentAuth(entry.Model, entry.Provider, d.authRegistry)
}

func (d onDemandAgentCreatorDeps) sessionLookup(sessionID string) *versioning.SessionVFS {
	if orch := d.orchRef.Load(); orch != nil {
		return orch.GetSessionVFS(sessionID)
	}
	return nil
}

func (d onDemandAgentCreatorDeps) defaultSessionID() string {
	if d.orchRef != nil {
		if orch := d.orchRef.Load(); orch != nil {
			return orch.SessionID()
		}
	}
	return bootstrapSessionID(d.fallbackSessionID)
}

func (d onDemandAgentCreatorDeps) workspaceViews(defaultView versioning.WorkspaceView) *versioning.SessionWorkspaceViews {
	return versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      defaultView,
		DefaultSessionID: d.defaultSessionID(),
		WorkingDir:       d.projectRoot,
		SessionLookup:    d.sessionLookup,
		DiskFallback:     versioning.NewDiskFileAccess(d.projectRoot, true),
	})
}

func (d onDemandAgentCreatorDeps) requestGuard(agentName string) func() func() {
	if d.actCtrlRef == nil {
		return nil
	}
	if ac := d.actCtrlRef.Load(); ac != nil {
		return func() func() {
			return ac.AcquireRequestGuard(agentName)
		}
	}
	return nil
}

func registerOnDemandAgentCreators(
	reg *container.AgentCreatorRegistry,
	ids *container.AgentIdentityRegistry,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	defaultSessionID string,
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	knowledgeStore *knowledge.KnowledgeStore,
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
	forest agentShared.MemoryForestService,
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	quota *container.ResourceQuota,
	factoryRef *atomic.Pointer[identity.Factory],
	scope *concurrency.GoroutineScope,
) {
	deps := onDemandAgentCreatorDeps{
		reg:               reg,
		ids:               ids,
		bus:               bus,
		actPub:            actPub,
		projectRoot:       projectRoot,
		fallbackSessionID: defaultSessionID,
		googleGw:          googleGw,
		anthropicGw:       anthropicGw,
		openaiGw:          openaiGw,
		authRegistry:      authRegistry,
		actCtrlRef:        actCtrlRef,
		knowledgeStore:    knowledgeStore,
		knowledgeBackend:  knowledgeBackend,
		forest:            forest,
		guardianRef:       guardianRef,
		orchRef:           orchRef,
		quarantineRef:     quarantineRef,
		quota:             quota,
		providerPool:      newProviderWarmPool(),
		factoryRef:        factoryRef,
		scope:             scope,
	}
	registerOnDemandKnowledgeAgentCreators(deps)
	registerOnDemandQualityAgentCreators(deps)
	registerOnDemandImplementationAgentCreators(deps)
}

func registerOnDemandKnowledgeAgentCreators(deps onDemandAgentCreatorDeps) {
	registerLibrarianAgentCreator(deps)
	registerArchivalistAgentCreator(deps)
	registerAcademicAgentCreator(deps)
}

func registerLibrarianAgentCreator(deps onDemandAgentCreatorDeps) {
	librarianID, _ := deps.ids.Get("librarian")
	deps.reg.Register("librarian", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("librarian", librarian.DefaultLibrarianModel)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("librarian provider: %w", err)
		}

		libCfg := librarian.Config{
			ID:               librarianID,
			SessionID:        deps.defaultSessionID(),
			EnableLLM:        true,
			Model:            model,
			AnthropicAPIKey:  providers.ResolveAnthropicAPIKey(""),
			ActivityPub:      deps.actPub,
			WorkingDirectory: deps.projectRoot,
			SearchSystem:     librarian.NewCommittedKnowledgeSearchSystem(deps.knowledgeBackend),
			KnowledgeBackend: deps.knowledgeBackend,
			ContextQuota:     deps.quota,
			Forest:           deps.forest,
			Factory:          deps.factory(),
		}
		if guard := deps.requestGuard("librarian"); guard != nil {
			libCfg.RequestGuard = guard
		}
		l, err := librarian.New(libCfg, wrapped)
		if err != nil {
			return nil, err
		}
		l.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		l.SetKnowledgeStore(deps.knowledgeStore)
		l.SetScope(deps.scope)

		if startErr := l.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return l, nil
	})
}

func registerArchivalistAgentCreator(deps onDemandAgentCreatorDeps) {
	archivalistID, _ := deps.ids.Get("archivalist")
	deps.reg.Register("archivalist", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("archivalist", archivalist.ModelSonnet45)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("archivalist provider: %w", err)
		}

		archCfg := buildOnDemandArchivalistConfig(deps, archivalistID, model)
		a, err := archivalist.New(ctx, archCfg)
		if err != nil {
			return nil, err
		}
		a.SetProvider(wrapped)
		a.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		a.SetKnowledgeStore(deps.knowledgeStore)
		a.SetKnowledgeBackend(deps.knowledgeBackend)
		a.SetScope(deps.scope)
		if startErr := a.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})
}

func buildOnDemandArchivalistConfig(deps onDemandAgentCreatorDeps, agentID, model string) archivalist.Config {
	archCfg := archivalist.Config{
		ID:                   agentID,
		Model:                model,
		ActivityPub:          deps.actPub,
		EnableLLM:            true,
		EnableArchive:        true,
		EnableRAG:            true,
		EnableKnowledgeGraph: true,
		EnableHybridQuery:    true,
		EnableACTR:           true,
		ContextQuota:         deps.quota,
		Forest:               deps.forest,
		Factory:              deps.factory(),
	}
	if guard := deps.requestGuard("archivalist"); guard != nil {
		archCfg.RequestGuard = guard
	}
	return archCfg
}

func registerAcademicAgentCreator(deps onDemandAgentCreatorDeps) {
	academicID, _ := deps.ids.Get("academic")
	deps.reg.Register("academic", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("academic", academic.DefaultModel)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("academic provider: %w", err)
		}
		acaCfg := academic.Config{
			ID:           academicID,
			SessionID:    deps.defaultSessionID(),
			Model:        model,
			ActivityPub:  deps.actPub,
			ContextQuota: deps.quota,
			Forest:       deps.forest,
			Factory:      deps.factory(),
		}
		if guard := deps.requestGuard("academic"); guard != nil {
			acaCfg.RequestGuard = guard
		}
		a, err := academic.New(acaCfg, wrapped)
		if err != nil {
			return nil, err
		}
		a.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		a.SetKnowledgeStore(deps.knowledgeStore)
		a.SetKnowledgeBackend(deps.knowledgeBackend)
		a.SetFetchPipeline(buildAcademicFetchPipeline(deps.projectRoot, deps.guardianRef, deps.quarantineRef, deps.knowledgeBackend))
		a.SetScope(deps.scope)
		if startErr := a.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})
}

func registerOnDemandQualityAgentCreators(deps onDemandAgentCreatorDeps) {
	registerGlobalInspectorAgentCreator(deps)
	registerPipelineInspectorAgentCreator(deps)
	registerGlobalTesterAgentCreator(deps)
	registerPipelineTesterAgentCreator(deps)
}

func registerGlobalInspectorAgentCreator(deps onDemandAgentCreatorDeps) {
	inspectorID, _ := deps.ids.Get("inspector")
	deps.reg.Register("inspector", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("inspector", "claude-opus-4-6")
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("global inspector provider: %w", err)
		}
		gi, err := inspectorGlobal.New(inspectorShared.GlobalInspectorConfig{
			AgentID:     inspectorID,
			SessionID:   deps.defaultSessionID(),
			Model:       model,
			ActivityPub: deps.actPub,
			Forest:      deps.forest,
			Factory:     deps.factory(),
		}, wrapped)
		if err != nil {
			return nil, err
		}
		gi.SetFileAccess(versioning.NewSessionRoutingFileAccess(
			false,
			deps.sessionLookup,
			versioning.NewDiskFileAccess(deps.projectRoot, false),
		))
		gi.SetWorkspaceViews(deps.workspaceViews(versioning.WorkspaceViewGlobal))
		gi.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation))
		gi.SetScope(deps.scope)
		if startErr := gi.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return gi, nil
	})
}

func registerPipelineInspectorAgentCreator(deps onDemandAgentCreatorDeps) {
	deps.reg.Register("inspector-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		creatorStart := time.Now()
		agentID := pipelineWorkerAgentID(ctx, "inspector-pipeline")
		model := deps.configuredModel("inspector-pipeline", "claude-opus-4-6")
		slog.Info("pipeline_creator_enter", "agent_type", "inspector-pipeline", "agent_id", agentID, "model", model)
		providerStart := time.Now()
		wrapped, err := deps.swapProvider(ctx, model, gateway.PriorityValidation)
		if err != nil {
			slog.Error("pipeline_creator_provider_failed", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds(), "error", err.Error())
			return nil, fmt.Errorf("pipeline inspector provider: %w", err)
		}
		slog.Info("pipeline_creator_provider_ok", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds())
		newStart := time.Now()
		pi, err := inspectorPipeline.New(inspectorShared.PipelineInspectorConfig{AgentID: agentID, Forest: deps.forest, Factory: deps.factory()}, wrapped)
		if err != nil {
			slog.Error("pipeline_creator_new_failed", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err.Error())
			return nil, err
		}
		slog.Info("pipeline_creator_new_ok", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds())
		pi.SetActivityPublisher(deps.actPub)
		if board := claims.DefaultSessionBoardRegistry().Lookup(deps.defaultSessionID()); board != nil {
			pi.SetClaimsBoard(board)
			slog.Info("pipeline_creator_board_attached", "agent_type", "inspector-pipeline", "agent_id", agentID, "session_id", deps.defaultSessionID(), "board_id", board.BoardID())
		} else {
			slog.Warn("pipeline_creator_board_missing", "agent_type", "inspector-pipeline", "agent_id", agentID, "session_id", deps.defaultSessionID(), "reason", "session board lookup returned nil; inbox will resolve deltas with empty Node")
		}
		pi.SetPipelineCommitter(agentShared.NewSessionVFSPipelineCommitter(func(sessionID string) agentShared.SessionVFSPipelineCommitterBackend {
			return deps.sessionLookup(sessionID)
		}))
		pi.SetScope(deps.scope)
		startStart := time.Now()
		if startErr := pi.Start(deps.bus); startErr != nil {
			slog.Error("pipeline_creator_start_failed", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "error", startErr.Error())
			return nil, startErr
		}
		slog.Info("pipeline_creator_start_ok", "agent_type", "inspector-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "total_ms", time.Since(creatorStart).Milliseconds())
		return pi, nil
	})
}

func registerGlobalTesterAgentCreator(deps onDemandAgentCreatorDeps) {
	testerID, _ := deps.ids.Get("tester")
	deps.reg.Register("tester", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("tester", "gpt-5.4-pro")
		var wrapped providers.Provider
		if provider, provErr := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation); provErr != nil {
			slog.Warn("tester: provider unavailable, LLM features disabled", "error", provErr)
		} else {
			wrapped = provider
		}
		gt, err := globaltester.New(shared.GlobalTesterConfig{
			AgentID:     testerID,
			SessionID:   deps.defaultSessionID(),
			Model:       model,
			ActivityPub: deps.actPub,
			Forest:      deps.forest,
			Factory:     deps.factory(),
		}, wrapped)
		if err != nil {
			return nil, err
		}
		gt.SetFileAccess(versioning.NewSessionRoutingFileAccess(
			false,
			deps.sessionLookup,
			versioning.NewDiskFileAccess(deps.projectRoot, false),
		))
		gt.SetWorkspaceViews(deps.workspaceViews(versioning.WorkspaceViewGlobal))
		gt.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation))
		gt.SetScope(deps.scope)
		if startErr := gt.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return gt, nil
	})
}

func registerPipelineTesterAgentCreator(deps onDemandAgentCreatorDeps) {
	deps.reg.Register("tester-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		creatorStart := time.Now()
		agentID := pipelineWorkerAgentID(ctx, "tester-pipeline")
		model := deps.configuredModel("tester-pipeline", "gpt-5.4-pro")
		slog.Info("pipeline_creator_enter", "agent_type", "tester-pipeline", "agent_id", agentID, "model", model)
		providerStart := time.Now()
		wrapped, err := deps.swapProvider(ctx, model, gateway.PriorityValidation)
		if err != nil {
			slog.Error("pipeline_creator_provider_failed", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds(), "error", err.Error())
			return nil, fmt.Errorf("pipeline tester provider: %w", err)
		}
		slog.Info("pipeline_creator_provider_ok", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds())
		newStart := time.Now()
		pt, err := pipelinetester.New(shared.PipelineTesterConfig{AgentID: agentID, Model: model, Forest: deps.forest, Factory: deps.factory()}, wrapped)
		if err != nil {
			slog.Error("pipeline_creator_new_failed", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err.Error())
			return nil, err
		}
		slog.Info("pipeline_creator_new_ok", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds())
		pt.SetActivityPublisher(deps.actPub)
		if board := claims.DefaultSessionBoardRegistry().Lookup(deps.defaultSessionID()); board != nil {
			pt.SetClaimsBoard(board)
			slog.Info("pipeline_creator_board_attached", "agent_type", "tester-pipeline", "agent_id", agentID, "session_id", deps.defaultSessionID(), "board_id", board.BoardID())
		} else {
			slog.Warn("pipeline_creator_board_missing", "agent_type", "tester-pipeline", "agent_id", agentID, "session_id", deps.defaultSessionID(), "reason", "session board lookup returned nil; inbox will resolve deltas with empty Node")
		}
		pt.SetScope(deps.scope)
		startStart := time.Now()
		if startErr := pt.Start(deps.bus); startErr != nil {
			slog.Error("pipeline_creator_start_failed", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "error", startErr.Error())
			return nil, startErr
		}
		slog.Info("pipeline_creator_start_ok", "agent_type", "tester-pipeline", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "total_ms", time.Since(creatorStart).Milliseconds())
		return pt, nil
	})
}

func registerOnDemandImplementationAgentCreators(deps onDemandAgentCreatorDeps) {
	registerEngineerAgentCreator(deps)
	registerDesignerAgentCreator(deps)
}

func registerEngineerAgentCreator(deps onDemandAgentCreatorDeps) {
	engineerID, _ := deps.ids.Get("engineer")
	deps.reg.Register("engineer", func(ctx context.Context) (container.ContainerAgent, error) {
		creatorStart := time.Now()
		agentID := taskScopedCreationAgentID(ctx, "engineer", engineerID)
		model := deps.configuredModel("engineer", "gpt-5.4-pro")
		slog.Info("pipeline_creator_enter", "agent_type", "engineer", "agent_id", agentID, "model", model)
		providerStart := time.Now()
		wrapped, err := deps.swapProvider(ctx, model, gateway.PriorityExecution)
		if err != nil {
			slog.Error("pipeline_creator_provider_failed", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds(), "error", err.Error())
			return nil, fmt.Errorf("engineer provider: %w", err)
		}
		slog.Info("pipeline_creator_provider_ok", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds())
		engCfg := engineer.Config{ID: agentID, ActivityPub: deps.actPub, Forest: deps.forest, Factory: deps.factory()}
		engCfg.EngineerConfig.Model = model
		if guard := deps.requestGuard("engineer"); guard != nil {
			engCfg.RequestGuard = guard
		}
		newStart := time.Now()
		e, err := engineer.New(engCfg, wrapped)
		if err != nil {
			slog.Error("pipeline_creator_new_failed", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err.Error())
			return nil, err
		}
		slog.Info("pipeline_creator_new_ok", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds())
		e.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		if board := claims.DefaultSessionBoardRegistry().Lookup(deps.defaultSessionID()); board != nil {
			e.SetClaimsBoard(board)
			slog.Info("pipeline_creator_board_attached", "agent_type", "engineer", "agent_id", agentID, "session_id", deps.defaultSessionID(), "board_id", board.BoardID())
		} else {
			slog.Warn("pipeline_creator_board_missing", "agent_type", "engineer", "agent_id", agentID, "session_id", deps.defaultSessionID(), "reason", "session board lookup returned nil; inbox will resolve deltas with empty Node")
		}
		e.SetScope(deps.scope)
		startStart := time.Now()
		if startErr := e.Start(deps.bus); startErr != nil {
			slog.Error("pipeline_creator_start_failed", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "error", startErr.Error())
			return nil, startErr
		}
		slog.Info("pipeline_creator_start_ok", "agent_type", "engineer", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "total_ms", time.Since(creatorStart).Milliseconds())
		return e, nil
	})
}

func registerDesignerAgentCreator(deps onDemandAgentCreatorDeps) {
	designerID, _ := deps.ids.Get("designer")
	deps.reg.Register("designer", func(ctx context.Context) (container.ContainerAgent, error) {
		creatorStart := time.Now()
		agentID := taskScopedCreationAgentID(ctx, "designer", designerID)
		model := deps.configuredModel("designer", string(providers.Gemini31Pro))
		slog.Info("pipeline_creator_enter", "agent_type", "designer", "agent_id", agentID, "model", model)
		providerStart := time.Now()
		wrapped, err := deps.swapProvider(ctx, model, gateway.PriorityExecution)
		if err != nil {
			slog.Error("pipeline_creator_provider_failed", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds(), "error", err.Error())
			return nil, fmt.Errorf("designer provider: %w", err)
		}
		slog.Info("pipeline_creator_provider_ok", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(providerStart).Milliseconds())
		desCfg := designer.Config{ID: agentID, ActivityPub: deps.actPub, Forest: deps.forest, Factory: deps.factory()}
		if guard := deps.requestGuard("designer"); guard != nil {
			desCfg.RequestGuard = guard
		}
		newStart := time.Now()
		d, err := designer.New(desCfg, wrapped)
		if err != nil {
			slog.Error("pipeline_creator_new_failed", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err.Error())
			return nil, err
		}
		slog.Info("pipeline_creator_new_ok", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(newStart).Milliseconds())
		d.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		if board := claims.DefaultSessionBoardRegistry().Lookup(deps.defaultSessionID()); board != nil {
			d.SetClaimsBoard(board)
			slog.Info("pipeline_creator_board_attached", "agent_type", "designer", "agent_id", agentID, "session_id", deps.defaultSessionID(), "board_id", board.BoardID())
		} else {
			slog.Warn("pipeline_creator_board_missing", "agent_type", "designer", "agent_id", agentID, "session_id", deps.defaultSessionID(), "reason", "session board lookup returned nil; inbox will resolve deltas with empty Node")
		}
		d.SetScope(deps.scope)
		startStart := time.Now()
		if startErr := d.Start(deps.bus); startErr != nil {
			slog.Error("pipeline_creator_start_failed", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "error", startErr.Error())
			return nil, startErr
		}
		slog.Info("pipeline_creator_start_ok", "agent_type", "designer", "agent_id", agentID, "elapsed_ms", time.Since(startStart).Milliseconds(), "total_ms", time.Since(creatorStart).Milliseconds())
		return d, nil
	})
}

// =============================================================================
// Probe Factory Holder
// =============================================================================

// probesPerContainer is the number of probes wired per container. Used by
// quota derivation in place of len(spec.Probes) because probes are attached
// by the ProbeFactory after spec mutation (not present at quota time).
const probesPerContainer int64 = 2

// probeFactoryHolder builds probe specs for containers. The Guide's
// IsAgentReady callback is set after the Guide container is created,
// breaking the circular dependency between probe construction and Guide.
type probeFactoryHolder struct {
	mu      sync.Mutex
	isReady func(string) bool
}

// SetIsReady wires the Guide's readiness callback. Thread-safe.
func (pf *probeFactoryHolder) SetIsReady(fn func(string) bool) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	pf.isReady = fn
}

// Build constructs liveness and readiness probe specs for a container.
// Always produces a liveness probe. Produces a readiness probe only when
// the Guide's IsAgentReady callback has been wired.
func (pf *probeFactoryHolder) Build(agent container.ContainerAgent, spec *container.ContainerSpec) []container.ProbeSpec {
	probes := make([]container.ProbeSpec, 0, 2)
	probes = append(probes, container.LivenessProbeSpec(agent, spec.GracefulStop))

	pf.mu.Lock()
	isReady := pf.isReady
	pf.mu.Unlock()

	if isReady != nil {
		probes = append(probes, container.ReadinessProbeSpec(agent.AgentID(), isReady))
	}
	return probes
}

// =============================================================================
// Lifecycle Hook Mutator
// =============================================================================

// lifecycleHookMutator is a SpecMutator that attaches PostStart registration
// and PreStop deregistration hooks to every container spec. The Guide reference
// is set after the Guide container is created (breaking the circular dep).
// Protected by sync.Mutex for the deferred Guide wiring.
type lifecycleHookMutator struct {
	mu         sync.Mutex
	guide      container.AgentRouter
	serviceReg *network.ServiceRegistry
}

// SetGuide sets the Guide reference for hook creation. Called after the Guide
// container is created by the DaemonSet controller. Thread-safe.
func (m *lifecycleHookMutator) SetGuide(g container.AgentRouter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guide = g
}

// Mutate attaches lifecycle hooks to the container spec. Hooks use
// ContainerAwareHook to read the real agent ID at execution time,
// avoiding the spec-time vs runtime ID mismatch.
func (m *lifecycleHookMutator) Mutate(spec *container.ContainerSpec) error {
	m.mu.Lock()
	g := m.guide
	reg := m.serviceReg
	m.mu.Unlock()

	// PostStart — register with Guide routing + ServiceRegistry.
	// The hook implements ContainerAwareHook, so the HookRunner injects
	// the Container before Execute. This provides the real agent ID.
	spec.Hooks.PostStart = container.HookAction{
		Handler: container.NewPostStartRegistrationHook(container.PostStartRegistrationHookConfig{
			Guide:    g,
			Registry: reg,
		}),
	}

	// PreStop — deregister from Guide + ServiceRegistry.
	spec.Hooks.PreStop = container.HookAction{
		Handler: container.NewPreStopDeregistrationHook(g, reg),
	}

	return nil
}

// =============================================================================
// Agent Extraction
// =============================================================================

// extractAgent retrieves the first container of the given agent type from the
// registry and asserts its agent to the requested concrete type.
func extractAgent[T container.ContainerAgent](reg *container.ContainerRegistry, agentType string) (T, error) {
	var zero T
	containers := reg.ListByType(agentType)
	if len(containers) == 0 {
		return zero, fmt.Errorf("no container for agent type %q", agentType)
	}
	agent, ok := containers[0].Agent().(T)
	if !ok {
		return zero, fmt.Errorf("container agent type assertion failed for %q", agentType)
	}
	return agent, nil
}

// =============================================================================
// Quota Derivation
// =============================================================================

// asyncHookGoroutinesPerContainer is the number of goroutines the HookRunner
// spawns asynchronously per container start. Only RunPostStart uses runHookAsync
// (hooks.go:77-90); all other hooks run synchronously.
const asyncHookGoroutinesPerContainer int64 = 1

// runtimeOverhead returns the goroutine overhead the container runtime adds
// per container:
//   - 1 goroutine per probe (ProbeRunner.startProbeLoop in probe.go)
//   - 1 goroutine for the async post-start hook (HookRunner.runHookAsync)
//
// Uses the constant probesPerContainer because the ProbeFactory attaches
// probes after spec mutation — spec.Probes is empty at quota derivation time.
func runtimeOverhead(_ container.ContainerSpec) int64 {
	return probesPerContainer + asyncHookGoroutinesPerContainer
}

// quotaFromSpecs derives resource quota limits from the AgentSpecRegistry.
// Every value is computed from per-agent ContainerSpec data; no magic numbers.
//
// The quota must accommodate the worst-case concurrent resource usage:
//   - All agents simultaneously active (Hot tier)
//   - Restart overlap: agents with RestartOnFailure/RestartAlways may have
//     a dying container + a new container alive concurrently during restart
//   - Runtime overhead per container (probes + async hooks, from spec data)
func quotaFromSpecs(specReg *container.AgentSpecRegistry) container.ResourceQuotaConfig {
	all := specReg.Descriptors().All()
	agentCount := int64(len(all))

	var (
		totalGoroutines     int64
		totalContextRequest int64
		totalContextWindow  int64
		restartableCount    int64 // agents that may restart (concurrent old+new overlap)
		maxGoroutineLimit   int64 // largest single-agent goroutine budget (incl overhead)
		maxContextRequest   int64 // largest single-agent request window
		maxContextWindow    int64 // largest single-agent context window
	)

	for _, d := range all {
		spec, err := specReg.SpecForAgent(d.AgentType)
		if err != nil {
			continue
		}

		overhead := runtimeOverhead(spec)
		goroutines := spec.Resources.GoroutineLimit + overhead
		ctxRequest := int64(spec.Resources.ContextWindowRequest)
		ctxWindow := int64(spec.Resources.ContextWindowLimit)

		totalGoroutines += goroutines
		totalContextRequest += ctxRequest
		totalContextWindow += ctxWindow

		if goroutines > maxGoroutineLimit {
			maxGoroutineLimit = goroutines
		}
		if ctxRequest > maxContextRequest {
			maxContextRequest = ctxRequest
		}
		if ctxWindow > maxContextWindow {
			maxContextWindow = ctxWindow
		}

		if spec.RestartPolicy == container.RestartOnFailure || spec.RestartPolicy == container.RestartAlways {
			restartableCount++
		}
	}

	// Restart headroom: during restart, at most restartableCount agents
	// have overlapping old+new containers. Each overlap adds one extra
	// container's worth of goroutines (incl. runtime overhead). Use the
	// max per-agent total as the conservative upper bound per overlap slot.
	restartGoroutineHeadroom := restartableCount * maxGoroutineLimit

	// Context window: sum of all agent windows + one restart overlap.
	// During restart, at most one agent has both old (draining) and new
	// (starting) containers. The overlap is bounded by the single largest
	// context window.
	contextHeadroom := maxContextWindow

	return container.ResourceQuotaConfig{
		GoroutineLimit:       totalGoroutines + restartGoroutineHeadroom,
		ContextWindowRequest: totalContextRequest + maxContextRequest,
		ContextWindowLimit:   totalContextWindow + contextHeadroom,
		ContainerLimit:       agentCount + restartableCount,
	}
}

// =============================================================================
// Agent Bootstrap Helpers (unchanged from before)
// =============================================================================

// bootstrapLiveGuide creates and starts a Guide with LLM classification.
// When hydrated is non-nil, it reuses pre-resolved auth (skipping duplicate
// OAuth + Code Assist setup). If provider auth is unavailable, it falls back
// to a local rule-based classifier so the UI can launch without authorization.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus, actPub events.ActivityPublisher, hydrated *providers.HydratedGoogleAuth, googleGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, projectRoot string, sessionID string, forest agentShared.MemoryForestService, scope *concurrency.GoroutineScope, sessionVFSLookup func(string) *versioning.SessionVFS, factory *identity.Factory) (*guide.Guide, error) {
	if factory == nil {
		return nil, fmt.Errorf("guide bootstrap: nil identity factory")
	}
	sessionID = bootstrapSessionID(sessionID)
	googleCfg := defaultGuideGoogleConfig(authRegistry)
	cfg := guide.Config{
		Bus:          bus,
		ActivityPub:  actPub,
		AgentID:      "guide",
		SessionID:    sessionID,
		GoogleConfig: &googleCfg,
		Forest:       forest,
		Factory:      factory,
		WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:      versioning.WorkspaceViewDisk,
			DefaultSessionID: sessionID,
			WorkingDir:       projectRoot,
			SessionLookup:    sessionVFSLookup,
			DiskFallback:     versioning.NewDiskFileAccess(projectRoot, true),
		}),
	}

	promptSkills := guide.DiscoverGuidePromptSkills(guide.GuideGoSkills())

	var provider *providers.GoogleProvider
	var err error
	if hydrated != nil {
		provider, err = providers.NewGoogleProviderFromHydrated(ctx, googleCfg, hydrated, promptSkills...)
	} else {
		provider, err = providers.NewGoogleProvider(ctx, googleCfg, promptSkills...)
	}
	if err == nil && provider != nil {
		wrapped := googleGw.WrapProvider(providers.Instrument(provider), gateway.PriorityUserInteractive)
		g, newErr := guide.NewWithProvider(wrapped, googleCfg.Model, cfg)
		if newErr != nil {
			return nil, newErr
		}
		g.SetScope(scope)
		if startErr := g.Start(ctx); startErr != nil {
			return nil, startErr
		}
		return g, nil
	}

	g, newErr := guide.NewWithClassifier(guide.NewRuleClassifierClient(), cfg)
	if newErr != nil {
		return nil, newErr
	}
	g.SetScope(scope)
	if startErr := g.Start(ctx); startErr != nil {
		return nil, startErr
	}
	return g, nil
}

func bootstrapArchitect(ctx context.Context, canonicalID string, sessionID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, anthropicGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, actCtrlRef *atomic.Pointer[activation.ActivationController], planStore *architect.PlanStore, forest agentShared.MemoryForestService, sessionVFSLookup func(string) *versioning.SessionVFS, factoryRef *atomic.Pointer[identity.Factory], scope *concurrency.GoroutineScope) (*architect.Architect, error) {
	sessionID = bootstrapSessionID(sessionID)
	bootstrapStart := time.Now()
	deadline, hasDL := ctx.Deadline()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_start", "has_deadline", hasDL, "deadline", deadline)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	authMode := registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode(""))
	apiKey := ""
	if authMode == providers.AnthropicAuthModeAPIKey {
		apiKey = providers.ResolveAnthropicAPIKey("")
	}
	guide.DebugFileLog().Info(
		"DEBUG: bootstrap_architect_auth_resolved",
		"auth_mode", authMode,
		"has_key", apiKey != "",
		"elapsed_ms", time.Since(bootstrapStart).Milliseconds(),
	)
	var factory *identity.Factory
	if factoryRef != nil {
		factory = factoryRef.Load()
	}
	cfg := architect.Config{
		ID:                canonicalID,
		SessionID:         sessionID,
		EnableLLM:         true,
		Model:             architect.DefaultArchitectModel,
		AnthropicAuthMode: authMode,
		AnthropicAPIKey:   apiKey,
		ActivityPub:       actPub,
		PlanStore:         planStore,
		Forest:            forest,
		Factory:           factory,
		PlannerProviderWrapper: func(p *providers.AnthropicProvider) architect.PlannerStreamProvider {
			return anthropicGw.WrapProvider(providers.Instrument(p), gateway.PriorityPlanning)
		},
	}
	if ac := actCtrlRef.Load(); ac != nil {
		cfg.RequestGuard = func() func() {
			return ac.AcquireRequestGuard("architect")
		}
	}
	newStart := time.Now()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_new_start")
	a, err := architect.New(ctx, cfg)
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_new_done", "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err)
	if err != nil {
		return nil, err
	}
	a.SetScope(scope)
	startStart := time.Now()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_bus_start")
	if err := a.Start(bus); err != nil {
		return nil, err
	}
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_bus_done", "elapsed_ms", time.Since(startStart).Milliseconds())
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_complete", "total_elapsed_ms", time.Since(bootstrapStart).Milliseconds())
	return a, nil
}

// bootstrapGuardian creates and starts a Guardian agent. Provider creation is
// best-effort: when the OpenAI API key is missing the guardian starts in
// degraded mode — deterministic safety checks still work, LLM-dependent
// escalation paths return a clear error. Git subsystems are wired from the
// atomic ref if available (daemon restart case); on initial boot they are
// nil and wired later via SetGitSubsystems in Phase 3.
func bootstrapGuardian(
	ctx context.Context,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	gitSubsRef *atomic.Pointer[gitBootResult],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	forest agentShared.MemoryForestService,
	sessionVFSLookup func(string) *versioning.SessionVFS,
	factory *identity.Factory,
	scope *concurrency.GoroutineScope,
) (*guardian.Guardian, error) {
	if factory == nil {
		return nil, fmt.Errorf("guardian bootstrap: nil identity factory")
	}
	model := effectiveModelForCurrentAuth(authRegistry, guardian.DefaultGuardianModel)
	var wrapped providers.Provider
	if p, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation); err != nil {
		slog.Warn("guardian: provider unavailable, LLM features disabled", "error", err)
	} else {
		wrapped = p
	}

	cfg := guardian.Config{
		ActivityPub: actPub,
		FileAccess: versioning.NewSessionRoutingFileAccess(
			true,
			sessionVFSLookup,
			versioning.NewDiskFileAccess(projectRoot, true),
		),
		WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:   versioning.WorkspaceViewGlobal,
			WorkingDir:    projectRoot,
			SessionLookup: sessionVFSLookup,
			DiskFallback:  versioning.NewDiskFileAccess(projectRoot, true),
		}),
		Sanitizer: security.NewSecretSanitizer(),
		Forest:    forest,
		Factory:   factory,
	}

	// Wire git subsystems from atomic ref (available on daemon restart,
	// nil on initial boot — Phase 3 wires via SetGitSubsystems).
	if gs := gitSubsRef.Load(); gs != nil {
		cfg.GitBus = gs.bus
		cfg.GitWatcher = gs.watcher
	}

	g, err := guardian.New(cfg, wrapped)
	if err != nil {
		return nil, err
	}
	g.SetCommandApprovalStore(commandapproval.NewRuleStore(filepath.Join(projectRoot, ".sylk", "local", "command_approvals.yaml")))
	if wrapped != nil && model != guardian.DefaultGuardianModel {
		if err := g.SwapModel(ctx, model, wrapped); err != nil {
			return nil, err
		}
	}

	// Wire quarantine buffer for external content inspection.
	q := fetch.NewQuarantineBuffer(
		guardian.DefaultQuarantineMaxItems,
		guardian.DefaultQuarantineMaxBytes,
	)
	g.SetQuarantine(q)
	if quarantineRef != nil {
		quarantineRef.Store(q)
	}
	g.SetScope(scope)

	if err := g.Start(bus); err != nil {
		return nil, err
	}
	return g, nil
}

// wireGuardianPostBootDeps re-wires observability, git, and agent-seeding
// dependencies on the Guardian. Called from the factory closure so that
// daemon-restart instances receive the same wiring as the initial boot's
// Phase 3. All parameters are read from atomic refs; nil-safe.
func wireGuardianPostBootDeps(
	grd *guardian.Guardian,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	daemonCtrl *daemon.DaemonSetController,
	guideRef *atomic.Pointer[guide.Guide],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	authRegistry *credentials.AuthRegistry,
	openaiGw *gateway.ProviderGateway,
	allGateways []*gateway.ProviderGateway,
	knowledgeStore *knowledge.KnowledgeStore,
	budget *concurrency.GoroutineBudget,
) {
	var acQuerier guardian.ActivationQuerier
	var amQuerier guardian.ActivationMetricsQuerier
	if ac := actCtrlRef.Load(); ac != nil {
		acQuerier = ac
		amQuerier = &activationMetricsAdapter{m: ac.Metrics()}
	}
	var dcQuerier guardian.DaemonQuerier
	if daemonCtrl != nil {
		dcQuerier = &daemonQuerierAdapter{dc: daemonCtrl}
	}
	if ac := actCtrlRef.Load(); ac != nil {
		grd.SetRequestGuard(func() func() {
			return ac.AcquireRequestGuard("guardian")
		})
	}
	grd.SetObservabilityDeps(acQuerier, amQuerier, dcQuerier)
	grd.SetProviderRefresher(buildProviderRefresher(authRegistry, allGateways[0], allGateways[1], allGateways[2], gateway.PriorityValidation))

	// VFS observability — aggregate CVS/VFS stats across all sessions.
	grd.SetVFSDeps(
		&orchestratorCVSAdapter{orchRef: orchRef},
		&orchestratorVFSAdapter{orchRef: orchRef},
	)

	// Extended observability — pipeline, gateway, knowledge, concurrency.
	grd.SetExtendedObservabilityDeps(
		&pipelineQuerierAdapter{orchRef: orchRef},
		&gatewayQuerierAdapter{gateways: allGateways},
		&knowledgeQuerierAdapter{store: knowledgeStore},
		&concurrencyQuerierAdapter{budget: budget},
	)

	// Cost calculator — derives USD-cents per LLM call from model pricing.
	grd.SetCostCalculator(buildCostCalculator())

	// Seed agent registry from the Guide's current state so the Guardian
	// doesn't start with total=0 after daemon restart.
	if g := guideRef.Load(); g != nil {
		// Reap agents stuck in not-ready state before seeding — prevents
		// stale registrations from persisting across daemon restarts.
		if reaped := g.ReapStaleRegistrations(); reaped > 0 {
			slog.Info("guardian post-boot: reaped stale agent registrations", "count", reaped)
		}
		grd.SeedKnownAgents(g.RegisteredAgentInfos())
		_ = registerAgentWithGuide(g, grd, "guardian")
	}
}

func buildAcademicFetchPipeline(
	_ string,
	guardianRef *atomic.Pointer[guardian.Guardian],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
) *fetch.Pipeline {
	policy := fetch.NewFetchPolicy(fetch.DefaultPolicyConfig())
	consent := newAcademicFetchConsentGate()

	quarantine := (*fetch.QuarantineBuffer)(nil)
	if quarantineRef != nil {
		quarantine = quarantineRef.Load()
	}
	if quarantine == nil {
		quarantine = fetch.NewQuarantineBuffer(
			guardian.DefaultQuarantineMaxItems,
			guardian.DefaultQuarantineMaxBytes,
		)
	}

	return fetch.NewPipeline(fetch.PipelineConfig{
		Policy:     policy,
		Consent:    consent,
		Quarantine: quarantine,
		Client: fetch.NewClient(fetch.ClientConfig{
			MaxBytes: policy.MaxBytes(),
		}),
		Extractor: fetch.NewContentExtractor(),
		Inspect: func(ctx context.Context, entry *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
			if guardianRef == nil {
				return fetch.VerdictBlocked, nil, fmt.Errorf("guardian inspection is unavailable")
			}
			grd := guardianRef.Load()
			if grd == nil {
				return fetch.VerdictBlocked, nil, fmt.Errorf("guardian inspection is unavailable")
			}
			return grd.InspectQuarantinedContent(ctx, entry)
		},
		Ingest: func(ctx context.Context, entry *fetch.QuarantineEntry, provenance *fetch.Provenance, extracted *fetch.ExtractResult) error {
			if knowledgeBackend == nil {
				return fmt.Errorf("committed knowledge backend is unavailable")
			}
			return knowledgeBackend.IngestFetchedDocument(ctx, entry, provenance, extracted)
		},
		Logger: slog.Default(),
	})
}

func registerArchitectWithGuide(g *guide.Guide, a *architect.Architect) error {
	if g == nil || a == nil {
		return nil
	}
	seedRouterKnownAgents(g, a)
	if err := g.RegisterRouter(a); err != nil {
		return err
	}
	g.MarkAgentReady(a.GetRoutingInfo().ID)
	return nil
}

// registerAgentWithGuide registers any AgentRouter with the Guide and marks it ready.
func registerAgentWithGuide(g *guide.Guide, router guide.AgentRouter, _ string) error {
	if g == nil || router == nil {
		return nil
	}
	seedRouterKnownAgents(g, router)
	return registerRoutingInfoWithGuide(g, router.GetRoutingInfo())
}

func registerRoutingInfoWithGuide(g *guide.Guide, info *guide.AgentRoutingInfo) error {
	if g == nil || info == nil {
		return nil
	}
	if err := g.Register(info); err != nil {
		return err
	}
	// Use the agent's own routing ID (UUID) — Register() stores
	// readyAgents keyed by info.ID, not by agentType.
	g.MarkAgentReady(info.ID)
	return nil
}

type guideKnownAgentSeeder interface {
	SeedKnownAgents([]*guide.AgentAnnouncement)
}

func seedRouterKnownAgents(g *guide.Guide, router guide.AgentRouter) {
	if g == nil || router == nil {
		return
	}
	seeder, ok := router.(guideKnownAgentSeeder)
	if !ok {
		return
	}
	seeder.SeedKnownAgents(g.RegisteredAgentInfos())
}

func findRegisteredPodContainer(reg *container.ContainerRegistry, podID, agentType string) *container.Container {
	if reg == nil {
		return nil
	}
	if podID != "" {
		for _, ctr := range reg.ListByPod(container.PodID(podID)) {
			if ctr == nil {
				continue
			}
			if ctr.Spec().AgentType == agentType {
				return ctr
			}
		}
		return nil
	}
	containers := reg.ListByType(agentType)
	if len(containers) == 0 {
		return nil
	}
	return containers[0]
}

func isTaskScopedPipelineWorker(podID, agentType string) bool {
	if strings.TrimSpace(podID) == "" || podID == agentType {
		return false
	}
	switch agentType {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return true
	default:
		return false
	}
}

func taskScopedWorkerRoutingInfo(info *guide.AgentRoutingInfo, podID, agentType string) *guide.AgentRoutingInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	taskAlias := orchestrator.TaskScopedRoutingName("", podID, agentType)
	cloned.PodID = podID
	cloned.Name = strings.TrimSpace(info.Name)
	if taskAlias != "" {
		cloned.Name = taskAlias
	}
	cloned.Aliases = nil
	cloned.ActionShortcuts = nil
	cloned.Triggers = guide.AgentTriggers{}
	if info.Registration != nil {
		reg := *info.Registration
		reg.Name = strings.TrimSpace(info.Registration.Name)
		if taskAlias != "" {
			reg.Name = taskAlias
		}
		reg.Aliases = nil
		cloned.Registration = &reg
	}
	return &cloned
}

func pipelineWorkerAgentID(ctx context.Context, agentType string) string {
	if spec, ok := container.CreationSpecFromContext(ctx); ok {
		if spec.Labels != nil {
			if specID, labelOK := spec.Labels["pipeline_worker_id"]; labelOK {
				if specID = strings.TrimSpace(specID); specID != "" {
					return specID
				}
			}
		}
	}
	return ""
}

func taskScopedCreationAgentID(ctx context.Context, agentType, singletonID string) string {
	if workerID := pipelineWorkerAgentID(ctx, agentType); workerID != "" {
		return workerID
	}
	podID, ok := container.CreationPodIDFromContext(ctx)
	if ok && isTaskScopedPipelineWorker(string(podID), agentType) {
		return ""
	}
	return singletonID
}

// preRegisterAgentRouting pre-registers static routing metadata for all
// non-pipeline agents with the Guide. This makes agents visible to the
// classifier before their containers are activated.
func preRegisterAgentRouting(g *guide.Guide, ids *container.AgentIdentityRegistry) {
	type staticInfo struct {
		agentType string
		fn        func(string) *guide.AgentRoutingInfo
	}
	agents := []staticInfo{
		{"architect", architect.ArchitectRoutingInfo},
		{"inspector", inspectorGlobal.InspectorRoutingInfo},
		{"tester", globaltester.TesterRoutingInfo},
		{"librarian", librarian.LibrarianRoutingInfo},
		{"academic", academic.AcademicRoutingInfo},
	}
	for _, a := range agents {
		canonicalID, ok := ids.Get(a.agentType)
		if !ok {
			continue
		}
		if err := g.PreRegister(a.fn(canonicalID)); err != nil {
			slog.Warn("pre-register agent", "agent", a.agentType, "error", err)
		}
	}

	// Guardian uses a hardcoded ID (daemon, not identity-registered).
	if err := g.PreRegister(guardian.GuardianRoutingInfo("guardian")); err != nil {
		slog.Warn("pre-register guardian", "error", err)
	}

	// Archivalist uses a no-arg routing info function (hardcoded ID).
	if err := g.PreRegister(archivalist.ArchivalistRoutingInfo()); err != nil {
		slog.Warn("pre-register archivalist", "error", err)
	}
}

// buildAuthPublisher creates an AuthPublisher that broadcasts credential
// changes over the event bus. DaemonSet agents (Guide, Orchestrator)
// subscribe to this topic directly.
func buildAuthPublisher(bus guide.EventBus) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		msg := guide.NewAuthChangedMessage("", guide.AuthEventPayload{
			ProviderType: event.ProviderType,
			AuthMethod:   event.AuthMethod,
			Available:    event.Available,
		})
		if err := bus.Publish(guide.TopicAuthCredentials, msg); err != nil {
			slog.Warn("tui_auth_changed_publish_failed",
				"provider_type", event.ProviderType,
				"auth_method", event.AuthMethod,
				"available", event.Available,
				"error", err.Error(),
			)
		}
	}
}

// buildOnDemandAuthRefresher creates a publisher that walks the container
// registry and refreshes on-demand agents matching the event's provider type.
func buildOnDemandAuthRefresher(containerReg *container.ContainerRegistry) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		if !event.Available {
			return
		}
		for _, c := range containerReg.All() {
			agent := c.Agent()
			refreshable, ok := agent.(container.AuthRefreshable)
			if !ok || refreshable.ProviderType() != event.ProviderType {
				continue
			}
			refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := refreshable.RefreshProvider(refreshCtx, event.AuthMethod)
			refreshCancel()
			if err != nil {
				slog.Warn("on-demand auth refresh failed",
					"agent", c.ID(),
					"provider", event.ProviderType,
					"method", event.AuthMethod,
					"error", err)
			}
		}
	}
}

// populateSeedModels enriches AgentSeed entries with supported models
// from agents in the container registry that implement ModelSwappable.
func populateSeedModels(seeds []ui.AgentSeed, reg *container.ContainerRegistry) {
	for i := range seeds {
		for _, c := range reg.ListByType(seeds[i].AgentType) {
			swappable, ok := c.Agent().(container.ModelSwappable)
			if !ok {
				continue
			}
			opts := swappable.SupportedModels()
			entries := make([]agentpkg.ModelEntry, len(opts))
			for j, opt := range opts {
				entries[j] = agentpkg.ModelEntry{ID: opt.ID, DisplayName: opt.DisplayName}
			}
			seeds[i].SupportedModels = entries
			break // One agent per type is sufficient.
		}
	}
}

func resolvePersistedModelForCurrentAuth(modelID, providerID string, authRegistry *credentials.AuthRegistry) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = string(container.ProviderForModel(modelID))
	}
	if providerID != string(container.ProviderOpenAI) {
		return modelID
	}
	return effectiveModelForCurrentAuth(authRegistry, modelID)
}

func effectiveModelForCurrentAuth(authRegistry *credentials.AuthRegistry, modelID string) string {
	if container.ProviderForModel(modelID) != container.ProviderOpenAI {
		return modelID
	}
	authMode := registryAuthMethod(authRegistry, "openai", "api_key")
	return providers.ResolveOpenAIModelForAuth(modelID, authMode)
}

// agentSwapPriority maps agent type to the gateway request priority used at
// bootstrap. Derived from registerOnDemandAgentCreators wiring.
var agentSwapPriority = map[string]gateway.RequestPriority{
	"guide":        gateway.PriorityUserInteractive,
	"orchestrator": gateway.PriorityUserInteractive,
	"architect":    gateway.PriorityPlanning,
	"guardian":     gateway.PriorityValidation,
	"inspector":    gateway.PriorityValidation,
	"tester":       gateway.PriorityValidation,
	"engineer":     gateway.PriorityExecution,
	"designer":     gateway.PriorityExecution,
	"librarian":    gateway.PriorityExecution,
	"archivalist":  gateway.PriorityExecution,
	"academic":     gateway.PriorityExecution,
}

// buildModelSwapper creates a closure that creates the correct provider with
// the correct gateway wrapper, then calls SwapModel on the matching agent.
// Provider creation is centralized here because cmd/tui.go owns all gateways.
func buildModelSwapper(
	containerReg *container.ContainerRegistry,
	activator modelSwapActivator,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
) func(ctx context.Context, agentType, modelID string) error {
	return func(ctx context.Context, agentType, modelID string) error {
		swappableContainer, err := resolveModelSwapContainer(ctx, containerReg, activator, agentType)
		if err != nil {
			if errors.Is(err, errNoSwappableContainer) && !architectModelSwapRequiresLiveActivation(agentType) {
				return nil
			}
			return err
		}
		modelID = effectiveModelForCurrentAuth(authRegistry, modelID)
		priority, ok := agentSwapPriority[agentType]
		if !ok {
			return fmt.Errorf("no model swap priority configured for agent type %q", agentType)
		}
		provider, err := createSwapProvider(ctx, modelID, authRegistry, googleGw, anthropicGw, openaiGw, priority)
		if err != nil {
			return fmt.Errorf("model swap provider for %s: %w", agentType, err)
		}
		swappable, ok := swappableContainer.Agent().(container.ModelSwappable)
		if !ok {
			return fmt.Errorf("container %q for type %q does not implement ModelSwappable", swappableContainer.ID(), agentType)
		}
		return swappable.SwapModel(ctx, modelID, provider)
	}
}

func resolveModelSwapContainer(
	ctx context.Context,
	containerReg *container.ContainerRegistry,
	activator modelSwapActivator,
	agentType string,
) (*container.Container, error) {
	if architectModelSwapRequiresLiveActivation(agentType) && activator != nil {
		ctr, err := activator.EnsureActive(ctx, agentType)
		if err != nil {
			return nil, fmt.Errorf("activate %s for model swap: %w", agentType, err)
		}
		if ctr != nil {
			if _, ok := ctr.Agent().(container.ModelSwappable); ok {
				return ctr, nil
			}
		}
	}
	if ctr := firstRunningSwappableContainerForType(containerReg, agentType); ctr != nil {
		return ctr, nil
	}
	if ctr := firstSwappableContainerForType(containerReg, agentType); ctr != nil {
		return ctr, nil
	}
	if architectModelSwapRequiresLiveActivation(agentType) && activator != nil {
		if _, err := activator.EnsureActive(ctx, agentType); err != nil {
			return nil, fmt.Errorf("activate %s for model swap: %w", agentType, err)
		}
		if ctr := firstSwappableContainerForType(containerReg, agentType); ctr != nil {
			return ctr, nil
		}
	}
	return nil, fmt.Errorf("%w for type %q", errNoSwappableContainer, agentType)
}

func architectModelSwapRequiresLiveActivation(agentType string) bool {
	return agentType == "architect"
}

func firstRunningSwappableContainerForType(containerReg *container.ContainerRegistry, agentType string) *container.Container {
	for _, c := range containerReg.ListByType(agentType) {
		if !c.IsRunning() {
			continue
		}
		if _, ok := c.Agent().(container.ModelSwappable); ok {
			return c
		}
	}
	return nil
}

func firstSwappableContainerForType(containerReg *container.ContainerRegistry, agentType string) *container.Container {
	for _, c := range containerReg.ListByType(agentType) {
		if _, ok := c.Agent().(container.ModelSwappable); ok {
			return c
		}
	}
	return nil
}

// buildProviderRefresher returns a ProviderRefresher for the given priority
// that creates a fresh provider for any model/auth-method combination.
func buildProviderRefresher(
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) container.ProviderRefresher {
	return func(ctx context.Context, modelID, authMethod string) (providers.ProviderAdapter, error) {
		return createRefreshProvider(ctx, modelID, authMethod, authRegistry, googleGw, anthropicGw, openaiGw, priority)
	}
}

// createRefreshProvider creates a raw provider for the given model ID and
// auth method, then wraps it with the correct gateway at the specified priority.
func createRefreshProvider(
	ctx context.Context,
	modelID, authMethod string,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	switch container.ProviderForModel(modelID) {
	case container.ProviderAnthropic:
		am := authMethod
		if am == "" {
			am = registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode(""))
		}
		raw, err := providers.NewAnthropicProvider(ctx, providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: am,
		})
		if err != nil {
			return nil, err
		}
		return anthropicGw.WrapProvider(providers.Instrument(raw), priority), nil
	case container.ProviderGoogle:
		cfg := providers.DefaultGoogleConfig()
		cfg.Model = modelID
		cfg.MaxTokens = container.SwapMaxTokens
		if authMethod != "" {
			cfg.AuthMode = authMethod
		} else {
			cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
		}
		raw, err := providers.NewGoogleProvider(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return googleGw.WrapProvider(providers.Instrument(raw), priority), nil
	case container.ProviderOpenAI:
		am := authMethod
		if am == "" {
			am = registryAuthMethod(authRegistry, "openai", "api_key")
		}
		modelID = providers.ResolveOpenAIModelForAuth(modelID, am)
		raw, err := providers.NewOpenAIProvider(ctx, providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: am,
		})
		if err != nil {
			return nil, err
		}
		return openaiGw.WrapProvider(providers.Instrument(raw), priority), nil
	default:
		return nil, fmt.Errorf("unknown provider for model %q", modelID)
	}
}

// createSwapProvider creates a raw provider for the given model ID, then
// wraps it with the correct gateway at the specified priority.
func createSwapProvider(
	ctx context.Context,
	modelID string,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	switch container.ProviderForModel(modelID) {
	case container.ProviderAnthropic:
		raw, err := providers.NewAnthropicProvider(ctx, providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode("")),
		})
		if err != nil {
			return nil, err
		}
		return anthropicGw.WrapProvider(providers.Instrument(raw), priority), nil
	case container.ProviderGoogle:
		cfg := providers.DefaultGoogleConfig()
		cfg.Model = modelID
		cfg.MaxTokens = container.SwapMaxTokens
		cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
		raw, err := providers.NewGoogleProvider(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return googleGw.WrapProvider(providers.Instrument(raw), priority), nil
	case container.ProviderOpenAI:
		authMode := registryAuthMethod(authRegistry, "openai", "api_key")
		modelID = providers.ResolveOpenAIModelForAuth(modelID, authMode)
		raw, err := providers.NewOpenAIProvider(ctx, providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: authMode,
		})
		if err != nil {
			return nil, err
		}
		return openaiGw.WrapProvider(providers.Instrument(raw), priority), nil
	default:
		return nil, fmt.Errorf("unknown provider for model %q", modelID)
	}
}

// buildAuthResolver creates a provider auth resolver that reports which
// canonical auth methods are currently usable for each provider.
func buildAuthResolver() credentials.AuthMethodResolver {
	// Pre-resolve the credential manager so the closure can check the
	// secure store (keychain / encrypted file) in addition to env vars.
	var credManager *credentials.Manager
	if dirs, dirErr := storage.ResolveDirs(); dirErr == nil && dirs != nil {
		credManager, _ = credentials.NewManager(dirs, "default")
	}

	return func(providerType string) map[string]bool {
		methods := make(map[string]bool)
		switch providerType {
		case "google":
			if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" || probeSecureKey(credManager, "google") {
				methods[providers.GoogleAuthModeAPIKey] = true
			}
			if probeOAuthToken("google") {
				methods[providers.GoogleAuthModeOAuth] = true
			}
			if probeGoogleServiceAccount(credManager) {
				methods[providers.GoogleAuthModeServiceAccount] = true
			}
		case "anthropic":
			if providers.ResolveAnthropicAPIKey("") != "" {
				methods[providers.AnthropicAuthModeAPIKey] = true
			}
			if probeOAuthToken("anthropic") {
				methods[providers.AnthropicAuthModeOAuth] = true
			}
		case "openai":
			if os.Getenv("OPENAI_API_KEY") != "" || probeSecureKey(credManager, "openai") {
				methods["api_key"] = true
			}
			if probeOAuthToken("openai") {
				methods["chatgpt"] = true
			}
		}
		return methods
	}
}

// probeSecureKey checks the secure credential store for a stored API key.
func probeSecureKey(mgr *credentials.Manager, provider string) bool {
	if mgr == nil {
		return false
	}
	key, err := mgr.GetAPIKey(provider)
	return err == nil && key != ""
}

func probeGoogleServiceAccount(mgr *credentials.Manager) bool {
	if payload := strings.TrimSpace(os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")); payload != "" {
		return true
	}
	if mgr == nil {
		return false
	}
	payload, err := mgr.GetAPIKey("google_service_account")
	return err == nil && strings.TrimSpace(payload) != ""
}

// probeOAuthToken checks whether a stored OAuth token exists for the
// given provider by attempting to load from the provider's token store.
func probeOAuthToken(provider string) bool {
	ctx := context.Background()
	switch provider {
	case "google":
		auth, err := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	case "anthropic":
		auth, err := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	case "openai":
		auth, err := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	default:
		return false
	}
}

// chainPublishers combines multiple AuthPublishers into one.
func chainPublishers(publishers ...credentials.AuthPublisher) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		for _, pub := range publishers {
			pub(event)
		}
	}
}

func registryAuthMethod(reg *credentials.AuthRegistry, providerType, fallback string) string {
	if reg != nil {
		if method := credentials.CanonicalAuthMethod(providerType, reg.ActiveMethod(providerType)); method != "" {
			return method
		}
		if method := credentials.CanonicalAuthMethod(providerType, reg.PreferredMethod(providerType)); method != "" {
			return method
		}
	}
	if method := credentials.CanonicalAuthMethod(providerType, fallback); method != "" {
		return method
	}
	return credentials.DefaultAuthMethod(providerType)
}

func defaultGuideGoogleConfig(authRegistry *credentials.AuthRegistry) providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3.1-pro-preview"
	cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
	return cfg
}

func defaultOrchestratorGoogleConfig(authRegistry *credentials.AuthRegistry) providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3-flash-preview"
	cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
	return cfg
}

func bootstrapOrchestrator(ctx context.Context, agentID, sessionID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, hydrated *providers.HydratedGoogleAuth, authRegistry *credentials.AuthRegistry, googleGw *gateway.ProviderGateway, forest agentShared.MemoryForestService, factory *identity.Factory, scope *concurrency.GoroutineScope) (*orchestrator.Orchestrator, error) {
	if factory == nil {
		return nil, fmt.Errorf("orchestrator bootstrap: nil identity factory")
	}
	sessionID = bootstrapSessionID(sessionID)
	googleCfg := defaultOrchestratorGoogleConfig(authRegistry)

	// Best-effort provider creation. If Google auth isn't available yet,
	// the orchestrator starts in LLM-ready mode and activates when the
	// user authorizes later (via SetProvider from the auth refresh hook).
	provider, provErr := resolveOrchestratorGoogleProvider(ctx, googleCfg, hydrated)
	if provErr != nil {
		slog.Warn("orchestrator google provider deferred — will activate on auth",
			"error", provErr,
			"auth_mode", googleCfg.AuthMode)
	}

	cfg := orchestrator.DefaultConfig()
	cfg.AgentID = agentID
	cfg.SessionID = sessionID
	cfg.EnableLLM = true
	cfg.Forest = forest
	cfg.Factory = factory
	if provErr == nil && provider != nil {
		cfg.GoogleConfig = &googleCfg
	}

	sd := sylkdir.New(projectRoot)
	if err := sd.Init(); err != nil {
		return nil, fmt.Errorf("orchestrator sylkdir init: %w", err)
	}

	var orchProvider orchestrator.OrchestratorProvider
	if provider != nil {
		orchProvider = googleGw.WrapProvider(providers.Instrument(provider), gateway.PriorityUserInteractive)
	}
	orch, err := orchestrator.New(cfg, orchProvider, actPub, sd)
	if err != nil {
		return nil, err
	}
	orch.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      versioning.WorkspaceViewGlobal,
		DefaultSessionID: cfg.SessionID,
		WorkingDir:       projectRoot,
		SessionLookup:    orch.GetSessionVFS,
		DiskFallback:     versioning.NewDiskFileAccess(projectRoot, true),
	}))
	if err := orch.Start(bus); err != nil {
		return nil, err
	}
	return orch, nil
}

// resolveOrchestratorGoogleProvider creates the Google provider for the
// orchestrator. Tries the hydrated auth first, then the configured auth mode,
// then falls back to API key mode if the configured mode fails.
func resolveOrchestratorGoogleProvider(ctx context.Context, cfg providers.GoogleConfig, hydrated *providers.HydratedGoogleAuth) (*providers.GoogleProvider, error) {
	// Best case: shared hydrated auth from Phase 1.5.
	if hydrated != nil {
		provider, err := providers.NewGoogleProviderFromHydrated(ctx, cfg, hydrated)
		if err == nil {
			return provider, nil
		}
		slog.Debug("orchestrator hydrated provider failed, trying fresh", "error", err)
	}

	// Try the configured auth mode (typically OAuth).
	provider, err := providers.NewGoogleProvider(ctx, cfg)
	if err == nil {
		return provider, nil
	}

	// If the configured mode was not API key, retry with API key mode.
	// The user may have GEMINI_API_KEY or GOOGLE_API_KEY set.
	if cfg.AuthMode != providers.GoogleAuthModeAPIKey {
		apiKeyCfg := cfg
		apiKeyCfg.AuthMode = providers.GoogleAuthModeAPIKey
		provider, apiKeyErr := providers.NewGoogleProvider(ctx, apiKeyCfg)
		if apiKeyErr == nil {
			return provider, nil
		}
		// Return the original error — it's more informative.
	}

	return nil, err
}

func registerOrchestratorWithGuide(g *guide.Guide, orch *orchestrator.Orchestrator) error {
	if g == nil || orch == nil {
		return nil
	}
	seedRouterKnownAgents(g, orch)
	if err := g.RegisterRouter(orch); err != nil {
		return err
	}
	g.MarkAgentReady(orch.GetRoutingInfo().ID)
	return nil
}

func refreshOrchestratorProvider(ctx context.Context, orch *orchestrator.Orchestrator, googleGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, authMethod string) {
	if orch == nil {
		return
	}
	googleCfg := defaultOrchestratorGoogleConfig(authRegistry)
	if authMethod != "" {
		googleCfg.AuthMode = authMethod
	}
	provider, err := resolveOrchestratorGoogleProvider(ctx, googleCfg, nil)
	if err != nil || provider == nil {
		slog.Warn("orchestrator provider refresh failed", "error", err)
		return
	}
	orch.SetProvider(googleGw.WrapProvider(providers.Instrument(provider), gateway.PriorityUserInteractive))
}

// =============================================================================
// Infrastructure Cleanup
// =============================================================================

// cleanupInfra tears down infrastructure on early bootstrap failure.
// Errors are logged rather than returned because this runs during error
// recovery — the caller already has a primary error to report.
func cleanupInfra(
	rt *container.DefaultRuntime,
	reg *container.ContainerRegistry,
	ns *network.NetworkNamespace,
	bus guide.EventBus,
) {
	if rt != nil && reg != nil {
		for _, c := range reg.All() {
			if c.IsRunning() {
				if err := rt.StopContainer(context.Background(), c); err != nil {
					slog.Warn("cleanup: stop container failed", "container", c.ID(), "error", err)
				}
			}
			if err := rt.RemoveContainer(context.Background(), c); err != nil {
				slog.Warn("cleanup: remove container failed", "container", c.ID(), "error", err)
			}
		}
	}
	if ns != nil {
		ns.Close()
	}
	if rt != nil {
		rt.Close()
	}
	if bus != nil {
		if err := bus.Close(); err != nil {
			slog.Warn("cleanup: bus close failed", "error", err)
		}
	}
}

// activationStorageDir returns the directory for Cool tier state files.
func activationStorageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sylk", "activation"), nil
}

// =============================================================================
// Theme + Misc
// =============================================================================

func parseThemeMode(s string) ui.ThemeMode {
	if s == "light" {
		return ui.ThemeLight
	}
	return ui.ThemeDark
}

// =============================================================================
// Handoff Supervisor
// =============================================================================

// bootstrapHandoffSupervisor creates and starts the handoff supervisor, registers
// agent creators and live agents, and wires the agent-replacement callback.
// Returns nil if initialization fails (non-fatal — handoff is optional).
func bootstrapHandoffSupervisor(
	g *guide.Guide,
	bus guide.EventBus,
	serviceReg *network.ServiceRegistry,
	containerReg *container.ContainerRegistry,
	creatorReg *container.AgentCreatorRegistry,
	activationCtrl *activation.ActivationController,
) *handoff.HandoffSupervisor {
	walDir, err := handoffWALDir()
	if err != nil {
		return nil
	}

	supervisorCfg := handoff.DefaultSupervisorConfig()
	supervisorCfg.WALDir = walDir
	supervisor := handoff.NewHandoffSupervisor(supervisorCfg)
	if bus != nil {
		supervisor.SetBriefSource(agentShared.NewArchivalistBriefSource(bus))
	}

	// Register creators for every known agent type so any handoff-capable
	// agent can spawn a fresh replacement instance.
	registerHandoffCreators(supervisor, creatorReg)

	// Wire agent replacement: atomic swap of canonical identity.
	// New agent was invisible to Guide during the traffic shift.
	// At swap time: assign canonical ID → unregister old → register new.
	supervisor.SetAgentReplacedCallback(func(oldID, _ string, newAgent handoff.HandoffableAgent) error {
		if setter, ok := newAgent.(interface{ SetCanonicalID(string) }); ok {
			setter.SetCanonicalID(oldID)
		}
		if replacement, ok := newAgent.(handoffScribeAgent); ok {
			if pod := replacement.AgentPod(); pod != nil {
				previous := pod.ReplaceScribe(replacement.ParentAgentType(), replacement)
				if previous != nil && previous != replacement {
					if retiring, ok := previous.(handoffRetiringScribe); ok {
						if err := retiring.RetireForHandoff(); err != nil {
							slog.Warn("retire replaced scribe", "agent_id", oldID, "error", err)
						}
					} else if err := previous.Stop(); err != nil {
						slog.Warn("stop replaced scribe", "agent_id", oldID, "error", err)
					}
				}
			}
		}
		inheritHandoffRuntimeBindings(containerReg, oldID, newAgent)
		g.UnregisterAgent(oldID)
		if router, ok := newAgent.(guide.AgentRouter); ok {
			seedRouterKnownAgents(g, router)
			_ = g.RegisterRouter(router)
			g.MarkAgentReady(oldID)
		}

		// Adopt the new container into the ActivationController so the
		// entry points to the live replacement, not the terminated old one.
		if activationCtrl != nil && containerReg != nil {
			containers := containerReg.ListByType(newAgent.AgentType())
			for _, c := range containers {
				if agent := c.Agent(); agent != nil && agent.AgentID() == newAgent.AgentID() {
					if _, err := activationCtrl.AdoptContainer(newAgent.AgentType(), c); err != nil {
						slog.Warn("adopt replacement container", "agent_type", newAgent.AgentType(), "error", err)
					}
					break
				}
			}
		}

		return nil
	})

	// Wire quality publisher: GP quality flows to the service registry.
	supervisor.SetQualityPublisher(func(agentID string, quality, stdDev float64) {
		serviceReg.UpdateQuality(agentID, quality, stdDev)
	})

	// Wire traffic shift callbacks.
	// During gradual shift the new agent stays unregistered with the Guide.
	// The bridge handles overlap routing internally.
	supervisor.SetTrafficShiftCallbacks(
		func(_ string, newAgent handoff.HandoffableAgent) {
			// New agent stays internal to bridge during overlap.
			serviceReg.UpdateWeight(newAgent.AgentID(), 0)
		},
		func(agentID string, weight float64) {
			serviceReg.UpdateWeight(agentID, weight)
		},
	)

	// Wire Guide's quality checker via adapter.
	g.SetServiceQualityRegistry(&serviceRegistryQualityAdapter{reg: serviceReg})

	if startErr := supervisor.Start(); startErr != nil {
		return nil
	}

	// Register all live handoff-capable agents — not just the bootstrap trio.
	registerAllHandoffContainers(supervisor, containerReg)
	registerHandoffAgent(supervisor, g)

	return supervisor
}

// serviceRegistryQualityAdapter adapts ServiceRegistry to the Guide's
// ServiceQualityChecker interface, avoiding circular imports.
type serviceRegistryQualityAdapter struct {
	reg *network.ServiceRegistry
}

func (a *serviceRegistryQualityAdapter) HasHealthyEndpoints(agentType string) bool {
	return a.reg.HasHealthyEndpoints(agentType)
}

func (a *serviceRegistryQualityAdapter) GetWeightedEndpoints(agentType string) []guide.QualityEndpoint {
	endpoints := a.reg.GetWeightedEndpoints(agentType)
	result := make([]guide.QualityEndpoint, len(endpoints))
	for i := range endpoints {
		result[i] = guide.QualityEndpoint{
			AgentID:   endpoints[i].AgentID,
			AgentType: endpoints[i].AgentType,
			Quality:   endpoints[i].Quality,
			StdDev:    endpoints[i].StdDev,
			Weight:    endpoints[i].Weight,
		}
	}
	return result
}

// handoffWALDir returns the WAL directory path under the user's home .sylk directory.
func handoffWALDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sylk", "handoff"), nil
}

// handoffBridgeSetter is implemented by agents that accept a handoff bridge.
type handoffBridgeSetter interface {
	SetHandoffBridge(bridge *handoff.HandoffBridge)
}

type handoffPodGetter interface {
	AgentPod() *agentShared.AgentPod
}

type handoffScribeAgent interface {
	agentShared.Scribe
	handoff.HandoffInjectable
	ParentAgentType() string
	AgentPod() *agentShared.AgentPod
}

type handoffRetiringScribe interface {
	RetireForHandoff() error
}

// registerHandoffAgent registers a single agent with the supervisor and
// sets the bridge on the agent if it supports it.
func registerHandoffAgent(supervisor *handoff.HandoffSupervisor, agent handoff.HandoffableAgent) {
	if supervisor == nil || agent == nil {
		return
	}
	bridge, err := supervisor.RegisterAgent(agent)
	if err != nil {
		return
	}
	if setter, ok := agent.(handoffBridgeSetter); ok {
		setter.SetHandoffBridge(bridge)
	}
}

func inheritHandoffRuntimeBindings(containerReg *container.ContainerRegistry, oldID string, newAgent handoff.HandoffableAgent) {
	if containerReg == nil || strings.TrimSpace(oldID) == "" || newAgent == nil {
		return
	}

	var oldAgent handoff.HandoffableAgent
	for _, ctr := range containerReg.All() {
		if ctr == nil || ctr.Agent() == nil {
			continue
		}
		handoffable, ok := ctr.Agent().(handoff.HandoffableAgent)
		if !ok {
			continue
		}
		if handoffable.AgentID() == oldID {
			oldAgent = handoffable
			break
		}
	}
	if oldAgent == nil {
		return
	}

	podSource, ok := oldAgent.(handoffPodGetter)
	if !ok {
		return
	}
	pod := podSource.AgentPod()
	if pod == nil {
		return
	}

	if setter, ok := newAgent.(agentPodSetter); ok {
		setter.SetAgentPod(pod)
	}
	if consumer, ok := newAgent.(versioning.FileAccessConsumer); ok {
		if fa := pod.FileAccessFor(newAgent.AgentType()); fa != nil {
			consumer.SetFileAccess(fa)
		}
	}
	if viewsConsumer, ok := newAgent.(versioning.WorkspaceViewsConsumer); ok {
		if views := pod.WorkspaceViewsFor(newAgent.AgentType()); views != nil {
			viewsConsumer.SetWorkspaceViews(views)
		}
	}
}

func registerHandoffCreators(supervisor *handoff.HandoffSupervisor, creatorReg *container.AgentCreatorRegistry) {
	if supervisor == nil || creatorReg == nil {
		return
	}
	for _, agentType := range creatorReg.Types() {
		agentType := agentType
		supervisor.Factory().RegisterCreator(agentType, func(ctx context.Context) (handoff.HandoffableAgent, error) {
			agent, err := creatorReg.Create(applyHandoffCreationContext(ctx, agentType), agentType)
			if err != nil {
				return nil, err
			}
			handoffable, ok := agent.(handoff.HandoffableAgent)
			if !ok {
				return nil, fmt.Errorf("agent %q does not implement handoff support", agentType)
			}
			return handoffable, nil
		})
	}
}

func applyHandoffCreationContext(ctx context.Context, agentType string) context.Context {
	metadata, ok := handoff.FactoryCreationMetadataFromContext(ctx)
	if !ok {
		return ctx
	}

	spec := container.ContainerSpec{
		AgentType: agentType,
		Labels:    make(map[string]string, 4),
	}
	if strings.TrimSpace(metadata.AgentID) != "" {
		spec.Labels["pipeline_worker_id"] = metadata.AgentID
	}
	if strings.TrimSpace(metadata.TaskID) != "" {
		spec.Labels["task_id"] = metadata.TaskID
		if strings.TrimSpace(metadata.TaskSlug) != "" {
			spec.Labels["task_slug"] = metadata.TaskSlug
		}
		return container.WithCreationContext(ctx, spec, container.PodID(metadata.TaskID))
	}
	if strings.TrimSpace(metadata.AgentType) != "" {
		spec.AgentType = metadata.AgentType
	}
	return container.WithCreationContext(ctx, spec, "")
}

func registerAllHandoffContainers(supervisor *handoff.HandoffSupervisor, reg *container.ContainerRegistry) {
	if supervisor == nil || reg == nil {
		return
	}
	for _, c := range reg.All() {
		registerHandoffContainer(supervisor, c)
	}
}

func registerHandoffContainer(supervisor *handoff.HandoffSupervisor, c *container.Container) {
	if supervisor == nil || c == nil || c.Agent() == nil {
		return
	}
	handoffable, ok := c.Agent().(handoff.HandoffableAgent)
	if !ok {
		return
	}
	registerHandoffAgent(supervisor, handoffable)
}

func unregisterHandoffContainer(supervisor *handoff.HandoffSupervisor, c *container.Container) {
	if supervisor == nil || c == nil || c.Agent() == nil {
		return
	}
	handoffable, ok := c.Agent().(handoff.HandoffableAgent)
	if !ok {
		return
	}
	_ = supervisor.UnregisterAgent(handoffable.AgentID())
	if setter, ok := handoffable.(handoffBridgeSetter); ok {
		setter.SetHandoffBridge(nil)
	}
}

type handoffManagedScribe struct {
	inner           *scribe.Scribe
	supervisorRef   *atomic.Pointer[handoff.HandoffSupervisor]
	logger          *slog.Logger
	parentAgentType string
	pod             *agentShared.AgentPod
	registry        *handoffManagedScribeRegistry
	newManaged      func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error)
	autoRegister    bool

	mu             sync.Mutex
	registeredSup  *handoff.HandoffSupervisor
	bridgeAssigned bool
}

func (s *handoffManagedScribe) Start() error {
	if err := s.inner.Start(); err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Track(s)
	}
	s.ensureHandoffRegistration()
	return nil
}

func (s *handoffManagedScribe) Stop() error {
	agentID := s.AgentID()
	if s.registry != nil {
		s.registry.DeleteIf(agentID, s)
	}

	s.mu.Lock()
	sup := s.registeredSup
	shouldUnregister := s.registeredSup != nil || s.bridgeAssigned
	s.registeredSup = nil
	s.bridgeAssigned = false
	s.mu.Unlock()

	if shouldUnregister {
		if sup == nil && s.supervisorRef != nil {
			sup = s.supervisorRef.Load()
		}
		if sup != nil {
			_ = sup.UnregisterAgent(agentID)
		}
		s.SetHandoffBridge(nil)
	}

	return s.inner.Stop()
}

func (s *handoffManagedScribe) RetireForHandoff() error {
	agentID := s.AgentID()
	if s.registry != nil {
		s.registry.DeleteIf(agentID, s)
	}
	s.mu.Lock()
	s.registeredSup = nil
	s.bridgeAssigned = false
	s.mu.Unlock()
	s.SetHandoffBridge(nil)
	return s.inner.Stop()
}

func (s *handoffManagedScribe) Feed(feed agentShared.ScribeFeed) {
	s.ensureHandoffRegistration()
	s.inner.Feed(feed)
}

func (s *handoffManagedScribe) AgentID() string { return s.inner.AgentID() }

func (s *handoffManagedScribe) AgentType() string { return s.inner.AgentType() }

func (s *handoffManagedScribe) Descriptor() handoff.AgentDescriptor { return s.inner.Descriptor() }

func (s *handoffManagedScribe) ExtractArchivableState() *handoff.ArchivableState {
	return s.inner.ExtractArchivableState()
}

func (s *handoffManagedScribe) Terminate(ctx context.Context) error { return s.Stop() }

func (s *handoffManagedScribe) InjectPreparedContext(pc *handoff.PreparedContext) error {
	return s.inner.InjectPreparedContext(pc)
}

func (s *handoffManagedScribe) SetCanonicalID(id string) {
	oldID := s.AgentID()
	s.inner.SetCanonicalID(id)
	if s.registry != nil {
		s.registry.Rename(oldID, s.AgentID(), s)
	}
}

func (s *handoffManagedScribe) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	s.mu.Lock()
	s.bridgeAssigned = bridge != nil
	s.mu.Unlock()
	s.inner.SetHandoffBridge(bridge)
}

func (s *handoffManagedScribe) SetAgentPod(pod *agentShared.AgentPod) {
	s.pod = pod
}

func (s *handoffManagedScribe) AgentPod() *agentShared.AgentPod {
	return s.pod
}

func (s *handoffManagedScribe) ParentAgentType() string {
	return s.parentAgentType
}

func (s *handoffManagedScribe) ensureHandoffRegistration() {
	if s == nil || !s.autoRegister || s.supervisorRef == nil {
		return
	}
	sup := s.supervisorRef.Load()
	if sup == nil {
		return
	}

	if s.registry != nil && s.newManaged != nil {
		agentType := s.AgentType()
		sup.Factory().RegisterCreator(agentType, func(ctx context.Context) (handoff.HandoffableAgent, error) {
			metadata, ok := handoff.FactoryCreationMetadataFromContext(ctx)
			if !ok || strings.TrimSpace(metadata.AgentID) == "" {
				return nil, fmt.Errorf("scribe handoff for %q missing source agent id", agentType)
			}
			source := s.registry.Get(metadata.AgentID)
			if source == nil {
				return nil, fmt.Errorf("no live scribe source found for agent id %q", metadata.AgentID)
			}
			replacement, err := source.newManaged(source.parentAgentType, source.logger, false)
			if err != nil {
				return nil, err
			}
			replacement.SetAgentPod(source.AgentPod())
			if err := replacement.Start(); err != nil {
				return nil, err
			}
			return replacement, nil
		})
	}

	s.mu.Lock()
	if s.registeredSup != nil || s.bridgeAssigned {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	registerHandoffAgent(sup, s)

	s.mu.Lock()
	if s.bridgeAssigned {
		s.registeredSup = sup
	}
	s.mu.Unlock()
}

type handoffManagedScribeRegistry struct {
	mu   sync.Mutex
	byID map[string]*handoffManagedScribe
}

func newHandoffManagedScribeRegistry() *handoffManagedScribeRegistry {
	return &handoffManagedScribeRegistry{
		byID: make(map[string]*handoffManagedScribe),
	}
}

func (r *handoffManagedScribeRegistry) Track(s *handoffManagedScribe) {
	if r == nil || s == nil {
		return
	}
	id := strings.TrimSpace(s.AgentID())
	if id == "" {
		return
	}
	r.mu.Lock()
	r.byID[id] = s
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) DeleteIf(id string, target *handoffManagedScribe) {
	if r == nil || target == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	r.mu.Lock()
	if existing := r.byID[id]; existing == target {
		delete(r.byID, id)
	}
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) Rename(oldID, newID string, target *handoffManagedScribe) {
	if r == nil || target == nil {
		return
	}
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	r.mu.Lock()
	if oldID != "" {
		if existing := r.byID[oldID]; existing == target {
			delete(r.byID, oldID)
		}
	}
	if newID != "" {
		r.byID[newID] = target
	}
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) Get(id string) *handoffManagedScribe {
	if r == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id]
}

// buildScribeFactory creates a ScribeFactory that produces Gemini 3 Flash
// sidecars. Returns nil if the Google provider cannot be created (non-fatal
// — pipelines run without Scribes).
func buildScribeFactory(
	ctx context.Context,
	googleGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	bus guide.EventBus,
	scope *concurrency.GoroutineScope,
	forest agentShared.MemoryForestService,
	supervisorRef *atomic.Pointer[handoff.HandoffSupervisor],
) agentShared.ScribeFactory {
	scribeCfg := providers.DefaultGoogleConfig()
	scribeCfg.Model = "gemini-3-flash-preview"
	scribeCfg.BaseConfig.MaxTokens = 512
	scribeCfg.AuthMode = registryAuthMethod(authRegistry, "google", scribeCfg.AuthMode)

	scribeProvider, err := providers.NewGoogleProvider(ctx, scribeCfg)
	if err != nil {
		slog.Warn("scribe google provider creation failed — scribes disabled", "error", err)
		return nil
	}

	wrapped := googleGw.WrapProvider(scribeProvider, gateway.PriorityBackground)
	registry := newHandoffManagedScribeRegistry()
	var newManaged func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error)
	newManaged = func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error) {
		managed := &handoffManagedScribe{
			inner: scribe.New(scribe.Config{
				ParentAgentType: parentAgentType,
				Provider:        wrapped,
				Model:           "gemini-3-flash-preview",
				Bus:             bus,
				Scope:           scope,
				Logger:          logger,
				Forest:          forest,
			}),
			supervisorRef:   supervisorRef,
			logger:          logger,
			parentAgentType: parentAgentType,
			registry:        registry,
			autoRegister:    autoRegister,
		}
		managed.newManaged = newManaged
		return managed, nil
	}
	return func(parentAgentType string, logger *slog.Logger) (agentShared.Scribe, error) {
		return newManaged(parentAgentType, logger, true)
	}
}

// agentPodSetter is implemented by agents that accept an AgentPod.
type agentPodSetter interface {
	SetAgentPod(pod *agentShared.AgentPod)
}

// wireGlobalAgentPod creates a single-member AgentPod for a global agent
// (architect, inspector, tester) and sets it on the agent. The pod manages
// Scribe sidecars for the agent. Calls PreActivate to start Scribes.
func wireGlobalAgentPod(
	agent agentPodSetter,
	agentType string,
	sessionID string,
	scribeFactory agentShared.ScribeFactory,
	activator guide.PodActivator,
	activityPub events.ActivityPublisher,
	logger *slog.Logger,
) {
	pod := agentShared.NewAgentPod(agentShared.AgentPodConfig{
		PodID:         agentType + "-global-pod",
		SessionID:     bootstrapSessionID(sessionID),
		Activator:     activator,
		ActivityPub:   activityPub,
		Logger:        logger,
		MemberTypes:   []string{agentType},
		DisplayNames:  map[string]string{agentType: agentType},
		ScribeFactory: scribeFactory,
	})
	pod.PreActivate(context.Background())
	agent.SetAgentPod(pod)
}

// ---------------------------------------------------------------------------
// Guardian observability adapters
// ---------------------------------------------------------------------------

// activationMetricsAdapter adapts *activation.ActivationMetrics to
// guardian.ActivationMetricsQuerier.
type activationMetricsAdapter struct {
	m *activation.ActivationMetrics
}

func (a *activationMetricsAdapter) Snapshot() map[string]int64 {
	s := a.m.Snapshot()
	return map[string]int64{
		"activations_total":     s.ActivationsTotal,
		"cold_starts":           s.ColdStarts,
		"cool_starts":           s.CoolStarts,
		"warm_starts":           s.WarmStarts,
		"hot_hits":              s.HotHits,
		"coalesced_requests":    s.CoalescedRequests,
		"demotions_to_warm":     s.DemotionsToWarm,
		"demotions_to_cool":     s.DemotionsToCool,
		"demotions_to_cold":     s.DemotionsToCold,
		"predictor_hits":        s.PredictorHits,
		"predictor_misses":      s.PredictorMisses,
		"evictions_by_pressure": s.EvictionsByPressure,
	}
}

// daemonQuerierAdapter adapts *daemon.DaemonSetController to
// guardian.DaemonQuerier.
type daemonQuerierAdapter struct {
	dc *daemon.DaemonSetController
}

func (a *daemonQuerierAdapter) Status() []guardian.DaemonStatusSnapshot {
	statuses := a.dc.Status()
	out := make([]guardian.DaemonStatusSnapshot, len(statuses))
	for i, s := range statuses {
		out[i] = guardian.DaemonStatusSnapshot{
			Name:         s.Name,
			Running:      s.Running,
			ContainerID:  string(s.ContainerID),
			RestartCount: s.RestartCount,
			Healthy:      s.Healthy,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// VFS adapters — aggregate per-session VFS stats for Guardian
// ---------------------------------------------------------------------------

// orchestratorCVSAdapter implements guardian.CVSQuerier by aggregating live
// SessionVFS stats across all of the Orchestrator's active sessions.
type orchestratorCVSAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *orchestratorCVSAdapter) Stats() guardian.CVSStatsSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return guardian.CVSStatsSnapshot{}
	}
	var agg guardian.CVSStatsSnapshot
	for _, svfs := range orch.AllSessionVFS() {
		s := svfs.Stats()
		agg.TotalFiles += s.TrackedFiles
		agg.TotalVersions += s.TotalVersions
		agg.TotalOperations += s.TotalOperations
		agg.ActivePipelines += s.ActivePipelines
		agg.ActiveVariants += s.ActiveVariants
		agg.ActiveLocks += s.ActiveLocks
		agg.ActiveSubscribers += s.ActiveSubscribers
		agg.CurrentVersion = s.CurrentVersion.String()
		agg.WALEntries += s.WALEntries
	}
	return agg
}

// orchestratorVFSAdapter implements guardian.VFSManagerQuerier by
// aggregating VFS manager stats across all active sessions.
type orchestratorVFSAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *orchestratorVFSAdapter) Stats() guardian.VFSManagerSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return guardian.VFSManagerSnapshot{}
	}
	var agg guardian.VFSManagerSnapshot
	for _, svfs := range orch.AllSessionVFS() {
		s := svfs.VFSManager().Stats()
		agg.ActiveVFSes += s.ActiveVFSes
		agg.VariantGroups += s.VariantGroups
		agg.ActiveSessions += s.ActiveSessions
		agg.TotalPipelines += s.TotalPipelines
	}
	return agg
}

// ---------------------------------------------------------------------------
// Extended observability adapters — Pipeline, Gateway, Knowledge, Concurrency
// ---------------------------------------------------------------------------

// pipelineQuerierAdapter implements guardian.PipelineQuerier by delegating
// to the Orchestrator's summary and DAG snapshot methods.
type pipelineQuerierAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *pipelineQuerierAdapter) Summary() (*guardian.PipelineSummarySnapshot, error) {
	orch := a.orchRef.Load()
	if orch == nil {
		return &guardian.PipelineSummarySnapshot{}, nil
	}
	s, err := orch.GetSummary(context.Background())
	if err != nil {
		return nil, err
	}
	return &guardian.PipelineSummarySnapshot{
		Overview:        s.Overview,
		ActiveWorkflows: s.Workflows.Running,
		TotalWorkflows:  s.Workflows.Total,
		ActiveTasks:     s.Tasks.Running,
		CompletedTasks:  s.Tasks.Completed,
		FailedTasks:     s.Tasks.Failed,
		TotalTasks:      s.Tasks.Total,
	}, nil
}

func (a *pipelineQuerierAdapter) DAGSnapshots(limit int) []guardian.DAGSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return nil
	}
	snaps := orch.GetDAGSnapshots(limit)
	out := make([]guardian.DAGSnapshot, len(snaps))
	for i, s := range snaps {
		out[i] = guardian.DAGSnapshot{
			ID:           s.ID,
			PlanID:       s.PlanID,
			State:        s.State,
			CurrentLayer: s.CurrentLayer,
			TotalLayers:  s.TotalLayers,
			Progress:     s.Progress,
			NodesFailed:  s.NodesFailed,
			Duration:     s.Duration,
		}
	}
	return out
}

// gatewayQuerierAdapter implements guardian.GatewayQuerier by aggregating
// metrics from all provider gateways.
type gatewayQuerierAdapter struct {
	gateways []*gateway.ProviderGateway
}

func (a *gatewayQuerierAdapter) AllMetrics() map[string]guardian.GatewayMetricsSnapshot {
	out := make(map[string]guardian.GatewayMetricsSnapshot, len(a.gateways))
	for _, gw := range a.gateways {
		m := gw.Metrics()
		name := gw.Name()
		out[name] = guardian.GatewayMetricsSnapshot{
			Name:        name,
			Admitted:    m.Admitted,
			Rejected:    m.Rejected,
			Queued:      m.Queued,
			Completed:   m.Completed,
			RateLimited: m.RateLimited,
			Errors429:   m.Errors429,
			Inflight:    m.Inflight,
			TotalWaitMs: m.TotalWaitNs / 1_000_000,
		}
	}
	return out
}

// knowledgeQuerierAdapter implements guardian.KnowledgeQuerier by
// delegating to *knowledge.KnowledgeStore and its coordinator.
type knowledgeQuerierAdapter struct {
	store *knowledge.KnowledgeStore
}

func (a *knowledgeQuerierAdapter) Status() guardian.KnowledgeStatusSnapshot {
	level := a.store.Level()
	labels := [3]string{"none", "partial", "full"}
	label := labels[0]
	if int(level) < len(labels) {
		label = labels[level]
	}

	var searchers []string
	if coord := a.store.Coordinator(); coord != nil {
		searchers = coord.ReadySearchers()
	}

	return guardian.KnowledgeStatusSnapshot{
		ReadinessLevel: int(level),
		ReadinessLabel: label,
		ReadySearchers: searchers,
	}
}

func (a *knowledgeQuerierAdapter) QueryMetrics() guardian.KnowledgeQueryMetrics {
	coord := a.store.Coordinator()
	if coord == nil {
		return guardian.KnowledgeQueryMetrics{}
	}
	m := coord.GetAverageMetrics()
	if m == nil {
		return guardian.KnowledgeQueryMetrics{}
	}
	return guardian.KnowledgeQueryMetrics{
		TextLatencyMs:       m.TextLatency.Milliseconds(),
		SemanticLatencyMs:   m.SemanticLatency.Milliseconds(),
		GraphLatencyMs:      m.GraphLatency.Milliseconds(),
		FusionLatencyMs:     m.FusionLatency.Milliseconds(),
		TotalLatencyMs:      m.TotalLatency.Milliseconds(),
		TextContributed:     m.TextContributed,
		SemanticContributed: m.SemanticContributed,
		GraphContributed:    m.GraphContributed,
	}
}

func (a *knowledgeQuerierAdapter) IngestionProgress() (indexed, total int64) {
	w := a.store.BackgroundWaiter()
	if w == nil {
		return 0, 0
	}
	return w.Progress()
}

// concurrencyQuerierAdapter implements guardian.ConcurrencyQuerier by
// delegating to *concurrency.GoroutineBudget.
type concurrencyQuerierAdapter struct {
	budget *concurrency.GoroutineBudget
}

func (a *concurrencyQuerierAdapter) GoroutineStats() guardian.GoroutineBudgetSnapshot {
	return guardian.GoroutineBudgetSnapshot{
		TotalActive: a.budget.TotalActive(),
		SystemLimit: a.budget.SystemLimit(),
		AgentCount:  a.budget.AgentCount(),
	}
}

func (a *concurrencyQuerierAdapter) LLMGateStats() guardian.LLMGateSnapshot {
	return guardian.LLMGateSnapshot{}
}

// ---------------------------------------------------------------------------
// Cost calculator — derives USD-cents from model pricing
// ---------------------------------------------------------------------------

// buildCostCalculator returns a CostCalculator that looks up per-model
// pricing from a static table. Returns 0 for unknown models. The table
// is built once at boot from a snapshot of known model prices.
func buildCostCalculator() guardian.CostCalculator {
	// Static pricing table (USD per million tokens).
	// Updated at compile-time; runtime registration is not needed because
	// the set of models used by a running instance is fixed at build.
	type pricing struct{ input, output float64 }
	prices := map[string]pricing{
		// Anthropic
		"claude-opus-4-6":           {15.0, 75.0},
		"claude-sonnet-4-6":         {3.0, 15.0},
		"claude-haiku-4-5-20251001": {0.80, 4.0},
		// OpenAI
		"gpt-5.4-pro": {2.50, 10.0},
		// Google
		"gemini-3.1-pro-preview": {1.25, 5.0},
		"gemini-3-flash":         {0.075, 0.30},
		"gemini-2.5-pro":         {1.25, 5.0},
		"gemini-2.5-flash":       {0.075, 0.30},
	}

	return func(model string, usage *providers.Usage) int64 {
		p, ok := prices[model]
		if !ok || usage == nil {
			return 0
		}
		// Cache reads cost 90% less at Anthropic, 50% less at OpenAI/Google.
		// Subtract cache-read tokens from full-price input and price separately.
		fullPriceInput := int64(usage.InputTokens - usage.CacheReadTokens)
		cacheReadInput := int64(usage.CacheReadTokens)
		cacheWriteInput := int64(usage.CacheWriteTokens)

		costUSD := float64(fullPriceInput) / 1_000_000 * p.input
		costUSD += float64(cacheReadInput) / 1_000_000 * (p.input * 0.1)   // 90% discount
		costUSD += float64(cacheWriteInput) / 1_000_000 * (p.input * 1.25) // 25% surcharge
		costUSD += float64(usage.OutputTokens) / 1_000_000 * p.output
		// Convert to cents, rounding down (accumulation converges).
		return int64(costUSD * 100)
	}
}
