package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type ClaimsOperationsSkillConfig struct {
	BoardProvider       BoardProvider
	BoardRegistry       *SessionBoardRegistry
	InboxRegistry       *SessionInboxRegistry
	ServiceDispatchers  *ServiceDispatcherRegistry
	ValidatorDispatcher *BoardValidatorDispatcher
	ParticipantRegistry *ParticipantRegistry
	TypeRegistry        *TypeRegistry
	Metrics             ClaimsMetricsSink
}

type ClaimsOperationsStatus struct {
	Telemetry     InfrastructureTelemetrySnapshot `json:"telemetry"`
	QueueHealth   ClaimsOperationsQueueHealth     `json:"queue_health"`
	ResourceAudit OperationsAuditResult           `json:"resource_audit"`
}

type ClaimsOperationsQueueHealth struct {
	Inboxes     SessionInboxRegistrySnapshot      `json:"inboxes"`
	Dispatchers ServiceDispatcherRegistrySnapshot `json:"dispatchers"`
	Validators  ValidatorDispatcherStats          `json:"validators"`
}

func ClaimsOperationsStatusSkill(cfg ClaimsOperationsSkillConfig) *skills.Skill {
	return skills.NewSkill("claims_operations_status").
		Description("Inspect claims operations state from board-derived projections: telemetry snapshot, projection health, queue health, cancellation/recovery-adjacent resource state, and bounded audit findings. Read-only.").
		Domain("claims").
		Keywords("claims", "operations", "health", "audit", "queues", "recovery", "cancellation", "boot").
		Priority(62).
		BoolParam("include_audit", "When true, run the bounded claims operations audit and return findings.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireOperationsBoard(cfg.BoardProvider)
			if err != nil {
				return nil, err
			}
			var params struct {
				IncludeAudit bool `json:"include_audit"`
			}
			if len(input) != 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}
			return buildClaimsOperationsStatus(ctx, cfg, board, params.IncludeAudit)
		}).
		Build()
}

func ClaimsOperationsRepairSkill(cfg ClaimsOperationsSkillConfig) *skills.Skill {
	return skills.NewSkill("claims_operations_repair").
		Description("Run bounded claims operations repair surfaces. Dry-run is the default. Non-dry-run repairs require authorized=true and write report testaments when emit_report=true.").
		Domain("claims").
		Keywords("claims", "operations", "repair", "recovery", "outbox", "projection").
		Priority(31).
		EnumParam("operation", "repair operation to run", []string{"outbox_projection", "resource_audit"}, true).
		BoolParam("dry_run", "When true, report what would happen without mutating projection state. Defaults true.", false).
		BoolParam("authorized", "Required for non-dry-run operation.", false).
		BoolParam("emit_report", "When true, commit a report testament for state-changing repair/audit evidence.", false).
		IntParam("limit", "Maximum records/findings to inspect. Zero uses bounded defaults.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireOperationsBoard(cfg.BoardProvider)
			if err != nil {
				return nil, err
			}
			params, err := parseClaimsOperationsRepairParams(input)
			if err != nil {
				return nil, err
			}
			if !params.DryRun && !params.Authorized {
				return nil, fmt.Errorf("claims operations repair apply requires authorized=true; run dry_run=true first")
			}
			return runClaimsOperationsRepair(ctx, cfg, board, params)
		}).
		Build()
}

func AllOperationsSkills(cfg ClaimsOperationsSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		ClaimsInfrastructureHealthSkill(InfrastructureHealthSkillConfig{
			BoardProvider:       cfg.BoardProvider,
			ParticipantRegistry: cfg.ParticipantRegistry,
			ServiceDispatchers:  cfg.ServiceDispatchers,
			ValidatorDispatcher: programmaticValidatorDispatcher(cfg.ValidatorDispatcher),
			TypeRegistry:        cfg.TypeRegistry,
		}),
		ClaimsOperationsStatusSkill(cfg),
		ClaimsOperationsRepairSkill(cfg),
		ProjectionHealthSkill(cfg.BoardProvider),
		RebuildClaimsProjectionsSkillWithRegistry(cfg.BoardProvider, firstNonNilSessionBoardRegistry(cfg.BoardRegistry)),
	}
}

type claimsOperationsRepairParams struct {
	Operation  string `json:"operation"`
	DryRun     bool   `json:"dry_run"`
	Authorized bool   `json:"authorized"`
	EmitReport bool   `json:"emit_report"`
	Limit      int    `json:"limit"`
}

func parseClaimsOperationsRepairParams(input json.RawMessage) (claimsOperationsRepairParams, error) {
	params := claimsOperationsRepairParams{DryRun: true}
	if len(input) == 0 {
		return params, fmt.Errorf("operation is required")
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return params, fmt.Errorf("invalid parameters: %w", err)
	}
	params.Operation = strings.TrimSpace(params.Operation)
	if params.Operation == "" {
		return params, fmt.Errorf("operation is required")
	}
	return params, nil
}

func requireOperationsBoard(bp BoardProvider) (*ClaimsBoard, error) {
	if bp == nil {
		return nil, fmt.Errorf("claims operations skill requires board provider")
	}
	board, err := bp()
	if err != nil {
		return nil, fmt.Errorf("claims board: %w", err)
	}
	if board == nil {
		return nil, fmt.Errorf("claims board unavailable")
	}
	return board, nil
}

func buildClaimsOperationsStatus(ctx context.Context, cfg ClaimsOperationsSkillConfig, board *ClaimsBoard, includeAudit bool) (ClaimsOperationsStatus, error) {
	status := ClaimsOperationsStatus{
		Telemetry: InfrastructureTelemetry(ctx, InfrastructureTelemetryOptions{
			Board:               board,
			ParticipantRegistry: cfg.ParticipantRegistry,
			ServiceDispatchers:  cfg.ServiceDispatchers,
			ValidatorDispatcher: programmaticValidatorDispatcher(cfg.ValidatorDispatcher),
			TypeRegistry:        firstNonNilTypeRegistry(cfg.TypeRegistry),
		}),
		QueueHealth: claimsOperationsQueueHealth(cfg, board.SessionID()),
	}
	if includeAudit {
		audit, err := RunClaimsOperationsAudit(ctx, auditOptionsForOperationsSkill(cfg, board))
		if err != nil {
			return status, err
		}
		status.ResourceAudit = audit
	}
	return status, nil
}

func claimsOperationsQueueHealth(cfg ClaimsOperationsSkillConfig, sessionID string) ClaimsOperationsQueueHealth {
	inboxRegistry := cfg.InboxRegistry
	if inboxRegistry == nil {
		inboxRegistry = DefaultSessionInboxRegistry()
	}
	return ClaimsOperationsQueueHealth{
		Inboxes:     inboxRegistry.SnapshotStats(),
		Dispatchers: snapshotServiceDispatchers(cfg.ServiceDispatchers, sessionID),
		Validators:  validatorStats(cfg.ValidatorDispatcher),
	}
}

func runClaimsOperationsRepair(ctx context.Context, cfg ClaimsOperationsSkillConfig, board *ClaimsBoard, params claimsOperationsRepairParams) (any, error) {
	switch params.Operation {
	case "outbox_projection":
		if board.durable == nil && params.DryRun {
			return &ProjectionRebuildResult{
				BoardID:   board.BoardID(),
				SessionID: board.SessionID(),
				DryRun:    true,
				Warnings:  []string{"claims board is not durable; projection rebuild requires durable outbox records"},
			}, nil
		}
		return board.RebuildProjections(ctx, ProjectionRebuildOptions{DryRun: params.DryRun, EmitReport: params.EmitReport, Limit: params.Limit})
	case "resource_audit":
		opts := auditOptionsForOperationsSkill(cfg, board)
		if !params.EmitReport {
			opts.EvidenceBoard = nil
		}
		opts.Disabled = false
		return RunClaimsOperationsAudit(ctx, opts)
	default:
		return nil, fmt.Errorf("unknown repair operation %q", params.Operation)
	}
}

func auditOptionsForOperationsSkill(cfg ClaimsOperationsSkillConfig, board *ClaimsBoard) OperationsAuditOptions {
	return OperationsAuditOptions{
		Config:              board.OperationsConfig(),
		BoardRegistry:       cfg.BoardRegistry,
		InboxRegistry:       cfg.InboxRegistry,
		ServiceDispatchers:  cfg.ServiceDispatchers,
		ValidatorDispatcher: cfg.ValidatorDispatcher,
		Boards:              []*ClaimsBoard{board},
		EvidenceBoard:       board,
		SessionID:           board.SessionID(),
		ActorID:             "sys:claims_operations_skill",
		Metrics:             firstNonNilMetrics(cfg.Metrics, board.metrics),
	}
}

func firstNonNilSessionBoardRegistry(registry *SessionBoardRegistry) *SessionBoardRegistry {
	if registry != nil {
		return registry
	}
	return DefaultSessionBoardRegistry()
}

func firstNonNilMetrics(values ...ClaimsMetricsSink) ClaimsMetricsSink {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return NoopClaimsMetricsSink{}
}

func snapshotServiceDispatchers(registry *ServiceDispatcherRegistry, sessionID string) ServiceDispatcherRegistrySnapshot {
	if registry == nil {
		return ServiceDispatcherRegistrySnapshot{SessionID: sessionID}
	}
	return registry.Snapshot(sessionID)
}

func validatorStats(dispatcher *BoardValidatorDispatcher) ValidatorDispatcherStats {
	if dispatcher == nil || dispatcher.programmatic == nil {
		return ValidatorDispatcherStats{}
	}
	return dispatcher.programmatic.Stats()
}

func programmaticValidatorDispatcher(dispatcher *BoardValidatorDispatcher) *ProgrammaticValidatorDispatcher {
	if dispatcher == nil {
		return nil
	}
	return dispatcher.programmatic
}
