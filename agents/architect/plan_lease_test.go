package architect

import (
	"testing"
	"time"
)

func TestNewPlanLeaseManager_DerivedTimeouts(t *testing.T) {
	llmTimeout := 120 * time.Second
	readyMax := 30 * time.Minute

	lm := NewPlanLeaseManager(llmTimeout, readyMax)

	expectedExecuting := llmTimeout * executingLeaseMultiplier
	if lm.executingLeaseTimeout != expectedExecuting {
		t.Fatalf("expected executing=%v, got %v", expectedExecuting, lm.executingLeaseTimeout)
	}
	if lm.readyLeaseTimeout != readyMax {
		t.Fatalf("expected ready=%v, got %v", readyMax, lm.readyLeaseTimeout)
	}
	expectedTerminal := readyMax * terminalRetentionMultiplier
	if lm.terminalRetention != expectedTerminal {
		t.Fatalf("expected terminal=%v, got %v", expectedTerminal, lm.terminalRetention)
	}
	// Reaper interval = min(executing, ready) / 3. executing=360s, ready=1800s → 360s/3=120s
	expectedReaper := expectedExecuting / reaperIntervalDivisor
	if lm.reaperInterval != expectedReaper {
		t.Fatalf("expected reaper=%v, got %v", expectedReaper, lm.reaperInterval)
	}
}

func TestGrantReadyLease(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	plan := &DesignPlan{ID: "plan-1"}
	lm.GrantReadyLease(plan)

	if plan.LeaseHolder != "" {
		t.Fatalf("expected empty holder, got %q", plan.LeaseHolder)
	}
	if plan.LeaseExpiry.IsZero() {
		t.Fatal("expected non-zero lease expiry")
	}
	if lm.IsLeaseExpired(plan) {
		t.Fatal("expected lease to not be expired immediately after grant")
	}
}

func TestGrantExecutingLease(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	plan := &DesignPlan{ID: "plan-1"}
	lm.GrantExecutingLease(plan, "orchestrator")

	if plan.LeaseHolder != "orchestrator" {
		t.Fatalf("expected orchestrator, got %q", plan.LeaseHolder)
	}
	if lm.IsLeaseExpired(plan) {
		t.Fatal("expected lease to not be expired immediately after grant")
	}
}

func TestRenewLease(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	plan := &DesignPlan{ID: "plan-1"}
	lm.GrantExecutingLease(plan, "orchestrator")
	firstExpiry := plan.LeaseExpiry

	time.Sleep(time.Millisecond)
	lm.RenewLease(plan)

	if !plan.LeaseExpiry.After(firstExpiry) {
		t.Fatal("expected renewed expiry to be after first expiry")
	}
}

func TestIsLeaseExpired_Nil(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	if !lm.IsLeaseExpired(nil) {
		t.Fatal("expected nil plan to be expired")
	}
}

func TestIsLeaseExpired_PastExpiry(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	plan := &DesignPlan{
		ID:          "plan-1",
		LeaseExpiry: time.Now().Add(-time.Hour),
	}
	if !lm.IsLeaseExpired(plan) {
		t.Fatal("expected past-expiry plan to be expired")
	}
}

func TestIsTerminalRetentionExpired(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	plan := &DesignPlan{
		ID:        "plan-1",
		UpdatedAt: time.Now().Add(-lm.terminalRetention - time.Second),
	}
	if !lm.IsTerminalRetentionExpired(plan) {
		t.Fatal("expected terminal retention to be expired")
	}

	fresh := &DesignPlan{ID: "plan-2", UpdatedAt: time.Now()}
	if lm.IsTerminalRetentionExpired(fresh) {
		t.Fatal("expected fresh plan to not be expired")
	}
}

func TestGrantReadyLease_Nil(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	lm.GrantReadyLease(nil) // should not panic
}
