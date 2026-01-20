package relations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// TCP.1 - EntryType Tests
// =============================================================================

func TestEntryType_String(t *testing.T) {
	tests := []struct {
		name     string
		entry    EntryType
		expected string
	}{
		{"EdgeAdd", EntryTypeEdgeAdd, "EdgeAdd"},
		{"EdgeRemove", EntryTypeEdgeRemove, "EdgeRemove"},
		{"Delta", EntryTypeDelta, "Delta"},
		{"Stratum", EntryTypeStratum, "Stratum"},
		{"Checkpoint", EntryTypeCheckpoint, "Checkpoint"},
		{"ComputationStart", EntryTypeComputationStart, "ComputationStart"},
		{"ComputationEnd", EntryTypeComputationEnd, "ComputationEnd"},
		{"Unknown", EntryType(0), "Unknown"},
		{"Unknown high value", EntryType(100), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.String(); got != tt.expected {
				t.Errorf("EntryType.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEntryType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		entry    EntryType
		expected bool
	}{
		{"EdgeAdd is valid", EntryTypeEdgeAdd, true},
		{"EdgeRemove is valid", EntryTypeEdgeRemove, true},
		{"Delta is valid", EntryTypeDelta, true},
		{"Stratum is valid", EntryTypeStratum, true},
		{"Checkpoint is valid", EntryTypeCheckpoint, true},
		{"ComputationStart is valid", EntryTypeComputationStart, true},
		{"ComputationEnd is valid", EntryTypeComputationEnd, true},
		{"ComputationAbort is valid", EntryTypeComputationAbort, true},
		{"Zero is invalid", EntryType(0), false},
		{"High value is invalid", EntryType(100), false},
		{"Nine is invalid", EntryType(9), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.IsValid(); got != tt.expected {
				t.Errorf("EntryType.IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAllEntryTypes(t *testing.T) {
	types := AllEntryTypes()

	// Should return exactly 8 entry types
	if len(types) != 8 {
		t.Errorf("AllEntryTypes() returned %d types, want 8", len(types))
	}

	// All returned types should be valid
	for _, et := range types {
		if !et.IsValid() {
			t.Errorf("AllEntryTypes() returned invalid type: %v", et)
		}
	}

	// Check that all expected types are present
	expectedTypes := map[EntryType]bool{
		EntryTypeEdgeAdd:           false,
		EntryTypeEdgeRemove:        false,
		EntryTypeDelta:             false,
		EntryTypeStratum:           false,
		EntryTypeCheckpoint:        false,
		EntryTypeComputationStart:  false,
		EntryTypeComputationEnd:    false,
		EntryTypeComputationAbort:  false,
	}

	for _, et := range types {
		expectedTypes[et] = true
	}

	for et, found := range expectedTypes {
		if !found {
			t.Errorf("AllEntryTypes() missing type: %v", et)
		}
	}
}

func TestEntryType_Values(t *testing.T) {
	// Verify the iota values are as expected (starting from 1)
	if EntryTypeEdgeAdd != 1 {
		t.Errorf("EntryTypeEdgeAdd = %d, want 1", EntryTypeEdgeAdd)
	}
	if EntryTypeEdgeRemove != 2 {
		t.Errorf("EntryTypeEdgeRemove = %d, want 2", EntryTypeEdgeRemove)
	}
	if EntryTypeDelta != 3 {
		t.Errorf("EntryTypeDelta = %d, want 3", EntryTypeDelta)
	}
	if EntryTypeStratum != 4 {
		t.Errorf("EntryTypeStratum = %d, want 4", EntryTypeStratum)
	}
	if EntryTypeCheckpoint != 5 {
		t.Errorf("EntryTypeCheckpoint = %d, want 5", EntryTypeCheckpoint)
	}
	if EntryTypeComputationStart != 6 {
		t.Errorf("EntryTypeComputationStart = %d, want 6", EntryTypeComputationStart)
	}
	if EntryTypeComputationEnd != 7 {
		t.Errorf("EntryTypeComputationEnd = %d, want 7", EntryTypeComputationEnd)
	}
}

// =============================================================================
// TCP.2 - CheckpointEntry Creation Tests
// =============================================================================

func TestNewCheckpointEntry(t *testing.T) {
	for _, et := range AllEntryTypes() {
		t.Run(et.String(), func(t *testing.T) {
			entry := NewCheckpointEntry(et)

			if entry.Type != et {
				t.Errorf("NewCheckpointEntry(%v).Type = %v, want %v", et, entry.Type, et)
			}

			if entry.Timestamp.IsZero() {
				t.Error("NewCheckpointEntry() should set Timestamp")
			}

			// Timestamp should be recent (within 1 second)
			if time.Since(entry.Timestamp) > time.Second {
				t.Error("NewCheckpointEntry() Timestamp is not recent")
			}
		})
	}
}

func TestNewEdgeAddEntry(t *testing.T) {
	entry := NewEdgeAddEntry("subject1", "predicate1", "object1")

	if entry.Type != EntryTypeEdgeAdd {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeEdgeAdd)
	}
	if entry.Subject != "subject1" {
		t.Errorf("Subject = %v, want %v", entry.Subject, "subject1")
	}
	if entry.Predicate != "predicate1" {
		t.Errorf("Predicate = %v, want %v", entry.Predicate, "predicate1")
	}
	if entry.Object != "object1" {
		t.Errorf("Object = %v, want %v", entry.Object, "object1")
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestNewEdgeRemoveEntry(t *testing.T) {
	entry := NewEdgeRemoveEntry("subject2", "predicate2", "object2")

	if entry.Type != EntryTypeEdgeRemove {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeEdgeRemove)
	}
	if entry.Subject != "subject2" {
		t.Errorf("Subject = %v, want %v", entry.Subject, "subject2")
	}
	if entry.Predicate != "predicate2" {
		t.Errorf("Predicate = %v, want %v", entry.Predicate, "predicate2")
	}
	if entry.Object != "object2" {
		t.Errorf("Object = %v, want %v", entry.Object, "object2")
	}
}

func TestNewDeltaEntry(t *testing.T) {
	edges := []EdgeKey{
		{Subject: "s1", Predicate: "p1", Object: "o1"},
		{Subject: "s2", Predicate: "p2", Object: "o2"},
	}

	entry := NewDeltaEntry(edges)

	if entry.Type != EntryTypeDelta {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeDelta)
	}
	if len(entry.DeltaEdges) != 2 {
		t.Errorf("DeltaEdges length = %d, want 2", len(entry.DeltaEdges))
	}

	// Verify it's a copy (mutation of original shouldn't affect entry)
	edges[0].Subject = "modified"
	if entry.DeltaEdges[0].Subject == "modified" {
		t.Error("NewDeltaEntry should create a copy of edges slice")
	}
}

func TestNewDeltaEntry_EmptySlice(t *testing.T) {
	entry := NewDeltaEntry([]EdgeKey{})

	if entry.Type != EntryTypeDelta {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeDelta)
	}
	if entry.DeltaEdges == nil {
		t.Error("DeltaEdges should not be nil for empty slice")
	}
	if len(entry.DeltaEdges) != 0 {
		t.Errorf("DeltaEdges length = %d, want 0", len(entry.DeltaEdges))
	}
}

func TestNewStratumEntry(t *testing.T) {
	entry := NewStratumEntry(5)

	if entry.Type != EntryTypeStratum {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeStratum)
	}
	if entry.Stratum != 5 {
		t.Errorf("Stratum = %d, want 5", entry.Stratum)
	}
}

func TestNewStratumEntry_Zero(t *testing.T) {
	entry := NewStratumEntry(0)

	if entry.Type != EntryTypeStratum {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeStratum)
	}
	if entry.Stratum != 0 {
		t.Errorf("Stratum = %d, want 0", entry.Stratum)
	}
}

func TestNewCheckpointMarker(t *testing.T) {
	entry := NewCheckpointMarker("checkpoint-123")

	if entry.Type != EntryTypeCheckpoint {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeCheckpoint)
	}
	if entry.CheckpointID != "checkpoint-123" {
		t.Errorf("CheckpointID = %v, want %v", entry.CheckpointID, "checkpoint-123")
	}
}

func TestNewComputationStartEntry(t *testing.T) {
	ruleIDs := []string{"rule1", "rule2", "rule3"}
	entry := NewComputationStartEntry("comp-456", ruleIDs)

	if entry.Type != EntryTypeComputationStart {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeComputationStart)
	}
	if entry.ComputationID != "comp-456" {
		t.Errorf("ComputationID = %v, want %v", entry.ComputationID, "comp-456")
	}
	if len(entry.RuleIDs) != 3 {
		t.Errorf("RuleIDs length = %d, want 3", len(entry.RuleIDs))
	}

	// Verify it's a copy
	ruleIDs[0] = "modified"
	if entry.RuleIDs[0] == "modified" {
		t.Error("NewComputationStartEntry should create a copy of ruleIDs slice")
	}
}

func TestNewComputationEndEntry(t *testing.T) {
	entry := NewComputationEndEntry("comp-789")

	if entry.Type != EntryTypeComputationEnd {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeComputationEnd)
	}
	if entry.ComputationID != "comp-789" {
		t.Errorf("ComputationID = %v, want %v", entry.ComputationID, "comp-789")
	}
}

// =============================================================================
// TCP.2 - Validate Tests
// =============================================================================

func TestCheckpointEntry_Validate_EdgeAdd(t *testing.T) {
	tests := []struct {
		name      string
		entry     *CheckpointEntry
		wantErr   error
	}{
		{
			name:    "valid EdgeAdd",
			entry:   NewEdgeAddEntry("s", "p", "o"),
			wantErr: nil,
		},
		{
			name: "missing subject",
			entry: &CheckpointEntry{
				Type:      EntryTypeEdgeAdd,
				Predicate: "p",
				Object:    "o",
			},
			wantErr: ErrMissingSubject,
		},
		{
			name: "missing predicate",
			entry: &CheckpointEntry{
				Type:    EntryTypeEdgeAdd,
				Subject: "s",
				Object:  "o",
			},
			wantErr: ErrMissingPredicate,
		},
		{
			name: "missing object",
			entry: &CheckpointEntry{
				Type:      EntryTypeEdgeAdd,
				Subject:   "s",
				Predicate: "p",
			},
			wantErr: ErrMissingObject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_EdgeRemove(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name:    "valid EdgeRemove",
			entry:   NewEdgeRemoveEntry("s", "p", "o"),
			wantErr: nil,
		},
		{
			name: "missing subject",
			entry: &CheckpointEntry{
				Type:      EntryTypeEdgeRemove,
				Predicate: "p",
				Object:    "o",
			},
			wantErr: ErrMissingSubject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_Delta(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name: "valid Delta",
			entry: NewDeltaEntry([]EdgeKey{
				{Subject: "s", Predicate: "p", Object: "o"},
			}),
			wantErr: nil,
		},
		{
			name: "nil edges",
			entry: &CheckpointEntry{
				Type:       EntryTypeDelta,
				DeltaEdges: nil,
			},
			wantErr: ErrMissingDeltaEdges,
		},
		{
			name:    "empty edges",
			entry:   NewDeltaEntry([]EdgeKey{}),
			wantErr: ErrMissingDeltaEdges,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_Stratum(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name:    "valid stratum 0",
			entry:   NewStratumEntry(0),
			wantErr: nil,
		},
		{
			name:    "valid stratum positive",
			entry:   NewStratumEntry(10),
			wantErr: nil,
		},
		{
			name: "negative stratum",
			entry: &CheckpointEntry{
				Type:    EntryTypeStratum,
				Stratum: -1,
			},
			wantErr: ErrInvalidStratum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_Checkpoint(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name:    "valid checkpoint",
			entry:   NewCheckpointMarker("cp-123"),
			wantErr: nil,
		},
		{
			name: "missing checkpoint ID",
			entry: &CheckpointEntry{
				Type:         EntryTypeCheckpoint,
				CheckpointID: "",
			},
			wantErr: ErrMissingCheckpointID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_ComputationStart(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name:    "valid computation start",
			entry:   NewComputationStartEntry("comp-1", []string{"rule1"}),
			wantErr: nil,
		},
		{
			name: "missing computation ID",
			entry: &CheckpointEntry{
				Type:    EntryTypeComputationStart,
				RuleIDs: []string{"rule1"},
			},
			wantErr: ErrMissingComputationID,
		},
		{
			name: "nil rule IDs",
			entry: &CheckpointEntry{
				Type:          EntryTypeComputationStart,
				ComputationID: "comp-1",
				RuleIDs:       nil,
			},
			wantErr: ErrMissingRuleIDs,
		},
		{
			name: "empty rule IDs",
			entry: &CheckpointEntry{
				Type:          EntryTypeComputationStart,
				ComputationID: "comp-1",
				RuleIDs:       []string{},
			},
			wantErr: ErrMissingRuleIDs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_ComputationEnd(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name:    "valid computation end",
			entry:   NewComputationEndEntry("comp-1"),
			wantErr: nil,
		},
		{
			name: "missing computation ID",
			entry: &CheckpointEntry{
				Type:          EntryTypeComputationEnd,
				ComputationID: "",
			},
			wantErr: ErrMissingComputationID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Validate_InvalidType(t *testing.T) {
	entry := &CheckpointEntry{
		Type: EntryType(0),
	}

	err := entry.Validate()
	if err != ErrInvalidEntryType {
		t.Errorf("Validate() error = %v, want %v", err, ErrInvalidEntryType)
	}
}

// =============================================================================
// TCP.2 - Size Tests
// =============================================================================

func TestCheckpointEntry_Size(t *testing.T) {
	tests := []struct {
		name     string
		entry    *CheckpointEntry
		minSize  int
	}{
		{
			name:    "empty entry",
			entry:   NewCheckpointEntry(EntryTypeCheckpoint),
			minSize: 91, // base overhead
		},
		{
			name:    "edge add entry",
			entry:   NewEdgeAddEntry("subject", "predicate", "object"),
			minSize: 91 + len("subject") + len("predicate") + len("object"),
		},
		{
			name: "delta entry with edges",
			entry: NewDeltaEntry([]EdgeKey{
				{Subject: "s1", Predicate: "p1", Object: "o1"},
				{Subject: "s2", Predicate: "p2", Object: "o2"},
			}),
			minSize: 91 + 2*(6+30), // 2 edges with ~6 chars each + overhead
		},
		{
			name:    "computation start with rules",
			entry:   NewComputationStartEntry("comp-id", []string{"rule1", "rule2", "rule3"}),
			minSize: 91 + len("comp-id") + 3*(5+3), // 3 rules with ~5 chars + quotes/comma
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := tt.entry.Size()
			if size < tt.minSize {
				t.Errorf("Size() = %d, want >= %d", size, tt.minSize)
			}
		})
	}
}

func TestCheckpointEntry_Size_Consistency(t *testing.T) {
	// Larger entries should have larger sizes
	small := NewEdgeAddEntry("a", "b", "c")
	large := NewEdgeAddEntry("aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc")

	if small.Size() >= large.Size() {
		t.Errorf("Larger entry should have larger size: small=%d, large=%d", small.Size(), large.Size())
	}
}

// =============================================================================
// TCP.2 - Clone Tests
// =============================================================================

func TestCheckpointEntry_Clone(t *testing.T) {
	original := NewEdgeAddEntry("s", "p", "o")
	original.SequenceNum = 42

	clone := original.Clone()

	// Verify all fields are copied
	if clone.Type != original.Type {
		t.Errorf("Clone().Type = %v, want %v", clone.Type, original.Type)
	}
	if clone.Subject != original.Subject {
		t.Errorf("Clone().Subject = %v, want %v", clone.Subject, original.Subject)
	}
	if clone.Predicate != original.Predicate {
		t.Errorf("Clone().Predicate = %v, want %v", clone.Predicate, original.Predicate)
	}
	if clone.Object != original.Object {
		t.Errorf("Clone().Object = %v, want %v", clone.Object, original.Object)
	}
	if clone.SequenceNum != original.SequenceNum {
		t.Errorf("Clone().SequenceNum = %v, want %v", clone.SequenceNum, original.SequenceNum)
	}
	if !clone.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Clone().Timestamp = %v, want %v", clone.Timestamp, original.Timestamp)
	}

	// Verify it's a deep copy (modifying original shouldn't affect clone)
	original.Subject = "modified"
	if clone.Subject == "modified" {
		t.Error("Clone should be a deep copy")
	}
}

func TestCheckpointEntry_Clone_WithSlices(t *testing.T) {
	edges := []EdgeKey{
		{Subject: "s1", Predicate: "p1", Object: "o1"},
		{Subject: "s2", Predicate: "p2", Object: "o2"},
	}
	original := NewDeltaEntry(edges)

	clone := original.Clone()

	// Verify slices are copied
	if len(clone.DeltaEdges) != len(original.DeltaEdges) {
		t.Errorf("Clone().DeltaEdges length = %d, want %d", len(clone.DeltaEdges), len(original.DeltaEdges))
	}

	// Verify it's a deep copy of slices
	original.DeltaEdges[0].Subject = "modified"
	if clone.DeltaEdges[0].Subject == "modified" {
		t.Error("Clone should deep copy DeltaEdges slice")
	}
}

func TestCheckpointEntry_Clone_WithRuleIDs(t *testing.T) {
	original := NewComputationStartEntry("comp-1", []string{"rule1", "rule2"})

	clone := original.Clone()

	// Verify RuleIDs are copied
	if len(clone.RuleIDs) != len(original.RuleIDs) {
		t.Errorf("Clone().RuleIDs length = %d, want %d", len(clone.RuleIDs), len(original.RuleIDs))
	}

	// Verify it's a deep copy
	original.RuleIDs[0] = "modified"
	if clone.RuleIDs[0] == "modified" {
		t.Error("Clone should deep copy RuleIDs slice")
	}
}

func TestCheckpointEntry_Clone_Nil(t *testing.T) {
	var entry *CheckpointEntry
	clone := entry.Clone()

	if clone != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestCheckpointEntry_Clone_NilSlices(t *testing.T) {
	original := &CheckpointEntry{
		Type:       EntryTypeDelta,
		DeltaEdges: nil,
		RuleIDs:    nil,
	}

	clone := original.Clone()

	if clone.DeltaEdges != nil {
		t.Error("Clone should preserve nil DeltaEdges")
	}
	if clone.RuleIDs != nil {
		t.Error("Clone should preserve nil RuleIDs")
	}
}

// =============================================================================
// TCP.2 - JSON Marshaling Tests
// =============================================================================

func TestCheckpointEntry_MarshalJSON(t *testing.T) {
	entry := NewEdgeAddEntry("subject1", "predicate1", "object1")
	entry.SequenceNum = 123

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify the type is serialized as a string
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	if typeStr, ok := raw["type"].(string); !ok || typeStr != "EdgeAdd" {
		t.Errorf("type field = %v, want 'EdgeAdd'", raw["type"])
	}
}

func TestCheckpointEntry_UnmarshalJSON(t *testing.T) {
	jsonData := `{
		"type": "EdgeAdd",
		"timestamp": "2024-01-15T10:30:00Z",
		"sequence_num": 456,
		"subject": "s",
		"predicate": "p",
		"object": "o"
	}`

	var entry CheckpointEntry
	if err := json.Unmarshal([]byte(jsonData), &entry); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if entry.Type != EntryTypeEdgeAdd {
		t.Errorf("Type = %v, want %v", entry.Type, EntryTypeEdgeAdd)
	}
	if entry.SequenceNum != 456 {
		t.Errorf("SequenceNum = %d, want 456", entry.SequenceNum)
	}
	if entry.Subject != "s" {
		t.Errorf("Subject = %v, want 's'", entry.Subject)
	}
}

func TestCheckpointEntry_UnmarshalJSON_AllTypes(t *testing.T) {
	for _, et := range AllEntryTypes() {
		t.Run(et.String(), func(t *testing.T) {
			jsonData := `{"type": "` + et.String() + `"}`

			var entry CheckpointEntry
			if err := json.Unmarshal([]byte(jsonData), &entry); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			if entry.Type != et {
				t.Errorf("Type = %v, want %v", entry.Type, et)
			}
		})
	}
}

func TestCheckpointEntry_UnmarshalJSON_InvalidType(t *testing.T) {
	jsonData := `{"type": "InvalidType"}`

	var entry CheckpointEntry
	err := json.Unmarshal([]byte(jsonData), &entry)
	if err != ErrInvalidEntryType {
		t.Errorf("Unmarshal with invalid type error = %v, want %v", err, ErrInvalidEntryType)
	}
}

func TestCheckpointEntry_RoundTrip(t *testing.T) {
	original := NewDeltaEntry([]EdgeKey{
		{Subject: "s1", Predicate: "p1", Object: "o1"},
		{Subject: "s2", Predicate: "p2", Object: "o2"},
	})
	original.SequenceNum = 789

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var restored CheckpointEntry
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify
	if restored.Type != original.Type {
		t.Errorf("Type mismatch: got %v, want %v", restored.Type, original.Type)
	}
	if restored.SequenceNum != original.SequenceNum {
		t.Errorf("SequenceNum mismatch: got %d, want %d", restored.SequenceNum, original.SequenceNum)
	}
	if len(restored.DeltaEdges) != len(original.DeltaEdges) {
		t.Errorf("DeltaEdges length mismatch: got %d, want %d", len(restored.DeltaEdges), len(original.DeltaEdges))
	}
	for i, edge := range restored.DeltaEdges {
		if edge != original.DeltaEdges[i] {
			t.Errorf("DeltaEdges[%d] mismatch: got %v, want %v", i, edge, original.DeltaEdges[i])
		}
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCheckpointEntry_EmptyStrings(t *testing.T) {
	// Test with empty strings in edge entry
	entry := NewEdgeAddEntry("", "", "")

	err := entry.Validate()
	if err != ErrMissingSubject {
		t.Errorf("Validate() with empty subject should return ErrMissingSubject, got %v", err)
	}
}

func TestCheckpointEntry_LongStrings(t *testing.T) {
	// Test with very long strings
	longString := make([]byte, 10000)
	for i := range longString {
		longString[i] = 'a'
	}

	entry := NewEdgeAddEntry(string(longString), "predicate", "object")

	if err := entry.Validate(); err != nil {
		t.Errorf("Validate() with long string should succeed, got %v", err)
	}

	size := entry.Size()
	if size < 10000 {
		t.Errorf("Size() should account for long string: got %d, want >= 10000", size)
	}
}

func TestCheckpointEntry_SpecialCharacters(t *testing.T) {
	// Test with special characters
	entry := NewEdgeAddEntry("sub\nject", "pred\ticate", "obj\"ect")

	if err := entry.Validate(); err != nil {
		t.Errorf("Validate() with special characters should succeed, got %v", err)
	}

	// Should be able to marshal/unmarshal
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal with special characters failed: %v", err)
	}

	var restored CheckpointEntry
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal with special characters failed: %v", err)
	}

	if restored.Subject != entry.Subject {
		t.Errorf("Subject mismatch after round-trip: got %q, want %q", restored.Subject, entry.Subject)
	}
}

func TestCheckpointEntry_UnicodeCharacters(t *testing.T) {
	// Test with unicode characters
	entry := NewEdgeAddEntry("主题", "谓语", "对象")

	if err := entry.Validate(); err != nil {
		t.Errorf("Validate() with unicode should succeed, got %v", err)
	}

	// Should be able to marshal/unmarshal
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal with unicode failed: %v", err)
	}

	var restored CheckpointEntry
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal with unicode failed: %v", err)
	}

	if restored.Subject != entry.Subject {
		t.Errorf("Subject mismatch after round-trip: got %q, want %q", restored.Subject, entry.Subject)
	}
}

// =============================================================================
// TCP.3 - ComputationState Tests
// =============================================================================

func TestNewComputationState(t *testing.T) {
	ruleIDs := []string{"rule1", "rule2", "rule3"}
	state := NewComputationState("comp-123", ruleIDs)

	if state.ComputationID != "comp-123" {
		t.Errorf("ComputationID = %v, want %v", state.ComputationID, "comp-123")
	}

	if len(state.RuleIDs) != 3 {
		t.Errorf("RuleIDs length = %d, want 3", len(state.RuleIDs))
	}

	for i, id := range ruleIDs {
		if state.RuleIDs[i] != id {
			t.Errorf("RuleIDs[%d] = %v, want %v", i, state.RuleIDs[i], id)
		}
	}

	if state.CurrentStratum != 0 {
		t.Errorf("CurrentStratum = %d, want 0", state.CurrentStratum)
	}

	if state.TotalStrata != 0 {
		t.Errorf("TotalStrata = %d, want 0", state.TotalStrata)
	}

	if state.DeltasSeen != 0 {
		t.Errorf("DeltasSeen = %d, want 0", state.DeltasSeen)
	}

	if state.EdgeCount != 0 {
		t.Errorf("EdgeCount = %d, want 0", state.EdgeCount)
	}

	if state.Completed {
		t.Error("Completed should be false initially")
	}

	if state.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}

	if state.LastUpdate.IsZero() {
		t.Error("LastUpdate should be set")
	}

	// Verify times are recent
	if time.Since(state.StartedAt) > time.Second {
		t.Error("StartedAt should be recent")
	}
}

func TestNewComputationState_RuleIDsCopy(t *testing.T) {
	ruleIDs := []string{"rule1", "rule2"}
	state := NewComputationState("comp-1", ruleIDs)

	// Modify the original slice
	ruleIDs[0] = "modified"

	// State should not be affected
	if state.RuleIDs[0] == "modified" {
		t.Error("NewComputationState should create a copy of ruleIDs")
	}
}

func TestComputationState_AdvanceStratum(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 5

	initialStratum := state.CurrentStratum
	initialUpdate := state.LastUpdate

	// Small delay to ensure time difference
	time.Sleep(time.Millisecond)

	state.AdvanceStratum()

	if state.CurrentStratum != initialStratum+1 {
		t.Errorf("CurrentStratum = %d, want %d", state.CurrentStratum, initialStratum+1)
	}

	if !state.LastUpdate.After(initialUpdate) {
		t.Error("LastUpdate should be updated after AdvanceStratum")
	}
}

func TestComputationState_AdvanceStratum_Multiple(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 10

	for i := 0; i < 5; i++ {
		state.AdvanceStratum()
	}

	if state.CurrentStratum != 5 {
		t.Errorf("CurrentStratum = %d, want 5 after 5 advances", state.CurrentStratum)
	}
}

func TestComputationState_AddDelta(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	initialUpdate := state.LastUpdate

	time.Sleep(time.Millisecond)

	state.AddDelta(10)

	if state.DeltasSeen != 1 {
		t.Errorf("DeltasSeen = %d, want 1", state.DeltasSeen)
	}

	if state.EdgeCount != 10 {
		t.Errorf("EdgeCount = %d, want 10", state.EdgeCount)
	}

	if !state.LastUpdate.After(initialUpdate) {
		t.Error("LastUpdate should be updated after AddDelta")
	}
}

func TestComputationState_AddDelta_Accumulation(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})

	state.AddDelta(5)
	state.AddDelta(10)
	state.AddDelta(15)

	if state.DeltasSeen != 3 {
		t.Errorf("DeltasSeen = %d, want 3", state.DeltasSeen)
	}

	if state.EdgeCount != 30 {
		t.Errorf("EdgeCount = %d, want 30", state.EdgeCount)
	}
}

func TestComputationState_AddDelta_ZeroEdges(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})

	state.AddDelta(0)

	if state.DeltasSeen != 1 {
		t.Errorf("DeltasSeen = %d, want 1", state.DeltasSeen)
	}

	if state.EdgeCount != 0 {
		t.Errorf("EdgeCount = %d, want 0", state.EdgeCount)
	}
}

func TestComputationState_MarkCompleted(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	initialUpdate := state.LastUpdate

	time.Sleep(time.Millisecond)

	state.MarkCompleted()

	if !state.Completed {
		t.Error("Completed should be true after MarkCompleted")
	}

	if !state.LastUpdate.After(initialUpdate) {
		t.Error("LastUpdate should be updated after MarkCompleted")
	}
}

func TestComputationState_Duration_InProgress(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})

	// Sleep a small amount
	time.Sleep(10 * time.Millisecond)

	duration := state.Duration()

	if duration < 10*time.Millisecond {
		t.Errorf("Duration = %v, want >= 10ms", duration)
	}
}

func TestComputationState_Duration_Completed(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})

	// Sleep a small amount
	time.Sleep(10 * time.Millisecond)

	state.MarkCompleted()

	// Sleep more after completion
	time.Sleep(10 * time.Millisecond)

	// Duration should not increase after completion
	duration := state.Duration()

	// Duration should be approximately 10ms (time between start and completion)
	// not 20ms (time since start)
	if duration > 15*time.Millisecond {
		t.Errorf("Duration = %v, should be around 10ms for completed computation", duration)
	}
}

func TestComputationState_Progress_NoStrata(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 0

	progress := state.Progress()

	if progress != 0.0 {
		t.Errorf("Progress = %v, want 0.0 when TotalStrata is 0", progress)
	}
}

func TestComputationState_Progress_InProgress(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 4
	state.CurrentStratum = 2

	progress := state.Progress()

	if progress != 0.5 {
		t.Errorf("Progress = %v, want 0.5", progress)
	}
}

func TestComputationState_Progress_Completed(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 4
	state.CurrentStratum = 2
	state.MarkCompleted()

	progress := state.Progress()

	if progress != 1.0 {
		t.Errorf("Progress = %v, want 1.0 for completed computation", progress)
	}
}

func TestComputationState_Progress_AllStrata(t *testing.T) {
	state := NewComputationState("comp-1", []string{"rule1"})
	state.TotalStrata = 5

	expectedProgress := []float64{0.0, 0.2, 0.4, 0.6, 0.8, 1.0}

	for i := 0; i <= 5; i++ {
		state.CurrentStratum = i
		progress := state.Progress()

		if progress != expectedProgress[i] {
			t.Errorf("Progress at stratum %d = %v, want %v", i, progress, expectedProgress[i])
		}
	}
}

func TestComputationState_Clone(t *testing.T) {
	original := NewComputationState("comp-1", []string{"rule1", "rule2"})
	original.CurrentStratum = 3
	original.TotalStrata = 5
	original.DeltasSeen = 10
	original.EdgeCount = 100
	original.Completed = true

	clone := original.Clone()

	// Verify all fields are copied
	if clone.ComputationID != original.ComputationID {
		t.Errorf("Clone().ComputationID = %v, want %v", clone.ComputationID, original.ComputationID)
	}

	if len(clone.RuleIDs) != len(original.RuleIDs) {
		t.Errorf("Clone().RuleIDs length = %d, want %d", len(clone.RuleIDs), len(original.RuleIDs))
	}

	if clone.CurrentStratum != original.CurrentStratum {
		t.Errorf("Clone().CurrentStratum = %d, want %d", clone.CurrentStratum, original.CurrentStratum)
	}

	if clone.TotalStrata != original.TotalStrata {
		t.Errorf("Clone().TotalStrata = %d, want %d", clone.TotalStrata, original.TotalStrata)
	}

	if clone.DeltasSeen != original.DeltasSeen {
		t.Errorf("Clone().DeltasSeen = %d, want %d", clone.DeltasSeen, original.DeltasSeen)
	}

	if clone.EdgeCount != original.EdgeCount {
		t.Errorf("Clone().EdgeCount = %d, want %d", clone.EdgeCount, original.EdgeCount)
	}

	if clone.Completed != original.Completed {
		t.Errorf("Clone().Completed = %v, want %v", clone.Completed, original.Completed)
	}

	if !clone.StartedAt.Equal(original.StartedAt) {
		t.Errorf("Clone().StartedAt = %v, want %v", clone.StartedAt, original.StartedAt)
	}

	if !clone.LastUpdate.Equal(original.LastUpdate) {
		t.Errorf("Clone().LastUpdate = %v, want %v", clone.LastUpdate, original.LastUpdate)
	}
}

func TestComputationState_Clone_DeepCopy(t *testing.T) {
	original := NewComputationState("comp-1", []string{"rule1", "rule2"})

	clone := original.Clone()

	// Modify original
	original.RuleIDs[0] = "modified"
	original.CurrentStratum = 99

	// Clone should not be affected
	if clone.RuleIDs[0] == "modified" {
		t.Error("Clone should create a deep copy of RuleIDs")
	}

	if clone.CurrentStratum == 99 {
		t.Error("Clone should be independent of original")
	}
}

func TestComputationState_Clone_Nil(t *testing.T) {
	var state *ComputationState
	clone := state.Clone()

	if clone != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestComputationState_Clone_EmptyRuleIDs(t *testing.T) {
	original := NewComputationState("comp-1", []string{})

	clone := original.Clone()

	if clone.RuleIDs == nil {
		t.Error("Clone should preserve empty RuleIDs slice (not nil)")
	}

	if len(clone.RuleIDs) != 0 {
		t.Errorf("Clone().RuleIDs length = %d, want 0", len(clone.RuleIDs))
	}
}

// =============================================================================
// TCP.4 - TCCheckpointer Tests
// =============================================================================

func TestNewTCCheckpointer(t *testing.T) {
	checkpointer, err := NewTCCheckpointer("/tmp/test.wal")

	if err != nil {
		t.Fatalf("NewTCCheckpointer failed: %v", err)
	}

	if checkpointer == nil {
		t.Fatal("NewTCCheckpointer returned nil")
	}

	if checkpointer.GetLogPath() != "/tmp/test.wal" {
		t.Errorf("GetLogPath() = %v, want %v", checkpointer.GetLogPath(), "/tmp/test.wal")
	}

	if checkpointer.IsOpen() {
		t.Error("New checkpointer should not be open")
	}

	if checkpointer.checkpointInterval != DefaultCheckpointInterval {
		t.Errorf("checkpointInterval = %v, want %v", checkpointer.checkpointInterval, DefaultCheckpointInterval)
	}

	if checkpointer.bufferSize != DefaultBufferSize {
		t.Errorf("bufferSize = %v, want %v", checkpointer.bufferSize, DefaultBufferSize)
	}
}

func TestNewTCCheckpointer_EmptyPath(t *testing.T) {
	_, err := NewTCCheckpointer("")

	if err != ErrInvalidLogPath {
		t.Errorf("NewTCCheckpointer(\"\") error = %v, want %v", err, ErrInvalidLogPath)
	}
}

func TestTCCheckpointer_OpenClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, err := NewTCCheckpointer(logPath)
	if err != nil {
		t.Fatalf("NewTCCheckpointer failed: %v", err)
	}

	// Open
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	if !checkpointer.IsOpen() {
		t.Error("IsOpen() should return true after Open()")
	}

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("WAL file should be created after Open()")
	}

	// Close
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	if checkpointer.IsOpen() {
		t.Error("IsOpen() should return false after Close()")
	}
}

func TestTCCheckpointer_OpenAlreadyOpen(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.Open()
	defer checkpointer.Close()

	// Try to open again
	err := checkpointer.Open()

	if err != ErrCheckpointerAlreadyOpen {
		t.Errorf("Open() on already open checkpointer error = %v, want %v", err, ErrCheckpointerAlreadyOpen)
	}
}

func TestTCCheckpointer_CloseAlreadyClosed(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.Open()
	checkpointer.Close()

	// Try to close again - should be idempotent (no error)
	err := checkpointer.Close()

	if err != nil {
		t.Errorf("Close() on already closed checkpointer should be idempotent, got error = %v", err)
	}
}

func TestTCCheckpointer_CloseWithoutOpen(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Try to close without opening - should be idempotent (no error)
	err := checkpointer.Close()

	if err != nil {
		t.Errorf("Close() without open should be idempotent, got error = %v", err)
	}
}

func TestTCCheckpointer_OpenInvalidPath(t *testing.T) {
	// Try to open with a path in a non-existent directory
	checkpointer, _ := NewTCCheckpointer("/nonexistent/directory/test.wal")

	err := checkpointer.Open()

	if err == nil {
		checkpointer.Close()
		t.Error("Open() with invalid path should fail")
	}
}

func TestTCCheckpointer_GetLogPath(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/path/to/wal.log")

	if checkpointer.GetLogPath() != "/path/to/wal.log" {
		t.Errorf("GetLogPath() = %v, want %v", checkpointer.GetLogPath(), "/path/to/wal.log")
	}
}

func TestTCCheckpointer_GetSequenceNum(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Initial sequence number should be 0
	if checkpointer.GetSequenceNum() != 0 {
		t.Errorf("GetSequenceNum() = %v, want 0", checkpointer.GetSequenceNum())
	}

	// Manually increment sequence number
	checkpointer.sequenceNum.Add(5)

	if checkpointer.GetSequenceNum() != 5 {
		t.Errorf("GetSequenceNum() = %v, want 5", checkpointer.GetSequenceNum())
	}
}

func TestTCCheckpointer_GetCurrentComputation_Nil(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	comp := checkpointer.GetCurrentComputation()

	if comp != nil {
		t.Error("GetCurrentComputation() should return nil when no computation is set")
	}
}

func TestTCCheckpointer_GetCurrentComputation_Clone(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Manually set current computation
	checkpointer.currentComp = NewComputationState("comp-1", []string{"rule1"})
	checkpointer.currentComp.CurrentStratum = 3

	comp := checkpointer.GetCurrentComputation()

	if comp == nil {
		t.Fatal("GetCurrentComputation() returned nil")
	}

	if comp.ComputationID != "comp-1" {
		t.Errorf("ComputationID = %v, want %v", comp.ComputationID, "comp-1")
	}

	if comp.CurrentStratum != 3 {
		t.Errorf("CurrentStratum = %d, want 3", comp.CurrentStratum)
	}

	// Modify returned computation
	comp.CurrentStratum = 99

	// Original should not be affected
	if checkpointer.currentComp.CurrentStratum == 99 {
		t.Error("GetCurrentComputation should return a clone")
	}
}

func TestTCCheckpointer_SetCheckpointInterval(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	checkpointer.SetCheckpointInterval(10 * time.Minute)

	if checkpointer.checkpointInterval != 10*time.Minute {
		t.Errorf("checkpointInterval = %v, want %v", checkpointer.checkpointInterval, 10*time.Minute)
	}
}

func TestTCCheckpointer_SetBufferSize(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	checkpointer.SetBufferSize(500)

	if checkpointer.bufferSize != 500 {
		t.Errorf("bufferSize = %v, want 500", checkpointer.bufferSize)
	}
}

func TestTCCheckpointer_SetBufferSize_MinimumOne(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Set to 0 should become 1
	checkpointer.SetBufferSize(0)

	if checkpointer.bufferSize != 1 {
		t.Errorf("bufferSize = %v, want 1 (minimum)", checkpointer.bufferSize)
	}

	// Set to negative should become 1
	checkpointer.SetBufferSize(-10)

	if checkpointer.bufferSize != 1 {
		t.Errorf("bufferSize = %v, want 1 (minimum)", checkpointer.bufferSize)
	}
}

func TestTCCheckpointer_SetBufferSize_TruncatesExisting(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Add some entries to buffer
	checkpointer.buffer = append(checkpointer.buffer,
		NewEdgeAddEntry("s1", "p1", "o1"),
		NewEdgeAddEntry("s2", "p2", "o2"),
		NewEdgeAddEntry("s3", "p3", "o3"),
		NewEdgeAddEntry("s4", "p4", "o4"),
		NewEdgeAddEntry("s5", "p5", "o5"),
	)

	// Reduce buffer size
	checkpointer.SetBufferSize(3)

	if len(checkpointer.buffer) != 3 {
		t.Errorf("buffer length = %d, want 3 after truncation", len(checkpointer.buffer))
	}
}

func TestTCCheckpointer_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.Open()
	defer checkpointer.Close()

	var wg sync.WaitGroup
	iterations := 100

	// Test concurrent GetSequenceNum
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func() {
			defer wg.Done()
			_ = checkpointer.GetSequenceNum()
		}()
	}

	// Test concurrent IsOpen
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func() {
			defer wg.Done()
			_ = checkpointer.IsOpen()
		}()
	}

	// Test concurrent SetCheckpointInterval
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			checkpointer.SetCheckpointInterval(time.Duration(n) * time.Second)
		}(i)
	}

	// Test concurrent SetBufferSize
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			checkpointer.SetBufferSize(n + 1)
		}(i)
	}

	// Test concurrent GetCurrentComputation
	checkpointer.currentComp = NewComputationState("comp-1", []string{"rule1"})
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func() {
			defer wg.Done()
			_ = checkpointer.GetCurrentComputation()
		}()
	}

	wg.Wait()
}

func TestTCCheckpointer_ReopenAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)

	// First open/close cycle
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("First Open() failed: %v", err)
	}
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("First Close() failed: %v", err)
	}

	// Second open/close cycle
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Second Open() failed: %v", err)
	}
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("Second Close() failed: %v", err)
	}
}

func TestTCCheckpointer_DefaultConstants(t *testing.T) {
	// Verify default constants have sensible values
	if DefaultCheckpointInterval != 5*time.Minute {
		t.Errorf("DefaultCheckpointInterval = %v, want 5 minutes", DefaultCheckpointInterval)
	}

	if DefaultBufferSize != 100 {
		t.Errorf("DefaultBufferSize = %d, want 100", DefaultBufferSize)
	}
}

func TestTCCheckpointer_ErrorConstants(t *testing.T) {
	// Verify error constants are properly defined
	if ErrCheckpointerClosed.Error() != "checkpointer is closed" {
		t.Errorf("ErrCheckpointerClosed message unexpected: %v", ErrCheckpointerClosed)
	}

	if ErrCheckpointerNotOpen.Error() != "checkpointer is not open" {
		t.Errorf("ErrCheckpointerNotOpen message unexpected: %v", ErrCheckpointerNotOpen)
	}

	if ErrCheckpointerAlreadyOpen.Error() != "checkpointer is already open" {
		t.Errorf("ErrCheckpointerAlreadyOpen message unexpected: %v", ErrCheckpointerAlreadyOpen)
	}

	if ErrInvalidLogPath.Error() != "invalid log path" {
		t.Errorf("ErrInvalidLogPath message unexpected: %v", ErrInvalidLogPath)
	}
}

// =============================================================================
// TCP.5 - writeEntry Tests
// =============================================================================

func TestTCCheckpointer_writeEntry_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	entry := NewEdgeAddEntry("subject", "predicate", "object")

	if err := checkpointer.writeEntry(entry); err != nil {
		t.Fatalf("writeEntry() failed: %v", err)
	}

	// Verify sequence number was assigned
	if entry.SequenceNum != 1 {
		t.Errorf("SequenceNum = %d, want 1", entry.SequenceNum)
	}

	// Verify checkpointer's sequence number advanced
	if checkpointer.GetSequenceNum() != 1 {
		t.Errorf("GetSequenceNum() = %d, want 1", checkpointer.GetSequenceNum())
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Errorf("buffer length = %d, want 1", len(checkpointer.buffer))
	}
}

func TestTCCheckpointer_writeEntry_BufferFlush(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(3) // Small buffer for testing
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Write 3 entries (should trigger flush)
	for i := 0; i < 3; i++ {
		entry := NewEdgeAddEntry("s"+string(rune('1'+i)), "p", "o")
		if err := checkpointer.writeEntry(entry); err != nil {
			t.Fatalf("writeEntry() failed on entry %d: %v", i, err)
		}
	}

	// Buffer should be empty after flush
	if len(checkpointer.buffer) != 0 {
		t.Errorf("buffer length = %d, want 0 after flush", len(checkpointer.buffer))
	}

	// Verify file has content
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if info.Size() == 0 {
		t.Error("WAL file should have content after flush")
	}
}

func TestTCCheckpointer_writeEntry_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	entry := NewEdgeAddEntry("s", "p", "o")
	err := checkpointer.writeEntry(entry)

	if err != ErrCheckpointerClosed {
		t.Errorf("writeEntry() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_writeEntry_NotOpen(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	checkpointer.Close()

	entry := NewEdgeAddEntry("s", "p", "o")
	err := checkpointer.writeEntry(entry)

	if err != ErrCheckpointerClosed {
		t.Errorf("writeEntry() on not open checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_writeEntry_InvalidEntry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Create invalid entry (missing subject)
	entry := &CheckpointEntry{
		Type:      EntryTypeEdgeAdd,
		Predicate: "p",
		Object:    "o",
	}

	err := checkpointer.writeEntry(entry)

	if err == nil {
		t.Error("writeEntry() with invalid entry should fail")
	}
}

func TestTCCheckpointer_writeEntry_SequenceIncrement(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100) // Large buffer to avoid auto-flush
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Write multiple entries
	for i := 1; i <= 5; i++ {
		entry := NewEdgeAddEntry("s", "p", "o")
		if err := checkpointer.writeEntry(entry); err != nil {
			t.Fatalf("writeEntry() failed: %v", err)
		}
		if entry.SequenceNum != uint64(i) {
			t.Errorf("Entry %d SequenceNum = %d, want %d", i, entry.SequenceNum, i)
		}
	}

	if checkpointer.GetSequenceNum() != 5 {
		t.Errorf("GetSequenceNum() = %d, want 5", checkpointer.GetSequenceNum())
	}
}

// =============================================================================
// TCP.5 - Flush Tests
// =============================================================================

func TestTCCheckpointer_Flush_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100) // Large buffer to prevent auto-flush
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Write some entries
	for i := 0; i < 5; i++ {
		entry := NewEdgeAddEntry("s", "p", "o")
		if err := checkpointer.writeEntry(entry); err != nil {
			t.Fatalf("writeEntry() failed: %v", err)
		}
	}

	// Buffer should have entries
	if len(checkpointer.buffer) != 5 {
		t.Errorf("buffer length = %d, want 5", len(checkpointer.buffer))
	}

	// Flush
	if err := checkpointer.Flush(); err != nil {
		t.Fatalf("Flush() failed: %v", err)
	}

	// Buffer should be empty
	if len(checkpointer.buffer) != 0 {
		t.Errorf("buffer length after Flush() = %d, want 0", len(checkpointer.buffer))
	}

	// Verify file has content
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if info.Size() == 0 {
		t.Error("WAL file should have content after Flush()")
	}
}

func TestTCCheckpointer_Flush_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Flush with no entries
	if err := checkpointer.Flush(); err != nil {
		t.Fatalf("Flush() on empty buffer failed: %v", err)
	}

	// Buffer should still be empty
	if len(checkpointer.buffer) != 0 {
		t.Errorf("buffer length = %d, want 0", len(checkpointer.buffer))
	}
}

func TestTCCheckpointer_Flush_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.Flush()

	if err != ErrCheckpointerClosed {
		t.Errorf("Flush() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_Flush_NotOpen(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.Open()
	checkpointer.Close()

	err := checkpointer.Flush()

	if err != ErrCheckpointerClosed {
		t.Errorf("Flush() on not open checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

// =============================================================================
// TCP.6 - Log Method Tests
// =============================================================================

func TestTCCheckpointer_LogEdgeAdd(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	if err := checkpointer.LogEdgeAdd("subject", "predicate", "object"); err != nil {
		t.Fatalf("LogEdgeAdd() failed: %v", err)
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(checkpointer.buffer))
	}

	entry := checkpointer.buffer[0]
	if entry.Type != EntryTypeEdgeAdd {
		t.Errorf("entry.Type = %v, want %v", entry.Type, EntryTypeEdgeAdd)
	}
	if entry.Subject != "subject" {
		t.Errorf("entry.Subject = %v, want %v", entry.Subject, "subject")
	}
	if entry.Predicate != "predicate" {
		t.Errorf("entry.Predicate = %v, want %v", entry.Predicate, "predicate")
	}
	if entry.Object != "object" {
		t.Errorf("entry.Object = %v, want %v", entry.Object, "object")
	}
}

func TestTCCheckpointer_LogEdgeRemove(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	if err := checkpointer.LogEdgeRemove("subject", "predicate", "object"); err != nil {
		t.Fatalf("LogEdgeRemove() failed: %v", err)
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(checkpointer.buffer))
	}

	entry := checkpointer.buffer[0]
	if entry.Type != EntryTypeEdgeRemove {
		t.Errorf("entry.Type = %v, want %v", entry.Type, EntryTypeEdgeRemove)
	}
	if entry.Subject != "subject" {
		t.Errorf("entry.Subject = %v, want %v", entry.Subject, "subject")
	}
}

func TestTCCheckpointer_LogDelta(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	edges := []EdgeKey{
		{Subject: "s1", Predicate: "p1", Object: "o1"},
		{Subject: "s2", Predicate: "p2", Object: "o2"},
	}

	if err := checkpointer.LogDelta(edges); err != nil {
		t.Fatalf("LogDelta() failed: %v", err)
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(checkpointer.buffer))
	}

	entry := checkpointer.buffer[0]
	if entry.Type != EntryTypeDelta {
		t.Errorf("entry.Type = %v, want %v", entry.Type, EntryTypeDelta)
	}
	if len(entry.DeltaEdges) != 2 {
		t.Errorf("entry.DeltaEdges length = %d, want 2", len(entry.DeltaEdges))
	}
}

func TestTCCheckpointer_LogDelta_UpdatesComputationState(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Start a computation
	checkpointer.StartComputation("comp-1", []string{"rule1"})

	edges := []EdgeKey{
		{Subject: "s1", Predicate: "p1", Object: "o1"},
		{Subject: "s2", Predicate: "p2", Object: "o2"},
	}

	if err := checkpointer.LogDelta(edges); err != nil {
		t.Fatalf("LogDelta() failed: %v", err)
	}

	// Verify computation state was updated
	comp := checkpointer.GetCurrentComputation()
	if comp.DeltasSeen != 1 {
		t.Errorf("DeltasSeen = %d, want 1", comp.DeltasSeen)
	}
	if comp.EdgeCount != 2 {
		t.Errorf("EdgeCount = %d, want 2", comp.EdgeCount)
	}
}

func TestTCCheckpointer_LogStratum(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	if err := checkpointer.LogStratum(3); err != nil {
		t.Fatalf("LogStratum() failed: %v", err)
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(checkpointer.buffer))
	}

	entry := checkpointer.buffer[0]
	if entry.Type != EntryTypeStratum {
		t.Errorf("entry.Type = %v, want %v", entry.Type, EntryTypeStratum)
	}
	if entry.Stratum != 3 {
		t.Errorf("entry.Stratum = %d, want 3", entry.Stratum)
	}
}

func TestTCCheckpointer_LogStratum_UpdatesComputationState(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Start a computation
	checkpointer.StartComputation("comp-1", []string{"rule1"})

	// Log a stratum completion
	if err := checkpointer.LogStratum(0); err != nil {
		t.Fatalf("LogStratum() failed: %v", err)
	}

	// Verify computation state was updated
	comp := checkpointer.GetCurrentComputation()
	if comp.CurrentStratum != 1 {
		t.Errorf("CurrentStratum = %d, want 1", comp.CurrentStratum)
	}
}

func TestTCCheckpointer_LogCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Add some entries first
	checkpointer.LogEdgeAdd("s", "p", "o")

	if err := checkpointer.LogCheckpoint(); err != nil {
		t.Fatalf("LogCheckpoint() failed: %v", err)
	}

	// Buffer should be empty (checkpoint forces flush)
	if len(checkpointer.buffer) != 0 {
		t.Errorf("buffer length = %d, want 0 after checkpoint", len(checkpointer.buffer))
	}

	// Verify file has content
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if info.Size() == 0 {
		t.Error("WAL file should have content after LogCheckpoint()")
	}
}

func TestTCCheckpointer_StartEndComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Start computation
	if err := checkpointer.StartComputation("comp-123", []string{"rule1", "rule2"}); err != nil {
		t.Fatalf("StartComputation() failed: %v", err)
	}

	// Verify computation state was initialized
	comp := checkpointer.GetCurrentComputation()
	if comp == nil {
		t.Fatal("GetCurrentComputation() returned nil after StartComputation()")
	}
	if comp.ComputationID != "comp-123" {
		t.Errorf("ComputationID = %v, want %v", comp.ComputationID, "comp-123")
	}
	if len(comp.RuleIDs) != 2 {
		t.Errorf("RuleIDs length = %d, want 2", len(comp.RuleIDs))
	}
	if comp.Completed {
		t.Error("Computation should not be completed after StartComputation()")
	}

	// Verify entry was added to buffer
	if len(checkpointer.buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(checkpointer.buffer))
	}

	entry := checkpointer.buffer[0]
	if entry.Type != EntryTypeComputationStart {
		t.Errorf("entry.Type = %v, want %v", entry.Type, EntryTypeComputationStart)
	}

	// End computation
	if err := checkpointer.EndComputation("comp-123"); err != nil {
		t.Fatalf("EndComputation() failed: %v", err)
	}

	// Verify computation state was marked complete
	comp = checkpointer.GetCurrentComputation()
	if !comp.Completed {
		t.Error("Computation should be completed after EndComputation()")
	}

	// Buffer should be empty (EndComputation forces flush)
	if len(checkpointer.buffer) != 0 {
		t.Errorf("buffer length = %d, want 0 after EndComputation()", len(checkpointer.buffer))
	}
}

func TestTCCheckpointer_EndComputation_WrongID(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Start computation with one ID
	checkpointer.StartComputation("comp-1", []string{"rule1"})

	// End with different ID
	if err := checkpointer.EndComputation("comp-2"); err != nil {
		t.Fatalf("EndComputation() failed: %v", err)
	}

	// Original computation should not be marked complete
	comp := checkpointer.GetCurrentComputation()
	if comp.Completed {
		t.Error("Computation should not be completed when EndComputation called with wrong ID")
	}
}

func TestTCCheckpointer_ShouldCheckpoint(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// Set a very short checkpoint interval
	checkpointer.SetCheckpointInterval(1 * time.Millisecond)
	checkpointer.lastCheckpoint = time.Now()

	// Initially should not need checkpoint
	if checkpointer.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint() should return false immediately after checkpoint")
	}

	// Wait for interval to pass
	time.Sleep(2 * time.Millisecond)

	// Now should need checkpoint
	if !checkpointer.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint() should return true after interval")
	}
}

func TestTCCheckpointer_ShouldCheckpoint_Default(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	// With default interval (5 minutes), should not need checkpoint
	checkpointer.lastCheckpoint = time.Now()

	if checkpointer.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint() should return false with default interval")
	}
}

func TestTCCheckpointer_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1000) // Large buffer to prevent auto-flush
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent LogEdgeAdd
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			checkpointer.LogEdgeAdd("s", "p", "o")
		}(i)
	}

	// Concurrent LogEdgeRemove
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			checkpointer.LogEdgeRemove("s", "p", "o")
		}(i)
	}

	// Concurrent LogStratum
	wg.Add(iterations)
	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			checkpointer.LogStratum(n)
		}(i)
	}

	wg.Wait()

	// Verify all entries were written
	expectedEntries := iterations * 3
	if len(checkpointer.buffer) != expectedEntries {
		t.Errorf("buffer length = %d, want %d", len(checkpointer.buffer), expectedEntries)
	}

	// Verify sequence numbers are unique and sequential
	seqNums := make(map[uint64]bool)
	for _, entry := range checkpointer.buffer {
		if seqNums[entry.SequenceNum] {
			t.Errorf("Duplicate sequence number: %d", entry.SequenceNum)
		}
		seqNums[entry.SequenceNum] = true
	}

	// Flush and verify
	if err := checkpointer.Flush(); err != nil {
		t.Fatalf("Flush() failed: %v", err)
	}

	// Verify file has content
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if info.Size() == 0 {
		t.Error("WAL file should have content")
	}
}

func TestTCCheckpointer_RoundTrip_ReadEntries(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	// Write entries
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1) // Flush after each entry
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	checkpointer.LogEdgeAdd("s1", "p1", "o1")
	checkpointer.LogEdgeRemove("s2", "p2", "o2")
	checkpointer.LogDelta([]EdgeKey{{Subject: "s3", Predicate: "p3", Object: "o3"}})
	checkpointer.LogStratum(1)

	checkpointer.Close()

	// Read entries back
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Open file for reading failed: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var entries []*CheckpointEntry

	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		entries = append(entries, &entry)
	}

	if len(entries) != 4 {
		t.Fatalf("Expected 4 entries, got %d", len(entries))
	}

	// Verify entry types
	expectedTypes := []EntryType{
		EntryTypeEdgeAdd,
		EntryTypeEdgeRemove,
		EntryTypeDelta,
		EntryTypeStratum,
	}

	for i, entry := range entries {
		if entry.Type != expectedTypes[i] {
			t.Errorf("Entry %d type = %v, want %v", i, entry.Type, expectedTypes[i])
		}
		if entry.SequenceNum != uint64(i+1) {
			t.Errorf("Entry %d SequenceNum = %d, want %d", i, entry.SequenceNum, i+1)
		}
	}
}

func TestTCCheckpointer_LogEdgeAdd_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.LogEdgeAdd("s", "p", "o")

	if err != ErrCheckpointerClosed {
		t.Errorf("LogEdgeAdd() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_LogEdgeRemove_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.LogEdgeRemove("s", "p", "o")

	if err != ErrCheckpointerClosed {
		t.Errorf("LogEdgeRemove() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_LogDelta_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})

	if err != ErrCheckpointerClosed {
		t.Errorf("LogDelta() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_LogStratum_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.LogStratum(0)

	if err != ErrCheckpointerClosed {
		t.Errorf("LogStratum() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_LogCheckpoint_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.LogCheckpoint()

	if err != ErrCheckpointerClosed {
		t.Errorf("LogCheckpoint() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_StartComputation_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.StartComputation("comp-1", []string{"rule1"})

	if err != ErrCheckpointerClosed {
		t.Errorf("StartComputation() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_EndComputation_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")

	err := checkpointer.EndComputation("comp-1")

	if err != ErrCheckpointerClosed {
		t.Errorf("EndComputation() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestTCCheckpointer_MultipleComputations(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// First computation
	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})
	checkpointer.LogStratum(0)
	checkpointer.EndComputation("comp-1")

	// Second computation
	checkpointer.StartComputation("comp-2", []string{"rule2", "rule3"})

	comp := checkpointer.GetCurrentComputation()
	if comp.ComputationID != "comp-2" {
		t.Errorf("ComputationID = %v, want %v", comp.ComputationID, "comp-2")
	}
	if len(comp.RuleIDs) != 2 {
		t.Errorf("RuleIDs length = %d, want 2", len(comp.RuleIDs))
	}
	// New computation should start fresh
	if comp.DeltasSeen != 0 {
		t.Errorf("DeltasSeen = %d, want 0 for new computation", comp.DeltasSeen)
	}
	if comp.CurrentStratum != 0 {
		t.Errorf("CurrentStratum = %d, want 0 for new computation", comp.CurrentStratum)
	}
}

// =============================================================================
// TCP.7 - Recovery Tests
// =============================================================================

func TestTCCheckpointer_RecoverFromFile_NoFile(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/nonexistent.wal")

	state, err := checkpointer.RecoverFromFile("/tmp/nonexistent_recovery.wal")

	if err != nil {
		t.Errorf("RecoverFromFile() with nonexistent file error = %v, want nil", err)
	}
	if state != nil {
		t.Errorf("RecoverFromFile() with nonexistent file state = %v, want nil", state)
	}
}

func TestTCCheckpointer_RecoverFromFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "empty.wal")

	// Create empty file
	file, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("Create empty file failed: %v", err)
	}
	file.Close()

	checkpointer, _ := NewTCCheckpointer(logPath)

	state, err := checkpointer.RecoverFromFile(logPath)

	if err != nil {
		t.Errorf("RecoverFromFile() with empty file error = %v, want nil", err)
	}
	if state != nil {
		t.Errorf("RecoverFromFile() with empty file state = %v, want nil", state)
	}
}

func TestTCCheckpointer_RecoverFromFile_CompletedComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "completed.wal")

	// Write a completed computation
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1) // Force immediate flush
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})
	checkpointer.LogStratum(0)
	checkpointer.EndComputation("comp-1")
	checkpointer.Flush()
	checkpointer.Close()

	// Recover
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, err := checkpointer2.RecoverFromFile(logPath)

	if err != nil {
		t.Errorf("RecoverFromFile() error = %v, want nil", err)
	}
	if state != nil {
		t.Errorf("RecoverFromFile() with completed computation state = %v, want nil", state)
	}
}

func TestTCCheckpointer_RecoverFromFile_InProgressComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "inprogress.wal")

	// Write an in-progress computation (no end)
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1) // Force immediate flush
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	checkpointer.StartComputation("comp-1", []string{"rule1", "rule2"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s1", Predicate: "p1", Object: "o1"}})
	checkpointer.LogStratum(0)
	checkpointer.LogDelta([]EdgeKey{{Subject: "s2", Predicate: "p2", Object: "o2"}})
	checkpointer.Flush()
	checkpointer.Close()

	// Recover
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, err := checkpointer2.RecoverFromFile(logPath)

	if err != nil {
		t.Fatalf("RecoverFromFile() error = %v", err)
	}
	if state == nil {
		t.Fatal("RecoverFromFile() should return state for in-progress computation")
	}

	if state.ComputationID != "comp-1" {
		t.Errorf("ComputationID = %v, want %v", state.ComputationID, "comp-1")
	}
	if state.Phase != "started" {
		t.Errorf("Phase = %v, want %v", state.Phase, "started")
	}
	if len(state.RuleIDs) != 2 {
		t.Errorf("RuleIDs length = %d, want 2", len(state.RuleIDs))
	}

	// Should have pending entries (delta, stratum, delta)
	if len(state.PendingEntries) != 3 {
		t.Errorf("PendingEntries length = %d, want 3", len(state.PendingEntries))
	}
}

func TestTCCheckpointer_RecoverFromFile_AbortedComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "aborted.wal")

	// Write an aborted computation
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})
	checkpointer.AbortComputation("comp-1", "test abort")
	checkpointer.Flush()
	checkpointer.Close()

	// Recover
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, err := checkpointer2.RecoverFromFile(logPath)

	if err != nil {
		t.Errorf("RecoverFromFile() error = %v, want nil", err)
	}
	if state != nil {
		t.Errorf("RecoverFromFile() with aborted computation state = %v, want nil", state)
	}
}

func TestTCCheckpointer_RecoverFromFile_WithCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "checkpoint.wal")

	// Write a computation with a checkpoint
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s1", Predicate: "p1", Object: "o1"}})
	checkpointer.LogCheckpointWithProgress("comp-1", "phase1", 0.5)
	checkpointer.LogDelta([]EdgeKey{{Subject: "s2", Predicate: "p2", Object: "o2"}})
	checkpointer.Flush()
	checkpointer.Close()

	// Recover
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, err := checkpointer2.RecoverFromFile(logPath)

	if err != nil {
		t.Fatalf("RecoverFromFile() error = %v", err)
	}
	if state == nil {
		t.Fatal("RecoverFromFile() should return state for checkpointed computation")
	}

	if state.ComputationID != "comp-1" {
		t.Errorf("ComputationID = %v, want %v", state.ComputationID, "comp-1")
	}
	if state.Phase != "phase1" {
		t.Errorf("Phase = %v, want %v", state.Phase, "phase1")
	}
	if state.ProgressValue != 0.5 {
		t.Errorf("ProgressValue = %v, want %v", state.ProgressValue, 0.5)
	}

	// Should only have entries after checkpoint
	if len(state.PendingEntries) != 1 {
		t.Errorf("PendingEntries length = %d, want 1 (only delta after checkpoint)", len(state.PendingEntries))
	}
}

func TestTCCheckpointer_RecoverFromFile_MultipleComputations(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "multiple.wal")

	// Write multiple computations, last one incomplete
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	// First computation - complete
	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.EndComputation("comp-1")

	// Second computation - incomplete
	checkpointer.StartComputation("comp-2", []string{"rule2", "rule3"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})
	checkpointer.Flush()
	checkpointer.Close()

	// Recover
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, err := checkpointer2.RecoverFromFile(logPath)

	if err != nil {
		t.Fatalf("RecoverFromFile() error = %v", err)
	}
	if state == nil {
		t.Fatal("RecoverFromFile() should return state for last incomplete computation")
	}

	// Should recover the second (incomplete) computation
	if state.ComputationID != "comp-2" {
		t.Errorf("ComputationID = %v, want %v", state.ComputationID, "comp-2")
	}
	if len(state.RuleIDs) != 2 {
		t.Errorf("RuleIDs length = %d, want 2", len(state.RuleIDs))
	}
}

// =============================================================================
// TCP.8 - StartComputationWithHandle Tests
// =============================================================================

func TestTCCheckpointer_StartComputationWithHandle(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "handle.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	handle, err := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1", "rule2"})

	if err != nil {
		t.Fatalf("StartComputationWithHandle() error = %v", err)
	}
	if handle == nil {
		t.Fatal("StartComputationWithHandle() should return handle")
	}

	if handle.ID != "comp-1" {
		t.Errorf("Handle.ID = %v, want %v", handle.ID, "comp-1")
	}
	if handle.State == nil {
		t.Fatal("Handle.State should not be nil")
	}
	if handle.State.Phase != "init" {
		t.Errorf("State.Phase = %v, want %v", handle.State.Phase, "init")
	}
	if handle.Context() != ctx {
		t.Error("Handle.Context() should return the provided context")
	}

	// Should have active computation
	activeComp := checkpointer.GetActiveComputation()
	if activeComp == nil {
		t.Fatal("GetActiveComputation() should not be nil")
	}
	if activeComp.ID != "comp-1" {
		t.Errorf("ActiveComputation.ID = %v, want %v", activeComp.ID, "comp-1")
	}
}

func TestTCCheckpointer_StartComputationWithHandle_AlreadyInProgress(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "duplicate.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	_, err := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})
	if err != nil {
		t.Fatalf("First StartComputationWithHandle() error = %v", err)
	}

	// Second computation should fail
	_, err = checkpointer.StartComputationWithHandle(ctx, "comp-2", []string{"rule2"})
	if err != ErrComputationInProgress {
		t.Errorf("Second StartComputationWithHandle() error = %v, want %v", err, ErrComputationInProgress)
	}
}

func TestTCCheckpointer_StartComputationWithHandle_Closed(t *testing.T) {
	checkpointer, _ := NewTCCheckpointer("/tmp/closed.wal")

	ctx := context.Background()
	_, err := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	if err != ErrCheckpointerClosed {
		t.Errorf("StartComputationWithHandle() on closed checkpointer error = %v, want %v", err, ErrCheckpointerClosed)
	}
}

func TestComputationHandle_UpdateProgress(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "progress.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	// Set very short checkpoint interval to trigger checkpoint
	checkpointer.SetCheckpointInterval(0)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	handle, _ := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	err := handle.UpdateProgress("phase1", 0.5)
	if err != nil {
		t.Errorf("UpdateProgress() error = %v", err)
	}

	if handle.State.Phase != "phase1" {
		t.Errorf("State.Phase = %v, want %v", handle.State.Phase, "phase1")
	}
	if handle.State.ProgressValue != 0.5 {
		t.Errorf("State.ProgressValue = %v, want %v", handle.State.ProgressValue, 0.5)
	}
}

func TestComputationHandle_Complete(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "complete.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	handle, _ := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	err := handle.Complete()
	if err != nil {
		t.Errorf("Complete() error = %v", err)
	}

	// Computation should be marked complete
	comp := checkpointer.GetCurrentComputation()
	if comp != nil && !comp.Completed {
		t.Error("GetCurrentComputation().Completed should be true")
	}
}

func TestComputationHandle_Abort(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "abort.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	handle, _ := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	err := handle.Abort("test reason")
	if err != nil {
		t.Errorf("Abort() error = %v", err)
	}

	// Active computation should be cleared
	activeComp := checkpointer.GetActiveComputation()
	if activeComp != nil {
		t.Error("GetActiveComputation() should be nil after abort")
	}
}

func TestTCCheckpointer_ResumeComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "resume.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Create a state as if recovered
	state := &ComputationState{
		ComputationID: "recovered-comp",
		RuleIDs:       []string{"rule1", "rule2"},
		Phase:         "phase2",
		ProgressValue: 0.75,
		PendingEntries: []*CheckpointEntry{
			{Type: EntryTypeDelta, DeltaEdges: []EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}}},
		},
	}

	ctx := context.Background()
	handle, err := checkpointer.ResumeComputation(ctx, state)

	if err != nil {
		t.Fatalf("ResumeComputation() error = %v", err)
	}
	if handle == nil {
		t.Fatal("ResumeComputation() should return handle")
	}

	if handle.ID != "recovered-comp" {
		t.Errorf("Handle.ID = %v, want %v", handle.ID, "recovered-comp")
	}
	if handle.State.Phase != "phase2" {
		t.Errorf("State.Phase = %v, want %v", handle.State.Phase, "phase2")
	}
	if handle.State.ProgressValue != 0.75 {
		t.Errorf("State.ProgressValue = %v, want %v", handle.State.ProgressValue, 0.75)
	}
	if len(handle.State.PendingEntries) != 1 {
		t.Errorf("State.PendingEntries length = %d, want 1", len(handle.State.PendingEntries))
	}
}

func TestTCCheckpointer_ResumeComputation_AlreadyActive(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "resume_active.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	_, _ = checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	// Try to resume another computation
	state := &ComputationState{ComputationID: "comp-2"}
	_, err := checkpointer.ResumeComputation(ctx, state)

	if err != ErrComputationInProgress {
		t.Errorf("ResumeComputation() with active computation error = %v, want %v", err, ErrComputationInProgress)
	}
}

func TestTCCheckpointer_AbortComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "abort_comp.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(1)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	checkpointer.StartComputation("comp-1", []string{"rule1"})
	checkpointer.LogDelta([]EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}})

	err := checkpointer.AbortComputation("comp-1", "user requested")
	if err != nil {
		t.Errorf("AbortComputation() error = %v", err)
	}

	// Verify abort was logged
	checkpointer.Close()

	// Read WAL and verify abort entry exists
	checkpointer2, _ := NewTCCheckpointer(logPath)
	state, _ := checkpointer2.RecoverFromFile(logPath)

	// Should be nothing to recover since computation was aborted
	if state != nil {
		t.Errorf("RecoverFromFile() after abort should return nil, got %v", state)
	}
}

func TestTCCheckpointer_ClearActiveComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "clear.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ctx := context.Background()
	_, _ = checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1"})

	if checkpointer.GetActiveComputation() == nil {
		t.Fatal("GetActiveComputation() should not be nil")
	}

	checkpointer.ClearActiveComputation()

	if checkpointer.GetActiveComputation() != nil {
		t.Error("GetActiveComputation() should be nil after ClearActiveComputation()")
	}
}

func TestEntryType_ComputationAbort(t *testing.T) {
	// Verify EntryTypeComputationAbort is valid
	if !EntryTypeComputationAbort.IsValid() {
		t.Error("EntryTypeComputationAbort should be valid")
	}

	if EntryTypeComputationAbort.String() != "ComputationAbort" {
		t.Errorf("EntryTypeComputationAbort.String() = %v, want ComputationAbort", EntryTypeComputationAbort.String())
	}
}

func TestCheckpointEntry_Validate_ComputationAbort(t *testing.T) {
	tests := []struct {
		name    string
		entry   *CheckpointEntry
		wantErr error
	}{
		{
			name: "valid abort",
			entry: &CheckpointEntry{
				Type:          EntryTypeComputationAbort,
				ComputationID: "comp-1",
				Metadata:      map[string]string{"reason": "test"},
			},
			wantErr: nil,
		},
		{
			name: "missing computation ID",
			entry: &CheckpointEntry{
				Type:     EntryTypeComputationAbort,
				Metadata: map[string]string{"reason": "test"},
			},
			wantErr: ErrMissingComputationID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckpointEntry_Clone_WithMetadata(t *testing.T) {
	original := &CheckpointEntry{
		Type:          EntryTypeComputationAbort,
		ComputationID: "comp-1",
		Phase:         "phase1",
		Progress:      0.5,
		Metadata:      map[string]string{"reason": "test", "key": "value"},
	}

	clone := original.Clone()

	// Verify new fields are copied
	if clone.Phase != original.Phase {
		t.Errorf("Clone().Phase = %v, want %v", clone.Phase, original.Phase)
	}
	if clone.Progress != original.Progress {
		t.Errorf("Clone().Progress = %v, want %v", clone.Progress, original.Progress)
	}
	if len(clone.Metadata) != len(original.Metadata) {
		t.Errorf("Clone().Metadata length = %d, want %d", len(clone.Metadata), len(original.Metadata))
	}

	// Verify metadata is a deep copy
	original.Metadata["reason"] = "modified"
	if clone.Metadata["reason"] == "modified" {
		t.Error("Clone should deep copy Metadata map")
	}
}

func TestComputationState_Clone_WithPendingEntries(t *testing.T) {
	original := &ComputationState{
		ComputationID: "comp-1",
		Phase:         "phase1",
		ProgressValue: 0.5,
		PendingEntries: []*CheckpointEntry{
			{Type: EntryTypeDelta, DeltaEdges: []EdgeKey{{Subject: "s", Predicate: "p", Object: "o"}}},
			{Type: EntryTypeStratum, Stratum: 1},
		},
	}

	clone := original.Clone()

	// Verify new fields are copied
	if clone.Phase != original.Phase {
		t.Errorf("Clone().Phase = %v, want %v", clone.Phase, original.Phase)
	}
	if clone.ProgressValue != original.ProgressValue {
		t.Errorf("Clone().ProgressValue = %v, want %v", clone.ProgressValue, original.ProgressValue)
	}
	if len(clone.PendingEntries) != len(original.PendingEntries) {
		t.Errorf("Clone().PendingEntries length = %d, want %d", len(clone.PendingEntries), len(original.PendingEntries))
	}

	// Verify pending entries is a deep copy
	original.PendingEntries[0].Stratum = 999
	if clone.PendingEntries[0].Stratum == 999 {
		t.Error("Clone should deep copy PendingEntries slice")
	}
}

// =============================================================================
// TCP.9 - Enhanced Close Tests
// =============================================================================

func TestTCCheckpointer_Close_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	// First close
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("First Close() failed: %v", err)
	}

	// Second close should be idempotent (no error)
	if err := checkpointer.Close(); err != nil {
		t.Errorf("Second Close() should be idempotent, got error: %v", err)
	}
}

func TestTCCheckpointer_Close_AbortsActiveComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100) // Ensure entries are buffered
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	// Start a computation
	ctx := context.Background()
	handle, err := checkpointer.StartComputationWithHandle(ctx, "comp-1", []string{"rule1", "rule2"})
	if err != nil {
		t.Fatalf("StartComputationWithHandle() failed: %v", err)
	}

	// Verify computation is active
	if checkpointer.GetActiveComputation() == nil {
		t.Error("Active computation should be set")
	}

	// Close should abort the computation
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Verify the computation was aborted by reading the WAL
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var foundStart, foundAbort bool
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeComputationStart && entry.ComputationID == "comp-1" {
			foundStart = true
		}
		if entry.Type == EntryTypeComputationAbort && entry.ComputationID == "comp-1" {
			foundAbort = true
			if entry.Metadata == nil || entry.Metadata["reason"] != "checkpointer closed" {
				t.Error("Abort entry should have reason 'checkpointer closed'")
			}
		}
	}

	if !foundStart {
		t.Error("WAL should contain computation start entry")
	}
	if !foundAbort {
		t.Error("WAL should contain computation abort entry")
	}

	// Verify handle is no longer associated with checkpointer
	_ = handle // We don't need to use the handle after close
}

func TestTCCheckpointer_Close_FlushesBuffer(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetBufferSize(100) // Large buffer to ensure entries are buffered
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	// Write entries that should be buffered
	for i := 0; i < 5; i++ {
		if err := checkpointer.LogEdgeAdd("s", "p", "o"); err != nil {
			t.Fatalf("LogEdgeAdd() failed: %v", err)
		}
	}

	// Verify entries are buffered
	if len(checkpointer.buffer) != 5 {
		t.Errorf("Buffer should have 5 entries, got %d", len(checkpointer.buffer))
	}

	// Close should flush the buffer
	if err := checkpointer.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Verify WAL has all entries
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	count := 0
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeEdgeAdd {
			count++
		}
	}

	if count != 5 {
		t.Errorf("WAL should have 5 EdgeAdd entries, got %d", count)
	}
}

func TestTCCheckpointer_Flush_UpdatesLastFlush(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Add an entry to the buffer
	if err := checkpointer.LogEdgeAdd("s", "p", "o"); err != nil {
		t.Fatalf("LogEdgeAdd() failed: %v", err)
	}

	beforeFlush := checkpointer.lastFlush

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Flush manually
	if err := checkpointer.Flush(); err != nil {
		t.Fatalf("Flush() failed: %v", err)
	}

	// Verify lastFlush was updated
	if !checkpointer.lastFlush.After(beforeFlush) {
		t.Error("lastFlush should be updated after Flush()")
	}
}

// =============================================================================
// TCP.10 - CheckpointedEvaluator Tests
// =============================================================================

func TestNewCheckpointedEvaluator(t *testing.T) {
	// Create a simple rule
	rule := NewInferenceRule(
		"rule1",
		"test-rule",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?z"},
		},
	)

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	if err != nil {
		t.Fatalf("NewSemiNaiveEvaluator() failed: %v", err)
	}

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	if ce == nil {
		t.Fatal("NewCheckpointedEvaluator returned nil")
	}
	if ce.GetEvaluator() != evaluator {
		t.Error("GetEvaluator() should return the same evaluator")
	}
	if ce.GetCheckpointer() != checkpointer {
		t.Error("GetCheckpointer() should return the same checkpointer")
	}
}

func TestCheckpointedEvaluator_getRuleIDs(t *testing.T) {
	rules := []*InferenceRule{
		NewInferenceRule("rule1", "r1", RuleCondition{Subject: "?x", Predicate: "p", Object: "?y"}, []RuleCondition{{Subject: "?x", Predicate: "q", Object: "?y"}}),
		NewInferenceRule("rule2", "r2", RuleCondition{Subject: "?a", Predicate: "p", Object: "?b"}, []RuleCondition{{Subject: "?a", Predicate: "r", Object: "?b"}}),
	}

	evaluator, _ := NewSemiNaiveEvaluator(rules)
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")
	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	ruleIDs := ce.getRuleIDs()

	if len(ruleIDs) != 2 {
		t.Errorf("getRuleIDs() length = %d, want 2", len(ruleIDs))
	}
	if ruleIDs[0] != "rule1" || ruleIDs[1] != "rule2" {
		t.Errorf("getRuleIDs() = %v, want [rule1, rule2]", ruleIDs)
	}
}

func TestCheckpointedEvaluator_Evaluate_Basic(t *testing.T) {
	// Create a transitive closure rule
	rule := NewInferenceRule(
		"tc-rule",
		"transitive-closure",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		},
	)

	evaluator, err := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	if err != nil {
		t.Fatalf("NewSemiNaiveEvaluator() failed: %v", err)
	}

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	// Create database with edges A->B->C
	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})

	// Evaluate
	ctx := context.Background()
	derived, err := ce.Evaluate(ctx, db)
	if err != nil {
		t.Fatalf("Evaluate() failed: %v", err)
	}

	// Should derive reachable(A, C)
	if derived != 1 {
		t.Errorf("Evaluate() derived = %d, want 1", derived)
	}

	// Verify the derived edge exists
	if !db.ContainsEdge(EdgeKey{Subject: "A", Predicate: "reachable", Object: "C"}) {
		t.Error("Database should contain derived edge (A)-[reachable]->(C)")
	}

	// Verify WAL contains computation entries
	checkpointer.Close()
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var foundStart, foundEnd bool
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeComputationStart {
			foundStart = true
		}
		if entry.Type == EntryTypeComputationEnd {
			foundEnd = true
		}
	}

	if !foundStart {
		t.Error("WAL should contain computation start entry")
	}
	if !foundEnd {
		t.Error("WAL should contain computation end entry")
	}
}

func TestCheckpointedEvaluator_Evaluate_ContextCancellation(t *testing.T) {
	rule := NewInferenceRule(
		"rule1",
		"test-rule",
		RuleCondition{Subject: "?x", Predicate: "p", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "q", Object: "?y"}},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	db := NewInMemoryDatabase()

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := ce.Evaluate(ctx, db)
	if err != context.Canceled {
		t.Errorf("Evaluate() with cancelled context error = %v, want context.Canceled", err)
	}

	// Verify computation was aborted in WAL
	checkpointer.Flush()
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var foundAbort bool
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeComputationAbort {
			foundAbort = true
		}
	}

	if !foundAbort {
		t.Error("WAL should contain computation abort entry for cancelled context")
	}
}

func TestCheckpointedEvaluator_Evaluate_ProgressTracking(t *testing.T) {
	// Create rules with multiple strata
	rule1 := NewInferenceRule(
		"rule1",
		"base-rule",
		RuleCondition{Subject: "?x", Predicate: "derived1", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
	)
	rule2 := NewInferenceRule(
		"rule2",
		"derived-rule",
		RuleCondition{Subject: "?x", Predicate: "derived2", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "derived1", Object: "?y"}},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule1, rule2})

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	checkpointer.SetCheckpointInterval(0) // Force checkpoints on every progress update
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	ctx := context.Background()
	_, err := ce.Evaluate(ctx, db)
	if err != nil {
		t.Fatalf("Evaluate() failed: %v", err)
	}

	// Verify stratum entries were logged
	checkpointer.Close()
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	stratumCount := 0
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeStratum {
			stratumCount++
		}
	}

	if stratumCount == 0 {
		t.Error("WAL should contain stratum entries for progress tracking")
	}
}

func TestCheckpointedEvaluator_EvaluateWithEdgeLogging(t *testing.T) {
	rule := NewInferenceRule(
		"tc-rule",
		"transitive-closure",
		RuleCondition{Subject: "?x", Predicate: "reachable", Object: "?z"},
		[]RuleCondition{
			{Subject: "?x", Predicate: "edge", Object: "?y"},
			{Subject: "?y", Predicate: "edge", Object: "?z"},
		},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "edge", Object: "B"})
	db.AddEdge(EdgeKey{Subject: "B", Predicate: "edge", Object: "C"})

	ctx := context.Background()
	derived, err := ce.EvaluateWithEdgeLogging(ctx, db)
	if err != nil {
		t.Fatalf("EvaluateWithEdgeLogging() failed: %v", err)
	}

	if derived != 1 {
		t.Errorf("EvaluateWithEdgeLogging() derived = %d, want 1", derived)
	}

	// Verify edge add entries were logged
	checkpointer.Close()
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	edgeAddCount := 0
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeEdgeAdd && entry.Predicate == "reachable" {
			edgeAddCount++
		}
	}

	if edgeAddCount != 1 {
		t.Errorf("WAL should have 1 EdgeAdd entry for reachable, got %d", edgeAddCount)
	}
}

func TestCheckpointedEvaluator_ResumeFromCheckpoint(t *testing.T) {
	// Create a rule
	rule := NewInferenceRule(
		"rule1",
		"test-rule",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	// Create a state simulating recovery
	state := &ComputationState{
		ComputationID:  "recovered-comp",
		RuleIDs:        []string{"rule1"},
		CurrentStratum: 0,
		Phase:          "stratum_0",
		ProgressValue:  0.5,
		PendingEntries: []*CheckpointEntry{
			{
				Type:      EntryTypeEdgeAdd,
				Subject:   "X",
				Predicate: "base",
				Object:    "Y",
			},
		},
	}

	db := NewInMemoryDatabase()

	ctx := context.Background()
	_, err := ce.ResumeFromCheckpoint(ctx, db, state)
	if err != nil {
		t.Fatalf("ResumeFromCheckpoint() failed: %v", err)
	}

	// Verify pending entry was replayed
	if !db.ContainsEdge(EdgeKey{Subject: "X", Predicate: "base", Object: "Y"}) {
		t.Error("Pending entry should be replayed to database")
	}

	checkpointer.Close()
}

func TestCheckpointedEvaluator_ResumeFromCheckpoint_NilState(t *testing.T) {
	rule := NewInferenceRule(
		"rule1",
		"test-rule",
		RuleCondition{Subject: "?x", Predicate: "p", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "q", Object: "?y"}},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})
	checkpointer, _ := NewTCCheckpointer("/tmp/test.wal")
	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	db := NewInMemoryDatabase()
	ctx := context.Background()

	_, err := ce.ResumeFromCheckpoint(ctx, db, nil)
	if err == nil {
		t.Error("ResumeFromCheckpoint with nil state should fail")
	}
}

func TestCheckpointedEvaluator_RecoverAndEvaluate_NoWAL(t *testing.T) {
	rule := NewInferenceRule(
		"rule1",
		"test-rule",
		RuleCondition{Subject: "?x", Predicate: "derived", Object: "?y"},
		[]RuleCondition{{Subject: "?x", Predicate: "base", Object: "?y"}},
	)

	evaluator, _ := NewSemiNaiveEvaluator([]*InferenceRule{rule})

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")
	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	ce := NewCheckpointedEvaluator(evaluator, checkpointer)

	db := NewInMemoryDatabase()
	db.AddEdge(EdgeKey{Subject: "A", Predicate: "base", Object: "B"})

	ctx := context.Background()
	nonExistentWAL := filepath.Join(tmpDir, "nonexistent.wal")
	derived, recovered, err := ce.RecoverAndEvaluate(ctx, db, nonExistentWAL)
	if err != nil {
		t.Fatalf("RecoverAndEvaluate() failed: %v", err)
	}

	if recovered {
		t.Error("Should not report recovery when WAL doesn't exist")
	}

	if derived != 1 {
		t.Errorf("RecoverAndEvaluate() derived = %d, want 1", derived)
	}
}

func TestTCCheckpointer_LogStratumWithComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	// Start a computation
	checkpointer.currentComp = NewComputationState("comp-1", []string{"rule1"})

	err := checkpointer.LogStratumWithComputation("comp-1", 2, map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("LogStratumWithComputation() failed: %v", err)
	}

	// Verify computation state was updated
	if checkpointer.currentComp.CurrentStratum != 1 {
		t.Errorf("CurrentStratum should be advanced to 1, got %d", checkpointer.currentComp.CurrentStratum)
	}

	// Flush and verify WAL
	checkpointer.Flush()
	checkpointer.Close()

	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var found bool
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeStratum && entry.Stratum == 2 && entry.ComputationID == "comp-1" {
			found = true
			if entry.Metadata == nil || entry.Metadata["key"] != "value" {
				t.Error("Stratum entry should have metadata")
			}
		}
	}

	if !found {
		t.Error("WAL should contain stratum entry")
	}
}

func TestTCCheckpointer_LogEdgeAddWithComputation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.wal")

	checkpointer, _ := NewTCCheckpointer(logPath)
	if err := checkpointer.Open(); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer checkpointer.Close()

	edge := EdgeKey{Subject: "A", Predicate: "rel", Object: "B"}
	err := checkpointer.LogEdgeAddWithComputation("comp-1", edge)
	if err != nil {
		t.Fatalf("LogEdgeAddWithComputation() failed: %v", err)
	}

	// Flush and verify WAL
	checkpointer.Flush()
	checkpointer.Close()

	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var found bool
	for {
		var entry CheckpointEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if entry.Type == EntryTypeEdgeAdd &&
			entry.Subject == "A" &&
			entry.Predicate == "rel" &&
			entry.Object == "B" &&
			entry.ComputationID == "comp-1" {
			found = true
		}
	}

	if !found {
		t.Error("WAL should contain edge add entry with computation ID")
	}
}

func TestGenerateComputationID(t *testing.T) {
	id1 := generateComputationID()
	time.Sleep(1 * time.Millisecond)
	id2 := generateComputationID()

	if id1 == id2 {
		t.Error("generateComputationID should produce unique IDs")
	}

	if len(id1) < 10 {
		t.Error("generateComputationID should produce sufficiently long IDs")
	}
}
