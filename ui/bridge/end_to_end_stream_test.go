package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// streamDelivery is a test-only harness that subscribes to the bus
// asynchronously (mirroring production) and exposes a per-correlation
// ack channel so the test can publish one message, wait for its
// handler to complete, then publish the next. This simulates the
// natural inter-chunk latency of real LLM streaming without forcing
// the bus into synchronous delivery — which would be unrepresentative
// of the async path production actually runs.
//
// Without this pacing the bus's async subscribers spawn handler
// goroutines faster than they can run, so a tight publish loop in
// a test can see chunk handlers race even though the enqueue order
// is strict FIFO. Production doesn't hit this because real chunks
// arrive with provider-side latency between them.
type streamDelivery struct {
	mu      sync.Mutex
	cond    *sync.Cond
	counts  map[string]int
	model   *chat.Model
	session string
}

func newStreamDelivery(session string, m *chat.Model) *streamDelivery {
	d := &streamDelivery{
		counts:  map[string]int{},
		model:   m,
		session: session,
	}
	d.cond = sync.NewCond(&d.mu)
	return d
}

func (d *streamDelivery) handle(t *testing.T, msg *guide.Message) error {
	t.Helper()
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return nil
	}
	// Only the three event types the chat model actually consumes
	// (Start / Data / Complete) count toward publish-paced
	// synchronization. PublishStreamStart / Complete also emit
	// accompanying agent_state events onto the same topic; those
	// flow through this handler but must not advance the counter or
	// the test races ahead of its own publishes.
	switch stream.Event.Type {
	case guide.StreamEventStart, guide.StreamEventData, guide.StreamEventComplete:
	default:
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.model = deliverStreamToModel(t, d.model, d.session, stream)
	d.counts[stream.CorrelationID]++
	d.cond.Broadcast()
	return nil
}

// waitForEventCount blocks until the given correlation has had `want`
// events fully delivered (start + N chunks + complete, etc). Fails
// the test on timeout so async races surface as clear failures
// rather than silent staleness.
func (d *streamDelivery) waitForEventCount(t *testing.T, correlationID string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.counts[correlationID] < want {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for %d events on %q, got %d", want, correlationID, d.counts[correlationID])
		}
		done := make(chan struct{})
		go func() {
			time.Sleep(remaining)
			d.cond.Broadcast()
			close(done)
		}()
		d.cond.Wait()
	}
}

func (d *streamDelivery) snapshotModel() *chat.Model {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.model
}

// deliverStreamToModel converts a StreamResponse to the tea.Msg the
// chat panel expects and feeds it to the model. Mirrors the subset
// of GuideBridge.dispatchStream (guide.go:640) needed for Start/Data/
// Complete end-to-end coverage without pulling in the stateful
// watcher/suppression layer that belongs to the real bridge.
func deliverStreamToModel(t *testing.T, m *chat.Model, sessionID string, stream *guide.StreamResponse) *chat.Model {
	t.Helper()
	if stream == nil || stream.Event == nil {
		return m
	}
	cid := stream.CorrelationID
	var teaMsg tea.Msg
	switch stream.Event.Type {
	case guide.StreamEventStart:
		teaMsg = parseStreamStartMsg(sessionID, cid, stream)
	case guide.StreamEventData:
		teaMsg = msg.StreamChunkMsg{SessionID: sessionID, CorrelationID: cid, Text: stream.Event.Text}
	case guide.StreamEventComplete:
		teaMsg = parseStreamCompleteMsg(sessionID, cid, stream)
	default:
		return m
	}
	next, _ := m.Update(teaMsg)
	return next.(*chat.Model)
}

// TestEndToEnd_SingleStream_ChunksLandInView round-trips a full
// PublishStreamStart → N × PublishStreamChunk → PublishStreamComplete
// sequence through a real async ChannelBus, into the bridge conversion
// helpers, into a chat.Model, and verifies the assembled text
// appears in View() output.
func TestEndToEnd_SingleStream_ChunksLandInView(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian-1")
	m := chat.New(theme.DefaultDark(), 16)
	m.SetSize(160, 40)
	delivery := newStreamDelivery("session-e2e", m)

	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		return delivery.handle(t, msg)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := agentshared.WithStreamContext(context.Background(), "corr-e2e", "tui")
	ctx = agentshared.WithStreamContextMetadata(ctx, map[string]any{
		"agent_type": "librarian",
		"agent_name": "Librarian",
	})
	ctx, acc := agentshared.WithUsageAccumulator(ctx)

	expected := 0
	publishAndWait := func(pub func() error) {
		t.Helper()
		if err := pub(); err != nil {
			t.Fatalf("publish: %v", err)
		}
		expected++
		delivery.waitForEventCount(t, "corr-e2e", expected, 2*time.Second)
	}

	publishAndWait(func() error { return agentshared.PublishStreamStart(bus, channels, ctx, "librarian-1") })
	chunks := []string{"Reading ", "the index ", "for matches."}
	for _, text := range chunks {
		text := text
		publishAndWait(func() error { return agentshared.PublishStreamChunk(bus, channels, ctx, "librarian-1", text) })
	}
	publishAndWait(func() error { return agentshared.PublishStreamComplete(bus, channels, ctx, "librarian-1", "", acc.Total()) })

	view := delivery.snapshotModel().View()
	wantText := strings.Join(chunks, "")
	if !strings.Contains(view, wantText) {
		t.Fatalf("View missing accumulated text\nwant substring: %q\nview:\n%s", wantText, view)
	}
}

// TestEndToEnd_ConcurrentReplicas_RenderDistinctRowsAndOwnContent
// round-trips N parallel replicas through the full bus → bridge →
// chat pipeline with per-chunk pacing so async handlers land
// deterministically. Asserts each replica's text lands in its own
// row in the rendered View() output and no replica's text appears
// in a sibling's row.
func TestEndToEnd_ConcurrentReplicas_RenderDistinctRowsAndOwnContent(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian-parent")

	type replica struct {
		correlationID string
		runtimeID     string
		chunks        []string
	}
	replicas := []replica{
		{"corr-rep-1", "librarian#replica-corr-rep-1", []string{"alpha-1 ", "alpha-2 ", "alpha-3"}},
		{"corr-rep-2", "librarian#replica-corr-rep-2", []string{"beta-1 ", "beta-2 ", "beta-3"}},
		{"corr-rep-3", "librarian#replica-corr-rep-3", []string{"gamma-1 ", "gamma-2 ", "gamma-3"}},
	}

	m := chat.New(theme.DefaultDark(), 4) // deliberately < N to exercise Grow()
	m.SetSize(200, 40)
	delivery := newStreamDelivery("session-e2e", m)

	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		return delivery.handle(t, msg)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	expected := map[string]int{}
	publishAndWait := func(cid string, pub func() error) {
		t.Helper()
		if err := pub(); err != nil {
			t.Fatalf("publish %q: %v", cid, err)
		}
		expected[cid]++
		delivery.waitForEventCount(t, cid, expected[cid], 3*time.Second)
	}

	replicaCtxs := make([]context.Context, len(replicas))
	replicaAccs := make([]*agentshared.UsageAccumulator, len(replicas))
	for i, r := range replicas {
		ctx := agentshared.WithStreamContext(context.Background(), r.correlationID, "tui")
		ctx = agentshared.WithStreamContextMetadata(ctx, map[string]any{
			"runtime_agent_id":   r.runtimeID,
			"handoff_replica_id": r.runtimeID,
			"agent_type":         "librarian",
			"agent_name":         "Librarian",
		})
		ctx, acc := agentshared.WithUsageAccumulator(ctx)
		replicaCtxs[i] = ctx
		replicaAccs[i] = acc
		publishAndWait(r.correlationID, func() error {
			return agentshared.PublishStreamStart(bus, channels, ctx, "librarian-parent")
		})
	}

	// Interleave chunk delivery across replicas — each chunk paces
	// against its own correlation's ack so we know it landed before
	// the next one fires.
	maxChunks := 0
	for _, r := range replicas {
		if len(r.chunks) > maxChunks {
			maxChunks = len(r.chunks)
		}
	}
	for i := range maxChunks {
		for ri, r := range replicas {
			if i >= len(r.chunks) {
				continue
			}
			ctx := replicaCtxs[ri]
			text := r.chunks[i]
			publishAndWait(r.correlationID, func() error {
				return agentshared.PublishStreamChunk(bus, channels, ctx, "librarian-parent", text)
			})
		}
	}

	for i, r := range replicas {
		ctx := replicaCtxs[i]
		acc := replicaAccs[i]
		publishAndWait(r.correlationID, func() error {
			return agentshared.PublishStreamComplete(bus, channels, ctx, "librarian-parent", "", acc.Total())
		})
	}

	m = delivery.snapshotModel()
	view := m.View()

	// Each replica's concatenated text must appear in the rendered
	// output — proves the bridge converted its chunks and the chat
	// model routed them to the right row.
	for _, r := range replicas {
		want := strings.Join(r.chunks, "")
		if !strings.Contains(view, want) {
			t.Fatalf("View missing replica %q text %q\nview:\n%s", r.correlationID, want, view)
		}
	}

	// Cross-contamination at the row level: each entry carries only
	// its own replica's chunks, never a sibling's fragment.
	historyView := chat.HistoryEntriesForTest(m)
	byCID := make(map[string]string, len(historyView))
	for _, e := range historyView {
		byCID[e.CorrelationID] = e.Content
	}
	for _, r := range replicas {
		entryContent, ok := byCID[r.correlationID]
		if !ok {
			t.Fatalf("no entry for replica %q", r.correlationID)
		}
		wantOwn := strings.Join(r.chunks, "")
		if !strings.Contains(entryContent, wantOwn) {
			t.Fatalf("replica %q entry missing own text\nwant: %q\ngot: %q",
				r.correlationID, wantOwn, entryContent)
		}
		for _, other := range replicas {
			if other.correlationID == r.correlationID {
				continue
			}
			for _, frag := range other.chunks {
				trimmed := strings.TrimSpace(frag)
				if trimmed == "" {
					continue
				}
				if strings.Contains(entryContent, trimmed) {
					t.Fatalf("replica %q entry leaked sibling %q fragment %q",
						r.correlationID, other.correlationID, trimmed)
				}
			}
		}
	}
}

// TestEndToEnd_ReplicaCompleteDoesNotEraseSibling pins the
// pipeline-lifecycle-disk-commit analog at the chat layer: when one
// replica's stream completes, any still-streaming sibling's
// accumulated text must not be dropped. The ring buffer's
// Grow-on-streaming guard prevents a live entry from being evicted
// when a new entry is pushed.
func TestEndToEnd_ReplicaCompleteDoesNotEraseSibling(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian-parent")

	m := chat.New(theme.DefaultDark(), 2) // tiny capacity — forces Grow
	m.SetSize(160, 40)
	delivery := newStreamDelivery("session-e2e", m)

	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		return delivery.handle(t, msg)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	expected := map[string]int{}
	publishAndWait := func(cid string, pub func() error) {
		t.Helper()
		if err := pub(); err != nil {
			t.Fatalf("publish %q: %v", cid, err)
		}
		expected[cid]++
		delivery.waitForEventCount(t, cid, expected[cid], 2*time.Second)
	}

	ctxA := agentshared.WithStreamContext(context.Background(), "corr-A", "tui")
	ctxA = agentshared.WithStreamContextMetadata(ctxA, map[string]any{
		"runtime_agent_id": "librarian#replica-corr-A",
		"agent_type":       "librarian",
		"agent_name":       "Librarian",
	})
	ctxA, accA := agentshared.WithUsageAccumulator(ctxA)

	ctxB := agentshared.WithStreamContext(context.Background(), "corr-B", "tui")
	ctxB = agentshared.WithStreamContextMetadata(ctxB, map[string]any{
		"runtime_agent_id": "librarian#replica-corr-B",
		"agent_type":       "librarian",
		"agent_name":       "Librarian",
	})
	ctxB, accB := agentshared.WithUsageAccumulator(ctxB)

	// Replica A: start + chunk, but DO NOT complete yet.
	publishAndWait("corr-A", func() error { return agentshared.PublishStreamStart(bus, channels, ctxA, "librarian-parent") })
	publishAndWait("corr-A", func() error {
		return agentshared.PublishStreamChunk(bus, channels, ctxA, "librarian-parent", "A-text-live")
	})

	// Replica B: full lifecycle.
	publishAndWait("corr-B", func() error { return agentshared.PublishStreamStart(bus, channels, ctxB, "librarian-parent") })
	publishAndWait("corr-B", func() error {
		return agentshared.PublishStreamChunk(bus, channels, ctxB, "librarian-parent", "B-text-done")
	})
	publishAndWait("corr-B", func() error {
		return agentshared.PublishStreamComplete(bus, channels, ctxB, "librarian-parent", "", accB.Total())
	})

	// Now complete A so its text syncs.
	publishAndWait("corr-A", func() error {
		return agentshared.PublishStreamComplete(bus, channels, ctxA, "librarian-parent", "", accA.Total())
	})

	view := delivery.snapshotModel().View()
	if !strings.Contains(view, "A-text-live") {
		t.Fatalf("View missing replica A text — sibling complete evicted live entry\nview:\n%s", view)
	}
	if !strings.Contains(view, "B-text-done") {
		t.Fatalf("View missing replica B text\nview:\n%s", view)
	}
}

// TestEndToEnd_ArbitraryReplicaCount_AllRenderCorrectly parameterizes
// the concurrent-replica end-to-end across several N values to prove
// the design is not hard-coded to a specific fan-out. Uses a
// deliberately small initial history capacity to force Grow().
func TestEndToEnd_ArbitraryReplicaCount_AllRenderCorrectly(t *testing.T) {
	for _, n := range []int{2, 5, 11, 24} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
			defer bus.Close()

			channels := guide.NewAgentChannels("librarian", "librarian-parent")

			initialCap := 3
			if initialCap >= n {
				initialCap = 1
			}
			m := chat.New(theme.DefaultDark(), initialCap)
			m.SetSize(200, 40)
			delivery := newStreamDelivery("session-e2e", m)

			sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
				return delivery.handle(t, msg)
			})
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			defer sub.Unsubscribe()

			expected := map[string]int{}
			publishAndWait := func(cid string, pub func() error) {
				t.Helper()
				if err := pub(); err != nil {
					t.Fatalf("publish %q: %v", cid, err)
				}
				expected[cid]++
				delivery.waitForEventCount(t, cid, expected[cid], 3*time.Second)
			}

			ctxs := make([]context.Context, n)
			accs := make([]*agentshared.UsageAccumulator, n)
			chunks := make([][]string, n)
			cids := make([]string, n)
			for i := range n {
				cid := fmt.Sprintf("corr-n%d-%d", n, i)
				cids[i] = cid
				runtimeID := fmt.Sprintf("librarian#replica-%s", cid)
				ctx := agentshared.WithStreamContext(context.Background(), cid, "tui")
				ctx = agentshared.WithStreamContextMetadata(ctx, map[string]any{
					"runtime_agent_id": runtimeID,
					"agent_type":       "librarian",
					"agent_name":       "Librarian",
				})
				ctx, acc := agentshared.WithUsageAccumulator(ctx)
				ctxs[i] = ctx
				accs[i] = acc
				chunks[i] = []string{fmt.Sprintf("rep%d-a ", i), fmt.Sprintf("rep%d-b", i)}
				publishAndWait(cid, func() error {
					return agentshared.PublishStreamStart(bus, channels, ctx, "librarian-parent")
				})
			}
			for chunkIdx := range 2 {
				for i := range n {
					ctx := ctxs[i]
					text := chunks[i][chunkIdx]
					publishAndWait(cids[i], func() error {
						return agentshared.PublishStreamChunk(bus, channels, ctx, "librarian-parent", text)
					})
				}
			}
			for i := range n {
				ctx := ctxs[i]
				acc := accs[i]
				publishAndWait(cids[i], func() error {
					return agentshared.PublishStreamComplete(bus, channels, ctx, "librarian-parent", "", acc.Total())
				})
			}

			// Assert on model state (history), not View(). View() is a
			// 40-row window; for large N the oldest replica rows scroll
			// off the top — that's expected presentation behavior, not
			// a streaming bug. Streaming correctness means every
			// replica's text is accumulated on its own entry in the
			// history; the viewport then renders whatever window the
			// user has scrolled to.
			m = delivery.snapshotModel()
			entries := chat.HistoryEntriesForTest(m)
			byCID := make(map[string]string, len(entries))
			for _, e := range entries {
				byCID[e.CorrelationID] = e.Content
			}
			for i := range n {
				want := strings.Join(chunks[i], "")
				got, ok := byCID[cids[i]]
				if !ok {
					t.Fatalf("n=%d: no history entry for replica %d (cid=%s)", n, i, cids[i])
				}
				if !strings.Contains(got, want) {
					t.Fatalf("n=%d: replica %d entry Content missing text\nwant: %q\ngot: %q", n, i, want, got)
				}
			}

			// Spot check: within the rendered viewport window, at least
			// the most recent few replicas are visible end-to-end.
			view := m.View()
			for i := n - min(3, n); i < n; i++ {
				want := strings.Join(chunks[i], "")
				if !strings.Contains(view, want) {
					t.Fatalf("n=%d: View window missing recent replica %d text %q", n, i, want)
				}
			}
		})
	}
}
