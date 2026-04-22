package versioning

import (
	"testing"
)

func TestReplicaLifecycleLog_SpawnedToDecided(t *testing.T) {
	l := NewReplicaLifecycleLog(nil)
	ver := SemanticVersion{Major: 3, Minor: 7}
	if err := l.RecordSpawned("inspector-global#replica-sess-1:3.7", "inspector-global", ver); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	entry := l.Lookup("inspector-global#replica-sess-1:3.7")
	if entry == nil || entry.State != ReplicaLifecycleInFlight {
		t.Fatalf("post-spawn state = %v, want in_flight", entry)
	}

	if err := l.RecordDecided("inspector-global#replica-sess-1:3.7", ReplicaDecisionAccepted, "looks good", nil); err != nil {
		t.Fatalf("decide: %v", err)
	}
	entry = l.Lookup("inspector-global#replica-sess-1:3.7")
	if entry.State != ReplicaLifecycleDecided {
		t.Fatalf("post-decide state = %v, want decided", entry.State)
	}
	if entry.Decision != ReplicaDecisionAccepted {
		t.Fatalf("decision = %v, want accepted", entry.Decision)
	}

	if len(l.InFlight()) != 0 {
		t.Fatalf("InFlight = %d, want 0 after decide", len(l.InFlight()))
	}
}

func TestReplicaLifecycleLog_CrashedStaysInFlightNo(t *testing.T) {
	l := NewReplicaLifecycleLog(nil)
	ver := SemanticVersion{Major: 1, Minor: 1}
	if err := l.RecordSpawned("r1", "t", ver); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := l.RecordCrashed("r1", "ctx cancelled"); err != nil {
		t.Fatalf("crash: %v", err)
	}
	entry := l.Lookup("r1")
	if entry.State != ReplicaLifecycleCrashed {
		t.Fatalf("state = %v, want crashed", entry.State)
	}
	if len(l.InFlight()) != 0 {
		t.Fatal("InFlight should be empty after crash")
	}
}

func TestReplicaLifecycleLog_InFlightListsSurvivors(t *testing.T) {
	l := NewReplicaLifecycleLog(nil)
	ver := SemanticVersion{Minor: 1}
	l.RecordSpawned("r1", "inspector-global", ver)
	l.RecordSpawned("r2", "tester-global", ver)
	l.RecordSpawned("r3", "inspector-global", SemanticVersion{Minor: 2})
	l.RecordDecided("r1", ReplicaDecisionAccepted, "ok", nil)

	inFlight := l.InFlight()
	if len(inFlight) != 2 {
		t.Fatalf("InFlight count = %d, want 2", len(inFlight))
	}
	ids := map[string]bool{}
	for _, e := range inFlight {
		ids[e.ReplicaID] = true
	}
	if !ids["r2"] || !ids["r3"] {
		t.Fatalf("InFlight ids = %v, want r2 and r3", ids)
	}
}

func TestReplicaLifecycleLog_DurableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	l1 := NewReplicaLifecycleLog(wal)
	ver := SemanticVersion{Major: 4, Minor: 2}
	l1.RecordSpawned("inspector-global#replica-s:4.2", "inspector-global", ver)
	l1.RecordSpawned("tester-global#replica-s:4.2", "tester-global", ver)
	l1.RecordDecided("inspector-global#replica-s:4.2", ReplicaDecisionRejected, "cross-module regression", []string{"dep-cycle"})
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	wal2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	defer wal2.Close()
	l2 := NewReplicaLifecycleLog(wal2)
	if err := wal2.Replay(func(e *ControlEntry) error {
		l2.ApplyControlEntry(e)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	inspector := l2.Lookup("inspector-global#replica-s:4.2")
	if inspector == nil || inspector.State != ReplicaLifecycleDecided || inspector.Decision != ReplicaDecisionRejected {
		t.Fatalf("inspector after replay: %+v, want decided/rejected", inspector)
	}
	if len(inspector.Concerns) != 1 || inspector.Concerns[0] != "dep-cycle" {
		t.Fatalf("concerns after replay: %v", inspector.Concerns)
	}

	tester := l2.Lookup("tester-global#replica-s:4.2")
	if tester == nil || tester.State != ReplicaLifecycleInFlight {
		t.Fatalf("tester after replay: %+v, want in_flight", tester)
	}
	inFlight := l2.InFlight()
	if len(inFlight) != 1 || inFlight[0].ReplicaID != "tester-global#replica-s:4.2" {
		t.Fatalf("InFlight after replay = %v, want [tester]", inFlight)
	}
}

func TestReplicaLifecycleLog_InvalidTransitions(t *testing.T) {
	l := NewReplicaLifecycleLog(nil)
	if err := l.RecordDecided("unknown", ReplicaDecisionAccepted, "", nil); err == nil {
		t.Fatal("RecordDecided on unknown: expected error")
	}
	if err := l.RecordCrashed("unknown", "x"); err == nil {
		t.Fatal("RecordCrashed on unknown: expected error")
	}
	ver := SemanticVersion{Minor: 1}
	l.RecordSpawned("r", "t", ver)
	l.RecordDecided("r", ReplicaDecisionAccepted, "", nil)
	if err := l.RecordDecided("r", ReplicaDecisionRejected, "", nil); err == nil {
		t.Fatal("double-decide: expected error")
	}
	if err := l.RecordCrashed("r", "x"); err == nil {
		t.Fatal("crash after decide: expected error")
	}
}
