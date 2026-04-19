package bridge

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/llm/accounting"
	"github.com/adalundhe/sylk/ui/msg"
)

type capturingProgram struct {
	mu   sync.Mutex
	msgs []any
}

func (p *capturingProgram) Send(m any) {
	p.mu.Lock()
	p.msgs = append(p.msgs, m)
	p.mu.Unlock()
}

func (p *capturingProgram) Messages() []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]any, len(p.msgs))
	copy(out, p.msgs)
	return out
}

func testBridgeIdent(uid string) *identity.AgentIdentity {
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:       identity.UID(uid),
		Namespace: "sess-bridge-test",
		Pod:       identity.PodRef{ID: "guide", Type: identity.PodTypeDaemon},
		Name:      identity.Name("guide"),
		Kind:      identity.AgentTypeGuide,
		Category:  identity.CategoryStandalone,
		Model:     "haiku-4.5-200k",
	})
}

func testBridgeTask(corr string) *identity.TaskRef {
	return identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:         identity.TaskUID("task-" + corr),
		Correlation: identity.CorrelationID(corr),
		Namespace:   "sess-bridge-test",
	})
}

func TestAccountantBridge_ForwardsDeltaAsTokenDeltaMsg(t *testing.T) {
	acc, err := accounting.New(accounting.Config{Namespace: "sess-bridge-test"})
	if err != nil {
		t.Fatalf("new accountant: %v", err)
	}
	t.Cleanup(func() { _ = acc.Close() })

	b := NewAccountantBridge("test", acc)
	prog := &capturingProgram{}
	if err := b.Start(prog); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(b.Stop)

	id := testBridgeIdent("uid-1")
	task := testBridgeTask("corr-1")
	if err := acc.Record(accounting.Event{
		Identity: id,
		Task:     task,
		Usage:    accounting.UsageSample{InputTokens: 100, OutputTokens: 50},
		Elapsed:  10 * time.Millisecond,
		Phase:    accounting.PhaseResponse,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range prog.Messages() {
			tm, ok := m.(msg.TokenDeltaMsg)
			if !ok {
				continue
			}
			if tm.Identity == nil || tm.Task == nil {
				t.Fatalf("expected typed identity+task on delta, got %+v", tm)
			}
			if tm.Identity.UID() != id.UID() {
				t.Fatalf("uid mismatch: %s vs %s", tm.Identity.UID(), id.UID())
			}
			if tm.Task.UID() != task.UID() {
				t.Fatalf("task uid mismatch: %s vs %s", tm.Task.UID(), task.UID())
			}
			if tm.InputTokens != 100 || tm.OutputTokens != 50 {
				t.Fatalf("tokens: in=%d out=%d", tm.InputTokens, tm.OutputTokens)
			}
			if tm.ElapsedMS != 10 {
				t.Fatalf("elapsed: %d", tm.ElapsedMS)
			}
			if !tm.Billable {
				t.Fatal("expected billable for standalone agent")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no TokenDeltaMsg received within deadline")
}

func TestAccountantBridge_PublishesSnapshot(t *testing.T) {
	acc, _ := accounting.New(accounting.Config{Namespace: "sess-bridge-test"})
	t.Cleanup(func() { _ = acc.Close() })

	b := NewAccountantBridge("test", acc)
	b.snapshotMS = 30 * time.Millisecond
	prog := &capturingProgram{}
	if err := b.Start(prog); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(b.Stop)

	// Wait for at least one snapshot tick.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range prog.Messages() {
			if _, ok := m.(msg.AccountingSnapshotMsg); ok {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no AccountingSnapshotMsg received within deadline")
}

func TestAccountantBridge_NilAccountantIsNoop(t *testing.T) {
	b := NewAccountantBridge("test", nil)
	prog := &capturingProgram{}
	if err := b.Start(prog); err != nil {
		t.Fatalf("start: %v", err)
	}
	b.Stop()
	if got := prog.Messages(); len(got) != 0 {
		t.Fatalf("expected no messages, got %d", len(got))
	}
}

func TestAccountantBridge_BillableForNamespace(t *testing.T) {
	acc, _ := accounting.New(accounting.Config{Namespace: "sess-bridge-test"})
	t.Cleanup(func() { _ = acc.Close() })
	b := NewAccountantBridge("test", acc)

	id := testBridgeIdent("uid-2")
	task := testBridgeTask("corr-2")
	_ = acc.Record(accounting.Event{
		Identity: id,
		Task:     task,
		Usage:    accounting.UsageSample{InputTokens: 333, OutputTokens: 222},
		Phase:    accounting.PhaseResponse,
	})
	got := b.BillableForNamespace("sess-bridge-test")
	if got.Input != 333 || got.Output != 222 {
		t.Fatalf("billable: in=%d out=%d", got.Input, got.Output)
	}
	if zero := b.BillableForNamespace("other"); zero.Input != 0 || zero.Output != 0 {
		t.Fatalf("wrong namespace returned data: %+v", zero)
	}
}
