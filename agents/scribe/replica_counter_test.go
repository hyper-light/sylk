package scribe

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAcquireReplicaGeneration_StartsAtOne(t *testing.T) {
	dir := t.TempDir()
	got, err := AcquireReplicaGeneration(dir, "tester-pipeline")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got != 1 {
		t.Fatalf("first acquire = %d, want 1", got)
	}
}

func TestAcquireReplicaGeneration_MonotonicallyIncrements(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 5; i++ {
		got, err := AcquireReplicaGeneration(dir, "engineer")
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		if got != i {
			t.Fatalf("acquire %d returned %d", i, got)
		}
	}
}

func TestAcquireReplicaGeneration_PerAgentTypeIndependent(t *testing.T) {
	dir := t.TempDir()
	if g, _ := AcquireReplicaGeneration(dir, "tester-pipeline"); g != 1 {
		t.Fatalf("tester first = %d", g)
	}
	if g, _ := AcquireReplicaGeneration(dir, "engineer"); g != 1 {
		t.Fatalf("engineer first = %d", g)
	}
	if g, _ := AcquireReplicaGeneration(dir, "tester-pipeline"); g != 2 {
		t.Fatalf("tester second = %d", g)
	}
}

func TestAcquireReplicaGeneration_PersistsAcrossInstantiations(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		// Each iteration simulates a fresh process by going through the
		// file-based state only.
		_, _ = AcquireReplicaGeneration(dir, "designer")
	}
	current := CurrentReplicaGeneration(dir, "designer")
	if current != 3 {
		t.Fatalf("CurrentReplicaGeneration = %d, want 3", current)
	}
	next, _ := AcquireReplicaGeneration(dir, "designer")
	if next != 4 {
		t.Fatalf("next acquire after 3 = %d, want 4", next)
	}
}

// TestAcquireReplicaGeneration_DegradesGracefullyOnEmptyArgs documents
// that empty session dir or agent type returns zero with no error
// rather than panicking. The scribe must keep working even when its
// session-dir wiring is incomplete.
func TestAcquireReplicaGeneration_DegradesGracefullyOnEmptyArgs(t *testing.T) {
	if g, err := AcquireReplicaGeneration("", "engineer"); g != 0 || err != nil {
		t.Fatalf("empty sessionDir: got=%d err=%v", g, err)
	}
	if g, err := AcquireReplicaGeneration(t.TempDir(), ""); g != 0 || err != nil {
		t.Fatalf("empty agentType: got=%d err=%v", g, err)
	}
}

// TestAcquireReplicaGeneration_RecoversFromCorruptCounter documents
// the non-happy-path: if the counter file is unreadable garbage, the
// next acquire restarts at 1 rather than panicking. Better to
// over-count (and collide with prior generations of the same logical
// number) than to fail the boot.
func TestAcquireReplicaGeneration_RecoversFromCorruptCounter(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "tester-pipeline")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "replica_counter"), []byte("not a number"), 0o644); err != nil {
		t.Fatalf("write corrupt counter: %v", err)
	}
	got, err := AcquireReplicaGeneration(dir, "tester-pipeline")
	if err != nil {
		t.Fatalf("acquire on corrupt counter: %v", err)
	}
	if got != 1 {
		t.Fatalf("acquire on corrupt counter = %d, want 1 (treat as fresh)", got)
	}
}

func TestAcquireReplicaGeneration_ConcurrentInProcessIsSerialized(t *testing.T) {
	dir := t.TempDir()
	const concurrent = 50
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []int
	)
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			defer wg.Done()
			g, err := AcquireReplicaGeneration(dir, "tester-pipeline")
			if err != nil {
				t.Errorf("concurrent acquire: %v", err)
				return
			}
			mu.Lock()
			results = append(results, g)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Each acquire should return a distinct value 1..N.
	seen := make(map[int]bool, concurrent)
	for _, g := range results {
		if seen[g] {
			t.Fatalf("duplicate generation %d under concurrent acquire", g)
		}
		seen[g] = true
	}
	if len(seen) != concurrent {
		t.Fatalf("expected %d distinct generations, got %d", concurrent, len(seen))
	}
}

// TestAcquireReplicaGeneration_FilePathShape documents the canonical
// on-disk path so future refactors don't accidentally relocate the
// counter and break reads from prior process generations.
func TestAcquireReplicaGeneration_FilePathShape(t *testing.T) {
	dir := t.TempDir()
	if _, err := AcquireReplicaGeneration(dir, "tester-pipeline"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	expected := filepath.Join(dir, "agents", "tester-pipeline", "replica_counter")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("counter file not at expected path %s: %v", expected, err)
	}
	data, _ := os.ReadFile(expected)
	if !strings.Contains(string(data), "1") {
		t.Errorf("counter file should contain '1'; got %q", string(data))
	}
}
