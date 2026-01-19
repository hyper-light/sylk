package chunking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// ChunkConfigWALEntry Tests
// =============================================================================

func TestChunkConfigWALEntry_Validate(t *testing.T) {
	tests := []struct {
		name    string
		entry   ChunkConfigWALEntry
		wantErr bool
	}{
		{
			name: "valid entry with config snapshot",
			entry: ChunkConfigWALEntry{
				Timestamp:      time.Now(),
				SequenceID:     1,
				ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
				EntryType:      EntryTypeSnapshot,
			},
			wantErr: false,
		},
		{
			name: "valid entry with learned params",
			entry: ChunkConfigWALEntry{
				Timestamp: time.Now(),
				SequenceID: 2,
				LearnedParams: map[string]*DomainLearnedParams{
					"code": {Domain: "code", Confidence: 0.5},
				},
				EntryType: EntryTypeCheckpoint,
			},
			wantErr: false,
		},
		{
			name: "valid observation entry",
			entry: ChunkConfigWALEntry{
				Timestamp:      time.Now(),
				SequenceID:     3,
				ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
				Observation: &ChunkUsageObservation{
					Domain:     DomainCode,
					TokenCount: 300,
					ChunkID:    "chunk-1",
					SessionID:  "session-1",
					Timestamp:  time.Now(),
				},
				EntryType: EntryTypeObservation,
			},
			wantErr: false,
		},
		{
			name: "zero timestamp",
			entry: ChunkConfigWALEntry{
				Timestamp:      time.Time{},
				SequenceID:     1,
				ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
				EntryType:      EntryTypeSnapshot,
			},
			wantErr: true,
		},
		{
			name: "nil config snapshot and learned params",
			entry: ChunkConfigWALEntry{
				Timestamp:      time.Now(),
				SequenceID:     1,
				ConfigSnapshot: nil,
				LearnedParams:  nil,
				EntryType:      EntryTypeSnapshot,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if tt.wantErr {
				if err == nil {
					t.Error("Validate() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestChunkConfigWALEntryType_String(t *testing.T) {
	tests := []struct {
		entryType ChunkConfigWALEntryType
		want      string
	}{
		{EntryTypeObservation, "observation"},
		{EntryTypeCheckpoint, "checkpoint"},
		{EntryTypeSnapshot, "snapshot"},
		{ChunkConfigWALEntryType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.entryType.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// ChunkConfigWAL Constructor Tests
// =============================================================================

func TestNewChunkConfigWAL(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		config  ChunkConfigWALConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ChunkConfigWALConfig{
				FilePath:  filepath.Join(tmpDir, "test1.wal"),
				CreateDir: true,
			},
			wantErr: false,
		},
		{
			name: "valid config with subdirectory",
			config: ChunkConfigWALConfig{
				FilePath:  filepath.Join(tmpDir, "subdir", "test2.wal"),
				CreateDir: true,
			},
			wantErr: false,
		},
		{
			name: "empty file path",
			config: ChunkConfigWALConfig{
				FilePath: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wal, err := NewChunkConfigWAL(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("NewChunkConfigWAL() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewChunkConfigWAL() unexpected error: %v", err)
			}
			if wal == nil {
				t.Fatal("NewChunkConfigWAL() returned nil")
			}
			defer wal.Close()

			if wal.FilePath() != tt.config.FilePath {
				t.Errorf("FilePath() = %v, want %v", wal.FilePath(), tt.config.FilePath)
			}
			if wal.IsClosed() {
				t.Error("WAL should not be closed after creation")
			}
		})
	}
}

func TestDefaultChunkConfigWALConfig(t *testing.T) {
	dir := "/tmp/test"
	config := DefaultChunkConfigWALConfig(dir)

	if config.FilePath != filepath.Join(dir, "chunk_config.wal") {
		t.Errorf("FilePath = %v, want %v", config.FilePath, filepath.Join(dir, "chunk_config.wal"))
	}
	if config.SyncInterval != 0 {
		t.Errorf("SyncInterval = %v, want 0", config.SyncInterval)
	}
	if !config.CreateDir {
		t.Error("CreateDir should be true by default")
	}
}

// =============================================================================
// Write and Read Single Entry Tests
// =============================================================================

func TestChunkConfigWAL_AppendAndLoadSingleEntry(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: true,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Create and append an entry
	entry := &ChunkConfigWALEntry{
		ConfigSnapshot: &ChunkConfigSnapshot{
			MaxTokens: 2048,
			TargetTokens: &LearnedContextSizeSnapshot{
				Alpha:            1000,
				Beta:             2.0,
				EffectiveSamples: 10,
				PriorAlpha:       500,
				PriorBeta:        1.0,
			},
		},
		LearnedParams: map[string]*DomainLearnedParams{
			"code": {
				Domain:           "code",
				Confidence:       0.75,
				ObservationCount: 15,
			},
		},
		EntryType: EntryTypeSnapshot,
	}

	seqID, err := wal.AppendEntry(entry)
	if err != nil {
		t.Fatalf("AppendEntry() error: %v", err)
	}
	if seqID != 1 {
		t.Errorf("AppendEntry() seqID = %v, want 1", seqID)
	}

	// Verify sequence ID was set
	if entry.SequenceID != 1 {
		t.Errorf("Entry SequenceID = %v, want 1", entry.SequenceID)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Entry Timestamp should be set")
	}

	// Load entries and verify
	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("LoadEntries() returned %d entries, want 1", len(entries))
	}

	loaded := entries[0]
	if loaded.SequenceID != 1 {
		t.Errorf("Loaded SequenceID = %v, want 1", loaded.SequenceID)
	}
	if loaded.ConfigSnapshot.MaxTokens != 2048 {
		t.Errorf("Loaded MaxTokens = %v, want 2048", loaded.ConfigSnapshot.MaxTokens)
	}
	if loaded.LearnedParams["code"].Confidence != 0.75 {
		t.Errorf("Loaded Confidence = %v, want 0.75", loaded.LearnedParams["code"].Confidence)
	}

	wal.Close()
}

// =============================================================================
// Multiple Entries Append Tests
// =============================================================================

func TestChunkConfigWAL_AppendMultipleEntries(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: true,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Append multiple entries
	numEntries := 10
	for i := 0; i < numEntries; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{
				MaxTokens: 2048 + i,
			},
			EntryType: EntryTypeCheckpoint,
		}
		seqID, err := wal.AppendEntry(entry)
		if err != nil {
			t.Fatalf("AppendEntry() iteration %d error: %v", i, err)
		}
		if seqID != uint64(i+1) {
			t.Errorf("AppendEntry() iteration %d seqID = %v, want %v", i, seqID, i+1)
		}
	}

	// Verify last sequence ID
	if wal.LastSequenceID() != uint64(numEntries) {
		t.Errorf("LastSequenceID() = %v, want %v", wal.LastSequenceID(), numEntries)
	}

	// Load and verify all entries
	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() error: %v", err)
	}
	if len(entries) != numEntries {
		t.Fatalf("LoadEntries() returned %d entries, want %d", len(entries), numEntries)
	}

	for i, entry := range entries {
		if entry.SequenceID != uint64(i+1) {
			t.Errorf("Entry %d SequenceID = %v, want %v", i, entry.SequenceID, i+1)
		}
		if entry.ConfigSnapshot.MaxTokens != 2048+i {
			t.Errorf("Entry %d MaxTokens = %v, want %v", i, entry.ConfigSnapshot.MaxTokens, 2048+i)
		}
	}

	wal.Close()
}

// =============================================================================
// Recovery from Existing WAL Tests
// =============================================================================

func TestChunkConfigWAL_RecoverFromExistingWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	// Create WAL and write entries
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: true,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Write a snapshot entry
	snapshotEntry := &ChunkConfigWALEntry{
		ConfigSnapshot: &ChunkConfigSnapshot{
			MaxTokens: 2048,
			TargetTokens: &LearnedContextSizeSnapshot{
				Alpha:            500,
				Beta:             1.0,
				EffectiveSamples: 5,
				PriorAlpha:       500,
				PriorBeta:        1.0,
			},
		},
		LearnedParams: map[string]*DomainLearnedParams{
			"code": {
				Domain: "code",
				Config: &ChunkConfigSnapshot{
					MaxTokens: 2048,
					TargetTokens: &LearnedContextSizeSnapshot{
						Alpha:            300,
						Beta:             1.0,
						EffectiveSamples: 10,
						PriorAlpha:       500,
						PriorBeta:        1.0,
					},
				},
				Confidence:       0.6,
				ObservationCount: 10,
			},
		},
		EntryType: EntryTypeSnapshot,
	}
	if _, err := wal1.AppendEntry(snapshotEntry); err != nil {
		t.Fatalf("AppendEntry() snapshot error: %v", err)
	}

	// Write observation entries
	for i := 0; i < 5; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300 + i*10,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		obsEntry := &ChunkConfigWALEntry{
			ConfigSnapshot: snapshotEntry.ConfigSnapshot,
			Observation:    &obs,
			EntryType:      EntryTypeObservation,
		}
		if _, err := wal1.AppendEntry(obsEntry); err != nil {
			t.Fatalf("AppendEntry() observation %d error: %v", i, err)
		}
	}

	lastSeq := wal1.LastSequenceID()
	wal1.Close()

	// Open WAL again and verify sequence ID is recovered
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: false,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() reopen error: %v", err)
	}
	defer wal2.Close()

	if wal2.LastSequenceID() != lastSeq {
		t.Errorf("Recovered LastSequenceID() = %v, want %v", wal2.LastSequenceID(), lastSeq)
	}

	// Test Recover function
	learner, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if learner == nil {
		t.Fatal("Recover() returned nil learner")
	}

	// Verify recovered state
	if learner.GlobalPriors == nil {
		t.Error("GlobalPriors should not be nil after recovery")
	}
	if learner.GlobalPriors.MaxTokens != 2048 {
		t.Errorf("Recovered MaxTokens = %v, want 2048", learner.GlobalPriors.MaxTokens)
	}

	// Verify domain config was recovered
	if _, exists := learner.DomainConfigs[DomainCode]; !exists {
		t.Error("DomainCode config should exist after recovery")
	}
}

func TestChunkConfigWAL_RecoverSequenceIDContinues(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	// Create and close WAL with some entries
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	for i := 0; i < 5; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		}
		wal1.AppendEntry(entry)
	}
	wal1.Close()

	// Reopen and append more
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() reopen error: %v", err)
	}

	entry := &ChunkConfigWALEntry{
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 4096},
		EntryType:      EntryTypeCheckpoint,
	}
	seqID, err := wal2.AppendEntry(entry)
	if err != nil {
		t.Fatalf("AppendEntry() after reopen error: %v", err)
	}

	// Sequence should continue from 5 to 6
	if seqID != 6 {
		t.Errorf("SeqID after reopen = %v, want 6", seqID)
	}

	wal2.Close()
}

// =============================================================================
// Empty WAL Handling Tests
// =============================================================================

func TestChunkConfigWAL_EmptyWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "empty.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: true,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	// Load from empty WAL
	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() from empty WAL error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("LoadEntries() from empty WAL returned %d entries, want 0", len(entries))
	}

	// Recover from empty WAL
	learner, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover() from empty WAL error: %v", err)
	}
	if learner == nil {
		t.Fatal("Recover() from empty WAL returned nil")
	}
	if learner.GlobalPriors == nil {
		t.Error("Recovered learner should have GlobalPriors")
	}

	// Sequence ID should be 0
	if wal.LastSequenceID() != 0 {
		t.Errorf("LastSequenceID() on empty WAL = %v, want 0", wal.LastSequenceID())
	}
}

// =============================================================================
// Corrupt Entry Handling Tests
// =============================================================================

func TestChunkConfigWAL_CorruptEntrySkipped(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "corrupt.wal")

	// Create file with valid and corrupted entries
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Write valid entry
	validEntry := ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		SequenceID:     1,
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
		EntryType:      EntryTypeSnapshot,
	}
	validData, _ := json.Marshal(validEntry)
	f.Write(validData)
	f.Write([]byte{'\n'})

	// Write corrupted entry (invalid JSON)
	f.Write([]byte("this is not valid json\n"))

	// Write another valid entry
	validEntry2 := ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		SequenceID:     3,
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 4096},
		EntryType:      EntryTypeCheckpoint,
	}
	validData2, _ := json.Marshal(validEntry2)
	f.Write(validData2)
	f.Write([]byte{'\n'})

	f.Close()

	// Open WAL and load entries
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:  walPath,
		CreateDir: false,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() error: %v", err)
	}

	// Should have 2 valid entries (corrupted one skipped)
	if len(entries) != 2 {
		t.Errorf("LoadEntries() returned %d entries, want 2", len(entries))
	}

	// Verify the valid entries
	if entries[0].ConfigSnapshot.MaxTokens != 2048 {
		t.Errorf("First entry MaxTokens = %v, want 2048", entries[0].ConfigSnapshot.MaxTokens)
	}
	if entries[1].ConfigSnapshot.MaxTokens != 4096 {
		t.Errorf("Second entry MaxTokens = %v, want 4096", entries[1].ConfigSnapshot.MaxTokens)
	}
}

func TestChunkConfigWAL_PartiallyCorruptJSON(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "partial_corrupt.wal")

	// Create file with valid entry and partial/incomplete JSON
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Write valid entry
	validEntry := ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		SequenceID:     1,
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
		EntryType:      EntryTypeSnapshot,
	}
	validData, _ := json.Marshal(validEntry)
	f.Write(validData)
	f.Write([]byte{'\n'})

	// Write incomplete/truncated JSON
	f.Write([]byte(`{"timestamp":"2024-01-01T00:00:00Z","sequence_id":2,"config_snapshot":{"max_to`))
	f.Write([]byte{'\n'})

	// Write empty line (should be skipped)
	f.Write([]byte{'\n'})

	// Write another valid entry
	validEntry2 := ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		SequenceID:     4,
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 1024},
		EntryType:      EntryTypeCheckpoint,
	}
	validData2, _ := json.Marshal(validEntry2)
	f.Write(validData2)
	f.Write([]byte{'\n'})

	f.Close()

	// Load entries
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() error: %v", err)
	}

	// Should have 2 valid entries
	if len(entries) != 2 {
		t.Errorf("LoadEntries() returned %d entries, want 2", len(entries))
	}
}

// =============================================================================
// Closed WAL Tests
// =============================================================================

func TestChunkConfigWAL_OperationsAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "closed.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Close the WAL
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Verify it's closed
	if !wal.IsClosed() {
		t.Error("IsClosed() should return true after Close()")
	}

	// Append should fail
	entry := &ChunkConfigWALEntry{
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
		EntryType:      EntryTypeSnapshot,
	}
	_, err = wal.AppendEntry(entry)
	if err != ErrWALClosed {
		t.Errorf("AppendEntry() after close error = %v, want ErrWALClosed", err)
	}

	// LoadEntries should fail
	_, err = wal.LoadEntries()
	if err != ErrWALClosed {
		t.Errorf("LoadEntries() after close error = %v, want ErrWALClosed", err)
	}

	// Sync should fail
	err = wal.Sync()
	if err != ErrWALClosed {
		t.Errorf("Sync() after close error = %v, want ErrWALClosed", err)
	}

	// Double close should return error
	err = wal.Close()
	if err != ErrWALClosed {
		t.Errorf("Double Close() error = %v, want ErrWALClosed", err)
	}
}

// =============================================================================
// Snapshot and Observation Entry Creation Tests
// =============================================================================

func TestChunkConfigWAL_CreateSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "snapshot.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	// Create a learner with some state
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record some observations
	for i := 0; i < 5; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    300,
			ContextBefore: 75,
			ContextAfter:  40,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       "chunk-" + string(rune('0'+i)),
			SessionID:     "session-1",
		}
		learner.RecordObservation(obs)
	}

	// Create snapshot
	entry, err := wal.CreateSnapshot(learner)
	if err != nil {
		t.Fatalf("CreateSnapshot() error: %v", err)
	}
	if entry == nil {
		t.Fatal("CreateSnapshot() returned nil")
	}
	if entry.EntryType != EntryTypeSnapshot {
		t.Errorf("Entry type = %v, want EntryTypeSnapshot", entry.EntryType)
	}
	if entry.ConfigSnapshot == nil {
		t.Error("ConfigSnapshot should not be nil")
	}
	if entry.LearnedParams == nil {
		t.Error("LearnedParams should not be nil")
	}
	if _, exists := entry.LearnedParams["code"]; !exists {
		t.Error("LearnedParams should contain 'code' domain")
	}
}

func TestChunkConfigWAL_CreateObservationEntry(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "obs.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record an observation first
	obs := ChunkUsageObservation{
		Domain:        DomainAcademic,
		TokenCount:    500,
		ContextBefore: 100,
		ContextAfter:  50,
		WasUseful:     true,
		Timestamp:     time.Now(),
		ChunkID:       "chunk-1",
		SessionID:     "session-1",
	}
	learner.RecordObservation(obs)

	// Create observation entry
	entry, err := wal.CreateObservationEntry(obs, learner)
	if err != nil {
		t.Fatalf("CreateObservationEntry() error: %v", err)
	}
	if entry == nil {
		t.Fatal("CreateObservationEntry() returned nil")
	}
	if entry.EntryType != EntryTypeObservation {
		t.Errorf("Entry type = %v, want EntryTypeObservation", entry.EntryType)
	}
	if entry.Observation == nil {
		t.Error("Observation should not be nil")
	}
	if entry.Observation.Domain != DomainAcademic {
		t.Errorf("Observation domain = %v, want DomainAcademic", entry.Observation.Domain)
	}
}

func TestChunkConfigWAL_CreateSnapshotNilLearner(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "nil.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	_, err = wal.CreateSnapshot(nil)
	if err == nil {
		t.Error("CreateSnapshot(nil) should return error")
	}
}

// =============================================================================
// Truncate Tests
// =============================================================================

func TestChunkConfigWAL_Truncate(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "truncate.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Write some entries
	for i := 0; i < 5; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		}
		wal.AppendEntry(entry)
	}

	if wal.LastSequenceID() != 5 {
		t.Errorf("LastSequenceID() before truncate = %v, want 5", wal.LastSequenceID())
	}

	// Truncate
	if err := wal.Truncate(); err != nil {
		t.Fatalf("Truncate() error: %v", err)
	}

	// Sequence should reset to 0
	if wal.LastSequenceID() != 0 {
		t.Errorf("LastSequenceID() after truncate = %v, want 0", wal.LastSequenceID())
	}

	// Should be able to append again
	entry := &ChunkConfigWALEntry{
		ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 4096},
		EntryType:      EntryTypeSnapshot,
	}
	seqID, err := wal.AppendEntry(entry)
	if err != nil {
		t.Fatalf("AppendEntry() after truncate error: %v", err)
	}
	if seqID != 1 {
		t.Errorf("SeqID after truncate = %v, want 1", seqID)
	}

	// Load and verify
	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() after truncate error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("LoadEntries() returned %d entries, want 1", len(entries))
	}

	wal.Close()
}

// =============================================================================
// Full Recovery Integration Test
// =============================================================================

func TestChunkConfigWAL_FullRecoveryIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "integration.wal")

	// Create learner and record observations
	learner1, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("NewChunkConfigLearner() error: %v", err)
	}

	// Record observations across multiple domains
	observations := []ChunkUsageObservation{
		{Domain: DomainCode, TokenCount: 300, ContextBefore: 75, ContextAfter: 40, WasUseful: true, Timestamp: time.Now(), ChunkID: "c1", SessionID: "s1"},
		{Domain: DomainCode, TokenCount: 350, ContextBefore: 80, ContextAfter: 45, WasUseful: true, Timestamp: time.Now(), ChunkID: "c2", SessionID: "s1"},
		{Domain: DomainAcademic, TokenCount: 500, ContextBefore: 100, ContextAfter: 50, WasUseful: true, Timestamp: time.Now(), ChunkID: "c3", SessionID: "s1"},
		{Domain: DomainHistory, TokenCount: 400, ContextBefore: 150, ContextAfter: 30, WasUseful: false, Timestamp: time.Now(), ChunkID: "c4", SessionID: "s1"},
	}

	for _, obs := range observations {
		learner1.RecordObservation(obs)
	}

	// Create WAL and save state
	wal1, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}

	// Write a snapshot
	snapshotEntry, err := wal1.CreateSnapshot(learner1)
	if err != nil {
		t.Fatalf("CreateSnapshot() error: %v", err)
	}
	if _, err := wal1.AppendEntry(snapshotEntry); err != nil {
		t.Fatalf("AppendEntry() snapshot error: %v", err)
	}

	// Write observation entries
	for _, obs := range observations {
		obsEntry, err := wal1.CreateObservationEntry(obs, learner1)
		if err != nil {
			t.Fatalf("CreateObservationEntry() error: %v", err)
		}
		if _, err := wal1.AppendEntry(obsEntry); err != nil {
			t.Fatalf("AppendEntry() observation error: %v", err)
		}
	}

	wal1.Close()

	// Recover from WAL
	wal2, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: false})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() reopen error: %v", err)
	}
	defer wal2.Close()

	learner2, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}

	// Verify recovered state matches
	if learner2.GetObservationCount(DomainCode) == 0 {
		t.Error("DomainCode observation count should be > 0 after recovery")
	}
	if learner2.GetDomainConfidence(DomainCode) <= 0 {
		t.Error("DomainCode confidence should be > 0 after recovery")
	}
	if _, exists := learner2.DomainConfigs[DomainCode]; !exists {
		t.Error("DomainCode config should exist after recovery")
	}
	if _, exists := learner2.DomainConfigs[DomainAcademic]; !exists {
		t.Error("DomainAcademic config should exist after recovery")
	}
}

// =============================================================================
// Sync Tests
// =============================================================================

func TestChunkConfigWAL_SyncInterval(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "sync.wal")

	// Create WAL with sync interval
	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{
		FilePath:     walPath,
		SyncInterval: 100 * time.Millisecond,
		CreateDir:    true,
	})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	// Append entries quickly (should not sync each time due to interval)
	for i := 0; i < 10; i++ {
		entry := &ChunkConfigWALEntry{
			ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
			EntryType:      EntryTypeCheckpoint,
		}
		if _, err := wal.AppendEntry(entry); err != nil {
			t.Fatalf("AppendEntry() error: %v", err)
		}
	}

	// Force sync
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestChunkConfigWAL_ConcurrentAppends(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "concurrent.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	entriesPerGoroutine := 50

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < entriesPerGoroutine; i++ {
				entry := &ChunkConfigWALEntry{
					ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048 + id},
					EntryType:      EntryTypeCheckpoint,
				}
				_, err := wal.AppendEntry(entry)
				if err != nil {
					t.Errorf("Goroutine %d AppendEntry() error: %v", id, err)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify total entries
	expectedEntries := numGoroutines * entriesPerGoroutine
	if wal.LastSequenceID() != uint64(expectedEntries) {
		t.Errorf("LastSequenceID() = %v, want %v", wal.LastSequenceID(), expectedEntries)
	}

	entries, err := wal.LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries() error: %v", err)
	}
	if len(entries) != expectedEntries {
		t.Errorf("LoadEntries() returned %d entries, want %d", len(entries), expectedEntries)
	}

	// Verify all sequence IDs are unique
	seqIDs := make(map[uint64]bool)
	for _, entry := range entries {
		if seqIDs[entry.SequenceID] {
			t.Errorf("Duplicate sequence ID: %d", entry.SequenceID)
		}
		seqIDs[entry.SequenceID] = true
	}
}

func TestChunkConfigWAL_ConcurrentReadsAndWrites(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "concurrent_rw.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	var wg sync.WaitGroup

	// Writers
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				entry := &ChunkConfigWALEntry{
					ConfigSnapshot: &ChunkConfigSnapshot{MaxTokens: 2048},
					EntryType:      EntryTypeCheckpoint,
				}
				wal.AppendEntry(entry)
			}
		}(g)
	}

	// Readers
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				wal.LoadEntries()
				wal.LastSequenceID()
			}
		}(g)
	}

	wg.Wait()
}

// =============================================================================
// Domain Parsing Tests
// =============================================================================

func TestParseDomain(t *testing.T) {
	tests := []struct {
		input string
		want  Domain
	}{
		{"code", DomainCode},
		{"academic", DomainAcademic},
		{"history", DomainHistory},
		{"general", DomainGeneral},
		{"unknown", DomainGeneral},
		{"", DomainGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseDomain(tt.input); got != tt.want {
				t.Errorf("parseDomain(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Config Snapshot Conversion Tests
// =============================================================================

func TestConfigToSnapshotAndBack(t *testing.T) {
	// Create original config
	original, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	// Convert to snapshot
	snapshot := configToSnapshot(original)
	if snapshot == nil {
		t.Fatal("configToSnapshot() returned nil")
	}

	// Convert back
	restored := snapshotToConfig(snapshot)
	if restored == nil {
		t.Fatal("snapshotToConfig() returned nil")
	}

	// Verify
	if restored.MaxTokens != original.MaxTokens {
		t.Errorf("MaxTokens = %v, want %v", restored.MaxTokens, original.MaxTokens)
	}
	if restored.TargetTokens.Alpha != original.TargetTokens.Alpha {
		t.Errorf("TargetTokens.Alpha = %v, want %v", restored.TargetTokens.Alpha, original.TargetTokens.Alpha)
	}
	if restored.TargetTokens.Beta != original.TargetTokens.Beta {
		t.Errorf("TargetTokens.Beta = %v, want %v", restored.TargetTokens.Beta, original.TargetTokens.Beta)
	}
}

func TestConfigToSnapshotNil(t *testing.T) {
	if configToSnapshot(nil) != nil {
		t.Error("configToSnapshot(nil) should return nil")
	}
	if snapshotToConfig(nil) != nil {
		t.Error("snapshotToConfig(nil) should return nil")
	}
}

// =============================================================================
// Invalid Entry Tests
// =============================================================================

func TestChunkConfigWAL_AppendInvalidEntry(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "invalid.wal")

	wal, err := NewChunkConfigWAL(ChunkConfigWALConfig{FilePath: walPath, CreateDir: true})
	if err != nil {
		t.Fatalf("NewChunkConfigWAL() error: %v", err)
	}
	defer wal.Close()

	// Entry with nil ConfigSnapshot and nil LearnedParams
	invalidEntry := &ChunkConfigWALEntry{
		Timestamp:      time.Now(),
		ConfigSnapshot: nil,
		LearnedParams:  nil,
		EntryType:      EntryTypeSnapshot,
	}

	_, err = wal.AppendEntry(invalidEntry)
	if err == nil {
		t.Error("AppendEntry() with invalid entry should return error")
	}

	// Sequence ID should not have been incremented
	if wal.LastSequenceID() != 0 {
		t.Errorf("LastSequenceID() after failed append = %v, want 0", wal.LastSequenceID())
	}
}
