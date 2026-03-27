package agent

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

func TestSummarizeAgentPanelActivity_PrefersSummaryOverJSONContent(t *testing.T) {
	ev := &events.ActivityEvent{
		ID:        "evt_summary",
		EventType: events.EventTypeAgentAction,
		Timestamp: time.Now(),
		AgentID:   "inspector",
		Content:   `{"response":"","intent":"check"}`,
		Summary:   "Inspection task completed",
	}

	if got := summarizeAgentPanelActivity(ev); got != "Inspection task completed" {
		t.Fatalf("summarizeAgentPanelActivity() = %q, want %q", got, "Inspection task completed")
	}
}

func TestSummarizeAgentPanelPayload_CompactsStructuredJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "literal null payload",
			raw:  `null`,
			want: "",
		},
		{
			name: "quoted null payload",
			raw:  `"null"`,
			want: "",
		},
		{
			name: "handoff dispatched",
			raw:  `{"handoff_dispatched":true}`,
			want: "Handoff dispatched",
		},
		{
			name: "no results found",
			raw:  `{"count":0,"entries":null,"found":false}`,
			want: "No results found",
		},
		{
			name: "criteria payload",
			raw:  `{"Criteria":{"task_id":"task_1","success_criteria":[{"id":"SC-01"}]}}`,
			want: "Loaded validation criteria",
		},
		{
			name: "response payload",
			raw:  `{"response":"Need stronger integration coverage.","intent":"check"}`,
			want: "Need stronger integration coverage.",
		},
		{
			name: "nested response payload",
			raw:  `"{\"response\":\"Need stronger integration coverage.\",\"intent\":\"check\"}"`,
			want: "Need stronger integration coverage.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeAgentPanelPayload(tc.raw); got != tc.want {
				t.Fatalf("summarizeAgentPanelPayload() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarizeAgentPanelPayload_DropsOpaqueStructuredJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "empty conversation payload",
			raw:  `{"content":"","type":"conversation"}`,
		},
		{
			name: "opaque nested payload",
			raw:  `{"id":"989f3fb0-bcf7-4876-bd45-0b2f377748a1","request_id":"981c803d-0991-49ca-8a65-d04d99acb6ac","success":true,"result":{"task_id":"8538d"}}`,
		},
		{
			name: "incomplete json payload",
			raw:  `{"Criteria":{"task_id":"task_1"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeAgentPanelPayload(tc.raw); got != "" {
				t.Fatalf("summarizeAgentPanelPayload() = %q, want empty summary", got)
			}
		})
	}
}

func TestSummarizeStructuredAgentPanelValue_StringJSONCompactsOrDrops(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "query string payload compacts",
			value: `{"query":"search existing auth middleware patterns","limit":5}`,
			want:  "search existing auth middleware patterns",
		},
		{
			name:  "opaque string payload drops",
			value: `{"id":"req_123","success":true,"result":{"task_id":"task-1"}}`,
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeStructuredAgentPanelValue(tc.value); got != tc.want {
				t.Fatalf("summarizeStructuredAgentPanelValue() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarizeAgentPanelPayload_UsesTaskSummaryAndUserMessage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "architect task summary",
			raw:  `{"layer_count":0,"next_action":"route_plan_acceptance","task_summary":"Draft the CLI task graph"}`,
			want: "Draft the CLI task graph",
		},
		{
			name: "handoff user message",
			raw:  `{"status":"acceptance_evaluation_pending","user_message":"I'm reviewing your response against the current plan now. I'll update you shortly."}`,
			want: "I'm reviewing your response against the current plan now. I'll update you shortly.",
		},
		{
			name: "tester output file",
			raw:  `{"output_file":"tests/test_cli.py","next_basis":"lease-2"}`,
			want: "tests/test_cli.py",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeAgentPanelPayload(tc.raw); got != tc.want {
				t.Fatalf("summarizeAgentPanelPayload() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarizeAgentPanelActivity_SummaryJSONUsesCompactSummary(t *testing.T) {
	ev := &events.ActivityEvent{
		ID:        "evt_summary_json",
		EventType: events.EventTypeToolResult,
		Timestamp: time.Now(),
		AgentID:   "architect",
		Summary:   `{"layer_count":0,"next_action":"route_plan_acceptance","task_summary":"Draft the CLI task graph"}`,
	}

	if got := summarizeAgentPanelActivity(ev); got != "Draft the CLI task graph" {
		t.Fatalf("summarizeAgentPanelActivity() = %q, want %q", got, "Draft the CLI task graph")
	}
}

func TestSummarizeAgentPanelToolEvent_UsesInterAgentSummary(t *testing.T) {
	ev := msg.ToolCallEventMsg{
		Phase: 0,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "consult",
			AgentTypes: []string{"academic"},
			Summary:    "cleaner or more robust approach?",
		},
	}

	if got := summarizeAgentPanelToolEvent(ev); got != "Consulting academic: cleaner or more robust approach?" {
		t.Fatalf("summarizeAgentPanelToolEvent() = %q, want %q", got, "Consulting academic: cleaner or more robust approach?")
	}
}

func TestSummarizeAgentPanelToolEvent_UsesStructuredArgsQuery(t *testing.T) {
	ev := msg.ToolCallEventMsg{
		Phase:    0,
		ToolName: "search_codebase",
		FullArgs: `{"query":"existing test harness patterns","limit":5}`,
	}

	if got := summarizeAgentPanelToolEvent(ev); got != "existing test harness patterns" {
		t.Fatalf("summarizeAgentPanelToolEvent() = %q, want %q", got, "existing test harness patterns")
	}
}

func TestSummarizeAgentPanelActivity_IgnoresLLMBookkeeping(t *testing.T) {
	cases := []struct {
		name string
		ev   *events.ActivityEvent
	}{
		{
			name: "active content",
			ev: &events.ActivityEvent{
				ID:        "evt_active",
				EventType: events.EventTypeLLMRequest,
				Timestamp: time.Now(),
				AgentID:   "tester",
				Content:   "active",
			},
		},
		{
			name: "transport response in data",
			ev: &events.ActivityEvent{
				ID:        "evt_data_response",
				EventType: events.EventTypeLLMResponse,
				Timestamp: time.Now(),
				AgentID:   "librarian",
				Data: map[string]any{
					"response": "LLM response from claude-sonnet-4-6: 10 input, 579 output tokens in 11.024608797s",
				},
			},
		},
		{
			name: "transport request in summary",
			ev: &events.ActivityEvent{
				ID:        "evt_summary_request",
				EventType: events.EventTypeLLMRequest,
				Timestamp: time.Now(),
				AgentID:   "engineer",
				Summary:   "LLM request to gpt-5.4 with 0 tokens",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeAgentPanelActivity(tc.ev); got != "" {
				t.Fatalf("summarizeAgentPanelActivity() = %q, want empty summary", got)
			}
		})
	}
}
