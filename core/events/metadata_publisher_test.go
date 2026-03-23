package events

import "testing"

func TestMetadataCachingPublisher_RewritesPipelineAgentIDs(t *testing.T) {
	collector := NewTestActivityCollector()
	pub := NewMetadataCachingPublisher(collector, map[string]AgentIdentityMetadata{
		"designer": {AgentType: "designer", AgentName: "Designer"},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "designer",
		Data: map[string]any{
			"agent_type":  "designer",
			"agent_name":  "Designer",
			"pipeline_id": "task_auth_checkout",
			"task_id":     "task_auth_checkout",
			"task_slug":   "auth-checkout",
		},
	})

	events := collector.Events()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if got := events[0].AgentID; got != "task_auth_checkout:designer" {
		t.Fatalf("agent id = %q, want task_auth_checkout:designer", got)
	}
	if got := extractMetadataString(events[0].Data, "runtime_agent_id"); got != "designer" {
		t.Fatalf("runtime_agent_id = %q, want designer", got)
	}
}

func TestMetadataCachingPublisher_ReusesCachedPipelineMetadata(t *testing.T) {
	collector := NewTestActivityCollector()
	pub := NewMetadataCachingPublisher(collector, map[string]AgentIdentityMetadata{
		"inspector-pipeline": {AgentType: "inspector-pipeline", AgentName: "Inspector"},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "inspector-pipeline",
		Data: map[string]any{
			"agent_type":  "inspector-pipeline",
			"agent_name":  "Inspector",
			"pipeline_id": "task_payment_retry",
			"task_id":     "task_payment_retry",
			"task_slug":   "payment-retry",
		},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "inspector-pipeline",
		Data: map[string]any{
			"model": "claude-opus-4-6",
		},
	})

	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}

	second := events[1]
	if got := second.AgentID; got != "task_payment_retry:inspector-pipeline" {
		t.Fatalf("agent id = %q, want task_payment_retry:inspector-pipeline", got)
	}
	if got := extractMetadataString(second.Data, "agent_type"); got != "inspector-pipeline" {
		t.Fatalf("agent_type = %q, want inspector-pipeline", got)
	}
	if got := extractMetadataString(second.Data, "pipeline_id"); got != "task_payment_retry" {
		t.Fatalf("pipeline_id = %q, want task_payment_retry", got)
	}
}

func TestMetadataCachingPublisher_ReusesCachedTaskName(t *testing.T) {
	collector := NewTestActivityCollector()
	pub := NewMetadataCachingPublisher(collector, map[string]AgentIdentityMetadata{
		"designer": {AgentType: "designer", AgentName: "Designer"},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "designer",
		Data: map[string]any{
			"agent_type":  "designer",
			"agent_name":  "Designer",
			"pipeline_id": "task_auth_checkout",
			"task_id":     "task_auth_checkout",
			"task_name":   "Auth checkout",
			"task_slug":   "auth-checkout",
		},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "designer",
		Data: map[string]any{
			"model": "claude-opus-4-6",
		},
	})

	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}

	if got := extractMetadataString(events[1].Data, "task_name"); got != "Auth checkout" {
		t.Fatalf("task_name = %q, want Auth checkout", got)
	}
}

func TestMetadataCachingPublisher_AppliesKnownAgentDefaults(t *testing.T) {
	collector := NewTestActivityCollector()
	pub := NewMetadataCachingPublisher(collector, map[string]AgentIdentityMetadata{
		"architect": {AgentType: "architect", AgentName: "Architect"},
	})

	pub.PublishActivity(&ActivityEvent{AgentID: "architect"})

	events := collector.Events()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if got := extractMetadataString(events[0].Data, "agent_type"); got != "architect" {
		t.Fatalf("agent_type = %q, want architect", got)
	}
	if got := extractMetadataString(events[0].Data, "agent_name"); got != "Architect" {
		t.Fatalf("agent_name = %q, want Architect", got)
	}
}

func TestMetadataCachingPublisher_PrefersTaskIDForPipelineIdentity(t *testing.T) {
	collector := NewTestActivityCollector()
	pub := NewMetadataCachingPublisher(collector, map[string]AgentIdentityMetadata{
		"designer": {AgentType: "designer", AgentName: "Designer"},
	})

	pub.PublishActivity(&ActivityEvent{
		AgentID: "designer",
		Data: map[string]any{
			"agent_type":  "designer",
			"agent_name":  "Designer",
			"pipeline_id": "runtime-pipeline-123",
			"task_id":     "task_auth_checkout",
			"task_slug":   "auth-checkout",
		},
	})

	events := collector.Events()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if got := events[0].AgentID; got != "task_auth_checkout:designer" {
		t.Fatalf("agent id = %q, want task_auth_checkout:designer", got)
	}
	if got := extractMetadataString(events[0].Data, "pipeline_id"); got != "task_auth_checkout" {
		t.Fatalf("pipeline_id = %q, want task_auth_checkout", got)
	}
}
