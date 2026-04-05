package shared

import "testing"

func TestPipelineWorkerCanonicalID_IsDeterministicPerLogicalWorker(t *testing.T) {
	first := PipelineWorkerCanonicalID("session-1", "task-1", "engineer")
	second := PipelineWorkerCanonicalID("session-1", "task-1", "engineer")
	if first == "" {
		t.Fatal("expected non-empty canonical worker id")
	}
	if first != second {
		t.Fatalf("PipelineWorkerCanonicalID() = %q then %q, want stable value", first, second)
	}
}

func TestPipelineWorkerCanonicalID_DistinguishesTaskSessionAndRole(t *testing.T) {
	base := PipelineWorkerCanonicalID("session-1", "task-1", "engineer")
	if other := PipelineWorkerCanonicalID("session-1", "task-1", "designer"); other == base {
		t.Fatalf("different roles should not share worker id %q", base)
	}
	if other := PipelineWorkerCanonicalID("session-1", "task-2", "engineer"); other == base {
		t.Fatalf("different task ids should not share worker id %q", base)
	}
	if other := PipelineWorkerCanonicalID("session-2", "task-1", "engineer"); other == base {
		t.Fatalf("different sessions should not share worker id %q", base)
	}
}
