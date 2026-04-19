package forest

import (
	"math"
	"testing"
)

// TestBranchPacket_ProvenanceRefs_UnionsAndDedupes locks the MEM-05
// provenance-accessor contract. Multiple supporting evidence items can
// carry overlapping ProvenanceRefs; the packet-level accessor
// de-duplicates them in first-seen order so the Architect sees one
// canonical list.
func TestBranchPacket_ProvenanceRefs_UnionsAndDedupes(t *testing.T) {
	p := &BranchPacket{
		Support: []PacketEvidence{
			{ProvenanceRefs: []string{"src-a", "src-b"}},
			{ProvenanceRefs: []string{"src-b", "src-c", ""}}, // empty trimmed
			{ProvenanceRefs: []string{"src-a"}},              // duplicate trimmed
		},
	}
	got := p.ProvenanceRefs()
	want := []string{"src-a", "src-b", "src-c"}
	if len(got) != len(want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d] = %q, want %q", i, got[i], v)
		}
	}
}

// TestBranchPacket_ProvenanceRefs_NilSafe verifies the nil contract.
func TestBranchPacket_ProvenanceRefs_NilSafe(t *testing.T) {
	var p *BranchPacket
	if refs := p.ProvenanceRefs(); len(refs) != 0 {
		t.Errorf("nil packet refs = %v, want empty", refs)
	}
}

// TestBranchPacket_RolledUpConfidence_GeometricMean verifies the
// rolled-up confidence uses the geometric mean of branch + score
// confidence, rewarding agreement and penalizing mismatch.
func TestBranchPacket_RolledUpConfidence_GeometricMean(t *testing.T) {
	p := &BranchPacket{
		Branch: &Branch{Confidence: 0.64},
		Score:  PacketScore{Confidence: 0.81},
	}
	got := p.RolledUpConfidence()
	want := math.Sqrt(0.64 * 0.81)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RolledUpConfidence = %v, want %v", got, want)
	}
}

// TestBranchPacket_RolledUpConfidence_ZeroEitherIsZero verifies the
// weakest-link semantic: if either input confidence is zero, the
// rolled-up value is zero — a conservative view that prevents treating
// untrusted packets as credible just because the other axis is high.
func TestBranchPacket_RolledUpConfidence_ZeroEitherIsZero(t *testing.T) {
	cases := []struct {
		name             string
		branchConf       float64
		scoreConf        float64
		wantRolledUpZero bool
	}{
		{"both positive", 0.5, 0.5, false},
		{"branch zero", 0, 0.9, true},
		{"score zero", 0.9, 0, true},
		{"both zero", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &BranchPacket{
				Branch: &Branch{Confidence: tc.branchConf},
				Score:  PacketScore{Confidence: tc.scoreConf},
			}
			got := p.RolledUpConfidence()
			if tc.wantRolledUpZero && got != 0 {
				t.Errorf("got = %v, want 0", got)
			}
			if !tc.wantRolledUpZero && got <= 0 {
				t.Errorf("got = %v, want > 0", got)
			}
		})
	}
}

// TestBranchPacket_RolledUpConfidence_NilBranchIsZero verifies nil-branch
// safety.
func TestBranchPacket_RolledUpConfidence_NilBranchIsZero(t *testing.T) {
	p := &BranchPacket{Score: PacketScore{Confidence: 0.9}}
	if got := p.RolledUpConfidence(); got != 0 {
		t.Errorf("nil branch confidence = %v, want 0", got)
	}
}

// TestBranchPacket_MaxConflictSeverity_FindsMax verifies the
// conflict-severity accessor returns the maximum severity across all
// conflicts — the gating signal for consumer escalation decisions.
func TestBranchPacket_MaxConflictSeverity_FindsMax(t *testing.T) {
	p := &BranchPacket{
		Conflicts: []PacketConflict{
			{Severity: 0.3},
			{Severity: 0.7},
			{Severity: 0.1},
		},
	}
	if got := p.MaxConflictSeverity(); got != 0.7 {
		t.Errorf("MaxConflictSeverity = %v, want 0.7", got)
	}
}

// TestBranchPacket_MaxConflictSeverity_EmptyIsZero verifies the no-
// conflict case returns 0 cleanly.
func TestBranchPacket_MaxConflictSeverity_EmptyIsZero(t *testing.T) {
	p := &BranchPacket{}
	if got := p.MaxConflictSeverity(); got != 0 {
		t.Errorf("empty conflicts = %v, want 0", got)
	}
}

// TestBranchPacket_HasUnresolvedConflicts verifies the convenience
// predicate tracks severity > 0.
func TestBranchPacket_HasUnresolvedConflicts(t *testing.T) {
	noConflict := &BranchPacket{}
	if noConflict.HasUnresolvedConflicts() {
		t.Error("no conflicts should return false")
	}
	zeroSeverity := &BranchPacket{Conflicts: []PacketConflict{{Severity: 0}}}
	if zeroSeverity.HasUnresolvedConflicts() {
		t.Error("severity=0 should be treated as resolved")
	}
	real := &BranchPacket{Conflicts: []PacketConflict{{Severity: 0.01}}}
	if !real.HasUnresolvedConflicts() {
		t.Error("any positive severity should be unresolved")
	}
}
