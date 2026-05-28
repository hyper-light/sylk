package claims

import (
	"errors"
	"fmt"
	"sort"
	"strings"
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
		{"ops.semantic.programmatic_or_agentic_validation", OperationsSurfacePartial, "core/claims", "ValidatorRegistry"},
		{"ops.semantic.infrastructure_outcomes_are_testaments", OperationsSurfacePartial, "core/claims", "ServiceDispatcher"},
		{"ops.semantic.board_source_of_truth", OperationsSurfaceImplemented, "core/claims", "ClaimsBoard"},
		{"ops.semantic.universal_identity", OperationsSurfacePartial, "core/claims", "AgentRef"},
		{"ops.semantic.replay_reconstructs_perspectives", OperationsSurfaceImplemented, "core/claims", "OpenDurableBoard"},
		{"ops.semantic.bounded_tracked_resources", OperationsSurfacePartial, "core/claims", "ScopeProvider"},
		{"ops.invariant.no_untracked_goroutines", OperationsSurfacePartial, "core/claims", "ScopeProvider"},
		{"ops.invariant.no_unbounded_queues", OperationsSurfacePartial, "core/claims", "ParticipantRegistration"},
		{"ops.invariant.no_silent_drops", OperationsSurfacePartial, "core/claims", "ServiceDispatcher"},
		{"ops.invariant.cancellation_propagates", OperationsSurfacePlanned, "core/claims", "cancellation graph traversal"},
		{"ops.invariant.replay_reconstructs_state", OperationsSurfaceImplemented, "core/claims", "OpenDurableBoard"},
		{"ops.invariant.bootstrap_idempotent", OperationsSurfaceImplemented, "core/boot", "OperationsSequencer"},
		{"ops.invariant.shutdown_drains", OperationsSurfacePlanned, "core/claims", "shutdown drain ordering"},
		{"ops.invariant.performance_bounds_declared", OperationsSurfacePartial, "core/claims", "ParticipantRegistration"},
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
		{"ops.recovery.orphan_validations", OperationsSurfacePlanned, "core/claims", "recovery audit worker"},
		{"ops.telemetry.exporters", OperationsSurfacePlanned, "core/claims", "telemetry exporter"},
		{"ops.ui.observer_intake", OperationsSurfacePartial, "ui/bridge", "ClaimsBridge.startClaimsIntake"},
		{"ops.delta.legacy_compatibility", OperationsSurfacePartial, "core/claims", "deltas.go"},
		{"ops.service.identity_registry", OperationsSurfacePlanned, "core/container", "identity registry service participant"},
		{"ops.service.activation_controller", OperationsSurfacePlanned, "core/container", "activation controller service participant"},
		{"ops.service.dag_processor", OperationsSurfacePlanned, "agents/orchestrator", "DAG processor service participant"},
		{"ops.service.vfs_provisioner", OperationsSurfacePlanned, "core/versioning", "pipeline VFS service participant"},
		{"ops.service.tool_vfs_provisioner", OperationsSurfacePlanned, "agents/shared", "tool VFS service participant"},
		{"ops.service.global_vfs_merger", OperationsSurfacePlanned, "core/versioning", "global VFS merger service participant"},
		{"ops.service.kg_writer", OperationsSurfacePlanned, "core/knowledge", "knowledge graph writer service participant"},
		{"ops.service.kg_reader", OperationsSurfacePlanned, "core/knowledge", "knowledge graph reader service participant"},
		{"ops.service.doc_db_writer", OperationsSurfacePlanned, "core/search", "document DB writer service participant"},
		{"ops.service.doc_db_reader", OperationsSurfacePlanned, "core/search", "document DB reader service participant"},
		{"ops.service.guardian", OperationsSurfacePlanned, "agents/guardian", "guardian service participant"},
		{"ops.service.boot_sequencer", OperationsSurfacePartial, "core/boot", "OperationsSequencer"},
		{"ops.service.tool_runtime", OperationsSurfacePlanned, "core/toolruntime", "tool runtime service participant"},
		{"ops.service.provider_gateway", OperationsSurfacePlanned, "core/providers", "provider gateway service participant"},
		{"ops.service.session_manager", OperationsSurfacePlanned, "core/claims", "session manager service participant"},
		{"ops.service.fabric_subscriber", OperationsSurfacePlanned, "agents/orchestrator", "fabric subscriber service participant"},
		{"ops.service.bus_administrator", OperationsSurfacePartial, "core/claims", "DeltaBus"},
	}
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
