package bridge

import (
	"testing"
)

// Performance budgets per UI_DESIGN.md §7 P8.3 + §4.7.5:
//   - per-resolver-event: < 10 µs, < 200 B
//   - per-artifact pair: < 20 µs, < 512 B
// These benchmarks are sanity rails — they do not assert against the
// budget at test time (Go's `go test -bench` is the inspection tool),
// but the numbers should stay flat as the resolver evolves.

func BenchmarkResolver_OpenTopLevelCycle(b *testing.B) {
	r := newCycleResolver()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.onClaimCreated("c"+itoa(i), "agent", "subject", "", "")
	}
}

func BenchmarkResolver_AttachChildToCycle(b *testing.B) {
	r := newCycleResolver()
	r.onClaimCreated("root", "agent", "subject", "", "")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.onClaimCreated("child"+itoa(i), "agent", "subject", "root", "")
	}
}

func BenchmarkResolver_ArtifactPair(b *testing.B) {
	r := newCycleResolver()
	r.onClaimCreated("root", "agent", "subject", "", "")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		artID := "a" + itoa(i)
		r.onArtifactStarted(artID, "root")
		r.onArtifactCompleted(artID)
	}
}

func BenchmarkResolver_HandoffSequence(b *testing.B) {
	r := newCycleResolver()
	prev := "seed"
	r.onClaimCreated(prev, "agent-0", "agent-0", "", "")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		successor := "h" + itoa(i)
		r.onClaimCreated(successor, "agent-"+itoa(i+1), "agent-"+itoa(i+1), "", prev)
		prev = successor
	}
}

// CycleForClaim should remain a single map lookup under mu — sub-µs.
func BenchmarkResolver_CycleForClaim(b *testing.B) {
	r := newCycleResolver()
	r.onClaimCreated("root", "agent", "subject", "", "")
	for i := 0; i < 100; i++ {
		r.onClaimCreated("child"+itoa(i), "agent", "subject", "root", "")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.CycleForClaim("child50")
	}
}
