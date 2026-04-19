package guardian

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/versioning"
)

// Default configuration values.
const (
	DefaultGuardianModel       = "gpt-5.4-pro"
	DefaultMaxOutputTokens     = 8192
	DefaultMaxToolRuns         = 32
	DefaultCheckpointInterval  = 10 * time.Minute
	DefaultDirtyThreshold      = 20
	DefaultHealthCheckInterval = 15 * time.Second
	DefaultAgentTimeout        = 5 * time.Minute
	DefaultApprovalTTL         = 2 * time.Minute
	DefaultConversationBuffer  = 10

	// Budget defaults — soft limits that trigger warnings at 80% and
	// findings at 100%. Zero means unlimited (no enforcement).
	DefaultTokenBudget int64 = 5_000_000 // ~5M tokens per session
	DefaultCostBudget  int64 = 2_000     // 2000 cents = $20 USD per session

	// Quarantine buffer defaults.
	DefaultQuarantineMaxItems       = 256
	DefaultQuarantineMaxBytes int64 = 64 * 1024 * 1024 // 64 MiB
)

// Config holds configuration for the Guardian agent.
type Config struct {
	// Canonical agent ID. If empty, defaults to "guardian".
	ID        string
	SessionID string
	Logger    *slog.Logger

	// External dependencies.
	ActivityPub    events.ActivityPublisher
	FileAccess     versioning.FileAccess
	WorkspaceViews versioning.WorkspaceViewAccess
	GitBus         *git.GitBus
	GitWatcher     *git.StatusWatcher
	Sanitizer      *security.SecretSanitizer

	// Git Safety.
	CheckpointInterval time.Duration // Default: 10min
	DirtyThreshold     int           // Default: 20 files
	ProtectedBranches  []string      // Default: ["main","master","release/*","production"]

	// Content Validation.
	InjectionScanEnabled  bool // Default: true
	CredentialScanEnabled bool // Default: true

	// Health Monitoring.
	HealthCheckInterval time.Duration // Default: 15s
	AgentTimeoutDefault time.Duration // Default: 5min
	TokenBudget         int64         // 0 = unlimited
	CostBudget          int64         // 0 = unlimited (USD cents)

	// Diff Gate.
	DiffReviewEnabled      bool     // Default: true
	SuspiciousDiffPatterns []string // Additional regex patterns

	// Observability dependencies (optional).
	ActivationController ActivationQuerier
	ActivationMetrics    ActivationMetricsQuerier
	DaemonController     DaemonQuerier
	CVS                  CVSQuerier
	VFSManager           VFSManagerQuerier
	Pipeline             PipelineQuerier
	Gateway              GatewayQuerier
	Knowledge            KnowledgeQuerier
	Concurrency          ConcurrencyQuerier

	// Tool dispatch loop.
	MaxToolRuns int // Safety ceiling for tool-call turns. Context governor is the real budget. Defaults to DefaultMaxToolRuns (32).

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// Forest exposes Memory Forest governance precedent skills.
	Forest shared.MemoryForestService

	// Factory mints the Guardian's AgentIdentity at New() time. Required —
	// every LLM dispatch goes through the provider gateway which rejects
	// requests without typed identity on ctx.
	Factory *identity.Factory
}

// applyDefaults fills in zero values with sensible defaults.
func applyDefaults(cfg Config) Config {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.CheckpointInterval == 0 {
		cfg.CheckpointInterval = DefaultCheckpointInterval
	}
	if cfg.DirtyThreshold == 0 {
		cfg.DirtyThreshold = DefaultDirtyThreshold
	}
	if len(cfg.ProtectedBranches) == 0 {
		cfg.ProtectedBranches = []string{"main", "master", "release/*", "production"}
	}
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = DefaultHealthCheckInterval
	}
	if cfg.AgentTimeoutDefault == 0 {
		cfg.AgentTimeoutDefault = DefaultAgentTimeout
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.TokenBudget == 0 {
		cfg.TokenBudget = DefaultTokenBudget
	}
	if cfg.CostBudget == 0 {
		cfg.CostBudget = DefaultCostBudget
	}
	// Content validation enabled by default.
	// Bool zero-value is false, so we use a helper to detect "not set".
	// The Config struct fields default to true in the constructor.
	return cfg
}

// validate checks that required dependencies are present.
func (c *Config) validate() error {
	if c.ActivityPub == nil {
		return fmt.Errorf("guardian: ActivityPub is required")
	}
	// Factory is optional at construction time because the Guardian
	// is a daemon that boots before the session (and thus the
	// session-scoped Factory) exists. Callers wire it in after phase4
	// via SetIdentityFactory; if still unset when a forwarded request
	// arrives, dispatch fails loudly via ErrDispatchIdentityMissing.
	return nil
}
