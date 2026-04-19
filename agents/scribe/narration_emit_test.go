package scribe

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity"
)

func sharedScribeFeedHelper(corr string) shared.ScribeFeed {
	return shared.ScribeFeed{ParentCorrelationID: corr}
}

func TestEmitNarrationActivity_PublishesActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	s := &Scribe{
		id:                "scribe-tester-pipeline-abc123",
		parentAgentType:   "tester-pipeline",
		sessionID:         "sess-1",
		replicaGeneration: 3,
	}

	commentary := `{"summary":"detected pytest","decisions":"chose pytest"}`
	archivalEntryID := "scribe_tester-pipeline_rep3_1234567890"
	s.emitNarrationActivity(context.Background(), commentary, archivalEntryID, "corr-7")

	emitted := col.FilterByKind(activity.ActionNarrationEmitted)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 narration activity; got %d", len(emitted))
	}
	a := emitted[0]
	if a.Resolution != activity.ResolutionCoarse {
		t.Errorf("Resolution = %q, want coarse", a.Resolution)
	}
	if a.Subject.Domain != "tester-pipeline" {
		t.Errorf("Subject.Domain = %q, want tester-pipeline", a.Subject.Domain)
	}
	if a.Actor.AgentType != "scribe-tester-pipeline" {
		t.Errorf("Actor.AgentType = %q, want scribe-tester-pipeline", a.Actor.AgentType)
	}
	if a.SourceTable != "archivalist_entries" {
		t.Errorf("SourceTable = %q, want archivalist_entries", a.SourceTable)
	}
	if a.SourceID != archivalEntryID {
		t.Errorf("SourceID = %q, want %q", a.SourceID, archivalEntryID)
	}
	if a.Subject.Coordinates["replica_generation"] != "3" {
		t.Errorf("replica_generation coord = %q, want 3", a.Subject.Coordinates["replica_generation"])
	}
	if a.Subject.Coordinates["parent_correlation_id"] != "corr-7" {
		t.Errorf("parent_correlation_id coord = %q, want corr-7", a.Subject.Coordinates["parent_correlation_id"])
	}
	if !strings.Contains(string(a.Payload), "pytest") {
		t.Errorf("payload should embed commentary; got %s", string(a.Payload))
	}
}

func TestEmitNarrationActivity_SkipsBlankCommentary(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	s := &Scribe{
		id:              "scribe-x",
		parentAgentType: "x",
	}
	s.emitNarrationActivity(context.Background(), "  ", "entry-id", "corr")
	if got := col.Count(); got != 0 {
		t.Fatalf("blank commentary must not emit; got %d", got)
	}
}

// TestEmitNarrationActivity_WrapsNonJsonCommentary documents the
// non-happy-path: even if the scribe LLM returned malformed JSON
// (unlikely but possible), the fabric activity row stays well-formed
// by wrapping the raw text in a {"raw": "..."} envelope.
func TestEmitNarrationActivity_WrapsNonJsonCommentary(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	s := &Scribe{
		id:              "scribe-y",
		parentAgentType: "engineer",
	}
	s.emitNarrationActivity(context.Background(), "not valid json", "e-id", "")
	emitted := col.FilterByKind(activity.ActionNarrationEmitted)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emission; got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0].Payload), `"raw"`) {
		t.Errorf("non-JSON commentary should be wrapped; got %s", string(emitted[0].Payload))
	}
}

func TestEmitPrecedentActivity_PublishesActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	s := &Scribe{
		id:                "scribe-tester-pipeline-x",
		parentAgentType:   "tester-pipeline",
		sessionID:         "sess-1",
		replicaGeneration: 5,
	}
	commentary := map[string]any{
		"summary":          "scope-split resolved cleanly",
		"precedent_worthy": true,
		"precedent_why":    "two pipelines reconciled in 3 messages — template for framework disputes",
	}
	s.emitPrecedentActivity(context.Background(), commentary, sharedScribeFeedHelper("corr-99"))

	emitted := col.FilterByKind(activity.ActionPrecedentEmitted)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 precedent activity; got %d", len(emitted))
	}
	a := emitted[0]
	if !strings.Contains(string(a.Payload), "framework disputes") {
		t.Errorf("payload should embed precedent_why; got %s", string(a.Payload))
	}
	if a.Subject.Coordinates["replica_generation"] != "5" {
		t.Errorf("coords replica_generation = %q, want 5", a.Subject.Coordinates["replica_generation"])
	}
}

func TestPrecedentWorthyFromCommentary(t *testing.T) {
	cases := map[string]struct {
		commentary map[string]any
		want       bool
	}{
		"bool true":   {map[string]any{"precedent_worthy": true}, true},
		"bool false":  {map[string]any{"precedent_worthy": false}, false},
		"string yes":  {map[string]any{"precedent_worthy": "yes"}, true},
		"string True": {map[string]any{"precedent_worthy": "True"}, true},
		"why only":    {map[string]any{"precedent_why": "good reason"}, true},
		"empty":       {map[string]any{}, false},
		"nil map":     {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := precedentWorthyFromCommentary(tc.commentary); got != tc.want {
				t.Errorf("precedentWorthyFromCommentary(%v) = %v, want %v", tc.commentary, got, tc.want)
			}
		})
	}
}

func TestEmitNarrationActivity_EmptyCoordsStillEmits(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	s := &Scribe{
		id:                "scribe-z",
		parentAgentType:   "architect",
		replicaGeneration: 0,
	}
	s.emitNarrationActivity(context.Background(), `{"summary":"x"}`, "id", "")
	emitted := col.FilterByKind(activity.ActionNarrationEmitted)
	if len(emitted) != 1 {
		t.Fatal("emission should succeed even without correlation ID")
	}
	if got := emitted[0].Subject.Coordinates["replica_generation"]; got != "0" {
		t.Errorf("replica_generation coord = %q, want 0", got)
	}
}
