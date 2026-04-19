package global

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestArchitectSnapshotJSONPath_PrefersLatestVersionedSnapshot(t *testing.T) {
	root := t.TempDir()
	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	planDir := filepath.Join(root, ".sylk", "sessions", "sess-1", "agents", "architect", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"plan-1.json", "plan-1_v1.0.json", "plan-1_v1.2.json"} {
		if err := os.WriteFile(filepath.Join(planDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := architectSnapshotJSONPath("sess-1", "plan-1")
	want := filepath.Join(".sylk", "sessions", "sess-1", "agents", "architect", "plans", "plan-1_v1.2.json")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("architectSnapshotJSONPath = %q, want %q", got, want)
	}
}

func TestLoadPlanContext_UsesLatestVersionedArchitectSnapshot(t *testing.T) {
	root := t.TempDir()
	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	planDir := filepath.Join(root, ".sylk", "sessions", "sess-1", "agents", "architect", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeSnapshot := func(name string, payload map[string]any) {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(planDir, name), data, 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
	}

	writeSnapshot("plan-1_v1.0.json", map[string]any{"revision": 1, "status": "ready"})
	writeSnapshot("plan-1_v1.3.json", map[string]any{"revision": 4, "status": "executing"})

	gi := &GlobalInspector{
		config: shared.GlobalInspectorConfig{Factory: newTestFactory(t), SessionID: "sess-1"},
	}
	content, source, resolvedPath, err := gi.loadPlanContext(context.Background(), "", "plan-1", "", "sess-1")
	if err != nil {
		t.Fatalf("loadPlanContext: %v", err)
	}
	if source != "architect_snapshot_json" {
		t.Fatalf("source = %q, want architect_snapshot_json", source)
	}
	if filepath.Clean(resolvedPath) != filepath.Clean(filepath.Join(".sylk", "sessions", "sess-1", "agents", "architect", "plans", "plan-1_v1.3.json")) {
		t.Fatalf("resolvedPath = %q", resolvedPath)
	}
	if content == "" || !containsAll(content, "\"revision\":4", "\"status\":\"executing\"") {
		t.Fatalf("content = %q, want latest versioned snapshot content", content)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func TestConsultAgent_FailedAcademicConsultMarksInterAgentBranchFailed(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	gi := &GlobalInspector{
		id:         "inspector-id",
		bus:        bus,
		channels:   guide.NewAgentChannels("inspector", "inspector-id"),
		pendingBus: make(map[string]*pendingWait),
		config:     shared.GlobalInspectorConfig{Factory: newTestFactory(t), SessionID: "sess-1"},
	}

	respSub, err := bus.SubscribeAsync(gi.channels.Responses, gi.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe inspector responses: %v", err)
	}
	defer respSub.Unsubscribe()

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		return bus.Publish(gi.channels.Responses, guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           false,
			RespondingAgentID: "academic",
			Error:             "academic consultation failed: provider unavailable",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	var events []agentshared.ToolCallEvent
	parentCorr := fmt.Sprintf("corr-parent-%d", time.Now().UnixNano())
	ctx := agentshared.WithStreamContext(context.Background(), parentCorr, "inspector")
	ctx = agentshared.WithToolCallEmitter(ctx, func(ev agentshared.ToolCallEvent) {
		events = append(events, ev)
	})

	_, err = gi.consultAgent(ctx, "academic", "Assess whether the current approach is sound.", nil)
	if err == nil {
		t.Fatal("expected consultAgent error")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("error = %v, want provider-unavailable detail", err)
	}
	if len(events) != 2 {
		t.Fatalf("emitted events = %d, want 2", len(events))
	}
	complete := events[1]
	if complete.Success {
		t.Fatal("expected failed consult completion")
	}
	if complete.ErrorMsg != "academic consultation failed: provider unavailable" {
		t.Fatalf("completion error = %q", complete.ErrorMsg)
	}
	if complete.InterAgent == nil {
		t.Fatal("expected inter-agent metadata on completion")
	}
	if complete.InterAgent.Status != agentshared.InterAgentToolEventStatusFailed {
		t.Fatalf("status = %q, want %q", complete.InterAgent.Status, agentshared.InterAgentToolEventStatusFailed)
	}
}
