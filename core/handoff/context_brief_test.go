package handoff

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

// --- test helpers ---

// stubBriefProvider implements providers.Provider for testing.
type stubBriefProvider struct {
	response string
	err      error
}

func (s *stubBriefProvider) Name() string                                            { return "stub" }
func (s *stubBriefProvider) SupportedModels() []providers.ModelInfo                  { return nil }
func (s *stubBriefProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	return nil, nil
}
func (s *stubBriefProvider) CountTokens(_ []providers.Message) (int, error) { return 0, nil }
func (s *stubBriefProvider) MaxContextTokens(_ string) int                 { return 200000 }
func (s *stubBriefProvider) HealthCheck(_ context.Context) error           { return nil }
func (s *stubBriefProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &providers.Response{Content: s.response}, nil
}

// stubBriefSource implements BriefSource for testing.
type stubBriefSource struct {
	brief *ContextBrief
	err   error
	calls int
}

func (s *stubBriefSource) RequestBrief(_ context.Context, _ string, contextSize, turnNumber int) (*ContextBrief, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	brief := *s.brief
	brief.ContextSize = contextSize
	brief.TurnNumber = turnNumber
	return &brief, nil
}

func TestBriefGeneratorLatestNil(t *testing.T) {
	bg := NewBriefGenerator(&stubBriefSource{brief: &ContextBrief{}})
	if bg.Latest() != nil {
		t.Fatal("expected nil before any generation")
	}
}

func TestBriefGeneratorGenerate(t *testing.T) {
	source := &stubBriefSource{
		brief: &ContextBrief{
			TaskSummary:  "testing handoff",
			KeyDecisions: "chose approach A",
			ActiveState:  "file.go open",
			NextSteps:    "run tests",
			Blockers:     "none",
			GeneratedAt:  time.Now(),
		},
	}
	bg := NewBriefGenerator(source)

	msgs := []Message{
		{Role: "user", Content: "please fix bug", Timestamp: time.Now()},
		{Role: "assistant", Content: "fixing now", Timestamp: time.Now()},
	}

	bg.Generate(context.Background(), msgs, "engineer", 5000, 3)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bg.Latest() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	brief := bg.Latest()
	if brief == nil {
		t.Fatal("expected non-nil brief after generation")
	}
	if brief.TaskSummary != "testing handoff" {
		t.Fatalf("unexpected task_summary: %s", brief.TaskSummary)
	}
	if brief.ContextSize != 5000 {
		t.Fatalf("unexpected context_size: %d", brief.ContextSize)
	}
	if brief.TurnNumber != 3 {
		t.Fatalf("unexpected turn_number: %d", brief.TurnNumber)
	}
}

func TestBriefGeneratorSetOnPreparedContext(t *testing.T) {
	source := &stubBriefSource{
		brief: &ContextBrief{
			TaskSummary:  "test",
			KeyDecisions: "d",
			ActiveState:  "s",
			NextSteps:    "n",
			Blockers:     "b",
			GeneratedAt:  time.Now(),
		},
	}
	bg := NewBriefGenerator(source)

	bg.Generate(context.Background(), []Message{{Role: "user", Content: "hi"}}, "designer", 1000, 1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bg.Latest() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	pc := NewPreparedContextDefault()
	bg.SetOnPreparedContext(pc)

	meta := pc.Metadata()
	raw, ok := meta["context_brief"]
	if !ok {
		t.Fatal("expected context_brief in metadata")
	}

	var decoded ContextBrief
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("failed to decode stored brief: %v", err)
	}
	if decoded.TaskSummary != "test" {
		t.Fatalf("unexpected decoded task_summary: %s", decoded.TaskSummary)
	}
}

func TestBriefGeneratorConcurrentGuard(t *testing.T) {
	source := &stubBriefSource{
		brief: &ContextBrief{TaskSummary: "slow"},
	}
	bg := NewBriefGenerator(source)

	// Manually set generating to simulate in-flight generation.
	bg.generating.Store(true)

	// This should be a no-op.
	bg.Generate(context.Background(), []Message{{Role: "user", Content: "hi"}}, "test", 100, 1)

	// Release.
	bg.generating.Store(false)

	// After reset, generation should work.
	bg.Generate(context.Background(), []Message{{Role: "user", Content: "hi"}}, "test", 100, 1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bg.Latest() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if bg.Latest() == nil {
		t.Fatal("expected brief after guard was released")
	}
}

func TestSnapshotMessages(t *testing.T) {
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: "msg"}
	}

	snapped := snapshotMessages(msgs, 10)
	if len(snapped) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(snapped))
	}

	small := []Message{{Role: "a"}, {Role: "b"}}
	snapped = snapshotMessages(small, 10)
	if len(snapped) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snapped))
	}
}

func TestDirectBriefSource(t *testing.T) {
	briefJSON := `{"task_summary":"direct test","key_decisions":"k","active_state":"s","next_steps":"n","blockers":"b"}`
	provider := &stubBriefProvider{response: briefJSON}
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}

	source := NewDirectBriefSource(provider, "test-model", func() []Message { return msgs })

	brief, err := source.RequestBrief(context.Background(), "engineer", 5000, 3)
	if err != nil {
		t.Fatalf("RequestBrief: %v", err)
	}
	if brief.TaskSummary != "direct test" {
		t.Fatalf("unexpected task_summary: %s", brief.TaskSummary)
	}
	if brief.ContextSize != 5000 {
		t.Fatalf("unexpected context_size: %d", brief.ContextSize)
	}
}
