package shared

import (
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestMergeReplicaAgentID_Deterministic(t *testing.T) {
	ver := versioning.SemanticVersion{Major: 3, Minor: 7}
	a := MergeReplicaAgentID("inspector-global", "sess-1", ver)
	b := MergeReplicaAgentID("inspector-global", "sess-1", ver)
	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}
	if a != "inspector-global#replica-sess-1:3.7" {
		t.Fatalf("unexpected format: %q", a)
	}
}

func TestMergeReplicaAgentID_DistinctPerAgentType(t *testing.T) {
	ver := versioning.SemanticVersion{Major: 3, Minor: 7}
	inspector := MergeReplicaAgentID("inspector-global", "sess-1", ver)
	tester := MergeReplicaAgentID("tester-global", "sess-1", ver)
	if inspector == tester {
		t.Fatal("inspector and tester share identity for same merge")
	}
}

func TestMergeReplicaAgentID_DistinctPerSession(t *testing.T) {
	ver := versioning.SemanticVersion{Major: 3, Minor: 7}
	a := MergeReplicaAgentID("inspector-global", "sess-1", ver)
	b := MergeReplicaAgentID("inspector-global", "sess-2", ver)
	if a == b {
		t.Fatal("sessions share identity for same merge")
	}
}

func TestMergeReplicaAgentID_DistinctPerMerge(t *testing.T) {
	v7 := versioning.SemanticVersion{Major: 3, Minor: 7}
	v8 := versioning.SemanticVersion{Major: 3, Minor: 8}
	a := MergeReplicaAgentID("inspector-global", "sess-1", v7)
	b := MergeReplicaAgentID("inspector-global", "sess-1", v8)
	if a == b {
		t.Fatal("merges share identity within the same session")
	}
}

func TestParseMergeReplicaAgentID_RoundTrip(t *testing.T) {
	ver := versioning.SemanticVersion{Major: 12, Minor: 345}
	id := MergeReplicaAgentID("tester-global", "sess-xyz", ver)
	agentType, sessionID, gotVer, ok := ParseMergeReplicaAgentID(id)
	if !ok {
		t.Fatalf("ParseMergeReplicaAgentID returned not-ok for %q", id)
	}
	if agentType != "tester-global" {
		t.Errorf("agentType = %q, want tester-global", agentType)
	}
	if sessionID != "sess-xyz" {
		t.Errorf("sessionID = %q, want sess-xyz", sessionID)
	}
	if gotVer != ver {
		t.Errorf("version = %v, want %v", gotVer, ver)
	}
}

func TestParseMergeReplicaAgentID_RejectsNonReplicaID(t *testing.T) {
	for _, id := range []string{
		"",
		"inspector-global",
		"inspector-global-abc123",
		"random-string",
		"inspector#replica-", // missing version
		"inspector#replica-sess:abc",
	} {
		if _, _, _, ok := ParseMergeReplicaAgentID(id); ok {
			t.Errorf("ParseMergeReplicaAgentID(%q) = ok, want not-ok", id)
		}
	}
}
