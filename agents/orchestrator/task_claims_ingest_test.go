package orchestrator

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/core/claims"
)

func TestCollectTaskClaimsFromHandoff_SkipsEmptyTasks(t *testing.T) {
	h := &architect.PlanHandoff{
		Tasks: []*architect.HandoffTask{
			{ID: "task-a", Claims: []architect.TaskClaim{{Title: "claim-1"}}},
			{ID: "task-b", Claims: nil}, // no claims → skipped
			{ID: "", Claims: []architect.TaskClaim{{Title: "claim-x"}}}, // no ID → skipped
			nil, // nil entry → skipped
		},
	}
	got := collectTaskClaimsFromHandoff(h)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	if len(got["task-a"]) != 1 {
		t.Fatalf("task-a should have 1 claim, got %+v", got["task-a"])
	}
}

func TestConvertTaskClaimsToBoardClaims_PreservesScopeAndValidations(t *testing.T) {
	src := []architect.TaskClaim{
		{
			Title:       "Implement HS256 deserialization",
			Description: "Atomic claim",
			Subject:     "engineer",
			Scope: []architect.TaskClaimScope{
				{Kind: "file", Key: "auth/jwt.go"},
				{Kind: "symbol", Key: "DeserializeJWK"},
			},
			Validations: []architect.TaskClaimValidation{
				{Description: "Round-trip test", QualityBar: "100% deserialization fidelity", Type: "test"},
				{Description: "Inspector review", QualityBar: "no panics on malformed input", Type: "inspection"},
			},
		},
	}
	out := convertTaskClaimsToBoardClaims(src, "task-a", "plan-1")
	if len(out) != 1 {
		t.Fatalf("want 1 claim, got %d", len(out))
	}
	c := out[0]
	if c.Title != "Implement HS256 deserialization" {
		t.Fatalf("title not preserved: %q", c.Title)
	}
	// Scope must include task + plan + the architect's entries.
	gotScopes := map[string]string{}
	for _, s := range c.Scope {
		gotScopes[s.Kind] = s.Key
	}
	if gotScopes["task"] != "task-a" {
		t.Fatalf("task scope missing: %+v", gotScopes)
	}
	if gotScopes["plan"] != "plan-1" {
		t.Fatalf("plan scope missing: %+v", gotScopes)
	}
	if gotScopes["file"] != "auth/jwt.go" {
		t.Fatalf("architect file scope dropped: %+v", gotScopes)
	}
	if gotScopes["symbol"] != "DeserializeJWK" {
		t.Fatalf("architect symbol scope dropped: %+v", gotScopes)
	}
	// Subject relation must be preserved.
	subjectFound := false
	for _, r := range c.Relations {
		if r.Relationship == claims.RelationshipSubject && r.Related == "engineer" {
			subjectFound = true
		}
	}
	if !subjectFound {
		t.Fatalf("subject=engineer relation missing: %+v", c.Relations)
	}
	// Validations: count + status.
	if len(c.Validations) != 2 {
		t.Fatalf("want 2 validations, got %d", len(c.Validations))
	}
	for _, v := range c.Validations {
		if v.Status != claims.ValidationStatusPending {
			t.Fatalf("validation status: want Pending, got %v", v.Status)
		}
		if !v.Required {
			t.Fatalf("validation must be Required")
		}
	}
}

func TestPostTaskClaimsForPod_IdempotentOnSecondCall(t *testing.T) {
	b := fakeBridgeForEnsure()
	meta := &ActiveDAGMeta{
		PlanID:    "plan-1",
		SessionID: "sess-1",
		taskClaims: map[string][]architect.TaskClaim{
			"task-a": {{Title: "claim-1", Validations: []architect.TaskClaimValidation{{Description: "v"}}}},
		},
		claimsPostedForTask: make(map[string]struct{}),
	}
	// No claims board registered → postTaskClaimsForPod should
	// silently no-op without panicking AND without permanently
	// marking the task as posted (so a later attempt can retry
	// once a board exists).
	if err := b.postTaskClaimsForPod(context.Background(), meta, "task-a"); err != nil {
		t.Fatalf("post 1: unexpected error: %v", err)
	}
	if _, marked := meta.claimsPostedForTask["task-a"]; marked {
		t.Fatal("post should NOT mark as posted when no board exists (allows retry once wired)")
	}
}

func TestPostTaskClaimsForPod_TasksWithoutClaimsAreMarkedPostedSkippingFutureWork(t *testing.T) {
	b := fakeBridgeForEnsure()
	meta := &ActiveDAGMeta{
		PlanID:              "plan-1",
		SessionID:           "sess-1",
		taskClaims:          map[string][]architect.TaskClaim{}, // no entries for task-a
		claimsPostedForTask: make(map[string]struct{}),
	}
	if err := b.postTaskClaimsForPod(context.Background(), meta, "task-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, marked := meta.claimsPostedForTask["task-a"]; !marked {
		t.Fatal("task with no claims should be marked posted to short-circuit future calls")
	}
}

func TestRegisterTaskClaims_RoundtripsThroughTakePending(t *testing.T) {
	b := &DAGBridge{}
	src := map[string][]architect.TaskClaim{
		"task-a": {{Title: "claim-a"}},
		"task-b": {{Title: "claim-b1"}, {Title: "claim-b2"}},
	}
	b.RegisterTaskClaims("dag-1", src)
	got := b.takePendingTaskClaims("dag-1")
	if len(got) != 2 {
		t.Fatalf("want 2 task entries, got %d", len(got))
	}
	if len(got["task-a"]) != 1 || len(got["task-b"]) != 2 {
		t.Fatalf("claim counts wrong: %+v", got)
	}
	// Take is destructive — second call returns nil.
	if again := b.takePendingTaskClaims("dag-1"); again != nil {
		t.Fatalf("second take should return nil; got %+v", again)
	}
}

func TestRegisterTaskClaims_NilSafe(t *testing.T) {
	var b *DAGBridge
	// Must not panic.
	b.RegisterTaskClaims("dag-1", map[string][]architect.TaskClaim{
		"task-a": {{Title: "claim"}},
	})
	if got := b.takePendingTaskClaims("dag-1"); got != nil {
		t.Fatalf("nil bridge: want nil; got %+v", got)
	}
}
