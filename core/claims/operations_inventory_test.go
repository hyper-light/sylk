package claims

import (
	"strings"
	"testing"
)

func TestOperationsInventoryCoversKnownDeltaActionsAndValidationTypes(t *testing.T) {
	inventory := OperationsInventory()
	for _, action := range KnownDeltaActions() {
		assertInventoryRequirement(t, inventory, "delta.action."+string(action))
	}
	for _, validationType := range KnownValidationTypes() {
		assertInventoryRequirement(t, inventory, "validation.type."+string(validationType))
	}
}

func TestOperationsInventoryMarksCompatibilityAndPlannedGapsExplicitly(t *testing.T) {
	inventory := OperationsInventory()
	for _, requirement := range []string{
		"ops.delta.legacy_compatibility",
		"ops.invariant.cancellation_propagates",
		"ops.recovery.orphan_validations",
		"ops.telemetry.exporters",
	} {
		entry := assertInventoryRequirement(t, inventory, requirement)
		if entry.Package == "" || entry.Boundary == "" {
			t.Fatalf("%s does not name package and boundary: %#v", requirement, entry)
		}
	}
}

func TestOperationsInventorySyntheticMissingRequirementFailsLookup(t *testing.T) {
	inventory := OperationsInventory()
	if entry, ok := findInventoryRequirement(inventory, "delta.action.synthetic_missing"); ok {
		t.Fatalf("synthetic missing action unexpectedly found: %#v", entry)
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
