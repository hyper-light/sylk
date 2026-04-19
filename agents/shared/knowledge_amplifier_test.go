package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

func TestEmitAdvisory_PublishesActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	id := EmitAdvisory(context.Background(), AdvisoryInput{
		SessionID:           "sess-1",
		AdvisorAgentID:      "librarian-1",
		AdvisorAgentType:    "librarian",
		RequestingAgentID:   "tester-2",
		RequestingAgentType: "tester-pipeline",
		Scope:               "tests/billing/",
		Domain:              "test_framework",
		OriginalQuery:       "what fixture pattern fits a shared model?",
		Advice:              "use pytest-fixtures pattern A; precedent supports it",
	})
	if id == "" {
		t.Fatal("expected activity ID")
	}
	emitted := col.FilterByKind(activity.ActionAdvisoryEmitted)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 advisory; got %d", len(emitted))
	}
	if emitted[0].Subject.TargetAgent != "tester-2" {
		t.Errorf("Subject.TargetAgent = %q, want tester-2", emitted[0].Subject.TargetAgent)
	}
	if !strings.Contains(string(emitted[0].Payload), "pytest-fixtures") {
		t.Errorf("payload should embed advice")
	}
}

func TestEmitAdvisory_EmptyAdviceIsNoOp(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)
	if id := EmitAdvisory(context.Background(), AdvisoryInput{Advice: "  "}); id != "" {
		t.Fatalf("expected empty ID for blank advice; got %q", id)
	}
	if col.Count() != 0 {
		t.Fatalf("blank advice must not emit; got %d", col.Count())
	}
}

func TestEmitProactiveAdvisory_TargetsSpecificPeer(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	EmitProactiveAdvisory(context.Background(), ProactiveAdvisoryInput{
		SessionID:        "sess-1",
		AdvisorAgentID:   "academic-1",
		AdvisorAgentType: "academic",
		TargetAgentID:    "engineer-3",
		TargetAgentType:  "engineer",
		Scope:            "internal/",
		Trigger:          "pattern X anti-pattern detected in scope",
		Advice:           "consider approach Y instead",
	})
	emitted := col.FilterByKind(activity.ActionProactiveAdvisory)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 proactive advisory; got %d", len(emitted))
	}
	if emitted[0].Subject.TargetAgent != "engineer-3" {
		t.Errorf("Subject.TargetAgent = %q, want engineer-3", emitted[0].Subject.TargetAgent)
	}
	if emitted[0].Resolution != activity.ResolutionCoarse {
		t.Errorf("proactive advisories should be Coarse resolution; got %s", emitted[0].Resolution)
	}
}

func TestEmitNotification_RecordsCategory(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	EmitNotification(context.Background(), NotificationInput{
		SessionID:       "sess-1",
		SenderAgentID:   "inspector-1",
		SenderAgentType: "inspector-pipeline",
		TargetAgentID:   "tester-2",
		Category:        "pile-on warning",
		Body:            "scope tests/auth/ has 5 inbound challenges; consider adopting an existing thread",
	})
	emitted := col.FilterByKind(activity.ActionNotificationEmitted)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 notification; got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0].Payload), "pile-on warning") {
		t.Errorf("payload should embed category")
	}
}
