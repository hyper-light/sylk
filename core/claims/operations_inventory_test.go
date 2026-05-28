package claims

import (
	"errors"
	"strings"
	"testing"
)

func TestOperationsInventoryCoversKnownDeltaActionsAndValidationTypes(t *testing.T) {
	inventory := OperationsInventory()
	if err := ValidateOperationsInventory(inventory); err != nil {
		t.Fatalf("ValidateOperationsInventory: %v", err)
	}
	for _, action := range KnownDeltaActions() {
		assertInventoryRequirement(t, inventory, "delta.action."+string(action))
	}
	for _, validationType := range KnownValidationTypes() {
		assertInventoryRequirement(t, inventory, "validation.type."+string(validationType))
	}
	for _, category := range KnownParticipantCategories() {
		assertInventoryRequirement(t, inventory, "participant.category."+string(category))
	}
}

func TestOperationsInventoryMarksCompatibilityAndPlannedGapsExplicitly(t *testing.T) {
	inventory := OperationsInventory()
	for _, requirement := range []string{
		"ops.semantic.participant_agnostic_claims",
		"ops.semantic.programmatic_or_agentic_validation",
		"ops.semantic.infrastructure_outcomes_are_testaments",
		"ops.semantic.board_source_of_truth",
		"ops.semantic.universal_identity",
		"ops.delta.legacy_compatibility",
		"ops.invariant.cancellation_propagates",
		"ops.invariant.shutdown_drains",
		"ops.recovery.orphan_validations",
		"ops.telemetry.exporters",
	} {
		entry := assertInventoryRequirement(t, inventory, requirement)
		if entry.Package == "" || entry.Boundary == "" {
			t.Fatalf("%s does not name package and boundary: %#v", requirement, entry)
		}
	}
}

func TestOperationsInventoryCoversInfrastructureServiceCatalog(t *testing.T) {
	inventory := OperationsInventory()
	for _, requirement := range []string{
		"ops.service.identity_registry",
		"ops.service.activation_controller",
		"ops.service.dag_processor",
		"ops.service.pipeline_vfs_provisioner",
		"ops.service.tool_vfs_provisioner",
		"ops.service.global_vfs_merger",
		"ops.service.knowledge_graph_writer",
		"ops.service.knowledge_graph_reader",
		"ops.service.document_db_writer",
		"ops.service.document_db_reader",
		"ops.service.guardian_subsystem",
		"ops.service.boot_sequencer",
		"ops.service.tool_runtime",
		"ops.service.llm_provider_gateway",
		"ops.service.session_manager",
		"ops.service.fabric_subscriber",
		"ops.service.bus_transport",
	} {
		assertInventoryRequirement(t, inventory, requirement)
	}
}

func TestOperationsInventorySyntheticMissingRequirementFailsLookup(t *testing.T) {
	inventory := OperationsInventory()
	if entry, ok := findInventoryRequirement(inventory, "delta.action.synthetic_missing"); ok {
		t.Fatalf("synthetic missing action unexpectedly found: %#v", entry)
	}
}

func TestOperationsInventoryDuplicateRequirementValidationNamesKey(t *testing.T) {
	err := ValidateOperationsInventory([]OperationsInventoryEntry{
		{Requirement: "duplicate", Status: OperationsSurfaceImplemented, Package: "core/claims", Boundary: "A"},
		{Requirement: "duplicate", Status: OperationsSurfacePlanned, Package: "core/claims", Boundary: "B"},
	})
	if !errors.Is(err, ErrOperationsInventoryDuplicate) || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate validation error = %v, want duplicate key named", err)
	}
}

func assertInventoryRequirement(t *testing.T, inventory []OperationsInventoryEntry, requirement string) OperationsInventoryEntry {
	t.Helper()
	entry, ok := findInventoryRequirement(inventory, requirement)
	if !ok {
		t.Fatalf("inventory missing %s", requirement)
	}
	if strings.TrimSpace(entry.Package) == "" || strings.TrimSpace(entry.Boundary) == "" {
		t.Fatalf("inventory entry %s lacks package/boundary: %#v", requirement, entry)
	}
	return entry
}

func findInventoryRequirement(inventory []OperationsInventoryEntry, requirement string) (OperationsInventoryEntry, bool) {
	for _, entry := range inventory {
		if entry.Requirement == requirement {
			return entry, true
		}
	}
	return OperationsInventoryEntry{}, false
}
