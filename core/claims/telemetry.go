package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

const (
	defaultTelemetryParticipantLimit  = 128
	defaultTelemetryArtifactTypeLimit = 256
	defaultTelemetryWarningLimit      = 64
)

type InfrastructureTelemetryOptions struct {
	Board               *ClaimsBoard
	ParticipantRegistry *ParticipantRegistry
	ServiceDispatchers  *ServiceDispatcherRegistry
	ValidatorDispatcher *ProgrammaticValidatorDispatcher
	TypeRegistry        *TypeRegistry
	Recovery            *ServiceRecoveryResult
	Cancellation        *ClaimCancellationResult
	BootHealth          []InfrastructureBootHealth
	RepairSkills        []InfrastructureRepairSkillStatus
	ParticipantLimit    int
	ArtifactTypeLimit   int
}

type InfrastructureBootHealth struct {
	Phase       string `json:"phase"`
	Status      string `json:"status"`
	ClaimID     string `json:"claim_id,omitempty"`
	TestamentID string `json:"testament_id,omitempty"`
}

type InfrastructureRepairSkillStatus struct {
	Name                   string `json:"name"`
	DryRunDefault          bool   `json:"dry_run_default"`
	ApplyRequiresAuth      bool   `json:"apply_requires_auth"`
	ReportTestamentOnApply bool   `json:"report_testament_on_apply"`
}

type InfrastructureTelemetrySnapshot struct {
	BoardID           string                            `json:"board_id,omitempty"`
	SessionID         string                            `json:"session_id,omitempty"`
	Participants      []ParticipantRegistration         `json:"participants,omitempty"`
	ServiceDispatch   ServiceDispatcherRegistrySnapshot `json:"service_dispatch"`
	ValidatorDispatch ValidatorDispatcherStats          `json:"validator_dispatch"`
	ArtifactTypes     []RegisteredArtifactType          `json:"artifact_types,omitempty"`
	Projection        ProjectionHealthSnapshot          `json:"projection"`
	FeatureFlags      map[string]string                 `json:"feature_flags,omitempty"`
	Recovery          *ServiceRecoveryResult            `json:"recovery,omitempty"`
	Cancellation      *ClaimCancellationResult          `json:"cancellation,omitempty"`
	BootHealth        []InfrastructureBootHealth        `json:"boot_health,omitempty"`
	RepairSkills      []InfrastructureRepairSkillStatus `json:"repair_skills,omitempty"`
	Warnings          []string                          `json:"warnings,omitempty"`
}

func InfrastructureTelemetry(ctx context.Context, opts InfrastructureTelemetryOptions) InfrastructureTelemetrySnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx
	opts = normalizeTelemetryOptions(opts)
	snap := InfrastructureTelemetrySnapshot{}
	if opts.Board != nil {
		snap.BoardID = opts.Board.BoardID()
		snap.SessionID = opts.Board.SessionID()
		snap.Projection = opts.Board.ProjectionHealth()
		snap.FeatureFlags = opts.Board.RolloutConfig().FeatureFlags()
	} else {
		cfg := CurrentRolloutConfig()
		snap.FeatureFlags = cfg.FeatureFlags()
		snap.Warnings = append(snap.Warnings, "claims board unavailable")
	}
	if opts.ParticipantRegistry != nil {
		snap.Participants = boundedParticipants(opts.ParticipantRegistry.List(), opts.ParticipantLimit)
	}
	if opts.ServiceDispatchers != nil {
		snap.ServiceDispatch = opts.ServiceDispatchers.Snapshot(snap.SessionID)
	}
	if opts.ValidatorDispatcher != nil {
		snap.ValidatorDispatch = opts.ValidatorDispatcher.Stats()
	}
	if opts.TypeRegistry != nil {
		snap.ArtifactTypes = boundedArtifactTypes(opts.TypeRegistry.ListArtifactTypes(), opts.ArtifactTypeLimit)
	}
	if opts.Recovery != nil {
		recovery := *opts.Recovery
		snap.Recovery = &recovery
	}
	if opts.Cancellation != nil {
		cancellation := *opts.Cancellation
		snap.Cancellation = &cancellation
	}
	snap.BootHealth = boundedBootHealth(opts.BootHealth, defaultTelemetryWarningLimit)
	snap.RepairSkills = boundedRepairSkills(firstNonEmptyRepairSkills(opts.RepairSkills), defaultTelemetryWarningLimit)
	snap.Warnings = boundedStrings(append(snap.Warnings, snap.Projection.Warnings...), defaultTelemetryWarningLimit)
	return snap
}

type InfrastructureHealthSkillConfig struct {
	BoardProvider       BoardProvider
	ParticipantRegistry *ParticipantRegistry
	ServiceDispatchers  *ServiceDispatcherRegistry
	ValidatorDispatcher *ProgrammaticValidatorDispatcher
	TypeRegistry        *TypeRegistry
}

func ClaimsInfrastructureHealthSkill(cfg InfrastructureHealthSkillConfig) *skills.Skill {
	return skills.NewSkill("claims_infrastructure_health").
		Description("Inspect claims participant infrastructure health: participant registry, service dispatcher queues, validator queues, typed artifact coverage, rollout flags, and projection health. Output is bounded and read-only.").
		Domain("claims").
		Keywords("claims", "infrastructure", "participants", "health", "dispatch", "validators", "rollout").
		Priority(61).
		IntParam("participant_limit", "Maximum participants to return. Default/cap is bounded internally.", false).
		IntParam("artifact_type_limit", "Maximum artifact datatype records to return. Default/cap is bounded internally.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if cfg.BoardProvider == nil {
				return nil, fmt.Errorf("claims infrastructure health requires a board provider")
			}
			board, err := cfg.BoardProvider()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			var params struct {
				ParticipantLimit  int `json:"participant_limit"`
				ArtifactTypeLimit int `json:"artifact_type_limit"`
			}
			if len(input) != 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}
			return InfrastructureTelemetry(ctx, InfrastructureTelemetryOptions{
				Board:               board,
				ParticipantRegistry: cfg.ParticipantRegistry,
				ServiceDispatchers:  cfg.ServiceDispatchers,
				ValidatorDispatcher: cfg.ValidatorDispatcher,
				TypeRegistry:        firstNonNilTypeRegistry(cfg.TypeRegistry),
				ParticipantLimit:    params.ParticipantLimit,
				ArtifactTypeLimit:   params.ArtifactTypeLimit,
			}), nil
		}).
		Build()
}

func normalizeTelemetryOptions(opts InfrastructureTelemetryOptions) InfrastructureTelemetryOptions {
	if opts.ParticipantLimit <= 0 || opts.ParticipantLimit > defaultTelemetryParticipantLimit {
		opts.ParticipantLimit = defaultTelemetryParticipantLimit
	}
	if opts.ArtifactTypeLimit <= 0 || opts.ArtifactTypeLimit > defaultTelemetryArtifactTypeLimit {
		opts.ArtifactTypeLimit = defaultTelemetryArtifactTypeLimit
	}
	if opts.TypeRegistry == nil {
		opts.TypeRegistry = DefaultTypeRegistry()
	}
	return opts
}

func boundedParticipants(values []ParticipantRegistration, limit int) []ParticipantRegistration {
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]ParticipantRegistration, 0, len(values))
	for _, value := range values {
		out = append(out, cloneParticipantRegistration(value))
	}
	return out
}

func boundedArtifactTypes(values []RegisteredArtifactType, limit int) []RegisteredArtifactType {
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]RegisteredArtifactType(nil), values...)
}

func boundedStrings(values []string, limit int) []string {
	out := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) == limit {
			return out
		}
	}
	return out
}

func boundedBootHealth(values []InfrastructureBootHealth, limit int) []InfrastructureBootHealth {
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]InfrastructureBootHealth(nil), values...)
}

func boundedRepairSkills(values []InfrastructureRepairSkillStatus, limit int) []InfrastructureRepairSkillStatus {
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]InfrastructureRepairSkillStatus(nil), values...)
}

func firstNonEmptyRepairSkills(values []InfrastructureRepairSkillStatus) []InfrastructureRepairSkillStatus {
	if len(values) != 0 {
		return values
	}
	return []InfrastructureRepairSkillStatus{{
		Name:                   "rebuild_claims_projections",
		DryRunDefault:          true,
		ApplyRequiresAuth:      true,
		ReportTestamentOnApply: true,
	}}
}

func firstNonNilTypeRegistry(values ...*TypeRegistry) *TypeRegistry {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return DefaultTypeRegistry()
}
