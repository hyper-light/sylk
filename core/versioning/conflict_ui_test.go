package versioning

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAutoResolvePolicy_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		policy   AutoResolvePolicy
		expected string
	}{
		{AutoResolvePolicyNone, "none"},
		{AutoResolvePolicyKeepNewest, "keep_newest"},
		{AutoResolvePolicyKeepOldest, "keep_oldest"},
		{AutoResolvePolicyKeepBoth, "keep_both"},
		{AutoResolvePolicy(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.policy.String(); got != tt.expected {
				t.Errorf("AutoResolvePolicy.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNewGuideConflictResolver(t *testing.T) {
	t.Parallel()

	t.Run("with defaults", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})

		if resolver.timeout != 5*time.Minute {
			t.Errorf("expected default timeout 5m, got %v", resolver.timeout)
		}
	})

	t.Run("with custom timeout", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
			Timeout: 10 * time.Second,
		})

		if resolver.timeout != 10*time.Second {
			t.Errorf("expected timeout 10s, got %v", resolver.timeout)
		}
	})

	t.Run("with policy", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
			Policy: AutoResolvePolicyKeepNewest,
		})

		if resolver.policy != AutoResolvePolicyKeepNewest {
			t.Errorf("expected policy KeepNewest, got %v", resolver.policy)
		}
	})
}

func TestGuideConflictResolver_ResolveConflict(t *testing.T) {
	t.Parallel()

	t.Run("nil conflict returns error", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})

		_, err := resolver.ResolveConflict(context.Background(), nil)

		if !errors.Is(err, ErrInvalidResolution) {
			t.Errorf("expected ErrInvalidResolution, got %v", err)
		}
	})

	t.Run("no sender returns error", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})

		conflict := createTestConflict()
		_, err := resolver.ResolveConflict(context.Background(), conflict)

		if !errors.Is(err, ErrResolverNotReady) {
			t.Errorf("expected ErrResolverNotReady, got %v", err)
		}
	})

	t.Run("auto-resolve with KeepNewest policy", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
			Policy: AutoResolvePolicyKeepNewest,
		})

		conflict := createTestConflictWithTimestamps()
		res, err := resolver.ResolveConflict(context.Background(), conflict)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Label != "Keep newest" {
			t.Errorf("expected 'Keep newest' resolution, got %q", res.Label)
		}
	})
}

func TestGuideConflictResolver_ResolveConflictBatch(t *testing.T) {
	t.Parallel()

	t.Run("empty batch returns error", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})

		_, err := resolver.ResolveConflictBatch(context.Background(), nil)

		if !errors.Is(err, ErrBatchEmpty) {
			t.Errorf("expected ErrBatchEmpty, got %v", err)
		}
	})

	t.Run("batch with auto-resolve", func(t *testing.T) {
		resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
			Policy: AutoResolvePolicyKeepNewest,
		})

		conflicts := []*Conflict{
			createTestConflictWithTimestamps(),
			createTestConflictWithTimestamps(),
		}

		resolutions, err := resolver.ResolveConflictBatch(context.Background(), conflicts)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resolutions) != 2 {
			t.Errorf("expected 2 resolutions, got %d", len(resolutions))
		}
	})
}

func TestGuideConflictResolver_AutoResolve(t *testing.T) {
	t.Parallel()

	resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})

	t.Run("nil conflict returns false", func(t *testing.T) {
		_, ok := resolver.AutoResolve(nil, AutoResolvePolicyKeepNewest)
		if ok {
			t.Error("expected false for nil conflict")
		}
	})

	t.Run("PolicyNone returns false", func(t *testing.T) {
		conflict := createTestConflictWithTimestamps()
		_, ok := resolver.AutoResolve(conflict, AutoResolvePolicyNone)
		if ok {
			t.Error("expected false for PolicyNone")
		}
	})

	t.Run("KeepNewest selects newer operation", func(t *testing.T) {
		conflict := createTestConflictWithTimestamps()
		res, ok := resolver.AutoResolve(conflict, AutoResolvePolicyKeepNewest)

		if !ok {
			t.Fatal("expected auto-resolve to succeed")
		}
		if res.ResultOp != conflict.Op2 {
			t.Error("expected newer operation (Op2) to be selected")
		}
	})

	t.Run("KeepOldest selects older operation", func(t *testing.T) {
		conflict := createTestConflictWithTimestamps()
		res, ok := resolver.AutoResolve(conflict, AutoResolvePolicyKeepOldest)

		if !ok {
			t.Fatal("expected auto-resolve to succeed")
		}
		if res.ResultOp != conflict.Op1 {
			t.Error("expected older operation (Op1) to be selected")
		}
	})

	t.Run("KeepBoth merges insert operations", func(t *testing.T) {
		conflict := createInsertInsertConflict()
		res, ok := resolver.AutoResolve(conflict, AutoResolvePolicyKeepBoth)

		if !ok {
			t.Fatal("expected auto-resolve to succeed")
		}
		expected := append(conflict.Op1.Content, conflict.Op2.Content...)
		if string(res.ResultOp.Content) != string(expected) {
			t.Errorf("expected merged content %q, got %q", expected, res.ResultOp.Content)
		}
	})

	t.Run("KeepBoth fails for delete-edit conflicts", func(t *testing.T) {
		conflict := &Conflict{
			Type: ConflictTypeDeleteEdit,
			Op1:  &Operation{Type: OpDelete},
			Op2:  &Operation{Type: OpInsert},
		}

		_, ok := resolver.AutoResolve(conflict, AutoResolvePolicyKeepBoth)
		if ok {
			t.Error("expected auto-resolve to fail for delete-edit")
		}
	})
}

func TestGuideConflictResolver_CustomMerge(t *testing.T) {
	t.Parallel()

	sender := NewMockConflictMessageSender()
	resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
		Sender:  sender,
		Timeout: 100 * time.Millisecond,
	})

	conflict := createTestConflict()
	customContent := []byte("custom merged content")

	sender.SetResponse(conflict.Op1.ID.String(), &ConflictResponse{
		CustomMerge: customContent,
	})

	res, err := resolver.ResolveConflict(context.Background(), conflict)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Label != "Custom merge" {
		t.Errorf("expected 'Custom merge' label, got %q", res.Label)
	}
	if string(res.ResultOp.Content) != string(customContent) {
		t.Errorf("expected custom content, got %q", res.ResultOp.Content)
	}
}

func TestGuideConflictResolver_Timeout(t *testing.T) {
	t.Parallel()

	sender := NewMockConflictMessageSender()
	sender.SetReceiveError(context.DeadlineExceeded)

	resolver := NewGuideConflictResolver(GuideConflictResolverConfig{
		Sender:  sender,
		Timeout: 10 * time.Millisecond,
	})

	conflict := createTestConflict()
	_, err := resolver.ResolveConflict(context.Background(), conflict)

	if !errors.Is(err, ErrResolutionTimeout) {
		t.Errorf("expected ErrResolutionTimeout, got %v", err)
	}
}

func TestGuideConflictResolver_SetTimeout(t *testing.T) {
	t.Parallel()

	resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})
	resolver.SetTimeout(30 * time.Second)

	if resolver.timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", resolver.timeout)
	}
}

func TestGuideConflictResolver_SetPolicy(t *testing.T) {
	t.Parallel()

	resolver := NewGuideConflictResolver(GuideConflictResolverConfig{})
	resolver.SetPolicy(AutoResolvePolicyKeepBoth)

	if resolver.policy != AutoResolvePolicyKeepBoth {
		t.Errorf("expected policy KeepBoth, got %v", resolver.policy)
	}
}

func TestNoOpConflictResolver(t *testing.T) {
	t.Parallel()

	resolver := NewNoOpConflictResolver()

	t.Run("ResolveConflict returns first option", func(t *testing.T) {
		conflict := createTestConflict()
		res, err := resolver.ResolveConflict(context.Background(), conflict)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Label != conflict.Resolutions[0].Label {
			t.Errorf("expected first resolution, got %q", res.Label)
		}
	})

	t.Run("ResolveConflict nil conflict returns error", func(t *testing.T) {
		_, err := resolver.ResolveConflict(context.Background(), nil)
		if !errors.Is(err, ErrInvalidResolution) {
			t.Errorf("expected ErrInvalidResolution, got %v", err)
		}
	})

	t.Run("ResolveConflictBatch empty returns error", func(t *testing.T) {
		_, err := resolver.ResolveConflictBatch(context.Background(), nil)
		if !errors.Is(err, ErrBatchEmpty) {
			t.Errorf("expected ErrBatchEmpty, got %v", err)
		}
	})

	t.Run("AutoResolve returns false", func(t *testing.T) {
		conflict := createTestConflict()
		_, ok := resolver.AutoResolve(conflict, AutoResolvePolicyKeepNewest)
		if ok {
			t.Error("expected AutoResolve to return false")
		}
	})
}

func TestMockConflictMessageSender(t *testing.T) {
	t.Parallel()

	t.Run("SendConflictMessage records messages", func(t *testing.T) {
		sender := NewMockConflictMessageSender()
		msg := &ConflictMessage{FilePath: "/test/file.go"}

		err := sender.SendConflictMessage(context.Background(), msg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sender.Messages()) != 1 {
			t.Errorf("expected 1 message, got %d", len(sender.Messages()))
		}
	})

	t.Run("SendConflictMessage returns configured error", func(t *testing.T) {
		sender := NewMockConflictMessageSender()
		expectedErr := errors.New("send failed")
		sender.SetSendError(expectedErr)

		err := sender.SendConflictMessage(context.Background(), &ConflictMessage{})

		if !errors.Is(err, expectedErr) {
			t.Errorf("expected configured error, got %v", err)
		}
	})

	t.Run("ReceiveResponse returns configured response", func(t *testing.T) {
		sender := NewMockConflictMessageSender()
		resp := &ConflictResponse{ChosenOptionID: "option_1"}
		sender.SetResponse("conflict-123", resp)

		got, err := sender.ReceiveResponse(context.Background(), "conflict-123")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ChosenOptionID != resp.ChosenOptionID {
			t.Errorf("expected option_1, got %s", got.ChosenOptionID)
		}
	})

	t.Run("ReceiveResponse returns error for unknown conflict", func(t *testing.T) {
		sender := NewMockConflictMessageSender()

		_, err := sender.ReceiveResponse(context.Background(), "unknown")

		if !errors.Is(err, ErrNoResolutionChosen) {
			t.Errorf("expected ErrNoResolutionChosen, got %v", err)
		}
	})
}

func createTestConflict() *Conflict {
	op1 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 10),
		OpReplace,
		[]byte("op1 content"),
		[]byte("old"),
		"pipeline1",
		"session1",
		"agent1",
		NewVectorClock(),
	)
	op2 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 10),
		OpReplace,
		[]byte("op2 content"),
		[]byte("old"),
		"pipeline2",
		"session2",
		"agent2",
		NewVectorClock(),
	)

	return &Conflict{
		Type:        ConflictTypeOverlappingEdit,
		Op1:         &op1,
		Op2:         &op2,
		Description: "Test conflict",
		Resolutions: []Resolution{
			{Label: "Keep op1", ResultOp: &op1},
			{Label: "Keep op2", ResultOp: &op2},
		},
	}
}

func createTestConflictWithTimestamps() *Conflict {
	now := time.Now()

	op1 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 10),
		OpReplace,
		[]byte("op1 content"),
		[]byte("old"),
		"pipeline1",
		"session1",
		"agent1",
		NewVectorClock(),
	)
	op1.Timestamp = now.Add(-time.Hour)

	op2 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 10),
		OpReplace,
		[]byte("op2 content"),
		[]byte("old"),
		"pipeline2",
		"session2",
		"agent2",
		NewVectorClock(),
	)
	op2.Timestamp = now

	return &Conflict{
		Type:        ConflictTypeOverlappingEdit,
		Op1:         &op1,
		Op2:         &op2,
		Description: "Test conflict with timestamps",
		Resolutions: []Resolution{
			{Label: "Keep op1", ResultOp: &op1},
			{Label: "Keep op2", ResultOp: &op2},
		},
	}
}

func createInsertInsertConflict() *Conflict {
	now := time.Now()

	op1 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 0),
		OpInsert,
		[]byte("first "),
		nil,
		"pipeline1",
		"session1",
		"agent1",
		NewVectorClock(),
	)
	op1.Timestamp = now.Add(-time.Hour)

	op2 := NewOperation(
		VersionID{},
		"/test/file.go",
		NewOffsetTarget(0, 0),
		OpInsert,
		[]byte("second"),
		nil,
		"pipeline2",
		"session2",
		"agent2",
		NewVectorClock(),
	)
	op2.Timestamp = now

	return &Conflict{
		Type:        ConflictTypeOverlappingEdit,
		Op1:         &op1,
		Op2:         &op2,
		Description: "Insert-insert conflict",
		Resolutions: []Resolution{
			{Label: "Keep op1", ResultOp: &op1},
			{Label: "Keep op2", ResultOp: &op2},
			{Label: "Keep both", ResultOp: nil},
		},
	}
}
