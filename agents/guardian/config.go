package guardian

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/versioning"
)

// Default configuration values.
const (
	DefaultGuardianModel       = "gpt-5.3-codex"
	DefaultMaxOutputTokens     = 8192
	DefaultMaxToolRuns         = 32
	DefaultCheckpointInterval  = 10 * time.Minute
	DefaultDirtyThreshold      = 20
	DefaultHealthCheckInterval = 15 * time.Second
	DefaultAgentTimeout        = 5 * time.Minute
	DefaultApprovalTTL         = 2 * time.Minute
	DefaultConversationBuffer  = 10
)

// Config holds configuration for the Guardian agent.
type Config struct {
	// Canonical agent ID. If empty, defaults to "guardian".
	ID        string
	SessionID string
	Logger    *slog.Logger

	// External dependencies.
	ActivityPub events.ActivityPublisher
	FileAccess  versioning.FileAccess
	GitBus      *git.GitBus
	GitWatcher  *git.StatusWatcher
	Sanitizer   *security.SecretSanitizer

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
	return nil
}
