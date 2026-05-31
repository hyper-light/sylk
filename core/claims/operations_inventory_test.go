package claims

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
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

func TestOperationsInventoryIsSortedDeterministicAndConcurrentSafe(t *testing.T) {
	want := OperationsInventory()
	for i := 1; i < len(want); i++ {
		if want[i-1].Requirement > want[i].Requirement {
			t.Fatalf("inventory not sorted at %d: %s > %s", i, want[i-1].Requirement, want[i].Requirement)
		}
	}
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range cap(errs) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := OperationsInventory(); !reflect.DeepEqual(got, want) {
				errs <- fmt.Errorf("inventory changed across calls")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestOperationsInventoryMarksCompatibilityAndPlannedGapsExplicitly(t *testing.T) {
	inventory := OperationsInventory()
	for _, requirement := range []string{
		"ops.semantic.participant_agnostic_claims",
		"ops.semantic.participant_agnostic_wire_format",
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
		"ops.service.vfs_provisioner",
		"ops.service.tool_vfs_provisioner",
		"ops.service.global_vfs_merger",
		"ops.service.kg_writer",
		"ops.service.kg_reader",
		"ops.service.memory_forest",
		"ops.service.doc_db_writer",
		"ops.service.doc_db_reader",
		"ops.service.guardian",
		"ops.service.boot_sequencer",
		"ops.service.tool_runtime",
		"ops.service.provider_gateway",
		"ops.service.session_manager",
		"ops.service.fabric_subscriber",
		"ops.service.bus_administrator",
	} {
		assertInventoryRequirement(t, inventory, requirement)
	}
}

func TestOperationsInventoryCoversServiceCatalogFromInfrastructureDoc(t *testing.T) {
	inventory := OperationsInventory()
	participantTypes := documentedServiceParticipantTypes(t)
	if len(participantTypes) == 0 {
		t.Fatal("no service participant types parsed from docs/CLAIMS_AND_INFRASTRUCTURE.md §14")
	}
	for _, participantType := range participantTypes {
		assertInventoryRequirement(t, inventory, "ops.service."+participantType)
	}
}

func TestOperationsInventorySyntheticMissingRequirementFailsCoverage(t *testing.T) {
	inventory := OperationsInventory()
	err := validateInventoryRequirements(inventory, []string{"delta.action.synthetic_missing"})
	if err == nil || !strings.Contains(err.Error(), "delta.action.synthetic_missing") {
		t.Fatalf("coverage error = %v, want missing requirement named", err)
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

func TestOperationsInventoryPlannedEntriesRequireOwnerClaimAndDeadline(t *testing.T) {
	if err := ValidateOperationsInventoryPlanning(OperationsInventory(), OperationsInventoryOwners()); err != nil {
		t.Fatalf("ValidateOperationsInventoryPlanning: %v", err)
	}
	entry := OperationsInventoryEntry{Requirement: "ops.planned.synthetic", Status: OperationsSurfacePlanned, Package: "core/claims", Boundary: "planned"}
	if err := ValidateOperationsInventoryPlanning([]OperationsInventoryEntry{entry}, nil); !errors.Is(err, ErrOperationsInventoryInvalid) {
		t.Fatalf("missing owner error = %v, want ErrOperationsInventoryInvalid", err)
	}
	owners := map[string]OperationsInventoryOwner{
		entry.Requirement: {Requirement: entry.Requirement, OwnerClaimID: "owner", Deadline: "not-a-date"},
	}
	if err := ValidateOperationsInventoryPlanning([]OperationsInventoryEntry{entry}, owners); !errors.Is(err, ErrOperationsInventoryInvalid) || !strings.Contains(err.Error(), "invalid deadline") {
		t.Fatalf("invalid deadline error = %v, want named invalid deadline", err)
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

func validateInventoryRequirements(inventory []OperationsInventoryEntry, requirements []string) error {
	for _, requirement := range requirements {
		if _, ok := findInventoryRequirement(inventory, requirement); !ok {
			return fmt.Errorf("inventory missing %s", requirement)
		}
	}
	return nil
}

func documentedServiceParticipantTypes(t *testing.T) []string {
	t.Helper()
	body := readClaimsFile(t, claimsRepoRoot(t)+"/docs/CLAIMS_AND_INFRASTRUCTURE.md")
	section := claimsInfrastructureSection(body, "## 14.", "## 15.")
	matches := regexp.MustCompile("(?m)^\\| Participant type \\| `([^`]+)`").FindAllStringSubmatch(section, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, strings.TrimSpace(match[1]))
	}
	return out
}

func claimsInfrastructureSection(body, startMarker, endMarker string) string {
	start := strings.Index(body, startMarker)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start+len(startMarker):], endMarker)
	if end < 0 {
		return body[start:]
	}
	return body[start : start+len(startMarker)+end]
}
