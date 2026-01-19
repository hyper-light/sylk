package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// HO.9.1 WAL Entry Types Tests
// =============================================================================

func TestWALEntryTypes(t *testing.T) {
	t.Run("EntryTypeConstants", func(t *testing.T) {
		if EntryTypeObservation != "observation" {
			t.Errorf("Expected EntryTypeObservation to be 'observation', got %s", EntryTypeObservation)
		}
		if EntryTypeCheckpoint != "checkpoint" {
			t.Errorf("Expected EntryTypeCheckpoint to be 'checkpoint', got %s", EntryTypeCheckpoint)
		}
		if EntryTypePriorUpdate != "prior_update" {
			t.Errorf("Expected EntryTypePriorUpdate to be 'prior_update', got %s", EntryTypePriorUpdate)
		}
		if EntryTypeHandoffOutcome != "handoff_outcome" {
			t.Errorf("Expected EntryTypeHandoffOutcome to be 'handoff_outcome', got %s", EntryTypeHandoffOutcome)
		}
	})
}

func TestProfileSnapshot(t *testing.T) {
	t.Run("CreateFromProfile", func(t *testing.T) {
		profile := NewAgentHandoffProfile("coder", "gpt-4", "instance-123")
		profile.EffectiveSamples = 15.5

		snap := NewProfileSnapshot(profile)

		if snap == nil {
			t.Fatal("Expected non-nil snapshot")
		}
		if snap.AgentType != "coder" {
			t.Errorf("Expected AgentType 'coder', got %s", snap.AgentType)
		}
		if snap.ModelID != "gpt-4" {
			t.Errorf("Expected ModelID 'gpt-4', got %s", snap.ModelID)
		}
		if snap.InstanceID != "instance-123" {
			t.Errorf("Expected InstanceID 'instance-123', got %s", snap.InstanceID)
		}
		if snap.EffectiveSamples != 15.5 {
			t.Errorf("Expected EffectiveSamples 15.5, got %f", snap.EffectiveSamples)
		}
	})

	t.Run("CreateFromNilProfile", func(t *testing.T) {
		snap := NewProfileSnapshot(nil)
		if snap != nil {
			t.Error("Expected nil snapshot from nil profile")
		}
	})

	t.Run("ToProfileConversion", func(t *testing.T) {
		profile := NewAgentHandoffProfile("reviewer", "claude-3", "inst-456")
		profile.EffectiveSamples = 20.0

		snap := NewProfileSnapshot(profile)
		restored := snap.ToProfile()

		if restored == nil {
			t.Fatal("Expected non-nil restored profile")
		}
		if restored.AgentType != profile.AgentType {
			t.Errorf("AgentType mismatch: expected %s, got %s", profile.AgentType, restored.AgentType)
		}
		if restored.ModelID != profile.ModelID {
			t.Errorf("ModelID mismatch: expected %s, got %s", profile.ModelID, restored.ModelID)
		}
		if restored.EffectiveSamples != profile.EffectiveSamples {
			t.Errorf("EffectiveSamples mismatch: expected %f, got %f", profile.EffectiveSamples, restored.EffectiveSamples)
		}
	})

	t.Run("ToProfileFromNilSnapshot", func(t *testing.T) {
		var snap *ProfileSnapshot
		restored := snap.ToProfile()
		if restored != nil {
			t.Error("Expected nil profile from nil snapshot")
		}
	})

	t.Run("LearnedParametersPreserved", func(t *testing.T) {
		profile := NewAgentHandoffProfile("agent", "model", "inst")

		// Modify learned parameters
		if profile.OptimalHandoffThreshold != nil {
			profile.OptimalHandoffThreshold.Alpha = 10.0
			profile.OptimalHandoffThreshold.Beta = 5.0
		}

		snap := NewProfileSnapshot(profile)
		restored := snap.ToProfile()

		if restored.OptimalHandoffThreshold == nil {
			t.Fatal("Expected OptimalHandoffThreshold to be preserved")
		}
		if restored.OptimalHandoffThreshold.Alpha != 10.0 {
			t.Errorf("Expected Alpha 10.0, got %f", restored.OptimalHandoffThreshold.Alpha)
		}
		if restored.OptimalHandoffThreshold.Beta != 5.0 {
			t.Errorf("Expected Beta 5.0, got %f", restored.OptimalHandoffThreshold.Beta)
		}
	})
}

func TestGPSnapshot(t *testing.T) {
	t.Run("CreateFromGP", func(t *testing.T) {
		gp := NewAgentGaussianProcess(nil)
		gp.AddObservationFromValues(1000, 100, 5, 0.8)
		gp.AddObservationFromValues(2000, 150, 10, 0.7)

		snap := NewGPSnapshot(gp)

		if snap == nil {
			t.Fatal("Expected non-nil snapshot")
		}
		if len(snap.Observations) != 2 {
			t.Errorf("Expected 2 observations, got %d", len(snap.Observations))
		}
		if snap.Hyperparams == nil {
			t.Error("Expected non-nil hyperparams")
		}
	})

	t.Run("CreateFromNilGP", func(t *testing.T) {
		snap := NewGPSnapshot(nil)
		if snap != nil {
			t.Error("Expected nil snapshot from nil GP")
		}
	})

	t.Run("ToGPConversion", func(t *testing.T) {
		gp := NewAgentGaussianProcess(nil)
		gp.AddObservationFromValues(500, 50, 2, 0.9)

		snap := NewGPSnapshot(gp)
		restored := snap.ToGP()

		if restored == nil {
			t.Fatal("Expected non-nil restored GP")
		}
		if restored.NumObservations() != 1 {
			t.Errorf("Expected 1 observation, got %d", restored.NumObservations())
		}
	})

	t.Run("ToGPFromNilSnapshot", func(t *testing.T) {
		var snap *GPSnapshot
		restored := snap.ToGP()
		if restored == nil {
			t.Error("Expected default GP from nil snapshot")
		}
	})
}

func TestHandoffWALEntry(t *testing.T) {
	t.Run("NewObservationEntry", func(t *testing.T) {
		obs := NewHandoffObservation(1000, 5, 0.8, true, false)
		gpObs := NewGPObservation(1000, 100, 3, 0.8)

		entry := NewObservationEntry(obs, gpObs, 42)

		if entry.EntryType != EntryTypeObservation {
			t.Errorf("Expected EntryTypeObservation, got %s", entry.EntryType)
		}
		if entry.SequenceNumber != 42 {
			t.Errorf("Expected sequence 42, got %d", entry.SequenceNumber)
		}
		if entry.Observation == nil {
			t.Error("Expected non-nil observation")
		}
		if entry.GPObservation == nil {
			t.Error("Expected non-nil GP observation")
		}
	})

	t.Run("NewCheckpointEntry", func(t *testing.T) {
		profile := NewAgentHandoffProfile("coder", "gpt-4", "inst")
		gp := NewAgentGaussianProcess(nil)

		entry := NewCheckpointEntry(profile, gp, 100)

		if entry.EntryType != EntryTypeCheckpoint {
			t.Errorf("Expected EntryTypeCheckpoint, got %s", entry.EntryType)
		}
		if entry.ProfileSnapshot == nil {
			t.Error("Expected non-nil profile snapshot")
		}
		if entry.GPSnapshot == nil {
			t.Error("Expected non-nil GP snapshot")
		}
	})

	t.Run("NewPriorUpdateEntry", func(t *testing.T) {
		profile := NewAgentHandoffProfile("agent", "model", "inst")

		entry := NewPriorUpdateEntry(profile, 50)

		if entry.EntryType != EntryTypePriorUpdate {
			t.Errorf("Expected EntryTypePriorUpdate, got %s", entry.EntryType)
		}
		if entry.ProfileSnapshot == nil {
			t.Error("Expected non-nil profile snapshot")
		}
	})

	t.Run("NewHandoffOutcomeEntry", func(t *testing.T) {
		decision := NewHandoffDecision(true, 0.9, "test", TriggerQualityDegrading, DecisionFactors{})

		entry := NewHandoffOutcomeEntry(decision, true, 75)

		if entry.EntryType != EntryTypeHandoffOutcome {
			t.Errorf("Expected EntryTypeHandoffOutcome, got %s", entry.EntryType)
		}
		if entry.Decision == nil {
			t.Error("Expected non-nil decision")
		}
		if !entry.WasSuccessful {
			t.Error("Expected WasSuccessful to be true")
		}
	})

	t.Run("JSONSerialization", func(t *testing.T) {
		entry := NewObservationEntry(
			NewHandoffObservation(1000, 5, 0.8, true, false),
			NewGPObservation(1000, 100, 3, 0.8),
			1,
		)

		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("Failed to marshal entry: %v", err)
		}

		var restored HandoffWALEntry
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("Failed to unmarshal entry: %v", err)
		}

		if restored.EntryType != entry.EntryType {
			t.Errorf("EntryType mismatch: expected %s, got %s", entry.EntryType, restored.EntryType)
		}
		if restored.SequenceNumber != entry.SequenceNumber {
			t.Errorf("SequenceNumber mismatch: expected %d, got %d", entry.SequenceNumber, restored.SequenceNumber)
		}
	})
}

// =============================================================================
// HO.9.2 WAL Recovery Tests
// =============================================================================

func createTempWALPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.wal")
}

func TestHandoffWALBasics(t *testing.T) {
	t.Run("CreateNewWAL", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		if wal.FilePath() != path {
			t.Errorf("FilePath mismatch: expected %s, got %s", path, wal.FilePath())
		}
		if wal.GetSequence() != 0 {
			t.Errorf("Expected initial sequence 0, got %d", wal.GetSequence())
		}
	})

	t.Run("CreateWALWithConfig", func(t *testing.T) {
		path := createTempWALPath(t)
		config := &WALConfig{CheckpointInterval: 50}

		wal, err := NewHandoffWAL(path, config)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		if wal.checkpointInterval != 50 {
			t.Errorf("Expected checkpoint interval 50, got %d", wal.checkpointInterval)
		}
	})

	t.Run("AppendAndLoadEntries", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Append entries
		for i := 0; i < 5; i++ {
			entry := NewObservationEntry(
				NewHandoffObservation(1000+i*100, i, 0.8, true, false),
				nil,
				0,
			)
			if err := wal.AppendEntry(entry); err != nil {
				t.Fatalf("Failed to append entry %d: %v", i, err)
			}
		}

		// Load entries
		entries, err := wal.LoadEntries()
		if err != nil {
			t.Fatalf("Failed to load entries: %v", err)
		}

		if len(entries) != 5 {
			t.Errorf("Expected 5 entries, got %d", len(entries))
		}

		// Verify sequence numbers
		for i, entry := range entries {
			if entry.SequenceNumber != uint64(i+1) {
				t.Errorf("Entry %d: expected sequence %d, got %d", i, i+1, entry.SequenceNumber)
			}
		}

		wal.Close()
	})

	t.Run("SequenceNumberPersistence", func(t *testing.T) {
		path := createTempWALPath(t)

		// Create and write entries
		wal1, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		for i := 0; i < 3; i++ {
			entry := NewObservationEntry(nil, nil, 0)
			if err := wal1.AppendEntry(entry); err != nil {
				t.Fatalf("Failed to append entry: %v", err)
			}
		}
		wal1.Close()

		// Reopen and verify sequence continues
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal2.Close()

		if wal2.GetSequence() != 3 {
			t.Errorf("Expected sequence 3 after reopen, got %d", wal2.GetSequence())
		}

		// Append more and verify
		entry := NewObservationEntry(nil, nil, 0)
		if err := wal2.AppendEntry(entry); err != nil {
			t.Fatalf("Failed to append after reopen: %v", err)
		}

		if wal2.GetSequence() != 4 {
			t.Errorf("Expected sequence 4, got %d", wal2.GetSequence())
		}
	})

	t.Run("AppendNilEntry", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		err = wal.AppendEntry(nil)
		if err == nil {
			t.Error("Expected error when appending nil entry")
		}
	})

	t.Run("AppendToClosedWAL", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		wal.Close()

		entry := NewObservationEntry(nil, nil, 0)
		err = wal.AppendEntry(entry)
		if err == nil {
			t.Error("Expected error when appending to closed WAL")
		}
	})
}

func TestWALRecovery(t *testing.T) {
	t.Run("RecoverFromEmpty", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		state, err := wal.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		if state.Profile == nil {
			t.Error("Expected non-nil profile")
		}
		if state.GP == nil {
			t.Error("Expected non-nil GP")
		}
		if state.EntriesReplayed != 0 {
			t.Errorf("Expected 0 entries replayed, got %d", state.EntriesReplayed)
		}
	})

	t.Run("RecoverFromCheckpoint", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Create profile and GP with specific values
		profile := NewAgentHandoffProfile("coder", "gpt-4", "inst")
		profile.EffectiveSamples = 25.0
		gp := NewAgentGaussianProcess(nil)
		gp.AddObservationFromValues(1000, 100, 5, 0.85)

		// Write checkpoint
		if err := wal.WriteCheckpoint(profile, gp); err != nil {
			t.Fatalf("Failed to write checkpoint: %v", err)
		}

		wal.Close()

		// Reopen and recover
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal2.Close()

		state, err := wal2.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		if state.Profile.AgentType != "coder" {
			t.Errorf("Expected AgentType 'coder', got %s", state.Profile.AgentType)
		}
		if state.Profile.EffectiveSamples != 25.0 {
			t.Errorf("Expected EffectiveSamples 25.0, got %f", state.Profile.EffectiveSamples)
		}
		if state.GP.NumObservations() != 1 {
			t.Errorf("Expected 1 GP observation, got %d", state.GP.NumObservations())
		}
	})

	t.Run("RecoverCheckpointPlusObservations", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Write checkpoint
		profile := NewAgentHandoffProfile("agent", "model", "inst")
		gp := NewAgentGaussianProcess(nil)

		if err := wal.WriteCheckpoint(profile, gp); err != nil {
			t.Fatalf("Failed to write checkpoint: %v", err)
		}

		// Write observations after checkpoint
		for i := 0; i < 5; i++ {
			obs := NewHandoffObservation(1000+i*100, i+1, 0.8, true, false)
			gpObs := NewGPObservation(1000+i*100, 100, 5, 0.8)
			if err := wal.WriteObservation(obs, gpObs); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		wal.Close()

		// Reopen and recover
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal2.Close()

		state, err := wal2.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		// Should have replayed 5 observations
		if state.EntriesReplayed != 5 {
			t.Errorf("Expected 5 entries replayed, got %d", state.EntriesReplayed)
		}

		// GP should have observations
		if state.GP.NumObservations() != 5 {
			t.Errorf("Expected 5 GP observations, got %d", state.GP.NumObservations())
		}
	})

	t.Run("RecoverOnlyObservations", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Write only observations (no checkpoint)
		for i := 0; i < 3; i++ {
			obs := NewHandoffObservation(1000, i, 0.75, true, false)
			gpObs := NewGPObservation(1000, 50, 2, 0.75)
			if err := wal.WriteObservation(obs, gpObs); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		wal.Close()

		// Reopen and recover
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal2.Close()

		state, err := wal2.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		// Should start with default profile and replay all observations
		if state.EntriesReplayed != 3 {
			t.Errorf("Expected 3 entries replayed, got %d", state.EntriesReplayed)
		}
		if state.GP.NumObservations() != 3 {
			t.Errorf("Expected 3 GP observations, got %d", state.GP.NumObservations())
		}
	})

	t.Run("RecoverMultipleCheckpoints", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Write first checkpoint
		profile1 := NewAgentHandoffProfile("agent1", "model1", "inst")
		gp1 := NewAgentGaussianProcess(nil)
		if err := wal.WriteCheckpoint(profile1, gp1); err != nil {
			t.Fatalf("Failed to write first checkpoint: %v", err)
		}

		// Some observations
		for i := 0; i < 3; i++ {
			if err := wal.WriteObservation(nil, NewGPObservation(1000, 100, 5, 0.8)); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		// Write second checkpoint with different values
		profile2 := NewAgentHandoffProfile("agent2", "model2", "inst")
		gp2 := NewAgentGaussianProcess(nil)
		gp2.AddObservationFromValues(2000, 200, 10, 0.9)
		if err := wal.WriteCheckpoint(profile2, gp2); err != nil {
			t.Fatalf("Failed to write second checkpoint: %v", err)
		}

		// More observations after second checkpoint
		for i := 0; i < 2; i++ {
			if err := wal.WriteObservation(nil, NewGPObservation(3000, 300, 15, 0.95)); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		wal.Close()

		// Reopen and recover
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}
		defer wal2.Close()

		state, err := wal2.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		// Should use the second checkpoint
		if state.Profile.AgentType != "agent2" {
			t.Errorf("Expected AgentType 'agent2', got %s", state.Profile.AgentType)
		}

		// Should have 1 from checkpoint + 2 replayed = 3
		if state.GP.NumObservations() != 3 {
			t.Errorf("Expected 3 GP observations, got %d", state.GP.NumObservations())
		}

		// Should only replay entries after last checkpoint
		if state.EntriesReplayed != 2 {
			t.Errorf("Expected 2 entries replayed, got %d", state.EntriesReplayed)
		}
	})
}

func TestWALCorruptEntryHandling(t *testing.T) {
	t.Run("SkipCorruptedEntries", func(t *testing.T) {
		path := createTempWALPath(t)

		// Write some valid entries
		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		for i := 0; i < 3; i++ {
			if err := wal.WriteObservation(nil, NewGPObservation(1000, 100, 5, 0.8)); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}
		wal.Close()

		// Manually append corrupted entry to file
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatalf("Failed to open file for corruption: %v", err)
		}
		f.WriteString("{invalid json\n")
		f.Close()

		// Append more valid entries
		wal2, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to reopen WAL: %v", err)
		}

		for i := 0; i < 2; i++ {
			if err := wal2.WriteObservation(nil, NewGPObservation(2000, 200, 10, 0.9)); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		// Load entries should skip corrupted
		entries, err := wal2.LoadEntries()
		if err != nil {
			t.Fatalf("Failed to load entries: %v", err)
		}

		// Should have 3 + 2 = 5 valid entries (corrupted skipped)
		if len(entries) != 5 {
			t.Errorf("Expected 5 valid entries, got %d", len(entries))
		}

		// Recovery should work
		state, err := wal2.Recover()
		if err != nil {
			t.Fatalf("Failed to recover: %v", err)
		}

		if state.GP.NumObservations() != 5 {
			t.Errorf("Expected 5 GP observations after recovery, got %d", state.GP.NumObservations())
		}

		wal2.Close()
	})
}

func TestWALTruncate(t *testing.T) {
	t.Run("TruncateRemovesAllEntries", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		// Write entries
		for i := 0; i < 10; i++ {
			if err := wal.WriteObservation(nil, nil); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		// Truncate
		if err := wal.Truncate(); err != nil {
			t.Fatalf("Failed to truncate: %v", err)
		}

		// Load should return empty
		entries, err := wal.LoadEntries()
		if err != nil {
			t.Fatalf("Failed to load entries: %v", err)
		}

		if len(entries) != 0 {
			t.Errorf("Expected 0 entries after truncate, got %d", len(entries))
		}
	})

	t.Run("TruncateResetsCheckpointTracking", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		// Write checkpoint and observations
		if err := wal.WriteCheckpoint(NewAgentHandoffProfile("a", "m", "i"), nil); err != nil {
			t.Fatalf("Failed to write checkpoint: %v", err)
		}
		for i := 0; i < 5; i++ {
			if err := wal.WriteObservation(nil, nil); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		if wal.GetEntriesSinceCheckpoint() != 5 {
			t.Errorf("Expected 5 entries since checkpoint, got %d", wal.GetEntriesSinceCheckpoint())
		}

		// Truncate
		if err := wal.Truncate(); err != nil {
			t.Fatalf("Failed to truncate: %v", err)
		}

		if wal.GetEntriesSinceCheckpoint() != 0 {
			t.Errorf("Expected 0 entries since checkpoint after truncate, got %d", wal.GetEntriesSinceCheckpoint())
		}
	})
}

func TestWALCompaction(t *testing.T) {
	t.Run("CompactReducesFileSize", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Write many entries
		for i := 0; i < 100; i++ {
			if err := wal.WriteObservation(
				NewHandoffObservation(1000, i, 0.8, true, false),
				NewGPObservation(1000, 100, 5, 0.8),
			); err != nil {
				t.Fatalf("Failed to write observation: %v", err)
			}
		}

		// Get file size before compaction
		info1, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}
		sizeBefore := info1.Size()

		// Compact with current state
		profile := NewAgentHandoffProfile("agent", "model", "inst")
		gp := NewAgentGaussianProcess(nil)
		gp.AddObservationFromValues(1000, 100, 5, 0.8)

		if err := wal.Compact(profile, gp); err != nil {
			t.Fatalf("Failed to compact: %v", err)
		}

		// Get file size after compaction
		info2, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Failed to stat file after compaction: %v", err)
		}
		sizeAfter := info2.Size()

		if sizeAfter >= sizeBefore {
			t.Errorf("Expected smaller file after compaction: before=%d, after=%d", sizeBefore, sizeAfter)
		}

		// Verify we can still recover
		state, err := wal.Recover()
		if err != nil {
			t.Fatalf("Failed to recover after compaction: %v", err)
		}

		if state.Profile.AgentType != "agent" {
			t.Errorf("Expected AgentType 'agent', got %s", state.Profile.AgentType)
		}

		wal.Close()
	})
}

func TestWALConcurrentAccess(t *testing.T) {
	t.Run("ConcurrentWrites", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		var wg sync.WaitGroup
		numGoroutines := 10
		writesPerGoroutine := 20

		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				for i := 0; i < writesPerGoroutine; i++ {
					obs := NewHandoffObservation(goroutineID*1000+i, i, 0.8, true, false)
					if err := wal.WriteObservation(obs, nil); err != nil {
						t.Errorf("Goroutine %d write %d failed: %v", goroutineID, i, err)
					}
				}
			}(g)
		}

		wg.Wait()

		// Verify all entries were written
		entries, err := wal.LoadEntries()
		if err != nil {
			t.Fatalf("Failed to load entries: %v", err)
		}

		expectedEntries := numGoroutines * writesPerGoroutine
		if len(entries) != expectedEntries {
			t.Errorf("Expected %d entries, got %d", expectedEntries, len(entries))
		}

		// Verify sequence numbers are unique
		seen := make(map[uint64]bool)
		for _, entry := range entries {
			if seen[entry.SequenceNumber] {
				t.Errorf("Duplicate sequence number: %d", entry.SequenceNumber)
			}
			seen[entry.SequenceNumber] = true
		}
	})

	t.Run("ConcurrentReadsAndWrites", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		var wg sync.WaitGroup

		// Writers
		for g := 0; g < 5; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 10; i++ {
					wal.WriteObservation(nil, NewGPObservation(1000, 100, 5, 0.8))
					time.Sleep(time.Millisecond)
				}
			}()
		}

		// Readers
		for g := 0; g < 3; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					_, err := wal.LoadEntries()
					if err != nil {
						t.Errorf("Failed to load entries: %v", err)
					}
					time.Sleep(2 * time.Millisecond)
				}
			}()
		}

		wg.Wait()
	})
}

func TestWALCheckpointing(t *testing.T) {
	t.Run("NeedsCheckpoint", func(t *testing.T) {
		path := createTempWALPath(t)
		config := &WALConfig{CheckpointInterval: 5}

		wal, err := NewHandoffWAL(path, config)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		// Initially should not need checkpoint
		if wal.NeedsCheckpoint() {
			t.Error("Should not need checkpoint initially")
		}

		// Write entries
		for i := 0; i < 4; i++ {
			if err := wal.WriteObservation(nil, nil); err != nil {
				t.Fatalf("Failed to write: %v", err)
			}
		}

		// Still shouldn't need checkpoint (4 < 5)
		if wal.NeedsCheckpoint() {
			t.Error("Should not need checkpoint after 4 entries")
		}

		// Write one more
		if err := wal.WriteObservation(nil, nil); err != nil {
			t.Fatalf("Failed to write: %v", err)
		}

		// Now should need checkpoint (5 >= 5)
		if !wal.NeedsCheckpoint() {
			t.Error("Should need checkpoint after 5 entries")
		}
	})

	t.Run("MaybeCheckpoint", func(t *testing.T) {
		path := createTempWALPath(t)
		config := &WALConfig{CheckpointInterval: 3}

		wal, err := NewHandoffWAL(path, config)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		profile := NewAgentHandoffProfile("a", "m", "i")
		gp := NewAgentGaussianProcess(nil)

		// Should not checkpoint yet
		written, err := wal.MaybeCheckpoint(profile, gp)
		if err != nil {
			t.Fatalf("MaybeCheckpoint failed: %v", err)
		}
		if written {
			t.Error("Should not have written checkpoint")
		}

		// Write enough entries
		for i := 0; i < 3; i++ {
			if err := wal.WriteObservation(nil, nil); err != nil {
				t.Fatalf("Failed to write: %v", err)
			}
		}

		// Now should checkpoint
		written, err = wal.MaybeCheckpoint(profile, gp)
		if err != nil {
			t.Fatalf("MaybeCheckpoint failed: %v", err)
		}
		if !written {
			t.Error("Should have written checkpoint")
		}

		// Counter should be reset
		if wal.GetEntriesSinceCheckpoint() != 0 {
			t.Errorf("Expected 0 entries since checkpoint, got %d", wal.GetEntriesSinceCheckpoint())
		}
	})

	t.Run("CheckpointResetsCounter", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		// Write observations
		for i := 0; i < 10; i++ {
			if err := wal.WriteObservation(nil, nil); err != nil {
				t.Fatalf("Failed to write: %v", err)
			}
		}

		if wal.GetEntriesSinceCheckpoint() != 10 {
			t.Errorf("Expected 10 entries since checkpoint, got %d", wal.GetEntriesSinceCheckpoint())
		}

		// Write checkpoint
		if err := wal.WriteCheckpoint(nil, nil); err != nil {
			t.Fatalf("Failed to write checkpoint: %v", err)
		}

		if wal.GetEntriesSinceCheckpoint() != 0 {
			t.Errorf("Expected 0 entries since checkpoint, got %d", wal.GetEntriesSinceCheckpoint())
		}
	})
}

func TestWALConvenienceMethods(t *testing.T) {
	t.Run("WriteObservation", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		obs := NewHandoffObservation(1000, 5, 0.8, true, false)
		gpObs := NewGPObservation(1000, 100, 5, 0.8)

		if err := wal.WriteObservation(obs, gpObs); err != nil {
			t.Fatalf("WriteObservation failed: %v", err)
		}

		entries, _ := wal.LoadEntries()
		if len(entries) != 1 {
			t.Fatalf("Expected 1 entry, got %d", len(entries))
		}
		if entries[0].EntryType != EntryTypeObservation {
			t.Errorf("Expected observation entry type")
		}
	})

	t.Run("WriteCheckpoint", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		profile := NewAgentHandoffProfile("a", "m", "i")
		gp := NewAgentGaussianProcess(nil)

		if err := wal.WriteCheckpoint(profile, gp); err != nil {
			t.Fatalf("WriteCheckpoint failed: %v", err)
		}

		entries, _ := wal.LoadEntries()
		if len(entries) != 1 {
			t.Fatalf("Expected 1 entry, got %d", len(entries))
		}
		if entries[0].EntryType != EntryTypeCheckpoint {
			t.Errorf("Expected checkpoint entry type")
		}
	})

	t.Run("WritePriorUpdate", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		profile := NewAgentHandoffProfile("a", "m", "i")

		if err := wal.WritePriorUpdate(profile); err != nil {
			t.Fatalf("WritePriorUpdate failed: %v", err)
		}

		entries, _ := wal.LoadEntries()
		if len(entries) != 1 {
			t.Fatalf("Expected 1 entry, got %d", len(entries))
		}
		if entries[0].EntryType != EntryTypePriorUpdate {
			t.Errorf("Expected prior_update entry type")
		}
	})

	t.Run("WriteHandoffOutcome", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		decision := NewHandoffDecision(true, 0.9, "test", TriggerContextFull, DecisionFactors{})

		if err := wal.WriteHandoffOutcome(decision, true); err != nil {
			t.Fatalf("WriteHandoffOutcome failed: %v", err)
		}

		entries, _ := wal.LoadEntries()
		if len(entries) != 1 {
			t.Fatalf("Expected 1 entry, got %d", len(entries))
		}
		if entries[0].EntryType != EntryTypeHandoffOutcome {
			t.Errorf("Expected handoff_outcome entry type")
		}
	})
}

func TestWALEdgeCases(t *testing.T) {
	t.Run("EmptyWALFile", func(t *testing.T) {
		path := createTempWALPath(t)

		// Create empty file
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		f.Close()

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to open empty WAL: %v", err)
		}
		defer wal.Close()

		entries, err := wal.LoadEntries()
		if err != nil {
			t.Fatalf("Failed to load from empty WAL: %v", err)
		}

		if len(entries) != 0 {
			t.Errorf("Expected 0 entries from empty WAL, got %d", len(entries))
		}
	})

	t.Run("LargeEntry", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}
		defer wal.Close()

		// Create GP with many observations
		gp := NewAgentGaussianProcess(nil)
		gp.SetMaxObservations(500)
		for i := 0; i < 500; i++ {
			gp.AddObservationFromValues(i*100, i*10, i, float64(i)/500.0)
		}

		profile := NewAgentHandoffProfile("agent", "model", "instance")

		// Write large checkpoint
		if err := wal.WriteCheckpoint(profile, gp); err != nil {
			t.Fatalf("Failed to write large checkpoint: %v", err)
		}

		// Recover
		state, err := wal.Recover()
		if err != nil {
			t.Fatalf("Failed to recover large checkpoint: %v", err)
		}

		if state.GP.NumObservations() != 500 {
			t.Errorf("Expected 500 observations, got %d", state.GP.NumObservations())
		}
	})

	t.Run("CloseMultipleTimes", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		// Close multiple times should not panic
		err1 := wal.Close()
		err2 := wal.Close()
		err3 := wal.Close()

		if err1 != nil {
			t.Errorf("First close should succeed: %v", err1)
		}
		// Subsequent closes should be no-ops
		if err2 != nil {
			t.Errorf("Second close should be no-op: %v", err2)
		}
		if err3 != nil {
			t.Errorf("Third close should be no-op: %v", err3)
		}
	})

	t.Run("LoadFromClosedWAL", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		wal.Close()

		_, err = wal.LoadEntries()
		if err == nil {
			t.Error("Expected error loading from closed WAL")
		}
	})

	t.Run("RecoverFromClosedWAL", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		wal.Close()

		_, err = wal.Recover()
		if err == nil {
			t.Error("Expected error recovering from closed WAL")
		}
	})

	t.Run("TruncateClosedWAL", func(t *testing.T) {
		path := createTempWALPath(t)

		wal, err := NewHandoffWAL(path, nil)
		if err != nil {
			t.Fatalf("Failed to create WAL: %v", err)
		}

		wal.Close()

		err = wal.Truncate()
		if err == nil {
			t.Error("Expected error truncating closed WAL")
		}
	})
}

func TestDefaultWALConfig(t *testing.T) {
	config := DefaultWALConfig()

	if config.CheckpointInterval != 100 {
		t.Errorf("Expected default checkpoint interval 100, got %d", config.CheckpointInterval)
	}
}

func TestHandoffStateFields(t *testing.T) {
	state := &HandoffState{
		Profile:           NewAgentHandoffProfile("a", "m", "i"),
		GP:                NewAgentGaussianProcess(nil),
		LastSequence:      42,
		RecoveryTimestamp: time.Now(),
		EntriesReplayed:   10,
		CorruptedEntries:  2,
	}

	if state.LastSequence != 42 {
		t.Errorf("Expected LastSequence 42, got %d", state.LastSequence)
	}
	if state.EntriesReplayed != 10 {
		t.Errorf("Expected EntriesReplayed 10, got %d", state.EntriesReplayed)
	}
	if state.CorruptedEntries != 2 {
		t.Errorf("Expected CorruptedEntries 2, got %d", state.CorruptedEntries)
	}
}
