package claims

import "sort"

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

func OperationsInventory() []OperationsInventoryEntry {
	entries := baseOperationsInventory()
	entries = append(entries, deltaActionInventory()...)
	entries = append(entries, validationTypeInventory()...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Requirement < entries[j].Requirement })
	return entries
}

func baseOperationsInventory() []OperationsInventoryEntry {
	return []OperationsInventoryEntry{
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
