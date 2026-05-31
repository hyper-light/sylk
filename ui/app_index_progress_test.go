package ui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge"
	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/status"
)

type recordingTeaProgram struct {
	mu   sync.Mutex
	msgs []any
	ch   chan any
}

type noopActivityPublisher struct{}

func (noopActivityPublisher) PublishActivity(*events.ActivityEvent) error { return nil }

func newRecordingTeaProgram() *recordingTeaProgram {
	return &recordingTeaProgram{ch: make(chan any, 16)}
}

type mockBackgroundWaiter struct {
	ready      chan struct{}
	indexed    atomic.Int64
	total      atomic.Int64
	progressMu sync.Mutex
	progressFn func(indexed, total int64)
}

func newMockBackgroundWaiter(indexed, total int64) *mockBackgroundWaiter {
	w := &mockBackgroundWaiter{ready: make(chan struct{})}
	w.indexed.Store(indexed)
	w.total.Store(total)
	return w
}

func (w *mockBackgroundWaiter) Ready() <-chan struct{} { return w.ready }

func (w *mockBackgroundWaiter) Progress() (indexed, total int64) {
	return w.indexed.Load(), w.total.Load()
}

func (w *mockBackgroundWaiter) OnProgress(fn func(indexed, total int64)) {
	w.progressMu.Lock()
	w.progressFn = fn
	w.progressMu.Unlock()
}

func (w *mockBackgroundWaiter) setProgress(indexed, total int64) {
	w.indexed.Store(indexed)
	w.total.Store(total)
	w.progressMu.Lock()
	fn := w.progressFn
	w.progressMu.Unlock()
	if fn != nil {
		fn(indexed, total)
	}
}

func (p *recordingTeaProgram) Send(m any) {
	p.mu.Lock()
	p.msgs = append(p.msgs, m)
	p.mu.Unlock()
	select {
	case p.ch <- m:
	default:
	}
}

func (p *recordingTeaProgram) waitForIndexProgress(t *testing.T, predicate func(msg.IndexProgressMsg) bool) msg.IndexProgressMsg {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-p.ch:
			progress, ok := raw.(msg.IndexProgressMsg)
			if ok && predicate(progress) {
				return progress
			}
		case <-deadline:
			t.Fatal("timed out waiting for index progress message")
		}
	}
}

func (p *recordingTeaProgram) waitForDoneIndexProgress(t *testing.T) msg.IndexProgressMsg {
	return p.waitForIndexProgress(t, func(progress msg.IndexProgressMsg) bool { return progress.Done })
}

func newIndexProgressScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "ui-index-progress-test", nil)
	scope.SetMaxLifetime(time.Minute)
	t.Cleanup(func() {
		_ = scope.Shutdown(100*time.Millisecond, time.Second)
	})
	return scope
}

func TestStartIndexProgressObserver_SendsDoneWhenFullArrivesWithoutBackgroundWaiter(t *testing.T) {
	ks := knowledge.NewKnowledgeStore(nil, nil)
	scope := newIndexProgressScope(t)
	app := &AppModel{deps: Deps{KnowledgeStore: ks, Scope: scope}}
	program := newRecordingTeaProgram()

	app.startIndexProgressObserver(program)
	ks.PromotePartial(knowledgequery.NewBleveSearcher(nil), nil, nil)
	ks.PromoteFull()

	progress := program.waitForDoneIndexProgress(t)
	if !progress.Done {
		t.Fatalf("done progress = %#v, want Done=true", progress)
	}
}

func TestStartIndexProgressObserver_SendsDoneImmediatelyWhenAlreadyFullWithoutBackgroundWaiter(t *testing.T) {
	ks := knowledge.NewKnowledgeStore(nil, nil)
	ks.PromotePartial(knowledgequery.NewBleveSearcher(nil), nil, nil)
	ks.PromoteFull()

	scope := newIndexProgressScope(t)
	app := &AppModel{deps: Deps{KnowledgeStore: ks, Scope: scope}}
	program := newRecordingTeaProgram()

	app.startIndexProgressObserver(program)

	progress := program.waitForDoneIndexProgress(t)
	if !progress.Done {
		t.Fatalf("done progress = %#v, want Done=true", progress)
	}
}

func TestStartIndexProgressObserver_SendsInitialBackgroundIndexPhaseBeforeFirstBatch(t *testing.T) {
	ks := knowledge.NewKnowledgeStore(nil, nil)
	waiter := newMockBackgroundWaiter(0, 32)

	scope := newIndexProgressScope(t)
	app := &AppModel{deps: Deps{KnowledgeStore: ks, Scope: scope}}
	program := newRecordingTeaProgram()

	app.startIndexProgressObserver(program)
	ks.PromotePartial(knowledgequery.NewBleveSearcher(nil), waiter, nil)

	progress := program.waitForIndexProgress(t, func(progress msg.IndexProgressMsg) bool {
		return !progress.Done && progress.Phase == int(status.PhaseIndex)
	})
	if progress.Current != 0 || progress.Total != 32 {
		t.Fatalf("initial index progress = %#v, want current=0 total=32", progress)
	}
}

func TestStartIndexProgressObserver_SendsPipelineProgressAfterObserverAttached(t *testing.T) {
	ks := knowledge.NewKnowledgeStore(nil, nil)

	scope := newIndexProgressScope(t)
	app := &AppModel{deps: Deps{KnowledgeStore: ks, Scope: scope}}
	program := newRecordingTeaProgram()

	app.startIndexProgressObserver(program)
	ks.NotifyProgress("ingest", 4, 6)

	progress := program.waitForIndexProgress(t, func(progress msg.IndexProgressMsg) bool {
		return !progress.Done && progress.Phase == int(status.PhaseEmbed)
	})
	if progress.Current != 1 || progress.Total != 1 {
		t.Fatalf("replayed pipeline progress = %#v, want snapped stage completion 1/1", progress)
	}
}

func TestStartIndexProgressObserver_TracksReplacementBackgroundWaitersAcrossSyncs(t *testing.T) {
	ks := knowledge.NewKnowledgeStore(nil, nil)
	waiter1 := newMockBackgroundWaiter(0, 8)
	waiter2 := newMockBackgroundWaiter(0, 13)

	scope := newIndexProgressScope(t)
	app := &AppModel{deps: Deps{KnowledgeStore: ks, Scope: scope}}
	program := newRecordingTeaProgram()

	app.startIndexProgressObserver(program)

	ks.PromotePartial(knowledgequery.NewBleveSearcher(nil), waiter1, nil)
	progress := program.waitForIndexProgress(t, func(progress msg.IndexProgressMsg) bool {
		return !progress.Done && progress.Phase == int(status.PhaseIndex) && progress.Total == 8
	})
	if progress.Current != 0 {
		t.Fatalf("first waiter initial progress = %#v, want current=0", progress)
	}

	close(waiter1.ready)
	done := program.waitForDoneIndexProgress(t)
	if !done.Done {
		t.Fatalf("first waiter done progress = %#v, want Done=true", done)
	}

	ks.PromotePartial(knowledgequery.NewBleveSearcher(nil), waiter2, nil)
	progress = program.waitForIndexProgress(t, func(progress msg.IndexProgressMsg) bool {
		return !progress.Done && progress.Phase == int(status.PhaseIndex) && progress.Total == 13
	})
	if progress.Current != 0 {
		t.Fatalf("replacement waiter initial progress = %#v, want current=0", progress)
	}

	close(waiter2.ready)
	done = program.waitForDoneIndexProgress(t)
	if !done.Done {
		t.Fatalf("replacement waiter done progress = %#v, want Done=true", done)
	}
}

func TestHandleBridgeReadyStartsKnowledgeBootOnce(t *testing.T) {
	var starts atomic.Int64
	app := &AppModel{deps: Deps{
		ActivityPub: noopActivityPublisher{},
		StartKnowledgeBoot: func() {
			starts.Add(1)
		},
	}}

	if cmd := app.handleBridgeReady(bridgeReadyMsg{}); cmd != nil {
		t.Fatal("handleBridgeReady returned unexpected command")
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("knowledge boot starts = %d, want 1", got)
	}
	if !app.bridgesStarted {
		t.Fatal("bridgesStarted = false, want true")
	}

	if cmd := app.handleBridgeReady(bridgeReadyMsg{}); cmd != nil {
		t.Fatal("second handleBridgeReady returned unexpected command")
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("knowledge boot starts after duplicate ready = %d, want 1", got)
	}
}
