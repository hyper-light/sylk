package handoff

import (
	"testing"
	"time"
)

// TestPreparedContext_TrimToTokenBudget_DropsOldestUntilFits verifies the
// HAND-09 core invariant: TrimToTokenBudget pops the oldest messages in
// order until the total token count fits the budget.
func TestPreparedContext_TrimToTokenBudget_DropsOldestUntilFits(t *testing.T) {
	cfg := DefaultPreparedContextConfig()
	cfg.MaxRecentMessages = 20
	pc := NewPreparedContext(cfg)

	// Seed 10 messages × 50 tokens each = 500 tokens of messages.
	for i := 0; i < 10; i++ {
		pc.AddMessage(Message{
			Role:       "user",
			Content:    "msg",
			TokenCount: 50,
			Timestamp:  time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	before := pc.TokenCount()
	if before < 500 {
		t.Fatalf("precondition: expected tokenCount >= 500 (got %d)", before)
	}

	// Trim to a 250-token budget. Should drop enough oldest messages to
	// bring total below 250.
	dropped := pc.TrimToTokenBudget(250)
	if dropped == 0 {
		t.Fatal("TrimToTokenBudget should have dropped messages")
	}
	if got := pc.TokenCount(); got > 250 {
		t.Errorf("post-trim tokenCount = %d, want ≤ 250", got)
	}
}

// TestPreparedContext_TrimToTokenBudget_PreservesOrdering verifies that
// after trimming, the remaining messages are the most-recent ones — the
// trim drops from the head (oldest), not the tail.
func TestPreparedContext_TrimToTokenBudget_PreservesOrdering(t *testing.T) {
	cfg := DefaultPreparedContextConfig()
	cfg.MaxRecentMessages = 10
	pc := NewPreparedContext(cfg)

	for i := 0; i < 5; i++ {
		pc.AddMessage(Message{
			Role:       "user",
			Content:    "content",
			TokenCount: 100,
			Timestamp:  time.Unix(int64(i), 0),
		})
	}

	pc.TrimToTokenBudget(150)

	remaining := pc.RecentMessages()
	if len(remaining) == 0 {
		t.Fatal("no messages after trim")
	}
	// The last (newest) message must still be present — its timestamp
	// matches the most recent seed.
	last := remaining[len(remaining)-1]
	if last.Timestamp.Unix() != 4 {
		t.Errorf("last remaining message timestamp = %d, want 4 (most recent preserved)", last.Timestamp.Unix())
	}
}

// TestPreparedContext_TrimToTokenBudget_NoopWhenAlreadyFits verifies a
// trim call against an under-budget context is a no-op; nothing is
// dropped and the version counter does not advance spuriously.
func TestPreparedContext_TrimToTokenBudget_NoopWhenAlreadyFits(t *testing.T) {
	cfg := DefaultPreparedContextConfig()
	cfg.MaxRecentMessages = 5
	pc := NewPreparedContext(cfg)
	pc.AddMessage(Message{TokenCount: 20})

	versionBefore := pc.Version()
	dropped := pc.TrimToTokenBudget(1000)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if pc.Version() != versionBefore {
		t.Errorf("Version advanced from %d to %d on no-op trim", versionBefore, pc.Version())
	}
}

// TestPreparedContext_TrimToTokenBudget_ZeroBudgetIsNoop verifies that a
// non-positive budget is rejected as a no-op (silent drop would be
// catastrophic — it would empty the context entirely on a caller bug).
func TestPreparedContext_TrimToTokenBudget_ZeroBudgetIsNoop(t *testing.T) {
	cfg := DefaultPreparedContextConfig()
	cfg.MaxRecentMessages = 5
	pc := NewPreparedContext(cfg)
	pc.AddMessage(Message{TokenCount: 100})

	for _, budget := range []int{0, -1, -1000} {
		dropped := pc.TrimToTokenBudget(budget)
		if dropped != 0 {
			t.Errorf("budget=%d: dropped=%d, want 0 (non-positive budget must be no-op)", budget, dropped)
		}
	}
}

// TestHandoffManager_ApplyOptimalSizeTrim_UsesLearnerPosterior is the
// HAND-09 integration regression: the manager consults the profile
// learner's OptimalPreparedSize posterior at handoff time and trims the
// prepared context to it.
func TestHandoffManager_ApplyOptimalSizeTrim_UsesLearnerPosterior(t *testing.T) {
	cfg := DefaultHandoffManagerConfig()
	cfg.AgentID = "engineer-1"
	cfg.ModelName = "opus-4.7"
	learner := NewProfileLearner(nil, nil)
	manager := NewHandoffManager(cfg, nil, nil, learner)

	// Record a successful handoff observation so the learner's
	// OptimalPreparedSize posterior reflects a ~150-token size.
	for i := 0; i < 5; i++ {
		learner.RecordObservation("engineer-1", "opus-4.7", &HandoffObservation{
			ContextSize:   150,
			TurnNumber:    i + 1,
			QualityScore:  0.9,
			WasSuccessful: true,
			Timestamp:     time.Now(),
		})
	}

	pc := NewPreparedContextDefault()
	for i := 0; i < 10; i++ {
		pc.AddMessage(Message{
			Role:       "user",
			Content:    "content",
			TokenCount: 80,
			Timestamp:  time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	beforeCount := pc.TokenCount()
	manager.applyOptimalSizeTrim(pc)
	afterCount := pc.TokenCount()

	if afterCount >= beforeCount {
		t.Errorf("trim did not shrink context: before=%d after=%d (learner's posterior should be well below %d)",
			beforeCount, afterCount, beforeCount)
	}
}

// TestHandoffManager_ApplyOptimalSizeTrim_EmptyAgentIsNoop verifies the
// manager does nothing when AgentID/ModelName are unset — no attribution
// key for the learner means we cannot know which posterior to apply, so
// the handoff proceeds with the full context rather than guessing.
func TestHandoffManager_ApplyOptimalSizeTrim_EmptyAgentIsNoop(t *testing.T) {
	cfg := DefaultHandoffManagerConfig()
	cfg.AgentID = "" // no attribution
	cfg.ModelName = ""
	manager := NewHandoffManager(cfg, nil, nil, NewProfileLearner(nil, nil))
	pc := NewPreparedContextDefault()
	pc.AddMessage(Message{TokenCount: 5000})

	before := pc.TokenCount()
	manager.applyOptimalSizeTrim(pc) // must not panic, must not trim
	if pc.TokenCount() < before {
		t.Error("trim should be a no-op without agent attribution")
	}
}

// TestHandoffManager_ApplyOptimalSizeTrim_NilPreparedCtxIsNoop locks the
// defensive-nil contract so an over-zealous caller cannot crash the
// handoff path.
func TestHandoffManager_ApplyOptimalSizeTrim_NilPreparedCtxIsNoop(t *testing.T) {
	cfg := DefaultHandoffManagerConfig()
	cfg.AgentID = "a"
	cfg.ModelName = "m"
	manager := NewHandoffManager(cfg, nil, nil, NewProfileLearner(nil, nil))
	manager.applyOptimalSizeTrim(nil) // must not panic
}
