package fabriclog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

type harvestCall struct {
	Activity activity.AgentActivity
	Reason   string
}

func newBridgeHarness(t *testing.T) (*FabricLogger, *ForestFabricBridge, *[]harvestCall, *sync.Mutex) {
	t.Helper()
	l, _ := newLogger(t, 256, 1024)
	var mu sync.Mutex
	var calls []harvestCall
	harvest := func(_ context.Context, a activity.AgentActivity, reason string) error {
		mu.Lock()
		calls = append(calls, harvestCall{Activity: a, Reason: reason})
		mu.Unlock()
		return nil
	}
	bridge := NewForestFabricBridge(l, harvest)
	bridge.Wire()
	return l, bridge, &calls, &mu
}

// TestForestFabricBridge_ConsumeForwardsElectedActivity verifies the
// happy path: a publish of a forest-eligible activity followed by a
// read returning it causes the bridge to forward the consume → forest.
func TestForestFabricBridge_ConsumeForwardsElectedActivity(t *testing.T) {
	l, bridge, calls, mu := newBridgeHarness(t)

	// Publish an eligible activity (decision_declared is on the
	// widened allowlist).
	pub := sampleActivity("act-decision")
	pub.Action = activity.ActionDecisionDeclared
	l.RecordPublish(pub)

	// Simulate a FilterActivities read that returned the publish.
	reader := AgentRef{AgentID: "reader-1", AgentType: "inspector"}
	ctx := WithReader(context.Background(), reader)
	readerSeq := l.RecordRead(ctx, "FilterActivities", "WhatAreTheyDoing", &FilterBody{}, []string{"act-decision"}, 1, 100*time.Microsecond, nil)
	l.RecordConsume(ctx, readerSeq, pub)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 harvest call, got %d", len(*calls))
	}
	call := (*calls)[0]
	if call.Activity.ID != "act-decision" {
		t.Errorf("harvested activity ID = %q", call.Activity.ID)
	}
	if call.Reason == "" {
		t.Error("harvest reason should be populated")
	}
	stats := bridge.Stats()
	if stats.ConsumesForwarded != 1 {
		t.Errorf("ConsumesForwarded = %d", stats.ConsumesForwarded)
	}
}

// TestForestFabricBridge_ConsumeSkipsIneligibleActivity verifies the
// bridge filters out non-forest-eligible activities even when they
// get consumed. File reads flowing through a lens should not hit the
// forest via this path.
func TestForestFabricBridge_ConsumeSkipsIneligibleActivity(t *testing.T) {
	l, bridge, calls, mu := newBridgeHarness(t)

	pub := sampleActivity("act-filenoise")
	pub.Action = activity.ActionFileRead // not on the allowlist
	l.RecordPublish(pub)

	readerSeq := l.RecordRead(context.Background(), "FilterActivities", "", &FilterBody{}, []string{"act-filenoise"}, 1, 0, nil)
	l.RecordConsume(context.Background(), readerSeq, pub)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 0 {
		t.Errorf("expected 0 harvest calls for ineligible kind, got %d", len(*calls))
	}
	if bridge.Stats().NotEligible != 1 {
		t.Errorf("NotEligible counter should advance; got %d", bridge.Stats().NotEligible)
	}
}

// TestForestFabricBridge_ResolveForwardsOriginalPublish verifies the
// resolve path: when an activity resolves another, the bridge looks
// up the RESOLVED activity (not the resolver) and forwards it. The
// original decision_declared is forest-eligible, so resolving it
// with decision_promoted should reinforce it via the bridge with a
// "lifecycle resolved" reason.
func TestForestFabricBridge_ResolveForwardsOriginalPublish(t *testing.T) {
	l, bridge, calls, mu := newBridgeHarness(t)

	orig := sampleActivity("act-decision-declared")
	orig.Action = activity.ActionDecisionDeclared
	l.RecordPublish(orig)

	resolver := sampleActivity("act-decision-promoted")
	resolver.Action = activity.ActionDecisionPromoted
	resolvedID := orig.ID
	resolver.Resolves = &resolvedID
	resolver.Timestamp = orig.Timestamp.Add(200 * time.Millisecond)
	l.RecordPublish(resolver)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, c := range *calls {
		if c.Activity.ID == "act-decision-declared" {
			found = true
			if c.Reason == "" {
				t.Error("resolve harvest reason should be populated")
			}
		}
	}
	if !found {
		t.Fatalf("expected resolve harvest to reference original activity; got %+v", *calls)
	}
	if bridge.Stats().ResolvesForwarded < 1 {
		t.Errorf("ResolvesForwarded = %d, want >= 1", bridge.Stats().ResolvesForwarded)
	}
}

// TestForestFabricBridge_CacheMissCounts verifies that a consume
// referencing an activity no longer in the LRU increments the cache
// miss counter rather than silently dropping.
func TestForestFabricBridge_CacheMissCounts(t *testing.T) {
	l, bridge, calls, mu := newBridgeHarness(t)

	// Consume refers to an activity ID that was never published
	// into this logger.
	l.RecordConsume(context.Background(), 99, activity.AgentActivity{
		ID:     "act-orphan",
		Action: activity.ActionDecisionDeclared,
	})

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 0 {
		t.Errorf("cache miss should not produce harvest calls; got %d", len(*calls))
	}
	if bridge.Stats().CacheMisses == 0 {
		t.Error("CacheMisses counter should advance on cache miss")
	}
}
