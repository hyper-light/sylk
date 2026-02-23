package security

import "testing"

func TestCapabilitySet_NewAndCheck(t *testing.T) {
	caps := NewCapabilitySet(
		[]string{"pause_all"},
		[]string{"requests"},
		[]string{"responses"},
		[]string{"/data"},
		[]string{"/output"},
		false,
	)

	if !caps.CanSignal("pause_all") {
		t.Fatal("should allow pause_all signal")
	}
	if caps.CanSignal("unknown") {
		t.Fatal("should deny unknown signal")
	}
	if !caps.CanPublish("requests") {
		t.Fatal("should allow publishing to requests")
	}
	if !caps.CanSubscribe("responses") {
		t.Fatal("should allow subscribing to responses")
	}
	if !caps.CanReadPath("/data") {
		t.Fatal("should allow read /data")
	}
	if !caps.CanWritePath("/output") {
		t.Fatal("should allow write /output")
	}
	if caps.CanEscalate() {
		t.Fatal("should not allow escalation")
	}
}

func TestCapabilitySet_Empty(t *testing.T) {
	caps := EmptyCapabilitySet()
	if caps.CanSignal("any") {
		t.Fatal("empty set should deny all signals")
	}
	if caps.CanPublish("any") {
		t.Fatal("empty set should deny all publish")
	}
	if caps.CanEscalate() {
		t.Fatal("empty set should deny escalation")
	}
}

func TestCapabilitySet_Grant(t *testing.T) {
	caps := EmptyCapabilitySet()
	if !caps.GrantSignal("pause_all") {
		t.Fatal("should return true for new grant")
	}
	if caps.GrantSignal("pause_all") {
		t.Fatal("should return false for duplicate grant")
	}
	if !caps.CanSignal("pause_all") {
		t.Fatal("should allow after grant")
	}
}

func TestCapabilitySet_Revoke(t *testing.T) {
	caps := NewCapabilitySet([]string{"s1"}, nil, nil, nil, nil, false)
	if !caps.RevokeSignal("s1") {
		t.Fatal("should return true for revoke")
	}
	if caps.RevokeSignal("s1") {
		t.Fatal("should return false for revoke of non-existent")
	}
	if caps.CanSignal("s1") {
		t.Fatal("should deny after revoke")
	}
}

func TestCapabilitySet_Merge(t *testing.T) {
	caps1 := NewCapabilitySet([]string{"s1"}, []string{"t1"}, nil, nil, nil, false)
	caps2 := NewCapabilitySet([]string{"s2"}, nil, []string{"t2"}, nil, nil, true)

	caps1.Merge(caps2)

	if !caps1.CanSignal("s1") || !caps1.CanSignal("s2") {
		t.Fatal("merge should union signals")
	}
	if !caps1.CanPublish("t1") {
		t.Fatal("merge should preserve existing topics")
	}
	if !caps1.CanSubscribe("t2") {
		t.Fatal("merge should add subscribe topics")
	}
	if !caps1.CanEscalate() {
		t.Fatal("merge should OR escalation")
	}
}

func TestCapabilitySet_Clone(t *testing.T) {
	caps := NewCapabilitySet([]string{"s1"}, nil, nil, nil, nil, true)
	cloned := caps.Clone()

	// Modify original
	caps.GrantSignal("s2")

	if cloned.CanSignal("s2") {
		t.Fatal("clone should be independent")
	}
	if !cloned.CanSignal("s1") {
		t.Fatal("clone should preserve original values")
	}
}

func TestCapabilitySet_SortedSnapshots(t *testing.T) {
	caps := NewCapabilitySet(
		[]string{"c", "a", "b"},
		nil, nil, nil, nil, false,
	)
	signals := caps.Signals()
	if len(signals) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(signals))
	}
	if signals[0] != "a" || signals[1] != "b" || signals[2] != "c" {
		t.Fatalf("expected sorted, got %v", signals)
	}
}
