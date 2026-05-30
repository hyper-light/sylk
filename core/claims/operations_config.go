package claims

import (
	"runtime"
	"strings"
	"time"
)

type ClaimsDurabilityTier string
type WALSyncMode string

const (
	ClaimsDurabilityTierEventual ClaimsDurabilityTier = "eventual"
	ClaimsDurabilityTierDurable  ClaimsDurabilityTier = "durable"
	ClaimsDurabilityTierStrict   ClaimsDurabilityTier = "strict"

	WALSyncModeNever      WALSyncMode = "never"
	WALSyncModeAppend     WALSyncMode = "append"
	WALSyncModeAppendSync WALSyncMode = "append_and_snapshot"
)

const (
	operationsInboxSlotsPerCore       = 256
	operationsExpectedFanIn           = 12
	operationsMessagesPerTurn         = 15
	operationsReplicasPerAgent        = 8
	operationsBurstSafetyFactor       = 2
	operationsDefaultServiceQueue     = 64
	operationsDefaultServiceWorkers   = 1
	operationsDefaultValidatorWorkers = 1
	operationsDefaultOutboxBatch      = 128
	operationsDefaultOutboxRepair     = 256
	operationsDefaultReplayPreview    = 80
	operationsDefaultAuditFindings    = 64
	operationsDefaultOrphanMin        = 16
	operationsDefaultOrphanDivisor    = 4

	OperationsDefaultOutboxLease      = 30 * time.Second
	OperationsDefaultShutdownGrace    = 5 * time.Second
	OperationsDefaultShutdownDeadline = 30 * time.Second
	OperationsDefaultAuditInterval    = time.Minute
	OperationsDefaultAuditDeadline    = 5 * time.Second
)

type ClaimsOperationsConfig struct {
	Durability   ClaimsDurabilityConfig
	Budgets      ClaimsOperationsBudgets
	Backpressure ClaimsBackpressureConfig
	Audit        ClaimsOperationsAuditConfig
	Warnings     []string
}

type ClaimsDurabilityConfig struct {
	Tier        ClaimsDurabilityTier
	WALSyncMode WALSyncMode
}

type ClaimsOperationsBudgets struct {
	InboxSubscriptionQueueCap  int
	ServiceQueueCapacity       int
	ServiceConcurrency         int
	ValidatorConcurrency       int
	ContinuationOrphanLimit    int
	OutboxProjectionBatchLimit int
	OutboxRepairLimit          int
	ReplayErrorPreviewBytes    int
	OutboxLease                time.Duration
	ShutdownGracePeriod        time.Duration
	ShutdownHardDeadline       time.Duration
	AuditFindingLimit          int
	AuditInterval              time.Duration
	AuditDeadline              time.Duration
}

type ClaimsBackpressureConfig struct {
	NearCapacityRatio float64
	SummaryLimit      int
}

type ClaimsOperationsAuditConfig struct {
	Enabled  bool
	Disabled bool
}

type ClaimsOperationsBudgetSnapshot struct {
	InboxSubscriptionQueueCap  int           `json:"inbox_subscription_queue_cap"`
	ServiceQueueCapacity       int           `json:"service_queue_capacity"`
	ServiceConcurrency         int           `json:"service_concurrency"`
	ValidatorConcurrency       int           `json:"validator_concurrency"`
	ContinuationOrphanLimit    int           `json:"continuation_orphan_limit"`
	OutboxProjectionBatchLimit int           `json:"outbox_projection_batch_limit"`
	OutboxRepairLimit          int           `json:"outbox_repair_limit"`
	ReplayErrorPreviewBytes    int           `json:"replay_error_preview_bytes"`
	OutboxLease                time.Duration `json:"outbox_lease"`
	ShutdownGracePeriod        time.Duration `json:"shutdown_grace_period"`
	ShutdownHardDeadline       time.Duration `json:"shutdown_hard_deadline"`
	AuditFindingLimit          int           `json:"audit_finding_limit"`
	AuditInterval              time.Duration `json:"audit_interval"`
	AuditDeadline              time.Duration `json:"audit_deadline"`
	Warnings                   []string      `json:"warnings,omitempty"`
}

func DefaultClaimsOperationsConfig() ClaimsOperationsConfig {
	return NormalizeClaimsOperationsConfig(ClaimsOperationsConfig{})
}

func NormalizeClaimsOperationsConfig(cfg ClaimsOperationsConfig) ClaimsOperationsConfig {
	out := cfg
	out.Warnings = nil
	out.Durability = normalizeClaimsDurabilityConfig(out.Durability)
	out.Budgets = normalizeClaimsOperationsBudgets(out.Budgets, &out.Warnings)
	out.Backpressure = normalizeClaimsBackpressureConfig(out.Backpressure, &out.Warnings)
	out.Audit = normalizeClaimsOperationsAuditConfig(out.Audit)
	return out
}

func ClaimsOperationsBudgetSnapshotFromConfig(cfg ClaimsOperationsConfig) ClaimsOperationsBudgetSnapshot {
	cfg = NormalizeClaimsOperationsConfig(cfg)
	return ClaimsOperationsBudgetSnapshot{
		InboxSubscriptionQueueCap:  cfg.Budgets.InboxSubscriptionQueueCap,
		ServiceQueueCapacity:       cfg.Budgets.ServiceQueueCapacity,
		ServiceConcurrency:         cfg.Budgets.ServiceConcurrency,
		ValidatorConcurrency:       cfg.Budgets.ValidatorConcurrency,
		ContinuationOrphanLimit:    cfg.Budgets.ContinuationOrphanLimit,
		OutboxProjectionBatchLimit: cfg.Budgets.OutboxProjectionBatchLimit,
		OutboxRepairLimit:          cfg.Budgets.OutboxRepairLimit,
		ReplayErrorPreviewBytes:    cfg.Budgets.ReplayErrorPreviewBytes,
		OutboxLease:                cfg.Budgets.OutboxLease,
		ShutdownGracePeriod:        cfg.Budgets.ShutdownGracePeriod,
		ShutdownHardDeadline:       cfg.Budgets.ShutdownHardDeadline,
		AuditFindingLimit:          cfg.Budgets.AuditFindingLimit,
		AuditInterval:              cfg.Budgets.AuditInterval,
		AuditDeadline:              cfg.Budgets.AuditDeadline,
		Warnings:                   append([]string(nil), cfg.Warnings...),
	}
}

func normalizeClaimsDurabilityConfig(cfg ClaimsDurabilityConfig) ClaimsDurabilityConfig {
	cfg.Tier = ClaimsDurabilityTier(strings.TrimSpace(string(cfg.Tier)))
	switch cfg.Tier {
	case ClaimsDurabilityTierEventual, ClaimsDurabilityTierDurable, ClaimsDurabilityTierStrict:
	default:
		cfg.Tier = ClaimsDurabilityTierDurable
	}
	cfg.WALSyncMode = WALSyncMode(strings.TrimSpace(string(cfg.WALSyncMode)))
	if cfg.WALSyncMode == "" {
		cfg.WALSyncMode = syncModeForDurabilityTier(cfg.Tier)
	}
	switch cfg.WALSyncMode {
	case WALSyncModeNever, WALSyncModeAppend, WALSyncModeAppendSync:
	default:
		cfg.WALSyncMode = syncModeForDurabilityTier(cfg.Tier)
	}
	return cfg
}

func syncModeForDurabilityTier(tier ClaimsDurabilityTier) WALSyncMode {
	switch tier {
	case ClaimsDurabilityTierEventual:
		return WALSyncModeNever
	case ClaimsDurabilityTierStrict:
		return WALSyncModeAppendSync
	default:
		return WALSyncModeAppend
	}
}

func normalizeClaimsOperationsBudgets(b ClaimsOperationsBudgets, warnings *[]string) ClaimsOperationsBudgets {
	b.InboxSubscriptionQueueCap = positiveBudget("inbox_subscription_queue_cap", b.InboxSubscriptionQueueCap, defaultInboxSubscriptionQueueCap(), warnings)
	b.ServiceQueueCapacity = positiveBudget("service_queue_capacity", b.ServiceQueueCapacity, operationsDefaultServiceQueue, warnings)
	b.ServiceConcurrency = positiveBudget("service_concurrency", b.ServiceConcurrency, operationsDefaultServiceWorkers, warnings)
	b.ValidatorConcurrency = positiveBudget("validator_concurrency", b.ValidatorConcurrency, operationsDefaultValidatorWorkers, warnings)
	b.ContinuationOrphanLimit = positiveBudget("continuation_orphan_limit", b.ContinuationOrphanLimit, defaultContinuationOrphanLimit(b.InboxSubscriptionQueueCap), warnings)
	b.OutboxProjectionBatchLimit = positiveBudget("outbox_projection_batch_limit", b.OutboxProjectionBatchLimit, operationsDefaultOutboxBatch, warnings)
	b.OutboxRepairLimit = positiveBudget("outbox_repair_limit", b.OutboxRepairLimit, operationsDefaultOutboxRepair, warnings)
	b.ReplayErrorPreviewBytes = positiveBudget("replay_error_preview_bytes", b.ReplayErrorPreviewBytes, operationsDefaultReplayPreview, warnings)
	b.AuditFindingLimit = positiveBudget("audit_finding_limit", b.AuditFindingLimit, operationsDefaultAuditFindings, warnings)
	b.OutboxLease = positiveDuration("outbox_lease", b.OutboxLease, OperationsDefaultOutboxLease, warnings)
	b.ShutdownGracePeriod = positiveDuration("shutdown_grace_period", b.ShutdownGracePeriod, OperationsDefaultShutdownGrace, warnings)
	b.ShutdownHardDeadline = positiveDuration("shutdown_hard_deadline", b.ShutdownHardDeadline, OperationsDefaultShutdownDeadline, warnings)
	b.AuditInterval = positiveDuration("audit_interval", b.AuditInterval, OperationsDefaultAuditInterval, warnings)
	b.AuditDeadline = positiveDuration("audit_deadline", b.AuditDeadline, OperationsDefaultAuditDeadline, warnings)
	return b
}

func normalizeClaimsBackpressureConfig(cfg ClaimsBackpressureConfig, warnings *[]string) ClaimsBackpressureConfig {
	if cfg.SummaryLimit <= 0 {
		appendOperationWarning(warnings, "backpressure_summary_limit normalized to audit finding limit")
		cfg.SummaryLimit = operationsDefaultAuditFindings
	}
	if cfg.NearCapacityRatio <= 0 || cfg.NearCapacityRatio >= 1 {
		appendOperationWarning(warnings, "near_capacity_ratio normalized to bounded default")
		cfg.NearCapacityRatio = derivedNearCapacityRatio()
	}
	return cfg
}

func normalizeClaimsOperationsAuditConfig(cfg ClaimsOperationsAuditConfig) ClaimsOperationsAuditConfig {
	if cfg.Disabled {
		cfg.Enabled = false
		return cfg
	}
	cfg.Enabled = true
	return cfg
}

func defaultInboxSubscriptionQueueCap() int {
	procs := runtime.GOMAXPROCS(0)
	if procs < 1 {
		procs = 1
	}
	floor := procs * operationsInboxSlotsPerCore
	fanIn := operationsExpectedFanIn * operationsMessagesPerTurn * operationsReplicasPerAgent * operationsBurstSafetyFactor
	if floor > fanIn {
		return floor
	}
	return fanIn
}

func defaultContinuationOrphanLimit(inboxCap int) int {
	limit := inboxCap / operationsDefaultOrphanDivisor
	if limit < operationsDefaultOrphanMin {
		return operationsDefaultOrphanMin
	}
	return limit
}

func derivedNearCapacityRatio() float64 {
	return float64(operationsBurstSafetyFactor*operationsReplicasPerAgent-1) / float64(operationsBurstSafetyFactor*operationsReplicasPerAgent)
}

func positiveBudget(name string, value, fallback int, warnings *[]string) int {
	if value > 0 {
		return value
	}
	appendOperationWarning(warnings, name+" normalized to bounded default")
	return fallback
}

func positiveDuration(name string, value, fallback time.Duration, warnings *[]string) time.Duration {
	if value > 0 {
		return value
	}
	appendOperationWarning(warnings, name+" normalized to bounded default")
	return fallback
}

func appendOperationWarning(warnings *[]string, msg string) {
	if warnings == nil || strings.TrimSpace(msg) == "" {
		return
	}
	*warnings = append(*warnings, msg)
}

func (mode WALSyncMode) syncsWALOnAppend() bool {
	return mode == WALSyncModeAppend || mode == WALSyncModeAppendSync
}
