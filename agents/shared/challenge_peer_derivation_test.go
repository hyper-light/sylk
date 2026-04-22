package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

// TestChallengePeerSkill_EmitsDerivableCompletionEvent is the regression
// guard for the UI-attribution bug where challenge_peer's tool-call
// completion never produced a child agent row in the chat panel. The
// handler has to resolve the challenged activity's author and stamp
// target_agent_type into its result map so interAgentChallengeTargets
// can attach the branch to the right agent — otherwise the derivation
// sees only an opaque target_activity_id UUID, returns empty targets,
// and the UI event is silently dropped.
//
// If this assertion fails, challenge_peer will go back to firing into
// the void as far as the chat UI is concerned.
func TestChallengePeerSkill_EmitsDerivableCompletionEvent(t *testing.T) {
	source := &fakeActivitySource{
		activities: map[activity.ActivityID]activity.AgentActivity{
			"target-activity-1": {
				ID: "target-activity-1",
				Actor: activity.Actor{
					// Designer is a legitimate challenge target for the
					// engineer per authority.PermittedChallengeTargets.
					// See docs/COMMS_MATRIX.md.
					AgentID:    "designer-1",
					AgentType:  "designer",
					PipelineID: "pipeline-a",
				},
			},
		},
	}
	prev := activity.SetDefaultSource(source)
	t.Cleanup(func() { activity.SetDefaultSource(prev) })

	skills := CrossPipelineSkills(CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "engineer-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipeline-b" },
	})

	var challenge *struct {
		Name    string
		Handler func(ctx context.Context, input json.RawMessage) (any, error)
	}
	for _, s := range skills {
		if s.Name == "challenge_peer" {
			challenge = &struct {
				Name    string
				Handler func(ctx context.Context, input json.RawMessage) (any, error)
			}{Name: s.Name, Handler: s.Handler}
			break
		}
	}
	if challenge == nil {
		t.Fatal("challenge_peer skill not found")
	}

	input := json.RawMessage(`{
		"target_activity_id": "target-activity-1",
		"evidence": "Designer's style decision conflicts with a published convention in docs/STYLE.md",
		"alternative": "Adopt convention from docs/STYLE.md instead",
		"deadline_seconds": 300
	}`)
	raw, err := challenge.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("challenge_peer handler returned error: %v", err)
	}
	result, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T, want map[string]any", raw)
	}

	// Result must carry target_agent_type so UI derivation can attach
	// the branch. Without this key, the chat panel gets no child row.
	targetAgent, _ := result["target_agent_type"].(string)
	if targetAgent != "designer" {
		t.Fatalf("result target_agent_type = %q, want %q", targetAgent, "designer")
	}
	targetPipeline, _ := result["target_pipeline_id"].(string)
	if targetPipeline != "pipeline-a" {
		t.Fatalf("result target_pipeline_id = %q, want %q", targetPipeline, "pipeline-a")
	}

	// End-to-end: the completion derivation must now produce a
	// publishable event with the correct AgentTypes.
	outputJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	event := DeriveInterAgentToolEvent(
		"challenge_peer",
		string(input),
		string(outputJSON),
		ToolCallComplete,
		true,
		"",
	)
	if event == nil {
		t.Fatal("DeriveInterAgentToolEvent returned nil — UI will render no child row")
	}
	if event.Kind != InterAgentToolEventKindChallenge {
		t.Fatalf("event kind = %q, want %q", event.Kind, InterAgentToolEventKindChallenge)
	}
	if len(event.AgentTypes) != 1 || event.AgentTypes[0] != "designer" {
		t.Fatalf("event AgentTypes = %v, want [designer]", event.AgentTypes)
	}
}

// TestChallengePeerSkill_GracefulWhenActivitySourceMissing is the
// negative path: if the activity source is unavailable (test wiring
// missing, or the target activity has been pruned), the handler still
// succeeds and returns a valid challenge_id. The UI branch won't
// attach — but that's a downgrade, not a hard failure.
func TestChallengePeerSkill_GracefulWhenActivitySourceMissing(t *testing.T) {
	prev := activity.SetDefaultSource(nil)
	t.Cleanup(func() { activity.SetDefaultSource(prev) })

	skills := CrossPipelineSkills(CrossPipelineSkillConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "engineer-1" },
		AgentType: func() string { return "engineer" },
	})
	var handler func(ctx context.Context, input json.RawMessage) (any, error)
	for _, s := range skills {
		if s.Name == "challenge_peer" {
			handler = s.Handler
			break
		}
	}
	if handler == nil {
		t.Fatal("challenge_peer not found")
	}
	raw, err := handler(context.Background(), json.RawMessage(`{
		"target_activity_id": "some-id",
		"evidence": "x"
	}`))
	if err != nil {
		t.Fatalf("handler should succeed when source is nil, got %v", err)
	}
	result := raw.(map[string]any)
	if _, ok := result["challenge_id"]; !ok {
		t.Fatal("result missing challenge_id on graceful path")
	}
	// target_agent_type must be absent when resolution failed (don't
	// fabricate one).
	if _, ok := result["target_agent_type"]; ok {
		t.Fatal("target_agent_type should be absent when source returns no activity")
	}
}

// fakeActivitySource implements activity.Source for the challenge_peer
// derivation test. FilterActivities and LatestActivityID are unused
// here; minimal stubs keep the interface satisfied.
type fakeActivitySource struct {
	activities map[activity.ActivityID]activity.AgentActivity
}

func (f *fakeActivitySource) FilterActivities(context.Context, activity.QueryFilter) ([]activity.AgentActivity, error) {
	return nil, nil
}

func (f *fakeActivitySource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	if a, ok := f.activities[id]; ok {
		return &a, nil
	}
	return nil, nil
}

func (f *fakeActivitySource) LatestActivityID(context.Context, activity.QueryFilter) (activity.ActivityID, error) {
	return "", nil
}

// Sanity on build: make sure the package-local helper we're depending
// on still normalizes "librarian" as we expect.
func TestChallengePeerDerivation_NormalizesKnownAgentType(t *testing.T) {
	t.Parallel()
	got := normalizeAgentTypeList([]string{"librarian"})
	if len(got) != 1 || !strings.EqualFold(got[0], "librarian") {
		t.Fatalf("normalizeAgentTypeList([librarian]) = %v, want [librarian]", got)
	}
}
