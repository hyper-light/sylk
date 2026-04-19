package planview

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func testPalette() *theme.Palette {
	p := theme.DarkPalette
	return &p
}

func TestNew(t *testing.T) {
	m := New(testPalette())
	if m.Visible() {
		t.Error("should not be visible initially")
	}
	if m.HasPlan() {
		t.Error("should not have a plan initially")
	}
	if m.Focused() {
		t.Error("should not be focused initially")
	}
}

func TestApplyUpdate(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)

	update := msg.PlanUpdateMsg{
		PlanID: "plan-1",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "Setup", AgentType: "engineer", Status: "completed"},
			{ID: "t2", Name: "Implement", AgentType: "architect", Status: "pending", Dependencies: []string{"t1"}},
			{ID: "t3", Name: "Test", AgentType: "tester", Status: "pending", Dependencies: []string{"t2"}},
		},
		ExecutionLayers: [][]string{
			{"t1"},
			{"t2"},
			{"t3"},
		},
		StartTime: time.Now(),
	}

	comp, _ := m.Update(update)
	m = comp.(*Model)

	if !m.Visible() {
		t.Error("should be visible after update")
	}
	if !m.HasPlan() {
		t.Error("should have plan after update")
	}
	if m.PlanStatus() != "ready" {
		t.Errorf("expected status 'ready', got %q", m.PlanStatus())
	}
	// 3 layers + 3 tasks = 6 entries.
	if len(m.entries) != 6 {
		t.Errorf("expected 6 entries, got %d", len(m.entries))
	}
}

func TestCursorNavigation(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)
	m.SetFocused(true)

	update := msg.PlanUpdateMsg{
		PlanID: "p",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "A", Status: "pending"},
			{ID: "t2", Name: "B", Status: "pending"},
		},
		ExecutionLayers: [][]string{{"t1", "t2"}},
	}
	m.Update(update)

	// Layer 0 + t1 + t2 = 3 entries, cursor starts at 0.
	if m.cursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.cursor)
	}

	m.moveCursor(1)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.cursor)
	}

	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("expected cursor 2, got %d", m.cursor)
	}

	// Clamp at end.
	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("expected cursor 2 (clamped), got %d", m.cursor)
	}
}

func TestLayerCollapse(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)

	update := msg.PlanUpdateMsg{
		PlanID: "p",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "A", Status: "pending"},
			{ID: "t2", Name: "B", Status: "pending"},
		},
		ExecutionLayers: [][]string{{"t1", "t2"}},
	}
	m.Update(update)

	// 1 layer + 2 tasks = 3 entries.
	if len(m.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(m.entries))
	}

	// Collapse layer (cursor at 0 = layer header).
	m.cursor = 0
	m.toggleExpand()

	// After collapse: only the layer header remains.
	if len(m.entries) != 1 {
		t.Errorf("expected 1 entry after collapse, got %d", len(m.entries))
	}

	// Re-expand. UI-19 regression: rebuildVisible must re-derive the task
	// entries from the last update, not just filter a stale m.entries list.
	m.cursor = 0
	m.toggleExpand()
	if len(m.entries) != 3 {
		t.Errorf("expected 3 entries after re-expand (layer + 2 tasks), got %d", len(m.entries))
	}

	// Subsequent updates must continue to work.
	m.Update(update)
	if len(m.entries) != 3 {
		t.Errorf("expected 3 entries after re-expand + update, got %d", len(m.entries))
	}
}

// TestLayerCollapseRestoresTasksAfterReExpand is the explicit regression
// guard for UI-19: toggleExpand → toggleExpand must return the original
// entries without needing a fresh PlanUpdateMsg.
func TestLayerCollapseRestoresTasksAfterReExpand(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)
	m.Update(msg.PlanUpdateMsg{
		PlanID: "p",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "A", Status: "pending"},
			{ID: "t2", Name: "B", Status: "pending"},
		},
		ExecutionLayers: [][]string{{"t1", "t2"}},
	})

	if len(m.entries) != 3 {
		t.Fatalf("initial: expected 3 entries, got %d", len(m.entries))
	}

	// Collapse then re-expand — no Update in between.
	m.cursor = 0
	m.toggleExpand()
	if len(m.entries) != 1 {
		t.Fatalf("collapsed: expected 1 entry, got %d", len(m.entries))
	}
	m.cursor = 0
	m.toggleExpand()
	if len(m.entries) != 3 {
		t.Fatalf("re-expanded: expected 3 entries without external update, got %d", len(m.entries))
	}

	// Task identities must be preserved.
	gotIDs := map[string]bool{}
	for _, e := range m.entries {
		if e.Kind == entryTask {
			gotIDs[e.TaskID] = true
		}
	}
	for _, id := range []string{"t1", "t2"} {
		if !gotIDs[id] {
			t.Errorf("expected task %s to be restored, got entries: %+v", id, m.entries)
		}
	}
}

func TestViewDirty(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)

	// First call after SetSize should return true.
	if !m.ViewDirty() {
		t.Error("expected dirty after SetSize")
	}
	// Second call should return false.
	if m.ViewDirty() {
		t.Error("expected clean after first ViewDirty()")
	}
}

func TestNeedsDecorTick(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)

	// Not visible → no tick needed.
	if m.NeedsDecorTick() {
		t.Error("should not need tick when not visible")
	}

	// Visible with running task → tick needed.
	update := msg.PlanUpdateMsg{
		PlanID: "p",
		Status: "executing",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "Running", Status: "running"},
		},
		ExecutionLayers: [][]string{{"t1"}},
	}
	m.Update(update)

	if !m.NeedsDecorTick() {
		t.Error("should need tick with running task")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "--"},
		{500 * time.Millisecond, "500ms"},
		{2500 * time.Millisecond, "2.5s"},
		{90 * time.Second, "1m30s"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatTokenPair(t *testing.T) {
	cases := []struct {
		in, out int
		want    string
	}{
		{0, 0, "--"},
		{500, 1200, "↑500 ↓1.2k"},
		{1500, 3400, "↑1.5k ↓3.4k"},
	}
	for _, tc := range cases {
		got := formatTokenPair(tc.in, tc.out)
		if got != tc.want {
			t.Errorf("formatTokenPair(%d, %d) = %q, want %q", tc.in, tc.out, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"abc", 0, ""},
	}
	for _, tc := range cases {
		got := truncate(tc.s, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
		}
	}
}

func TestViewRender(t *testing.T) {
	m := New(testPalette())
	m.SetSize(80, 24)

	// Empty view when not visible.
	if got := m.View(false); got != "" {
		t.Errorf("expected empty view when not visible, got %q", got)
	}

	// After update, should render non-empty.
	update := msg.PlanUpdateMsg{
		PlanID: "p",
		Status: "ready",
		Tasks: []msg.PlanTaskSnapshot{
			{ID: "t1", Name: "Setup", Status: "completed", AgentType: "engineer"},
		},
		ExecutionLayers: [][]string{{"t1"}},
	}
	m.Update(update)
	view := m.View(false)
	if view == "" {
		t.Error("expected non-empty view after update")
	}
}
