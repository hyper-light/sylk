package claims

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type OperationsSurfaceStatus string

const (
	OperationsSurfaceImplemented OperationsSurfaceStatus = "implemented"
	OperationsSurfacePartial     OperationsSurfaceStatus = "partially_implemented"
	OperationsSurfacePlanned     OperationsSurfaceStatus = "planned"
)

type OperationsInventoryEntry struct {
	Requirement string
	Status      OperationsSurfaceStatus
	Package     string
	Boundary    string
}

type OperationsInventoryOwner struct {
	Requirement  string
	OwnerClaimID string
	Deadline     string
}

var (
	ErrOperationsInventoryInvalid   = errors.New("operations inventory invalid")
	ErrOperationsInventoryDuplicate = errors.New("operations inventory duplicate requirement")
)

func OperationsInventory() []OperationsInventoryEntry {
	entries := baseOperationsInventory()
	entries = append(entries, deltaActionInventory()...)
	entries = append(entries, validationTypeInventory()...)
	entries = append(entries, participantCategoryInventory()...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Requirement != entries[j].Requirement {
			return entries[i].Requirement < entries[j].Requirement
		}
		if entries[i].Package != entries[j].Package {
			return entries[i].Package < entries[j].Package
		}
		return entries[i].Boundary < entries[j].Boundary
	})
	return entries
}

func ValidateOperationsInventory(entries []OperationsInventoryEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for idx, entry := range entries {
		requirement := strings.TrimSpace(entry.Requirement)
		if requirement == "" || strings.TrimSpace(entry.Package) == "" || strings.TrimSpace(entry.Boundary) == "" || !entry.Status.Valid() {
			return fmt.Errorf("%w: entry %d has empty or invalid fields: %#v", ErrOperationsInventoryInvalid, idx, entry)
		}
		if _, ok := seen[requirement]; ok {
			return fmt.Errorf("%w: %s", ErrOperationsInventoryDuplicate, requirement)
		}
		seen[requirement] = struct{}{}
	}
	return nil
}

func baseOperationsInventory() []OperationsInventoryEntry {
	return []OperationsInventoryEntry{
		{"ops.semantic.participant_agnostic_claims", OperationsSurfaceImplemented, "core/claims", "ParticipantRef"},
		{"ops.semantic.participant_agnostic_wire_format", OperationsSurfaceImplemented, "core/claims", "CanonicalDelta"},
		{"ops.semantic.participant_categories", OperationsSurfaceImplemented, "core/claims", "ParticipantCategory"},
		{"ops.semantic.programmatic_or_agentic_validation", OperationsSurfaceImplemented, "core/claims", "ValidatorRegistry"},
		{"ops.semantic.infrastructure_outcomes_are_testaments", OperationsSurfaceImplemented, "core/claims", "ServiceDispatcher"},
		{"ops.semantic.board_source_of_truth", OperationsSurfaceImplemented, "core/claims", "ClaimsBoard"},
		{"ops.semantic.universal_identity", OperationsSurfacePartial, "core/claims", "AgentRef"},
		{"ops.semantic.replay_reconstructs_perspectives", OperationsSurfaceImplemented, "core/claims", "OpenDurableBoard"},
		{"ops.semantic.bounded_tracked_resources", OperationsSurfaceImplemented, "core/claims", "ClaimsOperationsConfig"},
		{"ops.invariant.no_untracked_goroutines", OperationsSurfaceImplemented, "core/claims", "ScopeProvider"},
		{"ops.invariant.no_unbounded_queues", OperationsSurfaceImplemented, "core/claims", "ClaimsOperationsConfig"},
		{"ops.invariant.no_silent_drops", OperationsSurfaceImplemented, "core/claims", "ServiceDispatcher"},
		{"ops.invariant.cancellation_propagates", OperationsSurfaceImplemented, "core/claims", "CancelClaimTree"},
		{"ops.invariant.replay_reconstructs_state", OperationsSurfaceImplemented, "core/claims", "OpenDurableBoard"},
		{"ops.invariant.bootstrap_idempotent", OperationsSurfaceImplemented, "core/boot", "OperationsSequencer"},
		{"ops.invariant.shutdown_drains", OperationsSurfaceImplemented, "core/claims", "SessionShutdownCoordinator"},
		{"ops.invariant.performance_bounds_declared", OperationsSurfaceImplemented, "core/claims", "ClaimsOperationsBudgetSnapshot"},
		{"ops.boot.phase_0", OperationsSurfaceImplemented, "core/boot", "InitializePhase0"},
		{"ops.boot.phase_1", OperationsSurfaceImplemented, "core/boot", "CommitPhase1"},
		{"ops.boot.phase_2", OperationsSurfaceImplemented, "core/boot", "CommitPhase2"},
		{"ops.boot.phase_3", OperationsSurfaceImplemented, "core/boot", "CommitPhase3"},
		{"ops.boot.phase_4", OperationsSurfaceImplemented, "core/boot", "CommitPhase4"},
		{"ops.boot.phase_5", OperationsSurfaceImplemented, "core/boot", "CommitPhase5"},
		{"ops.boot.phase_6", OperationsSurfaceImplemented, "core/boot", "CommitPhase6"},
		{"ops.boot.phase_7", OperationsSurfaceImplemented, "core/boot", "CommitPhase7"},
		{"ops.dispatch.service_handlers", OperationsSurfaceImplemented, "core/claims", "ServiceDispatcher"},
		{"ops.dispatch.validator_registry", OperationsSurfaceImplemented, "core/claims", "ProgrammaticValidatorDispatcher"},
		{"ops.recovery.orphan_validations", OperationsSurfaceImplemented, "core/claims", "RecoverServiceWork"},
		{"ops.recovery.wal_replay_classification", OperationsSurfaceImplemented, "core/claims", "WALReplayIssue"},
		{"ops.recovery.outbox_repair", OperationsSurfaceImplemented, "core/claims", "OutboxRepairReport"},
		{"ops.invariant.cancellation_context_registry", OperationsSurfaceImplemented, "core/claims", "ClaimCancelRegistry"},
		{"ops.invariant.backpressure_observable", OperationsSurfaceImplemented, "core/claims", "BackpressureReporter"},
		{"ops.invariant.resource_audit", OperationsSurfaceImplemented, "core/claims", "RunClaimsOperationsAudit"},
		{"ops.telemetry.exporters", OperationsSurfaceImplemented, "core/claims", "ClaimsMetricsSink"},
		{"ops.ui.observer_intake", OperationsSurfaceImplemented, "ui/bridge", "ClaimsBridge.startClaimsIntake"},
		{"ops.delta.legacy_compatibility", OperationsSurfaceImplemented, "core/claims", "UnmarshalDeltaTolerant"},
		{"ops.operator.skills", OperationsSurfaceImplemented, "core/claims", "AllOperationsSkills"},
		{"ops.runbook.scenarios", OperationsSurfaceImplemented, "core/claims", "claims operations runbook scenario tests"},
		{"ops.production_readiness", OperationsSurfaceImplemented, "core/claims", "RecordProductionReadinessEvidence"},
		{"ops.service.identity_registry", OperationsSurfaceImplemented, "core/claims", "IdentityRegistryService"},
		{"ops.service.activation_controller", OperationsSurfaceImplemented, "core/claims", "ActivationControllerService"},
		{"ops.service.dag_processor", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.vfs_provisioner", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.tool_vfs_provisioner", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.global_vfs_merger", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.kg_writer", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.kg_reader", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.memory_forest", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.doc_db_writer", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.doc_db_reader", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.guardian", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.boot_sequencer", OperationsSurfaceImplemented, "core/boot", "OperationsSequencer"},
		{"ops.service.tool_runtime", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.provider_gateway", OperationsSurfaceImplemented, "core/claims", "InfrastructureService"},
		{"ops.service.session_manager", OperationsSurfaceImplemented, "core/claims", "SessionBoardRegistry"},
		{"ops.service.fabric_subscriber", OperationsSurfaceImplemented, "core/claims", "SessionInboxRegistry"},
		{"ops.service.bus_administrator", OperationsSurfaceImplemented, "core/claims", "DeltaBus"},
	}
}

func OperationsInventoryOwners() map[string]OperationsInventoryOwner {
	return map[string]OperationsInventoryOwner{}
}

func ValidateOperationsInventoryPlanning(entries []OperationsInventoryEntry, owners map[string]OperationsInventoryOwner) error {
	for _, entry := range entries {
		if entry.Status != OperationsSurfacePlanned {
			continue
		}
		owner := owners[strings.TrimSpace(entry.Requirement)]
		if strings.TrimSpace(owner.OwnerClaimID) == "" || strings.TrimSpace(owner.Deadline) == "" || strings.TrimSpace(owner.Requirement) != strings.TrimSpace(entry.Requirement) {
			return fmt.Errorf("%w: planned entry %s requires owner claim and deadline", ErrOperationsInventoryInvalid, entry.Requirement)
		}
		if _, err := time.Parse(time.DateOnly, strings.TrimSpace(owner.Deadline)); err != nil {
			return fmt.Errorf("%w: planned entry %s has invalid deadline %q", ErrOperationsInventoryInvalid, entry.Requirement, owner.Deadline)
		}
	}
	return nil
}

func deltaActionInventory() []OperationsInventoryEntry {
	actions := KnownDeltaActions()
	out := make([]OperationsInventoryEntry, 0, len(actions))
	for _, action := range actions {
		out = append(out, OperationsInventoryEntry{
			Requirement: "delta.action." + string(action),
			Status:      OperationsSurfaceImplemented,
			Package:     "core/claims",
			Boundary:    "CanonicalDelta",
		})
	}
	return out
}

func validationTypeInventory() []OperationsInventoryEntry {
	types := KnownValidationTypes()
	out := make([]OperationsInventoryEntry, 0, len(types))
	for _, validationType := range types {
		out = append(out, OperationsInventoryEntry{
			Requirement: "validation.type." + string(validationType),
			Status:      OperationsSurfaceImplemented,
			Package:     "core/claims",
			Boundary:    "ValidationTypeSemanticsFor",
		})
	}
	return out
}

func participantCategoryInventory() []OperationsInventoryEntry {
	categories := KnownParticipantCategories()
	out := make([]OperationsInventoryEntry, 0, len(categories))
	for _, category := range categories {
		out = append(out, OperationsInventoryEntry{
			Requirement: "participant.category." + string(category),
			Status:      OperationsSurfaceImplemented,
			Package:     "core/claims",
			Boundary:    "ParticipantCategory",
		})
	}
	return out
}

func (s OperationsSurfaceStatus) Valid() bool {
	switch s {
	case OperationsSurfaceImplemented, OperationsSurfacePartial, OperationsSurfacePlanned:
		return true
	default:
		return false
	}
}
